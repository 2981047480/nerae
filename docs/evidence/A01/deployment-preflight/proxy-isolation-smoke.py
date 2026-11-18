#!/usr/bin/env python3
"""Bounded synthetic Caddy-to-Caddy UDS check; does not touch the live Caddy."""
import grp
import http.client
import http.server
import json
import os
import pwd
import resource
import shutil
import socket
import subprocess
import sys
import tempfile
import threading
import time
from pathlib import Path

FRONT_UID, SIDE_UID, SHARED_GID, OTHER_UID, OTHER_GID = 61121, 61122, 61120, 61123, 61123
SYNTHETIC_CREDENTIAL = 'synthetic-proxy-boundary-key-for-isolation-test'
MIN_FREE_BYTES = 4 * 1024**3
MAX_CHILD_FILE_BYTES = 1024**2


def drop(uid, gid):
    def become():
        # The proxies have no persistent config or certificates; cap any
        # per-process test file, including the temporary stdout/stderr log.
        resource.setrlimit(resource.RLIMIT_FSIZE, (MAX_CHILD_FILE_BYTES, MAX_CHILD_FILE_BYTES))
        os.setgroups([SIDE_UID] if uid == SIDE_UID else [])
        os.setgid(gid)
        os.setuid(uid)
    return become


def must_be_unused_ids():
    for uid in (FRONT_UID, SIDE_UID, OTHER_UID):
        try:
            pwd.getpwuid(uid)
        except KeyError:
            continue
        raise RuntimeError('test UID already allocated')
    for gid in (SHARED_GID, OTHER_GID):
        try:
            grp.getgrgid(gid)
        except KeyError:
            continue
        raise RuntimeError('test GID already allocated')


def free_port():
    with socket.socket() as s:
        s.bind(('127.0.0.1', 0))
        return s.getsockname()[1]


def chown_dir(path, uid, gid, mode):
    path.mkdir()
    os.chown(path, uid, gid)
    os.chmod(path, mode)


class Backend(http.server.BaseHTTPRequestHandler):
    def log_message(self, *args):
        pass

    def do_GET(self):
        creds = self.headers.get_all('X-Nerae-Proxy-Credential', [])
        sources = self.headers.get_all('X-Nerae-Trusted-Source-IP', [])
        body = json.dumps({'path': self.path, 'credential_count': len(creds),
                           'credential_matches': creds == [SYNTHETIC_CREDENTIAL],
                           'source_count': len(sources), 'source_matches': sources == ['127.0.0.1']}).encode()
        self.send_response(200)
        self.send_header('Content-Type', 'application/json')
        self.send_header('Content-Length', str(len(body)))
        self.end_headers()
        self.wfile.write(body)


