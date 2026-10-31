# Auth entry component implementation evidence

## Scope

Baseline: `a7eced7eb57e8f41914cb203e0c6e787d4095c29`.

Added a uni-app Vue component and controller for the existing App authentication runtime. The component accepts `AppAuthRuntime` as a prop and emits only the current public `PublicSession` after server-confirmed authentication and an explicit 18+ self-declaration. It does not construct an alternate transport, persist credentials, or change app routes.

The controller restores on mount, separates email challenge and code submission, exposes exact-retry actions for uncertain challenge/login outcomes, and clears displayed email/code when the session guard is invalidated. Approval-required results remain pending and state that a logged-in device is needed; this component does not approve them or create a session. Age declaration is a separate explicit action and is described as self-attestation, not identity or document verification.

## Files

- `apps/client/src/features/auth/AuthEntry.vue`
- `apps/client/src/features/auth/authEntryController.ts`
- `apps/client/src/features/auth/authEntryController.test.mjs`
- This report.

## Verification

- Focused controller tests through the repository's P02-11 TypeScript loader: **6/6 passed**.
- Auth adapter/coordinator/controller tests: **48/48 passed** with
  `node --import ../../docs/evidence/P02/P02-11/node-ts-loader.mjs --test src/features/auth/authEntryController.test.mjs src/features/auth/emailLoginFlow.test.mjs src/features/auth/sessionCoordinator.test.mjs src/features/auth/ageAttestationApi.test.mjs src/api/auth/emailAuthApi.test.mjs`.
- Vue/TypeScript check: passed with `vue-tsc --noEmit -p tsconfig.json` from the existing integration worktree's dependency tree. The target worktree lock and client package hashes matched that tree. Local offline frozen install was attempted but failed before installation because pnpm could not open a temporary file at the worktree root (`EPERM`); no lockfile was changed.
- App resources: uni-app compiler **5.26 (Vue 3)** reported `DONE Build complete` for `uni build -p app` (exit 0).
- WeChat mini-program resources: the same compiler reported `DONE Build complete` for `uni build -p mp-weixin` (exit 0).
- `git diff --check`: passed.

The compiler commands use an existing dependency tree via a temporary ignored `apps/client/node_modules` link because this worktree's offline install was blocked. The link was removed after verification. Resource builds produced files under `apps/client/dist/build/app` and `apps/client/dist/build/mp-weixin`; these are build resources, not an APK, signed App, or installed mini-program.

## Limits

- The component is not imported by a production page or shared route, per scope. Therefore the resource builds do not establish that this component is rendered or linked into a shipping page. `vue-tsc` includes the component source, but no App/WeChat runtime screen was visually inspected.
- Tests use synthetic ports. They do not establish actual email delivery, live service behavior, device approval, native vault behavior, or true App/mini-program runtime behavior.
- The entry flow is ready for a parent page to mount with a configured runtime. The parent remains responsible for preserving the user's previous destination and handling the emitted public session.

## Follow-up review fixes

- Challenge `rate_limited` now keeps the controller in `retry_challenge`; login `rate_limited` stays in `retry_login`. Both controls retry through `EmailLoginFlow`'s existing retry port. A test drives the actual `EmailLoginFlow` with synthetic ports and verifies the second challenge/login call has exactly the same request body and idempotency key. The OTP cannot be edited during a login retry.
- `AuthEntryController.dispose()` removes the session watcher and listeners, clears controller-owned email/code state and its parent callback, and suppresses late UI notifications. If a request is unresolved, it does not reset `EmailLoginFlow`, so that flow retains the exact retry state. This is in-memory only; the flow has no request-abort API, so an already in-flight successful login may still install through its own session coordinator even though the disposed component will not emit it.
- The component now emits `cancel` for the parent to handle, displays the server-provided approval comparison code, and continues to state that device approval is not implemented here.

Follow-up verification:

- Focused controller test: 9/9 passed.
- Auth and auth API test set: 70/70 passed with `node --import ../../docs/evidence/P02/P02-11/node-ts-loader.mjs --test src/features/auth/*.test.mjs src/api/auth/*.test.mjs`.
- Vue/TypeScript check: passed by invoking the existing integration dependency tree's `vue-tsc` JS entry with `--noEmit -p apps/client/tsconfig.json`. Two temporary symlinks to that dependency tree were removed after the check. A `pnpm run typecheck` attempt did not reach typechecking because its workspace wrapper attempted install and aborted on `ERR_PNPM_ABORTED_REMOVE_MODULES_DIR_NO_TTY`.
- `git diff --check`: passed.

No App page imports this component, and no visual or device-runtime acceptance was performed in this follow-up.

## Scoped cancellation and retry follow-up

This follow-up depends on the shared cancellation API from `f9d075e0a4d178a1897cf8c106f0e118bd5769a2`, which is intentionally not duplicated in this UI commit.

The controller now carries the opaque per-attempt handle through request/retry and uses the coordinator's scoped `cancelAttempt` barrier. It emits the parent-handled `cancel` event only after that barrier returns a safe terminal result. An unavailable cleanup stays visible with a same-handle retry; disposal preserves only the opaque handle in an in-process `WeakMap` until cleanup resolves, so a remounted component can resume that barrier. Old controller disposal and async `finally` paths never call an unscoped login reset, which could otherwise clear another controller's active attempt.

Expired challenge/login outcomes now cancel and retire the stale attempt before allowing a fresh challenge. A rejected code keeps the still-active challenge available for corrected input. UI text avoids internal request identifiers; challenge acceptance is described as “请查看此邮箱”, since HTTP 202 does not prove delivery. Retry wording is bounded to continuing the current challenge and avoiding duplicate mail; login retry explicitly does not request another email.

Verification from this follow-up, in `apps/client`:

- `node --import ../../docs/evidence/P02/P02-11/node-ts-loader.mjs --test src/features/auth/authEntryController.test.mjs src/features/auth/emailLoginFlow.test.mjs src/features/auth/sessionCoordinator.test.mjs src/features/auth/ageAttestationApi.test.mjs src/api/auth/emailAuthApi.test.mjs`: **79/79 passed**. Coverage includes a remount during pending vault cleanup, old controller disposal while a newer flow is active, expired challenge/login requiring scoped retirement, corrected code after rejection, late HTTP response, cancellation during vault install, and cancellation cleanup retry.
- `vue-tsc --noEmit -p apps/client/tsconfig.json`, invoked through the existing offline dependency tree: passed.
- uni-app 5.26 (Vue 3): `uni build -p app` and `uni build -p mp-weixin`: both reported `DONE Build complete` (exit 0). These are resource builds only.
- `git diff --check`: passed.

The test/typecheck/build commands used temporary links to the pre-existing integration dependency tree; those links were removed after execution, and no package manifest or lockfile changed. Cancellation is an in-process guarantee only: process termination while secure-vault cleanup itself remains unavailable cannot be recovered by this component. The App has no wired parent page or free-browsing route in this task; no live email, native vault, device, page rendering, or production route was tested. New-device approval remains display-only and does not create a session.
