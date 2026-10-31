# Versioned journal adapter boundary

The App bridge now exposes a separate versioned journal API. A client snapshot
binds the exact native slot bytes and revision to the issuing adapter in the
current JavaScript runtime. A compare-and-set consumes the handle on success,
conflict, session change or an unknown native outcome. A write to any entry in
the shared slot invalidates every prior snapshot for that slot.

The `versionedJournalSnapshot.test.mjs` synthetic native port covers A→B→A,
same-value write, deletion/recreation, unrelated entry changes, forged and
reused handles, changed credentials, unknown outcomes and invalid revisions.
`vue-tsc` and the 5.26 App/Weixin import probe passed for the adapter source.
Those checks do not execute Keychain, Android Keystore, device storage or the
network and cannot establish native persistence or cross-device recovery.

The 5.26-generated secure-vault Swift wrapper was additionally type-checked
with `swiftc -typecheck -target arm64-apple-ios15.0` together with the reviewed
iOS bridge, Xcode 26.6's iPhoneOS 26.5 SDK and the downloaded DCloud 5.33
frameworks. Exit status was 0 with generated/vendor warnings; an individual
arm64 dynamic library for the same two sources also linked successfully.
This verifies only this bridge's compilation and isolated linkage across the
observed version combination; it does not prove full App compilation, linkage,
runtime compatibility, signing or install.

The legacy value-based API remains available for existing callers; it does not
itself prevent ABA. Snapshot compare-and-set is a storage primitive only: it
does not validate a server receipt, persist a draft, settle a pending operation
or claim that a workout is synchronized. The native revision is limited to a
single cooperating App process and cannot protect against whole-device storage
rollback. Actual use for settlement requires the receipt and draft boundaries
to be integrated and verified together.
