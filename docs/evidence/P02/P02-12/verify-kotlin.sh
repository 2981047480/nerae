#!/usr/bin/env bash
set -euo pipefail

evidence_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)
repo_root=$(git -C "$evidence_dir" rev-parse --show-toplevel)
java_home=${NERAE_JAVA_HOME:-/private/tmp/nerae-hbuilder-download/HBuilderX.app/Contents/HBuilderX/plugins/amazon-corretto}
kotlinc=${NERAE_KOTLINC:-/private/tmp/nerae-kotlin-1.9.24/kotlinc/bin/kotlinc}
android_jar=${NERAE_ANDROID_JAR:-"$HOME/Library/Android/sdk/platforms/android-35/android.jar"}
source_file="$repo_root/apps/client/src/uni_modules/nerae-secure-vault/utssdk/app-android/NeraeAndroidSecureVaultBridge.kt"
test_file="$evidence_dir/RegistrySyntheticSmoke.kt"

if [[ ! -x "$java_home/bin/java" || ! -x "$java_home/bin/javac" || ! -x "$kotlinc" ||
      ! -f "$android_jar" || ! -f "$source_file" || ! -f "$test_file" ]]; then
	printf 'Required local Corretto, Kotlin compiler, Android API 35, or source file is missing.\n' >&2
	exit 2
fi
export JAVA_HOME="$java_home"
export PATH="$JAVA_HOME/bin:$PATH"
if ! "$kotlinc" -version 2>&1 | rg -q '1\.9\.24'; then
	printf 'Expected Kotlin 1.9.24.\n' >&2
	exit 2
fi

work_dir=$(mktemp -d /private/tmp/nerae-p02-08-vault.XXXXXXXX)
succeeded=0
cleanup() {
	if [[ "$succeeded" == 1 && "${NERAE_KEEP_P02_08_VAULT:-0}" != 1 ]]; then
		rm -rf -- "$work_dir"
	else
		printf 'Android vault test directory retained: %s\n' "$work_dir" >&2
	fi
}
trap cleanup EXIT
mkdir -p "$work_dir/classes"
printf 'JDK: '
"$JAVA_HOME/bin/java" -version 2>&1 | head -n 1
printf 'Kotlin: '
"$kotlinc" -version 2>&1 | head -n 1
"$kotlinc" -classpath "$android_jar" -d "$work_dir/classes" "$source_file" \
  "$repo_root/apps/client/src/uni_modules/nerae-secure-vault/utssdk/app-android/NativeFirstDispatchParser.kt" "$test_file"
	"$JAVA_HOME/bin/java" -cp "$work_dir/classes:$(dirname "$kotlinc")/../lib/kotlin-stdlib.jar:$android_jar" \
	uts.sdk.modules.neraeSecureVault.RegistrySyntheticSmokeKt
printf 'PASS: production Kotlin source compiled against Android API 35; synthetic crypto tests used software AES only.\n'
succeeded=1
