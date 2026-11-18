#!/usr/bin/env bash
set -euo pipefail
repo_root=$(git -C "$(dirname "${BASH_SOURCE[0]}")" rev-parse --show-toplevel)
evidence_dir="$repo_root/docs/evidence/P02/P02-13"
source_dir="$repo_root/apps/client/src/uni_modules/nerae-secure-vault/utssdk/app-ios"
scratch=$(mktemp -d /private/tmp/nerae-recovery-http.XXXXXXXX)
succeeded=0
server_pid=''
cleanup() {
  if [[ -n "$server_pid" ]]; then kill "$server_pid" 2>/dev/null || true; wait "$server_pid" 2>/dev/null || true; fi
  if [[ "$succeeded" == 1 ]]; then rm -rf -- "$scratch"
  else printf 'Recovery HTTPS test directory retained: %s\n' "$scratch" >&2; fi
}
trap cleanup EXIT
mkdir -p "$scratch/modules"
openssl req -x509 -newkey rsa:2048 -nodes -keyout "$scratch/ca.key" -out "$scratch/ca.pem" -days 1 -subj '/CN=Nerae Recovery Test CA' >/dev/null 2>&1
openssl req -newkey rsa:2048 -nodes -keyout "$scratch/server.key" -out "$scratch/server.csr" -subj '/CN=localhost' >/dev/null 2>&1
cat > "$scratch/server.ext" <<'CERT'
basicConstraints=critical,CA:FALSE
keyUsage=critical,digitalSignature,keyEncipherment
extendedKeyUsage=serverAuth
subjectAltName=IP:127.0.0.1
CERT
openssl x509 -in "$scratch/ca.pem" -outform DER -out "$scratch/ca.der"
openssl x509 -req -in "$scratch/server.csr" -CA "$scratch/ca.pem" -CAkey "$scratch/ca.key" -CAcreateserial -out "$scratch/server.pem" -days 1 -sha256 -extfile "$scratch/server.ext" >/dev/null 2>&1
openssl req -x509 -newkey rsa:2048 -nodes -keyout "$scratch/bad.key" -out "$scratch/bad.pem" -days 1 -subj '/CN=Untrusted Recovery Test CA' >/dev/null 2>&1
openssl x509 -in "$scratch/bad.pem" -outform DER -out "$scratch/bad.der"
xcrun swiftc -module-cache-path "$scratch/modules" -D NERAE_RECOVERY_HTTP_TESTING \
  "$source_dir/NeraeStrictJSON.swift" "$source_dir/NeraeRecordingV2JournalValidator.swift" "$source_dir/NeraeSecureVaultBridge.swift" "$source_dir/NeraeRecoveryHTTP.swift" \
  "$source_dir/NeraeAuthenticationStateGeneration.swift" \
  "$source_dir/NeraeRecoveryReplayValidator.swift" "$source_dir/NeraeRecoveryInspector.swift" \
  "$evidence_dir/RecoveryHTTPProbe.swift" -framework Security -o "$scratch/probe"
export NERAE_RECOVERY_CERT="$scratch/server.pem" NERAE_RECOVERY_KEY="$scratch/server.key"
export NERAE_RECOVERY_PORT="$scratch/port" NERAE_RECOVERY_HITS="$scratch/hits"
python3 -B "$evidence_dir/recovery-http-server.py" >"$scratch/server.log" 2>&1 &
server_pid=$!
for _attempt in {1..100}; do
  [[ -s "$NERAE_RECOVERY_PORT" ]] && break
  kill -0 "$server_pid" 2>/dev/null || break
  sleep 0.05
done
if [[ ! -s "$NERAE_RECOVERY_PORT" ]]; then printf 'TLS fixture did not start.\n' >&2; exit 1; fi
"$scratch/probe" "$(cat "$NERAE_RECOVERY_PORT")" "$scratch/ca.der" "$scratch/bad.der"
python3 - "$NERAE_RECOVERY_HITS" <<'PY'
import json
import sys
from pathlib import Path

rows = [json.loads(line) for line in Path(sys.argv[1]).read_text().splitlines()]
expected = {'/ok/me/sessions/replay', '/redirect/me/sessions/replay', '/large/me/sessions/replay',
            '/chunked/me/sessions/replay', '/slow/me/sessions/replay', '/verified/me/sessions/replay',
            '/mismatched/me/sessions/replay', '/slow-verified/me/sessions/replay', '/slow-aba/me/sessions/replay'}
assert {row['path'] for row in rows} == expected, rows
assert len(rows) == len(expected) and all(row['requestMatches'] for row in rows), rows
print('PASS: exact replay-only routes; no mismatched-owner, redirect destination or ordinary write reached the server')
PY
sdk_path=$(xcrun --sdk iphoneos --show-sdk-path)
xcrun swiftc -module-cache-path "$scratch/modules" -target arm64-apple-ios15.0 -sdk "$sdk_path" -typecheck \
  "$source_dir/NeraeStrictJSON.swift" "$source_dir/NeraeRecordingV2JournalValidator.swift" "$source_dir/NeraeSecureVaultBridge.swift" "$source_dir/NeraeRecoveryHTTP.swift" \
  "$source_dir/NeraeAuthenticationStateGeneration.swift" \
  "$source_dir/NeraeRecoveryReplayValidator.swift" "$source_dir/NeraeRecoveryInspector.swift"
printf 'PASS: iPhoneOS arm64 source typecheck; no signed app or device was run\n'
succeeded=1
