# `Idempotency-Replayed` transport preservation

## Scope

From integration baseline `4ddd0ca100f204b8e5325d758c27236947da4824`, the
iOS and Android native HTTPS response allowlists and the TypeScript App
transport now include `idempotency-replayed`. When present, both native
bridges require the parsed value to equal exactly `true`; duplicate allowlisted
headers visible to the bridge or invalid values fail with the fixed `invalid_response` outcome. The
TypeScript adapter independently rejects duplicate/case-colliding JSON keys
and any replay marker value other than the exact string `true`.
On iOS, URLSession may coalesce headers before `allHeaderFields` is available;
duplicates that the platform has already collapsed into one indistinguishable
value cannot be independently detected by this bridge.

The transport only preserves metadata. It does not change HTTP status, body,
or success/error interpretation based on this header. A synthetic 503 carrying
the marker remains 503 at the `RequestPort` boundary. Missing marker remains
missing and cannot establish a replay. No business adapter, recording API,
journal, OpenAPI, route, or page was changed.

## Verification

- `node --import ../../docs/evidence/P02/P02-11/node-ts-loader.mjs --test src/platform/app/nativeHttpsTransport.test.mjs` (from `apps/client`): exit 0, 25/25 passed. New cases prove exact `true` survives, 503 status is preserved, and false/case/whitespace/duplicate/case-colliding variants fail closed.
- `bash docs/evidence/P02/P02-10/verify-tls.sh`: exit 0 in the authorized local environment. The existing Swift synthetic HTTPS test now verifies marker forwarding on a 503 and rejects malformed and duplicate fields. It remains a macOS URLSession loopback test, not iOS device verification.
- `bash docs/evidence/P02/P02-10/verify-android-transport.sh`: exit 0 in the authorized local environment. Kotlin 1.9.24 compiled against Android API 35 and JVM loopback TLS verified marker forwarding/status preservation and malformed/duplicate rejection. This is not Android runtime/device verification.
- A TypeScript 5.9.3 focused `tsc` command was attempted; it exits 2 on the existing dependency declaration error `src/features/training/recordingApi.ts(820,65): Property 'header' does not exist on type '{ statusCode: number; data: unknown; }'`. That file is outside this task's write scope. The Node transport tests execute the changed TS module but do not replace this typecheck.
- `git diff --check`: exit 0.

Initial ordinary-sandbox Swift TLS execution was blocked binding `127.0.0.1`
and retained `/private/tmp/nerae-p02-10-tls.PxM70zYn`. The first Android test
attempt found a Unicode bytes-literal syntax error in the synthetic server and
retained `/private/tmp/nerae-p02-10-android-tls.KeZzsnPr`; the harness was
corrected and the next run passed. No database, external host, simulator, or
real device was used.

## Limits

This work only reaches the `RequestPort` response object. The existing
`RecordingApi`, persistence journal, and any higher-level acknowledgement
policy must still validate that the expected operation response is otherwise
valid; the replay header alone does not prove success. Real iOS/Android UTS
runtime delivery and device behavior remain unverified.
