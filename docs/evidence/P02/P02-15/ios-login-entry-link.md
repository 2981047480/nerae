# iOS link of the current login-entry resources

An isolated copy of the user-provided 5.33 offline SDK sample was populated
with the App resource build from integration commit `a4d1108` and the four
corresponding generated iOS plugin source sets. Its sample-specific Podspec
metadata and shared helper ownership adjustments remain test-only. CocoaPods
installed the local SDK Core and plugins using an isolated home and local specs
repository. The generic iPhoneOS arm64 `xcodebuild` with
`CODE_SIGNING_ALLOWED=NO` exited 0 and ended in `BUILD SUCCEEDED`.

The first build copied an older test probe and the SDK's sample app along with
the desired resources. They were moved outside the sample's bundled resource
tree, without deleting the originals, and a fresh derived-data build passed.
The final bundle has exactly one `app-service.js` under its app resources;
`cmp` confirms that it matches the integrated client build. Those app
resources contain no `.swift` or `.kt` files. `file` identifies the executable
as arm64; `nm` found Objective-C class symbols for all four UTS plugins and
`otool` shows the JavaScriptCore framework. `codesign -dv` reports the app is
not signed. The four generated plugin `config.json` files and the single
bundled root `config.json` are all the same two-byte empty object; future
non-empty plugin resource collisions still require a packaging solution.

Fixture and logs: `/private/tmp/nerae-ios-entry-link-20261103/SDK/HBuilder-Hello`,
`/private/tmp/nerae-ios-entry-link-20261103/build-final.log`, and
`/private/tmp/nerae-ios-entry-link-20261103/DerivedData-final`. The first hash
inspection command accidentally used zsh's special `path` variable and could
not find the tools; it was rerun successfully with a task-specific variable.
The SDK produced vendor/deprecation and precompiled-module warnings.

This is an unsigned SDK fixture with the actual Nerae login-entry resources,
not a production packaging pipeline or a usable installation. No API endpoint
was configured, no AppKey or provisioning profile was verified, and no app was
installed or executed on an iPhone. Linker symbols do not establish plugin
discovery, Keychain behavior or UI appearance at runtime. Training writes,
cross-device synchronization, Android packaging and Weixin login remain open.
