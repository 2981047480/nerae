# Deployment migration executor

`apps/api/cmd/migrate` verifies the existing ordered manifest and SQL hashes
before opening a database. `check` is read-only. `up` requires an explicitly
named database, a non-root account, and either a Unix socket or verified TLS.
The database name is restricted to letters and digits because MySQL may treat
underscores and percent signs in database grants as wildcards. The connected
account must have no active or assigned roles, no global privileges beyond
USAGE, no grant option, and only the explicit privileges needed by this
manifest on that exact database. Broad `ALL`, routine execution and trigger
creation privileges are rejected. These checks restrict that account from
switching to or writing another schema while its grants remain unchanged. Only
MySQL 8.0.16 or later within the 8.0 series, or MySQL 8.4.x, is accepted.

The executor acquires the same per-database advisory lock as the existing
local-test migration tool. It refuses an untracked nonempty database and
requires any existing history to be an exact clean prefix of the manifest.
Each migration receives a dirty marker before its SQL is executed; an error or
uncertain outcome stops the run without automatic repair or rollback. MySQL
DDL may commit partially, so an operator must inspect a dirty database before
any further action. After each SQL file, the executor also rechecks the
selected database and autocommit; a changed session stays dirty and its
connection is discarded rather than reused.

## Verification

- Local `go test -race ./internal/migrations ./cmd/migrate` passed. The
  environment-dependent MySQL integration test was skipped in that run.
  The release-manifest `check` accepted the current 18 registered migrations.
- On the user-selected Tencent host, a fresh private socket-only MySQL 8.0.46
  and Redis 7.0.15 were started from extracted Ubuntu packages. The Linux
  migration executable applied all 18 migrations to a fresh synthetic schema;
  a second run completed without reapplying them. Another fresh synthetic
  schema exercised two simultaneous runners, multi-statement DDL, an account
  with access to another database, checksum drift and a deliberately failed
  migration that left a dirty marker. A separate case changed the selected
  database and autocommit after partial DDL; the executor detected the change,
  retained the dirty row and discarded the session. The API process integration
  test also passed against its separate synthetic schema. The successful
  fixture stopped its services and removed its own test data.
- A first packaging attempt included macOS `._*.up.sql` sidecar files. The
  validator correctly refused the unregistered files; a fresh archive created
  with `COPYFILE_DISABLE=1` contained no such entries and passed the rerun.
- Review identified that checking `DATABASE()` alone cannot prevent SQL from
  switching schemas. The executor now checks the account's actual grants and
  active roles after acquiring its migration lock and before making changes. The
  overbroad-account test above verifies that guard on MySQL 8.0.46.
- The same isolated test was subsequently run on the target host with the
  repository-pinned MySQL 8.4.13 and Redis 7.4.12 container images, both with
  networking disabled. All 18 migrations, the repeat run, restricted-grant,
  concurrent, failed-DDL, session-drift and API checks passed. The two test
  containers were stopped and their successful synthetic data removed. Two
  initial attempts reached the image's short-lived initialization server rather
  than its final server and failed to bootstrap; the harness now waits for the
  image's initialization-complete marker and final readiness before proceeding.
  A later run passed its functional tests but reported cleanup as unconfirmed
  because the Docker client's missing-object error was lowercase; inspection
  showed no remaining test containers. Cleanup classification was corrected,
  mounts were narrowed, and the full container test then passed again.

No production database, real account, SMTP credential or public endpoint was
used. The executor does not provision accounts, databases, backups or services;
an already populated schema without a compatible ledger is deliberately not
adopted. The release bundle and database administration must remain trusted;
existing privileged triggers and concurrent administrative grant changes are
outside this check. Real deployment remains to be independently validated.
