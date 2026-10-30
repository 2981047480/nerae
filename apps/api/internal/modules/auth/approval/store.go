// Package approval implements internal new-device approval transactions.
// It does not exchange approvals for tokens or expose HTTP endpoints.
package approval

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"strconv"
	"strings"
	"time"

	"nerae/api/internal/modules/auth/challenge"
	"nerae/api/internal/modules/auth/domain"
	"nerae/api/internal/modules/auth/identity"
	"nerae/api/internal/modules/auth/loginreceipt"
	"nerae/api/internal/modules/auth/session"
)

var ErrUnavailable = errors.New("approval unavailable")

type Policy struct {
	Lifetime     time.Duration
	ReauthWindow time.Duration
}

type Store struct {
	resolver *identity.Resolver
	key      []byte
	keyID    string
	epoch    [32]byte
	policy   Policy
	now      func() time.Time
}

// NewStore requires an externally generated 256-bit HMAC key and explicit
// bounded policy. It does not load secrets or provide rotation.
func NewStore(resolver *identity.Resolver, key []byte, keyID string, policy Policy) (*Store, error) {
	if resolver == nil || len(key) != 32 || len(keyID) < 1 || len(keyID) > 64 ||
		strings.IndexFunc(keyID, func(r rune) bool { return r < 33 || r > 126 }) >= 0 ||
		policy.Lifetime < time.Second || policy.Lifetime > 30*time.Minute || policy.Lifetime%time.Second != 0 ||
		policy.ReauthWindow < time.Second || policy.ReauthWindow > 24*time.Hour || policy.ReauthWindow%time.Second != 0 {
		return nil, ErrUnavailable
	}
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte("nerae/auth/approval-key-epoch/v1\x00" + keyID))
	var epoch [32]byte
	copy(epoch[:], mac.Sum(nil))
	return &Store{resolver: resolver, key: append([]byte(nil), key...), keyID: keyID, epoch: epoch, policy: policy, now: time.Now}, nil
}

func (Store) String() string               { return "ApprovalStore{redacted}" }
func (Store) GoString() string             { return "ApprovalStore{redacted}" }
func (Store) MarshalJSON() ([]byte, error) { return nil, ErrUnavailable }
func (Store) LogValue() slog.Value         { return slog.StringValue("ApprovalStore{redacted}") }

// InitializeKey is a deployment-only setup transaction. Existing mismatched
// keys fail closed; key rotation requires a separately reviewed migration.
func (s *Store) InitializeKey(ctx context.Context, tx *sql.Tx) error {
	if s == nil || ctx == nil || tx == nil || len(s.key) != 32 {
		return ErrUnavailable
	}
	var id, tag []byte
	if err := tx.QueryRowContext(ctx, `SELECT key_id, key_tag FROM auth_approval_key_epoch WHERE slot = 1 FOR UPDATE`).Scan(&id, &tag); err != nil {
		return ErrUnavailable
	}
	if id == nil && tag == nil {
		if _, err := tx.ExecContext(ctx, `UPDATE auth_approval_key_epoch SET key_id = ?, key_tag = ? WHERE slot = 1`, s.keyID, s.epoch[:]); err != nil {
			return ErrUnavailable
		}
		return nil
	}
	if string(id) != s.keyID || !hmac.Equal(tag, s.epoch[:]) {
		return ErrUnavailable
	}
	return nil
}

func (s *Store) checkEpoch(ctx context.Context, tx *sql.Tx) error {
	var id, tag []byte
	if err := tx.QueryRowContext(ctx, `SELECT key_id, key_tag FROM auth_approval_key_epoch WHERE slot = 1 FOR SHARE`).Scan(&id, &tag); err != nil ||
		string(id) != s.keyID || !hmac.Equal(tag, s.epoch[:]) {
		return ErrUnavailable
	}
	return nil
}

func (s *Store) digest(id, secret string) []byte {
	mac := hmac.New(sha256.New, s.key)
	_, _ = mac.Write([]byte("nerae/auth/approval-secret/v1\x00"))
	var length [4]byte
	binary.BigEndian.PutUint32(length[:], uint32(len(id)))
	_, _ = mac.Write(length[:])
	_, _ = mac.Write([]byte(id))
	_, _ = mac.Write([]byte(secret))
	return mac.Sum(nil)
}

