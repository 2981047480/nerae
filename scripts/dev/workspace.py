#!/usr/bin/env python3
"""Manage one explicitly isolated, local-only Nerae test environment per worktree."""
from __future__ import annotations

import argparse
import contextlib
import fcntl
import hashlib
import json
import os
from pathlib import Path
import re
import secrets
import socket
import stat
import subprocess
import sys


class EnvironmentError_(RuntimeError):
    pass


def workspace_identity(root: Path) -> dict:
    root = root.resolve(strict=True)
    token = hashlib.sha256(os.fsencode(str(root))).hexdigest()[:16]
    namespace = f"nerae_test_{token}"
    return {
        "version": 1,
        "environment": "test",
        "workspace": str(root),
        "namespace": namespace,
        "database": namespace,
        "redis_prefix": f"nerae:test:{token}:",
        "object_prefix": f"nerae/test/{token}/",
    }


def validate_port(value: int) -> int:
    if type(value) is not int or not 1024 <= value <= 65535:
        raise EnvironmentError_("ports must be distinct integers from 1024 to 65535")
    return value


def state_dir(root: Path) -> Path:
    # .review is ignored by Git. Refuse symlinks rather than following a path elsewhere.
    review = root / ".review"
    state = review / "nerae-test-env"
    for path in (review, state):
        if path.is_symlink():
            raise EnvironmentError_(f"refusing symlink in local state path: {path}")
    return state


def validate_state(root: Path, state: dict) -> dict:
    identity = workspace_identity(root)
    if type(state) is not dict or set(state) != set(identity) | {"mysql_port", "redis_port"}:
        raise EnvironmentError_("invalid local test environment state")
    if any(type(state.get(k)) is not type(v) or state.get(k) != v for k, v in identity.items()):
        raise EnvironmentError_("state does not belong to this test worktree")
    mysql_port = validate_port(state["mysql_port"])
    redis_port = validate_port(state["redis_port"])
    if mysql_port == redis_port:
        raise EnvironmentError_("MySQL and Redis ports must differ")
    return state


def check_private_file(path: Path) -> None:
    try:
        info = path.lstat()
    except FileNotFoundError as exc:
        raise EnvironmentError_(f"missing local state file: {path.name}") from exc
    if not stat.S_ISREG(info.st_mode) or info.st_mode & 0o077 or info.st_uid != os.getuid():
        raise EnvironmentError_(f"local state file must be owned by the current user and private: {path.name}")


def _unique_object(pairs: list[tuple[str, object]]) -> dict:
    result = {}
    for key, value in pairs:
        if key in result:
            raise ValueError("duplicate state field")
        result[key] = value
    return result


def read_state(root: Path) -> dict:
    directory = state_dir(root)
    try:
        info = directory.lstat()
    except FileNotFoundError as exc:
        raise EnvironmentError_("local test environment has not been initialized") from exc
    if not stat.S_ISDIR(info.st_mode) or info.st_mode & 0o077 or info.st_uid != os.getuid():
        raise EnvironmentError_("local test environment directory must be private and owned by the current user")
    config = directory / "state.json"
    check_private_file(config)
    try:
        state = json.loads(config.read_text(encoding="utf-8"), object_pairs_hook=_unique_object)
    except (ValueError, OSError) as exc:
        raise EnvironmentError_("cannot parse local environment state") from exc
    validate_state(root, state)
    for name in ("mysql-root-password", "mysql-app-password"):
        check_private_file(directory / name)
    return state


def available(port: int) -> None:
    with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as sock:
        try:
            sock.bind(("127.0.0.1", port))
        except OSError as exc:
            raise EnvironmentError_(f"loopback port {port} is unavailable") from exc


def write_private(path: Path, value: str) -> None:
    flags = os.O_WRONLY | os.O_CREAT | os.O_EXCL
    flags |= getattr(os, "O_NOFOLLOW", 0)
    fd = os.open(path, flags, 0o600)
    with os.fdopen(fd, "w", encoding="utf-8") as handle:
        handle.write(value)


