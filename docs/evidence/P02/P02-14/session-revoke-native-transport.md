# P02-14: Native HTTPS session revoke transport seam

## Scope

Adds an unmounted transport port for `DELETE /auth/sessions/current`. It sends a bodyless same-origin request with the supplied bearer token and `Cache-Control: no-store`; only an empty HTTP 204 with a `no-store` response header resolves successfully. Known 401/403/404/409/410/429 responses map to fixed rejection classes. Other statuses and network outcomes remain unavailable/uncertain. Existing GET and POST responses still require JSON objects; empty-body acceptance is opt-in for this revoke operation only.

No production route, UI, session state, or server deployment was changed.

## Verification

- Node transport tests: 23/23 passed, including origin/auth/body checks, explicit rejection vs uncertain responses, malformed/non-empty 204, strict GET/POST JSON, and late callback after cancellation.
- iOS Swift + local TLS harness (`NERAE_KEEP_P02_10_TLS=1 bash docs/evidence/P02/P02-10/verify-tls.sh`): passed. It checked bodyless authenticated DELETE, empty 204, nonzero Content-Length on 204, missing no-store, unauthorized/non-204 responses, cancellation, and one-shot callbacks.
- Android Kotlin + local TLS harness (`bash docs/evidence/P02/P02-10/verify-android-transport.sh`): passed. It checked strict HTTP framing, TLS/hostname validation, redirects, bounded bodies, timeout/cancel callbacks, and no CookieHandler use. This is JVM source-level verification, not Android device verification.
- Android UTS source generation: passed with HBuilderX 5.33.2026102401; generated Kotlin fallback source only, no APK or device check.
- `bash docs/evidence/P02/P02-10/verify-uts.sh`: did not produce a successful iOS UTS compilation (`UTS_RESULT={"msg":""}`, followed by `no result`). The retained diagnostics are in `/private/tmp/nerae-p02-10-uts.bGJcjYTh`.
- App 5.26 resource build was attempted after disk space recovered. The pinned CLI identified itself as `5.26（vue3）`, then failed because the staged dependency tree lacks `@dcloudio/uni-uts-v1`; no `app-service.js` was produced. No build success is claimed. The first attempt also found that the staged dependency tree needs a separately supplied pinned `core-js` path; that was supplied on the second attempt.
- The non-escalated TLS attempt could not bind loopback (`PermissionError`); the authorized local TLS runs succeeded. Failed retained scratch directories: `/private/tmp/nerae-p02-10-tls.KNIeXqu0`, `/private/tmp/nerae-p02-10-tls.fKJJ50y2`, and App build attempts `/private/tmp/nerae-p02-11-app.UsfP031p` and `/private/tmp/nerae-p02-11-app.rgJaCXBG`.

## Platform limit

Foundation `URLSession` suppresses unframed bytes following a 204 response: the raw TLS probe sent `bad` after the header terminator, but Swift received an empty 204 and reported success. The bridge rejects a nonzero `Content-Length`, any `Transfer-Encoding`, and any body that URLSession delivers, but cannot observe bytes URLSession discards. Android's raw HTTP parser does detect these bytes. This iOS limitation remains a security acceptance risk if “truly empty” requires detecting illegal unframed trailing bytes; the platform API would need a lower-level transport or an explicit server framing guarantee.

## Boundary

The port only returns transport outcome. It does not clear local session state, prove that the server has mounted the endpoint, or resolve a lost response after the server may have committed revocation.
