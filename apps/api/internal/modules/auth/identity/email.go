// Package identity manages verified subjects and devices, not sessions.
package identity

import (
	"context"
	"crypto/hmac"
	"database/sql"
	"errors"
	"log/slog"
	"strconv"
	"time"

	"nerae/api/internal/modules/auth/challenge"
	"nerae/api/internal/modules/auth/domain"
)

var ErrIdentityUnavailable = errors.New("identity unavailable")

type Resolver struct {
	protector *domain.EmailIdentityProtector
	now       func() time.Time
}

func NewResolver(protector *domain.EmailIdentityProtector) (*Resolver, error) {
	if protector == nil {
		return nil, ErrIdentityUnavailable
	}
	return &Resolver{protector: protector, now: time.Now}, nil
}

type State string

const (
	Created          State = "created"
	Trusted          State = "trusted"
	ApprovalRequired State = "approval_required"
)

// Resolution is internal transaction state, not a session or public response.
// In particular, ApprovalRequired must not return a token or private data.
type Resolution struct {
	State     State
	SubjectID string
	DeviceID  string
}

func (Resolution) String() string               { return "Resolution{redacted}" }
func (Resolution) GoString() string             { return "Resolution{redacted}" }
func (Resolution) MarshalJSON() ([]byte, error) { return nil, ErrIdentityUnavailable }
func (Resolution) LogValue() slog.Value         { return slog.StringValue("Resolution{redacted}") }

// ResolveEmail accepts only an opaque proof from the same live transaction.
// The caller must roll back on errors. For a new subject it creates the first
// trusted device, but no session, role or age verification. For an existing
// subject on a new device it returns ApprovalRequired without trusting that key.
// Before committing, the caller must complete the appropriate approval or
// session/idempotency flow and recheck authorization in this transaction.
func (r *Resolver) ResolveEmail(ctx context.Context, tx *sql.Tx, proof *challenge.VerifiedEmail) (Resolution, error) {
	if r == nil || r.protector == nil || r.now == nil || ctx == nil || tx == nil || proof == nil {
		return Resolution{}, ErrIdentityUnavailable
	}
	details, err := proof.ForTransaction(ctx, tx)
	if err != nil {
		return Resolution{}, ErrIdentityUnavailable
	}
	keyID, epoch, err := r.protector.KeyEpoch()
	if err != nil || details.KeyID != keyID {
		return Resolution{}, ErrIdentityUnavailable
	}
	var storedEpoch []byte
	if err := tx.QueryRowContext(ctx, `SELECT key_tag FROM auth_email_identity_key_epoch WHERE slot = 1 FOR SHARE`).Scan(&storedEpoch); err != nil || !hmac.Equal(storedEpoch, epoch[:]) {
		return Resolution{}, ErrIdentityUnavailable
	}
	if _, err := r.protector.Open(details.Index, details.Ciphertext); err != nil {
		return Resolution{}, ErrIdentityUnavailable
	}
	fingerprint, err := domain.DeviceKeyFingerprint(details.PublicKey)
	if err != nil || !hmac.Equal(fingerprint[:], details.Fingerprint[:]) {
		return Resolution{}, ErrIdentityUnavailable
	}
	finish := func(result Resolution) (Resolution, error) {
		// Database work may have waited on locks; never return a resolution
		// after the original device proof or challenge expired.
		if _, err := proof.ForTransaction(ctx, tx); err != nil {
			return Resolution{}, ErrIdentityUnavailable
		}
		return result, nil
	}
	var userID uint64
	var subjectCiphertext, storedKeyID []byte
	err = tx.QueryRowContext(ctx, `SELECT user_id, subject_enc, key_id FROM login_identity
        WHERE kind = 'email' AND provider_scope = 'beta-email' AND subject_lookup_hmac = ? AND active_slot = 1 FOR UPDATE`, details.Index[:]).Scan(&userID, &subjectCiphertext, &storedKeyID)
	if errors.Is(err, sql.ErrNoRows) {
		created, err := r.create(ctx, tx, details)
		if err != nil {
			return Resolution{}, err
		}
		return finish(created)
	}
	if err != nil || userID == 0 {
		return Resolution{}, ErrIdentityUnavailable
	}
	if string(storedKeyID) != keyID {
		return Resolution{}, ErrIdentityUnavailable
	}
	if _, err := r.protector.Open(details.Index, subjectCiphertext); err != nil {
		return Resolution{}, ErrIdentityUnavailable
	}
	var status string
	if err := tx.QueryRowContext(ctx, `SELECT status FROM user WHERE id = ? FOR UPDATE`, userID).Scan(&status); err != nil || status != "active" {
		return Resolution{}, ErrIdentityUnavailable
	}
	var deviceID []byte
	var deviceUser uint64
	var state, x, y string
	err = tx.QueryRowContext(ctx, `SELECT id, user_id, state, public_x, public_y FROM trusted_device WHERE key_fingerprint = ? FOR UPDATE`, fingerprint[:]).
		Scan(&deviceID, &deviceUser, &state, &x, &y)
	if errors.Is(err, sql.ErrNoRows) {
		return finish(Resolution{State: ApprovalRequired, SubjectID: strconv.FormatUint(userID, 10)})
	}
	if err != nil || deviceUser != userID || state != "active" || len(deviceID) == 0 || x != details.PublicKey.X || y != details.PublicKey.Y {
		return Resolution{}, ErrIdentityUnavailable
	}
	return finish(Resolution{State: Trusted, SubjectID: strconv.FormatUint(userID, 10), DeviceID: string(deviceID)})
}

