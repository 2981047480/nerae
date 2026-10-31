package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func validSecretsForTest() workerSecrets {
	encode := func(value byte) string { return base64.RawURLEncoding.EncodeToString(bytesRepeat(value, 32)) }
	return workerSecrets{
		SMTPAddress: "smtp.example.test:465", SMTPUsername: "worker@example.test", SMTPPassword: "smtp-password-never-log",
		SMTPFrom: "worker@example.test", SMTPTimeoutMS: 5000,
		EmailIdentityIndexKey: encode(0x11), EmailIdentityEncryptionKey: encode(0x22), EmailIdentityKeyID: "identity-v1",
		EmailCodeHashKey: encode(0x33), EmailCodeDerivationKey: encode(0x44), EmailCodeKeyID: "code-v1",
		EmailCodeHashKeyID: "code-hash-v1",
		PollIntervalMS:     1000, RecoveryBatch: 10, LeaseMS: 20000, StartupTimeoutMS: 5000,
	}
}

func bytesRepeat(value byte, size int) []byte {
	result := make([]byte, size)
	for i := range result {
		result[i] = value
	}
	return result
}

func writeSecretsFile(t *testing.T, raw []byte, mode os.FileMode) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "worker-secrets.json")
	if err := os.WriteFile(path, raw, mode); err != nil {
		t.Fatal("could not prepare protected config fixture")
	}
	return path
}

func marshalSecrets(t *testing.T, cfg workerSecrets) []byte {
	t.Helper()
	type wire workerSecrets
	raw, err := json.Marshal(wire(cfg))
	if err != nil {
		t.Fatal("could not marshal config fixture")
	}
	return raw
}

func TestLoadWorkerSecretsRequiresProtectedExplicitConfig(t *testing.T) {
	t.Run("missing path", func(t *testing.T) {
		t.Setenv(secretsFileEnv, "")
		if _, err := loadWorkerSecrets(); err == nil {
			t.Fatal("missing protected config accepted")
		}
	})
	t.Run("relative path", func(t *testing.T) {
		file := writeSecretsFile(t, marshalSecrets(t, validSecretsForTest()), 0600)
		t.Setenv(secretsFileEnv, filepath.Base(file))
		if _, err := loadWorkerSecrets(); err == nil {
			t.Fatal("relative config path accepted")
		}
	})
	t.Run("permissions", func(t *testing.T) {
		file := writeSecretsFile(t, marshalSecrets(t, validSecretsForTest()), 0640)
		t.Setenv(secretsFileEnv, file)
		if _, err := loadWorkerSecrets(); err == nil {
			t.Fatal("group-readable secret file accepted")
		}
	})
	t.Run("symlink", func(t *testing.T) {
		dir := t.TempDir()
		target := filepath.Join(dir, "target.json")
		if err := os.WriteFile(target, marshalSecrets(t, validSecretsForTest()), 0600); err != nil {
			t.Fatal("could not prepare config fixture")
		}
		link := filepath.Join(dir, "link.json")
		if err := os.Symlink(target, link); err != nil {
			t.Fatal("could not prepare symlink fixture")
		}
		t.Setenv(secretsFileEnv, link)
		if _, err := loadWorkerSecrets(); err == nil {
			t.Fatal("symlink config accepted")
		}
	})
	t.Run("fifo does not block", func(t *testing.T) {
		fifo := filepath.Join(t.TempDir(), "worker-secrets.fifo")
		if err := syscall.Mkfifo(fifo, 0600); err != nil {
			t.Fatal("could not prepare FIFO fixture")
		}
		t.Setenv(secretsFileEnv, fifo)
		result := make(chan error, 1)
		go func() {
			_, err := loadWorkerSecrets()
			result <- err
		}()
		select {
		case err := <-result:
			if err == nil {
				t.Fatal("FIFO config accepted")
			}
		case <-time.After(time.Second):
			t.Fatal("opening a FIFO config blocked")
		}
	})
	t.Run("valid", func(t *testing.T) {
		file := writeSecretsFile(t, marshalSecrets(t, validSecretsForTest()), 0600)
		t.Setenv(secretsFileEnv, file)
		cfg, err := loadWorkerSecrets()
		if err != nil || !cfg.valid() {
			t.Fatal("valid protected config rejected")
		}
	})
}