type Data struct {
	ID             string
	Secret         string
	Nonce          string
	ComparisonCode string
	ExpiresAt      time.Time
}

func (Data) String() string               { return "ApprovalData{redacted}" }
func (Data) GoString() string             { return "ApprovalData{redacted}" }
func (Data) MarshalJSON() ([]byte, error) { return nil, ErrUnavailable }
func (Data) LogValue() slog.Value         { return slog.StringValue("ApprovalData{redacted}") }

type Pending struct {
	store           *Store
	sourceTx        *sql.Tx
	data            Data
	userID          uint64
	identityID      string
	challengeID     string
	tokenVersion    uint64
	identityVersion uint64
	fingerprint     [32]byte
	scope           [32]byte
}

func (Pending) String() string               { return "PendingApproval{redacted}" }
func (Pending) GoString() string             { return "PendingApproval{redacted}" }
func (Pending) MarshalJSON() ([]byte, error) { return nil, ErrUnavailable }
func (Pending) LogValue() slog.Value         { return slog.StringValue("PendingApproval{redacted}") }

// BeginEmail binds an approval to one verified challenge and new device.
// Caller must commit the challenge and approval together, or roll back on any
// error. This creates no trusted device, session, or business permission.
func (s *Store) BeginEmail(ctx context.Context, tx *sql.Tx, proof *challenge.VerifiedEmail) (Pending, error) {
	if s == nil || s.resolver == nil || s.now == nil || ctx == nil || tx == nil || proof == nil {
		return Pending{}, ErrUnavailable
	}
	resolved, err := s.resolver.ResolveEmail(ctx, tx, proof)
	if err != nil || resolved.State != identity.ApprovalRequired || resolved.DeviceID != "" {
		return Pending{}, ErrUnavailable
	}
	userID, err := strconv.ParseUint(resolved.SubjectID, 10, 64)
	if err != nil || userID == 0 {
		return Pending{}, ErrUnavailable
	}
	verified, err := proof.ForTransaction(ctx, tx)
	if err != nil {
		return Pending{}, ErrUnavailable
	}
	challengeID, err := proof.ChallengeIDForTransaction(ctx, tx)
	if err != nil {
		return Pending{}, ErrUnavailable
	}
	var identityID []byte
	if err := tx.QueryRowContext(ctx, `SELECT id FROM login_identity WHERE user_id = ? AND kind = 'email' AND provider_scope = 'beta-email'
        AND subject_lookup_hmac = ? AND state = 'active' FOR UPDATE`, userID, verified.Index[:]).Scan(&identityID); err != nil || len(identityID) == 0 {
		return Pending{}, ErrUnavailable
	}
	var status string
	var tokenVersion, identityVersion uint64
	if err := tx.QueryRowContext(ctx, `SELECT status, token_version, identity_version FROM user WHERE id = ? FOR UPDATE`, userID).
		Scan(&status, &tokenVersion, &identityVersion); err != nil || status != "active" || tokenVersion == 0 || identityVersion == 0 {
		return Pending{}, ErrUnavailable
	}
	if s.checkEpoch(ctx, tx) != nil {
		return Pending{}, ErrUnavailable
	}
	id, err := domain.GenerateChallengeID()
	if err != nil {
		return Pending{}, ErrUnavailable
	}
	secret, err := domain.GenerateChallengeNonce()
	if err != nil {
		return Pending{}, ErrUnavailable
	}
	nonce, err := domain.GenerateChallengeNonce()
	if err != nil {
		return Pending{}, ErrUnavailable
	}
	code, err := rand.Int(rand.Reader, big.NewInt(1000000))
	if err != nil {
		return Pending{}, ErrUnavailable
	}
	now := s.now().UTC().Truncate(time.Microsecond)
	if now.IsZero() {
		return Pending{}, ErrUnavailable
	}
	data := Data{ID: id, Secret: secret, Nonce: nonce, ComparisonCode: fmt.Sprintf("%06d", code.Int64()), ExpiresAt: now.Add(s.policy.Lifetime)}
	_, err = tx.ExecContext(ctx, `INSERT INTO auth_login_approval
        (id, user_id, identity_id, challenge_id, token_version, identity_version, target_fingerprint, public_x, public_y,
         secret_digest, key_id, nonce, comparison_code, state, created_at, expires_at)
        VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 'pending', ?, ?)`, []byte(data.ID), userID, identityID, []byte(challengeID),
		tokenVersion, identityVersion, verified.Fingerprint[:], verified.PublicKey.X, verified.PublicKey.Y,
		s.digest(data.ID, data.Secret), s.keyID, []byte(data.Nonce), data.ComparisonCode, timestamp(now), timestamp(data.ExpiresAt))
	if err != nil {
		return Pending{}, ErrUnavailable
	}
	if _, err := proof.ForTransaction(ctx, tx); err != nil {
		return Pending{}, ErrUnavailable
	}
	return Pending{store: s, sourceTx: tx, data: data, userID: userID, identityID: string(identityID), challengeID: challengeID,
		tokenVersion: tokenVersion, identityVersion: identityVersion, fingerprint: verified.Fingerprint, scope: verified.Index}, nil
}

