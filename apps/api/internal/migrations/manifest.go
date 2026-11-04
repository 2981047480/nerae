// Package migrations validates the release migration manifest before a database
// is touched. The deployment bundle uses the same manifest as local testing.
package migrations

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
)

var (
	modulePattern   = regexp.MustCompile(`^[a-z][a-z0-9_]{0,31}$`)
	filenamePattern = regexp.MustCompile(`^[0-9]{4}_[a-z0-9_]+\.up\.sql$`)
	checksumPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)
)

type Entry struct {
	Order  int    `json:"order"`
	ID     string `json:"id"`
	Module string `json:"module"`
	File   string `json:"file"`
	SHA256 string `json:"sha256"`
	SQL    []byte `json:"-"`
}

type manifest struct {
	Version    int     `json:"version"`
	Migrations []Entry `json:"migrations"`
}

// rejectDuplicates prevents a JSON decoder from silently accepting an earlier
// manifest field that differs from the last one.
func rejectDuplicates(dec *json.Decoder) error {
	token, err := dec.Token()
	if err != nil {
		return err
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		seen := make(map[string]bool)
		for dec.More() {
			name, err := dec.Token()
			if err != nil {
				return err
			}
			key, ok := name.(string)
			if !ok || seen[key] {
				return errors.New("duplicate or invalid manifest field")
			}
			seen[key] = true
			if err := rejectDuplicates(dec); err != nil {
				return err
			}
		}
		_, err = dec.Token()
		return err
	case '[':
		for dec.More() {
			if err := rejectDuplicates(dec); err != nil {
				return err
			}
		}
		_, err = dec.Token()
		return err
	default:
		return errors.New("invalid manifest structure")
	}
}

func directory(path string) bool {
	info, err := os.Lstat(path)
	return err == nil && info.IsDir() && info.Mode()&os.ModeSymlink == 0
}

func readFile(path string, limit int64) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() > limit {
		return nil, errors.New("migration file unavailable")
	}
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, errors.New("migration file unavailable")
	}
	file := os.NewFile(uintptr(fd), "migration-file")
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(info, opened) || !opened.Mode().IsRegular() {
		return nil, errors.New("migration file changed")
	}
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil || int64(len(data)) > limit {
		return nil, errors.New("migration file unavailable")
	}
	return data, nil
}

// Load reads only the registered, checksum-matched release files. It rejects
// unregistered up migrations and symbolic links under the migration tree.
func Load(root string) ([]Entry, error) {
	if !filepath.IsAbs(root) || !directory(root) {
		return nil, errors.New("invalid migration root")
	}
	for _, part := range []string{"scripts", "scripts/dev", "apps", "apps/api", "apps/api/migrations"} {
		if !directory(filepath.Join(root, part)) {
			return nil, errors.New("invalid migration directory")
		}
	}
	data, err := readFile(filepath.Join(root, "scripts/dev/migrations.json"), 4<<20)
	if err != nil {
		return nil, err
	}
	duplicateCheck := json.NewDecoder(bytes.NewReader(data))
	if err := rejectDuplicates(duplicateCheck); err != nil {
		return nil, errors.New("invalid migration manifest")
	}
	if _, err := duplicateCheck.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("invalid migration manifest")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var value manifest
	if err := decoder.Decode(&value); err != nil || value.Version != 1 || len(value.Migrations) == 0 {
		return nil, errors.New("invalid migration manifest")
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return nil, errors.New("invalid migration manifest")
	}
	base := filepath.Join(root, "apps/api/migrations")
	seen := make(map[string]bool, len(value.Migrations))
	registered := make(map[string]bool, len(value.Migrations))
	for i := range value.Migrations {
		entry := &value.Migrations[i]
		if entry.Order != i+1 || !modulePattern.MatchString(entry.Module) || !filenamePattern.MatchString(entry.File) ||
			entry.ID != entry.Module+"/"+entry.File || seen[entry.ID] || !checksumPattern.MatchString(entry.SHA256) ||
			!directory(filepath.Join(base, entry.Module)) {
			return nil, fmt.Errorf("invalid migration entry %d", i+1)
		}
		seen[entry.ID] = true
		path := filepath.Join(base, entry.Module, entry.File)
		entry.SQL, err = readFile(path, 4<<20)
		if err != nil || len(bytes.TrimSpace(entry.SQL)) == 0 {
			return nil, fmt.Errorf("migration %d unavailable", i+1)
		}
		sum := sha256.Sum256(entry.SQL)
		if hex.EncodeToString(sum[:]) != entry.SHA256 {
			return nil, fmt.Errorf("migration %d checksum mismatch", i+1)
		}
		registered[filepath.Clean(path)] = true
	}
	if err := filepath.WalkDir(base, func(path string, item os.DirEntry, err error) error {
		if err != nil || item.Type()&os.ModeSymlink != 0 {
			return errors.New("unsafe migration tree")
		}
		if !item.IsDir() && strings.HasSuffix(item.Name(), ".up.sql") && !registered[filepath.Clean(path)] {
			return errors.New("unregistered migration")
		}
		return nil
	}); err != nil {
		return nil, errors.New("invalid migration tree")
	}
	return value.Migrations, nil
}
