#!/usr/bin/env bash
set -euo pipefail

evidence_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)
repo_root=$(git -C "$evidence_dir" rev-parse --show-toplevel)
hbuilder_app=${NERAE_HBUILDERX_APP:-/private/tmp/nerae-hbuilder-download/HBuilderX.app}
node_bin=${NERAE_NODE:-/opt/homebrew/bin/node}
plugins_dir="$hbuilder_app/Contents/HBuilderX/plugins"
base_ipa="$plugins_dir/launcher/base/iPhone_base.ipa"
compiler="$plugins_dir/uniapp-uts-v1/node_modules/@dcloudio/uni-uts-v1/dist/swift.js"
module_source="$repo_root/apps/client/src/uni_modules/nerae-random"

if [[ ! -x "$node_bin" || ! -f "$base_ipa" || ! -f "$compiler" || ! -f "$hbuilder_app/Contents/Info.plist" ]]; then
	printf 'Required local HBuilderX compiler, iPhone base IPA, or Node executable is missing.\n' >&2
	exit 2
fi
version=$(/usr/libexec/PlistBuddy -c 'Print CFBundleShortVersionString' "$hbuilder_app/Contents/Info.plist")
if [[ "$version" != 5.33.* ]]; then
	printf 'Expected HBuilderX 5.33, got %s.\n' "$version" >&2
	exit 2
fi

work_dir=$(mktemp -d /private/tmp/nerae-p02-09-ios.XXXXXXXX)
succeeded=0
cleanup() {
	if [[ "$succeeded" == 1 && "${NERAE_KEEP_P02_09_IOS:-0}" != 1 ]]; then
		rm -rf -- "$work_dir"
	else
		printf 'iOS random-key test directory retained: %s\n' "$work_dir" >&2
	fi
}
trap cleanup EXIT
input_dir="$work_dir/input"
output_dir="$work_dir/output"
mkdir -p "$input_dir/uni_modules" "$output_dir" "$work_dir/tmp"
cp -R "$module_source" "$input_dir/uni_modules/"

printf 'HBuilderX %s; iPhoneOS SDK %s; Node %s\n' "$version" "$(xcrun --sdk iphoneos --show-sdk-version)" "$($node_bin --version)"
TMPDIR="$work_dir/tmp" UNI_INPUT_DIR="$input_dir" UNI_OUTPUT_DIR="$output_dir" \
UNI_HBUILDERX_PLUGINS="$plugins_dir" IOS_STANDARD_BASE_PATH="$base_ipa" IOS_RUNTIME_NASE_PATH="$base_ipa" \
HX_Version=5.33 UNI_COMPILER_VERSION=5.33 NODE_ENV=development NERAE_UTS_COMPILER="$compiler" \
"$node_bin" <<'NODE'
const swift = require(process.env.NERAE_UTS_COMPILER)
const filename = process.env.UNI_INPUT_DIR + '/uni_modules/nerae-random/utssdk/app-ios/index.uts'
;(async () => {
  try {
    const result = await swift.runSwiftDev(filename, {
      components: {}, customElements: {}, isX: false, isSingleThread: false,
      isPlugin: true, isExtApi: false, extApis: {}, uniModules: [],
    })
    if (!result || result.code !== 0) throw new Error(String(result?.msg || result?.error || 'HBuilderX returned no successful compilation result'))
  } catch (_error) {
    process.stderr.write(String(_error?.message || 'HBuilderX iOS UTS compilation failed') + '\n')
    process.exitCode = 1
  }
})()
NODE

framework="$input_dir/unpackage/modules/unimoduleNeraeRandom.framework"
binary="$framework/unimoduleNeraeRandom"
if [[ ! -f "$binary" ]] || ! file "$binary" | grep -q 'Mach-O 64-bit dynamically linked shared library arm64'; then
	printf 'Expected unsigned iPhoneOS arm64 framework was not produced.\n' >&2
	exit 1
fi
if ! xcrun vtool -show-build "$binary" | grep -Eq 'platform[[:space:]]+IOS$'; then
	printf 'The generated framework is not an iPhoneOS build.\n' >&2
	exit 1
fi

swiftc -o "$work_dir/ios-random-smoke" \
	"$module_source/utssdk/app-ios/NeraeSecureRandomBridge.swift" \
	"$evidence_dir/ios-random-smoke.swift"
"$work_dir/ios-random-smoke"
printf 'PASS: HBuilderX produced an unsigned iPhoneOS arm64 framework (not an IPA).\n'
shasum -a 256 "$binary"
succeeded=1
