#!/usr/bin/env python3
"""Verify the locked local toolchain and contract test dependencies."""
from importlib import metadata
import json
import os
from pathlib import Path
import re
import subprocess
import sys

root = Path(__file__).resolve().parents[2]
if sys.version_info < (3, 11):
    print('Python 3.11 or newer is required', file=sys.stderr)
    raise SystemExit(1)


def command(name: str, *args: str) -> str:
    try:
        result = subprocess.run([name, *args], capture_output=True, text=True, check=False,
                                env={**os.environ, 'GOTOOLCHAIN': 'local'})
    except OSError as error:
        raise ValueError(f'cannot run {name}: {error}') from error
    if result.returncode:
        raise ValueError(f'{name} exited {result.returncode}')
    return result.stdout.strip()


try:
    node = command(os.environ.get('NERAE_NODE', 'node'), '--version').removeprefix('v')
    pnpm = command(os.environ.get('NERAE_PNPM', 'pnpm'), '--version')
    go = command(os.environ.get('NERAE_GO', 'go'), 'env', 'GOVERSION').removeprefix('go')
    expected_node = (root / '.node-version').read_text().strip().removeprefix('v')
    expected_go = (root / '.go-version').read_text().strip().removeprefix('go')
    expected_pnpm = json.loads((root / 'package.json').read_text())['packageManager'].split('@', 1)[1]
    for name, actual, expected in [('Node', node, expected_node), ('pnpm', pnpm, expected_pnpm), ('Go', go, expected_go)]:
        if actual != expected:
            raise ValueError(f'{name}: expected {expected}, got {actual}')
    for raw in (root / 'api/test-requirements.txt').read_text().splitlines():
        line = raw.strip()
        if not line or line.startswith('#'):
            continue
        match = re.fullmatch(r'([A-Za-z0-9_.-]+)==([A-Za-z0-9_.+-]+)', line)
        if not match:
            raise ValueError(f'unpinned test dependency: {line}')
        name, expected = match.groups()
        try:
            actual = metadata.version(name)
        except metadata.PackageNotFoundError as error:
            raise ValueError(f'missing Python test dependency: {name}') from error
        if actual != expected:
            raise ValueError(f'{name}: expected {expected}, got {actual}')
except (ValueError, OSError, KeyError, IndexError) as error:
    print(f'toolchain check failed: {error}', file=sys.stderr)
    raise SystemExit(1)
print(f'Toolchain: Node {node}, pnpm {pnpm}, Go {go}, Python {sys.version_info.major}.{sys.version_info.minor}; pinned test dependencies present')
