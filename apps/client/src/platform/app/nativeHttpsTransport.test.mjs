import assert from 'node:assert/strict'
import test from 'node:test'
import { AuthApiError } from '../../api/auth/emailAuthApi.ts'
import { RecordingApiError } from '../../features/training/recordingApi.ts'
import { createAppNativeHttpsPorts } from './nativeHttpsTransport.ts'

const ok = (requestId = 'req-1', status = 200, body = '{"ok":true}', headersJson = '{"content-type":"application/json","cache-control":"no-store"}') =>
  ({ requestId, status, headersJson, body })
const make = (nativeRequest, base = 'https://api.example.test/v1', cancel = () => {}) =>
  createAppNativeHttpsPorts(base, nativeRequest, cancel, 1000)
const errorCode = code => error => error instanceof AuthApiError || error instanceof RecordingApiError ? error.code === code : false

test('auth transport uses explicit configured HTTPS base and returns parsed native wire response', async () => {
  let sent
  const ports = make((request, callbacks) => { sent = request; callbacks.success(ok()); return 'req-1' })
  const body = '{ "email" : "user@example.test", "nested": {"a":1} }'
  const response = await ports.authTransport({
    url: 'https://api.example.test/v1/auth/email/challenges', method: 'POST',
    headers: { 'Content-Type': 'application/json', 'Idempotency-Key': 'caller-key-1234567890' }, body,
  })
  assert.equal(sent.baseUrl, 'https://api.example.test/v1')
  assert.equal(sent.path, '/auth/email/challenges')
  assert.equal(sent.body, body)
  assert.equal(sent.method, 'POST')
  assert.equal(JSON.parse(sent.headersJson)['Idempotency-Key'], 'caller-key-1234567890')
  assert.deepEqual(response.body, { ok: true })
  assert.equal(response.status, 200)
  assert.equal(response.headers['cache-control'], 'no-store')
})

test('recording request preserves raw body, caller idempotency key and method', async () => {
  let sent
  const ports = make((request, callbacks) => { sent = request; callbacks.success(ok('req-2', 201, '{"accepted":true}')); return 'req-2' })
  const body = '{"expectedVersion":4, "sets": [ {"rpe":7.5} ]}'
  const response = await ports.recordingRequest({
    url: 'https://api.example.test/v1/me/sessions/101/sets', method: 'POST',
    header: { Authorization: 'Bearer test-token', 'Idempotency-Key': 'caller-key-abcdef12', 'Content-Type': 'application/json' }, data: body,
  })
  assert.equal(sent.body, body)
  assert.equal(JSON.parse(sent.headersJson)['Idempotency-Key'], 'caller-key-abcdef12')
  assert.equal(sent.method, 'POST')
  assert.deepEqual(response, { statusCode: 201, data: { accepted: true } })
})

test('recording GET supports exact base path and query and carries no body', async () => {
  let sent
  const ports = make((request, callbacks) => { sent = request; callbacks.success(ok('req-3')); return 'req-3' })
  const response = await ports.recordingRequest({
    url: 'https://api.example.test/v1/me/sessions?page=2&pageSize=20&from=2026-01-01', method: 'GET',
    header: { Authorization: 'Bearer token', Accept: 'application/json' },
  })
  assert.equal(sent.path, '/me/sessions?page=2&pageSize=20&from=2026-01-01')
  assert.equal(sent.body, '')
  assert.deepEqual(response.data, { ok: true })
})

test('current-user port is a fixed no-store GET and preserves response headers for verification', async () => {
  let sent
  let calls = 0
  const ports = make((request, callbacks) => {
    calls++
    sent = request
    callbacks.success(ok('me-1', 200,
      '{"id":"42","nickname":"member","roles":["student"]}',
      '{"content-type":"application/json","cache-control":"no-store"}'))
    return 'me-1'
  })
  const token = 'synthetic.me.token'
  const response = await ports.readCurrentUser(token)
  assert.equal(calls, 1)
  assert.equal(sent.baseUrl, 'https://api.example.test/v1')
  assert.equal(sent.path, '/me')
  assert.equal(sent.method, 'GET')
  assert.equal(sent.body, '')
  assert.deepEqual(JSON.parse(sent.headersJson), {
    Accept: 'application/json', Authorization: `Bearer ${token}`, 'Cache-Control': 'no-store',
  })
  assert.equal(response.status, 200)
  assert.equal(response.headers['cache-control'], 'no-store')
  assert.deepEqual(response.body, { id: '42', nickname: 'member', roles: ['student'] })
})

test('current-user port rejects malformed credentials before native access and exposes fixed errors', async () => {
  let calls = 0
  const ports = make(() => { calls++; return 'unexpected' })
  for (const token of ['', 'secret\r\nX-Header: injected']) {
    await assert.rejects(ports.readCurrentUser(token), error => error instanceof AuthApiError &&
      error.code === 'invalid_request' && !String(error).includes('secret'))
  }
  assert.equal(calls, 0)
})

