# iOS versioned training journal storage seam

## Scope

This isolated slice adds a native v2 Keychain record whose revision and journal
payload are encoded together in one binary property-list value. The native API
generates the revision; callers provide only the observed revision. Every
successful write, same-value rewrite, and tombstone transition increments the
signed 64-bit revision. `0` is reserved for an absent v1/v2 item; the maximum
accepted revision is `9223372036854775807`, after which writes fail closed.

The existing Swift `readTrainingOperationJournal()` and
`compareAndSetTrainingOperationJournal(...)` bridge methods remain available.
They now read/migrate/write the v2 item; current bridge code no longer writes
the v1 Keychain item. The compatibility CAS increments the v2 revision, but its
old signature has no caller revision argument. Until the shared UTS/TypeScript
adapter switches to `readVersionedTrainingOperationJournal()` and
`compareAndSetVersionedTrainingOperationJournal(...)`, old callers cannot use
the revision to reject their own stale A→B→A snapshot. This commit is a storage
primitive, not completed replay settlement.

## Migration and failure behavior

- If only v1 exists, read validates it, writes a revision-1 v2 copy, reads the
  v2 item back, and checks that v1 is unchanged before returning. v1 is retained
  and never deleted by this code.
- A malformed v2 item, invalid v1 value, changed/recreated v1 item after
  migration, failed verification, or uncertain write returns a fixed
  unavailable result; v2 is never silently replaced from v1.
- A logical delete is represented by a v2 tombstone (`found=false`, empty
  payload) and keeps the revision. Recreating the value must CAS against that
  tombstone and increments again.
- All v2 instances in this process share an `NSRecursiveLock`. This is an
  explicit single-writer-process assumption; it is not a cross-process Keychain
  CAS guarantee. Deployment must not run old and new writer processes at once.

## Verification

Command:

```sh
bash docs/evidence/P02/P02-13/verify-ios.sh
```

The macOS Swift synthetic harness verifies A→B→A revision changes, delete and
recreate, same-value rewrite, stale revision conflict, canonical revision
format, signed-64-bit overflow, corrupt v2 rejection, v1 preservation/migration,
legacy bridge writes routed through v2, detection of a v1 mutation after
migration, uncertain migration/write outcomes, two vault instances racing, and
the versioned wire result shape. It also reruns the existing synthetic vault
checks. This does not exercise the real Keychain, iOS process coordination,
device rollback, UTS linkage, or an iPhone.

The first run exited 133 because `PropertyListSerialization` rejects `CFNull`.
The envelope now encodes the absence of a legacy fingerprint as an empty string;
the corrected harness passed. The current independent checkout is based on
`c05a6f6c95c4baf8b18205358b9708cd86bdf3a3`.
