#!/usr/bin/env python3
"""Run the API process integration against fresh private socket services."""
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
    "user/0002_age_declaration_version.up.sql",
    "auth/0003_email_identity.up.sql",
    "workout/0001_workout_session.up.sql",
    "workout/0002_workout_sets.up.sql",
    "auth/0004_auth_session.up.sql",
    "auth/0005_login_approval.up.sql",
    "auth/0006_approval_exchange.up.sql",
    "workout/0003_workout_exercise_order.up.sql",
    "auth/0007_email_outbox.up.sql",
    "auth/0008_email_challenge_request.up.sql",
    "auth/0009_email_login_receipt.up.sql",
    "auth/0010_email_code_hash_key_epoch.up.sql",
    "transport/0001_idempotency.up.sql",
)


def run() -> int:
    if shutil.disk_usage(TEMP).free < 1 << 30:
        print("BLOCKED: less than 1 GiB free; no services started", file=sys.stderr)
        return 2
    executables = (MYSQL / "bin/mysqld", MYSQL / "bin/mysql", MYSQL / "bin/mysqladmin", REDIS_SERVER, REDIS_CLI, GO)
    if any(not path.is_file() for path in executables):
        print("BLOCKED: required local MySQL, Redis, or Go executable is missing", file=sys.stderr)
        return 2

    base = Path(tempfile.mkdtemp(prefix="nerae-api-process-", dir=TEMP))
    if base.resolve().parent != TEMP or base.stat().st_mode & 0o077:
        print("FAIL: private test directory validation failed; retained:", base, file=sys.stderr)
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
            apply = subprocess.run(mysql, input="CREATE DATABASE nerae_api_process_test;\nUSE nerae_api_process_test;\n" + migration_sql,
                                   text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE, env=env, timeout=45, check=False)
            if apply.returncode:
                raise RuntimeError(f"private migrations failed ({apply.returncode})")
            test_env = {**env, "GOTOOLCHAIN": "local", "GOPROXY": "off", "GOSUMDB": "off",
                        "GOCACHE": "/private/tmp/nerae-go-api-cache", "GOMODCACHE": "/private/tmp/nerae-go-api-mod",
                        "NERAE_API_PROCESS_TEST_DSN": f"root@unix({mysql_socket})/nerae_api_process_test?parseTime=true&loc=UTC",
                        "NERAE_API_PROCESS_REDIS_SOCKET": str(redis_socket)}
            test = subprocess.run([str(GO), "test", "-mod=readonly", "-race", "-count=1", "-v",
                                   "-run", "^TestAPIProcessPrivateServices$", "./cmd/api"],
                                  cwd=ROOT / "apps/api", env=test_env, timeout=420,
                                  stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True, check=False)
            print(test.stdout, end="")
            if test.returncode:
                raise RuntimeError(f"API process integration test failed ({test.returncode})")
            marker = "PASS: protected API process startup -> loopback business route -> enqueue -> clean shutdown"
            if marker not in test.stdout:
                raise RuntimeError("targeted test did not emit its PASS marker; it may have been skipped")
            subprocess.run([str(REDIS_CLI), "-s", str(redis_socket), "SHUTDOWN", "NOSAVE"],
                           stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, env=env, timeout=10, check=False)
            redis_child.wait(timeout=15)
            stop = subprocess.run(admin + ["shutdown"], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL,
                                  env=env, timeout=30, check=False)
            if stop.returncode or mysql_child.wait(timeout=30) != 0:
                raise RuntimeError("private MySQL did not shut down cleanly")
            succeeded = True
    except (OSError, subprocess.TimeoutExpired, RuntimeError) as error:
        print(f"FAIL: {error}; private test directory retained: {base}", file=sys.stderr)
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
            print("PASS: own socket-only private services completed and own directory removed")
    return 0 if succeeded else 1


def wait_mysql(child: subprocess.Popen[bytes], admin: list[str]) -> None:
    deadline = time.monotonic() + 60
    while time.monotonic() < deadline and child.poll() is None:
        probe = subprocess.run(admin + ["ping", "--silent"], stdout=subprocess.DEVNULL,
                               stderr=subprocess.DEVNULL, timeout=3, check=False)
        if probe.returncode == 0:
            return
        time.sleep(0.2)
    raise RuntimeError("private MySQL did not become ready")


def wait_redis(child: subprocess.Popen[bytes], socket: Path) -> None:
    deadline = time.monotonic() + 30
    while time.monotonic() < deadline and child.poll() is None:
        probe = subprocess.run([str(REDIS_CLI), "-s", str(socket), "PING"], stdout=subprocess.PIPE,
                               stderr=subprocess.DEVNULL, timeout=3, check=False)
        if probe.returncode == 0 and probe.stdout.strip() == b"PONG":
            return
        time.sleep(0.2)
    raise RuntimeError("private Redis did not become ready")


if __name__ == "__main__":
    sys.exit(run())
