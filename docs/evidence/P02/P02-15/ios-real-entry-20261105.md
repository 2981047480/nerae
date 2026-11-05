# iOS real-entry unsigned build, 2026-11-05

An isolated integration copy of `c2aeb59` was built with the installed npm
uni-app compiler `3.0.0-5030320261023001` (reported as 5.33). Unlike earlier
import fixtures, this used the current Nerae application entry and pages:

- The 5.33 compiler generated App and Weixin resources successfully. The
  executable was invoked through the package's actual `bin/uni.js`; the linked
  `.bin/uni` wrapper did not resolve its relative pnpm path from this isolated
  copy.
- The App resources and four generated iOS plugins were staged into a private
  copy of the previously extracted 5.33 SDK sample. The staged web resources
  excluded `uni_modules`; the plugin sources were copied separately for native
  compilation. CocoaPods installed only the five local SDK/plugin pods using
  the existing local-only specs fixture.
- `xcodebuild -workspace HBuilder-Hello.xcworkspace -scheme HBuilder -sdk
  iphoneos -configuration Debug -destination 'generic/platform=iOS'
  CODE_SIGNING_ALLOWED=NO INDEX_ENABLE_DATA_STORE=NO -quiet build` exited 0.
  The sample SDK and Apple framework headers emitted deprecation and debug
  symbol warnings. The resulting `HBuilder.app` executable is arm64.
- The bundled `app-service.js` SHA-256 matches the generated current-entry
  file (`c5a2a99ad7a0b64e90a58bd44aa30e5c60ec409608e84e03c25586d754c9064b`).
  The linked debug library contains the four Nerae native bridge classes, and
  no Swift or Kotlin files were found under the bundled web resources.

This is a build probe, not a releasable or installable Nerae app. The isolated
sample still has test-only Podspec metadata, a sample bundle identifier and an
unverified DCloud AppKey. Four generated plugin `config.json` files all happen
to be empty in this build and collapse to one empty file in the app bundle;
future non-empty plugin resources need an explicit packaging solution. Native
plugin discovery, sign-in, Keychain, network sync and page behavior have not
been verified on a device. The iPhone was reported unavailable when checked.
An Apple signing identity was visible in an authorized local check, but no
matching team/profile or successful signature was established.
