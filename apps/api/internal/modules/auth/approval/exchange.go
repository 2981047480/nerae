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

	"nerae/api/internal/modules/auth/approvalreceipt"
	"nerae/api/internal/modules/auth/domain"
	"nerae/api/internal/modules/auth/repository"
	"nerae/api/internal/modules/auth/session"
)

type ExchangeInput struct {
	ApprovalID string
	Secret     string
	Proof      string
}

func (ExchangeInput) String() string               { return "ExchangeInput{redacted}" }
func (ExchangeInput) GoString() string             { return "ExchangeInput{redacted}" }
func (ExchangeInput) MarshalJSON() ([]byte, error) { return nil, ErrUnavailable }
func (ExchangeInput) LogValue() slog.Value         { return slog.StringValue("ExchangeInput{redacted}") }

type Exchanger struct {
	approvals *Store
	codec     *session.TokenCodec
	replay    *repository.ProofReplayStore
	policy    domain.ProofPolicy
	now       func() time.Time
}

func (Exchanger) String() string               { return "ApprovalExchanger{redacted}" }
func (Exchanger) GoString() string             { return "ApprovalExchanger{redacted}" }
func (Exchanger) MarshalJSON() ([]byte, error) { return nil, ErrUnavailable }
func (Exchanger) LogValue() slog.Value         { return slog.StringValue("ApprovalExchanger{redacted}") }

func NewExchanger(approvals *Store, codec *session.TokenCodec, replay *repository.ProofReplayStore, policy domain.ProofPolicy) (*Exchanger, error) {
	if approvals == nil || codec == nil || replay == nil || !domain.ValidProofPolicy(policy) {
		return nil, ErrUnavailable
	}
	return &Exchanger{approvals: approvals, codec: codec, replay: replay, policy: policy, now: time.Now}, nil
}

// Exchange owns the entire database transaction and exposes a token only after
// a successful commit. Its ambiguous-commit response is NOT safely replayable;
// HTTP idempotency and rate limiting must be integrated before any public route.
func (e *Exchanger) Exchange(ctx context.Context, db *sql.DB, input ExchangeInput) (session.AccessToken, error) {
	if e == nil || ctx == nil || db == nil {
		return session.AccessToken{}, ErrUnavailable
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return session.AccessToken{}, ErrUnavailable
	}
	defer tx.Rollback()
	candidate, err := e.exchangeInTransaction(ctx, tx, input)
	if err != nil {
		return session.AccessToken{}, err
	}
	if err := candidate.verifyForTransaction(ctx, tx); err != nil {
		return session.AccessToken{}, err
	}
	if err := tx.Commit(); err != nil {
		return session.AccessToken{}, ErrUnavailable
	}
	return candidate.token, nil
}

// exchangeCandidate is private to this package and never serializes a token.
// It is only a transaction-bound staging value, not a replay authorization.
// The enclosing coordinator must commit and perform fresh authorization before
// exposing a credential; Exchange above remains the only current release path.
type exchangeCandidate struct {
	issuer         *Exchanger
	sourceTx       *sql.Tx
	token          session.AccessToken
	input          ExchangeInput
	public         domain.DevicePublicJWK
	expected       domain.ProofExpectation
	fingerprint    [32]byte
	sourceExpiry   time.Time
	approvalExpiry time.Time
}

func (exchangeCandidate) String() string               { return "ExchangeCandidate{redacted}" }
func (exchangeCandidate) GoString() string             { return "ExchangeCandidate{redacted}" }
func (exchangeCandidate) MarshalJSON() ([]byte, error) { return nil, ErrUnavailable }
func (exchangeCandidate) LogValue() slog.Value {
	return slog.StringValue("ExchangeCandidate{redacted}")
}

// verifyForTransaction rechecks time-sensitive authority after operations
// such as receipt writes. The original transaction retains the locked source
// authorization and approval rows until commit. It does not authorize replay.
func (c exchangeCandidate) verifyForTransaction(ctx context.Context, tx *sql.Tx) error {
	if c.issuer == nil || c.issuer.now == nil || c.issuer.codec == nil || c.issuer.replay == nil ||
		ctx == nil || tx == nil || c.sourceTx != tx || c.token.ForResponse() == "" {
		return ErrUnavailable
	}
	check := func() (domain.VerifiedDeviceProof, error) {
		now := c.issuer.now().UTC()
		if now.IsZero() || !c.sourceExpiry.After(now) || !c.approvalExpiry.After(now) {
			return domain.VerifiedDeviceProof{}, ErrUnavailable
		}
		proof, err := domain.VerifyDeviceProof(c.input.Proof, c.public, c.expected, now, c.issuer.policy)
		if err != nil {
			return domain.VerifiedDeviceProof{}, ErrUnavailable
		}
		if _, err := c.issuer.codec.Verify(c.token.ForResponse()); err != nil {
			return domain.VerifiedDeviceProof{}, ErrUnavailable
		}
		return proof, nil
	}
	proof, err := check()
	if err != nil {
		return ErrUnavailable
	}
	if err := c.issuer.replay.EnsureConsumed(ctx, tx, c.fingerprint, proof, c.issuer.now().UTC()); err != nil {
		return ErrUnavailable
	}
	if _, err := check(); err != nil {
		return ErrUnavailable
	}
	return nil
}

