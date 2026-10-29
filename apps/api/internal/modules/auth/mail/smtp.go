// Package mail provides the email transport; it does not authenticate users.
package mail

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/quotedprintable"
	"net"
	netmail "net/mail"
	"net/smtp"
	"net/textproto"
	"strconv"
	"strings"
	"time"
)

var (
	ErrInvalidConfiguration = errors.New("invalid email transport configuration")
	ErrInvalidMessage       = errors.New("invalid verification email")
	ErrDeliveryFailed       = errors.New("email delivery was not accepted")
	ErrDeliveryCancelled    = errors.New("email delivery was cancelled before submission")
	ErrDeliveryUncertain    = errors.New("email submission result is unknown")
)

// SMTPConfig must be supplied by protected server configuration, never logged.
// This transport uses implicit TLS (for example, an SMTP TLS port), not plain
// SMTP or an opportunistic downgrade. Timeout bounds the entire operation.
type SMTPConfig struct {
	Address  string
	Username string
	Password string
	From     string
	Timeout  time.Duration
}

func (SMTPConfig) String() string   { return "SMTPConfig{redacted}" }
func (SMTPConfig) GoString() string { return "SMTPConfig{redacted}" }

type SMTPMailer struct {
	address  string
	host     string
	username string
	password string
	from     string
	timeout  time.Duration
	roots    *x509.CertPool // nil in production: use the system trust store
}

func (*SMTPMailer) String() string   { return "SMTPMailer{redacted}" }
func (*SMTPMailer) GoString() string { return "SMTPMailer{redacted}" }

func NewSMTPMailer(cfg SMTPConfig) (*SMTPMailer, error) {
	host, port, err := net.SplitHostPort(cfg.Address)
	portNumber, portErr := strconv.Atoi(port)
	if err != nil || host == "" || portErr != nil || portNumber < 1 || portNumber > 65535 ||
		strings.ContainsAny(host, " \t\r\n\x00") || !validAddress(cfg.From) ||
		cfg.Username == "" || len(cfg.Username) > 254 || strings.ContainsAny(cfg.Username, "\r\n\x00") ||
		cfg.Password == "" || len(cfg.Password) > 4096 || strings.ContainsRune(cfg.Password, 0) ||
		cfg.Timeout <= 0 || cfg.Timeout > time.Minute {
		return nil, ErrInvalidConfiguration
	}
	return &SMTPMailer{address: cfg.Address, host: host, username: cfg.Username,
		password: cfg.Password, from: cfg.From, timeout: cfg.Timeout}, nil
}

// SendVerificationCode submits one message; nil means the SMTP server accepted
// it, not that it reached the recipient. The caller must not blindly retry an
// uncertain result, because the provider may already have accepted the email.
func (s *SMTPMailer) SendVerificationCode(ctx context.Context, recipient, code string) error {
	if s == nil || ctx == nil || !validAddress(recipient) || !validCode(code) {
		return ErrInvalidMessage
	}
	if ctx.Err() != nil {
		return ErrDeliveryCancelled
	}
	deadline := time.Now().Add(s.timeout)
	if contextDeadline, ok := ctx.Deadline(); ok && contextDeadline.Before(deadline) {
		deadline = contextDeadline
	}
	operationCtx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	dialer := tls.Dialer{NetDialer: &net.Dialer{}, Config: &tls.Config{
		ServerName: s.host, RootCAs: s.roots, MinVersion: tls.VersionTLS12,
	}}
	conn, err := dialer.DialContext(operationCtx, "tcp", s.address)
	if err != nil {
		return beforeSubmissionError(ctx)
	}
	defer conn.Close()
	if err := conn.SetDeadline(deadline); err != nil {
		return beforeSubmissionError(ctx)
	}
	stopCancellation := context.AfterFunc(operationCtx, func() { _ = conn.SetDeadline(time.Now()) })
	defer stopCancellation()
	client, err := smtp.NewClient(conn, s.host)
	if err != nil {
		return beforeSubmissionError(ctx)
	}
	defer client.Close()
	if err := client.Auth(smtp.PlainAuth("", s.username, s.password, s.host)); err != nil {
		return beforeSubmissionError(ctx)
	}
	if err := client.Mail(s.from); err != nil {
		return beforeSubmissionError(ctx)
	}
	if err := client.Rcpt(recipient); err != nil {
		return beforeSubmissionError(ctx)
	}
	writer, err := client.Data()
	if err != nil {
		return beforeSubmissionError(ctx)
	}
	subject := mime.QEncoding.Encode("UTF-8", "Nerae 验证码")
	var body bytes.Buffer
	encoded := quotedprintable.NewWriter(&body)
	_, _ = io.WriteString(encoded, "你的 Nerae 验证码是 "+code+"。如非本人操作，请忽略此邮件。\r\n")
	_ = encoded.Close() // bytes.Buffer writes cannot fail
	message := fmt.Sprintf("From: <%s>\r\nTo: <%s>\r\nSubject: %s\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=UTF-8\r\nContent-Transfer-Encoding: quoted-printable\r\n\r\n%s", s.from, recipient, subject, body.String())
	if _, err := io.WriteString(writer, message); err != nil {
		return ErrDeliveryUncertain
	}
	if err := writer.Close(); err != nil {
		var smtpError *textproto.Error
		if errors.As(err, &smtpError) && smtpError.Code >= 400 && smtpError.Code < 600 {
			return ErrDeliveryFailed
		}
		return ErrDeliveryUncertain
	}
	// After DATA's positive response, QUIT failure does not undo acceptance.
	_ = client.Quit()
	return nil
}

func beforeSubmissionError(ctx context.Context) error {
	if ctx.Err() != nil {
		return ErrDeliveryCancelled
	}
	return ErrDeliveryFailed
}

func validAddress(value string) bool {
	if value == "" || len(value) > 254 || strings.Count(value, "@") != 1 {
		return false
	}
	for _, c := range value {
		if c <= ' ' || c >= 127 {
			return false
		}
	}
	parsed, err := netmail.ParseAddress(value)
	return err == nil && parsed.Name == "" && parsed.Address == value
}

func validCode(value string) bool {
	if len(value) < 6 || len(value) > 10 {
		return false
	}
	for _, c := range value {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}
