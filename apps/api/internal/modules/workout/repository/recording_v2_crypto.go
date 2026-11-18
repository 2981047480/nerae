package repository

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"io"
	"log/slog"
	"strings"
)

var errRecordingV2Protection = errors.New("recording v2 protection unavailable")

type recordingV2Protector struct {
	aead     cipher.AEAD
	keyID    string
	epochTag [32]byte
}

// The existing independently provisioned workout root key is used only to
// derive a separate AES key; its epoch tag must be checked inside the write tx.
func newRecordingV2Protector(root []byte, keyID string) (*recordingV2Protector, error) {
	if len(root) != 32 || len(keyID) < 1 || len(keyID) > 64 || strings.IndexFunc(keyID, func(r rune) bool { return r < 33 || r > 126 }) >= 0 {
		return nil, errRecordingV2Protection
	}
	mac := hmac.New(sha256.New, root)
	_, _ = mac.Write([]byte("nerae/workout/recording-snapshot-key/v2\x00" + keyID))
	derived := mac.Sum(nil)
	block, err := aes.NewCipher(derived)
	clear(derived)
	if err != nil {
		return nil, errRecordingV2Protection
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, errRecordingV2Protection
	}
	mac = hmac.New(sha256.New, root)
	_, _ = mac.Write([]byte("nerae/workout/exercise-key-epoch/v1\x00" + keyID))
	var tag [32]byte
	copy(tag[:], mac.Sum(nil))
	return &recordingV2Protector{aead: aead, keyID: keyID, epochTag: tag}, nil
}
func (recordingV2Protector) String() string               { return "recordingV2Protector{redacted}" }
func (recordingV2Protector) GoString() string             { return "recordingV2Protector{redacted}" }
func (recordingV2Protector) MarshalJSON() ([]byte, error) { return nil, errRecordingV2Protection }
func (recordingV2Protector) LogValue() slog.Value {
	return slog.StringValue("recordingV2Protector{redacted}")
}

func recordingV2AAD(subject, session, version uint64, keyID string) []byte {
	const prefix = "nerae/workout/recording-snapshot/v2\x00"
	out := make([]byte, len(prefix)+24+len(keyID))
	copy(out, prefix)
	binary.BigEndian.PutUint64(out[len(prefix):], subject)
	binary.BigEndian.PutUint64(out[len(prefix)+8:], session)
	binary.BigEndian.PutUint64(out[len(prefix)+16:], version)
	copy(out[len(prefix)+24:], keyID)
	return out
}
func (p *recordingV2Protector) seal(subject, session, version uint64, plain []byte) ([]byte, error) {
	if p == nil || p.aead == nil || subject == 0 || session == 0 || version == 0 || len(plain) == 0 || len(plain) > maxRecordingV2SnapshotBytes {
		return nil, errRecordingV2Protection
	}
	nonce := make([]byte, p.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, errRecordingV2Protection
	}
	return p.aead.Seal(nonce, nonce, plain, recordingV2AAD(subject, session, version, p.keyID)), nil
}
func (p *recordingV2Protector) open(subject, session, version uint64, sealed []byte) ([]byte, error) {
	if p == nil || p.aead == nil || subject == 0 || session == 0 || version == 0 || len(sealed) < p.aead.NonceSize()+p.aead.Overhead()+1 || len(sealed) > maxRecordingV2SnapshotBytes+p.aead.NonceSize()+p.aead.Overhead() {
		return nil, errRecordingV2Protection
	}
	plain, err := p.aead.Open(nil, sealed[:p.aead.NonceSize()], sealed[p.aead.NonceSize():], recordingV2AAD(subject, session, version, p.keyID))
	if err != nil {
		return nil, errRecordingV2Protection
	}
	return plain, nil
}
