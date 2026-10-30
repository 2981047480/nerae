#!/usr/bin/env bash
set -euo pipefail

repo_root=$(git rev-parse --show-toplevel)
node_bin=${NERAE_NODE:-/opt/homebrew/bin/node}
deps=${NERAE_CLIENT_NODE_MODULES:-"$repo_root/apps/client/node_modules"}
core_js=${NERAE_CORE_JS:-"$repo_root/node_modules/.pnpm/core-js@3.51.0/node_modules/core-js"}
[[ -x "$node_bin" && -x "$deps/.bin/uni" ]] || { echo 'Existing client compiler is unavailable.' >&2; exit 2; }
work_dir=$(mktemp -d /private/tmp/nerae-p02-11-app.XXXXXXXX)
succeeded=0
cleanup() {
  if [[ "$succeeded" == 1 ]]; then rm -rf -- "$work_dir"; else printf 'App resource evidence retained: %s\n' "$work_dir" >&2; fi
}
trap cleanup EXIT
client="$work_dir/client"
mkdir -p "$client" "$work_dir/tmp" "$work_dir/output"
cp "$repo_root/apps/client/package.json" "$repo_root/apps/client/tsconfig.json" \
  "$repo_root/apps/client/vite.config.ts" "$repo_root/apps/client/index.html" "$client/"
cp -R "$repo_root/apps/client/src" "$client/src"
modules="$deps"
if [[ ! -f "$deps/core-js/actual/url/index.js" ]]; then
  [[ -f "$core_js/actual/url/index.js" ]] || { echo 'Pinned core-js dependency is unavailable.' >&2; exit 2; }
  modules="$work_dir/modules"
  mkdir -p "$modules"
  for module in .bin @dcloudio vite vue; do ln -s "$deps/$module" "$modules/$module"; done
  ln -s "$core_js" "$modules/core-js"
fi
ln -s "$modules" "$client/node_modules"
cat >> "$client/src/main.ts" <<'TS'
import { cancel as nativeCancel, request as nativeRequest } from '@/uni_modules/nerae-https/utssdk'
import { createConfiguredAppHttpsPorts } from './platform/app'
const buildProbePorts = createConfiguredAppHttpsPorts('https://api.example.test/v1')
;(globalThis as typeof globalThis & { __p02_11_probe?: unknown }).__p02_11_probe = {
  factory: createConfiguredAppHttpsPorts,
  authTransport: buildProbePorts.authTransport,
  recordingRequest: buildProbePorts.recordingRequest,
  nativeRequest,
  nativeCancel,
}
TS
(cd "$client" && PATH="$(dirname "$node_bin"):$PATH" TMPDIR="$work_dir/tmp" \
  UNI_OUTPUT_DIR="$work_dir/output" "$deps/.bin/uni" build -p app)
artifact="$work_dir/output/app-service.js"
[[ -s "$artifact" ]] || { echo 'Missing nonempty App app-service.js resource.' >&2; exit 1; }
NERAE_APP_SERVICE="$artifact" "$node_bin" "$repo_root/docs/evidence/P02/P02-11/verify-app-resource.mjs"
printf 'app-service.js: %s bytes\n' "$(wc -c < "$artifact" | tr -d ' ')"
succeeded=1
