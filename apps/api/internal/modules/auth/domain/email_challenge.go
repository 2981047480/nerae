package domain

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"errors"
)

var ErrInvalidEmailChallenge = errors.New("invalid email challenge input")

// GenerateEmailCode returns a cryptographically random code. The caller must
// transmit it only through the configured provider and never log or persist it.
// Issuing a code does not establish delivery, authentication, or permission.
func GenerateEmailCode(digits int) (string, error) {
	code, err := randomSMSCode(rand.Reader, digits)
	if err != nil {
		return "", ErrInvalidEmailChallenge
	}
	return code, nil
}

// EmailCodeHasher uses a protected, high-entropy server key; key length alone
// cannot prove randomness. The domain separator keeps email and SMS digests
// distinct even if a deployment erroneously supplies the same key for both.
type EmailCodeHasher struct{ key []byte }

func NewEmailCodeHasher(key []byte) (*EmailCodeHasher, error) {
	if len(key) < 32 {
		return nil, ErrInvalidEmailChallenge
	}
	return &EmailCodeHasher{key: append([]byte(nil), key...)}, nil
}

// Digest binds a code to a server-generated challenge and an email purpose.
// Expiry, rate limiting, single-use consumption and device trust are enforced
// separately and must all succeed before issuing a real session.
func (h *EmailCodeHasher) Digest(challengeID, purpose, code string) ([]byte, error) {
	if h == nil || len(h.key) < 32 || !validChallengeID(challengeID) ||
		(purpose != "email_login" && purpose != "email_link") || !validSMSCode(code) {
		return nil, ErrInvalidEmailChallenge
	}
	mac := hmac.New(sha256.New, h.key)
	_, _ = mac.Write([]byte("nerae.email-code.v1\x00"))
	for _, value := range []string{challengeID, purpose, code} {
		var length [4]byte
		binary.BigEndian.PutUint32(length[:], uint32(len(value)))
		_, _ = mac.Write(length[:])
		_, _ = mac.Write([]byte(value))
	}
	return mac.Sum(nil), nil
}

func (h *EmailCodeHasher) Verify(challengeID, purpose, code string, expected []byte) (bool, error) {
	if len(expected) != sha256.Size {
		return false, ErrInvalidEmailChallenge
	}
	actual, err := h.Digest(challengeID, purpose, code)
	if err != nil {
		return false, err
	}
	return hmac.Equal(actual, expected), nil
}
