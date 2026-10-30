# A04-05 transaction capability adapter evidence

- Worktree base: `fa32440234b5241c73e433745057795f5f9ba5a3`.
- Scope: keep `CompleteFree(*sql.Tx, ...)` as a compatibility wrapper; add `CompleteFreeTx(dbtx.Transaction, ...)`, reading the ordered snapshot through `ReadOrderedFreeTx`. The caller still owns authorization and commit/rollback. The fixed UPDATE is shared through `workoutCompleteSessionSQL`; `CompletionStatements()` returns a fresh list.
- No training start/end/duration is inferred or written.

## Verification

- `GOTOOLCHAIN=local GOPROXY=off GOMODCACHE=/private/tmp/nerae-go-api-mod GOCACHE=/private/tmp/nerae-go-api-cache /private/tmp/nerae-go-toolchain-1.27.3/go/bin/go test -race -count=1 ./internal/modules/workout/application` — exit 0.
- `python3 -B docs/evidence/A04/A04-05/mysql_smoke.py` — exit 0. The existing harness initialized a fresh private socket-only MySQL datadir, applied its migration set, ran `TestWorkoutCompletionTransactions`, shut down its own server, and removed its own temporary directory after success. Coverage includes nil rejection for both entry points, capability UPDATE statement list copy, successful DBTX completion/readback, and stale-version rejection with state unchanged, alongside existing completion rollback/authorization scenarios.
- `git diff --check` — exit 0.

These checks are local synthetic integration evidence. They do not establish public HTTP routing, production authorization wiring, real user age verification, client behavior, or native app/device acceptance. The repository capability method itself relies on its caller for authorization and transaction outcome.
