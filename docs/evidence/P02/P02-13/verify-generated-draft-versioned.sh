#!/usr/bin/env bash
# Type-check the actual UTS-generated calls against the Android bridge with stub UTS helpers.
set -euo pipefail
repo_root=$(git -C "$(dirname "${BASH_SOURCE[0]}")" rev-parse --show-toplevel)
generated=${1:?pass generated Android app-android/src/index.kt}
java_home=${NERAE_JAVA_HOME:?set NERAE_JAVA_HOME}
kotlinc=${NERAE_KOTLINC:?set NERAE_KOTLINC}
android_jar=${NERAE_ANDROID_JAR:-"$HOME/Library/Android/sdk/platforms/android-35/android.jar"}
if [[ ! -f "$generated" || ! -x "$java_home/bin/java" || ! -x "$kotlinc" || ! -f "$android_jar" ]]; then
  printf 'Missing generated wrapper, Java, Kotlin or Android API 35.\n' >&2; exit 2
fi
export JAVA_HOME="$java_home"
export PATH="$JAVA_HOME/bin:$PATH"
work_dir=$(mktemp -d /private/tmp/nerae-android-generated-draft.XXXXXXXX)
succeeded=0
cleanup() {
  if [[ "$succeeded" == 1 && "${NERAE_KEEP_GENERATED_DRAFT:-0}" != 1 ]]; then rm -rf -- "$work_dir"
  else printf 'Generated draft probe retained: %s\n' "$work_dir" >&2; fi
}
trap cleanup EXIT
python3 - "$generated" "$work_dir/GeneratedDraftCalls.kt" <<'PY'
from pathlib import Path
import sys
source = Path(sys.argv[1]).read_text(encoding='utf-8')
start = source.find('fun readVersionedTrainingDraft(key: String): VersionedSecureVaultResult {')
end = source.find('\nfun decodeVersioned(raw: String, isRead: Boolean)', start)
if start < 0 or end < 0:
    raise SystemExit('generated draft functions not found')
stub = '''package uts.sdk.modules.neraeSecureVault
import android.content.Context
object UTSAndroid { fun getAppContext(): Context? = null }
class VersionedSecureVaultResult
fun validRevision(value: String): Boolean = value.isNotEmpty()
fun versionedFailure(code: String): VersionedSecureVaultResult = VersionedSecureVaultResult()
fun decodeVersionedDraft(raw: String, isRead: Boolean): VersionedSecureVaultResult = VersionedSecureVaultResult()
'''
Path(sys.argv[2]).write_text(stub + source[start:end] + '\n', encoding='utf-8')
PY
src="$repo_root/apps/client/src/uni_modules/nerae-secure-vault/utssdk/app-android"
"$kotlinc" -classpath "$android_jar" -d "$work_dir/classes" \
  "$src/NeraeAndroidSecureVaultBridge.kt" "$src/NativeFirstDispatchParser.kt" \
  "$src/NeraeAndroidTrainingDraftStore.kt" "$work_dir/GeneratedDraftCalls.kt"
printf 'PASS: generated versioned draft calls type-check against native bridge with stub UTS helpers; no DCloud runtime or device was run.\n'
succeeded=1
