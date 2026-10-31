# Email queue producer separation

The API-side producer can construct an outbox without SMTP credentials. It can initialize/check the code epoch and enqueue challenges, but worker-only claim, prepare, send and recovery operations fail before reaching the database. The existing SMTP-capable constructor remains the worker path.

Validation on the isolated candidate based on `93a20b7`:

- `go test -race ./internal/modules/auth/issuance -run '^TestEnqueueOutboxCannotOperateAsWorker$' -count=1`: exit 0 using the project Go 1.27.3 toolchain and the existing Go caches. Tests cover required dependencies and producer rejection of worker operations before database access.
- `python3 -B docs/evidence/A01/A01-24/mysql_redis_smtp_http_smoke.py`: exit 0 under local authorization. The test used a producer without a sender to issue the challenge and the distinct SMTP-capable outbox to deliver it, then exercised login, age declaration and workout writes/reads against fresh private MySQL/Redis and a local TLS SMTP sink. The harness removed only its own successful fixture.
- `git diff --check`: exit 0.

This is an internal module separation, not production process assembly or real mail delivery. It does not establish that independently configured API and worker instances use matching code-hash keys; that key has no database epoch yet. The application, real provider and devices remain unverified.
