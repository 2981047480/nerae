# Deployment candidate

This is a proposed path, not a deployed service. The latest read-only check
found Caddy running and an existing API listening on `127.0.0.1:18080`; do
not use that port or stop the existing service. The check did not establish
all container, database, storage, backup or certificate-renewal state.

Before exposing a route:

1. Recheck the host and reserve a free loopback port. Keep MySQL and Redis
   isolated, use dedicated data directories, and verify that the service
   identities can access only their required Unix sockets.
2. Preserve and verify a recoverable database backup, then check the release
   migration manifest and run the migration executor with a separate account.
   Do not automatically repeat a failed or dirty migration.
3. Configure separate owner-only API and worker secret files. Generate distinct
   keys; the email identity and code epochs shared by the processes must match.
   Add the QQ SMTP credential only through a protected channel when available.
   Run `preflight.py` and the Go binaries' own configuration checks.
4. Start only Nerae's worker and loopback API after dependencies are ready.
   Test authentication and database access locally before routing traffic.
5. Review the complete existing Caddy configuration, replace the placeholder
   in `caddy-route.example` with the reserved port, validate coexistence and
   reload only after a rollback configuration has been preserved. Verify the
   existing service, certificate renewal and iPhone HTTPS trust.

On failure, stop only Nerae-owned processes, restore the validated prior Caddy
configuration if it was changed, and preserve the data and protected key
backups. Do not stop unrelated services or delete data as a rollback shortcut.
Real SMTP delivery, backup restoration, account access and mobile sync still
require end-to-end verification.
