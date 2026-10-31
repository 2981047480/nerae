package codeepoch_test

import (
	"bytes"
	"context"
	"crypto/hmac"
	"database/sql"
	"encoding/base64"
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
	"nerae/api/internal/modules/auth/codeepoch"
	"nerae/api/internal/modules/auth/domain"
	"nerae/api/internal/modules/auth/issuance"
)

const (
	testDSNEnv = "NERAE_EMAIL_CODEEPOCH_TEST_DSN"
	testDBName = "nerae_email_codeepoch_test"
)

var testDeriverKey = bytes.Repeat([]byte{0x51}, 32)

func TestNewValidatesConfigurationAndRedacts(t *testing.T) {
	key := bytes.Repeat([]byte{0x31}, 32)
	hasher, err := domain.NewEmailCodeHasher(key)
	if err != nil {
		t.Fatal("test hasher setup failed")
	}
	for _, keyID := range []string{"", "has space", "has/slash", strings.Repeat("x", 65), "密钥"} {
		if _, err := codeepoch.New(hasher, keyID); !errors.Is(err, codeepoch.ErrEpoch) {
			t.Fatalf("invalid key ID accepted: %q", keyID)
		}
	}
	if _, err := codeepoch.New(nil, "hash-v1"); !errors.Is(err, codeepoch.ErrEpoch) {
		t.Fatal("nil hasher accepted")
	}
	store, err := codeepoch.New(hasher, "hash-v1")
	if err != nil {
		t.Fatal("valid config rejected")
	}
	formatted := fmt.Sprintf("%v %#v", store, store)
	if strings.Contains(formatted, base64.RawURLEncoding.EncodeToString(key)) || strings.Contains(formatted, "hash-v1") {
		t.Fatal("formatted epoch store exposed configuration")
	}
	if _, err := store.MarshalJSON(); err == nil {
		t.Fatal("epoch store serialized")
	}
	var logs strings.Builder
	slog.New(slog.NewJSONHandler(&logs, nil)).Info("epoch", "store", store)
	if strings.Contains(logs.String(), base64.RawURLEncoding.EncodeToString(key)) || strings.Contains(logs.String(), "hash-v1") {
		t.Fatal("structured log exposed configuration")
	}
}

