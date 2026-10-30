package loginreceipt

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"
)

func testKeys() KeyConfig {
	return KeyConfig{
		IdempotencyHMACKey: bytes.Repeat([]byte{0x11}, 32),
		RequestHMACKey:     bytes.Repeat([]byte{0x22}, 32),
		AES256GCMKey:       bytes.Repeat([]byte{0x33}, 32),
	}
}

func testRequest() Request {
	return Request{
		IdempotencyKey: base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{0x41}, 16)),
		ChallengeID:    "AbCdEf0123456789_aBcDE",
		Code:           "000123",
		DeviceProof:    testProof(1),
	}
}

func testProof(payloadSize int) string {
	enc := base64.RawURLEncoding.EncodeToString
	header := enc([]byte(`{"alg":"ES256","typ":"JWT"}`))
	payload := enc(bytes.Repeat([]byte{'x'}, payloadSize))
	sig := enc(make([]byte, 64))
	return header + "." + payload + "." + sig
}

func newTestCrypto(t *testing.T) *Crypto {
	t.Helper()
	c, err := New(testKeys())
	if err != nil {
		t.Fatal("test crypto setup failed")
	}
	return c
}

func TestNewRequiresThreeDistinct256BitKeysAndCopiesThem(t *testing.T) {
	base := testKeys()
	if _, err := New(KeyConfig{}); !errors.Is(err, ErrConfiguration) {
		t.Fatal("empty key config accepted")
	}
	for _, mutate := range []func(*KeyConfig){
		func(k *KeyConfig) { k.IdempotencyHMACKey = k.IdempotencyHMACKey[:31] },
		func(k *KeyConfig) { k.RequestHMACKey = k.RequestHMACKey[:31] },
		func(k *KeyConfig) { k.AES256GCMKey = k.AES256GCMKey[:31] },
		func(k *KeyConfig) { k.RequestHMACKey = append([]byte(nil), k.IdempotencyHMACKey...) },
		func(k *KeyConfig) { k.AES256GCMKey = append([]byte(nil), k.IdempotencyHMACKey...) },
		func(k *KeyConfig) { k.AES256GCMKey = append([]byte(nil), k.RequestHMACKey...) },
		func(k *KeyConfig) { k.IdempotencyHMACKey = make([]byte, 32) },
		func(k *KeyConfig) { k.RequestHMACKey = make([]byte, 32) },
		func(k *KeyConfig) { k.AES256GCMKey = make([]byte, 32) },
	} {
		cfg := testKeys()
		mutate(&cfg)
		if _, err := New(cfg); !errors.Is(err, ErrConfiguration) {
			t.Fatal("invalid or repeated key accepted")
		}
	}
	c, err := New(base)
	if err != nil {
		t.Fatal("valid independent keys rejected")
	}
	base.IdempotencyHMACKey[0] ^= 0xff
	if c.idempotencyKey[0] != 0x11 {
		t.Fatal("crypto retained caller-owned key slice")
	}
}

