# iOS receipt settlement: isolated candidate

An unintegrated Swift candidate is retained at
`/private/tmp/nerae-ios-receipt-journal`; its report contains the compile and
synthetic test command. It separates the active training event from each
request-bound receipt. The synthetic test exercises create, sets and finish
for one event, replay guards, and recovery from uncertain item writes.

Independent review found two data-loss cases in an earlier candidate: an old
finish receipt overwrote later edits, and an older receipt rolled back the
draft's server version. The revised candidate rejects both. A separate fixed
snapshot, `/private/tmp/nerae-ios-receipt-recheck.P40OLd96`, passed the full
synthetic test and independently reran both counterexamples. In those cases
the draft bytes, protected-item bytes and journal remained unchanged; the
receipt remained `verified` rather than being settled.

This is not wired into the app or a real service. In particular, the
candidate has a global limit of 128 retained receipts with no safe pruning
policy, and its separate draft and journal items are not one transaction.
Receipt origin, live session binding at settlement, conflict recovery,
cross-process behavior, actual Keychain, device and server tests remain
necessary before enabling sends. The earlier failing synthetic iterations
and reviewer probes are retained under the above temporary directories.
