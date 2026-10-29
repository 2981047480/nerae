package domain

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"io"
	netmail "net/mail"
	"strings"
)

var ErrEmailIdentity = errors.New("invalid or unavailable email identity")

// NormalizeEmail accepts the beta's ASCII addr-spec subset. Domain names are
// case-insensitive; local parts are retained exactly and provider-specific
// aliases are never collapsed. Errors intentionally omit the address.
func NormalizeEmail(value string) (string, error) {
	if value == "" || len(value) > 254 || strings.Count(value, "@") != 1 {
		return "", ErrEmailIdentity
	}
	for _, c := range value {
		if c <= ' ' || c >= 127 {
			return "", ErrEmailIdentity
		}
	}
	parsed, err := netmail.ParseAddress(value)
	if err != nil || parsed.Name != "" || parsed.Address != value {
		return "", ErrEmailIdentity
	}
	parts := strings.SplitN(value, "@", 2)
	return parts[0] + "@" + strings.ToLower(parts[1]), nil
}

// EmailIdentityProtector uses separate high-entropy lookup and encryption keys.
// Key IDs are non-secret rotation metadata. Its caller must enforce a database
// key-epoch/rotation policy before writing identities; independent HMAC keys
// cannot safely maintain global uniqueness without a coordinated migration.
type EmailIdentityProtector struct {
	indexKey []byte
	aead     cipher.AEAD
	keyID    string
	epochTag [32]byte
}

func NewEmailIdentityProtector(indexKey, encryptionKey []byte, keyID string) (*EmailIdentityProtector, error) {
	if len(indexKey) < 32 || len(encryptionKey) != 32 || hmac.Equal(indexKey, encryptionKey) || !validEmailKeyID(keyID) {
		return nil, ErrEmailIdentity
	}
	block, err := aes.NewCipher(encryptionKey)
	if err != nil {
		return nil, ErrEmailIdentity
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, ErrEmailIdentity
	}
	encTag := hmac.New(sha256.New, encryptionKey)
	_, _ = encTag.Write([]byte("nerae.email-identity.encryption-epoch.v1"))
	epoch := hmac.New(sha256.New, indexKey)
	_, _ = epoch.Write([]byte("nerae.email-identity.epoch.v1\x00"))
	_, _ = epoch.Write([]byte{byte(len(keyID))})
	_, _ = epoch.Write([]byte(keyID))
	_, _ = epoch.Write(encTag.Sum(nil))
	var tag [32]byte
	copy(tag[:], epoch.Sum(nil))
	return &EmailIdentityProtector{indexKey: append([]byte(nil), indexKey...), aead: aead, keyID: keyID, epochTag: tag}, nil
}

// KeyEpoch is a non-secret keyed tag for fail-closed database epoch checks.
// Rotation still needs a separately reviewed migration preserving uniqueness.
func (p *EmailIdentityProtector) KeyEpoch() (string, [32]byte, error) {
	if p == nil || p.aead == nil || len(p.indexKey) < 32 || !validEmailKeyID(p.keyID) {
		return "", [32]byte{}, ErrEmailIdentity
	}
	return p.keyID, p.epochTag, nil
}

func (p *EmailIdentityProtector) Index(address string) ([32]byte, error) {
	var result [32]byte
	if p == nil || len(p.indexKey) < 32 || p.aead == nil {
		return result, ErrEmailIdentity
	}
	normalized, err := NormalizeEmail(address)
	if err != nil {
		return result, ErrEmailIdentity
	}
	mac := hmac.New(sha256.New, p.indexKey)
	_, _ = mac.Write([]byte("nerae.email-identity.index.v1\x00"))
	_, _ = mac.Write([]byte(normalized))
	copy(result[:], mac.Sum(nil))
	return result, nil
}

// Protect returns a lookup index and authenticated ciphertext, never plaintext.
// The ciphertext is bound to both key ID and index so rows cannot be swapped.
func (p *EmailIdentityProtector) Protect(address string) ([32]byte, []byte, error) {
	var empty [32]byte
	normalized, err := NormalizeEmail(address)
	if err != nil {
		return empty, nil, ErrEmailIdentity
	}
	index, err := p.Index(normalized)
	if err != nil {
		return empty, nil, ErrEmailIdentity
	}
	nonce := make([]byte, p.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return empty, nil, ErrEmailIdentity
	}
	sealed := p.aead.Seal(nonce, nonce, []byte(normalized), p.associatedData(index))
	return index, sealed, nil
}

// Open verifies the index and ciphertext before returning a normalized address.
func (p *EmailIdentityProtector) Open(index [32]byte, ciphertext []byte) (string, error) {
	if p == nil || p.aead == nil || len(p.indexKey) < 32 ||
		len(ciphertext) < p.aead.NonceSize()+p.aead.Overhead() ||
		len(ciphertext) > p.aead.NonceSize()+p.aead.Overhead()+254 {
		return "", ErrEmailIdentity
	}
	nonce := ciphertext[:p.aead.NonceSize()]
	plain, err := p.aead.Open(nil, nonce, ciphertext[p.aead.NonceSize():], p.associatedData(index))
	if err != nil {
		return "", ErrEmailIdentity
	}
	address, err := NormalizeEmail(string(plain))
	if err != nil || address != string(plain) {
		return "", ErrEmailIdentity
	}
	actual, err := p.Index(address)
	if err != nil || !hmac.Equal(index[:], actual[:]) {
		return "", ErrEmailIdentity
	}
	return address, nil
}

func (p *EmailIdentityProtector) associatedData(index [32]byte) []byte {
	data := append([]byte("nerae.email-identity.cipher.v1\x00"), byte(len(p.keyID)))
	data = append(data, p.keyID...)
	return append(data, index[:]...)
}

func validEmailKeyID(value string) bool {
	if len(value) == 0 || len(value) > 64 {
		return false
	}
	for _, c := range value {
		if !(c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-' || c == '_' || c == '.') {
			return false
		}
	}
	return true
}
