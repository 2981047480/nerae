package main

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"
	"nerae/api/internal/config"
	"nerae/api/internal/modules/auth/trustedproxy"
)

// Uses only a fresh private socket-only database and Redis supplied by the
// harness. The server listens exclusively on loopback and never sends mail.
func TestAPIProcessPrivateServices(t *testing.T) {
	dsn, socket := os.Getenv("NERAE_API_PROCESS_TEST_DSN"), os.Getenv("NERAE_API_PROCESS_REDIS_SOCKET")
	if dsn == "" || socket == "" {
		t.Skip("private API process services not configured")
	}
	parsed, err := mysql.ParseDSN(dsn)
	if err != nil || parsed.Net != "unix" || parsed.DBName != "nerae_api_process_test" {
		t.Fatal("unexpected private test database")
	}
	reservation, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal("loopback reservation unavailable")
	}
	addr := reservation.Addr().String()
	if err := reservation.Close(); err != nil {
		t.Fatal("loopback reservation close failed")
	}
	redisURL := (&url.URL{Scheme: "unix", Path: socket}).String()
	cfg := config.Config{Environment: "test", HTTPAddress: addr, MySQLDSN: dsn, RedisURL: redisURL,
		HealthTimeout: 10 * time.Second, ShutdownTimeout: 3 * time.Second}
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	t.Setenv("NERAE_API_ENABLE_BUSINESS", "true")
	t.Setenv(apiSecretsFileEnv, "/private/tmp/nonexistent-nerae-api-secrets")
	if err := run(context.Background(), cfg, logger); err == nil {
		t.Fatal("missing business configuration started")
	}
	if listener, err := net.Listen("tcp", addr); err != nil {
		t.Fatal("failed startup opened the listener")
	} else {
		_ = listener.Close()
	}

	fixture := validAPIConfigFixture()
	path := testAPIConfigFile(t, encodeAPIConfig(t, fixture), 0o600)
	t.Setenv(apiSecretsFileEnv, path)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- run(ctx, cfg, logger) }()
	client := &http.Client{Timeout: time.Second}
	base := "http://" + addr
	ready := false
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case err := <-done:
			t.Fatalf("API stopped before readiness: %v", err)
		default:
		}
		resp, err := client.Get(base + "/health/ready")
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				ready = true
				break
			}
		}
		time.Sleep(25 * time.Millisecond)
	}
	if !ready {
		t.Fatal("API did not become ready")
	}
	device, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal("synthetic device key generation failed")
	}
	enc := base64.RawURLEncoding.EncodeToString
	body, _ := json.Marshal(map[string]any{"email": "api-process@example.test", "devicePublicKey": map[string]string{
		"kty": "EC", "crv": "P-256", "x": enc(device.X.FillBytes(make([]byte, 32))), "y": enc(device.Y.FillBytes(make([]byte, 32))),
	}})
	idem := make([]byte, 24)
	if _, err := rand.Read(idem); err != nil {
		t.Fatal("synthetic request key failed")
	}
	newRequest := func() *http.Request {
		t.Helper()
		req, err := http.NewRequest(http.MethodPost, base+"/auth/email/challenges", bytes.NewReader(body))
		if err != nil {
			t.Fatal("challenge request construction failed")
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", enc(idem))
		return req
	}
	for _, tc := range []struct {
		name                 string
		credentials, sources []string
		want                 int
	}{
		{"missing proxy credential", nil, []string{"203.0.113.9"}, http.StatusForbidden},
		{"wrong proxy credential", []string{enc(make([]byte, 32))}, []string{"203.0.113.9"}, http.StatusForbidden},
		{"duplicate proxy credentials", []string{fixture.ProxyCredential, fixture.ProxyCredential}, []string{"203.0.113.9"}, http.StatusForbidden},
		{"missing trusted source", []string{fixture.ProxyCredential}, nil, http.StatusServiceUnavailable},
		{"duplicate trusted source", []string{fixture.ProxyCredential}, []string{"203.0.113.9", "203.0.113.10"}, http.StatusServiceUnavailable},
		{"joined trusted source", []string{fixture.ProxyCredential}, []string{"203.0.113.9, 203.0.113.10"}, http.StatusServiceUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := newRequest()
			for _, credential := range tc.credentials {
				req.Header.Add(proxyCredentialHeader, credential)
			}
			for _, source := range tc.sources {
				req.Header.Add(trustedproxy.SourceIPHeader, source)
			}
			denied, err := client.Do(req)
			if err != nil {
				t.Fatal("local rejection request failed")
			}
			_ = denied.Body.Close()
			if denied.StatusCode != tc.want {
				t.Fatalf("unexpected rejection status %d", denied.StatusCode)
			}
		})
	}
	req := newRequest()
	req.Header.Set(trustedproxy.SourceIPHeader, "203.0.113.9")
	req.Header.Set(proxyCredentialHeader, fixture.ProxyCredential)
	response, err := client.Do(req)
	if err != nil {
		t.Fatal("local challenge request failed")
	}
	responseBody, readErr := io.ReadAll(io.LimitReader(response.Body, 8192))
	_ = response.Body.Close()
	var challenge struct {
		ChallengeID string `json:"challengeId"`
		Nonce       string `json:"nonce"`
	}
	if readErr != nil || response.StatusCode != http.StatusAccepted || response.Header.Get("Cache-Control") != "no-store" ||
		json.Unmarshal(responseBody, &challenge) != nil || challenge.ChallengeID == "" || challenge.Nonce == "" {
		t.Fatalf("mounted challenge returned status %d", response.StatusCode)
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatal("private database unavailable")
	}
	defer db.Close()
	var count int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM auth_email_outbox WHERE challenge_id=?`, []byte(challenge.ChallengeID)).Scan(&count); err != nil || count != 1 {
		t.Fatal("mounted challenge did not enqueue exactly once")
	}
	meRequest, err := http.NewRequest(http.MethodGet, base+"/me", nil)
	if err != nil {
		t.Fatal("anonymous /me request construction failed")
	}
	meRequest.Header.Set(proxyCredentialHeader, fixture.ProxyCredential)
	me, err := client.Do(meRequest)
	if err != nil {
		t.Fatal("local /me request failed")
	}
	_ = me.Body.Close()
	if me.StatusCode != http.StatusUnauthorized {
		t.Fatalf("anonymous /me status %d", me.StatusCode)
	}
	exchangePath := "/auth/login-approvals/approval-0000000000001/exchange"
	exchangeRequest, err := http.NewRequest(http.MethodPost, base+exchangePath, strings.NewReader(`{}`))
	if err != nil {
		t.Fatal("approval exchange request construction failed")
	}
	exchangeRequest.Header.Set("Content-Type", "application/json")
	exchangeRequest.Header.Set("Idempotency-Key", enc(idem))
	exchangeRequest.Header.Set(trustedproxy.SourceIPHeader, "203.0.113.9")
	exchangeRequest.Header.Set(proxyCredentialHeader, fixture.ProxyCredential)
	exchangeResponse, err := client.Do(exchangeRequest)
	if err != nil {
		t.Fatal("local approval exchange request failed")
	}
	exchangeResponseBody, readErr := io.ReadAll(io.LimitReader(exchangeResponse.Body, 8192))
	_ = exchangeResponse.Body.Close()
	if readErr != nil || (exchangeResponse.StatusCode != http.StatusBadRequest && exchangeResponse.StatusCode != http.StatusUnprocessableEntity) ||
		exchangeResponse.Header.Get("Cache-Control") != "no-store" || bytes.Contains(exchangeResponseBody, []byte("approval-0000000000001")) {
		t.Fatalf("mounted approval exchange returned status %d", exchangeResponse.StatusCode)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal("API failed to shut down")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("API did not shut down")
	}
	if strings.Contains(logs.String(), fixture.CodeHash) || strings.Contains(logs.String(), fixture.ProxyCredential) || strings.Contains(logs.String(), path) || strings.Contains(logs.String(), challenge.Nonce) {
		t.Fatal("process log leaked protected configuration or challenge material")
	}
	t.Log("PASS: protected API process startup -> loopback business route -> enqueue -> clean shutdown")
	t.Log("PASS: protected API process exposes bounded approval exchange route")
}