func TestWorkerSecretsRejectMalformedKeysAndDuplicateFields(t *testing.T) {
	valid := validSecretsForTest()
	cases := []struct {
		name   string
		update func(*workerSecrets)
	}{
		{"padded encoding", func(c *workerSecrets) { c.EmailCodeHashKey += "=" }},
		{"short key", func(c *workerSecrets) { c.EmailCodeHashKey = base64.RawURLEncoding.EncodeToString([]byte("short")) }},
		{"reused key", func(c *workerSecrets) { c.EmailCodeHashKey = c.EmailIdentityIndexKey }},
		{"zero identity index key", func(c *workerSecrets) {
			c.EmailIdentityIndexKey = base64.RawURLEncoding.EncodeToString(make([]byte, 32))
		}},
		{"zero identity encryption key", func(c *workerSecrets) {
			c.EmailIdentityEncryptionKey = base64.RawURLEncoding.EncodeToString(make([]byte, 32))
		}},
		{"zero code hash key", func(c *workerSecrets) { c.EmailCodeHashKey = base64.RawURLEncoding.EncodeToString(make([]byte, 32)) }},
		{"zero code derivation key", func(c *workerSecrets) {
			c.EmailCodeDerivationKey = base64.RawURLEncoding.EncodeToString(make([]byte, 32))
		}},
		{"missing identity key id", func(c *workerSecrets) { c.EmailIdentityKeyID = "" }},
		{"missing code hash key id", func(c *workerSecrets) { c.EmailCodeHashKeyID = "" }},
		{"invalid code hash key id", func(c *workerSecrets) { c.EmailCodeHashKeyID = "../bad" }},
		{"invalid interval", func(c *workerSecrets) { c.PollIntervalMS = 1 }},
		{"missing smtp secret", func(c *workerSecrets) { c.SMTPPassword = "" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := valid
			tc.update(&cfg)
			file := writeSecretsFile(t, marshalSecrets(t, cfg), 0600)
			t.Setenv(secretsFileEnv, file)
			if _, err := loadWorkerSecrets(); err == nil {
				t.Fatal("invalid worker config accepted")
			}
		})
	}
	t.Run("duplicate field", func(t *testing.T) {
		raw := marshalSecrets(t, valid)
		needle := []byte(`"smtp_address":`)
		if !strings.Contains(string(raw), string(needle)) {
			t.Fatal("fixture lacks address")
		}
		duplicate := append(append([]byte(`{"smtp_address":"first",`), raw[1:]...), []byte(`}`)...)
		file := writeSecretsFile(t, duplicate, 0600)
		t.Setenv(secretsFileEnv, file)
		if _, err := loadWorkerSecrets(); err == nil {
			t.Fatal("duplicate field accepted")
		}
	})
	t.Run("noncanonical case variant alone", func(t *testing.T) {
		raw := strings.Replace(string(marshalSecrets(t, valid)), `"smtp_password"`, `"SMTP_PASSWORD"`, 1)
		file := writeSecretsFile(t, []byte(raw), 0600)
		t.Setenv(secretsFileEnv, file)
		if _, err := loadWorkerSecrets(); err == nil {
			t.Fatal("case-variant field accepted")
		}
	})
	t.Run("noncanonical case variant with canonical field", func(t *testing.T) {
		raw := strings.Replace(string(marshalSecrets(t, valid)), `"smtp_password":`, `"SMTP_PASSWORD":"alternate", "smtp_password":`, 1)
		file := writeSecretsFile(t, []byte(raw), 0600)
		t.Setenv(secretsFileEnv, file)
		if _, err := loadWorkerSecrets(); err == nil {
			t.Fatal("case-variant duplicate field accepted")
		}
	})
	t.Run("trailing document", func(t *testing.T) {
		file := writeSecretsFile(t, append(marshalSecrets(t, valid), []byte(` {}`)...), 0600)
		t.Setenv(secretsFileEnv, file)
		if _, err := loadWorkerSecrets(); err == nil {
			t.Fatal("trailing JSON accepted")
		}
	})
}

func TestWorkerSecretsAndStartupErrorsAreRedacted(t *testing.T) {
	secrets := validSecretsForTest()
	if got := fmt.Sprintf("%v %#v", secrets, secrets); strings.Contains(got, secrets.SMTPPassword) || strings.Contains(got, secrets.EmailIdentityIndexKey) {
		t.Fatal("formatted worker secrets contain secret values")
	}
	if _, err := json.Marshal(secrets); err == nil {
		t.Fatal("worker secrets were JSON serializable")
	}
	var logs strings.Builder
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	logger.Info("config", "secrets", secrets)
	if strings.Contains(logs.String(), secrets.SMTPPassword) || strings.Contains(logs.String(), secrets.EmailIdentityIndexKey) {
		t.Fatal("structured log contains secret values")
	}
}

func TestWorkerDSNRequiresVerifiedTCPTransport(t *testing.T) {
	cases := []struct {
		name string
		dsn  string
		want bool
	}{
		{"private unix socket", "worker@unix(/private/tmp/worker.sock)/worker?parseTime=true&loc=UTC", true},
		{"verified TLS TCP", "worker:secret@tcp(db.example.test:3306)/worker?parseTime=true&loc=UTC&tls=true", true},
		{"plaintext TCP", "worker:secret@tcp(db.example.test:3306)/worker?parseTime=true&loc=UTC", false},
		{"skip verification", "worker:secret@tcp(db.example.test:3306)/worker?parseTime=true&loc=UTC&tls=skip-verify", false},
		{"non UTC timestamps", "worker@unix(/private/tmp/worker.sock)/worker?parseTime=true&loc=Local", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := validWorkerDSN(tc.dsn); got != tc.want {
				t.Fatalf("validWorkerDSN() = %v, want %v", got, tc.want)
			}
		})
	}
}
