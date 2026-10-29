package session

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"
)

func TestUserTokenSignatureAndClaims(t *testing.T) {
	key := bytes.Repeat([]byte{7}, 32)
	codec, err := NewTokenCodec(key, TokenPolicy{MaxLifetime: time.Hour, ClockSkew: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 29, 10, 30, 0, 250000000, time.UTC)
	codec.now = func() time.Time { return now }
	token, err := codec.Issue("123", "session_abcdefghijklmnop", 2)
	if err != nil {
		t.Fatal("could not issue test token")
	}
	// Mutating the caller's key must not alter the verifier's configuration.
	key[0] ^= 1
	claims, err := codec.Verify(token.ForResponse())
	if err != nil || claims.SubjectID != "123" || claims.SessionID != "session_abcdefghijklmnop" || claims.TokenVersion != 2 ||
		!claims.IssuedAt.Equal(now.Truncate(time.Second)) || !claims.ExpiresAt.Equal(now.Truncate(time.Second).Add(time.Hour)) {
		t.Fatal("valid token did not preserve the expected claims")
	}
	wrong, _ := NewTokenCodec(bytes.Repeat([]byte{8}, 32), TokenPolicy{MaxLifetime: time.Hour})
	wrong.now = codec.now
	if _, err := wrong.Verify(token.ForResponse()); err == nil {
		t.Fatal("wrong key verified token")
	}
	parts := strings.Split(token.ForResponse(), ".")
	parts[1] = base64.RawURLEncoding.EncodeToString([]byte(`{"sub":"456"}`))
	if _, err := codec.Verify(strings.Join(parts, ".")); err == nil {
		t.Fatal("tampered token verified")
	}
	codec.now = func() time.Time { return claims.ExpiresAt }
	if _, err := codec.Verify(token.ForResponse()); err == nil {
		t.Fatal("token was valid at its expiry")
	}
	codec.now = func() time.Time { return now }
	for _, subject := range []string{"", "0", "01", "-1", "+1", "18446744073709551616"} {
		if _, err := codec.Issue(subject, "session_abcdefghijklmnop", 1); err == nil {
			t.Fatal("invalid subject was issued a token")
		}
	}
	if _, err := codec.Issue("123", "short", 1); err == nil {
		t.Fatal("short session ID was accepted")
	}
	if _, err := NewTokenCodec([]byte("short"), TokenPolicy{MaxLifetime: time.Hour}); err == nil {
		t.Fatal("short signing key was accepted")
	}
}

func TestSignedMalformedTokensFailClosed(t *testing.T) {
	key := bytes.Repeat([]byte{7}, 32)
	codec, _ := NewTokenCodec(key, TokenPolicy{MaxLifetime: time.Hour, ClockSkew: time.Minute})
	now := time.Unix(1800000000, 0)
	codec.now = func() time.Time { return now }
	encode := base64.RawURLEncoding.EncodeToString
	sign := func(header, payload string) string {
		input := encode([]byte(header)) + "." + encode([]byte(payload))
		mac := hmac.New(sha256.New, key)
		_, _ = mac.Write([]byte(input))
		return input + "." + encode(mac.Sum(nil))
	}
	header := `{"alg":"HS256","typ":"JWT"}`
	valid := `{"iss":"nerae-users","aud":"nerae-users","sub":"123","jti":"session_abcdefghijklmnop","tv":1,"iat":1800000000,"exp":1800003600}`
	if _, err := codec.Verify(sign(header, valid)); err != nil {
		t.Fatal("valid signed fixture failed")
	}
	atSkewBoundary := strings.Replace(valid, `"iat":1800000000`, `"iat":1800000060`, 1)
	if _, err := codec.Verify(sign(header, atSkewBoundary)); err != nil {
		t.Fatal("token at the configured clock-skew boundary was rejected")
	}
	maxPolicy, err := NewTokenCodec(key, TokenPolicy{MaxLifetime: 30 * 24 * time.Hour})
	if err != nil {
		t.Fatal("configured maximum lifetime was rejected")
	}
	maxPolicy.now = codec.now
	maxLifetime := strings.Replace(valid, `"exp":1800003600`, `"exp":1802592000`, 1)
	if _, err := maxPolicy.Verify(sign(header, maxLifetime)); err != nil {
		t.Fatal("token at maximum lifetime was rejected")
	}
	if _, err := NewTokenCodec(key, TokenPolicy{MaxLifetime: 30*24*time.Hour + time.Second}); err == nil {
		t.Fatal("policy beyond maximum lifetime was accepted")
	}
	cases := []struct{ header, payload string }{
		{`{"alg":"none","typ":"JWT"}`, valid},
		{`{"alg":"HS256","typ":"JWT","crit":["b64"],"b64":false}`, valid},
		{`{"alg":"HS256","alg":"HS256","typ":"JWT"}`, valid},
		{header, strings.Replace(valid, `"aud":"nerae-users"`, `"aud":"nerae-admin"`, 1)},
		{header, strings.Replace(valid, `"iss":"nerae-users"`, `"iss":"other"`, 1)},
		{header, strings.Replace(strings.Replace(valid, `"aud":"nerae-users"`, `"aud":"nerae-admin"`, 1), `"iss":"nerae-users"`, `"iss":"nerae-admin"`, 1)},
		{header, strings.Replace(valid, `"sub":"123"`, `"sub":"123","sub":"456"`, 1)},
		{header, strings.Replace(valid, `"tv":1`, `"tv":0`, 1)},
		{header, strings.Replace(valid, `"iat":1800000000`, `"iat":1800000061`, 1)},
		{header, strings.Replace(valid, `"exp":1800003600`, `"exp":1800003601`, 1)},
		{header, strings.Replace(valid, `"exp":1800003600`, `"exp":1800000000`, 1)},
		{header, strings.Replace(valid, `"tv":1`, `"tv":1.0`, 1)},
		{header, strings.Replace(valid, `"tv":1`, `"tv":null`, 1)},
		{header, strings.Replace(valid, `"tv":1`, `"tv":18446744073709551616`, 1)},
		{header, strings.Replace(valid, `"iat":1800000000`, `"iat":-1`, 1)},
		{header, strings.Replace(valid, `"exp":1800003600`, `"exp":9223372036854775808`, 1)},
		{header, strings.TrimSuffix(valid, "}") + `,"extra":true}`},
		{header, strings.Replace(valid, `"sub":"123",`, "", 1)},
		{header, valid + `{}`},
	}
	for i, item := range cases {
		if _, err := codec.Verify(sign(item.header, item.payload)); err == nil {
			t.Fatalf("invalid signed case %d was accepted", i)
		}
	}
}

func TestTokenValuesAreRedacted(t *testing.T) {
	codec, _ := NewTokenCodec(bytes.Repeat([]byte{7}, 32), TokenPolicy{MaxLifetime: time.Hour})
	token, err := codec.Issue("123456789", "session_abcdefghijklmnop", 1)
	if err != nil {
		t.Fatal("could not issue test token")
	}
	claims, err := codec.Verify(token.ForResponse())
	if err != nil {
		t.Fatal("could not verify test token")
	}
	for _, value := range []any{codec, *codec, token, &token, claims, &claims} {
		if _, err := json.Marshal(value); err == nil {
			t.Fatal("sensitive value was JSON-serializable")
		}
		var log bytes.Buffer
		slog.New(slog.NewJSONHandler(&log, nil)).Info("test", "value", value)
		var entry map[string]any
		if json.Unmarshal(log.Bytes(), &entry) != nil {
			t.Fatal("could not parse test log")
		}
		output := fmt.Sprintf("%+v %#v %v", value, value, entry["value"])
		for _, secret := range []string{token.ForResponse(), claims.SubjectID, claims.SessionID} {
			if strings.Contains(output, secret) {
				t.Fatal("credential or identity leaked into output")
			}
		}
	}
}
