package mailworker

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
	"encoding/base64"
	"encoding/pem"
	"io"
	"math/big"
	"mime/quotedprintable"
	"net"
	netmail "net/mail"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"
	"nerae/api/internal/modules/auth/bootstrap"
	"nerae/api/internal/modules/auth/challenge"
	"nerae/api/internal/modules/auth/domain"
	"nerae/api/internal/modules/auth/issuance"
	authmail "nerae/api/internal/modules/auth/mail"
	"nerae/api/internal/modules/auth/repository"
)

// This test requires a disposable, socket-only schema created by the evidence
// harness. It never resolves or connects to an external SMTP provider.
func TestWorkerOutboxPrivateMySQLLoopbackSMTP(t *testing.T) {
	dsn := os.Getenv("NERAE_EMAIL_OUTBOX_WORKER_TEST_DSN")
	if dsn == "" {
		t.Skip("isolated worker MySQL DSN is not configured")
	}
	parsed, err := mysql.ParseDSN(dsn)
	if err != nil || parsed.Net != "unix" || parsed.User != "root" || parsed.Passwd != "" ||
		parsed.DBName != "nerae_email_worker_test" || !parsed.ParseTime || parsed.Loc == nil || parsed.Loc.String() != "UTC" {
		t.Fatal("worker test requires its dedicated UTC Unix socket DSN")
	}
	base := filepath.Dir(parsed.Addr)
	baseInfo, err := os.Lstat(base)
	if err != nil || !baseInfo.IsDir() || baseInfo.Mode()&os.ModeSymlink != 0 || baseInfo.Mode().Perm()&0o077 != 0 {
		t.Fatal("worker test socket parent is not a private directory")
	}
	resolvedBase, err := filepath.EvalSymlinks(base)
	expectedBase, absErr := filepath.Abs(base)
	if err != nil || absErr != nil || resolvedBase != expectedBase ||
		filepath.Dir(resolvedBase) != "/private/tmp" || !strings.HasPrefix(filepath.Base(resolvedBase), "nerae-a01-19-mysql-") ||
		filepath.Base(parsed.Addr) != "mysql.sock" {
		t.Fatal("worker test socket is outside its dedicated private harness directory")
	}
	socketInfo, err := os.Lstat(parsed.Addr)
	if err != nil || socketInfo.Mode()&os.ModeSocket == 0 || socketInfo.Mode()&os.ModeSymlink != 0 {
		t.Fatal("worker test socket is missing or is a symbolic link")
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatal("private database open failed")
	}
	db.SetMaxOpenConns(12)
	db.SetMaxIdleConns(12)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	t.Cleanup(func() { _ = db.Close() })
	if err := db.PingContext(ctx); err != nil {
		t.Fatal("private database unavailable")
	}

	hasher, err := domain.NewEmailCodeHasher(bytes.Repeat([]byte{0x11}, 32))
	if err != nil {
		t.Fatal("test hasher setup failed")
	}
	replay, err := repository.NewProofReplayStore(bytes.Repeat([]byte{0x22}, 32))
	if err != nil {
		t.Fatal("test replay setup failed")
	}
	identities, err := domain.NewEmailIdentityProtector(bytes.Repeat([]byte{0x33}, 32), bytes.Repeat([]byte{0x44}, 32), "worker-test-email-v1")
	if err != nil {
		t.Fatal("test identity protector setup failed")
	}
	deriver, err := issuance.NewDeriver(bytes.Repeat([]byte{0x55}, 32))
	if err != nil {
		t.Fatal("test code deriver setup failed")
	}
	challengeStore, err := challenge.NewStore(hasher, replay, identities, challenge.Policy{ChallengeTTL: 5 * time.Minute, MaxAttempts: 3, DeviceProof: domain.ProofPolicy{MaxLifetime: 5 * time.Minute, ClockSkew: 30 * time.Second}})
	if err != nil {
		t.Fatal("test challenge store setup failed")
	}
	mailer, results, listener, acceptedConnections, sinkDone := workerLocalSMTP(t)
	outbox, err := issuance.NewOutbox(deriver, "worker-test-code-v1", hasher, identities, mailer, 20*time.Second)
	if err != nil {
		t.Fatal("test outbox setup failed")
	}

	initializer, err := bootstrap.NewIdentityEpochInitializer(identities)
	if err != nil {
		t.Fatal("test identity initializer setup failed")
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal("test epoch transaction failed")
	}
	if err := initializer.Initialize(ctx, tx); err != nil {
		_ = tx.Rollback()
		t.Fatal("test identity epoch initialization failed")
	}
	if err := outbox.InitializeCodeKey(ctx, tx); err != nil {
		_ = tx.Rollback()
		t.Fatal("test code epoch initialization failed")
	}
	if err := tx.Commit(); err != nil {
		t.Fatal("test epoch commit failed")
	}

	acceptedID, acceptedCode := workerEnqueue(t, ctx, db, outbox, challengeStore, deriver, "accepted@worker.test")
	worker, err := New(Config{Outbox: outbox, DB: db, PollInterval: 100 * time.Millisecond, RecoveryBatch: 10})
	if err != nil {
		t.Fatal("worker construction failed")
	}
	if err := worker.RunOnce(ctx); err != nil {
		t.Fatal("accepted worker iteration failed")
	}
	if got := workerState(t, ctx, db, acceptedID); got != string(issuance.DeliveryAccepted) {
		t.Fatalf("accepted state = %q", got)
	}
	assertWorkerSinkMessage(t, <-results, "accepted@worker.test", acceptedCode)

	unknownID, unknownCode := workerEnqueue(t, ctx, db, outbox, challengeStore, deriver, "unknown@worker.test")
	if err := worker.RunOnce(ctx); err != nil {
		t.Fatal("unknown worker iteration failed")
	}
	if got := workerState(t, ctx, db, unknownID); got != string(issuance.DeliveryUnknown) {
		t.Fatalf("unknown state = %q", got)
	}
	assertWorkerSinkMessage(t, <-results, "unknown@worker.test", unknownCode)

	// Reconstructing the worker models process restart. A terminal unknown row
	// must remain ineligible and the sink must receive no third connection.
	restarted, err := New(Config{Outbox: outbox, DB: db, PollInterval: 100 * time.Millisecond, RecoveryBatch: 10})
	if err != nil {
		t.Fatal("restarted worker construction failed")
	}
	if err := restarted.RunOnce(ctx); err != nil {
		t.Fatal("post-restart empty iteration failed")
	}
	if got := workerState(t, ctx, db, unknownID); got != string(issuance.DeliveryUnknown) {
		t.Fatalf("post-restart unknown state = %q", got)
	}
	if got := acceptedConnections.Load(); got != 2 {
		t.Fatalf("SMTP connection count after restart = %d, want 2", got)
	}
	if err := listener.Close(); err != nil {
		t.Fatal("local SMTP sink close failed")
	}
	select {
	case <-sinkDone:
	case <-time.After(time.Second):
		t.Fatal("local SMTP sink did not stop")
	}
}

