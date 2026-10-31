# P02-14: Native HTTPS session revoke transport seam

## Scope

Adds an unmounted transport port for `DELETE /auth/sessions/current`. It sends a bodyless same-origin request with the supplied bearer token and `Cache-Control: no-store`; only an unframed empty HTTP 204 with a `no-store` response header returns `state: revoked`. An explicit unauthorized rejection additionally requires a schema-shaped `Error` with code `UNAUTHENTICATED`, a 32-character lowercase hex request ID matching `X-Request-ID`, and JSON content type. Other 401 responses, as well as unverified 403/404/409/410/429 responses, are `unknown`; they are not treated as proof of the session's state. Network, malformed response, non-204 success, 5xx, or missing no-store return `state: unknown`, because the server may have committed before the response failed. Existing GET and POST responses still require JSON objects; empty-body acceptance is opt-in for this revoke operation only.

No production route, UI, session state, or server deployment was changed.

## Verification

- `node --import ./docs/evidence/P02/P02-11/node-ts-loader.mjs --test apps/client/src/platform/app/nativeHttpsTransport.test.mjs` — exit 0, 23/23 passed. It includes valid structured 401 and malformed/mismatched error/header cases mapping to `unknown`, origin/auth/body checks, malformed/non-empty 204 at the adapter boundary, strict GET/POST JSON, and late callback after cancellation.
- `bash docs/evidence/P02/P02-10/verify-tls.sh` — exit 0 with local loopback authorization. It compiles the production Swift bridge and exercised bodyless authenticated DELETE, unframed empty 204, rejection of Content-Length 0/nonzero and Transfer-Encoding on 204, missing no-store, matching and mismatching structured 401 wire responses, cancellation, and one-shot callbacks.
- `bash docs/evidence/P02/P02-10/verify-android-transport.sh` — exit 0 with local loopback authorization. It compiled production Kotlin against Android API 35 and exercised rejection of Content-Length 0/nonzero and Transfer-Encoding on 204, unframed empty 204, prompt return with a keep-alive peer, matching/mismatching structured 401 wire responses, TLS/hostname validation, redirects, bounded bodies, timeout/cancel callbacks, and no CookieHandler use. This is JVM source-level verification, not Android device verification.
- `git diff --check` — exit 0. Client `vue-tsc` was not available in the isolated clone; no typecheck result is claimed.
- Failed attempts were retained: `/private/tmp/nerae-p02-10-tls.oQsWNv9J` (normal sandbox denied loopback bind), `/private/tmp/nerae-p02-10-tls.2qM7HXZM` (initial Swift edit compiled with an error before rerun), and `/private/tmp/nerae-p02-10-android-tls.yJpsgw9n` (initial Android edit had an unresolved `headers` reference, fixed before the successful run). They were not cleaned.
- Android UTS source generation: passed with HBuilderX 5.33.2026102401; generated Kotlin fallback source only, no APK or device check.
- `bash docs/evidence/P02/P02-10/verify-uts.sh`: did not produce a successful iOS UTS compilation (`UTS_RESULT={"msg":""}`, followed by `no result`). The retained diagnostics are in `/private/tmp/nerae-p02-10-uts.bGJcjYTh`.
- App 5.26 resource build was attempted after disk space recovered. The pinned CLI identified itself as `5.26（vue3）`, then failed because the staged dependency tree lacks `@dcloudio/uni-uts-v1`; no `app-service.js` was produced. No build success is claimed. The first attempt also found that the staged dependency tree needs a separately supplied pinned `core-js` path; that was supplied on the second attempt.
- The non-escalated TLS attempt could not bind loopback (`PermissionError`); the authorized local TLS runs succeeded. Failed retained scratch directories: `/private/tmp/nerae-p02-10-tls.KNIeXqu0`, `/private/tmp/nerae-p02-10-tls.fKJJ50y2`, and App build attempts `/private/tmp/nerae-p02-11-app.UsfP031p` and `/private/tmp/nerae-p02-11-app.rgJaCXBG`.

## Platform limit

Both native stacks reject any visible `Content-Length` field (including `0`) or `Transfer-Encoding` on 204. The local iOS TLS test observed the bridge reject the CL:0 response; `URLSession` exposes only its normalized `HTTPURLResponse` metadata to this implementation, so this test does not establish header visibility on every OS/runtime version. Foundation `URLSession` suppresses unframed bytes following a 204 response: the raw TLS probe sent `bad` after the header terminator, but Swift received an empty 204 and reported success. Android returns immediately after a valid 204 header block so that a keep-alive server cannot hold the callback open waiting for EOF; its harness likewise observes the unframed response as empty. Neither platform can verify illegal unframed trailing bytes at this abstraction. Android's keep-alive regression confirms it finishes promptly while the test peer holds the connection open. Detecting those bytes would require a lower-level protocol reader or an explicit server framing guarantee.

## Boundary

The port only returns transport outcome. It does not clear local session state, prove that the server has mounted the endpoint, or resolve a lost response after the server may have committed revocation.

## Server expiry classification follow-up

The session revoke implementation now classifies an expired principal recheck as `ErrInvalidSession` only when the already-authenticated principal's valid clock confirms expiry. If the conditional UPDATE affects zero rows, it performs a locking current read: missing, revoked, or explicitly expired is invalid; a live row, an unknown state, or a query error remains unavailable. This avoids reporting a definite auth failure for storage corruption.

- `GOTOOLCHAIN=local GOPROXY=off GOSUMDB=off GOMODCACHE=/private/tmp/nerae-go-api-mod GOCACHE=/private/tmp/nerae-go-api-cache /private/tmp/nerae-go-toolchain-1.27.3/go/bin/go test -race ./internal/modules/auth/session ./internal/modules/auth/transport` from `apps/api`: passed.
- `python3 -B docs/evidence/A01/A01-07/mysql_smoke.py`: passed with a fresh socket-only private MySQL; it loaded the harness's seven listed migrations and ran the session package with `-race`. The new real-DB assertions covered a controllable expiry clock, an expired DB row, cancelled DB recheck, normal current-session revocation, replay of revoke, and preservation of a second session. Unknown/corrupt state classification is covered by a pure unit test because the schema CHECK correctly prevents persisting that invalid state.
- The private DB suite exercises the zero-row classification helper directly against a locked expired row; it does not time expiry to fall strictly between the service's liveness SELECT and conditional UPDATE. That narrow race remains covered by the production recheck logic but was not injected end-to-end.

## Integrated regression, 2026-10-31

After integration, the iOS Swift and Android Kotlin loopback TLS scripts both exited 0, and the Node native HTTPS suite passed 23/23. The App and Weixin 5.26 import probe compiled with explicit references to the revocation port and training journal. The first integrated typecheck exposed a stale TypeScript declaration that omitted `DELETE`; the declaration and its UTS comment were corrected, and the full client typecheck then exited 0. The recording request surface remains GET/POST only. These are local synthetic checks, not native app installation or device validation.
