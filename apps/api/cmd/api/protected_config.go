package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/netip"
	"os"
	"path/filepath"
	"reflect"
	"syscall"
	"time"
	"unicode/utf8"

	"nerae/api/internal/modules/auth/approval"
	"nerae/api/internal/modules/auth/challenge"
	"nerae/api/internal/modules/auth/domain"
	"nerae/api/internal/modules/auth/ratelimit"
	"nerae/api/internal/modules/auth/trustedproxy"
)

const apiSecretsFileEnv = "NERAE_API_SECRETS_FILE"
const maxAPISecretsSize = 64 * 1024

var errAPIConfig = errors.New("API business configuration unavailable")

// All fields are required. Keys are independent CSPRNG-generated 32-byte
// values encoded as canonical unpadded base64url, never user input or defaults.
type apiSecretFile struct {
	ProxyCredential    string   `json:"proxy_credential"`
	IdentityIndex      string   `json:"email_identity_index_key"`
	IdentityCipher     string   `json:"email_identity_encryption_key"`
	IdentityID         string   `json:"email_identity_key_id"`
	CodeHash           string   `json:"email_code_hash_key"`
	CodeHashID         string   `json:"email_code_hash_key_id"`
	CodeDerive         string   `json:"email_code_derivation_key"`
	CodeID             string   `json:"email_code_key_id"`
	ProofReplay        string   `json:"proof_replay_key"`
	SessionToken       string   `json:"session_token_key"`
	Approval           string   `json:"approval_key"`
	ApprovalID         string   `json:"approval_key_id"`
	ChallengeIdem      string   `json:"challenge_idempotency_key"`
	ChallengeRequest   string   `json:"challenge_request_key"`
	ChallengeCipher    string   `json:"challenge_response_key"`
	ChallengeID        string   `json:"challenge_key_id"`
	LoginIdem          string   `json:"login_idempotency_key"`
	LoginRequest       string   `json:"login_request_key"`
	LoginCipher        string   `json:"login_response_key"`
	LoginID            string   `json:"login_key_id"`
	ExchangeIdem       string   `json:"approval_exchange_idempotency_key"`
	ExchangeRequest    string   `json:"approval_exchange_request_key"`
	ExchangeCipher     string   `json:"approval_exchange_response_key"`
	ExchangeID         string   `json:"approval_exchange_key_id"`
	RateEmail          string   `json:"rate_email_key"`
	RateDevice         string   `json:"rate_device_key"`
	RateIP             string   `json:"rate_ip_key"`
	RateGlobal         string   `json:"rate_global_key"`
	WorkoutName        string   `json:"workout_name_key"`
	WorkoutNameID      string   `json:"workout_name_key_id"`
	WorkoutHash        string   `json:"workout_idempotency_key"`
	WorkoutCipher      string   `json:"workout_response_key"`
	WorkoutID          string   `json:"workout_key_id"`
	TrustedProxies     []string `json:"trusted_proxy_prefixes"`
	ChallengeTTL       int64    `json:"challenge_ttl_seconds"`
	ChallengeAttempts  int      `json:"challenge_max_attempts"`
	ProofLifetime      int64    `json:"proof_lifetime_seconds"`
	ProofSkew          int64    `json:"proof_clock_skew_seconds"`
	ChallengeRetention int64    `json:"challenge_receipt_seconds"`
	LoginRetention     int64    `json:"login_receipt_seconds"`
	ExchangeRetention  int64    `json:"approval_exchange_receipt_seconds"`
	SessionLifetime    int64    `json:"session_lifetime_seconds"`
	WorkoutRetention   int64    `json:"workout_receipt_seconds"`
	ApprovalLifetime   int64    `json:"approval_lifetime_seconds"`
	ReauthWindow       int64    `json:"approval_reauth_seconds"`
	CodeDigits         int      `json:"email_code_digits"`
	IPv6PrefixBits     int      `json:"rate_ipv6_prefix_bits"`
	ChallengeWindow    int64    `json:"rate_challenge_window_seconds"`
	ChallengeEmail     uint64   `json:"rate_challenge_email_limit"`
	ChallengeDevice    uint64   `json:"rate_challenge_device_limit"`
	ChallengeIP        uint64   `json:"rate_challenge_ip_limit"`
	ChallengeGlobal    uint64   `json:"rate_challenge_global_limit"`
	LoginWindow        int64    `json:"rate_login_window_seconds"`
	LoginEmail         uint64   `json:"rate_login_email_limit"`
	LoginDevice        uint64   `json:"rate_login_device_limit"`
	LoginIP            uint64   `json:"rate_login_ip_limit"`
	LoginGlobal        uint64   `json:"rate_login_global_limit"`
	ApprovalWindow     int64    `json:"rate_approval_window_seconds"`
	ApprovalSubject    uint64   `json:"rate_approval_subject_limit"`
	ApprovalAttempt    uint64   `json:"rate_approval_attempt_limit"`
	ApprovalIP         uint64   `json:"rate_approval_ip_limit"`
	ApprovalGlobal     uint64   `json:"rate_approval_global_limit"`
	ExchangeWindow     int64    `json:"rate_approval_exchange_window_seconds"`
	ExchangeCapability uint64   `json:"rate_approval_exchange_capability_limit"`
	ExchangeIP         uint64   `json:"rate_approval_exchange_ip_limit"`
	ExchangeGlobal     uint64   `json:"rate_approval_exchange_global_limit"`
}

