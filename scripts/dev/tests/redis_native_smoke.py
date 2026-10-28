from pathlib import Path
import argparse
import importlib.util
import json
import socket
import subprocess
import tempfile
import time

parser = argparse.ArgumentParser(description='Run two temporary native Redis isolation checks')
parser.add_argument('--server', required=True, type=Path)
parser.add_argument('--cli', required=True, type=Path)
args = parser.parse_args()
server, client = args.server.resolve(strict=True), args.cli.resolve(strict=True)
script = Path(__file__).resolve().parents[1] / 'workspace.py'
spec = importlib.util.spec_from_file_location('workspace', script)
workspace = importlib.util.module_from_spec(spec)
spec.loader.exec_module(workspace)


def free_port():
    with socket.socket() as sock:
        sock.bind(('127.0.0.1', 0))
        return sock.getsockname()[1]


def call(port, *args):
    result = subprocess.run([str(client), '-h', '127.0.0.1', '-p', str(port), '--raw', *args],
                            capture_output=True, text=True, check=True, timeout=5)
    return result.stdout.strip()

with tempfile.TemporaryDirectory(prefix='nerae-redis-smoke-', dir='/private/tmp') as tmp:
    roots = [Path(tmp)/'worktree-a', Path(tmp)/'worktree-b']
    for root in roots:
        root.mkdir()
    names = [workspace.workspace_identity(root) for root in roots]
    ports = [free_port(), free_port()]
    if ports[0] == ports[1]:
        raise SystemExit('port collision; no Redis server started')
    processes, logs = [], []
    try:
        for root, port in zip(roots, ports):
            log = open(root/'redis.log', 'w')
            logs.append(log)
            processes.append(subprocess.Popen([str(server), '--bind', '127.0.0.1', '--port', str(port),
                                               '--save', '', '--appendonly', 'no', '--protected-mode', 'yes',
                                               '--dir', str(root)], stdout=log, stderr=subprocess.STDOUT))
        for port, proc in zip(ports, processes):
            deadline = time.monotonic() + 15
            while time.monotonic() < deadline:
                if proc.poll() is not None:
                    raise RuntimeError(f'Redis exited unexpectedly: {proc.returncode}')
                try:
                    if call(port, 'PING') == 'PONG':
                        break
                except (subprocess.CalledProcessError, subprocess.TimeoutExpired):
                    pass
                time.sleep(0.1)
            else:
                raise RuntimeError('Redis did not become ready')
        a, b = names[0]['redis_prefix']+'sentinel', names[1]['redis_prefix']+'sentinel'
        assert call(ports[0], 'SET', a, 'a-only') == 'OK'
        assert call(ports[1], 'SET', b, 'b-only') == 'OK'
        assert call(ports[0], 'GET', a) == 'a-only'
        assert call(ports[1], 'GET', b) == 'b-only'
        assert call(ports[0], 'EXISTS', b) == '0'
        assert call(ports[1], 'EXISTS', a) == '0'
        assert call(ports[0], 'SET', 'shared-name', 'on-a') == 'OK'
        assert call(ports[1], 'SET', 'shared-name', 'on-b') == 'OK'
        assert call(ports[0], 'GET', 'shared-name') == 'on-a'
        assert call(ports[1], 'GET', 'shared-name') == 'on-b'
        version = subprocess.check_output([str(server), '--version'], text=True).strip()
        print(json.dumps({'status':'passed', 'redis_version':version, 'instances':2,
                          'tests':['independent ports', 'own key read', 'cross key absent', 'same key isolated'],
                          'runtime':'native macOS temporary processes', 'compose_verified':False}))
    finally:
        for proc in processes:
            if proc.poll() is None:
                proc.terminate()
        for proc in processes:
            try:
                proc.wait(timeout=5)
            except subprocess.TimeoutExpired:
                proc.kill()
                proc.wait(timeout=5)
        for log in logs:
            log.close()
