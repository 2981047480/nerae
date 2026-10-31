package main

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

func TestProxyCredentialGuardsBusinessRoutes(t *testing.T) {
	secret := []byte("01234567890123456789012345678901")
	encoded := base64.RawURLEncoding.EncodeToString(secret)
	calls := 0
	handler := authenticateProxy(secret, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		for name := range r.Header {
			if strings.EqualFold(name, proxyCredentialHeader) {
				t.Fatal("proxy credential reached business handler")
			}
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	for _, tc := range []struct {
		name    string
		headers http.Header
		want    int
	}{
		{"missing", http.Header{}, http.StatusForbidden},
		{"wrong", http.Header{proxyCredentialHeader: {base64.RawURLEncoding.EncodeToString(make([]byte, 32))}}, http.StatusForbidden},
		{"padded", http.Header{proxyCredentialHeader: {encoded + "="}}, http.StatusForbidden},
		{"duplicate values", http.Header{proxyCredentialHeader: {encoded, encoded}}, http.StatusForbidden},
		{"duplicate case", http.Header{proxyCredentialHeader: {encoded}, strings.ToLower(proxyCredentialHeader): {encoded}}, http.StatusForbidden},
		{"valid", http.Header{proxyCredentialHeader: {encoded}}, http.StatusNoContent},
		{"valid lower case", http.Header{strings.ToLower(proxyCredentialHeader): {encoded}}, http.StatusNoContent},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/business", nil)
			r.Header = tc.headers.Clone()
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			if w.Code != tc.want || strings.Contains(w.Body.String(), encoded) {
				t.Fatal("proxy authentication returned an unexpected status or exposed its credential")
			}
		})
	}
	if calls != 2 {
		t.Fatal("unauthenticated request reached the business handler")
	}
}

