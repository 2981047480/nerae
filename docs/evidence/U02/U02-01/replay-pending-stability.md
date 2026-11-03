# Replay receipt and concurrent pending changes

`RecordingApi.replayPending` already validates a replay response against the
original request and current login. It previously returned `committed` even if
that pending operation was deleted or changed while the HTTP request was in
flight. It now rereads the pending entry after validating the response and
returns `unresolved` if the normalized operation, including its revision,
changed or cannot be reread. A changed login still returns `session_changed`.
The comparison is an in-process check over two journal-issued snapshots.

This is **not** authorization to settle the journal. It cannot prove that an
intermediate ABA did not restore the same operation bytes, cannot prevent a
change immediately after the second read, and does not atomically merge a
receipt into a draft. Native first-send/settlement, receipt merge, recovery UI
and device/server verification remain necessary before real synchronization.

The new regression initially failed when the entry was deleted during the
request. After the fix it passed for deletion, changed request/revision/state,
corruption, reread failure and session switch. The training-related Node and
App factory suites passed 220 tests and client typecheck passed.

The first local MySQL/TLS integration attempt failed: its older in-memory
fixture did not implement the later `claimFirstSend` test contract. That
fixture was updated with an explicitly synthetic in-memory claim, then
`python3 -B docs/evidence/A04/A04-09/mysql_smoke.py` passed under an authorized
local environment. The interop run exercised the real Go HTTP server and
TypeScript adapter with synthetic accounts, including a changed operation
during a valid TLS replay. The failed test directory
`/private/tmp/nerae-workout-integration-mysql-a7pswoo9` was retained; the
successful harness removed only the private directory it created. The test
does not validate a native Keychain claim, production deployment or iPhone.
