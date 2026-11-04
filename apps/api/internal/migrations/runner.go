package migrations

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"
)

// Disallow '_' and '%' because MySQL can interpret them as wildcards in
// database-level grants. A dedicated, literal database name is required.
var databasePattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9]{0,46}$`)
var mysqlVersionPattern = regexp.MustCompile(`^8\.(0|4)\.([0-9]+)(?:[-+].*)?$`)
var grantAccountPattern = regexp.MustCompile("^`[^`\\r\\n]+`@`[^`\\r\\n]+`$")

var databasePrivileges = map[string]bool{
	"ALTER": true, "CREATE": true, "DROP": true, "INDEX": true,
	"INSERT": true, "REFERENCES": true, "SELECT": true, "UPDATE": true,
}

// Only direct privileges on this literal database and global USAGE are
// accepted. Roles, cross-database grants, administrative privileges and GRANT
// OPTION must be removed from the dedicated migration account.
func restrictedGrants(grants []string, database string) bool {
	if !databasePattern.MatchString(database) || len(grants) == 0 {
		return false
	}
	targetFound := false
	seenPrivileges := make(map[string]bool, len(databasePrivileges))
	for _, grant := range grants {
		privileges, rest, ok := strings.Cut(strings.TrimPrefix(grant, "GRANT "), " ON ")
		if !strings.HasPrefix(grant, "GRANT ") || !ok {
			return false
		}
		target, account, ok := strings.Cut(rest, " TO ")
		if !ok || !grantAccountPattern.MatchString(account) {
			return false
		}
		if target == "*.*" && privileges == "USAGE" {
			continue
		}
		if target != "`"+database+"`.*" {
			return false
		}
		for _, privilege := range strings.Split(privileges, ", ") {
			if !databasePrivileges[privilege] || seenPrivileges[privilege] {
				return false
			}
			seenPrivileges[privilege] = true
		}
		targetFound = true
	}
	return targetFound && len(seenPrivileges) == len(databasePrivileges)
}

func supportedVersion(version string) bool {
	match := mysqlVersionPattern.FindStringSubmatch(version)
	if len(match) != 3 {
		return false
	}
	patch, err := strconv.Atoi(match[2])
	return err == nil && (match[1] == "4" || patch >= 16)
}

type appliedEntry struct {
	Order  int
	ID     string
	SHA256 string
	Dirty  bool
}

func validateHistory(history []appliedEntry, entries []Entry) error {
	if len(history) > len(entries) {
		return errors.New("migration history exceeds manifest")
	}
	for i, row := range history {
		if row.Order != entries[i].Order || row.ID != entries[i].ID || row.SHA256 != entries[i].SHA256 {
			return fmt.Errorf("migration history differs at order %d", i+1)
		}
		if row.Dirty {
			return fmt.Errorf("migration %d is dirty; inspect the database before retrying", row.Order)
		}
	}
	return nil
}

// Open accepts only an explicitly named database and a protected connection.
// It never returns driver errors, which can contain a DSN or secret material.
func Open(dsn, expectedDatabase string) (*sql.DB, error) {
	if !databasePattern.MatchString(expectedDatabase) {
		return nil, errors.New("invalid migration database")
	}
	cfg, err := mysql.ParseDSN(dsn)
	if err != nil || cfg.User == "" || strings.EqualFold(cfg.User, "root") || cfg.DBName != expectedDatabase ||
		!cfg.ParseTime || cfg.Loc == nil || cfg.Loc.String() != "UTC" || cfg.AllowFallbackToPlaintext || len(cfg.Params) != 0 {
		return nil, errors.New("invalid migration connection")
	}
	switch cfg.Net {
	case "unix":
		if !filepath.IsAbs(cfg.Addr) || filepath.Clean(cfg.Addr) != cfg.Addr {
			return nil, errors.New("invalid migration connection")
		}
	case "tcp":
		if cfg.TLSConfig != "true" {
			return nil, errors.New("invalid migration connection")
		}
	default:
		return nil, errors.New("invalid migration connection")
	}
	cfg.MultiStatements = true
	cfg.Timeout = 5 * time.Second
	cfg.ReadTimeout = 30 * time.Second
	cfg.WriteTimeout = 30 * time.Second
	connector, err := mysql.NewConnector(cfg)
	if err != nil {
		return nil, errors.New("invalid migration connection")
	}
	db := sql.OpenDB(connector)
	db.SetMaxOpenConns(1)
	return db, nil
}

// Apply keeps a dirty row across any failed or uncertain DDL outcome. It never
// automatically repairs or rolls back a migration, because MySQL DDL can commit
// independently of a transaction.
func Apply(ctx context.Context, db *sql.DB, expectedDatabase string, entries []Entry) error {
	if ctx == nil || db == nil || !databasePattern.MatchString(expectedDatabase) || len(entries) == 0 {
		return errors.New("invalid migration input")
	}
	conn, err := db.Conn(ctx)
	if err != nil {
		return errors.New("migration database unavailable")
	}
	defer conn.Close()
	// Migration SQL can change session settings. Never return this session to a
	// shared pool, even after success or an uncertain outcome.
	defer func() { _ = conn.Raw(func(any) error { return driver.ErrBadConn }) }()
	var actual string
	var version string
	var autocommit int
	if err := conn.QueryRowContext(ctx, "SELECT DATABASE(), @@autocommit, VERSION()").Scan(&actual, &autocommit, &version); err != nil ||
		actual != expectedDatabase || autocommit != 1 {
		return errors.New("migration database does not match the configured target")
	}
	if !supportedVersion(version) {
		return errors.New("unsupported MySQL version")
	}
	// This is deliberately the same lock name used by scripts/dev/migrate.go.
	lockName := "nerae_migrations_" + expectedDatabase
	var locked sql.NullInt64
	if err := conn.QueryRowContext(ctx, "SELECT GET_LOCK(?, 10)", lockName).Scan(&locked); err != nil || !locked.Valid || locked.Int64 != 1 {
		return errors.New("migration lock unavailable")
	}
	defer func() {
		release, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_, _ = conn.ExecContext(release, "SELECT RELEASE_LOCK(?)", lockName)
	}()
	var activeRoles string
	if err := conn.QueryRowContext(ctx, "SELECT CURRENT_ROLE()").Scan(&activeRoles); err != nil || activeRoles != "NONE" {
		return errors.New("migration account must not have active roles")
	}
	if _, err := conn.ExecContext(ctx, "SET SESSION sql_quote_show_create = 1"); err != nil {
		return errors.New("migration account permissions unavailable")
	}
	grants, err := conn.QueryContext(ctx, "SHOW GRANTS FOR CURRENT_USER()")
	if err != nil {
		return errors.New("migration account permissions unavailable")
	}
	var grantLines []string
	for grants.Next() {
		var line string
		if err := grants.Scan(&line); err != nil {
			grants.Close()
			return errors.New("migration account permissions unavailable")
		}
		grantLines = append(grantLines, line)
	}
	if err := grants.Err(); err != nil {
		grants.Close()
		return errors.New("migration account permissions unavailable")
	}
	grants.Close()
	if !restrictedGrants(grantLines, expectedDatabase) {
		return errors.New("migration account must be restricted to the target database")
	}
	var ledgerCount int
	if err := conn.QueryRowContext(ctx, `SELECT COUNT(*) FROM information_schema.tables
		WHERE table_schema = DATABASE() AND table_name = 'nerae_schema_migrations'`).Scan(&ledgerCount); err != nil || ledgerCount > 1 {
		return errors.New("migration history unavailable")
	}
	if ledgerCount == 0 {
		var tables int
		if err := conn.QueryRowContext(ctx, `SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = DATABASE()`).Scan(&tables); err != nil {
			return errors.New("migration database unavailable")
		}
		if tables != 0 {
			return errors.New("database is not empty and has no migration history")
		}
		_, err = conn.ExecContext(ctx, `CREATE TABLE nerae_schema_migrations (
			sequence_no INT NOT NULL PRIMARY KEY,
			migration_id VARCHAR(255) NOT NULL UNIQUE,
			checksum CHAR(64) NOT NULL,
			dirty BOOLEAN NOT NULL,
			applied_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
		) ENGINE=InnoDB`)
		if err != nil {
			return errors.New("migration history initialization failed")
		}
	}
	rows, err := conn.QueryContext(ctx, "SELECT sequence_no, migration_id, checksum, dirty FROM nerae_schema_migrations ORDER BY sequence_no")
	if err != nil {
		return errors.New("migration history unavailable")
	}
	var history []appliedEntry
	for rows.Next() {
		var row appliedEntry
		if err := rows.Scan(&row.Order, &row.ID, &row.SHA256, &row.Dirty); err != nil {
			rows.Close()
			return errors.New("migration history unavailable")
		}
		history = append(history, row)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return errors.New("migration history unavailable")
	}
	rows.Close()
	if err := validateHistory(history, entries); err != nil {
		return err
	}
	for _, entry := range entries[len(history):] {
		if _, err := conn.ExecContext(ctx, "INSERT INTO nerae_schema_migrations (sequence_no, migration_id, checksum, dirty) VALUES (?, ?, ?, TRUE)",
			entry.Order, entry.ID, entry.SHA256); err != nil {
			return fmt.Errorf("cannot mark migration %d dirty; inspect before retrying", entry.Order)
		}
		if _, err := conn.ExecContext(ctx, string(entry.SQL)); err != nil {
			return fmt.Errorf("migration %d failed or is uncertain and remains dirty", entry.Order)
		}
		if err := conn.QueryRowContext(ctx, "SELECT DATABASE(), @@autocommit").Scan(&actual, &autocommit); err != nil ||
			actual != expectedDatabase || autocommit != 1 {
			return fmt.Errorf("migration %d changed the database session and remains dirty", entry.Order)
		}
		result, err := conn.ExecContext(ctx, "UPDATE nerae_schema_migrations SET dirty = FALSE WHERE sequence_no = ? AND dirty = TRUE", entry.Order)
		if err != nil {
			return fmt.Errorf("migration %d completed but its history is uncertain", entry.Order)
		}
		changed, err := result.RowsAffected()
		if err != nil || changed != 1 {
			return fmt.Errorf("migration %d completed but its history is uncertain", entry.Order)
		}
	}
	return nil
}
