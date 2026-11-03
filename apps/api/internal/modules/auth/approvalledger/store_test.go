package approvalledger

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"
	"nerae/api/internal/modules/auth/approvalreceipt"
)

type syntheticVerifier struct {
	subject string
	session string
	expires time.Time
	token   string
	revoked bool
}

func (v *syntheticVerifier) VerifyCurrentSession(_ context.Context, _ *sql.Tx, token string) (string, string, time.Time, time.Time, error) {
	if v == nil || v.revoked || token != v.token {
		return "", "", time.Time{}, time.Time{}, ErrUnavailable
	}
	return v.subject, v.session, v.expires, v.expires, nil
}

func ledgerDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("NERAE_APPROVAL_LEDGER_TEST_DSN")
	if dsn == "" {
		t.Skip("private approval-ledger MySQL is required")
	}
	cfg, err := mysql.ParseDSN(dsn)
	if err != nil {
		t.Fatal("invalid private ledger DSN")
	}
	parent := filepath.Dir(cfg.Addr)
	resolved, resolveErr := filepath.EvalSymlinks(parent)
	dirInfo, dirErr := os.Lstat(parent)
	socketInfo, socketErr := os.Lstat(cfg.Addr)
	if cfg.Net != "unix" || cfg.DBName != "nerae_approval_ledger_test" || cfg.User != "root" || cfg.Passwd != "" ||
		!cfg.ParseTime || cfg.Loc != time.UTC || filepath.Base(cfg.Addr) != "mysql.sock" || resolveErr != nil ||
		dirErr != nil || socketErr != nil || filepath.Dir(resolved) != "/private/tmp" ||
		!strings.HasPrefix(filepath.Base(resolved), "nerae-approval-ledger-mysql-") || !dirInfo.IsDir() ||
		dirInfo.Mode().Perm()&0077 != 0 || dirInfo.Mode()&os.ModeSymlink != 0 || socketInfo.Mode()&os.ModeSocket == 0 {
		t.Fatal("refusing non-isolated ledger database")
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatal("private ledger database unavailable")
	}
	db.SetMaxOpenConns(12)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestApprovalLedgerPrivateMySQL(t *testing.T) {
	db := ledgerDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	crypto, err := approvalreceipt.New(approvalreceipt.KeyConfig{IdempotencyHMACKey: bytes.Repeat([]byte{1}, 32),
		RequestHMACKey: bytes.Repeat([]byte{2}, 32), AES256GCMKey: bytes.Repeat([]byte{3}, 32)})
	if err != nil {
		t.Fatal("synthetic receipt crypto setup failed")
	}
	store, err := New(crypto, "test-approval-exchange-v1")
	if err != nil {
		t.Fatal("ledger setup failed")
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil || store.Initialize(ctx, tx) != nil {
		if tx != nil {
			_ = tx.Rollback()
		}
		t.Fatal("ledger epoch initialization failed")
	}
	initialized := make(chan error, 1)
	initStarted := make(chan struct{}, 1)
	go func() {
		otherTx, err := db.BeginTx(ctx, nil)
		if err != nil {
			initStarted <- struct{}{}
			initialized <- err
			return
		}
		defer otherTx.Rollback()
		initStarted <- struct{}{}
		if err := store.Initialize(ctx, otherTx); err != nil {
			initialized <- err
			return
		}
		initialized <- otherTx.Commit()
	}()
	<-initStarted
	initDeadline := time.Now().Add(5 * time.Second)
	initBlocked := false
	for time.Now().Before(initDeadline) {
		var waiting int
		if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM performance_schema.data_lock_waits w JOIN performance_schema.data_locks l ON l.ENGINE_LOCK_ID=w.REQUESTING_ENGINE_LOCK_ID WHERE l.OBJECT_SCHEMA=DATABASE() AND l.OBJECT_NAME='auth_approval_exchange_receipt_key_epoch'`).Scan(&waiting); err != nil {
			_ = tx.Rollback()
			t.Fatal("could not observe concurrent epoch initialization")
		}
		if waiting > 0 {
			initBlocked = true
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !initBlocked {
		_ = tx.Rollback()
		t.Fatal("concurrent epoch initialization did not wait")
	}
	if err := tx.Commit(); err != nil {
		t.Fatal("first ledger epoch commit failed")
	}
	if err := <-initialized; err != nil {
		t.Fatal("concurrent ledger initialization failed")
	}
	otherCrypto, _ := approvalreceipt.New(approvalreceipt.KeyConfig{IdempotencyHMACKey: bytes.Repeat([]byte{4}, 32),
		RequestHMACKey: bytes.Repeat([]byte{5}, 32), AES256GCMKey: bytes.Repeat([]byte{6}, 32)})
	otherStore, _ := New(otherCrypto, "test-approval-exchange-v1")
	wrongTx, err := db.BeginTx(ctx, nil)
	if err != nil || otherStore.Initialize(ctx, wrongTx) == nil {
		if wrongTx != nil {
			_ = wrongTx.Rollback()
		}
		t.Fatal("mismatched receipt key epoch was accepted")
	}
	_ = wrongTx.Rollback()

	enc := base64.RawURLEncoding.EncodeToString
	request := approvalreceipt.Request{IdempotencyKey: enc(bytes.Repeat([]byte{7}, 16)), ApprovalID: enc(bytes.Repeat([]byte{8}, 24)),
		ApprovalSecret: enc(bytes.Repeat([]byte{9}, 32)), DeviceProof: enc([]byte("header")) + "." + enc([]byte("payload")) + "." + enc(bytes.Repeat([]byte{10}, 64))}
	binding, err := crypto.Summarize(request)
	if err != nil {
		t.Fatal("synthetic request binding failed")
	}
	verifier := &syntheticVerifier{subject: "101", session: enc(bytes.Repeat([]byte{11}, 24)),
		expires: time.Now().UTC().Add(time.Hour).Truncate(time.Microsecond), token: "synthetic-session-token-for-ledger-testing"}
	expires := time.Now().UTC().Add(5 * time.Minute).Truncate(time.Microsecond)
	makeSealed := func(tx *sql.Tx, b approvalreceipt.Binding, subject string, v *syntheticVerifier, expiry time.Time) approvalreceipt.Sealed {
		t.Helper()
		subjectScope, err := crypto.SubjectScope(subject)
		if err != nil {
			t.Fatal("subject scope setup failed")
		}
		approvalScope, err := crypto.ApprovalScope(b.ApprovalID())
		if err != nil {
			t.Fatal("approval scope setup failed")
		}
		receiptContext, err := crypto.NewContext("test-approval-exchange-v1", subjectScope, approvalScope, b, "issued_session", v.session, expiry)
		if err != nil {
			t.Fatal("receipt context setup failed")
		}
		now, err := approvalreceipt.ReadDatabaseUTC(ctx, tx)
		if err != nil {
			t.Fatal("database clock unavailable")
		}
		sealed, err := crypto.Seal(ctx, receiptContext, v.token, now, tx, v)
		if err != nil {
			t.Fatal("synthetic receipt sealing failed")
		}
		return sealed
	}
	rollbackTx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal("rollback transaction unavailable")
	}
	handle, err := store.Lock(ctx, rollbackTx, verifier.subject, binding)
	if err != nil || handle.Exists() {
		t.Fatal("new receipt was not empty")
	}
	sealed := makeSealed(rollbackTx, binding, verifier.subject, verifier, expires)
	if err := handle.Put(ctx, rollbackTx, verifier.session, expires, sealed, verifier); err != nil {
		t.Fatal("receipt insert failed")
	}
	if err := handle.Put(ctx, rollbackTx, verifier.session, expires, sealed, verifier); err == nil {
		t.Fatal("handle accepted a second insert")
	}
	if err := rollbackTx.Rollback(); err != nil {
		t.Fatal("receipt rollback failed")
	}
	var count int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM auth_approval_exchange_receipt`).Scan(&count); err != nil || count != 0 {
		t.Fatal("rolled-back receipt survived")
	}

	writeTx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal("receipt write transaction unavailable")
	}
	handle, err = store.Lock(ctx, writeTx, verifier.subject, binding)
	if err != nil || handle.Exists() {
		t.Fatal("receipt reservation failed")
	}
	sealed = makeSealed(writeTx, binding, verifier.subject, verifier, expires)
	if err := handle.Put(ctx, writeTx, verifier.session, expires, sealed, verifier); err != nil || writeTx.Commit() != nil {
		t.Fatal("receipt commit failed")
	}
	missingEpochTx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal("missing-epoch transaction unavailable")
	}
	if _, err := missingEpochTx.ExecContext(ctx, `DELETE FROM auth_approval_exchange_receipt_key_epoch WHERE slot=1`); err != nil {
		_ = missingEpochTx.Rollback()
		t.Fatal("could not simulate missing epoch")
	}
	if err := store.Initialize(ctx, missingEpochTx); err == nil {
		_ = missingEpochTx.Rollback()
		t.Fatal("ledger adopted existing history without its epoch")
	}
	if err := missingEpochTx.Rollback(); err != nil {
		t.Fatal("missing-epoch rollback failed")
	}
	if _, err := handle.Open(ctx, writeTx, verifier); err == nil {
		t.Fatal("ended transaction opened a receipt")
	}
	var encrypted []byte
	if err := db.QueryRowContext(ctx, `SELECT response_enc FROM auth_approval_exchange_receipt`).Scan(&encrypted); err != nil || bytes.Contains(encrypted, []byte(verifier.token)) {
		t.Fatal("receipt token was not encrypted at rest")
	}
	replayTx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal("replay transaction unavailable")
	}
	handle, err = store.Lock(ctx, replayTx, verifier.subject, binding)
	if err != nil || !handle.Exists() {
		t.Fatal("exact receipt replay was not found")
	}
	plain, err := handle.Open(ctx, replayTx, verifier)
	if err != nil || string(plain.Bytes()) != verifier.token {
		t.Fatal("exact receipt did not decrypt")
	}
	var nilVerifier *syntheticVerifier
	if plain, err := handle.Open(ctx, replayTx, nilVerifier); err == nil || len(plain.Bytes()) != 0 {
		t.Fatal("typed-nil session verifier opened a receipt")
	}
	verifier.revoked = true
	if plain, err := handle.Open(ctx, replayTx, verifier); err == nil || len(plain.Bytes()) != 0 {
		t.Fatal("revoked session opened a receipt")
	}
	verifier.revoked = false
	tampered := append([]byte(nil), encrypted...)
	tampered[len(tampered)-1] ^= 1
	if _, err := replayTx.ExecContext(ctx, `UPDATE auth_approval_exchange_receipt SET response_enc=?`, tampered); err != nil {
		t.Fatal("could not corrupt synthetic ciphertext")
	}
	if plain, err := handle.Open(ctx, replayTx, verifier); err == nil || len(plain.Bytes()) != 0 {
		t.Fatal("tampered ciphertext opened a receipt")
	}
	if err := replayTx.Rollback(); err != nil {
		t.Fatal("replay cleanup failed")
	}

	changed := request
	changed.ApprovalSecret = enc(bytes.Repeat([]byte{12}, 32))
	changedBinding, _ := crypto.Summarize(changed)
	checkTx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal("conflict transaction unavailable")
	}
	if _, err := store.Lock(ctx, checkTx, verifier.subject, changedBinding); !errors.Is(err, ErrConflict) {
		t.Fatal("changed request did not conflict")
	}
	changedKey := request
	changedKey.IdempotencyKey = enc(bytes.Repeat([]byte{13}, 16))
	changedKeyBinding, err := crypto.Summarize(changedKey)
	if err != nil {
		t.Fatal("changed key binding failed")
	}
	if _, err := store.Lock(ctx, checkTx, verifier.subject, changedKeyBinding); !errors.Is(err, ErrConflict) {
		t.Fatal("a second key could reserve the same approval")
	}
	if _, err := store.Lock(ctx, checkTx, "202", binding); !errors.Is(err, ErrUnavailable) {
		t.Fatal("another subject accessed receipt metadata")
	}
	if err := checkTx.Rollback(); err != nil {
		t.Fatal("conflict cleanup failed")
	}

	// A concurrent exact retry waits for the first transaction and sees the
	// same committed ciphertext. It cannot create a second approval receipt.
	concurrentRequest := request
	concurrentRequest.ApprovalID = enc(bytes.Repeat([]byte{14}, 24))
	concurrentBinding, err := crypto.Summarize(concurrentRequest)
	if err != nil {
		t.Fatal("concurrent binding failed")
	}
	firstTx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal("concurrent writer unavailable")
	}
	firstHandle, err := store.Lock(ctx, firstTx, verifier.subject, concurrentBinding)
	if err != nil || firstHandle.Exists() {
		_ = firstTx.Rollback()
		t.Fatal("concurrent writer reservation failed")
	}
	concurrentSealed := makeSealed(firstTx, concurrentBinding, verifier.subject, verifier, expires)
	if err := firstHandle.Put(ctx, firstTx, verifier.session, expires, concurrentSealed, verifier); err != nil {
		_ = firstTx.Rollback()
		t.Fatal("concurrent writer insert failed")
	}
	concurrent := make(chan error, 1)
	go func() {
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			concurrent <- err
			return
		}
		defer tx.Rollback()
		other, err := store.Lock(ctx, tx, verifier.subject, concurrentBinding)
		if err != nil || !other.Exists() {
			concurrent <- errors.New("receipt missing after concurrent commit")
			return
		}
		plaintext, err := other.Open(ctx, tx, verifier)
		if err != nil || string(plaintext.Bytes()) != verifier.token {
			concurrent <- errors.New("concurrent receipt did not open")
			return
		}
		concurrent <- nil
	}()
	deadline := time.Now().Add(5 * time.Second)
	blocked := false
	for time.Now().Before(deadline) {
		var waiting int
		if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM performance_schema.data_lock_waits w JOIN performance_schema.data_locks l ON l.ENGINE_LOCK_ID=w.REQUESTING_ENGINE_LOCK_ID WHERE l.OBJECT_SCHEMA=DATABASE() AND l.OBJECT_NAME='auth_approval_exchange_receipt'`).Scan(&waiting); err != nil {
			_ = firstTx.Rollback()
			t.Fatal("could not observe concurrent ledger lock")
		}
		if waiting > 0 {
			blocked = true
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !blocked {
		_ = firstTx.Rollback()
		t.Fatal("concurrent receipt lookup did not wait")
	}
	if err := firstTx.Commit(); err != nil {
		t.Fatal("concurrent writer commit failed")
	}
	if err := <-concurrent; err != nil {
		t.Fatal("concurrent exact receipt replay failed")
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM auth_approval_exchange_receipt`).Scan(&count); err != nil || count != 2 {
		t.Fatal("concurrent replay changed receipt count")
	}

	// A receipt that expires while a replay waits on its row lock cannot open.
	waitRequest := request
	waitRequest.ApprovalID = enc(bytes.Repeat([]byte{15}, 24))
	waitBinding, err := crypto.Summarize(waitRequest)
	if err != nil {
		t.Fatal("expiry-wait binding failed")
	}
	waitExpiry := time.Now().UTC().Add(4 * time.Second).Truncate(time.Microsecond)
	insertTx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal("expiry-wait insert transaction unavailable")
	}
	waitHandle, err := store.Lock(ctx, insertTx, verifier.subject, waitBinding)
	if err != nil {
		_ = insertTx.Rollback()
		t.Fatal("expiry-wait reservation failed")
	}
	waitSealed := makeSealed(insertTx, waitBinding, verifier.subject, verifier, waitExpiry)
	if err := waitHandle.Put(ctx, insertTx, verifier.session, waitExpiry, waitSealed, verifier); err != nil {
		_ = insertTx.Rollback()
		t.Fatal("expiry-wait receipt insert failed")
	}
	if err := insertTx.Commit(); err != nil {
		t.Fatal("expiry-wait receipt commit failed")
	}
	gateTx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal("expiry-wait gate unavailable")
	}
	scope, err := crypto.ApprovalScope(waitRequest.ApprovalID)
	if err != nil {
		_ = gateTx.Rollback()
		t.Fatal("expiry-wait scope failed")
	}
	scopeDigest := scope.Digest()
	var locked []byte
	if err := gateTx.QueryRowContext(ctx, `SELECT approval_scope FROM auth_approval_exchange_receipt WHERE approval_scope=? FOR UPDATE`, scopeDigest[:]).Scan(&locked); err != nil {
		_ = gateTx.Rollback()
		t.Fatal("could not lock expiry-wait receipt")
	}
	waitResult := make(chan error, 1)
	go func() {
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			waitResult <- err
			return
		}
		defer tx.Rollback()
		_, err = store.Lock(ctx, tx, verifier.subject, waitBinding)
		waitResult <- err
	}()
	deadline = time.Now().Add(5 * time.Second)
	blocked = false
	for time.Now().Before(deadline) {
		var waiting int
		if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM performance_schema.data_lock_waits w JOIN performance_schema.data_locks l ON l.ENGINE_LOCK_ID=w.REQUESTING_ENGINE_LOCK_ID WHERE l.OBJECT_SCHEMA=DATABASE() AND l.OBJECT_NAME='auth_approval_exchange_receipt'`).Scan(&waiting); err != nil {
			_ = gateTx.Rollback()
			t.Fatal("could not observe expiry-wait receipt lock")
		}
		if waiting > 0 {
			blocked = true
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !blocked {
		_ = gateTx.Rollback()
		t.Fatal("receipt replay did not reach the expiry lock")
	}
	if delay := time.Until(waitExpiry) + 50*time.Millisecond; delay > 0 {
		time.Sleep(delay)
	}
	if err := gateTx.Rollback(); err != nil {
		t.Fatal("could not release expiry-wait gate")
	}
	if err := <-waitResult; !errors.Is(err, ErrExpired) {
		t.Fatal("receipt replay ignored expiry during lock wait")
	}

	if _, err := db.ExecContext(ctx, `CREATE TABLE synthetic_exchange_mutation (id INT NOT NULL PRIMARY KEY) ENGINE=InnoDB`); err != nil {
		t.Fatal("could not create synthetic transaction fixture")
	}
	if _, err := db.ExecContext(ctx, `CREATE TRIGGER synthetic_receipt_failure BEFORE INSERT ON auth_approval_exchange_receipt FOR EACH ROW SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='synthetic receipt failure'`); err != nil {
		t.Fatal("could not create synthetic receipt failure")
	}
	defer func() {
		_, _ = db.ExecContext(context.Background(), `DROP TRIGGER IF EXISTS synthetic_receipt_failure`)
	}()
	faultRequest := request
	faultRequest.ApprovalID = enc(bytes.Repeat([]byte{16}, 24))
	faultBinding, err := crypto.Summarize(faultRequest)
	if err != nil {
		t.Fatal("fault binding failed")
	}
	faultTx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal("fault transaction unavailable")
	}
	faultHandle, err := store.Lock(ctx, faultTx, verifier.subject, faultBinding)
	if err != nil {
		_ = faultTx.Rollback()
		t.Fatal("fault reservation failed")
	}
	if _, err := faultTx.ExecContext(ctx, `INSERT INTO synthetic_exchange_mutation (id) VALUES (1)`); err != nil {
		_ = faultTx.Rollback()
		t.Fatal("synthetic exchange mutation failed")
	}
	faultSealed := makeSealed(faultTx, faultBinding, verifier.subject, verifier, expires)
	if err := faultHandle.Put(ctx, faultTx, verifier.session, expires, faultSealed, verifier); err == nil {
		_ = faultTx.Rollback()
		t.Fatal("receipt insert fault was reported as success")
	}
	if err := faultTx.Rollback(); err != nil {
		t.Fatal("fault transaction rollback failed")
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM synthetic_exchange_mutation`).Scan(&count); err != nil || count != 0 {
		t.Fatal("synthetic exchange mutation survived receipt failure rollback")
	}
	if _, err := db.ExecContext(ctx, `DROP TRIGGER synthetic_receipt_failure`); err != nil {
		t.Fatal("could not remove synthetic receipt failure")
	}

	if _, err := db.ExecContext(ctx, `UPDATE auth_approval_exchange_receipt SET created_at=DATE_SUB(UTC_TIMESTAMP(6), INTERVAL 2 HOUR), expires_at=DATE_SUB(UTC_TIMESTAMP(6), INTERVAL 1 HOUR)`); err != nil {
		t.Fatal("could not expire test receipt")
	}
	expiredTx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal("expiry transaction unavailable")
	}
	if _, err := store.Lock(ctx, expiredTx, verifier.subject, binding); !errors.Is(err, ErrExpired) {
		t.Fatal("expired receipt was replayed")
	}
	if err := expiredTx.Rollback(); err != nil {
		t.Fatal("expiry cleanup failed")
	}
	if fmt.Sprint(handle) != "ApprovalExchangeReceiptHandle{redacted}" {
		t.Fatal("handle was not redacted")
	}
}
