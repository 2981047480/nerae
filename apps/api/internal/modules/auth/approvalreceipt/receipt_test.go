package approvalreceipt

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"
)

func testCrypto(t *testing.T) *Crypto {
	t.Helper()
	c, err := New(KeyConfig{
		IdempotencyHMACKey: bytes.Repeat([]byte{0x11}, 32),
		RequestHMACKey:     bytes.Repeat([]byte{0x22}, 32),
		AES256GCMKey:       bytes.Repeat([]byte{0x33}, 32),
	})
	if err != nil {
		t.Fatal("New failed")
	}
	return c
}

func validRequest() Request {
	return Request{
		IdempotencyKey: base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{0x44}, 24)),
		ApprovalID:     "approval_0123456789abcdefgh",
		ApprovalSecret: base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{0x55}, 32)),
		DeviceProof:    compactProof(),
	}
}

func compactProof() string {
	enc := base64.RawURLEncoding.EncodeToString
	return enc([]byte(`{"alg":"ES256","typ":"JWT"}`)) + "." + enc([]byte(`{"purpose":"approval_exchange"}`)) + "." + enc(bytes.Repeat([]byte{0x66}, 64))
}

func makeTestContext(t *testing.T, c *Crypto, req Request) (Binding, Context) {
	t.Helper()
	b, err := c.Summarize(req)
	if err != nil {
		t.Fatal("Summarize failed")
	}
	expires := time.Date(2026, 10, 31, 12, 30, 15, 123456000, time.UTC)
	ctx, err := c.NewContext("approval-key-v1", bytes.Repeat([]byte{0x71}, 32), bytes.Repeat([]byte{0x72}, 32), b,
		resultSession, "session_0123456789abcdefgh", expires)
	if err != nil {
		t.Fatal("NewContext failed")
	}
	return b, ctx
}

func TestSummarizeSeparatesKeyAndRequestBinding(t *testing.T) {
	c := testCrypto(t)
	req := validRequest()
	base, err := c.Summarize(req)
	if err != nil {
		t.Fatal(err)
	}
	if again, err := c.Summarize(req); err != nil || again != base {
		t.Fatal("same request did not produce stable binding")
	}
	changedKey := req
	changedKey.IdempotencyKey = base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{0x45}, 24))
	keyOnly, err := c.Summarize(changedKey)
	if err != nil || keyOnly.idempotencyHMAC == base.idempotencyHMAC || keyOnly.requestHMAC != base.requestHMAC {
		t.Fatal("idempotency-key binding was not independent")
	}
	for name, mutate := range map[string]func(*Request){
		"approval id": func(r *Request) { r.ApprovalID += "x" },
		"secret": func(r *Request) {
			r.ApprovalSecret = base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{0x57}, 32))
		},
		"proof": func(r *Request) { r.DeviceProof = strings.Replace(r.DeviceProof, "eyJ", "eXJ", 1) },
	} {
		t.Run(name, func(t *testing.T) {
			changed := req
			mutate(&changed)
			got, err := c.Summarize(changed)
			if err != nil || got.requestHMAC == base.requestHMAC {
				t.Fatal("request field did not change request digest")
			}
		})
	}
}

