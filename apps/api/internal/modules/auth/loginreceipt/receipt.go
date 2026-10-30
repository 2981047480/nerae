// Package loginreceipt contains cryptographic building blocks for a future
// anonymous-login retry receipt. It does not verify device proofs, current
// authorization, proof replay state, transaction commit, or credential release.
// Open must only be called by a future transaction coordinator after it has
// independently reverified the original signature and current authorization.
package loginreceipt

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"hash"
	"io"
	"log/slog"
	"strings"
	"time"
	"unicode/utf8"
)

var (
	ErrConfiguration  = errors.New("login receipt configuration unavailable")
	ErrInvalidInput   = errors.New("invalid login receipt input")
	ErrInvalidReceipt = errors.New("login receipt unavailable")
)

const (
	maxChallengeID            = 128
	maxProofBytes             = 4096
	maxResultReference        = 256
	maxPlaintext              = 8192
	receiptVersion       byte = 1
	gcmNonceSize              = 12
	gcmTagSize                = 16
	receiptKeyDomain          = "nerae.auth.login-receipt.key.v1\x00"
	receiptRequestDomain      = "nerae.auth.login-receipt.request.v1\x00"
	receiptAADDomain          = "nerae.auth.login-receipt.aad.v1\x00"
)

type ResultType string

const (
	ResultAuthenticated    ResultType = "authenticated"
	ResultApprovalRequired ResultType = "approval_required"
	ResultRejected         ResultType = "rejected"
)

// KeyConfig accepts three separately managed 256-bit keys. Length and
// distinctness are validated; this code cannot establish randomness or entropy.
type KeyConfig struct {
	IdempotencyHMACKey []byte
	RequestHMACKey     []byte
	AES256GCMKey       []byte
}

func (KeyConfig) String() string               { return "LoginReceiptKeyConfig{redacted}" }
func (KeyConfig) GoString() string             { return "LoginReceiptKeyConfig{redacted}" }
func (KeyConfig) MarshalJSON() ([]byte, error) { return nil, ErrConfiguration }
func (KeyConfig) LogValue() slog.Value         { return slog.StringValue("LoginReceiptKeyConfig{redacted}") }

// Request contains validated-shape request fields. DeviceProof is the original
// compact proof string; this package validates encoding but does not verify it.
type Request struct {
	IdempotencyKey string
	ChallengeID    string
	Code           string
	DeviceProof    string
}

func (Request) String() string               { return "LoginReceiptRequest{redacted}" }
func (Request) GoString() string             { return "LoginReceiptRequest{redacted}" }
func (Request) MarshalJSON() ([]byte, error) { return nil, ErrInvalidInput }
func (Request) LogValue() slog.Value         { return slog.StringValue("LoginReceiptRequest{redacted}") }

// Binding contains only keyed digests and the challenge identifier. Keep it
// internal to trusted service code and never use it as proof verification.
type Binding struct {
	challengeID     string
	idempotencyHMAC [sha256.Size]byte
	requestHMAC     [sha256.Size]byte
}

func (Binding) String() string               { return "LoginReceiptBinding{redacted}" }
func (Binding) GoString() string             { return "LoginReceiptBinding{redacted}" }
func (Binding) MarshalJSON() ([]byte, error) { return nil, ErrInvalidReceipt }
func (Binding) LogValue() slog.Value         { return slog.StringValue("LoginReceiptBinding{redacted}") }

// These values are persistence-safe keyed digests, never caller secrets.
func (b Binding) ChallengeID() string                  { return b.challengeID }
func (b Binding) IdempotencyDigest() [sha256.Size]byte { return b.idempotencyHMAC }
func (b Binding) RequestDigest() [sha256.Size]byte     { return b.requestHMAC }

// Context is the full associated-data tuple persisted beside a ciphertext.
// The byte scope must be a server-derived fixed 32-byte protected scope.
type Context struct {
	scope           [sha256.Size]byte
	binding         Binding
	resultType      ResultType
	resultReference string
	expiresAt       time.Time
}

func (Context) String() string               { return "LoginReceiptContext{redacted}" }
func (Context) GoString() string             { return "LoginReceiptContext{redacted}" }
func (Context) MarshalJSON() ([]byte, error) { return nil, ErrInvalidReceipt }
func (Context) LogValue() slog.Value         { return slog.StringValue("LoginReceiptContext{redacted}") }

