package approval

import (
	"context"
	"crypto/hmac"
	"database/sql"
	"encoding/base64"
	"errors"
	"log/slog"
	"strconv"
	"time"

	"nerae/api/internal/modules/auth/approvalledger"
	"nerae/api/internal/modules/auth/approvalreceipt"
	"nerae/api/internal/modules/auth/domain"
	"nerae/api/internal/modules/auth/session"
)

var ErrExchangeReceiptConflict = errors.New("approval exchange receipt conflicts")

// ReceiptExchanger is an internal transaction coordinator, not an HTTP route
// or rate limiter. It never releases a staged credential before commit.
type ReceiptExchanger struct {
	exchanger *Exchanger
	ledger    *approvalledger.Store
	crypto    *approvalreceipt.Crypto
	sessions  *session.Store
	keyID     string
	retention time.Duration
}

func (ReceiptExchanger) String() string               { return "ReceiptExchanger{redacted}" }
func (ReceiptExchanger) GoString() string             { return "ReceiptExchanger{redacted}" }
func (ReceiptExchanger) MarshalJSON() ([]byte, error) { return nil, ErrUnavailable }
func (ReceiptExchanger) LogValue() slog.Value         { return slog.StringValue("ReceiptExchanger{redacted}") }

func NewReceiptExchanger(exchanger *Exchanger, ledger *approvalledger.Store, crypto *approvalreceipt.Crypto,
	sessions *session.Store, keyID string, retention time.Duration) (*ReceiptExchanger, error) {
	if exchanger == nil || ledger == nil || crypto == nil || sessions == nil ||
		retention < time.Second || retention > 24*time.Hour || retention%time.Microsecond != 0 {
		return nil, ErrUnavailable
	}
	if _, err := crypto.EpochTag(keyID); err != nil {
		return nil, ErrUnavailable
	}
	return &ReceiptExchanger{exchanger: exchanger, ledger: ledger, crypto: crypto, sessions: sessions, keyID: keyID, retention: retention}, nil
}

// Initialize checks the exchange receipt epoch inside the caller's setup
// transaction. It must not be used as an implicit key rotation mechanism.
func (c *ReceiptExchanger) Initialize(ctx context.Context, tx *sql.Tx) error {
	if c == nil || c.ledger == nil || ctx == nil || tx == nil || c.ledger.Initialize(ctx, tx) != nil {
		return ErrUnavailable
	}
	return nil
}

// exchange either atomically creates a device session and encrypted receipt or
// replays the exact original request. On first success it re-reads the receipt
// in a fresh transaction before releasing anything. Any commit uncertainty
// returns no credential: the caller can retry the identical request and key.
func (c *ReceiptExchanger) exchange(ctx context.Context, db *sql.DB, key string, input ExchangeInput) (string, error) {
	if !c.valid() || ctx == nil || db == nil {
		return "", ErrUnavailable
	}
	binding, err := c.crypto.Summarize(approvalreceipt.Request{IdempotencyKey: key, ApprovalID: input.ApprovalID,
		ApprovalSecret: input.Secret, DeviceProof: input.Proof})
	if err != nil {
		return "", ErrUnavailable
	}
	// A concurrent request can establish a snapshot before the first exchange
	// commits, then wait on the same user/approval locks. Under MySQL's default
	// repeatable-read isolation, later non-locking session lookups can remain on
	// that old snapshot even though locking reads see the committed receipt.
	// Read committed keeps those reads current; the authorization rows and
	// receipt themselves are still explicitly locked for the transaction.
	tx, err := db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return "", ErrUnavailable
	}
	defer tx.Rollback()
	authority, err := c.lockAuthority(ctx, tx, input)
	if err != nil {
		return "", ErrUnavailable
	}
	handle, err := c.ledger.Lock(ctx, tx, strconv.FormatUint(authority.userID, 10), binding)
	if errors.Is(err, approvalledger.ErrConflict) {
		return "", ErrExchangeReceiptConflict
	}
	if err != nil {
		return "", ErrUnavailable
	}
	if handle.Exists() {
		token, expiry, err := c.openLocked(ctx, tx, input, authority, binding, handle)
		if err != nil {
			return "", ErrUnavailable
		}
		if err := tx.Commit(); err != nil {
			return "", ErrUnavailable
		}
		if c.finalTimeCheck(input, authority, token, expiry) != nil {
			return "", ErrUnavailable
		}
		return token, nil
	}
	if authority.state != "approved" {
		return "", ErrUnavailable
	}
	candidate, err := c.exchanger.exchangeInTransaction(ctx, tx, input)
	if err != nil {
		return "", ErrUnavailable
	}
	now := c.exchanger.now().UTC()
	if now.IsZero() {
		return "", ErrUnavailable
	}
	expires := now.Add(c.retention).Truncate(time.Microsecond)
	sealed, err := candidate.sealForReceipt(ctx, tx, c.crypto, c.keyID, key, expires, c.sessions)
	if err != nil || sealed.binding != binding || sealed.subjectID != strconv.FormatUint(authority.userID, 10) ||
		handle.Put(ctx, tx, sealed.reference, sealed.expiresAt, sealed.sealed, c.sessions) != nil ||
		candidate.verifyForTransaction(ctx, tx) != nil {
		return "", ErrUnavailable
	}
	// Put and the last proof check can wait; lock and check the stored receipt
	// again before commit, then recheck the proof after that final lock.
	finalHandle, err := c.ledger.Lock(ctx, tx, sealed.subjectID, binding)
	if err != nil || !finalHandle.Exists() || candidate.verifyForTransaction(ctx, tx) != nil {
		return "", ErrUnavailable
	}
	if err := tx.Commit(); err != nil {
		return "", ErrUnavailable
	}
	return c.recoverCommitted(ctx, db, key, input, binding)
}