func (apiSecretFile) String() string               { return "APISecrets{redacted}" }
func (apiSecretFile) GoString() string             { return "APISecrets{redacted}" }
func (apiSecretFile) MarshalJSON() ([]byte, error) { return nil, errAPIConfig }
func (apiSecretFile) LogValue() slog.Value         { return slog.StringValue("APISecrets{redacted}") }

type apiBusinessSettings struct {
	keys            businessKeys
	policy          businessPolicy
	proxies         []netip.Prefix
	proxyCredential []byte
}

func (apiBusinessSettings) String() string               { return "APIBusinessSettings{redacted}" }
func (apiBusinessSettings) GoString() string             { return "APIBusinessSettings{redacted}" }
func (apiBusinessSettings) MarshalJSON() ([]byte, error) { return nil, errAPIConfig }
func (apiBusinessSettings) LogValue() slog.Value {
	return slog.StringValue("APIBusinessSettings{redacted}")
}

// loadAPISettings follows no final path symlink, opens nonblocking, checks the
// opened file rather than the name, and never returns a path or content in an
// error. Deployments must also protect the parent directory and file creation.
func loadAPISettings() (apiBusinessSettings, error) {
	path := os.Getenv(apiSecretsFileEnv)
	if !filepath.IsAbs(path) {
		return apiBusinessSettings{}, errAPIConfig
	}
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC|syscall.O_NONBLOCK, 0)
	if err != nil {
		return apiBusinessSettings{}, errAPIConfig
	}
	file := os.NewFile(uintptr(fd), "api-secrets")
	if file == nil {
		_ = syscall.Close(fd)
		return apiBusinessSettings{}, errAPIConfig
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 || info.Mode().Perm()&0o400 == 0 || info.Size() < 2 || info.Size() > maxAPISecretsSize {
		return apiBusinessSettings{}, errAPIConfig
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || uint32(stat.Uid) != uint32(os.Geteuid()) {
		return apiBusinessSettings{}, errAPIConfig
	}
	raw, err := io.ReadAll(io.LimitReader(file, maxAPISecretsSize+1))
	if err != nil || len(raw) > maxAPISecretsSize || !utf8.Valid(raw) || !apiJSONFieldsAreCanonical(raw) {
		return apiBusinessSettings{}, errAPIConfig
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var fileConfig apiSecretFile
	if err := decoder.Decode(&fileConfig); err != nil {
		return apiBusinessSettings{}, errAPIConfig
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return apiBusinessSettings{}, errAPIConfig
	}
	return fileConfig.settings()
}

func apiJSONFieldsAreCanonical(raw []byte) bool {
	typ := reflect.TypeOf(apiSecretFile{})
	allowed := make(map[string]struct{}, typ.NumField())
	for i := 0; i < typ.NumField(); i++ {
		allowed[typ.Field(i).Tag.Get("json")] = struct{}{}
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return false
	}
	seen := make(map[string]struct{}, len(allowed))
	for decoder.More() {
		token, err = decoder.Token()
		name, ok := token.(string)
		if err != nil || !ok {
			return false
		}
		if _, ok := allowed[name]; !ok {
			return false
		}
		if _, ok := seen[name]; ok {
			return false
		}
		seen[name] = struct{}{}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return false
		}
		if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return false
		}
	}
	token, err = decoder.Token()
	if err != nil || token != json.Delim('}') || len(seen) != len(allowed) {
		return false
	}
	var extra any
	return errors.Is(decoder.Decode(&extra), io.EOF)
}

