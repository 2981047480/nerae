#!/usr/bin/env python3
"""Exercise the compiled API test with fresh, socket-only synthetic services."""
import hashlib
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import time

if len(sys.argv) != 2:
    raise SystemExit('usage: remote-private-services-smoke.py /tmp/nerae-package-preflight.XXXXXXXX')
requested_root = Path(sys.argv[1])
if requested_root.is_symlink():
    raise SystemExit('unsafe preflight directory')
root = requested_root.resolve(strict=True)
if (root.parent != Path('/tmp').resolve(strict=True) or not root.name.startswith('nerae-package-preflight.') or
        root.stat().st_uid != os.getuid() or root.stat().st_mode & 0o077):
    raise SystemExit('unsafe preflight directory')
package = root / 'root/usr'
mysql = package / 'bin/mysql'
mysqld = package / 'sbin/mysqld'
redis = package / 'bin/redis-server'
redis_cli = package / 'bin/redis-cli'
binary = root / 'api-integration.test'
manifest_path = root / 'scripts/dev/migrations.json'
manifest = json.loads(manifest_path.read_text())
sql_parts = []
for index, item in enumerate(manifest['migrations'], 1):
    if item['order'] != index or item['id'] != item['module'] + '/' + item['file']:
        raise SystemExit('invalid migration manifest')
    path = (root / 'apps/api/migrations' / item['id']).resolve(strict=True)
    if not path.is_relative_to(root / 'apps/api/migrations') or hashlib.sha256(path.read_bytes()).hexdigest() != item['sha256']:
        raise SystemExit('migration checksum mismatch')
    sql_parts.append(path.read_text())
base = Path(tempfile.mkdtemp(prefix='api-smoke-', dir=root))
data = base / 'mysql-data'
data.mkdir(mode=0o700)
mysql_sock, redis_sock = base / 'mysql.sock', base / 'redis.sock'
env = {**os.environ, 'LC_ALL': 'C', 'MYSQL_PWD': '',
       'LD_LIBRARY_PATH': str(root / 'root/usr/lib/x86_64-linux-gnu')}
common = [str(mysqld), '--no-defaults', f'--basedir={package}', f'--datadir={data}',
          f'--plugin-dir={package / "lib/mysql/plugin"}', f'--lc-messages-dir={package / "share/mysql"}',
          f'--tmpdir={base}', '--skip-name-resolve', '--mysqlx=0', '--skip-log-bin',
          '--secure-file-priv=NULL', '--innodb-buffer-pool-size=64M', '--innodb-redo-log-capacity=16M']
client = [str(mysql), '--no-defaults', '--protocol=socket', f'--socket={mysql_sock}', '--user=root']
children = []
succeeded = False
try:
    with (base / 'services.log').open('wb') as log:
        initialized = subprocess.run(common + ['--initialize-insecure'], env=env, stdout=log, stderr=subprocess.STDOUT, timeout=90)
        if initialized.returncode:
            raise RuntimeError('private MySQL initialization failed')
        mysql_child = subprocess.Popen(common + ['--skip-networking', '--local-infile=0', f'--socket={mysql_sock}',
                                                 f'--pid-file={base / "mysql.pid"}'],
                                       env=env, stdin=subprocess.DEVNULL, stdout=log, stderr=subprocess.STDOUT)
        children.append(mysql_child)
        deadline = time.monotonic() + 60
        while time.monotonic() < deadline and mysql_child.poll() is None:
            try:
                check = subprocess.run(client + ['-e', 'SELECT 1'], env=env, stdout=subprocess.DEVNULL,
                                       stderr=subprocess.DEVNULL, timeout=3)
                if check.returncode == 0:
                    break
            except subprocess.TimeoutExpired:
                pass
            time.sleep(.2)
        else:
            raise RuntimeError('private MySQL did not become ready')
        redis_child = subprocess.Popen([str(redis), '--port', '0', '--unixsocket', str(redis_sock), '--unixsocketperm', '700',
                                        '--save', '', '--appendonly', 'no', '--daemonize', 'no', '--dir', str(base)],
                                       env=env, stdin=subprocess.DEVNULL, stdout=log, stderr=subprocess.STDOUT)
        children.append(redis_child)
        deadline = time.monotonic() + 30
        while time.monotonic() < deadline and redis_child.poll() is None:
            check = subprocess.run([str(redis_cli), '-s', str(redis_sock), 'PING'], env=env,
                                   stdout=subprocess.PIPE, stderr=subprocess.DEVNULL, timeout=3)
            if check.returncode == 0 and check.stdout.strip() == b'PONG':
                break
            time.sleep(.2)
        else:
            raise RuntimeError('private Redis did not become ready')
        sql = 'CREATE DATABASE nerae_api_process_test;\nUSE nerae_api_process_test;\n' + '\n'.join(sql_parts)
        migration = subprocess.run(client, input=sql, env=env, stdout=subprocess.PIPE, stderr=subprocess.PIPE,
                                   text=True, timeout=60)
        if migration.returncode:
            raise RuntimeError('private migration failed: ' + migration.stderr[-600:])
        test_env = {**env,
                    'NERAE_API_PROCESS_TEST_DSN': f'root@unix({mysql_sock})/nerae_api_process_test?parseTime=true&loc=UTC',
                    'NERAE_API_PROCESS_REDIS_SOCKET': str(redis_sock)}
        test = subprocess.run([str(binary), '-test.v', '-test.run=^TestAPIProcessPrivateServices$'], env=test_env,
                              stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True, timeout=90)
        print(test.stdout, end='')
        if test.returncode or 'PASS: protected API process startup -> loopback business route -> enqueue -> clean shutdown' not in test.stdout:
            raise RuntimeError('API process integration test failed or was skipped')
        succeeded = True
except (OSError, RuntimeError, subprocess.TimeoutExpired) as error:
    print('FAIL:', error, '; synthetic directory retained:', base, file=sys.stderr)
finally:
    for child in reversed(children):
        if child.poll() is None:
            child.terminate()
            try:
                child.wait(timeout=20)
            except subprocess.TimeoutExpired:
                child.kill()
                child.wait(timeout=5)
    if succeeded:
        shutil.rmtree(base)
        print('PASS: private synthetic services stopped and their test directory removed')
sys.exit(0 if succeeded else 1)
