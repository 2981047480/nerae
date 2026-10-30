# A01-24 delivery-to-login integration evidence

## Scope and result

On baseline `17998ecc4fbc8a7c991fad2ba9f0c702edc59281`, a test-only integration exercised the actual email challenge HTTP handler and issuer, outbox worker, SMTP mail adapter, email login coordinator/handler, and `/me` handler. The test starts no production route or application process.

The test created one synthetic challenge through `POST /auth/email/challenges`, replayed the same idempotency key and verified the exact response, and confirmed exactly one outbox row. The real worker submitted that queued challenge to a local implicit-TLS SMTP sink. The test parsed the received message and checked that its code matched the challenge-bound derivation, then used the received code in the actual login handler. A wrong code was rejected and its same-key retry persisted only one attempt. The correct code logged in; same-key replay returned the original token without increasing its remaining lifetime; `GET /me` returned the same user without the bearer token. Closed-database challenge and login handlers returned `503` with `Cache-Control: no-store`.

## Verification

Command:

```sh
python3 docs/evidence/A01/A01-24/mysql_redis_smtp_http_smoke.py
```

Exit code: `0`. The harness required at least 1 GiB free, created a unique mode-0700 directory under `/private/tmp`, ran MySQL with networking disabled and Redis on a private Unix socket with persistence disabled, applied the listed local migrations, and ran the focused Go race test. The test used only a loopback TLS SMTP sink and generated in-memory synthetic certificates/device keys. The harness shut down both services and removed only its own successful temporary directory.

Focused Go compilation check before the harness:

```sh
GOPROXY=off GOSUMDB=off GOTOOLCHAIN=local \
GOMODCACHE=/private/tmp/nerae-go-api-mod GOCACHE=/private/tmp/nerae-go-api-cache \
/private/tmp/nerae-go-toolchain-1.27.3/go/bin/go test -mod=readonly \
-run '^TestEmailChallengeSMTPLoginHTTPToMeOverLocalTLS$' ./internal/modules/auth/transport
```

Exit code: `0`. Without the harness environment this verifies compilation and skips the service-backed test; the harness run above is the actual integration evidence.

The harness source also passed an in-memory Python syntax compilation. `python3 -m py_compile` was not used because Python attempted to write bytecode under the user cache outside the authorized workspace; no dependency or cache installation was performed.

## Boundaries

- The HTTP handlers are instantiated directly in a test mux; production routing and server configuration are not exercised.
- SMTP is a local TLS test sink. No real mailbox, SMTP provider, public network, or QQ service is used.
- The device key, account, challenge, and email are synthetic; this is not a real-user login or mobile-device test.
- The sink accepting DATA proves local SMTP protocol acceptance, not external delivery.

## File hashes

SHA-256 values recorded after verification:

- `apps/api/internal/modules/auth/transport/email_delivery_login_e2e_test.go`: `80bfe50b6e422b43d0450bb8b861f618a93f260f0885b50efe93c3a5ce88d988`
- `docs/evidence/A01/A01-24/mysql_redis_smtp_http_smoke.py`: `08d943431c835f85f63bb6532e30417a52933910f188ea5bedcdeec08a00c9da`

## Lead integration recheck · 2026-10-31

After the versioned adult-declaration migration and session profile change were integrated, the isolated harness was updated to include `user/0002_age_declaration_version.up.sql`. The same command ran again with private MySQL/Redis and loopback TLS SMTP: exit `0`, including the focused Go race test. The current harness SHA-256 is `e0d35a240da01121ed60bab157f9bb0580931b75270d1c9c0e7cd96ce32ff9d2`. This remains test-only assembly with synthetic identities, not deployed routes or real delivery.

After the authenticated response began returning the declaration version, the same harness passed again and asserted version `1` in both login and `/me`; the updated integration test SHA-256 is `7d3e6d3f479f465a311062230b6dd704b6c157d3ef88c43dad57ddb3238d4a4b`.

## Versioned age declaration HTTP seam · 2026-10-31

Added an unmounted `POST /me/age-verify` handler to the same synthetic TLS integration. The test verifies an affirmative version-1 declaration returns the authenticated user's current version 2 with `Cache-Control: no-store`; repeating the stale version returns 412 without changing state; a withdrawal interleaved after declaration commit but before the handler's fresh profile lookup causes 412 instead of stale success; a new explicit declaration at the current version succeeds; `isAdult:false` is rejected without changing state; and a revoked session cannot mutate the declaration. It also sends a forged `X-User-ID` and verifies the returned user remains bound to the bearer session. The interleaving is a deterministic integration hook, not a stress/concurrency test.