func (s apiSecretFile) settings() (apiBusinessSettings, error) {
	keys := businessKeys{identityID: s.IdentityID, codeID: s.CodeID, codeHashID: s.CodeHashID,
		approvalID: s.ApprovalID, challengeID: s.ChallengeID, loginID: s.LoginID, exchangeID: s.ExchangeID,
		workoutNameID: s.WorkoutNameID, workoutID: s.WorkoutID}
	var proxyCredential []byte
	fields := []struct {
		encoded string
		target  *[]byte
	}{
		{s.IdentityIndex, &keys.identityIndex}, {s.IdentityCipher, &keys.identityCipher}, {s.CodeHash, &keys.codeHash}, {s.CodeDerive, &keys.codeDerive},
		{s.ProofReplay, &keys.proofReplay}, {s.SessionToken, &keys.sessionToken}, {s.Approval, &keys.approval},
		{s.ChallengeIdem, &keys.challengeIdempotency}, {s.ChallengeRequest, &keys.challengeRequest}, {s.ChallengeCipher, &keys.challengeCipher},
		{s.LoginIdem, &keys.loginIdempotency}, {s.LoginRequest, &keys.loginRequest}, {s.LoginCipher, &keys.loginCipher},
		{s.ExchangeIdem, &keys.exchangeIdempotency}, {s.ExchangeRequest, &keys.exchangeRequest}, {s.ExchangeCipher, &keys.exchangeCipher},
		{s.RateEmail, &keys.rateEmail}, {s.RateDevice, &keys.rateDevice}, {s.RateIP, &keys.rateIP}, {s.RateGlobal, &keys.rateGlobal},
		{s.WorkoutName, &keys.workoutName}, {s.WorkoutHash, &keys.workoutHash}, {s.WorkoutCipher, &keys.workoutCipher},
		{s.ProxyCredential, &proxyCredential},
	}
	seen := make(map[string]struct{}, len(fields))
	for _, field := range fields {
		key, err := base64.RawURLEncoding.Strict().DecodeString(field.encoded)
		if err != nil || len(key) != 32 || base64.RawURLEncoding.EncodeToString(key) != field.encoded || bytes.Equal(key, make([]byte, 32)) {
			return apiBusinessSettings{}, errAPIConfig
		}
		if _, exists := seen[field.encoded]; exists {
			return apiBusinessSettings{}, errAPIConfig
		}
		seen[field.encoded] = struct{}{}
		*field.target = key
	}
	if !keys.distinctKeys() {
		return apiBusinessSettings{}, errAPIConfig
	}
	for _, id := range []string{keys.identityID, keys.codeID, keys.codeHashID, keys.approvalID, keys.challengeID, keys.loginID, keys.exchangeID, keys.workoutNameID, keys.workoutID} {
		if !apiValidKeyID(id) {
			return apiBusinessSettings{}, errAPIConfig
		}
	}
	if !within(s.ChallengeTTL, 1, 1800) || s.ChallengeAttempts < 1 || s.ChallengeAttempts > 10 ||
		!within(s.ProofLifetime, 1, 1800) || !within(s.ProofSkew, 0, 300) ||
		!within(s.ChallengeRetention, 60, 86400) || !within(s.LoginRetention, 1, 86400) || !within(s.ExchangeRetention, 1, 86400) ||
		!within(s.SessionLifetime, 1, 30*86400) || !within(s.WorkoutRetention, 1, 30*86400) ||
		!within(s.ApprovalLifetime, 1, 1800) || !within(s.ReauthWindow, 1, 86400) ||
		s.CodeDigits < 6 || s.CodeDigits > 10 || s.IPv6PrefixBits < 16 || s.IPv6PrefixBits > 128 ||
		!within(s.ChallengeWindow, 1, 86400) || !within(s.LoginWindow, 1, 86400) ||
		!within(s.ApprovalWindow, 1, 86400) || !within(s.ExchangeWindow, 1, 86400) || s.ApprovalWindow < s.ApprovalLifetime || s.ApprovalAttempt < 1 || s.ApprovalAttempt > 10 {
		return apiBusinessSettings{}, errAPIConfig
	}
	limits := []uint64{s.ChallengeEmail, s.ChallengeDevice, s.ChallengeIP, s.ChallengeGlobal, s.LoginEmail, s.LoginDevice, s.LoginIP, s.LoginGlobal,
		s.ApprovalSubject, s.ApprovalAttempt, s.ApprovalIP, s.ApprovalGlobal, s.ExchangeCapability, s.ExchangeIP, s.ExchangeGlobal}
	for _, limit := range limits {
		if limit < 1 || limit > 1000000 {
			return apiBusinessSettings{}, errAPIConfig
		}
	}
	seconds := func(n int64) time.Duration { return time.Duration(n) * time.Second }
	proof := domain.ProofPolicy{MaxLifetime: seconds(s.ProofLifetime), ClockSkew: seconds(s.ProofSkew)}
	if !domain.ValidProofPolicy(proof) {
		return apiBusinessSettings{}, errAPIConfig
	}
	policyFor := func(window int64, email, device, ip, global uint64) ratelimit.Policy {
		dim := func(limit uint64) ratelimit.DimensionPolicy {
			return ratelimit.DimensionPolicy{Limit: limit, Window: seconds(window)}
		}
		return ratelimit.Policy{Email: dim(email), Device: dim(device), IP: dim(ip), Global: dim(global)}
	}
	approvalRate := policyFor(s.ApprovalWindow, s.ApprovalSubject, s.ApprovalAttempt, s.ApprovalIP, s.ApprovalGlobal)
	exchangeRate := policyFor(s.ExchangeWindow, s.ExchangeCapability, s.ExchangeCapability, s.ExchangeIP, s.ExchangeGlobal)
	policy := businessPolicy{challenge: challenge.Policy{ChallengeTTL: seconds(s.ChallengeTTL), MaxAttempts: s.ChallengeAttempts, DeviceProof: proof},
		rate: ratelimit.Config{IPv6PrefixBits: s.IPv6PrefixBits,
			EmailChallenge: policyFor(s.ChallengeWindow, s.ChallengeEmail, s.ChallengeDevice, s.ChallengeIP, s.ChallengeGlobal),
			EmailLogin:     policyFor(s.LoginWindow, s.LoginEmail, s.LoginDevice, s.LoginIP, s.LoginGlobal)},
		approvalRate:       ratelimit.ApprovalDecisionPolicy{Subject: approvalRate.Email, Approval: approvalRate.Device, IP: approvalRate.IP, Global: approvalRate.Global},
		exchangeRate:       ratelimit.ApprovalExchangePolicy{Capability: exchangeRate.Device, IP: exchangeRate.IP, Global: exchangeRate.Global},
		challengeRetention: seconds(s.ChallengeRetention), loginRetention: seconds(s.LoginRetention),
		exchangeRetention: seconds(s.ExchangeRetention),
		sessionLifetime:   seconds(s.SessionLifetime), workoutRetention: seconds(s.WorkoutRetention),
		approval: approval.Policy{Lifetime: seconds(s.ApprovalLifetime), ReauthWindow: seconds(s.ReauthWindow)}, codeDigits: s.CodeDigits}
	if len(s.TrustedProxies) < 1 || len(s.TrustedProxies) > 16 {
		return apiBusinessSettings{}, errAPIConfig
	}
	prefixes := make([]netip.Prefix, 0, len(s.TrustedProxies))
	for _, raw := range s.TrustedProxies {
		prefix, err := netip.ParsePrefix(raw)
		if err != nil || prefix.String() != raw {
			return apiBusinessSettings{}, errAPIConfig
		}
		prefixes = append(prefixes, prefix)
	}
	if _, err := trustedproxy.NewResolver(prefixes); err != nil {
		return apiBusinessSettings{}, errAPIConfig
	}
	return apiBusinessSettings{keys: keys, policy: policy, proxies: prefixes, proxyCredential: proxyCredential}, nil
}

func within(value, min, max int64) bool { return value >= min && value <= max }

func apiValidKeyID(value string) bool {
	if len(value) < 1 || len(value) > 64 {
		return false
	}
	for _, r := range value {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.') {
			return false
		}
	}
	return true
}
