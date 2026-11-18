#!/usr/bin/env bash
set -euo pipefail
repo_root=$(git -C "$(dirname "${BASH_SOURCE[0]}")" rev-parse --show-toplevel)
work_dir=$(mktemp -d /private/tmp/nerae-auth-generation.XXXXXXXX)
succeeded=0
cleanup() {
  if [[ "$succeeded" == 1 && "${NERAE_KEEP_AUTH_GENERATION:-0}" != 1 ]]; then
    rm -rf -- "$work_dir"
  else
    printf 'Authentication generation test directory retained: %s\n' "$work_dir" >&2
  fi
}
trap cleanup EXIT
mkdir -p "$work_dir/cache"
source_dir="$repo_root/apps/client/src/uni_modules/nerae-secure-vault/utssdk/app-ios"
xcrun swiftc -module-cache-path "$work_dir/cache" \
  "$source_dir/NeraeStrictJSON.swift" "$source_dir/NeraeRecordingV2JournalValidator.swift" "$source_dir/NeraeSecureVaultBridge.swift" \
  "$source_dir/NeraeAuthenticationStateGeneration.swift" \
  "$repo_root/docs/evidence/P02/P02-17/AuthenticationStateGenerationTests.swift" \
  -o "$work_dir/auth-generation-tests"
"$work_dir/auth-generation-tests"
succeeded=1
