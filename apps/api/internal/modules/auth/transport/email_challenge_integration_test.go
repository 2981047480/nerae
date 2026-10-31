package transport

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/redis/go-redis/v9"
	api "nerae/api/internal/gen"
	"nerae/api/internal/middleware"
	"nerae/api/internal/modules/auth/bootstrap"
	"nerae/api/internal/modules/auth/challenge"
	"nerae/api/internal/modules/auth/codeepoch"
	"nerae/api/internal/modules/auth/domain"
	"nerae/api/internal/modules/auth/issuance"
	"nerae/api/internal/modules/auth/ratelimit"
	"nerae/api/internal/modules/auth/repository"
)

type noSendCode struct{}

func (noSendCode) SendVerificationCode(context.Context, string, string) error {
	return errors.New("mail delivery is outside this handler test")
}

func TestEmailChallengeHandlerRealIssuerOverLocalTLS(t *testing.T) {
	dsn := os.Getenv("NERAE_EMAIL_CHALLENGE_REQUEST_TEST_DSN")
	redisSocket := os.Getenv("NERAE_EMAIL_CHALLENGE_REQUEST_REDIS_SOCKET")
	if dsn == "" || redisSocket == "" {
		t.Skip("private socket-only MySQL and Redis are required")
	}
	cfg, err := mysql.ParseDSN(dsn)
	if err != nil || cfg.Net != "unix" || cfg.Addr == "" {
		t.Fatal("MySQL test DSN must use a Unix socket")
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatal("private MySQL could not open")
	}
	db.SetMaxOpenConns(12)
	db.SetMaxIdleConns(12)
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		t.Fatal("private MySQL unavailable")
	}
	rdb := redis.NewClient(&redis.Options{Network: "unix", Addr: redisSocket, DialTimeout: time.Second, ReadTimeout: 2 * time.Second, WriteTimeout: 2 * time.Second})
	if err := rdb.Ping(ctx).Err(); err != nil {
		_ = rdb.Close()
		_ = db.Close()
		t.Fatal("private Redis unavailable")
	}
	t.Cleanup(func() { _ = rdb.Close(); _ = db.Close() })

	identities, err := domain.NewEmailIdentityProtector(bytes.Repeat([]byte{0x31}, 32), bytes.Repeat([]byte{0x32}, 32), "a01-18-email-v1")
	if err != nil {
		t.Fatal("identity protector setup failed")
	}
	hasher, err := domain.NewEmailCodeHasher(bytes.Repeat([]byte{0x33}, 32))
	if err != nil {
		t.Fatal("code hasher setup failed")
	}
	replay, err := repository.NewProofReplayStore(bytes.Repeat([]byte{0x34}, 32))
	if err != nil {
		t.Fatal("proof replay setup failed")
	}
	challengeStore, err := challenge.NewStore(hasher, replay, identities, "fixture-email-code-hash-v1", challenge.Policy{ChallengeTTL: 5 * time.Minute, MaxAttempts: 3,
		DeviceProof: domain.ProofPolicy{MaxLifetime: 5 * time.Minute, ClockSkew: 30 * time.Second}})
	if err != nil {
		t.Fatal("challenge store setup failed")
	}
	deriver, err := issuance.NewDeriver(bytes.Repeat([]byte{0x35}, 32))
	if err != nil {
		t.Fatal("code deriver setup failed")
	}
	outbox, err := issuance.NewOutbox(deriver, "a01-18-code-v1", hasher, "fixture-email-code-hash-v1", identities, noSendCode{}, 2*time.Second)
	if err != nil {
		t.Fatal("outbox setup failed")
	}
	limitConfig := integrationRateConfig(t, 100)
	limiter, err := ratelimit.New(rdb, limitConfig)
	if err != nil {
		t.Fatal("rate limiter setup failed")
	}
	keys := requestKeyConfig()
	issuer, err := issuance.NewChallengeRequestIssuer(challengeStore, outbox, identities, limiter, keys)
	if err != nil {
		t.Fatal("challenge issuer setup failed")
	}
	identityEpoch, err := bootstrap.NewIdentityEpochInitializer(identities)
	if err != nil {
		t.Fatal("identity epoch initializer setup failed")
	}
	bootstrapTx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal("epoch bootstrap transaction failed")
	}
	if err := identityEpoch.Initialize(ctx, bootstrapTx); err != nil {
		_ = bootstrapTx.Rollback()
		t.Fatal("identity epoch initialization failed")
	}
	if err := outbox.InitializeCodeKey(ctx, bootstrapTx); err != nil {
		_ = bootstrapTx.Rollback()
		t.Fatal("code epoch initialization failed")
	}
	hashEpoch, err := codeepoch.New(hasher, "fixture-email-code-hash-v1")
	if err != nil {
		_ = bootstrapTx.Rollback()
		t.Fatal("code hash epoch setup failed")
	}
	if err := hashEpoch.Initialize(ctx, bootstrapTx); err != nil {
		_ = bootstrapTx.Rollback()
		t.Fatal("code hash epoch initialization failed")
	}
	if err := issuer.InitializeKey(ctx, bootstrapTx); err != nil {
		_ = bootstrapTx.Rollback()
		t.Fatal("request epoch initialization failed")
	}
	if err := bootstrapTx.Commit(); err != nil {
		t.Fatal("epoch bootstrap commit failed")
	}
	validator, err := middleware.NewSchemaValidator(16<<10, 2048)
	if err != nil {
		t.Fatal("schema validator setup failed")
	}
	trustedIP := netip.MustParseAddr("192.0.2.18")
	handler, err := NewEmailChallengeHandler(db, issuer, validator, TrustedSourceIPFunc(func(*http.Request) (netip.Addr, error) { return trustedIP, nil }))
	if err != nil {
		t.Fatal("HTTP handler setup failed")
	}
	server := httptest.NewTLSServer(handler)
	defer server.Close()

	public := integrationPublicKey(t)
	requestBody, err := json.Marshal(map[string]any{"email": "http-flow@example.test", "devicePublicKey": map[string]string{
		"kty": public.Kty, "crv": public.Crv, "x": public.X, "y": public.Y,
	}})
	if err != nil {
		t.Fatal("request encoding failed")
	}
	keyText := randomIdempotencyKey(t)
	post := func(body []byte, idempotencyKey string) *http.Response {
		t.Helper()
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, server.URL+emailChallengesPath, bytes.NewReader(body))
		if err != nil {
			t.Fatal("local HTTPS request setup failed")
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", idempotencyKey)
		// This spoofed header must not alter the explicitly injected source IP.
		req.Header.Set("X-Forwarded-For", "203.0.113.200")
		response, err := server.Client().Do(req)
		if err != nil {
			t.Fatal("local HTTPS handler request failed")
		}
		return response
	}
	first := post(requestBody, keyText)
	firstBody, err := io.ReadAll(first.Body)
	_ = first.Body.Close()
	if err != nil || first.StatusCode != http.StatusAccepted || first.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("first request did not return accepted/no-store (status %d)", first.StatusCode)
	}
	if bytes.Contains(firstBody, []byte("http-flow@example.test")) {
		t.Fatal("challenge success response leaked the submitted email")
	}
	if !validEncodedChallenge(validator, firstBody) {
		t.Fatal("issuer response did not satisfy the generated OpenAPI response schema")
	}
	second := post(requestBody, keyText)
	secondBody, err := io.ReadAll(second.Body)
	_ = second.Body.Close()
	if err != nil || second.StatusCode != http.StatusAccepted || !bytes.Equal(firstBody, secondBody) {
		t.Fatal("same-key replay did not return the exact protected response")
	}
	var changed map[string]any
	if err := json.Unmarshal(requestBody, &changed); err != nil {
		t.Fatal("test request parse failed")
	}
	changed["deviceLabel"] = "different label"
	changedBody, err := json.Marshal(changed)
	if err != nil {
		t.Fatal("changed request encoding failed")
	}
	conflict := post(changedBody, keyText)
	conflictBody, _ := io.ReadAll(conflict.Body)
	_ = conflict.Body.Close()
	if conflict.StatusCode != http.StatusConflict || conflict.Header.Get("Cache-Control") != "no-store" || bytes.Contains(conflictBody, []byte("http-flow@example.test")) {
		t.Fatal("changed same-key request was not safely rejected")
	}

	limitedConfig := integrationRateConfig(t, 1)
	limitedLimiter, err := ratelimit.New(rdb, limitedConfig)
	if err != nil {
		t.Fatal("limited test limiter setup failed")
	}
	limitedIssuer, err := issuance.NewChallengeRequestIssuer(challengeStore, outbox, identities, limitedLimiter, keys)
	if err != nil {
		t.Fatal("limited test issuer setup failed")
	}
	limitedHandler, err := NewEmailChallengeHandler(db, limitedIssuer, validator, TrustedSourceIPFunc(func(*http.Request) (netip.Addr, error) { return trustedIP, nil }))
	if err != nil {
		t.Fatal("limited handler setup failed")
	}
	limitedServer := httptest.NewTLSServer(limitedHandler)
	defer limitedServer.Close()
	for attempt := 0; attempt < 2; attempt++ {
		limitedReq, err := http.NewRequestWithContext(ctx, http.MethodPost, limitedServer.URL+emailChallengesPath, bytes.NewReader(requestBody))
		if err != nil {
			t.Fatal("limited request setup failed")
		}
		limitedReq.Header.Set("Content-Type", "application/json")
		limitedReq.Header.Set("Idempotency-Key", randomIdempotencyKey(t))
		limitedResponse, err := limitedServer.Client().Do(limitedReq)
		if err != nil {
			t.Fatal("limited local HTTPS request failed")
		}
		body, _ := io.ReadAll(limitedResponse.Body)
		_ = limitedResponse.Body.Close()
		want := http.StatusAccepted
		if attempt == 1 {
			want = http.StatusTooManyRequests
		}
		if limitedResponse.StatusCode != want || limitedResponse.Header.Get("Cache-Control") != "no-store" || bytes.Contains(body, []byte("http-flow@example.test")) {
			t.Fatalf("rate limit response status=%d want=%d", limitedResponse.StatusCode, want)
		}
	}

	closedDB, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatal("closed-DB test handle setup failed")
	}
	_ = closedDB.Close()
	failureHandler, err := NewEmailChallengeHandler(closedDB, issuer, validator, TrustedSourceIPFunc(func(*http.Request) (netip.Addr, error) { return trustedIP, nil }))
	if err != nil {
		t.Fatal("failure handler setup failed")
	}
	failureServer := httptest.NewTLSServer(failureHandler)
	defer failureServer.Close()
	failureReq, err := http.NewRequestWithContext(ctx, http.MethodPost, failureServer.URL+emailChallengesPath, bytes.NewReader(requestBody))
	if err != nil {
		t.Fatal("DB error test request setup failed")
	}
	failureReq.Header.Set("Content-Type", "application/json")
	failureReq.Header.Set("Idempotency-Key", randomIdempotencyKey(t))
	failureResponse, err := failureServer.Client().Do(failureReq)
	if err != nil {
		t.Fatal("local HTTPS DB error request failed")
	}
	failureBody, _ := io.ReadAll(failureResponse.Body)
	_ = failureResponse.Body.Close()
	if failureResponse.StatusCode != http.StatusServiceUnavailable || failureResponse.Header.Get("Cache-Control") != "no-store" || bytes.Contains(failureBody, []byte(dsn)) {
		t.Fatal("database failure was not redacted")
	}
	if os.Getenv("NERAE_AUTH_CLIENT_INTEROP") == "1" {
		cwd, err := os.Getwd()
		if err != nil {
			t.Fatal("could not locate client interop")
		}
		root, err := filepath.Abs(filepath.Join(cwd, "../../../../../.."))
		if err != nil {
			t.Fatal("could not locate repository root")
		}
		cert, err := x509.ParseCertificate(server.Certificate().Raw)
		if err != nil {
			t.Fatal("could not parse isolated server certificate")
		}
		caFile := filepath.Join(t.TempDir(), "local-test-ca.pem")
		if err := os.WriteFile(caFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw}), 0600); err != nil {
			t.Fatal("could not write isolated server trust")
		}
		command := exec.CommandContext(ctx, "/opt/homebrew/bin/node", filepath.Join(root, "docs/evidence/A01/A01-18/client_challenge_interop.mjs"))
		command.Env = append(os.Environ(), "NODE_EXTRA_CA_CERTS="+caFile, "NODE_TLS_REJECT_UNAUTHORIZED=1",
			"NERAE_AUTH_INTEROP_URL="+server.URL, "NERAE_AUTH_INTEROP_CLIENT_DIR="+filepath.Join(root, "apps/client/src/api/auth"))
		output, err := command.CombinedOutput()
		if err != nil || !bytes.Contains(output, []byte("PASS: actual email client adapter")) {
			t.Fatalf("isolated client interop failed: %s", output)
		}
		t.Log("PASS: real client adapter and challenge handler interoperated over isolated TLS")
	}
}

