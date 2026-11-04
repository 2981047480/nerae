#!/usr/bin/env python3
"""Offline deployment preflight for the Nerae API/worker release bundle.

This reads protected settings in memory and emits classifications only. It
never connects to a database, SMTP server, Redis, Caddy, or a remote host.
"""

from __future__ import annotations

import argparse
import base64
import hashlib
import ipaddress
import json
import os
from pathlib import Path
import re
import stat
import sys
from urllib.parse import urlsplit


class PreflightError(Exception):
    pass


def fail(message: str) -> None:
    raise PreflightError(message)


def reject_duplicate_pairs(pairs):
    result = {}
    for key, value in pairs:
        if key in result:
            raise ValueError("duplicate member")
        result[key] = value
    return result


def read_regular_file(path: Path, limit: int, expected_uid: int | None = None,
                      owner_only: bool = False) -> tuple[bytes, os.stat_result]:
    before = path.lstat()
    if not stat.S_ISREG(before.st_mode) or stat.S_ISLNK(before.st_mode) or before.st_size > limit:
        fail("file type or size rejected")
    flags = os.O_RDONLY | getattr(os, "O_NOFOLLOW", 0) | getattr(os, "O_CLOEXEC", 0) | getattr(os, "O_NONBLOCK", 0)
    fd = os.open(path, flags)
    try:
        opened = os.fstat(fd)
        if (not stat.S_ISREG(opened.st_mode) or opened.st_dev != before.st_dev or opened.st_ino != before.st_ino or
                opened.st_size > limit):
            fail("file changed during read")
        if owner_only and (opened.st_uid != expected_uid or opened.st_mode & 0o077 or not opened.st_mode & 0o400):
            fail("secret file owner or permissions rejected")
        chunks = []
        total = 0
        while total <= limit:
            chunk = os.read(fd, min(65536, limit + 1 - total))
            if not chunk:
                break
            chunks.append(chunk)
            total += len(chunk)
        if total > limit:
            fail("file size limit exceeded")
        return b"".join(chunks), opened
    finally:
        os.close(fd)


def load_json(path: Path, label: str, limit: int, expected_uid: int) -> tuple[dict, os.stat_result]:
    try:
        if not path.is_absolute():
            fail(f"{label}: absolute path required")
        for parent in path.parents:
            parent_stat = parent.lstat()
            if not stat.S_ISDIR(parent_stat.st_mode) or stat.S_ISLNK(parent_stat.st_mode):
                fail(f"{label}: unsafe parent directory")
            if parent_stat.st_uid not in (0, expected_uid):
                fail(f"{label}: untrusted parent owner")
            if parent_stat.st_mode & 0o022 and not (parent_stat.st_uid == 0 and parent_stat.st_mode & stat.S_ISVTX):
                fail(f"{label}: writable parent directory")
        raw, opened_stat = read_regular_file(path, limit, expected_uid=expected_uid, owner_only=True)
        raw.decode("utf-8")
        value = json.loads(raw, object_pairs_hook=reject_duplicate_pairs)
    except PreflightError:
        raise
    except Exception:
        fail(f"{label}: unreadable or invalid JSON")
    if not isinstance(value, dict):
        fail(f"{label}: top-level object required")
    return value, opened_stat


def json_fields_from_go(source: Path, type_name: str) -> set[str]:
    try:
        raw, _ = read_regular_file(source, 2 << 20)
        text = raw.decode("utf-8")
    except Exception:
        fail("source schema unavailable")
    match = re.search(r"type\s+" + re.escape(type_name) + r"\s+struct\s*\{([^}]*)\}", text, re.S)
    if not match:
        fail("source schema unavailable")
    fields = set(re.findall(r'json:"([^",]+)', match.group(1)))
    if not fields or "-" in fields:
        fail("source schema unavailable")
    return fields


