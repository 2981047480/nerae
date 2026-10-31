# iOS offline SDK 5.33 foundation probe

On 2026-10-31 the user-provided `/Users/zephyr/Downloads/SDK 2.zip` was found to be 613,728,676 bytes; SHA-256 `8b84cac86c8bd7224fbce3375d61d19cc2c1b1595e4abf6fa9561741ec90a22e`. Its `uniapp.podspec` declares version `5.33.0`. The installed HBuilderX used in earlier resource probes is 5.33.2026102401, while the repository's CLI dependencies remain locked to 5.26. DCloud's [iOS setup documentation](https://nativesupport.dcloud.net.cn/AppDocs/usesdk/ios) requires the HBuilderX-exported app resources and offline SDK to match. This probe does not establish the whole Nerae application works with HBuilderX 5.33, or justify changing the lockfile.

The archive contains 8,114 entries and approximately 1.81 GB of uncompressed data. To conserve disk, 1,987 selected sample-project, Core SDK, header, bundle and UTS-support entries totaling 98,781,997 bytes were extracted under `/private/tmp/nerae-sdk533-core-20261031`; those entries were read through ZIP CRC validation. The sample Xcode project directly links `libmp3lame.a`, so that additional 2,222,320-byte entry was extracted after the first linker failure. Other archive entries were not checked. The supplied archive's provenance/signature was not independently established by this probe.

In the isolated sample only, the Podfile was restricted to the local Core subspec. The first CocoaPods attempt sought a remote CDN and failed under the network sandbox. A new empty local-only specs repository and isolated `CP_HOME_DIR` then allowed `pod install --no-repo-update` to finish with uniapp 5.33.0; no remote dependency was fetched or existing CocoaPods setup changed.

The initial sandboxed `xcodebuild` failed with Xcode service/log access denied and workspace-open error. An authorized local retry reached the linker but failed because the selected extraction omitted the sample project's direct `libmp3lame.a` dependency. After extracting that file, this command exited 0 and emitted `** BUILD SUCCEEDED **`:

```sh
xcodebuild -workspace /private/tmp/nerae-sdk533-core-20261031/SDK/HBuilder-Hello/HBuilder-Hello.xcworkspace \
  -scheme HBuilder -sdk iphoneos -configuration Debug -destination 'generic/platform=iOS' \
  -derivedDataPath /private/tmp/nerae-sdk533-core-20261031/DerivedData \
  CODE_SIGNING_ALLOWED=NO INDEX_ENABLE_DATA_STORE=NO build
```

`file` identified the generated sample executable as a Mach-O 64-bit arm64 executable. It is an **unsigned SDK sample**, not an installed or usable Nerae app. The probe does not link Nerae's UTS plugins, use Nerae app resources or AppKey, validate signing/provisioning, launch a simulator, contact a real service, or install/run on the iPhone. Apple Development identity was visible locally, but the matching provisioning profile and real installation are not yet verified.

## Project-plugin link attempt

A separate copy of the current client source was prepared at `/private/tmp/nerae-native-533-build`, with the existing import probe as its isolated entry and the current client dependency directory linked for compilation. HBuilderX CLI 5.33 was invoked for an iOS app-resource export. It repeatedly reported that `compile-typescript` was missing and attempted to install it. The export was stopped after the repeated failure; no usable resource output or Nerae-plugin link was established. A read-only follow-up found the plugin directory and its TypeScript dependency, but HBuilderX's own log recorded a plugin-host installation/load exception after reporting installation success. The root cause is not yet established; directory existence and CLI exit status do not prove the plugin is usable. The earlier successful 5.33 small probe used a different source state. The 5.26 App/Weixin import probe on the integrated source did pass, but it is not a 5.33 native link or device run.

A later isolated source copy, `/private/tmp/nerae-hx-533-minimaldeps-nl2x4hya/src`,
linked only `core-js` rather than the project's whole dependency directory and
was exported once with the same HBuilderX command. The log at
`/private/tmp/nerae-hx-533-minimaldeps-nl2x4hya/export.log` still reported the
missing `compile-typescript` plugin and repeated the export; the attempt was
stopped after 65 seconds. Removing the full dependency-directory link alone
did not resolve the problem. This result does not establish a root cause.
HBuilderX's installed-plugin panel separately displayed `typescript编译` 1.0.6
as installed, without an enable control in that panel. That visible status
does not establish the plugin host successfully activated it.
HBuilderX was then restarted with its project windows restored; the DCloud
account session returned after startup. A fresh isolated export at
`/private/tmp/nerae-hx-533-restart-j5ihlk1w/src` again reported the same plugin
error, and was stopped after 60 seconds. Restarting alone did not resolve it;
no resource export success or App build is claimed.

## Direct local 5.33 compiler probe

A new route invoked the bundled 5.33 uni-app CLI directly, setting the local
HBuilderX plugin root explicitly instead of asking the running plugin host to
activate `compile-typescript`. An isolated copy of the current client source
and the compile-only import fixture was prepared at
`/private/tmp/nerae-533-direct-cli-4buf0omd`. Its `node_modules` points at the
bundled HBuilderX 5.33 compiler packages; no project dependency lock was changed.

```sh
env -u HX_PLUGIN_PATHS \
  HX_APP_ROOT=/private/tmp/nerae-hbuilder-download/HBuilderX.app/Contents/HBuilderX \
  /private/tmp/nerae-hbuilder-download/HBuilderX.app/Contents/HBuilderX/plugins/node/node \
  /private/tmp/nerae-hbuilder-download/HBuilderX.app/Contents/HBuilderX/plugins/uniapp-cli-vite/node_modules/@dcloudio/vite-plugin-uni/bin/uni.js \
  build -p app
```

Run from the isolated fixture, this printed `Compiler version: 5.33` and exited
0. Inspection found the referenced AuthEntry and all four native JS proxies,
their iOS and Android UTS wrappers (including versioned journal methods), and
byte-identical copies of the current native source. Each generated iOS plugin
source set was independently linked against the extracted 5.33 DCloud runtime
with `swiftc -emit-library -target arm64-apple-ios15.0`; all four exited 0 and
produced arm64 Mach-O dynamic libraries. Warnings remain in generated/vendor
code. This is a local resource-generation and isolated-plugin linkage probe:
the fixture entry is not Nerae's actual page, and it is not an App link, signed
package, installed device run, or evidence of functional cross-device sync.
The GUI plugin-host activation failure remains unresolved.

For repeatable verification, `python3 scripts/quality/verify_hbuilder_533_local.py`
uses the local HBuilderX bundles and verifies App and Weixin import probes,
generated proxies/wrappers, native source copies and the Weixin unsupported
fallbacks; the integrated-source run exited 0. Weixin fallbacks only show that
App-only bridges fail closed there; they do not implement Weixin login. The script creates
an isolated fixture and removes that fixture only after success. Once a fixture
is created, a failed probe retains it; compiler output is saved if the compiler
has started. Set `NERAE_HBUILDERX_APP` to the actual local
installation path when it differs. This script does not perform the separate
Swift plugin-linking probe. The compiler has the caller's permissions: the
script does not itself modify HBuilderX, but does not enforce an installation
write lock or an OS-level network sandbox.

## iPhoneOS Swift source type-check

With Xcode 26.6 and the iPhoneOS 26.5 SDK, the 5.26-generated UTS Swift
wrappers were independently type-checked with the corresponding Nerae native
Swift files and the extracted 5.33 DCloud frameworks. The `nerae-device-key`,
`nerae-random`, `nerae-https` and versioned `nerae-secure-vault` checks each
exited 0 using `swiftc -typecheck -swift-version 5 -target arm64-apple-ios15.0`.
The same four individual source sets also built with `swiftc -emit-library`
against `DCloudUTSFoundation`; `file` identified each output as an arm64 Mach-O
dynamic library, and `otool -L` confirmed the secure-vault probe references the
DCloud framework. They emitted warnings from generated/vendor source. The secure-vault wrapper
was from the pending shared adapter and its native Swift implementation was
from the reviewed `d68a8c1` integration revision. This is source type-checking
and isolated plugin linkage only: it does not link the four plugins into Nerae, establish 5.26/5.33 runtime
compatibility, sign or install an app, or resolve the HBuilderX export failure.

## Unsigned Xcode integration fixture

An isolated copy of the SDK sample at
`/private/tmp/nerae-ios-533-pod-probe-3r6s75__` was populated with the 5.33
compile-only fixture resources and the generated `app-ios` sources for all four
Nerae UTS plugins. Its `control.xml` and resource directory use the fixture's
manifest AppID. CocoaPods 1.16.2 installed five local Pods (SDK Core and four
plugins) without updating repositories. The sample's generator omitted required
Podspec author/homepage fields; the **temporary fixture only** uses explicitly
test-only metadata, including `example.invalid`. It is not release metadata.

The first Xcode build failed to resolve `DCloudUTSFoundation` because CocoaPods
did not discover SDK files through the fixture's initial directory symlink.
An attempted framework search-path override reached a separate missing-header
error. After using an isolated SDK copy and regenerating the Pods, all plugin
targets compiled, but final linking failed because the SDK sample generator
copied `DCloudUTSConfig` and `UTSCPP` into every static plugin. The temporary
generator was then adjusted to compile those shared helpers in one plugin only.

With those fixture-specific corrections, `xcodebuild` exited 0 for generic
`iphoneos` arm64 with `CODE_SIGNING_ALLOWED=NO` and `INDEX_ENABLE_DATA_STORE=NO`;
the log ends in `** BUILD SUCCEEDED **`. The resulting executable and debug
library are arm64 Mach-O files. `nm` found all four generated UTS Objective-C
classes in the linked debug library; the bundled `app-service.js` is identical
to the 5.33 fixture output, and `codesign` reports the app is not signed.

This is an **unsigned test fixture**, not an installable Nerae release or a
usable page: the entry is the import probe, the generator/metadata changes exist
only in the temporary sample, and class symbols do not prove plugin discovery
or runtime behavior. No AppKey, team provisioning, real service, phone install,
or device operation was verified. The generated support-code ownership and
Podspec metadata must be solved in the eventual project packaging pipeline.
The fixture copied the full generated resource directory, so its bundle also
contains generated Swift/Kotlin source files and one root `config.json`; release
packaging must exclude native source and separately verify per-plugin resource
resolution. This fixture must not be installed or distributed as a product.
