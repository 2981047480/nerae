# P02-10 staged verification report

Status: **partial**. The iOS transport source, isolated TLS checks, generated Swift typecheck, and unsigned HBuilderX iPhoneOS framework are verified. This is not an IPA, installed app, or device verification.

## Changes covered

- The UTS failure factory now parses its object through `JSON.parseObject<HttpsFailure>` so the generated Swift returns the declared mapped UTS type.
- Native success and failure results now always contain `ok`, `requestId`, `status`, `headersJson`, `body`, and `code`. Failure values use status `0`, empty headers/body, and a fixed code; success uses an empty code. This matches the generated `NativeResult` Codable decoder, which requires all six fields.
- The UTS boundary now carries request/response headers as `headersJson: string`, avoiding DCloud UTS `Record` in generated Swift. The iOS bridge parses request header JSON into a dictionary, applies its existing allowlist and value checks, and serializes response headers back to JSON text. Callers must parse `headersJson` as a JSON object and validate its schema before use.
- UTS `JSON.stringify` results are checked for null before they are passed to APIs requiring `String`; generated Swift uses a safe coalesced value only after the explicit null branch.
- Path and query validation now admits only RFC 3986 characters allowed for those components and complete `%HH` escapes before assigning `URLComponents.percentEncodedPath` or `percentEncodedQuery`. Raw space, raw Unicode, malformed escapes, and other excluded characters are rejected. Valid UTF-8 percent escapes are accepted. Apple documents that invalid percent-encoded setter values raise an exception: [percentEncodedPath](https://developer.apple.com/documentation/foundation/nsurlcomponents/percentencodedpath), [percentEncodedQuery](https://developer.apple.com/documentation/foundation/nsurlcomponents/percentencodedquery).
- The loopback probe decodes both success and failure results into a required-field Codable type and checks the wire shape.
- `verify-uts.sh` now passes xcodebuild arguments through unchanged and captures its output to `xcodebuild.log` inside that run's temporary directory. It no longer appends `-derivedDataPath` unconditionally; failure preserves the run directory and success cleans only that run's directory.

## Commands and results

- `git diff --check` — exit 0.
- `bash docs/evidence/P02/P02-10/verify-tls.sh` — exit 0 after the RFC 3986 validation changes and unified required fields; after switching the header wire key to `headersJson`, one run failed because the synthetic request still used old key `headers` (retained at `/private/tmp/nerae-p02-10-tls.Q2lG8QOi`), then the corrected run exited 0. Successful runs removed only their own scratch directories.
- The TLS harness compiles the current Swift source with the explicit test-only CA hook and verifies: trusted local TLS succeeds; untrusted CA and wrong hostname fail; redirects are not followed (including cross-origin and HTTPS-to-HTTP); body limits apply to fixed and chunked responses; timeout/cancel callbacks settle once; cookies are not retained; cache directives remain `no-store`; invalid path/query encodings are rejected and valid encoded UTF-8 is accepted; success and failure JSON decode with the same required field set.
- HBuilderX `runSwiftProd` generated Swift from the changed UTS source; generated `index.swift` has no `Record` references and both JSON stringify results are checked. The exact iPhoneOS typecheck command was `xcrun --sdk iphoneos swiftc -typecheck -target arm64-apple-ios13.0 -sdk "$(xcrun --sdk iphoneos --show-sdk-path)" -F /private/tmp/nerae-ios-https-typecheck.W48hblA8/Payload/HBuilder.app/Frameworks -module-cache-path /private/tmp/nerae-ios-https-typecheck.W48hblA8/module-cache /private/tmp/nerae-p02-10-typecheck.1eAGFmCE/output/uni_modules/nerae-https/utssdk/app-ios/src/index.swift /private/tmp/nerae-p02-10-typecheck.1eAGFmCE/output/uni_modules/nerae-https/utssdk/app-ios/src/NeraeHttpsBridge.swift` — exit 0. Reused the existing DCloud framework and module cache; generated source and typecheck log are under `/private/tmp/nerae-p02-10-typecheck.1eAGFmCE`.
- After the generated Swift typecheck passed, a targeted `bash docs/evidence/P02/P02-10/verify-uts.sh` with the old wrapper still failed with exit 1 / HBuilderX `-10060`; that failed scene is retained at `/private/tmp/nerae-p02-10-uts.Ji6qfdY3`.
- After changing the wrapper to pass xcodebuild arguments through and capture its output, `bash docs/evidence/P02/P02-10/verify-uts.sh` — exit 0. HBuilderX `5.33.2026102401`, Node `v26.0.0`, iPhoneOS SDK `26.5`; emitted an unsigned `unimoduleNeraeHttps` Mach-O 64-bit arm64 framework. `vtool -show-build` reported `platform IOS`, `minos 15.0`, `sdk 26.5`; SHA-256: `e24404b1618e042b2f717f3809081710a15f0e42ae47dde3770086669d66f8d2`. The successful run's temp directory `/private/tmp/nerae-p02-10-uts.K5o3Tn83` was removed by the script's success-only cleanup.
- The wrapper change is an observed difference correlated with success, not a proven root cause for earlier `-10060` failures. The earlier wrapper always appended another `-derivedDataPath`; this run passed through xcodebuild's supplied arguments unchanged. No diagnostic failure remains to establish whether that extra option alone caused the old errors.
- An earlier standalone production Swift compilation also emitted an unsigned iPhoneOS arm64 dylib (exit 0), before the final UTS wire/path edits.

## Retained failed scenes

Failed TLS scratch directories are preserved because the harness only removes its own directory after success:

- `/private/tmp/nerae-p02-10-tls.YkalEwH4` — probe compile failed on an access-control issue in test fixture.
- `/private/tmp/nerae-p02-10-tls.6IDHPDrX` — ordinary sandbox denied loopback bind.
- `/private/tmp/nerae-p02-10-tls.zoHEhJ3q` — early trust/callback debugging run.
- `/private/tmp/nerae-p02-10-tls.Mi0eU3A5` — CA encoding mismatch before PEM-to-DER correction.
- `/private/tmp/nerae-p02-10-tls.chmMr2EV` — test trust injection debugging.
- `/private/tmp/nerae-p02-10-tls.uRtZH1Lh` — bad CA DER was generated before the CA file existed; this is a script-order failure scene.
- `/private/tmp/nerae-p02-10-tls.vccauFRX` — untrusted CA was initially classified as cancellation; the test trust hook was corrected to default handling.
- `/private/tmp/nerae-p02-10-tls.GME4dlhQ` — intermediate harness assertion failed; retained for diagnosis.

Failed UTS scratch directories are also retained:

- `/private/tmp/nerae-p02-10-uts.keN05cUp` — initial unsupported UTS type-predicate issue, later removed from source.
- `/private/tmp/nerae-p02-10-uts.SHNWAmSw` — early fixture/preparation attempt.
- `/private/tmp/nerae-p02-10-uts.4ViJghm2` — compiler startup/PIF cache failure.
- `/private/tmp/nerae-p02-10-uts.XwIb5DzT` — generic HBuilderX `-10060`.
- `/private/tmp/nerae-p02-10-uts.jZFDZBdK` — generic HBuilderX `-10060`; its generated Swift exposed the UTS object return mismatch and required Codable fields, which were fixed in source.
- `/private/tmp/nerae-p02-10-uts.aflVWY08` — targeted retry after those fixes; still generic HBuilderX `-10060`.
- `/private/tmp/nerae-p02-10-uts.Ji6qfdY3` — framework retry after generated Swift passed the real DCloud framework typecheck; old wrapper still returned generic HBuilderX `-10060`.
- `/private/tmp/nerae-p02-10-typecheck.9jNL0U64` — first generated-source typecheck exposed the optional `String?` flow issue; kept as a failed diagnostic scene.
- `/private/tmp/nerae-p02-10-tls.Q2lG8QOi` — first TLS run after adopting `headersJson` exposed a stale synthetic request field; retained, then fixed and the harness passed.

The successful framework run removed only its own temp directory. All failed scenes listed above remain preserved.

## Remaining limits

- The emitted framework is unsigned; no IPA was produced, and there was no iPhone runtime, simulator, production server, authentication, or training-flow verification.
- The DCloud-framework Swift typecheck and HBuilderX framework emission passed, but neither verifies device networking or real app integration. Downstream callers must strictly parse and validate `headersJson`.
- The TLS trust anchor exists only in the explicit test build macro; production keeps system trust and has no custom CA injection.
- The earlier standalone dylib result predates the final wire-shape and encoded-character edits; the later framework build uses the current source.

## Lead integrated verification

On the integrated working tree, `verify-tls.sh` and `verify-uts.sh` both exited 0. The TLS test used a private loopback receiver and a test-only trust anchor; the iPhoneOS arm64 framework was unsigned, with SHA-256 `96d845b22fb4d3b700f91db24a1f9ae4cc36913d4f6c8fa008da8018e10557ed`. Build hashes differ and no reproducible-build claim is made. The platform adapter is not yet connected to authentication or recording, and Android currently explicitly returns unsupported.

`verify-weixin-resource.sh` passed with the locked uni-app 5.26 compiler after importing this module into a disposable client fixture; output contains the explicit unsupported fallback. `verify-android-uts-source.sh` passed with HBuilderX 5.33 and produced Kotlin fallback source containing `unsupported_platform`. These do not prove a WeChat device, Android UTS linkage, APK, or actual Android HTTPS transport; Android production transport remains unimplemented.
