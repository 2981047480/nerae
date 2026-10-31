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

## Decimal precision guard follow-up (8e93cd0-based fix)

The precision check now counts fractional digits from `JSON.stringify(number)`, including any exponent adjustment, instead of applying a floating-point epsilon. This is consistent with the adapter's existing requirement that the request body exactly match its canonical `JSON.stringify` form. Regression coverage accepts `42.5`, `12.55`, and `7.5`, rejects `42.50000000005` and `7.500000000005`, rejects a noncanonical `42.5e0` body spelling, and rejects canonical exponent-form numbers that fall outside the allowed weight range. This remains a client-side shape guard; the server's exact decimal validation remains authoritative.

Verification in the isolated worktree:

```sh
node --import /Users/zephyr/.codex/worktrees/nerae-device-proof/nerae/docs/evidence/P02/P02-11/node-ts-loader.mjs --test apps/client/src/features/training/secureOperationJournalStorage.test.mjs
git diff --check
```

Result after the precision fix: 20/20 Node tests passed; `git diff --check` passed. In the earlier guard worktree, `pnpm --dir apps/client run typecheck` could not complete: dependencies were absent and pnpm began an install, but package registry requests returned `EPERM`; the command was interrupted before installation completed. See the integration recheck below for typechecking. No native vault, device, HTTP replay, account recovery, or server-snapshot behavior was tested.

## 集成复测

集成代码 `ccdd3c8` 的 `secureOperationJournalStorage.test.mjs`：20/20 通过，客户端类型检查退出 0。锁定版 uni-app 5.26 的 App/小程序源码探针引用 `createNativeTrainingJournalStorage` 后编译通过，探针保留于 `/var/folders/6k/klwbxv35003_wd7b9kwtnt6c0000gn/T/nerae-uts-build-28wtc7tw`。该适配器检查受信客户端的主体与会话代际，并不是原生 vault 对任意 JS 调用的账号访问控制；它仍不发送或结算训练操作，亦未经过设备验证。
