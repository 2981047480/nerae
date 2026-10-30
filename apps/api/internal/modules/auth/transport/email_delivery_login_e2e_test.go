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
)

// This test-only assembly exercises the real challenge issuer, outbox worker,
// local TLS SMTP adapter, login coordinator, HTTP handlers and /me handler.
// It does not mount production routes or contact an external mail service.
func TestEmailChallengeSMTPLoginHTTPToMeOverLocalTLS(t *testing.T) {
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
	if cfg.Net != "unix" || cfg.DBName != "nerae_a01_24_test" || cfg.User != "root" || cfg.Passwd != "" ||
		!cfg.ParseTime || cfg.Loc != time.UTC || filepath.Base(socket) != "mysql.sock" || resolveErr != nil ||
		dirErr != nil || socketErr != nil || filepath.Dir(resolved) != "/private/tmp" ||
		!strings.HasPrefix(filepath.Base(resolved), "nerae-a01-24-") || !dirInfo.IsDir() ||
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
	challenges, err := challenge.NewStore(hasher, proofReplay, identities, challenge.Policy{ChallengeTTL: 5 * time.Minute,
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
	outbox, err := issuance.NewOutbox(deriver, "a01-24-code-v1", hasher, identities, smtp, 20*time.Second)
	if err != nil {
		t.Fatal("outbox setup failed")
	}
	challengeLimiter, err := ratelimit.New(rdb, integrationRateConfig(t, 100))
	if err != nil {
		t.Fatal("challenge limiter setup failed")
	}
	requestKeys := requestKeyConfig()
	issuer, err := issuance.NewChallengeRequestIssuer(challenges, outbox, identities, challengeLimiter, requestKeys)
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
	for _, initialize := range []func(context.Context, *sql.Tx) error{identityEpoch.Initialize, outbox.InitializeCodeKey, issuer.InitializeKey} {
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
	validator, err := middleware.NewSchemaValidator(16<<10, 2048)
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
	mux := http.NewServeMux()
	mux.Handle(emailChallengesPath, challengeHandler)
	mux.Handle(emailLoginPath, loginHandler)
	mux.Handle("/me", meHandler)
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
		success.Status != api.Authenticated || success.Token == "" || success.User.Id == "" || success.ExpiresIn < 1 {
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
		json.Unmarshal(meBody, &user) != nil || user.Id != success.User.Id || bytes.Contains(meBody, []byte(success.Token)) {
		t.Fatal("/me did not authenticate the issued session without exposing its token")
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
	if code, responseHeaders, body := postWithHandler(t, ctx, unavailableChallenge, emailChallengesPath, challengeBody, emailLoginHTTPKey(t)); code != http.StatusServiceUnavailable || responseHeaders.Get("Cache-Control") != "no-store" || bytes.Contains(body, []byte(success.Token)) {
		t.Fatal("unavailable challenge service did not fail closed")
	}
	if code, responseHeaders, body := postWithHandler(t, ctx, unavailableLogin, emailLoginPath, loginBody, emailLoginHTTPKey(t)); code != http.StatusServiceUnavailable || responseHeaders.Get("Cache-Control") != "no-store" || bytes.Contains(body, []byte(success.Token)) {
		t.Fatal("unavailable login service did not fail closed")
	}

	t.Log("PASS: challenge HTTP -> outbox -> local TLS SMTP -> login HTTP replay -> /me; synthetic test-only services")
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
