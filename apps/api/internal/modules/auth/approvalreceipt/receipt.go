// Package approvalreceipt provides cryptographic summaries and encrypted
// success receipts for a future approval-exchange coordinator. It does not
// verify device signatures, current authorization, proof replay state, database
// transactions, or permission to release a credential. Open must only be used
// after those checks have independently succeeded in the owning coordinator.
package approvalreceipt

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
)

var (
	ErrConfiguration  = errors.New("approval receipt configuration unavailable")
	ErrInvalidInput   = errors.New("invalid approval receipt input")
	ErrInvalidReceipt = errors.New("approval receipt unavailable")
)

const (
	maxOpaqueID    = 128
	maxProofBytes  = 4096
	maxPlaintext   = 8192
	maxReference   = 128
	receiptVersion = byte(1)
	gcmNonceSize   = 12
	gcmTagSize     = 16
	keyDomain      = "nerae.auth.approval-receipt.idempotency.v1\x00"
	requestDomain  = "nerae.auth.approval-receipt.request.v1\x00"
	aadDomain      = "nerae.auth.approval-receipt.aad.v1\x00"
	epochKeyDomain = "nerae.auth.approval-receipt.epoch-key.v1\x00"
	epochDomain    = "nerae.auth.approval-receipt.epoch.v1\x00"
	resultSession  = "issued_session"
)

// KeyConfig uses three independently managed 256-bit keys. Validation checks
// size, nonzero content, and distinctness; it cannot prove entropy.
type KeyConfig struct {
	IdempotencyHMACKey []byte
	RequestHMACKey     []byte
	AES256GCMKey       []byte
}

func (KeyConfig) String() string               { return "ApprovalReceiptKeyConfig{redacted}" }
func (KeyConfig) GoString() string             { return "ApprovalReceiptKeyConfig{redacted}" }
func (KeyConfig) MarshalJSON() ([]byte, error) { return nil, ErrConfiguration }
func (KeyConfig) LogValue() slog.Value         { return slog.StringValue("ApprovalReceiptKeyConfig{redacted}") }

// Request contains the exact request values. DeviceProof is shape-checked as
// compact base64url data only; signature and claims verification are outside
// this package.
type Request struct {
	IdempotencyKey string
	ApprovalID     string
	ApprovalSecret string
	DeviceProof    string
}

func (Request) String() string               { return "ApprovalReceiptRequest{redacted}" }
func (Request) GoString() string             { return "ApprovalReceiptRequest{redacted}" }
func (Request) MarshalJSON() ([]byte, error) { return nil, ErrInvalidInput }
func (Request) LogValue() slog.Value         { return slog.StringValue("ApprovalReceiptRequest{redacted}") }

// Binding holds only keyed digests and the approval identifier.
type Binding struct {
	approvalID      string
	idempotencyHMAC [sha256.Size]byte
	requestHMAC     [sha256.Size]byte
}

func (Binding) String() string                         { return "ApprovalReceiptBinding{redacted}" }
func (Binding) GoString() string                       { return "ApprovalReceiptBinding{redacted}" }
func (Binding) MarshalJSON() ([]byte, error)           { return nil, ErrInvalidReceipt }
func (Binding) LogValue() slog.Value                   { return slog.StringValue("ApprovalReceiptBinding{redacted}") }
func (b Binding) ApprovalID() string                   { return b.approvalID }
func (b Binding) IdempotencyDigest() [sha256.Size]byte { return b.idempotencyHMAC }
func (b Binding) RequestDigest() [sha256.Size]byte     { return b.requestHMAC }

// Context binds encrypted bytes to server-derived subject and approval scopes,
// the exact request binding, result session ID, key ID, and UTC expiry. Scopes
// must be nonzero 32-byte protected values, not caller-supplied identities.
type Context struct {
	subjectScope  [sha256.Size]byte
	approvalScope [sha256.Size]byte
	binding       Binding
	keyID         string
	resultType    string
	reference     string
	expiresAt     time.Time
}

func (Context) String() string               { return "ApprovalReceiptContext{redacted}" }
func (Context) GoString() string             { return "ApprovalReceiptContext{redacted}" }
func (Context) MarshalJSON() ([]byte, error) { return nil, ErrInvalidReceipt }
func (Context) LogValue() slog.Value         { return slog.StringValue("ApprovalReceiptContext{redacted}") }
func (c Context) KeyID() string              { return c.keyID }
func (c Context) ResultType() string         { return c.resultType }
func (c Context) ResultReference() string    { return c.reference }
func (c Context) ExpiresAt() time.Time       { return c.expiresAt }

// Sealed is a versioned nonce+ciphertext blob. Bytes returns a defensive copy.
type Sealed struct{ blob []byte }

func (Sealed) String() string               { return "ApprovalReceiptSealed{redacted}" }
func (Sealed) GoString() string             { return "ApprovalReceiptSealed{redacted}" }
func (Sealed) MarshalJSON() ([]byte, error) { return nil, ErrInvalidReceipt }
func (Sealed) LogValue() slog.Value         { return slog.StringValue("ApprovalReceiptSealed{redacted}") }
func (s Sealed) Bytes() []byte              { return append([]byte(nil), s.blob...) }

