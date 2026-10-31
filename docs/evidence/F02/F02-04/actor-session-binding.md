# Authenticated session identity in idempotent transactions

The fixed idempotency policy callback can read the opaque session ID derived from the transaction-authenticated `session.Principal`. The caller cannot supply it in the request or construct a populated actor. Execute and replay recheck the same subject and session across the transaction; the actor remains redacted from formatting and serialization. A future authorization policy must still decide whether a receipt may be read from another currently valid session of the same subject.

On the integrated version, `PYTHONDONTWRITEBYTECODE=1 /Users/zephyr/.local/bin/python3.11 docs/evidence/F02/F02-04/mysql_smoke.py` exited 0 in the authorized local environment. Its private socket-only MySQL race suite checked two distinct sessions for one synthetic subject, correct session IDs in callbacks and rejection after revocation. Test setup, email addresses and data were synthetic. An initial ordinary-sandbox MySQL initialization on the independent worktree failed with SIGSEGV, and an early fixture incorrectly omitted `revoked_at`; these were not recorded as passing runs.

This is only a reusable identity capability. It does not implement an approval decision, new-device exchange, recovery code, real login, or deployment.
