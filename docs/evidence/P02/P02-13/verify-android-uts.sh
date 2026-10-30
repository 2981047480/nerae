#!/usr/bin/env bash
set -euo pipefail
evidence_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)
repo_root=$(git -C "$evidence_dir" rev-parse --show-toplevel)
hbuilder_app=${NERAE_HBUILDERX_APP:-/private/tmp/nerae-hbuilder-download/HBuilderX.app}
node_bin=${NERAE_NODE:-/opt/homebrew/bin/node}
plugins_dir="$hbuilder_app/Contents/HBuilderX/plugins"
compiler="$plugins_dir/uniapp-uts-v1/node_modules/@dcloudio/uni-uts-v1/dist/kotlin.js"
if [[ ! -x "$node_bin" || ! -f "$compiler" || ! -f "$hbuilder_app/Contents/Info.plist" ]]; then
  printf 'Required local HBuilderX Kotlin UTS compiler or Node executable is missing.\n' >&2
  exit 2
fi
version=$(/usr/libexec/PlistBuddy -c 'Print CFBundleShortVersionString' "$hbuilder_app/Contents/Info.plist")
if [[ "$version" != 5.33.* ]]; then printf 'Expected HBuilderX 5.33; got %s.\n' "$version" >&2; exit 2; fi
work_dir=$(mktemp -d /private/tmp/nerae-p02-13-android-uts.XXXXXXXX)
succeeded=0
cleanup() {
  if [[ "$succeeded" == 1 && "${NERAE_KEEP_P02_13_UTS:-0}" != 1 ]]; then rm -rf -- "$work_dir"
  else printf 'Android UTS conversion directory retained: %s\n' "$work_dir" >&2; fi
}
trap cleanup EXIT
input_dir="$work_dir/input"; output_dir="$work_dir/output"
mkdir -p "$input_dir/uni_modules" "$output_dir" "$work_dir/tmp"
cp -R "$repo_root/apps/client/src/uni_modules/nerae-secure-vault" "$input_dir/uni_modules/"
TMPDIR="$work_dir/tmp" UNI_INPUT_DIR="$input_dir" UNI_OUTPUT_DIR="$output_dir" \
UNI_HBUILDERX_PLUGINS="$plugins_dir" HX_Version=5.33 UNI_COMPILER_VERSION=5.33 \
NODE_ENV=development NERAE_UTS_COMPILER="$compiler" \
NERAE_UTS_INPUT="$input_dir/uni_modules/nerae-secure-vault/utssdk/app-android/index.uts" \
NERAE_UTS_OUTPUT="$output_dir" "$node_bin" <<'NODE'
const path = require('path')
const fs = require('fs')
const kotlin = require(process.env.NERAE_UTS_COMPILER)
;(async () => {
  try {
    const result = await kotlin.runKotlinProd(process.env.NERAE_UTS_INPUT, {
      components: {}, customElements: {}, uniModuleId: 'nerae-secure-vault',
      isPlugin: true, isModule: false, isX: false, isSingleThread: false,
      isExtApi: false, hookClass: '', extApis: {}, uniModules: [], noEmit: false, sourceMap: false,
    })
    if (!result || result.error) throw new Error('HBuilderX returned no successful Kotlin conversion result')
    const generated = []
    const visit = dir => { for (const entry of fs.readdirSync(dir, { withFileTypes: true })) {
      const next = path.join(dir, entry.name)
      if (entry.isDirectory()) visit(next)
      else if (entry.isFile() && entry.name.endsWith('.kt')) generated.push(next)
    } }
    visit(process.env.NERAE_UTS_OUTPUT)
    const wrapper = generated.find(file => file.endsWith(path.join('app-android', 'src', 'index.kt')))
    if (!wrapper) throw new Error('UTS conversion omitted generated Kotlin wrapper')
    const source = fs.readFileSync(wrapper, 'utf8')
    for (const operation of ['readTrainingOperationJournal', 'compareAndSetTrainingOperationJournal']) {
      if (!source.includes(`NeraeAndroidSecureVaultBridge.${operation}`)) throw new Error(`generated wrapper omitted ${operation}`)
    }
    process.stdout.write(`PASS: HBuilderX ${process.env.HX_Version} generated ${generated.length} Kotlin file(s); no dex/APK/Keystore was run.\n`)
  } catch (error) { process.stderr.write(String(error?.message || error) + '\n'); process.exitCode = 1 }
})()
NODE
succeeded=1
