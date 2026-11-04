package migrations

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func manifestFixture(t *testing.T) (string, Entry) {
	t.Helper()
	root := t.TempDir()
	for _, path := range []string{"scripts/dev", "apps/api/migrations/auth"} {
		if err := os.MkdirAll(filepath.Join(root, path), 0700); err != nil {
			t.Fatal(err)
		}
	}
	content := []byte("CREATE TABLE fixture (id INT);\n")
	name := "0001_fixture.up.sql"
	if err := os.WriteFile(filepath.Join(root, "apps/api/migrations/auth", name), content, 0600); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(content)
	entry := Entry{Order: 1, ID: "auth/" + name, Module: "auth", File: name, SHA256: hex.EncodeToString(sum[:])}
	writeManifest(t, root, manifest{Version: 1, Migrations: []Entry{entry}})
	return root, entry
}

func writeManifest(t *testing.T, root string, value any) {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "scripts/dev/migrations.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
}

func TestLoadManifestAndRejectTampering(t *testing.T) {
	root, entry := manifestFixture(t)
	entries, err := Load(root)
	if err != nil || len(entries) != 1 || entries[0].ID != entry.ID || string(entries[0].SQL) != "CREATE TABLE fixture (id INT);\n" {
		t.Fatalf("valid manifest rejected: %v", err)
	}
	for name, mutate := range map[string]func(string, Entry){
		"order gap": func(root string, e Entry) { e.Order = 2; writeManifest(t, root, manifest{1, []Entry{e}}) },
		"wrong checksum": func(root string, e Entry) {
			e.SHA256 = strings.Repeat("0", 64)
			writeManifest(t, root, manifest{1, []Entry{e}})
		},
		"traversal": func(root string, e Entry) {
			e.File = "../fixture.up.sql"
			e.ID = "auth/" + e.File
			writeManifest(t, root, manifest{1, []Entry{e}})
		},
		"duplicate": func(root string, e Entry) {
			other := e
			other.Order = 2
			writeManifest(t, root, manifest{1, []Entry{e, other}})
		},
		"unregistered": func(root string, _ Entry) {
			_ = os.WriteFile(filepath.Join(root, "apps/api/migrations/auth/0002_extra.up.sql"), []byte("SELECT 1;"), 0600)
		},
		"symlink": func(root string, _ Entry) {
			_ = os.Symlink("0001_fixture.up.sql", filepath.Join(root, "apps/api/migrations/auth/link"))
		},
		"empty": func(root string, _ Entry) { writeManifest(t, root, manifest{1, []Entry{}}) },
	} {
		t.Run(name, func(t *testing.T) {
			root, entry := manifestFixture(t)
			mutate(root, entry)
			if _, err := Load(root); err == nil {
				t.Fatal("invalid manifest accepted")
			}
		})
	}
	validJSON, err := json.Marshal(manifest{1, []Entry{entry}})
	if err != nil {
		t.Fatal(err)
	}
	for name, raw := range map[string]string{
		"duplicate JSON field": strings.Replace(string(validJSON), `"version":1`, `"version":1,"version":1`, 1),
		"unknown JSON field":   strings.TrimSuffix(string(validJSON), "}") + `,"unexpected":true}`,
		"trailing JSON":        string(validJSON) + " {}",
	} {
		t.Run(name, func(t *testing.T) {
			root, _ := manifestFixture(t)
			if err := os.WriteFile(filepath.Join(root, "scripts/dev/migrations.json"), []byte(raw), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := Load(root); err == nil {
				t.Fatal("ambiguous manifest accepted")
			}
		})
	}
}

func TestHistoryMustBeCleanPrefix(t *testing.T) {
	_, entry := manifestFixture(t)
	valid := appliedEntry{Order: entry.Order, ID: entry.ID, SHA256: entry.SHA256}
	if err := validateHistory([]appliedEntry{valid}, []Entry{entry}); err != nil {
		t.Fatal(err)
	}
	badHash, dirty, extra := valid, valid, []appliedEntry{valid, valid}
	badHash.SHA256 = strings.Repeat("0", 64)
	dirty.Dirty = true
	for _, history := range [][]appliedEntry{{badHash}, {dirty}, extra} {
		if err := validateHistory(history, []Entry{entry}); err == nil {
			t.Fatal("unsafe migration history accepted")
		}
	}
}

func TestConnectionTargetAndTransport(t *testing.T) {
	validSocket := "migrator:synthetic-secret@unix(/tmp/nerae.sock)/neraedb?parseTime=true&loc=UTC"
	validTCP := "migrator:synthetic-secret@tcp(db.example.test:3306)/neraedb?parseTime=true&loc=UTC&tls=true"
	for _, dsn := range []string{validSocket, validTCP} {
		db, err := Open(dsn, "neraedb")
		if err != nil {
			t.Fatalf("valid connection rejected: %v", err)
		}
		db.Close()
	}
	for _, dsn := range []string{
		strings.Replace(validSocket, "migrator:", "root:", 1),
		strings.Replace(validSocket, "/neraedb?", "/other?", 1),
		strings.Replace(validSocket, "/tmp/nerae.sock", "relative.sock", 1),
		strings.Replace(validSocket, "parseTime=true", "parseTime=false", 1),
		validSocket + "&autocommit=0",
		strings.Replace(validTCP, "&tls=true", "", 1),
		strings.Replace(validTCP, "tls=true", "tls=skip-verify", 1),
		validTCP + "&allowFallbackToPlaintext=true",
	} {
		db, err := Open(dsn, "neraedb")
		if err == nil {
			db.Close()
			t.Fatal("unsafe connection accepted")
		}
		if strings.Contains(err.Error(), "synthetic-secret") {
			t.Fatal("connection failure exposed a secret")
		}
	}
	if _, err := Open(validSocket, "bad/name"); err == nil {
		t.Fatal("invalid target name accepted")
	}
	if _, err := Open(validSocket, "nerae_db"); err == nil {
		t.Fatal("wildcard-capable target name accepted")
	}
}

func TestMigrationGrantsMustBeRestricted(t *testing.T) {
	usage := "GRANT USAGE ON *.* TO `migrator`@`localhost`"
	target := "GRANT SELECT, INSERT, UPDATE, CREATE, ALTER, DROP, INDEX, REFERENCES ON `neraedb`.* TO `migrator`@`localhost`"
	if !restrictedGrants([]string{usage, target}, "neraedb") {
		t.Fatal("dedicated account grants rejected")
	}
	for _, grants := range [][]string{
		{usage},
		{usage, target, "GRANT SELECT ON `otherdb`.* TO `migrator`@`localhost`"},
		{usage, target, "GRANT SELECT ON *.* TO `migrator`@`localhost`"},
		{usage, target, "GRANT `admin`@`localhost` TO `migrator`@`localhost`"},
		{usage, target + " WITH GRANT OPTION"},
		{usage, "GRANT ALL PRIVILEGES ON `neraedb`.* TO `migrator`@`localhost`"},
		{usage, "GRANT EXECUTE ON `neraedb`.* TO `migrator`@`localhost`"},
		{usage, "GRANT TRIGGER ON `neraedb`.* TO `migrator`@`localhost`"},
		{usage, "GRANT SELECT, CREATE ON `neraedb`.* TO `migrator`@`localhost`"},
		{usage, "GRANT CREATE USER ON `neraedb`.* TO `migrator`@`localhost`"},
		{usage, "GRANT ALL PRIVILEGES ON `nerae%`.* TO `migrator`@`localhost`"},
	} {
		if restrictedGrants(grants, "neraedb") {
			t.Fatal("unsafe migration grants accepted")
		}
	}
}

func TestSupportedMySQLVersion(t *testing.T) {
	for _, version := range []string{"8.0.16", "8.0.46-0ubuntu0.24.04.4", "8.4.13"} {
		if !supportedVersion(version) {
			t.Fatalf("supported version rejected: %s", version)
		}
	}
	for _, version := range []string{"8.0.15", "8.1.0", "9.0.0", "10.11.2-MariaDB", "8.0.x"} {
		if supportedVersion(version) {
			t.Fatalf("unsupported version accepted: %s", version)
		}
	}
}
