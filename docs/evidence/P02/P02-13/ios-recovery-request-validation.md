# iOS pending request validation

The native first-send claim and recovery-intent paths now reject duplicate JSON
keys (including escaped aliases), non-ASCII field names, malformed JSON, and
request shapes outside the create/sets/finish journal contract. Create binds
the body event ID to the journal event by exact UTF-8 bytes. Set/finish
validation checks their allowed and required fields, value types and safe
integers. Unsigned `NSNumber` values are range-checked before conversion, so
large positive values cannot wrap into an accepted negative integer.

The scanner leaves the original request bytes unchanged. It limits the body to
64 KiB, nesting to 32, each array to 5,000 entries and total value nodes to
12,000 before Foundation decodes the entire document. The last bound is an
additional native fail-closed constraint compared with the TypeScript journal;
a synthetic batch containing 200 sets and 2,000 entry keys was accepted.
The native 64 KiB body limit is also stricter than the TypeScript journal's
512 KiB input limit. After the bounded scan, a fresh JavaScriptCore context
executes a fixed function with the body passed as data, and requires exact
UTF-8 equality with `JSON.stringify(JSON.parse(body))`, matching the existing
TypeScript normalization rule. Context creation or evaluation failure rejects
the claim. This is a shape and consistency check, not full server business
validation, authentication, evidence of sending or a receipt.

Verification in the task worktree:

- `bash docs/evidence/P02/P02-13/verify-ios.sh` and `verify-draft-ios.sh`:
  Swift synthetic tests passed, including duplicate/escaped keys, invalid
  types, Unicode values, recursion/node/array limits, boundary numbers and the
  larger batch. A reviewer also identified noncanonical JSON accepted by native
  but rejected by TypeScript; whitespace, `-0`, `1.0`, exponent and escaped
  key/slash counterexamples now fail. Missing rejection of invalid body shapes,
  integer overflow and noncanonical input was reproduced before the fixes;
  retained failed directories include
  `/private/tmp/nerae-p02-13-swift.dLnjuOED` and
  `/private/tmp/nerae-p02-13-swift.Y76ogPuC` and
  `/private/tmp/nerae-p02-13-swift.LMC0eaJQ`. Two subsequent failures were
  outdated synthetic fixtures with noncanonical whitespace or escaped slash;
  the fixtures were corrected to exercise valid JSON.stringify output.
- `node --import ./docs/evidence/P02/P02-11/node-ts-loader.mjs --test apps/client/src/features/training/*.test.mjs`:
  217 passed, 0 failed. Client `vue-tsc --noEmit -p tsconfig.json` passed.
  The initial `pnpm --dir apps/client typecheck` invocation stopped because
  pnpm wanted to purge a symlinked modules directory without a TTY; invoking
  the same installed typecheck executable directly succeeded without a purge.
- `python3 -B scripts/quality/verify_ios_uts_sdk_link.py --sdk-root /private/tmp/nerae-sdk533-uts-20261103/SDK`:
  App/Weixin import probes and the four separate iPhoneOS arm64 plugin links
  passed with compiler 5.33.
- An independent read-only review checked field parity, integer bounds,
  duplicate/Unicode handling and the JavaScriptCore fixed-function call. It
  found the noncanonical-input difference described above; a second review
  found no reproducible P0/P1 in that correction. Neither review ran tests.

All storage tests use synthetic in-process backends. The recovery intent has no
UTS entry, does not validate a live session, query the server, merge a draft or
clear a pending request. App linking, signing, actual Keychain behavior and
device execution have not been established by this check.
