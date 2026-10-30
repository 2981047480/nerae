// Package issuance derives reproducible one-time email codes for trusted
// server-side issuance workflows. It does not send, persist, or authorize a
// code; callers must protect the independent key and bind delivery to the
// corresponding stored challenge.
package issuance

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"hash"
	"log/slog"
)

var ErrInvalidDerivation = errors.New("invalid email code derivation")

const (
	codeDomain       = "nerae.auth.email-code-derive.v1\x00"
	epochDomain      = "nerae.auth.email-code-epoch.v1\x00"
	maxAttempts      = 64
	minChallengeSize = 22
	maxChallengeSize = 128
)

// Deriver owns a copy of the independently managed 32-byte key. A 32-byte
// length does not prove entropy; production callers must supply a protected,
// cryptographically random key. Do not use the login verifier key here.
type Deriver struct {
	key         [32]byte
	initialized bool
}

func (Deriver) String() string               { return "EmailCodeDeriver{redacted}" }
func (Deriver) GoString() string             { return "EmailCodeDeriver{redacted}" }
func (Deriver) MarshalJSON() ([]byte, error) { return nil, ErrInvalidDerivation }
func (Deriver) LogValue() slog.Value         { return slog.StringValue("EmailCodeDeriver{redacted}") }

func NewDeriver(key []byte) (*Deriver, error) {
	if len(key) != 32 {
		return nil, ErrInvalidDerivation
	}
	d := &Deriver{initialized: true}
	copy(d.key[:], key)
	return d, nil
}

// Derive returns the same zero-padded decimal code for the same challenge,
// purpose, digit count, and key. The challenge identifier and purpose are
// length-prefixed and domain-separated; a monotonically increasing candidate
// counter makes rejection sampling deterministic and bounded.
func (d *Deriver) Derive(challengeID, purpose string, digits int) (string, error) {
	if d == nil || !d.initialized || !validChallengeID(challengeID) || !validPurpose(purpose) || digits < 6 || digits > 10 {
		return "", ErrInvalidDerivation
	}
	n := decimalRange(digits)
	value, err := sampleUniform(n, func(counter uint64) (uint64, error) {
		mac := hmac.New(sha256.New, d.key[:])
		_, _ = mac.Write([]byte(codeDomain))
		writeField(mac, []byte(challengeID))
		writeField(mac, []byte(purpose))
		writeField(mac, []byte{byte(digits)})
		var encodedCounter [8]byte
		binary.BigEndian.PutUint64(encodedCounter[:], counter)
		writeField(mac, encodedCounter[:])
		return binary.BigEndian.Uint64(mac.Sum(nil)[:8]), nil
	})
	if err != nil {
		return "", ErrInvalidDerivation
	}
	return fmt.Sprintf("%0*d", digits, value), nil
}

// KeyEpochTag is a non-secret verifier for the selected key. A caller may
// persist it with a separately managed key identifier and fail closed on a
// mismatch; this package does not implement database epoch storage or rotation.
func (d *Deriver) KeyEpochTag() ([sha256.Size]byte, error) {
	if d == nil || !d.initialized {
		return [sha256.Size]byte{}, ErrInvalidDerivation
	}
	mac := hmac.New(sha256.New, d.key[:])
	_, _ = mac.Write([]byte(epochDomain))
	var tag [sha256.Size]byte
	copy(tag[:], mac.Sum(nil))
	return tag, nil
}

func validChallengeID(value string) bool {
	if len(value) < minChallengeSize || len(value) > maxChallengeSize {
		return false
	}
	for _, c := range value {
		if !(c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '_' || c == '-') {
			return false
		}
	}
	return true
}

func validPurpose(value string) bool { return value == "email_login" || value == "email_link" }

func decimalRange(digits int) uint64 {
	n := uint64(1)
	for i := 0; i < digits; i++ {
		n *= 10
	}
	return n
}

func writeField(dst hash.Hash, value []byte) {
	var size [4]byte
	binary.BigEndian.PutUint32(size[:], uint32(len(value)))
	_, _ = dst.Write(size[:])
	_, _ = dst.Write(value)
}

func sampleUniform(n uint64, candidate func(uint64) (uint64, error)) (uint64, error) {
	if n == 0 || candidate == nil {
		return 0, ErrInvalidDerivation
	}
	threshold := -n % n
	for counter := uint64(0); counter < maxAttempts; counter++ {
		value, err := candidate(counter)
		if err != nil {
			return 0, ErrInvalidDerivation
		}
		if value >= threshold {
			return value % n, nil
		}
	}
	return 0, ErrInvalidDerivation
}
