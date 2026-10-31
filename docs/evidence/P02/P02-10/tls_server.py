#!/usr/bin/env python3
import http.server
import json
import os
import ssl
import sys
import threading
import time

class Handler(http.server.BaseHTTPRequestHandler):
    protocol_version = 'HTTP/1.1'
    def log_message(self, *_):
        pass
    def send_body(self, status, body, extra=None):
        raw = body if isinstance(body, bytes) else body.encode()
        self.send_response(status)
        for key, value in (extra or {}).items():
            self.send_header(key, value)
        self.send_header('Content-Length', str(len(raw)))
        self.send_header('Connection', 'close')
        self.end_headers()
        try:
            self.wfile.write(raw)
        except (BrokenPipeError, ConnectionResetError):
            pass
        self.close_connection = True
    def do_POST(self):
        length = int(self.headers.get('Content-Length', '0'))
        body = self.rfile.read(length)
        if self.path == '/post-size':
            self.send_body(200, str(len(body)))
        else:
            self.send_body(404, 'not-found')
    def do_GET(self):
        with open(os.environ['NERAE_TLS_HITS'], 'a', encoding='utf-8') as out:
            out.write(self.path + '\n')
        if self.path.startswith('/success'):
            self.send_body(200, '{"ok":true}', {'Content-Type': 'application/json', 'Cache-Control': 'no-store'})
        elif self.path == '/inspect':
            body = json.dumps({'cacheControl': self.headers.get('Cache-Control'),
                               'authorizationPresent': self.headers.get('Authorization') == 'Bearer synthetic-probe',
                               'cookiePresent': self.headers.get('Cookie') is not None})
            self.send_body(200, body, {'Content-Type': 'application/json', 'Cache-Control': 'public, max-age=600'})
        elif self.path == '/redirect-301':
            self.send_body(301, 'redirect-301', {'Location': '/success'})
        elif self.path == '/redirect-302':
            self.send_body(302, 'redirect-302', {'Location': '/success'})
        elif self.path == '/redirect-cross':
            self.send_body(302, 'redirect-cross', {'Location': 'https://localhost:%d/echo' % self.server.server_port})
        elif self.path == '/redirect-downgrade':
            self.send_body(302, 'redirect-downgrade', {'Location': 'http://127.0.0.1:%d/echo' % self.server.http_port})
        elif self.path == '/large-fixed':
            self.send_body(200, b'x' * (4 * 1024 * 1024 + 1))
        elif self.path == '/large-chunked':
            self.send_response(200)
            self.send_header('Transfer-Encoding', 'chunked')
            self.send_header('Connection', 'close')
            self.end_headers()
            try:
                chunk = b'x' * 65536
                for _ in range(65):
                    self.wfile.write(('%x\r\n' % len(chunk)).encode() + chunk + b'\r\n')
                    self.wfile.flush()
            except (BrokenPipeError, ConnectionResetError):
                pass
            self.close_connection = True
        elif self.path == '/slow':
            time.sleep(2.2)
            self.send_body(200, 'slow')
        elif self.path == '/set-cookie':
            self.send_body(200, 'set', {'Set-Cookie': 'probe=1; Path=/; Secure'})
        elif self.path == '/check-cookie':
            self.send_body(200, 'present' if self.headers.get('Cookie') else 'absent')
        elif self.path == '/echo':
            self.send_body(200, 'followed')
        else:
            self.send_body(404, 'not-found')
    def do_DELETE(self):
        with open(os.environ['NERAE_TLS_HITS'], 'a', encoding='utf-8') as out:
            out.write(json.dumps({'method': 'DELETE', 'path': self.path,
                                  'authorizationPresent': self.headers.get('Authorization') == 'Bearer synthetic-probe',
                                  'contentLength': self.headers.get('Content-Length', '')}) + '\n')
        if self.path == '/logout-empty':
            self.wfile.write(b'HTTP/1.1 204 No Content\r\nCache-Control: no-store\r\nContent-Length: 0\r\nConnection: close\r\n\r\n')
            self.wfile.flush(); self.close_connection = True
        elif self.path == '/logout-body-length':
            self.wfile.write(b'HTTP/1.1 204 No Content\r\nCache-Control: no-store\r\nContent-Length: 3\r\nConnection: close\r\n\r\nbad')
            self.wfile.flush(); self.close_connection = True
        elif self.path == '/logout-body-unframed':
            self.wfile.write(b'HTTP/1.1 204 No Content\r\nCache-Control: no-store\r\nConnection: close\r\n\r\nbad')
            self.wfile.flush(); self.close_connection = True
        elif self.path == '/logout-no-store':
            self.wfile.write(b'HTTP/1.1 204 No Content\r\nContent-Length: 0\r\nConnection: close\r\n\r\n')
            self.wfile.flush(); self.close_connection = True
        elif self.path == '/logout-unauthorized':
            self.send_body(401, '{"code":"UNAUTHENTICATED"}', {'Content-Type': 'application/json', 'Cache-Control': 'no-store'})
        elif self.path == '/logout-200':
            self.send_body(200, '{"unexpected":true}', {'Content-Type': 'application/json', 'Cache-Control': 'no-store'})
        elif self.path == '/logout-slow':
            time.sleep(1.5)
            self.wfile.write(b'HTTP/1.1 204 No Content\r\nCache-Control: no-store\r\nContent-Length: 0\r\nConnection: close\r\n\r\n')
            try: self.wfile.flush()
            except (BrokenPipeError, ConnectionResetError): pass
            self.close_connection = True
        else:
            self.send_body(404, 'not-found')

tls_server = http.server.ThreadingHTTPServer(('127.0.0.1', 0), Handler)
tls_server.server_port = tls_server.server_address[1]
plain_server = http.server.ThreadingHTTPServer(('127.0.0.1', 0), Handler)
tls_server.http_port = plain_server.server_address[1]
context = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
context.load_cert_chain(os.environ['NERAE_TLS_CERT'], os.environ['NERAE_TLS_KEY'])
tls_server.socket = context.wrap_socket(tls_server.socket, server_side=True)
with open(os.environ['NERAE_TLS_PORT'], 'w', encoding='ascii') as f:
    f.write(str(tls_server.server_port))
with open(os.environ['NERAE_HTTP_PORT_FILE'], 'w', encoding='ascii') as f:
    f.write(str(plain_server.server_port))
threading.Thread(target=plain_server.serve_forever, daemon=True).start()
tls_server.serve_forever()