// ParseSealed performs bounded framing/version checks and copies persisted
// bytes. Authentication is performed only by Open.
func ParseSealed(blob []byte) (Sealed, error) {
	min, max := 1+gcmNonceSize+gcmTagSize, 1+gcmNonceSize+gcmTagSize+maxPlaintext
	if len(blob) < min || len(blob) > max || blob[0] != receiptVersion {
		return Sealed{}, ErrInvalidReceipt
	}
	return Sealed{blob: append([]byte(nil), blob...)}, nil
}

// Plaintext is sensitive receipt material. Callers must keep it inside the
// authorized coordinator and never log or serialize it.
type Plaintext struct{ data []byte }

func (Plaintext) String() string               { return "ApprovalReceiptPlaintext{redacted}" }
func (Plaintext) GoString() string             { return "ApprovalReceiptPlaintext{redacted}" }
func (Plaintext) MarshalJSON() ([]byte, error) { return nil, ErrInvalidReceipt }
func (Plaintext) LogValue() slog.Value         { return slog.StringValue("ApprovalReceiptPlaintext{redacted}") }
func (p Plaintext) Bytes() []byte              { return append([]byte(nil), p.data...) }

type Crypto struct {
	idempotencyKey [sha256.Size]byte
	requestKey     [sha256.Size]byte
	aesKey         [sha256.Size]byte
	random         io.Reader
}

func (Crypto) String() string               { return "ApprovalReceiptCrypto{redacted}" }
func (Crypto) GoString() string             { return "ApprovalReceiptCrypto{redacted}" }
func (Crypto) MarshalJSON() ([]byte, error) { return nil, ErrConfiguration }
func (Crypto) LogValue() slog.Value         { return slog.StringValue("ApprovalReceiptCrypto{redacted}") }

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

// EpochTag returns a non-reversible verifier for all three keys and keyID. It
// neither initializes nor rotates persistent state.
func (c *Crypto) EpochTag(keyID string) ([sha256.Size]byte, error) {
	if !c.validKeys() || !validKeyID(keyID) {
		return [sha256.Size]byte{}, ErrConfiguration
	}
	parts := [3][sha256.Size]byte{
		keyTag(c.idempotencyKey[:], "idempotency"), keyTag(c.requestKey[:], "request"), keyTag(c.aesKey[:], "aead"),
	}
	h := sha256.New()
	_, _ = io.WriteString(h, epochDomain)
	writePart(h, []byte(keyID))
	for i := range parts {
		writePart(h, parts[i][:])
	}
	var out [sha256.Size]byte
	copy(out[:], h.Sum(nil))
	return out, nil
}

func (c *Crypto) Summarize(req Request) (Binding, error) {
	if !c.validKeys() {
		return Binding{}, ErrConfiguration
	}
	if !validOpaqueID(req.ApprovalID) || !validIdempotencyKey(req.IdempotencyKey) ||
		!validApprovalSecret(req.ApprovalSecret) || !validProof(req.DeviceProof) {
		return Binding{}, ErrInvalidInput
	}
	idem := hmac.New(sha256.New, c.idempotencyKey[:])
	_, _ = io.WriteString(idem, keyDomain)
	writePart(idem, []byte(req.IdempotencyKey))
	request := hmac.New(sha256.New, c.requestKey[:])
	_, _ = io.WriteString(request, requestDomain)
	writePart(request, []byte(req.ApprovalID))
	writePart(request, []byte(req.ApprovalSecret))
	writePart(request, []byte(req.DeviceProof))
	var idemDigest, requestDigest [sha256.Size]byte
	copy(idemDigest[:], idem.Sum(nil))
	copy(requestDigest[:], request.Sum(nil))
	return Binding{approvalID: req.ApprovalID, idempotencyHMAC: idemDigest, requestHMAC: requestDigest}, nil
}

// NewContext accepts only an issued-session receipt. Pending/denied/expired
// poll results are intentionally outside this primitive; their dynamic
// idempotency semantics must be settled by the owning coordinator/contract.
func (c *Crypto) NewContext(keyID string, subjectScope, approvalScope []byte, binding Binding, resultType, reference string, expiresAt time.Time) (Context, error) {
	if !c.validKeys() || !validKeyID(keyID) || len(subjectScope) != sha256.Size || len(approvalScope) != sha256.Size ||
		allZero(subjectScope) || allZero(approvalScope) || !validBinding(binding) || resultType != resultSession ||
		!validOpaqueID(reference) || expiresAt.IsZero() || expiresAt.Location() != time.UTC || expiresAt.Nanosecond()%int(time.Microsecond) != 0 {
		return Context{}, ErrInvalidInput
	}
	var subject, approval [sha256.Size]byte
	copy(subject[:], subjectScope)
	copy(approval[:], approvalScope)
	return Context{subjectScope: subject, approvalScope: approval, binding: binding, keyID: keyID,
		resultType: resultType, reference: reference, expiresAt: expiresAt}, nil
}

