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
