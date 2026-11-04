#!/usr/bin/env bash
set -euo pipefail
evidence_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)
repo_root=$(git -C "$evidence_dir" rev-parse --show-toplevel)
java_home=${NERAE_JAVA_HOME:-/private/tmp/nerae-hbuilder-download/HBuilderX.app/Contents/HBuilderX/plugins/amazon-corretto}
kotlinc=${NERAE_KOTLINC:-/private/tmp/nerae-kotlin-1.9.24/kotlinc/bin/kotlinc}
android_jar=${NERAE_ANDROID_JAR:-"$HOME/Library/Android/sdk/platforms/android-35/android.jar"}
source_file="$repo_root/apps/client/src/uni_modules/nerae-secure-vault/utssdk/app-android/NeraeAndroidSecureVaultBridge.kt"
test_file="$evidence_dir/VaultJournalVersionedSyntheticTests.kt"
if [[ ! -x "$java_home/bin/java" || ! -x "$kotlinc" || ! -f "$android_jar" ]]; then
  printf 'Required local Corretto, Kotlin compiler or Android API 35 is missing.\n' >&2
  exit 2
fi
export JAVA_HOME="$java_home"
export PATH="$JAVA_HOME/bin:$PATH"
if ! "$kotlinc" -version 2>&1 | rg -q '1\.9\.24'; then
  printf 'Expected Kotlin 1.9.24.\n' >&2
  exit 2
fi
work_dir=$(mktemp -d /private/tmp/nerae-p02-14-kotlin.XXXXXXXX)
succeeded=0
cleanup() {
  if [[ "$succeeded" == 1 && "${NERAE_KEEP_P02_14_ANDROID:-0}" != 1 ]]; then
    rm -rf -- "$work_dir"
  else
    printf 'Android synthetic test directory retained: %s\n' "$work_dir" >&2
  fi
}
trap cleanup EXIT
mkdir -p "$work_dir/classes"
"$kotlinc" -classpath "$android_jar" -d "$work_dir/classes" "$source_file" \
  "$repo_root/apps/client/src/uni_modules/nerae-secure-vault/utssdk/app-android/NativeFirstDispatchParser.kt" "$test_file"
"$JAVA_HOME/bin/java" -cp "$work_dir/classes:$(dirname "$kotlinc")/../lib/kotlin-stdlib.jar:$android_jar" \
  uts.sdk.modules.neraeSecureVault.VaultJournalVersionedSyntheticTestsKt
python3 - "$source_file" <<'PY'
import sys

source = open(sys.argv[1], encoding="utf-8").read()
legacy_read = source.split("fun readTrainingOperationJournal(", 1)[1].split("@JvmStatic", 1)[0]
legacy_cas = source.split("fun compareAndSetTrainingOperationJournal(", 1)[1].split("@JvmStatic", 1)[0]
versioned_read = source.split("fun readVersionedTrainingOperationJournal(", 1)[1].split("@JvmStatic", 1)[0]
versioned_cas = source.split("fun compareAndSetVersionedTrainingOperationJournal(", 1)[1].split("private fun withVault", 1)[0]
legacy_encoder = source.split("private fun encode(ok: Boolean", 1)[1].split("\n}", 1)[0]
versioned_encoder = source.split("private fun encodeJournal(", 1)[1].split("private fun encode(ok: Boolean", 1)[0]
journal_failure = source.split("private fun journalFailure(", 1)[1].split("private fun encodeJournal(", 1)[0]
assert "encode(ok = true, found = result.found, value = result.value)" in legacy_read
assert "encodeJournal" not in legacy_read
assert "versionedResult = false" in legacy_read
assert "encode(ok = true)" in legacy_cas
assert "encodeJournal" not in legacy_cas
assert "versionedResult = false" in legacy_cas
assert "encodeJournal(ok = true, found = result.found, value = result.value, revision = result.revision.toString())" in versioned_read
assert "encodeJournal(ok = true, revision = revision.toString())" in versioned_cas
assert all(field in legacy_encoder for field in ['.put("ok"', '.put("errorCode"', '.put("found"', '.put("value"'])
assert '.put("revision"' not in legacy_encoder
assert all(field in versioned_encoder for field in ['.put("ok"', '.put("errorCode"', '.put("found"', '.put("value"', '.put("revision"'])
assert all(field in journal_failure for field in ['.put("ok", false)', '.put("found", false)', '.put("value", "")', '.put("revision", "")'])
assert 'else if (problem.code == "conflict") conflict() else failure(problem.code)' in source
assert 'if (value.size > maxEnvelopeBytes()) throw VaultFailure()' in source
assert 'NeraeVersionedTrainingJournal.MAX_ENVELOPE_BYTES' in source
print("PASS: legacy journal API remains four fields; versioned API includes decimal revision")
PY
printf 'Synthetic JVM keys are software test keys; production remains Android Keystore only.\n'
succeeded=1
