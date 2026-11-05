# iOS first-dispatch bridge

The existing native first-send claim is exposed through the iOS secure-vault
bridge and UTS, then injected into the App journal-storage factory. The bridge
returns only the native revision on success and returns redacted failures on a
conflict, invalid transition or uncertain write. The Weixin/general fallback
explicitly returns `unsupported_platform` for the same interface; the later
Android bridge is documented in `../P02-18/android-first-dispatch-candidate.md`.

Verification on the candidate source:

- The Swift synthetic journal harness passed, including the new bridge success,
  stale claim and write-then-error cases. Its injectable storage does not test
  iPhone Keychain, device restart or cross-process behavior.
- 402 training, authentication, App adapter and entry Node tests passed; the
  new factory tests cover exact argument forwarding, unsupported platforms and
  a commit followed by an unknown result. These use synthetic native ports.
- Client TypeScript checking and the 5.33 App/Weixin import probe passed. The
  generated iOS sources were linked against the local 5.33 SDK as four separate
  unsigned iPhoneOS arm64 plugin libraries. This is not an installed App.

The user-facing record page still cannot submit a server save: a coordinator,
trusted replay receipt, crash-safe draft merge and journal settlement are not
implemented. Claiming alone retains an active marker and cannot finish a
workout's sequence of writes. No claim here establishes real login, network
sync or device acceptance; the later Android internal candidate is documented
separately. The native history limit is 8,192 markers, normally two per
distinct request, and no safe pruning protocol has yet been implemented.
Reaching it blocks further claims and must be addressed before production use.

On 2026-11-05, both ordinary and legacy iOS journal CAS were hardened so an
active first-send binding cannot be rolled back, deleted, or replaced by a
different request. The one-way in-flight to uncertain transition and changes
to unrelated entries remain possible. History or active bindings that cannot
be reconstructed are retained and rejected, rather than guessed; this can
require recovery for older journal formats.

The integrated Swift synthetic journal and draft harnesses passed, including
request mutation, Unicode byte identity and legacy-format cases. The bridge
also typechecked for an arm64 iPhoneOS target. These checks use synthetic
storage; they do not establish real Keychain persistence, process-restart
behavior, a UTS build, receipt settlement, or synchronization on a device.
