package loginledger

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"
	"nerae/api/internal/modules/auth/domain"
	"nerae/api/internal/modules/auth/loginreceipt"
)

func testCrypto(t *testing.T) *loginreceipt.Crypto {
	t.Helper()
	keys := [][]byte{bytes32(1), bytes32(2), bytes32(3)}
	c, err := loginreceipt.New(loginreceipt.KeyConfig{IdempotencyHMACKey: keys[0], RequestHMACKey: keys[1], AES256GCMKey: keys[2]})
	if err != nil {
		t.Fatal("crypto setup failed")
	}
	return c
}

func bytes32(seed byte) []byte {
	out := make([]byte, 32)
	for i := range out {
		out[i] = seed + byte(i)
	}
	return out
}

func TestNewStoreFailsClosed(t *testing.T) {
	if _, err := NewStore(nil, nil, "key-1"); !errors.Is(err, ErrLedger) {
		t.Fatal("nil configuration accepted")
	}
	identities, err := domain.NewEmailIdentityProtector(bytes32(4), bytes32(5), "identity-1")
	if err != nil {
		t.Fatal("identity setup failed")
	}
	if _, err := NewStore(testCrypto(t), identities, "bad/key"); !errors.Is(err, ErrLedger) {
		t.Fatal("invalid key ID accepted")
	}
	if got := fmt.Sprint(Store{}); !strings.Contains(got, "redacted") {
		t.Fatal("store formatting is not redacted")
	}
}