func workerEnqueue(t *testing.T, ctx context.Context, db *sql.DB, outbox *issuance.Outbox, challenges *challenge.Store, deriver *issuance.Deriver, email string) (string, string) {
	t.Helper()
	id, err := domain.GenerateChallengeID()
	if err != nil {
		t.Fatal("challenge ID generation failed")
	}
	nonce, err := domain.GenerateChallengeNonce()
	if err != nil {
		t.Fatal("challenge nonce generation failed")
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal("device test key generation failed")
	}
	encode := base64.RawURLEncoding.EncodeToString
	public := domain.DevicePublicJWK{Kty: "EC", Crv: "P-256", X: encode(key.X.FillBytes(make([]byte, 32))), Y: encode(key.Y.FillBytes(make([]byte, 32)))}
	code, err := deriver.Derive(id, "email_login", 6)
	if err != nil {
		t.Fatal("test code derivation failed")
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal("challenge transaction failed")
	}
	epoch, err := outbox.LockEpochs(ctx, tx)
	if err != nil {
		_ = tx.Rollback()
		t.Fatal("outbox epoch lock failed")
	}
	if err := challenges.Issue(ctx, tx, challenge.IssueInput{Email: email, ChallengeID: id, Nonce: nonce, Code: code, PublicKey: public}); err != nil {
		_ = tx.Rollback()
		t.Fatal("challenge issue failed")
	}
	if err := outbox.Enqueue(ctx, tx, epoch, id, 6); err != nil {
		_ = tx.Rollback()
		t.Fatal("outbox enqueue failed")
	}
	if err := tx.Commit(); err != nil {
		t.Fatal("challenge commit failed")
	}
	return id, code
}

func workerState(t *testing.T, ctx context.Context, db *sql.DB, id string) string {
	t.Helper()
	var state string
	if err := db.QueryRowContext(ctx, `SELECT state FROM auth_email_outbox WHERE challenge_id=?`, []byte(id)).Scan(&state); err != nil {
		t.Fatal("outbox state read failed")
	}
	return state
}

