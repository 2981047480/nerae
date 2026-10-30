package issuance

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"database/sql"
	"database/sql/driver"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/netip"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/redis/go-redis/v9"
	"nerae/api/internal/modules/auth/bootstrap"
	"nerae/api/internal/modules/auth/challenge"
	"nerae/api/internal/modules/auth/domain"
	"nerae/api/internal/modules/auth/ratelimit"
	"nerae/api/internal/modules/auth/repository"
)

func TestIdempotencyKeyRequiresCanonicalBase64URLAnd128Bits(t *testing.T) {
	valid := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{0x41}, 16))
	if decoded, err := parseIdempotencyKey(valid); err != nil || len(decoded) != 16 {
		t.Fatal("canonical 128-bit key was rejected")
	}
	maximum := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{0x42}, 96))
	if decoded, err := parseIdempotencyKey(maximum); err != nil || len(decoded) != 96 || len(maximum) != 128 {
		t.Fatal("contract maximum idempotency key was rejected")
	}
	for _, value := range []string{"", strings.Repeat("a", 21), valid + "=", "++++++++++++++++++++++", strings.Repeat("a", 129)} {
		if _, err := parseIdempotencyKey(value); err == nil {
			t.Fatalf("invalid idempotency key accepted: length=%d", len(value))
		}
	}
}

func TestOptionalDeviceLabelValidation(t *testing.T) {
	if !validDeviceLabel(nil) {
		t.Fatal("omitted optional device label was rejected")
	}
	for _, value := range []string{"", strings.Repeat("a", 81), string([]byte{0xff})} {
		if validDeviceLabel(&value) {
			t.Fatal("invalid optional device label was accepted")
		}
	}
	if !validDeviceLabel(ptrString("设备")) || !validDeviceLabel(ptrString(strings.Repeat("界", 80))) {
		t.Fatal("valid Unicode device label boundaries were rejected")
	}
}

func ptrString(value string) *string { return &value }

func TestChallengeRequestIssuerRejectsReusedOrMissingKeys(t *testing.T) {
	client := redis.NewClient(&redis.Options{Network: "unix", Addr: "/no/redis/socket"})
	defer client.Close()
	limiter, err := ratelimit.New(client, requestLimiterConfig())
	if err != nil {
		t.Fatal("test limiter construction failed")
	}
	identities, _ := domain.NewEmailIdentityProtector(bytes.Repeat([]byte{1}, 32), bytes.Repeat([]byte{2}, 32), "request-test")
	hasher, _ := domain.NewEmailCodeHasher(bytes.Repeat([]byte{3}, 32))
	replay, _ := repository.NewProofReplayStore(bytes.Repeat([]byte{4}, 32))
	challengeStore, _ := challenge.NewStore(hasher, replay, identities, challenge.Policy{ChallengeTTL: 5 * time.Minute, MaxAttempts: 3, DeviceProof: domain.ProofPolicy{MaxLifetime: 5 * time.Minute, ClockSkew: time.Second}})
	deriver, _ := NewDeriver(bytes.Repeat([]byte{5}, 32))
	outbox, _ := NewOutbox(deriver, "code-key", hasher, identities, &recordingSender{}, time.Second)
	valid := requestKeys()
	if _, err := NewChallengeRequestIssuer(challengeStore, outbox, identities, limiter, valid); err != nil {
		t.Fatal("independent keys were rejected")
	}
	invalid := valid
	invalid.RequestHMACKey = append([]byte(nil), invalid.IdempotencyHMACKey...)
	if _, err := NewChallengeRequestIssuer(challengeStore, outbox, identities, limiter, invalid); !errors.Is(err, ErrChallengeRequest) {
		t.Fatal("reused HMAC keys were accepted")
	}
	invalid = valid
	invalid.ResponseAESKey = []byte("short")
	if _, err := NewChallengeRequestIssuer(challengeStore, outbox, identities, limiter, invalid); !errors.Is(err, ErrChallengeRequest) {
		t.Fatal("short encryption key was accepted")
	}
	invalid = valid
	invalid.Retention = 0
	if _, err := NewChallengeRequestIssuer(challengeStore, outbox, identities, limiter, invalid); !errors.Is(err, ErrChallengeRequest) {
		t.Fatal("implicit retention was accepted")
	}
	invalid = valid
	invalid.CodeDigits = 5
	if _, err := NewChallengeRequestIssuer(challengeStore, outbox, identities, limiter, invalid); !errors.Is(err, ErrChallengeRequest) {
		t.Fatal("out-of-contract code length was accepted")
	}
	invalid.CodeDigits = 10
	if _, err := NewChallengeRequestIssuer(challengeStore, outbox, identities, limiter, invalid); err != nil {
		t.Fatal("maximum contract code length was rejected")
	}
}

type requestFixture struct {
	t          *testing.T
	db         *sql.DB
	ctx        context.Context
	redis      *redis.Client
	identities *domain.EmailIdentityProtector
	hasher     *domain.EmailCodeHasher
	challenges *challenge.Store
	outbox     *Outbox
	issuer     *ChallengeRequestIssuer
	limiter    *ratelimit.Limiter
}

