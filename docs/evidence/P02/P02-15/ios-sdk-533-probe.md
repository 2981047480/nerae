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
