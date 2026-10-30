package transport

import (
	"context"
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
	"time"

	api "nerae/api/internal/gen"
	"nerae/api/internal/middleware"
	"nerae/api/internal/modules/auth/application"
	"nerae/api/internal/modules/auth/challenge"
	"nerae/api/internal/modules/auth/ratelimit"
	"nerae/api/internal/modules/auth/session"
)

type loginVerifierFunc func(context.Context, *sql.DB, string, challenge.AttemptInput, netip.Addr) (application.Outcome, error)

func (f loginVerifierFunc) VerifyEmail(ctx context.Context, db *sql.DB, key string, input challenge.AttemptInput, ip netip.Addr) (application.Outcome, error) {
	return f(ctx, db, key, input, ip)
}

func TestEmailLoginResponseRecalculatesLifetimeAfterEncoding(t *testing.T) {
	validator, err := middleware.NewSchemaValidator(16<<10, 2048)
	if err != nil {
		t.Fatal(err)
	}
	base := time.Unix(1_700_000_000, 0).UTC()
	profile := session.ResponseProfile{ExpiresAt: base.Add(10 * time.Second)}
	response := api.PhoneAuthSuccess{Status: api.Authenticated, Token: "signed-token", User: api.User{
		Id: "101", Nickname: "", Roles: []api.UserRoles{},
	}}
	times := []time.Time{base.Add(100 * time.Millisecond), base.Add(1100 * time.Millisecond),
		base.Add(1200 * time.Millisecond), base.Add(1300 * time.Millisecond)}
	index := 0
	handler := &emailLoginHandler{validator: validator, now: func() time.Time {
		if index >= len(times) {
			t.Fatal("unexpected clock read")
		}
		value := times[index]
		index++
		return value
	}}
	encoded, err := handler.encodeAuthenticatedResponse(response, profile)
	var wire api.PhoneAuthSuccess
	if err != nil || json.Unmarshal(encoded, &wire) != nil || wire.ExpiresIn != 8 || index != 4 {
		t.Fatal("response did not recompute the shorter remaining lifetime")
	}
	handler.now = func() time.Time { return base.Add(9200 * time.Millisecond) }
	if _, err := handler.encodeAuthenticatedResponse(response, profile); !errors.Is(err, session.ErrInvalidSession) {
		t.Fatal("almost-expired session was returned as authenticated")
	}
	handler.now = func() time.Time { return time.Time{} }
	if _, err := handler.encodeAuthenticatedResponse(response, profile); !errors.Is(err, session.ErrSessionUnavailable) {
		t.Fatal("broken clock was treated as an expired account")
	}
}

type loginProfileFunc func(context.Context, *sql.DB, string) (session.ResponseProfile, error)

func (f loginProfileFunc) ProfileForResponse(ctx context.Context, db *sql.DB, token string) (session.ResponseProfile, error) {
	return f(ctx, db, token)
}