func TestSealOpenAndAADBinding(t *testing.T) {
	c := testCrypto(t)
	req := validRequest()
	_, ctx := makeTestContext(t, c, req)
	plain := []byte("opaque bearer response bytes")
	sealed, err := c.Seal(ctx, plain)
	if err != nil {
		t.Fatal("Seal failed")
	}
	opened, err := c.Open(ctx, sealed)
	if err != nil || !bytes.Equal(opened.Bytes(), plain) {
		t.Fatal("Open did not recover plaintext")
	}
	mutations := map[string]func(*Context){
		"subject scope":  func(c *Context) { c.subjectScope[0] ^= 1 },
		"approval scope": func(c *Context) { c.approvalScope[0] ^= 1 },
		"idempotency":    func(c *Context) { c.binding.idempotencyHMAC[0] ^= 1 },
		"request":        func(c *Context) { c.binding.requestHMAC[0] ^= 1 },
		"approval id":    func(c *Context) { c.binding.approvalID += "x" },
		"key id":         func(c *Context) { c.keyID = "approval-key-v2" },
		"result type":    func(c *Context) { c.resultType = "other" },
		"reference":      func(c *Context) { c.reference = "session_1123456789abcdefgh" },
		"expiry":         func(c *Context) { c.expiresAt = c.expiresAt.Add(time.Microsecond) },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			altered := ctx
			mutate(&altered)
			if _, err := c.Open(altered, sealed); !errors.Is(err, ErrInvalidReceipt) {
				t.Fatal("AAD change was accepted")
			}
		})
	}
	blob := sealed.Bytes()
	blob[len(blob)-1] ^= 1
	parsed, err := ParseSealed(blob)
	if err != nil {
		t.Fatal("framing-valid modified blob should parse before authentication")
	}
	if _, err := c.Open(ctx, parsed); !errors.Is(err, ErrInvalidReceipt) {
		t.Fatal("modified ciphertext authenticated")
	}
}

func TestValidationAndBounds(t *testing.T) {
	c := testCrypto(t)
	req := validRequest()
	for name, mutate := range map[string]func(*Request){
		"short id":         func(r *Request) { r.ApprovalID = "short" },
		"noncanonical key": func(r *Request) { r.IdempotencyKey += "=" },
		"short key":        func(r *Request) { r.IdempotencyKey = base64.RawURLEncoding.EncodeToString([]byte("tiny")) },
		"bad secret":       func(r *Request) { r.ApprovalSecret = strings.Repeat("A", 42) },
		"proof malformed":  func(r *Request) { r.DeviceProof = "not-a-jws" },
		"proof long":       func(r *Request) { r.DeviceProof = strings.Repeat("A", maxProofBytes+1) },
	} {
		t.Run(name, func(t *testing.T) {
			bad := req
			mutate(&bad)
			if _, err := c.Summarize(bad); !errors.Is(err, ErrInvalidInput) {
				t.Fatal("invalid request accepted")
			}
		})
	}
	binding, _ := makeTestContext(t, c, req)
	for name, args := range map[string]struct {
		key               string
		subject, approval []byte
		typ, ref          string
		expires           time.Time
	}{
		"zero subject":      {"approval-key-v1", make([]byte, 32), bytes.Repeat([]byte{1}, 32), resultSession, "session_0123456789abcdefgh", time.Now().UTC().Truncate(time.Microsecond).Add(time.Hour)},
		"short scope":       {"approval-key-v1", bytes.Repeat([]byte{1}, 31), bytes.Repeat([]byte{2}, 32), resultSession, "session_0123456789abcdefgh", time.Now().UTC().Truncate(time.Microsecond).Add(time.Hour)},
		"bad key id":        {"bad/key", bytes.Repeat([]byte{1}, 32), bytes.Repeat([]byte{2}, 32), resultSession, "session_0123456789abcdefgh", time.Now().UTC().Truncate(time.Microsecond).Add(time.Hour)},
		"wrong type":        {"approval-key-v1", bytes.Repeat([]byte{1}, 32), bytes.Repeat([]byte{2}, 32), "pending", "session_0123456789abcdefgh", time.Now().UTC().Truncate(time.Microsecond).Add(time.Hour)},
		"bad reference":     {"approval-key-v1", bytes.Repeat([]byte{1}, 32), bytes.Repeat([]byte{2}, 32), resultSession, "short", time.Now().UTC().Truncate(time.Microsecond).Add(time.Hour)},
		"nanosecond expiry": {"approval-key-v1", bytes.Repeat([]byte{1}, 32), bytes.Repeat([]byte{2}, 32), resultSession, "session_0123456789abcdefgh", time.Date(2026, 10, 31, 12, 30, 15, 123456789, time.UTC)},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := c.NewContext(args.key, args.subject, args.approval, binding, args.typ, args.ref, args.expires); !errors.Is(err, ErrInvalidInput) {
				t.Fatal("invalid context accepted")
			}
		})
	}
	_, ctx := makeTestContext(t, c, req)
	if _, err := c.Seal(ctx, nil); !errors.Is(err, ErrInvalidInput) {
		t.Fatal("empty plaintext accepted")
	}
	if _, err := c.Seal(ctx, bytes.Repeat([]byte{1}, maxPlaintext+1)); !errors.Is(err, ErrInvalidInput) {
		t.Fatal("oversized plaintext accepted")
	}
	maxBlob, err := c.Seal(ctx, bytes.Repeat([]byte{2}, maxPlaintext))
	if err != nil {
		t.Fatal("maximum plaintext rejected")
	}
	if _, err := ParseSealed(maxBlob.Bytes()); err != nil {
		t.Fatal("maximum valid sealed blob rejected")
	}
	for _, blob := range [][]byte{nil, []byte{1}, append([]byte{2}, bytes.Repeat([]byte{0}, 28)...), append([]byte{1}, bytes.Repeat([]byte{0}, 1+gcmNonceSize+gcmTagSize+maxPlaintext)...)} {
		if _, err := ParseSealed(blob); !errors.Is(err, ErrInvalidReceipt) {
			t.Fatal("malformed framing accepted")
		}
	}
}

