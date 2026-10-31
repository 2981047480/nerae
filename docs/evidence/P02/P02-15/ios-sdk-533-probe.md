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
