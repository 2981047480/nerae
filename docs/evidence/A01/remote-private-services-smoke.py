#!/usr/bin/env python3
"""Exercise the compiled API test with fresh, socket-only synthetic services."""
import hashlib
import json
import os
from pathlib import Path
import re
import secrets
import shutil
import subprocess
import sys
import tempfile
import time

if len(sys.argv) not in (2, 3) or len(sys.argv) == 3 and sys.argv[2] != '--containers':
    raise SystemExit('usage: remote-private-services-smoke.py /tmp/nerae-package-preflight.XXXXXXXX [--containers]')
use_containers = len(sys.argv) == 3
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
migrator = root / 'nerae-migrate'
migration_test = root / 'migrations-integration.test'
bundle = root / 'release-clean'
manifest_path = bundle / 'scripts/dev/migrations.json'
manifest = json.loads(manifest_path.read_text())
sql_parts = []
for index, item in enumerate(manifest['migrations'], 1):
    if item['order'] != index or item['id'] != item['module'] + '/' + item['file']:
        raise SystemExit('invalid migration manifest')
    path = (bundle / 'apps/api/migrations' / item['id']).resolve(strict=True)
    if not path.is_relative_to(bundle / 'apps/api/migrations') or hashlib.sha256(path.read_bytes()).hexdigest() != item['sha256']:
        raise SystemExit('migration checksum mismatch')
    sql_parts.append(path.read_text())
base = Path(tempfile.mkdtemp(prefix='api-smoke-', dir=root))
metadata = Path(tempfile.mkdtemp(prefix='api-smoke-meta-', dir=root))
data = metadata / 'mysql-data'
mysql_tmp = metadata / 'mysql-tmp'
redis_data = metadata / 'redis-data'
data.mkdir(mode=0o700)
mysql_tmp.mkdir(mode=0o700)
redis_data.mkdir(mode=0o700)
log_path = metadata / 'services.log'
mysql_sock, redis_sock = base / 'mysql.sock', base / 'redis.sock'
env = {**os.environ, 'LC_ALL': 'C', 'MYSQL_PWD': '',
       'LD_LIBRARY_PATH': str(root / 'root/usr/lib/x86_64-linux-gnu')}
common = [str(mysqld), '--no-defaults', f'--basedir={package}', f'--datadir={data}',
          f'--plugin-dir={package / "lib/mysql/plugin"}', f'--lc-messages-dir={package / "share/mysql"}',
          f'--tmpdir={mysql_tmp}', '--skip-name-resolve', '--mysqlx=0', '--skip-log-bin',
          '--secure-file-priv=NULL', '--innodb-buffer-pool-size=64M', '--innodb-redo-log-capacity=16M']
