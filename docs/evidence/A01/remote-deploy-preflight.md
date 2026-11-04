# Remote deployment preflight

2026-11-04: the user-selected Tencent host was inspected read-only. Its Linux
x86-64 system has Docker and approximately 27 GiB free on the root filesystem.
The existing Caddy service owns ports 80 and 443 and already serves the public
IP; its HTTPS certificate was accepted by this Mac's normal trust store. The
current IP HTTPS route responds statically, so any Nerae route must be added
without changing other routes. iPhone trust and a live proxy configuration have
not been tested. The observed certificate expires 2026-11-10 12:11:56 UTC;
automatic renewal has not been verified. No existing remote configuration or
service was changed.

On a later read-only check on 2026-11-04, Caddy was still active and the
previously suggested loopback port `127.0.0.1:18080` was already held by an
existing `api` process. Do not bind, stop, or reuse it for Nerae. A separate
free loopback port must be selected and reserved at deployment time. The same
check found no system `mysqld` or `redis-server` process; this is not a full
inventory of containers, sockets, or external services.
The paths `/srv/nerae`, `/var/lib/nerae`, `/etc/nerae` and `/run/nerae` were
absent on the same date, and no Nerae-named containers or systemd units were
listed. These are point-in-time checks, not permission to reuse a future path
without rechecking it.

An [offline deployment candidate](deployment-preflight/README.md) now checks
the release migration inputs and owner-only API/worker configuration, including
shared key epochs, without printing secrets or connecting to services. Eleven
synthetic tests passed. It does not provision the host, verify permissions on
running services, or replace startup and backup/restore verification.

No MySQL/Redis image was found among the checked local Docker images. A
read-only Docker Hub manifest request timed out while waiting for registry
headers. Ubuntu's configured package metadata lists MySQL and Redis candidates;
availability, isolation and operational suitability of those packages have not
yet been established.

Subsequently the host reached the project's pinned image digests through
[Tencent Cloud's internal Docker mirror](https://cloud.tencent.com/document/practice/1207/45596).
Both images were pulled into the host cache without restarting Docker. Their
isolated container verification is recorded in the
[migration evidence](production-migration-candidate.md); no persistent
production containers were started.

The integration source was cross-compiled locally to Linux x86-64 API and mail
worker executables with the pinned Go toolchain and existing module cache. These
executables were not copied to or run on the host. The API still needs isolated
database/Redis instances, migrations, protected configuration and a verified
proxy before real mobile login can be tested.

## Private host integration check

The host's configured Ubuntu package mirror supplied MySQL 8.0.46 and Redis
7.0.15 programs and their missing libraries into a new private temporary
directory; no packages were installed system-wide. The first synthetic MySQL
run failed because Ubuntu's default `secure-file-priv` directory did not exist.
The retry disabled file import/export and used a fresh private socket-only
database and Redis. The manifest's 18 migration checksums were verified, then
the cross-compiled `TestAPIProcessPrivateServices` test passed on the host:
API startup, a synthetic email-challenge request entering the outbox, rejection
of invalid proxy credentials/source headers, the approval-exchange route and
clean shutdown. The successful test's own synthetic database was removed; the
failed first test directory and the package preflight files were retained.

The repeatable harness is [remote-private-services-smoke.py](remote-private-services-smoke.py).
It expects an owner-only `/tmp/nerae-package-preflight.*` directory containing
the extracted Ubuntu packages, the API and migration integration-test binaries,
the migration executable and a clean release migration tree. The latest run
also exercised the new migration executor and session-drift protection; details
are in the [migration evidence](production-migration-candidate.md). Pass
`--containers` to use the pinned, network-isolated MySQL and Redis images; omit
it to use the extracted package binaries. No real SMTP
credentials, public listener, production data or iPhone were used. This does
not establish email delivery from the deployed service or cross-device sync.

On 2026-11-04 the pinned-container run was extended to a second fresh schema
and passed `TestBusinessAssemblyPrivateServices` on the Tencent host. Two
synthetic device keys completed the email-code and owner-approval flow; one
device created a workout, the other read it and saved a set, and the first read
the set. The code was derived in the test process, not delivered by QQ. The
assembly test now rejects any database name other than its dedicated synthetic
schema before connecting. The isolated services stopped and their successful
test directories were removed. This verifies the server assembly on the
target host; it does not demonstrate a deployed service or sync from either
real device.

The same synthetic business-assembly path was subsequently run using a fresh
database account with only database-level SELECT, INSERT, UPDATE and DELETE;
it had no DDL grants. The agent observed `SYNTHETIC_GRANT_PASS exit=0` from a
separately written status file; that file was removed after the successful run,
so no independent status artifact remains. This does not establish table-level
least privilege, worker permissions or coverage outside the tested path. The
repeatable harness now creates that restricted test account for this case.

## Candidate proxy behavior

The isolated [Caddy proxy test](caddy-proxy-boundary-smoke.py) passed on the
host using temporary loopback ports and a synthetic backend/credential. For the
candidate `/nerae-api-v1/` prefix it verified prefix removal, replacement of
duplicate forged proxy-credential and source-IP headers, and rejection of
health and unrelated paths. Its temporary processes and configuration were
removed on exit. This did not modify or validate coexistence with the existing
live Caddy site, check real HTTPS from an iPhone, or deploy the API.
