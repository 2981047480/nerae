#!/usr/bin/env python3
"""Run isolated native-MySQL migration smoke checks for two temporary Git worktrees.

This script is deliberately not run by its authoring task. It never connects to
an existing database: it launches two mysqld children that bind only to
127.0.0.1 and uses their private Unix sockets for root provisioning/shutdown.
"""
from __future__ import annotations

import argparse
import getpass
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import socket
import subprocess
import sys
import tempfile
import time
from typing import Any

SOURCE_ROOT = Path(__file__).resolve().parents[3]
TEMP_PARENT = Path("/tmp").resolve()
DB_NAME_RE = re.compile(r"^nerae_test_[a-f0-9]{16}$")
MODULE_RE = re.compile(r"^[a-z][a-z0-9_]{0,31}$")


class SmokeError(RuntimeError):
    pass


def git_env() -> dict[str, str]:
    return {key: value for key, value in os.environ.items() if not key.startswith("GIT_")}


def run_git(cwd: Path, *args: str) -> subprocess.CompletedProcess[str]:
    return subprocess.run(
        ["git", "-C", str(cwd), *args],
        check=True,
        text=True,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        env=git_env(),
    )


def free_loopback_ports(count: int) -> list[int]:
    ports: list[int] = []
    sockets: list[socket.socket] = []
    try:
        for _ in range(count):
            sock = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
            sock.bind(("127.0.0.1", 0))
            sockets.append(sock)
            ports.append(int(sock.getsockname()[1]))
    finally:
        for sock in sockets:
            sock.close()
    if len(set(ports)) != count:
        raise SmokeError("could not reserve distinct candidate loopback ports")
    return ports


def private_state(worktree: Path) -> tuple[dict[str, Any], str, str]:
    state_dir = worktree / ".review" / "nerae-test-env"
    state_stat = state_dir.lstat()
    if not state_dir.is_dir() or state_dir.is_symlink() or state_stat.st_mode & 0o077:
        raise SmokeError("workspace init did not create a private state directory")
    if state_stat.st_uid != os.getuid():
        raise SmokeError("workspace state directory has an unexpected owner")

    def read_private(name: str) -> str:
        path = state_dir / name
        info = path.lstat()
        if path.is_symlink() or not path.is_file() or info.st_mode & 0o077 or info.st_uid != os.getuid():
            raise SmokeError(f"workspace state file is not private: {name}")
        return path.read_text(encoding="utf-8")

    state = json.loads(read_private("state.json"))
    for field in ("namespace", "database", "mysql_port", "redis_port", "workspace", "environment", "version"):
        if field not in state:
            raise SmokeError("workspace state is missing a required field")
    if state["environment"] != "test" or state["workspace"] != str(worktree.resolve()):
        raise SmokeError("workspace state does not match its temporary worktree")
    if state["namespace"] != state["database"] or not DB_NAME_RE.fullmatch(state["database"]):
        raise SmokeError("workspace database name failed the test-namespace guard")
    for port_key in ("mysql_port", "redis_port"):
        port = state[port_key]
        if type(port) is not int or not 1024 <= port <= 65535:
            raise SmokeError("workspace state contains an invalid port")
    if state["mysql_port"] == state["redis_port"]:
        raise SmokeError("workspace assigned the same MySQL and Redis port")
    return state, read_private("mysql-root-password"), read_private("mysql-app-password")


def sql_literal(value: str) -> str:
    return "'" + value.replace("\\", "\\\\").replace("'", "\\'") + "'"


def write_mysql_defaults(path: Path, password: str, socket_path: Path) -> None:
    # mysql client option files use backslash escapes. File permissions remain 0600.
    escaped = password.replace("\\", "\\\\").replace('"', '\\"')
    content = f'[client]\nuser=root\npassword="{escaped}"\nprotocol=socket\nsocket={socket_path}\n'
    fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    with os.fdopen(fd, "w", encoding="utf-8") as handle:
        handle.write(content)


def mysql_client(mysql_bin: Path, defaults_file: Path, sql: str) -> str:
    # No password appears in argv or in emitted output.
    result = subprocess.run(
        [str(mysql_bin), f"--defaults-file={defaults_file}", "--batch", "--skip-column-names"],
        input=sql,
        check=False,
        text=True,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        timeout=15,
        env={**os.environ, "MYSQL_PWD": ""},
    )
    if result.returncode != 0:
        raise SmokeError(f"private local mysql client command failed (exit {result.returncode})")
    return result.stdout.strip()


