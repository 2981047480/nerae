# Android protected training draft candidate

## Scope

The Android secure-vault UTS adapter now routes protected draft read, discovery,
create-if-absent, and update calls to a native Kotlin candidate. Each record uses
the SHA-256 digest of its complete subject/event key for a dedicated filename
and Android Keystore alias. Discovery inspects only the dedicated no-backup
directory and the candidate alias prefix, decrypts entries, validates their
subject/event binding, and returns UTF-8 ordered pages of at most 100 records.
Orphan files/aliases, malformed entries, failed reads, and owner mismatches are
left in place and surfaced through `hasUnclassifiedItems`.

The store uses one process-shared lock. Creates are conditional, updates require
an existing valid record, and both verify the committed plaintext by reading it
back. An uncertain write returns `unavailable`; callers can read again to learn
which value is present. No draft count limit or automatic cleanup was added.
Keystore key creation failures return `unavailable` and do not remove existing
drafts. The existing Android key-generation policy is reused for each alias.
Discovery runs the same user-unlocked/device-unlocked preflight as the fixed
vault slots even when its file and alias inventories are empty. Before reading a
slot, the shared `AtomicFile` backend checks for `.new`/`.bak` recovery artifacts
and returns unavailable before calling `openRead`; these artifacts are retained.

## Synthetic verification

From the repository root, with the SHA-256-verified Corretto 17 and Kotlin
1.9.24 tools available:

```sh
NERAE_JAVA_HOME=/private/tmp/nerae-android-toolchain-20261104/amazon-corretto-17.jdk/Contents/Home \
NERAE_KOTLINC=/private/tmp/nerae-android-toolchain-20261104/kotlinc/bin/kotlinc \
bash docs/evidence/P02/P02-13/verify-draft-android.sh
```

Exit code: `0`. The JVM harness covers create/read/update, duplicate create,
same-process concurrent create, invalid subject/payload binding, orphan key and
file, damaged encrypted record preservation, pre-commit and apply-then-fail
write outcomes, key creation failure preserving a prior record, UTF-8 ordered
pagination, and subject filtering. Compilation emits the existing Android API
deprecation warning for `KeyInfo.isInsideSecureHardware`.

The harness injects software AES keys, memory files, and a payload validator.
It does not exercise the production Android Keystore, filesystem `AtomicFile`,
APK, emulator, or physical device. On the integrated source, the project's
matching 5.33 npm UTS compiler also generated the four Kotlin bridge calls
and copied both native Kotlin source files unchanged. This is UTS conversion,
not a complete HBuilderX link or dex/APK build. The check is repeatable with
`NERAE_UTS_COMPILER_FILE` pointing to the matching
`@dcloudio/uni-uts-v1/dist/kotlin.js`, then running
`bash docs/evidence/P02/P02-13/verify-android-uts.sh`.
The locked empty-inventory case is tested through the injected availability
gate; actual Android `UserManager`/`KeyguardManager` behavior and non-mutating
`AtomicFile` recovery handling remain unverified on a device/runtime.

## Limits

The candidate does not establish cross-process coordination, rollback
resistance, real-device hardware backing, or Android UI behavior. Per-draft
aliases consume Android Keystore resources; device capacity and enumeration /
decryption performance for large draft collections are not verified here. The
Android 17 behavior-change documentation describes per-app key-count limits
that vary with target SDK; key creation failure is handled as unavailable,
without deleting stored drafts or falling back to plaintext:
[Android 17 behavior changes](https://developer.android.com/about/versions/17/behavior-changes-all).

Android and Weixin runtime/device acceptance remains outstanding. This
candidate is local storage only and does not implement server synchronization.
