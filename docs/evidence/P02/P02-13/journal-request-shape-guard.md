# Training journal request shape guard

## Scope

Based on `8dca464a22c34306970ecac39251b312b009c30f`, `SecureOperationJournalStorage` now checks the exact allowed top-level fields and nested set-record fields for the three journaled write paths: free/planned `POST /me/sessions`, `POST /me/sessions/{id}/sets`, and `POST /me/sessions/{id}/finish`. The field lists follow `api/openapi.yaml` (`FreeSessionCreate`, `PlannedSessionCreate`, `SetBatchUpdate`, `SetRecordWrite`, and `WorkoutFinish`). Unknown auth, proof, credential, and arbitrary extension fields are rejected by the operation-specific allowlists rather than a blacklist.

The guard preserves the currently declared optional fields, including create `timeZone`/planned `occurrenceId`, set batch delete/restore arrays and set measurements, and finish date, duration, RPE, and note including nullable values. It enforces basic field types and bounded shapes, but intentionally does not replace the full OpenAPI validator, decimal-precision checks, or server/domain validation.

Existing same-subject rows whose request bodies match the current contract remain readable after reauthentication under a new generation. A legacy row containing an unknown field is reported as `corrupt`; its original native bytes remain untouched and no CAS write occurs. This avoids silently deleting or rewriting data that the new guard cannot safely interpret.

## Verification

Command:

```sh
node --test apps/client/src/features/training/secureOperationJournalStorage.test.mjs
```

Result: 16/16 passed. Coverage includes valid free/planned create, nested custom/library set writes and optional finish values; unknown top-level and nested fields on each path; old valid same-subject rows; preservation of legacy unknown-field bytes; malformed/duplicate-key registries; subject/generation changes; unknown write outcomes; and exact 65,536-byte acceptance followed by over-limit rejection using valid finish bodies.

`git diff --check` passed. A TypeScript compiler is not installed in this isolated checkout, so static typechecking was not run. No native UTS, iOS, Android, app runtime, queue, HTTP replay, or cross-device behavior was tested.

## Limits

This is a local storage field-shape guard only. Server authorization and the OpenAPI/runtime validator remain authoritative. The existing `TrainingOperationJournal` may map storage errors to its generic unavailable result; preserving finer error categories remains an integration concern.

## Follow-up guard coverage (18daa9d-based worktree)

The adapter now rejects parsed JSON strings and object keys containing lone UTF-16 surrogates, including `\\ud800`-style escapes in the operation envelope or the separately parsed request body. Its UTF-8 size counter also rejects a terminal high surrogate instead of counting it as four bytes. The 65,536-byte registry acceptance case now contains multibyte `é` characters and still lands on exactly 65,536 UTF-8 bytes; the next-byte write remains rejected.

The adapter mirrors statically checkable write rules from `api/openapi.yaml` and `RecordingApi`: required/forbidden metrics by set kind, completed actual groups, weight and RPE decimal-place bounds after canonical JSON parsing, unique updated record keys, unique set indexes within an updated entry, touched entries present in the supplied order, disjoint delete/update keys, restores represented by set payloads, and the 200 combined sets-plus-deletes batch limit. It cannot determine whether a restore key is actually deleted in this session, whether a record key was used historically, or whether unchanged server groups make the full target order valid. Those require the authenticated server snapshot/domain checks; this journal adapter does not make them replay-safe.

Verification in the isolated worktree:

```sh
node --test apps/client/src/features/training/secureOperationJournalStorage.test.mjs
git diff --check
```

Result: 19/19 Node tests passed; `git diff --check` passed. `pnpm --dir apps/client run typecheck` could not complete: dependencies were absent and pnpm began an install, but package registry requests returned `EPERM`; the command was interrupted before installation completed. Typecheck therefore remains unverified. No native vault, device, HTTP replay, account recovery, or server-snapshot behavior was tested.