func (c *ReceiptExchanger) recoverCommitted(ctx context.Context, db *sql.DB, key string, input ExchangeInput, binding approvalreceipt.Binding) (string, error) {
	// Re-derive the binding in the fresh transaction path; callers cannot
	// substitute a digest unrelated to the original request.
	want, err := c.crypto.Summarize(approvalreceipt.Request{IdempotencyKey: key, ApprovalID: input.ApprovalID,
		ApprovalSecret: input.Secret, DeviceProof: input.Proof})
	if err != nil || want != binding {
		return "", ErrUnavailable
	}
	tx, err := db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return "", ErrUnavailable
	}
	defer tx.Rollback()
	authority, err := c.lockAuthority(ctx, tx, input)
	if err != nil {
		return "", ErrUnavailable
	}
	handle, err := c.ledger.Lock(ctx, tx, strconv.FormatUint(authority.userID, 10), binding)
	if err != nil || !handle.Exists() {
		return "", ErrUnavailable
	}
	token, expiry, err := c.openLocked(ctx, tx, input, authority, binding, handle)
	if err != nil || tx.Commit() != nil {
		return "", ErrUnavailable
	}
	if c.finalTimeCheck(input, authority, token, expiry) != nil {
		return "", ErrUnavailable
	}
	return token, nil
}

func (c *ReceiptExchanger) valid() bool {
	return c != nil && c.exchanger != nil && c.exchanger.approvals != nil && c.exchanger.replay != nil &&
		c.exchanger.codec != nil && c.exchanger.now != nil && c.ledger != nil && c.crypto != nil && c.sessions != nil &&
		c.retention >= time.Second && c.retention <= 24*time.Hour && c.retention%time.Microsecond == 0
}

type exchangeAuthority struct {
	userID         uint64
	identityID     []byte
	challengeID    []byte
	sourceSession  []byte
	targetDevice   []byte
	targetSession  []byte
	fingerprint    [32]byte
	public         domain.DevicePublicJWK
	expected       domain.ProofExpectation
	sourceExpiry   time.Time
	approvalExpiry time.Time
	state          string
}

func (a exchangeAuthority) timeAndProof(e *Exchanger, input ExchangeInput) (domain.VerifiedDeviceProof, error) {
	if e == nil || e.now == nil {
		return domain.VerifiedDeviceProof{}, ErrUnavailable
	}
	now := e.now().UTC()
	if now.IsZero() || !a.sourceExpiry.After(now) || !a.approvalExpiry.After(now) {
		return domain.VerifiedDeviceProof{}, ErrUnavailable
	}
	proof, err := domain.VerifyDeviceProof(input.Proof, a.public, a.expected, now, e.policy)
	if err != nil {
		return domain.VerifiedDeviceProof{}, ErrUnavailable
	}
	return proof, nil
}

