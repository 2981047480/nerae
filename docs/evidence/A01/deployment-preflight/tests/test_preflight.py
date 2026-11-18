from __future__ import annotations

import base64
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import sys
import tempfile
import unittest
from unittest import mock

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
import preflight


TOOL = Path(__file__).resolve().parents[1] / "preflight.py"
SOURCE_ROOT = Path(os.environ.get("NERAE_SOURCE_ROOT", "/Users/zephyr/.codex/worktrees/nerae-foundation-integration/nerae"))


def tags(path: Path, type_name: str) -> list[str]:
    source = path.read_text()
    match = re.search(r"type\s+" + re.escape(type_name) + r"\s+struct\s*\{([^}]*)\}", source, re.S)
    if not match:
        raise AssertionError("fixture source contract unavailable")
    return re.findall(r'json:"([^",]+)', match.group(1))


def encoded(byte: int) -> str:
    return base64.urlsafe_b64encode(bytes([byte]) * 32).decode().rstrip("=")


def fixtures():
    api_source = SOURCE_ROOT / "apps/api/cmd/api/protected_config.go"
    worker_source = SOURCE_ROOT / "apps/api/cmd/worker/protected_config.go"
    api_names = tags(api_source, "apiSecretFile")
    worker_names = tags(worker_source, "workerSecrets")
    api = {}
    byte = 1
    for name in api_names:
        if name.endswith("_key") or name == "proxy_credential":
            api[name] = encoded(byte)
            byte += 1
        elif name.endswith("_key_id"):
            api[name] = name.replace("_key_id", "") + "-v1"
    api.update({
        "trusted_proxy_prefixes": ["127.0.0.1/32", "::1/128"],
        "challenge_ttl_seconds": 300, "challenge_max_attempts": 3,
        "proof_lifetime_seconds": 300, "proof_clock_skew_seconds": 30,
        "challenge_receipt_seconds": 3600, "login_receipt_seconds": 120,
        "approval_exchange_receipt_seconds": 120, "session_lifetime_seconds": 3600,
        "workout_receipt_seconds": 86400, "approval_lifetime_seconds": 300,
        "approval_reauth_seconds": 60, "email_code_digits": 6, "rate_ipv6_prefix_bits": 64,
        "rate_challenge_window_seconds": 900, "rate_challenge_email_limit": 5,
        "rate_challenge_device_limit": 10, "rate_challenge_ip_limit": 30,
        "rate_challenge_global_limit": 1000, "rate_login_window_seconds": 900,
        "rate_login_email_limit": 10, "rate_login_device_limit": 20,
        "rate_login_ip_limit": 50, "rate_login_global_limit": 1000,
        "rate_approval_window_seconds": 300, "rate_approval_subject_limit": 10,
        "rate_approval_attempt_limit": 3, "rate_approval_ip_limit": 20,
        "rate_approval_global_limit": 1000, "rate_approval_exchange_window_seconds": 60,
        "rate_approval_exchange_capability_limit": 20, "rate_approval_exchange_ip_limit": 50,
        "rate_approval_exchange_global_limit": 1000,
    })
    worker = {
        "smtp_address": "smtp.example.test:465", "smtp_username": "synthetic@example.test",
        "smtp_password": "synthetic-only-not-real", "smtp_from": "synthetic@example.test",
        "smtp_timeout_ms": 5000, "poll_interval_ms": 500, "recovery_batch": 20,
        "lease_ms": 30000, "startup_timeout_ms": 5000,
    }
    for name in ("email_identity_index_key", "email_identity_encryption_key", "email_code_hash_key", "email_code_derivation_key"):
        worker[name] = api[name]
    for name in ("email_identity_key_id", "email_code_key_id", "email_code_hash_key_id"):
        worker[name] = api[name]
    if set(api) != set(api_names) or set(worker) != set(worker_names):
        raise AssertionError("fixture contract coverage incomplete")
    return api, worker


def copy_release(root: Path):
    shutil.copy2(SOURCE_ROOT / "scripts/dev/migrations.json", root / "scripts/dev/migrations.json")
    shutil.copytree(SOURCE_ROOT / "apps/api/migrations", root / "apps/api/migrations")
    shutil.copy2(SOURCE_ROOT / "apps/api/cmd/api/protected_config.go", root / "apps/api/cmd/api/protected_config.go")
    shutil.copy2(SOURCE_ROOT / "apps/api/cmd/worker/protected_config.go", root / "apps/api/cmd/worker/protected_config.go")


class PreflightTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix="nerae-deploy-preflight-", dir="/private/tmp")
        self.base = Path(self.temp.name)
        (self.base / "release/scripts/dev").mkdir(parents=True)
        (self.base / "release/apps/api/cmd/api").mkdir(parents=True)
        (self.base / "release/apps/api/cmd/worker").mkdir(parents=True)
        copy_release(self.base / "release")
        self.api, self.worker = fixtures()
        self.api_path = self.base / "api.json"
        self.worker_path = self.base / "worker.json"
        self.write_configs()

    def tearDown(self):
        self.temp.cleanup()

    def write_configs(self):
        for path, value in ((self.api_path, self.api), (self.worker_path, self.worker)):
            path.write_text(json.dumps(value, separators=(",", ":")))
            path.chmod(0o600)

    def run_cli(self, worker_uid=None):
        return subprocess.run([
            sys.executable, str(TOOL), "--release-root", str(self.base / "release"),
            "--api-secrets", str(self.api_path), "--api-uid", str(os.getuid()),
            "--worker-secrets", str(self.worker_path), "--worker-uid", str(os.getuid() if worker_uid is None else worker_uid),
        ], capture_output=True, text=True, check=False)

    def test_valid_synthetic_bundle_passes_without_printing_values(self):
        result = self.run_cli()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("offline contract check", result.stdout)
        manifest = json.loads((self.base / "release/scripts/dev/migrations.json").read_text())
        self.assertIn(f"{len(manifest['migrations'])} migrations", result.stdout)
        self.assertNotIn(self.worker["smtp_password"], result.stdout + result.stderr)
        self.assertNotIn(next(iter(self.api.values())), result.stdout + result.stderr)

    def test_shared_epoch_mismatch_fails_closed_without_printing_key(self):
        self.worker["email_code_hash_key"] = encoded(250)
        self.write_configs()
        result = self.run_cli()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("shared key mismatch", result.stderr)
        self.assertNotIn(self.worker["email_code_hash_key"], result.stdout + result.stderr)

    def test_go_specific_approval_attempt_cap_is_enforced(self):
        self.api["rate_approval_attempt_limit"] = 11
        self.write_configs()
        result = self.run_cli()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("approval attempt limit rejected", result.stderr)

    def test_public_trusted_proxy_prefix_is_rejected(self):
        self.api["trusted_proxy_prefixes"] = ["8.8.8.8/32"]
        self.write_configs()
        result = self.run_cli()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("not private/local", result.stderr)

    def test_duplicate_json_member_is_rejected(self):
        self.api_path.write_text('{"proxy_credential":"first","proxy_credential":"second"}')
        self.api_path.chmod(0o600)
        result = self.run_cli()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("invalid JSON", result.stderr)
        self.assertNotIn("first", result.stdout + result.stderr)

    def test_group_readable_secret_is_rejected(self):
        self.api_path.chmod(0o640)
        result = self.run_cli()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("secret file owner or permissions rejected", result.stderr)

    def test_untrusted_parent_owner_is_rejected(self):
        result = self.run_cli(worker_uid=os.getuid() + 1)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("untrusted parent owner", result.stderr)

    def test_owner_read_permission_is_required(self):
        held_fd = os.open(self.worker_path, os.O_RDONLY)
        self.worker_path.chmod(0o200)
        try:
            with mock.patch.object(preflight.os, "open", side_effect=lambda path, flags: os.dup(held_fd)):
                with self.assertRaises(preflight.PreflightError) as caught:
                    preflight.check_config_file(self.worker_path, "Worker settings", os.getuid(),
                                                self.base / "release/apps/api/cmd/worker/protected_config.go",
                                                "workerSecrets", 32 * 1024)
        finally:
            os.close(held_fd)
        self.assertIn("secret file owner or permissions rejected", str(caught.exception))

    def test_secret_symlink_is_rejected(self):
        link = self.base / "worker-link.json"
        link.symlink_to(self.worker_path)
        self.worker_path = link
        result = self.run_cli()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("file type or size rejected", result.stderr)

    def test_path_replacement_cannot_change_checked_owner_after_fd_read(self):
        replacement = self.base / "replacement.json"
        replacement.write_text(json.dumps(self.api, separators=(",", ":")))
        replacement.chmod(0o600)
        real_fstat = os.fstat
        replaced = False

        def swap_during_fstat(fd):
            nonlocal replaced
            opened = real_fstat(fd)
            if not replaced:
                os.replace(replacement, self.api_path)
                fields = list(opened)
                fields[4] = os.getuid() + 1  # model a readable file owned by another UID
                replaced = True
                return os.stat_result(fields)
            return opened

        with mock.patch.object(preflight.os, "fstat", side_effect=swap_during_fstat):
            with self.assertRaises(preflight.PreflightError) as caught:
                preflight.check_config_file(self.api_path, "API settings", os.getuid(),
                                            self.base / "release/apps/api/cmd/api/protected_config.go",
                                            "apiSecretFile", 64 * 1024)
        self.assertIn("owner or permissions rejected", str(caught.exception))
        self.assertTrue(replaced)

    def test_migration_checksum_drift_is_rejected(self):
        sql_file = next((self.base / "release/apps/api/migrations").rglob("*.up.sql"))
        sql_file.write_bytes(sql_file.read_bytes() + b"\n-- drift\n")
        result = self.run_cli()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("checksum mismatch", result.stderr)


if __name__ == "__main__":
    unittest.main()
