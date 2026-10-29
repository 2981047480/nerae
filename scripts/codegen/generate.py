#!/usr/bin/env python3
"""Generate or check the committed TS/Go contract from the one local OpenAPI."""
from __future__ import annotations

import argparse
from contextlib import contextmanager
import fcntl
import hashlib
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / 'api/tests'))
from contract_support import Contract, UniqueKeyLoader, walk  # noqa: E402
import yaml  # noqa: E402

OUTPUTS = {
    'ts': Path('packages/contracts/src/generated-types.ts'),
    'go': Path('apps/api/internal/gen/api.gen.go'),
}
SOURCES = [
    '.node-version', '.go-version', 'api/openapi.yaml', 'api/oapi-codegen.yaml', 'api/templates/oapi-imports.tmpl',
    'apps/api/internal/contractnullable/nullable.go', 'apps/api/internal/contractnullable/nullable_test.go',
    'apps/api/go.mod', 'apps/api/go.sum', 'packages/contracts/package.json', 'packages/contracts/src/index.ts',
    'scripts/codegen/tools/go.mod', 'scripts/codegen/tools/go.sum',
    'scripts/codegen/npm/package.json', 'scripts/codegen/npm/package-lock.json',
    'api/tests/contract_support.py', 'api/tests/codegen/recording-types.ts', 'api/tests/codegen/identity-types.ts',
    'api/tests/codegen/recording_types_test.go', 'api/tests/codegen/identity_types_test.go',
    'api/tests/codegen/schema_validation_test.go',
    'docs/evidence/F02/F02-00/nullable_generated_test.go',
    'docs/evidence/F02/F02-00/nullable_inventory.py', 'docs/evidence/F02/F02-00/nullable-inventory.json',
    'scripts/codegen/generate.py',
]


def local_file(relative: str | Path, *, must_exist: bool = True) -> Path:
    path = Path(relative)
    if path.is_absolute() or '..' in path.parts:
        raise ValueError(f'unsafe path: {relative}')
    current = ROOT
    for part in path.parts:
        current = current / part
        if current.is_symlink():
            raise ValueError(f'symlink not permitted: {relative}')
    if must_exist and not current.is_file():
        raise ValueError(f'required file missing: {relative}')
    if not current.resolve().is_relative_to(ROOT.resolve()):
        raise ValueError(f'path escapes repository: {relative}')
    return current



def npm_bin(name: str) -> Path:
    modules = ROOT / 'scripts/codegen/npm/node_modules'
    if modules.is_symlink() or not modules.is_dir():
        raise ValueError('locked npm tools are missing; run npm ci in scripts/codegen/npm')
    tool = modules / '.bin' / name
    try:
        resolved = tool.resolve(strict=True)
    except OSError as error:
        raise ValueError(f'locked npm tool missing: {name}') from error
    if not resolved.is_file() or not resolved.is_relative_to(modules.resolve()):
        raise ValueError(f'npm tool escapes its installed directory: {name}')
    return tool


def sha256(data: bytes) -> str:
    return hashlib.sha256(data).hexdigest()


