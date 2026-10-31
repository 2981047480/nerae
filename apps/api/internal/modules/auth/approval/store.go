// Package approval implements internal new-device approval transactions.
// It does not exchange approvals for tokens or expose HTTP endpoints.
package approval

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
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
	"nerae/api/internal/modules/auth/loginledger"
	"nerae/api/internal/modules/auth/loginreceipt"
	"nerae/api/internal/modules/auth/session"
)

var (
	ErrUnavailable    = errors.New("approval unavailable")
	ErrInvalidReceipt = errors.New("approval receipt invalid")
)

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

// maxPendingListItems is a conservative internal bound. The wire contract has
// no pagination semantics, so an overfull result fails closed instead of
// returning a misleading partial list.
const maxPendingListItems = 100

// PendingListItem contains only fields permitted by the login-approval list
// contract. It intentionally excludes challenge, device, key and secret data.
type PendingListItem struct {
	ID             string    `json:"id"`
	ComparisonCode string    `json:"comparisonCode"`
	CreatedAt      time.Time `json:"createdAt"`
	ExpiresAt      time.Time `json:"expiresAt"`
}

func (PendingListItem) String() string   { return "PendingApprovalListItem{redacted}" }
func (PendingListItem) GoString() string { return "PendingApprovalListItem{redacted}" }
func (PendingListItem) LogValue() slog.Value {
	return slog.StringValue("PendingApprovalListItem{redacted}")
}

type PendingList struct {
	Items []PendingListItem `json:"items"`
}

func (PendingList) String() string       { return "PendingApprovalList{redacted}" }
func (PendingList) GoString() string     { return "PendingApprovalList{redacted}" }
func (PendingList) LogValue() slog.Value { return slog.StringValue("PendingApprovalList{redacted}") }

