package transport

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	issuancetestsupport "nerae/api/internal/modules/auth/issuance/testsupport"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	mysqlDriver "github.com/go-sql-driver/mysql"
	"github.com/redis/go-redis/v9"
	api "nerae/api/internal/gen"
	"nerae/api/internal/middleware"
	"nerae/api/internal/modules/auth/application"
	"nerae/api/internal/modules/auth/approval"
	"nerae/api/internal/modules/auth/challenge"
	"nerae/api/internal/modules/auth/domain"
	"nerae/api/internal/modules/auth/identity"
	"nerae/api/internal/modules/auth/loginledger"
	"nerae/api/internal/modules/auth/loginreceipt"
	"nerae/api/internal/modules/auth/ratelimit"
	"nerae/api/internal/modules/auth/repository"
	"nerae/api/internal/modules/auth/session"
)

func TestEmailLoginHTTPRealCoordinatorOverLocalTLS(t *testing.T) {
	dsn := os.Getenv("NERAE_EMAIL_LOGIN_HTTP_TEST_DSN")
	redisSocket := os.Getenv("NERAE_EMAIL_LOGIN_HTTP_REDIS_SOCKET")
	if dsn == "" || redisSocket == "" {
		t.Skip("private socket-only MySQL and Redis are required")
	}
	cfg, err := mysqlDriver.ParseDSN(dsn)
	if err != nil {
		t.Fatal("invalid isolated MySQL DSN")
	}
	socket := filepath.Clean(cfg.Addr)
	parent := filepath.Dir(socket)
	resolved, resolveErr := filepath.EvalSymlinks(parent)
	dirInfo, dirErr := os.Lstat(parent)
	socketInfo, socketErr := os.Lstat(socket)
	if cfg.Net != "unix" || cfg.DBName != "nerae_a01_23_test" || cfg.User != "root" || cfg.Passwd != "" ||
		!cfg.ParseTime || cfg.Loc != time.UTC || filepath.Base(socket) != "mysql.sock" || resolveErr != nil ||
		dirErr != nil || socketErr != nil || filepath.Dir(resolved) != "/private/tmp" ||
		!strings.HasPrefix(filepath.Base(resolved), "nerae-a01-23-mysql-") || !dirInfo.IsDir() ||
		dirInfo.Mode().Perm()&0077 != 0 || dirInfo.Mode()&os.ModeSymlink != 0 || socketInfo.Mode()&os.ModeSocket == 0 {
		t.Fatal("refusing non-isolated MySQL target")
	}
	redisParent := filepath.Dir(redisSocket)
	redisResolved, redisResolveErr := filepath.EvalSymlinks(redisParent)
	redisDir, redisDirErr := os.Lstat(redisParent)
	redisInfo, redisStatErr := os.Lstat(redisSocket)
	if redisResolveErr != nil || redisDirErr != nil || redisStatErr != nil || filepath.Dir(redisResolved) != "/private/tmp" ||
		filepath.Base(redisResolved) != filepath.Base(resolved) || filepath.Base(redisSocket) != "redis.sock" ||
		!redisDir.IsDir() || redisDir.Mode().Perm()&0077 != 0 || redisDir.Mode()&os.ModeSymlink != 0 ||
		redisInfo.Mode()&os.ModeSocket == 0 {
		t.Fatal("refusing non-isolated Redis target")
	}

	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatal("private MySQL could not open")
	}
	db.SetMaxOpenConns(12)
	db.SetMaxIdleConns(12)
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		t.Fatal("private MySQL unavailable")
	}
	rdb := redis.NewClient(&redis.Options{Network: "unix", Addr: redisSocket, DialTimeout: time.Second,
		ReadTimeout: 2 * time.Second, WriteTimeout: 2 * time.Second})
	if err := rdb.Ping(ctx).Err(); err != nil {
		_ = rdb.Close()
		_ = db.Close()
		t.Fatal("private Redis unavailable")
	}
	t.Cleanup(func() { _ = rdb.Close(); _ = db.Close() })

	identities, err := domain.NewEmailIdentityProtector(bytes.Repeat([]byte{0x31}, 32), bytes.Repeat([]byte{0x32}, 32), "a01-23-email-v1")
	if err != nil {
		t.Fatal("identity protector setup failed")
	}
	hasher, err := domain.NewEmailCodeHasher(bytes.Repeat([]byte{0x33}, 32))
	if err != nil {
		t.Fatal("code hasher setup failed")
	}
	proofReplay, err := repository.NewProofReplayStore(bytes.Repeat([]byte{0x34}, 32))
	if err != nil {
		t.Fatal("proof replay setup failed")
	}
	challenges, err := challenge.NewStore(hasher, proofReplay, identities, "fixture-email-code-hash-v1", challenge.Policy{ChallengeTTL: 5 * time.Minute,
		MaxAttempts: 3, DeviceProof: domain.ProofPolicy{MaxLifetime: 5 * time.Minute, ClockSkew: 30 * time.Second}})
	if err != nil {
		t.Fatal("challenge store setup failed")
	}
	issuancetestsupport.InitializeEmailCodeHashEpoch(t, db, identities, hasher)
	resolver, err := identity.NewResolver(identities)
	if err != nil {
		t.Fatal("identity resolver setup failed")
	}
	codec, err := session.NewTokenCodec(bytes.Repeat([]byte{0x35}, 32), session.TokenPolicy{MaxLifetime: time.Hour})
	if err != nil {
		t.Fatal("session token codec setup failed")
	}
	sessions, err := session.NewStore(resolver, codec)
	if err != nil {
		t.Fatal("session store setup failed")
	}
	approvals, err := approval.NewStore(resolver, bytes.Repeat([]byte{0x36}, 32), "a01-23-approval-v1",
		approval.Policy{Lifetime: 5 * time.Minute, ReauthWindow: time.Minute})
	if err != nil {
		t.Fatal("approval store setup failed")
	}
	crypto, err := loginreceipt.New(loginreceipt.KeyConfig{IdempotencyHMACKey: bytes.Repeat([]byte{0x37}, 32),
		RequestHMACKey: bytes.Repeat([]byte{0x38}, 32), AES256GCMKey: bytes.Repeat([]byte{0x39}, 32)})
	if err != nil {
		t.Fatal("receipt crypto setup failed")
	}
	ledger, err := loginledger.NewStore(crypto, identities, "a01-23-receipt-v1")
	if err != nil {
		t.Fatal("receipt ledger setup failed")
	}
	initTx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal("epoch initialization transaction failed")
	}
	if err := approvals.InitializeKey(ctx, initTx); err != nil {
		_ = initTx.Rollback()
		t.Fatal("approval epoch initialization failed")
	}
	if err := ledger.Initialize(ctx, initTx); err != nil {
		_ = initTx.Rollback()
		t.Fatal("receipt epoch initialization failed")
	}
	if err := initTx.Commit(); err != nil {
		t.Fatal("epoch initialization commit failed")
	}
	login, err := application.NewEmailLogin(challenges, sessions, approvals)
	if err != nil {
		t.Fatal("email login setup failed")
	}
	receipt, err := application.NewReceiptLogin(login, ledger, crypto, 2*time.Minute)
	if err != nil {
		t.Fatal("receipt login setup failed")
	}
	trustedIP := netip.MustParseAddr("192.0.2.231")
	limiter, err := newEmailLoginHTTPLimiter(rdb, 100, 100, 100, 500)
	if err != nil {
		t.Fatal("rate limiter setup failed")
	}
	coordinator, err := application.NewEmailLoginCoordinator(challenges, limiter, receipt)
	if err != nil {
		t.Fatal("login coordinator setup failed")
	}
	validator, err := middleware.NewSchemaValidator(16<<10, 2048)
	if err != nil {
		t.Fatal("schema validator setup failed")
	}
	resolverIP := TrustedSourceIPFunc(func(*http.Request) (netip.Addr, error) { return trustedIP, nil })
	loginHandler, err := NewEmailLoginHandler(db, coordinator, sessions, validator, resolverIP)
	if err != nil {
		t.Fatal("email login handler setup failed")
	}
	meHandler, err := NewCurrentUserHandler(db, sessions, validator)
	if err != nil {
		t.Fatal("current-user handler setup failed")
	}
	approvalListHandler, err := NewApprovalListHandler(db, approvals, sessions, sessions, validator)
	if err != nil {
		t.Fatal("approval list handler setup failed")
	}
	mux := http.NewServeMux()
	mux.Handle("/auth/email-login", loginHandler)
	mux.Handle("/me", meHandler)
	mux.Handle(approvalListPath, approvalListHandler)
	server := httptest.NewTLSServer(mux)
	defer server.Close()

	issue := func(email string, public domain.DevicePublicJWK) (string, string) {
		t.Helper()
		id, err := domain.GenerateChallengeID()
		if err != nil {
			t.Fatal("synthetic challenge ID generation failed")
		}
		nonce, err := domain.GenerateChallengeNonce()
		if err != nil {
			t.Fatal("synthetic nonce generation failed")
		}
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal("challenge transaction setup failed")
		}
		if err := challenges.Issue(ctx, tx, challenge.IssueInput{Email: email, ChallengeID: id, Nonce: nonce,
			Code: "012345", PublicKey: public}); err != nil {
			_ = tx.Rollback()
			t.Fatal("synthetic challenge issue failed")
		}
		if err := tx.Commit(); err != nil {
			t.Fatal("synthetic challenge commit failed")
		}
		return id, nonce
	}
	post := func(body []byte, key string) (int, http.Header, []byte) {
		t.Helper()
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, server.URL+emailLoginPath, bytes.NewReader(body))
		if err != nil {
			t.Fatal("TLS login request setup failed")
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", key)
		req.Header.Set("X-Forwarded-For", "203.0.113.250")
		response, err := server.Client().Do(req)
		if err != nil {
			t.Fatal("TLS login request failed")
		}
		bodyBytes, readErr := io.ReadAll(response.Body)
		_ = response.Body.Close()
		if readErr != nil {
			t.Fatal("TLS login response read failed")
		}
		if response.Header.Get("Cache-Control") != "no-store" {
			t.Fatal("login response was cacheable")
		}
		return response.StatusCode, response.Header.Clone(), bodyBytes
	}
	getMe := func(token string) (int, http.Header, []byte) {
		t.Helper()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/me", nil)
		if err != nil {
			t.Fatal("TLS current-user request setup failed")
		}
		req.Header.Set("Authorization", "Bearer "+token)
		response, err := server.Client().Do(req)
		if err != nil {
			t.Fatal("TLS current-user request failed")
		}
		bodyBytes, readErr := io.ReadAll(response.Body)
		_ = response.Body.Close()
		if readErr != nil {
			t.Fatal("TLS current-user response read failed")
		}
		if response.Header.Get("Cache-Control") != "no-store" {
			t.Fatal("current-user response was cacheable")
		}
		return response.StatusCode, response.Header.Clone(), bodyBytes
	}
	getApprovals := func(token string) (int, []byte) {
		t.Helper()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+approvalListPath, nil)
		if err != nil {
			t.Fatal("approval list request setup failed")
		}
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		req.Header.Set("X-User-ID", "999")
		response, err := server.Client().Do(req)
		if err != nil {
			t.Fatal("approval list TLS request failed")
		}
		payload, readErr := io.ReadAll(response.Body)
		_ = response.Body.Close()
		if readErr != nil || response.Header.Get("Cache-Control") != "no-store" {
			t.Fatal("approval list response unavailable or cacheable")
		}
		return response.StatusCode, payload
	}

	device, public := emailLoginHTTPDevice(t)
	challengeID, nonce := issue("a01-23-login@example.test", public)
	input := map[string]string{"challengeId": challengeID, "code": "012345",
		"deviceProof": emailLoginHTTPProof(t, device, challengeID, nonce)}
	requestBody, err := json.Marshal(input)
	if err != nil {
		t.Fatal("synthetic login request encoding failed")
	}
	idempotencyKey := emailLoginHTTPKey(t)
	status, headers, body := post(requestBody, idempotencyKey)
	var first api.PhoneAuthSuccess
	if status != http.StatusOK || headers.Get("X-Request-ID") == "" || json.Unmarshal(body, &first) != nil ||
		first.Status != api.Authenticated || first.Token == "" || first.ExpiresIn < 1 || first.User.Id == "" || first.User.Roles == nil ||
		first.User.AgeDeclarationVersion == nil || *first.User.AgeDeclarationVersion != 1 {
		t.Fatalf("real coordinator login did not return the contract success response: %s", safeEmailLoginHTTPDiagnostic(status, body, first))
	}
	firstExpiry := first.ExpiresIn
	status, _, replayBody := post(requestBody, idempotencyKey)
	var replay api.PhoneAuthSuccess
	if status != http.StatusOK || json.Unmarshal(replayBody, &replay) != nil || replay.Token != first.Token ||
		replay.ExpiresIn < 1 || replay.ExpiresIn > firstExpiry {
		t.Fatal("same-key replay changed the token or increased the remaining lifetime")
	}

	var current api.User
	status, _, meBody := getMe(first.Token)
	if status != http.StatusOK || json.Unmarshal(meBody, &current) != nil || current.Id != first.User.Id || current.Nickname != first.User.Nickname {
		t.Fatal("GET /me did not return the subject from the authenticated session")
	}
	if bytes.Contains(meBody, []byte(first.Token)) {
		t.Fatal("GET /me exposed the bearer token")
	}
	status, emptyApprovals := getApprovals(first.Token)
	if status != http.StatusOK || string(emptyApprovals) != `{"items":[]}` {
		t.Fatal("fresh authenticated account did not get an empty approval list")
	}

	changedInput := map[string]string{"challengeId": challengeID, "code": "999999", "deviceProof": input["deviceProof"]}
	changedBody, err := json.Marshal(changedInput)
	if err != nil {
		t.Fatal("changed request encoding failed")
	}
	status, _, conflictBody := post(changedBody, idempotencyKey)
	if status != http.StatusConflict || bytes.Contains(conflictBody, []byte(first.Token)) {
		t.Fatal("changed same-key request did not return a redacted conflict")
	}

	badDevice, badPublic := emailLoginHTTPDevice(t)
	badID, badNonce := issue("a01-23-wrong-code@example.test", badPublic)
	badRequest := map[string]string{"challengeId": badID, "code": "999999",
		"deviceProof": emailLoginHTTPProof(t, badDevice, badID, badNonce)}
	badBody, err := json.Marshal(badRequest)
	if err != nil {
		t.Fatal("wrong-code request encoding failed")
	}
	badKey := emailLoginHTTPKey(t)
	for attempt := 0; attempt < 2; attempt++ {
		status, _, errorBody := post(badBody, badKey)
		if status != http.StatusUnauthorized || bytes.Contains(errorBody, []byte("999999")) || bytes.Contains(errorBody, []byte(badRequest["deviceProof"])) {
			t.Fatal("wrong-code response was not generic and uncached")
		}
	}
	var attempts, rejectedReceipts int
	if err := db.QueryRowContext(ctx, `SELECT attempts FROM auth_email_login_challenge WHERE challenge_id=?`, badID).Scan(&attempts); err != nil || attempts != 1 {
		t.Fatal("exact wrong-code retry did not persist exactly one attempt")
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM auth_email_login_receipt WHERE challenge_id=? AND result_kind='rejected'`, badID).Scan(&rejectedReceipts); err != nil || rejectedReceipts != 1 {
		t.Fatal("wrong-code retry did not preserve one committed rejection receipt")
	}

	newDevice, newPublic := emailLoginHTTPDevice(t)
	newID, newNonce := issue("a01-23-login@example.test", newPublic)
	newRequest := map[string]string{"challengeId": newID, "code": "012345",
		"deviceProof": emailLoginHTTPProof(t, newDevice, newID, newNonce)}
	newBody, err := json.Marshal(newRequest)
	if err != nil {
		t.Fatal("new-device request encoding failed")
	}
	newKey := emailLoginHTTPKey(t)
	status, _, approvalBody := post(newBody, newKey)
	var pending api.PhoneAuthApprovalRequired
	if status != http.StatusOK || json.Unmarshal(approvalBody, &pending) != nil || pending.Status != api.ApprovalRequired ||
		pending.ApprovalId == "" || pending.ApprovalSecret == "" || pending.Nonce == "" || pending.ComparisonCode == "" || pending.ExpiresAt.IsZero() {
		t.Fatal("new device did not receive a pending approval response")
	}
	var pendingFields map[string]json.RawMessage
	if json.Unmarshal(approvalBody, &pendingFields) != nil || pendingFields["token"] != nil {
		t.Fatal("new device approval response exposed a session token")
	}
	status, _, approvalReplayBody := post(newBody, newKey)
	var replayPending api.PhoneAuthApprovalRequired
	if status != http.StatusOK || json.Unmarshal(approvalReplayBody, &replayPending) != nil ||
		replayPending.ApprovalId != pending.ApprovalId || replayPending.ApprovalSecret != pending.ApprovalSecret {
		t.Fatal("new-device replay did not preserve the original pending approval")
	}
	status, ownerApprovals := getApprovals(first.Token)
	var list api.LoginApprovalList
	if status != http.StatusOK || json.Unmarshal(ownerApprovals, &list) != nil || len(list.Items) != 1 ||
		list.Items[0].Id != pending.ApprovalId || list.Items[0].ComparisonCode != pending.ComparisonCode ||
		bytes.Contains(ownerApprovals, []byte(pending.ApprovalSecret)) || bytes.Contains(ownerApprovals, []byte(pending.Nonce)) ||
		bytes.Contains(ownerApprovals, []byte(first.Token)) {
		t.Fatal("owner approval list did not match the pending request or exposed credentials")
	}
	otherDevice, otherPublic := emailLoginHTTPDevice(t)
	otherID, otherNonce := issue("a01-23-other@example.test", otherPublic)
	otherBody, err := json.Marshal(map[string]string{"challengeId": otherID, "code": "012345",
		"deviceProof": emailLoginHTTPProof(t, otherDevice, otherID, otherNonce)})
	if err != nil {
		t.Fatal("second subject login encoding failed")
	}
	status, _, otherLoginBody := post(otherBody, emailLoginHTTPKey(t))
	var other api.PhoneAuthSuccess
	if status != http.StatusOK || json.Unmarshal(otherLoginBody, &other) != nil || other.Token == "" || other.User.Id == first.User.Id {
		t.Fatal("second synthetic subject did not authenticate")
	}
	status, otherApprovals := getApprovals(other.Token)
	if status != http.StatusOK || string(otherApprovals) != `{"items":[]}` || bytes.Contains(otherApprovals, []byte(pending.ApprovalId)) {
		t.Fatal("other subject could see the owner's pending approval")
	}
	status, _ = getApprovals("")
	if status != http.StatusUnauthorized {
		t.Fatal("anonymous approval listing was accepted")
	}
	// All rows are synthetic and live only in the private test database.
	for i := 0; i < 100; i++ {
		id := fmt.Sprintf("overflow%016d", i)
		challenge := fmt.Sprintf("challenge%015d", i)
		_, err := db.ExecContext(ctx, `INSERT INTO auth_login_approval
			(id, user_id, identity_id, challenge_id, token_version, identity_version, target_fingerprint,
			 public_x, public_y, secret_digest, key_id, nonce, comparison_code, state, decision_session_id, created_at, expires_at, decided_at)
			SELECT ?, user_id, identity_id, ?, token_version, identity_version, target_fingerprint,
			 public_x, public_y, secret_digest, key_id, nonce, comparison_code, state, decision_session_id, created_at, expires_at, decided_at
			FROM auth_login_approval WHERE id = ?`, []byte(id), []byte(challenge), []byte(pending.ApprovalId))
		if err != nil {
			t.Fatal("could not stage overflow fixture")
		}
	}
	status, overflowApprovals := getApprovals(first.Token)
	if status != http.StatusServiceUnavailable || bytes.Contains(overflowApprovals, []byte(pending.ApprovalId)) {
		t.Fatal("overflow list returned a partial result")
	}
	if _, err := db.ExecContext(ctx, `DELETE FROM auth_login_approval WHERE id LIKE 'overflow%'`); err != nil {
		t.Fatal("could not clear private overflow fixture")
	}
	if _, err := db.ExecContext(ctx, `UPDATE auth_login_approval SET created_at=UTC_TIMESTAMP(6)-INTERVAL 2 MINUTE,
		expires_at=UTC_TIMESTAMP(6)-INTERVAL 1 MINUTE WHERE id=?`, []byte(pending.ApprovalId)); err != nil {
		t.Fatal("could not stage expired approval fixture")
	}
	status, expiredApprovals := getApprovals(first.Token)
	if status != http.StatusOK || string(expiredApprovals) != `{"items":[]}` {
		t.Fatal("expired approval remained in the HTTP list")
	}

	malformed := []byte(strings.TrimSuffix(string(requestBody), "}") + `,"unexpected":"value"}`)
	status, _, malformedBody := post(malformed, emailLoginHTTPKey(t))
	if status != http.StatusUnprocessableEntity || bytes.Contains(malformedBody, []byte(first.Token)) {
		t.Fatal("schema-invalid body was not rejected before login")
	}
	var malformedAttempts int
	if err := db.QueryRowContext(ctx, `SELECT attempts FROM auth_email_login_challenge WHERE challenge_id=?`, challengeID).Scan(&malformedAttempts); err != nil || malformedAttempts != 0 {
		t.Fatal("schema-invalid request changed the challenge attempt count")
	}

	limitedLimiter, err := newEmailLoginHTTPLimiter(rdb, 100, 100, 1, 100)
	if err != nil {
		t.Fatal("limited rate limiter setup failed")
	}
	if err := limitedLimiter.AllowLoginEntry(ctx, trustedIP); err != nil {
		t.Fatal("could not seed private IP entry budget")
	}
	limitedCoordinator, err := application.NewEmailLoginCoordinator(challenges, limitedLimiter, receipt)
	if err != nil {
		t.Fatal("limited coordinator setup failed")
	}
	closedDB, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatal("closed database handle setup failed")
	}
	_ = closedDB.Close()
	limitedHandler, err := NewEmailLoginHandler(closedDB, limitedCoordinator, sessions, validator, resolverIP)
	if err != nil {
		t.Fatal("limited handler setup failed")
	}
	limitedServer := httptest.NewTLSServer(limitedHandler)
	limitedRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, limitedServer.URL+emailLoginPath, bytes.NewReader(requestBody))
	if err != nil {
		limitedServer.Close()
		t.Fatal("limited request setup failed")
	}
	limitedRequest.Header.Set("Content-Type", "application/json")
	limitedRequest.Header.Set("Idempotency-Key", emailLoginHTTPKey(t))
	limitedResponse, err := limitedServer.Client().Do(limitedRequest)
	if err != nil {
		limitedServer.Close()
		t.Fatal("limited TLS request failed")
	}
	limitedBody, readErr := io.ReadAll(limitedResponse.Body)
	_ = limitedResponse.Body.Close()
	limitedServer.Close()
	if readErr != nil || limitedResponse.StatusCode != http.StatusTooManyRequests || limitedResponse.Header.Get("Cache-Control") != "no-store" ||
		bytes.Contains(limitedBody, []byte(first.Token)) {
		t.Fatal("entry quota did not fail closed before database or expose data")
	}

	if _, err := db.ExecContext(ctx, `UPDATE user SET status='disabled' WHERE id=?`, first.User.Id); err != nil {
		t.Fatal("could not disable the synthetic account")
	}
	status, _, disabledMe := getMe(first.Token)
	if status != http.StatusUnauthorized || responseContainsCredentialOrUser(disabledMe, first.Token) {
		t.Fatal("disabled account still received a current-user response")
	}
	status, disabledApprovals := getApprovals(first.Token)
	if status != http.StatusUnauthorized || bytes.Contains(disabledApprovals, []byte(pending.ApprovalId)) {
		t.Fatal("disabled account still received its pending approval")
	}
	status, _, disabledReplay := post(requestBody, idempotencyKey)
	if status != http.StatusUnauthorized || responseContainsCredentialOrUser(disabledReplay, first.Token) {
		t.Fatal("disabled account replay returned profile or session credentials")
	}

	t.Log("PASS: A01-23 real coordinator, login handler, /me handler and receipt replays over local TLS")
}

func newEmailLoginHTTPLimiter(client *redis.Client, email, device, ip, global uint64) (*ratelimit.Limiter, error) {
	secret := func() []byte {
		value := make([]byte, 32)
		if _, err := rand.Read(value); err != nil {
			return nil
		}
		return value
	}
	emailKey, deviceKey, ipKey, globalKey := secret(), secret(), secret(), secret()
	if emailKey == nil || deviceKey == nil || ipKey == nil || globalKey == nil {
		return nil, errors.New("synthetic limiter key generation failed")
	}
	policy := ratelimit.Policy{Email: ratelimit.DimensionPolicy{Limit: email, Window: time.Minute},
		Device: ratelimit.DimensionPolicy{Limit: device, Window: time.Minute},
		IP:     ratelimit.DimensionPolicy{Limit: ip, Window: time.Minute},
		Global: ratelimit.DimensionPolicy{Limit: global, Window: time.Minute}}
	return ratelimit.New(client, ratelimit.Config{Keys: ratelimit.HMACKeys{EmailIndex: emailKey,
		DeviceFingerprint: deviceKey, TrustedIP: ipKey, Global: globalKey}, IPv6PrefixBits: 64,
		EmailLogin: policy, EmailChallenge: policy})
}

func emailLoginHTTPDevice(t *testing.T) (*ecdsa.PrivateKey, domain.DevicePublicJWK) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal("synthetic device key generation failed")
	}
	encode := base64.RawURLEncoding.EncodeToString
	public := domain.DevicePublicJWK{Kty: "EC", Crv: "P-256",
		X: encode(key.X.FillBytes(make([]byte, 32))), Y: encode(key.Y.FillBytes(make([]byte, 32)))}
	return key, public
}

func emailLoginHTTPProof(t *testing.T, key *ecdsa.PrivateKey, challengeID, nonce string) string {
	t.Helper()
	jti, err := domain.GenerateChallengeID()
	if err != nil {
		t.Fatal("synthetic proof JTI generation failed")
	}
	now := time.Now().UTC()
	payload, err := json.Marshal(map[string]any{"aud": "nerae-auth-device", "purpose": "email_login", "resourceId": challengeID,
		"nonce": nonce, "iat": now.Unix(), "exp": now.Add(time.Minute).Unix(), "jti": jti})
	if err != nil {
		t.Fatal("synthetic device proof encoding failed")
	}
	encode := base64.RawURLEncoding.EncodeToString
	unsigned := encode([]byte(`{"alg":"ES256","typ":"JWT"}`)) + "." + encode(payload)
	digest := sha256.Sum256([]byte(unsigned))
	r, s, err := ecdsa.Sign(rand.Reader, key, digest[:])
	if err != nil {
		t.Fatal("synthetic device proof signing failed")
	}
	signature := append(r.FillBytes(make([]byte, 32)), s.FillBytes(make([]byte, 32))...)
	return unsigned + "." + encode(signature)
}

func emailLoginHTTPKey(t *testing.T) string {
	t.Helper()
	value := make([]byte, 24)
	if _, err := rand.Read(value); err != nil {
		t.Fatal("synthetic idempotency key generation failed")
	}
	return base64.RawURLEncoding.EncodeToString(value)
}

func responseContainsCredentialOrUser(body []byte, token string) bool {
	if bytes.Contains(body, []byte(token)) {
		return true
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(body, &fields) != nil {
		return true
	}
	return fields["token"] != nil || fields["user"] != nil || fields["approvalSecret"] != nil
}

func safeEmailLoginHTTPDiagnostic(status int, body []byte, response api.PhoneAuthSuccess) string {
	var errorBody struct {
		Code string `json:"code"`
	}
	_ = json.Unmarshal(body, &errorBody)
	return fmt.Sprintf("http=%d errorCode=%q authenticated=%t tokenPresent=%t userIdPresent=%t roles=%d expiresIn=%d",
		status, errorBody.Code, response.Status == api.Authenticated, response.Token != "", response.User.Id != "", len(response.User.Roles), response.ExpiresIn)
}