def mysql_admin(mysqladmin_bin: Path, defaults_file: Path, *args: str) -> subprocess.CompletedProcess[str]:
    return subprocess.run(
        [str(mysqladmin_bin), f"--defaults-file={defaults_file}", *args],
        check=False,
        text=True,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        timeout=15,
        env={**os.environ, "MYSQL_PWD": ""},
    )


def wait_for_mysql(mysqladmin_bin: Path, defaults_file: Path, process: subprocess.Popen[Any], timeout: float = 30.0) -> None:
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        if process.poll() is not None:
            raise SmokeError("script-owned mysqld exited before its private socket became ready")
        ping = mysql_admin(mysqladmin_bin, defaults_file, "ping", "--silent")
        if ping.returncode == 0:
            return
        time.sleep(0.2)
    raise SmokeError("script-owned mysqld did not become ready before timeout")


def stop_owned_mysql(instance: dict[str, Any]) -> None:
    process = instance.get("process")
    try:
        if process is not None and process.poll() is None:
            # Only signal the exact child launched by this script; do not trust a PID file.
            process.terminate()
            try:
                process.wait(timeout=15)
            except subprocess.TimeoutExpired:
                process.kill()
                process.wait(timeout=5)
        if process is not None and process.poll() is None:
            raise SmokeError("script-owned MySQL process did not stop")
    finally:
        log = instance.get("log_handle")
        if log is not None:
            log.close()


def redis_call(cli: Path, socket_path: Path, *args: str) -> str:
    result = subprocess.run([str(cli), "-s", str(socket_path), "--raw", *args],
                            check=True, text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE, timeout=10)
    return result.stdout.strip()


def start_redis(instance: dict[str, Any], server: Path, cli: Path) -> None:
    directory = instance["datadir"].parent
    socket_path = directory / "redis.sock"
    log = (directory / "redis.log").open("xb")
    instance["redis_log"] = log
    instance["redis_process"] = subprocess.Popen(
        [str(server), "--bind", "127.0.0.1", "--port", str(instance["state"]["redis_port"]),
         "--unixsocket", str(socket_path), "--unixsocketperm", "700", "--save", "",
         "--appendonly", "no", "--protected-mode", "yes", "--dir", str(directory)],
        stdin=subprocess.DEVNULL, stdout=log, stderr=subprocess.STDOUT)
    process = instance["redis_process"]
    deadline = time.monotonic() + 15
    while time.monotonic() < deadline:
        if process.poll() is not None:
            raise SmokeError("script-owned Redis exited before readiness")
        try:
            if redis_call(cli, socket_path, "PING") == "PONG":
                info = redis_call(cli, socket_path, "INFO", "server")
                if f"process_id:{process.pid}" not in info:
                    raise SmokeError("Redis process identity differs from script-owned child")
                instance["redis_socket"] = socket_path
                return
        except subprocess.CalledProcessError:
            pass
        time.sleep(0.1)
    raise SmokeError("script-owned Redis did not become ready")


def stop_owned_redis(instance: dict[str, Any]) -> None:
    process = instance.get("redis_process")
    try:
        if process is not None and process.poll() is None:
            process.terminate()
            try:
                process.wait(timeout=10)
            except subprocess.TimeoutExpired:
                process.kill()
                process.wait(timeout=5)
        if process is not None and process.poll() is None:
            raise SmokeError("script-owned Redis process did not stop")
    finally:
        log = instance.get("redis_log")
        if log is not None:
            log.close()