func openRequestFixture(t *testing.T, policy ratelimit.Policy) *requestFixture {
	t.Helper()
	dsn := os.Getenv("NERAE_EMAIL_CHALLENGE_REQUEST_TEST_DSN")
	socket := os.Getenv("NERAE_EMAIL_CHALLENGE_REQUEST_REDIS_SOCKET")
	if dsn == "" || socket == "" {
		t.Skip("private socket-only MySQL and Redis are required")
	}
	cfg, err := mysql.ParseDSN(dsn)
	if err != nil || cfg.Net != "unix" || cfg.Addr == "" {
		t.Fatal("MySQL test DSN must use a Unix socket")
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatal("private test database could not open")
	}
	db.SetMaxOpenConns(16)
	db.SetMaxIdleConns(16)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	if err := db.PingContext(ctx); err != nil {
		cancel()
		db.Close()
		t.Fatal("private MySQL unavailable")
	}
	rdb := redis.NewClient(&redis.Options{Network: "unix", Addr: socket, DialTimeout: time.Second, ReadTimeout: 2 * time.Second, WriteTimeout: 2 * time.Second})
	if err := rdb.Ping(ctx).Err(); err != nil {
		cancel()
		db.Close()
		rdb.Close()
		t.Fatal("private Redis unavailable")
	}
	t.Cleanup(func() { cancel(); _ = rdb.Close(); _ = db.Close() })
	identities, err := domain.NewEmailIdentityProtector(bytes.Repeat([]byte{0x31}, 32), bytes.Repeat([]byte{0x32}, 32), "a01-16-email-v1")
	if err != nil {
		t.Fatal("identity protector setup failed")
	}
	hasher, err := domain.NewEmailCodeHasher(bytes.Repeat([]byte{0x33}, 32))
	if err != nil {
		t.Fatal("code hasher setup failed")
	}
	replay, err := repository.NewProofReplayStore(bytes.Repeat([]byte{0x34}, 32))
	if err != nil {
		t.Fatal("proof replay setup failed")
	}
	challenges, err := challenge.NewStore(hasher, replay, identities, challenge.Policy{ChallengeTTL: 5 * time.Minute, MaxAttempts: 3, DeviceProof: domain.ProofPolicy{MaxLifetime: 5 * time.Minute, ClockSkew: 30 * time.Second}})
	if err != nil {
		t.Fatal("challenge store setup failed")
	}
	deriver, err := NewDeriver(bytes.Repeat([]byte{0x35}, 32))
	if err != nil {
		t.Fatal("code deriver setup failed")
	}
	outbox, err := NewOutbox(deriver, "a01-16-code-v1", hasher, identities, &recordingSender{}, 2*time.Second)
	if err != nil {
		t.Fatal("outbox setup failed")
	}
	limitConfig := requestLimiterConfig()
	for _, key := range [][]byte{limitConfig.Keys.EmailIndex, limitConfig.Keys.DeviceFingerprint, limitConfig.Keys.TrustedIP, limitConfig.Keys.Global} {
		if _, err := rand.Read(key); err != nil {
			t.Fatal("test limiter key generation failed")
		}
	}
	limitConfig.EmailChallenge = policy
	limiter, err := ratelimit.New(rdb, limitConfig)
	if err != nil {
		t.Fatal("rate limiter setup failed")
	}
	issuer, err := NewChallengeRequestIssuer(challenges, outbox, identities, limiter, requestKeys())
	if err != nil {
		t.Fatal("request issuer setup failed")
	}
	bootstrapper, err := bootstrap.NewIdentityEpochInitializer(identities)
	if err != nil {
		t.Fatal("identity epoch initializer setup failed")
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal("bootstrap transaction failed")
	}
	if err := bootstrapper.Initialize(ctx, tx); err != nil {
		_ = tx.Rollback()
		t.Fatal("identity epoch bootstrap failed")
	}
	if err := outbox.InitializeCodeKey(ctx, tx); err != nil {
		_ = tx.Rollback()
		t.Fatal("code epoch bootstrap failed")
	}
	if err := issuer.InitializeKey(ctx, tx); err != nil {
		_ = tx.Rollback()
		t.Fatal("request key bootstrap failed")
	}
	if err := tx.Commit(); err != nil {
		t.Fatal("bootstrap commit failed")
	}
	return &requestFixture{t: t, db: db, ctx: ctx, redis: rdb, identities: identities, hasher: hasher, challenges: challenges, outbox: outbox, issuer: issuer, limiter: limiter}
}

func requestKeys() RequestKeyConfig {
	return RequestKeyConfig{IdempotencyHMACKey: bytes.Repeat([]byte{0x41}, 32), RequestHMACKey: bytes.Repeat([]byte{0x42}, 32),
		ResponseAESKey: bytes.Repeat([]byte{0x43}, 32), KeyID: "a01-16-request-v1", Retention: time.Hour, CodeDigits: 6}
}

func requestLimiterConfig() ratelimit.Config {
	p := ratelimit.Policy{Email: ratelimit.DimensionPolicy{Limit: 100, Window: time.Hour}, Device: ratelimit.DimensionPolicy{Limit: 100, Window: time.Hour},
		IP: ratelimit.DimensionPolicy{Limit: 100, Window: time.Hour}, Global: ratelimit.DimensionPolicy{Limit: 10000, Window: time.Hour}}
	return ratelimit.Config{Keys: ratelimit.HMACKeys{EmailIndex: bytes.Repeat([]byte{0x51}, 32), DeviceFingerprint: bytes.Repeat([]byte{0x52}, 32),
		TrustedIP: bytes.Repeat([]byte{0x53}, 32), Global: bytes.Repeat([]byte{0x54}, 32)}, IPv6PrefixBits: 64, EmailChallenge: p, EmailLogin: p}
}

