#!/usr/bin/env python3
"""Run A01-21 integration tests against a disposable socket-only MySQL."""
from __future__ import annotations

import getpass
import hashlib
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import time

ROOT = Path(__file__).resolve().parents[4]
MYSQL = Path("/private/tmp/nerae-runtime-artifacts/mysql")
GO = Path("/private/tmp/nerae-go-toolchain-1.27.3/go/bin/go")
TEMP = Path("/private/tmp")


def main() -> int:
    binaries = [MYSQL / "bin/mysqld", MYSQL / "bin/mysql", MYSQL / "bin/mysqladmin", GO]
    if any(not item.is_file() for item in binaries):
        raise RuntimeError("required local MySQL or Go executable is missing")
    if shutil.disk_usage(TEMP).free < 350 * 1024 * 1024:
        raise RuntimeError("less than 350 MiB free; refusing to initialize test database")
    base = Path(tempfile.mkdtemp(prefix="nerae-a01-21-mysql-", dir=TEMP))
    os.chmod(base, 0o700)
    info = base.lstat()
    if base.is_symlink() or not base.is_dir() or info.st_mode & 0o077 or base.resolve().parent != TEMP:
        raise RuntimeError("private MySQL directory failed safety checks")
    data = base / "data"
    data.mkdir(mode=0o700)
    sock = base / "mysql.sock"
    log = base / "mysqld.log"
    child: subprocess.Popen[bytes] | None = None
    succeeded = False
    common = [str(MYSQL / "bin/mysqld"), "--no-defaults", f"--basedir={MYSQL}", f"--datadir={data}",
              f"--user={getpass.getuser()}", "--skip-name-resolve", "--mysqlx=0", "--skip-log-bin",
              "--innodb-buffer-pool-size=64M", "--innodb-redo-log-capacity=16M"]
    env = {**os.environ, "MYSQL_PWD": ""}
    admin = [str(MYSQL / "bin/mysqladmin"), "--no-defaults", "--protocol=socket", f"--socket={sock}", "--user=root"]
    client = [str(MYSQL / "bin/mysql"), "--no-defaults", "--protocol=socket", f"--socket={sock}", "--user=root"]
    try:
        with log.open("wb") as output:
            init = subprocess.run(common + ["--initialize-insecure"], stdout=output, stderr=subprocess.STDOUT,
                                  env=env, timeout=90, check=False)
            if init.returncode:
                raise RuntimeError(f"private MySQL initialization failed ({init.returncode})")
            child = subprocess.Popen(common + ["--skip-networking", "--console", "--daemonize=0", f"--socket={sock}",
                                               f"--pid-file={base / 'mysql.pid'}", "--local-infile=0"],
                                     stdin=subprocess.DEVNULL, stdout=output, stderr=subprocess.STDOUT, env=env)
            deadline = time.monotonic() + 30
            while time.monotonic() < deadline:
                if child.poll() is not None:
                    raise RuntimeError(f"private MySQL exited ({child.returncode})")
                ready = subprocess.run(admin + ["ping", "--silent"], stdout=subprocess.DEVNULL,
                                       stderr=subprocess.DEVNULL, env=env, timeout=3, check=False)
                if ready.returncode == 0:
                    break
                time.sleep(0.2)
            else:
                raise RuntimeError("private MySQL did not become ready")
            manifest_path = ROOT / "scripts/dev/migrations.json"
            manifest = json.loads(manifest_path.read_text())
            if manifest.get("version") != 1 or not isinstance(manifest.get("migrations"), list):
                raise RuntimeError("migration manifest has an unsupported format")
            migrations: list[tuple[str, Path]] = []
            for expected_order, item in enumerate(manifest["migrations"], start=1):
                if not isinstance(item, dict) or item.get("order") != expected_order:
                    raise RuntimeError("migration manifest order is invalid")
                migration_id = item.get("id")
                module, separator, filename = migration_id.partition("/") if isinstance(migration_id, str) else ("", "", "")
                relative_path = Path(migration_id) if isinstance(migration_id, str) else Path()
                if (not separator or relative_path.is_absolute() or len(relative_path.parts) != 2 or
                        any(part in ("", ".", "..") for part in relative_path.parts) or
                        item.get("module") != module or item.get("file") != filename):
                    raise RuntimeError("migration manifest path is invalid")
                migration_root = ROOT / "apps/api/migrations"
                migration_path = migration_root / relative_path
                if migration_root.resolve() not in migration_path.resolve().parents:
                    raise RuntimeError("migration manifest path escapes the migration directory")
                contents = migration_path.read_bytes()
                if hashlib.sha256(contents).hexdigest() != item.get("sha256"):
                    raise RuntimeError(f"registered migration checksum mismatch: {migration_id}")
                migrations.append((migration_id, migration_path))
            task_migration = "auth/0009_email_login_receipt.up.sql"
            if task_migration not in {migration_id for migration_id, _ in migrations}:
                migrations.append((task_migration, ROOT / "apps/api/migrations" / task_migration))
            print("Applying migrations in order: " + ", ".join(migration_id for migration_id, _ in migrations))
            schema = "CREATE DATABASE nerae_email_login_ledger_test;\nUSE nerae_email_login_ledger_test;\n"
            schema += "\n".join(path.read_text() for _, path in migrations)
            applied = subprocess.run(client, input=schema, text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE,
                                     env=env, timeout=30, check=False)
            if applied.returncode:
                raise RuntimeError(f"private schema setup failed ({applied.returncode})")
            test_env = {**env, "GOTOOLCHAIN": "local", "GOPROXY": "off", "GOSUMDB": "off",
                        "GOCACHE": "/private/tmp/nerae-go-api-cache", "GOMODCACHE": "/private/tmp/nerae-go-api-mod",
                        "NERAE_EMAIL_LOGIN_LEDGER_TEST_DSN": f"root@unix({sock})/nerae_email_login_ledger_test?parseTime=true&loc=UTC"}
            result = subprocess.run([str(GO), "test", "-race", "-count=1", "-v", "./internal/modules/auth/loginledger"],
                                    cwd=ROOT / "apps/api", env=test_env, timeout=240,
                                    stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True, check=False)
            print(result.stdout, end="")
            if result.returncode:
                raise RuntimeError(f"race/integration tests failed ({result.returncode})")
            stopped = subprocess.run(admin + ["shutdown"], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL,
                                      env=env, timeout=30, check=False)
            if stopped.returncode or child.wait(timeout=30) != 0:
                raise RuntimeError("private MySQL did not shut down cleanly")
            succeeded = True
    except (OSError, subprocess.TimeoutExpired, RuntimeError) as error:
        print(f"FAIL: {error}; private test directory retained: {base}", file=sys.stderr)
    finally:
        if child is not None and child.poll() is None:
            child.terminate()
            try:
                child.wait(timeout=15)
            except subprocess.TimeoutExpired:
                child.kill()
                child.wait(timeout=5)
        if succeeded:
            shutil.rmtree(base)
    if succeeded:
        print("PASS: A01-21 isolated socket-only MySQL integration; private directory removed")
        return 0
    return 1


if __name__ == "__main__":
    try:
        sys.exit(main())
    except RuntimeError as error:
        print(f"FAIL: {error}", file=sys.stderr)
        sys.exit(1)
