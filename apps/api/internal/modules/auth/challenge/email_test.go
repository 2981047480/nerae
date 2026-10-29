package challenge

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"

	"nerae/api/internal/modules/auth/domain"
	"nerae/api/internal/modules/auth/repository"
)

func openPrivateDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("NERAE_EMAIL_TEST_DSN")
	if dsn == "" {
		t.Skip("set NERAE_EMAIL_TEST_DSN using the isolated MySQL test harness")
	}
	cfg, err := mysql.ParseDSN(dsn)
	if err != nil {
		t.Fatal("invalid isolated test DSN")
	}
	socket := filepath.Clean(cfg.Addr)
	parent := filepath.Dir(socket)
	resolved, err := filepath.EvalSymlinks(parent)
	info, statErr := os.Lstat(parent)
	socketInfo, socketErr := os.Lstat(socket)
	if err != nil || statErr != nil || socketErr != nil || cfg.Net != "unix" || cfg.DBName != "nerae_email_test" ||
		cfg.User != "root" || cfg.Passwd != "" || !cfg.ParseTime || cfg.Loc != time.UTC ||
		filepath.Base(socket) != "mysql.sock" || filepath.Dir(resolved) != "/private/tmp" ||
		!strings.HasPrefix(filepath.Base(resolved), "nerae-email-challenge-mysql-") ||
		!info.IsDir() || info.Mode().Perm()&0077 != 0 || info.Mode()&os.ModeSymlink != 0 ||
		socketInfo.Mode()&os.ModeSocket == 0 {
		t.Fatal("refusing non-isolated MySQL test target")
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatal("cannot open isolated MySQL")
	}
	db.SetMaxOpenConns(5)
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Ping(); err != nil {
		t.Fatal("cannot connect to isolated MySQL")
	}
	return db
}

func testKey(t *testing.T) (*ecdsa.PrivateKey, domain.DevicePublicJWK) {
	t.Helper()
	private, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	encode := base64.RawURLEncoding.EncodeToString
	return private, domain.DevicePublicJWK{Kty: "EC", Crv: "P-256", X: encode(private.X.FillBytes(make([]byte, 32))), Y: encode(private.Y.FillBytes(make([]byte, 32)))}
}

func testProof(t *testing.T, private *ecdsa.PrivateKey, challengeID, nonce, jti string, now time.Time) string {
	t.Helper()
	return testProofTimes(t, private, challengeID, nonce, jti, now, now.Add(time.Minute))
}

