package transport

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"database/sql"
	"encoding/json"
	"encoding/pem"
	"io"
	"math/big"
	"mime/quotedprintable"
	"net"
	"net/http"
	"net/http/httptest"
	netmail "net/mail"
	"net/netip"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	mysqlDriver "github.com/go-sql-driver/mysql"
	"github.com/redis/go-redis/v9"
	api "nerae/api/internal/gen"
	"nerae/api/internal/middleware"
	"nerae/api/internal/modules/auth/application"
	"nerae/api/internal/modules/auth/approval"
	"nerae/api/internal/modules/auth/bootstrap"
	"nerae/api/internal/modules/auth/challenge"
	"nerae/api/internal/modules/auth/codeepoch"
	"nerae/api/internal/modules/auth/domain"
	"nerae/api/internal/modules/auth/identity"
	"nerae/api/internal/modules/auth/issuance"
	"nerae/api/internal/modules/auth/loginledger"
	"nerae/api/internal/modules/auth/loginreceipt"
	authmail "nerae/api/internal/modules/auth/mail"
	"nerae/api/internal/modules/auth/mailworker"
	"nerae/api/internal/modules/auth/ratelimit"
	"nerae/api/internal/modules/auth/repository"
	"nerae/api/internal/modules/auth/session"
	workoutapp "nerae/api/internal/modules/workout/application"
	workoutrepo "nerae/api/internal/modules/workout/repository"
	workouttransport "nerae/api/internal/modules/workout/transport"
)