def check_config_file(path: Path, label: str, uid: int, source: Path, type_name: str,
                      limit: int) -> dict:
    value, _opened_stat = load_json(path, label, limit, uid)
    # This is the fstat result from the descriptor that supplied `value`.
    if (_opened_stat.st_uid != uid or _opened_stat.st_mode & 0o077 or
            not _opened_stat.st_mode & 0o400 or not stat.S_ISREG(_opened_stat.st_mode)):
        fail(f"{label}: secret file owner or permissions rejected")
    expected = json_fields_from_go(source, type_name)
    if set(value) != expected:
        fail(f"{label}: fields do not match release source")
    return value


def decode_key(value, label: str) -> bytes:
    if not isinstance(value, str) or not re.fullmatch(r"[A-Za-z0-9_-]{43}", value):
        fail(f"{label}: key encoding rejected")
    try:
        decoded = base64.urlsafe_b64decode(value + "=")
    except Exception:
        fail(f"{label}: key encoding rejected")
    if len(decoded) != 32 or base64.urlsafe_b64encode(decoded).decode().rstrip("=") != value or not any(decoded):
        fail(f"{label}: key encoding rejected")
    return decoded


def key_id(value) -> bool:
    return isinstance(value, str) and 1 <= len(value) <= 64 and re.fullmatch(r"[A-Za-z0-9_.-]+", value) is not None


def check_api(value: dict) -> dict[str, bytes]:
    key_fields = {name for name in value if name.endswith("_key") or name == "proxy_credential"}
    if len(key_fields) < 20:
        fail("API settings: key contract changed")
    decoded = {name: decode_key(value[name], "API settings") for name in key_fields}
    if len({item for item in decoded.values()}) != len(decoded):
        fail("API settings: key reuse rejected")
    id_fields = {name for name in value if name.endswith("_key_id")}
    if not id_fields or any(not key_id(value[name]) for name in id_fields):
        fail("API settings: key ID rejected")
    if not isinstance(value["trusted_proxy_prefixes"], list) or not 1 <= len(value["trusted_proxy_prefixes"]) <= 16:
        fail("API settings: trusted proxy list rejected")
    try:
        for raw in value["trusted_proxy_prefixes"]:
            if not isinstance(raw, str) or "%" in raw or str(ipaddress.ip_network(raw, strict=True)) != raw:
                fail("API settings: trusted proxy prefix is not canonical")
        prefixes = [ipaddress.ip_network(raw, strict=True) for raw in value["trusted_proxy_prefixes"]]
        if len(set(prefixes)) != len(prefixes):
            fail("API settings: duplicate trusted proxy prefix")
        permitted = [ipaddress.ip_network(x) for x in (
            "127.0.0.0/8", "10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16",
            "169.254.0.0/16", "::1/128", "fc00::/7", "fe80::/10",
        )]
        if any(getattr(prefix.network_address, "ipv4_mapped", None) is not None or
               not any(prefix.version == net.version and prefix.subnet_of(net) for net in permitted)
               for prefix in prefixes):
            fail("API settings: trusted proxy prefix is not private/local")
    except ValueError:
        fail("API settings: trusted proxy prefix rejected")
    int_fields = {name for name in value if name.endswith("_seconds") or name.endswith("_limit") or name.endswith("_attempts") or
                  name == "email_code_digits" or name == "rate_ipv6_prefix_bits"}
    for name in int_fields:
        if not isinstance(value[name], int) or isinstance(value[name], bool):
            fail("API settings: numeric policy rejected")
    ranges = {
        "challenge_ttl_seconds": (1, 1800), "challenge_max_attempts": (1, 10),
        "proof_lifetime_seconds": (1, 1800), "proof_clock_skew_seconds": (0, 300),
        "challenge_receipt_seconds": (60, 86400), "login_receipt_seconds": (1, 86400),
        "approval_exchange_receipt_seconds": (1, 86400), "session_lifetime_seconds": (1, 2592000),
        "workout_receipt_seconds": (1, 2592000), "approval_lifetime_seconds": (1, 1800),
        "approval_reauth_seconds": (1, 86400), "email_code_digits": (6, 10),
        "rate_ipv6_prefix_bits": (16, 128),
    }
    for name, (low, high) in ranges.items():
        if not low <= value[name] <= high:
            fail("API settings: duration or proof policy rejected")
    windows = [name for name in value if name.startswith("rate_") and name.endswith("_window_seconds")]
    if len(windows) != 4 or any(not 1 <= value[name] <= 86400 for name in windows):
        fail("API settings: rate window rejected")
    if value["rate_approval_window_seconds"] < value["approval_lifetime_seconds"]:
        fail("API settings: approval rate window is shorter than approval lifetime")
    if not 1 <= value["rate_approval_attempt_limit"] <= 10:
        fail("API settings: approval attempt limit rejected")
    limits = [name for name in value if name.startswith("rate_") and name.endswith("_limit")]
    if len(limits) != 15 or any(not 1 <= value[name] <= 1000000 for name in limits):
        fail("API settings: rate limit rejected")
    return decoded


