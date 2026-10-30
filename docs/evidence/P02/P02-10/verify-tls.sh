#!/usr/bin/env bash
set -euo pipefail
repo_root=$(git rev-parse --show-toplevel)
evidence_dir="$repo_root/docs/evidence/P02/P02-10"
source_file="$repo_root/apps/client/src/uni_modules/nerae-https/utssdk/app-ios/NeraeHttpsBridge.swift"
scratch=$(mktemp -d /private/tmp/nerae-p02-10-tls.XXXXXXXX)
succeeded=0
server_pid=''
cleanup() {
  if [[ -n "$server_pid" ]]; then kill "$server_pid" 2>/dev/null || true; wait "$server_pid" 2>/dev/null || true; fi
  if [[ "$succeeded" == 1 ]]; then rm -rf -- "$scratch"; else printf 'TLS evidence retained: %s\n' "$scratch" >&2; fi
}
trap cleanup EXIT
mkdir -p "$scratch/modules"
openssl req -x509 -newkey rsa:2048 -nodes -keyout "$scratch/ca.key" -out "$scratch/ca.pem" -days 1 -subj '/CN=Nerae P02-10 Test CA' >/dev/null 2>&1
openssl req -newkey rsa:2048 -nodes -keyout "$scratch/server.key" -out "$scratch/server.csr" -subj '/CN=localhost' >/dev/null 2>&1
cat > "$scratch/server.ext" <<'CERT'
basicConstraints=critical,CA:FALSE
keyUsage=critical,digitalSignature,keyEncipherment
extendedKeyUsage=serverAuth
subjectAltName=IP:127.0.0.1
CERT
openssl x509 -in "$scratch/ca.pem" -outform DER -out "$scratch/ca.der"
openssl x509 -req -in "$scratch/server.csr" -CA "$scratch/ca.pem" -CAkey "$scratch/ca.key" -CAcreateserial -out "$scratch/server.pem" -days 1 -sha256 -extfile "$scratch/server.ext" >/dev/null 2>&1
openssl req -x509 -newkey rsa:2048 -nodes -keyout "$scratch/bad-ca.key" -out "$scratch/bad-ca.pem" -days 1 -subj '/CN=Untrusted P02-10 Test CA' >/dev/null 2>&1
openssl x509 -in "$scratch/bad-ca.pem" -outform DER -out "$scratch/bad-ca.der"
cp "$evidence_dir/Probe.swift" "$scratch/main.swift"
/usr/bin/swiftc -module-cache-path "$scratch/modules" -D NERAE_HTTPS_TESTING "$source_file" "$scratch/main.swift" -framework Security -o "$scratch/probe"
export NERAE_TLS_CERT="$scratch/server.pem" NERAE_TLS_KEY="$scratch/server.key"
export NERAE_TLS_PORT="$scratch/tls.port" NERAE_HTTP_PORT_FILE="$scratch/http.port" NERAE_TLS_HITS="$scratch/hits.log"
python3 "$evidence_dir/tls_server.py" >"$scratch/server.log" 2>&1 & server_pid=$!
for _ in $(seq 1 100); do [[ -s "$scratch/tls.port" && -s "$scratch/http.port" ]] && break; sleep 0.05; done
[[ -s "$scratch/tls.port" && -s "$scratch/http.port" ]] || { cat "$scratch/server.log" >&2; exit 1; }
python3 "$evidence_dir/test_tls.py" "$scratch/probe" "$(cat "$scratch/tls.port")" "$scratch/ca.der" "$scratch/bad-ca.der" "$scratch/hits.log" "$(cat "$scratch/http.port")"
succeeded=1