func integrationRateConfig(t *testing.T, emailLimit uint64) ratelimit.Config {
	t.Helper()
	policy := ratelimit.Policy{Email: ratelimit.DimensionPolicy{Limit: emailLimit, Window: time.Hour}, Device: ratelimit.DimensionPolicy{Limit: 100, Window: time.Hour},
		IP: ratelimit.DimensionPolicy{Limit: 100, Window: time.Hour}, Global: ratelimit.DimensionPolicy{Limit: 1000, Window: time.Hour}}
	keys := make([][]byte, 4)
	for index := range keys {
		keys[index] = make([]byte, 32)
		if _, err := rand.Read(keys[index]); err != nil {
			t.Fatal("synthetic limiter key generation failed")
		}
	}
	return ratelimit.Config{Keys: ratelimit.HMACKeys{EmailIndex: keys[0], DeviceFingerprint: keys[1], TrustedIP: keys[2], Global: keys[3]},
		IPv6PrefixBits: 64, EmailChallenge: policy, EmailLogin: policy}
}

func requestKeyConfig() issuance.RequestKeyConfig {
	return issuance.RequestKeyConfig{IdempotencyHMACKey: bytes.Repeat([]byte{0x41}, 32), RequestHMACKey: bytes.Repeat([]byte{0x42}, 32),
		ResponseAESKey: bytes.Repeat([]byte{0x43}, 32), KeyID: "a01-18-request-v1", Retention: time.Hour, CodeDigits: 6}
}

func randomIdempotencyKey(t *testing.T) string {
	t.Helper()
	value := make([]byte, 24)
	if _, err := rand.Read(value); err != nil {
		t.Fatal("synthetic idempotency key generation failed")
	}
	return base64.RawURLEncoding.EncodeToString(value)
}

func integrationPublicKey(t *testing.T) domain.DevicePublicJWK {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal("synthetic P-256 key generation failed")
	}
	return domain.DevicePublicJWK{Kty: "EC", Crv: "P-256", X: base64.RawURLEncoding.EncodeToString(key.X.FillBytes(make([]byte, 32))),
		Y: base64.RawURLEncoding.EncodeToString(key.Y.FillBytes(make([]byte, 32)))}
}

func validEncodedChallenge(validator *middleware.SchemaValidator, body []byte) bool {
	if len(body) == 0 || len(body) > 2048 || !strictWireJSON(body) {
		return false
	}
	var response api.EmailLoginChallenge
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&response) != nil || decoder.Decode(new(any)) != io.EOF || !validChallengeResponse(response) {
		return false
	}
	_, err := validator.EncodeResponse("EmailLoginChallenge", response)
	return err == nil
}
