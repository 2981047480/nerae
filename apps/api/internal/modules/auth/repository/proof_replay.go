// Package repository contains authentication persistence; it does not issue sessions.
package repository

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"errors"
	"time"

	"github.com/go-sql-driver/mysql"

	"nerae/api/internal/modules/auth/domain"
)

var (
	ErrProofAlreadyUsed  = errors.New("device proof already consumed")
	ErrReplayKeyMismatch = errors.New("device proof replay key mismatch")
	ErrReplayInput       = errors.New("invalid device proof replay input")
	ErrReplayStore       = errors.New("device proof replay storage unavailable")
)

// ProofReplayStore must receive a protected, cryptographically random key.
// Its length cannot prove entropy. Key changes fail closed until an explicit,
// separately reviewed rotation protocol has handled all live proof markers.
type ProofReplayStore struct {
	key []byte
	tag [32]byte
}

func NewProofReplayStore(key []byte) (*ProofReplayStore, error) {
	if len(key) < 32 {
		return nil, ErrReplayInput
	}
	copyKey := append([]byte(nil), key...)
	mac := hmac.New(sha256.New, copyKey)
	_, _ = mac.Write([]byte("nerae.proof-replay.key-tag.v1"))
	var tag [32]byte
	copy(tag[:], mac.Sum(nil))
	return &ProofReplayStore{key: copyKey, tag: tag}, nil
}

// Consume inserts a single-use marker in the caller's transaction. The caller
// must first verify the proof and derive fingerprint from its trusted public
// key; it must roll back this transaction on any error. This function does not
// verify SMS, issue a session, or implement the separate idempotent-response
// replay and revocation checks required by the login contract.
func (s *ProofReplayStore) Consume(ctx context.Context, tx *sql.Tx, fingerprint [32]byte, proof domain.VerifiedDeviceProof, now time.Time) error {
	if s == nil || len(s.key) < 32 || tx == nil || fingerprint == [32]byte{} ||
		proof.JTI == "" || len(proof.JTI) > 256 || proof.ExpiresAt.IsZero() || proof.ReplayUntil.IsZero() ||
		now.IsZero() || proof.ExpiresAt.After(domain.DeviceProofLatestExpiry(now)) ||
		!proof.ReplayUntil.Equal(domain.DeviceProofReplayDeadline(proof.ExpiresAt)) || !proof.ReplayUntil.After(now) {
		return ErrReplayInput
	}
	if err := s.checkKeyEpoch(ctx, tx); err != nil {
		return err
	}
	digest := s.jtiDigest(fingerprint, proof.JTI)
	// MySQL DATETIME(6) has microsecond precision. Never truncate the last
	// fractional microsecond, which could let a live proof outlast its marker.
	retention := proof.ReplayUntil.UTC().Truncate(time.Microsecond)
	if retention.Before(proof.ReplayUntil) {
		retention = retention.Add(time.Microsecond)
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO auth_device_proof_replay
        (device_fingerprint, jti_digest, retain_until, created_at) VALUES (?, ?, ?, ?)`,
		fingerprint[:], digest[:], utcSQL(retention), utcSQL(now))
	if err != nil {
		var mysqlError *mysql.MySQLError
		if errors.As(err, &mysqlError) && mysqlError.Number == 1062 {
			return ErrProofAlreadyUsed
		}
		return ErrReplayStore
	}
	return nil
}

// DATETIME is timezone-free. Write UTC text so a driver's loc setting cannot
// shorten the replay deadline when another instance reads the same marker.
func utcSQL(value time.Time) string {
	return value.UTC().Truncate(time.Microsecond).Format("2006-01-02 15:04:05.000000")
}

func (s *ProofReplayStore) checkKeyEpoch(ctx context.Context, tx *sql.Tx) error {
	var stored []byte
	err := tx.QueryRowContext(ctx, `SELECT key_tag FROM auth_device_proof_key_epoch WHERE slot = 1 FOR SHARE`).Scan(&stored)
	if errors.Is(err, sql.ErrNoRows) {
		// The first transaction installs the epoch. A concurrent initializer can
		// deadlock or conflict; it fails closed and the caller may retry safely.
		if _, err = tx.ExecContext(ctx, `INSERT INTO auth_device_proof_key_epoch (slot, key_tag) VALUES (1, ?)`, s.tag[:]); err != nil {
			return ErrReplayStore
		}
		return nil
	}
	if err != nil {
		return ErrReplayStore
	}
	if !hmac.Equal(stored, s.tag[:]) {
		return ErrReplayKeyMismatch
	}
	return nil
}

func (s *ProofReplayStore) jtiDigest(fingerprint [32]byte, jti string) [32]byte {
	mac := hmac.New(sha256.New, s.key)
	_, _ = mac.Write([]byte("nerae.proof-replay.jti.v1\x00"))
	_, _ = mac.Write(fingerprint[:])
	var length [4]byte
	binary.BigEndian.PutUint32(length[:], uint32(len(jti)))
	_, _ = mac.Write(length[:])
	_, _ = mac.Write([]byte(jti))
	var result [32]byte
	copy(result[:], mac.Sum(nil))
	return result
}
