#!/usr/bin/env bash
set -euo pipefail
repo_root=$(git -C "$(dirname "${BASH_SOURCE[0]}")" rev-parse --show-toplevel)
java_home=${NERAE_JAVA_HOME:?set NERAE_JAVA_HOME}
kotlinc=${NERAE_KOTLINC:?set NERAE_KOTLINC}
android_jar=${NERAE_ANDROID_JAR:-"$HOME/Library/Android/sdk/platforms/android-35/android.jar"}
if [[ ! -x "$java_home/bin/java" || ! -x "$kotlinc" || ! -f "$android_jar" ]]; then
  printf 'Required Java, Kotlin or Android API 35 is missing.\n' >&2; exit 2
fi
export JAVA_HOME="$java_home"
export PATH="$JAVA_HOME/bin:$PATH"
if ! "$kotlinc" -version 2>&1 | rg -q '1\.9\.24'; then
  printf 'Expected Kotlin 1.9.24.\n' >&2; exit 2
fi
work_dir=$(mktemp -d /private/tmp/nerae-android-draft-revision-test.XXXXXXXX)
succeeded=0
cleanup() {
  if [[ "$succeeded" == 1 && "${NERAE_KEEP_DRAFT_VERSIONED:-0}" != 1 ]]; then rm -rf -- "$work_dir"
  else printf 'Android draft revision test directory retained: %s\n' "$work_dir" >&2; fi
}
trap cleanup EXIT
src="$repo_root/apps/client/src/uni_modules/nerae-secure-vault/utssdk/app-android"
"$kotlinc" -classpath "$android_jar" -d "$work_dir/classes" \
  "$src/NeraeAndroidSecureVaultBridge.kt" "$src/NativeFirstDispatchParser.kt" \
  "$src/NeraeAndroidTrainingDraftStore.kt" "$repo_root/docs/evidence/P02/P02-13/TrainingDraftAndroidVersionedTests.kt"
"$java_home/bin/java" -cp "$work_dir/classes:$(dirname "$kotlinc")/../lib/kotlin-stdlib.jar:$android_jar" \
  uts.sdk.modules.neraeSecureVault.TrainingDraftAndroidVersionedTestsKt
succeeded=1
