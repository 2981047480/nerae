# Remote deployment preflight

2026-11-04: the user-selected Tencent host was inspected read-only. Its Linux
x86-64 system has Docker and approximately 27 GiB free on the root filesystem.
The existing Caddy service owns ports 80 and 443 and already serves the public
IP; its HTTPS certificate was accepted by this Mac's normal trust store. The
current IP HTTPS route responds statically, so any Nerae route must be added
without changing other routes. iPhone trust and a live proxy configuration have
not been tested. No remote configuration or service was changed.

No MySQL/Redis image was found among the checked local Docker images. A
read-only Docker Hub manifest request timed out while waiting for registry
headers. Ubuntu's configured package metadata lists MySQL and Redis candidates;
availability, isolation and operational suitability of those packages have not
yet been established.

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
the extracted Ubuntu packages, the integration-test binary and migration tree.
After adding this argument check, the same host test was run again and passed.
No real SMTP
credentials, public listener, production data or iPhone were used. This does
not establish email delivery from the deployed service or cross-device sync.
