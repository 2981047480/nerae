#!/usr/bin/env bash
set -euo pipefail
repo_root=$(git -C "$(dirname "${BASH_SOURCE[0]}")" rev-parse --show-toplevel)
java_home=${NERAE_JAVA_HOME:?set NERAE_JAVA_HOME to the verified Corretto 17 runtime}
kotlinc=${NERAE_KOTLINC:?set NERAE_KOTLINC to the verified Kotlin 1.9.24 compiler}
android_jar=${NERAE_ANDROID_JAR:-"$HOME/Library/Android/sdk/platforms/android-35/android.jar"}
if [[ ! -x "$java_home/bin/java" || ! -x "$kotlinc" || ! -f "$android_jar" ]]; then
  printf 'Required Java, Kotlin or Android API 35 toolchain is missing.\n' >&2; exit 2
fi
export JAVA_HOME="$java_home"
work_dir=$(mktemp -d /private/tmp/nerae-android-dispatch.XXXXXXXX)
succeeded=0
cleanup() {
  if [[ "$succeeded" == 1 && "${NERAE_KEEP_ANDROID_DISPATCH:-0}" != 1 ]]; then
    rm -rf -- "$work_dir"
  else
    printf 'Android dispatch test directory retained: %s\n' "$work_dir" >&2
  fi
}
trap cleanup EXIT
mkdir -p "$work_dir/classes"
source_dir="$repo_root/apps/client/src/uni_modules/nerae-secure-vault/utssdk/app-android"
"$kotlinc" -classpath "$android_jar" -d "$work_dir/classes" \
  "$source_dir/NativeFirstDispatchParser.kt" "$source_dir/NeraeAndroidSecureVaultBridge.kt" \
  "$repo_root/docs/evidence/P02/P02-18/FirstDispatchGateTest.kt" \
  "$repo_root/docs/evidence/P02/P02-18/NeraeVersionedTrainingJournalDispatchTest.kt"
classpath="$work_dir/classes:$(dirname "$kotlinc")/../lib/kotlin-stdlib.jar:$android_jar"
"$java_home/bin/java" -cp "$classpath" uts.sdk.modules.neraeSecureVault.FirstDispatchGateTestKt
"$java_home/bin/java" -cp "$classpath" uts.sdk.modules.neraeSecureVault.NeraeVersionedTrainingJournalDispatchTestKt
succeeded=1