// This test-only assembly exercises the real challenge issuer, outbox worker,
// local TLS SMTP adapter, login coordinator, HTTP handlers and /me handler.
// It does not mount production routes or contact an external mail service.
func TestEmailChallengeSMTPLoginAgeAndWorkoutHTTPOverLocalTLS(t *testing.T) {
	dsn := os.Getenv("NERAE_EMAIL_DELIVERY_E2E_TEST_DSN")
	redisSocket := os.Getenv("NERAE_EMAIL_DELIVERY_E2E_REDIS_SOCKET")
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
	validFixture := (cfg.DBName == "nerae_a01_24_test" && strings.HasPrefix(filepath.Base(resolved), "nerae-a01-24-")) ||
		(cfg.DBName == "nerae_a01_25_test" && strings.HasPrefix(filepath.Base(resolved), "nerae-a01-25-"))
	if cfg.Net != "unix" || !validFixture || cfg.User != "root" || cfg.Passwd != "" ||
		!cfg.ParseTime || cfg.Loc != time.UTC || filepath.Base(socket) != "mysql.sock" || resolveErr != nil ||
		dirErr != nil || socketErr != nil || filepath.Dir(resolved) != "/private/tmp" || !dirInfo.IsDir() ||
		dirInfo.Mode().Perm()&0077 != 0 || dirInfo.Mode()&os.ModeSymlink != 0 || socketInfo.Mode()&os.ModeSocket == 0 {
		t.Fatal("refusing non-isolated MySQL target")
	}
	redisParent := filepath.Dir(redisSocket)
	redisResolved, redisResolveErr := filepath.EvalSymlinks(redisParent)
	redisDir, redisDirErr := os.Lstat(redisParent)
	redisInfo, redisStatErr := os.Lstat(redisSocket)
	if redisResolveErr != nil || redisDirErr != nil || redisStatErr != nil || filepath.Dir(redisResolved) != "/private/tmp" ||
		filepath.Base(redisResolved) != filepath.Base(resolved) || filepath.Base(redisSocket) != "redis.sock" ||
		!redisDir.IsDir() || redisDir.Mode().Perm()&0077 != 0 || redisDir.Mode()&os.ModeSymlink != 0 || redisInfo.Mode()&os.ModeSocket == 0 {
		t.Fatal("refusing non-isolated Redis target")
	}

	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatal("private MySQL could not open")
	}
	db.SetMaxOpenConns(16)
	db.SetMaxIdleConns(16)
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

	identities, err := domain.NewEmailIdentityProtector(bytes.Repeat([]byte{0x41}, 32), bytes.Repeat([]byte{0x42}, 32), "a01-24-email-v1")
	if err != nil {
		t.Fatal("identity protector setup failed")
	}
	hasher, err := domain.NewEmailCodeHasher(bytes.Repeat([]byte{0x43}, 32))
	if err != nil {
		t.Fatal("code hasher setup failed")
	}
	proofReplay, err := repository.NewProofReplayStore(bytes.Repeat([]byte{0x44}, 32))
	if err != nil {
		t.Fatal("proof replay setup failed")
	}
	challenges, err := challenge.NewStore(hasher, proofReplay, identities, "fixture-email-code-hash-v1", challenge.Policy{ChallengeTTL: 5 * time.Minute,
		MaxAttempts: 3, DeviceProof: domain.ProofPolicy{MaxLifetime: 5 * time.Minute, ClockSkew: 30 * time.Second}})
	if err != nil {
		t.Fatal("challenge store setup failed")
	}
	deriver, err := issuance.NewDeriver(bytes.Repeat([]byte{0x45}, 32))
	if err != nil {
		t.Fatal("code derivation setup failed")
	}
	smtp, smtpMessages, smtpListener, smtpConnections := localDeliverySMTP(t)
	defer smtpListener.Close()
	outbox, err := issuance.NewOutbox(deriver, "a01-24-code-v1", hasher, "fixture-email-code-hash-v1", identities, smtp, 20*time.Second)
	if err != nil {
		t.Fatal("outbox setup failed")
	}
	challengeLimiter, err := ratelimit.New(rdb, integrationRateConfig(t, 100))
	if err != nil {
		t.Fatal("challenge limiter setup failed")
	}
	requestKeys := requestKeyConfig()
	// The HTTP producer has no SMTP sender; the separate worker outbox above
	// consumes the same database queue using the same key material.
	producer, err := issuance.NewEnqueueOutbox(deriver, "a01-24-code-v1", hasher, "fixture-email-code-hash-v1", identities)
	if err != nil {
		t.Fatal("enqueue-only outbox setup failed")
	}
	issuer, err := issuance.NewChallengeRequestIssuer(challenges, producer, identities, challengeLimiter, requestKeys)
	if err != nil {
		t.Fatal("challenge issuer setup failed")
	}
	identityEpoch, err := bootstrap.NewIdentityEpochInitializer(identities)
	if err != nil {
		t.Fatal("identity epoch initializer setup failed")
	}
	bootstrapTx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal("epoch initialization transaction failed")
	}
	hashEpoch, err := codeepoch.New(hasher, "fixture-email-code-hash-v1")
	if err != nil {
		_ = bootstrapTx.Rollback()
		t.Fatal("code hash epoch setup failed")
	}
	for _, initialize := range []func(context.Context, *sql.Tx) error{identityEpoch.Initialize, outbox.InitializeCodeKey, hashEpoch.Initialize, issuer.InitializeKey} {
		if err := initialize(ctx, bootstrapTx); err != nil {
			_ = bootstrapTx.Rollback()
			t.Fatal("challenge epoch initialization failed")
		}
	}
	if err := bootstrapTx.Commit(); err != nil {
		t.Fatal("challenge epoch commit failed")
	}

	resolver, err := identity.NewResolver(identities)
	if err != nil {
		t.Fatal("identity resolver setup failed")
	}
	codec, err := session.NewTokenCodec(bytes.Repeat([]byte{0x46}, 32), session.TokenPolicy{MaxLifetime: time.Hour})
	if err != nil {
		t.Fatal("session token codec setup failed")
	}
	sessions, err := session.NewStore(resolver, codec)
	if err != nil {
		t.Fatal("session store setup failed")
	}
	approvals, err := approval.NewStore(resolver, bytes.Repeat([]byte{0x47}, 32), "a01-24-approval-v1", approval.Policy{Lifetime: 5 * time.Minute, ReauthWindow: time.Minute})
	if err != nil {
		t.Fatal("approval store setup failed")
	}
	receiptCrypto, err := loginreceipt.New(loginreceipt.KeyConfig{IdempotencyHMACKey: bytes.Repeat([]byte{0x48}, 32),
		RequestHMACKey: bytes.Repeat([]byte{0x49}, 32), AES256GCMKey: bytes.Repeat([]byte{0x4a}, 32)})
	if err != nil {
		t.Fatal("receipt crypto setup failed")
	}
	ledger, err := loginledger.NewStore(receiptCrypto, identities, "a01-24-receipt-v1")
	if err != nil {
		t.Fatal("receipt ledger setup failed")
	}
	epochTx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal("login epoch transaction failed")
	}
	if err := approvals.InitializeKey(ctx, epochTx); err != nil {
		_ = epochTx.Rollback()
		t.Fatal("approval epoch initialization failed")
	}
	if err := ledger.Initialize(ctx, epochTx); err != nil {
		_ = epochTx.Rollback()
		t.Fatal("receipt epoch initialization failed")
	}
	if err := epochTx.Commit(); err != nil {
		t.Fatal("login epoch commit failed")
	}
	login, err := application.NewEmailLogin(challenges, sessions, approvals)
	if err != nil {
		t.Fatal("email login setup failed")
	}
	receipt, err := application.NewReceiptLogin(login, ledger, receiptCrypto, 2*time.Minute)
	if err != nil {
		t.Fatal("receipt login setup failed")
	}
	trustedIP := netip.MustParseAddr("192.0.2.224")
	loginLimiter, err := newEmailLoginHTTPLimiter(rdb, 100, 100, 100, 500)
	if err != nil {
		t.Fatal("login limiter setup failed")
	}
	coordinator, err := application.NewEmailLoginCoordinator(challenges, loginLimiter, receipt)
	if err != nil {
		t.Fatal("login coordinator setup failed")
	}
	validator, err := middleware.NewSchemaValidator(4<<20, 100000)
	if err != nil {
		t.Fatal("schema validator setup failed")
	}
	challengeHandler, err := NewEmailChallengeHandler(db, issuer, validator, TrustedSourceIPFunc(func(*http.Request) (netip.Addr, error) { return trustedIP, nil }))
	if err != nil {
		t.Fatal("challenge handler setup failed")
	}
	loginHandler, err := NewEmailLoginHandler(db, coordinator, sessions, validator, TrustedSourceIPFunc(func(*http.Request) (netip.Addr, error) { return trustedIP, nil }))
	if err != nil {
		t.Fatal("login handler setup failed")
	}
	meHandler, err := NewCurrentUserHandler(db, sessions, validator)
	if err != nil {
		t.Fatal("current-user handler setup failed")
	}
	logoutHandler, err := NewCurrentSessionDeleteHandler(db, sessions, validator)
	if err != nil {
		t.Fatal("current-session delete handler setup failed")
	}
	ageHandler, err := NewAgeVerifyHandler(db, sessions, sessions, validator)
	if err != nil {
		t.Fatal("age verification handler setup failed")
	}
	exerciseProtector, err := workoutrepo.NewExerciseNameProtector(bytes.Repeat([]byte{0x51}, 32), "a01-24-workout-test-key")
	if err != nil {
		t.Fatal("workout exercise protector setup failed")
	}
	workoutInitTx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal("workout exercise-key transaction failed")
	}
	if err := exerciseProtector.InitializeExerciseKey(ctx, workoutInitTx); err != nil {
		_ = workoutInitTx.Rollback()
		t.Fatal("workout exercise-key initialization failed")
	}
	if err := workoutInitTx.Commit(); err != nil {
		t.Fatal("workout exercise-key commit failed")
	}
	records, err := workoutrepo.NewRecordStore(exerciseProtector)
	if err != nil {
		t.Fatal("workout record store setup failed")
	}
	workoutService, err := workoutapp.New(sessions, workoutrepo.NewSessionStore(), records)
	if err != nil {
		t.Fatal("workout service setup failed")
	}
	workoutExecutor, err := workoutapp.NewWorkoutExecutor(sessions, bytes.Repeat([]byte{0x52}, 32), bytes.Repeat([]byte{0x53}, 32), "a01-24-workout-v1", time.Hour)
	if err != nil {
		t.Fatal("workout idempotency executor setup failed")
	}
	decisionExecutor, err := approval.NewDecisionExecutor(approvals, sessions, bytes.Repeat([]byte{0x52}, 32), bytes.Repeat([]byte{0x53}, 32), "a01-24-workout-v1", time.Hour)
	if err != nil {
		t.Fatal("approval decision executor setup failed")
	}
	workoutKeyTx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal("workout idempotency-key transaction failed")
	}
	if err := workoutExecutor.InitializeKey(ctx, workoutKeyTx); err != nil {
		_ = workoutKeyTx.Rollback()
		t.Fatal("workout idempotency-key initialization failed")
	}
	if err := decisionExecutor.Initialize(ctx, workoutKeyTx); err != nil {
		_ = workoutKeyTx.Rollback()
		t.Fatal("approval decision epoch did not match the shared transport epoch")
	}
	if err := workoutKeyTx.Commit(); err != nil {
		t.Fatal("workout idempotency-key commit failed")
	}
	workoutWriteHandler, err := workouttransport.NewWriteHandler(db, workoutService, validator, workoutExecutor)
	if err != nil {
		t.Fatal("workout write handler setup failed")
	}
	workoutReadHandler, err := workouttransport.NewReadHandler(db, workoutService, validator)
	if err != nil {
		t.Fatal("workout read handler setup failed")
	}
	workoutHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			workoutReadHandler.ServeHTTP(w, r)
			return
		}
		workoutWriteHandler.ServeHTTP(w, r)
	})
	decisionLimiter, err := ratelimit.NewApprovalDecisionLimiter(challengeLimiter, ratelimit.ApprovalDecisionPolicy{
		Subject: ratelimit.DimensionPolicy{Limit: 10, Window: time.Minute}, Approval: ratelimit.DimensionPolicy{Limit: 3, Window: time.Minute},
		IP: ratelimit.DimensionPolicy{Limit: 100, Window: time.Minute}, Global: ratelimit.DimensionPolicy{Limit: 100, Window: time.Minute},
	})
	if err != nil {
		t.Fatal("test approval rate policy setup failed")
	}
	decisionHandler, err := NewApprovalDecisionHandler(db, decisionExecutor, sessions, decisionLimiter, validator,
		TrustedSourceIPFunc(func(*http.Request) (netip.Addr, error) { return trustedIP, nil }))
	if err != nil {
		t.Fatal("approval decision handler setup failed")
	}
	approvalListHandler, err := NewApprovalListHandler(db, approvals, sessions, sessions, validator)
	if err != nil {
		t.Fatal("approval list handler setup failed")
	}
	mux := http.NewServeMux()
	mux.Handle(emailChallengesPath, challengeHandler)
	mux.Handle(emailLoginPath, loginHandler)
	mux.Handle("/me", meHandler)
	mux.Handle("/auth/sessions/current", logoutHandler)
	mux.Handle(ageVerifyPath, ageHandler)
	mux.Handle("/me/login-approvals", approvalListHandler)
	mux.Handle("/me/login-approvals/", decisionHandler)
	mux.Handle("/me/sessions", workoutHandler)
	mux.Handle("/me/sessions/", workoutHandler)
	server := httptest.NewTLSServer(mux)
	defer server.Close()

	device, public := emailLoginHTTPDevice(t)
	challengeBody, err := json.Marshal(map[string]any{"email": "a01-24-e2e@example.test", "devicePublicKey": map[string]string{
		"kty": public.Kty, "crv": public.Crv, "x": public.X, "y": public.Y,
	}})
	if err != nil {
		t.Fatal("challenge request encoding failed")
	}
	challengeKey := randomIdempotencyKey(t)
	post := func(path string, body []byte, key string) (int, http.Header, []byte) {
		t.Helper()
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, server.URL+path, bytes.NewReader(body))
		if err != nil {
			t.Fatal("local TLS request setup failed")
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", key)
		req.Header.Set("X-Forwarded-For", "203.0.113.99") // test proves handler uses injected trusted IP
		response, err := server.Client().Do(req)
		if err != nil {
			t.Fatal("local TLS request failed")
		}
		payload, readErr := io.ReadAll(response.Body)
		_ = response.Body.Close()
		if readErr != nil {
			t.Fatal("local TLS response read failed")
		}
		return response.StatusCode, response.Header.Clone(), payload
	}
	status, headers, issuedBody := post(emailChallengesPath, challengeBody, challengeKey)
	if status != http.StatusAccepted || headers.Get("Cache-Control") != "no-store" || !validEncodedChallenge(validator, issuedBody) {
		t.Fatal("challenge endpoint did not return a contract-valid accepted response")
	}
	var issued api.EmailLoginChallenge
	if err := json.Unmarshal(issuedBody, &issued); err != nil || issued.ChallengeId == "" || issued.Nonce == "" {
		t.Fatal("challenge response lacked its opaque proof inputs")
	}
	status, _, replayedIssue := post(emailChallengesPath, challengeBody, challengeKey)
	if status != http.StatusAccepted || !bytes.Equal(issuedBody, replayedIssue) {
		t.Fatal("challenge idempotency replay changed its response")
	}
	var outboxRows int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM auth_email_outbox WHERE challenge_id=?`, []byte(issued.ChallengeId)).Scan(&outboxRows); err != nil || outboxRows != 1 {
		t.Fatal("challenge request did not create exactly one outbox row")
	}
	worker, err := mailworker.New(mailworker.Config{Outbox: outbox, DB: db, PollInterval: 100 * time.Millisecond, RecoveryBatch: 10})
	if err != nil {
		t.Fatal("mail worker setup failed")
	}
	if err := worker.RunOnce(ctx); err != nil {
		t.Fatal("outbox worker iteration failed")
	}
	if state := outboxState(t, ctx, db, issued.ChallengeId); state != string(issuance.DeliveryAccepted) || smtpConnections.Load() != 1 {
		t.Fatal("worker did not submit exactly one queued message to the local SMTP sink")
	}
	message := <-smtpMessages
	if message.err != nil || message.message == "" {
		t.Fatal("local SMTP sink did not receive a complete message")
	}
	code := deliveredCode(t, message.message, "a01-24-e2e@example.test")
	expectedCode, err := deriver.Derive(string(issued.ChallengeId), "email_login", 6)
	if err != nil || code != expectedCode {
		t.Fatal("SMTP-delivered code did not match the challenge-bound derivation")
	}

	challengeID := string(issued.ChallengeId)
	wrong := code
	if wrong == "000000" {
		wrong = "000001"
	} else {
		wrong = "000000"
	}
	wrongBody, err := json.Marshal(map[string]string{"challengeId": challengeID, "code": wrong,
		"deviceProof": emailLoginHTTPProof(t, device, challengeID, string(issued.Nonce))})
	if err != nil {
		t.Fatal("wrong-code body encoding failed")
	}
	wrongKey := emailLoginHTTPKey(t)
	for i := 0; i < 2; i++ {
		status, responseHeaders, body := post(emailLoginPath, wrongBody, wrongKey)
		if status != http.StatusUnauthorized || responseHeaders.Get("Cache-Control") != "no-store" || bytes.Contains(body, []byte(wrong)) {
			t.Fatal("wrong code was not rejected generically and without caching")
		}
	}
	var attempts int
	if err := db.QueryRowContext(ctx, `SELECT attempts FROM auth_email_login_challenge WHERE challenge_id=?`, []byte(challengeID)).Scan(&attempts); err != nil || attempts != 1 {
		t.Fatal("wrong-code idempotent retry did not persist one attempt")
	}

	loginBody, err := json.Marshal(map[string]string{"challengeId": challengeID, "code": code,
		"deviceProof": emailLoginHTTPProof(t, device, challengeID, string(issued.Nonce))})
	if err != nil {
		t.Fatal("login body encoding failed")
	}
	loginKey := emailLoginHTTPKey(t)
	status, headers, successBody := post(emailLoginPath, loginBody, loginKey)
	var success api.PhoneAuthSuccess
	if status != http.StatusOK || headers.Get("Cache-Control") != "no-store" || json.Unmarshal(successBody, &success) != nil ||
		success.Status != api.Authenticated || success.Token == "" || success.User.Id == "" || success.ExpiresIn < 1 ||
		success.User.AgeDeclarationVersion == nil || *success.User.AgeDeclarationVersion != 1 {
		t.Fatal("delivered code did not authenticate through the real login handler")
	}
	status, _, replayBody := post(emailLoginPath, loginBody, loginKey)
	var replay api.PhoneAuthSuccess
	if status != http.StatusOK || json.Unmarshal(replayBody, &replay) != nil || replay.Token != success.Token || replay.ExpiresIn > success.ExpiresIn {
		t.Fatal("login receipt replay changed the original token or increased its lifetime")
	}
	meRequest, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/me", nil)
	if err != nil {
		t.Fatal("/me request setup failed")
	}
	meRequest.Header.Set("Authorization", "Bearer "+success.Token)
	meResponse, err := server.Client().Do(meRequest)
	if err != nil {
		t.Fatal("/me request failed")
	}
	meBody, readErr := io.ReadAll(meResponse.Body)
	_ = meResponse.Body.Close()
	var user api.User
	if readErr != nil || meResponse.StatusCode != http.StatusOK || meResponse.Header.Get("Cache-Control") != "no-store" ||
		json.Unmarshal(meBody, &user) != nil || user.Id != success.User.Id || user.AgeDeclarationVersion == nil ||
		*user.AgeDeclarationVersion != 1 || bytes.Contains(meBody, []byte(success.Token)) {
		t.Fatal("/me did not authenticate the issued session without exposing its token")
	}

	// A second synthetic device must be approved by the existing session. All
	// mail is delivered to the local SMTP sink; this is not a QQ or device test.
	newDevice, newPublic := emailLoginHTTPDevice(t)
	newChallengeBody, err := json.Marshal(map[string]any{"email": "a01-24-e2e@example.test", "devicePublicKey": map[string]string{
		"kty": newPublic.Kty, "crv": newPublic.Crv, "x": newPublic.X, "y": newPublic.Y,
	}})
	if err != nil {
		t.Fatal("new-device challenge encoding failed")
	}
	status, _, newChallengeResponse := post(emailChallengesPath, newChallengeBody, randomIdempotencyKey(t))
	var newChallenge api.EmailLoginChallenge
	if status != http.StatusAccepted || json.Unmarshal(newChallengeResponse, &newChallenge) != nil || newChallenge.ChallengeId == "" {
		t.Fatal("new-device challenge did not start")
	}
	if err := worker.RunOnce(ctx); err != nil {
		t.Fatal("new-device local mail delivery failed")
	}
	var newMessage localSMTPMessage
	select {
	case newMessage = <-smtpMessages:
	case <-ctx.Done():
		t.Fatal("timed out waiting for new-device synthetic SMTP message")
	}
	if newMessage.err != nil {
		t.Fatal("new-device local SMTP sink failed")
	}
	newCode := deliveredCode(t, newMessage.message, "a01-24-e2e@example.test")
	newLoginBody, err := json.Marshal(map[string]string{"challengeId": string(newChallenge.ChallengeId), "code": newCode,
		"deviceProof": emailLoginHTTPProof(t, newDevice, string(newChallenge.ChallengeId), string(newChallenge.Nonce))})
	if err != nil {
		t.Fatal("new-device login encoding failed")
	}
	status, _, newLoginResponse := post(emailLoginPath, newLoginBody, randomIdempotencyKey(t))
	var pending api.PhoneAuthApprovalRequired
	if status != http.StatusOK || json.Unmarshal(newLoginResponse, &pending) != nil || string(pending.Status) != "approval_required" ||
		pending.ApprovalId == "" || pending.ComparisonCode == "" || pending.ApprovalSecret == "" {
		t.Fatal("new device did not receive a pending approval")
	}
	listRequest, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/me/login-approvals", nil)
	if err != nil {
		t.Fatal("approval list request failed")
	}
	listRequest.Header.Set("Authorization", "Bearer "+success.Token)
	listResponse, err := server.Client().Do(listRequest)
	if err != nil {
		t.Fatal("approval list HTTP call failed")
	}
	listBody, readErr := io.ReadAll(listResponse.Body)
	_ = listResponse.Body.Close()
	if readErr != nil || listResponse.StatusCode != http.StatusOK || !bytes.Contains(listBody, []byte(pending.ApprovalId)) ||
		!bytes.Contains(listBody, []byte(pending.ComparisonCode)) || bytes.Contains(listBody, []byte(pending.ApprovalSecret)) {
		t.Fatal("existing device could not safely inspect the pending approval")
	}
	decide := func(body []byte, key string) (int, http.Header, []byte) {
		t.Helper()
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, server.URL+"/me/login-approvals/"+string(pending.ApprovalId)+"/decision", bytes.NewReader(body))
		if err != nil {
			t.Fatal("approval decision request failed")
		}
		req.Header.Set("Authorization", "Bearer "+success.Token)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", key)
		resp, err := server.Client().Do(req)
		if err != nil {
			t.Fatal("approval decision HTTP call failed")
		}
		payload, readErr := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if readErr != nil || resp.Header.Get("Cache-Control") != "no-store" || bytes.Contains(payload, []byte(success.Token)) || bytes.Contains(payload, []byte(pending.ComparisonCode)) {
			t.Fatal("approval decision response was unsafe")
		}
		return resp.StatusCode, resp.Header.Clone(), payload
	}
	incorrect := "000000"
	if pending.ComparisonCode == incorrect {
		incorrect = "999999"
	}
	badDecision, _ := json.Marshal(map[string]any{"decision": "approve", "comparisonCode": incorrect, "confirmed": true})
	if code, _, _ := decide(badDecision, randomIdempotencyKey(t)); code != http.StatusForbidden {
		t.Fatalf("wrong approval code status %d", code)
	}
	goodDecision, _ := json.Marshal(map[string]any{"decision": "approve", "comparisonCode": pending.ComparisonCode, "confirmed": true})
	decisionKey := randomIdempotencyKey(t)
	decisionStatus, _, decided := decide(goodDecision, decisionKey)
	if decisionStatus != http.StatusOK || string(decided) != `{"status":"approved"}` {
		t.Fatalf("authenticated decision status %d", decisionStatus)
	}
	decisionStatus, decisionHeaders, replayedDecision := decide(goodDecision, decisionKey)
	if decisionStatus != http.StatusOK || decisionHeaders.Get("Idempotency-Replayed") != "true" || !bytes.Equal(decided, replayedDecision) {
		t.Fatal("approval decision retry did not recover the same receipt")
	}
	if code, _, _ := decide(goodDecision, decisionKey); code != http.StatusTooManyRequests {
		t.Fatalf("per-approval decision rate limit status %d", code)
	}
	var savedState string
	if err := db.QueryRowContext(ctx, `SELECT state FROM auth_login_approval WHERE id = ?`, []byte(pending.ApprovalId)).Scan(&savedState); err != nil || savedState != "approved" {
		t.Fatal("approval decision did not persist its terminal state")
	}
	t.Log("PASS: new-device approval decision, authorized retry and Redis attempt bound")

	postAge := func(client *http.Client, baseURL string, token string, body []byte) (int, http.Header, []byte) {
		t.Helper()
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+ageVerifyPath, bytes.NewReader(body))
		if err != nil {
			t.Fatal("age declaration request setup failed")
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("X-User-ID", "999") // response must remain bound to the authenticated bearer
		response, err := client.Do(req)
		if err != nil {
			t.Fatal("age declaration local TLS request failed")
		}
		payload, readErr := io.ReadAll(response.Body)
		_ = response.Body.Close()
		if readErr != nil {
			t.Fatal("age declaration response read failed")
		}
		return response.StatusCode, response.Header.Clone(), payload
	}
	type workoutHTTPResult struct {
		status  int
		headers http.Header
		body    []byte
	}
	responseSecrets := []string{success.Token}
	workoutRequest := func(method, path string, body []byte, token, key string) workoutHTTPResult {
		t.Helper()
		var requestBody io.Reader
		if body != nil {
			requestBody = bytes.NewReader(body)
		}
		req, err := http.NewRequestWithContext(ctx, method, server.URL+path, requestBody)
		if err != nil {
			t.Fatal("workout HTTP request setup failed")
		}
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		if key != "" {
			req.Header.Set("Idempotency-Key", key)
		}
		response, err := server.Client().Do(req)
		if err != nil {
			t.Fatal("workout local TLS request failed")
		}
		payload, readErr := io.ReadAll(io.LimitReader(response.Body, 1<<20))
		_ = response.Body.Close()
		if readErr != nil {
			t.Fatal("workout HTTP response read failed")
		}
		if response.Header.Get("Cache-Control") != "no-store" {
			t.Fatalf("workout response status %d was cacheable", response.StatusCode)
		}
		for _, secret := range responseSecrets {
			if secret != "" && bytes.Contains(payload, []byte(secret)) {
				t.Fatal("workout HTTP response leaked a bearer token")
			}
		}
		return workoutHTTPResult{status: response.StatusCode, headers: response.Header.Clone(), body: payload}
	}
	beforeAgeBody, err := json.Marshal(map[string]any{"source": "free", "date": time.Now().UTC().Format("2006-01-02"),
		"timeZone": "UTC", "clientEventId": "a01-24-before-age-001"})
	if err != nil {
		t.Fatal("pre-declaration workout body encoding failed")
	}
	if result := workoutRequest(http.MethodPost, "/me/sessions", beforeAgeBody, "", randomIdempotencyKey(t)); result.status != http.StatusUnauthorized {
		t.Fatalf("anonymous workout creation did not fail closed: %d", result.status)
	}
	if result := workoutRequest(http.MethodPost, "/me/sessions", beforeAgeBody, success.Token, randomIdempotencyKey(t)); result.status != http.StatusForbidden {
		t.Fatalf("workout creation before age declaration did not fail closed: %d", result.status)
	}
	var deniedCount int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM workout_session WHERE client_event_id = ?`,
		[]byte("a01-24-before-age-001")).Scan(&deniedCount); err != nil || deniedCount != 0 {
		t.Fatal("denied anonymous or pre-declaration creation left a workout")
	}
	ageBody := []byte(`{"isAdult":true,"expectedVersion":1}`)
	status, ageHeaders, ageResponse := postAge(server.Client(), server.URL, success.Token, ageBody)
	var declaredUser api.User
	if status != http.StatusOK || ageHeaders.Get("Cache-Control") != "no-store" || json.Unmarshal(ageResponse, &declaredUser) != nil ||
		declaredUser.Id != success.User.Id || declaredUser.AgeVerified == nil || !*declaredUser.AgeVerified ||
		declaredUser.AgeDeclarationVersion == nil || *declaredUser.AgeDeclarationVersion != 2 || bytes.Contains(ageResponse, []byte(success.Token)) {
		t.Fatal("age declaration did not return the authenticated user's current declaration state")
	}
	status, staleHeaders, staleResponse := postAge(server.Client(), server.URL, success.Token, ageBody)
	if status != http.StatusPreconditionFailed || staleHeaders.Get("Cache-Control") != "no-store" || bytes.Contains(staleResponse, []byte(success.Token)) {
		t.Fatal("stale age declaration version was not rejected without caching")
	}
	var ageVersion uint64
	var ageVerified bool
	if err := db.QueryRowContext(ctx, `SELECT age_declaration_version, age_verified_at IS NOT NULL FROM user WHERE id = ?`, success.User.Id).Scan(&ageVersion, &ageVerified); err != nil || ageVersion != 2 || !ageVerified {
		t.Fatal("stale version retry changed the committed age declaration")
	}

	// Interleave a withdrawal after the affirmative write but before the
	// handler's mandatory post-commit reauthorization. The handler must return
	// 412 rather than a stale success body.
	raceProfiles := ageProfileFunc(func(ctx context.Context, profileDB *sql.DB, token string) (session.ResponseProfile, error) {
		if _, err := sessions.ClearAdultDeclarationAtVersion(ctx, profileDB, token, 3); err != nil {
			return session.ResponseProfile{}, err
		}
		return sessions.ProfileForResponse(ctx, profileDB, token)
	})
	raceAgeHandler, err := NewAgeVerifyHandler(db, sessions, raceProfiles, validator)
	if err != nil {
		t.Fatal("concurrent age-state handler setup failed")
	}
	raceServer := httptest.NewTLSServer(raceAgeHandler)
	defer raceServer.Close()
	raceBody := []byte(`{"isAdult":true,"expectedVersion":2}`)
	status, raceHeaders, raceResponse := postAge(raceServer.Client(), raceServer.URL, success.Token, raceBody)
	if status != http.StatusPreconditionFailed || raceHeaders.Get("Cache-Control") != "no-store" || bytes.Contains(raceResponse, []byte(success.Token)) {
		t.Fatal("concurrent withdrawal after commit produced a stale success response")
	}
	if err := db.QueryRowContext(ctx, `SELECT age_declaration_version, age_verified_at IS NOT NULL FROM user WHERE id = ?`, success.User.Id).Scan(&ageVersion, &ageVerified); err != nil || ageVersion != 4 || ageVerified {
		t.Fatal("interleaved withdrawal did not remain the authoritative current state")
	}

	validAfterRace := []byte(`{"isAdult":true,"expectedVersion":4}`)
	status, latestHeaders, latestResponse := postAge(server.Client(), server.URL, success.Token, validAfterRace)
	if status != http.StatusOK || latestHeaders.Get("Cache-Control") != "no-store" || json.Unmarshal(latestResponse, &declaredUser) != nil ||
		declaredUser.Id != success.User.Id || declaredUser.AgeVerified == nil || !*declaredUser.AgeVerified || declaredUser.AgeDeclarationVersion == nil || *declaredUser.AgeDeclarationVersion != 5 {
		t.Fatal("current versioned self-declaration did not recover after explicit state review")
	}

	workoutDate := time.Now().UTC().Format("2006-01-02")
	createBody, err := json.Marshal(map[string]any{"source": "free", "date": workoutDate, "timeZone": "UTC", "clientEventId": "a01-24-workout-event-001"})
	if err != nil {
		t.Fatal("workout creation body encoding failed")
	}
	createKey := randomIdempotencyKey(t)
	created := workoutRequest(http.MethodPost, "/me/sessions", createBody, success.Token, createKey)
	var createdDetail struct {
		ID      string `json:"id"`
		Status  string `json:"status"`
		Version int    `json:"version"`
	}
	if created.status != http.StatusCreated || created.headers.Get("Idempotency-Key-Expires-At") == "" ||
		json.Unmarshal(created.body, &createdDetail) != nil || createdDetail.ID == "" || createdDetail.Status != "in_progress" || createdDetail.Version != 1 {
		t.Fatalf("authenticated workout creation failed: %d %s", created.status, created.body)
	}
	createReplay := workoutRequest(http.MethodPost, "/me/sessions", createBody, success.Token, createKey)
	if createReplay.status != http.StatusCreated || !bytes.Equal(createReplay.body, created.body) || createReplay.headers.Get("Idempotency-Key-Expires-At") != created.headers.Get("Idempotency-Key-Expires-At") {
		t.Fatal("same-key workout creation replay differed from its original response")
	}
	var createdCount int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM workout_session WHERE user_id = ? AND client_event_id = ?`,
		success.User.Id, []byte("a01-24-workout-event-001")).Scan(&createdCount); err != nil || createdCount != 1 {
		t.Fatal("same-key creation replay did not preserve exactly one workout")
	}
	setPath := "/me/sessions/" + createdDetail.ID + "/sets"
	setBody := []byte(`{"expectedVersion":1,"exerciseEntryKeys":["a01-24-entry-001"],"sets":[{"recordKey":"a01-24-record-001","exerciseEntryKey":"a01-24-entry-001","exerciseName":"卧推","kind":"weight","setIndex":1,"weightKg":42.50,"reps":8,"rpe":7.5,"completed":true}]}`)
	setKey := randomIdempotencyKey(t)
	// Training-after batch save and per-set confirmation both map to this same write endpoint.
	saved := workoutRequest(http.MethodPost, setPath, setBody, success.Token, setKey)
	var savedDetail struct {
		ID      string `json:"id"`
		Status  string `json:"status"`
		Version int    `json:"version"`
		Sets    []struct {
			RecordKey string      `json:"recordKey"`
			WeightKg  json.Number `json:"weightKg"`
			Completed bool        `json:"completed"`
		} `json:"sets"`
	}
	if saved.status != http.StatusOK || saved.headers.Get("Idempotency-Key-Expires-At") == "" || json.Unmarshal(saved.body, &savedDetail) != nil ||
		savedDetail.ID != createdDetail.ID || savedDetail.Status != "in_progress" || savedDetail.Version != 2 || len(savedDetail.Sets) != 1 ||
		savedDetail.Sets[0].RecordKey != "a01-24-record-001" || savedDetail.Sets[0].WeightKg.String() != "42.50" || !savedDetail.Sets[0].Completed {
		t.Fatalf("authenticated workout set write failed or lost exact data: %d %s", saved.status, saved.body)
	}
	setReplay := workoutRequest(http.MethodPost, setPath, setBody, success.Token, setKey)
	if setReplay.status != http.StatusOK || !bytes.Equal(setReplay.body, saved.body) || setReplay.headers.Get("Idempotency-Key-Expires-At") != saved.headers.Get("Idempotency-Key-Expires-At") {
		t.Fatal("same-key workout set replay differed from its original response")
	}
	readPath := "/me/sessions/" + createdDetail.ID
	ownerRead := workoutRequest(http.MethodGet, readPath, nil, success.Token, "")
	var readDetail struct {
		ID      string `json:"id"`
		Status  string `json:"status"`
		Version int    `json:"version"`
		Sets    []struct {
			RecordKey string      `json:"recordKey"`
			WeightKg  json.Number `json:"weightKg"`
		} `json:"sets"`
	}
	if ownerRead.status != http.StatusOK || json.Unmarshal(ownerRead.body, &readDetail) != nil || readDetail.ID != createdDetail.ID ||
		readDetail.Status != "in_progress" || readDetail.Version != 2 || len(readDetail.Sets) != 1 || readDetail.Sets[0].RecordKey != "a01-24-record-001" || readDetail.Sets[0].WeightKg.String() != "42.50" {
		t.Fatalf("owner readback differed from saved response: %d %s", ownerRead.status, ownerRead.body)
	}

	// Authenticate a second synthetic principal through the same challenge, SMTP, and login HTTP path.
	otherDevice, otherPublicKey := emailLoginHTTPDevice(t)
	otherEmail := "a01-24-other@example.test"
	otherChallengeBody, err := json.Marshal(map[string]any{"email": otherEmail, "devicePublicKey": map[string]string{
		"kty": otherPublicKey.Kty, "crv": otherPublicKey.Crv, "x": otherPublicKey.X, "y": otherPublicKey.Y,
	}})
	if err != nil {
		t.Fatal("second synthetic challenge body encoding failed")
	}
	otherChallengeKey := randomIdempotencyKey(t)
	status, otherChallengeHeaders, otherChallengeResponse := post(emailChallengesPath, otherChallengeBody, otherChallengeKey)
	var otherIssued api.EmailLoginChallenge
	if status != http.StatusAccepted || otherChallengeHeaders.Get("Cache-Control") != "no-store" || json.Unmarshal(otherChallengeResponse, &otherIssued) != nil || otherIssued.ChallengeId == "" || otherIssued.Nonce == "" {
		t.Fatalf("second subject challenge failed: %d %s", status, otherChallengeResponse)
	}
	if err := worker.RunOnce(ctx); err != nil {
		t.Fatal("second outbox worker iteration failed")
	}
	var otherMessage localSMTPMessage
	select {
	case otherMessage = <-smtpMessages:
	case <-ctx.Done():
		t.Fatal("timed out waiting for second synthetic SMTP message")
	}
	otherCode := deliveredCode(t, otherMessage.message, otherEmail)
	expectedOtherCode, err := deriver.Derive(string(otherIssued.ChallengeId), "email_login", 6)
	if err != nil || otherMessage.err != nil || otherCode != expectedOtherCode || smtpConnections.Load() != 3 {
		t.Fatal("second SMTP-delivered code did not match its challenge")
	}
	otherLoginBody, err := json.Marshal(map[string]string{"challengeId": string(otherIssued.ChallengeId), "code": otherCode,
		"deviceProof": emailLoginHTTPProof(t, otherDevice, string(otherIssued.ChallengeId), string(otherIssued.Nonce))})
	if err != nil {
		t.Fatal("second synthetic login body encoding failed")
	}
	status, otherLoginHeaders, otherLoginResponse := post(emailLoginPath, otherLoginBody, emailLoginHTTPKey(t))
	var otherSuccess api.PhoneAuthSuccess
	if status != http.StatusOK || otherLoginHeaders.Get("Cache-Control") != "no-store" || json.Unmarshal(otherLoginResponse, &otherSuccess) != nil ||
		otherSuccess.Status != api.Authenticated || otherSuccess.Token == "" || otherSuccess.User.Id == "" || otherSuccess.User.Id == success.User.Id {
		t.Fatalf("second synthetic email account did not authenticate independently: %d %s", status, otherLoginResponse)
	}
	responseSecrets = append(responseSecrets, otherSuccess.Token)
	otherAgeStatus, otherAgeHeaders, otherAgeResponse := postAge(server.Client(), server.URL, otherSuccess.Token, []byte(`{"isAdult":true,"expectedVersion":1}`))
	var otherUser api.User
	if otherAgeStatus != http.StatusOK || otherAgeHeaders.Get("Cache-Control") != "no-store" || json.Unmarshal(otherAgeResponse, &otherUser) != nil || otherUser.Id != otherSuccess.User.Id {
		t.Fatal("second synthetic subject could not establish its own age declaration")
	}
	foreignRead := workoutRequest(http.MethodGet, readPath, nil, otherSuccess.Token, "")
	if foreignRead.status != http.StatusNotFound || bytes.Contains(foreignRead.body, []byte("a01-24-record-001")) || bytes.Contains(foreignRead.body, []byte("42.50")) || bytes.Contains(foreignRead.body, []byte("卧推")) {
		t.Fatalf("another authenticated subject could read or infer the workout: %d %s", foreignRead.status, foreignRead.body)
	}
	finishPath := "/me/sessions/" + createdDetail.ID + "/finish"
	finishBody := []byte(`{"expectedVersion":2}`)
	if foreignWrite := workoutRequest(http.MethodPost, setPath, setBody, otherSuccess.Token, randomIdempotencyKey(t)); foreignWrite.status != http.StatusForbidden {
		t.Fatalf("another authenticated subject could attempt a workout set write: %d", foreignWrite.status)
	}
	if foreignFinish := workoutRequest(http.MethodPost, finishPath, finishBody, otherSuccess.Token, randomIdempotencyKey(t)); foreignFinish.status != http.StatusForbidden {
		t.Fatalf("another authenticated subject could attempt to finish the workout: %d", foreignFinish.status)
	}
	ownerAfterForeignWrites := workoutRequest(http.MethodGet, readPath, nil, success.Token, "")
	if ownerAfterForeignWrites.status != http.StatusOK || !bytes.Equal(ownerAfterForeignWrites.body, ownerRead.body) {
		t.Fatal("foreign write attempts changed the owner's workout")
	}
	finishKey := randomIdempotencyKey(t)
	finished := workoutRequest(http.MethodPost, finishPath, finishBody, success.Token, finishKey)
	var finishedDetail struct {
		ID      string `json:"id"`
		Status  string `json:"status"`
		Version int    `json:"version"`
	}
	if finished.status != http.StatusOK || json.Unmarshal(finished.body, &finishedDetail) != nil || finishedDetail.ID != createdDetail.ID || finishedDetail.Status != "completed" || finishedDetail.Version != 3 {
		t.Fatalf("authenticated workout finish failed: %d %s", finished.status, finished.body)
	}
	finishReplay := workoutRequest(http.MethodPost, finishPath, finishBody, success.Token, finishKey)
	if finishReplay.status != http.StatusOK || !bytes.Equal(finishReplay.body, finished.body) {
		t.Fatal("same-key workout finish replay differed from its original response")
	}
	finalRead := workoutRequest(http.MethodGet, readPath, nil, success.Token, "")
	var finalDetail struct {
		ID      string `json:"id"`
		Status  string `json:"status"`
		Version int    `json:"version"`
	}
	if finalRead.status != http.StatusOK || json.Unmarshal(finalRead.body, &finalDetail) != nil || finalDetail.ID != createdDetail.ID || finalDetail.Status != "completed" || finalDetail.Version != 3 {
		t.Fatalf("finished workout HTTP readback failed: %d %s", finalRead.status, finalRead.body)
	}
	declinedBody := []byte(`{"isAdult":false,"expectedVersion":5}`)
	status, declinedHeaders, declinedResponse := postAge(server.Client(), server.URL, success.Token, declinedBody)
	if status != http.StatusUnprocessableEntity || declinedHeaders.Get("Cache-Control") != "no-store" || bytes.Contains(declinedResponse, []byte(success.Token)) {
		t.Fatal("negative declaration was not rejected generically")
	}
	if err := db.QueryRowContext(ctx, `SELECT age_declaration_version, age_verified_at IS NOT NULL FROM user WHERE id = ?`, success.User.Id).Scan(&ageVersion, &ageVerified); err != nil || ageVersion != 5 || !ageVerified {
		t.Fatal("rejected negative declaration changed account state")
	}
	claims, err := codec.Verify(success.Token)
	if err != nil {
		t.Fatal("issued session claims could not be verified")
	}
	// Race a live protected read with logout. Either the read acquired the
	// session locks first (200) or logout did (401); after logout commits, all
	// later reads must observe the revoked state.
	startConcurrent := make(chan struct{})
	type concurrentHTTPResult struct {
		response *http.Response
		err      error
	}
	logoutResult := make(chan concurrentHTTPResult, 1)
	readResult := make(chan concurrentHTTPResult, 1)
	logoutRequest, err := http.NewRequestWithContext(ctx, http.MethodDelete, server.URL+"/auth/sessions/current", nil)
	if err != nil {
		t.Fatal("logout request setup failed")
	}
	logoutRequest.Header.Set("Authorization", "Bearer "+success.Token)
	readRequest, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/me", nil)
	if err != nil {
		t.Fatal("concurrent session read setup failed")
	}
	readRequest.Header.Set("Authorization", "Bearer "+success.Token)
	go func() {
		<-startConcurrent
		response, doErr := server.Client().Do(logoutRequest)
		logoutResult <- concurrentHTTPResult{response: response, err: doErr}
	}()
	go func() {
		<-startConcurrent
		response, doErr := server.Client().Do(readRequest)
		readResult <- concurrentHTTPResult{response: response, err: doErr}
	}()
	close(startConcurrent)
	logoutOutcome := <-logoutResult
	readOutcome := <-readResult
	if logoutOutcome.err != nil || readOutcome.err != nil || logoutOutcome.response == nil || readOutcome.response == nil {
		t.Fatal("concurrent logout/read request failed")
	}
	logoutResponse, readResponse := logoutOutcome.response, readOutcome.response
	if readResponse.StatusCode != http.StatusOK && readResponse.StatusCode != http.StatusUnauthorized {
		_ = readResponse.Body.Close()
		_ = logoutResponse.Body.Close()
		t.Fatal("concurrent read returned an unexpected status")
	}
	_, _ = io.Copy(io.Discard, readResponse.Body)
	_ = readResponse.Body.Close()
	logoutBody, readErr := io.ReadAll(logoutResponse.Body)
	_ = logoutResponse.Body.Close()
	if readErr != nil || logoutResponse.StatusCode != http.StatusNoContent || logoutResponse.Header.Get("Cache-Control") != "no-store" || len(logoutBody) != 0 {
		t.Fatal("logout did not return an empty no-store 204 after commit")
	}
	var state string
	var revokedAt sql.NullString
	if err := db.QueryRowContext(ctx, `SELECT state, DATE_FORMAT(revoked_at, '%Y-%m-%d %H:%i:%s.%f') FROM auth_session WHERE id = ?`, []byte(claims.SessionID)).Scan(&state, &revokedAt); err != nil || state != "revoked" || !revokedAt.Valid {
		t.Fatal("logout did not durably revoke the authenticated session")
	}
	// The other authenticated session remains usable; logout must not revoke the account.
	otherMeReq, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/me", nil)
	if err != nil {
		t.Fatal("other session check setup failed")
	}
	otherMeReq.Header.Set("Authorization", "Bearer "+otherSuccess.Token)
	otherMeResp, err := server.Client().Do(otherMeReq)
	if err != nil {
		t.Fatal("other session check failed")
	}
	otherMeBody, readErr := io.ReadAll(otherMeResp.Body)
	_ = otherMeResp.Body.Close()
	if readErr != nil || otherMeResp.StatusCode != http.StatusOK || !bytes.Contains(otherMeBody, []byte(otherSuccess.User.Id)) {
		t.Fatal("logout revoked a different authenticated session")
	}
	// A repeated request using the revoked bearer is rejected rather than
	// treating stale credentials as proof of a successful logout.
	repeatReq, err := http.NewRequestWithContext(ctx, http.MethodDelete, server.URL+"/auth/sessions/current", nil)
	if err != nil {
		t.Fatal("repeat logout setup failed")
	}
	repeatReq.Header.Set("Authorization", "Bearer "+success.Token)
	repeatResp, err := server.Client().Do(repeatReq)
	if err != nil {
		t.Fatal("repeat logout request failed")
	}
	repeatBody, readErr := io.ReadAll(repeatResp.Body)
	_ = repeatResp.Body.Close()
	if readErr != nil || repeatResp.StatusCode != http.StatusUnauthorized || repeatResp.Header.Get("Cache-Control") != "no-store" || bytes.Contains(repeatBody, []byte(success.Token)) {
		t.Fatal("repeated logout did not reject the revoked bearer safely")
	}
	// The original login receipt cannot resurrect a session after logout.
	status, receiptReplayHeaders, receiptReplayBody := post(emailLoginPath, loginBody, loginKey)
	if status != http.StatusUnauthorized || receiptReplayHeaders.Get("Cache-Control") != "no-store" || bytes.Contains(receiptReplayBody, []byte(success.Token)) {
		t.Fatal("login receipt replay restored a revoked session")
	}
	status, revokedHeaders, revokedResponse := postAge(server.Client(), server.URL, success.Token, []byte(`{"isAdult":true,"expectedVersion":5}`))
	if status != http.StatusUnauthorized || revokedHeaders.Get("Cache-Control") != "no-store" || bytes.Contains(revokedResponse, []byte(success.Token)) {
		t.Fatal("revoked session was allowed to declare age")
	}
	if err := db.QueryRowContext(ctx, `SELECT age_declaration_version, age_verified_at IS NOT NULL FROM user WHERE id = ?`, success.User.Id).Scan(&ageVersion, &ageVerified); err != nil || ageVersion != 5 || !ageVerified {
		t.Fatal("revoked session changed declaration state")
	}
	if revokedRead := workoutRequest(http.MethodGet, readPath, nil, success.Token, ""); revokedRead.status != http.StatusUnauthorized {
		t.Fatalf("revoked session could read workout data: %d", revokedRead.status)
	}
	if revokedReplay := workoutRequest(http.MethodPost, finishPath, finishBody, success.Token, finishKey); revokedReplay.status != http.StatusUnauthorized {
		t.Fatalf("revoked session could replay workout result: %d", revokedReplay.status)
	}
	var finalStatus string
	var finalVersion int
	if err := db.QueryRowContext(ctx, `SELECT status, version FROM workout_session WHERE id = ? AND user_id = ?`,
		createdDetail.ID, success.User.Id).Scan(&finalStatus, &finalVersion); err != nil || finalStatus != "completed" || finalVersion != 3 {
		t.Fatal("revoked session changed the completed workout")
	}

	closedDB, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatal("unavailable DB handle setup failed")
	}
	_ = closedDB.Close()
	unavailableChallenge, err := NewEmailChallengeHandler(closedDB, issuer, validator, TrustedSourceIPFunc(func(*http.Request) (netip.Addr, error) { return trustedIP, nil }))
	if err != nil {
		t.Fatal("unavailable challenge handler setup failed")
	}
	unavailableLogin, err := NewEmailLoginHandler(closedDB, coordinator, sessions, validator, TrustedSourceIPFunc(func(*http.Request) (netip.Addr, error) { return trustedIP, nil }))
	if err != nil {
		t.Fatal("unavailable login handler setup failed")
	}
	unavailableLogout, err := NewCurrentSessionDeleteHandler(closedDB, sessions, validator)
	if err != nil {
		t.Fatal("unavailable logout handler setup failed")
	}
	if code, responseHeaders, body := postWithHandler(t, ctx, unavailableChallenge, emailChallengesPath, challengeBody, emailLoginHTTPKey(t)); code != http.StatusServiceUnavailable || responseHeaders.Get("Cache-Control") != "no-store" || bytes.Contains(body, []byte(success.Token)) {
		t.Fatal("unavailable challenge service did not fail closed")
	}
	if code, responseHeaders, body := postWithHandler(t, ctx, unavailableLogin, emailLoginPath, loginBody, emailLoginHTTPKey(t)); code != http.StatusServiceUnavailable || responseHeaders.Get("Cache-Control") != "no-store" || bytes.Contains(body, []byte(success.Token)) {
		t.Fatal("unavailable login service did not fail closed")
	}
	logoutServer := httptest.NewTLSServer(unavailableLogout)
	logoutReq, err := http.NewRequestWithContext(ctx, http.MethodDelete, logoutServer.URL+"/auth/sessions/current", nil)
	if err != nil {
		logoutServer.Close()
		t.Fatal("unavailable logout request setup failed")
	}
	logoutReq.Header.Set("Authorization", "Bearer "+otherSuccess.Token)
	logoutResp, err := logoutServer.Client().Do(logoutReq)
	if err != nil {
		logoutServer.Close()
		t.Fatal("unavailable logout request failed")
	}
	logoutErrBody, readErr := io.ReadAll(logoutResp.Body)
	_ = logoutResp.Body.Close()
	logoutServer.Close()
	if readErr != nil || logoutResp.StatusCode != http.StatusServiceUnavailable || logoutResp.Header.Get("Cache-Control") != "no-store" || bytes.Contains(logoutErrBody, []byte(otherSuccess.Token)) {
		t.Fatal("unavailable logout did not fail closed without claiming success")
	}

	t.Log("PASS: challenge HTTP -> outbox -> local TLS SMTP -> login HTTP -> age declaration HTTP -> workout create/set/read/finish HTTP; synthetic test-only services")
}

func postWithHandler(t *testing.T, ctx context.Context, handler http.Handler, path string, body []byte, key string) (int, http.Header, []byte) {
	t.Helper()
	server := httptest.NewTLSServer(handler)
	defer server.Close()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, server.URL+path, bytes.NewReader(body))
	if err != nil {
		t.Fatal("unavailable-service request setup failed")
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", key)
	response, err := server.Client().Do(req)
	if err != nil {
		t.Fatal("unavailable-service request failed")
	}
	payload, readErr := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if readErr != nil {
		t.Fatal("unavailable-service response read failed")
	}
	return response.StatusCode, response.Header.Clone(), payload
}

func outboxState(t *testing.T, ctx context.Context, db *sql.DB, challengeID string) string {
	t.Helper()
	var state string
	if err := db.QueryRowContext(ctx, `SELECT state FROM auth_email_outbox WHERE challenge_id=?`, []byte(challengeID)).Scan(&state); err != nil {
		t.Fatal("outbox state read failed")
	}
	return state
}

type localSMTPMessage struct {
	message string
	err     error
}

func localDeliverySMTP(t *testing.T) (*authmail.SMTPMailer, <-chan localSMTPMessage, net.Listener, *atomic.Int64) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal("local SMTP bind failed")
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		_ = listener.Close()
		t.Fatal("local SMTP certificate key generation failed")
	}
	certificate := &x509.Certificate{SerialNumber: big.NewInt(2401), Subject: pkix.Name{CommonName: "local synthetic SMTP"},
		NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, IsCA: true, BasicConstraintsValid: true,
		IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}}
	der, err := x509.CreateCertificate(rand.Reader, certificate, certificate, &key.PublicKey, key)
	if err != nil {
		_ = listener.Close()
		t.Fatal("local SMTP certificate creation failed")
	}
	certDir := t.TempDir()
	certPath := filepath.Join(certDir, "local-smtp-ca.pem")
	if err := os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0600); err != nil {
		_ = listener.Close()
		t.Fatal("local SMTP trust setup failed")
	}
	t.Setenv("SSL_CERT_FILE", certPath)
	t.Setenv("SSL_CERT_DIR", certDir)
	t.Setenv("GODEBUG", "x509sslcertoverrideplatform=1")
	messages := make(chan localSMTPMessage, 1)
	var connections atomic.Int64
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			raw, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}
			connections.Add(1)
			conn := tls.Server(raw, &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}, MinVersion: tls.VersionTLS12})
			_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
			result := localSMTPMessage{}
			if result.err = conn.Handshake(); result.err == nil {
				_, result.err = io.WriteString(conn, "220 local synthetic SMTP\r\n")
			}
			reader := bufio.NewReader(conn)
			for result.err == nil {
				line, readErr := reader.ReadString('\n')
				if readErr != nil {
					result.err = readErr
					break
				}
				switch {
				case strings.HasPrefix(strings.ToUpper(line), "EHLO "):
					_, result.err = io.WriteString(conn, "250-local.test\r\n250-AUTH PLAIN\r\n250 SIZE 10000\r\n")
				case strings.HasPrefix(strings.ToUpper(line), "AUTH PLAIN "):
					_, result.err = io.WriteString(conn, "235 authenticated\r\n")
				case strings.HasPrefix(strings.ToUpper(line), "MAIL FROM:"), strings.HasPrefix(strings.ToUpper(line), "RCPT TO:"):
					_, result.err = io.WriteString(conn, "250 accepted\r\n")
				case strings.TrimSpace(strings.ToUpper(line)) == "DATA":
					_, result.err = io.WriteString(conn, "354 continue\r\n")
					var body strings.Builder
					for result.err == nil {
						line, result.err = reader.ReadString('\n')
						if result.err != nil || line == ".\r\n" {
							break
						}
						if strings.HasPrefix(line, "..") {
							line = line[1:]
						}
						body.WriteString(line)
					}
					result.message = body.String()
					if result.err == nil {
						_, result.err = io.WriteString(conn, "250 accepted\r\n")
					}
				case strings.TrimSpace(strings.ToUpper(line)) == "QUIT":
					_, _ = io.WriteString(conn, "221 bye\r\n")
					goto finished
				default:
					_, result.err = io.WriteString(conn, "500 unsupported\r\n")
				}
			}
		finished:
			_ = conn.Close()
			messages <- result
		}
	}()
	mailer, err := authmail.NewSMTPMailer(authmail.SMTPConfig{Address: listener.Addr().String(), Username: "synthetic@local.test",
		Password: "synthetic-password-not-a-real-secret", From: "synthetic@local.test", Timeout: 5 * time.Second})
	if err != nil {
		_ = listener.Close()
		t.Fatal("local SMTP mailer setup failed")
	}
	t.Cleanup(func() {
		_ = listener.Close()
		select {
		case <-done:
		case <-time.After(time.Second):
		}
	})
	return mailer, messages, listener, &connections
}

func deliveredCode(t *testing.T, raw, recipient string) string {
	t.Helper()
	message, err := netmail.ReadMessage(strings.NewReader(raw))
	if err != nil || message.Header.Get("To") != "<"+recipient+">" {
		t.Fatal("local SMTP message did not target the synthetic recipient")
	}
	body, err := io.ReadAll(quotedprintable.NewReader(message.Body))
	if err != nil {
		t.Fatal("local SMTP message body was malformed")
	}
	match := regexp.MustCompile(`(?:^|[^0-9])([0-9]{6})(?:[^0-9]|$)`).FindSubmatch(body)
	if len(match) != 2 {
		t.Fatal("SMTP message did not contain exactly a six digit verification code")
	}
	return string(match[1])
}