func (c *Crypto) Seal(ctx Context, plaintext []byte) (Sealed, error) {
	if !c.validKeys() || c.random == nil || !validContext(ctx) || len(plaintext) == 0 || len(plaintext) > maxPlaintext {
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

// Open authenticates and decrypts bytes only. It does not check expiry or
// authorize release; callers must reverify the original proof, JTI marker,
// current user/identity/device/session state, and transaction outcome first.
func (c *Crypto) Open(ctx Context, sealed Sealed) (Plaintext, error) {
	if c == nil || !c.validKeys() || !validContext(ctx) {
		return Plaintext{}, ErrInvalidReceipt
	}
	aead, err := c.aead()
	if err != nil {
		return Plaintext{}, ErrInvalidReceipt
	}
	min, max := 1+aead.NonceSize()+aead.Overhead(), 1+aead.NonceSize()+maxPlaintext+aead.Overhead()
	if len(sealed.blob) < min || len(sealed.blob) > max || sealed.blob[0] != receiptVersion {
		return Plaintext{}, ErrInvalidReceipt
	}
	nonce := sealed.blob[1 : 1+aead.NonceSize()]
	data, err := aead.Open(nil, nonce, sealed.blob[1+aead.NonceSize():], associatedData(ctx))
	if err != nil || len(data) == 0 || len(data) > maxPlaintext {
		return Plaintext{}, ErrInvalidReceipt
	}
	return Plaintext{data: data}, nil
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

func validContext(c Context) bool {
	return !allZero(c.subjectScope[:]) && !allZero(c.approvalScope[:]) && validBinding(c.binding) && validKeyID(c.keyID) &&
		c.resultType == resultSession && validOpaqueID(c.reference) && !c.expiresAt.IsZero() &&
		c.expiresAt.Location() == time.UTC && c.expiresAt.Nanosecond()%int(time.Microsecond) == 0
}

func associatedData(c Context) []byte {
	out := append([]byte(nil), aadDomain...)
	writeBytes(&out, []byte(c.keyID))
	writeBytes(&out, c.subjectScope[:])
	writeBytes(&out, c.approvalScope[:])
	writeBytes(&out, c.binding.idempotencyHMAC[:])
	writeBytes(&out, c.binding.requestHMAC[:])
	writeBytes(&out, []byte(c.binding.approvalID))
	writeBytes(&out, []byte(c.resultType))
	writeBytes(&out, []byte(c.reference))
	var micros [8]byte
	binary.BigEndian.PutUint64(micros[:], uint64(c.expiresAt.UnixMicro()))
	writeBytes(&out, micros[:])
	return out
}

func validBinding(b Binding) bool {
	return validOpaqueID(b.approvalID) && b.idempotencyHMAC != ([sha256.Size]byte{}) && b.requestHMAC != ([sha256.Size]byte{})
}

func keyTag(key []byte, role string) [sha256.Size]byte {
	h := hmac.New(sha256.New, key)
	_, _ = io.WriteString(h, epochKeyDomain)
	writePart(h, []byte(role))
	var out [sha256.Size]byte
	copy(out[:], h.Sum(nil))
	return out
}

func validKeyID(s string) bool {
	if len(s) == 0 || len(s) > 64 {
		return false
	}
	for i := range s {
		c := s[i]
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_' || c == '-' || c == '.') {
			return false
		}
	}
	return true
}

func validOpaqueID(s string) bool {
	if len(s) < 22 || len(s) > maxOpaqueID {
		return false
	}
	for i := range s {
		c := s[i]
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_' || c == '-') {
			return false
		}
	}
	return true
}

func validIdempotencyKey(s string) bool {
	if len(s) < 22 || len(s) > 128 {
		return false
	}
	b, err := base64.RawURLEncoding.Strict().DecodeString(s)
	return err == nil && len(b) >= 16 && len(b) <= 96 && base64.RawURLEncoding.EncodeToString(b) == s
}

func validApprovalSecret(s string) bool {
	if len(s) != 43 {
		return false
	}
	b, err := base64.RawURLEncoding.Strict().DecodeString(s)
	return err == nil && len(b) == 32 && base64.RawURLEncoding.EncodeToString(b) == s
}

func validProof(s string) bool {
	if len(s) < 32 || len(s) > maxProofBytes {
		return false
	}
	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return false
	}
	for i, p := range parts {
		if p == "" {
			return false
		}
		b, err := base64.RawURLEncoding.Strict().DecodeString(p)
		if err != nil || len(b) == 0 || base64.RawURLEncoding.EncodeToString(b) != p || (i == 2 && len(b) != 64) {
			return false
		}
	}
	return true
}

func allZero(b []byte) bool {
	var v byte
	for _, x := range b {
		v |= x
	}
	return v == 0
}

func writePart(dst hash.Hash, b []byte) {
	var n [4]byte
	binary.BigEndian.PutUint32(n[:], uint32(len(b)))
	_, _ = dst.Write(n[:])
	_, _ = dst.Write(b)
}

func writeBytes(dst *[]byte, b []byte) {
	var n [4]byte
	binary.BigEndian.PutUint32(n[:], uint32(len(b)))
	*dst = append(*dst, n[:]...)
	*dst = append(*dst, b...)
}
