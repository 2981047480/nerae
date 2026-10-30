# A04-08 isolated workout idempotency evidence

Status: targeted transaction test passed on isolated local MySQL. This is not HTTP, mobile, email-provider, production-database, or device acceptance.

## Coverage

`TestIdempotentWorkoutTransactions` signs in two synthetic email identities using in-process challenge codes and synthetic P-256 device keys, then makes explicit adult self-declarations in the isolated database. The test initializes workout and idempotency encryption epochs and uses the production `WorkoutExecutor` with real `Store.Execute` transactions and workout repository methods.

It verifies exact same-key creation replay with one mutation; same-key/different-body conflict; rollback for success responses with a missing resource reference, mismatched response ID, or extra private field; safe fixed-shape validation failure replay and rejection of an unscoped failure with extra private data; real ordered set save; a real stale-version 412 cached and replayed without a second mutation; finish and replay; a second subject retrying the exact successful finish path, body, and idempotency key is denied before its mutation and receives no cached body; cross-subject denial for an owned set resource; full rollback after a transient error following a business insert and successful same-key retry; overlapping same-key requests executing the mutation exactly once; and adult self-declaration withdrawal blocking replay without returning cached content.

The emails use the reserved `.test` domain, the challenge code and proof are generated in process, and no email is sent. Adult status is self-declared for test purposes; it is not an age or identity verification. The harness creates a private `/private/tmp` directory, disables TCP networking, uses only a Unix socket, applies the listed migrations including `transport/0001_idempotency.up.sql`, and runs only this test. Failed runs retain their directories; successful runs remove only the directory created by that run.

## Commands and results

- `python3 docs/evidence/A04/A04-08/mysql_smoke.py` — exit 0. Output: `TestIdempotentWorkoutTransactions` PASS; isolated socket-only MySQL test PASS; successful private directory removed.
- After adding the exact-key cross-subject replay case, `python3 docs/evidence/A04/A04-08/mysql_smoke.py` was rerun on a fresh private database — exit 0; the targeted race test passed and the successful run directory was removed.
- The harness ran `/private/tmp/nerae-go-toolchain-1.27.3/go/bin/go test -race -count=1 -v -run '^TestIdempotentWorkoutTransactions$' ./internal/modules/workout/application` with offline module/cache settings and `NERAE_WORKOUT_INTEGRATION_TEST_DSN` pointing only to its private socket database — exit 0.
- A separate `go test -mod=readonly ./internal/modules/workout/application -run '^TestIdempotentWorkoutTransactions$' -count=1` without an integration DSN — exit 0 after the existing fixture skipped the test. This confirms compilation only; the harness result above is the actual database run.
- The first ordinary-sandbox harness attempt — exit 1 during MySQL initialization with SIGSEGV in `Aligned_atomic` before migrations. Its failure directory was retained at `/private/tmp/nerae-workout-integration-mysql-zldppt5n`. A subsequent approved run passed; that earlier directory was not touched.

## Revision and hashes

The assigned baseline was `54b359cc54bc2ab4e3c220c580cc26227c282086`. The HEAD during the successful harness run was `4a5605121586ae83bbb191d5923fc0df11edd51c`. The worktree was not clean: `idempotent_workout.go` was an untracked Lead-owned implementation file, and this test plus the A04-08 evidence directory were untracked task outputs. No commit was created.

SHA-256 at report time:

| File | SHA-256 |
| --- | --- |
| `apps/api/internal/modules/workout/application/idempotent_workout_test.go` | `2f81fe54da7d4052993680d8a1bfa4fe2c78c86d4798ad9c19de8299d2391769` |
| `docs/evidence/A04/A04-08/mysql_smoke.py` | `00b47075e407d4e049a0c2712aaf22c229fe696dec1cc5751a100688c469521b` |
| `apps/api/internal/modules/workout/application/idempotent_workout.go` | `b44938815c6b4f5a04cf4f24b676474a0042c00967334c0fa52cdeb9079745dd` |
| `apps/api/internal/idempotency/store.go` | `e7fdd5b5fb098839455460f1ad8c2b5840810b1f3b1ed093fe273bdc0a9f6` |
| `apps/api/migrations/transport/0001_idempotency.up.sql` | `5d9d7bf7212434406bffe844c90e87ff473c4e7d8a8f8340833f0c1846950c64` |

The test uses real local MySQL transactions and synthetic authenticated sessions. It does not verify an HTTP handler, runtime request schema binding, deployed key management, provider email delivery, real identity/age, physical devices, or production isolation.