// sealedExchangeReceipt carries no bearer credential. The binding comes from
// the exact request used to create the candidate, never from a caller-built
// context which could name a different approval or device proof.
type sealedExchangeReceipt struct {
	binding   approvalreceipt.Binding
	subjectID string
	reference string
	expiresAt time.Time
	sealed    approvalreceipt.Sealed
}

func (sealedExchangeReceipt) String() string               { return "SealedExchangeReceipt{redacted}" }
func (sealedExchangeReceipt) GoString() string             { return "SealedExchangeReceipt{redacted}" }
func (sealedExchangeReceipt) MarshalJSON() ([]byte, error) { return nil, ErrUnavailable }
func (sealedExchangeReceipt) LogValue() slog.Value {
	return slog.StringValue("SealedExchangeReceipt{redacted}")
}

// sealForReceipt derives the binding and encryption context from the original
// exchange input and issued session. The coordinator must persist the result
// atomically, recheck this candidate after the write, and reauthorize replay.
func (c exchangeCandidate) sealForReceipt(ctx context.Context, tx *sql.Tx, crypto *approvalreceipt.Crypto,
	keyID, idempotencyKey string, expires time.Time, verifier approvalreceipt.CurrentSessionVerifier) (sealedExchangeReceipt, error) {
	if crypto == nil || c.verifyForTransaction(ctx, tx) != nil {
		return sealedExchangeReceipt{}, ErrUnavailable
	}
	binding, err := crypto.Summarize(approvalreceipt.Request{IdempotencyKey: idempotencyKey,
		ApprovalID: c.input.ApprovalID, ApprovalSecret: c.input.Secret, DeviceProof: c.input.Proof})
	if err != nil || c.expected.ResourceID != c.input.ApprovalID {
		return sealedExchangeReceipt{}, ErrUnavailable
	}
	claims, err := c.issuer.codec.Verify(c.token.ForResponse())
	if err != nil {
		return sealedExchangeReceipt{}, ErrUnavailable
	}
	// A receipt must never outlive the session it can recover. Short token
	// policies remain usable even when the configured retention is longer.
	if expires.After(claims.ExpiresAt) {
		expires = claims.ExpiresAt.UTC().Truncate(time.Microsecond)
	}
	subject, err := crypto.SubjectScope(claims.SubjectID)
	if err != nil {
		return sealedExchangeReceipt{}, ErrUnavailable
	}
	approval, err := crypto.ApprovalScope(c.input.ApprovalID)
	if err != nil {
		return sealedExchangeReceipt{}, ErrUnavailable
	}
	receiptContext, err := crypto.NewContext(keyID, subject, approval, binding, "issued_session", claims.SessionID, expires)
	if err != nil {
		return sealedExchangeReceipt{}, ErrUnavailable
	}
	now, err := approvalreceipt.ReadDatabaseUTC(ctx, tx)
	if err != nil {
		return sealedExchangeReceipt{}, ErrUnavailable
	}
	sealed, err := crypto.Seal(ctx, receiptContext, c.token.ForResponse(), now, tx, verifier)
	if err != nil || c.verifyForTransaction(ctx, tx) != nil {
		return sealedExchangeReceipt{}, ErrUnavailable
	}
	return sealedExchangeReceipt{binding: binding, subjectID: claims.SubjectID, reference: claims.SessionID, expiresAt: expires, sealed: sealed}, nil
}

