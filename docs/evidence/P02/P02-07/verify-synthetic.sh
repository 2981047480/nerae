#!/usr/bin/env bash
# Synthetic backend only; this does not access an actual Keychain item.
set -euo pipefail
repo_root=$(git -C "$(dirname "${BASH_SOURCE[0]}")" rev-parse --show-toplevel)
work_dir=$(mktemp -d /private/tmp/nerae-p02-07-swift.XXXXXXXX)
succeeded=0
cleanup() {
  if [[ "$succeeded" == 1 ]]; then
    rm -rf -- "$work_dir"
  else
    printf 'Synthetic test directory retained: %s\n' "$work_dir" >&2
  fi
}
trap cleanup EXIT
mkdir -p "$work_dir/cache"
xcrun swiftc -module-cache-path "$work_dir/cache" \
  "$repo_root/apps/client/src/uni_modules/nerae-secure-vault/utssdk/app-ios/NeraeSecureVaultBridge.swift" \
  "$repo_root/docs/evidence/P02/P02-07/VaultSyntheticTests.swift" -o "$work_dir/vault-synthetic-tests"
"$work_dir/vault-synthetic-tests"
succeeded=1