// Sealed is a versioned nonce+ciphertext blob. Bytes returns a copy suitable
// for persistence; the plaintext must never be stored beside it.
type Sealed struct{ blob []byte }

func (Sealed) String() string               { return "LoginReceiptSealed{redacted}" }
func (Sealed) GoString() string             { return "LoginReceiptSealed{redacted}" }
func (Sealed) MarshalJSON() ([]byte, error) { return nil, ErrInvalidReceipt }
func (Sealed) LogValue() slog.Value         { return slog.StringValue("LoginReceiptSealed{redacted}") }
func (s Sealed) Bytes() []byte              { return append([]byte(nil), s.blob...) }

// ParseSealed copies a persisted versioned nonce+ciphertext blob after cheap
// framing checks. It does not authenticate the ciphertext; Open must do that.
func ParseSealed(blob []byte) (Sealed, error) {
	min := 1 + gcmNonceSize + gcmTagSize
	max := min + maxPlaintext
	if len(blob) < min || len(blob) > max || blob[0] != receiptVersion {
		return Sealed{}, ErrInvalidReceipt
	}
	return Sealed{blob: append([]byte(nil), blob...)}, nil
}

// Plaintext is sensitive credential/result material. Bytes returns a copy for
// an authorized internal transaction coordinator; formatting stays redacted.
type Plaintext struct{ data []byte }

func (Plaintext) String() string               { return "LoginReceiptPlaintext{redacted}" }
func (Plaintext) GoString() string             { return "LoginReceiptPlaintext{redacted}" }
func (Plaintext) MarshalJSON() ([]byte, error) { return nil, ErrInvalidReceipt }
func (Plaintext) LogValue() slog.Value         { return slog.StringValue("LoginReceiptPlaintext{redacted}") }
func (p Plaintext) Bytes() []byte              { return append([]byte(nil), p.data...) }

type Crypto struct {
	idempotencyKey [sha256.Size]byte
	requestKey     [sha256.Size]byte
	aesKey         [sha256.Size]byte
	random         io.Reader
}

func (Crypto) String() string               { return "LoginReceiptCrypto{redacted}" }
func (Crypto) GoString() string             { return "LoginReceiptCrypto{redacted}" }
func (Crypto) MarshalJSON() ([]byte, error) { return nil, ErrConfiguration }
func (Crypto) LogValue() slog.Value         { return slog.StringValue("LoginReceiptCrypto{redacted}") }

func New(cfg KeyConfig) (*Crypto, error) {
	if len(cfg.IdempotencyHMACKey) != sha256.Size || len(cfg.RequestHMACKey) != sha256.Size || len(cfg.AES256GCMKey) != sha256.Size ||
		allZero(cfg.IdempotencyHMACKey) || allZero(cfg.RequestHMACKey) || allZero(cfg.AES256GCMKey) ||
		subtle.ConstantTimeCompare(cfg.IdempotencyHMACKey, cfg.RequestHMACKey) == 1 ||
		subtle.ConstantTimeCompare(cfg.IdempotencyHMACKey, cfg.AES256GCMKey) == 1 ||
		subtle.ConstantTimeCompare(cfg.RequestHMACKey, cfg.AES256GCMKey) == 1 {
		return nil, ErrConfiguration
	}
	c := &Crypto{random: rand.Reader}
	copy(c.idempotencyKey[:], cfg.IdempotencyHMACKey)
	copy(c.requestKey[:], cfg.RequestHMACKey)
	copy(c.aesKey[:], cfg.AES256GCMKey)
	return c, nil
}

// Summarize validates request encoding, then creates domain-separated HMACs.
// IdempotencyKey must originate from a caller CSPRNG; only its canonical
// base64url form and length are checked here.
func (c *Crypto) Summarize(req Request) (Binding, error) {
	if !c.validKeys() || c.random == nil {
		return Binding{}, ErrConfiguration
	}
	key, err := parseIdempotencyKey(req.IdempotencyKey)
	if err != nil || !validChallengeID(req.ChallengeID) || !validCode(req.Code) || !validDeviceProofEncoding(req.DeviceProof) {
		return Binding{}, ErrInvalidInput
	}
	idemac := hmac.New(sha256.New, c.idempotencyKey[:])
	_, _ = io.WriteString(idemac, receiptKeyDomain)
	writePart(idemac, key)
	var idem [sha256.Size]byte
	copy(idem[:], idemac.Sum(nil))
	requestMAC := hmac.New(sha256.New, c.requestKey[:])
	_, _ = io.WriteString(requestMAC, receiptRequestDomain)
	writePart(requestMAC, []byte(req.ChallengeID))
	writePart(requestMAC, []byte(req.Code))
	writePart(requestMAC, []byte(req.DeviceProof))
	var request [sha256.Size]byte
	copy(request[:], requestMAC.Sum(nil))
	return Binding{challengeID: req.ChallengeID, idempotencyHMAC: idem, requestHMAC: request}, nil
}