func testProofTimes(t *testing.T, private *ecdsa.PrivateKey, challengeID, nonce, jti string, issuedAt, expiresAt time.Time) string {
	t.Helper()
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"ES256","typ":"JWT"}`))
	claims, err := json.Marshal(map[string]any{"aud": "nerae-auth-device", "purpose": "email_login", "resourceId": challengeID,
		"nonce": nonce, "iat": issuedAt.Unix(), "exp": expiresAt.Unix(), "jti": jti})
	if err != nil {
		t.Fatal(err)
	}
	input := header + "." + base64.RawURLEncoding.EncodeToString(claims)
	digest := sha256.Sum256([]byte(input))
	r, s, err := ecdsa.Sign(rand.Reader, private, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	signature := append(r.FillBytes(make([]byte, 32)), s.FillBytes(make([]byte, 32))...)
	return input + "." + base64.RawURLEncoding.EncodeToString(signature)
}

func TestEmailChallengeTransaction(t *testing.T) {
	db := openPrivateDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	hasher, _ := domain.NewEmailCodeHasher(bytes.Repeat([]byte{1}, 32))
	replay, _ := repository.NewProofReplayStore(bytes.Repeat([]byte{2}, 32))
	protector, _ := domain.NewEmailIdentityProtector(bytes.Repeat([]byte{3}, 32), bytes.Repeat([]byte{4}, 32), "test-key")
	policy := Policy{ChallengeTTL: 5 * time.Minute, MaxAttempts: 2, DeviceProof: domain.ProofPolicy{MaxLifetime: 5 * time.Minute, ClockSkew: 30 * time.Second}}
	store, err := NewStore(hasher, replay, protector, policy)
	if err != nil {
		t.Fatal(err)
	}
	private, public := testKey(t)
	now := time.Now().UTC().Truncate(time.Second)
	var clock atomic.Int64
	clock.Store(now.UnixNano())
	store.now = func() time.Time { return time.Unix(0, clock.Load()).UTC() }
	email := "person@example.test"
	index, _ := protector.Index(email)
	nonce := strings.Repeat("N", 43)
	issue := func(id string) {
		t.Helper()
		clock.Store(now.UnixNano())
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		err = store.Issue(ctx, tx, IssueInput{Email: email, ChallengeID: id, Nonce: nonce, Code: "012345", PublicKey: public})
		if err != nil {
			t.Fatalf("failed to issue challenge: %v", err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatalf("failed to commit challenge: %v", err)
		}
	}
	attempt := func(id, code, proof string, when time.Time, commit bool) (AttemptResult, error) {
		clock.Store(when.UnixNano())
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return AttemptResult{}, err
		}
		defer tx.Rollback()
		result, err := store.Attempt(ctx, tx, AttemptInput{ChallengeID: id, Code: code, DeviceProof: proof})
		if err != nil {
			return result, err
		}
		if commit {
			err = tx.Commit()
		}
		return result, err
	}
	assertRejected := func(result AttemptResult, err error, changed bool) {
		t.Helper()
		if err != nil || result.Verified || result.Changed != changed {
			t.Fatalf("unexpected rejected result (changed=%v): %v", result.Changed, err)
		}
	}

	// Competing first initializers may conflict, but must fail closed and
	// leave exactly one key epoch rather than accepting mixed configurations.
	var epochStart sync.WaitGroup
	epochStart.Add(1)
	epochResults := make(chan error, 2)
	for n, id := range []string{strings.Repeat("X", 32), strings.Repeat("Y", 32)} {
		go func(n int, id string) {
			epochStart.Wait()
			tx, err := db.BeginTx(ctx, nil)
			if err != nil {
				epochResults <- err
				return
			}
			defer tx.Rollback()
			err = store.Issue(ctx, tx, IssueInput{Email: fmt.Sprintf("epoch%d@example.test", n), ChallengeID: id,
				Nonce: nonce, Code: "012345", PublicKey: public})
			if err == nil {
				err = tx.Commit()
			}
			epochResults <- err
		}(n, id)
	}
	epochStart.Done()
	epochA, epochB := <-epochResults, <-epochResults
	if (epochA != nil && !errors.Is(epochA, ErrChallengeStore)) || (epochB != nil && !errors.Is(epochB, ErrChallengeStore)) ||
		(epochA != nil && epochB != nil) {
		t.Fatalf("key epoch initialization failed closed unexpectedly: %v / %v", epochA, epochB)
	}
	var epochs int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM auth_email_identity_key_epoch`).Scan(&epochs); err != nil || epochs != 1 {
		t.Fatal("expected exactly one email key epoch")
	}

	first := strings.Repeat("A", 32)
	issue(first)
	validFirst := testProof(t, private, first, nonce, "wrong-attempt-jti", now)
	badProof := testProof(t, private, first, strings.Repeat("B", 43), "bad-proof-jti", now)
	result, err := attempt(first, "999999", badProof, now, true)
	assertRejected(result, err, false)
	result, err = attempt(first, "999999", validFirst, now, true)
	assertRejected(result, err, true)
	result, err = attempt(first, "999999", validFirst, now, true)
	assertRejected(result, err, true)
	result, err = attempt(first, "012345", validFirst, now, true)
	assertRejected(result, err, false)
	looser, _ := NewStore(hasher, replay, protector, Policy{ChallengeTTL: 10 * time.Minute, MaxAttempts: 3, DeviceProof: policy.DeviceProof})
	looser.now = store.now
	looseTx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	result, err = looser.Attempt(ctx, looseTx, AttemptInput{ChallengeID: first, Code: "012345", DeviceProof: validFirst})
	_ = looseTx.Rollback()
	assertRejected(result, err, false) // A later policy relaxation cannot restore exhausted attempts.

	second := strings.Repeat("B", 32)
	issue(second)
	result, err = attempt(first, "012345", validFirst, now, true)
	assertRejected(result, err, false)
	proof := testProof(t, private, second, nonce, "concurrent-proof", now)
	var start sync.WaitGroup
	start.Add(1)
	type attemptOutcome struct {
		result AttemptResult
		err    error
	}
	results := make(chan attemptOutcome, 2)
	for i := 0; i < 2; i++ {
		go func() {
			start.Wait()
			result, err := attempt(second, "012345", proof, now, true)
			results <- attemptOutcome{result, err}
		}()
	}
	start.Done()
	a, b := <-results, <-results
	if a.err != nil || b.err != nil || a.result.Verified == b.result.Verified {
		t.Fatalf("concurrent challenge consumption: %v / %v", a.err, b.err)
	}

	third := strings.Repeat("C", 32)
	issue(third)
	rollbackProof := testProof(t, private, third, nonce, "rollback-proof", now)
	result, err = attempt(third, "012345", rollbackProof, now, false)
	if err != nil || !result.Verified {
		t.Fatalf("initial verified attempt: %v", err)
	}
	result, err = attempt(third, "012345", rollbackProof, now, true)
	if err != nil || !result.Verified || result.Proof == nil {
		t.Fatalf("attempt after rollback: %v", err)
	}

	wrongProtector, _ := domain.NewEmailIdentityProtector(bytes.Repeat([]byte{5}, 32), bytes.Repeat([]byte{4}, 32), "test-key")
	wrongStore, _ := NewStore(hasher, replay, wrongProtector, policy)
	wrongTx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	wrongErr := wrongStore.Issue(ctx, wrongTx, IssueInput{Email: email, ChallengeID: strings.Repeat("Z", 32), Nonce: nonce,
		Code: "012345", PublicKey: public})
	_ = wrongTx.Rollback()
	if !errors.Is(wrongErr, ErrChallengeStore) {
		t.Fatal("a different email key epoch was not rejected")
	}

	expired := strings.Repeat("D", 32)
	issue(expired)
	stricter, _ := NewStore(hasher, replay, protector, Policy{ChallengeTTL: time.Minute, MaxAttempts: 1, DeviceProof: policy.DeviceProof})
	stricter.now = store.now
	clock.Store(now.Add(time.Minute).UnixNano())
	strictTx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	result, err = stricter.Attempt(ctx, strictTx, AttemptInput{ChallengeID: expired, Code: "012345",
		DeviceProof: testProof(t, private, expired, nonce, "stricter-policy", now.Add(time.Minute))})
	_ = strictTx.Rollback()
	assertRejected(result, err, false)
	result, err = attempt(expired, "012345", testProof(t, private, expired, nonce, "before-creation", now), now.Add(-time.Second), true)
	assertRejected(result, err, false)
	result, err = attempt(expired, "012345", testProof(t, private, expired, nonce, "expired-proof", now), now.Add(5*time.Minute), true)
	assertRejected(result, err, false)
	var digest, ciphertext []byte
	if err := db.QueryRowContext(ctx, `SELECT code_digest, email_ciphertext FROM auth_email_login_challenge WHERE challenge_id = ?`, expired).Scan(&digest, &ciphertext); err != nil || len(digest) != 32 || bytes.Contains(digest, []byte("012345")) || bytes.Contains(ciphertext, []byte(email)) {
		t.Fatal("stored code digest invalid or exposed test code")
	}
	if recovered, err := protector.Open(index, ciphertext); err != nil || recovered != email {
		t.Fatal("encrypted challenge email could not be recovered for account creation")
	}

	email = "concurrent@example.test" // Exercise concurrent creation of a previously absent slot.
	index, _ = protector.Index(email)
	clock.Store(now.UnixNano())
	var issuanceStart sync.WaitGroup
	issuanceStart.Add(1)
	issued := make(chan error, 2)
	ids := []string{strings.Repeat("E", 32), strings.Repeat("F", 32)}
	for _, id := range ids {
		go func(id string) {
			issuanceStart.Wait()
			tx, err := db.BeginTx(ctx, nil)
			if err != nil {
				issued <- err
				return
			}
			defer tx.Rollback()
			err = store.Issue(ctx, tx, IssueInput{Email: email, ChallengeID: id, Nonce: nonce, Code: "012345", PublicKey: public})
			if err == nil {
				err = tx.Commit()
			}
			issued <- err
		}(id)
	}
	issuanceStart.Done()
	if firstErr, secondErr := <-issued, <-issued; firstErr != nil || secondErr != nil {
		t.Fatalf("concurrent reissue failed: %v / %v", firstErr, secondErr)
	}
	var active []byte
	if err := db.QueryRowContext(ctx, `SELECT challenge_id FROM auth_email_login_slot WHERE email_index = ?`, index[:]).Scan(&active); err != nil {
		t.Fatal("could not read current challenge")
	}
	for _, id := range ids {
		result, err := attempt(id, "012345", testProof(t, private, id, nonce, "reissue-"+id, now), now, true)
		if err != nil || result.Verified != (string(active) == id) {
			t.Fatalf("reissue active-slot behavior incorrect: %v", err)
		}
	}

	email = "rollback@example.test"
	index, _ = protector.Index(email)
	oldID, newID := strings.Repeat("G", 32), strings.Repeat("H", 32)
	issue(oldID)
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Issue(ctx, tx, IssueInput{Email: email, ChallengeID: newID, Nonce: nonce, Code: "012345", PublicKey: public}); err != nil {
		_ = tx.Rollback()
		t.Fatal("could not create rollback candidate")
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal("reissue rollback failed")
	}
	result, err = attempt(oldID, "012345", testProof(t, private, oldID, nonce, "old-after-rollback", now), now, true)
	if err != nil || !result.Verified {
		t.Fatalf("rollback invalidated old challenge: %v", err)
	}

	// A different MySQL driver location must not extend a challenge deadline.
	otherCfg, _ := mysql.ParseDSN(os.Getenv("NERAE_EMAIL_TEST_DSN"))
	otherLocation, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	otherCfg.Loc = otherLocation
	otherDB, err := sql.Open("mysql", otherCfg.FormatDSN())
	if err != nil {
		t.Fatal("could not open alternate-location test connection")
	}
	defer otherDB.Close()
	email = "timezone@example.test"
	index, _ = protector.Index(email)
	timezoneID := strings.Repeat("J", 32)
	tx, err = otherDB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Issue(ctx, tx, IssueInput{Email: email, ChallengeID: timezoneID, Nonce: nonce, Code: "012345", PublicKey: public}); err != nil {
		_ = tx.Rollback()
		t.Fatal("could not issue with alternate driver location")
	}
	if err := tx.Commit(); err != nil {
		t.Fatal("could not commit alternate-location challenge")
	}
	result, err = attempt(timezoneID, "012345", testProof(t, private, timezoneID, nonce, "timezone-expired", now.Add(5*time.Minute)), now.Add(5*time.Minute), true)
	assertRejected(result, err, false)
	timezoneValid := strings.Repeat("K", 32)
	tx, err = otherDB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	clock.Store(now.UnixNano())
	if err := store.Issue(ctx, tx, IssueInput{Email: email, ChallengeID: timezoneValid, Nonce: nonce, Code: "012345", PublicKey: public}); err != nil {
		_ = tx.Rollback()
		t.Fatal("could not issue alternate-location valid challenge")
	}
	if err := tx.Commit(); err != nil {
		t.Fatal("could not commit alternate-location valid challenge")
	}
	beforeExpiry := now.Add(4 * time.Minute)
	result, err = attempt(timezoneValid, "012345", testProof(t, private, timezoneValid, nonce, "timezone-valid", beforeExpiry), beforeExpiry, true)
	if err != nil || !result.Verified {
		t.Fatalf("valid challenge rejected before UTC deadline: %v", err)
	}

	email = "bound@example.test"
	boundID := strings.Repeat("M", 32)
	issue(boundID)
	clock.Store(now.UnixNano())
	boundTx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	boundResult, err := store.Attempt(ctx, boundTx, AttemptInput{ChallengeID: boundID, Code: "012345",
		DeviceProof: testProof(t, private, boundID, nonce, "bound-proof", now)})
	if err != nil || !boundResult.Verified || boundResult.Proof == nil {
		_ = boundTx.Rollback()
		t.Fatal("valid challenge did not produce a transaction-bound proof")
	}
	details, err := boundResult.Proof.ForTransaction(ctx, boundTx)
	if err != nil || details.KeyID != "test-key" {
		_ = boundTx.Rollback()
		t.Fatal("bound identity details unavailable")
	}
	if recovered, err := protector.Open(details.Index, details.Ciphertext); err != nil || recovered != email {
		_ = boundTx.Rollback()
		t.Fatal("bound proof did not carry the protected address")
	}
	otherTx, err := db.BeginTx(ctx, nil)
	if err != nil {
		_ = boundTx.Rollback()
		t.Fatal(err)
	}
	if _, err := boundResult.Proof.ForTransaction(ctx, otherTx); !errors.Is(err, ErrChallengeStore) {
		_ = otherTx.Rollback()
		_ = boundTx.Rollback()
		t.Fatal("challenge proof accepted by another transaction")
	}
	_ = otherTx.Rollback()
	clock.Store(now.Add(2 * time.Minute).UnixNano())
	if _, err := boundResult.Proof.ForTransaction(ctx, boundTx); !errors.Is(err, ErrChallengeStore) {
		_ = boundTx.Rollback()
		t.Fatal("expired device proof accepted while the email challenge remained valid")
	}
	clock.Store(now.Add(5 * time.Minute).UnixNano())
	if _, err := boundResult.Proof.ForTransaction(ctx, boundTx); !errors.Is(err, ErrChallengeStore) {
		_ = boundTx.Rollback()
		t.Fatal("expired challenge proof accepted")
	}
	for _, rendered := range []string{fmt.Sprintf("%+v", *boundResult.Proof), fmt.Sprintf("%#v", *boundResult.Proof)} {
		if strings.Contains(rendered, "test-key") || strings.Contains(rendered, email) {
			_ = boundTx.Rollback()
			t.Fatal("formatted proof value exposed protected identity details")
		}
	}
	for _, value := range []any{boundResult.Proof, *boundResult.Proof, details} {
		if _, err := json.Marshal(value); err == nil {
			_ = boundTx.Rollback()
			t.Fatal("verified proof or details were JSON-serializable")
		}
	}
	var proofLog bytes.Buffer
	slog.New(slog.NewJSONHandler(&proofLog, nil)).Info("test", "proof", *boundResult.Proof, "details", details)
	if strings.Contains(proofLog.String(), "test-key") || strings.Contains(proofLog.String(), email) {
		_ = boundTx.Rollback()
		t.Fatal("structured log exposed protected proof details")
	}
	_ = boundTx.Rollback()
	clock.Store(now.UnixNano())
	if _, err := boundResult.Proof.ForTransaction(ctx, boundTx); !errors.Is(err, ErrChallengeStore) {
		t.Fatal("proof accepted after its transaction rolled back")
	}
	if _, err := (&VerifiedEmail{}).ForTransaction(ctx, boundTx); !errors.Is(err, ErrChallengeStore) {
		t.Fatal("unverified zero-value challenge proof accepted")
	}

	email = "policy@example.test"
	policyID := strings.Repeat("P", 32)
	issue(policyID)
	policyAttempt := func(candidate *Store, proof string) {
		t.Helper()
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		result, err := candidate.Attempt(ctx, tx, AttemptInput{ChallengeID: policyID, Code: "012345", DeviceProof: proof})
		assertRejected(result, err, false)
	}
	proofLooser, _ := NewStore(hasher, replay, protector, Policy{ChallengeTTL: 10 * time.Minute, MaxAttempts: 3,
		DeviceProof: domain.ProofPolicy{MaxLifetime: 24 * time.Hour, ClockSkew: 5 * time.Minute}})
	proofLooser.now = store.now
	policyAttempt(proofLooser, testProofTimes(t, private, policyID, nonce, "old-lifetime-bound", now.Add(-6*time.Minute), now.Add(time.Minute)))
	policyAttempt(proofLooser, testProofTimes(t, private, policyID, nonce, "old-skew-bound", now.Add(time.Minute), now.Add(2*time.Minute)))
	proofStricter, _ := NewStore(hasher, replay, protector, Policy{ChallengeTTL: 5 * time.Minute, MaxAttempts: 2,
		DeviceProof: domain.ProofPolicy{MaxLifetime: time.Minute, ClockSkew: 0}})
	proofStricter.now = store.now
	policyAttempt(proofStricter, testProofTimes(t, private, policyID, nonce, "new-lifetime-bound", now, now.Add(2*time.Minute)))
	policyAttempt(proofStricter, testProofTimes(t, private, policyID, nonce, "new-skew-bound", now.Add(time.Second), now.Add(30*time.Second)))

	email = "tamper@example.test"
	tamperedID := strings.Repeat("L", 32)
	issue(tamperedID)
	if err := db.QueryRowContext(ctx, `SELECT email_ciphertext FROM auth_email_login_challenge WHERE challenge_id = ?`, tamperedID).Scan(&ciphertext); err != nil {
		t.Fatal("cannot read test ciphertext")
	}
	ciphertext[len(ciphertext)-1] ^= 1
	if _, err := db.ExecContext(ctx, `UPDATE auth_email_login_challenge SET email_ciphertext = ? WHERE challenge_id = ?`, ciphertext, tamperedID); err != nil {
		t.Fatal("cannot tamper with test ciphertext")
	}
	_, err = attempt(tamperedID, "012345", testProof(t, private, tamperedID, nonce, "tampered-email", now), now, false)
	if !errors.Is(err, ErrChallengeStore) {
		t.Fatal("tampered encrypted email was accepted")
	}
}

