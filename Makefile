# Real foundation checks. Pending integration gates fail explicitly.
NERAE_PYTHON ?= python3.11
NERAE_NODE ?= node
NERAE_PNPM ?= pnpm
NERAE_GO ?= go
NERAE_GOFMT ?= gofmt
pnpm_config_verify_deps_before_run ?= error
export NERAE_NODE NERAE_PNPM NERAE_GO NERAE_GOFMT pnpm_config_verify_deps_before_run
export GOTOOLCHAIN := local
export PYTHONDONTWRITEBYTECODE := 1

.NOTPARALLEL:
.PHONY: check-foundation check-planning check-versions contract-check quality-tests check-dev format-go format-ts go-vet go-test \
        typecheck-client typecheck-admin build-client build-admin boundary-check codegen-check check-isolation \
        migration-check test test-integration e2e e2e-api visual-diff seed-check seed-dry-run check

check-foundation: check-versions check-planning contract-check quality-tests check-dev format-go format-ts go-vet go-test typecheck-client typecheck-admin build-client build-admin boundary-check

check-versions:
	"$(NERAE_PYTHON)" scripts/quality/check_versions.py

check-planning:
	"$(NERAE_PYTHON)" scripts/plan.py check
	"$(NERAE_PYTHON)" scripts/quality/run_unittest.py scripts/tests

contract-check:
	"$(NERAE_PYTHON)" scripts/quality/run_contract.py

quality-tests:
	"$(NERAE_PYTHON)" scripts/quality/run_unittest.py scripts/quality

check-dev:
	"$(NERAE_PYTHON)" scripts/quality/run_unittest.py scripts/dev/tests

format-go:
	"$(NERAE_PYTHON)" scripts/quality/check_go_format.py

format-ts:
	"$(NERAE_PYTHON)" scripts/quality/run_node_tests.py scripts/quality/format.test.mjs
	"$(NERAE_NODE)" scripts/quality/check_ts_format.mjs

go-vet:
	cd apps/api && "$(NERAE_GO)" vet ./...

go-test:
	"$(NERAE_PYTHON)" scripts/quality/run_go_tests.py

typecheck-client:
	"$(NERAE_PNPM)" --filter @nerae/client run typecheck

typecheck-admin:
	"$(NERAE_PNPM)" --filter @nerae/admin run typecheck

build-client:
	"$(NERAE_PYTHON)" scripts/quality/run_build.py client

build-admin:
	"$(NERAE_PYTHON)" scripts/quality/run_build.py admin

boundary-check:
	"$(NERAE_PYTHON)" scripts/quality/run_node_tests.py
	"$(NERAE_NODE)" scripts/quality/boundary.mjs

codegen-check check-isolation migration-check test test-integration e2e e2e-api visual-diff seed-check seed-dry-run check:
	@printf '%s\n' 'NOT_READY: $@ requires its designated implementation and real acceptance evidence' >&2
	@exit 1
