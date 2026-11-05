# Android versioned training drafts

The Android protected draft slot now reads both existing encrypted drafts and
the new versioned format. A legacy item has revision zero; the first successful
write upgrades that same item. Ordinary writes advance the revision, and a
versioned compare-and-set requires both the observed revision and value.
The existing `android-legacy-v1` capability remains unchanged until a client
controller actually uses the versioned API.

On 2026-11-05, in an integration copy based on `f8eacdc`, the Android draft
synthetic harness passed legacy upgrade, 64 KiB payload, ABA, concurrent CAS,
unknown write, overflow, and corruption cases. The existing draft harness
passed. The matching 5.33 UTS compiler generated Android sources, and the new
generated calls typechecked against the native bridge with stub UTS helpers.

The mutex is process-local. An unconditional compatibility write can still
overwrite the value, even though it advances the revision. Real Keystore,
DCloud runtime, APK, device, receipt settlement, and cross-device sync have
not been verified.
