// Package bootstrap provides fail-closed startup initialization for auth key
// epochs. Callers must run Initialize in a transaction before serving auth
// traffic and commit that transaction only when it returns nil.
package bootstrap

import (
	"context"
	"crypto/hmac"
	"database/sql"
	"errors"
	"log/slog"

	"nerae/api/internal/modules/auth/domain"
)

var ErrIdentityEpoch = errors.New("email identity epoch unavailable")

// IdentityEpochInitializer is bound to the configured identity protector.
// It deliberately exposes no key material or database details.
type IdentityEpochInitializer struct {
	keys *domain.EmailIdentityProtector
}

func NewIdentityEpochInitializer(keys *domain.EmailIdentityProtector) (*IdentityEpochInitializer, error) {
	if keys == nil {
		return nil, ErrIdentityEpoch
	}
	if _, _, err := keys.KeyEpoch(); err != nil {
		return nil, ErrIdentityEpoch
	}
	return &IdentityEpochInitializer{keys: keys}, nil
}

func (IdentityEpochInitializer) String() string               { return "IdentityEpochInitializer{redacted}" }
func (IdentityEpochInitializer) GoString() string             { return "IdentityEpochInitializer{redacted}" }
func (IdentityEpochInitializer) MarshalJSON() ([]byte, error) { return nil, ErrIdentityEpoch }
func (IdentityEpochInitializer) LogValue() slog.Value {
	return slog.StringValue("IdentityEpochInitializer{redacted}")
}

// Initialize locks an existing epoch and checks it against configured keys.
// A missing epoch may be inserted only into a database with no email slot,
// challenge, or email-kind identity rows (including revoked identities).
// It never commits: the caller owns transaction rollback/commit and must stop
// old lazy-initializing processes before deployment.
func (i *IdentityEpochInitializer) Initialize(ctx context.Context, tx *sql.Tx) error {
	if i == nil || i.keys == nil || ctx == nil || tx == nil {
		return ErrIdentityEpoch
	}
	_, expected, err := i.keys.KeyEpoch()
	if err != nil {
		return ErrIdentityEpoch
	}
	var stored []byte
	err = tx.QueryRowContext(ctx, `SELECT key_tag FROM auth_email_identity_key_epoch WHERE slot=1 FOR UPDATE`).Scan(&stored)
	if err == nil {
		if len(stored) != len(expected) || !hmac.Equal(stored, expected[:]) {
			return ErrIdentityEpoch
		}
		return i.validateEmailSchema(ctx, tx)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return ErrIdentityEpoch
	}
	if err := i.validateEmailSchema(ctx, tx); err != nil {
		return ErrIdentityEpoch
	}

	for _, query := range [...]string{
		`SELECT 1 FROM auth_email_login_slot LIMIT 1 FOR UPDATE`,
		`SELECT 1 FROM auth_email_login_challenge LIMIT 1 FOR UPDATE`,
		`SELECT 1 FROM login_identity WHERE kind='email' LIMIT 1 FOR UPDATE`,
	} {
		var populated int
		err := tx.QueryRowContext(ctx, query).Scan(&populated)
		if err == nil || !errors.Is(err, sql.ErrNoRows) {
			return ErrIdentityEpoch
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO auth_email_identity_key_epoch (slot,key_tag) VALUES (1,?)`, expected[:]); err != nil {
		return ErrIdentityEpoch
	}
	return nil
}

func (*IdentityEpochInitializer) validateEmailSchema(ctx context.Context, tx *sql.Tx) error {
	// Zero-row selects validate the columns used by the email challenge and
	// identity paths without reading or rejecting existing identity data.
	for _, query := range [...]string{
		`SELECT email_index,challenge_id FROM auth_email_login_slot LIMIT 0`,
		`SELECT challenge_id,email_index,email_ciphertext,email_key_id,nonce,public_x,public_y,key_fingerprint,code_digest,attempts,max_attempts,proof_max_lifetime_ns,proof_clock_skew_ns,created_at,expires_at,consumed_at FROM auth_email_login_challenge LIMIT 0`,
		`SELECT id,user_id,kind,provider_scope,subject_enc,subject_lookup_hmac,key_id,state,verified_at,created_at,updated_at FROM login_identity LIMIT 0`,
	} {
		rows, err := tx.QueryContext(ctx, query)
		if err != nil {
			return ErrIdentityEpoch
		}
		closeErr := rows.Close()
		if closeErr != nil || rows.Err() != nil {
			return ErrIdentityEpoch
		}
	}
	return nil
}
