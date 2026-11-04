# iOS protected draft revision candidate

The iOS protected draft store now writes its exact UTF-8 payload and a
monotonic revision into the same Keychain item. Existing unwrapped JSON drafts
are read without modification and upgraded only on a successful write. The
internal versioned read and compare-and-set check both revision and bytes;
the existing value-only methods still work and each successful mutation
advances the revision.

`bash docs/evidence/P02/P02-13/verify-draft-ios.sh` passed with a synthetic
backend. Cases include legacy reads and upgrade, owner isolation, A→B→A,
competing writers, the payload size limit, corrupt or unknown envelopes,
revision overflow, and writes that may have committed before returning an
error, including a create error and a failed readback. The existing journal,
authentication-generation and loopback HTTPS regressions also passed after
integration. The updated native sources were linked into unsigned iPhoneOS
arm64 plugin libraries with the matching 5.33 SDK.

The lock is process-local. A prior app version or external writer that
directly replaces the Keychain item could roll back the revision; downgrade
and cross-process fencing are not established. A failed write may have
committed, so callers must reread rather than assume failure. Real Keychain,
device lock state and iPhone execution have not been verified. The revision
APIs are exposed through the iOS bridge and UTS; Android and the generic
fallback explicitly report unsupported. The JS editor still uses its
existing value-only adapter, and no cloud receipt, draft merge or settlement
is wired.
