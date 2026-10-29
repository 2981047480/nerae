package mail

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/big"
	"mime/quotedprintable"
	"net"
	netmail "net/mail"
	"strings"
	"testing"
	"time"
)

type smtpResult struct {
	message  string
	authSeen bool
	err      error
}

// A private loopback TLS server tests the SMTP protocol without contacting an
// email provider. Its certificate, credentials and recipient are test-only.
func localSMTP(t *testing.T, mode string) (*SMTPMailer, <-chan smtpResult, <-chan struct{}) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	cert := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "local test"},
		NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour),
		KeyUsage:    x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, IsCA: true, BasicConstraintsValid: true,
		IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}}
	der, err := x509.CreateCertificate(rand.Reader, cert, cert, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(parsed)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	results := make(chan smtpResult, 1)
	connected := make(chan struct{})
	go func() {
		result := smtpResult{}
		defer func() { results <- result }()
		raw, err := listener.Accept()
		if err != nil {
			result.err = err
			return
		}
		conn := tls.Server(raw, &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}, MinVersion: tls.VersionTLS12})
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
		if err := conn.Handshake(); err != nil {
			result.err = err
			return
		}
		close(connected)
		if mode == "stall" {
			_, _ = io.Copy(io.Discard, conn)
			return
		}
		if _, err := io.WriteString(conn, "220 local test\r\n"); err != nil {
			result.err = err
			return
		}
		reader := bufio.NewReader(conn)
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				result.err = err
				return
			}
			upper := strings.ToUpper(line)
			switch {
			case strings.HasPrefix(upper, "EHLO "):
				_, err = io.WriteString(conn, "250-local.test\r\n250-AUTH PLAIN\r\n250 SIZE 10000\r\n")
			case strings.HasPrefix(upper, "AUTH PLAIN "):
				result.authSeen = true
				_, err = io.WriteString(conn, "235 authenticated\r\n")
			case strings.HasPrefix(upper, "MAIL FROM:"):
				_, err = io.WriteString(conn, "250 accepted\r\n")
			case strings.HasPrefix(upper, "RCPT TO:"):
				if mode == "reject-recipient" {
					_, err = io.WriteString(conn, "550 rejected\r\n")
				} else {
					_, err = io.WriteString(conn, "250 accepted\r\n")
				}
			case strings.TrimSpace(upper) == "DATA":
				if _, err = io.WriteString(conn, "354 continue\r\n"); err != nil {
					break
				}
				var body strings.Builder
				for {
					line, err = reader.ReadString('\n')
					if err != nil || line == ".\r\n" {
						break
					}
					body.WriteString(line)
				}
				result.message = body.String()
				if err != nil || mode == "uncertain" {
					return
				}
				if mode == "reject-data-4xx" || mode == "reject-data-5xx" {
					if mode == "reject-data-4xx" {
						_, _ = io.WriteString(conn, "450 rejected\r\n")
					} else {
						_, _ = io.WriteString(conn, "550 rejected\r\n")
					}
					return
				}
				_, err = io.WriteString(conn, "250 accepted\r\n")
				if mode == "quit-fails" {
					return
				}
			case strings.TrimSpace(upper) == "QUIT":
				_, _ = io.WriteString(conn, "221 bye\r\n")
				return
			default:
				_, err = io.WriteString(conn, "500 unsupported\r\n")
			}
			if err != nil {
				result.err = err
				return
			}
		}
	}()
	mailer, err := NewSMTPMailer(SMTPConfig{Address: listener.Addr().String(), Username: "sender@example.test",
		Password: "local-test-password", From: "sender@example.test", Timeout: 3 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	mailer.roots = roots
	return mailer, results, connected
}

func TestSMTPDeliveryAndUnknownResult(t *testing.T) {
	for _, mode := range []string{"success", "uncertain", "quit-fails", "reject-recipient", "reject-data-4xx", "reject-data-5xx"} {
		t.Run(mode, func(t *testing.T) {
			mailer, results, _ := localSMTP(t, mode)
			err := mailer.SendVerificationCode(context.Background(), "recipient@example.test", "012345")
			switch mode {
			case "success", "quit-fails":
				if err != nil {
					t.Fatalf("accepted message failed: %v", err)
				}
			case "uncertain":
				if !errors.Is(err, ErrDeliveryUncertain) {
					t.Fatalf("lost acknowledgement result: %v", err)
				}
			case "reject-recipient", "reject-data-4xx", "reject-data-5xx":
				if !errors.Is(err, ErrDeliveryFailed) {
					t.Fatalf("recipient rejection result: %v", err)
				}
			}
			result := <-results
			if !result.authSeen {
				t.Fatal("TLS test server did not receive SMTP authentication")
			}
			if mode == "reject-recipient" {
				if result.message != "" {
					t.Fatal("rejected recipient received a message")
				}
				return
			}
			message, err := netmail.ReadMessage(strings.NewReader(result.message))
			if err != nil {
				t.Fatal("accepted message is not valid mail")
			}
			body, err := io.ReadAll(quotedprintable.NewReader(message.Body))
			if err != nil || !strings.Contains(string(body), "012345") || message.Header.Get("To") != "<recipient@example.test>" {
				t.Fatal("accepted message omitted its test recipient or code")
			}
		})
	}
}

func TestSMTPRequiresValidCertificateAndHandlesCancellation(t *testing.T) {
	t.Run("untrusted certificate", func(t *testing.T) {
		mailer, results, _ := localSMTP(t, "success")
		mailer.roots = x509.NewCertPool()
		if err := mailer.SendVerificationCode(context.Background(), "recipient@example.test", "012345"); !errors.Is(err, ErrDeliveryFailed) {
			t.Fatalf("untrusted certificate accepted: %v", err)
		}
		if result := <-results; result.authSeen {
			t.Fatal("credentials sent before certificate verification")
		}
	})
	t.Run("certificate hostname mismatch", func(t *testing.T) {
		mailer, results, _ := localSMTP(t, "success")
		// Connect to the local fixture but ask TLS to verify a different name.
		mailer.host = "wrong.example.test"
		if err := mailer.SendVerificationCode(context.Background(), "recipient@example.test", "012345"); !errors.Is(err, ErrDeliveryFailed) {
			t.Fatalf("certificate for another host accepted: %v", err)
		}
		if result := <-results; result.authSeen {
			t.Fatal("credentials sent to a mismatched certificate")
		}
	})
	t.Run("cancel while waiting for greeting", func(t *testing.T) {
		mailer, results, connected := localSMTP(t, "stall")
		ctx, cancel := context.WithCancel(context.Background())
		finished := make(chan error, 1)
		go func() { finished <- mailer.SendVerificationCode(ctx, "recipient@example.test", "012345") }()
		<-connected
		cancel()
		if err := <-finished; !errors.Is(err, ErrDeliveryCancelled) {
			t.Fatalf("cancelled request result: %v", err)
		}
		<-results
	})
}

func TestSMTPRejectsInjectionAndRedactsConfiguration(t *testing.T) {
	cfg := SMTPConfig{Address: "localhost:465", Username: "user", Password: "secret-password", From: "sender@example.test", Timeout: time.Second}
	mailer, err := NewSMTPMailer(cfg)
	if err != nil {
		t.Fatal(err)
	}
	for _, recipient := range []string{"", "recipient@example.test\r\nBcc:other@example.test", "a@@example.test", "Recipient <recipient@example.test>"} {
		if err := mailer.SendVerificationCode(context.Background(), recipient, "012345"); !errors.Is(err, ErrInvalidMessage) || strings.Contains(err.Error(), recipient) && recipient != "" {
			t.Fatalf("bad address accepted or leaked: %v", err)
		}
	}
	if err := mailer.SendVerificationCode(context.Background(), "recipient@example.test", "12 345"); !errors.Is(err, ErrInvalidMessage) {
		t.Fatalf("bad code accepted: %v", err)
	}
	for _, rendered := range []string{fmt.Sprintf("%+v", cfg), fmt.Sprintf("%#v", cfg), fmt.Sprintf("%+v", mailer), fmt.Sprintf("%#v", mailer)} {
		if strings.Contains(rendered, cfg.Password) || strings.Contains(rendered, cfg.From) {
			t.Fatal("formatted mail transport exposed credentials or sender")
		}
	}
	for _, value := range []any{cfg, mailer} {
		if _, err := json.Marshal(value); err == nil {
			t.Fatal("sensitive SMTP configuration was JSON-serializable")
		}
	}
	var log strings.Builder
	slog.New(slog.NewJSONHandler(&log, nil)).Info("test", "config", cfg, "mailer", mailer)
	if strings.Contains(log.String(), cfg.Password) || strings.Contains(log.String(), cfg.From) {
		t.Fatal("structured logging exposed SMTP configuration")
	}
	bad := cfg
	bad.From = "sender@example.test\r\nBcc:other@example.test"
	if _, err := NewSMTPMailer(bad); !errors.Is(err, ErrInvalidConfiguration) {
		t.Fatalf("bad sender accepted: %v", err)
	}
}

func TestSMTPBoundsTheTLSHandshake(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		conn, err := listener.Accept()
		if err == nil {
			defer conn.Close()
			_, _ = io.Copy(io.Discard, conn) // Never send a TLS handshake response.
		}
	}()
	mailer, err := NewSMTPMailer(SMTPConfig{Address: listener.Addr().String(), Username: "sender@example.test",
		Password: "local-test-password", From: "sender@example.test", Timeout: 150 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	if err := mailer.SendVerificationCode(context.Background(), "recipient@example.test", "012345"); !errors.Is(err, ErrDeliveryFailed) {
		t.Fatalf("stalled TLS handshake result: %v", err)
	}
	if time.Since(started) > 2*time.Second {
		t.Fatal("configured timeout did not bound the TLS handshake")
	}
	<-finished
}
