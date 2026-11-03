package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func validAPIConfigFixture() apiSecretFile {
	key := func(b byte) string { return base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{b}, 32)) }
	return apiSecretFile{ProxyCredential: key(21), IdentityIndex: key(1), IdentityCipher: key(2), IdentityID: "identity-v1",
		CodeHash: key(3), CodeHashID: "code-hash-v1", CodeDerive: key(4), CodeID: "code-v1",
		ProofReplay: key(5), SessionToken: key(6), Approval: key(7), ApprovalID: "approval-v1",
		ChallengeIdem: key(8), ChallengeRequest: key(9), ChallengeCipher: key(10), ChallengeID: "challenge-v1",
		LoginIdem: key(11), LoginRequest: key(12), LoginCipher: key(13), LoginID: "login-v1",
		RateEmail: key(14), RateDevice: key(15), RateIP: key(16), RateGlobal: key(17),
		WorkoutName: key(18), WorkoutNameID: "name-v1", WorkoutHash: key(19), WorkoutCipher: key(20), WorkoutID: "workout-v1",
		TrustedProxies: []string{"127.0.0.1/32", "::1/128"}, ChallengeTTL: 300, ChallengeAttempts: 3,
		ProofLifetime: 300, ProofSkew: 30, ChallengeRetention: 3600, LoginRetention: 120,
		SessionLifetime: 3600, WorkoutRetention: 86400, ApprovalLifetime: 300, ReauthWindow: 60,
		CodeDigits: 6, IPv6PrefixBits: 64, ChallengeWindow: 900, ChallengeEmail: 5, ChallengeDevice: 10,
		ChallengeIP: 30, ChallengeGlobal: 1000, LoginWindow: 900, LoginEmail: 10, LoginDevice: 20, LoginIP: 50, LoginGlobal: 1000,
		ApprovalWindow: 300, ApprovalSubject: 10, ApprovalAttempt: 3, ApprovalIP: 20, ApprovalGlobal: 1000}
}

func encodeAPIConfig(t *testing.T, v apiSecretFile) []byte {
	t.Helper()
	// Deliberately bypass redacted MarshalJSON only inside a synthetic fixture.
	type fixtureWire apiSecretFile
	raw, err := json.Marshal(fixtureWire(v))
	if err != nil {
		t.Fatal("synthetic configuration encoding failed")
	}
	return raw
}

func testAPIConfigFile(t *testing.T, raw []byte, mode os.FileMode) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "api-secrets.json")
	if err := os.WriteFile(path, raw, mode); err != nil {
		t.Fatal("synthetic configuration file write failed")
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal("synthetic configuration chmod failed")
	}
	return path
}

func TestLoadAPISettingsProtectedFile(t *testing.T) {
	fixture := validAPIConfigFixture()
	raw := encodeAPIConfig(t, fixture)
	valid := testAPIConfigFile(t, raw, 0o600)
	t.Setenv(apiSecretsFileEnv, valid)
	settings, err := loadAPISettings()
	if err != nil || len(settings.proxies) != 2 || settings.keys.identityID != fixture.IdentityID ||
		settings.keys.codeHashID != fixture.CodeHashID || settings.policy.workoutRetention != 24*time.Hour ||
		settings.policy.rate.EmailChallenge.Email.Limit != 5 || settings.policy.rate.EmailLogin.IP.Limit != 50 ||
		settings.policy.approvalRate.Approval.Limit != 3 || settings.policy.approvalRate.Approval.Window != 5*time.Minute {
		t.Fatal("protected valid settings failed to load")
	}
	if fmt.Sprint(settings) != "APIBusinessSettings{redacted}" || fmt.Sprint(fixture) != "APISecrets{redacted}" {
		t.Fatal("settings exposed in formatted output")
	}
	if _, err := json.Marshal(settings); err == nil {
		t.Fatal("settings serialized")
	}
	var log bytes.Buffer
	slog.New(slog.NewJSONHandler(&log, nil)).Info("config", "value", settings)
	if strings.Contains(log.String(), fixture.IdentityIndex) || strings.Contains(log.String(), valid) {
		t.Fatal("settings leaked to logs")
	}

	for name, path := range map[string]string{
		"empty": "", "relative": "api-secrets.json", "missing": filepath.Join(t.TempDir(), "missing"),
		"world-readable": testAPIConfigFile(t, raw, 0o640), "directory": t.TempDir(),
	} {
		t.Run(name, func(t *testing.T) {
			t.Setenv(apiSecretsFileEnv, path)
			if _, err := loadAPISettings(); err == nil || strings.Contains(fmt.Sprint(err), path) && path != "" {
				t.Fatal("invalid file was accepted or leaked")
			}
		})
	}
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(valid, link); err != nil {
		t.Fatal("test symlink failed")
	}
	t.Setenv(apiSecretsFileEnv, link)
	if _, err := loadAPISettings(); err == nil {
		t.Fatal("symlink accepted")
	}
	fifo := filepath.Join(t.TempDir(), "fifo")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal("test FIFO failed")
	}
	t.Setenv(apiSecretsFileEnv, fifo)
	done := make(chan error, 1)
	go func() { _, err := loadAPISettings(); done <- err }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("FIFO accepted")
		}
	case <-time.After(time.Second):
		t.Fatal("FIFO blocked the startup loader")
	}
}

