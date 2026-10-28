// migrate is a local-test-only migration executor. Run it from apps/api so that
// it uses the API's pinned MySQL driver: go run ../../scripts/dev/migrate.go check|up.
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/go-sql-driver/mysql"
)

var modulePattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,31}$`)
var filenamePattern = regexp.MustCompile(`^[0-9]{4}_[a-z0-9_]+\.up\.sql$`)
var checksumPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

type manifest struct {
	Version    int         `json:"version"`
	Migrations []migration `json:"migrations"`
}
type migration struct {
	Order  int    `json:"order"`
	ID     string `json:"id"`
	Module string `json:"module"`
	File   string `json:"file"`
	SHA256 string `json:"sha256"`
	SQL    []byte `json:"-"`
}
type environment struct {
	Version      int    `json:"version"`
	Environment  string `json:"environment"`
	Workspace    string `json:"workspace"`
	Namespace    string `json:"namespace"`
	Database     string `json:"database"`
	RedisPrefix  string `json:"redis_prefix"`
	ObjectPrefix string `json:"object_prefix"`
	MySQLPort    int    `json:"mysql_port"`
	RedisPort    int    `json:"redis_port"`
}

// json.Decoder normally accepts duplicate fields, potentially hiding an earlier value.
func rejectDuplicateKeys(dec *json.Decoder) error {
	token, err := dec.Token()
	if err != nil {
		return err
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delimiter {
	case '{':
		keys := map[string]bool{}
		for dec.More() {
			keyToken, err := dec.Token()
			if err != nil {
				return err
			}
			key, ok := keyToken.(string)
			if !ok {
				return errors.New("invalid JSON object key")
			}
			if keys[key] {
				return fmt.Errorf("duplicate JSON key %q", key)
			}
			keys[key] = true
			if err := rejectDuplicateKeys(dec); err != nil {
				return err
			}
		}
		_, err = dec.Token()
		return err
	case '[':
		for dec.More() {
			if err := rejectDuplicateKeys(dec); err != nil {
				return err
			}
		}
		_, err = dec.Token()
		return err
	default:
		return errors.New("invalid JSON structure")
	}
}

func decodeStrict(data []byte, target any) error {
	duplicates := json.NewDecoder(bytes.NewReader(data))
	if err := rejectDuplicateKeys(duplicates); err != nil {
		return err
	}
	if _, err := duplicates.Token(); !errors.Is(err, io.EOF) {
		return errors.New("trailing JSON content")
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(target); err != nil {
		return err
	}
	if err := dec.Decode(new(any)); !errors.Is(err, io.EOF) {
		return errors.New("trailing JSON content")
	}
	return nil
}

func validateManifest(root string, data []byte) ([]migration, error) {
	var m manifest
	if err := decodeStrict(data, &m); err != nil {
		return nil, fmt.Errorf("manifest: %w", err)
	}
	if m.Version != 1 || m.Migrations == nil {
		return nil, errors.New("manifest version must be 1 and migrations must be an array")
	}
	migrationRoot := filepath.Join(root, "apps", "api", "migrations")
	for _, path := range []string{filepath.Join(root, "apps"), filepath.Join(root, "apps", "api")} {
		info, err := os.Lstat(path)
		if errors.Is(err, os.ErrNotExist) && len(m.Migrations) == 0 {
			break
		}
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return nil, errors.New("unsafe migration root ancestor")
		}
	}
	seenID, registered := map[string]bool{}, map[string]bool{}
	for i := range m.Migrations {
		entry := &m.Migrations[i]
		if entry.Order != i+1 {
			return nil, fmt.Errorf("migration order must be contiguous and ordered: expected %d", i+1)
		}
		if !modulePattern.MatchString(entry.Module) || !filenamePattern.MatchString(entry.File) || entry.ID != entry.Module+"/"+entry.File {
			return nil, fmt.Errorf("migration %d has invalid module, filename or ID", entry.Order)
		}
		if seenID[entry.ID] {
			return nil, fmt.Errorf("duplicate migration ID %s", entry.ID)
		}
		seenID[entry.ID] = true
		if !checksumPattern.MatchString(entry.SHA256) {
			return nil, fmt.Errorf("invalid checksum for %s", entry.ID)
		}
		path := filepath.Join(migrationRoot, entry.Module, entry.File)
		for _, component := range []string{migrationRoot, filepath.Join(migrationRoot, entry.Module), path} {
			info, err := os.Lstat(component)
			if err != nil {
				return nil, fmt.Errorf("migration %s missing or inaccessible", entry.ID)
			}
			if info.Mode()&os.ModeSymlink != 0 {
				return nil, fmt.Errorf("symlink is not allowed in migration path: %s", entry.ID)
			}
		}
		info, err := os.Stat(path)
		if err != nil || !info.Mode().IsRegular() || info.Size() > 4*1024*1024 {
			return nil, fmt.Errorf("migration %s must be a regular file at most 4 MiB", entry.ID)
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("cannot read migration %s", entry.ID)
		}
		sum := sha256.Sum256(content)
		if hex.EncodeToString(sum[:]) != entry.SHA256 {
			return nil, fmt.Errorf("checksum mismatch for %s", entry.ID)
		}
		if len(bytes.TrimSpace(content)) == 0 {
			return nil, fmt.Errorf("empty migration %s", entry.ID)
		}
		entry.SQL = content
		registered[filepath.Clean(path)] = true
	}
	err := filepath.WalkDir(migrationRoot, func(path string, d os.DirEntry, err error) error {
		if errors.Is(err, os.ErrNotExist) && path == migrationRoot && len(m.Migrations) == 0 {
			return nil
		}
		if err != nil {
			return err
		}
		if d.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("symlink is not allowed under migration root")
		}
		if !d.IsDir() && strings.HasSuffix(d.Name(), ".up.sql") && !registered[filepath.Clean(path)] {
			return fmt.Errorf("unregistered migration: %s", path)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("scan migrations: %w", err)
	}
	return m.Migrations, nil
}

func loadEnvironment(root string) (environment, string, error) {
	var env environment
	directory := filepath.Join(root, ".review", "nerae-test-env")
	for _, path := range []string{filepath.Join(root, ".review"), directory} {
		info, err := os.Lstat(path)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return env, "", errors.New("local test environment directory is missing or unsafe")
		}
		if path == directory && (info.Mode().Perm()&0077 != 0 || !ownedByCurrentUser(info)) {
			return env, "", errors.New("local test environment directory must be private and owned by the current user")
		}
	}
	statePath := filepath.Join(directory, "state.json")
	data, err := readPrivate(statePath)
	if err != nil {
		return env, "", err
	}
	if err := decodeStrict(data, &env); err != nil {
		return env, "", errors.New("invalid local test environment state")
	}
	sum := sha256.Sum256([]byte(root))
	token := hex.EncodeToString(sum[:])[:16]
	namespace := "nerae_test_" + token
	if env.Version != 1 || env.Environment != "test" || env.Workspace != root || env.Namespace != namespace || env.Database != namespace ||
		env.RedisPrefix != "nerae:test:"+token+":" || env.ObjectPrefix != "nerae/test/"+token+"/" ||
		env.MySQLPort < 1024 || env.MySQLPort > 65535 || env.RedisPort < 1024 || env.RedisPort > 65535 || env.MySQLPort == env.RedisPort {
		return env, "", errors.New("state does not match this test worktree")
	}
	password, err := readPrivate(filepath.Join(directory, "mysql-app-password"))
	if err != nil {
		return env, "", err
	}
	if len(password) < 16 || len(password) > 256 || strings.TrimSpace(string(password)) != string(password) {
		return env, "", errors.New("invalid local test credential")
	}
	return env, string(password), nil
}

func ownedByCurrentUser(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && stat.Uid == uint32(os.Getuid())
}

func readPrivate(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || !ownedByCurrentUser(info) || info.Size() > 65536 {
		return nil, errors.New("local state file is missing or not private")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, errors.New("cannot read local state file")
	}
	return data, nil
}

func rootFromGit() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", errors.New("cannot resolve current working directory")
	}
	cmd := exec.Command("git", "-C", cwd, "rev-parse", "--show-toplevel")
	for _, value := range os.Environ() {
		if !strings.HasPrefix(value, "GIT_") {
			cmd.Env = append(cmd.Env, value)
		}
	}
	out, err := cmd.Output()
	if err != nil {
		return "", errors.New("must run within a Git worktree")
	}
	root, err := filepath.EvalSymlinks(strings.TrimSpace(string(out)))
	if err != nil {
		return "", errors.New("cannot resolve worktree root")
	}
	current, err := filepath.EvalSymlinks(cwd)
	if err != nil {
		return "", errors.New("cannot resolve current working directory")
	}
	relative, err := filepath.Rel(root, current)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", errors.New("Git root does not contain current working directory")
	}
	return root, nil
}

type appliedMigration struct {
	Order  int
	ID     string
	SHA256 string
	Dirty  bool
}

func validateApplied(applied []appliedMigration, migrations []migration) error {
	if len(applied) > len(migrations) {
		return errors.New("database contains migrations absent from manifest")
	}
	for i, row := range applied {
		expected := migrations[i]
		if row.Order != expected.Order || row.ID != expected.ID || row.SHA256 != expected.SHA256 {
			return fmt.Errorf("database migration history differs at order %d", i+1)
		}
		if row.Dirty {
			return fmt.Errorf("migration %d is dirty; inspect and recover manually before retrying", row.Order)
		}
	}
	return nil
}

func apply(root string, migrations []migration) error {
	env, password, err := loadEnvironment(root)
	if err != nil {
		return err
	}
	config := mysql.NewConfig()
	config.User, config.Passwd, config.Net = "nerae_app", password, "tcp"
	config.Addr, config.DBName = "127.0.0.1:"+strconv.Itoa(env.MySQLPort), env.Database
	config.MultiStatements, config.Timeout, config.ReadTimeout, config.WriteTimeout = true, 5*time.Second, 30*time.Second, 30*time.Second
	db, err := sql.Open("mysql", config.FormatDSN())
	if err != nil {
		return errors.New("cannot open test database")
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	conn, err := db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("test database connection failed: %w", err)
	}
	defer conn.Close()
	var actualDB string
	if err := conn.QueryRowContext(ctx, "SELECT DATABASE()").Scan(&actualDB); err != nil || actualDB != env.Database {
		return errors.New("connected database does not match test namespace")
	}
	lockName := "nerae_migrations_" + env.Database
	var locked sql.NullInt64
	if err := conn.QueryRowContext(ctx, "SELECT GET_LOCK(?, 10)", lockName).Scan(&locked); err != nil || !locked.Valid || locked.Int64 != 1 {
		return errors.New("cannot acquire migration lock")
	}
	defer conn.ExecContext(context.Background(), "SELECT RELEASE_LOCK(?)", lockName)
	_, err = conn.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS nerae_schema_migrations (
        sequence_no INT NOT NULL PRIMARY KEY,
        migration_id VARCHAR(255) NOT NULL UNIQUE,
        checksum CHAR(64) NOT NULL,
        dirty BOOLEAN NOT NULL,
        applied_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
    ) ENGINE=InnoDB`)
	if err != nil {
		return fmt.Errorf("cannot initialize migration history: %w", err)
	}
	rows, err := conn.QueryContext(ctx, "SELECT sequence_no, migration_id, checksum, dirty FROM nerae_schema_migrations ORDER BY sequence_no")
	if err != nil {
		return fmt.Errorf("cannot read migration history: %w", err)
	}
	var applied []appliedMigration
	for rows.Next() {
		var row appliedMigration
		if err := rows.Scan(&row.Order, &row.ID, &row.SHA256, &row.Dirty); err != nil {
			rows.Close()
			return err
		}
		applied = append(applied, row)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	if err := validateApplied(applied, migrations); err != nil {
		return err
	}
	for _, migration := range migrations[len(applied):] {
		if _, err := conn.ExecContext(ctx, "INSERT INTO nerae_schema_migrations (sequence_no, migration_id, checksum, dirty) VALUES (?, ?, ?, TRUE)", migration.Order, migration.ID, migration.SHA256); err != nil {
			return fmt.Errorf("cannot mark migration %d dirty: %w", migration.Order, err)
		}
		if _, err := conn.ExecContext(ctx, string(migration.SQL)); err != nil {
			return fmt.Errorf("migration %d failed and remains dirty: %w", migration.Order, err)
		}
		if _, err := conn.ExecContext(ctx, "UPDATE nerae_schema_migrations SET dirty = FALSE WHERE sequence_no = ? AND dirty = TRUE", migration.Order); err != nil {
			return fmt.Errorf("migration %d completed but remains dirty: %w", migration.Order, err)
		}
		fmt.Printf("applied %d %s\n", migration.Order, migration.ID)
	}
	fmt.Printf("test database %s: %d migrations current\n", env.Database, len(migrations))
	return nil
}

func run(args []string) error {
	if len(args) != 1 || (args[0] != "check" && args[0] != "up") {
		return errors.New("usage: go run ../../scripts/dev/migrate.go check|up")
	}
	root, err := rootFromGit()
	if err != nil {
		return err
	}
	data, err := os.ReadFile(filepath.Join(root, "scripts", "dev", "migrations.json"))
	if err != nil {
		return errors.New("cannot read migration manifest")
	}
	migrations, err := validateManifest(root, data)
	if err != nil {
		return err
	}
	if args[0] == "check" {
		fmt.Printf("manifest valid: %d migrations\n", len(migrations))
		return nil
	}
	return apply(root, migrations)
}
func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "migration:", err)
		os.Exit(1)
	}
}