func TestKeyConfigurationEpochAndRandomFailure(t *testing.T) {
	base := KeyConfig{bytes.Repeat([]byte{1}, 32), bytes.Repeat([]byte{2}, 32), bytes.Repeat([]byte{3}, 32)}
	for name, cfg := range map[string]KeyConfig{
		"short":     {bytes.Repeat([]byte{1}, 31), bytes.Repeat([]byte{2}, 32), bytes.Repeat([]byte{3}, 32)},
		"zero":      {make([]byte, 32), bytes.Repeat([]byte{2}, 32), bytes.Repeat([]byte{3}, 32)},
		"duplicate": {bytes.Repeat([]byte{1}, 32), bytes.Repeat([]byte{1}, 32), bytes.Repeat([]byte{3}, 32)},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := New(cfg); !errors.Is(err, ErrConfiguration) {
				t.Fatal("bad key configuration accepted")
			}
		})
	}
	c, err := New(base)
	if err != nil {
		t.Fatal(err)
	}
	tag, err := c.EpochTag("approval-key-v1")
	if err != nil {
		t.Fatal(err)
	}
	if again, _ := c.EpochTag("approval-key-v1"); again != tag {
		t.Fatal("epoch tag was unstable")
	}
	if changed, _ := c.EpochTag("approval-key-v2"); changed == tag {
		t.Fatal("keyID not bound to epoch")
	}
	for _, bad := range []string{"", "bad/key", strings.Repeat("a", 65), "é"} {
		if _, err := c.EpochTag(bad); !errors.Is(err, ErrConfiguration) {
			t.Fatal("invalid keyID accepted")
		}
	}
	req := validRequest()
	_, ctx := makeTestContext(t, c, req)
	c.random = failingReader{}
	if _, err := c.Seal(ctx, []byte("x")); !errors.Is(err, ErrInvalidReceipt) {
		t.Fatal("random source failure did not fail closed")
	}
	var zero Crypto
	if _, err := zero.EpochTag("approval-key-v1"); !errors.Is(err, ErrConfiguration) {
		t.Fatal("zero crypto accepted")
	}
	if _, err := zero.Open(ctx, Sealed{}); !errors.Is(err, ErrInvalidReceipt) {
		t.Fatal("zero crypto opened receipt")
	}
}