// SealedReceipt holds an encrypted approval response and a non-bearer
// reference. It does not prove commit or authorize replay.
type SealedReceipt struct {
	reference string
	sealed    loginreceipt.Sealed
	expiresAt time.Time
}

func (SealedReceipt) String() string               { return "SealedApprovalReceipt{redacted}" }
func (SealedReceipt) GoString() string             { return "SealedApprovalReceipt{redacted}" }
func (SealedReceipt) MarshalJSON() ([]byte, error) { return nil, ErrUnavailable }
func (SealedReceipt) LogValue() slog.Value {
	return slog.StringValue("SealedApprovalReceipt{redacted}")
}
func (r SealedReceipt) Reference() string               { return r.reference }
func (r SealedReceipt) Ciphertext() loginreceipt.Sealed { return r.sealed }
func (r SealedReceipt) ExpiresAt() time.Time            { return r.expiresAt }

type receiptPayload struct {
	Version        int       `json:"version"`
	Secret         string    `json:"secret"`
	Nonce          string    `json:"nonce"`
	ComparisonCode string    `json:"comparisonCode"`
	ExpiresAt      time.Time `json:"expiresAt"`
}

// SealForReceipt encrypts a newly pending approval only in its originating
// transaction. The caller must bind the exact login request, persist the result
// in the same transaction, and never release it before commit and fresh proof,
// receipt, account and approval authorization checks. The returned expiration
// is capped at the approval's own expiration.
func (p Pending) SealForReceipt(ctx context.Context, tx *sql.Tx, proof *challenge.VerifiedEmail, crypto *loginreceipt.Crypto, binding loginreceipt.Binding, expires time.Time) (SealedReceipt, error) {
	if ctx == nil || tx == nil || p.sourceTx != tx || p.store == nil || proof == nil || crypto == nil || binding.ChallengeID() != p.challengeID {
		return SealedReceipt{}, ErrUnavailable
	}
	verified, err := proof.ForTransaction(ctx, tx)
	if err != nil || verified.Index != p.scope || verified.Fingerprint != p.fingerprint {
		return SealedReceipt{}, ErrUnavailable
	}
	challengeID, err := proof.ChallengeIDForTransaction(ctx, tx)
	if err != nil || challengeID != p.challengeID || p.validatePending(ctx, tx, true) != nil {
		return SealedReceipt{}, ErrUnavailable
	}
	if _, err := proof.ForTransaction(ctx, tx); err != nil {
		return SealedReceipt{}, ErrUnavailable
	}
	now := p.store.now().UTC()
	if expires.After(p.data.ExpiresAt) {
		expires = p.data.ExpiresAt
	}
	if now.IsZero() || !expires.After(now) {
		return SealedReceipt{}, ErrUnavailable
	}
	aad, err := crypto.NewContext(p.scope[:], binding, loginreceipt.ResultApprovalRequired, p.data.ID, expires)
	if err != nil {
		return SealedReceipt{}, ErrUnavailable
	}
	payload, err := json.Marshal(receiptPayload{Version: 1, Secret: p.data.Secret, Nonce: p.data.Nonce,
		ComparisonCode: p.data.ComparisonCode, ExpiresAt: p.data.ExpiresAt})
	if err != nil {
		return SealedReceipt{}, ErrUnavailable
	}
	sealed, err := crypto.Seal(aad, payload)
	if err != nil {
		return SealedReceipt{}, ErrUnavailable
	}
	return SealedReceipt{reference: p.data.ID, sealed: sealed, expiresAt: expires}, nil
}