func TestSummarizeCanonicalValidationAndDomainBinding(t *testing.T) {
	c := newTestCrypto(t)
	valid := testRequest()
	base, err := c.Summarize(valid)
	if err != nil {
		t.Fatal("valid request rejected")
	}
	mutations := []struct {
		name string
		edit func(*Request)
	}{
		{"noncanonical key", func(r *Request) { r.IdempotencyKey += "=" }},
		{"short key", func(r *Request) { r.IdempotencyKey = base64.RawURLEncoding.EncodeToString([]byte("short")) }},
		{"padded challenge", func(r *Request) { r.ChallengeID += "=" }},
		{"short challenge", func(r *Request) { r.ChallengeID = "short" }},
		{"invalid challenge alphabet", func(r *Request) { r.ChallengeID = strings.Repeat("x", 21) + "/" }},
		{"short code", func(r *Request) { r.Code = "12345" }},
		{"long code", func(r *Request) { r.Code = "12345678901" }},
		{"nondigit code", func(r *Request) { r.Code = "12a456" }},
		{"oversized proof", func(r *Request) { r.DeviceProof = strings.Repeat("a", maxProofBytes+1) }},
		{"malformed proof parts", func(r *Request) { r.DeviceProof = "a.b" }},
		{"padded proof segment", func(r *Request) { r.DeviceProof = strings.Replace(r.DeviceProof, ".", "=.", 1) }},
		{"invalid proof alphabet", func(r *Request) { r.DeviceProof = "!" + r.DeviceProof[1:] }},
		{"wrong signature length", func(r *Request) {
			r.DeviceProof = testProof(0)[:strings.LastIndex(testProof(0), ".")+1] + base64.RawURLEncoding.EncodeToString(make([]byte, 63))
		}},
	}
	for _, tc := range mutations {
		t.Run(tc.name, func(t *testing.T) {
			input := valid
			tc.edit(&input)
			if _, err := c.Summarize(input); !errors.Is(err, ErrInvalidInput) {
				t.Fatal("invalid request accepted")
			}
		})
	}

	changedKey := valid
	changedKey.IdempotencyKey = base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{0x42}, 16))
	keyBinding, err := c.Summarize(changedKey)
	if err != nil || keyBinding.idempotencyHMAC == base.idempotencyHMAC || keyBinding.requestHMAC != base.requestHMAC {
		t.Fatal("idempotency digest is not separated from request digest")
	}
	for _, edit := range []func(*Request){
		func(r *Request) { r.ChallengeID += "X" },
		func(r *Request) { r.Code = "000124" },
		func(r *Request) { r.DeviceProof = testProof(2) },
	} {
		input := valid
		edit(&input)
		binding, err := c.Summarize(input)
		if err != nil || binding.idempotencyHMAC != base.idempotencyHMAC || binding.requestHMAC == base.requestHMAC {
			t.Fatal("request digest omitted or conflated a protected field")
		}
	}
}

func TestReceiptSealOpenAndNonceUniqueness(t *testing.T) {
	c := newTestCrypto(t)
	binding, err := c.Summarize(testRequest())
	if err != nil {
		t.Fatal("request summary failed")
	}
	ctx, err := c.NewContext(bytes.Repeat([]byte{0x61}, 32), binding, ResultAuthenticated, "receipt-ref-01", time.Date(2026, 10, 30, 12, 13, 14, 123456000, time.UTC))
	if err != nil {
		t.Fatal("receipt context rejected")
	}
	plain := []byte(`{"kind":"synthetic"}`)
	sealed, err := c.Seal(ctx, plain)
	if err != nil {
		t.Fatal("receipt sealing failed")
	}
	opened, err := c.Open(ctx, sealed)
	if err != nil || !bytes.Equal(opened.Bytes(), plain) {
		t.Fatal("receipt did not round trip")
	}
	openedBytes := opened.Bytes()
	openedBytes[0] ^= 0xff
	openedAgain, err := c.Open(ctx, sealed)
	if err != nil || !bytes.Equal(openedAgain.Bytes(), plain) {
		t.Fatal("Open exposed internal plaintext storage")
	}
	persisted := sealed.Bytes()
	rehydrated, err := ParseSealed(persisted)
	if err != nil {
		t.Fatal("persisted receipt could not be reconstructed")
	}
	persisted[1] ^= 0xff
	reopened, err := c.Open(ctx, rehydrated)
	if err != nil || !bytes.Equal(reopened.Bytes(), plain) {
		t.Fatal("ParseSealed retained caller-owned ciphertext storage")
	}
	badBlob := rehydrated.Bytes()
	badBlob[len(badBlob)-1] ^= 1
	badSealed, err := ParseSealed(badBlob)
	if err != nil {
		t.Fatal("structurally valid tampered blob rejected before authentication")
	}
	if _, err := c.Open(ctx, badSealed); !errors.Is(err, ErrInvalidReceipt) {
		t.Fatal("tampered rehydrated receipt authenticated")
	}

	nonces := map[string]struct{}{}
	for i := 0; i < 128; i++ {
		item, err := c.Seal(ctx, []byte("x"))
		if err != nil {
			t.Fatal("nonce uniqueness sample failed")
		}
		blob := item.Bytes()
		nonce := string(blob[1 : 1+12])
		if _, exists := nonces[nonce]; exists {
			t.Fatal("CSPRNG produced a repeated GCM nonce")
		}
		nonces[nonce] = struct{}{}
	}
}

