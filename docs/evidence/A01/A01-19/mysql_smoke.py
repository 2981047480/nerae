#!/usr/bin/env python3
"""Create a disposable socket-only MySQL and run A01-19 local TLS tests."""
from __future__ import annotations

import argparse
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


def main(startup_only: bool = False) -> int:
    binaries = [MYSQL / "bin/mysqld", MYSQL / "bin/mysql", MYSQL / "bin/mysqladmin", GO]
    if any(not binary.is_file() for binary in binaries):
        raise RuntimeError("required local MySQL or Go executable is missing")
    if shutil.disk_usage(TEMP).free < 350 * 1024 * 1024:
        raise RuntimeError("less than 350 MiB free; refusing to initialize test database")

    base = Path(tempfile.mkdtemp(prefix="nerae-a01-19-mysql-", dir=TEMP))
    if base.resolve().parent != TEMP or base.stat().st_mode & 0o077:
        raise RuntimeError("test directory is not private")
    data = base / "data"
    data.mkdir(mode=0o700)
    sock = base / "mysql.sock"
    logfile = base / "mysqld.log"
    child: subprocess.Popen[bytes] | None = None
    succeeded = False
    common = [str(MYSQL / "bin/mysqld"), "--no-defaults", f"--basedir={MYSQL}",
              f"--datadir={data}", f"--user={getpass.getuser()}", "--skip-name-resolve",
              "--mysqlx=0", "--skip-log-bin", "--innodb-buffer-pool-size=64M",
              "--innodb-redo-log-capacity=16M"]
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

            migration_names = ("auth/0001_device_proof_replay.up.sql", "auth/0002_email_challenge.up.sql",
                               "user/0001_user.up.sql", "auth/0003_email_identity.up.sql",
                               "auth/0007_email_outbox.up.sql", "auth/0010_email_code_hash_key_epoch.up.sql")
            migrations = "\n".join((ROOT / "apps/api/migrations" / name).read_text() for name in migration_names)
            databases = ("nerae_email_worker_test", "nerae_email_outbox_test", "nerae_email_smtp_test",
                         "nerae_email_worker_startup_test")
            schema_sql = "\n".join(f"CREATE DATABASE {name};\nUSE {name};\n{migrations}" for name in databases)
            apply = subprocess.run(mysql, input=schema_sql,
                                   text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE,
                                   env=env, timeout=20, check=False)
            if apply.returncode:
                raise RuntimeError(f"private test migration failed ({apply.returncode})")

            test_env = {**env, "GOTOOLCHAIN": "local", "GOPROXY": "off", "GOSUMDB": "off",
                        "GOCACHE": "/private/tmp/nerae-go-api-cache",
                        "GOMODCACHE": "/private/tmp/nerae-go-api-mod",
                        "NERAE_EMAIL_OUTBOX_WORKER_TEST_DSN": f"root@unix({sock})/nerae_email_worker_test?parseTime=true&loc=UTC",
                        "NERAE_EMAIL_OUTBOX_TEST_DSN": f"root@unix({sock})/nerae_email_outbox_test?parseTime=true&loc=UTC",
                        "NERAE_EMAIL_OUTBOX_SMTP_TEST_DSN": f"root@unix({sock})/nerae_email_smtp_test?parseTime=true&loc=UTC",
                        "NERAE_WORKER_STARTUP_TEST_DSN": f"root@unix({sock})/nerae_email_worker_startup_test?parseTime=true&loc=UTC"}
            packages = (["./cmd/worker"] if startup_only else
                        ["./internal/modules/auth/mailworker", "./internal/modules/auth/issuance",
                         "./internal/modules/auth/mail", "./cmd/worker"])
            result = subprocess.run([str(GO), "test", "-race", "-count=1", "-v", *packages], cwd=ROOT / "apps/api", env=test_env,
                                    timeout=240, stdout=subprocess.PIPE, stderr=subprocess.STDOUT,
                                    text=True, check=False)
            print(result.stdout, end="")
            if result.returncode:
                raise RuntimeError(f"race/integration tests failed ({result.returncode})")

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
        scope = "worker-startup" if startup_only else "worker packages"
        print(f"PASS: isolated socket-only MySQL and loopback TLS {scope} test; private directory removed")
        return 0
    return 1


if __name__ == "__main__":
    try:
        parser = argparse.ArgumentParser(description=__doc__)
        parser.add_argument("--worker-startup-only", action="store_true",
                            help="run only cmd/worker integration against the private MySQL and local TLS sink")
        sys.exit(main(parser.parse_args().worker_startup_only))
    except RuntimeError as error:
        print(f"FAIL: {error}", file=sys.stderr)
        sys.exit(1)
