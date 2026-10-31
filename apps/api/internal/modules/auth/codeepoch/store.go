// Package codeepoch binds the email verification hasher configuration to a
// database epoch. Callers must first hold verified identity and code-derivation
// epoch locks in the same transaction and stop older writers before bootstrap.
package codeepoch

import (
	"context"
	"crypto/hmac"
	"database/sql"
	"errors"
	"log/slog"

	"nerae/api/internal/modules/auth/domain"
)

var ErrEpoch = errors.New("email code hasher epoch unavailable")

// Store is bound to one verifier-key epoch. It stores only a non-secret HMAC
// tag and key ID; the verifier key remains owned by EmailCodeHasher.
type Store struct {
	keyID string
	tag   [32]byte
}

func New(hasher *domain.EmailCodeHasher, keyID string) (*Store, error) {
	if hasher == nil || !validKeyID(keyID) {
		return nil, ErrEpoch
	}
	tag, err := hasher.KeyEpochTag()
	if err != nil {
		return nil, ErrEpoch
	}
	return &Store{keyID: keyID, tag: tag}, nil
}

func (Store) String() string               { return "EmailCodeHashEpoch{redacted}" }
func (Store) GoString() string             { return "EmailCodeHashEpoch{redacted}" }
func (Store) MarshalJSON() ([]byte, error) { return nil, ErrEpoch }
func (Store) LogValue() slog.Value         { return slog.StringValue("EmailCodeHashEpoch{redacted}") }

// Initialize explicitly binds a missing epoch only when all email challenge
// history tables are empty. The caller owns commit/rollback. It must already
// hold the verified identity and code-derivation epoch locks in this tx.
func (s *Store) Initialize(ctx context.Context, tx *sql.Tx) error {
	if s == nil || ctx == nil || tx == nil || !validKeyID(s.keyID) {
		return ErrEpoch
	}
	var storedID, storedTag []byte
	err := tx.QueryRowContext(ctx, `SELECT key_id,key_tag FROM auth_email_code_hash_key_epoch WHERE slot=1 FOR UPDATE`).Scan(&storedID, &storedTag)
	if err == nil {
		return s.matches(storedID, storedTag)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return ErrEpoch
	}

	for _, statement := range [...]string{
		`SELECT 1 FROM auth_email_login_slot LIMIT 1 FOR UPDATE`,
		`SELECT 1 FROM auth_email_login_challenge LIMIT 1 FOR UPDATE`,
		`SELECT 1 FROM auth_email_outbox LIMIT 1 FOR UPDATE`,
	} {
		var found int
		err := tx.QueryRowContext(ctx, statement).Scan(&found)
		if err == nil || !errors.Is(err, sql.ErrNoRows) {
			return ErrEpoch
		}
	}

	result, err := tx.ExecContext(ctx, `INSERT INTO auth_email_code_hash_key_epoch (slot,key_id,key_tag) VALUES (1,?,?)`, []byte(s.keyID), s.tag[:])
	if err != nil {
		return ErrEpoch
	}
	if affected, err := result.RowsAffected(); err != nil || affected != 1 {
		return ErrEpoch
	}
	storedID = nil
	storedTag = nil
	if err := tx.QueryRowContext(ctx, `SELECT key_id,key_tag FROM auth_email_code_hash_key_epoch WHERE slot=1 FOR UPDATE`).Scan(&storedID, &storedTag); err != nil {
		return ErrEpoch
	}
	return s.matches(storedID, storedTag)
}

// Lock verifies an already initialized epoch under a shared row lock. It never
// creates a missing row and holds the lock until the caller ends tx.
func (s *Store) Lock(ctx context.Context, tx *sql.Tx) error {
	if s == nil || ctx == nil || tx == nil || !validKeyID(s.keyID) {
		return ErrEpoch
	}
	var storedID, storedTag []byte
	if err := tx.QueryRowContext(ctx, `SELECT key_id,key_tag FROM auth_email_code_hash_key_epoch WHERE slot=1 FOR SHARE`).Scan(&storedID, &storedTag); err != nil {
		return ErrEpoch
	}
	return s.matches(storedID, storedTag)
}

func (s *Store) matches(storedID, storedTag []byte) error {
	if s == nil || len(storedID) != len(s.keyID) || string(storedID) != s.keyID || len(storedTag) != len(s.tag) || !hmac.Equal(storedTag, s.tag[:]) {
		return ErrEpoch
	}
	return nil
}

func validKeyID(value string) bool {
	if len(value) == 0 || len(value) > 64 {
		return false
	}
	for _, char := range value {
		if !(char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' || char == '-' || char == '_' || char == '.') {
			return false
		}
	}
	return true
}