// lockAuthority derives the subject from the stored approval, validates its
// secret and device proof, and locks the source authority in the same order as
// Exchanger and session authentication. No anonymous subject is trusted.
func (c *ReceiptExchanger) lockAuthority(ctx context.Context, tx *sql.Tx, input ExchangeInput) (exchangeAuthority, error) {
	invalid := func() (exchangeAuthority, error) { return exchangeAuthority{}, ErrUnavailable }
	if !c.valid() || ctx == nil || tx == nil || len(input.ApprovalID) < 22 || len(input.ApprovalID) > 128 ||
		len(input.Secret) != 43 || len(input.Proof) < 32 || len(input.Proof) > 4096 {
		return invalid()
	}
	secret, err := base64.RawURLEncoding.Strict().DecodeString(input.Secret)
	if err != nil || len(secret) != 32 {
		return invalid()
	}
	var a exchangeAuthority
	if err := tx.QueryRowContext(ctx, `SELECT user_id,identity_id,decision_session_id FROM auth_login_approval WHERE id=?`, []byte(input.ApprovalID)).
		Scan(&a.userID, &a.identityID, &a.sourceSession); err != nil || a.userID == 0 || len(a.identityID) == 0 || len(a.sourceSession) == 0 {
		return invalid()
	}
	var initialUser uint64
	var initialIdentity, sourceDevice []byte
	if err := tx.QueryRowContext(ctx, `SELECT user_id,identity_id,device_id FROM auth_session WHERE id=?`, a.sourceSession).
		Scan(&initialUser, &initialIdentity, &sourceDevice); err != nil || initialUser != a.userID ||
		!hmac.Equal(initialIdentity, a.identityID) || len(sourceDevice) == 0 {
		return invalid()
	}
	var identityOwner uint64
	var identityState string
	if err := tx.QueryRowContext(ctx, `SELECT user_id,state FROM login_identity WHERE id=? FOR UPDATE`, a.identityID).
		Scan(&identityOwner, &identityState); err != nil || identityOwner != a.userID || identityState != "active" {
		return invalid()
	}
	var userState string
	var tokenVersion, identityVersion uint64
	if err := tx.QueryRowContext(ctx, `SELECT status,token_version,identity_version FROM user WHERE id=? FOR UPDATE`, a.userID).
		Scan(&userState, &tokenVersion, &identityVersion); err != nil || userState != "active" || tokenVersion == 0 || identityVersion == 0 {
		return invalid()
	}
	var deviceUser uint64
	var deviceState string
	if err := tx.QueryRowContext(ctx, `SELECT user_id,state FROM trusted_device WHERE id=? FOR UPDATE`, sourceDevice).
		Scan(&deviceUser, &deviceState); err != nil || deviceUser != a.userID || deviceState != "active" {
		return invalid()
	}
	var sessionUser, sessionVersion uint64
	var sessionIdentity, sessionDevice []byte
	var sessionState, sourceExpiryRaw string
	if err := tx.QueryRowContext(ctx, `SELECT user_id,identity_id,device_id,token_version,state,
		DATE_FORMAT(expires_at,'%Y-%m-%d %H:%i:%s.%f') FROM auth_session WHERE id=? FOR UPDATE`, a.sourceSession).
		Scan(&sessionUser, &sessionIdentity, &sessionDevice, &sessionVersion, &sessionState, &sourceExpiryRaw); err != nil ||
		sessionUser != a.userID || !hmac.Equal(sessionIdentity, a.identityID) || !hmac.Equal(sessionDevice, sourceDevice) ||
		sessionVersion != tokenVersion || sessionState != "active" || c.exchanger.approvals.checkEpoch(ctx, tx) != nil {
		return invalid()
	}
	var rowUser, rowTokenVersion, rowIdentityVersion uint64
	var rowIdentity, fingerprint, digest, keyID, nonce, rowSource []byte
	var x, y, approvalExpiryRaw string
	if err := tx.QueryRowContext(ctx, `SELECT user_id,identity_id,challenge_id,token_version,identity_version,target_fingerprint,
		public_x,public_y,secret_digest,key_id,nonce,state,decision_session_id,exchange_device_id,exchange_session_id,
		DATE_FORMAT(expires_at,'%Y-%m-%d %H:%i:%s.%f') FROM auth_login_approval WHERE id=? FOR UPDATE`, []byte(input.ApprovalID)).
		Scan(&rowUser, &rowIdentity, &a.challengeID, &rowTokenVersion, &rowIdentityVersion, &fingerprint, &x, &y,
			&digest, &keyID, &nonce, &a.state, &rowSource, &a.targetDevice, &a.targetSession, &approvalExpiryRaw); err != nil ||
		rowUser != a.userID || !hmac.Equal(rowIdentity, a.identityID) || !hmac.Equal(rowSource, a.sourceSession) ||
		rowTokenVersion != tokenVersion || rowIdentityVersion != identityVersion || len(a.challengeID) == 0 ||
		(a.state != "approved" && a.state != "consumed") || string(keyID) != c.exchanger.approvals.keyID ||
		!hmac.Equal(digest, c.exchanger.approvals.digest(input.ApprovalID, input.Secret)) {
		return invalid()
	}
	if (a.state == "approved" && (len(a.targetDevice) != 0 || len(a.targetSession) != 0)) ||
		(a.state == "consumed" && (len(a.targetDevice) == 0 || len(a.targetSession) == 0)) {
		return invalid()
	}
	const layout = "2006-01-02 15:04:05.000000"
	a.sourceExpiry, err = time.Parse(layout, sourceExpiryRaw)
	if err != nil {
		return invalid()
	}
	a.approvalExpiry, err = time.Parse(layout, approvalExpiryRaw)
	if err != nil {
		return invalid()
	}
	a.public = domain.DevicePublicJWK{Kty: "EC", Crv: "P-256", X: x, Y: y}
	a.fingerprint, err = domain.DeviceKeyFingerprint(a.public)
	if err != nil || !hmac.Equal(fingerprint, a.fingerprint[:]) {
		return invalid()
	}
	a.expected = domain.ProofExpectation{Purpose: "approval_exchange", ResourceID: input.ApprovalID, Nonce: string(nonce)}
	if _, err := a.timeAndProof(c.exchanger, input); err != nil {
		return invalid()
	}
	return a, nil
}

