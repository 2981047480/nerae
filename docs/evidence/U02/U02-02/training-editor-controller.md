# Local training editor controller

## Scope

This slice adds `TrainingEditorController` around the existing `recording.ts`
domain functions and authenticated `DraftController`. It supports local mode and
date changes, custom exercise/group creation, unsubmitted group edits/removal,
and local confirmation. IDs come only from an injected `SecureRecordingIdProvider`.
New drafts require `InitialDraftStorage.createIfAbsent` and write the initial
envelope atomically without replacing an existing key. Conflict returns no
controller. Unknown exceptions or adapter results return `outcome_unknown` and
the generated event ID only while the original subject/session generation is
still current; a session change returns no old ID. Adapter contract: `created`
means committed and readable after restart, while `exists` guarantees the old
bytes were unchanged. A process exit before an unknown event ID reaches the
caller can leave a draft the current API cannot discover; pending-draft recovery
is a page integration prerequisite.
`TrainingEditorController.attach` loads and validates an existing envelope
under the current authenticated session and returns fixed failure categories;
callers cannot attach an arbitrary, unloaded `DraftController` instance.

The controller is not connected to a page, HTTP API, operation journal, replay,
or server settlement. A confirmed group means the user confirmed the local
values; `localStatus` describes device persistence only. The host must provide a
platform secure random ID source and a current `AuthLease`. This implementation
does not verify that source's entropy or add cross-process guarantees.

The existing `createUniDraftStorage()` only provides ordinary `read` and
`write`; it does not implement atomic create-if-absent. It cannot safely use the
new draft creation factory yet. Real App creation therefore remains unconnected
until a native/platform atomic storage port exists.

Failed local storage writes retain the in-memory input and report
`storage_error`. Logout or account/session generation changes block subsequent
controller reads and writes. Persisted groups may be edited locally and become
dirty for the existing save workflow; deletion of persisted groups remains
blocked here and needs its server deletion workflow.

Create and attach acquire a process-local per-subject/event owner. A second
editor for that draft fails with `already_open`. `close()` blocks new operations
and resolves only after in-flight operations settle; repeated close is safe.
Close does not flush, retry, or prove persistence. Before navigating away or
exiting, the page must inspect each operation result and `localStatus`; failed
snapshots are retained only in the current process. A new
session generation revokes a stale owner only after its pending operation
settles, then may load and attach the saved draft. This registry does not
coordinate separate JS processes, duplicated module instances, app instances,
or devices. Page wiring must route every editor for a draft through the same
module instance.

The public low-level `DraftController` constructor and `persist()` can bypass
the editor owner; a focused test demonstrates that bypass. Production page
wiring must expose only `TrainingEditorController` and keep the low-level
controller private. The current `DraftStorage.write` has no cross-process CAS,
so cross-process editing is still unsupported. Newer writes within one editor
are reported as `superseded` or `pending`; no cross-device synchronization is
claimed.

## Verification

Run the focused and existing regression tests with the repository loader:

```sh
node --import ./docs/evidence/P02/P02-11/node-ts-loader.mjs --test ./apps/client/src/features/training/trainingEditorController.test.mjs ./apps/client/src/features/training/recording.test.mjs ./apps/client/src/features/training/draftStorage.test.mjs
```

Result: 71 tests passed, 0 failed. Type checking passed with:

```sh
/Users/zephyr/.codex/worktrees/nerae-foundation-integration/nerae/apps/client/node_modules/.bin/vue-tsc --noEmit -p apps/client/tsconfig.json
```

The existing `node_modules` directories were linked from the integration
checkout; no dependency installation or native build was run.

After integration, the full training test set plus the native HTTPS transport
tests passed together (221/221) and `vue-tsc --noEmit -p
apps/client/tsconfig.json` exited 0 on the same source tree. These are local
tests with synthetic adapters, not a device, platform-storage, or sync test.
