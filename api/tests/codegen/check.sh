#!/bin/sh
# Check the generated recording types in a disposable, isolated directory.
set -eu

NERAE_REPO=$(CDPATH='' cd -- "$(dirname -- "$0")/../../.." && pwd)
NERAE_STAGE=$(mktemp -d "${TMPDIR:-/tmp}/nerae-recording-codegen.XXXXXXXX")
trap 'rm -rf -- "$NERAE_STAGE"' EXIT HUP INT TERM

: "${NERAE_GO:=go}"
: "${NERAE_OAPI_CODEGEN:=oapi-codegen}"
: "${NERAE_OPENAPI_TYPESCRIPT:=openapi-typescript}"
: "${NERAE_TSC:=tsc}"

"$NERAE_OPENAPI_TYPESCRIPT" "$NERAE_REPO/api/openapi.yaml" -o "$NERAE_STAGE/generated-types.ts"
cp "$NERAE_REPO/api/tests/codegen/recording-types.ts" "$NERAE_STAGE/recording-types.ts"
"$NERAE_TSC" --strict --noEmit --skipLibCheck --target es2020 "$NERAE_STAGE/recording-types.ts"

"$NERAE_OAPI_CODEGEN" -config "$NERAE_REPO/api/tests/codegen/oapi-codegen-probe.yaml" \
  -o "$NERAE_STAGE/api.gen.go" "$NERAE_REPO/api/openapi.yaml"
cp "$NERAE_REPO/api/tests/codegen/recording_types_test.go" "$NERAE_STAGE/recording_types_test.go"
cp "$NERAE_REPO/api/tests/codegen/schema_validation_test.go" "$NERAE_STAGE/schema_validation_test.go"
cp "$NERAE_REPO/api/tests/codegen/go-probe/go.mod" "$NERAE_REPO/api/tests/codegen/go-probe/go.sum" "$NERAE_STAGE/"
(
  cd "$NERAE_STAGE"
  NERAE_OPENAPI="$NERAE_REPO/api/openapi.yaml" GOWORK=off "$NERAE_GO" test -mod=readonly -count=1 ./...
)
