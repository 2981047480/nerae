#!/usr/bin/env bash
set -euo pipefail

evidence_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)
repo_root=$(git -C "$evidence_dir" rev-parse --show-toplevel)
source_dir="$repo_root/infra/app/ios/device"
plugin_dir="$repo_root/apps/client/src/uni_modules/nerae-device-key/utssdk/app-ios"
hbuilder_app=${NERAE_HBUILDERX_APP:-/private/tmp/nerae-hbuilder-download/HBuilderX.app}
node_bin=${NERAE_NODE:-/opt/homebrew/bin/node}
plugins_dir="$hbuilder_app/Contents/HBuilderX/plugins"
base_ipa="$plugins_dir/launcher/base/iPhone_base.ipa"
compiler="$plugins_dir/uniapp-uts-v1/node_modules/@dcloudio/uni-uts-v1/dist/swift.js"

for file in DeviceKey.swift DeviceProof.swift SignatureEncoding.swift; do
	if ! cmp -s "$source_dir/$file" "$plugin_dir/$file"; then
		printf 'Swift source drift: %s differs from infra/app/ios/device/%s\n' "$plugin_dir/$file" "$file" >&2
		exit 1
	fi
done
printf 'PASS: the three reviewed Swift sources match their infra originals byte-for-byte.\n'

if [[ ! -x "$node_bin" || ! -f "$base_ipa" || ! -f "$compiler" || ! -f "$hbuilder_app/Contents/Info.plist" ]]; then
	printf 'Required local HBuilderX compiler, iPhone base IPA, or Node executable is missing.\n' >&2
	exit 1
fi
version=$(/usr/libexec/PlistBuddy -c 'Print CFBundleShortVersionString' "$hbuilder_app/Contents/Info.plist")
if [[ "$version" != 5.33.* ]]; then
	printf 'This UTS bridge probe is validated with HBuilderX 5.33; got %s.\n' "$version" >&2
	exit 1
fi

work_dir=$(mktemp -d /private/tmp/nerae-p02-05-uts.XXXXXXXX)
succeeded=0
cleanup() {
	if [[ "$succeeded" == 1 && "${NERAE_KEEP_P02_05_UTS:-0}" != 1 ]]; then
		rm -rf -- "$work_dir"
	else
		printf 'UTS compile directory retained: %s\n' "$work_dir" >&2
	fi
}
trap cleanup EXIT
input_dir="$work_dir/input"
output_dir="$work_dir/output"
mkdir -p "$input_dir/uni_modules" "$output_dir" "$work_dir/tmp"
cp -R "$repo_root/apps/client/src/uni_modules/nerae-device-key" "$input_dir/uni_modules/"

printf 'HBuilderX %s; iPhoneOS SDK %s; Node %s\n' "$version" "$(xcrun --sdk iphoneos --show-sdk-version)" "$($node_bin --version)"
TMPDIR="$work_dir/tmp" UNI_INPUT_DIR="$input_dir" UNI_OUTPUT_DIR="$output_dir" \
UNI_HBUILDERX_PLUGINS="$plugins_dir" IOS_STANDARD_BASE_PATH="$base_ipa" IOS_RUNTIME_NASE_PATH="$base_ipa" \
HX_Version=5.33 UNI_COMPILER_VERSION=5.33 NODE_ENV=development NERAE_UTS_COMPILER="$compiler" \
"$node_bin" <<'NODE'
const swift = require(process.env.NERAE_UTS_COMPILER)
const filename = process.env.UNI_INPUT_DIR + '/uni_modules/nerae-device-key/utssdk/app-ios/index.uts'
;(async () => {
  try {
    const result = await swift.runSwiftDev(filename, {
      components: {}, customElements: {}, isX: false, isSingleThread: false,
      isPlugin: true, isExtApi: false, extApis: {}, uniModules: [],
    })
    if (!result || result.code !== 0) {
      throw new Error('UTS native compilation failed: ' + String(result?.msg || 'no result'))
    }
  } catch (error) {
    process.stderr.write(String(error?.message || error) + '\n')
    process.exitCode = 1
  }
})()
NODE

framework="$input_dir/unpackage/modules/unimoduleNeraeDeviceKey.framework"
binary="$framework/unimoduleNeraeDeviceKey"
if [[ ! -f "$binary" ]] || ! file "$binary" | grep -q 'Mach-O 64-bit dynamically linked shared library arm64'; then
	printf 'The expected unsigned iPhoneOS arm64 framework was not produced.\n' >&2
	exit 1
fi
if ! xcrun vtool -show-build "$binary" | grep -Eq 'platform[[:space:]]+IOS$'; then
	printf 'The resulting framework is not an iPhoneOS build.\n' >&2
	exit 1
fi
printf 'PASS: official HBuilderX %s compiled the production UTS module to an unsigned iPhoneOS arm64 framework (not an app).\n' "$version"
shasum -a 256 "$binary"
succeeded=1