func (r *Resolver) create(ctx context.Context, tx *sql.Tx, details challenge.VerifiedEmailDetails) (Resolution, error) {
	// A key is unique across subjects. Reuse or a revoked key fails closed.
	var existing []byte
	err := tx.QueryRowContext(ctx, `SELECT id FROM trusted_device WHERE key_fingerprint = ? FOR UPDATE`, details.Fingerprint[:]).Scan(&existing)
	if err == nil || !errors.Is(err, sql.ErrNoRows) {
		return Resolution{}, ErrIdentityUnavailable
	}
	identityID, err := domain.GenerateChallengeID()
	if err != nil {
		return Resolution{}, ErrIdentityUnavailable
	}
	deviceID, err := domain.GenerateChallengeID()
	if err != nil {
		return Resolution{}, ErrIdentityUnavailable
	}
	now := r.now().UTC()
	if now.IsZero() {
		return Resolution{}, ErrIdentityUnavailable
	}
	timestamp := now.Truncate(time.Microsecond).Format("2006-01-02 15:04:05.000000")
	insert, err := tx.ExecContext(ctx, `INSERT INTO user (created_at, updated_at) VALUES (?, ?)`, timestamp, timestamp)
	if err != nil {
		return Resolution{}, ErrIdentityUnavailable
	}
	userID, err := insert.LastInsertId()
	if err != nil || userID <= 0 {
		return Resolution{}, ErrIdentityUnavailable
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO login_identity
        (id, user_id, kind, provider_scope, subject_enc, subject_lookup_hmac, key_id, state, verified_at, created_at, updated_at)
        VALUES (?, ?, 'email', 'beta-email', ?, ?, ?, 'active', ?, ?, ?)`, identityID, userID,
		details.Ciphertext, details.Index[:], details.KeyID, timestamp, timestamp, timestamp)
	if err != nil {
		return Resolution{}, ErrIdentityUnavailable
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO trusted_device
        (id, user_id, key_fingerprint, public_x, public_y, state, created_at)
        VALUES (?, ?, ?, ?, ?, 'active', ?)`, deviceID, userID, details.Fingerprint[:],
		details.PublicKey.X, details.PublicKey.Y, timestamp)
	if err != nil {
		return Resolution{}, ErrIdentityUnavailable
	}
	return Resolution{State: Created, SubjectID: strconv.FormatInt(userID, 10), DeviceID: deviceID}, nil
}