def api_sources() -> list[str]:
    git_env = {key: value for key, value in os.environ.items() if not key.startswith('GIT_')}
    git_env.update(GIT_CONFIG_NOSYSTEM='1', GIT_CONFIG_GLOBAL=os.devnull,
                   GIT_NO_REPLACE_OBJECTS='1', GIT_TERMINAL_PROMPT='0')
    result = subprocess.run(['git', '-C', str(ROOT), '--literal-pathspecs', 'ls-files',
                             '--cached', '--others', '--exclude-standard', '-z', '--', 'apps/api'],
                            env=git_env, check=False, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
    if result.returncode:
        raise ValueError('cannot enumerate API source files from the current worktree')
    names = sorted(set(os.fsdecode(item) for item in result.stdout.split(b'\0') if item))
    return [name for name in names if name != OUTPUTS['go'].as_posix()]


def snapshot() -> dict[str, str]:
    return {name: sha256(local_file(name).read_bytes()) for name in sorted(set(SOURCES + api_sources()))}


@contextmanager
def write_lock(mode: str):
    # A private per-worktree lock prevents simultaneous check/write operations.
    directory = ROOT / '.review/nerae-codegen'
    for path in (ROOT / '.review', directory):
        if path.is_symlink():
            raise ValueError('codegen state directory must not be a symlink')
        path.mkdir(mode=0o700, exist_ok=True)
    lock = directory / 'write.lock'
    marker = directory / 'write.pending'
    if lock.is_symlink() or marker.is_symlink():
        raise ValueError('codegen state files must not be symlinks')
    fd = os.open(lock, os.O_CREAT | os.O_RDWR | getattr(os, 'O_NOFOLLOW', 0), 0o600)
    try:
        try:
            fcntl.flock(fd, fcntl.LOCK_EX | fcntl.LOCK_NB)
        except BlockingIOError as error:
            raise ValueError('another codegen operation is running in this worktree') from error
        if marker.exists():
            if not marker.is_file() or marker.read_text() != 'nerae-codegen-write-v1\n':
                raise ValueError('unknown pending codegen state; inspect it before proceeding')
            if mode == 'check':
                raise ValueError('an interrupted codegen write requires rerunning write before checking')
        yield marker
    finally:
        os.close(fd)


def create_pending_marker(marker: Path) -> None:
    # Publish only a complete marker. An interrupted temporary write is harmless
    # because output replacement has not started and the pending name is absent.
    fd, temporary = tempfile.mkstemp(prefix='.write.pending.', dir=marker.parent)
    try:
        with os.fdopen(fd, 'w', encoding='utf-8') as handle:
            handle.write('nerae-codegen-write-v1\n')
            handle.flush()
            os.fsync(handle.fileno())
        os.replace(temporary, marker)
    finally:
        try:
            os.unlink(temporary)
        except FileNotFoundError:
            pass


def validate_inputs() -> None:
    contract = Contract(local_file('api/openapi.yaml'))
    for _, node in walk(contract.doc):
        if '$ref' in node:
            contract.resolve(node['$ref'])  # local refs only; no network retrieval
    config = yaml.load(local_file('api/oapi-codegen.yaml').read_text(encoding='utf-8'), Loader=UniqueKeyLoader)
    if not isinstance(config, dict) or config.get('output') != OUTPUTS['go'].as_posix():
        raise ValueError('Go generator output must remain inside the designated generated file')
    options = config.get('output-options') or {}
    if options.get('nullable-type') is not True or options.get('user-templates') != {
        'imports.tmpl': 'api/templates/oapi-imports.tmpl'
    }:
        raise ValueError('Go generator must use the reviewed nullable guard and local import template')


def run(command: list[str], *, cwd: Path, env: dict[str, str], capture: bool = False) -> str:
    try:
        result = subprocess.run(command, cwd=cwd, env=env, check=False, text=True,
                                stdout=subprocess.PIPE if capture else None,
                                stderr=subprocess.PIPE if capture else None)
    except OSError as error:
        raise ValueError(f'cannot run {command[0]}: {error}') from error
    if result.returncode:
        if capture:
            print(result.stdout, end='')
            print(result.stderr, end='', file=sys.stderr)
        raise ValueError(f'{Path(command[0]).name} exited {result.returncode}')
    return result.stdout.strip() if capture else ''


def copy_to(source: str | Path, dest: Path) -> None:
    dest.parent.mkdir(parents=True, exist_ok=True)
    shutil.copyfile(local_file(source), dest)


def prepare_workspace(directory: Path) -> None:
    for name in ['api/openapi.yaml', 'api/oapi-codegen.yaml', 'api/templates/oapi-imports.tmpl',
                 'packages/contracts/package.json', 'packages/contracts/src/index.ts', *api_sources()]:
        copy_to(name, directory / name)
    (directory / OUTPUTS['go']).parent.mkdir(parents=True, exist_ok=True)
    (directory / OUTPUTS['ts']).parent.mkdir(parents=True, exist_ok=True)


def generate(directory: Path, binary: Path, node: str, ts_tool: Path, env: dict[str, str]) -> None:
    prepare_workspace(directory)
    run([str(binary), '-config', 'api/oapi-codegen.yaml', 'api/openapi.yaml'], cwd=directory, env=env)
    run([node, str(ts_tool), str(directory / 'api/openapi.yaml'), '-o', str(directory / OUTPUTS['ts'])],
        cwd=directory, env=env)
    for name in OUTPUTS.values():
        path = directory / name
        if not path.is_file() or not path.stat().st_size:
            raise ValueError(f'generator did not produce {name}')


def verify_models(directory: Path, node: str, tsc: Path, go: str, env: dict[str, str]) -> None:
    ts_dir = directory / 'packages/contracts/src'
    for name in ('recording-types.ts', 'identity-types.ts'):
        copy_to('api/tests/codegen/' + name, ts_dir / name)
    # Verify the package's actual export map, not only a relative type import.
    package_link = directory / 'node_modules/@nerae/contracts'
    package_link.parent.mkdir(parents=True, exist_ok=True)
    package_link.symlink_to(directory / 'packages/contracts', target_is_directory=True)
    consumer = directory / 'contract-consumer.ts'
    consumer.write_text('import type { components } from "@nerae/contracts";\n'
                        'const valid: components["schemas"]["WorkoutFinish"] = { expectedVersion: 1 };\n'
                        'void valid;\n', encoding='utf-8')
    run([node, str(tsc), '--strict', '--noEmit', '--skipLibCheck', '--target', 'es2020',
         '--module', 'esnext', '--moduleResolution', 'bundler', str(consumer),
         str(ts_dir / 'index.ts'), str(ts_dir / 'recording-types.ts'), str(ts_dir / 'identity-types.ts')],
        cwd=directory, env=env)
    go_dir = directory / 'apps/api/internal/gen'
    for name in ('recording_types_test.go', 'identity_types_test.go', 'schema_validation_test.go'):
        destination = go_dir / ('nerae_probe_' + name)
        if destination.exists():
            raise ValueError(f'probe would overwrite API source: {destination.name}')
        copy_to('api/tests/codegen/' + name, destination)
    destination = go_dir / 'nerae_probe_nullable_generated_test.go'
    if destination.exists():
        raise ValueError(f'probe would overwrite API source: {destination.name}')
    copy_to('docs/evidence/F02/F02-00/nullable_generated_test.go', destination)
    go_env = {**env, 'NERAE_OPENAPI': str(directory / 'api/openapi.yaml')}
    command = [go, 'test', '-mod=readonly', '-race', '-count=1', '-json',
               './...']
    result = subprocess.run(command, cwd=directory / 'apps/api', env=go_env, text=True,
                            stdout=subprocess.PIPE, stderr=subprocess.STDOUT, check=False)
    passed = set()
    for line in result.stdout.splitlines():
        try:
            event = json.loads(line)
        except json.JSONDecodeError:
            print(line)
            continue
        if event.get('Output'):
            print(event['Output'], end='')
        if event.get('Action') == 'pass' and event.get('Package') and event.get('Test'):
            passed.add((event['Package'], event['Test']))
    package = 'nerae/api/internal/gen'
    required = {(package, 'TestGeneratedWorkoutFinishOptionalNullableStates/' + field + '/' + state)
                for field in ('durationMin', 'rpe', 'note') for state in ('omitted', 'null', 'value')}
    required |= {(package, name) for name in (
        'TestActualCommentListCannotInventEmptyCursor',
        'TestRequiredReadOnlyNullableNeedsResponseSchemaValidation',
        'TestRequiredReadOnlyExerciseNameNeedsResponseSchemaValidation',
        'TestGeneratedPendingLoginRoundTrip', 'TestGeneratedSetBatchCarriesTypedActualGroup',
    )}
    required |= {(package, 'TestAllRequiredNonReadOnlyNullableFieldsRejectUnspecified/' + name) for name in (
        'ExerciseList/NextCursor', 'CoachPlanList/NextCursor', 'TrainingOccurrenceList/NextCursor',
        'RelationMessageList/NextCursor', 'PlanDetail/PublishedVersion', 'Relation/StudentConfirmedAt',
        'DataGrant/GrantedAt', 'DataGrant/RevokedAt', 'PlanList/NextCursor', 'CommentList/NextCursor',
        'MessageList/NextCursor', 'ShareLink/ExpiresAt', 'AnomalyList/NextCursor',
    )}
    required.add(('nerae/api/internal/contractnullable', 'TestRequiredNullableMustBeExplicit'))
    if result.returncode or not required <= passed:
        raise ValueError('generated Go/schema tests failed, were skipped, or missed required cases')
    print(f'Generated Go/schema checks: {len(required)} required cases passed')


def write_outputs(data: dict[str, bytes]) -> None:
    prepared = []
    prior = {}
    replaced = []
    try:
        for key, relative in OUTPUTS.items():
            dest = local_file(relative, must_exist=False)
            if dest.exists() and not dest.is_file():
                raise ValueError(f'generated destination is not a file: {relative}')
            dest.parent.mkdir(parents=True, exist_ok=True)
            local_file(relative, must_exist=False)  # recheck parents after mkdir
            prior[dest] = dest.read_bytes() if dest.exists() else None
            with tempfile.NamedTemporaryFile(prefix='.nerae-codegen-', dir=dest.parent, delete=False) as handle:
                handle.write(data[key])
                prepared.append((Path(handle.name), dest))
        for temporary, dest in prepared:
            os.replace(temporary, dest)
            replaced.append(dest)
    except Exception:
        for dest in reversed(replaced):
            previous = prior[dest]
            if previous is None:
                dest.unlink(missing_ok=True)
            else:
                with tempfile.NamedTemporaryFile(prefix='.nerae-restore-', dir=dest.parent, delete=False) as handle:
                    handle.write(previous)
                    backup = Path(handle.name)
                os.replace(backup, dest)
        raise
    finally:
        for temporary, _ in prepared:
            temporary.unlink(missing_ok=True)


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('mode', choices=['check', 'write'])
    args = parser.parse_args()
    go = os.environ.get('NERAE_GO', 'go')
    node = os.environ.get('NERAE_NODE', 'node')
    env = {**os.environ, 'NERAE_GO': go, 'GOTOOLCHAIN': 'local', 'GOWORK': 'off', 'PYTHONDONTWRITEBYTECODE': '1'}
    ts_tool = npm_bin('openapi-typescript')
    tsc = npm_bin('tsc')
    # npm's .bin entries are symlinks created from the lockfile; verify via their
    # package metadata and executable versions instead of treating them as source.
    with write_lock(args.mode) as marker:
        initial = snapshot()
        validate_inputs()
        inventory = run([sys.executable, str(local_file('docs/evidence/F02/F02-00/nullable_inventory.py'))],
                        cwd=ROOT, env=env, capture=True)
        expected_inventory = local_file('docs/evidence/F02/F02-00/nullable-inventory.json').read_text(encoding='utf-8').strip()
        if inventory != expected_inventory:
            raise ValueError('nullable inventory or contract changed; review and regenerate the inventory')
        if initial != snapshot():
            raise ValueError('contract or source changed during validation')
        go_version = run([go, 'env', 'GOVERSION'], cwd=ROOT, env=env, capture=True)
        node_version = run([node, '--version'], cwd=ROOT, env=env, capture=True)
        if go_version != 'go' + local_file('.go-version').read_text().strip().removeprefix('go'):
            raise ValueError(f'unexpected Go version: {go_version}')
        if node_version.removeprefix('v') != local_file('.node-version').read_text().strip().removeprefix('v'):
            raise ValueError(f'unexpected Node version: {node_version}')
        version = run([node, str(ts_tool), '--version'], cwd=ROOT, env=env, capture=True)
        compiler = run([node, str(tsc), '--version'], cwd=ROOT, env=env, capture=True)
        if version != 'v7.6.1' or compiler != 'Version 5.9.3':
            raise ValueError(f'unexpected TypeScript tools: {version}, {compiler}')
        tools = ROOT / 'scripts/codegen/tools'
        version = run([go, 'list', '-mod=readonly', '-m', '-f', '{{.Version}}',
                       'github.com/oapi-codegen/oapi-codegen/v2'], cwd=tools, env=env, capture=True)
        if version != 'v2.4.1':
            raise ValueError(f'unexpected oapi-codegen version: {version}')
        with tempfile.TemporaryDirectory(prefix='nerae-codegen-') as temporary:
            stage = Path(temporary)
            binary = stage / 'oapi-codegen'
            run([go, 'build', '-mod=readonly', '-o', str(binary),
                 'github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen'], cwd=tools, env=env)
            first, second = stage / 'first', stage / 'second'
            generate(first, binary, node, ts_tool, env)
            generate(second, binary, node, ts_tool, env)
            data = {}
            for key, relative in OUTPUTS.items():
                left, right = (first / relative).read_bytes(), (second / relative).read_bytes()
                if left != right:
                    raise ValueError(f'{relative} differs between independent generations')
                data[key] = left
            if args.mode == 'check':
                for key, relative in OUTPUTS.items():
                    actual = local_file(relative).read_bytes()
                    if actual != data[key]:
                        raise ValueError(f'generated file drift: {relative}; expected {sha256(data[key])}, found {sha256(actual)}')
            verify_models(first, node, tsc, go, env)
            if initial != snapshot():
                raise ValueError('contract, configuration, tools or fixtures changed during generation')
            if args.mode == 'write':
                # If interrupted after one replace, the persistent marker makes the
                # next check fail until an explicit write rebuilds both outputs.
                if not marker.exists():
                    create_pending_marker(marker)
                write_outputs(data)
                marker.unlink()
            print('Contract generation: two identical TS/Go outputs, typed fixtures and API module compiled.')
    return 0


if __name__ == '__main__':
    try:
        raise SystemExit(main())
    except (ValueError, OSError, yaml.YAMLError, AssertionError) as error:
        print(f'codegen: {error}', file=sys.stderr)
        raise SystemExit(1)
