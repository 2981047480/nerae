package repository

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"

	"nerae/api/internal/modules/auth/domain"
)

func TestProofReplayStoreKeyOwnership(t *testing.T) {
	if _, err := NewProofReplayStore(make([]byte, 31)); !errors.Is(err, ErrReplayInput) {
		t.Fatalf("short key error = %v", err)
	}
	key := bytes.Repeat([]byte{1}, 32)
	store, err := NewProofReplayStore(key)
	if err != nil {
		t.Fatal(err)
	}
	fingerprint := [32]byte{7}
	before := store.jtiDigest(fingerprint, "proof")
	key[0] = 2
	if after := store.jtiDigest(fingerprint, "proof"); before != after {
		t.Fatal("mutating the caller's key changed the store")
	}
	if before == store.jtiDigest([32]byte{8}, "proof") || before == store.jtiDigest(fingerprint, "other") {
		t.Fatal("proof digest must bind both key fingerprint and JTI")
	}
}

// Refuse every target except the private Unix socket created by the dedicated
// local test harness. These tests must never modify a developer or remote DB.
func openPrivateTestDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("NERAE_PROOF_TEST_DSN")
	if dsn == "" {
		t.Skip("set NERAE_PROOF_TEST_DSN using the isolated MySQL test harness")
	}
	cfg, err := mysql.ParseDSN(dsn)
	if err != nil {
		t.Fatal("invalid isolated test DSN")
	}
	socket := filepath.Clean(cfg.Addr)
	parent := filepath.Dir(socket)
	resolvedParent, err := filepath.EvalSymlinks(parent)
	info, statErr := os.Lstat(parent)
	socketInfo, socketErr := os.Lstat(socket)
	if err != nil || statErr != nil || socketErr != nil || cfg.Net != "unix" || cfg.DBName != "nerae_proof_test" ||
		cfg.User != "root" || cfg.Passwd != "" || !cfg.ParseTime || cfg.Loc != time.UTC ||
		filepath.Base(socket) != "mysql.sock" || filepath.Dir(resolvedParent) != "/private/tmp" ||
		!strings.HasPrefix(filepath.Base(resolvedParent), "nerae-proof-mysql-") ||
		!info.IsDir() || info.Mode().Perm()&0077 != 0 || info.Mode()&os.ModeSymlink != 0 ||
		socketInfo.Mode()&os.ModeSocket == 0 {
		t.Fatal("refusing non-isolated MySQL test target")
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatal("failed to open isolated MySQL")
	}
	db.SetMaxOpenConns(4)
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Ping(); err != nil {
		t.Fatal("failed to connect to isolated MySQL")
	}
	return db
}

