# Email worker process startup assembly

## Status and scope

Implemented the `cmd/worker` startup path on integration base `c032c10`. The process now requires both the existing `NERAE_*` process configuration and an explicit protected secrets file named by `NERAE_WORKER_SECRETS_FILE`. It assembles the existing identity protector, code hasher/deriver, implicit-TLS SMTP mailer, outbox and `mailworker` loop. It does not start an HTTP listener or change API routes, shared configuration, migrations, or schemas.

The secrets file must be an absolute path to an owner-matched regular file without group/world permissions; symlinks, oversized files, duplicate/unknown JSON fields, malformed input, and missing values are rejected. Keys use canonical unpadded base64url and are checked for 32-byte length and reuse. `workerSecrets` formatting, JSON and structured logging are redacted. Runtime errors are returned as fixed classifications and `main` logs no underlying error text.

The worker validates a UTC, parsed MySQL DSN and requires `tls=true` for TCP connections; isolated Unix sockets remain accepted. It opens and pings MySQL before starting. In one startup transaction it calls `IdentityEpochInitializer.Initialize`, checks the code-key epoch, calls `Outbox.InitializeCodeKey`, and commits before constructing/running the worker. If a code-key epoch is absent while email slot, challenge, or outbox history exists, startup fails without rebinding a new code key. The identity epoch lock is held before this history check, matching the challenge issuance identity→code epoch lock order. Unknown commit outcome stops startup; the process does not retry the initialization transaction.

## Protected configuration fields

`NERAE_WORKER_SECRETS_FILE` points to a JSON file with these required fields:

- SMTP: `smtp_address`, `smtp_username`, `smtp_password`, `smtp_from`, `smtp_timeout_ms`.
- Identity protection: `email_identity_index_key`, `email_identity_encryption_key`, `email_identity_key_id`.
- Email code verification/delivery: `email_code_hash_key`, `email_code_derivation_key`, `email_code_key_id`.
- Loop bounds: `poll_interval_ms`, `recovery_batch`, `lease_ms`, `startup_timeout_ms`.

The process DSN continues to come from shared `NERAE_MYSQL_DSN`; the existing shared config loader also requires a syntactically valid `NERAE_REDIS_URL`, but this worker does not connect to Redis. `email_identity_key_id` plus the index/encryption keys feed the existing identity epoch tag. `email_code_key_id` plus the derivation key feed `auth_email_code_key_epoch`. The code hasher key currently has no persisted epoch in the schema; API and worker must therefore receive the same protected value, and its rotation requires a coordinated stopped deployment. It is not covered by the epoch checks in this change.

## Verification

- Focused config/DSN tests: `GOTOOLCHAIN=local GOPROXY=off GOSUMDB=off GOCACHE=/private/tmp/nerae-go-api-cache GOMODCACHE=/private/tmp/nerae-go-api-mod /private/tmp/nerae-go-toolchain-1.27.3/go/bin/go test -race -run 'TestLoadWorkerSecrets|TestWorkerSecrets|TestWorkerDSN|TestWorkerProcessStartup' ./cmd/worker` — exit `0`. In this invocation, the service-backed startup test skipped because no private DSN was set; the harness below runs it.
- Private integration: `python3 -B docs/evidence/A01/A01-19/mysql_smoke.py` — final run exit `0`. It initialized a new socket-only private MySQL datadir and four isolated schemas, then ran Go `-race` for `mailworker`, `issuance`, `mail`, and `cmd/worker`. The startup integration ran the actual `run` orchestration, bootstrapped an empty private schema, enqueued a synthetic challenge, submitted it through the local implicit-TLS SMTP sink with a test-process-only CA, observed the persisted `accepted` state, and cancelled the worker cleanly. It also rejected a mismatched identity epoch and refused to recreate a missing code epoch after challenge history existed. Successful harness data was shut down and removed by the harness itself.
- Existing issuance tests needing Redis reported their explicit skip markers because the harness is socket-only MySQL and this worker does not use Redis. This is not a Redis rate-limit verification.
- `gofmt -d apps/api/cmd/worker`, `git diff --check` — no output, exit `0`.
- The first authorized harness attempt exited `1` in the new integration assertion because the test cancelled immediately after the SMTP sink received DATA, before the Outbox accepted-state transaction became visible. The test was changed to wait for that persisted state before cancellation. Failed fixture retained at `/private/tmp/nerae-a01-19-mysql-gabu1xkz`; the final successful run used and removed a separate fresh fixture.

## Limits and production prerequisites

- No QQ/real SMTP account, real mailbox, external network, or delivery was used. The TLS sink proves local protocol and startup wiring only.
- The secrets file mechanism validates local ownership and mode; deployment must mount/create it securely and provide stable high-entropy independent keys. Neither this code nor file permissions provide a secrets manager, key rotation, backup/recovery, or fleet-wide provisioning.
- Existing legacy lazy-initializing writers must be stopped before deploying the epoch bootstrap path. API processes must be configured with the same identity and code keys/IDs. Missing schema, key mismatch, history without required epochs, or database failure causes startup to fail closed.
- Code-hasher epoch/version protection remains absent; identity/code epoch checks do not solve that key-rotation gap.
- The worker is assembled as a separate process only. The API production routes, trusted proxy boundary, public HTTPS deployment, QQ delivery, app installation, real login and new-device approval exchange remain outside this evidence.