func (f *requestFixture) key() string {
	f.t.Helper()
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		f.t.Fatal("test idempotency key generation failed")
	}
	return base64.RawURLEncoding.EncodeToString(buf)
}

func (f *requestFixture) input(email string, key string, publicKey domain.DevicePublicJWK) ChallengeRequestInput {
	return ChallengeRequestInput{Email: email, DevicePublicKey: publicKey, IdempotencyKey: key, TrustedSourceIP: netip.MustParseAddr("198.51.100.18")}
}

func testPublicJWK(t *testing.T) domain.DevicePublicJWK {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal("synthetic device key generation failed")
	}
	enc := base64.RawURLEncoding.EncodeToString
	x, y := key.PublicKey.X.FillBytes(make([]byte, 32)), key.PublicKey.Y.FillBytes(make([]byte, 32))
	return domain.DevicePublicJWK{Kty: "EC", Crv: "P-256", X: enc(x), Y: enc(y)}
}

func decodeChallenge(t *testing.T, result ChallengeRequestResult) (string, string, string) {
	t.Helper()
	body := result.WireBody()
	var response struct {
		ChallengeID string `json:"challengeId"`
		Nonce       string `json:"nonce"`
		ExpiresAt   string `json:"expiresAt"`
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&response); err != nil || response.ChallengeID == "" || !validNonce(response.Nonce) || response.ExpiresAt == "" {
		t.Fatal("issue result did not match the exact challenge response shape")
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		t.Fatal("challenge response included trailing data")
	}
	return response.ChallengeID, response.Nonce, response.ExpiresAt
}

func TestChallengeRequestConcurrentReplayDifferentKeysAndEmailIsolation(t *testing.T) {
	f := openRequestFixture(t, requestLimiterConfig().EmailChallenge)
	key := f.key()
	input := f.input("a01-16-concurrent@example.test", key, testPublicJWK(t))
	const workers = 2
	results := make([]ChallengeRequestResult, workers)
	errs := make([]error, workers)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for n := 0; n < workers; n++ {
		wg.Add(1)
		go func(n int) { defer wg.Done(); <-start; results[n], errs[n] = f.issuer.Issue(f.ctx, f.db, input) }(n)
	}
	close(start)
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			t.Fatalf("same-key concurrent issue failed: %v", err)
		}
	}
	if !bytes.Equal(results[0].WireBody(), results[1].WireBody()) {
		t.Fatal("same idempotency key did not replay the exact response")
	}
	challengeID, _, _ := decodeChallenge(t, results[0])
	assertRequestCounts(t, f, input.Email, 1)
	if calls, _, _ := f.outbox.sender.(*recordingSender).snapshot(); calls != 0 {
		t.Fatal("challenge request attempted to send mail in the database path")
	}
	var active []byte
	if err := f.db.QueryRowContext(f.ctx, `SELECT challenge_id FROM auth_email_login_slot WHERE email_index=(SELECT email_index FROM auth_email_challenge_request WHERE challenge_id=?)`, []byte(challengeID)).Scan(&active); err != nil || string(active) != challengeID {
		t.Fatal("replay changed the active email slot")
	}
	changedKey := f.input(input.Email, key, testPublicJWK(t))
	if _, err := f.issuer.Issue(f.ctx, f.db, changedKey); !errors.Is(err, ErrChallengeRequestConflict) {
		t.Fatal("same key with a different public key did not conflict")
	}
	changedLabel := f.input(input.Email, key, input.DevicePublicKey)
	label := "Studio One"
	changedLabel.DeviceLabel = &label
	if _, err := f.issuer.Issue(f.ctx, f.db, changedLabel); !errors.Is(err, ErrChallengeRequestConflict) {
		t.Fatal("same key with an added optional label did not conflict")
	}
	otherLabel := "Studio Two"
	changedLabel.DeviceLabel = &otherLabel
	if _, err := f.issuer.Issue(f.ctx, f.db, changedLabel); !errors.Is(err, ErrChallengeRequestConflict) {
		t.Fatal("same key with another optional label value did not conflict")
	}
	changedEmailSpelling := f.input("a01-16-concurrent@EXAMPLE.TEST", key, input.DevicePublicKey)
	if _, err := f.issuer.Issue(f.ctx, f.db, changedEmailSpelling); !errors.Is(err, ErrChallengeRequestConflict) {
		t.Fatal("same key with another accepted email spelling did not conflict")
	}
	assertRequestCounts(t, f, input.Email, 1)

	otherEmail := "a01-16-other@example.test"
	other, err := f.issuer.Issue(f.ctx, f.db, f.input(otherEmail, key, input.DevicePublicKey))
	if err != nil {
		t.Fatalf("same key under another email did not isolate: %v", err)
	}
	otherID, _, _ := decodeChallenge(t, other)
	if otherID == challengeID {
		t.Fatal("distinct email reused a challenge ID")
	}
	assertRequestCounts(t, f, otherEmail, 1)

	nextKey := f.key()
	next, err := f.issuer.Issue(f.ctx, f.db, f.input(input.Email, nextKey, input.DevicePublicKey))
	if err != nil {
		t.Fatalf("new same-email key did not issue a new challenge: %v", err)
	}
	nextID, _, _ := decodeChallenge(t, next)
	if nextID == challengeID {
		t.Fatal("new key did not advance challenge")
	}
	assertRequestCounts(t, f, input.Email, 2)
	if _, err := f.issuer.Issue(f.ctx, f.db, input); !errors.Is(err, ErrChallengeRequestConflict) {
		t.Fatal("superseded challenge response was replayed")
	}

	// Distinct keys for the same email may both create requests, but the slot
	// lock serializes them and only the final active challenge may replay.
	parallelEmail := "a01-16-different-keys@example.test"
	parallelInputs := []ChallengeRequestInput{
		f.input(parallelEmail, f.key(), testPublicJWK(t)),
		f.input(parallelEmail, f.key(), testPublicJWK(t)),
	}
	parallelResults := make([]ChallengeRequestResult, 2)
	parallelErrors := make([]error, 2)
	parallelStart := make(chan struct{})
	for n := range parallelInputs {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			<-parallelStart
			parallelResults[n], parallelErrors[n] = f.issuer.Issue(f.ctx, f.db, parallelInputs[n])
		}(n)
	}
	close(parallelStart)
	wg.Wait()
	for _, err := range parallelErrors {
		if err != nil {
			t.Fatalf("concurrent distinct-key request failed: %v", err)
		}
	}
	parallelIDs := make([]string, 2)
	for n := range parallelResults {
		parallelIDs[n], _, _ = decodeChallenge(t, parallelResults[n])
	}
	if parallelIDs[0] == parallelIDs[1] {
		t.Fatal("distinct same-email keys shared one challenge")
	}
	assertRequestCounts(t, f, parallelEmail, 2)
	var parallelActive []byte
	parallelIndex, _ := f.identities.Index(parallelEmail)
	if err := f.db.QueryRowContext(f.ctx, `SELECT challenge_id FROM auth_email_login_slot WHERE email_index=?`, parallelIndex[:]).Scan(&parallelActive); err != nil {
		t.Fatal("concurrent email slot read failed")
	}
	activeIndex := 0
	if string(parallelActive) == parallelIDs[1] {
		activeIndex = 1
	} else if string(parallelActive) != parallelIDs[0] {
		t.Fatal("slot did not point to either committed challenge")
	}
	if replayed, err := f.issuer.Issue(f.ctx, f.db, parallelInputs[activeIndex]); err != nil || !bytes.Equal(replayed.WireBody(), parallelResults[activeIndex].WireBody()) {
		t.Fatal("active concurrent request did not replay")
	}
	inactiveIndex := 1 - activeIndex
	if replayed, err := f.issuer.Issue(f.ctx, f.db, parallelInputs[inactiveIndex]); !errors.Is(err, ErrChallengeRequestConflict) || len(replayed.WireBody()) != 0 {
		t.Fatal("superseded concurrent request returned a response")
	}
}

