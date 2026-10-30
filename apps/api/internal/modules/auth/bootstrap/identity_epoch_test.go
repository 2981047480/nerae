package bootstrap

import (
	"bytes"
	"context"
	"crypto/hmac"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"
	"nerae/api/internal/modules/auth/domain"
)

func isolatedDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("NERAE_EMAIL_EPOCH_TEST_DSN")
	if dsn == "" {
		t.Skip("set NERAE_EMAIL_EPOCH_TEST_DSN using the isolated MySQL harness")
	}
	cfg, err := mysql.ParseDSN(dsn)
	if err != nil {
		t.Fatal("invalid isolated DSN")
	}
	socket := filepath.Clean(cfg.Addr)
	parent := filepath.Dir(socket)
	resolved, resolveErr := filepath.EvalSymlinks(parent)
	info, statErr := os.Lstat(parent)
	socketInfo, socketErr := os.Lstat(socket)
	if resolveErr != nil || statErr != nil || socketErr != nil || cfg.Net != "unix" || cfg.DBName != "nerae_email_epoch_test" ||
		cfg.User != "root" || cfg.Passwd != "" || !cfg.ParseTime || cfg.Loc != time.UTC || filepath.Base(socket) != "mysql.sock" ||
		filepath.Dir(resolved) != "/private/tmp" || !strings.HasPrefix(filepath.Base(resolved), "nerae-email-epoch-mysql-") ||
		!info.IsDir() || info.Mode().Perm()&0077 != 0 || info.Mode()&os.ModeSymlink != 0 || socketInfo.Mode()&os.ModeSocket == 0 {
		t.Fatal("refusing non-isolated MySQL target")
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil || db.Ping() != nil {
		t.Fatal("could not open isolated database")
	}
	db.SetMaxOpenConns(8)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func protector(t *testing.T, value byte) *domain.EmailIdentityProtector {
	t.Helper()
	p, err := domain.NewEmailIdentityProtector(bytes.Repeat([]byte{value}, 32), bytes.Repeat([]byte{value + 1}, 32), "epoch-test-key")
	if err != nil {
		t.Fatal("could not create test protector")
	}
	return p
}

func reset(t *testing.T, db *sql.DB) {
	t.Helper()
	for _, table := range []string{"auth_email_login_challenge", "auth_email_login_slot", "login_identity", "user", "auth_email_identity_key_epoch"} {
		if _, err := db.Exec("DELETE FROM " + table); err != nil {
			t.Fatalf("could not reset isolated table %s", table)
		}
	}
}

func runInit(t *testing.T, db *sql.DB, p *domain.EmailIdentityProtector) error {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return initialize(ctx, db, p)
}

func initialize(ctx context.Context, db *sql.DB, p *domain.EmailIdentityProtector) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return ErrIdentityEpoch
	}
	init, err := NewIdentityEpochInitializer(p)
	if err != nil {
		_ = tx.Rollback()
		return err
	}
	if err = init.Initialize(ctx, tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

func TestIdentityEpochInitializeAndKeyMismatch(t *testing.T) {
	db := isolatedDB(t)
	reset(t, db)
	p := protector(t, 10)
	if err := runInit(t, db, p); err != nil {
		t.Fatal("fresh empty database initialization failed")
	}
	if err := runInit(t, db, p); err != nil {
		t.Fatal("same-key restart failed")
	}
	var count int
	var stored []byte
	if err := db.QueryRow(`SELECT COUNT(*),MAX(key_tag) FROM auth_email_identity_key_epoch`).Scan(&count, &stored); err != nil || count != 1 {
		t.Fatal("expected one epoch row")
	}
	_, expected, _ := p.KeyEpoch()
	if !hmac.Equal(stored, expected[:]) {
		t.Fatal("stored epoch did not match configured key")
	}
	if err := runInit(t, db, protector(t, 20)); !errors.Is(err, ErrIdentityEpoch) {
		t.Fatal("wrong key was not rejected")
	}
}

func TestIdentityEpochSameKeyAcceptsExistingEmailIdentityData(t *testing.T) {
	db := isolatedDB(t)
	reset(t, db)
	p := protector(t, 15)
	if err := runInit(t, db, p); err != nil {
		t.Fatal("fresh initialization failed")
	}
	now := time.Now().UTC().Format("2006-01-02 15:04:05.000000")
	result, err := db.Exec(`INSERT INTO user(created_at,updated_at) VALUES (?,?)`, now, now)
	if err != nil {
		t.Fatal("could not seed synthetic user")
	}
	userID, _ := result.LastInsertId()
	if _, err := db.Exec(`INSERT INTO login_identity(id,user_id,kind,provider_scope,subject_enc,subject_lookup_hmac,key_id,state,verified_at,created_at,updated_at) VALUES ('existing-email',?,'email','default',X'01',REPEAT(CHAR(6),32),'key','active',?,?,?)`, userID, now, now, now); err != nil {
		t.Fatal("could not seed active synthetic email identity")
	}
	if err := runInit(t, db, p); err != nil {
		t.Fatal("matching epoch rejected existing valid email identity data")
	}
}

func TestIdentityEpochMatchingRowRequiresAllEmailTables(t *testing.T) {
	db := isolatedDB(t)
	createDDL := map[string]string{
		"auth_email_login_slot": `CREATE TABLE auth_email_login_slot (email_index BINARY(32) NOT NULL PRIMARY KEY, challenge_id VARBINARY(128) NOT NULL) ENGINE=InnoDB`,
		"auth_email_login_challenge": `CREATE TABLE auth_email_login_challenge (
			challenge_id VARBINARY(128) NOT NULL PRIMARY KEY,email_index BINARY(32) NOT NULL,email_ciphertext VARBINARY(512) NOT NULL,
			email_key_id VARBINARY(64) NOT NULL,nonce VARBINARY(256) NOT NULL,public_x CHAR(43) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
			public_y CHAR(43) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,key_fingerprint BINARY(32) NOT NULL,code_digest BINARY(32) NOT NULL,
			attempts SMALLINT UNSIGNED NOT NULL,max_attempts SMALLINT UNSIGNED NOT NULL,proof_max_lifetime_ns BIGINT UNSIGNED NOT NULL,
			proof_clock_skew_ns BIGINT UNSIGNED NOT NULL,created_at DATETIME(6) NOT NULL,expires_at DATETIME(6) NOT NULL,consumed_at DATETIME(6) NULL,
			KEY auth_email_challenge_expiry (expires_at),CONSTRAINT auth_email_attempt_limit CHECK (max_attempts BETWEEN 1 AND 10 AND attempts <= max_attempts)) ENGINE=InnoDB`,
		"login_identity": `CREATE TABLE login_identity (
			id VARBINARY(128) NOT NULL PRIMARY KEY,user_id BIGINT UNSIGNED NOT NULL,kind VARCHAR(32) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
			provider_scope VARBINARY(64) NOT NULL,subject_enc VARBINARY(512) NOT NULL,subject_lookup_hmac BINARY(32) NOT NULL,
			key_id VARBINARY(64) NOT NULL,state VARCHAR(16) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
			active_slot TINYINT GENERATED ALWAYS AS (CASE WHEN state = 'active' THEN 1 ELSE NULL END) STORED,
			verified_at DATETIME(6) NOT NULL,created_at DATETIME(6) NOT NULL,updated_at DATETIME(6) NOT NULL,
			UNIQUE KEY identity_active_subject (kind,provider_scope,subject_lookup_hmac,active_slot),
			UNIQUE KEY identity_active_kind (user_id,kind,active_slot),CONSTRAINT login_identity_user_fk FOREIGN KEY (user_id) REFERENCES user(id),
			CONSTRAINT login_identity_kind_valid CHECK (kind IN ('email','phone','wechat_mini')),
			CONSTRAINT login_identity_state_valid CHECK (state IN ('active','revoked'))) ENGINE=InnoDB`,
	}
	p := protector(t, 18)
	for table, create := range createDDL {
		t.Run(table, func(t *testing.T) {
			reset(t, db)
			if err := runInit(t, db, p); err != nil {
				t.Fatal("could not seed matching epoch")
			}
			if _, err := db.Exec("DROP TABLE " + table); err != nil {
				t.Fatal("could not prepare missing-table case")
			}
			if err := runInit(t, db, p); !errors.Is(err, ErrIdentityEpoch) {
				t.Fatal("matching epoch accepted with an email table missing")
			}
			if _, err := db.Exec(create); err != nil {
				t.Fatal("could not restore isolated test schema")
			}
		})
	}
}

func TestIdentityEpochMatchingRowRequiresEmailColumns(t *testing.T) {
	db := isolatedDB(t)
	reset(t, db)
	p := protector(t, 19)
	if err := runInit(t, db, p); err != nil {
		t.Fatal("could not seed matching epoch")
	}
	if _, err := db.Exec(`ALTER TABLE auth_email_login_slot DROP COLUMN challenge_id`); err != nil {
		t.Fatal("could not prepare missing-column case")
	}
	defer func() {
		_, _ = db.Exec(`ALTER TABLE auth_email_login_slot ADD COLUMN challenge_id VARBINARY(128) NOT NULL`)
	}()
	if err := runInit(t, db, p); !errors.Is(err, ErrIdentityEpoch) {
		t.Fatal("matching epoch accepted with a required email column missing")
	}
}

func TestIdentityEpochMissingRowRejectsEveryEmailDataClass(t *testing.T) {
	db := isolatedDB(t)
	for _, tc := range []struct {
		name string
		seed func(*testing.T, *sql.DB)
	}{
		{"slot", func(t *testing.T, db *sql.DB) {
			_, err := db.Exec(`INSERT INTO auth_email_login_slot(email_index,challenge_id) VALUES (REPEAT(CHAR(1),32), 'challenge-slot')`)
			if err != nil {
				t.Fatal("could not seed synthetic slot")
			}
		}},
		{"challenge", func(t *testing.T, db *sql.DB) {
			_, err := db.Exec(`INSERT INTO auth_email_login_challenge (challenge_id,email_index,email_ciphertext,email_key_id,nonce,public_x,public_y,key_fingerprint,code_digest,attempts,max_attempts,proof_max_lifetime_ns,proof_clock_skew_ns,created_at,expires_at) VALUES ('challenge-data',REPEAT(CHAR(2),32),X'01','key',X'01',REPEAT('A',43),REPEAT('B',43),REPEAT(CHAR(3),32),REPEAT(CHAR(4),32),0,3,1,1,UTC_TIMESTAMP(6),UTC_TIMESTAMP(6))`)
			if err != nil {
				t.Fatal("could not seed synthetic challenge")
			}
		}},
		{"revoked email identity", func(t *testing.T, db *sql.DB) {
			now := time.Now().UTC().Format("2006-01-02 15:04:05.000000")
			result, err := db.Exec(`INSERT INTO user(created_at,updated_at) VALUES (?,?)`, now, now)
			if err != nil {
				t.Fatal("could not seed synthetic user")
			}
			id, _ := result.LastInsertId()
			_, err = db.Exec(`INSERT INTO login_identity(id,user_id,kind,provider_scope,subject_enc,subject_lookup_hmac,key_id,state,verified_at,created_at,updated_at) VALUES ('revoked-email',?,'email','default',X'01',REPEAT(CHAR(5),32),'key','revoked',?,?,?)`, id, now, now, now)
			if err != nil {
				t.Fatal("could not seed revoked synthetic identity")
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reset(t, db)
			tc.seed(t, db)
			if err := runInit(t, db, protector(t, 30)); !errors.Is(err, ErrIdentityEpoch) {
				t.Fatal("missing epoch was accepted for populated email state")
			}
			var count int
			if err := db.QueryRow(`SELECT COUNT(*) FROM auth_email_identity_key_epoch`).Scan(&count); err != nil || count != 0 {
				t.Fatal("failed initialization left an epoch row")
			}
		})
	}
}

func TestIdentityEpochCallerRollbackLeavesNoInsert(t *testing.T) {
	db := isolatedDB(t)
	reset(t, db)
	ctx := context.Background()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal("could not begin isolated transaction")
	}
	init, _ := NewIdentityEpochInitializer(protector(t, 40))
	if err := init.Initialize(ctx, tx); err != nil {
		t.Fatal("initialization failed")
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal("caller rollback failed")
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM auth_email_identity_key_epoch`).Scan(&count); err != nil || count != 0 {
		t.Fatal("initializer committed or escaped caller transaction")
	}
}

func TestIdentityEpochUsesCurrentReadAfterOlderSnapshot(t *testing.T) {
	db := isolatedDB(t)
	reset(t, db)
	ctx := context.Background()
	bootstrapTx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal("could not begin isolated transaction")
	}
	var before int
	if err := bootstrapTx.QueryRowContext(ctx, `SELECT COUNT(*) FROM auth_email_login_slot`).Scan(&before); err != nil || before != 0 {
		t.Fatal("could not establish empty consistent snapshot")
	}
	writer, err := db.BeginTx(ctx, nil)
	if err != nil {
		_ = bootstrapTx.Rollback()
		t.Fatal("could not begin writer transaction")
	}
	if _, err := writer.ExecContext(ctx, `INSERT INTO auth_email_login_slot(email_index,challenge_id) VALUES (REPEAT(CHAR(8),32), 'committed-after-snapshot')`); err != nil {
		_ = writer.Rollback()
		_ = bootstrapTx.Rollback()
		t.Fatal("could not add committed synthetic slot")
	}
	if err := writer.Commit(); err != nil {
		_ = bootstrapTx.Rollback()
		t.Fatal("could not commit writer transaction")
	}
	init, err := NewIdentityEpochInitializer(protector(t, 45))
	if err != nil {
		_ = bootstrapTx.Rollback()
		t.Fatal("could not construct initializer")
	}
	if err := init.Initialize(ctx, bootstrapTx); !errors.Is(err, ErrIdentityEpoch) {
		_ = bootstrapTx.Rollback()
		t.Fatal("initializer trusted an older snapshot and accepted populated state")
	}
	_ = bootstrapTx.Rollback()
}

func TestIdentityEpochConcurrentDifferentKeysAcceptOneEpoch(t *testing.T) {
	db := isolatedDB(t)
	reset(t, db)
	type result struct {
		p   *domain.EmailIdentityProtector
		err error
	}
	start := make(chan struct{})
	results := make(chan result, 2)
	for _, p := range []*domain.EmailIdentityProtector{protector(t, 50), protector(t, 60)} {
		go func(p *domain.EmailIdentityProtector) {
			<-start
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			results <- result{p: p, err: initialize(ctx, db, p)}
		}(p)
	}
	close(start)
	a, b := <-results, <-results
	if a.err == nil && b.err == nil {
		_, at, _ := a.p.KeyEpoch()
		_, bt, _ := b.p.KeyEpoch()
		if !hmac.Equal(at[:], bt[:]) {
			t.Fatal("two different key epochs were both accepted")
		}
	}
	var stored []byte
	if err := db.QueryRow(`SELECT key_tag FROM auth_email_identity_key_epoch WHERE slot=1`).Scan(&stored); err != nil || len(stored) != 32 {
		t.Fatal("concurrent initialization did not leave exactly one epoch")
	}
	_, at, _ := a.p.KeyEpoch()
	_, bt, _ := b.p.KeyEpoch()
	if !hmac.Equal(stored, at[:]) && !hmac.Equal(stored, bt[:]) {
		t.Fatal("stored epoch matched neither configured key")
	}
}

func TestIdentityEpochIncompleteMigrationAndRedaction(t *testing.T) {
	db := isolatedDB(t)
	reset(t, db)
	if _, err := db.Exec(`DROP TABLE auth_email_login_slot`); err != nil {
		t.Fatal("could not prepare missing-table case")
	}
	defer func() {
		_, _ = db.Exec(`CREATE TABLE auth_email_login_slot (email_index BINARY(32) NOT NULL PRIMARY KEY, challenge_id VARBINARY(128) NOT NULL) ENGINE=InnoDB`)
	}()
	if err := runInit(t, db, protector(t, 70)); !errors.Is(err, ErrIdentityEpoch) {
		t.Fatal("missing migration table was not rejected")
	}
	init, err := NewIdentityEpochInitializer(protector(t, 70))
	if err != nil {
		t.Fatal("could not construct initializer")
	}
	if _, err := json.Marshal(init); !errors.Is(err, ErrIdentityEpoch) {
		t.Fatal("initializer unexpectedly serialized")
	}
	if strings.Contains(fmt.Sprintf("%#v", init), "email-test-key") || strings.Contains(fmt.Sprint(init), "key_tag") {
		t.Fatal("initializer formatting exposed key or schema details")
	}
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	logger.Info("bootstrap", "initializer", init, "error", ErrIdentityEpoch)
	if strings.Contains(logs.String(), "email-test-key") || strings.Contains(logs.String(), "email identity") && strings.Contains(logs.String(), "epochTag") {
		t.Fatal("structured logging exposed key material")
	}
}