def check_worker(value: dict) -> dict[str, bytes]:
    for name in ("smtp_address", "smtp_username", "smtp_password", "smtp_from"):
        if not isinstance(value[name], str) or not value[name] or any(c in value[name] for c in "\r\n\x00"):
            fail("Worker settings: SMTP fields rejected")
    try:
        endpoint = urlsplit("smtp://" + value["smtp_address"])
        port = endpoint.port
    except Exception:
        port = None
        endpoint = None
    if (not endpoint or not endpoint.hostname or not port or not 1 <= port <= 65535 or endpoint.username or
            endpoint.password or endpoint.path or endpoint.query or endpoint.fragment or
            any(c.isspace() or ord(c) < 32 for c in endpoint.hostname)):
        fail("Worker settings: SMTP endpoint rejected")
    if (len(value["smtp_username"].encode("utf-8")) > 254 or
            len(value["smtp_password"].encode("utf-8")) > 4096 or
            len(value["smtp_from"].encode("utf-8")) > 254 or not conservative_mailbox(value["smtp_from"])):
        fail("Worker settings: SMTP identity or credential rejected")
    if not isinstance(value["smtp_timeout_ms"], int) or isinstance(value["smtp_timeout_ms"], bool) or not 1 <= value["smtp_timeout_ms"] <= 60000:
        fail("Worker settings: timeout rejected")
    keys = ("email_identity_index_key", "email_identity_encryption_key", "email_code_hash_key", "email_code_derivation_key")
    decoded = {name: decode_key(value[name], "Worker settings") for name in keys}
    if len(set(decoded.values())) != len(decoded):
        fail("Worker settings: key reuse rejected")
    for name in ("email_identity_key_id", "email_code_key_id", "email_code_hash_key_id"):
        if not key_id(value[name]):
            fail("Worker settings: key ID rejected")
    bounds = {
        "poll_interval_ms": (100, 60000), "recovery_batch": (1, 500),
        "lease_ms": (1000, 600000), "startup_timeout_ms": (1000, 30000),
    }
    for name, (low, high) in bounds.items():
        if not isinstance(value[name], int) or isinstance(value[name], bool) or not low <= value[name] <= high:
            fail("Worker settings: worker policy rejected")
    return decoded


def conservative_mailbox(value: str) -> bool:
    # Deliberately accepts the common unquoted ASCII mailbox form. This is
    # stricter than Go net/mail's full grammar; the Go startup check remains
    # authoritative for accepted input.
    if any(ord(char) <= 32 or ord(char) >= 127 for char in value) or value.count("@") != 1:
        return False
    local, domain = value.split("@", 1)
    allowed_local = set("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789.!#$%&'*+-/=?^_`{|}~")
    if (not local or not domain or any(char not in allowed_local for char in local) or local.startswith(".") or
            local.endswith(".") or ".." in local or domain.startswith(".") or domain.endswith(".") or ".." in domain):
        return False
    labels = domain.split(".")
    return all(label and len(label) <= 63 and label[0].isalnum() and label[-1].isalnum() and
               all(char.isascii() and (char.isalnum() or char == "-") for char in label)
               for label in labels)


