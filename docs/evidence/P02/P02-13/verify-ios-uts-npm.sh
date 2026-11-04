#!/usr/bin/env bash
set -euo pipefail
evidence_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)
repo_root=$(git -C "$evidence_dir" rev-parse --show-toplevel)
node_bin=${NERAE_NODE:-/opt/homebrew/bin/node}
compiler=${NERAE_UTS_COMPILER_FILE:?set NERAE_UTS_COMPILER_FILE to the matching npm uni-uts-v1/dist/swift.js}
if [[ ! -x "$node_bin" || ! -f "$compiler" ]]; then printf 'Required Node or npm UTS compiler is missing.\n' >&2; exit 2; fi
NERAE_COMPILER_FILE="$compiler" NERAE_CLIENT_PACKAGE="$repo_root/apps/client/package.json" "$node_bin" <<'NODE'
const path = require('path')
const compiler = process.env.NERAE_COMPILER_FILE
const wanted = require(process.env.NERAE_CLIENT_PACKAGE).devDependencies['@dcloudio/uni-uts-v1']
const actual = require(path.resolve(path.dirname(compiler), '../package.json')).version
const uts = require(require.resolve('@dcloudio/uts/package.json', { paths: [path.dirname(compiler)] })).version
if (actual !== wanted || uts !== wanted) throw Error('npm UTS compiler versions do not match the client')
NODE
work_dir=$(mktemp -d /private/tmp/nerae-ios-uts-npm.XXXXXXXX)
succeeded=0
cleanup() {
  if [[ "$succeeded" == 1 && "${NERAE_KEEP_IOS_UTS_NPM:-0}" != 1 ]]; then rm -rf -- "$work_dir"
  else printf 'iOS npm UTS conversion directory retained: %s\n' "$work_dir" >&2; fi
}
trap cleanup EXIT
input_dir="$work_dir/input"
output_dir="$work_dir/output"
mkdir -p "$input_dir/uni_modules" "$output_dir" "$work_dir/tmp"
cp -R "$repo_root/apps/client/src/uni_modules/nerae-secure-vault" "$input_dir/uni_modules/"
TMPDIR="$work_dir/tmp" UNI_INPUT_DIR="$input_dir" UNI_OUTPUT_DIR="$output_dir" \
UNI_COMPILER_VERSION=5.33 NODE_ENV=development NERAE_UTS_COMPILER="$compiler" \
NERAE_UTS_INPUT="$input_dir/uni_modules/nerae-secure-vault/utssdk/app-ios/index.uts" \
NERAE_UTS_OUTPUT="$output_dir" "$node_bin" <<'NODE'
const fs = require('fs')
const path = require('path')
const compiler = require(process.env.NERAE_UTS_COMPILER)
;(async () => {
  await compiler.runSwiftProd(process.env.NERAE_UTS_INPUT, {
    components: {}, customElements: {}, uniModuleId: 'nerae-secure-vault', isPlugin: true,
    isModule: false, isX: false, isSingleThread: false, isExtApi: false, hookClass: '',
    extApis: {}, uniModules: [], sourceMap: false,
  })
  const files = []
  const walk = directory => { for (const entry of fs.readdirSync(directory, { withFileTypes: true })) {
    const file = path.join(directory, entry.name)
    if (entry.isDirectory()) walk(file); else if (file.endsWith('.swift')) files.push(file)
  } }
  walk(process.env.NERAE_UTS_OUTPUT)
  const native = path.dirname(process.env.NERAE_UTS_INPUT)
  for (const name of ['NeraeSecureVaultBridge.swift', 'NeraeStrictJSON.swift', 'NeraeRecoveryHTTP.swift']) {
    const copied = files.find(file => file.endsWith(path.join('app-ios', 'src', name)))
    if (!copied || !fs.readFileSync(copied).equals(fs.readFileSync(path.join(native, name)))) {
      throw Error(`native Swift source missing or changed: ${name}`)
    }
  }
  if (!files.some(file => file.endsWith(path.join('app-ios', 'src', 'index.swift')))) throw Error('UTS Swift wrapper missing')
  process.stdout.write('PASS: matching npm UTS compiler generated Swift wrapper and copied native source; no framework/app/device was run.\n')
})().catch(error => { process.stderr.write(String(error?.message || error) + '\n'); process.exitCode = 1 })
NODE
succeeded=1