def _initialize_locked(root: Path, mysql_port: int, redis_port: int) -> dict:
    root = root.resolve(strict=True)
    validate_port(mysql_port)
    validate_port(redis_port)
    if mysql_port == redis_port:
        raise EnvironmentError_("MySQL and Redis ports must differ")
    directory = state_dir(root)
    if directory.exists():
        existing = read_state(root)
        if (existing["mysql_port"], existing["redis_port"]) != (mysql_port, redis_port):
            raise EnvironmentError_("state already exists with different ports; no data was changed")
        assert_ports_unreserved(root, mysql_port, redis_port)
        return existing
    assert_ports_unreserved(root, mysql_port, redis_port)
    available(mysql_port)
    available(redis_port)
    directory.parent.mkdir(mode=0o700, exist_ok=True)
    directory.mkdir(mode=0o700, exist_ok=False)
    # Create credentials first, and write the state last; partial setup is never silently reused.
    write_private(directory / "mysql-root-password", secrets.token_urlsafe(32))
    write_private(directory / "mysql-app-password", secrets.token_urlsafe(32))
    state = workspace_identity(root) | {"mysql_port": mysql_port, "redis_port": redis_port}
    write_private(directory / "state.json", json.dumps(state, ensure_ascii=False, indent=2) + "\n")
    return state


def git_env() -> dict:
    # Git's environment can otherwise redirect operations into another worktree.
    return {key: value for key, value in os.environ.items() if not key.startswith("GIT_")}


def git_output(root: Path, *args: str) -> str:
    result = subprocess.run(["git", "-C", str(root), *args], check=True, text=True,
                            stdout=subprocess.PIPE, stderr=subprocess.PIPE, env=git_env())
    return result.stdout


def root_from_git() -> Path:
    cwd = Path.cwd().resolve(strict=True)
    root = Path(git_output(cwd, "rev-parse", "--show-toplevel").strip()).resolve(strict=True)
    script_root = Path(__file__).resolve(strict=True).parents[2]
    if root != script_root:
        raise EnvironmentError_("run this script from its own Git worktree")
    return root


def worktree_roots(root: Path) -> list[Path]:
    output = git_output(root, "worktree", "list", "--porcelain", "-z")
    paths = []
    for record in output.split("\0\0"):
        if not record:
            continue
        fields = record.split("\0")
        if not fields[0].startswith("worktree "):
            raise EnvironmentError_("cannot interpret Git worktree list")
        path = Path(fields[0][len("worktree "):])
        if path.is_dir():  # Git can list prunable worktrees that no longer exist.
            paths.append(path.resolve(strict=True))
    return paths


def assert_ports_unreserved(root: Path, mysql_port: int, redis_port: int) -> None:
    requested = {mysql_port, redis_port}
    for other in worktree_roots(root):
        if other == root:
            continue
        directory = state_dir(other)
        if not directory.exists() and not directory.is_symlink():
            continue
        state = read_state(other)
        if requested.intersection({state["mysql_port"], state["redis_port"]}):
            raise EnvironmentError_(f"port already assigned to another worktree: {other}")


@contextlib.contextmanager
def port_lock(root: Path):
    common = Path(git_output(root, "rev-parse", "--path-format=absolute", "--git-common-dir").strip()).resolve(strict=True)
    lock_id = hashlib.sha256(os.fsencode(str(common))).hexdigest()[:20]
    lock = Path("/tmp").resolve() / f"nerae-test-ports-{os.getuid()}-{lock_id}.lock"
    flags = os.O_CREAT | os.O_RDWR | getattr(os, "O_NOFOLLOW", 0)
    fd = os.open(lock, flags, 0o600)
    try:
        info = os.fstat(fd)
        if not stat.S_ISREG(info.st_mode) or info.st_mode & 0o077 or info.st_uid != os.getuid():
            raise EnvironmentError_("test port reservation lock is not private")
        fcntl.flock(fd, fcntl.LOCK_EX)
        try:
            yield
        finally:
            fcntl.flock(fd, fcntl.LOCK_UN)
    finally:
        os.close(fd)


def initialize(root: Path, mysql_port: int, redis_port: int) -> dict:
    root = root.resolve(strict=True)
    with port_lock(root):
        return _initialize_locked(root, mysql_port, redis_port)


