# Approval exchange receipt cryptographic primitive

## Scope

Added the isolated `approvalreceipt` package for bounded request HMACs and AES-256-GCM result sealing. It is a cryptographic building block only. It does not verify the device signature or claims, check current account/device/session authorization, consume or validate the JTI replay marker, coordinate SQL state, decide commit success, or authorize credential release.

The primitive currently supports only an `issued_session` receipt with a non-empty bounded plaintext and opaque session reference. Dynamic `pending`, `denied`, and `expired` poll results are intentionally excluded because their idempotency semantics must be specified by the exchange coordinator/API owner.

## Bound fields

- Idempotency HMAC: exact canonical base64url idempotency-key string, under its own domain and length prefix.
- Request HMAC: approval ID, canonical 32-byte approval secret string, and exact compact device-proof string, each length prefixed under a separate key/domain. Compact proof encoding is shape-checked only; no signature verification is claimed.
- Subject and approval scopes are distinct opaque Go types derived by domain-separated HMAC using the request key. Subject IDs must be canonical positive decimal values; approval IDs use the opaque-ID alphabet. The approval scope is checked against the approval ID in the binding.
- GCM associated data: key ID, the derived subject and approval scopes, idempotency HMAC, request HMAC, approval ID, result type, opaque session reference, and UTC expiry to exact microsecond precision.
- Encryption: AES-256-GCM with a CSPRNG nonce. Persistable ciphertext framing is versioned and bounded; parse checks framing only and GCM authentication occurs in `Open`.
- `Seal` and `Open` require a `DatabaseUTC` obtained by `ReadDatabaseUTC` from `UTC_TIMESTAMP(6)` in the same caller transaction. Both re-read the DB clock after current-session verification to reject expiry after a verifier lock wait.
- `Seal` and `Open` require a `CurrentSessionVerifier`; its contract requires session-token codec verification plus a live database check in the provided transaction. The primitive checks that verified subject derives the stored subject scope, that the verified session ID matches the result reference, that token expiry matches the current DB session expiry, and that receipt expiry does not exceed session expiry.

The three configured 32-byte keys must be nonzero and pairwise distinct. This validates configuration shape, not entropy. `EpochTag` returns a non-reversible verifier and does not initialize or rotate persistent state.

## Verification

Working directory: `/private/tmp/nerae-approval-list/apps/api`.

```sh
/private/tmp/nerae-go-toolchain-1.27.3/go/bin/gofmt -w internal/modules/auth/approvalreceipt/receipt.go internal/modules/auth/approvalreceipt/receipt_test.go
GOTOOLCHAIN=local GOPROXY=off GOSUMDB=off GOMODCACHE=/private/tmp/nerae-go-api-mod GOCACHE=/private/tmp/nerae-go-api-cache /private/tmp/nerae-go-toolchain-1.27.3/go/bin/go test ./internal/modules/auth/approvalreceipt
GOTOOLCHAIN=local GOPROXY=off GOSUMDB=off GOMODCACHE=/private/tmp/nerae-go-api-mod GOCACHE=/private/tmp/nerae-go-api-cache /private/tmp/nerae-go-toolchain-1.27.3/go/bin/go test -race ./internal/modules/auth/approvalreceipt
```

Both package test commands passed on 2026-10-31. Tests cover HMAC-derived scopes and raw-hash distinction, request-field separation, canonical input and scope validation, DB-clock query parsing, expiry checks, verifier subject/session/expiry mismatches, bounded token/ciphertext, GCM round-trip, nonce distinction, changed-key behavior, epoch tags, random-source failure, ciphertext/AAD field tampering, parse-vs-authentication behavior, and String/GoString/JSON/slog redaction.

## Not implemented / risks

- No migration, epoch table integration, exchange transaction, HTTP route, or replay coordinator is included.
- The Go types and private fields prevent callers from supplying a raw enumerable hash as a scope; scope HMACs are keyed with the request HMAC key under distinct derivation domains. Entropy and secret storage remain configuration responsibilities.
- `ReadDatabaseUTC` reads the authoritative MySQL clock, but its SQL behavior was unit-tested with a small in-process SQL driver, not a real MySQL integration test. The caller must use the same authoritative transaction and must not substitute a wall clock.
- `CurrentSessionVerifier` is an explicit trusted boundary, not an implementation of session verification. Tests use a synthetic verifier to test mismatch rejection; no real `session.TokenCodec` plus database verifier is connected or exercised here. Future adapters must verify the token cryptographically, compare token expiry to the current session row, and re-read active user, identity, device and session state in the supplied transaction.
- Before calling `Open`, the future coordinator must independently revalidate the original signed proof and its consumed JTI marker. The coordinator must commit its owning transaction before releasing returned plaintext; this primitive cannot enforce that sequencing.
- No behavior is defined here for dynamic polling outcomes, commit acknowledgment loss, source-session revocation after successful exchange, or recovery after the original proof expires.
- The current `Exchanger.Exchange` and schema do not consume this package; this evidence does not establish idempotent exchange behavior or runtime integration.
