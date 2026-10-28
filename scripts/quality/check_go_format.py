#!/usr/bin/env python3
"""Check Go source formatting without rewriting the working tree."""
import os
from pathlib import Path
import subprocess
import sys

root = Path(__file__).resolve().parents[2]
files = []
for directory in (root / 'apps/api', root / 'scripts/dev', root / 'scripts/quality'):
    if not directory.is_dir():
        print(f'missing Go source directory: {directory}', file=sys.stderr)
        raise SystemExit(1)
    for path in directory.rglob('*.go'):
        if path.is_symlink() or any(part in {'vendor', 'node_modules', '.git'} for part in path.parts):
            continue
        if path.is_file():
            files.append(path)
if not files:
    print('no Go source files found', file=sys.stderr)
    raise SystemExit(1)
try:
    result = subprocess.run([os.environ.get('NERAE_GOFMT', 'gofmt'), '-l', *map(str, sorted(files))],
                            text=True, capture_output=True, check=False)
except OSError as error:
    print(f'cannot run gofmt: {error}', file=sys.stderr)
    raise SystemExit(1)
if result.stdout:
    print(result.stdout, end='')
if result.stderr:
    print(result.stderr, end='', file=sys.stderr)
if result.returncode or result.stdout.strip():
    raise SystemExit(1)
print(f'Go format: {len(files)} files checked')