test('current-user port returns redirects as responses without following them', async () => {
  let calls = 0
  const ports = make((request, callbacks) => {
    calls++
    callbacks.success(ok('redirect-1', 302, '{"location":"https://evil.example"}'))
    return 'redirect-1'
  })
  const response = await ports.readCurrentUser('synthetic.token')
  assert.equal(response.status, 302)
  assert.equal(calls, 1)
})

test('rejects cross-origin, downgrade, base-prefix lookalike, fragment and normalized path escape before native call', async () => {
  let calls = 0
  const ports = make((_request, _callbacks) => { calls++; return 'req' })
  for (const url of [
    'http://api.example.test/v1/auth/email/challenges',
    'https://evil.example/v1/auth/email/challenges',
    'https://api.example.test/v10/auth/email/challenges',
    'https://api.example.test/v1/../private',
    'https://api.example.test/v1/%2f..%2fprivate',
    'https://api.example.test/v1/auth/email/challenges#frag',
  ]) {
    await assert.rejects(ports.authTransport({ url, method: 'POST', headers: {}, body: '{}' }), errorCode('invalid_request'))
  }
  await assert.rejects(Promise.resolve().then(() => make(() => 'x', 'http://api.example.test')),
    errorCode('invalid_configuration'))
  assert.equal(calls, 0)
})

test('synchronous callback and duplicate callbacks settle exactly once', async () => {
  let nativeSuccess = 0
  let nativeFail = 0
  const ports = make((_request, callbacks) => {
    callbacks.success(ok())
    callbacks.fail({ requestId: 'req-4', code: 'network' })
    callbacks.success(ok('req-4', 500, '{"late":true}'))
    return 'req-1'
  })
  const success = ports.authTransport({ url: 'https://api.example.test/v1/auth/email-login', method: 'POST', headers: {}, body: '{}' })
    .then(value => { nativeSuccess++; return value }, error => { nativeFail++; throw error })
  const result = await success
  assert.equal(result.status, 200)
  assert.equal(nativeSuccess, 1)
  assert.equal(nativeFail, 0)
})

test('a synchronous native throw is sanitized even if a callback was staged first', async () => {
  const ports = make((_request, callbacks) => {
    callbacks.success(ok())
    throw new Error('secret@example.test private native details')
  })
  await assert.rejects(ports.authTransport({
    url: 'https://api.example.test/v1/auth/email-login', method: 'POST', headers: {}, body: '{}',
  }), error => error instanceof AuthApiError && error.code === 'network' && !error.message.includes('secret@example.test'))
})

test('native failures map to fixed errors without exposing callback fields', async () => {
  const ports = make((_request, callbacks) => {
    callbacks.fail({ requestId: 'req-1', code: 'timeout' })
    return 'req-1'
  })
  await assert.rejects(ports.authTransport({
    url: 'https://api.example.test/v1/auth/email-login', method: 'POST', headers: {}, body: '{}',
  }), error => error instanceof AuthApiError && error.code === 'network' && error.message === 'network' && !error.message.includes('sensitive'))
})

test('synchronous native failure callback is fixed, one-shot and does not retry', async () => {
  let calls = 0
  const ports = make((_request, callbacks) => {
    calls++
    callbacks.fail({ requestId: 'req-sync', code: 'network' })
    callbacks.fail({ requestId: 'req-sync', code: 'timeout' })
    return 'req-sync'
  })
  await assert.rejects(ports.authTransport({
    url: 'https://api.example.test/v1/auth/email-login', method: 'POST', headers: {}, body: '{}',
  }), error => error instanceof AuthApiError && error.code === 'network')
  assert.equal(calls, 1)
})

test('missing native callback is bounded by the watchdog and cancels once without retry', async () => {
  let calls = 0
  const cancelled = []
  const ports = make(() => { calls++; return 'req-hung' }, undefined, id => cancelled.push(id))
  await assert.rejects(ports.recordingRequest({
    url: 'https://api.example.test/v1/me/sessions', method: 'GET', header: {},
  }), error => error instanceof RecordingApiError && error.code === 'network')
  assert.equal(calls, 1)
  assert.deepEqual(cancelled, ['req-hung'])
})

test('untrusted fields on a native error callback are rejected without copying their contents', async () => {
  const ports = make((_request, callbacks) => {
    callbacks.fail({ requestId: 'req-1', code: 'timeout', detail: 'secret@example.test raw body' })
    return 'req-1'
  })
  await assert.rejects(ports.authTransport({
    url: 'https://api.example.test/v1/auth/email-login', method: 'POST', headers: {}, body: '{}',
  }), error => error instanceof AuthApiError && error.code === 'invalid_response' &&
    error.message === 'invalid_response' && !error.message.includes('secret@example.test'))
})