// ListPending reads the current subject's pending approvals from a transaction
// authenticated by Principal. The caller must commit the transaction before
// exposing the returned list. This primitive is not an HTTP handler and does
// not authorize a decision; Decide currently retains its same-identity rule.
func (s *Store) ListPending(ctx context.Context, tx *sql.Tx, principal *session.Principal) (PendingList, error) {
	if s == nil || s.now == nil || ctx == nil || tx == nil || principal == nil {
		return PendingList{}, ErrUnavailable
	}
	actor, err := principal.ForTransaction(ctx, tx)
	if err != nil || actor.SubjectID == 0 || actor.SessionID == "" {
		return PendingList{}, ErrUnavailable
	}
	if s.checkEpoch(ctx, tx) != nil {
		return PendingList{}, ErrUnavailable
	}
	var status string
	var tokenVersion, identityVersion uint64
	if err := tx.QueryRowContext(ctx, `SELECT status, token_version, identity_version FROM user WHERE id = ? FOR UPDATE`, actor.SubjectID).
		Scan(&status, &tokenVersion, &identityVersion); err != nil || status != "active" || tokenVersion == 0 || identityVersion == 0 {
		return PendingList{}, ErrUnavailable
	}
	var sessionUser uint64
	var sessionState string
	if err := tx.QueryRowContext(ctx, `SELECT user_id, state FROM auth_session WHERE id = ? FOR UPDATE`, []byte(actor.SessionID)).
		Scan(&sessionUser, &sessionState); err != nil || sessionUser != actor.SubjectID || sessionState != "active" {
		return PendingList{}, ErrUnavailable
	}
	rows, err := tx.QueryContext(ctx, `SELECT a.id, a.comparison_code,
		DATE_FORMAT(a.created_at, '%Y-%m-%d %H:%i:%s.%f'),
		DATE_FORMAT(a.expires_at, '%Y-%m-%d %H:%i:%s.%f')
		FROM auth_login_approval a
		JOIN login_identity i ON i.id = a.identity_id AND i.user_id = a.user_id
			AND i.kind = 'email' AND i.provider_scope = 'beta-email' AND i.state = 'active'
		WHERE a.user_id = ? AND a.token_version = ? AND a.identity_version = ? AND a.state = 'pending'
			AND a.expires_at > UTC_TIMESTAMP(6)
		ORDER BY a.created_at ASC, a.id ASC LIMIT ? FOR UPDATE`, actor.SubjectID, tokenVersion, identityVersion, maxPendingListItems+1)
	if err != nil {
		return PendingList{}, ErrUnavailable
	}
	defer rows.Close()
	items := make([]PendingListItem, 0, maxPendingListItems)
	for rows.Next() {
		var idRaw, code, createdRaw, expiresRaw []byte
		if err := rows.Scan(&idRaw, &code, &createdRaw, &expiresRaw); err != nil {
			return PendingList{}, ErrUnavailable
		}
		if len(items) == maxPendingListItems {
			return PendingList{}, ErrUnavailable
		}
		created, createdErr := time.Parse("2006-01-02 15:04:05.000000", string(createdRaw))
		expires, expiresErr := time.Parse("2006-01-02 15:04:05.000000", string(expiresRaw))
		id := string(idRaw)
		if !validApprovalOpaqueID(id) || !validComparisonCode(string(code)) || createdErr != nil || expiresErr != nil || !expires.After(created) {
			return PendingList{}, ErrUnavailable
		}
		items = append(items, PendingListItem{ID: id, ComparisonCode: string(code), CreatedAt: created.UTC(), ExpiresAt: expires.UTC()})
	}
	if rows.Err() != nil || rows.Close() != nil {
		return PendingList{}, ErrUnavailable
	}
	// The SQL predicate uses statement-time UTC. Recheck after acquiring row
	// locks so a wait cannot return an approval that expired while blocked.
	var nowRaw string
	if err := tx.QueryRowContext(ctx, `SELECT DATE_FORMAT(UTC_TIMESTAMP(6), '%Y-%m-%d %H:%i:%s.%f')`).Scan(&nowRaw); err != nil {
		return PendingList{}, ErrUnavailable
	}
	dbNow, err := time.Parse("2006-01-02 15:04:05.000000", nowRaw)
	if err != nil {
		return PendingList{}, ErrUnavailable
	}
	for _, item := range items {
		if !item.ExpiresAt.After(dbNow) {
			return PendingList{}, ErrUnavailable
		}
	}
	if _, err := principal.ForTransaction(ctx, tx); err != nil {
		return PendingList{}, ErrUnavailable
	}
	return PendingList{Items: items}, nil
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

// ReceiptRecoverer validates an encrypted new-device approval result. It is
// not an approval decision, device registration, session, or HTTP endpoint.
type ReceiptRecoverer struct {
	approvals  *Store
	ledger     *loginledger.Store
	challenges *challenge.Store
	crypto     *loginreceipt.Crypto
}

func (ReceiptRecoverer) String() string               { return "ApprovalReceiptRecoverer{redacted}" }
func (ReceiptRecoverer) GoString() string             { return "ApprovalReceiptRecoverer{redacted}" }
func (ReceiptRecoverer) MarshalJSON() ([]byte, error) { return nil, ErrUnavailable }
func (ReceiptRecoverer) LogValue() slog.Value {
	return slog.StringValue("ApprovalReceiptRecoverer{redacted}")
}

func NewReceiptRecoverer(approvals *Store, ledger *loginledger.Store, challenges *challenge.Store, crypto *loginreceipt.Crypto) (*ReceiptRecoverer, error) {
	if approvals == nil || ledger == nil || challenges == nil || crypto == nil {
		return nil, ErrUnavailable
	}
	return &ReceiptRecoverer{approvals: approvals, ledger: ledger, challenges: challenges, crypto: crypto}, nil
}

// ReceiptCandidate keeps the approval secret private until a fresh transaction
// rechecks the committed receipt, original device proof, and authorization.
type ReceiptCandidate struct {
	recoverer *ReceiptRecoverer
	sourceTx  *sql.Tx
	request   loginreceipt.Request
	scope     [32]byte
	binding   loginreceipt.Binding
	data      Data
	expiresAt time.Time
	proof     challenge.ReceiptProofDetails
}

func (ReceiptCandidate) String() string               { return "ApprovalReceiptCandidate{redacted}" }
func (ReceiptCandidate) GoString() string             { return "ApprovalReceiptCandidate{redacted}" }
func (ReceiptCandidate) MarshalJSON() ([]byte, error) { return nil, ErrUnavailable }
func (ReceiptCandidate) LogValue() slog.Value {
	return slog.StringValue("ApprovalReceiptCandidate{redacted}")
}

// Recover checks exact request, proof and current account/approval state before
// decrypting. No polling secret or other sensitive value leaves this live tx.
func (r *ReceiptRecoverer) Recover(ctx context.Context, tx *sql.Tx, handle *loginledger.Handle, proof *challenge.ReceiptProof, request loginreceipt.Request) (ReceiptCandidate, error) {
	if r == nil || r.approvals == nil || r.ledger == nil || r.challenges == nil || r.crypto == nil || ctx == nil || tx == nil || handle == nil || proof == nil {
		return ReceiptCandidate{}, ErrUnavailable
	}
	want, err := r.crypto.Summarize(request)
	if err != nil {
		return ReceiptCandidate{}, ErrInvalidReceipt
	}
	record, scope, binding, err := handle.RecheckExisting(ctx, tx)
	if err != nil {
		return ReceiptCandidate{}, receiptError(err)
	}
	if record.ResultType() != loginreceipt.ResultApprovalRequired || record.ChallengeID() != request.ChallengeID ||
		binding.ChallengeID() != request.ChallengeID || binding.IdempotencyDigest() != want.IdempotencyDigest() || binding.RequestDigest() != want.RequestDigest() {
		return ReceiptCandidate{}, ErrInvalidReceipt
	}
	proofDetails, err := proof.ForRequest(ctx, tx, request.ChallengeID, request.DeviceProof)
	if err != nil {
		return ReceiptCandidate{}, receiptError(err)
	}
	if proofDetails.Scope != scope {
		return ReceiptCandidate{}, ErrInvalidReceipt
	}
	reference := record.ResultReference()
	authority, err := r.approvals.authorizeReceipt(ctx, tx, reference, proofDetails)
	if err != nil {
		return ReceiptCandidate{}, err
	}
	fresh, freshScope, freshBinding, err := handle.RecheckExisting(ctx, tx)
	if err != nil {
		return ReceiptCandidate{}, receiptError(err)
	}
	if freshScope != scope || freshBinding.IdempotencyDigest() != binding.IdempotencyDigest() || freshBinding.RequestDigest() != binding.RequestDigest() ||
		fresh.ResultType() != record.ResultType() || fresh.ResultReference() != reference || !fresh.ExpiresAt().Equal(record.ExpiresAt()) {
		return ReceiptCandidate{}, ErrUnavailable
	}
	proofDetails, err = proof.ForRequest(ctx, tx, request.ChallengeID, request.DeviceProof)
	if err != nil || proofDetails.Scope != scope {
		return ReceiptCandidate{}, receiptError(err)
	}
	aad, err := r.crypto.NewContext(scope[:], binding, fresh.ResultType(), reference, fresh.ExpiresAt())
	if err != nil {
		return ReceiptCandidate{}, ErrUnavailable
	}
	plain, err := r.crypto.Open(aad, fresh.Sealed())
	if err != nil {
		return ReceiptCandidate{}, ErrUnavailable
	}
	raw := plain.Bytes()
	var payload receiptPayload
	if json.Unmarshal(raw, &payload) != nil {
		return ReceiptCandidate{}, ErrUnavailable
	}
	canonical, err := json.Marshal(payload)
	if err != nil || !bytes.Equal(raw, canonical) || payload.Version != 1 || !authority.matchesPayload(r.approvals, reference, payload) {
		return ReceiptCandidate{}, ErrUnavailable
	}
	data := Data{ID: reference, Secret: payload.Secret, Nonce: payload.Nonce, ComparisonCode: payload.ComparisonCode, ExpiresAt: payload.ExpiresAt}
	finalRecord, _, _, err := handle.RecheckExisting(ctx, tx)
	if err != nil {
		return ReceiptCandidate{}, receiptError(err)
	}
	if finalRecord.ResultType() != fresh.ResultType() || finalRecord.ResultReference() != reference || !finalRecord.ExpiresAt().Equal(fresh.ExpiresAt()) ||
		!hmac.Equal(finalRecord.Sealed().Bytes(), fresh.Sealed().Bytes()) {
		return ReceiptCandidate{}, ErrUnavailable
	}
	finalProof, err := proof.ForRequest(ctx, tx, request.ChallengeID, request.DeviceProof)
	if err != nil {
		return ReceiptCandidate{}, receiptError(err)
	}
	if finalProof.Scope != scope || finalProof.Fingerprint != proofDetails.Fingerprint {
		return ReceiptCandidate{}, ErrUnavailable
	}
	finalAuthority, err := r.approvals.authorizeReceipt(ctx, tx, reference, finalProof)
	if err != nil {
		return ReceiptCandidate{}, err
	}
	if finalAuthority != authority || !finalAuthority.matchesPayload(r.approvals, reference, payload) {
		return ReceiptCandidate{}, ErrInvalidReceipt
	}
	now := r.approvals.now().UTC()
	if now.IsZero() || !finalRecord.ExpiresAt().After(now) || !data.ExpiresAt.After(now) {
		return ReceiptCandidate{}, ErrInvalidReceipt
	}
	return ReceiptCandidate{recoverer: r, sourceTx: tx, request: request, scope: scope, binding: binding,
		data: data, expiresAt: finalRecord.ExpiresAt(), proof: finalProof}, nil
}

// AfterCommit re-reads the committed receipt in a fresh transaction, validates
// its original proof and current account/approval state, then checks time once
// more after commit. A rolled-back new approval cannot be recovered.
func (c ReceiptCandidate) AfterCommit(ctx context.Context, db *sql.DB) (Data, error) {
	if c.recoverer == nil || c.sourceTx == nil || ctx == nil || db == nil || c.data.ID == "" || c.data.Secret == "" {
		return Data{}, ErrUnavailable
	}
	var live int
	if err := c.sourceTx.QueryRowContext(ctx, `SELECT 1`).Scan(&live); !errors.Is(err, sql.ErrTxDone) {
		return Data{}, ErrUnavailable
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return Data{}, ErrUnavailable
	}
	defer tx.Rollback()
	handle, err := c.recoverer.ledger.Lock(ctx, tx, c.scope, c.binding)
	if err != nil {
		return Data{}, receiptError(err)
	}
	if _, exists := handle.ExistingRecord(); !exists {
		return Data{}, ErrInvalidReceipt
	}
	proof, err := c.recoverer.challenges.VerifyReceiptProof(ctx, tx, c.scope, c.request.ChallengeID, c.request.DeviceProof)
	if err != nil {
		return Data{}, receiptError(err)
	}
	fresh, err := c.recoverer.Recover(ctx, tx, handle, proof, c.request)
	if err != nil {
		return Data{}, err
	}
	if fresh.data.ID != c.data.ID || !hmac.Equal([]byte(fresh.data.Secret), []byte(c.data.Secret)) ||
		!hmac.Equal([]byte(fresh.data.Nonce), []byte(c.data.Nonce)) || fresh.data.ComparisonCode != c.data.ComparisonCode || !fresh.data.ExpiresAt.Equal(c.data.ExpiresAt) {
		return Data{}, ErrUnavailable
	}
	if err := tx.Commit(); err != nil {
		return Data{}, ErrUnavailable
	}
	now := c.recoverer.approvals.now().UTC()
	if now.IsZero() || !fresh.expiresAt.After(now) || !fresh.data.ExpiresAt.After(now) || fresh.proof.VerifyTimeOnly(now) != nil {
		return Data{}, ErrInvalidReceipt
	}
	return fresh.data, nil
}

func receiptError(err error) error {
	if errors.Is(err, loginledger.ErrConflict) || errors.Is(err, loginledger.ErrExpired) || errors.Is(err, challenge.ErrReceiptProofRejected) {
		return ErrInvalidReceipt
	}
	return ErrUnavailable
}

type receiptAuthority struct {
	userID          uint64
	identityID      string
	tokenVersion    uint64
	identityVersion uint64
	state           string
	secretDigest    [32]byte
	nonce           string
	comparisonCode  string
	expiresAt       time.Time
}

func (a receiptAuthority) matchesPayload(store *Store, reference string, payload receiptPayload) bool {
	return store != nil && validReceiptSecret(payload.Secret) && validReceiptSecret(payload.Nonce) && validComparisonCode(payload.ComparisonCode) &&
		hmac.Equal(a.secretDigest[:], store.digest(reference, payload.Secret)) && hmac.Equal([]byte(a.nonce), []byte(payload.Nonce)) &&
		a.comparisonCode == payload.ComparisonCode && a.expiresAt.Equal(payload.ExpiresAt)
}

func validReceiptSecret(value string) bool {
	decoded, err := base64.RawURLEncoding.Strict().DecodeString(value)
	return err == nil && len(value) == 43 && len(decoded) == 32 && base64.RawURLEncoding.EncodeToString(decoded) == value
}

func validComparisonCode(value string) bool {
	if len(value) != 6 {
		return false
	}
	for _, digit := range value {
		if digit < '0' || digit > '9' {
			return false
		}
	}
	return true
}

// authorizeReceipt uses current locking reads rather than a repeatable-read
// snapshot. Order is identity -> user -> approval -> target device; receipt and
// challenge locks were acquired first by the caller.
func (s *Store) authorizeReceipt(ctx context.Context, tx *sql.Tx, reference string, proof challenge.ReceiptProofDetails) (receiptAuthority, error) {
	var authority receiptAuthority
	if s == nil || s.now == nil || ctx == nil || tx == nil || len(reference) < 22 || len(reference) > 128 ||
		proof.Scope == ([32]byte{}) || proof.Fingerprint == ([32]byte{}) || proof.ChallengeID == "" {
		return authority, ErrUnavailable
	}
	var identityID []byte
	var userID uint64
	err := tx.QueryRowContext(ctx, `SELECT id, user_id FROM login_identity WHERE kind='email' AND provider_scope='beta-email'
		AND subject_lookup_hmac=? AND active_slot=1 FOR UPDATE`, proof.Scope[:]).Scan(&identityID, &userID)
	if err != nil {
		return authority, approvalRowError(err)
	}
	if len(identityID) == 0 || userID == 0 {
		return authority, ErrUnavailable
	}
	var status string
	var tokenVersion, identityVersion uint64
	err = tx.QueryRowContext(ctx, `SELECT status, token_version, identity_version FROM user WHERE id=? FOR UPDATE`, userID).
		Scan(&status, &tokenVersion, &identityVersion)
	if err != nil {
		return authority, approvalRowError(err)
	}
	if status != "active" || tokenVersion == 0 || identityVersion == 0 {
		return authority, ErrInvalidReceipt
	}
	if s.checkEpoch(ctx, tx) != nil {
		return authority, ErrUnavailable
	}
	var rowUser, rowTokenVersion, rowIdentityVersion uint64
	var rowIdentity, challengeID, fingerprint, secretDigest, keyID, nonce []byte
	var state, comparison, expiresRaw string
	err = tx.QueryRowContext(ctx, `SELECT user_id, identity_id, challenge_id, token_version, identity_version, target_fingerprint,
		secret_digest, key_id, nonce, comparison_code, state, CAST(expires_at AS CHAR)
		FROM auth_login_approval WHERE id=? FOR UPDATE`, []byte(reference)).Scan(&rowUser, &rowIdentity, &challengeID, &rowTokenVersion,
		&rowIdentityVersion, &fingerprint, &secretDigest, &keyID, &nonce, &comparison, &state, &expiresRaw)
	if err != nil {
		return authority, approvalRowError(err)
	}
	if rowUser != userID || !hmac.Equal(rowIdentity, identityID) || !hmac.Equal(challengeID, []byte(proof.ChallengeID)) ||
		rowTokenVersion != tokenVersion || rowIdentityVersion != identityVersion || !hmac.Equal(fingerprint, proof.Fingerprint[:]) ||
		string(keyID) != s.keyID || len(secretDigest) != 32 || len(nonce) != 43 || len(comparison) != 6 ||
		(state != "pending" && state != "approved" && state != "denied") {
		return authority, ErrInvalidReceipt
	}
	// A target key that has since been revoked or assigned to another account
	// must not recover the approval polling secret.
	var targetUser uint64
	var targetState string
	err = tx.QueryRowContext(ctx, `SELECT user_id, state FROM trusted_device WHERE key_fingerprint=? FOR UPDATE`, proof.Fingerprint[:]).Scan(&targetUser, &targetState)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return authority, ErrUnavailable
	}
	if err == nil && (targetUser != userID || targetState != "active") {
		return authority, ErrInvalidReceipt
	}
	expires, err := time.Parse("2006-01-02 15:04:05.999999", expiresRaw)
	now := s.now().UTC()
	if err != nil || now.IsZero() {
		return authority, ErrUnavailable
	}
	if !expires.After(now) {
		return authority, ErrInvalidReceipt
	}
	copy(authority.secretDigest[:], secretDigest)
	authority.userID, authority.identityID = userID, string(identityID)
	authority.tokenVersion, authority.identityVersion = tokenVersion, identityVersion
	authority.state, authority.nonce, authority.comparisonCode, authority.expiresAt = state, string(nonce), comparison, expires
	return authority, nil
}

func approvalRowError(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return ErrInvalidReceipt
	}
	return ErrUnavailable
}

func validApprovalOpaqueID(value string) bool {
	if len(value) < 22 || len(value) > 128 {
		return false
	}
	for i := 0; i < len(value); i++ {
		c := value[i]
		if !(c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '_' || c == '-') {
			return false
		}
	}
	return true
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
