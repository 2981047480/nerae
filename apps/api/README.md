# API and worker process foundation

This directory contains a Go/Gin process foundation. It does not implement business endpoints, authentication, database migrations, queue handlers, or the recording flow. The worker validates configuration and waits for shutdown; it does not consume jobs until a later task installs real handlers.

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

`GET /health/live` reports whether the HTTP process is alive without contacting dependencies. `GET /health/ready` pings the configured MySQL database and Redis; it returns 503 when either dependency is unavailable or the check times out. Responses do not contain dependency errors or configuration values. Restrict operational endpoints at deployment; they do not implement authentication.

Both processes respond to SIGINT/SIGTERM. The API drains HTTP requests up to the configured shutdown limit and closes its clients. The worker currently has no job handlers and must not be treated as a queue consumer. The local process smoke test uses reserved, closed loopback ports and does not need a running database:

```sh
# From the repository root, after building the API and worker binaries:
python3 docs/evidence/F01/F01-02/smoke.py --api /path/to/api --worker /path/to/worker
python3 docs/evidence/F01/F01-02/drain_smoke.py --api /path/to/api
```

The drain test uses a deliberately stalled loopback connection to exercise graceful shutdown and its deadline; it does not impersonate a healthy MySQL server. Real healthy MySQL/Redis connectivity, isolated databases, migrations, worker jobs, dependency security review, and production settings are verified by later tasks. No source code or test should rely on a developer's local credentials.
