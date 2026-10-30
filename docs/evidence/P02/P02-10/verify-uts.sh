#!/usr/bin/env bash
set -euo pipefail
repo_root=$(git rev-parse --show-toplevel)
evidence_dir="$repo_root/docs/evidence/P02/P02-10"
hbuilder_app=${NERAE_HBUILDERX_APP:-/private/tmp/nerae-hbuilder-download/HBuilderX.app}
node_bin=${NERAE_NODE:-/opt/homebrew/bin/node}
plugins_dir="$hbuilder_app/Contents/HBuilderX/plugins"
compiler="$plugins_dir/uniapp-uts-v1/node_modules/@dcloudio/uni-uts-v1/dist/swift.js"
base_ipa="$plugins_dir/launcher/base/iPhone_base.ipa"
if [[ ! -x "$node_bin" || ! -f "$base_ipa" || ! -f "$compiler" || ! -f "$hbuilder_app/Contents/Info.plist" ]]; then
  echo 'Required HBuilderX compiler, iPhone base, or Node executable is missing.' >&2
  exit 1
fi
version=$(/usr/libexec/PlistBuddy -c 'Print CFBundleShortVersionString' "$hbuilder_app/Contents/Info.plist")
[[ "$version" == 5.33.* ]] || { echo "Expected HBuilderX 5.33; got $version" >&2; exit 1; }
work_dir=$(mktemp -d /private/tmp/nerae-p02-10-uts.XXXXXXXX)
succeeded=0
cleanup() {
  if [[ "$succeeded" == 1 ]]; then rm -rf -- "$work_dir"; else printf 'UTS compile evidence retained: %s\n' "$work_dir" >&2; fi
}
trap cleanup EXIT
mkdir -p "$work_dir/input/uni_modules" "$work_dir/output" "$work_dir/tmp" "$work_dir/bin"
cp -R "$repo_root/apps/client/src/uni_modules/nerae-https" "$work_dir/input/uni_modules/"
cat > "$work_dir/bin/xcodebuild" <<'WRAPPER'
#!/bin/bash
set -o pipefail
/usr/bin/xcodebuild "$@" 2>&1 | tee -a "$NERAE_XCODEBUILD_LOG"
exit "${PIPESTATUS[0]}"
WRAPPER
chmod +x "$work_dir/bin/xcodebuild"
export PATH="$work_dir/bin:$PATH"
export TMPDIR="$work_dir/tmp"
export NERAE_XCODEBUILD_LOG="$work_dir/xcodebuild.log"
export UNI_INPUT_DIR="$work_dir/input" UNI_OUTPUT_DIR="$work_dir/output"
export UNI_HBUILDERX_PLUGINS="$plugins_dir" IOS_STANDARD_BASE_PATH="$base_ipa" IOS_RUNTIME_NASE_PATH="$base_ipa"
export HX_Version=5.33 UNI_COMPILER_VERSION=5.33 NODE_ENV=development NERAE_UTS_COMPILER="$compiler"
printf 'HBuilderX %s; Node %s; iPhoneOS SDK %s\n' "$version" "$($node_bin --version)" "$(xcrun --sdk iphoneos --show-sdk-version)"
"$node_bin" <<'NODE'
const swift = require(process.env.NERAE_UTS_COMPILER)
const file = process.env.UNI_INPUT_DIR + '/uni_modules/nerae-https/utssdk/app-ios/index.uts'
;(async () => {
  const result = await swift.runSwiftDev(file, {
    components: {}, customElements: {}, isX: false, isSingleThread: false,
    isPlugin: true, isExtApi: false, extApis: {}, uniModules: [],
  })
  if (!result || result.code !== 0) {
    process.stderr.write('UTS_RESULT=' + JSON.stringify({ code: result?.code, msg: result?.msg || '' }) + '\n')
    throw new Error(String(result?.msg || 'no result'))
  }
})().catch(error => {
  process.stderr.write(String(error?.message || error) + '\n')
  process.exitCode = 1
})
NODE
framework=$(find "$work_dir/input/unpackage/modules" -maxdepth 2 -name 'unimoduleNeraeHttps' -type f -print -quit)
[[ -n "$framework" && -f "$framework" ]] || { echo 'Expected Nerae HTTPS framework binary was not produced.' >&2; exit 1; }
file "$framework" | grep -q 'Mach-O 64-bit dynamically linked shared library arm64' || { echo 'Framework is not an arm64 Mach-O binary.' >&2; exit 1; }
xcrun vtool -show-build "$framework" > "$work_dir/framework-build.txt"
grep -Eq 'platform[[:space:]]+IOS$' "$work_dir/framework-build.txt" || { echo 'Framework is not built for iPhoneOS.' >&2; exit 1; }
printf 'Framework: %s\n' "$framework"
cat "$work_dir/framework-build.txt"
shasum -a 256 "$framework"
printf 'PASS: HBuilderX %s emitted an unsigned iPhoneOS arm64 framework; this is not an IPA or installed App.\n' "$version"
succeeded=1