def initialize_instance(instance: dict[str, Any]) -> None:
    datadir: Path = instance["datadir"]
    datadir.mkdir(mode=0o700)
    mysqld: Path = instance["mysqld_bin"]
    basedir: Path = instance["mysql_basedir"]
    socket_path: Path = instance["socket_path"]
    pid_file: Path = instance["pid_file"]
    port: int = instance["state"]["mysql_port"]
    current_user = getpass.getuser()
    common = [
        str(mysqld),
        "--no-defaults",
        f"--basedir={basedir}",
        f"--datadir={datadir}",
        f"--user={current_user}",
        "--skip-name-resolve",
        "--mysqlx=0",
        "--skip-log-bin",
        "--innodb-buffer-pool-size=64M",
        "--innodb-redo-log-capacity=16M",
    ]
    init = subprocess.run(
        [*common, "--initialize-insecure"],
        check=False,
        text=True,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        timeout=90,
    )
    if init.returncode != 0:
        raise SmokeError(f"native MySQL initialize failed (exit {init.returncode}): {init.stderr[-800:]}")

    log_handle = (instance["log_path"]).open("xb")
    instance["log_handle"] = log_handle
    process = subprocess.Popen(
        [
            *common,
            "--console",
            "--daemonize=0",
            "--bind-address=127.0.0.1",
            f"--port={port}",
            f"--socket={socket_path}",
            f"--pid-file={pid_file}",
            "--local-infile=0",
        ],
        stdin=subprocess.DEVNULL,
        stdout=log_handle,
        stderr=subprocess.STDOUT,
        env={**os.environ, "MYSQL_PWD": ""},
    )
    instance["process"] = process
    initial_defaults = instance["initial_defaults"]
    wait_for_mysql(instance["mysqladmin_bin"], initial_defaults, process)

    # The freshly initialized server starts with only its local socket root account.
    password = instance["root_password"]
    initial_sql = (
        "ALTER USER 'root'@'localhost' IDENTIFIED BY " + sql_literal(password)
    )
    mysql_client(instance["mysql_bin"], initial_defaults, initial_sql)
    write_mysql_defaults(instance["root_defaults"], password, socket_path)
    instance["root_config_ready"] = True

    database = instance["state"]["database"]
    app_password = instance["app_password"]
    mysql_client(
        instance["mysql_bin"],
        instance["root_defaults"],
        f"CREATE DATABASE `{database}` CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_ai_ci; "
        f"CREATE USER 'nerae_app'@'127.0.0.1' IDENTIFIED BY {sql_literal(app_password)}; "
        f"GRANT ALL PRIVILEGES ON `{database}`.* TO 'nerae_app'@'127.0.0.1'; FLUSH PRIVILEGES;",
    )


def verify_mysql_tcp_owner(instance: dict[str, Any]) -> None:
    process = instance["process"]
    if process is None or process.poll() is not None:
        raise SmokeError("script-owned MySQL process is not running")
    lsof = shutil.which("lsof")
    if lsof is None:
        raise SmokeError("lsof is required to verify the MySQL port owner")
    port = instance["state"]["mysql_port"]
    result = subprocess.run([lsof, "-nP", "-t", f"-iTCP:{port}", "-sTCP:LISTEN"],
                            check=False, text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE, timeout=10)
    pids = {int(line) for line in result.stdout.splitlines() if line.isdecimal()}
    if result.returncode != 0 or pids != {process.pid} or process.poll() is not None:
        raise SmokeError("MySQL TCP port is not owned solely by the script-launched process")


def run_migration(go_bin: Path, worktree: Path, action: str, env: dict[str, str]) -> subprocess.CompletedProcess[str]:
    api_dir = worktree / "apps" / "api"
    return subprocess.run(
        [str(go_bin), "run", "../../scripts/dev/migrate.go", action],
        cwd=api_dir,
        check=False,
        text=True,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        env=env,
        timeout=120,
    )


def add_synthetic_migration(seed_root: Path) -> None:
    module = "smoke"
    filename = "0001_isolation_probe.up.sql"
    migration_dir = seed_root / "apps" / "api" / "migrations" / module
    migration_dir.mkdir(parents=True, exist_ok=False)
    sql = (
        "CREATE TABLE runtime_smoke_data ("
        "id BIGINT NOT NULL PRIMARY KEY, payload VARCHAR(64) NOT NULL"
        ") ENGINE=InnoDB;\n"
    )
    (migration_dir / filename).write_text(sql, encoding="utf-8")
    digest = hashlib.sha256(sql.encode("utf-8")).hexdigest()
    manifest = {
        "version": 1,
        "migrations": [{
            "order": 1,
            "id": f"{module}/{filename}",
            "module": module,
            "file": filename,
            "sha256": digest,
        }],
    }
    (seed_root / "scripts" / "dev" / "migrations.json").write_text(
        json.dumps(manifest, indent=2) + "\n", encoding="utf-8"
    )


def check_run(result: subprocess.CompletedProcess[str], expected: int, label: str, needle: str | None = None) -> None:
    if result.returncode != expected:
        raise SmokeError(f"{label} returned {result.returncode}; expected {expected}: {(result.stdout + result.stderr)[-1000:]}")
    combined = result.stdout + result.stderr
    if needle is not None and needle not in combined:
        raise SmokeError(f"{label} did not report the expected {needle!r} condition")


