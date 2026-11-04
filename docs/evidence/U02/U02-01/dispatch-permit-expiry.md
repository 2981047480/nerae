# Dispatch permit expiry check

The local dispatch permit now rechecks the clock and session when the caller
checks it immediately before sending. Reaching the stored deadline, an invalid
clock, a backward clock observation, or an invalid session permanently makes
that permit unusable. The already-persisted `in_flight` operation is retained;
while the original session remains current, the caller may still mark it
`uncertain` for later reconciliation. Marking an operation uncertain now
invalidates its local permit before waiting for storage, closing a concurrent
check window even if the storage transition fails.

The two initial synthetic regression cases failed before this change because
the permit remained current after its deadline and with an invalid clock. The
focused tests now also cover a claim that completes after the deadline and a
concurrent check during the uncertain-state write. All four pass with the
repository TypeScript loader. The full training test suite passed 241/241
after the change. These use synthetic storage and clocks.

This check does not consume a permit or atomically start a transport; a future
sender still needs a one-use, session-bound send boundary and a trustworthy
server-bound receipt/settlement protocol. It cannot detect a device clock
rollback that occurs entirely between checks. No real sender or mobile sync is
enabled by this change.
