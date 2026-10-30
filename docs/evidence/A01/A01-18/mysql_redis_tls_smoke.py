#!/usr/bin/env python3
"""Run A01-18 against private socket-only MySQL/Redis and localhost TLS."""
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
TEMP = Path("/private/tmp")
MYSQL = Path("/private/tmp/nerae-runtime-artifacts/mysql")
REDIS_SERVER = Path("/private/tmp/nerae-runtime-artifacts/redis-src/src/redis-server")
REDIS_CLI = REDIS_SERVER.with_name("redis-cli")
GO = Path("/private/tmp/nerae-go-toolchain-1.27.3/go/bin/go")
MIGRATIONS = (
    "auth/0001_device_proof_replay.up.sql",
    "auth/0002_email_challenge.up.sql",
    "user/0001_user.up.sql",
    "auth/0003_email_identity.up.sql",
    "auth/0007_email_outbox.up.sql",
    "auth/0008_email_challenge_request.up.sql",
)
MIN_FREE_BYTES = 1 << 30


def run() -> int:
    free = shutil.disk_usage(TEMP).free
    if free < MIN_FREE_BYTES:
        print(f"BLOCKED: {free} bytes free; need at least {MIN_FREE_BYTES}; no services started", file=sys.stderr)
        return 2
    executables = (MYSQL / "bin/mysqld", MYSQL / "bin/mysql", MYSQL / "bin/mysqladmin", REDIS_SERVER, REDIS_CLI, GO)
    if any(not path.is_file() for path in executables):
        print("BLOCKED: required offline MySQL, Redis, or Go executable is missing", file=sys.stderr)
        return 2

    base = Path(tempfile.mkdtemp(prefix="nerae-a01-18-", dir=TEMP))
    if base.resolve().parent != TEMP or base.stat().st_mode & 0o077:
        print("FAIL: temporary directory was not private; preserved path:", base, file=sys.stderr)
        return 1
    data = base / "mysql-data"
    data.mkdir(mode=0o700)
    mysql_socket, redis_socket = base / "mysql.sock", base / "redis.sock"
    log = base / "services.log"
    mysql_child: subprocess.Popen[bytes] | None = None
    redis_child: subprocess.Popen[bytes] | None = None
    succeeded = False
    env = {**os.environ, "LC_ALL": "C", "MYSQL_PWD": ""}
    mysql_common = [str(MYSQL / "bin/mysqld"), "--no-defaults", f"--basedir={MYSQL}", f"--datadir={data}",
                    f"--user={getpass.getuser()}", "--skip-name-resolve", "--mysqlx=0", "--skip-log-bin",
                    "--innodb-buffer-pool-size=64M", "--innodb-redo-log-capacity=16M"]
    admin = [str(MYSQL / "bin/mysqladmin"), "--no-defaults", "--protocol=socket", f"--socket={mysql_socket}", "--user=root"]
    mysql = [str(MYSQL / "bin/mysql"), "--no-defaults", "--protocol=socket", f"--socket={mysql_socket}", "--user=root"]

    try:
        with log.open("xb") as output:
            init = subprocess.run(mysql_common + ["--initialize-insecure"], stdout=output, stderr=subprocess.STDOUT,
                                  env=env, timeout=90, check=False)
            if init.returncode:
                raise RuntimeError(f"private MySQL initialization failed ({init.returncode})")
            mysql_child = subprocess.Popen(mysql_common + ["--skip-networking", "--console", "--daemonize=0",
                                                           f"--socket={mysql_socket}", f"--pid-file={base / 'mysql.pid'}",
                                                           "--local-infile=0"], cwd=base, stdin=subprocess.DEVNULL,
                                           stdout=output, stderr=subprocess.STDOUT, env=env)
            wait_mysql(mysql_child, admin)
            redis_child = subprocess.Popen([str(REDIS_SERVER), "--port", "0", "--unixsocket", str(redis_socket),
                                            "--unixsocketperm", "700", "--save", "", "--appendonly", "no",
                                            "--daemonize", "no", "--protected-mode", "yes", "--dir", str(base)],
                                           cwd=base, stdin=subprocess.DEVNULL, stdout=output, stderr=subprocess.STDOUT, env=env)
            wait_redis(redis_child, redis_socket)
            migration_sql = "\n".join((ROOT / "apps/api/migrations" / item).read_text() for item in MIGRATIONS)
            apply = subprocess.run(mysql, input="CREATE DATABASE nerae_a01_18_test;\nUSE nerae_a01_18_test;\n" + migration_sql,
                                   text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE, env=env, timeout=30, check=False)
            if apply.returncode:
                raise RuntimeError(f"private migrations failed ({apply.returncode})")
            test_env = {**env, "GOTOOLCHAIN": "local", "GOPROXY": "off", "GOSUMDB": "off",
                        "GOCACHE": "/private/tmp/nerae-go-api-cache", "GOMODCACHE": "/private/tmp/nerae-go-api-mod",
                        "NERAE_EMAIL_CHALLENGE_REQUEST_TEST_DSN": f"root@unix({mysql_socket})/nerae_a01_18_test?parseTime=true&loc=UTC",
                        "NERAE_EMAIL_CHALLENGE_REQUEST_REDIS_SOCKET": str(redis_socket),
                        "NERAE_AUTH_CLIENT_INTEROP": "1"}
            test = subprocess.run([str(GO), "test", "-mod=readonly", "-race", "-count=1", "-v", "./internal/modules/auth/transport"],
                                  cwd=ROOT / "apps/api", env=test_env, timeout=300,
                                  stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True, check=False)
            print(test.stdout, end="")
            if test.returncode:
                raise RuntimeError(f"A01-18 transport race tests failed ({test.returncode})")
            subprocess.run([str(REDIS_CLI), "-s", str(redis_socket), "SHUTDOWN", "NOSAVE"],
                           stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, env=env, timeout=10, check=False)
            redis_child.wait(timeout=15)
            stop = subprocess.run(admin + ["shutdown"], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, env=env, timeout=30, check=False)
            if stop.returncode or mysql_child.wait(timeout=30) != 0:
                raise RuntimeError("private MySQL did not shut down cleanly")
            succeeded = True
    except (OSError, subprocess.TimeoutExpired, RuntimeError) as error:
        print(f"FAIL: {error}; preserved only this run's directory: {base}", file=sys.stderr)
    finally:
        for child in (redis_child, mysql_child):
            if child is not None and child.poll() is None:
                child.terminate()
                try:
                    child.wait(timeout=10)
                except subprocess.TimeoutExpired:
                    child.kill()
                    child.wait(timeout=5)
        if succeeded:
            shutil.rmtree(base)
            print("PASS: private socket-only MySQL/Redis and localhost TLS test completed; own temporary directory removed")
    return 0 if succeeded else 1


def wait_mysql(child: subprocess.Popen[bytes], admin: list[str]) -> None:
    deadline = time.monotonic() + 60
    while time.monotonic() < deadline and child.poll() is None:
        probe = subprocess.run(admin + ["ping", "--silent"], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, timeout=3, check=False)
        if probe.returncode == 0:
            return
        time.sleep(0.2)
    raise RuntimeError("private MySQL did not become ready")


def wait_redis(child: subprocess.Popen[bytes], socket: Path) -> None:
    deadline = time.monotonic() + 30
    while time.monotonic() < deadline and child.poll() is None:
        probe = subprocess.run([str(REDIS_CLI), "-s", str(socket), "PING"], stdout=subprocess.PIPE, stderr=subprocess.DEVNULL, timeout=3, check=False)
        if probe.returncode == 0 and probe.stdout.strip() == b"PONG":
            return
        time.sleep(0.2)
    raise RuntimeError("private Redis did not become ready")


if __name__ == "__main__":
    sys.exit(run())
