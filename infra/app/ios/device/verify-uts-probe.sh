#!/usr/bin/env bash
# Compile the current Swift device-key sources through an isolated UTS probe.
# This does not build, sign, install, or run an application.
set -euo pipefail

source_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
hbuilder_app=${NERAE_HBUILDERX_APP:-/private/tmp/nerae-hbuilder-download/HBuilderX.app}
node_bin=${NERAE_NODE:-/opt/homebrew/bin/node}
plugins_dir="$hbuilder_app/Contents/HBuilderX/plugins"
base_ipa="$plugins_dir/launcher/base/iPhone_base.ipa"
compiler="$plugins_dir/uniapp-uts-v1/node_modules/@dcloudio/uni-uts-v1/dist/swift.js"

if [[ ! -x "$node_bin" || ! -f "$base_ipa" || ! -f "$compiler" || ! -f "$hbuilder_app/Contents/Info.plist" ]]; then
  printf 'Required HBuilderX compiler, base IPA, or Node executable is missing.\n' >&2
  exit 1
fi
version=$(/usr/libexec/PlistBuddy -c 'Print CFBundleShortVersionString' "$hbuilder_app/Contents/Info.plist")
if [[ "$version" != 5.33.* ]]; then
  printf 'This probe was validated with HBuilderX 5.33; got %s.\n' "$version" >&2
  exit 1
fi

work_dir=$(mktemp -d /private/tmp/nerae-uts-probe.XXXXXXXX)
succeeded=0
cleanup() {
  if [[ "$succeeded" == 1 && "${NERAE_KEEP_PROBE:-0}" != 1 ]]; then
    rm -rf -- "$work_dir"
  else
    printf 'Probe directory retained: %s\n' "$work_dir" >&2
  fi
}
trap cleanup EXIT
input_dir="$work_dir/input"
output_dir="$work_dir/output"
module_dir="$input_dir/uni_modules/nerae-device-probe/utssdk"
mkdir -p "$module_dir/app-ios" "$output_dir" "$work_dir/tmp"
cp "$source_dir/DeviceKey.swift" "$source_dir/DeviceProof.swift" "$source_dir/SignatureEncoding.swift" "$module_dir/app-ios/"
cat > "$input_dir/uni_modules/nerae-device-probe/package.json" <<'JSON'
{"id":"nerae-device-probe","displayName":"Nerae Compile Probe","version":"0.0.1","uni_modules":{"type":"uts"}}
JSON
cat > "$module_dir/interface.uts" <<'UTS'
export type Probe = (alias: string, resourceID: string, nonce: string) => string
UTS
cat > "$module_dir/app-ios/index.uts" <<'UTS'
import { Probe } from '../interface.uts'
export const probe: Probe = function (alias: string, resourceID: string, nonce: string): string {
  return NeraeBridgeProbe.sign(alias, resourceID, nonce)
}
UTS
cat > "$module_dir/app-ios/NeraeBridgeProbe.swift" <<'SWIFT'
import Foundation
// Compile-only bridge: never invoke or ship this as the production interface.
public class NeraeBridgeProbe {
    public static func sign(_ alias: String, _ resourceID: String, _ nonce: String) -> String {
        do {
            return try NeraeDeviceKeyStore.signForExisting(alias: alias, purpose: .emailLogin, resourceID: resourceID, nonce: nonce).forRequest()
        } catch {
            return "probe-error"
        }
    }
}
SWIFT

TMPDIR="$work_dir/tmp" UNI_INPUT_DIR="$input_dir" UNI_OUTPUT_DIR="$output_dir" \
UNI_HBUILDERX_PLUGINS="$plugins_dir" IOS_STANDARD_BASE_PATH="$base_ipa" IOS_RUNTIME_NASE_PATH="$base_ipa" \
HX_Version=5.33 UNI_COMPILER_VERSION=5.33 NODE_ENV=development NERAE_UTS_COMPILER="$compiler" \
"$node_bin" <<'NODE'
const swift = require(process.env.NERAE_UTS_COMPILER)
const filename = process.env.UNI_INPUT_DIR + '/uni_modules/nerae-device-probe/utssdk/app-ios/index.uts'
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

binary="$input_dir/unpackage/modules/unimoduleNeraeDeviceProbe.framework/unimoduleNeraeDeviceProbe"
if [[ ! -f "$binary" ]] || ! file "$binary" | grep -q 'Mach-O 64-bit dynamically linked shared library arm64'; then
  printf 'The expected arm64 dynamic library was not produced.\n' >&2
  exit 1
fi
if ! xcrun vtool -show-build "$binary" | grep -Eq 'platform[[:space:]]+IOS$'; then
  printf 'The resulting framework is not an iPhoneOS build.\n' >&2
  exit 1
fi
printf 'PASS: HBuilderX %s produced an unsigned iPhoneOS arm64 UTS probe, not an app.\n' "$version"
shasum -a 256 "$binary"
succeeded=1
