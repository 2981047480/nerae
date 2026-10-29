package repository

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"io"
	"log/slog"
	"strings"
)

// ExerciseNameProtector requires a random, externally managed 256-bit key.
// Production secret provisioning and rotation are separate integration work.
type ExerciseNameProtector struct {
	aead  cipher.AEAD
	keyID string
	tag   [32]byte
}

func NewExerciseNameProtector(key []byte, keyID string) (*ExerciseNameProtector, error) {
	if len(key) != 32 || len(keyID) < 1 || len(keyID) > 64 || strings.IndexFunc(keyID, func(r rune) bool { return r < 33 || r > 126 }) >= 0 {
		return nil, ErrUnavailable
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, ErrUnavailable
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, ErrUnavailable
	}
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte("nerae/workout/exercise-key-epoch/v1\x00" + keyID))
	var tag [32]byte
	copy(tag[:], mac.Sum(nil))
	return &ExerciseNameProtector{aead: aead, keyID: keyID, tag: tag}, nil
}

func (ExerciseNameProtector) String() string               { return "ExerciseNameProtector{redacted}" }
func (ExerciseNameProtector) GoString() string             { return "ExerciseNameProtector{redacted}" }
func (ExerciseNameProtector) MarshalJSON() ([]byte, error) { return nil, ErrUnavailable }
func (ExerciseNameProtector) LogValue() slog.Value {
	return slog.StringValue("ExerciseNameProtector{redacted}")
}

func exerciseAAD(subjectID, sessionID uint64, entry string) []byte {
	const prefix = "nerae/workout/exercise-name/v1\x00"
	buf := make([]byte, len(prefix)+16+len(entry))
	copy(buf, prefix)
	binary.BigEndian.PutUint64(buf[len(prefix):], subjectID)
	binary.BigEndian.PutUint64(buf[len(prefix)+8:], sessionID)
	copy(buf[len(prefix)+16:], entry)
	return buf
}

func (p *ExerciseNameProtector) seal(subjectID, sessionID uint64, entry, name string) ([]byte, error) {
	if p == nil || p.aead == nil || subjectID == 0 || sessionID == 0 {
		return nil, ErrUnavailable
	}
	nonce := make([]byte, p.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, ErrUnavailable
	}
	return p.aead.Seal(nonce, nonce, []byte(name), exerciseAAD(subjectID, sessionID, entry)), nil
}

func (p *ExerciseNameProtector) open(subjectID, sessionID uint64, entry string, value []byte) (string, error) {
	if p == nil || p.aead == nil || subjectID == 0 || sessionID == 0 || len(value) < p.aead.NonceSize()+p.aead.Overhead() {
		return "", ErrUnavailable
	}
	plain, err := p.aead.Open(nil, value[:p.aead.NonceSize()], value[p.aead.NonceSize():], exerciseAAD(subjectID, sessionID, entry))
	if err != nil {
		return "", ErrUnavailable
	}
	return string(plain), nil
}

// InitializeExerciseKey is a deployment-only initialization of an empty
// singleton. The caller commits its transaction before serving any writes.
// Existing mismatched keys fail closed; this is not a key rotation protocol.
func (p *ExerciseNameProtector) InitializeExerciseKey(ctx context.Context, tx *sql.Tx) error {
	if p == nil || ctx == nil || tx == nil {
		return ErrUnavailable
	}
	var keyID, tag []byte
	if err := tx.QueryRowContext(ctx, `SELECT key_id, key_tag FROM workout_crypto_key_epoch WHERE slot = 1 FOR UPDATE`).Scan(&keyID, &tag); err != nil {
		return ErrUnavailable
	}
	if keyID == nil && tag == nil {
		if _, err := tx.ExecContext(ctx, `UPDATE workout_crypto_key_epoch SET key_id = ?, key_tag = ? WHERE slot = 1`, p.keyID, p.tag[:]); err != nil {
			return ErrUnavailable
		}
		return nil
	}
	if string(keyID) != p.keyID || !hmac.Equal(tag, p.tag[:]) {
		return ErrUnavailable
	}
	return nil
}
