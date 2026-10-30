#!/usr/bin/env bash
set -euo pipefail

evidence_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)
repo_root=$(git -C "$evidence_dir" rev-parse --show-toplevel)

java_home=${NERAE_JAVA_HOME:-${JAVA_HOME:-}}
kotlinc=${NERAE_KOTLINC:-$(command -v kotlinc || true)}
android_sdk=${ANDROID_SDK_ROOT:-${ANDROID_HOME:-"$HOME/Library/Android/sdk"}}
android_jar=${NERAE_ANDROID_JAR:-"$android_sdk/platforms/android-35/android.jar"}
source_file="$repo_root/apps/client/src/uni_modules/nerae-device-key/utssdk/app-android/NeraeAndroidDeviceKeyBridge.kt"
test_source="$evidence_dir/DerSmoke.java"

if [[ -z "$java_home" || ! -x "$java_home/bin/java" || ! -x "$java_home/bin/javac" ]]; then
	printf 'Set NERAE_JAVA_HOME or JAVA_HOME to a JDK containing java and javac.\n' >&2
	exit 2
fi
if [[ -z "$kotlinc" || ! -x "$kotlinc" ]]; then
	printf 'Set NERAE_KOTLINC or put kotlinc on PATH.\n' >&2
	exit 2
fi
if [[ ! -f "$android_jar" || ! -f "$source_file" || ! -f "$test_source" ]]; then
	printf 'Missing Android android.jar, production Kotlin source, or DerSmoke.java.\n' >&2
	exit 2
fi

kotlin_home=$(cd "$(dirname "$kotlinc")/.." && pwd -P)
kotlin_stdlib=${NERAE_KOTLIN_STDLIB:-"$kotlin_home/lib/kotlin-stdlib.jar"}
if [[ ! -f "$kotlin_stdlib" ]]; then
	printf 'Kotlin stdlib not found at %s; set NERAE_KOTLIN_STDLIB.\n' "$kotlin_stdlib" >&2
	exit 2
fi

work_dir=$(mktemp -d /private/tmp/nerae-p02-06-der.XXXXXXXX)
succeeded=0
cleanup() {
	if [[ "$succeeded" == 1 && "${NERAE_KEEP_DER_SMOKE:-0}" != 1 ]]; then
		rm -rf -- "$work_dir"
	else
		printf 'DER smoke directory retained: %s\n' "$work_dir" >&2
	fi
}
trap cleanup EXIT
mkdir -p "$work_dir/kotlin-classes" "$work_dir/java-classes"

export JAVA_HOME="$java_home"
printf 'JDK: '
"$JAVA_HOME/bin/java" -version 2>&1 | head -n 1
printf 'Kotlin compiler: '
"$kotlinc" -version 2>&1 | head -n 1

"$kotlinc" -classpath "$android_jar" -d "$work_dir/kotlin-classes" "$source_file"
"$JAVA_HOME/bin/javac" -classpath "$work_dir/kotlin-classes:$kotlin_stdlib:$android_jar" \
	-d "$work_dir/java-classes" "$test_source"
"$JAVA_HOME/bin/java" -classpath "$work_dir/kotlin-classes:$work_dir/java-classes:$kotlin_stdlib:$android_jar" DerSmoke

printf 'PASS: production Android Kotlin source compiled; synthetic DER tests passed. No Android device or Keystore was used.\n'
succeeded=1
