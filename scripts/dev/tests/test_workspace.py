import contextlib
import importlib.util
import io
import json
import os
import subprocess
from pathlib import Path
import tempfile
import unittest
from unittest import mock

SCRIPT = Path(__file__).resolve().parents[1] / "workspace.py"
spec = importlib.util.spec_from_file_location("workspace", SCRIPT)
w = importlib.util.module_from_spec(spec)
spec.loader.exec_module(w)


class WorkspaceTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name) / "one"
        self.other = Path(self.temp.name) / "two"
        self.root.mkdir()
        self.other.mkdir()
        lock_patcher = mock.patch.object(w, "port_lock", side_effect=lambda root: contextlib.nullcontext())
        lock_patcher.start()
        self.addCleanup(lock_patcher.stop)
        ports_patcher = mock.patch.object(w, "assert_ports_unreserved")
        ports_patcher.start()
        self.addCleanup(ports_patcher.stop)
        patcher = mock.patch.object(w, "available")
        self.available = patcher.start()
        self.addCleanup(patcher.stop)

    def init(self, root=None):
        return w.initialize(root or self.root, 23306, 26379)

    def test_two_worktrees_have_different_resources(self):
        one, two = self.init(), self.init(self.other)
        for key in ("namespace", "database", "redis_prefix", "object_prefix"):
            self.assertNotEqual(one[key], two[key])

    def test_initialization_is_private_and_idempotent(self):
        first = self.init()
        directory = w.state_dir(self.root)
        password = (directory / "mysql-app-password").read_bytes()
        self.assertEqual(directory.stat().st_mode & 0o777, 0o700)
        for name in ("state.json", "mysql-app-password", "mysql-root-password"):
            self.assertEqual((directory / name).stat().st_mode & 0o777, 0o600)
        self.assertEqual(first, self.init())
        self.assertEqual(password, (directory / "mysql-app-password").read_bytes())
        self.assertEqual(self.available.call_count, 2)

    def test_existing_assignment_cannot_silently_change(self):
        before = self.init()
        with self.assertRaises(w.EnvironmentError_):
            w.initialize(self.root, 23307, 26379)
        self.assertEqual(before, w.read_state(self.root))

    def test_invalid_or_equal_ports_rejected(self):
        for ports in [(80, 26379), (23306, 23306), (23306, 65536), (True, 26379)]:
            with self.subTest(ports=ports), self.assertRaises(w.EnvironmentError_):
                w.initialize(self.root, *ports)
        self.assertFalse(w.state_dir(self.root).exists())

    def test_unavailable_port_does_not_create_state(self):
        self.available.side_effect = w.EnvironmentError_("unavailable")
        with self.assertRaises(w.EnvironmentError_):
            self.init()
        self.assertFalse(w.state_dir(self.root).exists())

    def test_copied_or_tampered_state_cannot_target_other_worktree(self):
        state = self.init()
        with self.assertRaises(w.EnvironmentError_):
            w.validate_state(self.other, state)
        state["database"] = "production"
        with self.assertRaises(w.EnvironmentError_):
            w.validate_state(self.root, state)

    def test_symlink_state_directory_is_rejected(self):
        (self.root / ".review").symlink_to(self.other, target_is_directory=True)
        with self.assertRaises(w.EnvironmentError_):
            self.init()

    def test_public_credential_or_state_directory_is_rejected(self):
        self.init()
        directory = w.state_dir(self.root)
        os.chmod(directory / "mysql-app-password", 0o644)
        with self.assertRaises(w.EnvironmentError_):
            w.read_state(self.root)
        os.chmod(directory / "mysql-app-password", 0o600)
        os.chmod(directory, 0o755)
        with self.assertRaises(w.EnvironmentError_):
            w.read_state(self.root)

    def test_duplicate_json_keys_are_rejected(self):
        self.init()
        state = w.state_dir(self.root) / "state.json"
        state.write_text('{"version":1,"version":1}')
        with self.assertRaises(w.EnvironmentError_):
            w.read_state(self.root)

    def test_clean_requires_exact_namespace_and_is_dry_run_by_default(self):
        state = self.init()
        with mock.patch.object(w, "root_from_git", return_value=self.root), mock.patch.object(w, "compose") as compose, contextlib.redirect_stdout(io.StringIO()), contextlib.redirect_stderr(io.StringIO()):
            self.assertEqual(w.main(["clean", "--namespace", "production"]), 1)
            self.assertEqual(w.main(["clean", "--namespace", state["namespace"]]), 0)
            self.assertEqual(w.main(["clean", "--namespace", state["namespace"], "--execute"]), 1)
            compose.assert_not_called()
            self.assertEqual(w.main(["clean", "--namespace", state["namespace"], "--execute", "--confirm", state["namespace"]]), 0)
            compose.assert_called_once_with(self.root, state, ["down", "--volumes", "--remove-orphans"])

    def test_environment_overrides_and_remote_docker_are_rejected(self):
        state = self.init()
        with mock.patch.dict(os.environ, {"NERAE_TEST_DATABASE": "production", "COMPOSE_PROJECT_NAME": "production", "DOCKER_HOST": "tcp://example:2375"}, clear=True):
            with self.assertRaises(w.EnvironmentError_):
                w.docker_env(self.root, state)
        with mock.patch.dict(os.environ, {"NERAE_TEST_DATABASE": "production", "COMPOSE_PROJECT_NAME": "production"}, clear=True):
            env = w.docker_env(self.root, state)
            self.assertEqual(env["NERAE_TEST_DATABASE"], state["database"])
            self.assertNotIn("COMPOSE_PROJECT_NAME", env)
        with mock.patch.object(w.subprocess, "run", return_value=mock.Mock(stdout=json.dumps([{"Endpoints": {"docker": {"Host": "ssh://server"}}}]))):
            with self.assertRaises(w.EnvironmentError_):
                w.require_local_docker({})


class GitIsolationTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name) / "one"
        self.other = Path(self.temp.name) / "two"
        subprocess.run(["git", "init", "--quiet", str(self.root)], check=True, env=w.git_env())
        subprocess.run(["git", "-C", str(self.root), "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "--quiet", "--allow-empty", "-m", "test"], check=True, env=w.git_env())
        subprocess.run(["git", "-C", str(self.root), "worktree", "add", "--quiet", "--detach", str(self.other), "HEAD"], check=True, env=w.git_env())
        self.root = self.root.resolve()
        self.other = self.other.resolve()

    def test_other_worktree_ports_are_reserved_across_initializations(self):
        with mock.patch.object(w, "available"):
            first = w.initialize(self.root, 23306, 26379)
            with self.assertRaisesRegex(w.EnvironmentError_, "another worktree"):
                w.initialize(self.other, 26379, 26380)
            second = w.initialize(self.other, 23307, 26380)
        self.assertNotEqual(first["namespace"], second["namespace"])
        self.assertEqual(w.read_state(self.root), first)
        self.assertEqual(w.read_state(self.other), second)
        # Simulate a legacy/conflicting state: repeated init must also refuse it.
        second["redis_port"] = first["mysql_port"]
        (w.state_dir(self.other) / "state.json").write_text(json.dumps(second))
        with mock.patch.object(w, "available"), self.assertRaisesRegex(w.EnvironmentError_, "another worktree"):
            w.initialize(self.root, first["mysql_port"], first["redis_port"])

    def test_git_environment_cannot_redirect_worktree(self):
        fake_script = self.root / "scripts" / "dev" / "workspace.py"
        fake_script.parent.mkdir(parents=True)
        fake_script.write_text("")
        old_cwd = Path.cwd()
        self.addCleanup(os.chdir, old_cwd)
        os.chdir(self.root)
        other_git = subprocess.check_output(["git", "-C", str(self.other), "rev-parse", "--absolute-git-dir"], text=True, env=w.git_env()).strip()
        with mock.patch.object(w, "__file__", str(fake_script)), mock.patch.dict(os.environ, {"GIT_DIR": other_git, "GIT_WORK_TREE": str(self.other)}):
            self.assertEqual(w.root_from_git(), self.root)


if __name__ == "__main__":
    unittest.main()
