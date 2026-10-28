#!/usr/bin/env python3
"""Run a unittest directory and reject an empty or entirely skipped suite."""
from pathlib import Path
import sys
import unittest


def main() -> int:
    if len(sys.argv) != 2:
        print('usage: run_unittest.py TEST_DIRECTORY', file=sys.stderr)
        return 2
    directory = Path(sys.argv[1])
    if not directory.is_dir():
        print(f'test directory missing: {directory}', file=sys.stderr)
        return 1
    suite = unittest.defaultTestLoader.discover(str(directory), pattern='test_*.py')
    if suite.countTestCases() == 0:
        print(f'empty test suite: {directory}', file=sys.stderr)
        return 1
    result = unittest.TextTestRunner(verbosity=2).run(suite)
    passed = result.testsRun - len(result.skipped) - len(result.expectedFailures) - len(result.failures) - len(result.errors)
    if passed <= 0:
        print(f'no passing tests: {directory}', file=sys.stderr)
        return 1
    return 0 if result.wasSuccessful() else 1


if __name__ == '__main__':
    raise SystemExit(main())
