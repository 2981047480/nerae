import assert from 'node:assert/strict'
import { test } from 'node:test'
import { RecordingApi, RecordingApiError, createUniRequestPort } from './recordingApi.ts'

const credential = { subjectId: '101', generation: 'login-1', accessToken: 'real-session-token' }
const auth = { current: () => credential, isCurrent: (id, generation) => id === '101' && generation === 'login-1' }
const set = {
  recordKey: 'record-001', exerciseEntryKey: 'entry-0001', exerciseName: '深蹲',
  resolvedExerciseName: '深蹲', setIndex: 1, kind: 'weight', weightKg: 50, reps: 8,
}
const detail = overrides => ({
  id: '42', date: '2026-10-29', timeZone: 'Asia/Shanghai', status: 'in_progress',
  source: 'free', version: 3, createdAt: '2026-10-29T08:00:00Z',
  updatedAt: '2026-10-29T08:00:00Z', recordedAt: null, sets: [], ...overrides,
})
const apiError = code => error => error instanceof RecordingApiError && error.code === code

test('free-session creation sends the actual input with authentication and a caller-owned idempotency key', async () => {
  const requests = []
  const api = new RecordingApi('https://api.example.org/v1/', auth, async request => {
    requests.push(request)
    return { statusCode: 201, data: detail() }
  })
  const input = { date: '2026-10-29', timeZone: 'Asia/Shanghai', clientEventId: 'event-0001' }
  assert.equal((await api.createFreeSession(input, 'create-0001')).id, '42')
  assert.equal(requests[0].url, 'https://api.example.org/v1/me/sessions')
  assert.equal(requests[0].header.Authorization, 'Bearer real-session-token')
  assert.equal(requests[0].header['Idempotency-Key'], 'create-0001')
  assert.deepEqual(JSON.parse(requests[0].data), { source: 'free', ...input })
  assert.ok(!('subjectId' in JSON.parse(requests[0].data)))
})

test('saving and rereading use the same resource, version, keys and caller-owned replay token', async () => {
  const requests = []
  const api = new RecordingApi('https://api.example.org/v1', auth, async request => {
    requests.push(request)
    return { statusCode: 200, data: detail({ sets: [set] }) }
  })
  const { resolvedExerciseName, ...fields } = set
  const write = { ...fields, completed: true }
  await api.saveSets('42', 3, [write], 'update-0001')
  await api.saveSets('42', 3, [write], 'update-0001')
  await api.readSession('42')
  assert.equal(requests[0].url, 'https://api.example.org/v1/me/sessions/42/sets')
  assert.equal(requests[2].url, 'https://api.example.org/v1/me/sessions/42')
  assert.equal(requests[0].data, requests[1].data)
  assert.equal(requests[0].header['Idempotency-Key'], requests[1].header['Idempotency-Key'])
  assert.deepEqual(JSON.parse(requests[0].data), { expectedVersion: 3, sets: [write] })
  assert.equal(requests[2].header['Idempotency-Key'], undefined)
})

test('version conflict and network errors are explicit and do not trigger a blind retry', async () => {
  let requests = 0
  const conflict = new RecordingApi('https://api.example.org', auth, async () => {
    requests++
    return { statusCode: 412, data: { message: 'server-private-details' } }
  })
  await assert.rejects(conflict.saveSets('42', 3, [{ ...set, completed: true }], 'update-0001'), apiError('version_conflict'))
  assert.equal(requests, 1)
  const offline = new RecordingApi('https://api.example.org', auth, async () => { throw new Error('token in low-level error') })
  await assert.rejects(offline.readSession('42'), error => apiError('network')(error) && !error.message.includes('token'))
})

test('finish preserves explicit nulls but omits unrelated fields', async () => {
  let captured
  const api = new RecordingApi('https://api.example.org/v1', auth, async request => {
    captured = request
    return { statusCode: 200, data: detail({ status: 'completed', recordedAt: '2026-10-29T08:00:01Z', sets: [set] }) }
  })
  await api.finishSession('42', { expectedVersion: 3, durationMin: null, note: '完成', ignored: 'do not send' }, 'finish-0001')
  assert.deepEqual(JSON.parse(captured.data), { expectedVersion: 3, durationMin: null, note: '完成' })
})