def run():
    if os.geteuid() != 0:
        raise RuntimeError('test requires root to simulate independent service UIDs')
    if not shutil.which('caddy'):
        raise RuntimeError('caddy unavailable')
    if shutil.disk_usage('/').free < MIN_FREE_BYTES:
        raise RuntimeError('less than 4 GiB free')
    must_be_unused_ids()
    base = Path(tempfile.mkdtemp(prefix='nerae-proxy-qa-', dir='/tmp'))
    os.chown(base, 0, SHARED_GID)
    os.chmod(base, 0o750)
    children = []
    logfiles = []
    backend = None
    successful = False
    try:
        front_dir, side_dir, socket_dir = base / 'front', base / 'side', base / 'socket'
        chown_dir(front_dir, FRONT_UID, SHARED_GID, 0o700)
        chown_dir(side_dir, 0, SIDE_UID, 0o750)
        side_data = base / 'side-data'
        chown_dir(side_data, SIDE_UID, SHARED_GID, 0o700)
        chown_dir(socket_dir, SIDE_UID, SHARED_GID, 0o2750)
        front_port = free_port()
        backend = http.server.ThreadingHTTPServer(('127.0.0.1', 0), Backend)
        backend.daemon_threads = True
        threading.Thread(target=backend.serve_forever, daemon=True).start()
        backend_port = backend.server_address[1]
        socket_path = socket_dir / 'proxy.sock'
        template_dir = Path(__file__).resolve().parent
        front_fragment = (template_dir / 'caddy-route.example').read_text()
        side_config = (template_dir / 'caddy-sidecar.example').read_text()
        socket_placeholder = '/run/nerae-proxy/proxy.sock'
        if front_fragment.count(socket_placeholder) != 1 or side_config.count(socket_placeholder) != 1:
            raise RuntimeError('unexpected socket placeholder count')
        if side_config.count('API_LOOPBACK_PORT') != 1 or side_config.count('REPLACE_WITH_PRIVATE_CREDENTIAL') != 1:
            raise RuntimeError('unexpected sidecar placeholder count')
        front_fragment = front_fragment.replace(socket_placeholder, str(socket_path))
        side_config = side_config.replace(socket_placeholder, str(socket_path))
        side_config = side_config.replace('API_LOOPBACK_PORT', str(backend_port))
        side_config = side_config.replace('REPLACE_WITH_PRIVATE_CREDENTIAL', SYNTHETIC_CREDENTIAL)
        front_config = '''{
 admin off
 persist_config off
 auto_https off
}
http://127.0.0.1:%s {
 %s
 respond "existing site" 404
}
''' % (front_port, front_fragment)
        for directory, uid, config in ((side_dir, SIDE_UID, side_config), (front_dir, FRONT_UID, front_config)):
            path = directory / 'Caddyfile'
            path.write_text(config)
            os.chown(path, 0 if directory == side_dir else uid, SIDE_UID if directory == side_dir else SHARED_GID)
            os.chmod(path, 0o640 if directory == side_dir else 0o600)
            data_dir = side_data if directory == side_dir else directory
            env = {'PATH': '/usr/bin:/bin', 'HOME': str(data_dir), 'XDG_CONFIG_HOME': str(data_dir / 'config'),
                   'XDG_DATA_HOME': str(data_dir / 'data')}
            adapt = subprocess.run(['caddy', 'adapt', '--config', str(path), '--adapter', 'caddyfile', '--validate'],
                                   env=env, preexec_fn=drop(uid, SHARED_GID), capture_output=True, timeout=20)
            if adapt.returncode:
                raise RuntimeError('isolated Caddy validation failed for '+directory.name)
            adapted = json.loads(adapt.stdout)
            if not adapted.get('admin', {}).get('disabled'):
                raise RuntimeError('isolated Caddy admin is not disabled')
            if adapted.get('admin', {}).get('config', {}).get('persist') is not False:
                raise RuntimeError('isolated Caddy config persistence is not disabled')
            if directory == front_dir and SYNTHETIC_CREDENTIAL.encode() in adapt.stdout:
                raise RuntimeError('front Caddy config contains credential')
            if directory == side_dir:
                servers = adapted.get('apps', {}).get('http', {}).get('servers', {})
                listens = [item for server in servers.values() for item in server.get('listen', [])]
                if len(listens) != 1 or not listens[0].startswith('unix/'):
                    raise RuntimeError('sidecar listener is not Unix-only')
            logfile = open(base / (directory.name + '.log'), 'wb')
            os.chmod(logfile.name, 0o600)
            logfiles.append(logfile)
            child = subprocess.Popen(['caddy', 'run', '--config', str(path), '--adapter', 'caddyfile'], env=env,
                                     preexec_fn=drop(uid, SHARED_GID), stdin=subprocess.DEVNULL,
                                     stdout=logfile, stderr=logfile)
            children.append(child)
            if directory == side_dir:
                deadline = time.monotonic() + 15
                while not socket_path.exists() and child.poll() is None and time.monotonic() < deadline:
                    time.sleep(0.1)
                if not socket_path.exists():
                    raise RuntimeError('isolated proxy socket did not appear')
        deadline = time.monotonic() + 15
        while time.monotonic() < deadline:
            if children[-1].poll() is not None:
                raise RuntimeError('isolated front proxy stopped')
            try:
                c = http.client.HTTPConnection('127.0.0.1', front_port, timeout=1)
                c.request('GET', '/other-site')
                response = c.getresponse(); response.read(); c.close()
                break
            except OSError:
                time.sleep(0.1)
        else:
            raise RuntimeError('isolated front proxy did not start')
        if response.status != 404:
            raise RuntimeError('unrelated route behavior changed')
        c = http.client.HTTPConnection('127.0.0.1', front_port, timeout=3)
        c.putrequest('GET', '/nerae-api-v1/auth/email/challenges?probe=1')
        c.putheader('X-Nerae-Proxy-Credential', 'forged-one')
        c.putheader('x-nerae-proxy-credential', 'forged-two')
        c.putheader('X-Nerae-Trusted-Source-IP', '198.51.100.9')
        c.putheader('x-nerae-trusted-source-ip', '198.51.100.10')
        c.endheaders()
        resp = c.getresponse(); body = resp.read(); c.close()
        if resp.status != 200:
            raise RuntimeError('synthetic proxy request failed')
        result = json.loads(body)
        if result != {'path': '/auth/email/challenges?probe=1', 'credential_count': 1,
                      'credential_matches': True, 'source_count': 1, 'source_matches': True}:
            raise RuntimeError('credential or source replacement failed')
        c = http.client.HTTPConnection('127.0.0.1', front_port, timeout=3)
        c.request('GET', '/nerae-api-v1/health/live')
        response = c.getresponse(); response.read(); c.close()
        if response.status != 404:
            raise RuntimeError('health path exposed')
        probe = 'import socket,sys; s=socket.socket(socket.AF_UNIX); s.connect(sys.argv[1]); s.close()'
        access = subprocess.run([sys.executable, '-c', probe, str(socket_path)], preexec_fn=drop(FRONT_UID, SHARED_GID),
                                stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, timeout=5)
        if access.returncode:
            raise RuntimeError('front service cannot access proxy socket')
        denied = subprocess.run([sys.executable, '-c', probe, str(socket_path)], preexec_fn=drop(OTHER_UID, OTHER_GID),
                                stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, timeout=5)
        if not denied.returncode:
            raise RuntimeError('unrelated UID can access proxy socket')
        read_probe = 'import sys; open(sys.argv[1], "rb").read()'
        denied_read = subprocess.run([sys.executable, '-c', read_probe, str(side_dir / 'Caddyfile')],
                                     preexec_fn=drop(FRONT_UID, SHARED_GID), stdout=subprocess.DEVNULL,
                                     stderr=subprocess.DEVNULL, timeout=5)
        if not denied_read.returncode:
            raise RuntimeError('front service can read sidecar credential file')
        write_probe = 'import os,sys; fd=os.open(sys.argv[1],os.O_WRONLY|os.O_APPEND); os.close(fd)'
        denied_write = subprocess.run([sys.executable, '-c', write_probe, str(side_dir / 'Caddyfile')],
                                      preexec_fn=drop(SIDE_UID, SHARED_GID), stdout=subprocess.DEVNULL,
                                      stderr=subprocess.DEVNULL, timeout=5)
        if not denied_write.returncode:
            raise RuntimeError('sidecar UID can rewrite root-owned credential configuration')
        create_probe = 'import os,sys; fd=os.open(sys.argv[1],os.O_WRONLY|os.O_CREAT|os.O_EXCL,0o600); os.close(fd)'
        denied_create = subprocess.run([sys.executable, '-c', create_probe, str(side_dir / 'probe')],
                                       preexec_fn=drop(SIDE_UID, SHARED_GID), stdout=subprocess.DEVNULL,
                                       stderr=subprocess.DEVNULL, timeout=5)
        if not denied_create.returncode:
            raise RuntimeError('sidecar UID can replace protected configuration')
        print('PASS: root-owned sidecar config readable by sidecar, not writable or replaceable')
        print('PASS: isolated Unix socket front/sidecar, header replacement, health denial and non-secret config')
        print('PASS: shared socket access, unrelated UID denied, front UID cannot read sidecar configuration')
        print('PASS: disabled admin/config autosave; non-target route preserved in isolated configuration')
        successful = True
    finally:
        for child in reversed(children):
            if child.poll() is None:
                child.terminate()
                try:
                    child.wait(timeout=8)
                except subprocess.TimeoutExpired:
                    child.kill(); child.wait(timeout=5)
        if backend is not None:
            backend.shutdown(); backend.server_close()
        for f in logfiles:
            f.close()
        if successful:
            shutil.rmtree(base)
            print('PASS: synthetic test directory removed')
        else:
            print('Synthetic test retained at ' + str(base))


if __name__ == '__main__':
    try:
        run()
    except Exception as e:
        print('FAIL: ' + str(e), file=sys.stderr)
        sys.exit(1)
