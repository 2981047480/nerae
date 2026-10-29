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
  updatedAt: '2026-10-29T08:00:00Z', recordedAt: null, sets: [], exerciseEntryKeys: [], ...overrides,
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
    return { statusCode: 200, data: detail({ sets: [set], exerciseEntryKeys: ['entry-0001'] }) }
  })
  const { resolvedExerciseName, ...fields } = set
  const write = { ...fields, completed: true }
  await api.saveSets('42', 3, [write], ['entry-0001'], 'update-0001')
  await api.saveSets('42', 3, [write], ['entry-0001'], 'update-0001')
  await api.readSession('42')
  assert.equal(requests[0].url, 'https://api.example.org/v1/me/sessions/42/sets')
  assert.equal(requests[2].url, 'https://api.example.org/v1/me/sessions/42')
  assert.equal(requests[0].data, requests[1].data)
  assert.equal(requests[0].header['Idempotency-Key'], requests[1].header['Idempotency-Key'])
  assert.deepEqual(JSON.parse(requests[0].data), { expectedVersion: 3, exerciseEntryKeys: ['entry-0001'], sets: [write] })
  assert.equal(requests[2].header['Idempotency-Key'], undefined)
})

test('version conflict and network errors are explicit and do not trigger a blind retry', async () => {
  let requests = 0
  const conflict = new RecordingApi('https://api.example.org', auth, async () => {
    requests++
    return { statusCode: 412, data: { message: 'server-private-details' } }
  })
  await assert.rejects(conflict.saveSets('42', 3, [{ ...set, completed: true }], ['entry-0001'], 'update-0001'), apiError('version_conflict'))
  assert.equal(requests, 1)
  const offline = new RecordingApi('https://api.example.org', auth, async () => { throw new Error('token in low-level error') })
  await assert.rejects(offline.readSession('42'), error => apiError('network')(error) && !error.message.includes('token'))
})

test('finish preserves explicit nulls but omits unrelated fields', async () => {
  let captured
  const api = new RecordingApi('https://api.example.org/v1', auth, async request => {
    captured = request
    return { statusCode: 200, data: detail({ status: 'completed', recordedAt: '2026-10-29T08:00:01Z', sets: [set], exerciseEntryKeys: ['entry-0001'] }) }
  })
  await api.finishSession('42', { expectedVersion: 3, durationMin: null, note: '完成', ignored: 'do not send' }, 'finish-0001')
  assert.deepEqual(JSON.parse(captured.data), { expectedVersion: 3, durationMin: null, note: '完成' })
})

