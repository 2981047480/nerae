# App cloud training history candidate

The App auth runtime can create a `RecordingApi` bound to the current
authenticated subject and session generation. The training entry uses it to
list and read account-owned server records through the existing native HTTPS
transport. Loading, empty, retry, pagination and account-change states are
explicit. The history is read-only and does not update or replace local
drafts. Opening a training entry without a valid active session is rejected.

Verification on 2026-11-04:

- The combined client training, authentication, App adapter and entry suites
  passed 405 tests, including synthetic authenticated list/detail requests,
  failure and retry, and a response arriving after an account change.
- `vue-tsc --noEmit -p tsconfig.json` passed in `apps/client`.
- App and WeChat resources compiled successfully in a temporary copy using
  DCloud's 5.33 compiler and its matching dependencies. This is a resource
  build, not a signed iOS application or proof of a working WeChat login.

No deployed server, real mobile account or device was used for these client
checks. The page cannot submit a training record or settle a pending write;
local drafts remain unsynchronized. Actual mobile networking, visual layout,
pagination while records change remotely, and native device behavior remain
to be verified.
