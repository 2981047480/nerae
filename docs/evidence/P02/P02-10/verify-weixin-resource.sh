#!/usr/bin/env bash
set -euo pipefail
repo_root=$(git rev-parse --show-toplevel)
node_bin=${NERAE_NODE:-/opt/homebrew/bin/node}
deps=${NERAE_CLIENT_NODE_MODULES:-"$repo_root/apps/client/node_modules"}
[[ -x "$node_bin" && -x "$deps/.bin/uni" ]] || { echo 'Existing client compiler is unavailable.' >&2; exit 2; }
work_dir=$(mktemp -d /private/tmp/nerae-p02-10-mp.XXXXXXXX)
succeeded=0
cleanup() {
  if [[ "$succeeded" == 1 ]]; then rm -rf -- "$work_dir"; else printf 'WeChat resource evidence retained: %s\n' "$work_dir" >&2; fi
}
trap cleanup EXIT
client="$work_dir/client"
mkdir -p "$client" "$work_dir/tmp"
cp "$repo_root/apps/client/package.json" "$repo_root/apps/client/tsconfig.json" "$repo_root/apps/client/vite.config.ts" "$repo_root/apps/client/index.html" "$client/"
cp -R "$repo_root/apps/client/src" "$client/src"
ln -s "$deps" "$client/node_modules"
cat >> "$client/src/main.ts" <<'TS'
import { request as neraeHttpsRequest } from './uni_modules/nerae-https'
export { neraeHttpsRequest }
TS
(cd "$client" && PATH="$(dirname "$node_bin"):$PATH" TMPDIR="$work_dir/tmp" UNI_OUTPUT_DIR="$work_dir/output" "$deps/.bin/uni" build -p mp-weixin)
[[ -s "$work_dir/output/app.js" ]] || { echo 'Missing WeChat app entry.' >&2; exit 1; }
if ! rg -q 'unsupported_platform' "$work_dir/output/uni_modules/nerae-https"; then
  echo 'HTTPS unsupported fallback is missing from WeChat output.' >&2; exit 1
fi
printf 'PASS: locked WeChat compiler emitted the HTTPS unsupported-platform fallback; no device or server was used.\n'
succeeded=1
