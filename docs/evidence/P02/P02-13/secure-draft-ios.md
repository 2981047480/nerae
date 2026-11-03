# iOS protected training draft candidate

The iOS source now has a separate Keychain item for each subject/workout draft,
addressed by a SHA-256 digest of the draft key. Creation uses `SecItemAdd`
without overwrite; update checks the previous envelope and reads back the new
bytes. Items use `WhenUnlockedThisDeviceOnly`. One complete UTF-8 envelope is
limited to 64 KiB. The shared adapter binds reads and creation to the current
authenticated subject and generation. Each prepared write captures one exact
snapshot while that lease is valid; `DraftController` orders those snapshots
per key in the current JS process. A queued write may finish after logout but
does not report a saved proof for an invalidated session.

The App editor factory now joins the existing two-mode editor to this adapter
and the platform random-ID function. Neither the editor factory nor its UI is
mounted in the application entry. Android and Weixin return
`unsupported_platform` for these new draft operations. They do not claim that
data was saved. Existing uni-storage drafts are not migrated, overwritten or
cleared by this change.

## Checks

- 57 focused Node tests passed for the editor, storage and App factories,
  including two-mode reopen, create collisions, Unicode/size bounds, uncertain
  writes, ordered queued writes and account changes. The App-boundary test
  substitutes an in-memory native port; it does not exercise Keychain.
- `vue-tsc --noEmit -p tsconfig.json` passed.
- `bash docs/evidence/P02/P02-13/verify-draft-ios.sh` passed synthetic backend
  checks, including 64 concurrent creates through two store instances, corrupt
  input preservation, size limits and an update with an unknown result.
- `bash docs/evidence/P02/P02-13/verify-ios.sh` passed the existing synthetic
  journal checks. The new Swift source compiled to an arm64 iPhoneOS object
  using the iPhoneOS 26.5 SDK. This does not link the UTS wrapper or an App.
- `python3 -B scripts/quality/verify_uts_plugin_build.py` passed the locked
  5.26 App and Weixin source/proxy probes. It is not native linking or device
  execution.

The first new factory test failed because its test used `entryKey` instead of
the model's `exerciseEntryKey`; the test was corrected and the focused suite
rerun successfully. An initial Swift compile exposed a throwing-expression
syntax error, corrected before the successful checks. An initial sandboxed
iPhoneOS compile could not write the default compiler cache; supplying a
private `/private/tmp` module cache succeeded.

## Limits and remaining work

The Keychain behavior, UTS native link, locked-device response, reinstall and
actual iPhone have not been tested. This process-local lock and JS queue do not
provide cross-process compare-and-swap, and the native bridge is not itself a
subject authorization boundary. A caller bypassing the editor/adapter can
bypass its ordering or session guards. No other process or extension may write
these items until a cross-process protocol is designed and verified.

A known event ID can be reopened, but the app cannot yet discover its own
drafts after losing the ID. Recovery discovery, a mounted record page, server
receipt settlement, synchronization, Android/Weixin secure draft support and
actual device acceptance remain open. Local storage alone is not cross-device
sync or an installable release.
