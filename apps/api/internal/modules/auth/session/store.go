package session

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"errors"
	"log/slog"
	"strconv"
	"time"

	"nerae/api/internal/modules/auth/challenge"
	"nerae/api/internal/modules/auth/identity"
)

var (
	ErrSessionUnavailable = errors.New("session unavailable")
	ErrApprovalRequired   = errors.New("device approval required")
)

type Store struct {
	resolver *identity.Resolver
	codec    *TokenCodec
	now      func() time.Time
}

func NewStore(resolver *identity.Resolver, codec *TokenCodec) (*Store, error) {
	if resolver == nil || codec == nil {
		return nil, ErrSessionUnavailable
	}
	return &Store{resolver: resolver, codec: codec, now: time.Now}, nil
}

// Issued is an internal result. A caller must commit the entire challenge,
// identity, session and idempotency transaction before returning a token.
type Issued struct {
	token    AccessToken
	sourceTx *sql.Tx
	store    *Store
}

func (Issued) String() string               { return "Issued{redacted}" }
func (Issued) GoString() string             { return "Issued{redacted}" }
func (Issued) MarshalJSON() ([]byte, error) { return nil, ErrSessionUnavailable }
func (Issued) LogValue() slog.Value         { return slog.StringValue("Issued{redacted}") }

// AfterCommit exposes a token only after the originating transaction has ended
// and a fresh transaction confirms that its session is durable and active.
// A rollback, uncommitted transaction, revocation, or expiry cannot release it.
func (i Issued) AfterCommit(ctx context.Context, db *sql.DB) (string, error) {
	if ctx == nil || db == nil || i.sourceTx == nil || i.store == nil || i.token.ForResponse() == "" {
		return "", ErrSessionUnavailable
	}
	var live int
	if err := i.sourceTx.QueryRowContext(ctx, `SELECT 1`).Scan(&live); !errors.Is(err, sql.ErrTxDone) {
		return "", ErrSessionUnavailable
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return "", ErrSessionUnavailable
	}
	defer tx.Rollback()
	principal, err := i.store.Authenticate(ctx, tx, i.token.ForResponse())
	if err != nil {
		return "", ErrSessionUnavailable
	}
	if _, err := principal.ForTransaction(ctx, tx); err != nil {
		return "", ErrSessionUnavailable
	}
	if err := tx.Commit(); err != nil {
		return "", ErrSessionUnavailable
	}
	return i.token.ForResponse(), nil
}