func TestMySQLEpochStore(t *testing.T) {
	dsn := os.Getenv(testDSNEnv)
	if dsn == "" {
		t.Skip("private socket-only email code epoch MySQL DSN is not configured")
	}
	validateTestDSN(t, dsn)
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatal("private database open failed")
	}
	defer db.Close()
	db.SetMaxOpenConns(8)
	db.SetMaxIdleConns(4)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		t.Fatal("private database unavailable")
	}
	deriver, err := issuance.NewDeriver(testDeriverKey)
	if err != nil {
		t.Fatal("test deriver setup failed")
	}
	keyA := bytes.Repeat([]byte{0x71}, 32)
	keyB := bytes.Repeat([]byte{0x72}, 32)
	storeA := mustStore(t, keyA, "hash-v1")

	t.Run("empty initialize and same-key restart", func(t *testing.T) {
		resetSchema(t, ctx, db, deriver)
		tx := beginWithPrereqLocks(t, ctx, db, deriver)
		if err := storeA.Lock(ctx, tx); !errors.Is(err, codeepoch.ErrEpoch) {
			t.Fatal("Lock initialized or accepted missing epoch")
		}
		if err := storeA.Initialize(ctx, tx); err != nil {
			t.Fatal("empty history did not initialize")
		}
		if err := storeA.Lock(ctx, tx); err != nil {
			t.Fatal("initialized epoch did not lock")
		}
		if err := tx.Commit(); err != nil {
			t.Fatal("initialization commit failed")
		}

		restarted := mustStore(t, keyA, "hash-v1")
		tx = beginWithPrereqLocks(t, ctx, db, deriver)
		if err := restarted.Initialize(ctx, tx); err != nil {
			t.Fatal("same-key restart was rejected")
		}
		if err := restarted.Lock(ctx, tx); err != nil {
			t.Fatal("same-key restart could not lock")
		}
		if err := tx.Commit(); err != nil {
			t.Fatal("restart verification commit failed")
		}
	})

	t.Run("wrong key or ID fails without mutation", func(t *testing.T) {
		resetSchema(t, ctx, db, deriver)
		tx := beginWithPrereqLocks(t, ctx, db, deriver)
		if err := storeA.Initialize(ctx, tx); err != nil {
			t.Fatal("initialization failed")
		}
		if err := tx.Commit(); err != nil {
			t.Fatal("initialization commit failed")
		}
		for name, candidate := range map[string]*codeepoch.Store{
			"key":    mustStore(t, keyB, "hash-v1"),
			"key ID": mustStore(t, keyA, "hash-v2"),
		} {
			t.Run(name, func(t *testing.T) {
				candidateTx := beginWithPrereqLocks(t, ctx, db, deriver)
				if err := candidate.Lock(ctx, candidateTx); !errors.Is(err, codeepoch.ErrEpoch) {
					t.Fatal("mismatch passed Lock")
				}
				if err := candidate.Initialize(ctx, candidateTx); !errors.Is(err, codeepoch.ErrEpoch) {
					t.Fatal("mismatch passed Initialize")
				}
				_ = candidateTx.Rollback()
			})
		}
		if countEpochRows(t, ctx, db) != 1 {
			t.Fatal("mismatched config changed epoch row")
		}
	})

	t.Run("rollback removes newly initialized row", func(t *testing.T) {
		resetSchema(t, ctx, db, deriver)
		tx := beginWithPrereqLocks(t, ctx, db, deriver)
		if err := storeA.Initialize(ctx, tx); err != nil {
			t.Fatal("initialization failed")
		}
		if err := tx.Rollback(); err != nil {
			t.Fatal("rollback failed")
		}
		if countEpochRows(t, ctx, db) != 0 {
			t.Fatal("uncommitted epoch survived rollback")
		}
		tx = beginWithPrereqLocks(t, ctx, db, deriver)
		if err := storeA.Initialize(ctx, tx); err != nil {
			t.Fatal("fresh initialization after rollback failed")
		}
		if err := tx.Commit(); err != nil {
			t.Fatal("fresh initialization commit failed")
		}
	})

	t.Run("any existing history blocks missing epoch", func(t *testing.T) {
		for _, history := range []string{"slot", "challenge", "outbox"} {
			t.Run(history, func(t *testing.T) {
				resetSchema(t, ctx, db, deriver)
				insertHistory(t, ctx, db, history)
				tx := beginWithPrereqLocks(t, ctx, db, deriver)
				if err := storeA.Initialize(ctx, tx); !errors.Is(err, codeepoch.ErrEpoch) {
					t.Fatalf("history %s allowed missing epoch initialization", history)
				}
				_ = tx.Rollback()
				if countEpochRows(t, ctx, db) != 0 {
					t.Fatal("history guard wrote an epoch")
				}
			})
		}
	})

	t.Run("concurrent different keys never both initialize", func(t *testing.T) {
		resetSchema(t, ctx, db, deriver)
		stores := []*codeepoch.Store{storeA, mustStore(t, keyB, "hash-v2")}
		txs := make([]*sql.Tx, len(stores))
		for i := range stores {
			txs[i] = beginWithPrereqLocks(t, ctx, db, deriver)
		}
		start := make(chan struct{})
		type result struct{ committed bool }
		results := make(chan result, len(stores))
		var wg sync.WaitGroup
		for i, store := range stores {
			wg.Add(1)
			go func(i int, store *codeepoch.Store) {
				defer wg.Done()
				<-start
				if err := store.Initialize(ctx, txs[i]); err != nil {
					_ = txs[i].Rollback()
					results <- result{}
					return
				}
				if txs[i].Commit() != nil {
					results <- result{}
					return
				}
				results <- result{committed: true}
			}(i, store)
		}
		close(start)
		wg.Wait()
		close(results)
		commits := 0
		for result := range results {
			if result.committed {
				commits++
			}
		}
		if commits > 1 {
			t.Fatalf("different configurations both initialized (%d commits)", commits)
		}
		if rows := countEpochRows(t, ctx, db); rows > 1 {
			t.Fatalf("singleton epoch table contains %d rows", rows)
		}
		if countEpochRows(t, ctx, db) == 0 {
			tx := beginWithPrereqLocks(t, ctx, db, deriver)
			if err := storeA.Initialize(ctx, tx); err != nil {
				t.Fatal("fresh bootstrap after concurrent transaction failure failed")
			}
			if err := tx.Commit(); err != nil {
				t.Fatal("fresh bootstrap commit failed")
			}
		}
		accepted := 0
		for _, candidate := range stores {
			tx := beginWithPrereqLocks(t, ctx, db, deriver)
			if err := candidate.Initialize(ctx, tx); err == nil {
				if err := tx.Commit(); err != nil {
					t.Fatal("matching concurrent key verification commit failed")
				}
				accepted++
			} else {
				_ = tx.Rollback()
			}
		}
		if accepted != 1 {
			t.Fatalf("after serialization, accepted configurations = %d, want exactly one", accepted)
		}
	})

	t.Run("shared lock blocks epoch replacement", func(t *testing.T) {
		resetSchema(t, ctx, db, deriver)
		tx := beginWithPrereqLocks(t, ctx, db, deriver)
		if err := storeA.Initialize(ctx, tx); err != nil {
			t.Fatal("initialization failed")
		}
		if err := tx.Commit(); err != nil {
			t.Fatal("initialization commit failed")
		}
		reader := beginWithPrereqLocks(t, ctx, db, deriver)
		if err := storeA.Lock(ctx, reader); err != nil {
			t.Fatal("runtime epoch lock failed")
		}
		writer, err := db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal("replacement transaction failed")
		}
		done := make(chan error, 1)
		go func() {
			_, err := writer.ExecContext(ctx, `UPDATE auth_email_code_hash_key_epoch SET key_tag=? WHERE slot=1`, bytes.Repeat([]byte{0x7f}, 32))
			if err == nil {
				err = writer.Commit()
			} else {
				_ = writer.Rollback()
			}
			done <- err
		}()
		select {
		case err := <-done:
			_ = reader.Rollback()
			t.Fatalf("epoch replacement did not wait for shared lock: %v", err)
		case <-time.After(150 * time.Millisecond):
		}
		if err := reader.Commit(); err != nil {
			t.Fatal("runtime lock transaction commit failed")
		}
		select {
		case err := <-done:
			if err != nil {
				t.Fatal("replacement did not resume after lock release")
			}
		case <-time.After(5 * time.Second):
			t.Fatal("replacement remained blocked after lock release")
		}
		check := beginWithPrereqLocks(t, ctx, db, deriver)
		if err := storeA.Lock(ctx, check); !errors.Is(err, codeepoch.ErrEpoch) {
			_ = check.Rollback()
			t.Fatal("old hasher configuration accepted replaced epoch")
		}
		_ = check.Rollback()
	})

	t.Run("cancelled database operation fails closed", func(t *testing.T) {
		resetSchema(t, ctx, db, deriver)
		tx := beginWithPrereqLocks(t, ctx, db, deriver)
		cancelled, stop := context.WithCancel(ctx)
		stop()
		if err := storeA.Initialize(cancelled, tx); !errors.Is(err, codeepoch.ErrEpoch) {
			t.Fatal("cancelled initialization did not fail closed")
		}
		if err := storeA.Lock(cancelled, tx); !errors.Is(err, codeepoch.ErrEpoch) {
			t.Fatal("cancelled lock did not fail closed")
		}
		_ = tx.Rollback()
	})
}

