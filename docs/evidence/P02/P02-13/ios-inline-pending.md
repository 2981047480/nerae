# iOS draft pending-field guard

The protected draft store treats draft.syncPending as an opaque string. Legacy
writes may keep it absent or retain the same UTF-8 value; a revision-bound
compare-and-set is required to introduce, change or remove it. Invalid or
ambiguous pending fields are retained and rejected, including during discovery.

On 2026-11-05 the synthetic Swift draft and journal harnesses passed, and the
bridge typechecked for arm64 iPhoneOS. Tests cover older data, deeply nested
unrelated fields, a 13,000-item unknown array, duplicate critical keys,
Unicode, invalid fields, stale writes, uncertain writes and competing writers.
An independent synthetic comparison found no behavior change across 53 legacy
JSON fixtures; it is not an exhaustive compatibility proof.

This protects one process's native storage operations. Real Keychain, process
restart, phone behavior, the network coordinator and trusted settlement have
not yet been verified.
