#!/bin/sh
# Probe the locked generators only; this is not a product API or a service test.
set -eu
NERAE_PROBE=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)
NERAE_STAGE=$(mktemp -d "${TMPDIR:-/tmp}/nerae-generator-probe.XXXXXXXX")
trap 'rm -rf -- "$NERAE_STAGE"' EXIT HUP INT TERM
: "${NERAE_GO:=go}"
cp "$NERAE_PROBE/package.json" "$NERAE_PROBE/package-lock.json" "$NERAE_PROBE/go.mod" "$NERAE_PROBE/go.sum" "$NERAE_PROBE/contract.yaml" "$NERAE_PROBE/oapi-codegen.yaml" "$NERAE_PROBE/fixture.ts" "$NERAE_PROBE/probe_test.go" "$NERAE_STAGE/"
cd "$NERAE_STAGE"
set -- ci --ignore-scripts --no-audit --no-fund
if [ "${NERAE_NPM_OFFLINE:-0}" = 1 ]; then set -- "$@" --offline; fi
if [ -n "${NERAE_NPM_CACHE:-}" ]; then set -- "$@" --cache "$NERAE_NPM_CACHE"; fi
npm "$@"
[ "$(./node_modules/.bin/openapi-typescript --version)" = v7.6.1 ]
[ "$(./node_modules/.bin/tsc --version)" = 'Version 5.9.3' ]
[ "$(GOWORK=off "$NERAE_GO" list -m -f '{{.Version}}' github.com/oapi-codegen/oapi-codegen/v2)" = v2.4.1 ]
./node_modules/.bin/openapi-typescript contract.yaml -o generated-types.ts
./node_modules/.bin/tsc --strict --noEmit --skipLibCheck --target es2020 fixture.ts
GOWORK=off "$NERAE_GO" tool oapi-codegen -config oapi-codegen.yaml -o model.gen.go contract.yaml
GOWORK=off "$NERAE_GO" test -mod=readonly -count=1 ./...
