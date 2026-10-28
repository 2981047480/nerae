#!/usr/bin/env python3
"""Fault-injection test for HTTP shutdown; it does not emulate a healthy database."""
import argparse
import http.client
import json
import os
import queue
import signal
import socket
import subprocess
import threading
import time


def require(condition, message):
    if not condition:
        raise AssertionError(message)


def request(port, path):
    conn = http.client.HTTPConnection('127.0.0.1', port, timeout=8)
    try:
        conn.request('GET', path)
        response = conn.getresponse()
        return response.status, response.read().decode('utf-8')
    finally:
        conn.close()


def wait_live(process, port):
    deadline = time.monotonic() + 10
    while time.monotonic() < deadline:
        require(process.poll() is None, 'api exited before startup')
        try:
            if request(port, '/health/live')[0] == 200:
                return
        except (OSError, http.client.HTTPException):
            pass
        time.sleep(0.05)
    raise AssertionError('api startup timed out')


def run_case(api_path, force_close):
    dependency = socket.socket()
    dependency.bind(('127.0.0.1', 0))
    dependency.listen(1)
    dependency.settimeout(8)
    redis_closed = socket.socket()
    redis_closed.bind(('127.0.0.1', 0))
    api_addr = socket.socket()
    api_addr.bind(('127.0.0.1', 0))
    api_port = api_addr.getsockname()[1]
    api_addr.close()
    accepted = threading.Event()
    release = threading.Event()
    events = queue.Queue()

    def stalled_dependency():
        try:
            conn, _ = dependency.accept()
            with conn:
                accepted.set()
                release.wait(8)
        except OSError as exc:
            events.put(('dependency-error', type(exc).__name__))

    thread = threading.Thread(target=stalled_dependency, daemon=True)
    thread.start()
    env = {
        'PATH': os.environ.get('PATH', '/usr/bin:/bin'),
        'NERAE_ENVIRONMENT': 'test',
        'NERAE_HTTP_ADDR': f'127.0.0.1:{api_port}',
        'NERAE_MYSQL_DSN': f'fixture-user:fixture-secret@tcp(127.0.0.1:{dependency.getsockname()[1]})/nerae_smoke',
        'NERAE_REDIS_URL': f'redis://127.0.0.1:{redis_closed.getsockname()[1]}/0',
        'NERAE_HEALTH_TIMEOUT': '4s' if force_close else '1s',
        'NERAE_SHUTDOWN_TIMEOUT': '1s' if force_close else '3s',
    }
    process = subprocess.Popen([api_path], env=env, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
    request_thread = None
    try:
        wait_live(process, api_port)

        def inflight_request():
            try:
                events.put(('response', request(api_port, '/health/ready')))
            except (OSError, http.client.HTTPException) as exc:
                events.put(('client-closed', type(exc).__name__))

        request_thread = threading.Thread(target=inflight_request, daemon=True)
        request_thread.start()
        require(accepted.wait(5), 'health request did not reach stalled dependency')
        process.send_signal(signal.SIGTERM)
        _, stderr = process.communicate(timeout=8)
        require('fixture-secret' not in stderr, 'logs leaked test credential')
        request_thread.join(timeout=3)
        require(not request_thread.is_alive(), 'in-flight client request did not terminate')
        observed = events.get(timeout=2)
        if force_close:
            require(process.returncode != 0 and 'HTTP shutdown failed' in stderr, 'shutdown deadline was not reported')
            require(observed[0] == 'client-closed', 'deadline did not close the in-flight HTTP connection')
            print('shutdown deadline: in-flight connection closed, process reported failure')
        else:
            require(process.returncode == 0, 'graceful shutdown did not exit successfully')
            require(observed[0] == 'response' and observed[1][0] == 503, 'in-flight health request was not drained')
            require(json.loads(observed[1][1]) == {'status': 'unavailable'}, 'unexpected readiness response')
            print('graceful shutdown: in-flight request completed before exit')
    finally:
        if process.poll() is None:
            process.kill()
            process.communicate()
        release.set()
        dependency.close()
        redis_closed.close()
        thread.join(timeout=2)
        if request_thread is not None:
            request_thread.join(timeout=2)


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--api', required=True)
    args = parser.parse_args()
    run_case(args.api, False)
    run_case(args.api, True)


if __name__ == '__main__':
    main()