def create_temp_git_worktrees(base: Path, source_root: Path) -> tuple[Path, list[Path]]:
    seed = base / "seed"
    seed.mkdir(mode=0o700)
    shutil.copytree(source_root / "scripts" / "dev", seed / "scripts" / "dev")
    api = seed / "apps" / "api"
    api.mkdir(parents=True)
    for name in ("go.mod", "go.sum"):
        shutil.copy2(source_root / "apps" / "api" / name, api / name)
    add_synthetic_migration(seed)

    run_git(seed, "init", "--quiet")
    run_git(seed, "config", "user.name", "Nerae temporary smoke")
    run_git(seed, "config", "user.email", "smoke.invalid@example.invalid")
    run_git(seed, "add", "scripts/dev", "apps/api/go.mod", "apps/api/go.sum", "apps/api/migrations")
    run_git(seed, "commit", "--quiet", "-m", "temporary native MySQL migration smoke fixture")
    worktrees = [base / "worktree-a", base / "worktree-b"]
    for worktree in worktrees:
        run_git(seed, "worktree", "add", "--quiet", "--detach", str(worktree), "HEAD")
    return seed, worktrees


def quote_for_sql_check(value: str) -> str:
    if not DB_NAME_RE.fullmatch(value):
        raise SmokeError("refusing a non-test database name")
    return f"`{value}`"