func mustStore(t *testing.T, key []byte, keyID string) *codeepoch.Store {
	t.Helper()
	hasher, err := domain.NewEmailCodeHasher(key)
	if err != nil {
		t.Fatal("test hasher setup failed")
	}
	store, err := codeepoch.New(hasher, keyID)
	if err != nil {
		t.Fatal("test epoch store setup failed")
	}
	return store
}

func validateTestDSN(t *testing.T, value string) {
	t.Helper()
	dsn, err := mysql.ParseDSN(value)
	if err != nil || dsn.Net != "unix" || dsn.User != "root" || dsn.Passwd != "" || dsn.DBName != testDBName || !dsn.ParseTime || dsn.Loc == nil || dsn.Loc.String() != "UTC" {
		t.Fatal("test requires the dedicated UTC private Unix socket DSN")
	}
	base := filepath.Dir(dsn.Addr)
	info, err := os.Lstat(base)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0o077 != 0 {
		t.Fatal("private test socket directory invalid")
	}
	resolved, err := filepath.EvalSymlinks(base)
	if err != nil || filepath.Dir(resolved) != "/private/tmp" || !strings.HasPrefix(filepath.Base(resolved), "nerae-codeepoch-mysql-") || filepath.Base(dsn.Addr) != "mysql.sock" {
		t.Fatal("test socket is outside its private temporary directory")
	}
	socket, err := os.Lstat(dsn.Addr)
	if err != nil || socket.Mode()&os.ModeSocket == 0 || socket.Mode()&os.ModeSymlink != 0 {
		t.Fatal("private MySQL socket missing or invalid")
	}
}

func resetSchema(t *testing.T, ctx context.Context, db *sql.DB, deriver *issuance.Deriver) {
	t.Helper()
	for _, statement := range []string{
		`DELETE FROM auth_email_outbox`,
		`DELETE FROM auth_email_login_challenge`,
		`DELETE FROM auth_email_login_slot`,
		`DELETE FROM auth_email_code_hash_key_epoch`,
		`DELETE FROM auth_email_code_key_epoch`,
		`DELETE FROM auth_email_identity_key_epoch`,
	} {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			t.Fatal("private schema reset failed")
		}
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO auth_email_identity_key_epoch(slot,key_tag) VALUES(1,?)`, bytes.Repeat([]byte{0x41}, 32)); err != nil {
		t.Fatal("identity epoch fixture failed")
	}
	tag, err := deriver.KeyEpochTag()
	if err != nil {
		t.Fatal("derivation tag fixture failed")
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO auth_email_code_key_epoch(slot,key_id,key_tag) VALUES(1,?,?)`, []byte("derive-v1"), tag[:]); err != nil {
		t.Fatal("derivation epoch fixture failed")
	}
}

