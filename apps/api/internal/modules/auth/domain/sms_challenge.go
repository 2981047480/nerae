package domain

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math/big"
)

var ErrInvalidSMSChallenge = errors.New("invalid SMS challenge input")

// GenerateChallengeID and GenerateChallengeNonce use cryptographic randomness.
// The caller stores only the nonce and challenge metadata required by its
// trusted transaction; neither value proves that an SMS was delivered.
func GenerateChallengeID() (string, error)    { return randomOpaque(rand.Reader, 24) }
func GenerateChallengeNonce() (string, error) { return randomOpaque(rand.Reader, 32) }

func randomOpaque(source io.Reader, bytes int) (string, error) {
	buf := make([]byte, bytes)
	if _, err := io.ReadFull(source, buf); err != nil {
		return "", ErrInvalidSMSChallenge
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// GenerateSMSCode returns a fresh one-time code for delivery to the verified
// provider only. Callers must not log it or include it in an API response.
func GenerateSMSCode(digits int) (string, error) { return randomSMSCode(rand.Reader, digits) }

func randomSMSCode(source io.Reader, digits int) (string, error) {
	if digits < 6 || digits > 10 {
		return "", ErrInvalidSMSChallenge
	}
	limit := int64(1)
	for i := 0; i < digits; i++ {
		limit *= 10
	}
	number, err := rand.Int(source, big.NewInt(limit))
	if err != nil {
		return "", ErrInvalidSMSChallenge
	}
	return fmt.Sprintf("%0*d", digits, number.Int64()), nil
}

// SMSCodeHasher keeps a copy of a server-only key. The caller must provide at
// least 32 cryptographically random bytes from protected key management; length
// alone does not establish entropy. Store the key identifier separately and
// retain the digest, never the raw code.
type SMSCodeHasher struct{ key []byte }

func NewSMSCodeHasher(key []byte) (*SMSCodeHasher, error) {
	if len(key) < 32 {
		return nil, ErrInvalidSMSChallenge
	}
	return &SMSCodeHasher{key: append([]byte(nil), key...)}, nil
}

// Digest binds a code to one server-created challenge and purpose. It does not
// provide rate limiting, expiration, single-use consumption or authorization.
func (h *SMSCodeHasher) Digest(challengeID, purpose, code string) ([]byte, error) {
	if h == nil || len(h.key) < 32 || !validChallengeID(challengeID) || !validSMSPurpose(purpose) || !validSMSCode(code) {
		return nil, ErrInvalidSMSChallenge
	}
	mac := hmac.New(sha256.New, h.key)
	_, _ = mac.Write([]byte("nerae.sms-code.v1\x00"))
	for _, value := range []string{challengeID, purpose, code} {
		var length [4]byte
		binary.BigEndian.PutUint32(length[:], uint32(len(value)))
		_, _ = mac.Write(length[:])
		_, _ = mac.Write([]byte(value))
	}
	return mac.Sum(nil), nil
}

func (h *SMSCodeHasher) Verify(challengeID, purpose, code string, expected []byte) (bool, error) {
	if len(expected) != sha256.Size {
		return false, ErrInvalidSMSChallenge
	}
	actual, err := h.Digest(challengeID, purpose, code)
	if err != nil {
		return false, err
	}
	return hmac.Equal(actual, expected), nil
}

func validChallengeID(value string) bool {
	if len(value) < 22 || len(value) > 128 {
		return false
	}
	for _, c := range value {
		if !(c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '_' || c == '-') {
			return false
		}
	}
	return true
}

func validSMSPurpose(value string) bool { return value == "phone_login" || value == "phone_link" }

func validSMSCode(value string) bool {
	if len(value) < 6 || len(value) > 10 {
		return false
	}
	for _, c := range value {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}
