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
	"nerae/api/internal/modules/auth/loginledger"
	"nerae/api/internal/modules/auth/loginreceipt"
)

var (
	ErrSessionUnavailable = errors.New("session unavailable")
	ErrInvalidSession     = errors.New("invalid or revoked session")
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

// SealedReceipt contains only an encrypted token and its non-bearer session
// reference. It does not prove commit or authorize replay.
type SealedReceipt struct {
	reference string
	sealed    loginreceipt.Sealed
	expiresAt time.Time
}

func (SealedReceipt) String() string                    { return "SealedSessionReceipt{redacted}" }
func (SealedReceipt) GoString() string                  { return "SealedSessionReceipt{redacted}" }
func (SealedReceipt) MarshalJSON() ([]byte, error)      { return nil, ErrSessionUnavailable }
func (SealedReceipt) LogValue() slog.Value              { return slog.StringValue("SealedSessionReceipt{redacted}") }
func (r SealedReceipt) Reference() string               { return r.reference }
func (r SealedReceipt) Ciphertext() loginreceipt.Sealed { return r.sealed }
func (r SealedReceipt) ExpiresAt() time.Time            { return r.expiresAt }

// SealForReceipt encrypts an issued token while its original transaction and
// verified email proof are still live. The caller must use a binding derived
// from the exact login request, persist the returned ciphertext in this same
// transaction, and release no credential until commit and a fresh replay-time
// signature, receipt, and authorization recheck. This method never returns a
// token or asserts that the transaction will commit. The returned expiration
// is capped at the session's own expiration.
func (i Issued) SealForReceipt(ctx context.Context, tx *sql.Tx, proof *challenge.VerifiedEmail, crypto *loginreceipt.Crypto, binding loginreceipt.Binding, expires time.Time) (SealedReceipt, error) {
	if ctx == nil || tx == nil || i.sourceTx != tx || i.store == nil || i.store.now == nil || proof == nil || crypto == nil || i.token.ForResponse() == "" {
		return SealedReceipt{}, ErrSessionUnavailable
	}
	verified, err := proof.ForTransaction(ctx, tx)
	if err != nil {
		return SealedReceipt{}, ErrSessionUnavailable
	}
	challengeID, err := proof.ChallengeIDForTransaction(ctx, tx)
	if err != nil || challengeID != binding.ChallengeID() {
		return SealedReceipt{}, ErrSessionUnavailable
	}
	claims, err := i.store.verifySessionToken(i.token.ForResponse())
	if err != nil {
		return SealedReceipt{}, ErrSessionUnavailable
	}
	if expires.After(claims.ExpiresAt) {
		expires = claims.ExpiresAt
	}
	principal, err := i.store.Authenticate(ctx, tx, i.token.ForResponse())
	if err != nil {
		return SealedReceipt{}, ErrSessionUnavailable
	}
	details, err := principal.ForTransaction(ctx, tx)
	if err != nil || details.SessionID != claims.SessionID {
		return SealedReceipt{}, ErrSessionUnavailable
	}
	var fingerprint, storedChallenge, identityID []byte
	if err := tx.QueryRowContext(ctx, `SELECT key_fingerprint FROM trusted_device WHERE id = ? FOR UPDATE`, []byte(details.DeviceID)).Scan(&fingerprint); err != nil ||
		!hmac.Equal(fingerprint, verified.Fingerprint[:]) {
		return SealedReceipt{}, ErrSessionUnavailable
	}
	if err := tx.QueryRowContext(ctx, `SELECT login_challenge_id, identity_id FROM auth_session WHERE id = ? FOR UPDATE`, []byte(details.SessionID)).Scan(&storedChallenge, &identityID); err != nil ||
		!hmac.Equal(storedChallenge, []byte(challengeID)) {
		return SealedReceipt{}, ErrSessionUnavailable
	}
	var identityKind string
	var providerScope, identityIndex []byte
	if err := tx.QueryRowContext(ctx, `SELECT kind, provider_scope, subject_lookup_hmac FROM login_identity WHERE id = ? FOR UPDATE`, identityID).
		Scan(&identityKind, &providerScope, &identityIndex); err != nil || identityKind != "email" || string(providerScope) != "beta-email" ||
		!hmac.Equal(identityIndex, verified.Index[:]) {
		return SealedReceipt{}, ErrSessionUnavailable
	}
	// These checks also catch expiry or state changes made by this transaction
	// while the identity, device and session locks were acquired.
	if _, err := proof.ForTransaction(ctx, tx); err != nil {
		return SealedReceipt{}, ErrSessionUnavailable
	}
	if _, err := principal.ForTransaction(ctx, tx); err != nil {
		return SealedReceipt{}, ErrSessionUnavailable
	}
	if now := i.store.now().UTC(); now.IsZero() || !expires.After(now) {
		return SealedReceipt{}, ErrSessionUnavailable
	}
	aad, err := crypto.NewContext(verified.Index[:], binding, loginreceipt.ResultAuthenticated, claims.SessionID, expires)
	if err != nil {
		return SealedReceipt{}, ErrSessionUnavailable
	}
	sealed, err := crypto.Seal(aad, []byte(i.token.ForResponse()))
	if err != nil {
		return SealedReceipt{}, ErrSessionUnavailable
	}
	return SealedReceipt{reference: claims.SessionID, sealed: sealed, expiresAt: expires}, nil
}

// ReceiptRecoverer verifies a stored session receipt. It is an internal
// transaction coordinator, not an HTTP endpoint or a substitute for rate limits.
type ReceiptRecoverer struct {
	sessions   *Store
	ledger     *loginledger.Store
	challenges *challenge.Store
	crypto     *loginreceipt.Crypto
}

func (ReceiptRecoverer) String() string               { return "SessionReceiptRecoverer{redacted}" }
func (ReceiptRecoverer) GoString() string             { return "SessionReceiptRecoverer{redacted}" }
func (ReceiptRecoverer) MarshalJSON() ([]byte, error) { return nil, ErrSessionUnavailable }
func (ReceiptRecoverer) LogValue() slog.Value {
	return slog.StringValue("SessionReceiptRecoverer{redacted}")
}

func NewReceiptRecoverer(sessions *Store, ledger *loginledger.Store, challenges *challenge.Store, crypto *loginreceipt.Crypto) (*ReceiptRecoverer, error) {
	if sessions == nil || ledger == nil || challenges == nil || crypto == nil {
		return nil, ErrSessionUnavailable
	}
	return &ReceiptRecoverer{sessions: sessions, ledger: ledger, challenges: challenges, crypto: crypto}, nil
}

// ReceiptCandidate contains sensitive material but exposes none until a new
// transaction verifies the committed receipt and current authorization again.
type ReceiptCandidate struct {
	recoverer *ReceiptRecoverer
	sourceTx  *sql.Tx
	request   loginreceipt.Request
	scope     [32]byte
	binding   loginreceipt.Binding
	token     string
	expiresAt time.Time
	proof     challenge.ReceiptProofDetails
}

func (ReceiptCandidate) String() string               { return "SessionReceiptCandidate{redacted}" }
func (ReceiptCandidate) GoString() string             { return "SessionReceiptCandidate{redacted}" }
func (ReceiptCandidate) MarshalJSON() ([]byte, error) { return nil, ErrSessionUnavailable }
func (ReceiptCandidate) LogValue() slog.Value {
	return slog.StringValue("SessionReceiptCandidate{redacted}")
}

// Recover accepts an exact-request receipt and proof locked in this tx. It
// first authorizes the referenced identity, user, device and session without
// decrypting, then authenticates the token and rechecks all time-sensitive
// inputs. It never returns a bearer token from the live transaction.
func (r *ReceiptRecoverer) Recover(ctx context.Context, tx *sql.Tx, handle *loginledger.Handle, proof *challenge.ReceiptProof, request loginreceipt.Request) (ReceiptCandidate, error) {
	if r == nil || r.sessions == nil || r.ledger == nil || r.challenges == nil || r.crypto == nil || ctx == nil || tx == nil || handle == nil || proof == nil {
		return ReceiptCandidate{}, ErrSessionUnavailable
	}
	want, err := r.crypto.Summarize(request)
	if err != nil {
		return ReceiptCandidate{}, ErrInvalidSession
	}
	record, scope, binding, err := handle.RecheckExisting(ctx, tx)
	if err != nil {
		return ReceiptCandidate{}, receiptCheckError(err)
	}
	if record.ResultType() != loginreceipt.ResultAuthenticated || record.ChallengeID() != request.ChallengeID ||
		binding.ChallengeID() != request.ChallengeID || binding.IdempotencyDigest() != want.IdempotencyDigest() || binding.RequestDigest() != want.RequestDigest() {
		return ReceiptCandidate{}, ErrInvalidSession
	}
	proofDetails, err := proof.ForRequest(ctx, tx, request.ChallengeID, request.DeviceProof)
	if err != nil {
		return ReceiptCandidate{}, receiptCheckError(err)
	}
	if proofDetails.Scope != scope {
		return ReceiptCandidate{}, ErrInvalidSession
	}
	reference := record.ResultReference()
	authority, err := r.sessions.authorizeReceiptReference(ctx, tx, reference, proofDetails)
	if err != nil {
		return ReceiptCandidate{}, err
	}
	// Account and session locks may wait. Recheck the receipt and exact proof
	// before decrypting, including their effective expiration.
	fresh, freshScope, freshBinding, err := handle.RecheckExisting(ctx, tx)
	if err != nil {
		return ReceiptCandidate{}, receiptCheckError(err)
	}
	if freshScope != scope || freshBinding.IdempotencyDigest() != binding.IdempotencyDigest() || freshBinding.RequestDigest() != binding.RequestDigest() ||
		fresh.ResultType() != record.ResultType() || fresh.ResultReference() != reference || !fresh.ExpiresAt().Equal(record.ExpiresAt()) {
		return ReceiptCandidate{}, ErrSessionUnavailable
	}
	proofDetails, err = proof.ForRequest(ctx, tx, request.ChallengeID, request.DeviceProof)
	if err != nil || proofDetails.Scope != scope {
		return ReceiptCandidate{}, receiptCheckError(err)
	}
	aad, err := r.crypto.NewContext(scope[:], binding, fresh.ResultType(), reference, fresh.ExpiresAt())
	if err != nil {
		return ReceiptCandidate{}, ErrSessionUnavailable
	}
	plaintext, err := r.crypto.Open(aad, fresh.Sealed())
	if err != nil {
		return ReceiptCandidate{}, ErrSessionUnavailable
	}
	token := string(plaintext.Bytes())
	if err := r.sessions.verifyReceiptToken(token, reference, authority); err != nil {
		return ReceiptCandidate{}, err
	}
	finalRecord, _, _, err := handle.RecheckExisting(ctx, tx)
	if err != nil {
		return ReceiptCandidate{}, receiptCheckError(err)
	}
	if finalRecord.ResultType() != fresh.ResultType() || finalRecord.ResultReference() != reference || !finalRecord.ExpiresAt().Equal(fresh.ExpiresAt()) ||
		!hmac.Equal(finalRecord.Sealed().Bytes(), fresh.Sealed().Bytes()) {
		return ReceiptCandidate{}, ErrSessionUnavailable
	}
	finalProof, err := proof.ForRequest(ctx, tx, request.ChallengeID, request.DeviceProof)
	if err != nil {
		return ReceiptCandidate{}, receiptCheckError(err)
	}
	if finalProof.Scope != scope || finalProof.Fingerprint != proofDetails.Fingerprint {
		return ReceiptCandidate{}, ErrSessionUnavailable
	}
	// The final proof lookup can itself wait. Do not rely on the earlier
	// session check if its token or account state changed in the meantime.
	finalAuthority, err := r.sessions.authorizeReceiptReference(ctx, tx, reference, finalProof)
	if err != nil {
		return ReceiptCandidate{}, err
	}
	if finalAuthority != authority {
		return ReceiptCandidate{}, ErrInvalidSession
	}
	if err := r.sessions.verifyReceiptToken(token, reference, finalAuthority); err != nil {
		return ReceiptCandidate{}, err
	}
	if now := r.sessions.now().UTC(); now.IsZero() || !finalRecord.ExpiresAt().After(now) {
		return ReceiptCandidate{}, ErrInvalidSession
	}
	return ReceiptCandidate{recoverer: r, sourceTx: tx, request: request, scope: scope, binding: binding, token: token, expiresAt: finalRecord.ExpiresAt(), proof: finalProof}, nil
}

// AfterCommit re-reads the committed receipt in a fresh transaction and repeats
// the proof and current authorization checks before exposing the token. A
// rolled-back newly issued receipt cannot be recovered by this method.
func (c ReceiptCandidate) AfterCommit(ctx context.Context, db *sql.DB) (string, error) {
	if c.recoverer == nil || c.sourceTx == nil || ctx == nil || db == nil || c.token == "" {
		return "", ErrSessionUnavailable
	}
	var live int
	if err := c.sourceTx.QueryRowContext(ctx, `SELECT 1`).Scan(&live); !errors.Is(err, sql.ErrTxDone) {
		return "", ErrSessionUnavailable
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return "", ErrSessionUnavailable
	}
	defer tx.Rollback()
	handle, err := c.recoverer.ledger.Lock(ctx, tx, c.scope, c.binding)
	if err != nil {
		return "", receiptCheckError(err)
	}
	if _, exists := handle.ExistingRecord(); !exists {
		return "", ErrInvalidSession
	}
	proof, err := c.recoverer.challenges.VerifyReceiptProof(ctx, tx, c.scope, c.request.ChallengeID, c.request.DeviceProof)
	if err != nil {
		return "", receiptCheckError(err)
	}
	fresh, err := c.recoverer.Recover(ctx, tx, handle, proof, c.request)
	if err != nil {
		return "", err
	}
	if !hmac.Equal([]byte(fresh.token), []byte(c.token)) {
		return "", ErrSessionUnavailable
	}
	if err := tx.Commit(); err != nil {
		return "", ErrSessionUnavailable
	}
	// A slow commit must not extend the session or receipt lifetime.
	if _, err := c.recoverer.sessions.verifySessionToken(fresh.token); err != nil {
		return "", err
	}
	now := c.recoverer.sessions.now().UTC()
	if now.IsZero() || !fresh.expiresAt.After(now) || fresh.proof.VerifyTimeOnly(now) != nil {
		return "", ErrInvalidSession
	}
	return fresh.token, nil
}

func receiptCheckError(err error) error {
	if errors.Is(err, loginledger.ErrConflict) || errors.Is(err, loginledger.ErrExpired) || errors.Is(err, challenge.ErrReceiptProofRejected) {
		return ErrInvalidSession
	}
	return ErrSessionUnavailable
}

func receiptRowError(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return ErrInvalidSession
	}
	return ErrSessionUnavailable
}

