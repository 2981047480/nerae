# Client session coordinator progress

## Scope and status

Implemented a client-side seam that accepts only the branded `Authenticated`
result from `EmailAuthApi`, stores its bearer value through an injected secure
vault port, and exposes a generation-bound credential to `RecordingApi` only
while active. The coordinator does not instantiate a vault, use uni-app storage,
or provide a Web fallback. Status: **partial; implementation seam only**.

## Behavior checked

- Restore reads the injected vault and requires the injected server verifier to
  confirm both token and expected subject before making a credential available.
- Expired or revoked results are retired. A verifier outage preserves the
  secure-vault record for retry but returns no active credential.
- Login replacement, logout, restore, and authoritative expiry invalidate the
  in-memory generation synchronously. Serialized vault operations and revision
  checks prevent an older async completion from publishing over a newer action.
- A non-secret signed-out tombstone is written before clearing the stored
  credential. This prevents a later restore from reviving the previous account
  when clearing fails after the tombstone write succeeded.
- The bearer property needed by `RecordingApi` is non-enumerable; active
  credentials, the injected vault, and the verifier live in JavaScript private
  fields. Public snapshots and default JSON/inspection output omit them. Vault
  and verifier exceptions are replaced with fixed results.
- `expiresIn` is validated as a positive response field but is not converted to
  an absolute local deadline. The current API does not provide the request
  start instant, and the server's replay semantics are not yet implemented.
  Restore therefore relies on an authoritative verifier; active-session expiry
  must be signaled by a future auth integration and passed to
  `invalidateIfCurrent`.

## Verification

- `node --import ../../docs/evidence/P02/P02-11/node-ts-loader.mjs --test src/api/auth/emailAuthApi.test.mjs src/features/auth/sessionCoordinator.test.mjs src/features/training/recording.test.mjs src/features/training/recordingApi.test.mjs src/features/training/draftStorage.test.mjs src/features/training/recordingProjection.test.mjs` — **110 passed, exit 0**.
- `tsc --noEmit --strict --target ES2022 --module ESNext --moduleResolution Bundler --skipLibCheck /private/tmp/nerae-login-coordinator-uni-stub.d.ts apps/client/src/features/auth/sessionCoordinator.ts` — **exit 0**, targeted type check including imported client modules. The stub only supplies existing uni-app global callback types.
- Full client typecheck command via the existing `vue-tsc` binary — **exit 2** because this worktree has no `node_modules` and `@dcloudio/types` is unavailable. No dependencies were installed.

## Not established

This does not connect an actual platform vault or production verifier, revoke a
server session, compose the coordinator with a real login screen, or prove iOS,
Android, HTTP, database, or service behavior. If both tombstone write and vault
clear fail, the coordinator returns unavailable and exposes no in-memory
credential, but cannot guarantee persistence changed; the platform repository
must provide reliable secure-store semantics and callers must not treat this
failure as a completed logout. The planned server `expiresIn` meaning and
request-time propagation also remain to be finalized.