func TestChallengeInputFormattingIsRedacted(t *testing.T) {
	input := IssueInput{Email: "person@example.test", Code: "012345", Nonce: "private-nonce"}
	attempt := AttemptInput{Code: "012345", DeviceProof: "private-proof"}
	for _, value := range []string{fmt.Sprintf("%+v", input), fmt.Sprintf("%#v", input), fmt.Sprintf("%+v", attempt), fmt.Sprintf("%#v", attempt)} {
		if strings.Contains(value, "012345") || strings.Contains(value, "private") || strings.Contains(value, input.Email) {
			t.Fatal("formatted challenge exposed secret material")
		}
	}
	for _, value := range []any{input, attempt, AttemptResult{Verified: true}} {
		if _, err := json.Marshal(value); err == nil {
			t.Fatal("sensitive challenge value was JSON-serializable")
		}
	}
	var log bytes.Buffer
	slog.New(slog.NewJSONHandler(&log, nil)).Info("test", "issue", input, "attempt", attempt)
	if strings.Contains(log.String(), "012345") || strings.Contains(log.String(), "private") || strings.Contains(log.String(), input.Email) {
		t.Fatal("structured logging exposed challenge secrets")
	}
	if _, err := NewStore(nil, nil, nil, Policy{}); !errors.Is(err, ErrChallengeStore) {
		t.Fatal("missing dependencies accepted")
	}
}