func TestProxyBoundaryOnlyExemptsExactHealthGets(t *testing.T) {
	secret := []byte("01234567890123456789012345678901")
	handler := proxyBoundary(secret, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for name := range r.Header {
			if strings.EqualFold(name, proxyCredentialHeader) {
				t.Fatal("proxy credential reached the router")
			}
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	for _, tc := range []struct {
		method, target string
		want           int
	}{
		{http.MethodGet, "/health/live", http.StatusNoContent},
		{http.MethodGet, "/health/ready", http.StatusNoContent},
		{http.MethodPost, "/health/live", http.StatusForbidden},
		{http.MethodGet, "/health/live?x=1", http.StatusForbidden},
		{http.MethodGet, "/health/live/", http.StatusForbidden},
		{http.MethodGet, "/future-gin-business-route", http.StatusForbidden},
	} {
		r := httptest.NewRequest(tc.method, tc.target, nil)
		if tc.want == http.StatusNoContent {
			r.Header[strings.ToLower(proxyCredentialHeader)] = []string{base64.RawURLEncoding.EncodeToString(secret)}
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != tc.want {
			t.Fatalf("boundary did not protect %s %s", tc.method, tc.target)
		}
	}
}

func TestHealthRoutesReflectDependencyChecks(t *testing.T) {
	secretError := errors.New("fixture-private-dsn")
	cases := []struct {
		name               string
		mysqlErr, redisErr error
		want               int
	}{
		{"all healthy", nil, nil, http.StatusOK},
		{"mysql unavailable", secretError, nil, http.StatusServiceUnavailable},
		{"redis unavailable", nil, secretError, http.StatusServiceUnavailable},
		{"both unavailable", secretError, secretError, http.StatusServiceUnavailable},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mysqlCalls, redisCalls := 0, 0
			router := newRouter(func(context.Context) error { mysqlCalls++; return tc.mysqlErr }, func(context.Context) error { redisCalls++; return tc.redisErr }, time.Second)
			live := httptest.NewRecorder()
			router.ServeHTTP(live, httptest.NewRequest(http.MethodGet, "/health/live", nil))
			if live.Code != http.StatusOK || mysqlCalls != 0 || redisCalls != 0 {
				t.Fatal("liveness must not require dependencies")
			}
			ready := httptest.NewRecorder()
			router.ServeHTTP(ready, httptest.NewRequest(http.MethodGet, "/health/ready", nil))
			if ready.Code != tc.want || mysqlCalls != 1 || redisCalls != 1 {
				t.Fatalf("readiness=%d; checks=%d/%d", ready.Code, mysqlCalls, redisCalls)
			}
			if strings.Contains(ready.Body.String(), secretError.Error()) {
				t.Fatal("dependency error leaked in response")
			}
		})
	}
}

func TestBusinessModeRequiresExplicitProductionOptIn(t *testing.T) {
	for _, tc := range []struct {
		environment, flag string
		want              bool
		valid             bool
	}{
		{"test", "", false, true}, {"development", "false", false, true},
		{"test", "true", true, true}, {"production", "true", true, true},
		{"production", "", false, false}, {"production", "false", false, false},
		{"production", "TRUE", false, false}, {"unknown", "true", false, false},
	} {
		got, err := businessMode(tc.environment, tc.flag)
		if (err == nil) != tc.valid || got != tc.want {
			t.Fatalf("unexpected business mode for %s/%s", tc.environment, tc.flag)
		}
	}
}

func TestBusinessNetworkRequiresLoopbackAndProtectedDependencies(t *testing.T) {
	unixRedis, err := redis.ParseURL("unix:///private/tmp/test.sock")
	if err != nil {
		t.Fatal("synthetic unix Redis configuration failed")
	}
	tlsRedis, err := redis.ParseURL("rediss://user:synthetic@example.test:6379/0")
	if err != nil {
		t.Fatal("synthetic TLS Redis configuration failed")
	}
	insecureRedis, err := redis.ParseURL("rediss://user:synthetic@example.test:6379/0?skip_verify=true")
	if err != nil {
		t.Fatal("synthetic insecure Redis configuration failed")
	}
	plainRedis, err := redis.ParseURL("redis://user:synthetic@example.test:6379/0")
	if err != nil {
		t.Fatal("synthetic plaintext Redis configuration failed")
	}
	unixDB := "user@unix(/private/tmp/test-mysql.sock)/nerae?parseTime=true&loc=UTC"
	tlsDB := "user@tcp(example.test:3306)/nerae?parseTime=true&loc=UTC&tls=true"
	for _, tc := range []struct {
		name, address, dsn string
		redis              *redis.Options
		want               bool
	}{
		{"local unix", "127.0.0.1:8080", unixDB, unixRedis, true},
		{"loopback tls", "[::1]:8080", tlsDB, tlsRedis, true},
		{"public listener", "0.0.0.0:8080", unixDB, unixRedis, false},
		{"hostname listener", "localhost:8080", unixDB, unixRedis, false},
		{"zero port", "127.0.0.1:0", unixDB, unixRedis, false},
		{"plain mysql", "127.0.0.1:8080", "user@tcp(example.test:3306)/nerae?parseTime=true&loc=UTC", unixRedis, false},
		{"mysql fallback", "127.0.0.1:8080", tlsDB + "&allowFallbackToPlaintext=true", unixRedis, false},
		{"mysql wrong time", "127.0.0.1:8080", "user@unix(/private/tmp/test.sock)/nerae", unixRedis, false},
		{"plain redis", "127.0.0.1:8080", unixDB, plainRedis, false},
		{"redis skips verify", "127.0.0.1:8080", unixDB, insecureRedis, false},
		{"missing redis", "127.0.0.1:8080", unixDB, nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := validBusinessNetwork(tc.address, tc.dsn, tc.redis); got != tc.want {
				t.Fatal("unexpected network boundary validation")
			}
		})
	}
}

func TestHealthCheckTimeoutAndPanicAreFailures(t *testing.T) {
	router := newRouter(func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() }, func(context.Context) error { return nil }, 10*time.Millisecond)
	result := httptest.NewRecorder()
	router.ServeHTTP(result, httptest.NewRequest(http.MethodGet, "/health/ready", nil))
	if result.Code != http.StatusServiceUnavailable {
		t.Fatalf("timeout status=%d", result.Code)
	}

	for _, panicAt := range []string{"mysql", "redis"} {
		t.Run(panicAt+" panic", func(t *testing.T) {
			mysqlPing := func(context.Context) error {
				if panicAt == "mysql" {
					panic("fixture-secret")
				}
				return nil
			}
			redisPing := func(context.Context) error {
				if panicAt == "redis" {
					panic("fixture-secret")
				}
				return nil
			}
			router := newRouter(mysqlPing, redisPing, time.Second)
			result := httptest.NewRecorder()
			router.ServeHTTP(result, httptest.NewRequest(http.MethodGet, "/health/ready", nil))
			if result.Code != http.StatusInternalServerError || strings.Contains(result.Body.String(), "fixture-secret") {
				t.Fatal("panic was not safely handled")
			}
		})
	}
}

func TestCancelledRequestCannotReportReadiness(t *testing.T) {
	router := newRouter(func(context.Context) error { return nil }, func(context.Context) error { return nil }, time.Second)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	request := httptest.NewRequest(http.MethodGet, "/health/ready", nil).WithContext(ctx)
	result := httptest.NewRecorder()
	router.ServeHTTP(result, request)
	if result.Code != http.StatusServiceUnavailable {
		t.Fatalf("cancelled request reported status %d", result.Code)
	}
}
