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
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"ES256","typ":"JWT"}`))
	claims, err := json.Marshal(map[string]any{"aud": "nerae-auth-device", "purpose": "email_login", "resourceId": challengeID,
		"nonce": nonce, "iat": now.Unix(), "exp": now.Add(time.Minute).Unix(), "jti": jti})
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
	store, err := NewStore(hasher, replay)
	if err != nil {
		t.Fatal(err)
	}
	private, public := testKey(t)
	now := time.Now().UTC().Truncate(time.Second)
	index := [32]byte{9}
	nonce := strings.Repeat("N", 43)
	issue := func(id string, attempts int) {
		t.Helper()
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		err = store.Issue(ctx, tx, IssueInput{EmailIndex: index, ChallengeID: id, Nonce: nonce, Code: "012345",
			PublicKey: public, CreatedAt: now, ExpiresAt: now.Add(5 * time.Minute), MaxAttempts: attempts})
		if err != nil {
			t.Fatalf("failed to issue challenge: %v", err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatalf("failed to commit challenge: %v", err)
		}
	}
	attempt := func(id, code, proof string, when time.Time, commit bool) (AttemptResult, error) {
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return AttemptResult{}, err
		}
		defer tx.Rollback()
		result, err := store.Attempt(ctx, tx, AttemptInput{ChallengeID: id, Code: code, DeviceProof: proof, Now: when,
			ProofPolicy: domain.ProofPolicy{MaxLifetime: 5 * time.Minute, ClockSkew: 30 * time.Second}})
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

	first := strings.Repeat("A", 32)
	issue(first, 2)
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

	second := strings.Repeat("B", 32)
	issue(second, 3)
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
	issue(third, 3)
	rollbackProof := testProof(t, private, third, nonce, "rollback-proof", now)
	result, err = attempt(third, "012345", rollbackProof, now, false)
	if err != nil || !result.Verified {
		t.Fatalf("initial verified attempt: %v", err)
	}
	result, err = attempt(third, "012345", rollbackProof, now, true)
	if err != nil || !result.Verified || result.EmailIndex != index {
		t.Fatalf("attempt after rollback: %v", err)
	}

	expired := strings.Repeat("D", 32)
	issue(expired, 3)
	result, err = attempt(expired, "012345", testProof(t, private, expired, nonce, "expired-proof", now), now.Add(5*time.Minute), true)
	assertRejected(result, err, false)
	var digest []byte
	if err := db.QueryRowContext(ctx, `SELECT code_digest FROM auth_email_login_challenge WHERE challenge_id = ?`, expired).Scan(&digest); err != nil || len(digest) != 32 || bytes.Contains(digest, []byte("012345")) {
		t.Fatal("stored code digest invalid or exposed test code")
	}

	index = [32]byte{10} // Exercise concurrent creation of a previously absent slot.
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
			err = store.Issue(ctx, tx, IssueInput{EmailIndex: index, ChallengeID: id, Nonce: nonce, Code: "012345",
				PublicKey: public, CreatedAt: now, ExpiresAt: now.Add(5 * time.Minute), MaxAttempts: 3})
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

	index = [32]byte{11}
	oldID, newID := strings.Repeat("G", 32), strings.Repeat("H", 32)
	issue(oldID, 3)
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Issue(ctx, tx, IssueInput{EmailIndex: index, ChallengeID: newID, Nonce: nonce, Code: "012345",
		PublicKey: public, CreatedAt: now, ExpiresAt: now.Add(5 * time.Minute), MaxAttempts: 3}); err != nil {
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
	index = [32]byte{12}
	timezoneID := strings.Repeat("J", 32)
	tx, err = otherDB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Issue(ctx, tx, IssueInput{EmailIndex: index, ChallengeID: timezoneID, Nonce: nonce, Code: "012345",
		PublicKey: public, CreatedAt: now, ExpiresAt: now.Add(5 * time.Minute), MaxAttempts: 3}); err != nil {
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
	if err := store.Issue(ctx, tx, IssueInput{EmailIndex: index, ChallengeID: timezoneValid, Nonce: nonce, Code: "012345",
		PublicKey: public, CreatedAt: now, ExpiresAt: now.Add(5 * time.Minute), MaxAttempts: 3}); err != nil {
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
}

func TestChallengeInputFormattingIsRedacted(t *testing.T) {
	input := IssueInput{Code: "012345", Nonce: "private-nonce"}
	attempt := AttemptInput{Code: "012345", DeviceProof: "private-proof"}
	for _, value := range []string{fmt.Sprintf("%+v", input), fmt.Sprintf("%#v", input), fmt.Sprintf("%+v", attempt), fmt.Sprintf("%#v", attempt)} {
		if strings.Contains(value, "012345") || strings.Contains(value, "private") {
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
	if strings.Contains(log.String(), "012345") || strings.Contains(log.String(), "private") {
		t.Fatal("structured logging exposed challenge secrets")
	}
	if _, err := NewStore(nil, nil); !errors.Is(err, ErrChallengeStore) {
		t.Fatal("missing dependencies accepted")
	}
}