// AfterCommit releases the polling secret only after a fresh transaction
// confirms that the approval, identity and account are still valid.
func (p Pending) AfterCommit(ctx context.Context, db *sql.DB) (Data, error) {
	if p.store == nil || p.sourceTx == nil || ctx == nil || db == nil || p.userID == 0 || p.data.ID == "" {
		return Data{}, ErrUnavailable
	}
	var live int
	if err := p.sourceTx.QueryRowContext(ctx, `SELECT 1`).Scan(&live); !errors.Is(err, sql.ErrTxDone) {
		return Data{}, ErrUnavailable
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return Data{}, ErrUnavailable
	}
	defer tx.Rollback()
	if err := p.validatePending(ctx, tx, false); err != nil {
		return Data{}, ErrUnavailable
	}
	if err := tx.Commit(); err != nil {
		return Data{}, ErrUnavailable
	}
	return p.data, nil
}

// validatePending locks current identity -> user -> approval state. Callers
// still need to verify the request proof and a matching receipt independently.
func (p Pending) validatePending(ctx context.Context, tx *sql.Tx, requirePending bool) error {
	if p.store == nil || p.store.now == nil || ctx == nil || tx == nil || p.userID == 0 || p.data.ID == "" || p.scope == ([32]byte{}) {
		return ErrUnavailable
	}
	var identityUser uint64
	var identityState, identityKind string
	var providerScope, identityIndex []byte
	if err := tx.QueryRowContext(ctx, `SELECT user_id, state, kind, provider_scope, subject_lookup_hmac FROM login_identity WHERE id = ? FOR UPDATE`, []byte(p.identityID)).
		Scan(&identityUser, &identityState, &identityKind, &providerScope, &identityIndex); err != nil || identityUser != p.userID || identityState != "active" ||
		identityKind != "email" || string(providerScope) != "beta-email" || !hmac.Equal(identityIndex, p.scope[:]) {
		return ErrUnavailable
	}
	var status string
	var tokenVersion, identityVersion uint64
	if err := tx.QueryRowContext(ctx, `SELECT status, token_version, identity_version FROM user WHERE id = ? FOR UPDATE`, p.userID).
		Scan(&status, &tokenVersion, &identityVersion); err != nil || status != "active" || tokenVersion != p.tokenVersion || identityVersion != p.identityVersion {
		return ErrUnavailable
	}
	if p.store.checkEpoch(ctx, tx) != nil {
		return ErrUnavailable
	}
	var userID, rowTokenVersion, rowIdentityVersion uint64
	var identityID, challengeID, fingerprint, secretDigest, keyID, nonce []byte
	var state, code, expiresRaw string
	if err := tx.QueryRowContext(ctx, `SELECT user_id, identity_id, challenge_id, token_version, identity_version, target_fingerprint,
        secret_digest, key_id, nonce, comparison_code, state, DATE_FORMAT(expires_at, '%Y-%m-%d %H:%i:%s.%f')
        FROM auth_login_approval WHERE id = ? FOR UPDATE`, []byte(p.data.ID)).Scan(&userID, &identityID, &challengeID, &rowTokenVersion,
		&rowIdentityVersion, &fingerprint, &secretDigest, &keyID, &nonce, &code, &state, &expiresRaw); err != nil ||
		userID != p.userID || string(identityID) != p.identityID || string(challengeID) != p.challengeID || rowTokenVersion != p.tokenVersion ||
		rowIdentityVersion != p.identityVersion || !hmac.Equal(fingerprint, p.fingerprint[:]) || !hmac.Equal(secretDigest, p.store.digest(p.data.ID, p.data.Secret)) ||
		string(keyID) != p.store.keyID || string(nonce) != p.data.Nonce || code != p.data.ComparisonCode ||
		(state != "pending" && state != "approved" && state != "denied") || requirePending && state != "pending" {
		return ErrUnavailable
	}
	expires, err := time.Parse("2006-01-02 15:04:05.000000", expiresRaw)
	now := p.store.now().UTC()
	if err != nil || now.IsZero() || !expires.Equal(p.data.ExpiresAt) || !expires.After(now) {
		return ErrUnavailable
	}
	return nil
}

