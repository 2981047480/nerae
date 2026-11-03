#!/usr/bin/env bash
set -euo pipefail
repo_root=$(git -C "$(dirname "${BASH_SOURCE[0]}")" rev-parse --show-toplevel)
work_dir=$(mktemp -d /private/tmp/nerae-p02-13-swift.XXXXXXXX)
succeeded=0
cleanup() {
  if [[ "$succeeded" == 1 && "${NERAE_KEEP_P02_13_IOS:-0}" != 1 ]]; then
    rm -rf -- "$work_dir"
  else
    printf 'iOS synthetic test directory retained: %s\n' "$work_dir" >&2
  fi
}
trap cleanup EXIT
mkdir -p "$work_dir/cache"
xcrun swiftc -module-cache-path "$work_dir/cache" \
  "$repo_root/apps/client/src/uni_modules/nerae-secure-vault/utssdk/app-ios/NeraeStrictJSON.swift" \
  "$repo_root/apps/client/src/uni_modules/nerae-secure-vault/utssdk/app-ios/NeraeSecureVaultBridge.swift" \
  "$repo_root/docs/evidence/P02/P02-13/VaultJournalSyntheticTests.swift" \
  -o "$work_dir/vault-journal-tests"
"$work_dir/vault-journal-tests"
succeeded=1
