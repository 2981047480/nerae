#!/usr/bin/env python3
import json
import os
import subprocess
import sys
import time

probe, port, good_ca, bad_ca, hits, http_port = sys.argv[1:]
base = 'https://127.0.0.1:' + port

def run(mode, url=base, ca=good_ca, timeout=1000):
    p = subprocess.run([probe, mode, url, ca, str(timeout)], text=True, capture_output=True, timeout=12)
    if p.returncode != 0:
        raise AssertionError(f'{mode} exit={p.returncode}; stderr={p.stderr}')
    return json.loads(p.stdout)['result']

def hitlist():
    try:
        return open(hits, encoding='utf-8').read().splitlines()
    except FileNotFoundError:
        return []

ok = run('success')
assert ok['ok'] and ok['status'] == 200 and json.loads(ok['body']) == {'ok': True}
required_wire_fields = {'ok', 'requestId', 'status', 'headersJson', 'body', 'code'}
assert set(ok) == required_wire_fields and ok['code'] == ''
assert isinstance(json.loads(ok['headersJson']), dict)
wire_failure = run('invalidHeader')
assert not wire_failure['ok'] and set(wire_failure) == required_wire_fields
assert wire_failure['status'] == 0 and wire_failure['headersJson'] == '{}' and wire_failure['body'] == ''
assert wire_failure['code'] == 'invalid_request'
before_cache = len(hitlist())
inspect = json.loads(run('inspect')['body'])
assert inspect == {'cacheControl': 'no-store', 'authorizationPresent': True, 'cookiePresent': False}
run('inspect')
assert len(hitlist()) == before_cache + 2
untrusted = run('success', ca=bad_ca)
assert untrusted.get('code') == 'network', untrusted
assert run('success', url='http://localhost:' + port)['code'] == 'invalid_configuration'
assert run('escape')['code'] == 'invalid_request'
for mode in ('spacePath', 'unicodePath', 'spaceQuery', 'unicodeQuery', 'badPercent'):
    assert run(mode).get('code') == 'invalid_request', mode
encoded = run('encodedQuery')
assert encoded['ok'] and encoded['status'] == 200
assert run('invalidHeader')['code'] == 'invalid_request'
wrong_host = run('success', url='https://localhost:' + port, ca=good_ca)
assert wrong_host.get('code') == 'network', wrong_host
for mode, status in [('redirect301', 301), ('redirect302', 302), ('crossOrigin', 302), ('downgrade', 302)]:
    before = len(hitlist())
    result = run(mode)
    assert result['ok'] and result['status'] == status, (mode, result)
    after = hitlist()[before:]
    assert len(after) == 1 and after[0].startswith('/redirect-'), (mode, after)
assert run('largeFixed')['code'] == 'response_too_large'
assert run('largeChunked')['code'] == 'response_too_large'
assert run('requestTooLarge')['code'] == 'invalid_request'
assert run('requestAtLimit')['status'] == 200
assert run('timeout', timeout=1000)['code'] == 'timeout'
assert run('cancel', timeout=5000)['code'] == 'cancelled'
run('setCookie')
assert run('checkCookie')['body'] == 'absent'
assert run('inspect')['ok']
print('PASS: TLS trust/hostname failures, redirect refusal, bounded bodies, timeout/cancel, cache and cookie policy; each callback count was exactly one.')