func (c *Crypto) NewContext(scope []byte, binding Binding, kind ResultType, reference string, expiresAt time.Time) (Context, error) {
	if !c.validKeys() || len(scope) != sha256.Size || !validChallengeID(binding.challengeID) ||
		allZero(scope) ||
		binding.idempotencyHMAC == ([sha256.Size]byte{}) || binding.requestHMAC == ([sha256.Size]byte{}) ||
		(kind != ResultAuthenticated && kind != ResultApprovalRequired && kind != ResultRejected) ||
		(kind == ResultRejected && reference != binding.challengeID) || len(reference) < 1 || len(reference) > maxResultReference ||
		!utf8.ValidString(reference) || strings.IndexByte(reference, 0) >= 0 || expiresAt.IsZero() || expiresAt.Location() != time.UTC || expiresAt.Nanosecond()%int(time.Microsecond) != 0 {
		return Context{}, ErrInvalidInput
	}
	var fixedScope [sha256.Size]byte
	copy(fixedScope[:], scope)
	return Context{scope: fixedScope, binding: binding, resultType: kind, resultReference: reference, expiresAt: expiresAt}, nil
}

// ResultType reports the authenticated AAD classification. It does not make
// any authorization decision or itself authorize credential release.
func (ctx Context) ResultType() ResultType { return ctx.resultType }

func allZero(value []byte) bool {
	var aggregate byte
	for _, b := range value {
		aggregate |= b
	}
	return aggregate == 0
}

