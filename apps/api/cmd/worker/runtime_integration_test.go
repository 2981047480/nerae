package main

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
	"log/slog"
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
	"nerae/api/internal/config"
	"nerae/api/internal/modules/auth/challenge"
	"nerae/api/internal/modules/auth/domain"
	"nerae/api/internal/modules/auth/issuance"
	"nerae/api/internal/modules/auth/repository"
)

func TestWorkerProcessStartupValidatesEpochAndSendsWithLocalTLS(t *testing.T) {
	dsn := os.Getenv("NERAE_WORKER_STARTUP_TEST_DSN")
	if dsn == "" {
		t.Skip("private worker startup MySQL DSN is not configured")
	}
	validateWorkerTestDSN(t, dsn)
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatal("private startup database open failed")
	}
	defer db.Close()
	db.SetMaxOpenConns(8)
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		t.Fatal("private startup database unavailable")
	}

	sink, received, connectionCount := startupLocalSMTPSink(t)
	defer sink.Close()
	secrets := validSecretsForTest()
	secrets.SMTPAddress = sink.Addr().String()
	secrets.SMTPUsername = "sender@worker.test"
	secrets.SMTPPassword = "synthetic-worker-password"
	secrets.SMTPFrom = "sender@worker.test"
	secrets.SMTPTimeoutMS = 5000
	configPath := writeSecretsFile(t, marshalSecrets(t, secrets), 0600)
	t.Setenv(secretsFileEnv, configPath)
	t.Setenv("NERAE_ENVIRONMENT", "test")
	t.Setenv("NERAE_MYSQL_DSN", dsn)
	// The shared process config currently requires a syntactically valid Redis
	// URL. The mail worker does not connect to Redis.
	t.Setenv("NERAE_REDIS_URL", "redis://127.0.0.1:6379/0")

	loaded, err := loadWorkerSecrets()
	if err != nil {
		t.Fatal("protected startup config rejected")
	}
	// A late epoch mismatch must roll back the identity and derivation epoch
	// rows that startup just initialized earlier in this same transaction.
	if _, err := db.ExecContext(ctx, `INSERT INTO auth_email_code_hash_key_epoch (slot,key_id,key_tag) VALUES (1,?,?)`, []byte("wrong-hash-key"), bytes.Repeat([]byte{0x7f}, 32)); err != nil {
		t.Fatal("could not prepare code hash epoch mismatch")
	}
	if _, err := newRuntime(ctx, config.Config{MySQLDSN: dsn}, loaded, slog.New(slog.NewTextHandler(io.Discard, nil))); err == nil {
		t.Fatal("startup accepted mismatched code hash epoch")
	}
	for _, table := range []string{"auth_email_identity_key_epoch", "auth_email_code_key_epoch"} {
		var count int
		if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+table).Scan(&count); err != nil || count != 0 {
			t.Fatalf("failed startup partially committed %s", table)
		}
	}
	if _, err := db.ExecContext(ctx, `DELETE FROM auth_email_code_hash_key_epoch WHERE slot=1`); err != nil {
		t.Fatal("could not remove synthetic hash epoch mismatch")
	}
	startupRuntime, err := newRuntime(ctx, config.Config{MySQLDSN: dsn}, loaded, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal("startup epoch verification failed on a fresh private schema")
	}
	challengeID, code := enqueueStartupChallenge(t, ctx, startupRuntime)
	startupRuntime.close()

	// Run the actual cmd/worker orchestration: it reloads process and protected
	// config, checks epochs, constructs SMTP/outbox/worker, then drains the row.
	workerCtx, stop := context.WithCancel(context.Background())
	runDone := make(chan error, 1)
	var logs strings.Builder
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	go func() { runDone <- run(workerCtx, logger) }()

	select {
	case message := <-received:
		parsed, parseErr := netmail.ReadMessage(strings.NewReader(message.body))
		var body []byte
		if parseErr == nil {
			body, parseErr = io.ReadAll(quotedprintable.NewReader(parsed.Body))
		}
		if message.err != nil || !message.auth || message.recipient != "startup@worker.test" || parseErr != nil ||
			parsed.Header.Get("To") != "<startup@worker.test>" || !bytes.Contains(body, []byte(code)) {
			stop()
			<-runDone
			t.Fatal("startup worker did not send the queued synthetic challenge")
		}
	case <-time.After(20 * time.Second):
		stop()
		<-runDone
		t.Fatal("startup worker did not process the queued row")
	}
	acceptedDeadline := time.Now().Add(3 * time.Second)
	for {
		var state string
		if err := db.QueryRowContext(ctx, `SELECT state FROM auth_email_outbox WHERE challenge_id=?`, []byte(challengeID)).Scan(&state); err != nil {
			stop()
			<-runDone
			t.Fatal("startup worker state unavailable")
		}
		if state == string(issuance.DeliveryAccepted) {
			break
		}
		if !time.Now().Before(acceptedDeadline) {
			stop()
			<-runDone
			t.Fatalf("startup worker delivery state = %q, want accepted", state)
		}
		time.Sleep(10 * time.Millisecond)
	}
	stop()
	select {
	case err := <-runDone:
		if err != nil {
			t.Fatal("worker did not stop cleanly after cancellation")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("worker did not stop after cancellation")
	}
	if connectionCount.Load() != 1 {
		t.Fatalf("local SMTP connection count = %d, want one", connectionCount.Load())
	}
	var state string
	if err := db.QueryRowContext(ctx, `SELECT state FROM auth_email_outbox WHERE challenge_id=?`, []byte(challengeID)).Scan(&state); err != nil || state != string(issuance.DeliveryAccepted) {
		t.Fatal("worker did not persist accepted delivery state")
	}
	if strings.Contains(logs.String(), secrets.SMTPPassword) || strings.Contains(logs.String(), secrets.EmailIdentityIndexKey) || strings.Contains(logs.String(), code) {
		t.Fatal("worker logs contain secret or challenge material")
	}

	wrongIdentity := loaded
	wrongIdentity.EmailIdentityIndexKey = base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{0x77}, 32))
	if _, err := newRuntime(ctx, config.Config{MySQLDSN: dsn}, wrongIdentity, slog.New(slog.NewTextHandler(io.Discard, nil))); err == nil {
		t.Fatal("mismatched identity epoch accepted")
	}
	wrongHasherKey := loaded
	wrongHasherKey.EmailCodeHashKey = base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{0x55}, 32))
	if _, err := newRuntime(ctx, config.Config{MySQLDSN: dsn}, wrongHasherKey, slog.New(slog.NewTextHandler(io.Discard, nil))); err == nil {
		t.Fatal("mismatched code hash key accepted")
	}
	wrongHasherID := loaded
	wrongHasherID.EmailCodeHashKeyID = "different-hash-key-v1"
	if _, err := newRuntime(ctx, config.Config{MySQLDSN: dsn}, wrongHasherID, slog.New(slog.NewTextHandler(io.Discard, nil))); err == nil {
		t.Fatal("mismatched code hash key id accepted")
	}
	if _, err := db.ExecContext(ctx, `DELETE FROM auth_email_code_hash_key_epoch WHERE slot=1`); err != nil {
		t.Fatal("private test could not remove code hash epoch")
	}
	if _, err := newRuntime(ctx, config.Config{MySQLDSN: dsn}, loaded, slog.New(slog.NewTextHandler(io.Discard, nil))); err == nil {
		t.Fatal("missing code hash epoch was recreated despite challenge history")
	}
	if _, err := db.ExecContext(ctx, `DELETE FROM auth_email_code_key_epoch WHERE slot=1`); err != nil {
		t.Fatal("private test could not remove code epoch")
	}
	if _, err := newRuntime(ctx, config.Config{MySQLDSN: dsn}, loaded, slog.New(slog.NewTextHandler(io.Discard, nil))); err == nil {
		t.Fatal("missing code epoch was recreated despite challenge history")
	}
}

