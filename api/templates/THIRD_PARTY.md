# Third-party attribution

`oapi-imports.tmpl` is adapted from `pkg/codegen/templates/imports.tmpl` in `github.com/oapi-codegen/oapi-codegen/v2` v2.4.1. The original project's Apache-2.0 license is reproduced in `LICENSE.oapi-codegen`. The Nerae modification redirects the nullable import to its local encoding guard.

`apps/api/internal/contractnullable/nullable.go` uses the tri-state map representation described by `github.com/oapi-codegen/nullable` v1.1.0. Its original copyright and license notice is reproduced in `NOTICE.nullable`; the local implementation differs by rejecting unspecified required values during JSON encoding.
