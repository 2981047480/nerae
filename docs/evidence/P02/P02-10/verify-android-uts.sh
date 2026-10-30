#!/usr/bin/env bash
set -euo pipefail

repo_root=$(git rev-parse --show-toplevel)
hbuilder_app=${NERAE_HBUILDERX_APP:-/private/tmp/nerae-hbuilder-download/HBuilderX.app}
node_bin=${NERAE_NODE:-/opt/homebrew/bin/node}
plugins_dir="$hbuilder_app/Contents/HBuilderX/plugins"
compiler="$plugins_dir/uniapp-uts-v1/node_modules/@dcloudio/uni-uts-v1/dist/kotlin.js"
version=$(/usr/libexec/PlistBuddy -c 'Print CFBundleShortVersionString' "$hbuilder_app/Contents/Info.plist")
[[ -x "$node_bin" && -f "$compiler" && "$version" == 5.33.* ]] || {
  echo 'HBuilderX 5.33 Android UTS compiler is unavailable.' >&2
  exit 2
}

work_dir=$(mktemp -d /private/tmp/nerae-p02-10-android-uts.XXXXXXXX)
succeeded=0
cleanup() {
  if [[ "$succeeded" == 1 && "${NERAE_KEEP_ANDROID_UTS:-0}" != 1 ]]; then rm -rf -- "$work_dir"; else printf 'Android UTS evidence retained: %s\n' "$work_dir" >&2; fi
}
trap cleanup EXIT
mkdir -p "$work_dir/input/uni_modules" "$work_dir/output" "$work_dir/tmp"
cp -R "$repo_root/apps/client/src/uni_modules/nerae-https" "$work_dir/input/uni_modules/"
TMPDIR="$work_dir/tmp" UNI_INPUT_DIR="$work_dir/input" UNI_OUTPUT_DIR="$work_dir/output" \
UNI_HBUILDERX_PLUGINS="$plugins_dir" HX_Version=5.33 UNI_COMPILER_VERSION=5.33 NODE_ENV=development \
NERAE_UTS_COMPILER="$compiler" "$node_bin" <<'NODE'
const fs = require('fs')
const path = require('path')
const kotlin = require(process.env.NERAE_UTS_COMPILER)
const filename = path.join(process.env.UNI_INPUT_DIR, 'uni_modules/nerae-https/utssdk/app-android/index.uts')
;(async () => {
  const result = await kotlin.runKotlinProd(filename, {
    components: {}, customElements: {}, uniModuleId: 'nerae-https', isPlugin: true,
    isModule: false, isX: false, isSingleThread: false, isExtApi: false,
    hookClass: '', extApis: {}, uniModules: [], noEmit: false, sourceMap: false,
  })
  if (!result || result.error) throw new Error('Kotlin UTS conversion failed')
  const source = path.join(process.env.UNI_OUTPUT_DIR, 'uni_modules/nerae-https/utssdk/app-android/src/index.kt')
  if (!fs.existsSync(source)) throw new Error('generated Android Kotlin wrapper missing')
  const text = fs.readFileSync(source, 'utf8')
  if (!text.includes('UTSNumber.isInteger(_request.timeoutMs)') ||
      !text.includes('timeoutMsText = "" + _request.timeoutMs.toInt()') ||
      !text.includes('NeraeHttpsBridge.request(payload, timeoutMsText') ||
      !text.includes('NeraeHttpsBridge.cancel(requestId)')) {
    throw new Error('generated wrapper does not contain Android request/cancel timeout bridge calls')
  }
  process.stdout.write('PASS: HBuilderX 5.33 generated integer timeout normalization plus Android request/cancel bridge calls. No APK or Android runtime was verified.\n')
})().catch(error => { process.stderr.write(String(error?.message || 'conversion failed') + '\n'); process.exitCode = 1 })
NODE
succeeded=1