func beginWithPrereqLocks(t *testing.T, ctx context.Context, db *sql.DB, deriver *issuance.Deriver) *sql.Tx {
	t.Helper()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal("private test transaction failed")
	}
	var identityTag []byte
	if err := tx.QueryRowContext(ctx, `SELECT key_tag FROM auth_email_identity_key_epoch WHERE slot=1 FOR SHARE`).Scan(&identityTag); err != nil || !bytes.Equal(identityTag, bytes.Repeat([]byte{0x41}, 32)) {
		_ = tx.Rollback()
		t.Fatal("verified identity epoch lock missing")
	}
	var keyID, storedTag []byte
	if err := tx.QueryRowContext(ctx, `SELECT key_id,key_tag FROM auth_email_code_key_epoch WHERE slot=1 FOR SHARE`).Scan(&keyID, &storedTag); err != nil {
		_ = tx.Rollback()
		t.Fatal("verified derivation epoch lock missing")
	}
	derivationTag, err := deriver.KeyEpochTag()
	if err != nil || string(keyID) != "derive-v1" || !hmac.Equal(storedTag, derivationTag[:]) {
		_ = tx.Rollback()
		t.Fatal("derivation epoch configuration mismatch")
	}
	return tx
}

func countEpochRows(t *testing.T, ctx context.Context, db *sql.DB) int {
	t.Helper()
	var count int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM auth_email_code_hash_key_epoch`).Scan(&count); err != nil {
		t.Fatal("epoch row count failed")
	}
	return count
}

func insertHistory(t *testing.T, ctx context.Context, db *sql.DB, history string) {
	t.Helper()
	const id = "codeepoch-history-challenge"
	now := time.Now().UTC().Truncate(time.Microsecond)
	if history == "slot" {
		if _, err := db.ExecContext(ctx, `INSERT INTO auth_email_login_slot(email_index,challenge_id) VALUES(?,?)`, bytes.Repeat([]byte{0x11}, 32), []byte(id)); err != nil {
			t.Fatal("slot history fixture failed")
		}
		return
	}
	insertChallenge(t, ctx, db, id)
	if history == "challenge" {
		return
	}
	if history == "outbox" {
		// With the production FK enabled, an outbox row necessarily references a
		// challenge, so this case also covers challenge history. Temporarily disable
		// the FK in the same connection to exercise the independent outbox guard.
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal("outbox history transaction failed")
		}
		if _, err := tx.ExecContext(ctx, `SET FOREIGN_KEY_CHECKS=0`); err != nil {
			_ = tx.Rollback()
			t.Fatal("could not prepare outbox history fixture")
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM auth_email_login_challenge WHERE challenge_id=?`, []byte(id)); err != nil {
			_ = tx.Rollback()
			t.Fatal("could not isolate outbox history fixture")
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO auth_email_outbox(challenge_id,code_digits,state,claim_token,lease_until,created_at,updated_at,terminal_at) VALUES(?,6,'pending',NULL,NULL,?,?,NULL)`, []byte(id), now, now); err != nil {
			_, _ = tx.ExecContext(context.Background(), `SET FOREIGN_KEY_CHECKS=1`)
			_ = tx.Rollback()
			t.Fatal("outbox history fixture failed")
		}
		if _, err := tx.ExecContext(ctx, `SET FOREIGN_KEY_CHECKS=1`); err != nil {
			_ = tx.Rollback()
			t.Fatal("could not restore foreign key checks")
		}
		if err := tx.Commit(); err != nil {
			t.Fatal("outbox history fixture commit failed")
		}
	}
}

func insertChallenge(t *testing.T, ctx context.Context, db *sql.DB, id string) {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Microsecond)
	if _, err := db.ExecContext(ctx, `INSERT INTO auth_email_login_challenge
		(challenge_id,email_index,email_ciphertext,email_key_id,nonce,public_x,public_y,key_fingerprint,code_digest,attempts,max_attempts,proof_max_lifetime_ns,proof_clock_skew_ns,created_at,expires_at,consumed_at)
		VALUES(?,?,?,?,?,?,?,?,?,0,3,300000000000,30000000000,?,?,NULL)`,
		[]byte(id), bytes.Repeat([]byte{0x11}, 32), []byte("synthetic-ciphertext"), []byte("identity-v1"), []byte("synthetic-nonce"), strings.Repeat("A", 43), strings.Repeat("B", 43), bytes.Repeat([]byte{0x22}, 32), bytes.Repeat([]byte{0x33}, 32), now, now.Add(5*time.Minute)); err != nil {
		t.Fatal("challenge history fixture failed")
	}
}
