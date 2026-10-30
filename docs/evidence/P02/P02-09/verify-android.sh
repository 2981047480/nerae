#!/usr/bin/env bash
set -euo pipefail

evidence_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)
repo_root=$(git -C "$evidence_dir" rev-parse --show-toplevel)
hbuilder_app=${NERAE_HBUILDERX_APP:-/private/tmp/nerae-hbuilder-download/HBuilderX.app}
node_bin=${NERAE_NODE:-/opt/homebrew/bin/node}
java_home=${NERAE_JAVA_HOME:-/private/tmp/nerae-hbuilder-download/HBuilderX.app/Contents/HBuilderX/plugins/amazon-corretto}
kotlinc=${NERAE_KOTLINC:-/private/tmp/nerae-kotlin-1.9.24/kotlinc/bin/kotlinc}
android_jar=${NERAE_ANDROID_JAR:-"$HOME/Library/Android/sdk/platforms/android-35/android.jar"}
plugins_dir="$hbuilder_app/Contents/HBuilderX/plugins"
kotlin_compiler="$plugins_dir/uniapp-uts-v1/node_modules/@dcloudio/uni-uts-v1/dist/kotlin.js"
module_source="$repo_root/apps/client/src/uni_modules/nerae-random"
native_source="$module_source/utssdk/app-android/NeraeAndroidSecureRandomBridge.kt"
test_source="$evidence_dir/android-random-smoke.kt"

if [[ ! -x "$node_bin" || ! -f "$kotlin_compiler" || ! -f "$hbuilder_app/Contents/Info.plist" ||
      ! -x "$java_home/bin/java" || ! -x "$kotlinc" || ! -f "$android_jar" ||
      ! -f "$native_source" || ! -f "$test_source" ]]; then
	printf 'Required local HBuilderX, Corretto, Kotlin, Android API 35, or source file is missing.\n' >&2
	exit 2
fi
version=$(/usr/libexec/PlistBuddy -c 'Print CFBundleShortVersionString' "$hbuilder_app/Contents/Info.plist")
if [[ "$version" != 5.33.* ]]; then
	printf 'Expected HBuilderX 5.33, got %s.\n' "$version" >&2
	exit 2
fi
export JAVA_HOME="$java_home"
export PATH="$JAVA_HOME/bin:$PATH"
if ! "$kotlinc" -version 2>&1 | rg -q '1\.9\.24'; then
	printf 'Expected Kotlin 1.9.24.\n' >&2
	exit 2
fi

work_dir=$(mktemp -d /private/tmp/nerae-p02-09-android.XXXXXXXX)
succeeded=0
cleanup() {
	if [[ "$succeeded" == 1 && "${NERAE_KEEP_P02_09_ANDROID:-0}" != 1 ]]; then
		rm -rf -- "$work_dir"
	else
		printf 'Android random-key test directory retained: %s\n' "$work_dir" >&2
	fi
}
trap cleanup EXIT
input_dir="$work_dir/input"
output_dir="$work_dir/output"
mkdir -p "$input_dir/uni_modules" "$output_dir" "$work_dir/tmp" "$work_dir/classes"
cp -R "$module_source" "$input_dir/uni_modules/"

TMPDIR="$work_dir/tmp" UNI_INPUT_DIR="$input_dir" UNI_OUTPUT_DIR="$output_dir" \
UNI_HBUILDERX_PLUGINS="$plugins_dir" HX_Version=5.33 UNI_COMPILER_VERSION=5.33 \
NODE_ENV=development NERAE_UTS_COMPILER="$kotlin_compiler" \
"$node_bin" <<'NODE'
const kotlin = require(process.env.NERAE_UTS_COMPILER)
const filename = process.env.UNI_INPUT_DIR + '/uni_modules/nerae-random/utssdk/app-android/index.uts'
;(async () => {
  try {
    const result = await kotlin.runKotlinProd(filename, {
      components: {}, customElements: {}, uniModuleId: 'nerae-random',
      isPlugin: true, isModule: false, isX: false, isSingleThread: false,
      isExtApi: false, hookClass: '', extApis: {}, uniModules: [], noEmit: false,
      sourceMap: false,
    })
    if (!result || result.error) throw new Error('no successful Kotlin conversion result')
    const fs = require('fs')
    const path = require('path')
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
    if (!wrapper || !fs.readFileSync(wrapper, 'utf8').includes('NeraeAndroidSecureRandomBridge.generateRequestKey')) {
      throw new Error('generated UTS wrapper omitted the native random bridge call')
    }
    process.stdout.write(`PASS: HBuilderX ${process.env.HX_Version} generated ${generated.length} Kotlin source file(s).\n`)
  } catch (_error) {
    process.stderr.write('HBuilderX Android UTS conversion failed.\n')
    process.exitCode = 1
  }
})()
NODE

printf 'JDK: '
"$JAVA_HOME/bin/java" -version 2>&1 | head -n 1
printf 'Kotlin: '
"$kotlinc" -version 2>&1 | head -n 1
"$kotlinc" -classpath "$android_jar" -d "$work_dir/classes" "$native_source" "$test_source"
"$JAVA_HOME/bin/java" -cp "$work_dir/classes:$(dirname "$kotlinc")/../lib/kotlin-stdlib.jar:$android_jar" \
	uts.sdk.modules.neraeRandom.Android_random_smokeKt
printf 'PASS: Kotlin source compiled against Android API 35; JVM random provider is not Android Keystore/device evidence.\n'
succeeded=1
