#!/usr/bin/env python3
"""Run A01-13 against a new private socket-only MySQL and local TLS SMTP tests.

No existing database or TCP listener is used. A failed run preserves only the
fresh private directory created here for diagnosis; a successful run removes it.
"""
from __future__ import annotations

import getpass
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
    if any(not binary.is_file() for binary in binaries):
        raise RuntimeError("required local MySQL or Go executable is missing")
    if shutil.disk_usage(TEMP).free < 350 * 1024 * 1024:
        raise RuntimeError("less than 350 MiB free; refusing to initialize test database")

    base = Path(tempfile.mkdtemp(prefix="nerae-email-outbox-mysql-", dir=TEMP))
    if base.resolve().parent != TEMP or base.stat().st_mode & 0o077:
        raise RuntimeError("test directory is not private")
    data = base / "data"
    data.mkdir(mode=0o700)
    sock = base / "mysql.sock"
    logfile = base / "mysqld.log"
    child: subprocess.Popen[bytes] | None = None
    succeeded = False
    common = [
        str(MYSQL / "bin/mysqld"), "--no-defaults", f"--basedir={MYSQL}",
        f"--datadir={data}", f"--user={getpass.getuser()}", "--skip-name-resolve",
        "--mysqlx=0", "--skip-log-bin", "--innodb-buffer-pool-size=64M",
        "--innodb-redo-log-capacity=16M",
    ]
    admin = [str(MYSQL / "bin/mysqladmin"), "--no-defaults", "--protocol=socket",
             f"--socket={sock}", "--user=root"]
    mysql = [str(MYSQL / "bin/mysql"), "--no-defaults", "--protocol=socket",
             f"--socket={sock}", "--user=root"]
    env = {**os.environ, "MYSQL_PWD": ""}
    try:
        with logfile.open("wb") as log:
            init = subprocess.run(common + ["--initialize-insecure"], stdout=log,
                                  stderr=subprocess.STDOUT, env=env, timeout=90, check=False)
            if init.returncode:
                raise RuntimeError(f"private MySQL initialization failed ({init.returncode})")
            child = subprocess.Popen(common + ["--skip-networking", "--console", "--daemonize=0",
                                               f"--socket={sock}", f"--pid-file={base / 'mysql.pid'}",
                                               "--local-infile=0"], stdin=subprocess.DEVNULL,
                                     stdout=log, stderr=subprocess.STDOUT, env=env)
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

            migrations = "\n".join((ROOT / "apps/api/migrations" / name).read_text()
                                    for name in ("auth/0001_device_proof_replay.up.sql",
                                                 "auth/0002_email_challenge.up.sql",
                                                 "auth/0007_email_outbox.up.sql"))
            apply = subprocess.run(mysql, input="CREATE DATABASE nerae_email_outbox_test;\nUSE nerae_email_outbox_test;\n" + migrations,
                                   text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE,
                                   env=env, timeout=20, check=False)
            if apply.returncode:
                raise RuntimeError(f"private test migration failed ({apply.returncode})")

            test_env = {**env, "GOTOOLCHAIN": "local", "GOPROXY": "off", "GOSUMDB": "off",
                        "GOCACHE": "/private/tmp/nerae-go-api-cache",
                        "GOMODCACHE": "/private/tmp/nerae-go-api-mod",
                        "NERAE_EMAIL_OUTBOX_TEST_DSN": f"root@unix({sock})/nerae_email_outbox_test?parseTime=true&loc=UTC"}
            result = subprocess.run([str(GO), "test", "-race", "-count=1", "-v",
                                     "./internal/modules/auth/issuance", "./internal/modules/auth/mail"],
                                    cwd=ROOT / "apps/api", env=test_env, timeout=240,
                                    stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True, check=False)
            print(result.stdout, end="")
            if result.returncode:
                raise RuntimeError(f"race tests failed ({result.returncode})")

            shutdown = subprocess.run(admin + ["shutdown"], stdout=subprocess.DEVNULL,
                                      stderr=subprocess.DEVNULL, env=env, timeout=30, check=False)
            if shutdown.returncode or child.wait(timeout=30) != 0:
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
        if succeeded and (child is None or child.poll() is not None):
            shutil.rmtree(base)
    if succeeded:
        print("PASS: isolated socket-only MySQL and local-only SMTP TLS tests; test directory removed")
        return 0
    return 1


if __name__ == "__main__":
    try:
        sys.exit(main())
    except RuntimeError as error:
        print(f"FAIL: {error}", file=sys.stderr)
        sys.exit(1)