// Seal encrypts result bytes using AES-256-GCM. A future coordinator must call
// it only for a result authorized for the current transaction.
func (c *Crypto) Seal(ctx Context, plaintext []byte) (Sealed, error) {
	if !c.validKeys() || c.random == nil || !validContext(ctx) || len(plaintext) > maxPlaintext ||
		(ctx.resultType == ResultRejected && len(plaintext) != 0) ||
		(ctx.resultType != ResultRejected && len(plaintext) < 1) {
		return Sealed{}, ErrInvalidInput
	}
	aead, err := c.aead()
	if err != nil {
		return Sealed{}, ErrConfiguration
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := io.ReadFull(c.random, nonce); err != nil {
		return Sealed{}, ErrInvalidReceipt
	}
	blob := make([]byte, 1, 1+len(nonce)+len(plaintext)+aead.Overhead())
	blob[0] = receiptVersion
	blob = append(blob, nonce...)
	blob = aead.Seal(blob, nonce, plaintext, associatedData(ctx))
	return Sealed{blob: blob}, nil
}

// Open decrypts a stored receipt only. A caller must first reverify the
// original device signature and current authorization in the owning transaction;
// this package deliberately does not implement those gates or expiry checks.
func (c *Crypto) Open(ctx Context, sealed Sealed) (Plaintext, error) {
	if c == nil || !validContext(ctx) {
		return Plaintext{}, ErrInvalidReceipt
	}
	aead, err := c.aead()
	if err != nil {
		return Plaintext{}, ErrInvalidReceipt
	}
	min := 1 + aead.NonceSize() + aead.Overhead()
	max := 1 + aead.NonceSize() + maxPlaintext + aead.Overhead()
	if len(sealed.blob) < min || len(sealed.blob) > max || sealed.blob[0] != receiptVersion {
		return Plaintext{}, ErrInvalidReceipt
	}
	start := 1
	nonce := sealed.blob[start : start+aead.NonceSize()]
	ciphertext := sealed.blob[start+aead.NonceSize():]
	plaintext, err := aead.Open(nil, nonce, ciphertext, associatedData(ctx))
	if err != nil || len(plaintext) > maxPlaintext ||
		(ctx.resultType == ResultRejected && len(plaintext) != 0) ||
		(ctx.resultType != ResultRejected && len(plaintext) < 1) {
		return Plaintext{}, ErrInvalidReceipt
	}
	return Plaintext{data: plaintext}, nil
}

func (c *Crypto) aead() (cipher.AEAD, error) {
	if !c.validKeys() {
		return nil, ErrConfiguration
	}
	block, err := aes.NewCipher(c.aesKey[:])
	if err != nil {
		return nil, ErrConfiguration
	}
	return cipher.NewGCM(block)
}

func (c *Crypto) validKeys() bool {
	return c != nil && !allZero(c.idempotencyKey[:]) && !allZero(c.requestKey[:]) && !allZero(c.aesKey[:]) &&
		subtle.ConstantTimeCompare(c.idempotencyKey[:], c.requestKey[:]) != 1 &&
		subtle.ConstantTimeCompare(c.idempotencyKey[:], c.aesKey[:]) != 1 &&
		subtle.ConstantTimeCompare(c.requestKey[:], c.aesKey[:]) != 1
}

func validContext(ctx Context) bool {
	return validChallengeID(ctx.binding.challengeID) &&
		ctx.binding.idempotencyHMAC != ([sha256.Size]byte{}) && ctx.binding.requestHMAC != ([sha256.Size]byte{}) &&
		(ctx.resultType == ResultAuthenticated || ctx.resultType == ResultApprovalRequired || ctx.resultType == ResultRejected) &&
		(ctx.resultType != ResultRejected || ctx.resultReference == ctx.binding.challengeID) &&
		len(ctx.resultReference) > 0 && len(ctx.resultReference) <= maxResultReference && utf8.ValidString(ctx.resultReference) &&
		strings.IndexByte(ctx.resultReference, 0) < 0 && !ctx.expiresAt.IsZero() && ctx.expiresAt.Location() == time.UTC && ctx.expiresAt.Nanosecond()%int(time.Microsecond) == 0
}

func associatedData(ctx Context) []byte {
	var out []byte
	out = append(out, receiptAADDomain...)
	writeBytes(&out, ctx.scope[:])
	writeBytes(&out, ctx.binding.idempotencyHMAC[:])
	writeBytes(&out, ctx.binding.requestHMAC[:])
	writeBytes(&out, []byte(ctx.binding.challengeID))
	writeBytes(&out, []byte(ctx.resultType))
	writeBytes(&out, []byte(ctx.resultReference))
	var micros [8]byte
	binary.BigEndian.PutUint64(micros[:], uint64(ctx.expiresAt.UnixMicro()))
	writeBytes(&out, micros[:])
	return out
}

func writeBytes(dst *[]byte, value []byte) {
	var size [4]byte
	binary.BigEndian.PutUint32(size[:], uint32(len(value)))
	*dst = append(*dst, size[:]...)
	*dst = append(*dst, value...)
}

func writePart(dst hash.Hash, value []byte) {
	var size [4]byte
	binary.BigEndian.PutUint32(size[:], uint32(len(value)))
	_, _ = dst.Write(size[:])
	_, _ = dst.Write(value)
}

func parseIdempotencyKey(value string) ([]byte, error) {
	if len(value) < 22 || len(value) > 128 {
		return nil, ErrInvalidInput
	}
	decoded, err := base64.RawURLEncoding.Strict().DecodeString(value)
	if err != nil || len(decoded) < 16 || len(decoded) > 96 || base64.RawURLEncoding.EncodeToString(decoded) != value {
		return nil, ErrInvalidInput
	}
	return decoded, nil
}

func validChallengeID(value string) bool {
	if len(value) < 22 || len(value) > maxChallengeID {
		return false
	}
	for _, r := range value {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-') {
			return false
		}
	}
	return true
}

func validCode(value string) bool {
	if len(value) < 6 || len(value) > 10 {
		return false
	}
	for i := range value {
		if value[i] < '0' || value[i] > '9' {
			return false
		}
	}
	return true
}

func validDeviceProofEncoding(value string) bool {
	if len(value) < 32 || len(value) > maxProofBytes {
		return false
	}
	parts := strings.Split(value, ".")
	if len(parts) != 3 {
		return false
	}
	decoded := make([][]byte, 3)
	for i, part := range parts {
		if part == "" {
			return false
		}
		b, err := base64.RawURLEncoding.Strict().DecodeString(part)
		if err != nil || len(b) == 0 || base64.RawURLEncoding.EncodeToString(b) != part {
			return false
		}
		decoded[i] = b
	}
	return len(decoded[2]) == 64
}