func TestParseSealedRejectsInvalidFramingAndVersion(t *testing.T) {
	valid := make([]byte, 1+gcmNonceSize+gcmTagSize)
	valid[0] = receiptVersion
	if _, err := ParseSealed(valid); err != nil {
		t.Fatal("minimum structurally valid blob rejected")
	}
	for _, blob := range [][]byte{
		{},
		make([]byte, 1+gcmNonceSize+gcmTagSize-1),
		append([]byte{receiptVersion + 1}, make([]byte, gcmNonceSize+gcmTagSize)...),
		make([]byte, 1+gcmNonceSize+gcmTagSize+maxPlaintext+1),
	} {
		if _, err := ParseSealed(blob); !errors.Is(err, ErrInvalidReceipt) {
			t.Fatal("invalid framed receipt accepted")
		}
	}
}

func TestRejectedReceiptIsChallengeBoundAndNeverCarriesCredentials(t *testing.T) {
	c := newTestCrypto(t)
	binding, err := c.Summarize(testRequest())
	if err != nil {
		t.Fatal("request summary failed")
	}
	scope := bytes.Repeat([]byte{0x51}, 32)
	expires := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	if _, err := c.NewContext(scope, binding, ResultRejected, "other-challenge-reference", expires); !errors.Is(err, ErrInvalidInput) {
		t.Fatal("rejected receipt reference was not bound to its challenge")
	}
	rejected, err := c.NewContext(scope, binding, ResultRejected, binding.challengeID, expires)
	if err != nil || rejected.ResultType() != ResultRejected {
		t.Fatal("challenge-bound rejected result context failed")
	}
	if _, err := c.Seal(rejected, []byte("must not contain credentials")); !errors.Is(err, ErrInvalidInput) {
		t.Fatal("rejected receipt accepted credential plaintext")
	}
	sealed, err := c.Seal(rejected, nil)
	if err != nil {
		t.Fatal("empty rejected receipt could not be encrypted")
	}
	opened, err := c.Open(rejected, sealed)
	if err != nil || len(opened.Bytes()) != 0 {
		t.Fatal("rejected receipt did not round trip as empty result")
	}
	successContext, err := c.NewContext(scope, binding, ResultAuthenticated, binding.challengeID, expires)
	if err != nil {
		t.Fatal("alternate AAD fixture failed")
	}
	if _, err := c.Open(successContext, sealed); !errors.Is(err, ErrInvalidReceipt) {
		t.Fatal("rejected receipt opened as authenticated success")
	}
}

func TestZeroValueCryptoFailsClosed(t *testing.T) {
	c := &Crypto{}
	if _, err := c.aead(); !errors.Is(err, ErrConfiguration) {
		t.Fatal("zero-value crypto constructed an AEAD")
	}
	if _, err := c.Summarize(testRequest()); !errors.Is(err, ErrConfiguration) {
		t.Fatal("zero-value crypto summarized requests")
	}
	if _, err := c.Open(Context{}, Sealed{}); !errors.Is(err, ErrInvalidReceipt) {
		t.Fatal("zero-value crypto opened a receipt")
	}
}

