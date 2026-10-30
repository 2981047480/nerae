#!/usr/bin/env python3
"""Start one private socket-only Redis and run the real limiter race tests."""
from __future__ import annotations

import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import time

ROOT = Path(__file__).resolve().parents[4]
TEMP = Path("/private/tmp")
REDIS_SERVER = Path("/private/tmp/nerae-runtime-artifacts/redis-src/src/redis-server")
REDIS_CLI = REDIS_SERVER.with_name("redis-cli")
GO = Path("/private/tmp/nerae-go-toolchain-1.27.3/go/bin/go")


def main() -> int:
    for executable in (REDIS_SERVER, REDIS_CLI, GO):
        if not executable.is_file():
            raise RuntimeError("required local Redis or Go executable is missing")
    if shutil.disk_usage(TEMP).free < 100 * 1024 * 1024:
        raise RuntimeError("less than 100 MiB free; refusing to start the test Redis")

    base = Path(tempfile.mkdtemp(prefix="nerae-email-ratelimit-", dir=TEMP))
    if base.resolve().parent != TEMP or base.stat().st_mode & 0o077:
        raise RuntimeError("temporary Redis directory is not private")
    socket = base / "redis.sock"
    logfile = base / "redis.log"
    child: subprocess.Popen[bytes] | None = None
    succeeded = False
    env = {**os.environ, "LC_ALL": "C"}
    try:
        with logfile.open("xb") as log:
            command = [str(REDIS_SERVER), "--bind", "127.0.0.1", "--port", "0",
                       "--unixsocket", str(socket), "--unixsocketperm", "700",
                       "--save", "", "--appendonly", "no", "--daemonize", "no",
                       "--protected-mode", "yes", "--dir", str(base)]
            child = subprocess.Popen(command, cwd=base, stdin=subprocess.DEVNULL,
                                     stdout=log, stderr=subprocess.STDOUT, env=env)
            deadline = time.monotonic() + 20
            while time.monotonic() < deadline:
                if child.poll() is not None:
                    raise RuntimeError(f"private Redis exited ({child.returncode})")
                ping = subprocess.run([str(REDIS_CLI), "-s", str(socket), "PING"],
                                      stdout=subprocess.PIPE, stderr=subprocess.DEVNULL,
                                      env=env, timeout=2, check=False)
                if ping.returncode == 0 and ping.stdout.strip() == b"PONG":
                    break
                time.sleep(0.1)
            else:
                raise RuntimeError("private Redis socket did not become ready")

            test_env = {
                **env,
                "GOTOOLCHAIN": "local",
                "GOPROXY": "off",
                "GOSUMDB": "off",
                "GOCACHE": "/private/tmp/nerae-go-api-cache",
                "GOMODCACHE": "/private/tmp/nerae-go-api-mod",
                "NERAE_RATELIMIT_REDIS_SOCKET": str(socket),
            }
            result = subprocess.run(
                [str(GO), "test", "-race", "-count=1", "-v", "./internal/modules/auth/ratelimit"],
                cwd=ROOT / "apps/api", env=test_env, timeout=240, check=False,
            )
            if result.returncode:
                raise RuntimeError(f"rate limiter Redis integration tests failed ({result.returncode})")
            shutdown = subprocess.run([str(REDIS_CLI), "-s", str(socket), "SHUTDOWN", "NOSAVE"],
                                      stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL,
                                      env=env, timeout=10, check=False)
            if child.wait(timeout=15) != 0:
                raise RuntimeError("private Redis did not stop cleanly")
            succeeded = True
    except (OSError, subprocess.TimeoutExpired, RuntimeError) as error:
        print(f"FAIL: {error}; private Redis directory retained: {base}", file=sys.stderr)
    finally:
        if child is not None and child.poll() is None:
            child.terminate()
            try:
                child.wait(timeout=10)
            except subprocess.TimeoutExpired:
                child.kill()
                child.wait(timeout=5)
        if succeeded and (child is None or child.poll() is not None):
            shutil.rmtree(base)
    if succeeded:
        print("PASS: isolated socket-only Redis rate-limit race and failure-closed verification")
        return 0
    return 1


if __name__ == "__main__":
    try:
        sys.exit(main())
    except RuntimeError as error:
        print(f"FAIL: {error}", file=sys.stderr)
        sys.exit(1)