test('invalid or duplicate sets are rejected before network access', () => {
  let called = false
  const api = new RecordingApi('https://api.example.org', auth, async () => { called = true })
  const write = { ...set, completed: true }
  assert.throws(() => api.saveSets('42', 3, [{ ...write, reps: 0 }], ['entry-0001'], 'update-0001'), apiError('invalid_request'))
  assert.throws(() => api.saveSets('42', 3, [write, write], ['entry-0001'], 'update-0001'), apiError('invalid_request'))
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
    detail({ sets: [{ ...set, resolvedExerciseName: '' }], exerciseEntryKeys: ['entry-0001'] }),
    detail({ sets: [{ ...set, reps: 0 }], exerciseEntryKeys: ['entry-0001'] }),
    detail({ sets: [{ ...set, weightKg: '50' }], exerciseEntryKeys: ['entry-0001'] }),
    detail({ sets: [set, set], exerciseEntryKeys: ['entry-0001'] }),
    detail({ sets: [{ ...set, exerciseId: '5' }], exerciseEntryKeys: ['entry-0001'] }),
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


test('ordered details accept gaps but reject missing, duplicate, mismatched or unsorted snapshots', async () => {
  const secondEntry = { ...set, recordKey: 'record-003', exerciseEntryKey: 'entry-0002', exerciseName: '划船', resolvedExerciseName: '划船' }
  const laterSet = { ...set, recordKey: 'record-002', setIndex: 4 }
  const valid = detail({ sets: [set, laterSet, secondEntry], exerciseEntryKeys: ['entry-0001', 'entry-0002'] })
  const apiFor = data => new RecordingApi('https://api.example.org', auth, async () => ({ statusCode: 200, data }))
  assert.deepEqual((await apiFor(valid).readSession('42')).exerciseEntryKeys, ['entry-0001', 'entry-0002'])
  const invalid = [
    { ...valid, exerciseEntryKeys: undefined },
    { ...valid, exerciseEntryKeys: null },
    { ...valid, exerciseEntryKeys: ['entry-0001', 'entry-0001'] },
    { ...valid, exerciseEntryKeys: ['entry-0001'] },
    { ...valid, exerciseEntryKeys: ['entry-0001', 'entry-0002', 'entry-0003'] },
    { ...valid, exerciseEntryKeys: ['Entry-0001', 'entry-0002'] },
    { ...valid, exerciseEntryKeys: ['entry-0002', 'entry-0001'] },
    { ...valid, sets: [laterSet, set, secondEntry] },
    { ...valid, sets: [set, { ...laterSet, setIndex: 1 }, secondEntry] },
    { ...valid, sets: [set, { ...laterSet, exerciseName: '其他动作' }, secondEntry] },
    detail({ sets: [], exerciseEntryKeys: ['entry-0001'] }),
  ]
  for (const data of invalid) await assert.rejects(apiFor(data).readSession('42'), apiError('invalid_response'))
})

test('batch sends an explicit projected order for a reorder, delete and restore', async () => {
  const requests = []
  const secondEntry = { ...set, recordKey: 'record-002', exerciseEntryKey: 'entry-0002', exerciseName: '划船', resolvedExerciseName: '划船' }
  const api = new RecordingApi('https://api.example.org', auth, async request => {
    requests.push(request)
    const body = JSON.parse(request.data)
    if (body.deleteRecordKeys) return { statusCode: 200, data: detail() }
    if (body.restoreRecordKeys) return { statusCode: 200, data: detail({ sets: [set], exerciseEntryKeys: ['entry-0001'] }) }
    return { statusCode: 200, data: detail({ sets: [secondEntry, set], exerciseEntryKeys: ['entry-0002', 'entry-0001'] }) }
  })
  const { resolvedExerciseName, ...fields } = set
  const write = { ...fields, completed: true }
  await api.saveBatch('42', { expectedVersion: 3, exerciseEntryKeys: ['entry-0002', 'entry-0001'] }, 'reorder-001')
  await api.saveBatch('42', { expectedVersion: 3, exerciseEntryKeys: [], deleteRecordKeys: ['record-001'] }, 'delete-0001')
  await api.saveBatch('42', { expectedVersion: 3, exerciseEntryKeys: ['entry-0001'], sets: [write], restoreRecordKeys: ['record-001'] }, 'restore-001')
  assert.deepEqual(JSON.parse(requests[0].data), { expectedVersion: 3, exerciseEntryKeys: ['entry-0002', 'entry-0001'] })
  assert.deepEqual(JSON.parse(requests[1].data), { expectedVersion: 3, exerciseEntryKeys: [], deleteRecordKeys: ['record-001'] })
  assert.deepEqual(JSON.parse(requests[2].data), { expectedVersion: 3, exerciseEntryKeys: ['entry-0001'], sets: [write], restoreRecordKeys: ['record-001'] })
})

test('invalid order and overlapping batches fail before a network request', () => {
  let requests = 0
  const api = new RecordingApi('https://api.example.org', auth, async () => { requests++; return { statusCode: 200, data: detail() } })
  const write = { ...set, completed: true }
  const base = { expectedVersion: 3, exerciseEntryKeys: ['entry-0001'] }
  const bad = [
    { ...base, exerciseEntryKeys: undefined },
    { ...base, exerciseEntryKeys: ['entry-0001', 'entry-0001'] },
    { ...base, exerciseEntryKeys: ['entry-0002'], sets: [write] },
    { ...base, sets: [] },
    { ...base, deleteRecordKeys: [] },
    { ...base, restoreRecordKeys: ['record-001'] },
    { ...base, sets: [write], deleteRecordKeys: ['record-001'] },
    { ...base, sets: [write], restoreRecordKeys: ['record-002'] },
    { ...base, sets: [write, { ...write, recordKey: 'record-002' }] },
    { ...base, sets: [write, { ...write, recordKey: 'record-002', setIndex: 2, exerciseName: '不同动作' }] },
    { ...base, deleteRecordKeys: ['record-001', 'record-001'] },
    { ...base, sets: Array.from({ length: 200 }, (_, i) => ({ ...write, recordKey: `record-${String(i).padStart(3, '0')}`, setIndex: i + 1 })), deleteRecordKeys: ['record-999'] },
    { ...base, exerciseEntryKeys: Array.from({ length: 2001 }, (_, i) => `entry-${String(i).padStart(4, '0')}`) },
  ]
  for (const input of bad) assert.throws(() => api.saveBatch('42', input, 'update-0001'), apiError('invalid_request'))
  assert.equal(requests, 0)
})


test('a 200 response that does not acknowledge the requested order or group is not treated as saved', async () => {
  const write = { ...set, completed: true }
  const wrong = [
    detail(),
    detail({ sets: [{ ...set, reps: 7 }], exerciseEntryKeys: ['entry-0001'] }),
    detail({ sets: [set], exerciseEntryKeys: ['entry-0001'], version: 2 }),
  ]
  for (const data of wrong) {
    const api = new RecordingApi('https://api.example.org', auth, async () => ({ statusCode: 200, data }))
    await assert.rejects(api.saveSets('42', 3, [write], ['entry-0001'], 'update-0001'), apiError('invalid_response'))
  }
  const undeleted = new RecordingApi('https://api.example.org', auth, async () => ({ statusCode: 200, data: detail({ sets: [set], exerciseEntryKeys: ['entry-0001'] }) }))
  await assert.rejects(undeleted.saveBatch('42', { expectedVersion: 3, exerciseEntryKeys: ['entry-0001'], deleteRecordKeys: ['record-001'] }, 'delete-0001'), apiError('invalid_response'))
})


test('in-flight batch acknowledgement uses the original immutable request values', async () => {
  let release
  let sent
  const api = new RecordingApi('https://api.example.org', auth, request => {
    sent = JSON.parse(request.data)
    return new Promise(resolve => { release = resolve })
  })
  const input = { expectedVersion: 3, exerciseEntryKeys: ['entry-0001'], sets: [{ ...set, completed: true }] }
  const pending = api.saveBatch('42', input, 'update-0001')
  input.expectedVersion = 99
  input.exerciseEntryKeys[0] = 'entry-0002'
  input.sets[0].reps = 1
  release({ statusCode: 200, data: detail({ sets: [set], exerciseEntryKeys: ['entry-0001'] }) })
  assert.equal((await pending).version, 3)
  assert.equal(sent.expectedVersion, 3)
  assert.deepEqual(sent.exerciseEntryKeys, ['entry-0001'])
  assert.equal(sent.sets[0].reps, 8)
})
