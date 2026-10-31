# F02-04 workout application replay-only seam

## Scope

`WorkoutExecutor` exposes replay-only methods for the fixed free-session create,
sets, and finish request shapes. The methods accept no mutation callback and
delegate only to `idempotency.Store.ReplayExisting`. They use the same actor,
age, and resource authorization functions as normal writes, and require the
same exact method, route, operation, and empty query constraints. They return
the stored receipt only after the storage layer checks the authenticated actor,
request digest, expiry, key epoch, resource permission, and response policy.
No route or client recovery behavior is connected by this change.

The focused integration test exercises create replay, sets receipt replay,
same-key/different-body conflict, route mismatch, cross-subject isolation,
age-declaration withdrawal, and expiry. Mutation counters and exact response
comparisons ensure replays do not re-run the original mutation callback. The
storage layer's existing tests cover missing, processing, expired, corrupt,
authorization-revoked, and lock-wait expiry cases.

## Verification

The following focused command passed without an integration DSN; in that run
the application integration test was skipped by `integrationDB`:

```sh
GOTOOLCHAIN=local GOPROXY=off GOSUMDB=off \
  GOMODCACHE=/private/tmp/nerae-go-api-mod \
  GOCACHE=/private/tmp/nerae-go-api-cache \
  /private/tmp/nerae-go-toolchain-1.27.3/go/bin/go test -race -count=1 ./internal/modules/workout/application
```

Exit code: `0`. The dedicated socket-only harness
`workout-replay-application-smoke.py` was then executed in the authorized local
environment and exited `0`. It initialized a fresh MySQL 8.4.13 local datadir
datadir, applies only the auth, user, workout, and idempotency migrations used
by the application fixture, and ran only
`TestWorkoutReplayOnlyApplication` with `-race`; the test passed. The harness
shut down the server and removed only its successful directory. The first
ordinary-sandbox attempt failed during MySQL initialization with a memory
mapping SIGSEGV and remains at `/private/tmp/nerae-workout-integration-mysql-2lfujpxj`.
The first authorized test attempt exposed the harness fixture's violation of
the migration's `expires_at > created_at` check and remains at
`/private/tmp/nerae-workout-integration-mysql-t55ug4s2`; the fixture now moves
both timestamps into the past while preserving their ordering.

## Limits

This is an internal application seam and synthetic integration test. It does
not add or mount HTTP endpoints, does not bind validated transport DTOs to
canonical request bytes, and does not implement client-side journal recovery.
It does not resolve a commit acknowledgement lost after a successful commit;
it only reads a receipt already committed and still live. No production replay
retention, online route, or cross-device user flow is claimed.