func TestServerChallengePolicyValidation(t *testing.T) {
	hasher, _ := domain.NewEmailCodeHasher(bytes.Repeat([]byte{1}, 32))
	replay, _ := repository.NewProofReplayStore(bytes.Repeat([]byte{2}, 32))
	protector, _ := domain.NewEmailIdentityProtector(bytes.Repeat([]byte{3}, 32), bytes.Repeat([]byte{4}, 32), "test-key")
	valid := Policy{ChallengeTTL: 5 * time.Minute, MaxAttempts: 3, DeviceProof: domain.ProofPolicy{MaxLifetime: time.Minute}}
	if _, err := NewStore(hasher, replay, protector, valid); err != nil {
		t.Fatalf("valid server policy rejected: %v", err)
	}
	for _, policy := range []Policy{
		{ChallengeTTL: 0, MaxAttempts: 3, DeviceProof: valid.DeviceProof},
		{ChallengeTTL: 31 * time.Minute, MaxAttempts: 3, DeviceProof: valid.DeviceProof},
		{ChallengeTTL: time.Second + time.Nanosecond, MaxAttempts: 3, DeviceProof: valid.DeviceProof},
		{ChallengeTTL: time.Minute, MaxAttempts: 0, DeviceProof: valid.DeviceProof},
		{ChallengeTTL: time.Minute, MaxAttempts: 11, DeviceProof: valid.DeviceProof},
		{ChallengeTTL: time.Minute, MaxAttempts: 3, DeviceProof: domain.ProofPolicy{MaxLifetime: 25 * time.Hour}},
	} {
		if _, err := NewStore(hasher, replay, protector, policy); !errors.Is(err, ErrChallengeStore) {
			t.Fatal("invalid server policy accepted")
		}
	}
}