func validateWorkerTestDSN(t *testing.T, dsn string) {
	t.Helper()
	parsed, err := mysql.ParseDSN(dsn)
	if err != nil || parsed.Net != "unix" || parsed.User != "root" || parsed.Passwd != "" ||
		parsed.DBName != "nerae_email_worker_startup_test" || !parsed.ParseTime || parsed.Loc == nil || parsed.Loc.String() != "UTC" {
		t.Fatal("startup test requires its dedicated UTC Unix socket DSN")
	}
	base := filepath.Dir(parsed.Addr)
	info, err := os.Lstat(base)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0o077 != 0 {
		t.Fatal("startup test socket parent is not private")
	}
	resolved, err := filepath.EvalSymlinks(base)
	if err != nil || filepath.Dir(resolved) != "/private/tmp" || !strings.HasPrefix(filepath.Base(resolved), "nerae-a01-19-mysql-") || filepath.Base(parsed.Addr) != "mysql.sock" {
		t.Fatal("startup test socket is outside the dedicated harness")
	}
	socket, err := os.Lstat(parsed.Addr)
	if err != nil || socket.Mode()&os.ModeSocket == 0 || socket.Mode()&os.ModeSymlink != 0 {
		t.Fatal("startup test socket missing or invalid")
	}
}

func enqueueStartupChallenge(t *testing.T, ctx context.Context, r *runtime) (string, string) {
	t.Helper()
	replay, err := repository.NewProofReplayStore(bytes.Repeat([]byte{0x66}, 32))
	if err != nil {
		t.Fatal("test replay store setup failed")
	}
	challenges, err := challenge.NewStore(r.hasher, replay, r.identities, r.hasherID, challenge.Policy{
		ChallengeTTL: 5 * time.Minute, MaxAttempts: 3,
		DeviceProof: domain.ProofPolicy{MaxLifetime: 5 * time.Minute, ClockSkew: 30 * time.Second},
	})
	if err != nil {
		t.Fatal("test challenge store setup failed")
	}
	id, err := domain.GenerateChallengeID()
	if err != nil {
		t.Fatal("test challenge id generation failed")
	}
	nonce, err := domain.GenerateChallengeNonce()
	if err != nil {
		t.Fatal("test challenge nonce generation failed")
	}
	deviceKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal("test device key generation failed")
	}
	encode := base64.RawURLEncoding.EncodeToString
	public := domain.DevicePublicJWK{Kty: "EC", Crv: "P-256", X: encode(deviceKey.X.FillBytes(make([]byte, 32))), Y: encode(deviceKey.Y.FillBytes(make([]byte, 32)))}
	code, err := r.deriver.Derive(id, "email_login", 6)
	if err != nil {
		t.Fatal("test code derivation failed")
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal("test challenge transaction failed")
	}
	epoch, err := r.outbox.LockEpochs(ctx, tx)
	if err != nil {
		_ = tx.Rollback()
		t.Fatal("test outbox epoch lock failed")
	}
	if err := challenges.Issue(ctx, tx, challenge.IssueInput{Email: "startup@worker.test", ChallengeID: id, Nonce: nonce, Code: code, PublicKey: public}); err != nil {
		_ = tx.Rollback()
		t.Fatal("test challenge issue failed")
	}
	if err := r.outbox.Enqueue(ctx, tx, epoch, id, 6); err != nil {
		_ = tx.Rollback()
		t.Fatal("test outbox enqueue failed")
	}
	if err := tx.Commit(); err != nil {
		t.Fatal("test challenge commit failed")
	}
	return id, code
}

