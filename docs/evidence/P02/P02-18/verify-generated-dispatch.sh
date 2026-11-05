#!/usr/bin/env bash
# Checks the generated call's Kotlin signature against production native code.
# Stub UTS types are used; this is not DCloud runtime linking, APK, or a device.
set -euo pipefail
repo_root=$(git -C "$(dirname "${BASH_SOURCE[0]}")" rev-parse --show-toplevel)
generated=${1:?pass the generated Android app-android/src/index.kt}
java_home=${NERAE_JAVA_HOME:?set NERAE_JAVA_HOME}
kotlinc=${NERAE_KOTLINC:?set NERAE_KOTLINC}
android_jar=${NERAE_ANDROID_JAR:-"$HOME/Library/Android/sdk/platforms/android-35/android.jar"}
if [[ ! -f "$generated" || ! -x "$java_home/bin/java" || ! -x "$kotlinc" || ! -f "$android_jar" ]]; then
  printf 'Missing generated wrapper, Java, Kotlin or Android API 35.\n' >&2; exit 2
fi
export JAVA_HOME="$java_home"
work_dir=$(mktemp -d /private/tmp/nerae-android-generated-call.XXXXXXXX)
succeeded=0
cleanup() {
  if [[ "$succeeded" == 1 && "${NERAE_KEEP_GENERATED_CALL:-0}" != 1 ]]; then rm -rf -- "$work_dir"
  else printf 'Generated-call probe retained: %s\n' "$work_dir" >&2; fi
}
trap cleanup EXIT
python3 - "$generated" "$work_dir/GeneratedDispatchCall.kt" <<'PY'
from pathlib import Path
import sys

source = Path(sys.argv[1]).read_text(encoding='utf-8')
start_marker = 'fun claimTrainingOperationDispatch(expectedRevision: String, expectedValue: String, operationKey: String, nextValue: String): VersionedSecureVaultResult {'
end_marker = '\nfun trainingDraftStorageCapabilityByJs()'
start = source.find(start_marker)
end = source.find(end_marker, start)
if start < 0 or end < 0:
    raise SystemExit('generated dispatch function not found')
stub = '''package uts.sdk.modules.neraeSecureVault
import android.content.Context
object UTSAndroid { fun getAppContext(): Context? = null }
class VersionedSecureVaultResult
fun validRevision(value: String): Boolean = value.isNotEmpty()
fun versionedFailure(code: String): VersionedSecureVaultResult = VersionedSecureVaultResult()
fun decodeVersioned(raw: String, isRead: Boolean): VersionedSecureVaultResult = VersionedSecureVaultResult()
'''
Path(sys.argv[2]).write_text(stub + source[start:end] + '\n', encoding='utf-8')
PY
source_dir="$repo_root/apps/client/src/uni_modules/nerae-secure-vault/utssdk/app-android"
"$kotlinc" -classpath "$android_jar" -d "$work_dir/classes" \
  "$source_dir/NativeFirstDispatchParser.kt" "$source_dir/NeraeAndroidSecureVaultBridge.kt" \
  "$work_dir/GeneratedDispatchCall.kt"
printf 'PASS: generated dispatch function type-checks against the native bridge with stub UTS types; no DCloud runtime was linked.\n'
succeeded=1
