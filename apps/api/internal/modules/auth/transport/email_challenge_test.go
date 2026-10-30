package transport

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"

	"nerae/api/internal/middleware"
	"nerae/api/internal/modules/auth/domain"
	"nerae/api/internal/modules/auth/issuance"
)

type issuerFunc func(context.Context, *sql.DB, issuance.ChallengeRequestInput) (issuance.ChallengeRequestResult, error)

func (f issuerFunc) Issue(ctx context.Context, db *sql.DB, input issuance.ChallengeRequestInput) (issuance.ChallengeRequestResult, error) {
	return f(ctx, db, input)
}

type ipFunc func(*http.Request) (netip.Addr, error)

func (f ipFunc) ResolveTrustedSourceIP(r *http.Request) (netip.Addr, error) { return f(r) }

func TestEmailChallengeHandlerRejectsMalformedRequestsBeforeIssuer(t *testing.T) {
	validator, err := middleware.NewSchemaValidator(16<<10, 2048)
	if err != nil {
		t.Fatal("schema validator setup failed")
	}
	called := 0
	issuer := issuerFunc(func(context.Context, *sql.DB, issuance.ChallengeRequestInput) (issuance.ChallengeRequestResult, error) {
		called++
		return issuance.ChallengeRequestResult{}, nil
	})
	trustedIPCalls := 0
	handler, err := NewEmailChallengeHandler(new(sql.DB), issuer, validator, ipFunc(func(*http.Request) (netip.Addr, error) {
		trustedIPCalls++
		return netip.MustParseAddr("192.0.2.17"), nil
	}))
	if err != nil {
		t.Fatal("handler setup failed")
	}
	validBody := validChallengeBody(t)
	validKey := base64.RawURLEncoding.EncodeToString(make([]byte, 16))
	tests := []struct {
		name    string
		method  string
		path    string
		query   string
		body    []byte
		headers map[string][]string
	}{
		{name: "wrong method", method: http.MethodGet, path: emailChallengesPath, body: validBody, headers: validHeaders(validKey)},
		{name: "query", method: http.MethodPost, path: emailChallengesPath, query: "email=a@example.test", body: validBody, headers: validHeaders(validKey)},
		{name: "missing key", method: http.MethodPost, path: emailChallengesPath, body: validBody, headers: map[string][]string{"Content-Type": {"application/json"}}},
		{name: "duplicate key", method: http.MethodPost, path: emailChallengesPath, body: validBody, headers: map[string][]string{"Content-Type": {"application/json"}, "Idempotency-Key": {validKey, validKey}}},
		{name: "noncanonical key", method: http.MethodPost, path: emailChallengesPath, body: validBody, headers: validHeaders(validKey + "=")},
		{name: "short key", method: http.MethodPost, path: emailChallengesPath, body: validBody, headers: validHeaders(base64.RawURLEncoding.EncodeToString(make([]byte, 15)))},
		{name: "wrong media type", method: http.MethodPost, path: emailChallengesPath, body: validBody, headers: map[string][]string{"Content-Type": {"text/plain"}, "Idempotency-Key": {validKey}}},
		{name: "duplicate content type", method: http.MethodPost, path: emailChallengesPath, body: validBody, headers: map[string][]string{"Content-Type": {"application/json", "application/json"}, "Idempotency-Key": {validKey}}},
		{name: "unknown field", method: http.MethodPost, path: emailChallengesPath, body: append(append([]byte(nil), validBody[:len(validBody)-1]...), []byte(`,"extra":true}`)...), headers: validHeaders(validKey)},
		{name: "duplicate JSON member", method: http.MethodPost, path: emailChallengesPath, body: []byte(strings.Replace(string(validBody), `"email":`, `"email":"other@example.test","email":`, 1)), headers: validHeaders(validKey)},
		{name: "multiple JSON values", method: http.MethodPost, path: emailChallengesPath, body: append(append([]byte(nil), validBody...), []byte(` {}`)...), headers: validHeaders(validKey)},
		{name: "invalid UTF-8", method: http.MethodPost, path: emailChallengesPath, body: []byte{0xff}, headers: validHeaders(validKey)},
		{name: "invalid email syntax", method: http.MethodPost, path: emailChallengesPath, body: []byte(strings.Replace(string(validBody), "person@example.test", "not-an-email", 1)), headers: validHeaders(validKey)},
		{name: "invalid public point", method: http.MethodPost, path: emailChallengesPath, body: []byte(strings.Replace(string(validBody), `"x":"`+testJWKX+`"`, `"x":"`+base64.RawURLEncoding.EncodeToString(make([]byte, 32))+`"`, 1)), headers: validHeaders(validKey)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(tc.method, "http://local"+tc.path, strings.NewReader(string(tc.body)))
			r.URL.RawQuery = tc.query
			for name, values := range tc.headers {
				for _, value := range values {
					r.Header.Add(name, value)
				}
			}
			r.Header.Set("X-Forwarded-For", "203.0.113.99")
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			if w.Code < 400 || w.Code >= 500 {
				t.Fatalf("malformed request status=%d, want 4xx", w.Code)
			}
			if w.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("error response missing no-store")
			}
			if called != 0 {
				t.Fatal("invalid request reached issuer")
			}
		})
	}
	if trustedIPCalls != 0 {
		t.Fatal("invalid request reached trusted IP resolver")
	}
}

