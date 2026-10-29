#!/bin/bash
set -euo pipefail
script_dir="$(cd "$(dirname "$0")" && pwd -P)"
repo_root="$(git -C "$script_dir" rev-parse --show-toplevel)"
virtual_test="$repo_root/apps/api/internal/modules/auth/domain/ios_device_interop_test.go"
if [[ -e "$virtual_test" ]]; then
  echo "Cannot overlay an existing Go test file" >&2
  exit 1
fi
tmp_root="$(mktemp -d /private/tmp/nerae-ios-device-verify.XXXXXXXX)"
trap 'rm -rf "$tmp_root"' EXIT
go_binary="${NERAE_GO_BIN:-go}"

# iOS device SDK compilation and link only. This neither runs on a device nor
# establishes the product's final minimum iOS version.
sdk="$(xcrun --sdk iphoneos --show-sdk-path)"
xcrun --sdk iphoneos swiftc -target arm64-apple-ios15.0 -sdk "$sdk" \
  -module-cache-path "$tmp_root/ios-cache" -parse-as-library -emit-library \
  -o "$tmp_root/libNeraeDevice.dylib" \
  "$script_dir/SignatureEncoding.swift" "$script_dir/DeviceProof.swift" "$script_dir/DeviceKey.swift"
echo "iPhoneOS SDK compile and link passed"

# The signing key here is a temporary macOS software key, not Secure Enclave.
xcrun swiftc -module-cache-path "$tmp_root/mac-cache" -parse-as-library \
  -o "$tmp_root/interop" "$script_dir/SignatureEncoding.swift" "$script_dir/DeviceProof.swift" \
  "$script_dir/tests/InteropSmoke.swift"
"$tmp_root/interop" "$tmp_root/fixture.json"

python3 - "$virtual_test" \
  "$script_dir/tests/ServerInterop_test.go" "$tmp_root/overlay.json" <<'PY'
import json, sys
with open(sys.argv[3], "w", encoding="utf-8") as out:
    json.dump({"Replace": {sys.argv[1]: sys.argv[2]}}, out)
PY
(cd "$repo_root/apps/api" && NERAE_DEVICE_PROOF_FIXTURE="$tmp_root/fixture.json" \
  GOTOOLCHAIN=local GOPROXY=off "$go_binary" test -count=1 -overlay "$tmp_root/overlay.json" \
  -run '^TestIOSDeviceProofInterop$' ./internal/modules/auth/domain)
