#!/bin/sh
# Verify the current formal config and contract using isolated generated output.
set -eu
NERAE_REPO=$(CDPATH='' cd -- "$(dirname -- "$0")/../../../.." && pwd)
NERAE_STAGE=$(mktemp -d "${TMPDIR:-/tmp}/nerae-f02-nullable.XXXXXXXX")
: "${NERAE_GO:=go}"
: "${NERAE_PYTHON:=python3.11}"
export GOTOOLCHAIN=local GOWORK=off
printf 'Verification output retained at %s\n' "$NERAE_STAGE"
PYTHONDONTWRITEBYTECODE=1 "$NERAE_PYTHON" "$NERAE_REPO/docs/evidence/F02/F02-00/nullable_inventory.py" > "$NERAE_STAGE/nullable-inventory.json"
if ! cmp -s "$NERAE_REPO/docs/evidence/F02/F02-00/nullable-inventory.json" "$NERAE_STAGE/nullable-inventory.json"; then
  printf '%s\n' 'Nullable inventory or contract changed; regenerate and review all affected fields.' >&2
  exit 1
fi
PYTHONDONTWRITEBYTECODE=1 "$NERAE_PYTHON" "$NERAE_REPO/docs/evidence/F02/F02-00/nullable_inventory_test.py"
(
  cd "$NERAE_REPO/docs/evidence/F01/F01-01/generator-probe"
  [ "$("$NERAE_GO" list -m -f '{{.Version}}' github.com/oapi-codegen/oapi-codegen/v2)" = v2.4.1 ]
  "$NERAE_GO" build -mod=readonly -o "$NERAE_STAGE/oapi-codegen" github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen
)
"$NERAE_STAGE/oapi-codegen" --version
for NERAE_RUN in first second; do
  NERAE_ROOT="$NERAE_STAGE/$NERAE_RUN"
  mkdir -p "$NERAE_ROOT/api/templates" "$NERAE_ROOT/apps/api/internal/gen" "$NERAE_ROOT/apps/api/internal/contractnullable"
  cp "$NERAE_REPO/api/openapi.yaml" "$NERAE_ROOT/api/openapi.yaml"
  cp "$NERAE_REPO/api/oapi-codegen.yaml" "$NERAE_ROOT/api/oapi-codegen.yaml"
  cp "$NERAE_REPO/api/templates/oapi-imports.tmpl" "$NERAE_ROOT/api/templates/oapi-imports.tmpl"
  cp "$NERAE_REPO/apps/api/internal/contractnullable/nullable.go" "$NERAE_ROOT/apps/api/internal/contractnullable/nullable.go"
  (cd "$NERAE_ROOT" && "$NERAE_STAGE/oapi-codegen" -config api/oapi-codegen.yaml api/openapi.yaml)
done
cmp "$NERAE_STAGE/first/apps/api/internal/gen/api.gen.go" "$NERAE_STAGE/second/apps/api/internal/gen/api.gen.go"
cp "$NERAE_REPO/apps/api/internal/contractnullable/nullable_test.go" "$NERAE_STAGE/first/apps/api/internal/contractnullable/nullable_test.go"
cp "$NERAE_REPO/docs/evidence/F02/F02-00/nullable_generated_test.go" "$NERAE_STAGE/first/apps/api/internal/gen/nullable_generated_test.go"
for NERAE_TEST in recording_types_test.go identity_types_test.go schema_validation_test.go; do
  cp "$NERAE_REPO/api/tests/codegen/$NERAE_TEST" "$NERAE_STAGE/first/apps/api/internal/gen/$NERAE_TEST"
done
sed '1s/^module nerae-contract-smoke$/module nerae\/api/' "$NERAE_REPO/api/tests/codegen/go-probe/go.mod" > "$NERAE_STAGE/first/apps/api/go.mod"
cp "$NERAE_REPO/api/tests/codegen/go-probe/go.sum" "$NERAE_STAGE/first/apps/api/go.sum"
(
  cd "$NERAE_STAGE/first/apps/api"
  NERAE_OPENAPI="$NERAE_STAGE/first/api/openapi.yaml" "$NERAE_GO" test -mod=readonly -race -count=1 ./internal/contractnullable ./internal/gen
)
printf '%s\n' 'Formal configuration generated twice identically; nullable and schema tests passed.'
