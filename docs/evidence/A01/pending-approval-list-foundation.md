# Pending approval list foundation

## Status

Internal storage primitive implemented and verified against the isolated MySQL harness. It is not mounted to HTTP and is not a complete device-confirmation flow.

## Behavior

`approval.Store.ListPending` accepts a database, the real `session.Store`, and a bearer token; callers cannot supply a subject ID or transaction. It opens and owns the transaction, authenticates the token, checks the approval key epoch, locks and checks active account versions and the authenticated session, reads the list internally, and returns serializable list data only after `Commit` succeeds. The package-private transaction reader cannot be called by production code in another package.

The result contains only `id`, `comparisonCode`, `createdAt`, and `expiresAt`. Rows use stable ascending `(created_at, id)` order. The internal maximum is 100. The query reads 101 rows to detect overflow; overflow returns `ErrUnavailable` and no partial items. Expiry selection and the post-lock recheck both use the approval store's application clock, matching `BeginEmail` and `Decide`. If that clock moves backwards while the query waits, the method fails unavailable. The store owns the read transaction and returns no list if commit fails. A later action must independently reauthorize because a list is informational and can become stale immediately after its transaction commits.

The query filters by authenticated subject ID and does not restrict rows to the current session identity, matching the intended same-subject listing rule in `docs/APP_FLOW.md` ("三端身份与恢复", lines 114–116). A synthetic `wechat_mini` identity and session were inserted in the isolated database and authenticated through the real `session.Store`; this verifies the storage/authentication path across two identity rows, but not a real WeChat account, provider callback, or identity-link flow. Cross-identity decisions remain unsupported: `Store.Decide` still requires the source session identity to equal the approval's originating identity. No decision behavior was changed here.

Returned IDs are checked against `AuthOpaqueId` length and ASCII URL-safe character constraints before serialization. The list does not lock a historical originating identity after principal authentication: authentication locks current identity → user → device → session, while `ResolveEmail` locks email identity → user. The list filters approval rows by the current user `identity_version`, avoiding that second identity lock. An audit of non-test auth code found only `identity.Resolver.create` inserts a new email identity together with its newly created user in the same transaction; there is no implemented identity link, revoke, or unlink mutation path to bump an existing user's version. The synthetic linked-identity fixture increments `identity_version` in the same transaction as identity insertion, and a test confirms a changed version hides older approvals. Any future identity mutation path must lock the user and increment `identity_version` atomically before it can rely on this filter. Direct database writes are outside that guarantee. A focused MySQL lock-order test holds the email identity then waits for the shared user while a synthetic different-identity session lists; the list completes without waiting on the email identity. This does not replace full integration review of future link/revoke transactions.

## Verification

The private harness ran a fresh socket-only MySQL instance, applied these 12 migrations from this worktree: `auth/0001_device_proof_replay`, `auth/0002_email_challenge`, `user/0001_user`, `user/0002_age_declaration_version`, `auth/0003_email_identity`, `workout/0001_workout_session`, `workout/0002_workout_sets`, `auth/0004_auth_session`, `auth/0005_login_approval`, `auth/0006_approval_exchange`, `auth/0009_email_login_receipt`, and `workout/0003_workout_exercise_order`.

Command: `PYTHONDONTWRITEBYTECODE=1 python3 -B docs/evidence/A01/A01-09/mysql_smoke.py` — exit 0. The script ran `go test -race -count=1 -v ./internal/modules/auth/approval`; all three approval package integration tests passed, including the new list assertions. It removed the successful private database directory. Earlier sandboxed MySQL initialization failed with SIGSEGV; the same harness succeeded in the authorized local environment. Failed-run directories were retained by the harness.

A compile-only check also passed: `GOTOOLCHAIN=local GOPROXY=off GOSUMDB=off GOMODCACHE=/private/tmp/nerae-go-api-mod GOCACHE=/private/tmp/nerae-go-api-cache go test ./internal/modules/auth/approval -run '^$' -count=1`.

## Limitations

- No HTTP route, client, cross-identity decision path, or full confirmation product flow is implemented.
- The wire contract does not define pagination or overflow behavior; this primitive conservatively fails unavailable above 100 entries rather than silently truncating.
- Returning the list does not authorize approval, deny, session issuance, or device trust. `Decide` and later confirmation actions must perform their own current authorization checks.
- Verification used the 12 migration files listed above from baseline `ea6fe06`; this does not establish compatibility with a later integrated migration registry.
