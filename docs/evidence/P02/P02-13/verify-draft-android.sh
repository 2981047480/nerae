#!/usr/bin/env bash
set -euo pipefail
evidence_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)
repo_root=$(git -C "$evidence_dir" rev-parse --show-toplevel)
java_home=${NERAE_JAVA_HOME:?set NERAE_JAVA_HOME to the SHA-256-verified Corretto 17 runtime}
kotlinc=${NERAE_KOTLINC:?set NERAE_KOTLINC to the SHA-256-verified Kotlin 1.9.24 compiler}
android_jar=${NERAE_ANDROID_JAR:-"$HOME/Library/Android/sdk/platforms/android-35/android.jar"}
if [[ ! -x "$java_home/bin/java" || ! -x "$kotlinc" || ! -f "$android_jar" ]]; then
  printf 'Required verified JDK, Kotlin compiler or Android API jar is missing.\n' >&2
  exit 2
fi
export JAVA_HOME="$java_home"
export PATH="$JAVA_HOME/bin:$PATH"
if ! "$kotlinc" -version 2>&1 | rg -q '1\.9\.24'; then
  printf 'Expected Kotlin 1.9.24.\n' >&2
  exit 2
fi
work_dir=$(mktemp -d /private/tmp/nerae-android-draft-test.XXXXXXXX)
succeeded=0
cleanup() {
  if [[ "$succeeded" == 1 && "${NERAE_KEEP_DRAFT_ANDROID:-0}" != 1 ]]; then
    rm -rf -- "$work_dir"
  else
    printf 'Android draft test directory retained: %s\n' "$work_dir" >&2
  fi
}
trap cleanup EXIT
mkdir -p "$work_dir/classes"
"$kotlinc" -classpath "$android_jar" -d "$work_dir/classes" \
  "$repo_root/apps/client/src/uni_modules/nerae-secure-vault/utssdk/app-android/NeraeAndroidSecureVaultBridge.kt" \
  "$repo_root/apps/client/src/uni_modules/nerae-secure-vault/utssdk/app-android/NeraeAndroidTrainingDraftStore.kt" \
  "$evidence_dir/TrainingDraftAndroidSyntheticTests.kt"
"$java_home/bin/java" -cp "$work_dir/classes:$(dirname "$kotlinc")/../lib/kotlin-stdlib.jar:$android_jar" \
  uts.sdk.modules.neraeSecureVault.TrainingDraftAndroidSyntheticTestsKt
succeeded=1