func TestProofReplayTransactions(t *testing.T) {
	db := openPrivateTestDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	store, err := NewProofReplayStore(bytes.Repeat([]byte{1}, 32))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	newProof := func(jti string) domain.VerifiedDeviceProof {
		expires := now.Add(time.Minute)
		return domain.VerifiedDeviceProof{JTI: jti, ExpiresAt: expires, ReplayUntil: domain.DeviceProofReplayDeadline(expires)}
	}
	fingerprint := [32]byte{1}
	consume := func(s *ProofReplayStore, key [32]byte, proof domain.VerifiedDeviceProof, commit bool) error {
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer tx.Rollback()
		if err := s.Consume(ctx, tx, key, proof, now); err != nil {
			return err
		}
		if commit {
			return tx.Commit()
		}
		return nil
	}

	ensure := func(s *ProofReplayStore, key [32]byte, proof domain.VerifiedDeviceProof) error {
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer tx.Rollback()
		return s.EnsureConsumed(ctx, tx, key, proof, now)
	}
	if err := ensure(store, fingerprint, newProof("before-initialization")); !errors.Is(err, ErrReplayStore) {
		t.Fatal("marker lookup initialized a missing key epoch")
	}
	var beforeEpoch int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM auth_device_proof_key_epoch`).Scan(&beforeEpoch); err != nil || beforeEpoch != 0 {
		t.Fatal("marker lookup modified missing key epoch")
	}

	// A stale consistent-read snapshot must not hide marker history when the
	// epoch row is missing. The guard uses a locking/current read.
	stale, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal("cannot begin stale-snapshot transaction")
	}
	var before int
	if err := stale.QueryRowContext(ctx, `SELECT COUNT(*) FROM auth_device_proof_replay`).Scan(&before); err != nil || before != 0 {
		_ = stale.Rollback()
		t.Fatal("expected empty replay table")
	}
	syntheticFingerprint := bytes.Repeat([]byte{0x73}, 32)
	syntheticDigest := bytes.Repeat([]byte{0x74}, 32)
	if _, err := db.ExecContext(ctx, `INSERT INTO auth_device_proof_replay (device_fingerprint,jti_digest,retain_until,created_at) VALUES (?,?,?,?)`, syntheticFingerprint, syntheticDigest, utcSQL(now.Add(time.Minute)), utcSQL(now)); err != nil {
		_ = stale.Rollback()
		t.Fatal("cannot insert synthetic marker")
	}
	if err := store.Consume(ctx, stale, fingerprint, newProof("snapshot-guard"), now); !errors.Is(err, ErrReplayStore) {
		_ = stale.Rollback()
		t.Fatalf("stale snapshot allowed initialization with marker history: %v", err)
	}
	_ = stale.Rollback()
	if _, err := db.ExecContext(ctx, `DELETE FROM auth_device_proof_replay WHERE device_fingerprint=? AND jti_digest=?`, syntheticFingerprint, syntheticDigest); err != nil {
		t.Fatal("cannot remove synthetic marker")
	}

	var start sync.WaitGroup
	start.Add(1)
	initial := make(chan error, 2)
	for _, id := range []string{"initialize-a", "initialize-b"} {
		go func(id string) {
			start.Wait()
			initial <- consume(store, fingerprint, newProof(id), true)
		}(id)
	}
	start.Done()
	initialA, initialB := <-initial, <-initial
	if (initialA != nil && !errors.Is(initialA, ErrReplayStore)) ||
		(initialB != nil && !errors.Is(initialB, ErrReplayStore)) || (initialA != nil && initialB != nil) {
		t.Fatalf("concurrent epoch initialization failed closed unexpectedly: (%v, %v)", initialA, initialB)
	}
	var epochCount int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM auth_device_proof_key_epoch`).Scan(&epochCount); err != nil || epochCount != 1 {
		t.Fatal("expected exactly one replay key epoch")
	}

	start.Add(1)
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() {
			start.Wait()
			results <- consume(store, fingerprint, newProof("concurrent"), true)
		}()
	}
	start.Done()
	first, second := <-results, <-results
	if !((first == nil && errors.Is(second, ErrProofAlreadyUsed)) ||
		(second == nil && errors.Is(first, ErrProofAlreadyUsed))) {
		t.Fatalf("concurrent consumption = (%v, %v), want one success and one duplicate", first, second)
	}
	if err := ensure(store, fingerprint, newProof("concurrent")); err != nil {
		t.Fatal("committed proof marker was not found")
	}
	if err := ensure(store, fingerprint, newProof("not-consumed")); !errors.Is(err, ErrProofNotConsumed) {
		t.Fatal("unconsumed proof marker was accepted")
	}
	if err := ensure(store, [32]byte{3}, newProof("concurrent")); !errors.Is(err, ErrProofNotConsumed) {
		t.Fatal("marker was not bound to device fingerprint")
	}
	if err := consume(store, [32]byte{2}, newProof("concurrent"), true); err != nil {
		t.Fatalf("same JTI on another key: %v", err)
	}
	if err := consume(store, fingerprint, newProof("rollback"), false); err != nil {
		t.Fatalf("first rollback: %v", err)
	}
	if err := ensure(store, fingerprint, newProof("rollback")); !errors.Is(err, ErrProofNotConsumed) {
		t.Fatal("rolled-back proof marker was visible")
	}
	if err := consume(store, fingerprint, newProof("rollback"), true); err != nil {
		t.Fatalf("retry after rollback: %v", err)
	}

	other, _ := NewProofReplayStore(bytes.Repeat([]byte{2}, 32))
	if err := ensure(other, fingerprint, newProof("concurrent")); !errors.Is(err, ErrReplayKeyMismatch) {
		t.Fatal("different epoch key was accepted during marker lookup")
	}
	if err := consume(other, fingerprint, newProof("wrong-key"), true); !errors.Is(err, ErrReplayKeyMismatch) {
		t.Fatalf("unexpected epoch-key result: %v", err)
	}
	tooShort := newProof("too-short")
	tooShort.ReplayUntil = tooShort.ExpiresAt.Add(time.Minute)
	if err := consume(store, fingerprint, tooShort, true); !errors.Is(err, ErrReplayInput) {
		t.Fatalf("short retention accepted: %v", err)
	}
	tooLong := newProof("too-long")
	tooLong.ReplayUntil = tooLong.ReplayUntil.Add(time.Second)
	if err := consume(store, fingerprint, tooLong, true); !errors.Is(err, ErrReplayInput) {
		t.Fatalf("long retention accepted: %v", err)
	}
	latest := newProof("latest-valid")
	latest.ExpiresAt = domain.DeviceProofLatestExpiry(now)
	latest.ReplayUntil = domain.DeviceProofReplayDeadline(latest.ExpiresAt)
	if err := consume(store, fingerprint, latest, true); err != nil {
		t.Fatalf("maximum permitted expiry rejected: %v", err)
	}
	farFuture := newProof("far-future")
	farFuture.ExpiresAt = domain.DeviceProofLatestExpiry(now).Add(time.Nanosecond)
	farFuture.ReplayUntil = domain.DeviceProofReplayDeadline(farFuture.ExpiresAt)
	if err := consume(store, fingerprint, farFuture, true); !errors.Is(err, ErrReplayInput) {
		t.Fatalf("far-future expiry accepted: %v", err)
	}
	expired := newProof("expired")
	expired.ExpiresAt = now.Add(-10 * time.Minute)
	expired.ReplayUntil = domain.DeviceProofReplayDeadline(expired.ExpiresAt)
	if err := consume(store, fingerprint, expired, true); !errors.Is(err, ErrReplayInput) {
		t.Fatalf("expired retention accepted: %v", err)
	}

	fractional := newProof("fractional-secret-jti")
	fractional.ExpiresAt = fractional.ExpiresAt.Add(123456789 * time.Nanosecond)
	fractional.ReplayUntil = domain.DeviceProofReplayDeadline(fractional.ExpiresAt)
	if err := consume(store, fingerprint, fractional, true); err != nil {
		t.Fatalf("fractional retention: %v", err)
	}
	digest := store.jtiDigest(fingerprint, fractional.JTI)
	var retained time.Time
	var savedDigest []byte
	if err := db.QueryRowContext(ctx, `SELECT retain_until, jti_digest FROM auth_device_proof_replay WHERE device_fingerprint = ? AND jti_digest = ?`, fingerprint[:], digest[:]).Scan(&retained, &savedDigest); err != nil {
		t.Fatal("failed to read isolated retention marker")
	}
	want := fractional.ReplayUntil.Truncate(time.Microsecond).Add(time.Microsecond)
	if !retained.Equal(want) || retained.Before(fractional.ReplayUntil) {
		t.Fatalf("retention %v, want %v", retained, want)
	}
	if bytes.Contains(savedDigest, []byte(fractional.JTI)) || len(savedDigest) != 32 {
		t.Fatal("stored marker exposed raw JTI or had an unexpected digest length")
	}
	if err := ensure(store, fingerprint, fractional); err != nil {
		t.Fatal("fractional retention marker was not accepted")
	}
	if _, err := db.ExecContext(ctx, `UPDATE auth_device_proof_replay SET retain_until=? WHERE device_fingerprint=? AND jti_digest=?`, utcSQL(fractional.ReplayUntil.Add(-time.Second)), fingerprint[:], digest[:]); err != nil {
		t.Fatal("cannot create short-retention fixture")
	}
	if err := ensure(store, fingerprint, fractional); !errors.Is(err, ErrReplayStore) {
		t.Fatal("shortened replay retention was accepted")
	}
	if _, err := db.ExecContext(ctx, `UPDATE auth_device_proof_replay SET retain_until=? WHERE device_fingerprint=? AND jti_digest=?`, utcSQL(want), fingerprint[:], digest[:]); err != nil {
		t.Fatal("cannot restore synthetic retention marker")
	}

	// A connection configured with another driver location must write the
	// same UTC deadline that a different service instance will read.
	otherCfg, _ := mysql.ParseDSN(os.Getenv("NERAE_PROOF_TEST_DSN"))
	otherLocation, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	otherCfg.Loc = otherLocation
	otherDB, err := sql.Open("mysql", otherCfg.FormatDSN())
	if err != nil {
		t.Fatal("cannot open alternate-location test connection")
	}
	defer otherDB.Close()
	tx, err := otherDB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	timezoneProof := newProof("timezone-proof")
	if err := store.Consume(ctx, tx, fingerprint, timezoneProof, now); err != nil {
		_ = tx.Rollback()
		t.Fatalf("consume with alternate driver location: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal("cannot commit alternate-location proof")
	}
	timezoneDigest := store.jtiDigest(fingerprint, timezoneProof.JTI)
	if err := db.QueryRowContext(ctx, `SELECT retain_until FROM auth_device_proof_replay WHERE device_fingerprint = ? AND jti_digest = ?`, fingerprint[:], timezoneDigest[:]).Scan(&retained); err != nil || !retained.Equal(timezoneProof.ReplayUntil) {
		t.Fatal("driver location changed replay retention")
	}

	// Simulate accidental loss of the singleton while a consumed proof is
	// still retained. Neither the old key nor a changed key can reinitialize
	// it or make the same proof consumable again.
	if _, err := db.ExecContext(ctx, `DELETE FROM auth_device_proof_key_epoch WHERE slot=1`); err != nil {
		t.Fatal("cannot simulate missing epoch")
	}
	for _, candidate := range []*ProofReplayStore{store, other} {
		if err := consume(candidate, fingerprint, newProof("concurrent"), true); !errors.Is(err, ErrReplayStore) {
			t.Fatalf("missing epoch with replay history did not fail closed: %v", err)
		}
	}
	if err := ensure(store, fingerprint, newProof("concurrent")); !errors.Is(err, ErrReplayStore) {
		t.Fatal("marker lookup accepted a missing key epoch")
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM auth_device_proof_key_epoch`).Scan(&epochCount); err != nil || epochCount != 0 {
		t.Fatal("missing epoch was recreated despite marker history")
	}
}
