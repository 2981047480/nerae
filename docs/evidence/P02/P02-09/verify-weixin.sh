#!/usr/bin/env bash
set -euo pipefail

evidence_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)
repo_root=$(git -C "$evidence_dir" rev-parse --show-toplevel)
node_bin=${NERAE_NODE:-/opt/homebrew/bin/node}
dependency_root=${NERAE_CLIENT_NODE_MODULES:-/Users/zephyr/.codex/worktrees/nerae-foundation-integration/nerae/apps/client/node_modules}

if [[ ! -x "$node_bin" || ! -d "$dependency_root/@dcloudio/types" ||
      ! -x "$dependency_root/.bin/tsc" || ! -x "$dependency_root/.bin/uni" ]]; then
	printf 'Existing locked client toolchain is unavailable; no dependency installation attempted.\n' >&2
	exit 2
fi
actual_uni=$(node -p "require('$dependency_root/@dcloudio/uni-mp-weixin/package.json').version")
if [[ "$actual_uni" != "3.0.0-5020620260917001" ]]; then
	printf 'Expected project uni-app compiler 3.0.0-5020620260917001, got %s.\n' "$actual_uni" >&2
	exit 2
fi

work_dir=$(mktemp -d /private/tmp/nerae-p02-09-weixin.XXXXXXXX)
succeeded=0
cleanup() {
	if [[ "$succeeded" == 1 && "${NERAE_KEEP_P02_09_WEIXIN:-0}" != 1 ]]; then
		rm -rf -- "$work_dir"
	else
		printf 'WeChat random-key test directory retained: %s\n' "$work_dir" >&2
	fi
}
trap cleanup EXIT
project="$work_dir/project"
client="$project/apps/client"
format_project="$work_dir/format-project"
format_client="$format_project/apps/client"
mkdir -p "$client/src/platform/weixin" "$project/scripts/quality" "$project/docs/evidence/P02/P02-09" \
	"$format_client/src/platform/weixin" "$format_project/scripts/quality" "$work_dir/tmp" "$work_dir/compiled"
cp "$repo_root/apps/client/package.json" "$repo_root/apps/client/tsconfig.json" "$repo_root/apps/client/vite.config.ts" "$repo_root/apps/client/index.html" "$client/"
cp -R "$repo_root/apps/client/src/." "$client/src/"
cp "$repo_root/apps/client/src/platform/weixin/secureRandom.ts" "$client/src/platform/weixin/"
cp "$repo_root/apps/client/package.json" "$format_client/"
cp "$repo_root/apps/client/src/platform/weixin/secureRandom.ts" "$format_client/src/platform/weixin/"
cp "$repo_root/scripts/quality/check_ts_format.mjs" "$format_project/scripts/quality/"
cp "$evidence_dir/weixin-random.test.mjs" "$project/docs/evidence/P02/P02-09/"
ln -s "$dependency_root" "$client/node_modules"
ln -s "$dependency_root" "$format_client/node_modules"

printf 'Project uni-app version: %s\n' "$actual_uni"
"$node_bin" "$format_project/scripts/quality/check_ts_format.mjs"
"$dependency_root/.bin/tsc" --noEmit -p "$client/tsconfig.json"
(cd "$client" && "$dependency_root/.bin/tsc" --strict --skipLibCheck --target ES2022 --module commonjs --moduleResolution node \
	--lib ES2022,DOM --types @dcloudio/types --outDir "$work_dir/compiled" \
	"$client/src/platform/weixin/secureRandom.ts")
NERAE_WEIXIN_RANDOM_MODULE="$work_dir/compiled/secureRandom.js" \
	"$node_bin" --test --test-reporter=tap "$project/docs/evidence/P02/P02-09/weixin-random.test.mjs"

# Build a disposable source copy with an import so Vite/uni must compile the adapter.
cat >> "$client/src/main.ts" <<'TS'
import { generateWeixinRequestKey } from './platform/weixin/secureRandom'
export { generateWeixinRequestKey }
TS
output="$work_dir/mp-weixin/output"
mkdir -p "$(dirname "$output")"
(cd "$client" && TMPDIR="$work_dir/tmp" XDG_CACHE_HOME="$work_dir/cache" UNI_OUTPUT_DIR="$output" \
	"$dependency_root/.bin/uni" build -p mp-weixin)
adapter_output="$output/platform/weixin/secureRandom.js"
if [[ ! -s "$output/app.js" || ! -s "$adapter_output" ]] || ! rg -q 'getRandomValues' "$adapter_output"; then
	printf 'WeChat resource build did not produce the app entry and reachable random adapter.\n' >&2
	exit 1
fi
printf 'PASS: formatting, typecheck, injected API tests and locked WeChat resource build. app.js: %s bytes; adapter: %s bytes.\n' \
	"$(wc -c < "$output/app.js" | tr -d ' ')" "$(wc -c < "$adapter_output" | tr -d ' ')"
succeeded=1
