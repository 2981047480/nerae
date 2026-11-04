# API and email worker

The API can assemble the implemented email authentication, device approval,
age declaration and training-record handlers when business mode is explicitly
enabled. The separate worker assembles the email outbox and SMTP delivery loop.
Both have been exercised in isolated local tests; the service has not yet been
deployed or accepted as a complete cross-device application.

Use Go 1.27.3, then from `apps/api` run:

```sh
go build ./...
go test ./...
go run ./cmd/api
go run ./cmd/worker
```

Set configuration through the environment or an appropriate secret provider. Never commit real credentials or print these values in diagnostics.

| Variable | Required | Meaning |
|---|---|---|
| `NERAE_ENVIRONMENT` | Both | `development`, `test`, or `production` |
| `NERAE_HTTP_ADDR` | API | Explicit host and port, unique to this worktree |
| `NERAE_MYSQL_DSN` | Both | MySQL driver DSN with user and database |
| `NERAE_REDIS_URL` | Both | `redis://` or `rediss://` connection URL |
| `NERAE_HEALTH_TIMEOUT` | No | Go duration between 1s and 30s; defaults to 2s |
| `NERAE_SHUTDOWN_TIMEOUT` | No | Go duration between 1s and 2m; defaults to 10s |
| `NERAE_API_ENABLE_BUSINESS` | API business mode | Must be `true`; production refuses health-only mode |
| `NERAE_API_SECRETS_FILE` | API business mode | Absolute path to protected API settings and keys |
| `NERAE_WORKER_SECRETS_FILE` | Worker | Absolute path to protected email worker settings and keys |

The protected files must be regular files owned by the process user, with no
group or other permissions; the API and worker must share the appropriate
identity and email-code keys. Business mode also requires a loopback API
listener, explicitly trusted proxy and proxy credential, and protected database
connections. See the [API assembly evidence](../../docs/evidence/A01/api-process/report.md)
and [worker startup evidence](../../docs/evidence/A01/worker-startup/report.md)
for the complete settings and verification boundaries.

The separate `cmd/migrate` executable verifies the release manifest and can
apply it to an explicitly configured database. Set `NERAE_MIGRATION_ROOT` to
the absolute release root containing `scripts/dev/migrations.json` and
`apps/api/migrations`. `nerae-migrate check` validates those files without a
database connection. For `nerae-migrate up`, also set `NERAE_MYSQL_DSN` and
`NERAE_MIGRATION_DATABASE`; the DSN must select that same database over a
Unix socket or verified TLS. Use a dedicated non-root account with direct
`SELECT, INSERT, UPDATE, CREATE, ALTER, DROP, INDEX, REFERENCES` privileges
only on that database, no roles and no grant option. The database
name must contain only letters and digits, starting with a letter, to avoid
MySQL database-grant wildcard semantics. MySQL 8.0.16 or later within the 8.0
series, or MySQL 8.4.x, is required.

An untracked database must be empty before its migration ledger is created.
An existing ledger must be a clean, exact prefix of the manifest. A failed or
uncertain migration remains dirty and requires inspection; the executor does
not automatically retry, repair or roll back DDL. A migration that changes the
selected database or autocommit also remains dirty, and its connection is
discarded. See the
[migration evidence](../../docs/evidence/A01/production-migration-candidate.md).

`GET /health/live` reports whether the HTTP process is alive without contacting dependencies. `GET /health/ready` pings the configured MySQL database and Redis; it returns 503 when either dependency is unavailable or the check times out. Responses do not contain dependency errors or configuration values. Restrict operational endpoints at deployment; they do not implement authentication.

Both processes respond to SIGINT/SIGTERM. The API drains HTTP requests up to the configured shutdown limit and closes its clients. The following older process-foundation smoke tests use reserved, closed loopback ports and do not need a running database:

```sh
# From the repository root, after building the API and worker binaries:
python3 docs/evidence/F01/F01-02/smoke.py --api /path/to/api --worker /path/to/worker
python3 docs/evidence/F01/F01-02/drain_smoke.py --api /path/to/api
```

The drain test uses a deliberately stalled loopback connection to exercise graceful shutdown and its deadline; it does not impersonate a healthy MySQL server. Local integration evidence is separate from actual deployment, remote dependency security, key management and device acceptance. No source code or test should rely on a developer's local credentials.
