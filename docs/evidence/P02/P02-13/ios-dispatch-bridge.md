# iOS first-dispatch bridge

The existing native first-send claim is exposed through the iOS secure-vault
bridge and UTS, then injected into the App journal-storage factory. The bridge
returns only the native revision on success and returns redacted failures on a
conflict, invalid transition or uncertain write. Android and the Weixin/general
fallback explicitly return `unsupported_platform` for the same interface.

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
sync, an Android implementation or device acceptance.
