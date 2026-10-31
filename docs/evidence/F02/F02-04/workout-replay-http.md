# Workout receipt replay HTTP boundary

The integrated idempotency and workout application seams are exposed through three distinct POST replay-only routes for creation, set updates and finishing. Each requires the original request body and idempotency key; the handler binds the original method and canonical path before calling the authenticated replay-only store operation. The replay route has no mutation callback. It is a separate path so a proxy dropping an optional header cannot turn a replay request into a normal write.

A committed matching receipt is reauthorized and schema-validated before returning its original status and body with `Idempotency-Replayed: true`. Cached errors preserve their original request ID. A conflicting request has no receipt marker; missing or expired receipts return the same generic 503 without the marker. Neither case proves whether an earlier write committed. The source and receipt must remain available and current authorization must still hold. Replay does not extend retention or reconstruct a deleted receipt.

Verification on this isolated worktree, with synthetic accounts and data:

- `/private/tmp/nerae-oas-venv/bin/python api/tests/run_contract_tests.py --summary`: 102 tests passed. This is static contract validation.
- `scripts/codegen/generate.py write` and `check`, using the pinned Go/Node tools and local caches: exited 0 in the authorized local environment. Two independent generations matched and generated Go/TypeScript checks passed. The first sandboxed `write` attempt exited 1 during its test run; its output was truncated, so the exact failure was not retained. The successful rerun does not establish that sandboxed tests work.
- `PYTHONDONTWRITEBYTECODE=1 /Users/zephyr/.local/bin/python3.11 docs/evidence/A04/A04-09/mysql_smoke.py`: exited 0 in the authorized local environment. The isolated socket-only MySQL and local TLS server exercised creation, set and finish receipt queries, missing and expired receipts, different actor/body, cached 412 request IDs and adult-declaration withdrawal. Readback confirmed the completed session version remained unchanged. The same harness also ran the existing real client adapter over the local TLS server, but that adapter does not yet call the new replay-only routes.
- Independent read-only review found no actionable P0–P2 in the replay route and authorization binding. It did not run the tests itself.

These are server-side and synthetic HTTP results, not real QQ delivery, production deployment, client journal settlement, cross-device recovery or an installed iOS app. The 24-hour retention shown in the draft contract remains a configuration decision, not a guarantee of receipt availability.
