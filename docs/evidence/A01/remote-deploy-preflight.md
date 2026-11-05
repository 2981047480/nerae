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
The later read-only capacity check reported about 7.5 GiB total memory,
6.0 GiB available memory, four CPUs and 21.3 GiB free on the root filesystem.
Available disk has fallen since the earlier check; the cause has not been
established and no ambiguous test directory was removed. Recheck resource
headroom and retention before provisioning long-lived services.

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

## 2026-11-05 release preflight

The QQ SMTP settings were restored locally in the Git-ignored main-project
`.env`, with owner-only permissions. A TLS SMTP authentication check passed
without sending a message; no credential was printed or transferred to the
host.

The server reported about 6.3 GiB free on its 59 GiB root filesystem, down from
the earlier reading. Caddy remains active on ports 80/443 and the unrelated
`127.0.0.1:18080` listener remains occupied. Ports 18081 and 18101 were not
listening at the time of the check; they have not been reserved. The checked
Nerae service/data directories were absent. A read-only disk inventory found
substantial existing non-Nerae data under `/var/lib` and approximately 1.3 GiB
in `/tmp/nerae-package-preflight.J47rlPJK`; nothing was deleted. The cause of
the change in available space has not been established.

An isolated, owner-only host directory
`/tmp/nerae-release-preflight.h02Nz8zI` contains a 21 MiB server release
candidate built from commit `cab5404`. The API source, migrations and
then-current deployment-preflight inputs matched that integration version. Archive and
package checksums passed on the server, all three binaries were identified as
Linux x86-64, and `nerae-migrate check` validated all 18 migration entries.
This check did not connect to a database, transfer SMTP credentials, start
an API or worker, change Caddy, or create a public route.

The proxy examples changed after that candidate was packaged: the staged
archive contains the older direct-to-API gateway fragment and does not contain
the new private-sidecar example. Do not use its proxy fragment for deployment;
build and verify a new release with the reviewed gateway configuration first.

After the user approved the exact target, the old synthetic test directory
`/tmp/nerae-package-preflight.J47rlPJK` was removed. The check immediately
before removal found no processes using it or mounts inside it; approximately
1.32 GiB was recovered and root filesystem free space reached 7.50 GiB. The
newer release archive in `/tmp/nerae-release-preflight.h02Nz8zI` remains and
its SHA-256 still matches; the existing Caddy service remained active. An
initial retention probe checked a nonexistent `release` subdirectory; a second
check identified the actual `package` directory and verified the archive.

The live Caddy configuration and admin endpoint were inspected without
changing them. An unrelated local UID could read the admin configuration over
loopback. A proxy credential in that configuration would therefore be
exposed, and public routing and source-IP trust also depend on protecting the
live admin boundary. No admin write was attempted.

An isolated two-Caddy smoke test ran on the target host with synthetic
credentials, separate temporary UIDs, a
restricted Unix socket and loopback backend. Forged duplicate credential and
source-IP headers were replaced, the health route was denied, a non-target
route retained its isolated behavior, an unrelated UID could not access the
socket, and the front UID could not read the sidecar config. Both temporary
Caddy admin APIs and config autosave were disabled. The test passed again with
the socket directory restricted to mode `2750`, then removed only its own
temporary files. It did not modify the live Caddy service, establish real-site
coexistence, prove the live admin boundary safe, or expose the Nerae API.

The [retained smoke script](deployment-preflight/proxy-isolation-smoke.py) was
then changed to exercise the checked-in configuration examples directly. A
subsequent attempt on the host stopped at its minimum-free-space guard before
starting either test proxy: free space had fallen again, from 7.50 GiB after
the authorized cleanup to 4.50 GiB.
The read-only inventory showed `/var/lib` at about 27 GiB and `/tmp` at about
129 MiB; it did not establish what caused the space change. A further read-only
directory-timestamp and short-term trend query was not executed because the
automatic approval service returned HTTP 403. No other project's data was
removed, and the database persistence test was not started.

A later approval retry succeeded without bypassing the review. The root
filesystem then had approximately 4.49 GiB free and was stable during a
five-second sample. A separate project's approximately 1.5 GiB backup/restore
directory had a recent modification time, but these observations do not
establish the full cause of the preceding space decrease.

The template-based proxy test was then bounded to 1 MiB per child-process file
and required at least 4 GiB free for this short, non-database check. It passed
on the host using the actual checked-in Caddy examples and cleaned only its
own synthetic files. The database persistence test's higher space guard was
not changed, and that test remains unrun. Live Caddy coexistence and its admin
boundary remain unresolved.

A replacement Linux x86-64 candidate was built locally from commit `728db31`
at `/private/tmp/nerae-real-deploy-candidate.20261105/out3/nerae-linux-amd64-728db3118895.tar.gz`.
Its SHA-256 is `a1629a849ff6c35ccf8bf19eb9d5977f875c91d8142582be0cc55526b33c9dcc`.
Archive member safety, all packaged checksums and byte-for-byte agreement of
both proxy examples with that commit passed an offline inspection. It contains
no environment file, has not been copied to the host, and has not been started
or checked against a real database. It does not resolve the deployment gates.

The current IP certificate was accepted by this Mac's trust store on
2026-11-05 and is valid from 2026-11-03 20:11:57 UTC through 2026-11-10
12:11:56 UTC. Automatic renewal and iPhone trust remain unverified.
