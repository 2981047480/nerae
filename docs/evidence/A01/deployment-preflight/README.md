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
  existing Caddy site block. It is not a standalone site config.
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

Run its synthetic tests with:

```sh
NERAE_SOURCE_ROOT=/path/to/nerae-release python3 -m unittest discover -s tests -v
```

The tests build isolated synthetic JSON and a temporary copy of migration
inputs. They do not use a QQ account, real server config, database, Redis or
SMTP.
