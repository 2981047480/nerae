# Android first-dispatch journal candidate

The Android native journal now has an internal v3 encrypted envelope holding
the journal payload, permanent request-key/body history and active request
bindings together. An internal first-dispatch operation validates the exact
ready-to-in-flight transition before writing this envelope and verifies the
stored result. Existing CAS paths preserve the history. New empty journals
have known-empty history; older v1/v2 journals are readable but their history
is unknown, so dispatch fails closed even after a normal write.

`bash docs/evidence/P02/P02-18/verify-android-dispatch.sh`, with the verified
Kotlin 1.9.24 and Corretto 17 paths supplied through `NERAE_KOTLINC` and
`NERAE_JAVA_HOME`, compiled against Android API 35 and passed 11 parser/gate
groups plus six synthetic tests using the production journal class. They
cover create, sets and finish request shapes, duplicate or changed data,
equivalent numeric encodings, replays, old-format migration, logical rewind,
concurrency, uncertain writes, full history/active capacity and corrupted
envelopes. The matching npm UTS compiler also copied the updated native sources.

The prior versioned-journal regression initially expected a migrated item to
use the old v2 header and failed; the expectation now reflects v3 while its
separate authenticated v2 fixture remains. The failed test directory was
retained at `/private/tmp/nerae-p02-14-kotlin.3dVF1dV5`.
Independent review then found that the JSON parser accepted signed or
non-ASCII digits in a Unicode escape. A regression failed before the fix;
escapes now require four ASCII hexadecimal digits, with valid escape and
surrogate-pair cases retained. The failing synthetic run was retained at
`/private/tmp/nerae-android-dispatch.GjedvrJw` and the updated suite passed.
The review also found that binary64 rounding could accept request numbers the
server rejects as non-integers. The candidate now validates bounded raw
decimal precision and exponents against the server's parsing rules before
claiming a send; existing binary64 body markers remain unchanged. An exact
fractional-version counterexample failed before the fix. The integrated API 35
JVM suite passed 14 parser groups and 7 journal scenarios, including unchanged
ciphertext on a rejected value. The first candidate compile failed on a local
variable name collision; a later clean compile and run passed. These are not
device tests.

After integration, the matching npm UTS compiler generated four Android
Kotlin files successfully using `P02-13/verify-android-uts.sh`. An initial
invocation pointed to a nonexistent copy under `P02-18` and exited 127; it
did not run the check. The corrected command passed, without building an APK
or exercising Keystore.

No UTS dispatch permit is exposed yet, and this is not server sync or a
settlement implementation. The Android `AtomicFile`, hardware Keystore,
actual process lifecycle and device execution were not exercised by the JVM
tests. Locks are process-local; external writers or privileged rollback of an
entire encrypted envelope are outside this guarantee. Unknown historical
state requires a future safe recovery path, not automatic reset.
The history is currently capped at 8,192 markers, normally two per distinct
request, and has no implemented safe pruning protocol. Reaching the cap blocks
further claims; durable retention and a user-safe recovery strategy remain
necessary before treating this as a production sync path.
