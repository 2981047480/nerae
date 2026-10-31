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
revoke = run('revoke')
assert revoke['ok'] and revoke['status'] == 204 and revoke['body'] == ''
assert json.loads(revoke['headersJson'])['cache-control'] == 'no-store'
assert run('revokeBody')['code'] == 'invalid_request'
assert run('revokeBodyLength')['code'] == 'invalid_response'
assert run('revokeContentLengthZero')['code'] == 'invalid_response'
assert run('revokeTransferEncoding')['code'] == 'invalid_response'
unframed_204 = run('revokeBodyUnframed')
print('iOS URLSession unframed 204 probe:', {key: unframed_204[key] for key in ('ok', 'status', 'code')})
assert run('revokeMissingNoStore')['ok'] and run('revokeMissingNoStore')['status'] == 204
valid_unauthorized = run('revokeUnauthorized')
valid_request_id = '0123456789abcdef0123456789abcdef'
assert valid_unauthorized['status'] == 401
assert json.loads(valid_unauthorized['body']) == {
    'code': 'UNAUTHENTICATED', 'message': '请先登录', 'requestId': valid_request_id
}
assert json.loads(valid_unauthorized['headersJson'])['x-request-id'] == valid_request_id
malformed_unauthorized = run('revokeUnauthorizedMalformed')
assert malformed_unauthorized['status'] == 401
assert json.loads(malformed_unauthorized['body'])['requestId'] == valid_request_id
assert json.loads(malformed_unauthorized['headersJson'])['x-request-id'] == 'f' * 32
assert run('revokeNon204')['status'] == 200
before_cancel = len(open(hits, encoding='utf-8').read().splitlines())
assert run('revokeCancel', timeout=5000)['code'] == 'cancelled'
time.sleep(1.7)
delete_hits = [json.loads(line) for line in open(hits, encoding='utf-8').read().splitlines()[before_cancel:]]
assert any(hit['method'] == 'DELETE' and hit['path'] == '/logout-slow' and hit['authorizationPresent'] and hit['contentLength'] in ('', '0') for hit in delete_hits)
run('setCookie')
assert run('checkCookie')['body'] == 'absent'
assert run('inspect')['ok']
print('PASS: TLS trust/hostname failures, redirect refusal, bounded bodies, timeout/cancel, cache/cookie policy, and DELETE empty-204 framing; callbacks settled exactly once.')
