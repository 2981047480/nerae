#!/usr/bin/env bash
set -euo pipefail
evidence_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)
repo_root=$(git -C "$evidence_dir" rev-parse --show-toplevel)
hbuilder_app=${NERAE_HBUILDERX_APP:-/private/tmp/nerae-hbuilder-download/HBuilderX.app}
node_bin=${NERAE_NODE:-/opt/homebrew/bin/node}
plugins_dir="$hbuilder_app/Contents/HBuilderX/plugins"
base_ipa="$plugins_dir/launcher/base/iPhone_base.ipa"
compiler="$plugins_dir/uniapp-uts-v1/node_modules/@dcloudio/uni-uts-v1/dist/swift.js"
if [[ ! -x "$node_bin" || ! -f "$base_ipa" || ! -f "$compiler" || ! -f "$hbuilder_app/Contents/Info.plist" ]]; then
  printf 'Required local HBuilderX compiler, iPhone base IPA, or Node executable is missing.\n' >&2
  exit 2
fi
version=$(/usr/libexec/PlistBuddy -c 'Print CFBundleShortVersionString' "$hbuilder_app/Contents/Info.plist")
if [[ "$version" != 5.33.* ]]; then printf 'Expected HBuilderX 5.33; got %s.\n' "$version" >&2; exit 2; fi
work_dir=$(mktemp -d /private/tmp/nerae-p02-13-ios-uts.XXXXXXXX)
succeeded=0
cleanup() {
  if [[ "$succeeded" == 1 && "${NERAE_KEEP_P02_13_IOS_UTS:-0}" != 1 ]]; then rm -rf -- "$work_dir"
  else printf 'iOS UTS compile directory retained: %s\n' "$work_dir" >&2; fi
}
trap cleanup EXIT
input_dir="$work_dir/input"; output_dir="$work_dir/output"
mkdir -p "$input_dir/uni_modules" "$output_dir" "$work_dir/tmp" "$work_dir/bin"
cp -R "$repo_root/apps/client/src/uni_modules/nerae-secure-vault" "$input_dir/uni_modules/"
cat > "$work_dir/bin/xcodebuild" <<'WRAPPER'
#!/bin/bash
set -o pipefail
/usr/bin/xcodebuild "$@" 2>&1 | tee -a "$NERAE_XCODEBUILD_LOG"
exit "${PIPESTATUS[0]}"
WRAPPER
chmod +x "$work_dir/bin/xcodebuild"
export PATH="$work_dir/bin:$PATH"
export NERAE_XCODEBUILD_LOG="$work_dir/xcodebuild.log"
TMPDIR="$work_dir/tmp" UNI_INPUT_DIR="$input_dir" UNI_OUTPUT_DIR="$output_dir" \
UNI_HBUILDERX_PLUGINS="$plugins_dir" IOS_STANDARD_BASE_PATH="$base_ipa" IOS_RUNTIME_NASE_PATH="$base_ipa" \
HX_Version=5.33 UNI_COMPILER_VERSION=5.33 NODE_ENV=development NERAE_UTS_COMPILER="$compiler" \
"$node_bin" <<'NODE'
const swift = require(process.env.NERAE_UTS_COMPILER)
const fs = require('fs')
const filename = process.env.UNI_INPUT_DIR + '/uni_modules/nerae-secure-vault/utssdk/app-ios/index.uts'
;(async () => {
  try {
    const result = await swift.runSwiftDev(filename, {
      components: {}, customElements: {}, isX: false, isSingleThread: false,
      isPlugin: true, isExtApi: false, extApis: {}, uniModules: [],
    })
    if (!result || result.code !== 0) throw new Error('UTS native compilation failed: ' + String(result?.msg || 'no result'))
    const generated = process.env.UNI_INPUT_DIR + '/unpackage/modules/unimoduleNeraeSecureVault.framework/unimoduleNeraeSecureVault'
    if (!fs.existsSync(generated)) throw new Error('expected iPhoneOS framework was not produced')
    process.stdout.write(`PASS: HBuilderX ${process.env.HX_Version} produced unsigned iPhoneOS framework ${generated}; not an app.\n`)
  } catch (error) { process.stderr.write(String(error?.message || error) + '\n'); process.exitCode = 1 }
})()
NODE
binary="$input_dir/unpackage/modules/unimoduleNeraeSecureVault.framework/unimoduleNeraeSecureVault"
swift_interface="$input_dir/unpackage/modules/unimoduleNeraeSecureVault.framework/Modules/unimoduleNeraeSecureVault.swiftmodule/arm64-apple-ios.private.swiftinterface"
if [[ ! -f "$binary" ]] || ! file "$binary" | grep -q 'Mach-O 64-bit dynamically linked shared library arm64'; then
  printf 'Expected unsigned iPhoneOS arm64 framework binary was not produced.\n' >&2; exit 1
fi
if ! xcrun vtool -show-build "$binary" | grep -Eq 'platform[[:space:]]+IOS$'; then
  printf 'Framework is not an iPhoneOS build.\n' >&2; exit 1
fi
if [[ ! -f "$swift_interface" ]] || ! grep -Fq 'public func readTrainingOperationJournal()' "$swift_interface" || \
   ! grep -Fq 'public func compareAndSetTrainingOperationJournal(_ expectedFound: Swift.Bool, _ expectedValue: Swift.String, _ value: Swift.String)' "$swift_interface" || \
   ! grep -Fq 'public static func compareAndSetTrainingOperationJournal(_ expectedFound: Swift.Bool, _ expectedValue: Swift.String, _ value: Swift.String)' "$swift_interface"; then
  printf 'Compiled framework interface omitted the new journal read/CAS methods or exposed parameter labels.\n' >&2; exit 1
fi
printf 'PASS: generated iPhoneOS arm64 framework interface exports both journal methods with unlabeled Swift bridge parameters.\n'
shasum -a 256 "$binary"
succeeded=1
