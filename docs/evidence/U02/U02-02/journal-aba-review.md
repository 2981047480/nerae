# Journal receipt settlement: ABA review

The journal currently uses compare-and-swap on stored bytes, with `revision` inside those same bytes. A separate, unmerged receipt-credential candidate re-read the bytes and compared their embedded revision. Independent review constructed a sequence in which a holder of the storage CAS interface changes `v1 → v2 → original v1`; the candidate then incorrectly considered the old snapshot current. The isolated regression exited 1 as expected. The candidate was **not integrated** and no journal entry was cleared.

Future settlement must provide an atomic mutation boundary that prevents a stale operation from being accepted after deletion/recreation or restoration of older bytes, and must define the scope of that guarantee across app processes, device reboot and storage rollback. A revision stored only in caller-replaceable value bytes is insufficient. Native iOS/Android storage support and migration need to be designed and tested before treating a verified receipt as authorization to clear a pending operation.

This finding does not invalidate the existing read-only replay query: it never clears or rewrites the pending request. The app still lacks automatic receipt settlement and crash-safe reconciliation of drafts.

## Fresh-revision rollback counterexample

补充（2026-11-03）：客户端发送入口已增加能力门禁；当前原生实现没有必需的逐操作标记，因此首次发送会失败关闭。旧基线的复现仍说明槽版本本身不够；它不会因为门禁而变成原生修复。详见 [保护接口与验证范围](dispatch-claim-guard.md)。

The versioned-slot adapter also cannot prevent a legacy value-only writer from
restoring the original full `ready` bytes after a first dispatch. A fresh
`readSnapshot` then binds those same `ready` bytes to the *current*, larger
native slot revision. The versioned CAS correctly accepts that fresh snapshot,
and a new `TrainingOperationJournal` using the available value-only methods
issues another permit. Thus native revision prevents reuse of an old snapshot,
but does not preserve per-operation history across a value rollback.

Reproducer: `docs/evidence/U02/U02-02/reproduce-fresh-revision-rollback.mjs`
(kept outside the normal test suite). Run with
`node --import ./docs/evidence/P02/P02-11/node-ts-loader.mjs docs/evidence/U02/U02-02/reproduce-fresh-revision-rollback.mjs`.
At this baseline it exits 1 with `EXPECTED FAILURE: fresh journal issued a
second permit...`; that nonzero exit is the expected evidence of the blocker,
not a passing security test. The script stages `R`, dispatches to `I`, restores
exact `R` with the legacy CAS (advancing native revision), obtains a fresh
native snapshot of `R`, confirms versioned CAS accepts it, restores `R` again,
then observes a new journal issue another permit. Future secure implementations
should make this script exit 0 by rejecting the reissue.

Minimum required capability is native-owned, per-operation monotonic
dispatch-attempt state (or an equivalent atomic tombstone) that survives
restoration of the mutable registry bytes and is checked/advanced atomically
when issuing a permit. It must not be writable/resettable through the legacy
value-only CAS. The current single-slot revision interface does not expose that
history, so this task must stop before wiring versioned snapshots into
`beginSend`; the existing dispatch can be replayed only after that native seam
is separately designed. No journal success receipt is settled or deleted.

## Server-side limits on an exact retry

Static review found additional business guards: free-session creation is unique
per subject and client event, while effective set updates and completion advance
the expected session version. These guards can prevent duplicate business
effects when the exact original event/body/version is submitted again; changing
an event or record key, or constructing a new finish request at a newer version,
is a new mutation and is not covered by that conclusion. A repeated completion
at a newer version can update its confirmation time.

Transport receipts can expire and their key can later be reused for execution;
the replay-only endpoint returns unavailable for a missing/expired receipt. A
fresh private MySQL test of original write → expired receipt → exact write
retry, including unchanged rows, version and completion time, remains needed.
This static review is not evidence that device recovery or cross-device sync
works, and does not remove the client-side stale-permit blocker above.
