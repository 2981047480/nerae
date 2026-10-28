#!/usr/bin/env python3
"""Run Node boundary tests and reject zero, wholly skipped, or empty-file results."""
import os
import re
import subprocess
import sys

node = os.environ.get('NERAE_NODE', 'node')
if len(sys.argv) > 2:
    print('usage: run_node_tests.py [TEST_FILE]', file=sys.stderr)
    raise SystemExit(2)
test_file = sys.argv[1] if len(sys.argv) == 2 else 'scripts/quality/boundary.test.mjs'
try:
    result = subprocess.run([node, '--test', '--test-reporter=tap', test_file],
                            text=True, capture_output=True, check=False)
except OSError as error:
    print(f'cannot run Node tests: {error}', file=sys.stderr)
    raise SystemExit(1)
print(result.stdout, end='')
print(result.stderr, end='', file=sys.stderr)
counts = dict(re.findall(r'^# (tests|pass) (\d+)\s*$', result.stdout, flags=re.MULTILINE))
if result.returncode or int(counts.get('tests', 0)) < 2 or int(counts.get('pass', 0)) < 2:
    print('Node boundary suite must include at least two passing tests', file=sys.stderr)
    raise SystemExit(1)
