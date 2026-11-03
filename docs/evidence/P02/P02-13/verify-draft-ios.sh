#!/usr/bin/env bash
set -euo pipefail
repo_root=$(git -C "$(dirname "${BASH_SOURCE[0]}")" rev-parse --show-toplevel)
work_dir=$(mktemp -d /private/tmp/nerae-ios-draft-test.XXXXXXXX)
succeeded=0
cleanup() {
  if [[ "$succeeded" == 1 && "${NERAE_KEEP_DRAFT_IOS:-0}" != 1 ]]; then
    rm -rf -- "$work_dir"
  else
    printf 'iOS draft test directory retained: %s\n' "$work_dir" >&2
  fi
}
trap cleanup EXIT
mkdir -p "$work_dir/cache"
xcrun swiftc -module-cache-path "$work_dir/cache" \
  "$repo_root/apps/client/src/uni_modules/nerae-secure-vault/utssdk/app-ios/NeraeStrictJSON.swift" \
  "$repo_root/apps/client/src/uni_modules/nerae-secure-vault/utssdk/app-ios/NeraeSecureVaultBridge.swift" \
  "$repo_root/docs/evidence/P02/P02-13/VaultDraftSyntheticTests.swift" \
  -o "$work_dir/draft-tests"
"$work_dir/draft-tests"
succeeded=1
