#!/usr/bin/env python3
"""Run Go race tests and reject a module with no passing tests."""
import json
import os
from pathlib import Path
import subprocess
import sys

root = Path(__file__).resolve().parents[2]
go = os.environ.get('NERAE_GO', 'go')
env = {**os.environ, 'GOTOOLCHAIN': 'local'}
try:
    process = subprocess.Popen([go, 'test', '-race', '-count=1', '-json', './...'],
                               cwd=root / 'apps/api', env=env, text=True,
                               stdout=subprocess.PIPE, stderr=subprocess.STDOUT)
except OSError as error:
    print(f'cannot run Go tests: {error}', file=sys.stderr)
    raise SystemExit(1)
started = set()
passed = set()
assert process.stdout is not None
for line in process.stdout:
    try:
        event = json.loads(line)
    except json.JSONDecodeError:
        print(line, end='')
        continue
    if event.get('Output'):
        print(event['Output'], end='')
    key = (event.get('Package'), event.get('Test'))
    if key[0] and key[1]:
        if event.get('Action') == 'run':
            started.add(key)
        elif event.get('Action') == 'pass':
            passed.add(key)
status = process.wait()
# A passing parent whose only children skip is not a passing test case.
passing_leaves = {(package, name) for package, name in passed
                  if not any(other_package == package and other_name.startswith(name + '/')
                             for other_package, other_name in started)}
if status != 0 or not passing_leaves:
    print('Go race tests failed or no test passed', file=sys.stderr)
    raise SystemExit(1)
print(f'Go race tests: {len(passing_leaves)} passing leaf tests')