func TestEmailLoginHandlerRejectsMalformedRequests(t *testing.T) {
	validator, err := middleware.NewSchemaValidator(16<<10, 2048)
	if err != nil {
		t.Fatal(err)
	}
	called := 0
	verifier := loginVerifierFunc(func(context.Context, *sql.DB, string, challenge.AttemptInput, netip.Addr) (application.Outcome, error) {
		called++
		return application.Outcome{}, application.ErrUnavailable
	})
	profiler := loginProfileFunc(func(context.Context, *sql.DB, string) (session.ResponseProfile, error) {
		t.Fatal("invalid login reached profile lookup")
		return session.ResponseProfile{}, session.ErrSessionUnavailable
	})
	ipCalls := 0
	handler, err := NewEmailLoginHandler(new(sql.DB), verifier, profiler, validator, ipFunc(func(*http.Request) (netip.Addr, error) {
		ipCalls++
		return netip.MustParseAddr("192.0.2.21"), nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	key := base64.RawURLEncoding.EncodeToString(make([]byte, 16))
	proof := strings.Repeat("a", 20) + "." + strings.Repeat("b", 30) + "." + strings.Repeat("c", 86)
	body := `{"challengeId":"` + strings.Repeat("C", 22) + `","code":"123456","deviceProof":"` + proof + `"}`
	for _, tc := range []struct {
		name, method, path, body string
		headers                  map[string][]string
	}{
		{"method", http.MethodGet, emailLoginPath, body, validHeaders(key)},
		{"query", http.MethodPost, emailLoginPath + "?a=1", body, validHeaders(key)},
		{"missing key", http.MethodPost, emailLoginPath, body, map[string][]string{"Content-Type": {"application/json"}}},
		{"duplicate key", http.MethodPost, emailLoginPath, body, map[string][]string{"Content-Type": {"application/json"}, "Idempotency-Key": {key, key}}},
		{"noncanonical key", http.MethodPost, emailLoginPath, body, validHeaders(key + "=")},
		{"wrong media", http.MethodPost, emailLoginPath, body, map[string][]string{"Content-Type": {"text/plain"}, "Idempotency-Key": {key}}},
		{"duplicate member", http.MethodPost, emailLoginPath, strings.Replace(body, `"code":`, `"code":"123456","code":`, 1), validHeaders(key)},
		{"extra member", http.MethodPost, emailLoginPath, strings.TrimSuffix(body, "}") + `,"userId":"101"}`, validHeaders(key)},
		{"extra document", http.MethodPost, emailLoginPath, body + ` {}`, validHeaders(key)},
		{"invalid code", http.MethodPost, emailLoginPath, strings.Replace(body, "123456", "abc123", 1), validHeaders(key)},
		{"invalid proof", http.MethodPost, emailLoginPath, strings.Replace(body, proof, "unsigned", 1), validHeaders(key)},
		{"invalid utf8", http.MethodPost, emailLoginPath, string([]byte{0xff}), validHeaders(key)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(tc.method, "http://local"+tc.path, strings.NewReader(tc.body))
			for name, values := range tc.headers {
				for _, value := range values {
					r.Header.Add(name, value)
				}
			}
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			if w.Code < 400 || w.Code >= 500 || w.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("malformed request was not rejected safely: %d", w.Code)
			}
			if strings.Contains(w.Body.String(), proof) || strings.Contains(w.Body.String(), "123456") {
				t.Fatal("error exposed request secrets")
			}
		})
	}
	if called != 0 || ipCalls != 0 {
		t.Fatal("malformed request reached authentication or source IP resolution")
	}
}

func TestEmailLoginHandlerMapsFailuresAndUsesTrustedIP(t *testing.T) {
	validator, err := middleware.NewSchemaValidator(16<<10, 2048)
	if err != nil {
		t.Fatal(err)
	}
	proof := strings.Repeat("a", 20) + "." + strings.Repeat("b", 30) + "." + strings.Repeat("c", 86)
	body := `{"challengeId":"` + strings.Repeat("C", 22) + `","code":"123456","deviceProof":"` + proof + `"}`
	key := base64.RawURLEncoding.EncodeToString(make([]byte, 16))
	for _, tc := range []struct {
		err  error
		want int
	}{
		{application.ErrRejected, http.StatusUnauthorized},
		{application.ErrReceiptConflict, http.StatusConflict},
		{ratelimit.ErrLimited, http.StatusTooManyRequests},
		{fmt.Errorf("private@example.test: %w", application.ErrUnavailable), http.StatusServiceUnavailable},
	} {
		verifier := loginVerifierFunc(func(_ context.Context, _ *sql.DB, gotKey string, input challenge.AttemptInput, ip netip.Addr) (application.Outcome, error) {
			if gotKey != key || input.Code != "123456" || input.DeviceProof != proof || ip != netip.MustParseAddr("192.0.2.21") {
				t.Fatal("handler altered input or trusted a forwarded header")
			}
			return application.Outcome{}, tc.err
		})
		profiler := loginProfileFunc(func(context.Context, *sql.DB, string) (session.ResponseProfile, error) {
			t.Fatal("failed login reached profile lookup")
			return session.ResponseProfile{}, session.ErrSessionUnavailable
		})
		handler, err := NewEmailLoginHandler(new(sql.DB), verifier, profiler, validator, ipFunc(func(*http.Request) (netip.Addr, error) {
			return netip.MustParseAddr("192.0.2.21"), nil
		}))
		if err != nil {
			t.Fatal(err)
		}
		r := httptest.NewRequest(http.MethodPost, "http://local"+emailLoginPath, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Idempotency-Key", key)
		r.Header.Set("X-Forwarded-For", "203.0.113.99")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != tc.want || w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("X-Request-ID") == "" {
			t.Fatalf("unsafe failure response: status %d", w.Code)
		}
		if strings.Contains(w.Body.String(), proof) || strings.Contains(w.Body.String(), "123456") || strings.Contains(w.Body.String(), "private@example.test") {
			t.Fatal("login error exposed secrets or internal error")
		}
	}
	if _, err := NewEmailLoginHandler(nil, nil, nil, nil, nil); !errors.Is(err, middleware.ErrContract) {
		t.Fatal("missing dependencies were accepted")
	}
}