def run_smoke(args: argparse.Namespace) -> None:
    source_root = args.source_root.resolve(strict=True)
    if not (source_root / ".git").exists() and not (source_root / ".git").is_file():
        raise SmokeError("source root is not a Git worktree")
    for required in (
        source_root / "scripts/dev/workspace.py",
        source_root / "scripts/dev/migrate.go",
        source_root / "apps/api/go.mod",
        source_root / "apps/api/go.sum",
    ):
        if not required.is_file():
            raise SmokeError(f"required source file is absent: {required.name}")

    binaries = [args.mysqld, args.mysql, args.mysqladmin, args.go, args.redis_server, args.redis_cli]
    for binary in binaries:
        if not binary.is_absolute() or not binary.is_file() or not os.access(binary, os.X_OK):
            raise SmokeError("each supplied Go/MySQL binary must be an absolute executable file")
    mysql_basedir = args.mysql_basedir.resolve(strict=True)
    if not mysql_basedir.is_dir():
        raise SmokeError("MySQL basedir is not a directory")

    base = Path(tempfile.mkdtemp(prefix="nerae-mysql-native-smoke-", dir=TEMP_PARENT))
    os.chmod(base, 0o700)
    if base.resolve().parent != TEMP_PARENT.resolve():
        raise SmokeError("temporary directory escaped the approved private temp parent")
    instances: list[dict[str, Any]] = []
    seed: Path | None = None
    worktrees: list[Path] = []
    succeeded = False
    try:
        seed, worktrees = create_temp_git_worktrees(base, source_root)
        ports = free_loopback_ports(4)
        for index, worktree in enumerate(worktrees):
            # Each workspace.py invocation allocates immutable, distinct test ports and private secrets.
            init = subprocess.run(
                [sys.executable, str(worktree / "scripts/dev/workspace.py"), "init",
                 "--mysql-port", str(ports[index * 2]), "--redis-port", str(ports[index * 2 + 1])],
                cwd=worktree,
                check=False,
                text=True,
                stdout=subprocess.PIPE,
                stderr=subprocess.PIPE,
                env=git_env(),
            )
            if init.returncode != 0:
                raise SmokeError(f"workspace.py init failed for temporary worktree {index + 1} (exit {init.returncode})")
            state, root_password, app_password = private_state(worktree)
            instances.append({
                "worktree": worktree,
                "state": state,
                "root_password": root_password,
                "app_password": app_password,
                "datadir": base / f"mysql-{index + 1}" / "data",
                "socket_path": base / f"mysql-{index + 1}" / "mysql.sock",
                "pid_file": base / f"mysql-{index + 1}" / "mysqld.pid",
                "log_path": base / f"mysql-{index + 1}" / "mysqld.log",
                "initial_defaults": base / f"mysql-{index + 1}" / "initial.cnf",
                "root_defaults": base / f"mysql-{index + 1}" / "root.cnf",
                "mysql_bin": args.mysql,
                "mysqladmin_bin": args.mysqladmin,
                "mysqld_bin": args.mysqld,
                "mysql_basedir": mysql_basedir,
                "process": None,
                "root_config_ready": False,
            })
            instance_dir = base / f"mysql-{index + 1}"
            instance_dir.mkdir(mode=0o700)
            write_mysql_defaults(instances[-1]["initial_defaults"], "", instances[-1]["socket_path"])

        if instances[0]["state"]["database"] == instances[1]["state"]["database"]:
            raise SmokeError("workspace.py assigned the same test database namespace twice")
        if instances[0]["state"]["mysql_port"] == instances[1]["state"]["mysql_port"]:
            raise SmokeError("workspace.py assigned the same MySQL port twice")

        for instance in instances:
            initialize_instance(instance)
            start_redis(instance, args.redis_server, args.redis_cli)

        # Verify both Redis processes while both independent MySQL instances are running.
        a_socket, b_socket = instances[0]["redis_socket"], instances[1]["redis_socket"]
        prefix_a, prefix_b = instances[0]["state"]["redis_prefix"], instances[1]["state"]["redis_prefix"]
        if prefix_a == prefix_b:
            raise SmokeError("worktrees share a Redis prefix")
        if redis_call(args.redis_cli, a_socket, "SET", prefix_a + "probe", "a-only") != "OK":
            raise SmokeError("cannot write to Redis A")
        if redis_call(args.redis_cli, b_socket, "SET", prefix_b + "probe", "b-only") != "OK":
            raise SmokeError("cannot write to Redis B")
        if (redis_call(args.redis_cli, a_socket, "GET", prefix_a + "probe") != "a-only" or
            redis_call(args.redis_cli, b_socket, "GET", prefix_b + "probe") != "b-only" or
            redis_call(args.redis_cli, a_socket, "EXISTS", prefix_b + "probe") != "0" or
            redis_call(args.redis_cli, b_socket, "EXISTS", prefix_a + "probe") != "0"):
            raise SmokeError("Redis data isolation check failed")

        # Fresh migration state is empty before the real migration command is invoked.
        for instance in instances:
            db = quote_for_sql_check(instance["state"]["database"])
            tables = mysql_client(instance["mysql_bin"], instance["root_defaults"], f"SHOW TABLES FROM {db}")
            if tables:
                raise SmokeError("test database was not empty before first migration")

        go_env = {
            **os.environ,
            "GOTOOLCHAIN": "local",
            "GOCACHE": str(args.go_cache),
            "GOMODCACHE": str(args.go_mod_cache),
            "GOSUMDB": "off",
        }
        if args.goproxy:
            go_env["GOPROXY"] = args.goproxy
        for number, instance in enumerate(instances, start=1):
            verify_mysql_tcp_owner(instance)
            first = run_migration(args.go, instance["worktree"], "up", go_env)
            check_run(first, 0, f"first migration up for instance {number}")
            db = quote_for_sql_check(instance["state"]["database"])
            count = mysql_client(
                instance["mysql_bin"], instance["root_defaults"],
                f"SELECT COUNT(*) FROM {db}.nerae_schema_migrations",
            )
            if count != "1":
                raise SmokeError(f"first migration did not record exactly one clean migration for instance {number}")
            dirty = mysql_client(
                instance["mysql_bin"], instance["root_defaults"],
                f"SELECT COUNT(*) FROM {db}.nerae_schema_migrations WHERE dirty = 1",
            )
            if dirty != "0":
                raise SmokeError(f"first migration left a dirty row in instance {number}")
            verify_mysql_tcp_owner(instance)
            repeat = run_migration(args.go, instance["worktree"], "up", go_env)
            check_run(repeat, 0, f"repeated migration up for instance {number}")

        # Data inserted only in A must not be visible in B's independently namespaced DB.
        db_a = quote_for_sql_check(instances[0]["state"]["database"])
        db_b = quote_for_sql_check(instances[1]["state"]["database"])
        mysql_client(
            instances[0]["mysql_bin"], instances[0]["root_defaults"],
            f"INSERT INTO {db_a}.runtime_smoke_data (id, payload) VALUES (1, 'only-instance-a')",
        )
        visible_a = mysql_client(
            instances[0]["mysql_bin"], instances[0]["root_defaults"],
            f"SELECT COUNT(*) FROM {db_a}.runtime_smoke_data WHERE payload = 'only-instance-a'",
        )
        visible_b = mysql_client(
            instances[1]["mysql_bin"], instances[1]["root_defaults"],
            f"SELECT COUNT(*) FROM {db_b}.runtime_smoke_data WHERE payload = 'only-instance-a'",
        )
        if visible_a != "1" or visible_b != "0":
            raise SmokeError("synthetic row crossed isolated MySQL instances")

        # Cause a synthetic migration failure after one successful DDL statement.
        worktree_a = instances[0]["worktree"]
        failed_sql = "CREATE TABLE partial_smoke (id INT); INSERT INTO missing_smoke_table VALUES (1);\n"
        failed_name = "0002_failure_probe.up.sql"
        (worktree_a / "apps/api/migrations/smoke" / failed_name).write_text(failed_sql, encoding="utf-8")
        manifest_path = worktree_a / "scripts/dev/migrations.json"
        manifest = json.loads(manifest_path.read_text(encoding="utf-8"))
        manifest["migrations"].append({"order": 2, "id": "smoke/" + failed_name,
                                       "module": "smoke", "file": failed_name,
                                       "sha256": hashlib.sha256(failed_sql.encode()).hexdigest()})
        manifest_path.write_text(json.dumps(manifest, indent=2) + "\n", encoding="utf-8")
        verify_mysql_tcp_owner(instances[0])
        failed = run_migration(args.go, worktree_a, "up", go_env)
        check_run(failed, 1, "failing migration", "remains dirty")
        dirty_count = mysql_client(instances[0]["mysql_bin"], instances[0]["root_defaults"],
                                   f"SELECT COUNT(*) FROM {db_a}.nerae_schema_migrations WHERE sequence_no=2 AND dirty=1")
        partial_count = mysql_client(instances[0]["mysql_bin"], instances[0]["root_defaults"],
                                     f"SELECT COUNT(*) FROM information_schema.tables WHERE table_schema='{instances[0]['state']['database']}' AND table_name='partial_smoke'")
        if dirty_count != "1" or partial_count != "1":
            raise SmokeError("real failed DDL did not leave the expected dirty and partial state")
        verify_mysql_tcp_owner(instances[0])
        dirty_retry = run_migration(args.go, worktree_a, "up", go_env)
        check_run(dirty_retry, 1, "dirty migration guard", "dirty")
        still_isolated = mysql_client(instances[1]["mysql_bin"], instances[1]["root_defaults"],
                                      f"SELECT COUNT(*) FROM {db_b}.nerae_schema_migrations WHERE dirty=1")
        if still_isolated != "0":
            raise SmokeError("dirty state crossed into the other test database")

        print("PASS: two private Git worktrees initialized with distinct test namespaces and ports")
        print("PASS: two MySQL and two Redis instances running simultaneously on distinct loopback ports")
        print("PASS: Redis worktree prefixes and cross-instance reads are isolated")
        print("PASS: empty database, first migration, repeat migration and data isolation")
        print("PASS: synthetic partially applied failing migration remains dirty and retry is rejected")
        print("Credentials and server logs were not printed.")
        succeeded = True
    finally:
        cleanup_errors = []
        for instance in reversed(instances):
            for stop in (stop_owned_redis, stop_owned_mysql):
                try:
                    stop(instance)
                except Exception as exc:
                    cleanup_errors.append(type(exc).__name__)
        if cleanup_errors:
            print(f"FAILED to stop a script-owned process; temporary files retained at {base}", file=sys.stderr)
            raise SmokeError("cannot safely remove temporary MySQL data while a child may be running")
        if succeeded:
            shutil.rmtree(base)
        else:
            print(f"Failure evidence retained in private temporary directory {base}", file=sys.stderr)


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--source-root", type=Path, default=SOURCE_ROOT)
    parser.add_argument("--go", type=Path, required=True, help="absolute path to the pinned Go executable")
    parser.add_argument("--mysqld", type=Path, required=True, help="absolute path to MySQL 8.4.13 mysqld")
    parser.add_argument("--mysql", type=Path, required=True, help="absolute path to matching mysql client")
    parser.add_argument("--mysqladmin", type=Path, required=True, help="absolute path to matching mysqladmin")
    parser.add_argument("--mysql-basedir", type=Path, required=True, help="absolute MySQL installation basedir")
    parser.add_argument("--redis-server", type=Path, required=True, help="absolute path to temporary Redis server binary")
    parser.add_argument("--redis-cli", type=Path, required=True, help="absolute path to matching Redis client binary")
    parser.add_argument("--go-cache", type=Path, required=True, help="existing isolated Go build cache")
    parser.add_argument("--go-mod-cache", type=Path, required=True, help="existing pinned Go module cache")
    parser.add_argument("--goproxy", default="off", help="Go proxy; defaults to off")
    return parser.parse_args()


def main() -> int:
    try:
        run_smoke(parse_args())
        return 0
    except (OSError, subprocess.SubprocessError, SmokeError, ValueError) as exc:
        print(f"FAIL: {exc}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