func TestKeySeparationAndNonceUniqueness(t *testing.T) {
	baseConfig := KeyConfig{bytes.Repeat([]byte{0x11}, 32), bytes.Repeat([]byte{0x22}, 32), bytes.Repeat([]byte{0x33}, 32)}
	base, err := New(baseConfig)
	if err != nil {
		t.Fatal(err)
	}
	req := validRequest()
	baseBinding, baseContext := makeTestContext(t, base, req)
	first, err := base.Seal(baseContext, []byte("stable-session-result"))
	if err != nil {
		t.Fatal(err)
	}
	second, err := base.Seal(baseContext, []byte("stable-session-result"))
	if err != nil {
		t.Fatal(err)
	}
	firstBytes, secondBytes := first.Bytes(), second.Bytes()
	if bytes.Equal(firstBytes[1:1+gcmNonceSize], secondBytes[1:1+gcmNonceSize]) {
		t.Fatal("consecutive seals reused the same nonce")
	}
	if bytes.Equal(firstBytes, secondBytes) {
		t.Fatal("consecutive seals produced identical ciphertext")
	}
	for _, role := range []string{"idempotency", "request", "aead"} {
		t.Run(role, func(t *testing.T) {
			cfg := KeyConfig{
				IdempotencyHMACKey: append([]byte(nil), baseConfig.IdempotencyHMACKey...),
				RequestHMACKey:     append([]byte(nil), baseConfig.RequestHMACKey...),
				AES256GCMKey:       append([]byte(nil), baseConfig.AES256GCMKey...),
			}
			switch role {
			case "idempotency":
				cfg.IdempotencyHMACKey[0] ^= 1
			case "request":
				cfg.RequestHMACKey[0] ^= 1
			case "aead":
				cfg.AES256GCMKey[0] ^= 1
			}
			other, err := New(cfg)
			if err != nil {
				t.Fatal(err)
			}
			otherBinding, err := other.Summarize(req)
			if err != nil {
				t.Fatal(err)
			}
			switch role {
			case "idempotency":
				if otherBinding.idempotencyHMAC == baseBinding.idempotencyHMAC || otherBinding.requestHMAC != baseBinding.requestHMAC {
					t.Fatal("idempotency key did not have independent effect")
				}
			case "request":
				if otherBinding.idempotencyHMAC != baseBinding.idempotencyHMAC || otherBinding.requestHMAC == baseBinding.requestHMAC {
					t.Fatal("request key did not have independent effect")
				}
			case "aead":
				if otherBinding != baseBinding {
					t.Fatal("AEAD key changed request digests")
				}
				if _, err := other.Open(baseContext, first); !errors.Is(err, ErrInvalidReceipt) {
					t.Fatal("different AEAD key opened ciphertext")
				}
			}
			tagA, errA := base.EpochTag("approval-key-v1")
			tagB, errB := other.EpochTag("approval-key-v1")
			if errA != nil || errB != nil || tagA == tagB {
				t.Fatal("key change was not reflected in epoch tag")
			}
		})
	}
}

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, errors.New("private entropy failure") }

func TestSensitiveValuesStayRedacted(t *testing.T) {
	c := testCrypto(t)
	req := validRequest()
	b, ctx := makeTestContext(t, c, req)
	sealed, err := c.Seal(ctx, []byte("sensitive-token-marker"))
	if err != nil {
		t.Fatal(err)
	}
	plain, err := c.Open(ctx, sealed)
	if err != nil {
		t.Fatal(err)
	}
	values := []any{KeyConfig{[]byte("secret-key-marker"), []byte("secret-key-two"), []byte("secret-key-three")}, req, b, ctx, sealed, plain, c, Crypto{}}
	for _, value := range values {
		formatted := fmt.Sprintf("%v %+v %#v", value, value, value)
		if strings.Contains(formatted, req.IdempotencyKey) || strings.Contains(formatted, req.ApprovalID) ||
			strings.Contains(formatted, req.ApprovalSecret) || strings.Contains(formatted, req.DeviceProof) ||
			strings.Contains(formatted, "sensitive-token-marker") || strings.Contains(formatted, "secret-key-marker") {
			t.Fatal("formatting exposed sensitive input")
		}
		if _, err := json.Marshal(value); err == nil {
			t.Fatal("sensitive value was JSON serializable")
		}
		var buf bytes.Buffer
		logger := slog.New(slog.NewTextHandler(&buf, nil))
		logger.Info("value", "v", value)
		if strings.Contains(buf.String(), req.IdempotencyKey) || strings.Contains(buf.String(), req.ApprovalSecret) ||
			strings.Contains(buf.String(), req.DeviceProof) || strings.Contains(buf.String(), "sensitive-token-marker") {
			t.Fatal("slog exposed sensitive input")
		}
	}
}
