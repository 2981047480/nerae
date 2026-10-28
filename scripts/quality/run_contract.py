#!/usr/bin/env python3
"""Run the project's contract suite and reject zero or wholly skipped tests."""
import json
import os
from pathlib import Path
import subprocess
import sys

ROOT = Path(__file__).resolve().parents[2]
env = os.environ.copy()
# The foundation gate always verifies this checkout's contract, not an override.
env.pop('NERAE_OPENAPI', None)
result = subprocess.run([sys.executable, str(ROOT / 'api/tests/run_contract_tests.py'), '--summary'],
                        cwd=ROOT, env=env, text=True, capture_output=True, check=False)
if result.stdout:
    print(result.stdout, end='')
if result.stderr:
    print(result.stderr, end='', file=sys.stderr)
try:
    summary = json.loads(result.stdout)
    ran, skipped = summary['testsRun'], summary['skipped']
    if type(ran) is not int or type(skipped) is not int or ran < 1 or skipped >= ran:
        raise ValueError('empty or entirely skipped contract suite')
    if result.returncode != 0 or summary['successful'] is not True:
        raise ValueError('contract suite failed')
except (ValueError, KeyError, TypeError, json.JSONDecodeError) as error:
    print(f'contract check failed: {error}', file=sys.stderr)
    raise SystemExit(1)

# The canonical summary does not count expected failures separately. Run the
# same suite with the nonempty test guard so an all-expected-failure suite fails.
nonempty = subprocess.run([sys.executable, str(ROOT / 'scripts/quality/run_unittest.py'), str(ROOT / 'api/tests')],
                          cwd=ROOT, env=env, check=False)
if nonempty.returncode:
    raise SystemExit(nonempty.returncode)
