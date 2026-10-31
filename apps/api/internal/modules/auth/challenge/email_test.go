package challenge

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hmac"
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

	"nerae/api/internal/modules/auth/bootstrap"
	"nerae/api/internal/modules/auth/codeepoch"
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

type loginDimensionRateLimiterFunc func(context.Context, [32]byte, [32]byte) error

func (f loginDimensionRateLimiterFunc) AllowLoginIdentityDimensions(ctx context.Context, emailIndex, deviceFingerprint [32]byte) error {
	return f(ctx, emailIndex, deviceFingerprint)
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
	store, err := NewStore(hasher, replay, protector, "fixture-email-code-hash-v1", policy)
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

	// An empty database is not implicitly initialized by Issue or Attempt.
	uninitializedID := strings.Repeat("U", 32)
	uninitializedTx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Issue(ctx, uninitializedTx, IssueInput{Email: email, ChallengeID: uninitializedID, Nonce: nonce,
		Code: "012345", PublicKey: public}); !errors.Is(err, ErrChallengeStore) {
		_ = uninitializedTx.Rollback()
		t.Fatal("Issue accepted an uninitialized identity epoch")
	}
	_ = uninitializedTx.Rollback()
	uninitializedAttemptTx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Attempt(ctx, uninitializedAttemptTx, AttemptInput{ChallengeID: uninitializedID, Code: "012345"}); !errors.Is(err, ErrChallengeStore) {
		_ = uninitializedAttemptTx.Rollback()
		t.Fatal("Attempt accepted an uninitialized identity epoch")
	}
	_ = uninitializedAttemptTx.Rollback()
	var epochs int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM auth_email_identity_key_epoch`).Scan(&epochs); err != nil {
		t.Fatal("could not inspect identity epoch before bootstrap")
	}
	if epochs != 0 {
		t.Fatalf("uninitialized calls created an identity epoch: %d", epochs)
	}
	initializeChallengeEpochs(t, db, protector, hasher)
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM auth_email_identity_key_epoch`).Scan(&epochs); err != nil || epochs != 1 {
		t.Fatal("explicit initialization did not create exactly one epoch")
	}

	first := strings.Repeat("A", 32)
	issue(first)
	metadataTx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	metadata, found, err := store.RateLimitDetails(ctx, metadataTx, first)
	_ = metadataTx.Rollback()
	expectedFingerprint, fingerprintErr := domain.DeviceKeyFingerprint(public)
	var limiterIndex, limiterFingerprint [32]byte
	applyErr := metadata.ApplyLoginDimensionLimit(ctx, loginDimensionRateLimiterFunc(func(_ context.Context, gotIndex, gotFingerprint [32]byte) error {
		limiterIndex, limiterFingerprint = gotIndex, gotFingerprint
		return nil
	}))
	if err != nil || !found || fingerprintErr != nil || applyErr != nil || limiterIndex != index || limiterFingerprint != expectedFingerprint {
		t.Fatal("server-stored limiter metadata did not match the issued challenge")
	}
	encodedMetadata, encodeErr := json.Marshal(metadata)
	if encodeErr != nil || strings.Contains(string(encodedMetadata), string(index[:])) ||
		strings.Contains(fmt.Sprintf("%#v", metadata), string(expectedFingerprint[:])) ||
		metadata.LogValue().String() != "EmailChallengeRateLimitMetadata{redacted}" {
		t.Fatal("opaque limiter metadata formatting exposed a stored key")
	}
	missingMetadataTx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	missingMetadata, missing, err := store.RateLimitDetails(ctx, missingMetadataTx, strings.Repeat("Z", 32))
	_ = missingMetadataTx.Rollback()
	if err != nil || missing || missingMetadata != (RateLimitMetadata{}) {
		t.Fatal("unknown challenge exposed or returned limiter metadata")
	}
	if _, err := db.ExecContext(ctx, `UPDATE auth_email_login_challenge SET key_fingerprint=? WHERE challenge_id=?`, make([]byte, 32), first); err != nil {
		t.Fatal("could not seed malformed challenge metadata")
	}
	malformedMetadataTx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, malformedFound, malformedErr := store.RateLimitDetails(ctx, malformedMetadataTx, first)
	_ = malformedMetadataTx.Rollback()
	if malformedFound || !errors.Is(malformedErr, ErrChallengeStore) {
		t.Fatal("malformed persisted fingerprint was accepted for limiting")
	}
	if _, err := db.ExecContext(ctx, `UPDATE auth_email_login_challenge SET key_fingerprint=? WHERE challenge_id=?`, expectedFingerprint[:], first); err != nil {
		t.Fatal("could not restore isolated challenge metadata")
	}
	if _, err := db.ExecContext(ctx, `UPDATE auth_email_login_challenge SET email_key_id='wrong-key' WHERE challenge_id=?`, first); err != nil {
		t.Fatal("could not seed mismatched challenge key id")
	}
	mismatchTx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, mismatchFound, mismatchErr := store.RateLimitDetails(ctx, mismatchTx, first)
	_ = mismatchTx.Rollback()
	if mismatchFound || !errors.Is(mismatchErr, ErrChallengeStore) {
		t.Fatal("challenge encrypted under a different persistent key was accepted")
	}
	if _, err := db.ExecContext(ctx, `UPDATE auth_email_login_challenge SET email_key_id='test-key' WHERE challenge_id=?`, first); err != nil {
		t.Fatal("could not restore isolated challenge key id")
	}
	if _, err := db.ExecContext(ctx, `UPDATE auth_email_login_challenge SET email_ciphertext=? WHERE challenge_id=?`, []byte("corrupt"), first); err != nil {
		t.Fatal("could not seed mismatched challenge ciphertext")
	}
	corruptCiphertextTx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, ciphertextFound, ciphertextErr := store.RateLimitDetails(ctx, corruptCiphertextTx, first)
	_ = corruptCiphertextTx.Rollback()
	if ciphertextFound || !errors.Is(ciphertextErr, ErrChallengeStore) {
		t.Fatal("challenge ciphertext inconsistent with the persistent identity key was accepted")
	}
	_, validCiphertext, err := protector.Protect(email)
	if err != nil {
		t.Fatal("could not restore isolated challenge ciphertext")
	}
	if _, err := db.ExecContext(ctx, `UPDATE auth_email_login_challenge SET email_ciphertext=? WHERE challenge_id=?`, validCiphertext, first); err != nil {
		t.Fatal("could not restore isolated challenge ciphertext")
	}
	// Losing the epoch after challenge data exists must reject authentication
	// and must not let either Issue or the bootstrapper recreate it.
	var epochTag []byte
	if err := db.QueryRowContext(ctx, `SELECT key_tag FROM auth_email_identity_key_epoch WHERE slot=1`).Scan(&epochTag); err != nil {
		t.Fatal("could not capture test epoch")
	}
	if _, err := db.ExecContext(ctx, `DELETE FROM auth_email_identity_key_epoch WHERE slot=1`); err != nil {
		t.Fatal("could not simulate missing epoch after challenge data")
	}
	missingIssueTx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	missingIssueID := strings.Repeat("V", 32)
	if err := store.Issue(ctx, missingIssueTx, IssueInput{Email: email, ChallengeID: missingIssueID, Nonce: nonce,
		Code: "012345", PublicKey: public}); !errors.Is(err, ErrChallengeStore) {
		_ = missingIssueTx.Rollback()
		t.Fatal("Issue rebuilt a missing epoch after challenge history existed")
	}
	_ = missingIssueTx.Rollback()
	missingAttemptTx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Attempt(ctx, missingAttemptTx, AttemptInput{ChallengeID: first, Code: "012345"}); !errors.Is(err, ErrChallengeStore) {
		_ = missingAttemptTx.Rollback()
		t.Fatal("Attempt accepted a missing identity epoch")
	}
	_ = missingAttemptTx.Rollback()
	missingRateLimitTx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, _, missingRateLimitErr := store.RateLimitDetails(ctx, missingRateLimitTx, first)
	_ = missingRateLimitTx.Rollback()
	if !errors.Is(missingRateLimitErr, ErrChallengeStore) {
		t.Fatal("rate limiter metadata accepted a missing identity key epoch")
	}
	reinitTx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	initializer, err := bootstrap.NewIdentityEpochInitializer(protector)
	if err != nil {
		_ = reinitTx.Rollback()
		t.Fatal("identity epoch initializer setup failed")
	}
	if err := initializer.Initialize(ctx, reinitTx); !errors.Is(err, bootstrap.ErrIdentityEpoch) {
		_ = reinitTx.Rollback()
		t.Fatal("bootstrapper recreated an epoch after challenge history existed")
	}
	_ = reinitTx.Rollback()
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM auth_email_identity_key_epoch`).Scan(&epochs); err != nil || epochs != 0 {
		t.Fatal("failed authentication/bootstrap rebuilt the missing epoch")
	}
	// Restore the known test key directly only as fixture repair so the
	// remaining challenge behavior tests can continue.
	if _, err := db.ExecContext(ctx, `INSERT INTO auth_email_identity_key_epoch(slot,key_tag) VALUES (1,?)`, epochTag); err != nil {
		t.Fatal("could not restore isolated test epoch")
	}
	validFirst := testProof(t, private, first, nonce, "wrong-attempt-jti", now)
	badProof := testProof(t, private, first, strings.Repeat("B", 43), "bad-proof-jti", now)
	result, err := attempt(first, "999999", badProof, now, true)
	assertRejected(result, err, false)
	// A rolled-back wrong-code attempt leaves neither count nor proof marker.
	result, err = attempt(first, "999999", validFirst, now, false)
	assertRejected(result, err, true)
	result, err = attempt(first, "999999", validFirst, now, true)
	assertRejected(result, err, true)
	// The same proof cannot be consumed again, even with another code; it
	// must not add attempts or authenticate. A fresh proof may try again.
	result, err = attempt(first, "999999", validFirst, now, true)
	assertRejected(result, err, false)
	result, err = attempt(first, "012345", validFirst, now, true)
	assertRejected(result, err, false)
	var wrongAttempts int
	if err := db.QueryRowContext(ctx, `SELECT attempts FROM auth_email_login_challenge WHERE challenge_id=?`, first).Scan(&wrongAttempts); err != nil || wrongAttempts != 1 {
		t.Fatal("reused proof changed the wrong-code counter")
	}
	validSecond := testProof(t, private, first, nonce, "wrong-attempt-jti-2", now)
	result, err = attempt(first, "999999", validSecond, now, true)
	assertRejected(result, err, true)
	result, err = attempt(first, "012345", testProof(t, private, first, nonce, "after-exhaustion", now), now, true)
	assertRejected(result, err, false)
	looser, _ := NewStore(hasher, replay, protector, "fixture-email-code-hash-v1", Policy{ChallengeTTL: 10 * time.Minute, MaxAttempts: 3, DeviceProof: policy.DeviceProof})
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
	wrongStore, _ := NewStore(hasher, replay, wrongProtector, "fixture-email-code-hash-v1", policy)
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
	stricter, _ := NewStore(hasher, replay, protector, "fixture-email-code-hash-v1", Policy{ChallengeTTL: time.Minute, MaxAttempts: 1, DeviceProof: policy.DeviceProof})
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
	proofLooser, _ := NewStore(hasher, replay, protector, "fixture-email-code-hash-v1", Policy{ChallengeTTL: 10 * time.Minute, MaxAttempts: 3,
		DeviceProof: domain.ProofPolicy{MaxLifetime: 24 * time.Hour, ClockSkew: 5 * time.Minute}})
	proofLooser.now = store.now
	policyAttempt(proofLooser, testProofTimes(t, private, policyID, nonce, "old-lifetime-bound", now.Add(-6*time.Minute), now.Add(time.Minute)))
	policyAttempt(proofLooser, testProofTimes(t, private, policyID, nonce, "old-skew-bound", now.Add(time.Minute), now.Add(2*time.Minute)))
	proofStricter, _ := NewStore(hasher, replay, protector, "fixture-email-code-hash-v1", Policy{ChallengeTTL: 5 * time.Minute, MaxAttempts: 2,
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

	// Simulate a clock advancing while database locks are being acquired.
	// A pre-lock timestamp must never authorize an expired challenge.
	email = "after-lock@example.test"
	afterLockID := strings.Repeat("R", 32)
	issue(afterLockID)
	afterLockProof := testProof(t, private, afterLockID, nonce, "after-lock-expiry", now)
	var calls atomic.Int32
	store.now = func() time.Time {
		if calls.Add(1) == 1 {
			return now
		}
		return now.Add(5 * time.Minute)
	}
	result, err = attempt(afterLockID, "012345", afterLockProof, now, true)
	assertRejected(result, err, false)
	store.now = func() time.Time { return time.Unix(0, clock.Load()).UTC() }

	// Simulate expiry during the replay-marker insertion. The required error
	// forces rollback; afterward the same proof remains usable at valid time.
	email = "after-marker@example.test"
	afterMarkerID := strings.Repeat("S", 32)
	issue(afterMarkerID)
	afterMarkerProof := testProof(t, private, afterMarkerID, nonce, "after-marker-expiry", now)
	calls.Store(0)
	store.now = func() time.Time {
		if calls.Add(1) <= 2 {
			return now
		}
		return now.Add(5 * time.Minute)
	}
	result, err = attempt(afterMarkerID, "999999", afterMarkerProof, now, true)
	if !errors.Is(err, ErrChallengeStore) || result.Verified || result.Changed {
		t.Fatal("expiry during replay-marker insertion did not require rollback")
	}
	store.now = func() time.Time { return time.Unix(0, clock.Load()).UTC() }
	if err := db.QueryRowContext(ctx, `SELECT attempts FROM auth_email_login_challenge WHERE challenge_id=?`, afterMarkerID).Scan(&wrongAttempts); err != nil || wrongAttempts != 0 {
		t.Fatal("expired attempt changed the wrong-code counter")
	}
	result, err = attempt(afterMarkerID, "012345", afterMarkerProof, now, true)
	if err != nil || !result.Verified || result.Proof == nil {
		t.Fatal("rolled-back stale proof marker prevented a valid retry")
	}
}

func TestEmailReceiptProofRevalidation(t *testing.T) {
	db := openPrivateDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	hasher, _ := domain.NewEmailCodeHasher(bytes.Repeat([]byte{1}, 32))
	replay, _ := repository.NewProofReplayStore(bytes.Repeat([]byte{2}, 32))
	protector, _ := domain.NewEmailIdentityProtector(bytes.Repeat([]byte{3}, 32), bytes.Repeat([]byte{4}, 32), "test-key")
	store, err := NewStore(hasher, replay, protector, "fixture-email-code-hash-v1", Policy{ChallengeTTL: 5 * time.Minute, MaxAttempts: 3,
		DeviceProof: domain.ProofPolicy{MaxLifetime: 5 * time.Minute, ClockSkew: 30 * time.Second}})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	var clock atomic.Int64
	clock.Store(now.UnixNano())
	store.now = func() time.Time { return time.Unix(0, clock.Load()).UTC() }
	initializeChallengeEpochs(t, db, protector, hasher)
	var tx *sql.Tx
	private, public := testKey(t)
	fingerprint, err := domain.DeviceKeyFingerprint(public)
	if err != nil {
		t.Fatal(err)
	}
	// Initialize the replay epoch independently of other test functions.
	bootstrapProof := domain.VerifiedDeviceProof{JTI: "receipt-test-bootstrap", ExpiresAt: now.Add(time.Minute)}
	bootstrapProof.ReplayUntil = domain.DeviceProofReplayDeadline(bootstrapProof.ExpiresAt)
	tx, err = db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := replay.Consume(ctx, tx, fingerprint, bootstrapProof, now); err != nil {
		_ = tx.Rollback()
		t.Fatal("replay epoch bootstrap failed")
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	const email = "receipt-proof@example.test"
	challengeID := strings.Repeat("a", 32)
	replacementID := strings.Repeat("b", 32)
	nonce := strings.Repeat("n", 43)
	scope, err := protector.Index(email)
	if err != nil {
		t.Fatal(err)
	}
	issue := func(id string) {
		t.Helper()
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		if err := store.Issue(ctx, tx, IssueInput{Email: email, ChallengeID: id, Nonce: nonce, Code: "012345", PublicKey: public}); err != nil {
			_ = tx.Rollback()
			t.Fatal("challenge issue failed")
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
	}
	issue(challengeID)
	proof := testProof(t, private, challengeID, nonce, "receipt-proof-jti", now)
	input := AttemptInput{ChallengeID: challengeID, Code: "999999", DeviceProof: proof}
	// A proof consumed in a rolled-back attempt must not be recoverable.
	tx, err = db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	attempt, err := store.Attempt(ctx, tx, input)
	if err != nil || !attempt.Changed || attempt.Verified {
		_ = tx.Rollback()
		t.Fatal("wrong-code fixture did not consume a proof")
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	check := func(compact string) error {
		t.Helper()
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer tx.Rollback()
		_, err = store.VerifyReceiptProof(ctx, tx, scope, challengeID, compact)
		return err
	}
	if err := check(proof); !errors.Is(err, ErrReceiptProofRejected) {
		t.Fatal("rolled-back replay marker was accepted")
	}
	tx, err = db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	attempt, err = store.Attempt(ctx, tx, input)
	if err != nil || !attempt.Changed || attempt.Verified {
		_ = tx.Rollback()
		t.Fatal("wrong-code attempt was not recorded")
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	otherKey, _ := testKey(t)
	if err := check(testProof(t, otherKey, challengeID, nonce, "receipt-proof-jti", now)); !errors.Is(err, ErrReceiptProofRejected) {
		t.Fatal("signature from another device was accepted")
	}
	if err := check(testProof(t, private, challengeID, nonce, "never-consumed-jti", now)); !errors.Is(err, ErrReceiptProofRejected) {
		t.Fatal("unconsumed signed proof was accepted")
	}
	tx, err = db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	gotScope, found, err := store.ReceiptScope(ctx, tx, challengeID)
	if err != nil || !found || gotScope != scope {
		_ = tx.Rollback()
		t.Fatal("protected challenge scope was not found")
	}
	_, found, err = store.ReceiptScope(ctx, tx, strings.Repeat("z", 32))
	if err != nil || found {
		_ = tx.Rollback()
		t.Fatal("unknown challenge produced a scope")
	}
	validated, err := store.VerifyReceiptProof(ctx, tx, scope, challengeID, proof)
	if err != nil {
		_ = tx.Rollback()
		t.Fatal("consumed proof was not revalidated")
	}
	details, err := validated.ForTransaction(ctx, tx)
	if err != nil || details.Scope != scope || details.Fingerprint != fingerprint || details.ChallengeID != challengeID {
		_ = tx.Rollback()
		t.Fatal("revalidated proof was not bound to the original device and challenge")
	}
	if err := details.VerifyTimeOnly(now); err != nil {
		_ = tx.Rollback()
		t.Fatal("valid proof snapshot failed its time-only check")
	}
	if err := details.VerifyTimeOnly(now.Add(2 * time.Minute)); !errors.Is(err, ErrReceiptProofRejected) {
		_ = tx.Rollback()
		t.Fatal("expired proof snapshot passed its time-only check")
	}
	if err := (ReceiptProofDetails{}).VerifyTimeOnly(now); !errors.Is(err, ErrReceiptProofRejected) {
		_ = tx.Rollback()
		t.Fatal("zero proof snapshot passed its time-only check")
	}
	if _, err := validated.ForRequest(ctx, tx, challengeID, proof); err != nil {
		_ = tx.Rollback()
		t.Fatal("original request proof did not match")
	}
	alternateSignature := testProof(t, private, challengeID, nonce, "receipt-proof-jti", now)
	if _, err := validated.ForRequest(ctx, tx, challengeID, alternateSignature); !errors.Is(err, ErrReceiptProofRejected) {
		_ = tx.Rollback()
		t.Fatal("a newly signed proof with the same JTI matched the original compact proof")
	}
	if _, err := validated.ForRequest(ctx, tx, replacementID, proof); !errors.Is(err, ErrReceiptProofRejected) {
		_ = tx.Rollback()
		t.Fatal("proof matched a different challenge")
	}
	if _, err := tx.ExecContext(ctx, `UPDATE auth_email_login_challenge SET nonce=? WHERE challenge_id=?`, strings.Repeat("x", 43), challengeID); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if _, err := validated.ForTransaction(ctx, tx); !errors.Is(err, ErrReceiptProofRejected) {
		_ = tx.Rollback()
		t.Fatal("transaction-local challenge change was not rechecked")
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if _, err := validated.ForTransaction(ctx, tx); !errors.Is(err, ErrChallengeStore) {
		t.Fatal("proof escaped its finished transaction")
	}
	issue(replacementID)
	if err := check(proof); err != nil {
		t.Fatal("original consumed proof could not be rechecked after slot replacement")
	}
	// Once consumed, the challenge deadline does not by itself invalidate a
	// still-live proof used to check an existing receipt. This is not a new
	// login and the caller still has to validate the receipt and authorization.
	store.policy.ChallengeTTL = 30 * time.Second
	shortID := strings.Repeat("c", 32)
	issue(shortID)
	shortProof := testProof(t, private, shortID, nonce, "short-challenge-jti", now)
	tx, err = db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	attempt, err = store.Attempt(ctx, tx, AttemptInput{ChallengeID: shortID, Code: "999999", DeviceProof: shortProof})
	if err != nil || !attempt.Changed || attempt.Verified {
		_ = tx.Rollback()
		t.Fatal("short challenge fixture was not consumed")
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	clock.Store(now.Add(31 * time.Second).UnixNano())
	tx, err = db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.VerifyReceiptProof(ctx, tx, scope, shortID, shortProof); err != nil {
		_ = tx.Rollback()
		t.Fatal("valid proof was rejected only because the original challenge expired")
	}
	_ = tx.Rollback()
	clock.Store(now.Add(2 * time.Minute).UnixNano())
	if err := check(proof); !errors.Is(err, ErrReceiptProofRejected) {
		t.Fatal("expired signed proof was accepted")
	}
	clock.Store(now.UnixNano())
	var calls atomic.Int32
	store.now = func() time.Time {
		if calls.Add(1) == 1 {
			return now
		}
		return now.Add(2 * time.Minute)
	}
	if err := check(proof); !errors.Is(err, ErrReceiptProofRejected) {
		t.Fatal("proof expiry after marker lookup was not rechecked")
	}
	for _, value := range []any{validated, details} {
		if _, err := json.Marshal(value); err == nil {
			t.Fatal("receipt proof was JSON-serializable")
		}
		if strings.Contains(fmt.Sprintf("%+v", value), proof) {
			t.Fatal("formatted receipt proof exposed a signed proof")
		}
	}
}

// initializeChallengeEpochs uses a synthetic derivation epoch because tests
// in package challenge cannot import issuance without creating an import cycle.
// The code-hash epoch itself is initialized by the production codeepoch store;
// issuance tests exercise the real derivation-key bootstrap path.
func initializeChallengeEpochs(t *testing.T, db *sql.DB, protector *domain.EmailIdentityProtector, hasher *domain.EmailCodeHasher) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	identityInitializer, err := bootstrap.NewIdentityEpochInitializer(protector)
	if err != nil {
		t.Fatal("identity epoch initializer setup failed")
	}
	hashEpoch, err := codeepoch.New(hasher, "fixture-email-code-hash-v1")
	if err != nil {
		t.Fatal("code hash epoch setup failed")
	}
	key := bytes.Repeat([]byte{0x7a}, 32)
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte("nerae.auth.email-code-epoch.v1\x00"))
	derivationTag := mac.Sum(nil)
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal("epoch transaction failed")
	}
	defer tx.Rollback()
	if err := identityInitializer.Initialize(ctx, tx); err != nil {
		t.Fatal("identity epoch initialization failed")
	}
	var codeKeyID, codeTag []byte
	err = tx.QueryRowContext(ctx, `SELECT key_id,key_tag FROM auth_email_code_key_epoch WHERE slot=1 FOR UPDATE`).Scan(&codeKeyID, &codeTag)
	if errors.Is(err, sql.ErrNoRows) {
		if _, err := tx.ExecContext(ctx, `INSERT INTO auth_email_code_key_epoch(slot,key_id,key_tag) VALUES (1,?,?)`, []byte("challenge-fixture-code-v1"), derivationTag); err != nil {
			t.Fatal("synthetic derivation epoch insert failed")
		}
	} else if err != nil || string(codeKeyID) != "challenge-fixture-code-v1" || !hmac.Equal(codeTag, derivationTag) {
		t.Fatal("synthetic derivation epoch mismatch")
	}
	if err := hashEpoch.Initialize(ctx, tx); err != nil {
		t.Fatal("code hash epoch initialization failed")
	}
	if err := tx.Commit(); err != nil {
		t.Fatal("epoch transaction commit failed")
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
	if _, err := NewStore(nil, nil, nil, "fixture-email-code-hash-v1", Policy{}); !errors.Is(err, ErrChallengeStore) {
		t.Fatal("missing dependencies accepted")
	}
}

func TestServerChallengePolicyValidation(t *testing.T) {
	hasher, _ := domain.NewEmailCodeHasher(bytes.Repeat([]byte{1}, 32))
	replay, _ := repository.NewProofReplayStore(bytes.Repeat([]byte{2}, 32))
	protector, _ := domain.NewEmailIdentityProtector(bytes.Repeat([]byte{3}, 32), bytes.Repeat([]byte{4}, 32), "test-key")
	valid := Policy{ChallengeTTL: 5 * time.Minute, MaxAttempts: 3, DeviceProof: domain.ProofPolicy{MaxLifetime: time.Minute}}
	if _, err := NewStore(hasher, replay, protector, "fixture-email-code-hash-v1", valid); err != nil {
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
		if _, err := NewStore(hasher, replay, protector, "fixture-email-code-hash-v1", policy); !errors.Is(err, ErrChallengeStore) {
			t.Fatal("invalid server policy accepted")
		}
	}
}
