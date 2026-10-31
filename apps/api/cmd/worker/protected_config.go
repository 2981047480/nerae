package main

import (
	"bytes"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"syscall"
	"time"
	"unicode/utf8"

	"nerae/api/internal/modules/auth/mail"
	"nerae/api/internal/modules/auth/mailworker"
)

const (
	secretsFileEnv = "NERAE_WORKER_SECRETS_FILE"
	maxSecretsSize = 32 * 1024
)

var workerSecretJSONFields = map[string]struct{}{
	"smtp_address": {}, "smtp_username": {}, "smtp_password": {}, "smtp_from": {}, "smtp_timeout_ms": {},
	"email_identity_index_key": {}, "email_identity_encryption_key": {}, "email_identity_key_id": {},
	"email_code_hash_key": {}, "email_code_derivation_key": {}, "email_code_key_id": {},
	"poll_interval_ms": {}, "recovery_batch": {}, "lease_ms": {}, "startup_timeout_ms": {},
}

var errWorkerConfig = errors.New("worker configuration unavailable")

// workerSecrets is loaded only from an explicitly configured, permission-
// restricted file. Its fields are intentionally never logged or serialized.
type workerSecrets struct {
	SMTPAddress                string `json:"smtp_address"`
	SMTPUsername               string `json:"smtp_username"`
	SMTPPassword               string `json:"smtp_password"`
	SMTPFrom                   string `json:"smtp_from"`
	SMTPTimeoutMS              int64  `json:"smtp_timeout_ms"`
	EmailIdentityIndexKey      string `json:"email_identity_index_key"`
	EmailIdentityEncryptionKey string `json:"email_identity_encryption_key"`
	EmailIdentityKeyID         string `json:"email_identity_key_id"`
	EmailCodeHashKey           string `json:"email_code_hash_key"`
	EmailCodeDerivationKey     string `json:"email_code_derivation_key"`
	EmailCodeKeyID             string `json:"email_code_key_id"`
	PollIntervalMS             int64  `json:"poll_interval_ms"`
	RecoveryBatch              int    `json:"recovery_batch"`
	LeaseMS                    int64  `json:"lease_ms"`
	StartupTimeoutMS           int64  `json:"startup_timeout_ms"`
}

func (workerSecrets) String() string               { return "WorkerSecrets{redacted}" }
func (workerSecrets) GoString() string             { return "WorkerSecrets{redacted}" }
func (workerSecrets) MarshalJSON() ([]byte, error) { return nil, errWorkerConfig }
func (workerSecrets) LogValue() slog.Value         { return slog.StringValue("WorkerSecrets{redacted}") }

func loadWorkerSecrets() (workerSecrets, error) {
	path := os.Getenv(secretsFileEnv)
	if !filepath.IsAbs(path) {
		return workerSecrets{}, errWorkerConfig
	}
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC|syscall.O_NONBLOCK, 0)
	if err != nil {
		return workerSecrets{}, errWorkerConfig
	}
	f := os.NewFile(uintptr(fd), "worker-secrets")
	if f == nil {
		_ = syscall.Close(fd)
		return workerSecrets{}, errWorkerConfig
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 || info.Mode().Perm()&0o400 == 0 || info.Size() < 2 || info.Size() > maxSecretsSize {
		return workerSecrets{}, errWorkerConfig
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || uint32(stat.Uid) != uint32(os.Geteuid()) {
		return workerSecrets{}, errWorkerConfig
	}
	raw, err := io.ReadAll(io.LimitReader(f, maxSecretsSize+1))
	if err != nil || len(raw) > maxSecretsSize || !utf8.Valid(raw) || !validUniqueTopLevelJSON(raw) {
		return workerSecrets{}, errWorkerConfig
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var cfg workerSecrets
	if err := decoder.Decode(&cfg); err != nil {
		return workerSecrets{}, errWorkerConfig
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return workerSecrets{}, errWorkerConfig
	}
	if !cfg.valid() {
		return workerSecrets{}, errWorkerConfig
	}
	return cfg, nil
}

func validUniqueTopLevelJSON(raw []byte) bool {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return false
	}
	seen := make(map[string]struct{}, 16)
	for decoder.More() {
		token, err = decoder.Token()
		if err != nil {
			return false
		}
		name, ok := token.(string)
		if !ok {
			return false
		}
		if _, canonical := workerSecretJSONFields[name]; !canonical {
			return false
		}
		if _, duplicate := seen[name]; duplicate {
			return false
		}
		seen[name] = struct{}{}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return false
		}
	}
	token, err = decoder.Token()
	if err != nil || token != json.Delim('}') {
		return false
	}
	var trailing any
	return errors.Is(decoder.Decode(&trailing), io.EOF)
}

func (s workerSecrets) valid() bool {
	if s.SMTPAddress == "" || s.SMTPUsername == "" || s.SMTPPassword == "" || s.SMTPFrom == "" ||
		!validKeyID(s.EmailIdentityKeyID) || !validKeyID(s.EmailCodeKeyID) ||
		s.SMTPTimeoutMS < 1 || s.SMTPTimeoutMS > int64(time.Minute/time.Millisecond) ||
		s.PollIntervalMS < 100 || s.PollIntervalMS > int64(time.Minute/time.Millisecond) ||
		s.RecoveryBatch < 1 || s.RecoveryBatch > 500 || s.LeaseMS < 1000 || s.LeaseMS > int64((10*time.Minute)/time.Millisecond) ||
		s.StartupTimeoutMS < 1000 || s.StartupTimeoutMS > 30000 {
		return false
	}
	keys := []string{s.EmailIdentityIndexKey, s.EmailIdentityEncryptionKey, s.EmailCodeHashKey, s.EmailCodeDerivationKey}
	decoded := make([][]byte, 0, len(keys))
	for _, encoded := range keys {
		key, err := base64.RawURLEncoding.DecodeString(encoded)
		if err != nil || len(key) != 32 || base64.RawURLEncoding.EncodeToString(key) != encoded {
			return false
		}
		if isZeroKey(key) {
			return false
		}
		for _, prior := range decoded {
			if bytes.Equal(key, prior) {
				return false
			}
		}
		decoded = append(decoded, key)
	}
	return true
}

func isZeroKey(key []byte) bool {
	if len(key) == 0 {
		return true
	}
	for _, value := range key {
		if value != 0 {
			return false
		}
	}
	return true
}

func validKeyID(value string) bool {
	if value == "" || len(value) > 64 {
		return false
	}
	for _, char := range value {
		if !(char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' || char == '-' || char == '_' || char == '.') {
			return false
		}
	}
	return true
}

func (s workerSecrets) smtpConfig() mail.SMTPConfig {
	return mail.SMTPConfig{
		Address: s.SMTPAddress, Username: s.SMTPUsername, Password: s.SMTPPassword,
		From: s.SMTPFrom, Timeout: time.Duration(s.SMTPTimeoutMS) * time.Millisecond,
	}
}

func (s workerSecrets) workerConfig(outbox mailworker.Outbox, dbHandle *sql.DB, logger *slog.Logger) mailworker.Config {
	return mailworker.Config{
		Outbox: outbox, DB: dbHandle, PollInterval: time.Duration(s.PollIntervalMS) * time.Millisecond,
		RecoveryBatch: s.RecoveryBatch, Logger: logger,
	}
}
