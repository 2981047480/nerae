"""Bound the cold-backup smoke's file operations to its own stopped test instance."""
import importlib.util
from pathlib import Path
import tempfile
import unittest
from unittest import mock

SCRIPT = Path(__file__).with_name('mysql_native_smoke.py')
spec = importlib.util.spec_from_file_location('mysql_native_smoke', SCRIPT)
smoke = importlib.util.module_from_spec(spec)
spec.loader.exec_module(smoke)


class RecoveryPathTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix='nerae-recovery-guard-')
        self.addCleanup(self.temp.cleanup)
        self.parent = Path(self.temp.name).resolve()
        self.base = self.parent / 'test-run'
        self.data = self.base / 'mysql-1' / 'data'
        self.data.mkdir(parents=True, mode=0o700)
        self.base.chmod(0o700)
        self.data.parent.chmod(0o700)
        self.data.chmod(0o700)
        (self.data / 'test-only.txt').write_text('fixture')
        self.process = mock.Mock()
        self.process.poll.return_value = 0
        self.instance = {'datadir': self.data, 'process': self.process}
        patcher = mock.patch.object(smoke, 'TEMP_PARENT', self.parent)
        patcher.start()
        self.addCleanup(patcher.stop)

    def test_accepts_only_stopped_script_owned_datadir(self):
        self.assertEqual(smoke.guarded_datadir(self.base, self.instance), self.data)

    def test_rejects_live_or_missing_process(self):
        for process in (None, mock.Mock(poll=mock.Mock(return_value=None))):
            with self.subTest(process=process), self.assertRaises(smoke.SmokeError):
                smoke.guarded_datadir(self.base, dict(self.instance, process=process))

    def test_rejects_wrong_instance_or_public_directory(self):
        with self.assertRaises(smoke.SmokeError):
            smoke.guarded_datadir(self.base, dict(self.instance, datadir=self.parent))
        self.data.chmod(0o755)
        with self.assertRaises(smoke.SmokeError):
            smoke.guarded_datadir(self.base, self.instance)

    def test_rejects_symlink_in_datadir(self):
        (self.data / 'escape').symlink_to(self.parent, target_is_directory=True)
        with self.assertRaises(smoke.SmokeError):
            smoke.guarded_datadir(self.base, self.instance)

    def test_initialization_uses_owned_socket_and_credentials(self):
        datadir = self.base / 'mysql-2' / 'data'
        datadir.parent.mkdir(mode=0o700)
        instance = {'datadir': datadir, 'initial_defaults': self.base / 'initial.cnf',
                    'root_defaults': self.base / 'root.cnf', 'socket_path': self.base / 'owned.sock',
                    'mysql_bin': self.base / 'mysql', 'root_password': 'test-root',
                    'app_password': 'test-app', 'state': {'database': 'nerae_test_0123456789abcdef'}}
        result = mock.Mock(returncode=0)
        with mock.patch.object(smoke, 'mysql_common', return_value=[]), \
             mock.patch.object(smoke.subprocess, 'run', return_value=result), \
             mock.patch.object(smoke, 'launch_mysql') as launch, \
             mock.patch.object(smoke, 'mysql_client') as client, \
             mock.patch.object(smoke, 'write_mysql_defaults') as write_defaults:
            smoke.initialize_instance(instance)
        launch.assert_called_once_with(instance, instance['initial_defaults'])
        write_defaults.assert_called_once_with(instance['root_defaults'], 'test-root', instance['socket_path'])
        self.assertTrue(instance['root_config_ready'])
        self.assertEqual(client.call_count, 2)

    def test_rejects_symlinked_datadir(self):
        real = self.data.with_name('actual-data')
        self.data.rename(real)
        self.data.symlink_to(real, target_is_directory=True)
        with self.assertRaises(smoke.SmokeError):
            smoke.guarded_datadir(self.base, self.instance)


if __name__ == '__main__':
    unittest.main()