client = [str(mysql), '--no-defaults', '--protocol=socket', f'--socket={mysql_sock}', '--user=root']
children = []
containers = []
succeeded = False
try:
    with log_path.open('wb') as log:
        if use_containers:
            docker = shutil.which('docker')
            if not docker:
                raise RuntimeError('docker unavailable')
            mysql_token = secrets.token_hex(16)
            mysql_name = 'nerae-private-mysql-' + mysql_token
            mysql_image = 'mirror.ccs.tencentyun.com/library/mysql@sha256:a0225d1ea6631c8bc2385dc4c2faba17bbf1834ecf369e1659a9a382bd8d5dd7'
            mysql_child = subprocess.Popen([docker, 'run', '--rm', '--network', 'none', '--user', f'{os.getuid()}:{os.getgid()}',
                                           '--name', mysql_name, '--label', 'com.nerae.smoke=' + mysql_token,
                                           '-v', f'{base}:/socket', '-v', f'{data}:/data', '-v', f'{mysql_tmp}:/tmp-data',
                                           '-e', 'MYSQL_ALLOW_EMPTY_PASSWORD=1', '-e', 'MYSQL_INITDB_SKIP_TZINFO=1', mysql_image,
                                           '--datadir=/data', '--socket=/socket/mysql.sock', '--pid-file=/socket/mysql.pid',
                                           '--tmpdir=/tmp-data', '--skip-networking', '--mysqlx=0', '--skip-log-bin', '--local-infile=0',
                                           '--secure-file-priv=NULL', '--innodb-buffer-pool-size=64M', '--innodb-redo-log-capacity=16M'],
                                          env=env, stdin=subprocess.DEVNULL, stdout=log, stderr=subprocess.STDOUT)
            containers.append((mysql_name, mysql_token, mysql_child))
        else:
            initialized = subprocess.run(common + ['--initialize-insecure'], env=env, stdout=log, stderr=subprocess.STDOUT, timeout=90)
            if initialized.returncode:
                raise RuntimeError('private MySQL initialization failed')
            mysql_child = subprocess.Popen(common + ['--skip-networking', '--local-infile=0', f'--socket={mysql_sock}',
                                                     f'--pid-file={base / "mysql.pid"}'],
                                           env=env, stdin=subprocess.DEVNULL, stdout=log, stderr=subprocess.STDOUT)
        children.append(mysql_child)
        deadline = time.monotonic() + 60
        while time.monotonic() < deadline and mysql_child.poll() is None:
            if use_containers:
                # The official image briefly starts an initialization server on
                # the same socket, stops it, then starts the real server. A
                # successful query to that temporary process is not readiness.
                output = log_path.read_bytes()
                marker = b'MySQL init process done. Ready for start up.'
                offset = output.find(marker)
                if offset < 0 or b'ready for connections' not in output[offset:]:
                    time.sleep(.2)
                    continue
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
        if use_containers:
            redis_token = secrets.token_hex(16)
            redis_name = 'nerae-private-redis-' + redis_token
            redis_image = 'mirror.ccs.tencentyun.com/library/redis@sha256:b6b6e3dc0c0952737748e55d5d885272840c7e42ba41ab1d5489f693419882f5'
            redis_child = subprocess.Popen([docker, 'run', '--rm', '--network', 'none', '--user', f'{os.getuid()}:{os.getgid()}',
                                           '--name', redis_name, '--label', 'com.nerae.smoke=' + redis_token,
                                           '-v', f'{base}:/socket', '-v', f'{redis_data}:/data', redis_image, 'redis-server',
                                           '--port', '0', '--unixsocket', '/socket/redis.sock', '--unixsocketperm', '700',
                                           '--save', '', '--appendonly', 'no', '--daemonize', 'no', '--dir', '/data'],
                                          env=env, stdin=subprocess.DEVNULL, stdout=log, stderr=subprocess.STDOUT)
            containers.append((redis_name, redis_token, redis_child))
        else:
            redis_child = subprocess.Popen([str(redis), '--port', '0', '--unixsocket', str(redis_sock), '--unixsocketperm', '700',
                                            '--save', '', '--appendonly', 'no', '--daemonize', 'no', '--dir', str(redis_data)],
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
        password = secrets.token_hex(24)
        sql = ("CREATE DATABASE nerae_api_process_test; CREATE DATABASE nerae_api_assembly_test; CREATE DATABASE neraemigrationrelease; "
               "CREATE DATABASE neraemigrationexecutortest; CREATE DATABASE neraemigrationdrifttest; CREATE DATABASE neraeother; "
               f"CREATE USER 'nerae_release'@'localhost' IDENTIFIED BY '{password}'; "
               f"CREATE USER 'nerae_migrator'@'localhost' IDENTIFIED BY '{password}'; "
               f"CREATE USER 'nerae_migratordrift'@'localhost' IDENTIFIED BY '{password}'; "
               f"CREATE USER 'nerae_overbroad'@'localhost' IDENTIFIED BY '{password}'; "
               "GRANT SELECT, INSERT, UPDATE, CREATE, ALTER, DROP, INDEX, REFERENCES ON neraemigrationrelease.* TO 'nerae_release'@'localhost'; "
               "GRANT SELECT, INSERT, UPDATE, CREATE, ALTER, DROP, INDEX, REFERENCES ON neraemigrationexecutortest.* TO 'nerae_migrator'@'localhost'; "
               "GRANT SELECT, INSERT, UPDATE, CREATE, ALTER, DROP, INDEX, REFERENCES ON neraemigrationdrifttest.* TO 'nerae_migratordrift'@'localhost'; "
               "GRANT SELECT, INSERT, UPDATE, CREATE, ALTER, DROP, INDEX, REFERENCES ON neraemigrationexecutortest.* TO 'nerae_overbroad'@'localhost'; "
               "GRANT SELECT, INSERT, UPDATE, CREATE, ALTER, DROP, INDEX, REFERENCES ON neraeother.* TO 'nerae_overbroad'@'localhost'; "
               "USE nerae_api_process_test;\n" + '\n'.join(sql_parts) +
               "\nUSE nerae_api_assembly_test;\n" + '\n'.join(sql_parts))
        # A dedicated account verifies the covered business path without DDL
        # or root privileges. This is database-wide DML, not least privilege.
        sql += f"\nCREATE USER 'nerae_api_runtime'@'localhost' IDENTIFIED BY '{password}'; GRANT SELECT, INSERT, UPDATE, DELETE ON nerae_api_assembly_test.* TO 'nerae_api_runtime'@'localhost';\n"
        bootstrap = subprocess.run(client, input=sql, env=env, stdout=subprocess.PIPE, stderr=subprocess.PIPE,
                                   text=True, timeout=30)
        if bootstrap.returncode:
            error_code = re.search(r'ERROR ([0-9]+) \(([A-Z0-9]+)\)(?: at line ([0-9]+))?', bootstrap.stderr)
            classification = '/'.join(part for part in error_code.groups() if part) if error_code else 'unknown'
            raise RuntimeError('private test database bootstrap failed: ' + classification)
        def migration_dsn(user, database):
            return f'{user}:{password}@unix({mysql_sock})/{database}?parseTime=true&loc=UTC'
        migration_env = {**env, 'NERAE_MIGRATION_ROOT': str(bundle), 'NERAE_MIGRATION_DATABASE': 'neraemigrationrelease',
                         'NERAE_MYSQL_DSN': migration_dsn('nerae_release', 'neraemigrationrelease')}
        for operation in ('check', 'up', 'up'):
            migration = subprocess.run([str(migrator), operation], env=migration_env, stdout=subprocess.PIPE,
                                       stderr=subprocess.STDOUT, text=True, timeout=90)
            if migration.returncode:
                raise RuntimeError(f'private migration {operation} failed: ' + migration.stdout[-600:])
            print(migration.stdout, end='')
        cases = [
            ('failure', 'nerae_migrator', 'neraemigrationexecutortest',
             'PASS: restricted grants, concurrent migrations, multi-statement DDL, drift and dirty recovery gates'),
            ('session_drift', 'nerae_migratordrift', 'neraemigrationdrifttest',
             'PASS: changed database session remains dirty and is discarded'),
        ]
        for case, user, database, marker in cases:
            migration_test_env = {**env, 'NERAE_MIGRATION_CASE': case,
                                  'NERAE_MIGRATION_TEST_DSN': migration_dsn(user, database),
                                  'NERAE_MIGRATION_BAD_DSN': migration_dsn('nerae_overbroad', database)}
            migration_checked = subprocess.run([str(migration_test), '-test.v', '-test.run=^TestApplyAgainstPrivateMySQL$'],
                                               env=migration_test_env, stdout=subprocess.PIPE, stderr=subprocess.STDOUT,
                                               text=True, timeout=90)
            print(migration_checked.stdout, end='')
            if migration_checked.returncode or marker not in migration_checked.stdout:
                raise RuntimeError('migration integration test failed or was skipped')
        test_env = {**env,
                    'NERAE_API_PROCESS_TEST_DSN': f'root@unix({mysql_sock})/nerae_api_process_test?parseTime=true&loc=UTC',
                    'NERAE_API_PROCESS_REDIS_SOCKET': str(redis_sock)}
        test = subprocess.run([str(binary), '-test.v', '-test.run=^TestAPIProcessPrivateServices$'], env=test_env,
                              stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True, timeout=90)
        print(test.stdout, end='')
        if test.returncode or 'PASS: protected API process startup -> loopback business route -> enqueue -> clean shutdown' not in test.stdout:
            raise RuntimeError('API process integration test failed or was skipped')
        assembly_env = {**env,
                        'NERAE_API_ASSEMBLY_TEST_DSN': f'nerae_api_runtime:{password}@unix({mysql_sock})/nerae_api_assembly_test?parseTime=true&loc=UTC',
                        'NERAE_API_ASSEMBLY_REDIS_SOCKET': str(redis_sock)}
        assembly = subprocess.run([str(binary), '-test.v', '-test.run=^TestBusinessAssemblyPrivateServices$'],
                                  env=assembly_env, stdout=subprocess.PIPE, stderr=subprocess.STDOUT,
                                  text=True, timeout=90)
        print(assembly.stdout, end='')
        if (assembly.returncode or
                'PASS: assembled new-device exchange and shared workout through proxy boundary' not in assembly.stdout):
            raise RuntimeError('two-device business integration test failed or was skipped')
        succeeded = True
except (OSError, RuntimeError, subprocess.TimeoutExpired) as error:
    print('FAIL:', error, '; synthetic directories retained:', base, metadata, file=sys.stderr)
finally:
    cleanup_ok = True
    cleanup_reasons = []
    for name, token, docker_child in reversed(containers):
        try:
            for attempt in range(3):
                check = subprocess.run([docker, 'inspect', '--format', '{{.Id}} {{index .Config.Labels "com.nerae.smoke"}} {{.State.Running}}', name],
                                       stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True, timeout=10)
                diagnostic = check.stderr.lower()
                missing = 'no such object' in diagnostic or 'no such container' in diagnostic
                if check.returncode != 0:
                    if not missing:
                        cleanup_ok = False
                        cleanup_reasons.append('container inspection unavailable')
                    elif docker_child.poll() is None:
                        try:
                            docker_child.wait(timeout=3)
                        except subprocess.TimeoutExpired:
                            cleanup_ok = False
                            cleanup_reasons.append('container absent while launch process still running')
                    break
                parts = check.stdout.split()
                if len(parts) != 3 or not re.fullmatch(r'[0-9a-f]{64}', parts[0]) or parts[1] != token:
                    cleanup_ok = False
                    cleanup_reasons.append('container identity or label mismatch')
                    break
                if parts[2] == 'false':
                    break
                if parts[2] != 'true' or attempt == 2:
                    cleanup_ok = False
                    cleanup_reasons.append('container remained running or state was invalid')
                    break
                try:
                    subprocess.run([docker, 'stop', '--time', '10', parts[0]], stdout=subprocess.DEVNULL,
                                   stderr=subprocess.DEVNULL, timeout=20, check=False)
                except subprocess.TimeoutExpired:
                    pass
        except (OSError, subprocess.TimeoutExpired):
            cleanup_ok = False
            cleanup_reasons.append('container cleanup command failed or timed out')
    for child in reversed(children):
        if child.poll() is None:
            child.terminate()
            try:
                child.wait(timeout=20)
            except subprocess.TimeoutExpired:
                child.kill()
                child.wait(timeout=5)
    if not cleanup_ok:
        succeeded = False
        print('FAIL: cannot confirm temporary containers stopped:', ', '.join(cleanup_reasons),
              '; synthetic directories retained:', base, metadata, file=sys.stderr)
    if succeeded:
        shutil.rmtree(base)
        shutil.rmtree(metadata)
        print('PASS: private synthetic services stopped and their test directory removed')
sys.exit(0 if succeeded else 1)
