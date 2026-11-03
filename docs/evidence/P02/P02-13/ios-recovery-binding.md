# iOS native pending-request binding candidate

The versioned iOS journal now stores a V4 native envelope. In the same
Keychain item as the registry and dispatch history, each active event is bound
to a domain-separated SHA-256 digest of its operation key, subject, path,
idempotency key and **exact UTF-8 request body**. Existing normalized-body and
key markers remain for the first-send guard. The native-only recovery snapshot
checks both the active binding and history before returning the retained
request. Its description is redacted.

For a create request, native code also checks that the event ID in the body is
the same UTF-8 byte sequence as the journal event. This avoids Swift's Unicode
canonical-equivalence comparison treating two different byte sequences as the
same ID. For set and finish requests, the server response still has to be
checked against the actual training event by the future replay coordinator.

An old V3 item with active events has no reconstructible exact binding. It
remains readable and retains its event and history markers, but new claims and
recovery intents fail closed. A V3 item without active events can use an empty
binding map while retaining all prior history; V2/older unknown history remains
unavailable for first send. Compatibility writes in this implementation write
V4 and retain the unknown state. These guarantees use the existing shared
**single-process** lock and revision checks; they are not cross-process
Keychain CAS or protection against an older binary writing the same item from
another process.

Synthetic Swift tests cover request/body whitespace and key-order changes,
path/key/subject/event changes, cross-event request splicing, restart with the
same test backend, revision changes after ABA, V3 active/idle migration,
competing same-process revisions, corrupted bindings, unknown write outcomes,
and the 64 KiB payload / 8192 history / 64 active limits. At capacity, new
claims fail without truncating stored markers. The original guard did not
check the create body's event: a reviewer identified this, a new regression
failed before the fix, and passed after it. Its failed test directory was
retained at `/private/tmp/nerae-p02-13-swift.G34KkiGT`.

The iPhoneOS Swift source and 5.33 UTS plugin linkage were compiled in local
probes. Tests use injected synthetic storage; they do not establish real
Keychain atomicity, process-restart recovery on a phone, or multi-process
behavior. A request digest is consistency evidence, **not** authentication or
proof that the request was sent or committed. The recovery intent has no UTS
entry, does not verify the current authenticated session, does not query the
server, does not clear an active marker and cannot settle or sync a draft.
Before exposing it, native request validation must also cover the full request
contract and duplicate JSON keys; the current identity check is not a complete
replacement for the TypeScript validator.