func TestChallengeRequestRejectsInactiveChallengeStatesAndCipherTamper(t *testing.T) {
	f := openRequestFixture(t, requestLimiterConfig().EmailChallenge)
	for _, tc := range []struct {
		name   string
		update string
		args   []any
	}{
		{"consumed", `UPDATE auth_email_login_challenge SET consumed_at=UTC_TIMESTAMP(6) WHERE challenge_id=?`, nil},
		{"expired", `UPDATE auth_email_login_challenge SET created_at=UTC_TIMESTAMP(6)-INTERVAL 2 SECOND,expires_at=UTC_TIMESTAMP(6)-INTERVAL 1 SECOND WHERE challenge_id=?`, nil},
		{"attempts exhausted", `UPDATE auth_email_login_challenge SET attempts=max_attempts WHERE challenge_id=?`, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input := f.input("a01-16-"+strings.ReplaceAll(tc.name, " ", "-")+"@example.test", f.key(), testPublicJWK(t))
			result, err := f.issuer.Issue(f.ctx, f.db, input)
			if err != nil {
				t.Fatal("fixture request failed")
			}
			id, _, _ := decodeChallenge(t, result)
			if _, err := f.db.ExecContext(f.ctx, tc.update, []byte(id)); err != nil {
				t.Fatal("challenge state setup failed")
			}
			if _, err := f.issuer.Issue(f.ctx, f.db, input); !errors.Is(err, ErrChallengeRequestConflict) {
				t.Fatal("inactive challenge response was replayed")
			}
		})
	}
	t.Run("missing outbox row", func(t *testing.T) {
		input := f.input("a01-16-missing-outbox@example.test", f.key(), testPublicJWK(t))
		result, err := f.issuer.Issue(f.ctx, f.db, input)
		if err != nil {
			t.Fatal("fixture request failed")
		}
		id, _, _ := decodeChallenge(t, result)
		if _, err := f.db.ExecContext(f.ctx, `DELETE FROM auth_email_outbox WHERE challenge_id=?`, []byte(id)); err != nil {
			t.Fatal("outbox row removal setup failed")
		}
		if replay, err := f.issuer.Issue(f.ctx, f.db, input); err == nil || len(replay.WireBody()) != 0 {
			t.Fatal("request replay succeeded without its outbox row")
		}
	})
	t.Run("ciphertext tamper", func(t *testing.T) {
		input := f.input("a01-16-tamper@example.test", f.key(), testPublicJWK(t))
		result, err := f.issuer.Issue(f.ctx, f.db, input)
		if err != nil {
			t.Fatal("fixture request failed")
		}
		id, _, _ := decodeChallenge(t, result)
		var encrypted []byte
		if err := f.db.QueryRowContext(f.ctx, `SELECT response_enc FROM auth_email_challenge_request WHERE challenge_id=?`, []byte(id)).Scan(&encrypted); err != nil {
			t.Fatal("cipher read failed")
		}
		encrypted[len(encrypted)-1] ^= 0x80
		if _, err := f.db.ExecContext(f.ctx, `UPDATE auth_email_challenge_request SET response_enc=? WHERE challenge_id=?`, encrypted, []byte(id)); err != nil {
			t.Fatal("cipher tamper setup failed")
		}
		if replay, err := f.issuer.Issue(f.ctx, f.db, input); err == nil || len(replay.WireBody()) != 0 {
			t.Fatal("tampered response returned a replay body")
		}
	})
}

