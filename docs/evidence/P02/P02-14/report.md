# P02-14 · Android versioned training journal storage

## Scope and result

Implemented the Android Kotlin bridge and a JVM synthetic harness for a revisioned journal slot. No shared UTS/TypeScript, iOS, page, or production workflow was changed. The versioned API is currently native Kotlin only and is not exposed through the shared UTS module.

The v2 journal is stored in a separate `AtomicFile` from the legacy v1 journal file, with the same dedicated Android Keystore alias and the existing AES-GCM envelope family. Its authenticated header contains format version, signed 64-bit revision, found/tombstone state, and (when migrating) a SHA-256 fingerprint of the preserved legacy file. Plaintext remains the original JSON object and retains the 64 KiB UTF-8 limit; Android `AtomicFile` reads and writes accept the larger v2 envelope size so the exact plaintext limit remains reachable. The legacy file is read-only during migration and remains present. Every later read compares it to the authenticated fingerprint; an old-version rewrite, deletion, or appearance after a v2-only start fails closed.

A versioned read of a legacy record performs migration under the process lock and returns revision `1` only after the v2 write and read-back verification succeed. Failed or uncertain migration returns unavailable; the original v1 file remains unchanged. CAS checks canonical decimal revisions from `0` through `9223372036854775807`, refuses overflow, increments on equal-value writes and logical delete/recreate, and verifies committed v2 state before returning success. Deletion is represented as an encrypted tombstone, not file removal.

The old native read/CAS methods are routed through this same versioned backend, so successful old-CAS writes also advance v2 revision. Their JSON response remains exactly four fields (`ok`, `errorCode`, `found`, `value`). New methods are `readVersionedTrainingOperationJournal()` and `compareAndSetVersionedTrainingOperationJournal(...)`; versioned reads return decimal-string revisions, CAS success returns the new revision with empty data fields, and all failures return empty data and revision fields.

## Verification

Command:

```sh
bash docs/evidence/P02/P02-14/verify-android-versioned.sh
```

Exit code: `0`. The Kotlin 1.9.24/JVM synthetic harness exercises:

- absent revision `0`; A→B→A and same-value revision advancement;
- logical deletion and recreation; stale revision/value conflicts;
- one winner under concurrent same-revision CAS through separate journal instances sharing the bridge process lock;
- exact 64 KiB payload, oversized/non-object input, and non-canonical revision rejection;
- deterministic write failure and apply-then-throw unknown outcome;
- legacy v1 migration failure and apply-then-throw, checking original bytes stay intact;
- preserved v1 data, old API writes advancing v2, v1 replacement detection, and a v1 file appearing after v2-only initialization;
- authenticated header tampering and `Long.MAX_VALUE` overflow refusal;
- static response-shape and bounded-file guards: old methods remain four fields; new versioned results include revision and failures use empty revision; Android storage accepts the v2 envelope maximum on read/write.

The compiler emits only the existing Android `KeyInfo.isInsideSecureHardware` deprecation warning. The synthetic test uses software AES keys and an injected strict JSON validator; it does not exercise a device Android Keystore or real `AtomicFile` implementation.

## Limits and risks

- Synchronization uses one process-level lock. It does not establish multi-process/extension CAS or prevent whole-device/file-system rollback of the v2 file and its revision.
- Migration assumes this app process is the sole writer while the old file is migrated. A concurrent legacy writer is detected by the stored fingerprint/read-back check when it races before verification, but there is no OS-wide lock preventing a later cross-process write. Reopening then fails closed; it cannot reconcile the competing values automatically.
- An older installed binary that only reads the preserved v1 file can see stale data after v2 writes. If it writes v1, the next new-version read detects the change and fails closed. Safe rollback/reconciliation UX remains outside this slice.
- The shared UTS contract and client journal adapter still lack revision fields and are intentionally unchanged. The new methods are not yet a client integration or a cross-platform contract.
- The compatibility CAS keeps its original expected-value-only signature. It writes the revisioned v2 envelope and advances the stored revision, so a version-aware caller will observe the change; however, a legacy caller cannot detect an A→B→A ABA change because its request carries no revision. New coordinators must use the versioned CAS API; the old API remains only for compatibility during serialized integration.
- No APK, signing, install, device Keystore, cross-process, or device rollback test was run.