type Decision string

const (
	Approve Decision = "approve"
	Deny    Decision = "deny"
)

// Decide accepts only a principal authenticated in this transaction. Approval
// requires the correct comparison code, explicit confirmation and a recent
// authentication. This is not an approval exchange or device registration.
func (s *Store) Decide(ctx context.Context, tx *sql.Tx, principal *session.Principal, approvalID string, decision Decision, code string, confirmed bool) error {
	if s == nil || s.now == nil || ctx == nil || tx == nil || principal == nil || len(approvalID) < 22 || len(approvalID) > 128 ||
		(decision != Approve && decision != Deny) || (decision == Approve && (!confirmed || len(code) != 6)) ||
		(decision == Deny && (confirmed || code != "")) {
		return ErrUnavailable
	}
	actor, err := principal.ForTransaction(ctx, tx)
	if err != nil {
		return ErrUnavailable
	}
	if s.checkEpoch(ctx, tx) != nil {
		return ErrUnavailable
	}
	var sessionIdentity []byte
	var authRaw string
	if err := tx.QueryRowContext(ctx, `SELECT identity_id, DATE_FORMAT(authenticated_at, '%Y-%m-%d %H:%i:%s.%f')
        FROM auth_session WHERE id = ? AND user_id = ? FOR UPDATE`, []byte(actor.SessionID), actor.SubjectID).
		Scan(&sessionIdentity, &authRaw); err != nil {
		return ErrUnavailable
	}
	var userID, tokenVersion, identityVersion uint64
	var originalIdentity []byte
	var storedCode, state, expiryRaw string
	if err := tx.QueryRowContext(ctx, `SELECT user_id, identity_id, token_version, identity_version, comparison_code, state,
        DATE_FORMAT(expires_at, '%Y-%m-%d %H:%i:%s.%f') FROM auth_login_approval WHERE id = ? FOR UPDATE`, []byte(approvalID)).
		Scan(&userID, &originalIdentity, &tokenVersion, &identityVersion, &storedCode, &state, &expiryRaw); err != nil ||
		userID != actor.SubjectID || state != "pending" || !hmac.Equal(originalIdentity, sessionIdentity) {
		return ErrUnavailable
	}
	var status string
	var currentTokenVersion, currentIdentityVersion uint64
	if err := tx.QueryRowContext(ctx, `SELECT status, token_version, identity_version FROM user WHERE id = ? FOR UPDATE`, userID).
		Scan(&status, &currentTokenVersion, &currentIdentityVersion); err != nil || status != "active" ||
		currentTokenVersion != tokenVersion || currentIdentityVersion != identityVersion {
		return ErrUnavailable
	}
	const layout = "2006-01-02 15:04:05.000000"
	expires, expiryErr := time.Parse(layout, expiryRaw)
	authenticated, authErr := time.Parse(layout, authRaw)
	now := s.now().UTC().Truncate(time.Microsecond)
	if expiryErr != nil || authErr != nil || now.IsZero() || !expires.After(now) || authenticated.After(now) {
		return ErrUnavailable
	}
	state = "denied"
	if decision == Approve {
		if !hmac.Equal([]byte(storedCode), []byte(code)) || !authenticated.Add(s.policy.ReauthWindow).After(now) {
			return ErrUnavailable
		}
		state = "approved"
	}
	if _, err := tx.ExecContext(ctx, `UPDATE auth_login_approval SET state = ?, decision_session_id = ?, decided_at = ? WHERE id = ? AND state = 'pending'`,
		state, []byte(actor.SessionID), timestamp(now), []byte(approvalID)); err != nil {
		return ErrUnavailable
	}
	if _, err := principal.ForTransaction(ctx, tx); err != nil {
		return ErrUnavailable
	}
	return nil
}

func timestamp(value time.Time) string {
	return value.UTC().Truncate(time.Microsecond).Format("2006-01-02 15:04:05.000000")
}
