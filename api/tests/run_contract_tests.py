"""Run unittest contract regressions; optionally print a compact JSON summary.

Default: python api/tests/run_contract_tests.py
Summary: python api/tests/run_contract_tests.py --summary
Input override: NERAE_OPENAPI=workspace/path/to/snapshot.yaml
This runner prints only; persistence is an explicit caller action.
"""

import argparse
import hashlib
import io
import json
import sys
import unittest
from pathlib import Path

from contract_support import Contract


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--summary", action="store_true", help="emit JSON instead of full failure tracebacks")
    arguments = parser.parse_args()
    contract = Contract()
    suite = unittest.defaultTestLoader.discover(str(Path(__file__).parent), pattern="test_*.py")
    stream = io.StringIO() if arguments.summary else sys.stderr
    result = unittest.TextTestRunner(stream=stream, verbosity=2).run(suite)
    failures = {}
    for test, traceback in result.failures + result.errors:
        parent = getattr(test, "test_case", test).id()
        failures.setdefault(parent, []).append({"case": test.id(), "reason": traceback.strip().splitlines()[-1]})
    summary = {
        "contract": str(contract.path),
        "sha256": hashlib.sha256(contract.source.encode("utf-8")).hexdigest(),
        "testsRun": result.testsRun,
        "failedTestMethods": len(failures),
        "failureEventsIncludingSubtests": len(result.failures),
        "errorEventsIncludingSubtests": len(result.errors),
        "skipped": len(result.skipped),
        "successful": result.wasSuccessful(),
        "failures": {name: {"events": len(events), "first": events[0]} for name, events in failures.items()},
    }
    print(json.dumps(summary, ensure_ascii=False, indent=2))
    return 0 if result.wasSuccessful() else 1


if __name__ == "__main__":
    sys.exit(main())
