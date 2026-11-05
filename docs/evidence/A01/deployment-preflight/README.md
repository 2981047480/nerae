# Nerae deployment candidate

Status: **offline preflight and deployment design only**. The candidate has not connected to Tencent Cloud,
started services, deployed a binary, sent SMTP, configured Caddy, or opened a
public route. No real credential is included.

## Contents

- `preflight.py`: checks the release migration manifest/checksums and the API
  and worker protected JSON files. It checks file owner/mode, exact JSON field
  sets against the release Go source, key encoding/uniqueness, API/worker shared
  epoch equality, policy bounds, and SMTP endpoint shape. It prints no values.
  - Secret-file owner/mode checks use `fstat` on the same descriptor that is
    read. Parent directories must be root-owned or owned by that service UID,
    not writable by group/others; only a root-owned sticky temporary ancestor
    is allowed. Treat parent ownership as trusted deployment state.
  - API policy bounds mirror `apiSecretFile.settings()` checks. SMTP sender
    address parsing is deliberately conservative for common ASCII mailboxes
    and may reject some values accepted by Go's full mail parser. The actual
    API and worker startup validators remain authoritative.
- `tests/test_preflight.py`: tests only generated synthetic keys, mailbox
  strings and configs. It also verifies that the release's real migration
  manifest is checksum-consistent without connecting to a database.
- `caddy-route.example`: a candidate fragment for a reviewed merge into the
  existing Caddy site block. It forwards over a restricted Unix socket and
  contains no proxy credential. It is not a standalone site config.
- `caddy-sidecar.example`: a template for a separate private Caddy process
  which reads the proxy credential and adds it only on the loopback API hop.
  Its admin endpoint and config autosave are disabled. Its generated config
  must be unreadable by the public gateway and other service users.
- `proxy-isolation-smoke.py`: isolated synthetic front/sidecar check. It needs
  root on a host with Caddy in order to use separate test UIDs, binds only a
  temporary loopback listener and Unix socket, and cleans up its own test
  directory on success. It requires at least 4 GiB free and limits each child
  process file to 1 MiB; failed test directories are retained for diagnosis.
  This small proxy test does not relax the separate database test's space guard.
- `deployment-plan.md`: service isolation, ordered rollout, blockers and
  rollback boundaries.

## Offline preflight

From this candidate directory, point at a clean release tree and the two
protected settings files. Supply the Unix UIDs that will run the API and worker:

```sh
python3 preflight.py \
  --release-root /srv/nerae/releases/RELEASE_ID \
  --api-secrets /etc/nerae/api/secrets.json --api-uid API_UID \
  --worker-secrets /etc/nerae/worker/secrets.json --worker-uid WORKER_UID
```

The files must be regular, non-symlink files owned by their respective service
UIDs and have no group/other permissions. Their parent directories must not be
group/world writable (except a sticky temporary ancestor). The tool reads the
files in memory, emits only pass/fail classifications, and makes no network
connections. Do not enable shell tracing or redirect secret-bearing process
data to logs. This check does not replace the binaries' own startup validation. It only checks the files opened during this run; a later file replacement must be prevented by trusted directory ownership and checked again at startup.

The live host's existing Caddy admin API was found readable by an unrelated
local UID. Placing the proxy credential in that gateway's config would expose
it through the admin API; even with the separate sidecar, gateway route and
source-IP trust still depend on protecting the existing admin API. The
isolated smoke test does not alter or validate that live management boundary.

Run its synthetic tests with:

```sh
NERAE_SOURCE_ROOT=/path/to/nerae-release python3 -m unittest discover -s tests -v
```

The tests build isolated synthetic JSON and a temporary copy of migration
inputs. They do not use a QQ account, real server config, database, Redis or
SMTP.