def verify_migrations(root: Path) -> int:
    manifest_path = root / "scripts/dev/migrations.json"
    migrations_dir = root / "apps/api/migrations"
    try:
        if not root.is_absolute() or root.is_symlink():
            fail("release migration inputs rejected")
        for relative in ("scripts", "scripts/dev", "apps", "apps/api", "apps/api/migrations"):
            directory = root / relative
            info = directory.lstat()
            if not stat.S_ISDIR(info.st_mode) or stat.S_ISLNK(info.st_mode):
                fail("release migration inputs rejected")
        if manifest_path.is_symlink() or not manifest_path.is_file():
            fail("release migration inputs rejected")
        manifest_raw, _ = read_regular_file(manifest_path, 4 << 20)
        manifest_raw.decode("utf-8")
        manifest = json.loads(manifest_raw, object_pairs_hook=reject_duplicate_pairs)
        if set(manifest) != {"version", "migrations"}:
            fail("release migration manifest rejected")
        entries = manifest["migrations"]
        if type(manifest["version"]) is not int or manifest["version"] != 1 or not isinstance(entries, list) or not entries:
            fail("release migration manifest rejected")
        registered = set()
        for index, entry in enumerate(entries, 1):
            if set(entry) != {"order", "id", "module", "file", "sha256"}:
                fail("release migration entry rejected")
            module, filename = entry["module"], entry["file"]
            if (type(entry["order"]) is not int or entry["order"] != index or
                    not re.fullmatch(r"[a-z][a-z0-9_]{0,31}", module) or
                    not re.fullmatch(r"[0-9]{4}_[a-z0-9_]+\.up\.sql", filename) or
                    entry["id"] != module + "/" + filename):
                fail("release migration entry rejected")
            path = migrations_dir / module / filename
            if path.parent.is_symlink() or not path.resolve().is_relative_to(migrations_dir.resolve()):
                fail("release migration file rejected")
            raw_sql, _ = read_regular_file(path, 4 << 20)
            digest = hashlib.sha256(raw_sql).hexdigest()
            if digest != entry["sha256"]:
                fail("release migration checksum mismatch")
            registered.add(path.resolve())
        for path in migrations_dir.rglob("*"):
            if path.is_symlink():
                fail("release migration tree contains symlink")
            if path.is_file() and path.name.endswith(".up.sql") and path.resolve() not in registered:
                fail("release migration tree has unregistered SQL")
        return len(entries)
    except PreflightError:
        raise
    except Exception:
        fail("release migration inputs rejected")


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--release-root", required=True, type=Path)
    parser.add_argument("--api-secrets", required=True, type=Path)
    parser.add_argument("--api-uid", required=True, type=int)
    parser.add_argument("--worker-secrets", required=True, type=Path)
    parser.add_argument("--worker-uid", required=True, type=int)
    args = parser.parse_args()
    try:
        root = args.release_root
        count = verify_migrations(root)
        api = check_config_file(args.api_secrets, "API settings", args.api_uid,
                                root / "apps/api/cmd/api/protected_config.go", "apiSecretFile", 64 * 1024)
        worker = check_config_file(args.worker_secrets, "Worker settings", args.worker_uid,
                                   root / "apps/api/cmd/worker/protected_config.go", "workerSecrets", 32 * 1024)
        api_keys = check_api(api)
        worker_keys = check_worker(worker)
        pairs = {
            "email_identity_index_key": "email_identity_index_key",
            "email_identity_encryption_key": "email_identity_encryption_key",
            "email_code_hash_key": "email_code_hash_key",
            "email_code_derivation_key": "email_code_derivation_key",
        }
        for api_name, worker_name in pairs.items():
            if api_keys[api_name] != worker_keys[worker_name]:
                fail("API/worker shared key mismatch")
        for api_name, worker_name in (("email_identity_key_id", "email_identity_key_id"),
                                      ("email_code_hash_key_id", "email_code_hash_key_id"),
                                      ("email_code_key_id", "email_code_key_id")):
            if api[api_name] != worker[worker_name]:
                fail("API/worker shared key ID mismatch")
        print(f"PASS: offline contract check; {count} migrations; API and worker settings match shared epochs")
        print("LIMIT: no DB/Redis/SMTP connection, backup check, service start, Caddy check, or public route test")
        return 0
    except PreflightError as exc:
        print(f"FAIL: {exc}", file=sys.stderr)
        return 1
    except Exception:
        print("FAIL: preflight internal validation error", file=sys.stderr)
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
