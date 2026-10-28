package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func fixture(t *testing.T) (string, migration) {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "apps", "api", "migrations", "auth")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	data := []byte("CREATE TABLE local_test (id INT);\n")
	name := "0001_create.up.sql"
	if err := os.WriteFile(filepath.Join(dir, name), data, 0600); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	return root, migration{Order: 1, ID: "auth/" + name, Module: "auth", File: name, SHA256: hex.EncodeToString(sum[:])}
}
func jsonManifest(t *testing.T, ms ...migration) []byte {
	t.Helper()
	if ms == nil {
		ms = []migration{}
	}
	b, err := json.Marshal(manifest{Version: 1, Migrations: ms})
	if err != nil {
		t.Fatal(err)
	}
	return b
}
func TestManifestNormalAndEmpty(t *testing.T) {
	root, entry := fixture(t)
	entries, err := validateManifest(root, jsonManifest(t, entry))
	if err != nil || len(entries) != 1 || string(entries[0].SQL) != "CREATE TABLE local_test (id INT);\n" {
		t.Fatalf("valid manifest rejected: %v", err)
	}
	emptyRoot := t.TempDir()
	entries, err = validateManifest(emptyRoot, jsonManifest(t))
	if err != nil || len(entries) != 0 {
		t.Fatalf("empty manifest rejected: %v", err)
	}
}
func TestManifestRejectsConflicts(t *testing.T) {
	root, entry := fixture(t)
	altered := entry
	altered.Order = 3
	duplicate := entry
	duplicate.Order = 2
	badPath := entry
	badPath.File = "../secret.up.sql"
	badPath.ID = "auth/../secret.up.sql"
	badHash := entry
	badHash.SHA256 = "0000000000000000000000000000000000000000000000000000000000000000"
	unknown := entry
	unknown.Module = "other"
	unknown.ID = "other/" + unknown.File
	cases := map[string][]byte{
		"gap":              jsonManifest(t, entry, altered),
		"duplicate id":     jsonManifest(t, entry, duplicate),
		"traversal":        jsonManifest(t, badPath),
		"checksum":         jsonManifest(t, badHash),
		"missing module":   jsonManifest(t, unknown),
		"unregistered":     jsonManifest(t),
		"duplicate key":    []byte(`{"version":1,"version":1,"migrations":[]}`),
		"unknown property": []byte(`{"version":1,"migrations":[],"unexpected":true}`),
		"trailing object":  []byte(`{"version":1,"migrations":[]} {}`),
	}
	for name, data := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := validateManifest(root, data); err == nil {
				t.Fatal("expected failure")
			}
		})
	}
}
func TestManifestRejectsSymlink(t *testing.T) {
	root, entry := fixture(t)
	dir := filepath.Join(root, "apps", "api", "migrations", "auth")
	if err := os.Rename(dir, dir+"-real"); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(dir+"-real", dir); err != nil {
		t.Fatal(err)
	}
	if _, err := validateManifest(root, jsonManifest(t, entry)); err == nil {
		t.Fatal("symlink accepted")
	}
}
func TestAppliedHistoryMustMatchAndBeClean(t *testing.T) {
	_, entry := fixture(t)
	valid := appliedMigration{Order: entry.Order, ID: entry.ID, SHA256: entry.SHA256}
	if err := validateApplied([]appliedMigration{valid}, []migration{entry}); err != nil {
		t.Fatal(err)
	}
	badHash := valid
	badHash.SHA256 = "different"
	badID := valid
	badID.ID = "other"
	dirty := valid
	dirty.Dirty = true
	cases := map[string][]appliedMigration{"hash": {badHash}, "id": {badID}, "dirty": {dirty}, "extra": {valid, valid}}
	for name, history := range cases {
		t.Run(name, func(t *testing.T) {
			if err := validateApplied(history, []migration{entry}); err == nil {
				t.Fatal("expected failure")
			}
		})
	}
}

func TestLoadEnvironmentBindsCredentialsAndDatabaseToWorktree(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(root))
	token := hex.EncodeToString(sum[:])[:16]
	ns := "nerae_test_" + token
	env := environment{Version: 1, Environment: "test", Workspace: root, Namespace: ns, Database: ns,
		RedisPrefix: "nerae:test:" + token + ":", ObjectPrefix: "nerae/test/" + token + "/", MySQLPort: 23306, RedisPort: 26379}
	dir := filepath.Join(root, ".review", "nerae-test-env")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(env)
	if err != nil {
		t.Fatal(err)
	}
	state := filepath.Join(dir, "state.json")
	if err := os.WriteFile(state, data, 0600); err != nil {
		t.Fatal(err)
	}
	secret := filepath.Join(dir, "mysql-app-password")
	if err := os.WriteFile(secret, []byte("a-long-random-password"), 0600); err != nil {
		t.Fatal(err)
	}
	if got, _, err := loadEnvironment(root); err != nil || got.Database != ns {
		t.Fatalf("valid state rejected: %v", err)
	}
	env.Database = "production"
	data, _ = json.Marshal(env)
	if err := os.WriteFile(state, data, 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := loadEnvironment(root); err == nil {
		t.Fatal("production database accepted")
	}
	env.Database = ns
	data, _ = json.Marshal(env)
	if err := os.WriteFile(state, data, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(secret, 0644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := loadEnvironment(root); err == nil {
		t.Fatal("public credential accepted")
	}
}

func TestRootFromGitIgnoresEnvironmentRedirect(t *testing.T) {
	parent, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	a, b := filepath.Join(parent, "a"), filepath.Join(parent, "b")
	for _, root := range []string{a, b} {
		cmd := exec.Command("git", "init", "--quiet", root)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git init: %v: %s", err, out)
		}
	}
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(a); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(old) })
	t.Setenv("GIT_DIR", filepath.Join(b, ".git"))
	t.Setenv("GIT_WORK_TREE", b)
	root, err := rootFromGit()
	if err != nil || root != a {
		t.Fatalf("root = %q, error = %v; wanted %q", root, err, a)
	}
}