type workerSMTPResult struct {
	message string
	auth    bool
	err     error
}

func workerLocalSMTP(t *testing.T) (*authmail.SMTPMailer, <-chan workerSMTPResult, net.Listener, *atomic.Int64, <-chan struct{}) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal("local TLS SMTP sink bind failed")
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		_ = listener.Close()
		t.Fatal("local TLS key generation failed")
	}
	cert := &x509.Certificate{SerialNumber: big.NewInt(23), Subject: pkix.Name{CommonName: "worker local SMTP test"}, NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, IsCA: true, BasicConstraintsValid: true, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}}
	der, err := x509.CreateCertificate(rand.Reader, cert, cert, &key.PublicKey, key)
	if err != nil {
		_ = listener.Close()
		t.Fatal("local TLS certificate creation failed")
	}
	certDir := t.TempDir()
	certPath := filepath.Join(certDir, "worker-ca.pem")
	if err := os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0600); err != nil {
		_ = listener.Close()
		t.Fatal("local trust root write failed")
	}
	t.Setenv("SSL_CERT_FILE", certPath)
	t.Setenv("SSL_CERT_DIR", certDir)
	t.Setenv("GODEBUG", "x509sslcertoverrideplatform=1")
	results := make(chan workerSMTPResult, 3)
	var acceptedConnections atomic.Int64
	sinkDone := make(chan struct{})
	go func() {
		defer close(sinkDone)
		for {
			result := workerSMTPResult{}
			raw, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}
			index := int(acceptedConnections.Add(1)) - 1
			conn := tls.Server(raw, &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}, MinVersion: tls.VersionTLS12})
			_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
			result.err = conn.Handshake()
			if result.err == nil {
				_, result.err = io.WriteString(conn, "220 local test\r\n")
			}
			reader := bufio.NewReader(conn)
			for result.err == nil {
				line, readErr := reader.ReadString('\n')
				if readErr != nil {
					result.err = readErr
					break
				}
				upper := strings.ToUpper(line)
				switch {
				case strings.HasPrefix(upper, "EHLO "):
					_, result.err = io.WriteString(conn, "250-local.test\r\n250-AUTH PLAIN\r\n250 SIZE 10000\r\n")
				case strings.HasPrefix(upper, "AUTH PLAIN "):
					result.auth = true
					_, result.err = io.WriteString(conn, "235 authenticated\r\n")
				case strings.HasPrefix(upper, "MAIL FROM:"), strings.HasPrefix(upper, "RCPT TO:"):
					_, result.err = io.WriteString(conn, "250 accepted\r\n")
				case strings.TrimSpace(upper) == "DATA":
					_, result.err = io.WriteString(conn, "354 continue\r\n")
					var body strings.Builder
					for result.err == nil {
						line, result.err = reader.ReadString('\n')
						if result.err != nil || line == ".\r\n" {
							break
						}
						body.WriteString(line)
					}
					result.message = body.String()
					if result.err == nil && index == 0 {
						_, result.err = io.WriteString(conn, "250 accepted\r\n")
					} else {
						_ = conn.Close()
						goto serveDone
					}
				case strings.TrimSpace(upper) == "QUIT":
					_, _ = io.WriteString(conn, "221 bye\r\n")
					goto serveDone
				default:
					_, result.err = io.WriteString(conn, "500 unsupported\r\n")
				}
			}
		serveDone:
			_ = conn.Close()
			results <- result
		}
	}()
	mailer, err := authmail.NewSMTPMailer(authmail.SMTPConfig{Address: listener.Addr().String(), Username: "sender@worker.test", Password: "synthetic-worker-password", From: "sender@worker.test", Timeout: 5 * time.Second})
	if err != nil {
		_ = listener.Close()
		t.Fatal("local SMTP configuration failed")
	}
	return mailer, results, listener, &acceptedConnections, sinkDone
}

func assertWorkerSinkMessage(t *testing.T, result workerSMTPResult, recipient, code string) {
	t.Helper()
	if result.err != nil || !result.auth || result.message == "" {
		t.Fatal("local SMTP sink did not receive complete message")
	}
	message, err := netmail.ReadMessage(strings.NewReader(result.message))
	if err != nil {
		t.Fatal("SMTP message parsing failed")
	}
	body, err := io.ReadAll(quotedprintable.NewReader(message.Body))
	if err != nil || message.Header.Get("To") != "<"+recipient+">" || !strings.Contains(string(body), code) {
		t.Fatal("SMTP content did not match queued challenge")
	}
}
