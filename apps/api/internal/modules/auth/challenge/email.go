// Package challenge persists single-use login challenges, not sessions.
package challenge

import (
	"context"
	"crypto/hmac"
	"database/sql"
	"errors"
	"log/slog"
	"time"

	"nerae/api/internal/modules/auth/domain"
	"nerae/api/internal/modules/auth/repository"
)

var ErrChallengeStore = errors.New("email challenge unavailable")

type Store struct {
	hasher     *domain.EmailCodeHasher
	replay     *repository.ProofReplayStore
	identities *domain.EmailIdentityProtector
}

func NewStore(hasher *domain.EmailCodeHasher, replay *repository.ProofReplayStore, identities *domain.EmailIdentityProtector) (*Store, error) {
	if hasher == nil || replay == nil || identities == nil {
		return nil, ErrChallengeStore
	}
	return &Store{hasher: hasher, replay: replay, identities: identities}, nil
}

// IssueInput contains a validated email and server-generated material. Code,
// nonce and ID must come from crypto/rand.
// Callers must never log this struct or its fields.
type IssueInput struct {
	Email       string
	ChallengeID string
	Nonce       string
	Code        string
	PublicKey   domain.DevicePublicJWK
	CreatedAt   time.Time
	ExpiresAt   time.Time
	MaxAttempts int
}

func (IssueInput) String() string               { return "IssueInput{redacted}" }
func (IssueInput) GoString() string             { return "IssueInput{redacted}" }
func (IssueInput) MarshalJSON() ([]byte, error) { return nil, ErrChallengeStore }
func (IssueInput) LogValue() slog.Value         { return slog.StringValue("IssueInput{redacted}") }

