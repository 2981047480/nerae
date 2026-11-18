#!/usr/bin/env bash
# Synthetic backend only; this does not access the real Keychain.
set -euo pipefail
evidence_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)
repo_root=$(git -C "$evidence_dir" rev-parse --show-toplevel)
work_dir=$(mktemp -d /private/tmp/nerae-p02-12-swift.XXXXXXXX)
succeeded=0
cleanup() {
  if [[ "$succeeded" == 1 && "${NERAE_KEEP_P02_12_SWIFT:-0}" != 1 ]]; then rm -rf -- "$work_dir"
  else printf 'Synthetic test directory retained: %s\n' "$work_dir" >&2; fi
}
trap cleanup EXIT
mkdir -p "$work_dir/cache"
xcrun swiftc -module-cache-path "$work_dir/cache" \
  "$repo_root/apps/client/src/uni_modules/nerae-secure-vault/utssdk/app-ios/NeraeStrictJSON.swift" \
  "$repo_root/apps/client/src/uni_modules/nerae-secure-vault/utssdk/app-ios/NeraeAuthenticationStateGeneration.swift" \
  "$repo_root/apps/client/src/uni_modules/nerae-secure-vault/utssdk/app-ios/NeraeRecordingV2JournalValidator.swift" "$repo_root/apps/client/src/uni_modules/nerae-secure-vault/utssdk/app-ios/NeraeSecureVaultBridge.swift" \
  "$evidence_dir/RegistrySyntheticTests.swift" -o "$work_dir/registry-synthetic-tests"
"$work_dir/registry-synthetic-tests"
succeeded=1
