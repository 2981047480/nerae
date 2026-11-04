#!/usr/bin/env python3
"""Socket-only TLS fixture for the iOS recovery transport; synthetic values only."""
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import json
import os
import ssl
import time


class Handler(BaseHTTPRequestHandler):
    def log_message(self, *_args):
        pass

    def do_GET(self):
        with open(os.environ['NERAE_RECOVERY_HITS'], 'a', encoding='utf-8') as output:
            output.write(json.dumps({'path': self.path, 'requestMatches': False}) + '\n')
        self.send_response(404)
        self.send_header('Content-Length', '0')
        self.end_headers()

    def do_POST(self):
        length = int(self.headers.get('Content-Length', '0'))
        body = self.rfile.read(length)
        valid = (self.headers.get('Authorization') == 'Bearer synthetic-token' and
                 self.headers.get('Idempotency-Key') == 'create-key-001' and
                 self.headers.get('Cache-Control') == 'no-store' and
                 body == b'{"source":"free","date":"2026-10-29","clientEventId":"event-0001"}')
        with open(os.environ['NERAE_RECOVERY_HITS'], 'a', encoding='utf-8') as output:
            output.write(json.dumps({'path': self.path, 'requestMatches': valid}) + '\n')
        if not valid or not self.path.endswith('/me/sessions/replay'):
            self.send_response(400)
            self.send_header('Content-Length', '0')
            self.end_headers()
            return
        if self.path.startswith('/redirect/'):
            self.send_response(302)
            self.send_header('Location', '/leaked')
            self.send_header('Content-Length', '0')
            self.end_headers()
            return
        if self.path.startswith('/slow/'):
            time.sleep(1.0)
        if self.path.startswith('/chunked/'):
            self.send_response(201)
            self.send_header('Content-Type', 'application/json')
            self.send_header('Transfer-Encoding', 'chunked')
            self.send_header('Connection', 'close')
            self.end_headers()
            try:
                for _ in range(65):
                    self.wfile.write(b'10000\r\n' + b'x' * 65536 + b'\r\n')
                    self.wfile.flush()
            except (BrokenPipeError, ConnectionResetError):
                pass
            self.close_connection = True
            return
        raw = b'x' * (4 * 1024 * 1024 + 1) if self.path.startswith('/large/') else b'{"id":"42"}'
        self.send_response(201)
        self.send_header('Content-Type', 'application/json')
        self.send_header('Cache-Control', 'no-store')
        self.send_header('Idempotency-Replayed', 'true')
        self.send_header('X-Request-ID', '0123456789abcdef0123456789abcdef')
        self.send_header('Content-Length', str(len(raw)))
        self.send_header('Connection', 'close')
        self.end_headers()
        try:
            self.wfile.write(raw)
        except (BrokenPipeError, ConnectionResetError):
            pass
        self.close_connection = True


server = ThreadingHTTPServer(('127.0.0.1', 0), Handler)
context = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
context.load_cert_chain(os.environ['NERAE_RECOVERY_CERT'], os.environ['NERAE_RECOVERY_KEY'])
server.socket = context.wrap_socket(server.socket, server_side=True)
with open(os.environ['NERAE_RECOVERY_PORT'], 'w', encoding='ascii') as output:
    output.write(str(server.server_port))
server.serve_forever()
