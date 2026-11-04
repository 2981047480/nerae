# iOS authentication generation candidate

On 2026-11-04, the App's authentication vault bridge and internal replay
transport were connected to one native generation gate. An authentication
write, conditional write or clear invalidates earlier snapshots before the
backend is called, including an operation whose outcome is unknown. Snapshot
capture and postflight checks use the same in-process lock and include a
process nonce, gate identity, generation and exact stored bytes. Results are
redacted when described.

`bash docs/evidence/P02/P02-17/verify-auth-generation.sh` passed synthetic
checks for A→B→A, separate gates, write and clear errors before and after an
applied mutation, concurrent reads/writes and fresh rereads. The private
loopback TLS test `bash docs/evidence/P02/P02-13/verify-recovery-http.sh` also
passed when an account changed and returned to its previous value during an
actual test request. These tests used synthetic credentials and storage, not
the real Keychain or an iPhone.

This guards only changes that pass through the gate in the same process.
External Keychain writers and a direct bypass of the gate are not protected
against A→B→A; the current JS session coordinator has a separate in-memory
generation which a future caller must recheck. A validated replay candidate
still does not authorize draft or journal settlement. The HTTP generation
check and callback delivery are not atomic: a gate mutation can occur between
them. The current read-only candidate carries no generation capability; any
future consumer must bind and recheck the native and JS session generations at
its serialized consumption/settlement boundary before acting on the result.
