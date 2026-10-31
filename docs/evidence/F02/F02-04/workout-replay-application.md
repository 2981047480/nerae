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

Exit code: `0`. A dedicated socket-only harness is provided at
`workout-replay-application-smoke.py`. It initializes a fresh local MySQL
datadir, applies only the auth, user, workout, and idempotency migrations used
by the application fixture, and runs only
`TestWorkoutReplayOnlyApplication`. The harness was not run in this change
because the Lead had reserved the database window for A01-24; the MySQL
integration assertions therefore remain unverified here.

## Limits

This is an internal application seam and synthetic integration test. It does
not add or mount HTTP endpoints, does not bind validated transport DTOs to
canonical request bytes, and does not implement client-side journal recovery.
It does not resolve a commit acknowledgement lost after a successful commit;
it only reads a receipt already committed and still live. No production replay
retention, online route, or cross-device user flow is claimed.
