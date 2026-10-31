# Pending approval list foundation

## Status

Internal storage primitive implemented and verified against the isolated MySQL harness. It is not mounted to HTTP and is not a complete device-confirmation flow.

## Behavior

`approval.Store.ListPending` accepts only a transaction-bound `session.Principal`; callers cannot supply a subject ID. It revalidates the principal in the same transaction, checks the approval key epoch, locks and checks the active account versions, and confirms the authenticated session remains active. It reads pending rows only for that subject, with current token/identity versions, an active email identity, and a database-UTC expiry later than the current time.

The result contains only `id`, `comparisonCode`, `createdAt`, and `expiresAt`. Rows use stable ascending `(created_at, id)` order. The internal maximum is 100. The query reads 101 rows to detect overflow; overflow returns `ErrUnavailable` and no partial items. A second database UTC check rejects an item that expired while row locks were being acquired. Approval rows remain locked until the caller commits or rolls back. A future HTTP caller must release the candidate response only after that same transaction commits, and any subsequent action must independently reauthorize.

The query filters by authenticated subject ID and does not restrict rows to the current session identity, matching the intended same-subject listing rule in `docs/APP_FLOW.md` ("三端身份与恢复", lines 114–116). Cross-identity sessions were not exercised: this test fixture creates only email sessions, and `identity_active_kind` prevents two active email identities on one subject. Therefore cross-identity listing is not claimed as verified. Cross-identity decisions remain unsupported: `Store.Decide` still requires the source session identity to equal the approval's originating identity. No decision behavior was changed here.

Returned IDs are checked against `AuthOpaqueId` length and ASCII URL-safe character constraints before serialization. Expiration uses the database's UTC clock in both the selection predicate and a post-lock recheck; the application `Store.now` clock is not used to decide list expiry, so application/DB clock skew does not change this result. The locking reads follow principal authentication's identity → user → device → session order, then recheck the account/session and lock approval plus originating-identity rows. Cross-identity deadlock behavior has not been proven by this card and requires integrated review; lock ordering was not changed.

## Verification

The private harness ran a fresh socket-only MySQL instance, applied these 12 migrations from this worktree: `auth/0001_device_proof_replay`, `auth/0002_email_challenge`, `user/0001_user`, `user/0002_age_declaration_version`, `auth/0003_email_identity`, `workout/0001_workout_session`, `workout/0002_workout_sets`, `auth/0004_auth_session`, `auth/0005_login_approval`, `auth/0006_approval_exchange`, `auth/0009_email_login_receipt`, and `workout/0003_workout_exercise_order`.

Command: `PYTHONDONTWRITEBYTECODE=1 python3 -B docs/evidence/A01/A01-09/mysql_smoke.py` — exit 0. The script ran `go test -race -count=1 -v ./internal/modules/auth/approval`; all three approval package integration tests passed, including the new list assertions. It removed the successful private database directory. Earlier sandboxed MySQL initialization failed with SIGSEGV; the same harness succeeded in the authorized local environment. Failed-run directories were retained by the harness.

A compile-only check also passed: `GOTOOLCHAIN=local GOPROXY=off GOSUMDB=off GOMODCACHE=/private/tmp/nerae-go-api-mod GOCACHE=/private/tmp/nerae-go-api-cache go test ./internal/modules/auth/approval -run '^$' -count=1`.

## Limitations

- No HTTP route, client, cross-identity decision path, or full confirmation product flow is implemented.
- The wire contract does not define pagination or overflow behavior; this primitive conservatively fails unavailable above 100 entries rather than silently truncating.
- Returning the list does not authorize approval, deny, session issuance, or device trust. `Decide` and later confirmation actions must perform their own current authorization checks.
- Verification used the 12 migration files listed above from baseline `ea6fe06`; this does not establish compatibility with a later integrated migration registry.
