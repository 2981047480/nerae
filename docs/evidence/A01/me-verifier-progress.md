# `/me` credential verifier progress

## Scope and status

Added an injectable `createCurrentUserVerifier` for `AuthSessionCoordinator` and
a narrowly scoped native App GET port. This is a client-side seam only; the
server `/me` handler and a real App login/restore flow are not part of this
change. Status: **partial; no backend or device integration claimed**.

## Contract and transport behavior

- OpenAPI `GET /me` (`operationId: getMe`) returns `components/schemas/User` on
  200; that schema requires `id`, `nickname`, and `roles`. The verifier checks
  those fields, the optional `avatarUrl`, `ageVerified`, and `status` types, and
  role/status enums. It verifies `id` equals the stored subject and projects
  only known user fields. A disabled or unknown status cannot activate a saved
  credential.
- The App HTTPS factory exposes `readCurrentUser(token)`. It constructs only a
  GET to the explicitly configured base plus `/me`, sends `Authorization:
  Bearer ...` and `Cache-Control: no-store`, and returns validated status,
  response headers, and parsed JSON. The caller cannot supply a URL or origin.
- The verifier requires JSON content type and `Cache-Control: no-store` before
  classifying a response. A 401 maps to `revoked` only when its Error object has
  `code: UNAUTHENTICATED`, string `message`, a 32-character lowercase hex
  `requestId`, and a matching `X-Request-ID` response header; optional `details`
  must be an object. This follows `middleware.WriteError`'s response and
  `NewRequestID` generation. Empty, differently coded, or malformed 401s remain
  `unavailable`; 403, 429, server failures, redirects, malformed responses,
  subject mismatch, and transport errors also remain unavailable, preserving
  the saved credential.
- Contract follow-up: neither the inline `/me` 200 response nor its 401
  `Unauthorized` component currently declares `Cache-Control: no-store`; the
  client fails closed and returns `unavailable` if the service omits it. The
  contract or handler needs to guarantee this header for 200 and 401.
- Redirect refusal is supported by current bridge source: iOS completes the
  `URLSession` redirect callback with `nil` at
  `apps/client/src/uni_modules/nerae-https/utssdk/app-ios/NeraeHttpsBridge.swift:270-275`.
  Android uses its raw TLS socket/HTTP parser and returns the received status
  directly from `HttpsTransport.execute` at
  `apps/client/src/uni_modules/nerae-https/utssdk/app-android/HttpsTransport.kt:57-86`;
  it does not follow a redirect. This is source inspection, not a device test.

## Verification

- From `apps/client`, the combined synthetic suite command
  `node --import ../../docs/evidence/P02/P02-11/node-ts-loader.mjs --test src/api/auth/emailAuthApi.test.mjs src/features/auth/sessionCoordinator.test.mjs src/features/auth/meVerifier.test.mjs src/features/training/recording.test.mjs src/features/training/recordingApi.test.mjs src/features/training/draftStorage.test.mjs src/features/training/recordingProjection.test.mjs src/platform/app/nativeHttpsTransport.test.mjs`:
  **137 passed, exit 0**.
- Targeted strict TypeScript check for the coordinator, verifier, and native
  transport: **exit 0**, using the existing temporary `uni` global declarations
  because this worktree has no installed DCloud types.
- A separate strict TypeScript composition check for
  `createCurrentUserVerifier(ports.readCurrentUser)`: **exit 0**.
- Both TypeScript commands used the existing
  `design/prototype/node_modules/.bin/tsc --noEmit --strict --target ES2022
  --module ESNext --moduleResolution Bundler --skipLibCheck` binary and
  `/private/tmp/nerae-login-coordinator-uni-stub.d.ts`.
- Full client `vue-tsc --noEmit -p apps/client/tsconfig.json`: **exit 2** because
  `@dcloudio/types` is unavailable in this worktree. No dependencies were
  installed.

## Limits

All network responses in these client tests are synthetic; no backend request
was made. Production base URL, SecureVault binding, App runtime, iOS/Android
device behavior, and end-to-end session restore remain unverified.
The verifier does not infer expiry from `expiresIn`; only an explicit 401 can
invalidate a saved token through this check. Redirect refusal evidence is from
source inspection, not a live HTTPS redirect test on either platform.

On integration code `7c75dbd`, the 137 related client tests and the full
`apps/client` `vue-tsc --noEmit -p tsconfig.json` both exited 0. The repository
now also contains an unmounted `/me` handler and a contract requiring no-store
on 200 and 401; the client verifier has not yet been connected to that handler
through a native device, and the production HTTPS origin is not configured.
