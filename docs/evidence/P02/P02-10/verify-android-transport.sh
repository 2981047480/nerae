#!/usr/bin/env bash
set -euo pipefail

repo_root=$(git rev-parse --show-toplevel)
evidence_dir="$repo_root/docs/evidence/P02/P02-10"
java_home=${NERAE_JAVA_HOME:-/private/tmp/nerae-hbuilder-download/HBuilderX.app/Contents/HBuilderX/plugins/amazon-corretto}
kotlinc=${NERAE_KOTLINC:-/private/tmp/nerae-kotlin-1.9.24/kotlinc/bin/kotlinc}
android_jar=${NERAE_ANDROID_JAR:-"$HOME/Library/Android/sdk/platforms/android-35/android.jar"}
python_bin=${NERAE_PYTHON:-python3}
openssl_bin=${NERAE_OPENSSL:-openssl}
keytool="$java_home/bin/keytool"

for path in "$java_home/bin/java" "$keytool" "$kotlinc" "$android_jar" \
  "$evidence_dir/AndroidHttpsSyntheticSmoke.kt" "$evidence_dir/android_tls_server.py"; do
  [[ -e "$path" ]] || { printf 'Missing local prerequisite: %s\n' "$path" >&2; exit 2; }
done
export JAVA_HOME="$java_home"
export PATH="$JAVA_HOME/bin:$PATH"
"$kotlinc" -version 2>&1 | rg -q '1\.9\.24' || { echo 'Expected Kotlin 1.9.24.' >&2; exit 2; }

scratch=$(mktemp -d /private/tmp/nerae-p02-10-android-tls.XXXXXXXX)
chmod 700 "$scratch"
succeeded=0
server_pid=''
cleanup() {
  if [[ -n "$server_pid" ]]; then kill "$server_pid" 2>/dev/null || true; wait "$server_pid" 2>/dev/null || true; fi
  if [[ "$succeeded" == 1 ]]; then rm -rf -- "$scratch"; else printf 'Android TLS evidence retained: %s\n' "$scratch" >&2; fi
}
trap cleanup EXIT
mkdir -p "$scratch/classes" "$scratch/tmp"
chmod 700 "$scratch/classes" "$scratch/tmp"

"$openssl_bin" req -x509 -newkey rsa:2048 -nodes -keyout "$scratch/ca.key" -out "$scratch/ca.pem" \
  -days 1 -subj '/CN=Nerae Android HTTPS Test CA' >/dev/null 2>&1
"$openssl_bin" req -newkey rsa:2048 -nodes -keyout "$scratch/server.key" -out "$scratch/server.csr" \
  -subj '/CN=127.0.0.1' >/dev/null 2>&1
cat > "$scratch/server.ext" <<'CERT'
basicConstraints=critical,CA:FALSE
keyUsage=critical,digitalSignature,keyEncipherment
extendedKeyUsage=serverAuth
subjectAltName=IP:127.0.0.1
CERT
"$openssl_bin" x509 -req -in "$scratch/server.csr" -CA "$scratch/ca.pem" -CAkey "$scratch/ca.key" \
  -CAcreateserial -out "$scratch/server.pem" -days 1 -sha256 -extfile "$scratch/server.ext" >/dev/null 2>&1
"$openssl_bin" req -x509 -newkey rsa:2048 -nodes -keyout "$scratch/bad.key" -out "$scratch/bad.pem" \
  -days 1 -subj '/CN=Untrusted Nerae Android Test CA' \
  -addext 'subjectAltName=IP:127.0.0.1' >/dev/null 2>&1
"$keytool" -importcert -noprompt -alias nerae-android-test-ca -file "$scratch/ca.pem" \
  -keystore "$scratch/test-trust.p12" -storetype PKCS12 -storepass nerae-test-only >/dev/null 2>&1

"$kotlinc" -classpath "$android_jar" -d "$scratch/classes" \
  "$repo_root/apps/client/src/uni_modules/nerae-https/utssdk/app-android/StrictJson.kt" \
  "$repo_root/apps/client/src/uni_modules/nerae-https/utssdk/app-android/HttpsRequestSpec.kt" \
  "$repo_root/apps/client/src/uni_modules/nerae-https/utssdk/app-android/HttpsTransport.kt" \
  "$repo_root/apps/client/src/uni_modules/nerae-https/utssdk/app-android/NeraeHttpsBridge.kt" \
  "$evidence_dir/AndroidHttpsSyntheticSmoke.kt"

export NERAE_ANDROID_HITS="$scratch/hits.jsonl"
export NERAE_ANDROID_PORTS="$scratch/ports.json"
export NERAE_ANDROID_GOOD_CERT="$scratch/server.pem"
export NERAE_ANDROID_GOOD_KEY="$scratch/server.key"
export NERAE_ANDROID_BAD_CERT="$scratch/bad.pem"
export NERAE_ANDROID_BAD_KEY="$scratch/bad.key"
TMPDIR="$scratch/tmp" "$python_bin" "$evidence_dir/android_tls_server.py" >"$scratch/server.log" 2>&1 & server_pid=$!
for _ in $(seq 1 200); do [[ -s "$NERAE_ANDROID_PORTS" ]] && break; sleep 0.05; done
[[ -s "$NERAE_ANDROID_PORTS" ]] || { cat "$scratch/server.log" >&2; exit 1; }
read -r good_port bad_port < <("$python_bin" -c \
  'import json,sys; p=json.load(open(sys.argv[1])); print(p["primary"],p["bad"])' "$NERAE_ANDROID_PORTS")

"$java_home/bin/java" \
  -Djavax.net.ssl.trustStore="$scratch/test-trust.p12" \
  -Djavax.net.ssl.trustStoreType=PKCS12 \
  -Djavax.net.ssl.trustStorePassword=nerae-test-only \
  -cp "$scratch/classes:$(dirname "$(dirname "$kotlinc")")/lib/kotlin-stdlib.jar:$android_jar" \
  uts.sdk.modules.neraeHttps.AndroidHttpsSyntheticSmokeKt \
  "$good_port" "$bad_port" "$NERAE_ANDROID_HITS"

printf 'PASS: Kotlin 1.9.24 compiled against Android API 35; JVM loopback TLS/framing checks passed. This is not Android runtime verification.\n'
succeeded=1