func (c *ReceiptExchanger) openLocked(ctx context.Context, tx *sql.Tx, input ExchangeInput, a exchangeAuthority,
	binding approvalreceipt.Binding, handle *approvalledger.Handle) (string, time.Time, error) {
	if a.state != "consumed" || handle == nil || !handle.Exists() {
		return "", time.Time{}, ErrUnavailable
	}
	proof, err := a.timeAndProof(c.exchanger, input)
	if err != nil || c.exchanger.replay.EnsureConsumed(ctx, tx, a.fingerprint, proof, c.exchanger.now().UTC()) != nil {
		return "", time.Time{}, ErrUnavailable
	}
	plaintext, err := handle.Open(ctx, tx, c.sessions)
	if err != nil {
		return "", time.Time{}, ErrUnavailable
	}
	token := string(plaintext.Bytes())
	claims, err := c.exchanger.codec.Verify(token)
	if err != nil || claims.SubjectID != strconv.FormatUint(a.userID, 10) || !hmac.Equal([]byte(claims.SessionID), a.targetSession) {
		return "", time.Time{}, ErrUnavailable
	}
	var userID uint64
	var identityID, deviceID, challengeID, deviceFingerprint []byte
	if err := tx.QueryRowContext(ctx, `SELECT user_id,identity_id,device_id,login_challenge_id FROM auth_session WHERE id=? FOR UPDATE`, a.targetSession).
		Scan(&userID, &identityID, &deviceID, &challengeID); err != nil || userID != a.userID ||
		!hmac.Equal(identityID, a.identityID) || !hmac.Equal(deviceID, a.targetDevice) || !hmac.Equal(challengeID, a.challengeID) {
		return "", time.Time{}, ErrUnavailable
	}
	if err := tx.QueryRowContext(ctx, `SELECT key_fingerprint FROM trusted_device WHERE id=? FOR UPDATE`, a.targetDevice).
		Scan(&deviceFingerprint); err != nil || !hmac.Equal(deviceFingerprint, a.fingerprint[:]) {
		return "", time.Time{}, ErrUnavailable
	}
	refreshed, err := c.ledger.Lock(ctx, tx, strconv.FormatUint(a.userID, 10), binding)
	if err != nil || !refreshed.Exists() {
		return "", time.Time{}, ErrUnavailable
	}
	expires, ok := refreshed.ExpiresAt()
	if !ok {
		return "", time.Time{}, ErrUnavailable
	}
	proof, err = a.timeAndProof(c.exchanger, input)
	if err != nil || c.exchanger.replay.EnsureConsumed(ctx, tx, a.fingerprint, proof, c.exchanger.now().UTC()) != nil ||
		c.finalTimeCheck(input, a, token, expires) != nil {
		return "", time.Time{}, ErrUnavailable
	}
	return token, expires, nil
}

func (c *ReceiptExchanger) finalTimeCheck(input ExchangeInput, a exchangeAuthority, token string, expires time.Time) error {
	if _, err := a.timeAndProof(c.exchanger, input); err != nil {
		return ErrUnavailable
	}
	now := c.exchanger.now().UTC()
	if now.IsZero() || !expires.After(now) {
		return ErrUnavailable
	}
	if _, err := c.exchanger.codec.Verify(token); err != nil {
		return ErrUnavailable
	}
	return nil
}
