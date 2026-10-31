# A01-25 current-session revocation evidence

## Scope

Added a transaction-owning `session.Store.RevokeCurrent` primitive and an unmounted `DELETE /auth/sessions/current` HTTP handler. The handler authenticates only the bearer, returns `204` with an empty body only after commit confirmation, maps invalid/revoked/expired credentials to `401`, and maps storage or commit uncertainty to a generic `503`. Every response is `Cache-Control: no-store`; response bodies and test diagnostics do not include bearer tokens or database errors.

The store reuses `Authenticate` and its identity → user → device → session lock order, rechecks the principal in the same transaction, checks expiry against the database UTC clock after lock acquisition, and updates only the authenticated `auth_session` row. A second request with the revoked bearer is `401`. A separate live session remains valid. The original login receipt cannot recover the revoked session. The current schema has no separate refresh-credential record to revoke.

## Verification

Go package race checks:

```sh
cd apps/api
GOTOOLCHAIN=local GOPROXY=off GOSUMDB=off \
GOMODCACHE=/private/tmp/nerae-go-api-mod GOCACHE=/private/tmp/nerae-go-api-cache \
/private/tmp/nerae-go-toolchain-1.27.3/go/bin/go test -mod=readonly -race -count=1 \
  ./internal/modules/auth/session ./internal/modules/auth/transport
```

Exit code: `0`.

Private integration:

```sh
python3 docs/evidence/A01/A01-25/mysql_session_revoke_http_smoke.py
```

Exit code: `0`. It created a unique mode-0700 `/private/tmp/nerae-a01-25-*` fixture, socket-only MySQL and Redis, applied the existing migration set, and ran the existing end-to-end test under Go `-race` with loopback TLS SMTP. The test exercised successful logout, an overlapping authenticated `/me` read (which may finish before logout or observe `401`), durable revocation, repeated DELETE, a still-valid unrelated session, rejected login-receipt recovery, and unavailable-database `503`. The harness removed only its own successful fixture. No production routes, real mail service, or external network were used.

The ordinary sandbox attempt exited `1` before tests because MySQL 8.4.13 initialization faulted with `SIGSEGV` in `memory::Aligned_atomic` (`Invalid permissions for mapped object`). Its failed private directory `/private/tmp/nerae-a01-25-odjletfn` was retained. The same harness subsequently ran with local authorization and exited `0`; its successful fixture was removed by the harness.

`git diff --check` passed.

## Limits and risks

- The handler is directly instantiated on a test mux; it is not mounted in production routing or deployment configuration.
- A database commit-ack loss was not deterministically injected. The implementation treats every `Commit` error as unavailable and never returns `204`, but the actual ambiguous-commit fault path is not experimentally simulated.
- The real integration exercises concurrent logout and a protected read, plus receipt replay after logout. It does not stress-test lock contention across many concurrent requests or simulate a login receipt racing logout at the exact commit boundary.
- The lock-order reasoning for receipt replay is that logout never acquires receipt locks: receipt recovery may wait for identity/session locks, then observes the committed revoked row. Exact-boundary receipt/logout interleaving was not forced in a test.
- Repeating DELETE with the now-invalid bearer returns `401` by design; clients must treat their local logout as complete after a confirmed `204`, and resolve a lost response by clearing local credentials and re-authenticating rather than assuming the server committed.
- Only the current server session is revoked. Device private keys, other sessions, login receipts, and training data are preserved.
