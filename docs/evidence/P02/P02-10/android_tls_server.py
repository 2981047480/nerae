#!/usr/bin/env python3
import json
import os
import ssl
import threading
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from urllib.parse import urlsplit

MAX_BODY = 4 * 1024 * 1024
HITS = os.environ["NERAE_ANDROID_HITS"]
PORTS_FILE = os.environ["NERAE_ANDROID_PORTS"]
HITS_LOCK = threading.Lock()
PORTS = {}


class ReusableServer(ThreadingHTTPServer):
    allow_reuse_address = True
    daemon_threads = True


class Handler(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def log_message(self, _format, *_args):
        return

    def log_hit(self):
        route = urlsplit(self.path).path
        entry = {"server": self.server.tag, "path": route, "cookiePresent": self.headers.get("Cookie") is not None,
                 "method": self.command,
                 "authorizationPresent": self.headers.get("Authorization") == "Bearer synthetic-probe",
                 "contentLength": self.headers.get("Content-Length", "")}
        with HITS_LOCK:
            with open(HITS, "a", encoding="utf-8") as output:
                output.write(json.dumps(entry, separators=(",", ":")) + "\n")
                output.flush()
        return route

    def do_GET(self):
        self.handle_request()

    def do_POST(self):
        self.handle_request()

    def do_DELETE(self):
        self.handle_request()

    def send_bytes(self, status, body=b"", headers=None, close=False):
        self.send_response(status)
        for name, value in (headers or {}).items():
            self.send_header(name, value)
        self.send_header("Content-Length", str(len(body)))
        if close:
            self.send_header("Connection", "close")
            self.close_connection = True
        self.end_headers()
        if body:
            try:
                self.wfile.write(body)
                self.wfile.flush()
            except (BrokenPipeError, ConnectionResetError):
                pass

    def raw(self, value):
        self.close_connection = True
        try:
            self.wfile.write(value)
            self.wfile.flush()
        except (BrokenPipeError, ConnectionResetError):
            pass

    def stream_fixed(self, total, content_length=True):
        self.send_response(200)
        if content_length:
            self.send_header("Content-Length", str(total))
        self.send_header("Connection", "close")
        self.end_headers()
        self.close_connection = True
        remaining = total
        chunk = b"x" * 32768
        while remaining > 0:
            size = min(remaining, len(chunk))
            try:
                self.wfile.write(chunk[:size])
                self.wfile.flush()
            except (BrokenPipeError, ConnectionResetError):
                return
            remaining -= size

    def stream_chunked(self, total):
        self.send_response(200)
        self.send_header("Transfer-Encoding", "chunked")
        self.send_header("Connection", "close")
        self.end_headers()
        self.close_connection = True
        remaining = total
        chunk = b"z" * (1024 * 1024)
        while remaining > 0:
            piece = chunk[:min(remaining, len(chunk))]
            try:
                self.wfile.write((format(len(piece), "x") + "\r\n").encode("ascii"))
                self.wfile.write(piece)
                self.wfile.write(b"\r\n")
                self.wfile.flush()
            except (BrokenPipeError, ConnectionResetError):
                return
            remaining -= len(piece)
        try:
            self.wfile.write(b"0\r\n\r\n")
            self.wfile.flush()
        except (BrokenPipeError, ConnectionResetError):
            pass

    def handle_request(self):
        route = self.log_hit()
        if self.command == "POST":
            try:
                body_length = int(self.headers.get("Content-Length", "0"))
                body = self.rfile.read(body_length)
            except Exception:
                self.send_bytes(400, b"bad request", close=True)
                return
            if len(body) != body_length:
                self.send_bytes(400, b"truncated request", close=True)
                return

        if route == "/success":
            self.send_bytes(200, b'{"ok":true}', {"Content-Type": "application/json", "X-Request-ID": "synthetic"})
        elif route == "/inspect":
            body = json.dumps({
                "acceptEncoding": self.headers.get("Accept-Encoding", ""),
                "cacheControl": self.headers.get("Cache-Control", ""),
                "authorizationPresent": self.headers.get("Authorization") == "Bearer synthetic-probe",
                "cookiePresent": self.headers.get("Cookie") is not None,
                "connection": self.headers.get("Connection", ""),
            }, separators=(",", ":")).encode("ascii")
            self.send_bytes(200, body, {"Content-Type": "application/json"})
        elif route == "/set-cookie":
            self.send_bytes(200, b"set", {"Set-Cookie": "probe=synthetic; Path=/; Secure"})
        elif route == "/check-cookie":
            self.send_bytes(200, b"present" if self.headers.get("Cookie") else b"absent")
        elif route in ("/redirect-301", "/redirect-302", "/redirect-cross", "/redirect-downgrade"):
            status = 301 if route == "/redirect-301" else 302
            if route in ("/redirect-301", "/redirect-302"):
                location = "/success"
            elif route == "/redirect-cross":
                location = "https://127.0.0.1:%d/target" % PORTS["alternate"]
            else:
                location = "http://127.0.0.1:%d/target" % PORTS["http"]
            self.send_bytes(status, b"", {"Location": location})
        elif route == "/target":
            self.send_bytes(200, b"redirect target reached")
        elif route == "/large-fixed":
            self.stream_fixed(MAX_BODY + 1, content_length=True)
        elif route == "/exact-response":
            self.stream_fixed(MAX_BODY, content_length=True)
        elif route == "/large-close":
            self.stream_fixed(MAX_BODY + 1, content_length=False)
        elif route == "/close-exact":
            self.stream_fixed(MAX_BODY, content_length=False)
        elif route == "/large-chunked":
            self.stream_chunked(MAX_BODY + 1)
        elif route == "/chunked-good":
            body = b"chunked-ok"
            self.send_response(200)
            self.send_header("Transfer-Encoding", "chunked")
            self.send_header("Connection", "close")
            self.end_headers()
            self.close_connection = True
            self.wfile.write((format(len(body), "x") + "\r\n").encode("ascii") + body + b"\r\n0\r\n\r\n")
            self.wfile.flush()
        elif route == "/close-good":
            self.send_response(200)
            self.send_header("Connection", "close")
            self.end_headers()
            self.close_connection = True
            try:
                self.wfile.write(b"close-ok")
                self.wfile.flush()
            except (BrokenPipeError, ConnectionResetError):
                pass
        elif route == "/post-size":
            self.send_bytes(200, str(len(body)).encode("ascii"), {"Content-Type": "text/plain"})
        elif route == "/logout-empty":
            self.raw(b"HTTP/1.1 204 No Content\r\nCache-Control: no-store\r\nContent-Length: 0\r\nConnection: close\r\n\r\n")
        elif route == "/logout-body-length":
            self.raw(b"HTTP/1.1 204 No Content\r\nCache-Control: no-store\r\nContent-Length: 3\r\nConnection: close\r\n\r\nbad")
        elif route == "/logout-body-unframed":
            self.raw(b"HTTP/1.1 204 No Content\r\nCache-Control: no-store\r\nConnection: close\r\n\r\nbad")
        elif route == "/logout-no-store":
            self.raw(b"HTTP/1.1 204 No Content\r\nContent-Length: 0\r\nConnection: close\r\n\r\n")
        elif route == "/logout-unauthorized":
            self.send_bytes(401, b'{"code":"UNAUTHENTICATED"}', {"Content-Type": "application/json", "Cache-Control": "no-store"}, close=True)
        elif route == "/logout-200":
            self.send_bytes(200, b'{"unexpected":true}', {"Content-Type": "application/json", "Cache-Control": "no-store"}, close=True)
        elif route == "/logout-slow":
            time.sleep(1.5)
            self.raw(b"HTTP/1.1 204 No Content\r\nCache-Control: no-store\r\nContent-Length: 0\r\nConnection: close\r\n\r\n")
        elif route == "/slow":
            time.sleep(2.2)
            self.send_bytes(200, b"slow", close=True)
        elif route == "/bad-status":
            self.raw(b"NOT-HTTP\r\nContent-Length: 0\r\n\r\n")
        elif route == "/long-header":
            self.raw(b"HTTP/1.1 200 OK\r\nX-Long: " + b"a" * 9000 + b"\r\nContent-Length: 0\r\n\r\n")
        elif route == "/folded-header":
            self.raw(b"HTTP/1.1 200 OK\r\nX-Test: one\r\n two\r\nContent-Length: 0\r\n\r\n")
        elif route == "/duplicate-cl":
            self.raw(b"HTTP/1.1 200 OK\r\nContent-Length: 1\r\nContent-Length: 1\r\n\r\nx")
        elif route == "/conflicting-cl":
            self.raw(b"HTTP/1.1 200 OK\r\nContent-Length: 1\r\nContent-Length: 2\r\n\r\nx")
        elif route == "/te-cl":
            self.raw(b"HTTP/1.1 200 OK\r\nTransfer-Encoding: chunked\r\nContent-Length: 1\r\n\r\n0\r\n\r\n")
        elif route == "/bad-chunk":
            self.raw(b"HTTP/1.1 200 OK\r\nTransfer-Encoding: chunked\r\n\r\nZ\r\n")
        elif route == "/chunk-hex-overflow":
            self.raw(b"HTTP/1.1 200 OK\r\nTransfer-Encoding: chunked\r\n\r\nFFFFFFFFFFFFFFFFF\r\n")
        elif route == "/chunk-over-limit":
            self.raw(b"HTTP/1.1 200 OK\r\nTransfer-Encoding: chunked\r\n\r\n400001\r\n")
        elif route == "/truncated-length":
            self.raw(b"HTTP/1.1 200 OK\r\nContent-Length: 20\r\n\r\nshort")
        elif route == "/truncated-chunk":
            self.raw(b"HTTP/1.1 200 OK\r\nTransfer-Encoding: chunked\r\n\r\n9\r\nshort")
        elif route == "/bad-encoding":
            self.raw(b"HTTP/1.1 200 OK\r\nContent-Encoding: gzip\r\nContent-Length: 4\r\n\r\ntest")
        elif route == "/unknown-transfer":
            self.raw(b"HTTP/1.1 200 OK\r\nTransfer-Encoding: gzip\r\n\r\n")
        else:
            self.send_bytes(404, b"not found")


def start_server(tag, use_tls, cert=None, key=None):
    server = ReusableServer(("127.0.0.1", 0), Handler)
    server.tag = tag
    if use_tls:
        context = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
        context.load_cert_chain(certfile=cert, keyfile=key)
        server.socket = context.wrap_socket(server.socket, server_side=True)
    thread = threading.Thread(target=server.serve_forever, name="nerae-android-test-" + tag, daemon=True)
    thread.start()
    return server, thread


good_cert = os.environ["NERAE_ANDROID_GOOD_CERT"]
good_key = os.environ["NERAE_ANDROID_GOOD_KEY"]
bad_cert = os.environ["NERAE_ANDROID_BAD_CERT"]
bad_key = os.environ["NERAE_ANDROID_BAD_KEY"]

primary, _ = start_server("primary", True, good_cert, good_key)
alternate, _ = start_server("alternate", True, good_cert, good_key)
bad, _ = start_server("bad", True, bad_cert, bad_key)
plain, _ = start_server("http", False)
PORTS.update(primary=primary.server_port, alternate=alternate.server_port, bad=bad.server_port, http=plain.server_port)
with open(PORTS_FILE, "w", encoding="utf-8") as output:
    json.dump(PORTS, output, sort_keys=True)

try:
    threading.Event().wait()
finally:
    for server in (primary, alternate, bad, plain):
        server.shutdown()
