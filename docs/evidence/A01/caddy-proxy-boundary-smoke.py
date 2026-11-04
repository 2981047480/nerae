#!/usr/bin/env python3
"""Test an isolated candidate Caddy route with synthetic values on loopback."""
import http.client
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import os
from pathlib import Path
import secrets
import shutil
import socket
import subprocess
import sys
import tempfile
import threading
import time

if len(sys.argv) != 2:
    raise SystemExit('usage: caddy-proxy-boundary-smoke.py /tmp/nerae-package-preflight.XXXXXXXX')
requested = Path(sys.argv[1])
if requested.is_symlink():
    raise SystemExit('unsafe preflight directory')
root = requested.resolve(strict=True)
if (root.parent != Path('/tmp').resolve(strict=True) or not root.name.startswith('nerae-package-preflight.') or
        root.stat().st_uid != os.getuid() or root.stat().st_mode & 0o077):
    raise SystemExit('unsafe preflight directory')
caddy = shutil.which('caddy')
if not caddy:
    raise SystemExit('caddy unavailable')

requests = []
class Backend(BaseHTTPRequestHandler):
    def do_POST(self):
        requests.append((self.path, self.headers.get_all('X-Nerae-Proxy-Credential'),
                         self.headers.get_all('X-Nerae-Trusted-Source-IP')))
        self.send_response(204)
        self.end_headers()

    def log_message(self, *_args):
        pass

backend = ThreadingHTTPServer(('127.0.0.1', 0), Backend)
thread = threading.Thread(target=backend.serve_forever, daemon=True)
thread.start()
with socket.socket() as reservation:
    reservation.bind(('127.0.0.1', 0))
    proxy_port = reservation.getsockname()[1]
backend_port = backend.server_address[1]
credential = secrets.token_urlsafe(32)
child = None
try:
    with tempfile.TemporaryDirectory(prefix='caddy-smoke-', dir=root) as temporary:
        temporary_path = Path(temporary)
        config = temporary_path / 'Caddyfile'
        config.write_text(f'''{{
    admin off
    auto_https off
}}
http://127.0.0.1:{proxy_port} {{
    handle_path /nerae-api-v1/* {{
        @health path /health/*
        respond @health 404
        reverse_proxy 127.0.0.1:{backend_port} {{
            header_up X-Nerae-Proxy-Credential "{credential}"
            header_up X-Nerae-Trusted-Source-IP {{remote_host}}
        }}
    }}
    respond 404
}}
''')
        config.chmod(0o600)
        env = {**os.environ, 'XDG_DATA_HOME': str(temporary_path / 'data'),
               'XDG_CONFIG_HOME': str(temporary_path / 'config')}
        with (temporary_path / 'caddy.log').open('wb') as log:
            child = subprocess.Popen([caddy, 'run', '--config', str(config), '--adapter', 'caddyfile'],
                                     env=env, stdin=subprocess.DEVNULL, stdout=log, stderr=subprocess.STDOUT)
            deadline = time.monotonic() + 15
            while time.monotonic() < deadline and child.poll() is None:
                try:
                    with socket.create_connection(('127.0.0.1', proxy_port), timeout=.5):
                        break
                except OSError:
                    time.sleep(.1)
            else:
                raise RuntimeError('candidate proxy did not start')

            def request(method, path, spoof=False):
                connection = http.client.HTTPConnection('127.0.0.1', proxy_port, timeout=5)
                connection.putrequest(method, path)
                if spoof:
                    connection.putheader('X-Nerae-Proxy-Credential', 'forged-one')
                    connection.putheader('x-nerae-proxy-credential', 'forged-two')
                    connection.putheader('X-Nerae-Trusted-Source-IP', '203.0.113.1')
                    connection.putheader('x-nerae-trusted-source-ip', '203.0.113.2')
                connection.endheaders()
                response = connection.getresponse()
                response.read()
                connection.close()
                return response.status

            if request('POST', '/nerae-api-v1/auth/email/challenges', True) != 204:
                raise RuntimeError('candidate business route did not reach backend')
            if requests != [('/auth/email/challenges', [credential], ['127.0.0.1'])]:
                raise RuntimeError('candidate proxy did not overwrite headers or strip its prefix')
            if request('POST', '/nerae-api-v1/health/ready', True) != 404 or len(requests) != 1:
                raise RuntimeError('operational endpoint was exposed')
            if request('POST', '/unrelated', True) != 404 or len(requests) != 1:
                raise RuntimeError('unrelated path reached Nerae backend')
            print('PASS: isolated Caddy route strips prefix, overwrites spoofed headers and blocks health/unrelated paths')
finally:
    if child is not None and child.poll() is None:
        child.terminate()
        try:
            child.wait(timeout=10)
        except subprocess.TimeoutExpired:
            child.kill()
            child.wait(timeout=5)
    backend.shutdown()
    backend.server_close()
    thread.join(timeout=5)
