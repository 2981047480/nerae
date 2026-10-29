package domain

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

var testNow = time.Unix(1793260800, 0).UTC()
var testPolicy = ProofPolicy{MaxLifetime: 5 * time.Minute, ClockSkew: 30 * time.Second}
var testExpected = ProofExpectation{Purpose: "phone_login", ResourceID: "challenge-123", Nonce: "server-nonce-123"}

func makeKey(t *testing.T) (*ecdsa.PrivateKey, DevicePublicJWK) {
	t.Helper()
	private, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	encode := func(value []byte) string { return base64.RawURLEncoding.EncodeToString(value) }
	return private, DevicePublicJWK{Kty: "EC", Crv: "P-256", X: encode(private.X.FillBytes(make([]byte, 32))), Y: encode(private.Y.FillBytes(make([]byte, 32)))}
}

func signProof(t *testing.T, private *ecdsa.PrivateKey, header, payload string) string {
	t.Helper()
	encode := base64.RawURLEncoding.EncodeToString
	input := encode([]byte(header)) + "." + encode([]byte(payload))
	digest := sha256.Sum256([]byte(input))
	r, s, err := ecdsa.Sign(rand.Reader, private, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	signature := append(r.FillBytes(make([]byte, 32)), s.FillBytes(make([]byte, 32))...)
	return input + "." + encode(signature)
}

func payload(t *testing.T, changes map[string]any) string {
	t.Helper()
	claims := map[string]any{
		"aud": "nerae-auth-device", "purpose": testExpected.Purpose,
		"resourceId": testExpected.ResourceID, "nonce": testExpected.Nonce,
		"iat": testNow.Unix(), "exp": testNow.Add(time.Minute).Unix(), "jti": "unique-request-id",
	}
	for key, value := range changes {
		if value == nil {
			delete(claims, key)
		} else {
			claims[key] = value
		}
	}
	encoded, err := json.Marshal(claims)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

func TestVerifyValidDeviceProofAndReplayRetention(t *testing.T) {
	private, key := makeKey(t)
	proof := signProof(t, private, `{"alg":"ES256","typ":"JWT"}`, payload(t, nil))
	result, err := VerifyDeviceProof(proof, key, testExpected, testNow, testPolicy)
	if err != nil {
		t.Fatal(err)
	}
	if result.JTI != "unique-request-id" || !result.ExpiresAt.Equal(testNow.Add(time.Minute)) ||
		!result.ReplayUntil.Equal(testNow.Add(time.Minute+maxProofClockSkew)) {
		t.Fatalf("unexpected proof metadata: %+v", result)
	}
	// This function validates a signature only; it cannot itself consume JTI.
	if _, err := VerifyDeviceProof(proof, key, testExpected, testNow, testPolicy); err != nil {
		t.Fatalf("stateless verification unexpectedly consumed JTI: %v", err)
	}
}

func TestEmailDeviceProofCannotBeReusedForPhoneLogin(t *testing.T) {
	private, key := makeKey(t)
	emailExpected := testExpected
	emailExpected.Purpose = "email_login"
	proof := signProof(t, private, `{"alg":"ES256","typ":"JWT"}`, payload(t, map[string]any{"purpose": "email_login"}))
	if _, err := VerifyDeviceProof(proof, key, emailExpected, testNow, testPolicy); err != nil {
		t.Fatalf("valid email proof rejected: %v", err)
	}
	if _, err := VerifyDeviceProof(proof, key, testExpected, testNow, testPolicy); !errors.Is(err, ErrInvalidDeviceProof) {
		t.Fatalf("email proof accepted for phone login: %v", err)
	}
}

func TestFractionalNumericDateDoesNotShortenReplayRetention(t *testing.T) {
	private, key := makeKey(t)
	body := fmt.Sprintf(`{"aud":"nerae-auth-device","purpose":"phone_login","resourceId":"challenge-123","nonce":"server-nonce-123","iat":%d.0000000001,"exp":%d.0000000001,"jti":"unique-request-id"}`, testNow.Unix(), testNow.Unix()+1)
	proof := signProof(t, private, `{"alg":"ES256","typ":"JWT"}`, body)
	result, err := VerifyDeviceProof(proof, key, testExpected, testNow.Add(500*time.Millisecond), testPolicy)
	if err != nil {
		t.Fatal(err)
	}
	want := testNow.Add(time.Second + time.Nanosecond + maxProofClockSkew)
	if !result.ReplayUntil.Equal(want) {
		t.Fatalf("replay retention=%v want %v", result.ReplayUntil, want)
	}
	if _, err := VerifyDeviceProof(proof, key, testExpected, want, testPolicy); !errors.Is(err, ErrInvalidDeviceProof) {
		t.Fatalf("proof accepted at or after replay retention: %v", err)
	}
}

func TestReplayRetentionCoversLaterSkewIncrease(t *testing.T) {
	private, key := makeKey(t)
	proof := signProof(t, private, `{"alg":"ES256","typ":"JWT"}`, payload(t, nil))
	result, err := VerifyDeviceProof(proof, key, testExpected, testNow, ProofPolicy{MaxLifetime: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	if !result.ReplayUntil.Equal(result.ExpiresAt.Add(maxProofClockSkew)) {
		t.Fatalf("replay retention does not cover an allowed skew increase")
	}
	nearEnd := result.ReplayUntil.Add(-time.Second)
	if _, err := VerifyDeviceProof(proof, key, testExpected, nearEnd, ProofPolicy{MaxLifetime: time.Minute, ClockSkew: maxProofClockSkew}); err != nil {
		t.Fatalf("proof should still require replay protection after skew increase: %v", err)
	}
}

func TestVerifyRejectsTamperingAndHeaderAmbiguity(t *testing.T) {
	private, key := makeKey(t)
	other, otherKey := makeKey(t)
	goodPayload := payload(t, nil)
	tests := []struct {
		name, header, body string
		sign               *ecdsa.PrivateKey
		verify             DevicePublicJWK
	}{
		{"wrong key", `{"alg":"ES256","typ":"JWT"}`, goodPayload, other, key},
		{"wrong public key", `{"alg":"ES256","typ":"JWT"}`, goodPayload, private, otherKey},
		{"wrong algorithm", `{"alg":"HS256","typ":"JWT"}`, goodPayload, private, key},
		{"wrong type", `{"alg":"ES256","typ":"JWS"}`, goodPayload, private, key},
		{"critical extension", `{"alg":"ES256","typ":"JWT","crit":["x"]}`, goodPayload, private, key},
		{"unencoded payload", `{"alg":"ES256","typ":"JWT","b64":false}`, goodPayload, private, key},
		{"duplicate algorithm", `{"alg":"none","alg":"ES256","typ":"JWT"}`, goodPayload, private, key},
		{"duplicate claim", `{"alg":"ES256","typ":"JWT"}`, strings.TrimSuffix(goodPayload, "}") + `,"aud":"nerae-auth-device"}`, private, key},
		{"duplicate nested claim", `{"alg":"ES256","typ":"JWT"}`, strings.TrimSuffix(goodPayload, "}") + `,"extra":{"x":1,"x":2}}`, private, key},
		{"trailing JSON", `{"alg":"ES256","typ":"JWT"} {}`, goodPayload, private, key},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			proof := signProof(t, tc.sign, tc.header, tc.body)
			if _, err := VerifyDeviceProof(proof, tc.verify, testExpected, testNow, testPolicy); !errors.Is(err, ErrInvalidDeviceProof) {
				t.Fatalf("expected invalid proof, got %v", err)
			}
		})
	}
	good := signProof(t, private, `{"alg":"ES256","typ":"JWT"}`, goodPayload)
	segments := strings.Split(good, ".")
	segments[1] = base64.RawURLEncoding.EncodeToString([]byte(payload(t, map[string]any{"nonce": "altered"})))
	if _, err := VerifyDeviceProof(strings.Join(segments, "."), key, testExpected, testNow, testPolicy); !errors.Is(err, ErrInvalidDeviceProof) {
		t.Fatalf("modified payload accepted: %v", err)
	}
}

func TestVerifyRejectsWrongBindingAndTime(t *testing.T) {
	private, key := makeKey(t)
	tests := []struct {
		name    string
		changes map[string]any
	}{
		{"audience", map[string]any{"aud": "other"}},
		{"purpose", map[string]any{"purpose": "recovery_code"}},
		{"resource", map[string]any{"resourceId": "another-challenge"}},
		{"nonce", map[string]any{"nonce": "another-nonce"}},
		{"missing jti", map[string]any{"jti": nil}},
		{"expired", map[string]any{"iat": testNow.Add(-2 * time.Minute).Unix(), "exp": testNow.Add(-31 * time.Second).Unix()}},
		{"future", map[string]any{"iat": testNow.Add(31 * time.Second).Unix(), "exp": testNow.Add(2 * time.Minute).Unix()}},
		{"too long", map[string]any{"exp": testNow.Add(6 * time.Minute).Unix()}},
		{"reversed time", map[string]any{"exp": testNow.Add(-time.Second).Unix()}},
		{"string time", map[string]any{"iat": fmt.Sprint(testNow.Unix())}},
		{"missing expiration", map[string]any{"exp": nil}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			proof := signProof(t, private, `{"alg":"ES256","typ":"JWT"}`, payload(t, tc.changes))
			if _, err := VerifyDeviceProof(proof, key, testExpected, testNow, testPolicy); !errors.Is(err, ErrInvalidDeviceProof) {
				t.Fatalf("expected invalid proof, got %v", err)
			}
		})
	}
}

func TestVerifyRejectsMalformedKeysAndPolicies(t *testing.T) {
	private, key := makeKey(t)
	proof := signProof(t, private, `{"alg":"ES256","typ":"JWT"}`, payload(t, nil))
	badKeys := []DevicePublicJWK{
		{Kty: "RSA", Crv: key.Crv, X: key.X, Y: key.Y},
		{Kty: key.Kty, Crv: "P-384", X: key.X, Y: key.Y},
		{Kty: key.Kty, Crv: key.Crv, X: strings.Repeat("A", 43), Y: strings.Repeat("A", 43)},
		{Kty: key.Kty, Crv: key.Crv, X: "not-a-key", Y: key.Y},
	}
	for _, bad := range badKeys {
		if _, err := VerifyDeviceProof(proof, bad, testExpected, testNow, testPolicy); !errors.Is(err, ErrInvalidDeviceProof) {
			t.Fatalf("bad public key accepted: %v", err)
		}
	}
	for _, policy := range []ProofPolicy{{}, {MaxLifetime: time.Hour, ClockSkew: -time.Second}, {MaxLifetime: 25 * time.Hour}, {MaxLifetime: time.Minute, ClockSkew: 6 * time.Minute}} {
		if _, err := VerifyDeviceProof(proof, key, testExpected, testNow, policy); !errors.Is(err, ErrInvalidDeviceProof) {
			t.Fatalf("bad policy accepted: %v", err)
		}
	}
	if _, err := VerifyDeviceProof(strings.Repeat("x", 4097), key, testExpected, testNow, testPolicy); !errors.Is(err, ErrInvalidDeviceProof) {
		t.Fatalf("oversized proof accepted: %v", err)
	}
}

func TestDeviceKeyFingerprintUsesCanonicalPublicJWK(t *testing.T) {
	_, key := makeKey(t)
	got, err := DeviceKeyFingerprint(key)
	if err != nil {
		t.Fatal(err)
	}
	canonical := fmt.Sprintf(`{"crv":"P-256","kty":"EC","x":"%s","y":"%s"}`, key.X, key.Y)
	want := sha256.Sum256([]byte(canonical))
	if got != want {
		t.Fatal("fingerprint is not the canonical JWK SHA-256 thumbprint")
	}
	key.Crv = "P-384"
	if _, err := DeviceKeyFingerprint(key); !errors.Is(err, ErrInvalidDeviceProof) {
		t.Fatalf("invalid key fingerprinted: %v", err)
	}
}