func TestReceiptRejectsAADChangesCiphertextAndWrongKey(t *testing.T) {
	c := newTestCrypto(t)
	req := testRequest()
	binding, err := c.Summarize(req)
	if err != nil {
		t.Fatal("request summary failed")
	}
	expiry := time.Date(2026, 10, 30, 12, 13, 14, 123456000, time.UTC)
	ctx, err := c.NewContext(bytes.Repeat([]byte{0x61}, 32), binding, ResultAuthenticated, "ref-1", expiry)
	if err != nil {
		t.Fatal("receipt context rejected")
	}
	sealed, err := c.Seal(ctx, []byte("synthetic credential payload"))
	if err != nil {
		t.Fatal("receipt sealing failed")
	}
	otherRequest := req
	otherRequest.Code = "999999"
	otherBinding, err := c.Summarize(otherRequest)
	if err != nil {
		t.Fatal("second request summary failed")
	}
	otherChallenge := req
	otherChallenge.ChallengeID += "X"
	otherChallengeBinding, err := c.Summarize(otherChallenge)
	if err != nil {
		t.Fatal("second challenge summary failed")
	}
	wrongContexts := []Context{}
	appendContext := func(scope []byte, b Binding, kind ResultType, ref string, exp time.Time) {
		value, e := c.NewContext(scope, b, kind, ref, exp)
		if e != nil {
			t.Fatal("valid altered context rejected")
		}
		wrongContexts = append(wrongContexts, value)
	}
	appendContext(bytes.Repeat([]byte{0x62}, 32), binding, ResultAuthenticated, "ref-1", expiry)
	appendContext(bytes.Repeat([]byte{0x61}, 32), otherBinding, ResultAuthenticated, "ref-1", expiry)
	appendContext(bytes.Repeat([]byte{0x61}, 32), otherChallengeBinding, ResultAuthenticated, "ref-1", expiry)
	appendContext(bytes.Repeat([]byte{0x61}, 32), binding, ResultApprovalRequired, "ref-1", expiry)
	appendContext(bytes.Repeat([]byte{0x61}, 32), binding, ResultAuthenticated, "ref-2", expiry)
	appendContext(bytes.Repeat([]byte{0x61}, 32), binding, ResultAuthenticated, "ref-1", expiry.Add(time.Microsecond))
	for _, altered := range wrongContexts {
		if _, err := c.Open(altered, sealed); !errors.Is(err, ErrInvalidReceipt) {
			t.Fatal("receipt opened under altered AAD")
		}
	}

	for offset := range sealed.blob {
		corrupt := sealed.Bytes()
		corrupt[offset] ^= 1
		if _, err := c.Open(ctx, Sealed{blob: corrupt}); !errors.Is(err, ErrInvalidReceipt) {
			t.Fatalf("tampered ciphertext byte %d accepted", offset)
		}
	}
	wrongKeys := testKeys()
	wrongKeys.AES256GCMKey = bytes.Repeat([]byte{0x34}, 32)
	wrongCipher, err := New(wrongKeys)
	if err != nil {
		t.Fatal("wrong-key sealer setup failed")
	}
	if _, err := wrongCipher.Open(ctx, sealed); !errors.Is(err, ErrInvalidReceipt) {
		t.Fatal("receipt opened with another AES key")
	}
}

func TestContextAndPlaintextBounds(t *testing.T) {
	c := newTestCrypto(t)
	request := testRequest()
	request.Code = "0123456789"
	request.IdempotencyKey = base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{0x7f}, 96))
	request.ChallengeID = strings.Repeat("C", maxChallengeID)
	request.DeviceProof = testProof(2979)
	if len(request.DeviceProof) != maxProofBytes || !validDeviceProofEncoding(request.DeviceProof) {
		t.Fatal("maximum device proof length was not constructed as a valid boundary")
	}
	if _, err := c.Summarize(request); err != nil {
		t.Fatal("maximum valid request fields rejected")
	}
	if validDeviceProofEncoding(testProof(2980)) {
		t.Fatal("oversized encoded device proof accepted")
	}
	binding, err := c.Summarize(testRequest())
	if err != nil {
		t.Fatal("request summary failed")
	}
	expiry := time.Date(2026, 10, 30, 0, 0, 0, 0, time.UTC)
	valid := func(scope []byte, kind ResultType, ref string, exp time.Time) (Context, error) {
		return c.NewContext(scope, binding, kind, ref, exp)
	}
	if _, err := valid(make([]byte, 32), ResultAuthenticated, "ref", expiry); !errors.Is(err, ErrInvalidInput) {
		t.Fatal("zero scope accepted")
	}
	if _, err := valid(make([]byte, 31), ResultAuthenticated, "ref", expiry); !errors.Is(err, ErrInvalidInput) {
		t.Fatal("short scope accepted")
	}
	if _, err := valid(bytes.Repeat([]byte{1}, 32), ResultType("session-token"), "ref", expiry); !errors.Is(err, ErrInvalidInput) {
		t.Fatal("unknown result type accepted")
	}
	if _, err := valid(bytes.Repeat([]byte{1}, 32), ResultAuthenticated, "", expiry); !errors.Is(err, ErrInvalidInput) {
		t.Fatal("empty result reference accepted")
	}
	if _, err := valid(bytes.Repeat([]byte{1}, 32), ResultAuthenticated, strings.Repeat("r", maxResultReference+1), expiry); !errors.Is(err, ErrInvalidInput) {
		t.Fatal("oversized result reference accepted")
	}
	if _, err := valid(bytes.Repeat([]byte{1}, 32), ResultAuthenticated, "ref", expiry.Add(time.Nanosecond)); !errors.Is(err, ErrInvalidInput) {
		t.Fatal("sub-microsecond expiry accepted")
	}
	if _, err := valid(bytes.Repeat([]byte{1}, 32), ResultAuthenticated, "ref", expiry.In(time.FixedZone("UTC+0", 0))); !errors.Is(err, ErrInvalidInput) {
		t.Fatal("non-UTC location accepted")
	}
	ctx, err := valid(bytes.Repeat([]byte{1}, 32), ResultAuthenticated, strings.Repeat("r", maxResultReference), expiry)
	if err != nil {
		t.Fatal("maximum result reference rejected")
	}
	if _, err := c.Seal(ctx, make([]byte, maxPlaintext)); err != nil {
		t.Fatal("maximum plaintext rejected")
	}
	if _, err := c.Seal(ctx, make([]byte, maxPlaintext+1)); !errors.Is(err, ErrInvalidInput) {
		t.Fatal("oversized plaintext accepted")
	}
	if _, err := c.Open(ctx, Sealed{blob: []byte{receiptVersion}}); !errors.Is(err, ErrInvalidReceipt) {
		t.Fatal("truncated receipt accepted")
	}
}

