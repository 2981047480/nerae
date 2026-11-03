# 5.33 npm compiler and offline iOS SDK candidate

The project and client DCloud compiler packages are aligned to npm version
`3.0.0-5030320261023001`, whose generated manifest reports compiler 5.33. The
user-supplied offline SDK's `uniapp.podspec` reports `5.33.0`. This aligns the
reported release family; it does not independently prove identical patch
builds or runtime compatibility. The SDK archive matched the SHA-256 recorded
in the earlier probe. Its origin/signature was not independently verified in
this test.

The official npm registry reported that all seven required DCloud packages
exist at that version. Its metadata requires Node >=22.12.0 and Vite 5.2.8;
this workspace used Node 26.0.0, pnpm 11.5.3, and the existing Vite 5.2.8 and
`@dcloudio/types` 3.4.31. No non-DCloud direct dependency was changed. The
generated pnpm lock also changes some transitive resolutions (including Babel
types, debug, magic-string, source-map-js, picocolors and resolve) and adds
`@rolldown/pluginutils` and `nostics`; those changes are part of the candidate
and were not independently accepted as general dependency upgrades.

## Verification

- An isolated `pnpm install --no-frozen-lockfile --ignore-scripts` fetched the
  candidate into a separate `/private/tmp` store; a subsequent offline frozen
  install passed. No dependency install scripts were run.
- Client typecheck and 220 training/App-factory tests passed. App, Weixin and
  H5 resource builds passed using compiler 5.33. The feature import probe
  generated iOS/Android native wrappers and Weixin unsupported fallbacks.
- Four generated iOS plugin source sets were individually linked to the
  extracted 5.33 SDK's `DCloudUTSFoundation` and `DCUniBase` as unsigned
  iPhoneOS arm64 libraries. The repeatable checker is
  `python3 -B scripts/quality/verify_ios_uts_sdk_link.py --sdk-root <SDK-dir>`;
  by default it checks installed compiler packages, freshly builds the probe,
  then checks the compiler and SDK release labels, fixture UTS inputs, copied
  Swift sources and target platform. An optional prebuilt probe supports a
  link-only check without proving build provenance. A negative check rejected
  an older 5.26 probe.
- A separate SDK sample was prepared under
  `/private/tmp/nerae-sdk533-uts-20261103/SDK/HBuilder-Hello` using only Core,
  the four freshly generated plugin sources and probe resources. CocoaPods
  installed five local pods without updating remote specs. The SDK sample's
  generator was changed **only in this temporary fixture** to add test-only
  Podspec author/homepage and give the shared native helper implementation one
  owner. An unsigned generic iPhoneOS `xcodebuild` exited 0 and reported
  `BUILD SUCCEEDED`. Its executable and debug library are arm64; Objective-C
  symbols for all four UTS modules are present and its bundled app-service JS
  matches the compiled probe. `codesign` reports the fixture is unsigned.

The first individual link attempt lacked `DCUniBase`; extracting that SDK
framework and headers resolved it. A first Xcode build logged success, but
its shell wrapper failed because `status` is reserved by zsh; a repeat using a
different variable exited 0. A subsequent pnpm H5 invocation tried to use a
different default store and refused noninteractive module removal; invoking the
installed local compiler directly succeeded. Apple's Python initially tried to
write its bytecode cache outside the sandbox; syntax compilation succeeded
with a private `/private/tmp` cache. These failures were not counted as
successful commands.

## Remaining limits

The Xcode fixture's entry is an import probe; Nerae's actual page is not wired.
It includes test-only metadata, copied native sources and colliding plugin
`config.json` resources, and the sample emitted vendor/privacy warnings. Its
generator changes are not a production packaging pipeline. No AppKey,
provisioning profile, code signing, installation, device launch, Keychain,
Android APK or Weixin login has been validated. The true authenticated sync
and both record screens are still unfinished. Neither this candidate nor the
fixture should be presented or installed as a completed Nerae app.
