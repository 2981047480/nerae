# Android draft pending-field guard

The protected draft store treats the draft.syncPending field as an opaque string. An
unversioned compatibility write may leave the field absent or retain the same
UTF-8 value, but cannot introduce, change, or remove it. A revision-bound
compare-and-set may make those transitions. This also prevents a stale editor
from restoring a pending request after a newer version has cleared it.

The integrated Kotlin synthetic draft and dispatch suites passed on
2026-11-05. They cover edits, stale writes, Unicode, malformed or duplicate
pending fields, old-format migration, uncertain writes and competing CAS.
The candidate's separate depth comparison used old and new production store
paths with JVM JSON substitutes and found no compatibility regression across
324 depth fixtures. The 5.33 UTS-generated calls were also typechecked against
the native bridge with UTS helpers stubbed.

These checks do not establish Android Keystore, process restart, runtime
plugin linkage, APK or device behavior. The marker does not implement a
network coordinator or authorize settlement.