func TestChallengeRequestRollbackAndSensitiveStorageBoundaries(t *testing.T) {
	f := openRequestFixture(t, requestLimiterConfig().EmailChallenge)
	input := f.input("a01-16-rollback@example.test", f.key(), testPublicJWK(t))
	if _, err := f.db.ExecContext(f.ctx, `CREATE TRIGGER fail_a01_16_request BEFORE INSERT ON auth_email_challenge_request FOR EACH ROW SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='synthetic request insert failure'`); err != nil {
		t.Fatal("synthetic failure trigger setup failed")
	}
	if _, err := f.issuer.Issue(f.ctx, f.db, input); err == nil {
		t.Fatal("request insert failure returned success")
	}
	if _, err := f.db.ExecContext(f.ctx, `DROP TRIGGER fail_a01_16_request`); err != nil {
		t.Fatal("synthetic trigger cleanup failed")
	}
	index, err := f.identities.Index(input.Email)
	if err != nil {
		t.Fatal("test email normalization failed")
	}
	for _, table := range []string{"auth_email_login_slot", "auth_email_login_challenge", "auth_email_outbox", "auth_email_challenge_request"} {
		var count int
		query := `SELECT COUNT(*) FROM ` + table + ` WHERE email_index=?`
		if table == "auth_email_outbox" {
			query = `SELECT COUNT(*) FROM auth_email_outbox WHERE challenge_id IN (SELECT challenge_id FROM auth_email_login_challenge WHERE email_index=?)`
		}
		if err := f.db.QueryRowContext(f.ctx, query, index[:]).Scan(&count); err != nil || count != 0 {
			t.Fatalf("failed request left rows in %s", table)
		}
	}
	forbidden := map[string]bool{"email": true, "email_address": true, "recipient": true, "code": true, "otp": true, "idempotency_key": true, "request_body": true}
	for _, table := range []string{"auth_email_challenge_request", "auth_email_outbox"} {
		rows, err := f.db.QueryContext(f.ctx, `SELECT column_name FROM information_schema.columns WHERE table_schema=DATABASE() AND table_name=?`, table)
		if err != nil {
			t.Fatal("schema enumeration failed")
		}
		for rows.Next() {
			var name string
			if err := rows.Scan(&name); err != nil {
				rows.Close()
				t.Fatal("schema scan failed")
			}
			if forbidden[name] {
				rows.Close()
				t.Fatalf("sensitive plaintext column exists in %s", table)
			}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			t.Fatal("schema enumeration failed")
		}
		if err := rows.Close(); err != nil {
			t.Fatal("schema rows close failed")
		}
	}
	fresh := f.input("a01-16-storage@example.test", f.key(), testPublicJWK(t))
	result, err := f.issuer.Issue(f.ctx, f.db, fresh)
	if err != nil {
		t.Fatal("storage fixture issue failed")
	}
	id, nonce, _ := decodeChallenge(t, result)
	keyBytes, _ := parseIdempotencyKey(fresh.IdempotencyKey)
	keyHash := f.issuer.idempotencyHash(keyBytes)
	var encrypted []byte
	if err := f.db.QueryRowContext(f.ctx, `SELECT response_enc FROM auth_email_challenge_request WHERE challenge_id=?`, []byte(id)).Scan(&encrypted); err != nil {
		t.Fatal("stored response read failed")
	}
	if bytes.Contains(encrypted, []byte(fresh.Email)) || bytes.Contains(encrypted, []byte(fresh.IdempotencyKey)) || bytes.Contains(encrypted, []byte(nonce)) {
		t.Fatal("request ciphertext contains a plaintext sentinel")
	}
	var outboxID []byte
	if err := f.db.QueryRowContext(f.ctx, `SELECT challenge_id FROM auth_email_outbox WHERE challenge_id=?`, []byte(id)).Scan(&outboxID); err != nil || string(outboxID) != id {
		t.Fatal("outbox did not retain only challenge reference")
	}
	if keyHash == [32]byte{} {
		t.Fatal("idempotency digest unexpectedly empty")
	}
}