// Issue writes a new challenge and advances the email's active slot in the
// caller's transaction. The caller must roll back on any error. A successful
// commit invalidates earlier challenges for this email and purpose. Sending
// email, durable idempotency and rate limits are separate requirements.
func (s *Store) Issue(ctx context.Context, tx *sql.Tx, input IssueInput) error {
	if s == nil || s.hasher == nil || s.replay == nil || s.identities == nil || ctx == nil || tx == nil ||
		!opaque(input.ChallengeID, 22, 128) || !opaque(input.Nonce, 43, 256) ||
		input.CreatedAt.IsZero() || input.ExpiresAt.IsZero() || input.MaxAttempts < 1 || input.MaxAttempts > 10 {
		return ErrChallengeStore
	}
	created := input.CreatedAt.UTC().Truncate(time.Microsecond)
	expires := input.ExpiresAt.UTC().Truncate(time.Microsecond)
	if !expires.After(created) || expires.Sub(created) > 30*time.Minute {
		return ErrChallengeStore
	}
	fingerprint, err := domain.DeviceKeyFingerprint(input.PublicKey)
	if err != nil {
		return ErrChallengeStore
	}
	digest, err := s.hasher.Digest(input.ChallengeID, "email_login", input.Code)
	if err != nil {
		return ErrChallengeStore
	}
	index, ciphertext, err := s.identities.Protect(input.Email)
	if err != nil {
		return ErrChallengeStore
	}
	keyID, _, err := s.identities.KeyEpoch()
	if err != nil || s.checkEmailEpoch(ctx, tx) != nil {
		return ErrChallengeStore
	}
	// Every issuance and attempt locks the slot before locking an existing
	// challenge; concurrent reissues therefore have a single current ID.
	_, err = tx.ExecContext(ctx, `INSERT INTO auth_email_login_slot (email_index, challenge_id)
		VALUES (?, ?) ON DUPLICATE KEY UPDATE challenge_id = VALUES(challenge_id)`, index[:], input.ChallengeID)
	if err != nil {
		return ErrChallengeStore
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO auth_email_login_challenge
		(challenge_id, email_index, email_ciphertext, email_key_id, nonce, public_x, public_y, key_fingerprint, code_digest, attempts, max_attempts, created_at, expires_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 0, ?, ?, ?)`, input.ChallengeID, index[:], ciphertext, keyID, input.Nonce,
		input.PublicKey.X, input.PublicKey.Y, fingerprint[:], digest, input.MaxAttempts, sqlUTC(created), sqlUTC(expires))
	if err != nil {
		return ErrChallengeStore
	}
	return nil
}

type AttemptInput struct {
	ChallengeID string
	Code        string
	DeviceProof string
	Now         time.Time
	ProofPolicy domain.ProofPolicy
}

func (AttemptInput) String() string               { return "AttemptInput{redacted}" }
func (AttemptInput) GoString() string             { return "AttemptInput{redacted}" }
func (AttemptInput) MarshalJSON() ([]byte, error) { return nil, ErrChallengeStore }
func (AttemptInput) LogValue() slog.Value         { return slog.StringValue("AttemptInput{redacted}") }

// AttemptResult never authenticates a user. Rejected results with Changed=true
// must be committed to retain the wrong-code attempt count. A Verified result
// must be committed only after identity, trusted-device, revocation, session and
// idempotency checks succeed in the same transaction.
type AttemptResult struct {
	Verified        bool
	Changed         bool
	EmailIndex      [32]byte
	EmailCiphertext []byte
	EmailKeyID      string
	Fingerprint     [32]byte
	PublicKey       domain.DevicePublicJWK
}

func (AttemptResult) String() string               { return "AttemptResult{redacted}" }
func (AttemptResult) GoString() string             { return "AttemptResult{redacted}" }
func (AttemptResult) MarshalJSON() ([]byte, error) { return nil, ErrChallengeStore }
func (AttemptResult) LogValue() slog.Value         { return slog.StringValue("AttemptResult{redacted}") }

// Attempt returns the same rejected result for unknown, expired, superseded,
// exhausted, invalid and replayed challenges. Callers must enforce independent
// email/IP/device/global rate limits before exposing any HTTP operation.
func (s *Store) Attempt(ctx context.Context, tx *sql.Tx, input AttemptInput) (AttemptResult, error) {
	rejected := AttemptResult{}
	if s == nil || s.hasher == nil || s.replay == nil || s.identities == nil || ctx == nil || tx == nil ||
		!opaque(input.ChallengeID, 22, 128) || input.Now.IsZero() {
		return rejected, ErrChallengeStore
	}
	if err := s.checkEmailEpoch(ctx, tx); err != nil {
		return rejected, ErrChallengeStore
	}
	var emailIndex []byte
	err := tx.QueryRowContext(ctx, `SELECT email_index FROM auth_email_login_challenge WHERE challenge_id = ?`, input.ChallengeID).Scan(&emailIndex)
	if errors.Is(err, sql.ErrNoRows) {
		return rejected, nil
	}
	if err != nil || len(emailIndex) != 32 {
		return rejected, ErrChallengeStore
	}
	var activeID []byte
	err = tx.QueryRowContext(ctx, `SELECT challenge_id FROM auth_email_login_slot WHERE email_index = ? FOR UPDATE`, emailIndex).Scan(&activeID)
	if errors.Is(err, sql.ErrNoRows) || err == nil && string(activeID) != input.ChallengeID {
		return rejected, nil
	}
	if err != nil {
		return rejected, ErrChallengeStore
	}
	var storedIndex, ciphertext, keyID, nonce, fingerprint, digest []byte
	var x, y string
	var attempts, maxAttempts int
	var expiresRaw string
	var consumed bool
	err = tx.QueryRowContext(ctx, `SELECT email_index, email_ciphertext, email_key_id, nonce, public_x, public_y, key_fingerprint, code_digest, attempts, max_attempts, CAST(expires_at AS CHAR), consumed_at IS NOT NULL
        FROM auth_email_login_challenge WHERE challenge_id = ? FOR UPDATE`, input.ChallengeID).
		Scan(&storedIndex, &ciphertext, &keyID, &nonce, &x, &y, &fingerprint, &digest, &attempts, &maxAttempts, &expiresRaw, &consumed)
	if errors.Is(err, sql.ErrNoRows) {
		return rejected, nil
	}
	if err != nil || len(storedIndex) != 32 || !hmac.Equal(emailIndex, storedIndex) || len(fingerprint) != 32 || len(digest) != 32 ||
		maxAttempts < 1 || maxAttempts > 10 || attempts < 0 || attempts > maxAttempts {
		return rejected, ErrChallengeStore
	}
	expires, err := time.Parse("2006-01-02 15:04:05.999999", expiresRaw)
	if err != nil {
		return rejected, ErrChallengeStore
	}
	if consumed || attempts >= maxAttempts || !expires.After(input.Now) {
		return rejected, nil
	}
	var index [32]byte
	copy(index[:], emailIndex)
	currentKeyID, _, err := s.identities.KeyEpoch()
	if err != nil || string(keyID) != currentKeyID {
		return rejected, ErrChallengeStore
	}
	if _, err := s.identities.Open(index, ciphertext); err != nil {
		return rejected, ErrChallengeStore
	}
	key := domain.DevicePublicJWK{Kty: "EC", Crv: "P-256", X: x, Y: y}
	actualFingerprint, err := domain.DeviceKeyFingerprint(key)
	if err != nil || !hmac.Equal(actualFingerprint[:], fingerprint) {
		return rejected, ErrChallengeStore
	}
	proof, err := domain.VerifyDeviceProof(input.DeviceProof, key,
		domain.ProofExpectation{Purpose: "email_login", ResourceID: input.ChallengeID, Nonce: string(nonce)}, input.Now, input.ProofPolicy)
	if err != nil {
		return rejected, nil
	}
	correct, err := s.hasher.Verify(input.ChallengeID, "email_login", input.Code, digest)
	if err != nil || !correct {
		if _, err := tx.ExecContext(ctx, `UPDATE auth_email_login_challenge SET attempts = attempts + 1 WHERE challenge_id = ?`, input.ChallengeID); err != nil {
			return rejected, ErrChallengeStore
		}
		return AttemptResult{Changed: true}, nil
	}
	if err := s.replay.Consume(ctx, tx, actualFingerprint, proof, input.Now); err != nil {
		if errors.Is(err, repository.ErrProofAlreadyUsed) {
			return rejected, nil
		}
		return rejected, ErrChallengeStore
	}
	result, err := tx.ExecContext(ctx, `UPDATE auth_email_login_challenge SET consumed_at = ? WHERE challenge_id = ? AND consumed_at IS NULL`, sqlUTC(input.Now), input.ChallengeID)
	if err != nil {
		return rejected, ErrChallengeStore
	}
	if count, err := result.RowsAffected(); err != nil || count != 1 {
		return rejected, ErrChallengeStore
	}
	return AttemptResult{Verified: true, Changed: true, EmailIndex: index, EmailCiphertext: ciphertext,
		EmailKeyID: currentKeyID, Fingerprint: actualFingerprint, PublicKey: key}, nil
}

func (s *Store) checkEmailEpoch(ctx context.Context, tx *sql.Tx) error {
	_, expected, err := s.identities.KeyEpoch()
	if err != nil {
		return ErrChallengeStore
	}
	var stored []byte
	err = tx.QueryRowContext(ctx, `SELECT key_tag FROM auth_email_identity_key_epoch WHERE slot = 1 FOR SHARE`).Scan(&stored)
	if errors.Is(err, sql.ErrNoRows) {
		// Concurrent initialization can deadlock; failing closed is safe and
		// callers may retry the entire transaction with fresh idempotency rules.
		if _, err = tx.ExecContext(ctx, `INSERT INTO auth_email_identity_key_epoch (slot, key_tag) VALUES (1, ?)`, expected[:]); err != nil {
			return ErrChallengeStore
		}
		return nil
	}
	if err != nil || !hmac.Equal(stored, expected[:]) {
		return ErrChallengeStore
	}
	return nil
}

// DATETIME has no timezone. Supplying and reading explicit UTC strings avoids
// silently changing deadlines when driver connections use different loc values.
func sqlUTC(value time.Time) string {
	return value.UTC().Truncate(time.Microsecond).Format("2006-01-02 15:04:05.000000")
}

func opaque(value string, min, max int) bool {
	if len(value) < min || len(value) > max {
		return false
	}
	for _, c := range value {
		if !(c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '_' || c == '-') {
			return false
		}
	}
	return true
}