Focused handler race test:

```sh
GOTOOLCHAIN=local GOPROXY=off GOSUMDB=off \
GOCACHE=/private/tmp/nerae-go-api-cache \
GOMODCACHE=/private/tmp/nerae-go-api-mod \
/private/tmp/nerae-go-toolchain-1.27.3/go/bin/go test -mod=readonly -race -count=1 \
-run '^TestAgeVerifyHandler' ./internal/modules/auth/transport
```

Exit code: `0`.

Private integration harness:

```sh
python3 docs/evidence/A01/A01-24/mysql_redis_smtp_http_smoke.py
```

The ordinary sandbox attempt failed before tests: MySQL 8.4.13 initialization exited 2 with SIGSEGV in `memory::Aligned_atomic` (`Invalid permissions for mapped object`). Its directory `/private/tmp/nerae-a01-24-yk58mnqc` was retained. The same harness then ran with normal local authorization against its own fresh socket-only MySQL/Redis and local TLS SMTP fixture; exit code: `0`. The test emitted its required PASS marker, ran under Go `-race`, shut down its services and removed only its successful temporary directory. It did not connect to a non-isolated database or external SMTP.

This is an unmounted handler exercised on a test mux, not a production route. Commit ambiguity is conservatively returned as 503 without retry; a client must fetch current state and explicitly retry with the new version. No mobile client, production server, real account, or real-age verification was exercised. The declaration represents self-attestation only.

SHA-256:

- `apps/api/internal/modules/auth/transport/age_verify.go`: `65131ae5321851a0cc0d7e0375a11ab818b93ec1529720f41996989644fe3737`
- `apps/api/internal/modules/auth/transport/age_verify_test.go`: `2c44ddce348a86f936367d17c5eb684900f70bae40abc0ee1eab40f94accb4ec`
- `apps/api/internal/modules/auth/transport/email_delivery_login_e2e_test.go`: `384a8b46055b5a5377950de49cce22e77fc6b9815ab89a0c45223b6efa2f75ec`
- `docs/evidence/A01/A01-24/mysql_redis_smtp_http_smoke.py`: `ac9da5e8395a147f4a5050483002fb5bbcf1f07b6f436f76ee439b02dd3c7eb6`

## Authentication-to-workout HTTP integration · 2026-10-31

At integration baseline `97813ba4164964f5494b5d3d248549d6997d1ce4`, the existing test and harness were extended to include the workout HTTP handlers. One `httptest` TLS process now assembles challenge, login, age-declaration, workout-read, and workout-write handlers over the same private MySQL and Redis instances. The local TLS SMTP sink receives challenge mail from the real worker; the test uses the delivered synthetic code to log in, declares age, then creates a free workout, replays the same create key, writes a set batch and replays that key, reads the saved set, finishes and replays the finish key. It then completes a second synthetic challenge/SMTP/login/age flow and confirms that second authenticated subject receives a generic 404 for the first subject's workout detail without record values.

The A01-24 harness now applies `transport/0001_idempotency.up.sql` and targets `TestEmailChallengeSMTPLoginAgeAndWorkoutHTTPOverLocalTLS`. It ran once with `require_escalated` local authorization against the harness's fresh socket-only MySQL and Redis plus loopback TLS SMTP; exit code `0`. The test emitted the required PASS marker and ran under Go `-race`. It did not contact public networks, real mailboxes, production routes, or existing databases. `git diff --check` passed. The earlier A01-24 sandbox SIGSEGV evidence remains historical and was not recreated during this run.

This test proves a synthetic same-process HTTP integration. The workout batch endpoint is shared by the two recording modes, but neither UI mode nor a real dual-device flow was exercised. Local synthetic accounts and SMTP do not establish user identity in production, deployment routing, or real mail delivery.

SHA-256:

- `apps/api/internal/modules/auth/transport/email_delivery_login_e2e_test.go`: `874566b34d9ab4c4d10a7671686da0dfd66f56bbd228a8abea6b22f2137d2132`
- `docs/evidence/A01/A01-24/mysql_redis_smtp_http_smoke.py`: `348f36179283c5135693c0f35bc609ec87c0b644a7c3c84422a124469bf366ea`
