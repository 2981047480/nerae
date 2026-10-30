# P02-13 Training journal storage adapter

Date: 2026-10-31

## Scope

Adds `SecureOperationJournalStorage`, an adapter for the frozen `JournalStorage` interface from commit `2d405b4`, over the P02-13 fixed native training-journal read/CAS slot. Construction binds it to the authenticated `subjectId` and `generation` from a token-free `JournalAuth` port. Each key's subject must match that binding; every native read and CAS is guarded by fresh `current()`/`isCurrent()` checks before and after the operation. Same-subject reauthentication must create a new adapter; old rows remain available to that new adapter, while other subjects' rows are not exposed or removed. The registry is one canonical versioned JSON object containing at most 64 subject/event entries. Every read validates the registry and each entry's subject/event ownership. Logical deletes remove only the registry entry; the native slot is retained and no clear/overwrite API is added.

The adapter limits the complete UTF-8 registry value to 65,536 bytes and uses at most eight CAS attempts when the native slot reports an explicit conflict. An explicit conflict is retried after rereading; repeated conflicts fail as unavailable. Native bridge exceptions, malformed results, and non-conflict errors fail as unavailable. In particular, a native call that may have committed before throwing is never reported as success or as an ordinary CAS conflict; callers must reread to inspect state. If the session changes during a native CAS that committed, the call reports `session_changed`; the possibly committed row is retained for recovery under a new same-subject adapter.

`nativeTrainingJournalStorage.ts` binds the adapter to the existing UTS exports and requires a `JournalAuth` argument; there is no unbound factory. This is a storage seam only. It does not send requests, restore/replay operations, settle acknowledgements, or claim cross-device synchronization. The frozen `JournalStorage` interface has no session-generation field or typed error result; the adapter itself fails closed with `session_changed`, but the existing `TrainingOperationJournal` may map storage exceptions to its generic `unavailable` category.

## Verification

- `node --test apps/client/src/features/training/secureOperationJournalStorage.test.mjs`: 12/12 passed. Covers frozen storage methods, logical deletion, bound subject/event access, logout/account switch during read, invalidation during CAS, same-subject new-generation recovery, Unicode keys, malformed and duplicate-key registry data, unknown write outcome, explicit conflict retry, exact 65,536-byte acceptance and over-limit rejection, and the 64-entry limit.
- `git diff --check`: passed.
- `pnpm --dir apps/client typecheck`: not completed. pnpm attempted a dependency install and exited 255 with `EPERM` opening a temporary `_tmp_...` file at the worktree root; no dependencies or lockfiles were changed. There is no local `vue-tsc` or `tsc` executable in this checkout.

## Limits and risks

- P02-13 CAS is atomic only within one App process; it is not a cross-process or cross-device lock.
- The one JSON value is strictly bounded to 64 KiB. This adapter rejects overflow instead of splitting or truncating records.
- The registry stores encrypted-at-rest native vault content but does not itself encrypt fields; it relies on the P02-13 native slot implementation.
- Runtime App/UTS integration and production TrainingOperationJournal wiring remain unverified. No automatic replay, HTTP behavior, or response settlement is implemented here.
