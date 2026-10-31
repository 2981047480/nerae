# F02-04 `ReplayExisting` implementation evidence

## Scope

Added an internal, read-only `Store.ReplayExisting` path for recovering a
previously committed idempotency receipt. It authenticates the current session,
derives the actor, checks the key epoch, performs a locking lookup for the exact
actor/method/normalized path/key, checks the request digest, and reauthorizes
the stored resource before decrypting and validating the cached response.
It never runs a mutation callback or creates, updates, or deletes a receipt.
Missing and expired receipts return the same `ErrReplayUnavailable`; a changed
request within the same scope returns `ErrConflict`. An unfinished row,
corruption, stale authorization, or an uncertain read-transaction commit
returns no cached body.

The ordinary `Execute` replay path now shares terminal metadata and response
decryption/validation helpers with `ReplayExisting`; its claim and mutation
semantics remain in `Execute`.

## Verification

Executed from the isolated worktree:

```sh
PYTHONDONTWRITEBYTECODE=1 /Users/zephyr/.local/bin/python3.11 docs/evidence/F02/F02-04/mysql_smoke.py
```

Exit code: `0`. The existing harness created a fresh private MySQL 8.4.13
datadir under `/private/tmp`, disabled TCP networking, applied its listed
migrations, and ran `go test -race -count=1 -v ./internal/idempotency` with the
test DSN pointing to that new Unix socket. All four `ReplayExisting` database
tests and the existing idempotency package integration test passed. The harness
shut down the server and removed only its own successful directory.

The harness migration list now includes `user/0002_age_declaration_version`
to match the current session schema. Test actor email values use a package-wide
atomic sequence so separate fixtures cannot accidentally authenticate as a
previous test's account. The two global purge tests clear the idempotency table
inside the harness-only synthetic database before arranging their own rows;
this keeps unrelated package test receipts from changing their purge assertions.

Coverage includes exact response/status/reference/expiry recovery, concurrent
read recovery, no mutation on replay, absent/expired/in-progress rows, changed
body/query/method, different actor/path, current resource revocation, ciphertext
tampering, and expiry while waiting on a MySQL row lock. Expiry and absence do
not disclose whether a receipt previously existed.

## Limits

This is an internal store primitive only. No workout HTTP route or client
recovery flow was changed or tested. Callers must derive the exact normalized
request from validated input and keep authorization callback permission rows
locked through commit as documented by `Authorize`. A failed transaction
commit remains `ErrOutcomeUnknown`; this method does not resolve uncertain
commits by re-executing work. Production retention, clock synchronization,
deployment key handling, route integration, and full end-to-end behavior remain
outside this evidence.