// IssueEmail only accepts a verified challenge from this live transaction.
// A new device for an existing subject is rejected until a separate approval
// or recovery flow is implemented. Caller must roll back on every error.
// No SMTP, rate limiting, response idempotency, or HTTP endpoint is provided.
func (s *Store) IssueEmail(ctx context.Context, tx *sql.Tx, proof *challenge.VerifiedEmail) (Issued, error) {
	if s == nil || s.resolver == nil || s.codec == nil || s.now == nil || ctx == nil || tx == nil || proof == nil {
		return Issued{}, ErrSessionUnavailable
	}
	resolved, err := s.resolver.ResolveEmail(ctx, tx, proof)
	if err != nil {
		return Issued{}, ErrSessionUnavailable
	}
	if resolved.State == identity.ApprovalRequired {
		return Issued{}, ErrApprovalRequired
	}
	if resolved.State != identity.Created && resolved.State != identity.Trusted {
		return Issued{}, ErrSessionUnavailable
	}
	subjectID, err := strconv.ParseUint(resolved.SubjectID, 10, 64)
	if err != nil || subjectID == 0 || resolved.DeviceID == "" {
		return Issued{}, ErrSessionUnavailable
	}
	details, err := proof.ForTransaction(ctx, tx)
	if err != nil {
		return Issued{}, ErrSessionUnavailable
	}
	challengeID, err := proof.ChallengeIDForTransaction(ctx, tx)
	if err != nil {
		return Issued{}, ErrSessionUnavailable
	}
	var identityID []byte
	if err := tx.QueryRowContext(ctx, `SELECT id FROM login_identity WHERE user_id = ? AND kind = 'email' AND provider_scope = 'beta-email'
        AND subject_lookup_hmac = ? AND state = 'active' FOR UPDATE`, subjectID, details.Index[:]).Scan(&identityID); err != nil || len(identityID) == 0 {
		return Issued{}, ErrSessionUnavailable
	}
	var tokenVersion uint64
	var status string
	if err := tx.QueryRowContext(ctx, `SELECT token_version, status FROM user WHERE id = ? FOR UPDATE`, subjectID).Scan(&tokenVersion, &status); err != nil || tokenVersion == 0 || status != "active" {
		return Issued{}, ErrSessionUnavailable
	}
	var deviceState string
	var deviceOwner uint64
	var fingerprint []byte
	if err := tx.QueryRowContext(ctx, `SELECT user_id, state, key_fingerprint FROM trusted_device WHERE id = ? FOR UPDATE`, []byte(resolved.DeviceID)).
		Scan(&deviceOwner, &deviceState, &fingerprint); err != nil || deviceOwner != subjectID || deviceState != "active" || !hmac.Equal(fingerprint, details.Fingerprint[:]) {
		return Issued{}, ErrSessionUnavailable
	}
	var random [32]byte
	if _, err := rand.Read(random[:]); err != nil {
		return Issued{}, ErrSessionUnavailable
	}
	sessionID := base64.RawURLEncoding.EncodeToString(random[:])
	token, err := s.codec.Issue(resolved.SubjectID, sessionID, tokenVersion)
	if err != nil {
		return Issued{}, ErrSessionUnavailable
	}
	claims, err := s.codec.Verify(token.ForResponse())
	if err != nil {
		return Issued{}, ErrSessionUnavailable
	}
	if _, err := proof.ForTransaction(ctx, tx); err != nil {
		return Issued{}, ErrSessionUnavailable
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO auth_session
		(id, user_id, identity_id, device_id, login_challenge_id, token_version, state, authenticated_at, expires_at, created_at)
		VALUES (?, ?, ?, ?, ?, ?, 'active', ?, ?, ?)`, []byte(sessionID), subjectID, identityID, []byte(resolved.DeviceID), []byte(challengeID), tokenVersion,
		utcTimestamp(claims.IssuedAt), utcTimestamp(claims.ExpiresAt), utcTimestamp(claims.IssuedAt))
	if err != nil {
		return Issued{}, ErrSessionUnavailable
	}
	if _, err := proof.ForTransaction(ctx, tx); err != nil {
		return Issued{}, ErrSessionUnavailable
	}
	return Issued{token: token, sourceTx: tx, store: s}, nil
}

type Principal struct {
	tx          *sql.Tx
	store       *Store
	token       string
	subjectID   uint64
	sessionID   string
	deviceID    string
	ageVerified bool
	expiresAt   time.Time
	now         func() time.Time
}

type PrincipalDetails struct {
	SubjectID   uint64
	SessionID   string
	DeviceID    string
	AgeVerified bool
}

func (Principal) String() string                      { return "Principal{redacted}" }
func (Principal) GoString() string                    { return "Principal{redacted}" }
func (Principal) MarshalJSON() ([]byte, error)        { return nil, ErrSessionUnavailable }
func (Principal) LogValue() slog.Value                { return slog.StringValue("Principal{redacted}") }
func (PrincipalDetails) String() string               { return "PrincipalDetails{redacted}" }
func (PrincipalDetails) GoString() string             { return "PrincipalDetails{redacted}" }
func (PrincipalDetails) MarshalJSON() ([]byte, error) { return nil, ErrSessionUnavailable }
func (PrincipalDetails) LogValue() slog.Value         { return slog.StringValue("PrincipalDetails{redacted}") }

// ForTransaction requires the original live transaction. Authentication locks
// the user, identity, device and session rows until that transaction ends.
// The caller must recheck this before committing business changes. AgeVerified
// alone is not role or resource authorization.
func (p *Principal) ForTransaction(ctx context.Context, tx *sql.Tx) (PrincipalDetails, error) {
	if p == nil || ctx == nil || tx == nil || p.tx != tx || p.store == nil || p.token == "" || p.now == nil || p.subjectID == 0 || p.sessionID == "" || p.deviceID == "" || !p.expiresAt.After(p.now().UTC()) {
		return PrincipalDetails{}, ErrSessionUnavailable
	}
	// Recheck even when rows are locked: the current transaction itself may
	// have revoked its user, identity, device or session since authentication.
	fresh, err := p.store.Authenticate(ctx, tx, p.token)
	if err != nil || fresh.subjectID != p.subjectID || fresh.sessionID != p.sessionID || fresh.deviceID != p.deviceID {
		return PrincipalDetails{}, ErrSessionUnavailable
	}
	return PrincipalDetails{SubjectID: fresh.subjectID, SessionID: fresh.sessionID, DeviceID: fresh.deviceID, AgeVerified: fresh.ageVerified}, nil
}

// Authenticate checks signed claims and locks current DB state. The caller
// must perform resource and age authorization and its business writes in the
// same tx, and must not use this value after commit/rollback. Lock order is
// identity -> user -> device -> session; workout rows are locked afterwards.
func (s *Store) Authenticate(ctx context.Context, tx *sql.Tx, token string) (*Principal, error) {
	if s == nil || s.codec == nil || s.now == nil || ctx == nil || tx == nil {
		return nil, ErrSessionUnavailable
	}
	claims, err := s.codec.Verify(token)
	if err != nil {
		return nil, ErrSessionUnavailable
	}
	subjectID, err := strconv.ParseUint(claims.SubjectID, 10, 64)
	if err != nil || subjectID == 0 {
		return nil, ErrSessionUnavailable
	}
	var initialUser uint64
	var identityID, deviceID []byte
	if err := tx.QueryRowContext(ctx, `SELECT user_id, identity_id, device_id FROM auth_session WHERE id = ?`, []byte(claims.SessionID)).
		Scan(&initialUser, &identityID, &deviceID); err != nil || initialUser != subjectID || len(identityID) == 0 || len(deviceID) == 0 {
		return nil, ErrSessionUnavailable
	}
	var identityOwner uint64
	var identityState string
	if err := tx.QueryRowContext(ctx, `SELECT user_id, state FROM login_identity WHERE id = ? FOR UPDATE`, identityID).Scan(&identityOwner, &identityState); err != nil || identityOwner != subjectID || identityState != "active" {
		return nil, ErrSessionUnavailable
	}
	var userStatus string
	var tokenVersion uint64
	var ageVerified bool
	if err := tx.QueryRowContext(ctx, `SELECT status, token_version, age_verified_at IS NOT NULL FROM user WHERE id = ? FOR UPDATE`, subjectID).
		Scan(&userStatus, &tokenVersion, &ageVerified); err != nil || userStatus != "active" || tokenVersion != claims.TokenVersion {
		return nil, ErrSessionUnavailable
	}
	var deviceOwner uint64
	var deviceState string
	if err := tx.QueryRowContext(ctx, `SELECT user_id, state FROM trusted_device WHERE id = ? FOR UPDATE`, deviceID).Scan(&deviceOwner, &deviceState); err != nil || deviceOwner != subjectID || deviceState != "active" {
		return nil, ErrSessionUnavailable
	}
	var rowUser, rowVersion uint64
	var rowIdentity, rowDevice []byte
	var state, authAt, expiresAt string
	if err := tx.QueryRowContext(ctx, `SELECT user_id, identity_id, device_id, token_version, state,
        DATE_FORMAT(authenticated_at, '%Y-%m-%d %H:%i:%s.%f'), DATE_FORMAT(expires_at, '%Y-%m-%d %H:%i:%s.%f')
        FROM auth_session WHERE id = ? FOR UPDATE`, []byte(claims.SessionID)).
		Scan(&rowUser, &rowIdentity, &rowDevice, &rowVersion, &state, &authAt, &expiresAt); err != nil || rowUser != subjectID ||
		string(rowIdentity) != string(identityID) || string(rowDevice) != string(deviceID) || rowVersion != claims.TokenVersion || state != "active" {
		return nil, ErrSessionUnavailable
	}
	issued, errIssued := time.Parse("2006-01-02 15:04:05.000000", authAt)
	expires, errExpires := time.Parse("2006-01-02 15:04:05.000000", expiresAt)
	if errIssued != nil || errExpires != nil || !issued.Equal(claims.IssuedAt) || !expires.Equal(claims.ExpiresAt) || !expires.After(s.now().UTC()) {
		return nil, ErrSessionUnavailable
	}
	// A long wait on any lock must not extend the signed token's validity.
	if _, err := s.codec.Verify(token); err != nil {
		return nil, ErrSessionUnavailable
	}
	return &Principal{tx: tx, store: s, token: token, subjectID: subjectID, sessionID: claims.SessionID, deviceID: string(deviceID), ageVerified: ageVerified, expiresAt: expires, now: s.now}, nil
}

func utcTimestamp(value time.Time) string {
	return value.UTC().Truncate(time.Microsecond).Format("2006-01-02 15:04:05.000000")
}
