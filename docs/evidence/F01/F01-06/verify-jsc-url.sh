#!/usr/bin/env bash
set -euo pipefail
repo_root=$(git rev-parse --show-toplevel)
core_js=${NERAE_CORE_JS:-"$repo_root/node_modules/.pnpm/core-js@3.51.0/node_modules/core-js"}
esbuild=${NERAE_ESBUILD:-"$repo_root/node_modules/.pnpm/esbuild@0.20.2/node_modules/esbuild/bin/esbuild"}
[[ -f "$core_js/actual/url/index.js" && -x "$esbuild" ]] || { printf 'Missing existing core-js or esbuild prerequisites.\n' >&2; exit 2; }
work_dir=$(mktemp -d /private/tmp/nerae-f01-06-jsc.XXXXXXXX)
succeeded=0
cleanup() {
  if [[ "$succeeded" == 1 ]]; then rm -rf -- "$work_dir"; else printf 'JavaScriptCore evidence retained: %s\n' "$work_dir" >&2; fi
}
trap cleanup EXIT
mkdir -p "$work_dir/node_modules" "$work_dir/tmp" "$work_dir/swift-cache"
ln -s "$core_js" "$work_dir/node_modules/core-js"
python3 - "$repo_root" "$work_dir/entry.ts" <<'PY'
import json
from pathlib import Path
import sys
root = Path(sys.argv[1])
entry = Path(sys.argv[2])
auth = json.dumps(str(root / 'apps/client/src/api/auth/emailAuthApi.ts'))
training = json.dumps(str(root / 'apps/client/src/features/training/recordingApi.ts'))
entry.write_text("""import 'core-js/actual/url'
import { EmailAuthApi } from %s
import { RecordingApi } from %s
;(globalThis as any).__neraeUrlProbe = () => {
  const u = new URL('https://api.example.test:443/v1/../v2?x=1')
  const ip = new URL('https://203.0.113.42:8443/v1/me/sessions?page=2')
  if (u.origin !== 'https://api.example.test' || u.pathname !== '/v2' || u.search !== '?x=1' ||
      ip.hostname !== '203.0.113.42' || ip.port !== '8443' || ip.search !== '?page=2') return false
  let invalid = false
  try { new URL('not-a-url') } catch { invalid = true }
  if (!invalid) return false
  const auth = new EmailAuthApi('https://api.example.test/v1', async () => { throw new Error('unused') })
  const recording = new RecordingApi('https://api.example.test/v1',
    { current: () => null, isCurrent: () => false }, async () => { throw new Error('unused') },
    { subjectId: '1', generation: 'synthetic' })
  let authRejected = false
  let recordingRejected = false
  try { new EmailAuthApi('http://api.example.test', async () => { throw new Error('unused') }) } catch { authRejected = true }
  try { new RecordingApi('https://name:password@api.example.test',
    { current: () => null, isCurrent: () => false }, async () => { throw new Error('unused') },
    { subjectId: '1', generation: 'synthetic' }) } catch { recordingRejected = true }
  return !!auth && !!recording && authRejected && recordingRejected
}
""" % (auth, training))
PY
"$esbuild" "$work_dir/entry.ts" --bundle --format=iife --target=es2019 --log-level=warning --outfile="$work_dir/probe.js"
TMPDIR="$work_dir/tmp" /usr/bin/swift -module-cache-path "$work_dir/swift-cache" "$repo_root/docs/evidence/F01/F01-06/UrlProbe.swift" "$work_dir/probe.js"
succeeded=1