def docker_env(root: Path, state: dict) -> dict:
    env = {k: v for k, v in os.environ.items() if not (
        k.startswith("NERAE_") or k.startswith("COMPOSE_") or k.startswith("MYSQL_")
    )}
    if env.get("DOCKER_HOST") or env.get("DOCKER_CONTEXT"):
        raise EnvironmentError_("unset DOCKER_HOST and DOCKER_CONTEXT; only a local Docker context is allowed")
    directory = state_dir(root)
    env.update({
        "COMPOSE_DISABLE_ENV_FILE": "1",
        "NERAE_TEST_DATABASE": state["database"],
        "NERAE_TEST_MYSQL_PORT": str(state["mysql_port"]),
        "NERAE_TEST_REDIS_PORT": str(state["redis_port"]),
        "NERAE_TEST_MYSQL_ROOT_PASSWORD_FILE": str(directory / "mysql-root-password"),
        "NERAE_TEST_MYSQL_APP_PASSWORD_FILE": str(directory / "mysql-app-password"),
    })
    return env


def require_local_docker(env: dict) -> None:
    try:
        result = subprocess.run(["docker", "context", "inspect"], env=env, check=True,
                                text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
        contexts = json.loads(result.stdout)
        host = contexts[0]["Endpoints"]["docker"]["Host"]
    except (FileNotFoundError, subprocess.CalledProcessError, ValueError, KeyError, IndexError) as exc:
        raise EnvironmentError_("Docker with a readable local context is required") from exc
    if not isinstance(host, str) or not host.startswith("unix:///"):
        raise EnvironmentError_("refusing a non-local Docker context")


def compose(root: Path, state: dict, arguments: list[str]) -> None:
    env = docker_env(root, state)
    require_local_docker(env)
    command = ["docker", "compose", "--project-name", state["namespace"],
               "--project-directory", str(root), "-f", str(root / "infra/compose/test.yml"), *arguments]
    try:
        subprocess.run(command, env=env, check=True)
    except (FileNotFoundError, subprocess.CalledProcessError) as exc:
        raise EnvironmentError_("Docker Compose command failed; existing data was not reset") from exc


def describe(state: dict) -> None:
    for key in ("workspace", "environment", "namespace", "database", "mysql_port", "redis_port",
                "redis_prefix", "object_prefix"):
        print(f"{key}: {state[key]}")


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    actions = parser.add_subparsers(dest="action", required=True)
    init = actions.add_parser("init", help="create private credentials and an immutable port assignment")
    init.add_argument("--mysql-port", required=True, type=int)
    init.add_argument("--redis-port", required=True, type=int)
    actions.add_parser("show", help="show non-secret isolation settings")
    actions.add_parser("up", help="start the existing test environment without resetting data")
    clean = actions.add_parser("clean", help="show or remove only this test project's containers and volumes")
    clean.add_argument("--namespace", required=True, help="exact current test namespace shown by 'show'")
    clean.add_argument("--execute", action="store_true", help="actually remove the specified test project's volumes")
    clean.add_argument("--confirm", help="repeat the exact namespace when --execute is supplied")
    args = parser.parse_args(argv)
    try:
        root = root_from_git()
        if args.action == "init":
            describe(initialize(root, args.mysql_port, args.redis_port))
            return 0
        state = read_state(root)
        if args.action == "show":
            describe(state)
        elif args.action == "up":
            with port_lock(root):
                assert_ports_unreserved(root, state["mysql_port"], state["redis_port"])
                describe(state)
                compose(root, state, ["up", "-d"])
        elif args.action == "clean":
            if args.namespace != state["namespace"]:
                raise EnvironmentError_("namespace must exactly match this worktree's test namespace")
            describe(state)
            if not args.execute:
                print("dry run: no containers, volumes, credentials or data removed")
            else:
                if args.confirm != state["namespace"]:
                    raise EnvironmentError_("--confirm must repeat the exact test namespace")
                with port_lock(root):
                    state = read_state(root)
                    if args.namespace != state["namespace"] or args.confirm != state["namespace"]:
                        raise EnvironmentError_("test namespace changed before cleanup")
                    compose(root, state, ["down", "--volumes", "--remove-orphans"])
                print("Only the specified Compose project's containers and volumes were removed; local credentials remain.")
        return 0
    except (EnvironmentError_, subprocess.CalledProcessError, OSError) as exc:
        print(f"nerae test environment: {exc}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