func TestEmailChallengeHandlerMapsIssuerFailuresWithoutLeakage(t *testing.T) {
	validator, err := middleware.NewSchemaValidator(16<<10, 2048)
	if err != nil {
		t.Fatal("schema validator setup failed")
	}
	key := base64.RawURLEncoding.EncodeToString(make([]byte, 16))
	for _, tc := range []struct {
		name string
		err  error
		want int
	}{
		{"limited", issuance.ErrChallengeRequestLimited, http.StatusTooManyRequests},
		{"conflict", issuance.ErrChallengeRequestConflict, http.StatusConflict},
		{"storage error redacted", fmt.Errorf("private@example.test: %w", issuance.ErrChallengeRequest), http.StatusServiceUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			handler, err := NewEmailChallengeHandler(new(sql.DB), issuerFunc(func(context.Context, *sql.DB, issuance.ChallengeRequestInput) (issuance.ChallengeRequestResult, error) {
				return issuance.ChallengeRequestResult{}, tc.err
			}), validator, ipFunc(func(*http.Request) (netip.Addr, error) { return netip.MustParseAddr("192.0.2.2"), nil }))
			if err != nil {
				t.Fatal("handler setup failed")
			}
			r := httptest.NewRequest(http.MethodPost, "http://local"+emailChallengesPath, strings.NewReader(string(validChallengeBody(t))))
			r.Header.Set("Content-Type", "application/json")
			r.Header.Set("Idempotency-Key", key)
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			if w.Code != tc.want || w.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("status/cache=%d/%q, want %d/no-store", w.Code, w.Header().Get("Cache-Control"), tc.want)
			}
			if strings.Contains(w.Body.String(), "private@example.test") || strings.Contains(w.Body.String(), "storage error") {
				t.Fatal("internal failure leaked in response")
			}
		})
	}
}