type startupSMTPMessage struct {
	recipient string
	body      string
	auth      bool
	err       error
}

func startupLocalSMTPSink(t *testing.T) (net.Listener, <-chan startupSMTPMessage, *atomic.Int64) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal("local TLS SMTP listener failed")
	}
	t.Cleanup(func() { _ = listener.Close() })
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal("local SMTP key generation failed")
	}
	cert := &x509.Certificate{SerialNumber: big.NewInt(61), Subject: pkix.Name{CommonName: "local worker SMTP"}, NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, IsCA: true, BasicConstraintsValid: true, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}}
	der, err := x509.CreateCertificate(rand.Reader, cert, cert, &key.PublicKey, key)
	if err != nil {
		t.Fatal("local SMTP certificate generation failed")
	}
	certPath := filepath.Join(t.TempDir(), "local-smtp-ca.pem")
	if err := os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0600); err != nil {
		t.Fatal("local SMTP trust fixture write failed")
	}
	t.Setenv("SSL_CERT_FILE", certPath)
	t.Setenv("SSL_CERT_DIR", filepath.Dir(certPath))
	t.Setenv("GODEBUG", "x509sslcertoverrideplatform=1")
	results := make(chan startupSMTPMessage, 1)
	var count atomic.Int64
	go func() {
		raw, err := listener.Accept()
		message := startupSMTPMessage{err: err}
		if err != nil {
			results <- message
			return
		}
		count.Add(1)
		conn := tls.Server(raw, &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}, MinVersion: tls.VersionTLS12})
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(15 * time.Second))
		message.err = conn.Handshake()
		if message.err != nil {
			results <- message
			return
		}
		_, message.err = io.WriteString(conn, "220 local test\r\n")
		reader := bufio.NewReader(conn)
		var body strings.Builder
		for message.err == nil {
			line, readErr := reader.ReadString('\n')
			if readErr != nil {
				message.err = readErr
				break
			}
			upper := strings.ToUpper(line)
			switch {
			case strings.HasPrefix(upper, "EHLO "):
				_, message.err = io.WriteString(conn, "250-local.test\r\n250-AUTH PLAIN\r\n250 SIZE 10000\r\n")
			case strings.HasPrefix(upper, "AUTH PLAIN "):
				message.auth = true
				_, message.err = io.WriteString(conn, "235 authenticated\r\n")
			case strings.HasPrefix(upper, "MAIL FROM:"):
				_, message.err = io.WriteString(conn, "250 accepted\r\n")
			case strings.HasPrefix(upper, "RCPT TO:"):
				message.recipient = strings.Trim(strings.TrimSpace(line[len("RCPT TO:"):]), "<>\r\n")
				_, message.err = io.WriteString(conn, "250 accepted\r\n")
			case strings.TrimSpace(upper) == "DATA":
				_, message.err = io.WriteString(conn, "354 continue\r\n")
				for message.err == nil {
					line, message.err = reader.ReadString('\n')
					if message.err != nil || line == ".\r\n" {
						break
					}
					body.WriteString(line)
				}
				message.body = body.String()
				if message.err == nil {
					_, message.err = io.WriteString(conn, "250 accepted\r\n")
				}
			case strings.TrimSpace(upper) == "QUIT":
				_, _ = io.WriteString(conn, "221 bye\r\n")
				message.err = nil
				results <- message
				return
			default:
				_, message.err = io.WriteString(conn, "500 unsupported\r\n")
			}
		}
		results <- message
	}()
	return listener, results, &count
}