var errSyntheticCommitAckLost = errors.New("synthetic commit acknowledgement loss")

type commitAckLossConnector struct{ inner driver.Connector }

func (c commitAckLossConnector) Connect(ctx context.Context) (driver.Conn, error) {
	conn, err := c.inner.Connect(ctx)
	if err != nil {
		return nil, err
	}
	return commitAckLossConn{Conn: conn}, nil
}
func (c commitAckLossConnector) Driver() driver.Driver { return c.inner.Driver() }

type commitAckLossConn struct{ driver.Conn }

func (c commitAckLossConn) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	beginner, ok := c.Conn.(driver.ConnBeginTx)
	if !ok {
		return nil, errors.New("driver does not support BeginTx")
	}
	tx, err := beginner.BeginTx(ctx, opts)
	if err != nil {
		return nil, err
	}
	return commitAckLossTx{Tx: tx}, nil
}

type commitAckLossTx struct{ driver.Tx }

func (tx commitAckLossTx) Commit() error {
	if err := tx.Tx.Commit(); err != nil {
		return err
	}
	return errSyntheticCommitAckLost
}

func TestChallengeRequestCommitAcknowledgmentLossReturnsUnknownAndCanReplay(t *testing.T) {
	f := openRequestFixture(t, requestLimiterConfig().EmailChallenge)
	dsn := os.Getenv("NERAE_EMAIL_CHALLENGE_REQUEST_TEST_DSN")
	config, err := mysql.ParseDSN(dsn)
	if err != nil {
		t.Fatal("private MySQL DSN parse failed")
	}
	connector, err := mysql.NewConnector(config)
	if err != nil {
		t.Fatal("private MySQL connector setup failed")
	}
	uncertainDB := sql.OpenDB(commitAckLossConnector{inner: connector})
	defer uncertainDB.Close()
	input := f.input("a01-16-commit-ack-loss@example.test", f.key(), testPublicJWK(t))
	lost, err := f.issuer.Issue(f.ctx, uncertainDB, input)
	if !errors.Is(err, ErrChallengeRequestUnknown) || len(lost.WireBody()) != 0 {
		t.Fatal("commit acknowledgement failure did not fail closed with unknown outcome")
	}
	// The injected driver commits on MySQL, then simulates losing the client
	// acknowledgement. This verifies safe retry after a committed-but-unreported
	// result; it is not a real network partition or ambiguous server outcome.
	assertRequestCounts(t, f, input.Email, 1)
	recovered, err := f.issuer.Issue(f.ctx, f.db, input)
	if err != nil {
		t.Fatal("retry did not recover the committed response")
	}
	if _, _, _ = decodeChallenge(t, recovered); len(recovered.WireBody()) == 0 {
		t.Fatal("retry recovered an empty response")
	}
	assertRequestCounts(t, f, input.Email, 1)
}

func TestChallengeRequestRateLimitAndRedisFailureFailClosed(t *testing.T) {
	p := ratelimit.Policy{Email: ratelimit.DimensionPolicy{Limit: 1, Window: time.Hour}, Device: ratelimit.DimensionPolicy{Limit: 10, Window: time.Hour},
		IP: ratelimit.DimensionPolicy{Limit: 10, Window: time.Hour}, Global: ratelimit.DimensionPolicy{Limit: 100, Window: time.Hour}}
	f := openRequestFixture(t, p)
	input := f.input("a01-16-rate@example.test", f.key(), testPublicJWK(t))
	if _, err := f.issuer.Issue(f.ctx, f.db, input); err != nil {
		t.Fatal("first rate-limited request failed")
	}
	if _, err := f.issuer.Issue(f.ctx, f.db, input); !errors.Is(err, ErrChallengeRequestLimited) {
		t.Fatal("replay was not rate-limited")
	}
	other := f.input("a01-16-redis-down@example.test", f.key(), testPublicJWK(t))
	if err := f.redis.Close(); err != nil {
		t.Fatal("private Redis close failed")
	}
	if _, err := f.issuer.Issue(f.ctx, f.db, other); !errors.Is(err, ErrChallengeRequest) {
		t.Fatal("Redis outage did not fail closed")
	}
	assertRequestCounts(t, f, other.Email, 0)
}

func TestChallengeRequestSensitiveFormattingIsRedacted(t *testing.T) {
	input := ChallengeRequestInput{Email: "private-sentinel@example.test", IdempotencyKey: "private-idem-sentinel", TrustedSourceIP: netip.MustParseAddr("198.51.100.8")}
	encoded, err := json.Marshal(input)
	if err == nil || strings.Contains(string(encoded), input.Email) || strings.Contains(fmt.Sprintf("%#v", input), input.IdempotencyKey) {
		t.Fatal("request input JSON or formatting exposed a sensitive field")
	}
	var log bytes.Buffer
	slog.New(slog.NewTextHandler(&log, nil)).Info("request", "input", input)
	if strings.Contains(log.String(), input.Email) || strings.Contains(log.String(), input.IdempotencyKey) || strings.Contains(log.String(), "198.51.100.8") {
		t.Fatal("structured logging exposed request data")
	}
	result := ChallengeRequestResult{body: []byte(`{"nonce":"private-nonce-sentinel"}`)}
	encoded, err = json.Marshal(result)
	if err == nil || strings.Contains(string(encoded), "private-nonce-sentinel") || strings.Contains(fmt.Sprintf("%#v", result), "private-nonce-sentinel") {
		t.Fatal("result JSON or formatting exposed nonce")
	}
}

