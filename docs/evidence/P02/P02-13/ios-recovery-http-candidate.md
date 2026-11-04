# iOS native replay HTTP candidate

`NeraeRecoveryHTTPTransport` reads the current protected authentication
state, requires the same subject as the native journal's recovery intent,
and sends the exact stored body and idempotency key only to that intent's
replay-only HTTPS route. Its service base comes from the signed App bundle's
`NeraeAPIBaseURL` setting; no JavaScript-selected URL is accepted by this
transport. It disables cookies and caches, rejects redirects, limits response
size and concurrency, and rechecks the exact authentication value before
returning the response. The response is deliberately not a settlement permit.

On 2026-11-04 `bash docs/evidence/P02/P02-13/verify-recovery-http.sh` exited
0. A private loopback TLS server and synthetic vault checked trusted and
untrusted certificates and hostname mismatch, the exact replay request,
account mismatch, account change in flight, redirect rejection, fixed-length
and chunked oversized responses, and one-shot
completion. The same script typechecked the source for iPhoneOS arm64.
Separately, the matching 5.33 npm UTS compiler generated the iOS wrapper and
copied the native Swift files unchanged. The UTS check is repeatable with
`NERAE_UTS_COMPILER_FILE` pointing to the matching npm
`@dcloudio/uni-uts-v1/dist/swift.js`, then running
`bash docs/evidence/P02/P02-13/verify-ios-uts-npm.sh`. The temporary TLS
authority is enabled only under the test compilation flag; the shipping
source uses system TLS trust.

This is an internal transport candidate, not a wired App flow or a verified
receipt. Response semantics, the journal's current revision and exact request,
and the current app session must be checked before any settlement. Comparing
authentication bytes before and after the request does not detect an
intermediate change that restores the same bytes. The App build must supply
the signed bundle setting and verify it matches the configured client API
base. No actual Keychain, iPhone TLS, production server, native framework
link, signed App, or cross-device sync was tested here.

The protected iOS draft store also exposes an internal conditional update for
future receipt merging. It compares the exact UTF-8 bytes under the same
process-wide draft lock used by ordinary writes, validates ownership, and
verifies the committed value by reading it back. A stale comparison returns
false; a write whose outcome is unknown throws and preserves the existing
recovery path. `verify-draft-ios.sh` passed synthetic stale, missing, racing,
Unicode-equivalent-but-byte-different and apply-then-error cases; the existing
journal tests and TLS test also passed after this change. This byte comparison
does not detect an ABA rewrite, coordinate writers in another process, or by
itself prove a server receipt or authorize journal settlement.

The same review found that draft ownership used Swift's canonical-equivalent
string comparison for event IDs, while the shared client requires the exact
event bytes. A new composed/decomposed Unicode regression failed before the
fix; ownership now compares UTF-8 bytes. The failed synthetic run was retained
at `/private/tmp/nerae-ios-draft-test.Z8H5PUcr`.
