# Local draft write ordering regression

Two races could expose an older draft after a newer edit. First, preparation of
the newer write could fail immediately while an older queued write was still
running; the older write then deleted or replaced the newer failed snapshot.
Second, a read already in progress could return an older value after a newer
write had succeeded or failed.

Failed in-memory snapshots now carry the process-wide write revision. An older
completion cannot clear or replace a newer failed snapshot; a later successful
write can clear it. Loading waits for queued writes and retries if a write
starts during the read. After eight unstable attempts it reports unavailable
instead of returning a stale draft. Read fences exist only while a read is in
flight and are scoped to the same subject and event; writes to unrelated
drafts do not trigger a retry. The separate process-wide revision still fences
the existing persistence proofs.

Four targeted regression cases were run before the fix: all failed, restoring
the old input rather than the latest one. They passed after the fix, covering
an older queued success/failure after a newer preparation failure and a
successful/failed write during a pending read. An independent review found that
the initial fix also retried on unrelated writes; a fifth regression reproduced
that extra read and now passes with per-draft fences. The complete training-related
Node suite plus the two native draft/editor factory suites passed 218 tests;
the client typecheck was also run separately. Tests use injected local storage.

Failed snapshots remain memory-only. A failed device write can still be lost
if the process terminates before a later successful save; callers must show a
storage error and not claim that input was durably saved. These tests do not
prove iOS Keychain behavior, cross-process ordering, server sync or a device
installation.