func TestPrivateMySQLReceiptLedger(t *testing.T) {
	dsn := os.Getenv("NERAE_EMAIL_LOGIN_LEDGER_TEST_DSN")
	if dsn == "" {
		t.Skip("private A01-21 MySQL harness required")
	}
	cfg, err := mysql.ParseDSN(dsn)
	if err != nil || cfg.Net != "unix" || cfg.User != "root" || cfg.Passwd != "" || cfg.DBName != "nerae_email_login_ledger_test" ||
		!cfg.ParseTime || cfg.Loc != time.UTC || cfg.Addr == "" {
		t.Fatal("test DSN is outside the private socket-only fixture")
	}
	socket := cfg.Addr
	parent := filepath.Dir(socket)
	info, err := os.Lstat(parent)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm() != 0700 ||
		filepath.Base(socket) != "mysql.sock" || !strings.HasPrefix(filepath.Base(parent), "nerae-a01-21-mysql-") ||
		filepath.Clean(parent) != filepath.Clean("/private/tmp/"+filepath.Base(parent)) {
		t.Fatal("test socket is outside a private A01-21 directory")
	}
	if !strings.HasPrefix(filepath.Clean(parent), "/private/tmp/nerae-a01-21-mysql-") {
		t.Fatal("test socket path is not under /private/tmp")
	}
	socketInfo, err := os.Lstat(socket)
	if err != nil || socketInfo.Mode()&os.ModeSocket == 0 || socketInfo.Mode()&os.ModeSymlink != 0 {
		t.Fatal("test socket is missing or unsafe")
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatal("private test DB open failed")
	}
	defer db.Close()
	db.SetMaxOpenConns(8)
	ctx := context.Background()
	identities, err := domain.NewEmailIdentityProtector(bytes32(7), bytes32(8), "identity-1")
	if err != nil {
		t.Fatal("identity setup failed")
	}
	crypto := testCrypto(t)
	store, err := NewStore(crypto, identities, "receipt-1")
	if err != nil {
		t.Fatal("store setup failed")
	}
	if err := initializeIdentityEpoch(ctx, db, identities); err != nil {
		t.Fatal("identity epoch setup failed")
	}
	if err := withTx(ctx, db, func(tx *sql.Tx) error { return store.Initialize(ctx, tx) }); err != nil {
		t.Fatalf("explicit init failed: %v", err)
	}
	wrong, err := NewStore(testCryptoDifferent(t), identities, "receipt-1")
	if err != nil {
		t.Fatal("wrong key store setup failed")
	}
	if err := withTx(ctx, db, func(tx *sql.Tx) error { return wrong.Initialize(ctx, tx) }); !errors.Is(err, ErrLedger) {
		t.Fatal("wrong receipt epoch accepted")
	}

	const email = "ledger@example.test"
	scope, cipherEmail, err := identities.Protect(email)
	if err != nil {
		t.Fatal("email fixture protection failed")
	}
	const challengeID = "challenge_0123456789ABCDEFG"
	if err := insertChallenge(ctx, db, scope, challengeID, cipherEmail); err != nil {
		t.Fatal("challenge fixture setup failed")
	}
	binding := makeBinding(t, crypto, challengeID, "AQEBAQEBAQEBAQEBAQEBAQ", "123456", "eyJhbGciOiJFUzI1NiJ9.eyJzdWIiOiJ4In0.AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA")
	expiry := time.Now().UTC().Add(2 * time.Hour).Truncate(time.Microsecond)
	secret := []byte("opaque-session-result-91")
	if err := withTx(ctx, db, func(tx *sql.Tx) error {
		h, err := store.Lock(ctx, tx, scope, binding)
		if err != nil {
			return err
		}
		if _, ok := h.ExistingRecord(); ok {
			return ErrLedger
		}
		aad, err := crypto.NewContext(scope[:], binding, loginreceipt.ResultAuthenticated, "session-ref-1", expiry)
		if err != nil {
			return err
		}
		sealed, err := crypto.Seal(aad, secret)
		if err != nil {
			return err
		}
		return store.Put(ctx, tx, h, loginreceipt.ResultAuthenticated, "session-ref-1", expiry, sealed)
	}); err != nil {
		t.Fatalf("first receipt transaction failed: %v", err)
	}

	if err := withTx(ctx, db, func(tx *sql.Tx) error {
		h, err := store.Lock(ctx, tx, scope, binding)
		if err != nil {
			return err
		}
		record, ok := h.ExistingRecord()
		if !ok {
			return ErrLedger
		}
		aad, err := crypto.NewContext(scope[:], binding, record.ResultType(), record.ResultReference(), record.ExpiresAt())
		if err != nil {
			return err
		}
		plain, err := crypto.Open(aad, record.Sealed())
		if err != nil {
			return err
		}
		if string(plain.Bytes()) != string(secret) {
			return ErrLedger
		}
		return nil
	}); err != nil {
		t.Fatalf("receipt recovery failed: %v", err)
	}

	// Rechecking must remain bound to the original live transaction, detect
	// transaction-local changes, and never delete a receipt merely because it
	// expired while another authorization check was waiting.
	checkTx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	checkedHandle, err := store.Lock(ctx, checkTx, scope, binding)
	if err != nil {
		_ = checkTx.Rollback()
		t.Fatal(err)
	}
	rechecked, checkedScope, checkedBinding, err := checkedHandle.RecheckExisting(ctx, checkTx)
	if err != nil || checkedScope != scope || checkedBinding.RequestDigest() != binding.RequestDigest() ||
		checkedBinding.IdempotencyDigest() != binding.IdempotencyDigest() || rechecked.ResultReference() != "session-ref-1" {
		_ = checkTx.Rollback()
		t.Fatal("existing receipt recheck did not retain its transaction binding")
	}
	otherTx, err := db.BeginTx(ctx, nil)
	if err != nil {
		_ = checkTx.Rollback()
		t.Fatal(err)
	}
	if _, _, _, err := checkedHandle.RecheckExisting(ctx, otherTx); !errors.Is(err, ErrLedger) {
		_ = otherTx.Rollback()
		_ = checkTx.Rollback()
		t.Fatal("receipt handle was usable in a different transaction")
	}
	_ = otherTx.Rollback()
	requestKey := binding.IdempotencyDigest()
	if _, err := checkTx.ExecContext(ctx, `UPDATE auth_email_login_receipt SET request_hmac=? WHERE email_scope=? AND request_key_hmac=?`, bytes32(0xef), scope[:], requestKey[:]); err != nil {
		_ = checkTx.Rollback()
		t.Fatal(err)
	}
	if _, _, _, err := checkedHandle.RecheckExisting(ctx, checkTx); !errors.Is(err, ErrConflict) {
		_ = checkTx.Rollback()
		t.Fatal("receipt recheck accepted a changed request digest")
	}
	_ = checkTx.Rollback()
	if _, _, _, err := checkedHandle.RecheckExisting(ctx, checkTx); !errors.Is(err, ErrLedger) {
		t.Fatal("receipt recheck accepted an ended transaction")
	}
	expiryTx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	expiryHandle, err := store.Lock(ctx, expiryTx, scope, binding)
	if err != nil {
		_ = expiryTx.Rollback()
		t.Fatal(err)
	}
	if _, err := expiryTx.ExecContext(ctx, `UPDATE auth_email_login_receipt SET created_at=DATE_SUB(UTC_TIMESTAMP(6), INTERVAL 2 MINUTE), expires_at=DATE_SUB(UTC_TIMESTAMP(6), INTERVAL 1 MINUTE) WHERE email_scope=? AND request_key_hmac=?`, scope[:], requestKey[:]); err != nil {
		_ = expiryTx.Rollback()
		t.Fatal(err)
	}
	if _, _, _, err := expiryHandle.RecheckExisting(ctx, expiryTx); !errors.Is(err, ErrExpired) {
		_ = expiryTx.Rollback()
		t.Fatal("expired receipt remained available for recheck")
	}
	if _, err := store.LockExisting(ctx, expiryTx, scope, binding); !errors.Is(err, ErrExpired) {
		_ = expiryTx.Rollback()
		t.Fatal("existing-only lookup did not report the expired receipt")
	}
	var stillPresent int
	if err := expiryTx.QueryRowContext(ctx, `SELECT COUNT(*) FROM auth_email_login_receipt WHERE email_scope=? AND request_key_hmac=?`, scope[:], requestKey[:]).Scan(&stillPresent); err != nil || stillPresent != 1 {
		_ = expiryTx.Rollback()
		t.Fatal("expiry recheck deleted the existing receipt")
	}
	_ = expiryTx.Rollback()
	missingBinding := makeBinding(t, crypto, challengeID, "Dg4ODg4ODg4ODg4ODg4ODg", "777777", bindingProof)
	if err := withTx(ctx, db, func(tx *sql.Tx) error {
		h, err := store.LockExisting(ctx, tx, scope, missingBinding)
		if err != nil {
			return err
		}
		if _, exists := h.ExistingRecord(); exists {
			return ErrLedger
		}
		return nil
	}); err != nil {
		t.Fatalf("existing-only lookup did not distinguish a missing receipt: %v", err)
	}

	conflicting := makeBinding(t, crypto, challengeID, "AQEBAQEBAQEBAQEBAQEBAQ", "654321", "eyJhbGciOiJFUzI1NiJ9.eyJzdWIiOiJ4In0.AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA")
	if err := withTx(ctx, db, func(tx *sql.Tx) error { _, err := store.Lock(ctx, tx, scope, conflicting); return err }); !errors.Is(err, ErrConflict) {
		t.Fatal("same-key changed request did not conflict")
	}
	wrongChallenge := makeBinding(t, crypto, "challenge_wrong_0123456789", "CQkJCQkJCQkJCQkJCQkJCQ", "666666", bindingProof)
	if err := withTx(ctx, db, func(tx *sql.Tx) error { _, err := store.Lock(ctx, tx, scope, wrongChallenge); return err }); !errors.Is(err, ErrConflict) {
		t.Fatal("scope/challenge mismatch was not classified as conflict")
	}
	if err := withTx(ctx, db, func(tx *sql.Tx) error {
		h, err := store.Lock(ctx, tx, scope, binding)
		if err != nil {
			return err
		}
		otherExpiry := expiry.Add(time.Microsecond)
		aad, err := crypto.NewContext(scope[:], binding, loginreceipt.ResultAuthenticated, "session-ref-1", otherExpiry)
		if err != nil {
			return err
		}
		sealed, err := crypto.Seal(aad, secret)
		if err != nil {
			return err
		}
		return store.Put(ctx, tx, h, loginreceipt.ResultAuthenticated, "session-ref-1", otherExpiry, sealed)
	}); !errors.Is(err, ErrLedger) {
		t.Fatal("second Put through handle accepted")
	}

	// Put rechecks the locked identity linkage because a caller can mutate the
	// slot later in the same transaction after acquiring the opaque handle.
	changedSlotBinding := makeBinding(t, crypto, challengeID, "BQUFBQUFBQUFBQUFBQUFBQ", "555555", bindingProof)
	changedSlotErr := withTx(ctx, db, func(tx *sql.Tx) error {
		h, err := store.Lock(ctx, tx, scope, changedSlotBinding)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE auth_email_login_slot SET challenge_id=? WHERE email_index=?`, []byte("challenge_changed_0123456789"), scope[:]); err != nil {
			return err
		}
		changedExpiry := time.Now().UTC().Add(time.Hour).Truncate(time.Microsecond)
		aad, err := crypto.NewContext(scope[:], changedSlotBinding, loginreceipt.ResultAuthenticated, "changed-slot-ref", changedExpiry)
		if err != nil {
			return err
		}
		sealed, err := crypto.Seal(aad, []byte("changed-slot-result"))
		if err != nil {
			return err
		}
		return store.Put(ctx, tx, h, loginreceipt.ResultAuthenticated, "changed-slot-ref", changedExpiry, sealed)
	})
	if !errors.Is(changedSlotErr, ErrConflict) {
		t.Fatal("Put accepted a handle after slot changed in the same transaction")
	}

	var stored []byte
	if err := db.QueryRow(`SELECT response_enc FROM auth_email_login_receipt WHERE email_scope=?`, scope[:]).Scan(&stored); err != nil {
		t.Fatal("receipt ciphertext read failed")
	}
	for _, forbidden := range [][]byte{secret, []byte(email), []byte("123456")} {
		if strings.Contains(string(stored), string(forbidden)) {
			t.Fatal("plaintext material found in stored ciphertext")
		}
	}
	if len(stored) < 29 {
		t.Fatal("invalid ciphertext record")
	}

	// A reservation held by one transaction serializes a second request with
	// the same key; after commit the waiter observes the exact stored receipt.
	concurrentBinding := makeBinding(t, crypto, challengeID, "AgICAgICAgICAgICAgICAg", "111111", bindingProof)
	firstTx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal("concurrency fixture transaction failed")
	}
	firstHandle, err := store.Lock(ctx, firstTx, scope, concurrentBinding)
	if err != nil {
		t.Fatal("first concurrent lock failed")
	}
	waiter := make(chan error, 1)
	go func() {
		waitTx, beginErr := db.BeginTx(ctx, nil)
		if beginErr != nil {
			waiter <- beginErr
			return
		}
		h, lockErr := store.Lock(ctx, waitTx, scope, concurrentBinding)
		if lockErr == nil {
			if _, ok := h.ExistingRecord(); !ok {
				lockErr = ErrLedger
			}
		}
		if lockErr != nil {
			_ = waitTx.Rollback()
			waiter <- lockErr
			return
		}
		waiter <- waitTx.Commit()
	}()
	time.Sleep(100 * time.Millisecond)
	concurrentExpiry := time.Now().UTC().Add(2 * time.Hour).Truncate(time.Microsecond)
	concurrentAAD, err := crypto.NewContext(scope[:], concurrentBinding, loginreceipt.ResultApprovalRequired, "approval-ref", concurrentExpiry)
	if err != nil {
		t.Fatal("concurrent AAD setup failed")
	}
	concurrentSealed, err := crypto.Seal(concurrentAAD, []byte("approval-result"))
	if err != nil {
		t.Fatal("concurrent receipt seal failed")
	}
	if err := store.Put(ctx, firstTx, firstHandle, loginreceipt.ResultApprovalRequired, "approval-ref", concurrentExpiry, concurrentSealed); err != nil {
		_ = firstTx.Rollback()
		t.Fatalf("concurrent receipt put failed: %v", err)
	}
	if err := firstTx.Commit(); err != nil {
		t.Fatal("concurrent receipt commit failed")
	}
	select {
	case err := <-waiter:
		if err != nil {
			t.Fatalf("serialized waiter failed: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("serialized waiter did not finish")
	}

	// A rollback leaves no reservation, and the same request can be acquired
	// again in a later transaction.
	rollbackBinding := makeBinding(t, crypto, challengeID, "AwMDAwMDAwMDAwMDAwMDAw", "222222", bindingProof)
	rollbackTx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal("rollback fixture transaction failed")
	}
	rollbackHandle, err := store.Lock(ctx, rollbackTx, scope, rollbackBinding)
	if err != nil {
		t.Fatal("rollback lock failed")
	}
	rollbackExpiry := time.Now().UTC().Add(time.Hour).Truncate(time.Microsecond)
	rollbackAAD, err := crypto.NewContext(scope[:], rollbackBinding, loginreceipt.ResultAuthenticated, "rollback-ref", rollbackExpiry)
	if err != nil {
		t.Fatal("rollback AAD setup failed")
	}
	rollbackSealed, err := crypto.Seal(rollbackAAD, []byte("rollback-material"))
	if err != nil {
		t.Fatal("rollback seal failed")
	}
	if err := store.Put(ctx, rollbackTx, rollbackHandle, loginreceipt.ResultAuthenticated, "rollback-ref", rollbackExpiry, rollbackSealed); err != nil {
		t.Fatal("rollback put failed")
	}
	if err := rollbackTx.Rollback(); err != nil {
		t.Fatal("rollback failed")
	}
	if err := withTx(ctx, db, func(tx *sql.Tx) error {
		h, err := store.Lock(ctx, tx, scope, rollbackBinding)
		if err == nil {
			if _, ok := h.ExistingRecord(); ok {
				return ErrLedger
			}
		}
		return err
	}); err != nil {
		t.Fatalf("rollback persisted a receipt: %v", err)
	}

	// The same request-key digest is isolated by a different protected scope.
	otherScope, otherCipher, err := identities.Protect("other-ledger@example.test")
	if err != nil {
		t.Fatal("second scope setup failed")
	}
	const otherChallenge = "challenge_9876543210ZYXWVUT"
	if err := insertChallenge(ctx, db, otherScope, otherChallenge, otherCipher); err != nil {
		t.Fatal("second challenge setup failed")
	}
	otherBinding := makeBinding(t, crypto, otherChallenge, "AQEBAQEBAQEBAQEBAQEBAQ", "333333", bindingProof)
	otherExpiry := time.Now().UTC().Add(time.Hour).Truncate(time.Microsecond)
	if err := withTx(ctx, db, func(tx *sql.Tx) error {
		h, err := store.Lock(ctx, tx, otherScope, otherBinding)
		if err != nil {
			return err
		}
		aad, err := crypto.NewContext(otherScope[:], otherBinding, loginreceipt.ResultAuthenticated, "other-ref", otherExpiry)
		if err != nil {
			return err
		}
		sealed, err := crypto.Seal(aad, []byte("other-scope-result"))
		if err != nil {
			return err
		}
		return store.Put(ctx, tx, h, loginreceipt.ResultAuthenticated, "other-ref", otherExpiry, sealed)
	}); err != nil {
		t.Fatalf("second scope receipt failed: %v", err)
	}
	var scopeRows int
	if err := db.QueryRow(`SELECT COUNT(*) FROM auth_email_login_receipt WHERE email_scope IN (?,?)`, scope[:], otherScope[:]).Scan(&scopeRows); err != nil || scopeRows != 3 {
		t.Fatal("receipt scopes did not remain isolated")
	}

	// An expired key is a new request. Deleting the prior row and writing its
	// replacement remain part of the caller's transaction.
	expiredBinding := makeBinding(t, crypto, challengeID, "BAQEBAQEBAQEBAQEBAQEBA", "444444", bindingProof)
	expiredAt := time.Now().UTC().Add(500 * time.Millisecond).Truncate(time.Microsecond)
	if err := withTx(ctx, db, func(tx *sql.Tx) error {
		h, err := store.Lock(ctx, tx, scope, expiredBinding)
		if err != nil {
			return err
		}
		aad, err := crypto.NewContext(scope[:], expiredBinding, loginreceipt.ResultAuthenticated, "expired-ref", expiredAt)
		if err != nil {
			return err
		}
		sealed, err := crypto.Seal(aad, []byte("expired-material"))
		if err != nil {
			return err
		}
		return store.Put(ctx, tx, h, loginreceipt.ResultAuthenticated, "expired-ref", expiredAt, sealed)
	}); err != nil {
		t.Fatalf("expired fixture insert failed: %v", err)
	}
	time.Sleep(time.Until(expiredAt) + 50*time.Millisecond)
	renewedBinding := makeBinding(t, crypto, challengeID, "BAQEBAQEBAQEBAQEBAQEBA", "888888", bindingProof)
	renewedKey := renewedBinding.IdempotencyDigest()
	rollbackExpiredTx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal("expired rollback transaction setup failed")
	}
	rolledHandle, err := store.Lock(ctx, rollbackExpiredTx, scope, renewedBinding)
	if err != nil {
		_ = rollbackExpiredTx.Rollback()
		t.Fatalf("expired request was not admitted for retry: %v", err)
	}
	if _, exists := rolledHandle.ExistingRecord(); exists {
		t.Fatal("expired receipt was returned instead of being treated as a new request")
	}
	if err := rollbackExpiredTx.Rollback(); err != nil {
		t.Fatal("expired receipt rollback failed")
	}
	var restoredRows int
	if err := db.QueryRow(`SELECT COUNT(*) FROM auth_email_login_receipt WHERE email_scope=? AND request_key_hmac=?`, scope[:], renewedKey[:]).Scan(&restoredRows); err != nil || restoredRows != 1 {
		t.Fatal("rollback did not restore the expired receipt row")
	}
	newExpiry := time.Now().UTC().Add(time.Hour).Truncate(time.Microsecond)
	if err := withTx(ctx, db, func(tx *sql.Tx) error {
		h, err := store.Lock(ctx, tx, scope, renewedBinding)
		if err != nil {
			return err
		}
		if _, exists := h.ExistingRecord(); exists {
			return ErrLedger
		}
		aad, err := crypto.NewContext(scope[:], renewedBinding, loginreceipt.ResultAuthenticated, "renewed-expired-key", newExpiry)
		if err != nil {
			return err
		}
		sealed, err := crypto.Seal(aad, []byte("renewed-after-expiry"))
		if err != nil {
			return err
		}
		return store.Put(ctx, tx, h, loginreceipt.ResultAuthenticated, "renewed-expired-key", newExpiry, sealed)
	}); err != nil {
		t.Fatalf("expired key could not be written as a new request: %v", err)
	}
	if err := withTx(ctx, db, func(tx *sql.Tx) error {
		h, err := store.Lock(ctx, tx, scope, renewedBinding)
		if err != nil {
			return err
		}
		if _, exists := h.ExistingRecord(); !exists {
			return ErrLedger
		}
		return nil
	}); err != nil {
		t.Fatalf("renewed receipt was not persisted: %v", err)
	}
	if err := withTx(ctx, db, func(tx *sql.Tx) error { _, err := store.Lock(ctx, tx, scope, expiredBinding); return err }); !errors.Is(err, ErrConflict) {
		t.Fatal("unexpired renewed receipt did not conflict with a changed request")
	}

	// A later challenge may replace the email slot while a still-live receipt
	// remains recoverable. That must not let the old challenge create a new row.
	const replacementChallenge = "challenge_replacement_0123456789"
	if err := insertChallengeRow(ctx, db, scope, replacementChallenge, cipherEmail); err != nil {
		t.Fatal("replacement challenge setup failed")
	}
	if _, err := db.Exec(`UPDATE auth_email_login_slot SET challenge_id=? WHERE email_index=?`, []byte(replacementChallenge), scope[:]); err != nil {
		t.Fatal("replacement slot setup failed")
	}
	if err := withTx(ctx, db, func(tx *sql.Tx) error {
		h, err := store.Lock(ctx, tx, scope, binding)
		if err != nil {
			return err
		}
		if _, ok := h.ExistingRecord(); !ok {
			return ErrLedger
		}
		return nil
	}); err != nil {
		t.Fatalf("exact old receipt was not available after slot replacement: %v", err)
	}
	changedOldRequest := makeBinding(t, crypto, challengeID, "AQEBAQEBAQEBAQEBAQEBAQ", "654321", bindingProof)
	if err := withTx(ctx, db, func(tx *sql.Tx) error { _, err := store.Lock(ctx, tx, scope, changedOldRequest); return err }); !errors.Is(err, ErrConflict) {
		t.Fatal("changed old request did not conflict after slot replacement")
	}
	oldChallengeNewKey := makeBinding(t, crypto, challengeID, "BgYGBgYGBgYGBgYGBgYGBg", "777777", bindingProof)
	if err := withTx(ctx, db, func(tx *sql.Tx) error { _, err := store.Lock(ctx, tx, scope, oldChallengeNewKey); return err }); !errors.Is(err, ErrConflict) {
		t.Fatal("old challenge was allowed to reserve a new receipt after slot replacement")
	}
	if _, err := db.Exec(`UPDATE auth_email_login_receipt SET expires_at=DATE_ADD(created_at, INTERVAL 1 MICROSECOND) WHERE email_scope=? AND request_key_hmac=?`, scope[:], renewedKey[:]); err != nil {
		t.Fatal("expired stale-slot fixture setup failed")
	}
	staleExpiredRequest := makeBinding(t, crypto, challengeID, "BAQEBAQEBAQEBAQEBAQEBA", "999999", bindingProof)
	if err := withTx(ctx, db, func(tx *sql.Tx) error { _, err := store.Lock(ctx, tx, scope, staleExpiredRequest); return err }); !errors.Is(err, ErrConflict) {
		t.Fatal("expired receipt for a replaced challenge was deleted or reused")
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM auth_email_login_receipt WHERE email_scope=? AND request_key_hmac=?`, scope[:], renewedKey[:]).Scan(&restoredRows); err != nil || restoredRows != 1 {
		t.Fatal("stale slot attempt removed the expired receipt")
	}
	idempotencyDigest := binding.IdempotencyDigest()
	var tampered []byte
	if err := db.QueryRow(`SELECT response_enc FROM auth_email_login_receipt WHERE email_scope=? AND request_key_hmac=?`, scope[:], idempotencyDigest[:]).Scan(&tampered); err != nil || len(tampered) <= 20 {
		t.Fatal("ciphertext tamper fixture could not be read")
	}
	tampered[20] ^= 1
	if _, err := db.Exec(`UPDATE auth_email_login_receipt SET response_enc=? WHERE email_scope=? AND request_key_hmac=?`, tampered, scope[:], idempotencyDigest[:]); err != nil {
		t.Fatal("ciphertext tamper failed")
	}
	if err := withTx(ctx, db, func(tx *sql.Tx) error {
		h, err := store.Lock(ctx, tx, scope, binding)
		if err != nil {
			return err
		}
		record, ok := h.ExistingRecord()
		if !ok {
			return ErrLedger
		}
		aad, err := crypto.NewContext(scope[:], binding, record.ResultType(), record.ResultReference(), record.ExpiresAt())
		if err != nil {
			return err
		}
		if _, err := crypto.Open(aad, record.Sealed()); err == nil {
			return ErrLedger
		}
		return nil
	}); err != nil {
		t.Fatalf("Lock did not return the structurally valid tampered receipt for deferred authentication: %v", err)
	}
	if _, err := db.Exec(`DELETE FROM auth_email_login_receipt_key_epoch WHERE slot=1`); err != nil {
		t.Fatal("epoch test setup failed")
	}
	if err := withTx(ctx, db, func(tx *sql.Tx) error { return store.Initialize(ctx, tx) }); !errors.Is(err, ErrLedger) {
		t.Fatal("missing epoch with receipt history was recreated")
	}
	currentTag, err := crypto.EpochTag("receipt-1")
	if err != nil {
		t.Fatal("receipt epoch setup failed")
	}
	if _, err := db.Exec(`INSERT INTO auth_email_login_receipt_key_epoch(slot,key_id,key_tag) VALUES (1,?,?)`, []byte("receipt-1"), currentTag[:]); err != nil {
		t.Fatal("receipt epoch restore for database error case failed")
	}
	if _, err := db.Exec(`DROP TABLE auth_email_login_slot`); err != nil {
		t.Fatal("private schema error fixture failed")
	}
	if err := withTx(ctx, db, func(tx *sql.Tx) error { _, err := store.Lock(ctx, tx, scope, binding); return err }); !errors.Is(err, ErrLedger) {
		t.Fatal("missing slot table database error was misclassified as request conflict")
	}
}

const bindingProof = "eyJhbGciOiJFUzI1NiJ9.eyJzdWIiOiJ4In0.AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"

func testCryptoDifferent(t *testing.T) *loginreceipt.Crypto {
	t.Helper()
	keys := [][]byte{bytes32(11), bytes32(12), bytes32(13)}
	c, err := loginreceipt.New(loginreceipt.KeyConfig{IdempotencyHMACKey: keys[0], RequestHMACKey: keys[1], AES256GCMKey: keys[2]})
	if err != nil {
		t.Fatal("crypto setup failed")
	}
	return c
}
func makeBinding(t *testing.T, c *loginreceipt.Crypto, challenge, idem, code, proof string) loginreceipt.Binding {
	t.Helper()
	b, err := c.Summarize(loginreceipt.Request{IdempotencyKey: idem, ChallengeID: challenge, Code: code, DeviceProof: proof})
	if err != nil {
		t.Fatal("request binding setup failed")
	}
	return b
}
func withTx(ctx context.Context, db *sql.DB, fn func(*sql.Tx) error) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return ErrLedger
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}
func initializeIdentityEpoch(ctx context.Context, db *sql.DB, identities *domain.EmailIdentityProtector) error {
	_, tag, err := identities.KeyEpoch()
	if err != nil {
		return ErrLedger
	}
	_, err = db.ExecContext(ctx, `INSERT INTO auth_email_identity_key_epoch(slot,key_tag) VALUES (1,?)`, tag[:])
	return err
}
func insertChallenge(ctx context.Context, db *sql.DB, scope [32]byte, challenge string, emailCipher []byte) error {
	_, err := db.ExecContext(ctx, `INSERT INTO auth_email_login_slot(email_index,challenge_id) VALUES (?,?)`, scope[:], []byte(challenge))
	if err != nil {
		return err
	}
	return insertChallengeRow(ctx, db, scope, challenge, emailCipher)
}

func insertChallengeRow(ctx context.Context, db *sql.DB, scope [32]byte, challenge string, emailCipher []byte) error {
	_, err := db.ExecContext(ctx, `INSERT INTO auth_email_login_challenge(challenge_id,email_index,email_ciphertext,email_key_id,nonce,public_x,public_y,key_fingerprint,code_digest,attempts,max_attempts,proof_max_lifetime_ns,proof_clock_skew_ns,created_at,expires_at) VALUES (?,?,?,?,?,?,?,?,?,0,5,60000000000,30000000000,UTC_TIMESTAMP(6),DATE_ADD(UTC_TIMESTAMP(6),INTERVAL 10 MINUTE))`, []byte(challenge), scope[:], emailCipher, []byte("identity-1"), []byte(strings.Repeat("n", 43)), strings.Repeat("A", 43), strings.Repeat("B", 43), bytes32(20), bytes32(21))
	return err
}
