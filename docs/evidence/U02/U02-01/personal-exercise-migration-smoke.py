import os
import pathlib
import secrets
import shutil
import subprocess
import sys
import tempfile
import time

root = pathlib.Path(__file__).resolve().parent
base = pathlib.Path(tempfile.mkdtemp(prefix='nerae-workout-session-mysql-', dir='/tmp'))
os.chmod(base, 0o700)
evidence = pathlib.Path(tempfile.mkdtemp(prefix='nerae-personal-exercise-evidence-', dir='/tmp'))
os.chmod(evidence, 0o700)
mysql = 'nerae-personal-mysql-' + secrets.token_hex(5)
container = False

def run(args, **kwargs):
    return subprocess.run(args, stdout=subprocess.PIPE, stderr=subprocess.PIPE, check=True, **kwargs)

def inspect():
    return run(['docker', 'inspect', mysql, '--format',
                '{{.State.Running}} {{.HostConfig.NetworkMode}} {{index .Config.Labels "com.nerae.smoke"}}']).stdout.decode().strip()

try:
    if shutil.disk_usage('/').free < 7 * 1024**3:
        raise RuntimeError('server free space below 7 GiB')
    uid, gid = os.getuid(), os.getgid()
    image = 'sha256:a0225d1ea6631c8bc2385dc4c2faba17bbf1834ecf369e1659a9a382bd8d5dd7'
    run(['docker', 'run', '-d', '--network', 'none', '--user', f'{uid}:{gid}',
         '--name', mysql, '--label', f'com.nerae.smoke={mysql}', '-e', 'MYSQL_ALLOW_EMPTY_PASSWORD=1',
         '-v', f'{base}:/socket', image, '--datadir=/socket/data', '--socket=/socket/mysql.sock',
         '--pid-file=/socket/mysql.pid', '--skip-networking', '--mysqlx=0', '--skip-log-bin',
         '--local-infile=0', '--secure-file-priv=NULL', '--innodb-buffer-pool-size=64M',
         '--innodb-redo-log-capacity=16M'])
    container = True
    sock = base / 'mysql.sock'
    ready = False
    for _ in range(240):
        logs = subprocess.run(['docker', 'logs', mysql], stdout=subprocess.PIPE, stderr=subprocess.STDOUT).stdout
        if b'MySQL init process done. Ready for start up.' in logs and sock.exists():
            if subprocess.run(['docker', 'exec', mysql, 'mysqladmin', '--protocol=socket',
                               '--socket=/socket/mysql.sock', '-uroot', 'ping'],
                              stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL).returncode == 0:
                ready = True
                break
        time.sleep(.5)
    if not ready or inspect() != f'true none {mysql}':
        raise RuntimeError('isolated MySQL unavailable')

    def sql(text, db=None):
        command = ['docker', 'exec', '-i', mysql, 'mysql', '--protocol=socket', '--socket=/socket/mysql.sock', '-uroot', '-N']
        if db:
            command.append(db)
        return run(command, input=text.encode()).stdout

    migration_db = 'neraepersonalmigrate'
    migration_user = 'nmig_' + secrets.token_hex(5)
    password = secrets.token_hex(24)
    sql(f'CREATE DATABASE {migration_db} CHARACTER SET utf8mb4 COLLATE utf8mb4_bin;')
    sql(f"CREATE USER '{migration_user}'@'localhost' IDENTIFIED BY '{password}'; GRANT ALTER,CREATE,DROP,INDEX,INSERT,REFERENCES,SELECT,UPDATE ON {migration_db}.* TO '{migration_user}'@'localhost';")
    seeded = False
    for package, count in (('release20', 20), ('release21', 21), ('release22', 22), ('release22', 22)):
        if count == 22 and package == 'release22' and not seeded:
            sql("INSERT INTO user (created_at,updated_at) VALUES (UTC_TIMESTAMP(6),UTC_TIMESTAMP(6)); SET @u=LAST_INSERT_ID(); INSERT INTO workout_personal_exercise (user_id,exercise_id,version,key_id,name_digest,snapshot_enc,created_at,updated_at) VALUES (@u,UNHEX('00000000000000000000000000000001'),1,'synthetic',UNHEX(REPEAT('ab',32)),REPEAT('x',29),UTC_TIMESTAMP(6),UTC_TIMESTAMP(6));", migration_db)
            seeded = True
        env = os.environ.copy()
        env['NERAE_MIGRATION_ROOT'] = str(root / package)
        env['NERAE_MIGRATION_DATABASE'] = migration_db
        env['NERAE_MYSQL_DSN'] = f'{migration_user}:{password}@unix({sock})/{migration_db}?parseTime=true&loc=UTC'
        result = subprocess.run([str(root/'nerae-migrate'), 'up'], env=env, stdout=subprocess.PIPE, stderr=subprocess.PIPE, timeout=120)
        if result.returncode or f'migration history current: {count} entries'.encode() not in result.stdout:
            raise RuntimeError('migration failed: ' + result.stderr.decode(errors='replace')[-1000:])
        print(result.stdout.decode().strip())
    copied = sql("SELECT COUNT(*) FROM workout_personal_exercise e JOIN workout_personal_exercise_snapshot v ON v.user_id=e.user_id AND v.exercise_id=e.exercise_id AND v.version=e.version WHERE v.key_id=e.key_id AND v.name_digest=e.name_digest AND v.snapshot_enc=e.snapshot_enc;", migration_db)
    if copied.strip() != b'1': raise RuntimeError('existing current snapshot was not copied')
    print('PASS: 20-to-21-to-22 migration, current snapshot backfill and idempotent repeat')
    db = 'nerae_workout_test'
    sql(f'CREATE DATABASE {db} CHARACTER SET utf8mb4 COLLATE utf8mb4_bin;')
    for name in ('user-0001.sql', 'workout-0001.sql', 'workout-0002.sql', 'workout-0004.sql', 'workout-0005.sql', 'workout-0006.sql', 'workout-0007.sql'):
        sql((root / name).read_text(), db)
    env = os.environ.copy()
    env['NERAE_WORKOUT_TEST_DSN'] = f'root@unix({sock})/{db}?parseTime=true&loc=UTC'
    test = subprocess.run([str(root / 'repository.test'), '-test.v', '-test.run', '^TestPersonalExercisePrivateMySQL$'],
                          env=env, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, timeout=90)
    (evidence / 'personal.log').write_bytes(test.stdout)
    output = test.stdout.decode(errors='replace')
    print(output[-6000:])
    if test.returncode or 'PASS: personal exercise ownership, encrypted names, duplicate names, version and replay with private MySQL' not in output or 'PASS: existing personal actions prevent key epoch reinitialization' not in output or "PASS: personal actions paginate and search without exposing another owner's entries" not in output or 'PASS: renamed personal actions retain owner-scoped historical name snapshots' not in output or 'PASS: training transaction enforces personal action ownership and immutable name' not in output:
        raise RuntimeError('test failed or skipped')
    if inspect() != f'true none {mysql}':
        raise RuntimeError('unexpected container identity before cleanup')
    run(['docker', 'rm', '-f', mysql])
    container = False
    shutil.rmtree(base)
    print('PASS: private MySQL personal exercise test; evidence:', evidence / 'personal.log')
except Exception as exc:
    print('FAILED:', type(exc).__name__, str(exc), 'synthetic directory:', base,
          'container:', mysql if container else 'none', 'evidence:', evidence, flush=True)
    sys.exit(1)
