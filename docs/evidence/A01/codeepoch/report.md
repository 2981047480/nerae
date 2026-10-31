# Email code verifier epoch primitive

## Scope

Base: `b69e9f3e818ce9838e061b4eeb91149e8819e7f8`.

Added a domain-separated HMAC epoch tag to `domain.EmailCodeHasher` and an explicit `auth/codeepoch` store. `New` requires a hasher and canonical nonempty ASCII key ID. `Initialize(ctx, tx)` locks an existing row and requires an exact key ID/tag match; if no epoch exists, it uses locking/current reads to require `auth_email_login_slot`, `auth_email_login_challenge`, and `auth_email_outbox` all have no rows before inserting and reading back the singleton. `Lock(ctx, tx)` only validates an existing row under `FOR SHARE`; it never initializes. Database and configuration failures return the fixed `ErrEpoch` classification. The epoch store contains no verifier key, email, or code.

The caller contract is explicit: the same transaction must already hold verified identity and code-derivation epoch locks, old writers must be stopped before initialization, and caller owns transaction commit/rollback. This package does not prove those preconditions and is not wired into API, challenge, outbox, or worker paths. Key entropy is not proven by the tag. No migration or shared migration list was changed; this base already contains `auth/0010_email_code_hash_key_epoch.up.sql`.

## Verification

- `GOTOOLCHAIN=local GOPROXY=off GOSUMDB=off GOCACHE=/private/tmp/nerae-go-api-cache GOMODCACHE=/private/tmp/nerae-go-api-mod go test -race ./internal/modules/auth/domain ./internal/modules/auth/codeepoch` — exit `0` with no private DSN; MySQL integration is skipped in that invocation.
- `go test -race -v -run '^TestMySQLEpochStore$' ./internal/modules/auth/codeepoch` without `NERAE_EMAIL_CODEEPOCH_TEST_DSN` — explicitly reported `SKIP: private socket-only email code epoch MySQL DSN is not configured`; this is not counted as DB verification.
- `python3 -B docs/evidence/A01/codeepoch/mysql_smoke.py` — first sandbox attempt exited `1` because MySQL initialization exited `2` with SIGSEGV in `memory::Aligned_atomic<long>::Aligned_atomic`; the retained failure directory is `/private/tmp/nerae-codeepoch-mysql-7bq6k2ny`.
- Re-ran the same harness with the authorized local environment and `--skip-networking`; exit `0`. It created a private temporary datadir and one socket-only schema, loaded migrations `auth/0002_email_challenge`, `user/0001_user`, `auth/0003_email_identity`, `auth/0007_email_outbox`, and `auth/0010_email_code_hash_key_epoch`, then ran `go test -race -count=1 -v ./internal/modules/auth/domain ./internal/modules/auth/codeepoch`. The actual MySQL tests passed for empty initialization, same-key restart, wrong key/ID, rollback, slot/challenge/outbox history, concurrent different keys, `FOR SHARE` blocking replacement, and canceled DB operations. Successful harness data was shut down and removed by the harness itself.
- `gofmt -d` and `git diff --check` — exit `0`.

No real account, production database, SMTP, Redis, server, or network connection was used. Remaining work is to wire `Lock` into every runtime hasher use and bootstrap `Initialize` in both API and worker startup under a shared, reviewed lock order. Existing databases with history and a missing hash epoch deliberately fail initialization; this primitive does not provide legacy-history recovery or key rotation.
