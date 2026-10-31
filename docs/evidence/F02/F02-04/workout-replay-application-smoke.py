#!/usr/bin/env python3
"""Run the workout replay-only application test on a fresh socket-only MySQL."""
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
DATABASE = "nerae_workout_integration_test"
MIGRATIONS = (
    "auth/0001_device_proof_replay.up.sql",
    "auth/0002_email_challenge.up.sql",
    "user/0001_user.up.sql",
    "user/0002_age_declaration_version.up.sql",
    "auth/0003_email_identity.up.sql",
    "workout/0001_workout_session.up.sql",
    "workout/0002_workout_sets.up.sql",
    "auth/0004_auth_session.up.sql",
    "workout/0003_workout_exercise_order.up.sql",
    "auth/0007_email_outbox.up.sql",
    "auth/0010_email_code_hash_key_epoch.up.sql",
    "transport/0001_idempotency.up.sql",
)


def main() -> int:
    binaries = (MYSQL / "bin/mysqld", MYSQL / "bin/mysql", MYSQL / "bin/mysqladmin", GO)
    if any(not item.is_file() for item in binaries):
        raise RuntimeError("required local MySQL or Go executable is missing")
    if shutil.disk_usage(TEMP).free < 350 * 1024 * 1024:
        raise RuntimeError("less than 350 MiB free; refusing to initialize private test database")

    base: Path | None = None
    child: subprocess.Popen[bytes] | None = None
    succeeded = False
    try:
        base = Path(tempfile.mkdtemp(prefix="nerae-workout-integration-mysql-", dir=TEMP))
        if base.resolve().parent != TEMP or base.stat().st_mode & 0o077:
            raise RuntimeError("temporary directory is not private")
        data = base / "data"
        data.mkdir(mode=0o700)
        sock = base / "mysql.sock"
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
        logfile = base / "mysqld.log"
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

            migration = "\n".join((ROOT / "apps/api/migrations" / name).read_text(encoding="utf-8") for name in MIGRATIONS)
            applied = subprocess.run(mysql, input=f"CREATE DATABASE {DATABASE};\nUSE {DATABASE};\n{migration}",
                                     text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE,
                                     env=env, timeout=30, check=False)
            if applied.returncode:
                raise RuntimeError(f"private migrations failed ({applied.returncode}): {applied.stderr[-500:]}")

            test_env = {
                **env, "GOTOOLCHAIN": "local", "GOPROXY": "off", "GOSUMDB": "off",
                "GOCACHE": "/private/tmp/nerae-go-api-cache",
                "GOMODCACHE": "/private/tmp/nerae-go-api-mod",
                "NERAE_WORKOUT_INTEGRATION_TEST_DSN":
                    f"root@unix({sock})/{DATABASE}?parseTime=true&loc=UTC",
            }
            tested = subprocess.run(
                [str(GO), "test", "-race", "-count=1", "-run", "^TestWorkoutReplayOnlyApplication$", "-v",
                 "./internal/modules/workout/application"],
                cwd=ROOT / "apps/api", env=test_env, timeout=240, check=False,
            )
            if tested.returncode:
                raise RuntimeError(f"workout application replay test failed ({tested.returncode})")
            shutdown = subprocess.run(admin + ["shutdown"], stdout=subprocess.DEVNULL,
                                      stderr=subprocess.DEVNULL, env=env, timeout=30, check=False)
            if shutdown.returncode or child.wait(timeout=30) != 0:
                raise RuntimeError("private MySQL did not shut down cleanly")
            succeeded = True
    except (OSError, subprocess.TimeoutExpired, RuntimeError) as error:
        retained = f"; private directory retained: {base}" if base is not None else ""
        print(f"FAIL: {error}{retained}", file=sys.stderr)
    finally:
        if child is not None and child.poll() is None:
            child.terminate()
            try:
                child.wait(timeout=15)
            except subprocess.TimeoutExpired:
                child.kill()
                child.wait(timeout=5)
        if succeeded and base is not None and (child is None or child.poll() is not None):
            shutil.rmtree(base)
    if succeeded:
        print("PASS: private socket-only workout replay application test")
        return 0
    return 1


if __name__ == "__main__":
    sys.exit(main())
