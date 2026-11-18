"""Failure-injection tests for the foundation quality entry points."""
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[2]
QUALITY = ROOT / 'scripts/quality'


class QualityEntrypointTests(unittest.TestCase):
    def run_python(self, script, *args, cwd=ROOT, env=None):
        return subprocess.run([sys.executable, str(script), *map(str, args)], cwd=cwd,
                              env=env, text=True, capture_output=True, check=False)

    def test_unittest_runner_rejects_empty_skipped_and_expected_failure(self):
        with tempfile.TemporaryDirectory(prefix='nerae-quality-unittest-') as temporary:
            directory = Path(temporary)
            runner = QUALITY / 'run_unittest.py'
            empty = self.run_python(runner, directory)
            self.assertNotEqual(empty.returncode, 0, empty.stdout + empty.stderr)
            sample = directory / 'test_sample.py'
            for name, source in [
                ('skipped', 'import unittest\nclass T(unittest.TestCase):\n @unittest.skip("fixture")\n def test_value(self): pass\n'),
                ('expected failure', 'import unittest\nclass T(unittest.TestCase):\n @unittest.expectedFailure\n def test_value(self): self.fail()\n'),
            ]:
                with self.subTest(case=name):
                    sample.write_text(source)
                    result = self.run_python(runner, directory)
                    self.assertNotEqual(result.returncode, 0, result.stdout + result.stderr)
            sample.write_text('import unittest\nclass T(unittest.TestCase):\n def test_value(self): self.assertEqual(1, 1)\n')
            passed = self.run_python(runner, directory)
            self.assertEqual(passed.returncode, 0, passed.stdout + passed.stderr)

    def test_go_runner_rejects_no_tests_and_a_parent_with_only_skipped_children(self):
        with tempfile.TemporaryDirectory(prefix='nerae-quality-go-') as temporary:
            root = Path(temporary)
            quality = root / 'scripts/quality'
            package = root / 'apps/api'
            quality.mkdir(parents=True)
            package.mkdir(parents=True)
            shutil.copyfile(QUALITY / 'run_go_tests.py', quality / 'run_go_tests.py')
            (package / 'go.mod').write_text('module example.test/quality\n\ngo 1.23\n')
            (package / 'main.go').write_text('package quality\n')
            env = {**os.environ, 'GOWORK': 'off', 'GOTOOLCHAIN': 'local'}
            runner = quality / 'run_go_tests.py'
            empty = self.run_python(runner, cwd=root, env=env)
            self.assertNotEqual(empty.returncode, 0, empty.stdout + empty.stderr)
            sample = package / 'main_test.go'
            sample.write_text('package quality\nimport "testing"\nfunc TestParent(t *testing.T) { t.Run("child", func(t *testing.T) { t.Skip("fixture") }) }\n')
            skipped = self.run_python(runner, cwd=root, env=env)
            self.assertNotEqual(skipped.returncode, 0, skipped.stdout + skipped.stderr)
            sample.write_text('package quality\nimport "testing"\nfunc TestValue(t *testing.T) { if 1 != 1 { t.Fatal("bad") } }\n')
            passed = self.run_python(runner, cwd=root, env=env)
            self.assertEqual(passed.returncode, 0, passed.stdout + passed.stderr)

    def test_node_runner_rejects_empty_or_entirely_skipped_suite(self):
        with tempfile.TemporaryDirectory(prefix='nerae-quality-node-') as temporary:
            root = Path(temporary)
            directory = root / 'scripts/quality'
            directory.mkdir(parents=True)
            sample = directory / 'boundary.test.mjs'
            runner = QUALITY / 'run_node_tests.py'
            sample.write_text('')
            empty = self.run_python(runner, cwd=root)
            self.assertNotEqual(empty.returncode, 0, empty.stdout + empty.stderr)
            sample.write_text("import test from 'node:test';\ntest.skip('a', () => {}); test.skip('b', () => {});\n")
            skipped = self.run_python(runner, cwd=root)
            self.assertNotEqual(skipped.returncode, 0, skipped.stdout + skipped.stderr)
            sample.write_text("import test from 'node:test';\ntest('a', () => {}); test('b', () => {});\n")
            passed = self.run_python(runner, cwd=root)
            self.assertEqual(passed.returncode, 0, passed.stdout + passed.stderr)

    def test_not_ready_and_missing_make_targets_fail(self):
        targets = ['check-isolation', 'migration-check', 'test', 'test-integration',
                   'e2e', 'e2e-api', 'visual-diff', 'seed-check', 'seed-dry-run', 'check']
        with tempfile.TemporaryDirectory(prefix='nerae-quality-make-') as temporary:
            for target in targets:
                with self.subTest(target=target):
                    result = subprocess.run(['make', '-f', str(ROOT / 'Makefile'), target], cwd=temporary,
                                            text=True, capture_output=True, check=False)
                    self.assertNotEqual(result.returncode, 0, result.stdout + result.stderr)
                    self.assertIn('NOT_READY', result.stdout + result.stderr)
            missing = subprocess.run(['make', '-f', str(ROOT / 'Makefile'), 'target-does-not-exist'], cwd=temporary,
                                     text=True, capture_output=True, check=False)
            self.assertNotEqual(missing.returncode, 0, missing.stdout + missing.stderr)

    def test_codegen_gate_fails_when_required_files_are_missing(self):
        with tempfile.TemporaryDirectory(prefix='nerae-quality-codegen-') as temporary:
            result = subprocess.run(['make', '-f', str(ROOT / 'Makefile'), 'codegen-check',
                                     f'NERAE_PYTHON={sys.executable}'], cwd=temporary,
                                    text=True, capture_output=True, check=False)
            self.assertNotEqual(result.returncode, 0, result.stdout + result.stderr)
            self.assertNotIn('NOT_READY', result.stdout + result.stderr)

    def test_app_build_gate_rejects_a_zero_exit_without_the_resource(self):
        script = ROOT / 'apps/client/scripts/build-app.mjs'
        package = json.loads((ROOT / 'apps/client/package.json').read_text())
        self.assertEqual(package['scripts']['build:app'], 'node ./scripts/build-app.mjs')
        node = shutil.which(os.environ.get('NERAE_NODE', 'node'))
        self.assertIsNotNone(node)
        with tempfile.TemporaryDirectory(prefix='nerae-app-build-gate-') as temporary:
            root = Path(temporary)
            client = root / 'client'
            (client / 'scripts').mkdir(parents=True)
            shutil.copyfile(script, client / 'scripts/build-app.mjs')
            bindir = root / 'bin'
            bindir.mkdir()
            fake = bindir / 'uni'
            fake.write_text(
                '#!/bin/sh\n'
                'printf "%s\\n" "compiler-marker"\n'
                'dir="${UNI_OUTPUT_DIR:-dist/build/app}"\n'
                'out="$dir/app-service.js"\n'
                'case "$NERAE_FAKE_UNI" in\n'
                '  ok)\n'
                '    mkdir -p "$(dirname "$out")"\n'
                '    printf service > "$out"\n'
                '    exit 0\n'
                '    ;;\n'
                '  silent-missing)\n'
                '    exit 0\n'
                '    ;;\n'
                '  module-missing)\n'
                '    printf "%s\\n" "Cannot find module: @dcloudio/uni-uts-v1" >&2\n'
                '    exit 0\n'
                '    ;;\n'
                '  module-and-file)\n'
                '    mkdir -p "$(dirname "$out")"\n'
                '    printf service > "$out"\n'
                '    printf "%s\\n" "Cannot find module: @dcloudio/uni-uts-v1" >&2\n'
                '    exit 0\n'
                '    ;;\n'
                '  empty)\n'
                '    mkdir -p "$(dirname "$out")"\n'
                '    : > "$out"\n'
                '    exit 0\n'
                '    ;;\n'
                '  symlink)\n'
                '    mkdir -p "$(dirname "$out")"\n'
                '    printf other > "$(dirname "$out")/other.js"\n'
                '    ln -s other.js "$out"\n'
                '    exit 0\n'
                '    ;;\n'
                '  compiler-fail)\n'
                '    mkdir -p "$(dirname "$out")"\n'
                '    printf service > "$out"\n'
                '    exit 2\n'
                '    ;;\n'
                'esac\n'
                'exit 9\n'
            )
            fake.chmod(0o755)
            gate = client / 'scripts/build-app.mjs'

            def run(mode, extra=None):
                env = {**os.environ, 'PATH': f'{bindir}{os.pathsep}{os.environ.get("PATH", "")}', 'NERAE_FAKE_UNI': mode}
                env.pop('UNI_OUTPUT_DIR', None)
                if extra:
                    env.update(extra)
                return subprocess.run([node, str(gate)], cwd=root, env=env, text=True, capture_output=True, check=False)

            ok = run('ok')
            self.assertEqual(ok.returncode, 0, ok.stdout + ok.stderr)
            self.assertIn('compiler-marker', ok.stdout)
            artifact = client / 'dist/build/app/app-service.js'
            self.assertTrue(artifact.is_file() and artifact.stat().st_size > 0)

            stale = artifact
            stale.write_text('stale')
            silent = run('silent-missing')
            self.assertNotEqual(silent.returncode, 0, silent.stdout + silent.stderr)
            self.assertIn('missing nonempty', silent.stderr)
            self.assertFalse(stale.exists())

            module = run('module-missing')
            self.assertEqual(module.returncode, 1, module.stdout + module.stderr)
            self.assertIn('Cannot find module: @dcloudio/uni-uts-v1', module.stderr)
            self.assertIn('missing nonempty', module.stderr)

            both = run('module-and-file')
            self.assertEqual(both.returncode, 1, both.stdout + both.stderr)
            self.assertIn('compiler log contains', both.stderr)
            self.assertNotIn('missing nonempty', both.stderr)
            self.assertGreater(artifact.stat().st_size, 0)

            empty = run('empty')
            self.assertEqual(empty.returncode, 1, empty.stdout + empty.stderr)
            self.assertIn('missing nonempty', empty.stderr)

            link = run('symlink')
            self.assertEqual(link.returncode, 1, link.stdout + link.stderr)
            self.assertIn('missing nonempty', link.stderr)

            failed = run('compiler-fail')
            self.assertEqual(failed.returncode, 2, failed.stdout + failed.stderr)
            self.assertIn('exited 2', failed.stderr)
            self.assertNotIn('missing nonempty', failed.stderr)

            custom = root / 'custom-out'
            artifact.write_text('stale-default')
            redirected = run('ok', {'UNI_OUTPUT_DIR': str(custom)})
            self.assertEqual(redirected.returncode, 0, redirected.stdout + redirected.stderr)
            self.assertEqual((custom / 'app-service.js').read_text(), 'service')
            self.assertEqual(artifact.read_text(), 'stale-default')

            empty_bin = root / 'empty-bin'
            empty_bin.mkdir()
            missing_uni = run('ok', {'PATH': str(empty_bin)})
            self.assertEqual(missing_uni.returncode, 1, missing_uni.stdout + missing_uni.stderr)
            self.assertIn('cannot run uni build -p app', missing_uni.stderr)

    def test_build_runner_rejects_success_without_a_new_artifact(self):
        with tempfile.TemporaryDirectory(prefix='nerae-quality-build-') as temporary:
            root = Path(temporary)
            fake = root / 'no-output-pnpm'
            fake.write_text('#!/bin/sh\nexit 0\n')
            fake.chmod(0o755)
            env = {**os.environ, 'NERAE_PNPM': str(fake), 'NERAE_BUILD_ROOT': str(root)}
            result = self.run_python(QUALITY / 'run_build.py', 'client', env=env)
            self.assertNotEqual(result.returncode, 0, result.stdout + result.stderr)
            self.assertIn('did not generate nonempty', result.stdout + result.stderr)

    def test_make_client_typecheck_rejects_injected_type_error(self):
        with tempfile.TemporaryDirectory(prefix='nerae-quality-typecheck-') as temporary:
            root = Path(temporary)
            (root / 'apps').mkdir()
            shutil.copytree(ROOT / 'apps/client', root / 'apps/client',
                            ignore=shutil.ignore_patterns('node_modules', 'dist'))
            for name in ['package.json', 'pnpm-workspace.yaml']:
                shutil.copyfile(ROOT / name, root / name)
            (root / 'node_modules').symlink_to(ROOT / 'node_modules', target_is_directory=True)
            (root / 'apps/client/node_modules').symlink_to(ROOT / 'apps/client/node_modules', target_is_directory=True)
            command = ['make', '-f', str(ROOT / 'Makefile'), 'typecheck-client']
            # The fixture reuses installed dependencies; pnpm must never purge the linked tree.
            env = {**os.environ, 'pnpm_config_verify_deps_before_run': 'warn'}
            baseline = subprocess.run(command, cwd=root, env=env, text=True, capture_output=True, check=False)
            self.assertEqual(baseline.returncode, 0, baseline.stdout + baseline.stderr)
            (root / 'apps/client/src/type-error-fixture.ts').write_text('const broken: number = "not a number"\n')
            failure = subprocess.run(command, cwd=root, env=env, text=True, capture_output=True, check=False)
            self.assertNotEqual(failure.returncode, 0, failure.stdout + failure.stderr)
            self.assertIn('TS2322', failure.stdout + failure.stderr)

    def test_contract_gate_ignores_an_external_contract_override(self):
        with tempfile.TemporaryDirectory(prefix='nerae-quality-contract-') as temporary:
            root = Path(temporary)
            quality = root / 'scripts/quality'
            tests = root / 'api/tests'
            quality.mkdir(parents=True)
            tests.mkdir(parents=True)
            shutil.copyfile(QUALITY / 'run_contract.py', quality / 'run_contract.py')
            (tests / 'run_contract_tests.py').write_text(
                'import json, os\nassert "NERAE_OPENAPI" not in os.environ\n'
                'print(json.dumps({"testsRun": 1, "skipped": 0, "successful": True}))\n')
            (quality / 'run_unittest.py').write_text('import os\nassert "NERAE_OPENAPI" not in os.environ\n')
            env = {**os.environ, 'NERAE_OPENAPI': '/nonexistent/other-contract.yaml'}
            result = self.run_python(quality / 'run_contract.py', cwd=root, env=env)
            self.assertEqual(result.returncode, 0, result.stdout + result.stderr)

    def test_missing_required_tool_fails_version_gate(self):
        env = {**os.environ, 'NERAE_GO': '/nonexistent/nerae-go'}
        result = self.run_python(QUALITY / 'check_versions.py', env=env)
        self.assertNotEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertIn('toolchain check failed', result.stdout + result.stderr)


if __name__ == '__main__':
    unittest.main()
