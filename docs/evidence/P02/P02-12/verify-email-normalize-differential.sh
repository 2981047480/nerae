#!/usr/bin/env bash
set -euo pipefail

script_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)
repo_root=$(git -C "$script_dir" rev-parse --show-toplevel)
go_bin=${NERAE_GO_BIN:-/private/tmp/nerae-go-toolchain-1.27.3/go/bin/go}
virtual_test="$repo_root/apps/api/internal/modules/auth/domain/email_identity_differential_test.go"
if [[ -e "$virtual_test" ]]; then
	echo "Refusing to overlay existing Go test: $virtual_test" >&2
	exit 2
fi
if [[ ! -x "$go_bin" ]]; then
	echo "Go toolchain not found: $go_bin" >&2
	exit 2
fi

scratch=$(mktemp -d /private/tmp/nerae-p02-12-email-normalize.XXXXXXXX)
succeeded=0
cleanup() {
	if [[ "$succeeded" == 1 && "${NERAE_KEEP_NORMALIZE_PROBE:-0}" != 1 ]]; then rm -rf -- "$scratch"; else echo "Retained: $scratch" >&2; fi
}
trap cleanup EXIT
cat > "$scratch/email_identity_differential_test.go" <<'GO'
package domain

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

type vectorParts struct {
	Local       string `json:"local"`
	DomainChar  string `json:"domainChar"`
	DomainLength int   `json:"domainLength"`
}

type vector struct {
	Name         string       `json:"name"`
	Input         string       `json:"input"`
	InputParts    *vectorParts `json:"inputParts"`
	Accept        bool         `json:"accept"`
	Expected      string       `json:"expected"`
	ExpectedParts *vectorParts `json:"expectedParts"`
	NormalizedStable *bool     `json:"normalizedStable"`
}

func expand(value string, parts *vectorParts) string {
	if parts == nil { return value }
	return parts.Local + "@" + strings.Repeat(parts.DomainChar, parts.DomainLength)
}

func TestEmailNormalizeSharedVectors(t *testing.T) {
	path := os.Getenv("NERAE_NORMALIZE_VECTORS")
	data, err := os.ReadFile(path)
	if err != nil { t.Fatal("could not read shared email normalization vectors") }
	var vectors []vector
	if err := json.Unmarshal(data, &vectors); err != nil { t.Fatal("shared email normalization vectors are invalid") }
	for _, item := range vectors {
		t.Run(item.Name, func(t *testing.T) {
			got, err := NormalizeEmail(expand(item.Input, item.InputParts))
			if item.Accept {
				expected := expand(item.Expected, item.ExpectedParts)
				if err != nil || got != expected {
					t.Fatalf("server normalization differs for vector %s", item.Name)
				}
				again, secondErr := NormalizeEmail(expected)
				stable := item.NormalizedStable == nil || *item.NormalizedStable
				if stable && (secondErr != nil || again != expected) {
					t.Fatalf("server normalized output is not stable for vector %s", item.Name)
				}
				if !stable && secondErr == nil {
					t.Fatalf("server unexpectedly accepted non-stable normalized output for vector %s", item.Name)
				}
			} else if err == nil {
				t.Fatalf("server accepted rejected vector %s", item.Name)
			}
		})
	}
}
GO

python3 - "$virtual_test" "$scratch/email_identity_differential_test.go" "$scratch/overlay.json" <<'PY'
import json, sys
with open(sys.argv[3], "w", encoding="utf-8") as output:
    json.dump({"Replace": {sys.argv[1]: sys.argv[2]}}, output)
PY

(cd "$repo_root/apps/api" && \
	NERAE_NORMALIZE_VECTORS="$script_dir/email-normalize-vectors.json" \
	GOTOOLCHAIN=local GOPROXY=off GOSUMDB=off \
	GOMODCACHE=/private/tmp/nerae-go-api-mod GOCACHE=/private/tmp/nerae-go-api-cache \
	"$go_bin" test -count=1 -overlay "$scratch/overlay.json" \
	-run '^TestEmailNormalizeSharedVectors$' ./internal/modules/auth/domain)
succeeded=1