func TestAPISettingsRejectAmbiguityAndInvalidPolicy(t *testing.T) {
	fixture := validAPIConfigFixture()
	raw := encodeAPIConfig(t, fixture)
	mutations := map[string][]byte{
		"duplicate":    append(append([]byte{}, raw[:len(raw)-1]...), []byte(`,"email_code_hash_key":"duplicate"}`)...),
		"case variant": bytes.Replace(raw, []byte(`"email_code_hash_key"`), []byte(`"EMAIL_CODE_HASH_KEY"`), 1),
		"missing":      bytes.Replace(raw, []byte(`"proof_clock_skew_seconds":30,`), nil, 1),
		"null numeric": bytes.Replace(raw, []byte(`"proof_clock_skew_seconds":30`), []byte(`"proof_clock_skew_seconds":null`), 1),
		"unknown":      append(append([]byte{}, raw[:len(raw)-1]...), []byte(`,"unexpected":true}`)...),
		"trailing":     append(append([]byte{}, raw...), []byte(`{}`)...),
	}
	for name, candidate := range mutations {
		t.Run(name, func(t *testing.T) {
			if bytes.Equal(candidate, raw) {
				t.Fatal("invalid fixture mutation did not apply")
			}
			t.Setenv(apiSecretsFileEnv, testAPIConfigFile(t, candidate, 0o600))
			if _, err := loadAPISettings(); err == nil {
				t.Fatal("ambiguous JSON was accepted")
			}
		})
	}
	cases := map[string]func(*apiSecretFile){
		"short key":                   func(s *apiSecretFile) { s.CodeHash = "c2hvcnQ" },
		"padded key":                  func(s *apiSecretFile) { s.CodeHash += "=" },
		"zero key":                    func(s *apiSecretFile) { s.CodeHash = base64.RawURLEncoding.EncodeToString(make([]byte, 32)) },
		"reused key":                  func(s *apiSecretFile) { s.CodeHash = s.SessionToken },
		"reused proxy key":            func(s *apiSecretFile) { s.ProxyCredential = s.SessionToken },
		"zero proxy key":              func(s *apiSecretFile) { s.ProxyCredential = base64.RawURLEncoding.EncodeToString(make([]byte, 32)) },
		"invalid key id":              func(s *apiSecretFile) { s.CodeHashID = "bad/id" },
		"public proxy":                func(s *apiSecretFile) { s.TrustedProxies = []string{"0.0.0.0/0"} },
		"noncanonical proxy":          func(s *apiSecretFile) { s.TrustedProxies = []string{"127.0.0.2/24"} },
		"duplicate proxy":             func(s *apiSecretFile) { s.TrustedProxies = []string{"127.0.0.1/32", "127.0.0.1/32"} },
		"overflow duration":           func(s *apiSecretFile) { s.SessionLifetime = math.MaxInt64 },
		"invalid challenge attempts":  func(s *apiSecretFile) { s.ChallengeAttempts = 0 },
		"invalid proof lifetime":      func(s *apiSecretFile) { s.ProofLifetime = 1801 },
		"invalid rate":                func(s *apiSecretFile) { s.LoginIP = 0 },
		"missing approval rate":       func(s *apiSecretFile) { s.ApprovalSubject = 0 },
		"weak approval attempt limit": func(s *apiSecretFile) { s.ApprovalAttempt = 11 },
		"approval window too short":   func(s *apiSecretFile) { s.ApprovalWindow = s.ApprovalLifetime - 1 },
		"invalid digits":              func(s *apiSecretFile) { s.CodeDigits = 5 },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			candidate := fixture
			mutate(&candidate)
			if _, err := candidate.settings(); err == nil {
				t.Fatal("invalid settings accepted")
			}
		})
	}
}