type failedReader struct{}

func (failedReader) Read([]byte) (int, error) { return 0, errors.New("private entropy source detail") }

func TestRandomSourceFailureIsSanitized(t *testing.T) {
	c := newTestCrypto(t)
	binding, err := c.Summarize(testRequest())
	if err != nil {
		t.Fatal("request summary failed")
	}
	ctx, err := c.NewContext(bytes.Repeat([]byte{1}, 32), binding, ResultAuthenticated, "ref", time.Now().UTC().Truncate(time.Microsecond))
	if err != nil {
		t.Fatal("receipt context setup failed")
	}
	c.random = failedReader{}
	if _, err := c.Seal(ctx, []byte("x")); !errors.Is(err, ErrInvalidReceipt) || strings.Contains(err.Error(), "private entropy") {
		t.Fatal("random failure was not sanitized")
	}
}

func TestSensitiveValuesAreRedacted(t *testing.T) {
	c := newTestCrypto(t)
	req := testRequest()
	binding, err := c.Summarize(req)
	if err != nil {
		t.Fatal("request summary failed")
	}
	ctx, err := c.NewContext(bytes.Repeat([]byte{0x61}, 32), binding, ResultAuthenticated, "private-result-reference", time.Now().UTC().Truncate(time.Microsecond))
	if err != nil {
		t.Fatal("receipt context setup failed")
	}
	sealed, err := c.Seal(ctx, []byte("credential-private"))
	if err != nil {
		t.Fatal("receipt encryption failed")
	}
	secretValues := []string{req.IdempotencyKey, req.ChallengeID, req.Code, req.DeviceProof, "private-result-reference", "credential-private", fmt.Sprintf("%x", testKeys().AES256GCMKey)}
	opened, err := c.Open(ctx, sealed)
	if err != nil {
		t.Fatal("receipt open failed")
	}
	for _, value := range []any{testKeys(), req, binding, ctx, sealed, opened, *c} {
		text := fmt.Sprintf("%+v %#v", value, value)
		for _, secret := range secretValues {
			if strings.Contains(text, secret) {
				t.Fatal("sensitive input appeared in formatting")
			}
		}
		if _, err := json.Marshal(value); err == nil {
			t.Fatal("sensitive value JSON serialization was allowed")
		}
		var log bytes.Buffer
		slog.New(slog.NewJSONHandler(&log, nil)).Info("test", "value", value)
		for _, secret := range secretValues {
			if strings.Contains(log.String(), secret) {
				t.Fatal("sensitive input appeared in slog output")
			}
		}
	}
}

func TestRequestDigestHasNoUnkeyedDisclosure(t *testing.T) {
	c := newTestCrypto(t)
	binding, err := c.Summarize(testRequest())
	if err != nil {
		t.Fatal("request summary failed")
	}
	if binding.requestHMAC == ([sha256.Size]byte{}) || binding.idempotencyHMAC == ([sha256.Size]byte{}) {
		t.Fatal("valid request produced an empty keyed digest")
	}
}
