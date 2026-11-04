# iOS replay response validator candidate

On 2026-11-04 the iOS secure-vault module gained an internal pure validator for
a historical replay response. It accepts the actual native journal intent and
checks the expected subject, exact UTF-8 identifiers, route and status, required
headers, bounded JSON without duplicate keys, session detail, and the relevant
create, set or finish fields. It returns a redacted candidate result, not proof
of origin, permission to change a draft, or permission to settle a journal.

`bash docs/evidence/P02/P02-16/verify-replay-validator.sh` compiled the actual
bridge and validator and passed synthetic positive and negative cases, including
long idempotency keys, CRLF, duplicate fields and headers, malformed responses,
wrong accounts and sessions, mismatched sets, booleans versus numbers, safe
integers and canonically equivalent but byte-distinct identifiers. The matching
5.33 npm UTS compiler also generated the Swift wrapper and copied the new file.
With the updated bridge, validator and inspector present, the four generated native plugin
libraries linked for iPhoneOS arm64 against the matching offline SDK. No server,
Keychain, device or user account was used by those validator tests.

Review found that the first candidate accepted additional fields inside a
returned set despite the `SetRecord` contract forbidding them. A regression
failed before the fix; returned and submitted set objects now reject fields
outside their respective contract lists. The failed run was retained at
`/private/tmp/nerae-replay-validator.TKOocdAj`.

The internal inspector now obtains an intent from the native journal, sends a
replay over the native HTTPS transport, validates the response, and rereads the
journal to compare its revision and exact intent. The loopback TLS test
`bash docs/evidence/P02/P02-13/verify-recovery-http.sh` passed with a matching
response, rejected another event and an incorrect subject, and discarded a
response when a journal write occurred in flight. This recheck is a point-in-time
guard, not an atomic settlement; the inspector cannot modify a draft or journal.

The inspector is not connected to journal settlement, draft merging or UI.
Future code must also recheck the app's current session generation and
coordinate the draft and journal writes under a crash-safe protocol. An
authenticated response alone is not authorization to discard a local edit or
to clear an in-flight operation. Real Keychain, device and server behavior
remain unverified.
