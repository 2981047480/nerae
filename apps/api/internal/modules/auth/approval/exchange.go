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
	if e == nil || e.approvals == nil || e.codec == nil || e.replay == nil || e.now == nil || ctx == nil || db == nil ||
		len(input.ApprovalID) < 22 || len(input.ApprovalID) > 128 || len(input.Secret) != 43 || len(input.Proof) < 32 || len(input.Proof) > 4096 {
		return session.AccessToken{}, ErrUnavailable
	}
	secret, err := base64.RawURLEncoding.Strict().DecodeString(input.Secret)
	if err != nil || len(secret) != 32 {
		return session.AccessToken{}, ErrUnavailable
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return session.AccessToken{}, ErrUnavailable
	}
	defer tx.Rollback()
	var userID uint64
	var identityID, sourceSessionID []byte
	if err := tx.QueryRowContext(ctx, `SELECT user_id, identity_id, decision_session_id FROM auth_login_approval
        WHERE id = ? AND state = 'approved'`, []byte(input.ApprovalID)).Scan(&userID, &identityID, &sourceSessionID); err != nil ||
		userID == 0 || len(identityID) == 0 || len(sourceSessionID) == 0 {
		return session.AccessToken{}, ErrUnavailable
	}
	var sourceUser uint64
	var sourceIdentity, sourceDevice []byte
	if err := tx.QueryRowContext(ctx, `SELECT user_id, identity_id, device_id FROM auth_session WHERE id = ?`, sourceSessionID).
		Scan(&sourceUser, &sourceIdentity, &sourceDevice); err != nil || sourceUser != userID || !hmac.Equal(sourceIdentity, identityID) || len(sourceDevice) == 0 {
		return session.AccessToken{}, ErrUnavailable
	}
	// Match Authenticate's lock order: identity -> user -> source device ->
	// source session -> approval. Target device is locked only afterwards.
	var identityUser uint64
	var identityState string
	if err := tx.QueryRowContext(ctx, `SELECT user_id, state FROM login_identity WHERE id = ? FOR UPDATE`, identityID).
		Scan(&identityUser, &identityState); err != nil || identityUser != userID || identityState != "active" {
		return session.AccessToken{}, ErrUnavailable
	}
	var userState string
	var tokenVersion, identityVersion uint64
	if err := tx.QueryRowContext(ctx, `SELECT status, token_version, identity_version FROM user WHERE id = ? FOR UPDATE`, userID).
		Scan(&userState, &tokenVersion, &identityVersion); err != nil || userState != "active" || tokenVersion == 0 || identityVersion == 0 {
		return session.AccessToken{}, ErrUnavailable
	}
	var deviceUser uint64
	var deviceState string
	if err := tx.QueryRowContext(ctx, `SELECT user_id, state FROM trusted_device WHERE id = ? FOR UPDATE`, sourceDevice).
		Scan(&deviceUser, &deviceState); err != nil || deviceUser != userID || deviceState != "active" {
		return session.AccessToken{}, ErrUnavailable
	}
	var sessionUser, sessionVersion uint64
	var sessionIdentity, sessionDevice []byte
	var sessionState, sourceExpiryRaw string
	if err := tx.QueryRowContext(ctx, `SELECT user_id, identity_id, device_id, token_version, state,
        DATE_FORMAT(expires_at, '%Y-%m-%d %H:%i:%s.%f') FROM auth_session WHERE id = ? FOR UPDATE`, sourceSessionID).
		Scan(&sessionUser, &sessionIdentity, &sessionDevice, &sessionVersion, &sessionState, &sourceExpiryRaw); err != nil ||
		sessionUser != userID || !hmac.Equal(sessionIdentity, identityID) || !hmac.Equal(sessionDevice, sourceDevice) ||
		sessionVersion != tokenVersion || sessionState != "active" {
		return session.AccessToken{}, ErrUnavailable
	}
	if e.approvals.checkEpoch(ctx, tx) != nil {
		return session.AccessToken{}, ErrUnavailable
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
		return session.AccessToken{}, ErrUnavailable
	}
	const layout = "2006-01-02 15:04:05.000000"
	sourceExpiry, errSource := time.Parse(layout, sourceExpiryRaw)
	approvalExpiry, errApproval := time.Parse(layout, approvalExpiryRaw)
	now := e.now().UTC()
	if errSource != nil || errApproval != nil || now.IsZero() || !sourceExpiry.After(now) || !approvalExpiry.After(now) {
		return session.AccessToken{}, ErrUnavailable
	}
	public := domain.DevicePublicJWK{Kty: "EC", Crv: "P-256", X: x, Y: y}
	actualFingerprint, err := domain.DeviceKeyFingerprint(public)
	if err != nil || !hmac.Equal(fingerprint, actualFingerprint[:]) {
		return session.AccessToken{}, ErrUnavailable
	}
	expected := domain.ProofExpectation{Purpose: "approval_exchange", ResourceID: input.ApprovalID, Nonce: string(nonce)}
	verified, err := domain.VerifyDeviceProof(input.Proof, public, expected, now, e.policy)
	if err != nil {
		return session.AccessToken{}, ErrUnavailable
	}
	if err := e.replay.Consume(ctx, tx, actualFingerprint, verified, now); err != nil {
		return session.AccessToken{}, ErrUnavailable
	}
	var targetID []byte
	var targetUser uint64
	var targetState, targetX, targetY string
	err = tx.QueryRowContext(ctx, `SELECT id, user_id, state, public_x, public_y FROM trusted_device WHERE key_fingerprint = ? FOR UPDATE`, actualFingerprint[:]).
		Scan(&targetID, &targetUser, &targetState, &targetX, &targetY)
	if errors.Is(err, sql.ErrNoRows) {
		id, err := domain.GenerateChallengeID()
		if err != nil {
			return session.AccessToken{}, ErrUnavailable
		}
		targetID = []byte(id)
		if _, err := tx.ExecContext(ctx, `INSERT INTO trusted_device (id, user_id, key_fingerprint, public_x, public_y, state, created_at)
            VALUES (?, ?, ?, ?, ?, 'active', ?)`, targetID, userID, actualFingerprint[:], x, y, timestamp(now)); err != nil {
			return session.AccessToken{}, ErrUnavailable
		}
	} else if err != nil || targetUser != userID || targetState != "active" || targetX != x || targetY != y || len(targetID) == 0 {
		return session.AccessToken{}, ErrUnavailable
	}
	sessionID, err := domain.GenerateChallengeNonce()
	if err != nil {
		return session.AccessToken{}, ErrUnavailable
	}
	token, err := e.codec.Issue(strconv.FormatUint(userID, 10), sessionID, tokenVersion)
	if err != nil {
		return session.AccessToken{}, ErrUnavailable
	}
	claims, err := e.codec.Verify(token.ForResponse())
	if err != nil {
		return session.AccessToken{}, ErrUnavailable
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO auth_session
        (id, user_id, identity_id, device_id, login_challenge_id, token_version, state, authenticated_at, expires_at, created_at)
        VALUES (?, ?, ?, ?, ?, ?, 'active', ?, ?, ?)`, []byte(sessionID), userID, identityID, targetID, challengeID, tokenVersion,
		timestamp(claims.IssuedAt), timestamp(claims.ExpiresAt), timestamp(claims.IssuedAt)); err != nil {
		return session.AccessToken{}, ErrUnavailable
	}
	result, err := tx.ExecContext(ctx, `UPDATE auth_login_approval SET state = 'consumed', exchange_device_id = ?, exchange_session_id = ?, consumed_at = ?
        WHERE id = ? AND state = 'approved'`, targetID, []byte(sessionID), timestamp(now), []byte(input.ApprovalID))
	if err != nil {
		return session.AccessToken{}, ErrUnavailable
	}
	if count, err := result.RowsAffected(); err != nil || count != 1 {
		return session.AccessToken{}, ErrUnavailable
	}
	// Locks or storage may have taken time; never commit after either source
	// authorization, approval or device proof expires.
	finished := e.now().UTC()
	if finished.IsZero() || !sourceExpiry.After(finished) || !approvalExpiry.After(finished) {
		return session.AccessToken{}, ErrUnavailable
	}
	if _, err := domain.VerifyDeviceProof(input.Proof, public, expected, finished, e.policy); err != nil {
		return session.AccessToken{}, ErrUnavailable
	}
	if _, err := e.codec.Verify(token.ForResponse()); err != nil {
		return session.AccessToken{}, ErrUnavailable
	}
	if err := tx.Commit(); err != nil {
		return session.AccessToken{}, ErrUnavailable
	}
	return token, nil
}