// exchangeInTransaction is a package-private seam for a future receipt
// coordinator. It returns an unexported, non-serializable candidate rather than
// a transport token. The caller must roll back on any error and must recheck
// it after receipt operations before committing.
func (e *Exchanger) exchangeInTransaction(ctx context.Context, tx *sql.Tx, input ExchangeInput) (exchangeCandidate, error) {
	if e == nil || e.approvals == nil || e.codec == nil || e.replay == nil || e.now == nil || ctx == nil || tx == nil ||
		len(input.ApprovalID) < 22 || len(input.ApprovalID) > 128 || len(input.Secret) != 43 || len(input.Proof) < 32 || len(input.Proof) > 4096 {
		return exchangeCandidate{}, ErrUnavailable
	}
	secret, err := base64.RawURLEncoding.Strict().DecodeString(input.Secret)
	if err != nil || len(secret) != 32 {
		return exchangeCandidate{}, ErrUnavailable
	}
	var userID uint64
	var identityID, sourceSessionID []byte
	if err := tx.QueryRowContext(ctx, `SELECT user_id, identity_id, decision_session_id FROM auth_login_approval
        WHERE id = ? AND state = 'approved'`, []byte(input.ApprovalID)).Scan(&userID, &identityID, &sourceSessionID); err != nil ||
		userID == 0 || len(identityID) == 0 || len(sourceSessionID) == 0 {
		return exchangeCandidate{}, ErrUnavailable
	}
	var sourceUser uint64
	var sourceIdentity, sourceDevice []byte
	if err := tx.QueryRowContext(ctx, `SELECT user_id, identity_id, device_id FROM auth_session WHERE id = ?`, sourceSessionID).
		Scan(&sourceUser, &sourceIdentity, &sourceDevice); err != nil || sourceUser != userID || !hmac.Equal(sourceIdentity, identityID) || len(sourceDevice) == 0 {
		return exchangeCandidate{}, ErrUnavailable
	}
	// Match Authenticate's lock order: identity -> user -> source device ->
	// source session -> approval. Target device is locked only afterwards.
	var identityUser uint64
	var identityState string
	if err := tx.QueryRowContext(ctx, `SELECT user_id, state FROM login_identity WHERE id = ? FOR UPDATE`, identityID).
		Scan(&identityUser, &identityState); err != nil || identityUser != userID || identityState != "active" {
		return exchangeCandidate{}, ErrUnavailable
	}
	var userState string
	var tokenVersion, identityVersion uint64
	if err := tx.QueryRowContext(ctx, `SELECT status, token_version, identity_version FROM user WHERE id = ? FOR UPDATE`, userID).
		Scan(&userState, &tokenVersion, &identityVersion); err != nil || userState != "active" || tokenVersion == 0 || identityVersion == 0 {
		return exchangeCandidate{}, ErrUnavailable
	}
	var deviceUser uint64
	var deviceState string
	if err := tx.QueryRowContext(ctx, `SELECT user_id, state FROM trusted_device WHERE id = ? FOR UPDATE`, sourceDevice).
		Scan(&deviceUser, &deviceState); err != nil || deviceUser != userID || deviceState != "active" {
		return exchangeCandidate{}, ErrUnavailable
	}
	var sessionUser, sessionVersion uint64
	var sessionIdentity, sessionDevice []byte
	var sessionState, sourceExpiryRaw string
	if err := tx.QueryRowContext(ctx, `SELECT user_id, identity_id, device_id, token_version, state,
        DATE_FORMAT(expires_at, '%Y-%m-%d %H:%i:%s.%f') FROM auth_session WHERE id = ? FOR UPDATE`, sourceSessionID).
		Scan(&sessionUser, &sessionIdentity, &sessionDevice, &sessionVersion, &sessionState, &sourceExpiryRaw); err != nil ||
		sessionUser != userID || !hmac.Equal(sessionIdentity, identityID) || !hmac.Equal(sessionDevice, sourceDevice) ||
		sessionVersion != tokenVersion || sessionState != "active" {
		return exchangeCandidate{}, ErrUnavailable
	}
	if e.approvals.checkEpoch(ctx, tx) != nil {
		return exchangeCandidate{}, ErrUnavailable
	}
	var rowUser, rowTokenVersion, rowIdentityVersion uint64
	var rowIdentity, challengeID, fingerprint, secretDigest, keyID, nonce, rowSourceSession []byte
	var x, y, state, approvalExpiryRaw string
	if err := tx.QueryRowContext(ctx, `SELECT user_id, identity_id, challenge_id, token_version, identity_version, target_fingerprint,
        public_x, public_y, secret_digest, key_id, nonce, state, decision_session_id,
        DATE_FORMAT(expires_at, '%Y-%m-%d %H:%i:%s.%f') FROM auth_login_approval WHERE id = ? FOR UPDATE`, []byte(input.ApprovalID)).
		Scan(&rowUser, &rowIdentity, &challengeID, &rowTokenVersion, &rowIdentityVersion, &fingerprint, &x, &y,
			&secretDigest, &keyID, &nonce, &state, &rowSourceSession, &approvalExpiryRaw); err != nil ||
		rowUser != userID || !hmac.Equal(rowIdentity, identityID) || !hmac.Equal(rowSourceSession, sourceSessionID) ||
		rowTokenVersion != tokenVersion || rowIdentityVersion != identityVersion || state != "approved" || len(challengeID) == 0 ||
		string(keyID) != e.approvals.keyID || !hmac.Equal(secretDigest, e.approvals.digest(input.ApprovalID, input.Secret)) {
		return exchangeCandidate{}, ErrUnavailable
	}
	const layout = "2006-01-02 15:04:05.000000"
	sourceExpiry, errSource := time.Parse(layout, sourceExpiryRaw)
	approvalExpiry, errApproval := time.Parse(layout, approvalExpiryRaw)
	now := e.now().UTC()
	if errSource != nil || errApproval != nil || now.IsZero() || !sourceExpiry.After(now) || !approvalExpiry.After(now) {
		return exchangeCandidate{}, ErrUnavailable
	}
	public := domain.DevicePublicJWK{Kty: "EC", Crv: "P-256", X: x, Y: y}
	actualFingerprint, err := domain.DeviceKeyFingerprint(public)
	if err != nil || !hmac.Equal(fingerprint, actualFingerprint[:]) {
		return exchangeCandidate{}, ErrUnavailable
	}
	expected := domain.ProofExpectation{Purpose: "approval_exchange", ResourceID: input.ApprovalID, Nonce: string(nonce)}
	verified, err := domain.VerifyDeviceProof(input.Proof, public, expected, now, e.policy)
	if err != nil {
		return exchangeCandidate{}, ErrUnavailable
	}
	if err := e.replay.Consume(ctx, tx, actualFingerprint, verified, now); err != nil {
		return exchangeCandidate{}, ErrUnavailable
	}
	var targetID []byte
	var targetUser uint64
	var targetState, targetX, targetY string
	err = tx.QueryRowContext(ctx, `SELECT id, user_id, state, public_x, public_y FROM trusted_device WHERE key_fingerprint = ? FOR UPDATE`, actualFingerprint[:]).
		Scan(&targetID, &targetUser, &targetState, &targetX, &targetY)
	if errors.Is(err, sql.ErrNoRows) {
		id, err := domain.GenerateChallengeID()
		if err != nil {
			return exchangeCandidate{}, ErrUnavailable
		}
		targetID = []byte(id)
		if _, err := tx.ExecContext(ctx, `INSERT INTO trusted_device (id, user_id, key_fingerprint, public_x, public_y, state, created_at)
            VALUES (?, ?, ?, ?, ?, 'active', ?)`, targetID, userID, actualFingerprint[:], x, y, timestamp(now)); err != nil {
			return exchangeCandidate{}, ErrUnavailable
		}
	} else if err != nil || targetUser != userID || targetState != "active" || targetX != x || targetY != y || len(targetID) == 0 {
		return exchangeCandidate{}, ErrUnavailable
	}
	sessionID, err := domain.GenerateChallengeNonce()
	if err != nil {
		return exchangeCandidate{}, ErrUnavailable
	}
	token, err := e.codec.Issue(strconv.FormatUint(userID, 10), sessionID, tokenVersion)
	if err != nil {
		return exchangeCandidate{}, ErrUnavailable
	}
	claims, err := e.codec.Verify(token.ForResponse())
	if err != nil {
		return exchangeCandidate{}, ErrUnavailable
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO auth_session
        (id, user_id, identity_id, device_id, login_challenge_id, token_version, state, authenticated_at, expires_at, created_at)
        VALUES (?, ?, ?, ?, ?, ?, 'active', ?, ?, ?)`, []byte(sessionID), userID, identityID, targetID, challengeID, tokenVersion,
		timestamp(claims.IssuedAt), timestamp(claims.ExpiresAt), timestamp(claims.IssuedAt)); err != nil {
		return exchangeCandidate{}, ErrUnavailable
	}
	result, err := tx.ExecContext(ctx, `UPDATE auth_login_approval SET state = 'consumed', exchange_device_id = ?, exchange_session_id = ?, consumed_at = ?
        WHERE id = ? AND state = 'approved'`, targetID, []byte(sessionID), timestamp(now), []byte(input.ApprovalID))
	if err != nil {
		return exchangeCandidate{}, ErrUnavailable
	}
	if count, err := result.RowsAffected(); err != nil || count != 1 {
		return exchangeCandidate{}, ErrUnavailable
	}
	// Locks or storage may have taken time; never commit after either source
	// authorization, approval or device proof expires.
	finished := e.now().UTC()
	if finished.IsZero() || !sourceExpiry.After(finished) || !approvalExpiry.After(finished) {
		return exchangeCandidate{}, ErrUnavailable
	}
	if _, err := domain.VerifyDeviceProof(input.Proof, public, expected, finished, e.policy); err != nil {
		return exchangeCandidate{}, ErrUnavailable
	}
	if _, err := e.codec.Verify(token.ForResponse()); err != nil {
		return exchangeCandidate{}, ErrUnavailable
	}
	return exchangeCandidate{issuer: e, sourceTx: tx, token: token, input: input,
		public: public, expected: expected, fingerprint: actualFingerprint,
		sourceExpiry: sourceExpiry, approvalExpiry: approvalExpiry}, nil
}
