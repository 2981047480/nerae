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