func TestEmailChallengeHandlerUsesInjectedTrustedIPAndRejectsBadResolver(t *testing.T) {
	validator, err := middleware.NewSchemaValidator(16<<10, 2048)
	if err != nil {
		t.Fatal("schema validator setup failed")
	}
	key := base64.RawURLEncoding.EncodeToString(make([]byte, 16))
	var captured issuance.ChallengeRequestInput
	called := 0
	issuer := issuerFunc(func(_ context.Context, _ *sql.DB, input issuance.ChallengeRequestInput) (issuance.ChallengeRequestResult, error) {
		captured = input
		called++
		return issuance.ChallengeRequestResult{}, issuance.ErrChallengeRequest
	})
	handler, err := NewEmailChallengeHandler(new(sql.DB), issuer, validator, ipFunc(func(r *http.Request) (netip.Addr, error) {
		if r.Header.Get("X-Forwarded-For") != "198.51.100.90" {
			t.Fatal("test spoof header missing")
		}
		return netip.MustParseAddr("192.0.2.44"), nil
	}))
	if err != nil {
		t.Fatal("handler setup failed")
	}
	r := httptest.NewRequest(http.MethodPost, "http://local"+emailChallengesPath, strings.NewReader(string(validChallengeBody(t))))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Idempotency-Key", key)
	r.Header.Set("X-Forwarded-For", "198.51.100.90")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if called != 1 || captured.TrustedSourceIP != netip.MustParseAddr("192.0.2.44") {
		t.Fatal("handler did not use the injected trusted IP")
	}

	badHandler, err := NewEmailChallengeHandler(new(sql.DB), issuer, validator, ipFunc(func(*http.Request) (netip.Addr, error) {
		return netip.Addr{}, errors.New("private resolver failure")
	}))
	if err != nil {
		t.Fatal("bad-resolver handler setup failed")
	}
	r = httptest.NewRequest(http.MethodPost, "http://local"+emailChallengesPath, strings.NewReader(string(validChallengeBody(t))))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Idempotency-Key", key)
	w = httptest.NewRecorder()
	prior := called
	badHandler.ServeHTTP(w, r)
	if w.Code != http.StatusServiceUnavailable || called != prior || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("trusted IP resolver failure did not fail closed before issuer")
	}
	invalidIPHandler, err := NewEmailChallengeHandler(new(sql.DB), issuer, validator, ipFunc(func(*http.Request) (netip.Addr, error) {
		return netip.IPv4Unspecified(), nil
	}))
	if err != nil {
		t.Fatal("invalid-IP handler setup failed")
	}
	r = httptest.NewRequest(http.MethodPost, "http://local"+emailChallengesPath, strings.NewReader(string(validChallengeBody(t))))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Idempotency-Key", key)
	w = httptest.NewRecorder()
	invalidIPHandler.ServeHTTP(w, r)
	if w.Code != http.StatusServiceUnavailable || called != prior || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("invalid trusted IP was not treated as a server boundary failure")
	}
}

func TestStrictWireJSONRejectsInvalidUTF8AndDuplicateMembers(t *testing.T) {
	if strictWireJSON([]byte(`{"challengeId":"abc","nonce":"abc","expiresAt":"2030-01-01T00:00:00Z"}`)) != true {
		t.Fatal("valid internal JSON was rejected")
	}
	if strictWireJSON([]byte{'{', '"', 0xff, '"', ':', '1', '}'}) {
		t.Fatal("invalid UTF-8 was silently repaired")
	}
	if strictWireJSON([]byte(`{"x":1,"x":2}`)) {
		t.Fatal("duplicate member was accepted")
	}
}

func TestChallengeIdempotencyKeyCanonicalBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name string
		data []byte
		ok   bool
	}{
		{"minimum 128-bit key", make([]byte, 16), true},
		{"maximum encoded key", make([]byte, 96), true},
		{"over maximum encoded key", make([]byte, 97), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			value := base64.RawURLEncoding.EncodeToString(tc.data)
			_, err := challengeIdempotencyKey(http.Header{"Idempotency-Key": {value}})
			if (err == nil) != tc.ok {
				t.Fatalf("key acceptance=%v, want %v", err == nil, tc.ok)
			}
		})
	}
	if _, err := challengeIdempotencyKey(http.Header{"Idempotency-Key": {base64.URLEncoding.EncodeToString(make([]byte, 16))}}); err == nil {
		t.Fatal("padded base64 key was accepted")
	}
}

var testJWKX string

func validChallengeBody(t *testing.T) []byte {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal("synthetic public key generation failed")
	}
	x := make([]byte, 32)
	y := make([]byte, 32)
	key.X.FillBytes(x)
	key.Y.FillBytes(y)
	testJWKX = base64.RawURLEncoding.EncodeToString(x)
	public := domain.DevicePublicJWK{Kty: "EC", Crv: "P-256", X: testJWKX, Y: base64.RawURLEncoding.EncodeToString(y)}
	if _, err := domain.DeviceKeyFingerprint(public); err != nil {
		t.Fatal("synthetic P-256 public key validation failed")
	}
	body, err := json.Marshal(map[string]any{"email": "person@example.test", "devicePublicKey": map[string]string{
		"kty": public.Kty, "crv": public.Crv, "x": public.X, "y": public.Y,
	}})
	if err != nil {
		t.Fatal("request encoding failed")
	}
	return body
}

func validHeaders(key string) map[string][]string {
	return map[string][]string{"Content-Type": {"application/json"}, "Idempotency-Key": {key}}
}
