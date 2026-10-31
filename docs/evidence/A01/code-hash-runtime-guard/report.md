# Email verifier hash epoch runtime guard

## Result

Added a dedicated integration regression only; production code, migrations, and shared files are unchanged. The test builds the real challenge Store, issuance Outbox, Worker, private MySQL tables, and a process-local TLS SMTP sink. It verifies both a modified hash tag and a missing hash-epoch row.

For both invalid epoch states, direct challenge issuance fails without slot/challenge/outbox rows; a valid signed challenge attempt fails without changing `attempts` or inserting a proof replay marker; and `Worker.RunOnce` fails without opening an SMTP connection or changing the queued outbox row. The dedicated test commits the transactions after the expected Issue and Attempt errors before inspecting the database, so rollback cannot hide staged writes. This is a test of the implemented early epoch-failure paths; production callers should still follow their normal error rollback policy. Restoring the exact configured tag permits one local SMTP delivery and a valid attempt using the same previously rejected proof. The test sends before consuming the challenge because the outbox intentionally refuses to send a consumed challenge.

## Evidence

- `apps/api/internal/modules/auth/mailworker/runtime_hash_epoch_integration_test.go`: focused regression. Its test DSN is accepted only for the named schema, root with an empty password, UTC parsing, and a private socket under a `/private/tmp/nerae-a01-19-mysql-*` directory; it refuses symlinked directories/sockets.
- `docs/evidence/A01/code-hash-runtime-guard/mysql_smoke.py`: initializes its own socket-only MySQL datadir, loads migrations `auth/0001`, `auth/0002`, `user/0001`, `auth/0003`, `auth/0007`, and `auth/0010`, runs only the focused test under Go race, and removes only its own fixture after a successful clean shutdown. Failed fixtures are retained.
- No real mailbox, QQ SMTP, or user server is contacted. The test-only CA is scoped to the Go test process; TLS certificate and hostname verification remain enabled.

## Commands and outcomes

- `go test -race ./internal/modules/auth/mailworker -run '^TestRuntimeHashEpochTamperAndLossFailClosed$' -count=1` with the configured offline Go environment: exit `0` (without DSN the DB test is skipped).
- `python3 -B docs/evidence/A01/code-hash-runtime-guard/mysql_smoke.py`: final authorized private MySQL + local TLS SMTP run exit `0`; successful fixture removed.
- `git diff --check`: exit `0`.
- After independent review identified that rollback could hide staged writes, the follow-up private MySQL/TLS run repeated the complete test with both expected-error transactions explicitly committed before database assertions; exit `0`.
- An initial sandbox run exited `1` because MySQL initialization received SIGSEGV. The same authorized harness then ran outside the restricted sandbox. Two subsequent test iterations exposed, respectively, the actual pending-state value and the outbox rule that consumed challenges cannot be sent; assertions/order were corrected and the final isolated run passed. Failed private fixtures were retained at `/private/tmp/nerae-a01-19-mysql-m4iejvxh`, `/private/tmp/nerae-a01-19-mysql-cpxzi7cd`, and `/private/tmp/nerae-a01-19-mysql-slfb85cw`.

## Limits

This verifies runtime rejection at challenge issuance, challenge attempt, and the worker's send path on one isolated MySQL instance. It does not test concurrent epoch replacement/lock waits, multi-instance rollout, API startup behavior, real SMTP delivery, or deployment. `RecoverExpiredSending` recovery of already-expired `sending` rows is not the target of this regression; the tested queued message remains unchanged and unsent until the correct epoch is restored.
