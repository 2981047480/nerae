# iOS native replay HTTP candidate

`NeraeRecoveryHTTPTransport` reads the current protected authentication
state, requires the same subject as the native journal's recovery intent,
and sends the exact stored body and idempotency key only to that intent's
replay-only HTTPS route. Its service base comes from the signed App bundle's
`NeraeAPIBaseURL` setting; no JavaScript-selected URL is accepted by this
transport. It disables cookies and caches, rejects redirects, limits response
size and concurrency, and rechecks the authentication generation and exact
value before returning the response. The response is deliberately not a
settlement permit.

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

The current integrated client source was also copied into an isolated build
fixture with matching 5.33 dependencies. `verify_ios_uts_sdk_link.py` generated
the App/Weixin import probes and linked all four iPhoneOS arm64 plugin
libraries against the user's 5.33 offline SDK, including the secure-vault
library containing this transport and draft comparison. It exited 0. These
libraries are unsigned and do not establish App linking or device execution.

On 2026-11-04 a separate isolated copy of the SDK sample was populated with
the Nerae App resources and the generated 5.33 UTS plugin sources from
integration commit `27236c9`.
The unsigned Debug iPhoneOS workspace build succeeded with Xcode 26.5 after
running outside the restricted environment, which could not recognize the
workspace. The resulting Mach-O executable is arm64 for iOS 15.0 or later;
the copied app-service.js matches the current resource build by SHA-256 and
the sample control.xml points at that app. The compiled bundle setting matches
the candidate client API base. The SDK sample also contains another example
app, and the origin and validity of the embedded sample AppKey have not been
established. This disposable build has no code signature, has not been
installed or run, and does not establish working login, synchronization,
server deployment, AppKey entitlement, or readiness for distribution.
The subsequent journal comparison fix, authentication generation gate,
response validator and inspector were also separately linked into the four
unsigned iPhoneOS plugin libraries. The isolated sample App was then rebuilt
successfully from integration commit `45c2ef2`; its embedded business
resources matched that build and the Xcode log listed the updated native
sources. It is still the same unsigned sample with the same limitations.

This is an internal transport candidate, not a wired App flow or a verified
receipt. Response semantics, the journal's current revision and exact request,
and the current app session must be checked before any settlement. The native
generation gate catches authentication A→B→A changes through that gate, but
not changes made by an uninstrumented external writer; the separate JS session
generation also remains to be checked. The App build must supply
the signed bundle setting and verify it matches the configured client API
base. No actual Keychain, iPhone TLS, production server, signed App, or
cross-device sync was tested here.

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

Subsequent journal review found the same distinction in versioned CAS,
first-dispatch checks, legacy CAS and persisted-value readback. The checks now
compare exact UTF-8 bytes, including untouched entries during a dispatch. A
new regression failed before the fix; after the fix `verify-ios.sh` passed
the stale-value, other-entry alteration and altered-readback cases as well as
the earlier journal tests. The failing synthetic run is retained at
`/private/tmp/nerae-p02-13-swift.LQbl2EUZ`. This is an in-process comparison;
it does not add cross-process synchronization or journal settlement.