test('invalid or duplicate sets are rejected before network access', () => {
  let called = false
  const api = new RecordingApi('https://api.example.org', auth, async () => { called = true })
  const write = { ...set, completed: true }
  assert.throws(() => api.saveSets('42', 3, [{ ...write, reps: 0 }], 'update-0001'), apiError('invalid_request'))
  assert.throws(() => api.saveSets('42', 3, [write, write], 'update-0001'), apiError('invalid_request'))
  assert.equal(called, false)
})

test('a response from the old session cannot be used after switching account or logging in again', async () => {
  let generation = 'login-1'
  let release
  const lease = {
    current: () => ({ ...credential, generation }),
    isCurrent: (id, captured) => id === '101' && captured === generation,
  }
  const api = new RecordingApi('https://api.example.org', lease, () => new Promise(resolve => { release = resolve }))
  const pending = api.readSession('42')
  generation = 'login-2'
  release({ statusCode: 200, data: detail() })
  await assert.rejects(pending, apiError('session_changed'))
})

test('mutating the credential object during a request does not make the old response current', async () => {
  let release
  const mutable = { ...credential }
  const lease = {
    current: () => mutable,
    isCurrent: (id, generation) => id === mutable.subjectId && generation === mutable.generation,
  }
  const api = new RecordingApi('https://api.example.org', lease, () => new Promise(resolve => { release = resolve }))
  const pending = api.readSession('42')
  mutable.generation = 'login-2'
  release({ statusCode: 200, data: detail() })
  await assert.rejects(pending, apiError('session_changed'))
})

test('missing authentication and insecure or malformed configuration fail closed', async () => {
  for (const base of ['http://api.example.org', 'https://user:pass@example.org', 'https://example.org/?token=secret']) {
    assert.throws(() => new RecordingApi(base, auth, async () => ({})), /服务地址/)
  }
  let called = false
  const api = new RecordingApi('https://api.example.org', { current: () => null, isCurrent: () => false }, async () => { called = true })
  await assert.rejects(api.readSession('42'), apiError('unauthenticated'))
  assert.equal(called, false)
  assert.throws(() => api.readSession('../42'), apiError('invalid_request'))
})

test('malformed, incomplete or mismatched server records never become an acknowledgement', async () => {
  const invalid = [
    detail({ id: '43' }),
    detail({ version: 0 }),
    detail({ date: '2026-02-30' }),
    detail({ timeZone: 'No/Such_Zone' }),
    detail({ createdAt: '2026-02-30T08:00:00Z' }),
    detail({ updatedAt: '2026-10-29T24:00:00Z' }),
    detail({ status: 'completed', sets: [] }),
    detail({ sets: [{ ...set, resolvedExerciseName: '' }] }),
    detail({ sets: [{ ...set, reps: 0 }] }),
    detail({ sets: [{ ...set, weightKg: '50' }] }),
    detail({ sets: [set, set] }),
    detail({ sets: [{ ...set, exerciseId: '5' }] }),
  ]
  for (const data of invalid) {
    const api = new RecordingApi('https://api.example.org', auth, async () => ({ statusCode: 200, data }))
    await assert.rejects(api.readSession('42'), apiError('invalid_response'))
  }
})

test('the uni-app adapter forwards request completion and rejects a transport failure', async () => {
  const previous = globalThis.uni
  try {
    globalThis.uni = { request(options) { options.success({ statusCode: 200, data: { ok: true } }) } }
    const request = createUniRequestPort()
    assert.deepEqual(await request({ url: 'https://example.org', method: 'GET', header: {} }), { statusCode: 200, data: { ok: true } })
    globalThis.uni = { request(options) { options.fail({ errMsg: 'offline' }) } }
    await assert.rejects(request({ url: 'https://example.org', method: 'GET', header: {} }), /network unavailable/)
  } finally { globalThis.uni = previous }
})