func assertRequestCounts(t *testing.T, f *requestFixture, email string, expected int) {
	t.Helper()
	index, err := f.identities.Index(email)
	if err != nil {
		t.Fatal("email index failed")
	}
	for _, table := range []string{"auth_email_login_challenge", "auth_email_challenge_request"} {
		var count int
		if err := f.db.QueryRowContext(f.ctx, `SELECT COUNT(*) FROM `+table+` WHERE email_index=?`, index[:]).Scan(&count); err != nil || count != expected {
			t.Fatalf("unexpected %s row count: got %d want %d", table, count, expected)
		}
	}
	var outbox int
	if err := f.db.QueryRowContext(f.ctx, `SELECT COUNT(*) FROM auth_email_outbox WHERE challenge_id IN (SELECT challenge_id FROM auth_email_login_challenge WHERE email_index=?)`, index[:]).Scan(&outbox); err != nil || outbox != expected {
		t.Fatalf("unexpected outbox row count: got %d want %d", outbox, expected)
	}
}

func TestChallengeRequestRateLimitsAllDimensions(t *testing.T) {
	cases := []struct {
		name     string
		setLimit func(*ratelimit.Policy)
		second   func(*requestFixture, ChallengeRequestInput) ChallengeRequestInput
	}{
		{"email", func(p *ratelimit.Policy) { p.Email.Limit = 1 }, func(f *requestFixture, first ChallengeRequestInput) ChallengeRequestInput {
			next := f.input(first.Email, f.key(), testPublicJWK(f.t))
			return next
		}},
		{"device", func(p *ratelimit.Policy) { p.Device.Limit = 1 }, func(f *requestFixture, first ChallengeRequestInput) ChallengeRequestInput {
			return f.input("a01-16-device-second@example.test", f.key(), first.DevicePublicKey)
		}},
		{"ip", func(p *ratelimit.Policy) { p.IP.Limit = 1 }, func(f *requestFixture, first ChallengeRequestInput) ChallengeRequestInput {
			next := f.input("a01-16-ip-second@example.test", f.key(), testPublicJWK(f.t))
			next.TrustedSourceIP = first.TrustedSourceIP
			return next
		}},
		{"global", func(p *ratelimit.Policy) { p.Global.Limit = 1 }, func(f *requestFixture, first ChallengeRequestInput) ChallengeRequestInput {
			next := f.input("a01-16-global-second@example.test", f.key(), testPublicJWK(f.t))
			next.TrustedSourceIP = netip.MustParseAddr("198.51.100.99")
			return next
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := requestLimiterConfig().EmailChallenge
			tc.setLimit(&p)
			f := openRequestFixture(t, p)
			first := f.input("a01-16-"+tc.name+"-first@example.test", f.key(), testPublicJWK(t))
			if _, err := f.issuer.Issue(f.ctx, f.db, first); err != nil {
				t.Fatal("first request failed")
			}
			second := tc.second(f, first)
			if _, err := f.issuer.Issue(f.ctx, f.db, second); !errors.Is(err, ErrChallengeRequestLimited) {
				t.Fatal("configured rate-limit dimension was not enforced")
			}
		})
	}
}

func TestChallengeRequestUsesConfiguredContractCodeLength(t *testing.T) {
	f := openRequestFixture(t, requestLimiterConfig().EmailChallenge)
	keys := requestKeys()
	keys.CodeDigits = 10
	issuer, err := NewChallengeRequestIssuer(f.challenges, f.outbox, f.identities, f.limiter, keys)
	if err != nil {
		t.Fatal("ten-digit server policy was rejected")
	}
	input := f.input("a01-16-ten-digit@example.test", f.key(), testPublicJWK(t))
	first, err := issuer.Issue(f.ctx, f.db, input)
	if err != nil {
		t.Fatal("ten-digit challenge issue failed")
	}
	id, _, _ := decodeChallenge(t, first)
	var digits int
	if err := f.db.QueryRowContext(f.ctx, `SELECT code_digits FROM auth_email_outbox WHERE challenge_id=?`, []byte(id)).Scan(&digits); err != nil || digits != 10 {
		t.Fatal("outbox did not retain the configured code length")
	}
	second, err := issuer.Issue(f.ctx, f.db, input)
	if err != nil || !bytes.Equal(first.WireBody(), second.WireBody()) {
		t.Fatal("configured-length challenge did not replay its exact response")
	}
}