test('empty-handle unsupported platform callback remains a sanitized unavailable error', async () => {
  const ports = make((_request, callbacks) => {
    callbacks.fail({ requestId: '', code: 'unsupported_platform' })
    return ''
  })
  await assert.rejects(ports.recordingRequest({ url: 'https://api.example.test/v1/me/sessions', method: 'GET', header: {} }),
    error => error instanceof RecordingApiError && error.code === 'unavailable')
})

test('malformed, duplicate, escaped-duplicate, deep, multi-value and non-object response JSON is rejected', async () => {
  const bodies = [
    '{"a":1,"a":2}',
    '{"nested":{"a":1,"\\u0061":2}}',
    '{"a":1} {"b":2}',
    '[{"a":1}]',
    '{"a":1e999}',
    '{"a":"\\uD800"}',
    `${'{"a":'.repeat(66)}0${'}'.repeat(66)}`,
  ]
  for (const body of bodies) {
    const ports = make((_request, callbacks) => { callbacks.success(ok('req-1', 200, body)); return 'req-1' })
    await assert.rejects(ports.authTransport({ url: 'https://api.example.test/v1/auth/email-login', method: 'POST', headers: {}, body: '{}' }),
      error => error instanceof AuthApiError && error.code === 'invalid_response')
  }
})

test('malformed, duplicate and case-colliding headersJson is rejected', async () => {
  for (const headersJson of [
    '["cache-control"]',
    '{"cache-control":"no-store","cache-control":"public"}',
    '{"cache-control":"no-store","Cache-Control":"public"}',
    '{"cache-control":1}',
    JSON.stringify({ 'content-type': '€'.repeat(3000) }),
    '{"set-cookie":"secret"}',
    '{"cache-control":"no-store"} garbage',
  ]) {
    const ports = make((request, callbacks) => { callbacks.success(ok('req-1', 200, '{"ok":true}', headersJson)); return 'req-1' })
    await assert.rejects(ports.authTransport({ url: 'https://api.example.test/v1/auth/email-login', method: 'POST', headers: {}, body: '{}' }),
      error => error instanceof AuthApiError && error.code === 'invalid_response')
  }
})

test('oversized native body is rejected and non-success HTTP status stays non-success', async () => {
  const oversized = make((request, callbacks) => { callbacks.success(ok('req-1', 200, `{"x":"${'a'.repeat(4 * 1024 * 1024)}"}`)); return 'req-1' })
  await assert.rejects(oversized.recordingRequest({ url: 'https://api.example.test/v1/me/sessions', method: 'GET', header: {} }),
    error => error instanceof RecordingApiError && error.code === 'invalid_response')
  const unauthorized = make((request, callbacks) => { callbacks.success(ok('req-1', 401, '{"code":"unauthorized"}')); return 'req-1' })
  const response = await unauthorized.authTransport({ url: 'https://api.example.test/v1/auth/email-login', method: 'POST', headers: {}, body: '{}' })
  assert.equal(response.status, 401)
  assert.deepEqual(response.body, { code: 'unauthorized' })
})

test('request body limit counts UTF-8 bytes without TextEncoder', async () => {
  const exactLimit = '💥'.repeat(1_048_576) // 4 MiB in UTF-8
  let calls = 0
  let captured
  const ports = make((request, callbacks) => {
    calls++
    captured = request.body
    callbacks.fail({ requestId: 'req-bytes', code: 'network' })
    return 'req-bytes'
  })
  await assert.rejects(ports.authTransport({
    url: 'https://api.example.test/v1/auth/email-login', method: 'POST', headers: {}, body: exactLimit,
  }), error => error instanceof AuthApiError && error.code === 'network')
  assert.equal(captured, exactLimit)
  assert.equal(calls, 1)
  await assert.rejects(ports.authTransport({
    url: 'https://api.example.test/v1/auth/email-login', method: 'POST', headers: {}, body: `${exactLimit}💥`,
  }), error => error instanceof AuthApiError && error.code === 'invalid_request')
  assert.equal(calls, 1)
})

test('native invocation occurs exactly once and does not synthesize an idempotency key', async () => {
  let calls = 0
  let sent
  const ports = make((request, callbacks) => { calls++; sent = request; callbacks.fail({ requestId: 'req-1', code: 'network' }); return 'req-1' })
  await assert.rejects(ports.authTransport({ url: 'https://api.example.test/v1/auth/email-login', method: 'POST', headers: {}, body: '{}' }),
    error => error instanceof AuthApiError && error.code === 'network')
  assert.equal(calls, 1)
  assert.equal(Object.hasOwn(JSON.parse(sent.headersJson), 'Idempotency-Key'), false)
})
