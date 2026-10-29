"""A changed contract must force a fresh nullable inventory review."""
from pathlib import Path
import os
import shutil
import subprocess
import sys
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[4]


class NullableInventoryTest(unittest.TestCase):
    def test_contract_drift_fails_before_generation(self):
        with tempfile.TemporaryDirectory(prefix='nerae-nullable-drift-') as temporary:
            root = Path(temporary)
            for relative in ['api/openapi.yaml', 'api/tests/contract_support.py',
                             'docs/evidence/F02/F02-00/nullable_inventory.py',
                             'docs/evidence/F02/F02-00/nullable-inventory.json',
                             'docs/evidence/F02/F02-00/verify.sh']:
                target = root / relative
                target.parent.mkdir(parents=True, exist_ok=True)
                shutil.copyfile(ROOT / relative, target)
            contract = root / 'api/openapi.yaml'
            contract.write_text(contract.read_text() + '\n# synthetic drift fixture\n')
            stages = root / 'stages'
            stages.mkdir()
            env = {**os.environ, 'NERAE_PYTHON': sys.executable, 'PYTHONDONTWRITEBYTECODE': '1',
                   'TMPDIR': str(stages)}
            result = subprocess.run(['/bin/sh', str(root / 'docs/evidence/F02/F02-00/verify.sh')],
                                    env=env, text=True, capture_output=True, check=False)
            self.assertNotEqual(result.returncode, 0, result.stdout + result.stderr)
            self.assertIn('Nullable inventory or contract changed', result.stderr)
            self.assertFalse(any(stages.rglob('oapi-codegen')), 'generation started despite contract drift')


if __name__ == '__main__':
    unittest.main()