func TestChallengeRequestExpiryCrossedWhileWaitingForEmailSlot(t *testing.T) {
	f := openRequestFixture(t, requestLimiterConfig().EmailChallenge)
	input := f.input("a01-16-lock-wait@example.test", f.key(), testPublicJWK(t))
	original, err := f.issuer.Issue(f.ctx, f.db, input)
	if err != nil {
		t.Fatal("initial request failed")
	}
	oldID, _, _ := decodeChallenge(t, original)
	index, err := f.identities.Index(input.Email)
	if err != nil {
		t.Fatal("email index failed")
	}
	if _, err := f.db.ExecContext(f.ctx, `UPDATE auth_email_challenge_request SET created_at=UTC_TIMESTAMP(6),expires_at=UTC_TIMESTAMP(6)+INTERVAL 1 SECOND WHERE challenge_id=?`, []byte(oldID)); err != nil {
		t.Fatal("request expiry setup failed")
	}
	gate, err := f.db.BeginTx(f.ctx, nil)
	if err != nil {
		t.Fatal("slot lock transaction failed")
	}
	var held []byte
	if err := gate.QueryRowContext(f.ctx, `SELECT challenge_id FROM auth_email_login_slot WHERE email_index=? FOR UPDATE`, index[:]).Scan(&held); err != nil {
		_ = gate.Rollback()
		t.Fatal("email slot lock failed")
	}
	type outcome struct {
		result ChallengeRequestResult
		err    error
	}
	finished := make(chan outcome, 1)
	go func() {
		result, err := f.issuer.Issue(f.ctx, f.db, input)
		finished <- outcome{result: result, err: err}
	}()
	deadline := time.Now().Add(5 * time.Second)
	blocked := 0
	for time.Now().Before(deadline) {
		if err := f.db.QueryRowContext(f.ctx, `SELECT COUNT(DISTINCT w.REQUESTING_ENGINE_TRANSACTION_ID)
			FROM performance_schema.data_lock_waits w JOIN performance_schema.data_locks l ON l.ENGINE_LOCK_ID=w.REQUESTING_ENGINE_LOCK_ID
			WHERE l.OBJECT_SCHEMA=DATABASE() AND l.OBJECT_NAME='auth_email_login_slot'`).Scan(&blocked); err != nil {
			_ = gate.Rollback()
			t.Fatal("could not observe slot lock wait")
		}
		if blocked > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if blocked == 0 {
		_ = gate.Rollback()
		t.Fatal("challenge request did not wait for the locked email slot")
	}
	time.Sleep(1200 * time.Millisecond)
	if err := gate.Commit(); err != nil {
		t.Fatal("slot lock release failed")
	}
	got := <-finished
	if got.err != nil {
		t.Fatalf("expired-key request did not proceed as a new request after acquiring the slot: %v", got.err)
	}
	newID, _, _ := decodeChallenge(t, got.result)
	if newID == oldID {
		t.Fatal("lock wait replayed the request after its retention deadline")
	}
	for table, expected := range map[string]int{"auth_email_login_challenge": 2, "auth_email_challenge_request": 1} {
		var count int
		if err := f.db.QueryRowContext(f.ctx, `SELECT COUNT(*) FROM `+table+` WHERE email_index=?`, index[:]).Scan(&count); err != nil || count != expected {
			t.Fatalf("unexpected %s rows after post-expiry request: got %d want %d", table, count, expected)
		}
	}
	var outbox int
	if err := f.db.QueryRowContext(f.ctx, `SELECT COUNT(*) FROM auth_email_outbox WHERE challenge_id IN (SELECT challenge_id FROM auth_email_login_challenge WHERE email_index=?)`, index[:]).Scan(&outbox); err != nil || outbox != 2 {
		t.Fatalf("unexpected outbox count after post-expiry request: got %d want 2", outbox)
	}
}

func TestRequestEpochCannotBeSilentlyRebound(t *testing.T) {
	f := openRequestFixture(t, requestLimiterConfig().EmailChallenge)
	// Reinitialization with the exact same key bundle is safe and idempotent.
	tx, err := f.db.BeginTx(f.ctx, nil)
	if err != nil {
		t.Fatal("same-key bootstrap transaction failed")
	}
	if err := f.issuer.InitializeKey(f.ctx, tx); err != nil {
		_ = tx.Rollback()
		t.Fatal("same-key restart was rejected")
	}
	if err := tx.Commit(); err != nil {
		t.Fatal("same-key bootstrap commit failed")
	}

	changed := requestKeys()
	changed.ResponseAESKey = bytes.Repeat([]byte{0x44}, 32)
	other, err := NewChallengeRequestIssuer(f.challenges, f.outbox, f.identities, f.limiter, changed)
	if err != nil {
		t.Fatal("changed-key issuer setup failed")
	}
	tx, err = f.db.BeginTx(f.ctx, nil)
	if err != nil {
		t.Fatal("different-key bootstrap transaction failed")
	}
	if err := other.InitializeKey(f.ctx, tx); !errors.Is(err, ErrChallengeRequest) {
		_ = tx.Rollback()
		t.Fatal("different key was accepted")
	}
	_ = tx.Rollback()

	input := f.input("a01-16-epoch-history@example.test", f.key(), testPublicJWK(t))
	if _, err := f.issuer.Issue(f.ctx, f.db, input); err != nil {
		t.Fatal("history fixture request failed")
	}
	if _, err := f.db.ExecContext(f.ctx, `DELETE FROM auth_email_challenge_request_key_epoch WHERE slot=1`); err != nil {
		t.Fatal("epoch-loss simulation failed")
	}
	tx, err = f.db.BeginTx(f.ctx, nil)
	if err != nil {
		t.Fatal("lost-epoch bootstrap transaction failed")
	}
	if err := f.issuer.InitializeKey(f.ctx, tx); !errors.Is(err, ErrChallengeRequest) {
		_ = tx.Rollback()
		t.Fatal("lost epoch was silently rebound despite request history")
	}
	_ = tx.Rollback()
}