type receiptAuthority struct {
	userID        uint64
	deviceID      string
	tokenVersion  uint64
	authenticated time.Time
	expiresAt     time.Time
}

func (s *Store) verifyReceiptToken(token, reference string, authority receiptAuthority) error {
	claims, err := s.verifySessionToken(token)
	if err != nil {
		return err
	}
	userID, err := strconv.ParseUint(claims.SubjectID, 10, 64)
	if err != nil || userID != authority.userID || claims.SessionID != reference || claims.TokenVersion != authority.tokenVersion ||
		!claims.IssuedAt.Equal(authority.authenticated) || !claims.ExpiresAt.Equal(authority.expiresAt) {
		return ErrInvalidSession
	}
	return nil
}

// authorizeReceiptReference uses current locking reads, even when the caller
// already established a repeatable-read snapshot before another login commit.
// Order is identity -> user -> device -> session; challenge and receipt locks
// are already held by the caller.
func (s *Store) authorizeReceiptReference(ctx context.Context, tx *sql.Tx, reference string, proof challenge.ReceiptProofDetails) (receiptAuthority, error) {
	var authority receiptAuthority
	if s == nil || s.now == nil || ctx == nil || tx == nil || !validSessionID(reference) || proof.Scope == ([32]byte{}) || proof.Fingerprint == ([32]byte{}) || proof.ChallengeID == "" {
		return authority, ErrSessionUnavailable
	}
	var userID uint64
	var identityID []byte
	err := tx.QueryRowContext(ctx, `SELECT id, user_id FROM login_identity WHERE kind='email' AND provider_scope='beta-email'
		AND subject_lookup_hmac=? AND active_slot=1 FOR UPDATE`, proof.Scope[:]).Scan(&identityID, &userID)
	if err != nil {
		return authority, receiptRowError(err)
	}
	if userID == 0 || len(identityID) == 0 {
		return authority, ErrSessionUnavailable
	}
	var status string
	var tokenVersion uint64
	err = tx.QueryRowContext(ctx, `SELECT status, token_version FROM user WHERE id = ? FOR UPDATE`, userID).Scan(&status, &tokenVersion)
	if err != nil {
		return authority, receiptRowError(err)
	}
	if status != "active" || tokenVersion == 0 {
		return authority, ErrInvalidSession
	}
	var deviceUser uint64
	var deviceState string
	var deviceID []byte
	err = tx.QueryRowContext(ctx, `SELECT id, user_id, state FROM trusted_device WHERE key_fingerprint = ? FOR UPDATE`, proof.Fingerprint[:]).Scan(&deviceID, &deviceUser, &deviceState)
	if err != nil {
		return authority, receiptRowError(err)
	}
	if deviceUser != userID || deviceState != "active" || len(deviceID) == 0 {
		return authority, ErrInvalidSession
	}
	var rowUser, rowVersion uint64
	var rowIdentity, rowDevice, challengeID []byte
	var state, authenticatedRaw, expiryRaw string
	err = tx.QueryRowContext(ctx, `SELECT user_id, identity_id, device_id, login_challenge_id, token_version, state, CAST(authenticated_at AS CHAR), CAST(expires_at AS CHAR)
		FROM auth_session WHERE id = ? FOR UPDATE`, []byte(reference)).Scan(&rowUser, &rowIdentity, &rowDevice, &challengeID, &rowVersion, &state, &authenticatedRaw, &expiryRaw)
	if err != nil {
		return authority, receiptRowError(err)
	}
	if rowUser != userID || !hmac.Equal(rowIdentity, identityID) || !hmac.Equal(rowDevice, deviceID) ||
		!hmac.Equal(challengeID, []byte(proof.ChallengeID)) || rowVersion != tokenVersion || state != "active" {
		return authority, ErrInvalidSession
	}
	authenticated, authErr := time.Parse("2006-01-02 15:04:05.999999", authenticatedRaw)
	expires, err := time.Parse("2006-01-02 15:04:05.999999", expiryRaw)
	now := s.now().UTC()
	if err != nil || authErr != nil || now.IsZero() {
		return authority, ErrSessionUnavailable
	}
	if !expires.After(now) {
		return authority, ErrInvalidSession
	}
	return receiptAuthority{userID: userID, deviceID: string(deviceID), tokenVersion: tokenVersion, authenticated: authenticated, expiresAt: expires}, nil
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
// ErrInvalidSession means the signed credential is invalid, expired, missing,
// or revoked. ErrSessionUnavailable means storage, context or internal state
// could not be verified and MUST NOT be presented as a definite logout.
func (s *Store) Authenticate(ctx context.Context, tx *sql.Tx, token string) (*Principal, error) {
	if s == nil || s.codec == nil || s.now == nil || ctx == nil || tx == nil {
		return nil, ErrSessionUnavailable
	}
	claims, err := s.verifySessionToken(token)
	if err != nil {
		return nil, err
	}
	subjectID, err := strconv.ParseUint(claims.SubjectID, 10, 64)
	if err != nil || subjectID == 0 {
		return nil, ErrSessionUnavailable
	}
	var initialUser uint64
	var identityID, deviceID []byte
	err = tx.QueryRowContext(ctx, `SELECT user_id, identity_id, device_id FROM auth_session WHERE id = ?`, []byte(claims.SessionID)).
		Scan(&initialUser, &identityID, &deviceID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrInvalidSession
	}
	if err != nil || initialUser != subjectID || len(identityID) == 0 || len(deviceID) == 0 {
		return nil, ErrSessionUnavailable
	}
	var identityOwner uint64
	var identityState string
	if err := tx.QueryRowContext(ctx, `SELECT user_id, state FROM login_identity WHERE id = ? FOR UPDATE`, identityID).Scan(&identityOwner, &identityState); err != nil || identityOwner != subjectID {
		return nil, ErrSessionUnavailable
	}
	if identityState == "revoked" {
		return nil, ErrInvalidSession
	}
	if identityState != "active" {
		return nil, ErrSessionUnavailable
	}
	var userStatus string
	var tokenVersion uint64
	var ageVerified bool
	if err := tx.QueryRowContext(ctx, `SELECT status, token_version, age_verified_at IS NOT NULL FROM user WHERE id = ? FOR UPDATE`, subjectID).
		Scan(&userStatus, &tokenVersion, &ageVerified); err != nil || tokenVersion == 0 {
		return nil, ErrSessionUnavailable
	}
	if userStatus == "disabled" || tokenVersion != claims.TokenVersion {
		return nil, ErrInvalidSession
	}
	if userStatus != "active" {
		return nil, ErrSessionUnavailable
	}
	var deviceOwner uint64
	var deviceState string
	if err := tx.QueryRowContext(ctx, `SELECT user_id, state FROM trusted_device WHERE id = ? FOR UPDATE`, deviceID).Scan(&deviceOwner, &deviceState); err != nil || deviceOwner != subjectID {
		return nil, ErrSessionUnavailable
	}
	if deviceState == "revoked" {
		return nil, ErrInvalidSession
	}
	if deviceState != "active" {
		return nil, ErrSessionUnavailable
	}
	var rowUser, rowVersion uint64
	var rowIdentity, rowDevice []byte
	var state, authAt, expiresAt string
	if err := tx.QueryRowContext(ctx, `SELECT user_id, identity_id, device_id, token_version, state,
        DATE_FORMAT(authenticated_at, '%Y-%m-%d %H:%i:%s.%f'), DATE_FORMAT(expires_at, '%Y-%m-%d %H:%i:%s.%f')
        FROM auth_session WHERE id = ? FOR UPDATE`, []byte(claims.SessionID)).
		Scan(&rowUser, &rowIdentity, &rowDevice, &rowVersion, &state, &authAt, &expiresAt); err != nil || rowUser != subjectID ||
		string(rowIdentity) != string(identityID) || string(rowDevice) != string(deviceID) || rowVersion != claims.TokenVersion {
		return nil, ErrSessionUnavailable
	}
	if state == "revoked" {
		return nil, ErrInvalidSession
	}
	if state != "active" {
		return nil, ErrSessionUnavailable
	}
	issued, errIssued := time.Parse("2006-01-02 15:04:05.000000", authAt)
	expires, errExpires := time.Parse("2006-01-02 15:04:05.000000", expiresAt)
	if errIssued != nil || errExpires != nil || !issued.Equal(claims.IssuedAt) || !expires.Equal(claims.ExpiresAt) {
		return nil, ErrSessionUnavailable
	}
	now := s.now().UTC()
	if now.IsZero() {
		return nil, ErrSessionUnavailable
	}
	if !expires.After(now) {
		return nil, ErrInvalidSession
	}
	// A long wait on any lock must not extend the signed token's validity.
	if _, err := s.verifySessionToken(token); err != nil {
		return nil, err
	}
	return &Principal{tx: tx, store: s, token: token, subjectID: subjectID, sessionID: claims.SessionID, deviceID: string(deviceID), ageVerified: ageVerified, expiresAt: expires, now: s.now}, nil
}

// Verify against one captured clock reading so codec configuration or clock
// failure cannot be confused with a bad credential. Capture afresh after locks.
func (s *Store) verifySessionToken(token string) (TokenClaims, error) {
	if s == nil || s.codec == nil || len(s.codec.key) != 32 || s.codec.now == nil ||
		s.codec.policy.MaxLifetime < time.Second || s.codec.policy.MaxLifetime > 30*24*time.Hour ||
		s.codec.policy.MaxLifetime%time.Second != 0 || s.codec.policy.ClockSkew < 0 ||
		s.codec.policy.ClockSkew > 5*time.Minute || s.codec.policy.ClockSkew%time.Second != 0 {
		return TokenClaims{}, ErrSessionUnavailable
	}
	now := s.codec.now().UTC()
	if now.IsZero() || now.Unix() < 1 {
		return TokenClaims{}, ErrSessionUnavailable
	}
	codec := *s.codec
	codec.now = func() time.Time { return now }
	claims, err := codec.Verify(token)
	if err != nil {
		return TokenClaims{}, ErrInvalidSession
	}
	return claims, nil
}

func utcTimestamp(value time.Time) string {
	return value.UTC().Truncate(time.Microsecond).Format("2006-01-02 15:04:05.000000")
}
