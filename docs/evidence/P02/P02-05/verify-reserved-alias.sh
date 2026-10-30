#!/usr/bin/env bash
set -euo pipefail

evidence_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)
repo_root=$(git -C "$evidence_dir" rev-parse --show-toplevel)
plugin="$repo_root/apps/client/src/uni_modules/nerae-device-key/utssdk"
infra="$repo_root/infra/app/ios/device"
swift_bin="${TMPDIR:-/tmp}/nerae-reserved-alias-swift-$$"
scratch=""
succeeded=0
cleanup() {
	rm -f "$swift_bin"
	if [[ -n "$scratch" ]]; then
		if [[ "$succeeded" == 1 ]]; then rm -rf -- "$scratch"; else echo "Retained: $scratch" >&2; fi
	fi
}
trap cleanup EXIT

cmp -s "$plugin/app-ios/DeviceKey.swift" "$infra/DeviceKey.swift"
echo "PASS: plugin and infrastructure DeviceKey.swift sources match"

swiftc -parse-as-library -o "$swift_bin" \
	"$plugin/app-ios/SignatureEncoding.swift" "$plugin/app-ios/DeviceProof.swift" \
	"$plugin/app-ios/DeviceKey.swift" "$evidence_dir/ReservedAliasSmoke.swift"
"$swift_bin"

java_home=${NERAE_JAVA_HOME:-/private/tmp/nerae-hbuilder-download/HBuilderX.app/Contents/HBuilderX/plugins/amazon-corretto}
kotlinc=${NERAE_KOTLINC:-/private/tmp/nerae-kotlin-1.9.24/kotlinc/bin/kotlinc}
android_jar=${NERAE_ANDROID_JAR:-"$HOME/Library/Android/sdk/platforms/android-35/android.jar"}
hbuilder_app=${NERAE_HBUILDERX_APP:-/private/tmp/nerae-hbuilder-download/HBuilderX.app}
node_bin=${NERAE_NODE:-/opt/homebrew/bin/node}
kotlin_compiler="$hbuilder_app/Contents/HBuilderX/plugins/uniapp-uts-v1/node_modules/@dcloudio/uni-uts-v1/dist/kotlin.js"
for required in "$java_home/bin/java" "$kotlinc" "$android_jar" "$node_bin" "$kotlin_compiler"; do
	if [[ ! -e "$required" ]]; then
		echo "Missing Android compile dependency: $required" >&2
		exit 2
	fi
done

scratch=$(mktemp -d /private/tmp/nerae-p02-05-reserved-alias.XXXXXXXX)
mkdir -p "$scratch/classes"
export JAVA_HOME="$java_home"

input_dir="$scratch/input"
output_dir="$scratch/output"
mkdir -p "$input_dir/uni_modules" "$output_dir" "$scratch/tmp"
cp -R "$repo_root/apps/client/src/uni_modules/nerae-device-key" "$input_dir/uni_modules/"
TMPDIR="$scratch/tmp" UNI_INPUT_DIR="$input_dir" UNI_OUTPUT_DIR="$output_dir" \
	UNI_HBUILDERX_PLUGINS="$hbuilder_app/Contents/HBuilderX/plugins" \
	NERAE_UTS_COMPILER="$kotlin_compiler" "$node_bin" <<'NODE'
const fs = require('fs')
const path = require('path')
const compiler = require(process.env.NERAE_UTS_COMPILER)
const filename = path.join(process.env.UNI_INPUT_DIR, 'uni_modules/nerae-device-key/utssdk/app-android/index.uts')
;(async () => {
  try {
    const result = await compiler.runKotlinProd(filename, {
      components: {}, customElements: {}, uniModuleId: 'nerae-device-key',
      isPlugin: true, isModule: false, isX: false, isSingleThread: false,
      isExtApi: false, hookClass: '', extApis: {}, uniModules: [], noEmit: false,
      sourceMap: false,
    })
    if (!result || result.error) throw new Error('Kotlin UTS compiler returned no successful result')
    const generated = []
    const visit = dir => {
      for (const entry of fs.readdirSync(dir, { withFileTypes: true })) {
        const next = path.join(dir, entry.name)
        if (entry.isDirectory()) visit(next)
        else if (entry.isFile() && entry.name.endsWith('.kt')) generated.push(next)
      }
    }
    visit(process.env.UNI_OUTPUT_DIR)
    const wrapper = generated.find(file => file.endsWith(path.join('app-android', 'src', 'index.kt')))
    if (!wrapper || !fs.readFileSync(wrapper, 'utf8').includes('createDeviceKeyAtReservedAlias')) {
      throw new Error('generated Android UTS wrapper omitted reserved alias operation')
    }
    process.stdout.write(`PASS: HBuilderX Android UTS conversion generated ${generated.length} Kotlin files with reserved alias operation\n`)
  } catch (error) {
    process.stderr.write(String(error?.message || error) + '\n')
    process.exitCode = 1
  }
})()
NODE

"$kotlinc" -classpath "$android_jar" -d "$scratch/classes" \
	"$plugin/app-android/NeraeAndroidDeviceKeyBridge.kt"
"$JAVA_HOME/bin/javac" -classpath "$scratch/classes:$(dirname "$kotlinc")/../lib/kotlin-stdlib.jar:$android_jar" \
	-d "$scratch/classes" "$evidence_dir/ReservedAliasAndroidSmoke.java"
"$JAVA_HOME/bin/java" -cp "$scratch/classes:$(dirname "$kotlinc")/../lib/kotlin-stdlib.jar:$android_jar" \
	ReservedAliasAndroidSmoke

if ! rg -q 'createDeviceKeyAtReservedAlias' "$plugin/interface.uts" "$plugin/app-ios/index.uts" "$plugin/app-android/index.uts"; then
	echo "Reserved alias operation missing from UTS surface" >&2
	exit 1
fi
echo "PASS: reserved alias UTS entry exists on iOS and Android"
succeeded=1
