#!/usr/bin/env python3
"""Local process smoke: real closed ports, no database substitute or production data."""
import argparse
import http.client
import json
import os
import select
import signal
import socket
import subprocess
import time

SECRETS = ('fixture-mysql-secret', 'fixture-redis-secret')


def safe_output(value):
    for secret in SECRETS:
        value = value.replace(secret, '[redacted]')
    return value


def expect(condition, message):
    if not condition:
        raise AssertionError(message)


def read_http(port, path):
    conn = http.client.HTTPConnection('127.0.0.1', port, timeout=3)
    try:
        conn.request('GET', path)
        response = conn.getresponse()
        return response.status, response.read().decode('utf-8')
    finally:
        conn.close()


def await_liveness(process, port):
    deadline = time.monotonic() + 10
    while time.monotonic() < deadline:
        if process.poll() is not None:
            raise AssertionError('api exited before liveness became available')
        try:
            status, body = read_http(port, '/health/live')
            if status == 200 and json.loads(body) == {'status': 'alive'}:
                return
        except (OSError, http.client.HTTPException):
            pass
        time.sleep(0.05)
    raise AssertionError('api liveness did not become available')


def stop_process(process, label):
    process.send_signal(signal.SIGTERM)
    stdout, stderr = process.communicate(timeout=8)
    expect(process.returncode == 0, label + ' did not stop cleanly: ' + safe_output(stderr))
    for secret in SECRETS:
        expect(secret not in stdout and secret not in stderr, label + ' logs leaked a test secret')
    return stderr


def await_worker(process):
    deadline = time.monotonic() + 10
    while time.monotonic() < deadline:
        if process.poll() is not None:
            raise AssertionError('worker exited before reporting startup')
        ready, _, _ = select.select([process.stderr], [], [], 0.1)
        if ready:
            line = process.stderr.readline()
            for secret in SECRETS:
                expect(secret not in line, 'worker startup log leaked a test secret')
            if 'without registered jobs' in line:
                return
    raise AssertionError('worker did not report startup before timeout')


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--api', required=True)
    parser.add_argument('--worker', required=True)
    args = parser.parse_args()

    # Bound sockets without listen() reserve known closed dependency ports.
    mysql_port = socket.socket()
    redis_port = socket.socket()
    api_port = socket.socket()
    for sock in (mysql_port, redis_port, api_port):
        sock.bind(('127.0.0.1', 0))
    mysql_number = mysql_port.getsockname()[1]
    redis_number = redis_port.getsockname()[1]
    api_number = api_port.getsockname()[1]
    api_port.close()

    env = {
        'PATH': os.environ.get('PATH', '/usr/bin:/bin'),
        'NERAE_ENVIRONMENT': 'test',
        'NERAE_HTTP_ADDR': f'127.0.0.1:{api_number}',
        'NERAE_MYSQL_DSN': f'fixture-user:fixture-mysql-secret@tcp(127.0.0.1:{mysql_number})/nerae_smoke?timeout=1s',
        'NERAE_REDIS_URL': f'redis://:fixture-redis-secret@127.0.0.1:{redis_number}/0',
        'NERAE_HEALTH_TIMEOUT': '1s',
        'NERAE_SHUTDOWN_TIMEOUT': '3s',
    }
    missing = dict(env)
    del missing['NERAE_MYSQL_DSN']
    result = subprocess.run([args.api], env=missing, capture_output=True, text=True, timeout=5)
    expect(result.returncode != 0 and 'NERAE_MYSQL_DSN' in result.stderr, 'missing configuration did not fail')
    for secret in SECRETS:
        expect(secret not in result.stderr, 'configuration error leaked a test secret')

    try:
        for iteration in range(2):
            api = subprocess.Popen([args.api], env=env, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
            try:
                await_liveness(api, api_number)
                status, body = read_http(api_number, '/health/ready')
                expect(status == 503 and json.loads(body) == {'status': 'unavailable'}, 'unavailable dependencies reported ready')
                for secret in SECRETS:
                    expect(secret not in body, 'readiness response leaked a test secret')
                stop_process(api, 'api')
            finally:
                if api.poll() is None:
                    api.kill()
                    api.communicate()
            print(f'api cycle {iteration + 1}: liveness 200, readiness 503, SIGTERM exit 0')

        worker = subprocess.Popen([args.worker], env=env, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
        try:
            await_worker(worker)
            stop_process(worker, 'worker')
        finally:
            if worker.poll() is None:
                worker.kill()
                worker.communicate()
        print('worker: no jobs registered, SIGTERM exit 0')
        print('missing MySQL configuration: nonzero exit, secret-free error')
    finally:
        mysql_port.close()
        redis_port.close()


if __name__ == '__main__':
    main()
