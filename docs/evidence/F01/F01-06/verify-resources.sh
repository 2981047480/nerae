#!/usr/bin/env bash
set -euo pipefail
repo_root=$(git rev-parse --show-toplevel)
node_bin=${NERAE_NODE:-/opt/homebrew/bin/node}
vue_tsc=${NERAE_VUE_TSC:-"$repo_root/apps/client/node_modules/.bin/vue-tsc"}
deps=${NERAE_CLIENT_NODE_MODULES:-"$repo_root/apps/client/node_modules"}
core_js=${NERAE_CORE_JS:-"$repo_root/node_modules/.pnpm/core-js@3.51.0/node_modules/core-js"}
[[ -x "$node_bin" && -x "$deps/.bin/uni" && -x "$vue_tsc" && -f "$core_js/actual/url/index.js" ]] || {
  printf 'Missing existing client compiler or core-js.\n' >&2; exit 2;
}
work_dir=$(mktemp -d /private/tmp/nerae-f01-06-resources.XXXXXXXX)
succeeded=0
cleanup() {
  if [[ "$succeeded" == 1 ]]; then rm -rf -- "$work_dir"; else printf 'Resource evidence retained: %s\n' "$work_dir" >&2; fi
}
trap cleanup EXIT
client="$work_dir/client"
mkdir -p "$client/node_modules" "$work_dir/tmp"
cp "$repo_root/apps/client/package.json" "$repo_root/apps/client/tsconfig.json" \
  "$repo_root/apps/client/vite.config.ts" "$repo_root/apps/client/index.html" "$client/"
cp -R "$repo_root/apps/client/src" "$client/src"
for module in .bin @dcloudio vite vue; do ln -s "$deps/$module" "$client/node_modules/$module"; done
ln -s "$core_js" "$client/node_modules/core-js"
PATH="$(dirname "$node_bin"):$PATH" "$vue_tsc" --noEmit -p "$client/tsconfig.json"
cat >> "$client/src/main.ts" <<'TS'
;(globalThis as any).__neraeURLBuildProbe = () => new URL('https://api.example.test/v1').hostname
TS
for platform in app mp-weixin; do
  output="$work_dir/output-$platform"
  (cd "$client" && PATH="$(dirname "$node_bin"):$PATH" TMPDIR="$work_dir/tmp" UNI_OUTPUT_DIR="$output" "$deps/.bin/uni" build -p "$platform")
  if [[ "$platform" == app ]]; then
    artifact="$output/app-service.js"
    polyfill_artifact="$artifact"
  else
    artifact="$output/app.js"
    polyfill_artifact="$output/common/vendor.js"
    rg -Fq 'require("./common/vendor.js")' "$artifact" || { printf 'WeChat entry does not load its vendor bundle.\n' >&2; exit 1; }
  fi
  [[ -s "$artifact" && -s "$polyfill_artifact" ]] && rg -Fq '__core-js_shared__' "$polyfill_artifact" && rg -Fq '__neraeURLBuildProbe' "$artifact" || {
    printf 'Built %s resource did not retain the URL polyfill and probe.\n' "$platform" >&2; exit 1;
  }
done
printf 'PASS: locked App and WeChat compilers emitted JS resources containing the URL polyfill; no native link, install, or device test.\n'
succeeded=1
