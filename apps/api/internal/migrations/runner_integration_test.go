package migrations

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"
)

// This test creates and alters tables. It only accepts the dedicated private
// synthetic database and socket created by the remote preflight harness.
func TestApplyAgainstPrivateMySQL(t *testing.T) {
	dsn := os.Getenv("NERAE_MIGRATION_TEST_DSN")
	if dsn == "" {
		t.Skip("private migration test database not configured")
	}
	config, err := mysql.ParseDSN(dsn)
	driftCase := os.Getenv("NERAE_MIGRATION_CASE") == "session_drift"
	expectedUser, expectedDB := "nerae_migrator", "neraemigrationexecutortest"
	if driftCase {
		expectedUser, expectedDB = "nerae_migratordrift", "neraemigrationdrifttest"
	}
	if err != nil || config.Net != "unix" || config.User != expectedUser || config.Passwd == "" ||
		config.DBName != expectedDB || !config.ParseTime || config.Loc == nil || config.Loc.String() != "UTC" {
		t.Fatal("test requires its dedicated private database")
	}
	base := filepath.Dir(config.Addr)
	info, err := os.Lstat(base)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0077 != 0 {
		t.Fatal("test socket directory is not private")
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != uint32(os.Getuid()) {
		t.Fatal("test socket directory has the wrong owner")
	}
	resolved, err := filepath.EvalSymlinks(base)
	parent := filepath.Dir(resolved)
	if err != nil || filepath.Base(config.Addr) != "mysql.sock" || !strings.HasPrefix(filepath.Base(resolved), "api-smoke-") ||
		!strings.HasPrefix(filepath.Base(parent), "nerae-package-preflight.") || filepath.Dir(parent) != "/tmp" {
		t.Fatal("test socket is outside the dedicated preflight directory")
	}
	socket, err := os.Lstat(config.Addr)
	if err != nil || socket.Mode()&os.ModeSocket == 0 || socket.Mode()&os.ModeSymlink != 0 {
		t.Fatal("test socket unavailable")
	}
	db, err := Open(dsn, config.DBName)
	if err != nil {
		t.Fatal("test connection rejected")
	}
	defer db.Close()
	second, err := Open(dsn, config.DBName)
	if err != nil {
		t.Fatal("second test connection rejected")
	}
	defer second.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	var tables int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = DATABASE()`).Scan(&tables); err != nil || tables != 0 {
		t.Fatal("dedicated test database is not empty")
	}
	entry := func(order int, name, content string) Entry {
		sum := sha256.Sum256([]byte(content))
		return Entry{Order: order, ID: "test/" + name, Module: "test", File: name,
			SHA256: hex.EncodeToString(sum[:]), SQL: []byte(content)}
	}
	first := entry(1, "0001_tables.up.sql", "DO SLEEP(0.3); CREATE TABLE migration_first (id INT); CREATE TABLE migration_second (id INT);")
	entries := []Entry{first}
	if !driftCase {
		badDSN := os.Getenv("NERAE_MIGRATION_BAD_DSN")
		badConfig, err := mysql.ParseDSN(badDSN)
		if err != nil || badConfig.User != "nerae_overbroad" || badConfig.Passwd == "" ||
			badConfig.Net != "unix" || badConfig.Addr != config.Addr || badConfig.DBName != config.DBName {
			t.Fatal("overbroad account fixture is unavailable")
		}
		badDB, err := Open(badDSN, config.DBName)
		if err != nil {
			t.Fatal("overbroad account fixture rejected too early")
		}
		crossSchema := entry(1, "0001_cross_schema.up.sql", "USE neraeother; CREATE TABLE migration_wrong_schema (id INT);")
		if err := Apply(ctx, badDB, config.DBName, []Entry{crossSchema}); err == nil {
			badDB.Close()
			t.Fatal("account with access to another database was accepted")
		}
		var otherTables int
		if err := badDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM information_schema.tables
			WHERE table_schema = 'neraeother' AND table_name = 'migration_wrong_schema'`).Scan(&otherTables); err != nil || otherTables != 0 {
			badDB.Close()
			t.Fatal("overbroad account modified another database")
		}
		badDB.Close()
		if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = DATABASE()`).Scan(&tables); err != nil || tables != 0 {
			t.Fatal("overbroad account modified the test database")
		}
	}
	if _, err := db.ExecContext(ctx, "CREATE TABLE unrelated_fixture (id INT)"); err != nil {
		t.Fatal("could not prepare nonempty test database")
	}
	if err := Apply(ctx, db, config.DBName, entries); err == nil {
		t.Fatal("nonempty database without history was accepted")
	}
	if _, err := db.ExecContext(ctx, "DROP TABLE unrelated_fixture"); err != nil {
		t.Fatal("could not reset synthetic test database")
	}
	var wait sync.WaitGroup
	wait.Add(2)
	errors := make(chan error, 2)
	for _, candidate := range []*sql.DB{db, second} {
		go func(candidate *sql.DB) {
			defer wait.Done()
			errors <- Apply(ctx, candidate, config.DBName, entries)
		}(candidate)
	}
	wait.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatalf("concurrent migration failed: %v", err)
		}
	}
	var count, dirty int
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*), SUM(dirty) FROM nerae_schema_migrations").Scan(&count, &dirty); err != nil || count != 1 || dirty != 0 {
		t.Fatal("concurrent migration did not produce one clean history entry")
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = DATABASE()
		AND table_name IN ('migration_first','migration_second')`).Scan(&count); err != nil || count != 2 {
		t.Fatal("multi-statement migration did not create both tables")
	}
	changed := first
	changed.SHA256 = strings.Repeat("0", 64)
	if err := Apply(ctx, db, config.DBName, []Entry{changed}); err == nil {
		t.Fatal("checksum drift was accepted")
	}
	partialSQL := "CREATE TABLE migration_partial (id INT); INVALID SQL;"
	if driftCase {
		partialSQL = "CREATE TABLE migration_partial (id INT); USE information_schema; SET autocommit=0;"
	}
	partial := entry(2, "0002_partial.up.sql", partialSQL)
	migrationErr := Apply(ctx, db, config.DBName, []Entry{first, partial})
	if migrationErr == nil {
		t.Fatal("failed migration was reported as successful")
	}
	if driftCase && !strings.Contains(migrationErr.Error(), "changed the database session") {
		t.Fatalf("session drift was not detected by the session check: %v", migrationErr)
	}
	if err := db.QueryRowContext(ctx, "SELECT dirty FROM nerae_schema_migrations WHERE sequence_no = 2").Scan(&dirty); err != nil || dirty != 1 {
		t.Fatal("failed migration did not remain dirty")
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = DATABASE()
		AND table_name = 'migration_partial'`).Scan(&count); err != nil || count != 1 {
		t.Fatal("partial DDL was not observed")
	}
	if err := Apply(ctx, db, config.DBName, []Entry{first, partial}); err == nil {
		t.Fatal("dirty migration was retried automatically")
	}
	if driftCase {
		t.Log("PASS: changed database session remains dirty and is discarded")
	} else {
		t.Log("PASS: restricted grants, concurrent migrations, multi-statement DDL, drift and dirty recovery gates")
	}
}
