# Approval exchange receipt cryptographic primitive

## Scope

Added the isolated `approvalreceipt` package for bounded request HMACs and AES-256-GCM result sealing. It is a cryptographic building block only. It does not verify the device signature or claims, check current account/device/session authorization, consume or validate the JTI replay marker, coordinate SQL state, decide commit success, or authorize credential release.

The primitive currently supports only an `issued_session` receipt with a non-empty bounded plaintext and opaque session reference. Dynamic `pending`, `denied`, and `expired` poll results are intentionally excluded because their idempotency semantics must be specified by the exchange coordinator/API owner.

## Bound fields

- Idempotency HMAC: exact canonical base64url idempotency-key string, under its own domain and length prefix.
- Request HMAC: approval ID, canonical 32-byte approval secret string, and exact compact device-proof string, each length prefixed under a separate key/domain. Compact proof encoding is shape-checked only; no signature verification is claimed.
- GCM associated data: key ID, fixed 32-byte server-derived subject scope, fixed 32-byte server-derived approval scope, idempotency HMAC, request HMAC, approval ID, result type, opaque session reference, and UTC expiry to exact microsecond precision.
- Encryption: AES-256-GCM with a CSPRNG nonce. Persistable ciphertext framing is versioned and bounded; parse checks framing only and GCM authentication occurs in `Open`.

The three configured 32-byte keys must be nonzero and pairwise distinct. This validates configuration shape, not entropy. `EpochTag` returns a non-reversible verifier and does not initialize or rotate persistent state.

## Verification

Working directory: `/private/tmp/nerae-approval-list/apps/api`.

```sh
/private/tmp/nerae-go-toolchain-1.27.3/go/bin/gofmt -w internal/modules/auth/approvalreceipt/receipt.go internal/modules/auth/approvalreceipt/receipt_test.go
GOTOOLCHAIN=local GOPROXY=off GOSUMDB=off GOMODCACHE=/private/tmp/nerae-go-api-mod GOCACHE=/private/tmp/nerae-go-api-cache /private/tmp/nerae-go-toolchain-1.27.3/go/bin/go test ./internal/modules/auth/approvalreceipt
GOTOOLCHAIN=local GOPROXY=off GOSUMDB=off GOMODCACHE=/private/tmp/nerae-go-api-mod GOCACHE=/private/tmp/nerae-go-api-cache /private/tmp/nerae-go-toolchain-1.27.3/go/bin/go test -race ./internal/modules/auth/approvalreceipt
```

Both package test commands passed on 2026-10-31. Tests cover request-field separation, canonical input and scope validation, bounded plaintext/ciphertext, GCM round-trip, nonce distinction, changed-key behavior, epoch tags, random-source failure, ciphertext/AAD field tampering, parse-vs-authentication behavior, and String/GoString/JSON/slog redaction.

## Not implemented / risks

- No migration, epoch table integration, exchange transaction, HTTP route, or replay coordinator is included.
- `subjectScope` and `approvalScope` must be produced by trusted server code. The package checks their fixed size/nonzero encoding but cannot prove their provenance or that the scopes were correctly derived.
- `Open` does not enforce expiry. Its future caller must check DB UTC time and revalidate the original signed proof, JTI marker, approval/user/identity/device/session status and versions in a transaction before releasing any credential.
- No behavior is defined here for dynamic polling outcomes, commit acknowledgment loss, source-session revocation after successful exchange, or recovery after the original proof expires.
- The current `Exchanger.Exchange` and schema do not consume this package; this evidence does not establish idempotent exchange behavior or runtime integration.
