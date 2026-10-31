# API business process and protected configuration probe

On 2026-10-31 the API process candidate was tested with explicitly provisioned synthetic settings. Business mode initializes its existing handler and key epochs before opening a loopback-only listener. Production refuses health-only mode. The settings reader requires an absolute, regular, owner-only file, rejects a final-component symlink or FIFO, bounds its size, and rejects ambiguous JSON, missing fields, zero/reused/noncanonical keys and invalid policy values. No real secrets were used.

The business endpoint boundary additionally requires one independently generated proxy credential, checked before the business handler and stripped before forwarding. Missing, wrong, repeated, malformed or noncanonical values are rejected. This credential is distinct from every business key. API configuration must remain readable only by the API account; the proxy needs a separately protected copy or equivalent restricted secret injection, not read access to the full API secret file. The reverse proxy must overwrite both the credential and trusted source-IP headers, protect them from logs, and the deployment must protect the host and configuration parent directories. No real proxy configuration or rotation was tested here.

Verification on this isolated candidate:

```sh
GOTOOLCHAIN=local GOPROXY=off GOSUMDB=off \
  GOCACHE=/private/tmp/nerae-go-api-cache GOMODCACHE=/private/tmp/nerae-go-api-mod \
  /private/tmp/nerae-go-toolchain-1.27.3/go/bin/go test -mod=readonly -race ./cmd/api
PYTHONDONTWRITEBYTECODE=1 /Users/zephyr/.local/bin/python3.11 docs/evidence/A01/api-process/mysql_redis_smoke.py
```

Both exited 0. The second command used fresh local socket-only MySQL and Redis with synthetic data, invoked `run` in the Go test process, and exercised the real loopback HTTP listener. It rejected missing/wrong/duplicate proxy credentials and missing/duplicate/joined source-IP headers; with the expected credential and a valid source it accepted a challenge and observed one outbox row, then shut down cleanly. The test did not start a separate production binary or send an email. An initial Go test invocation failed because the default module cache was inaccessible in the sandbox; the successful command explicitly used the already provisioned local cache.

An independent security review identified the original unauthenticated loopback proxy boundary and the limitation that readiness checks dependency connectivity only. The authenticated boundary was added and retested as above. `/health/ready` is **not** a guarantee that all configured epochs remain valid after startup; runtime operations independently fail closed on their relevant epoch checks. Readiness remains a deployment-review item. Also unverified: HTTPS proxy behavior, remote database and Redis TLS handshakes, real SMTP, real credentials, old-instance removal, proxy secret rotation, production isolation, and installation on any device.
