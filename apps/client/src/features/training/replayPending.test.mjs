import assert from 'node:assert/strict'
import { test } from 'node:test'
import { RecordingApi, RecordingApiError, createUniRequestPort } from './recordingApi.ts'
import { TrainingOperationJournal } from './operationJournal.ts'

class Storage {
  values = new Map()
  async read(key) { return this.values.get(key) }
  async createIfAbsent(key, value) {
    if (this.values.has(key)) return false
    this.values.set(key, value)
    return true
  }
  async compareAndSwap(key, before, after) {
    if (this.values.get(key) !== before) return false
    if (after === null) this.values.delete(key); else this.values.set(key, after)
    return true
  }
}

const eventId = 'event-0001'
const headers = () => ({ 'Idempotency-Replayed': 'true', 'Idempotency-Key-Expires-At': '2030-11-01T00:00:00Z',
  'Content-Type': 'application/json; charset=utf-8', 'Cache-Control': 'no-store', 'X-Request-ID': 'a'.repeat(32) })
const detail = (values = {}) => ({ id: '42', clientEventId: eventId, date: '2026-10-30', timeZone: 'Asia/Shanghai',
  status: 'in_progress', source: 'free', version: 1, createdAt: '2026-10-30T08:00:00Z',
  updatedAt: '2026-10-30T08:00:00Z', recordedAt: null, sets: [], exerciseEntryKeys: [], ...values })
function auth() {
  let credential = { subjectId: '101', generation: 'session-1', accessToken: 'synthetic-token' }
  return { current: () => credential && { ...credential },
    isCurrent: (id, generation) => credential?.subjectId === id && credential?.generation === generation,
    set: value => { credential = value && { ...value } } }
}
async function pending(path, body, store = new Storage(), identity = auth()) {
  const journal = new TrainingOperationJournal(store, identity, { retentionMs: 60_000, now: () => 1000 })
  await journal.stage(eventId, { method: 'POST', path, body: JSON.stringify(body), idempotencyKey: 'replay-key-001' })
  const permit = await journal.beginSend(eventId)
  await journal.markUncertain(permit)
  return { journal, store, identity }
}
const createBody = { source: 'free', date: '2026-10-30', timeZone: 'Asia/Shanghai', clientEventId: eventId }
const sentSet = { recordKey: 'record-001', exerciseEntryKey: 'entry-0001', exerciseName: '深蹲', setIndex: 1,
  kind: 'weight', weightKg: 42.5, reps: 8, completed: true }
const serverSet = { ...sentSet, resolvedExerciseName: '深蹲' }

test('replay reads the journal request verbatim and never clears it', async () => {
  const { journal, store, identity } = await pending('/me/sessions', createBody)
  const before = [...store.values.values()][0]
  let sent
  const api = new RecordingApi('https://api.example.org/v1', identity, async request => {
    sent = request
    return { statusCode: 201, headers: headers(), data: detail() }
  }, { subjectId: '101', generation: 'session-1' })
  const result = await api.replayPending(journal, eventId)
  assert.equal(result.state, 'committed')
  assert.equal(result.detail.clientEventId, eventId)
  assert.equal(sent.url, 'https://api.example.org/v1/me/sessions/replay')
  assert.equal(sent.data, JSON.stringify(createBody))
  assert.equal(sent.header['Idempotency-Key'], 'replay-key-001')
  assert.equal([...store.values.values()][0], before)
  assert.equal((await journal.readPending(eventId)).state, 'uncertain')
})

test('ready operations do not query a receipt and a same-subject new session can recover an uncertain one', async () => {
  const identity = auth(), store = new Storage()
  let journal = new TrainingOperationJournal(store, identity, { retentionMs: 60_000, now: () => 1000 })
  await journal.stage(eventId, { method: 'POST', path: '/me/sessions', body: JSON.stringify(createBody), idempotencyKey: 'replay-key-001' })
  let calls = 0
  let api = new RecordingApi('https://api.example.org', identity, async () => { calls++; return { statusCode: 201, headers: headers(), data: detail() } },
    { subjectId: '101', generation: 'session-1' })
  assert.equal((await api.replayPending(journal, eventId)).state, 'unresolved')
  assert.equal(calls, 0)
  const permit = await journal.beginSend(eventId)
  await journal.markUncertain(permit)
  identity.set({ subjectId: '101', generation: 'session-2', accessToken: 'new-token' })
  journal = new TrainingOperationJournal(store, identity, { retentionMs: 60_000, now: () => 999_999_999 })
  api = new RecordingApi('https://api.example.org', identity, async () => { calls++; return { statusCode: 201, headers: headers(), data: detail() } },
    { subjectId: '101', generation: 'session-2' })
  assert.equal((await api.replayPending(journal, eventId)).state, 'committed')
  assert.equal(calls, 1)
  assert.equal((await journal.readPending(eventId)).state, 'uncertain')
})

test('ambiguous, unauthorized or malformed receipts keep the operation unresolved', async () => {
  for (const [name, reply] of Object.entries({
    missing: { statusCode: 503, headers: {}, data: {} },
    unauthorized: { statusCode: 401, headers: {}, data: {} },
    cachedFailure: { statusCode: 412, headers: headers(), data: {} },
    missingMarker: { statusCode: 201, headers: { ...headers(), 'Idempotency-Replayed': undefined }, data: detail() },
    duplicateMarker: { statusCode: 201, headers: { ...headers(), 'idempotency-replayed': 'true' }, data: detail() },
    joinedMarker: { statusCode: 201, headers: { ...headers(), 'Idempotency-Replayed': 'true, true' }, data: detail() },
    missingExpiry: { statusCode: 201, headers: { ...headers(), 'Idempotency-Key-Expires-At': 'bad' }, data: detail() },
    wrongEvent: { statusCode: 201, headers: headers(), data: detail({ clientEventId: 'event-other' }) },
    wrongDate: { statusCode: 201, headers: headers(), data: detail({ date: '2026-10-29' }) },
    wrongStatus: { statusCode: 200, headers: headers(), data: detail() },
  })) {
    const { journal, store, identity } = await pending('/me/sessions', createBody)
    const before = [...store.values.values()][0]
    const api = new RecordingApi('https://api.example.org', identity, async () => reply, { subjectId: '101', generation: 'session-1' })
    assert.equal((await api.replayPending(journal, eventId)).state, 'unresolved', name)
    assert.equal([...store.values.values()][0], before, name)
  }
})

test('replay checks submitted set contents and finish confirmation', async () => {
  const scenarios = [
    { path: '/me/sessions/42/sets', body: { expectedVersion: 1, exerciseEntryKeys: ['entry-0001'], sets: [sentSet] },
      value: detail({ version: 2, exerciseEntryKeys: ['entry-0001'], sets: [serverSet] }) },
    { path: '/me/sessions/42/finish', body: { expectedVersion: 2 },
      value: detail({ version: 3, status: 'completed', recordedAt: '2026-10-30T08:10:00Z', exerciseEntryKeys: ['entry-0001'], sets: [serverSet] }) },
  ]
  for (const scenario of scenarios) {
    const { journal, identity } = await pending(scenario.path, scenario.body)
    let value = scenario.value
    const api = new RecordingApi('https://api.example.org', identity, async () => ({ statusCode: 200, headers: headers(), data: value }),
      { subjectId: '101', generation: 'session-1' })
    assert.equal((await api.replayPending(journal, eventId)).state, 'committed', scenario.path)
    value = scenario.path.endsWith('/sets')
      ? detail({ version: 2, exerciseEntryKeys: ['entry-0001'], sets: [{ ...serverSet, reps: 9 }] })
      : detail({ version: 2, status: 'in_progress', exerciseEntryKeys: ['entry-0001'], sets: [serverSet] })
    assert.equal((await api.replayPending(journal, eventId)).state, 'unresolved', scenario.path)
  }
})

test('session changes during replay cannot return a receipt', async () => {
  const { journal, identity } = await pending('/me/sessions', createBody)
  const api = new RecordingApi('https://api.example.org', identity, async () => {
    identity.set({ subjectId: '202', generation: 'session-2', accessToken: 'other-token' })
    return { statusCode: 201, headers: headers(), data: detail() }
  }, { subjectId: '101', generation: 'session-1' })
  await assert.rejects(api.replayPending(journal, eventId), error => error instanceof RecordingApiError && error.code === 'session_changed')
})

test('a restore receipt must contain every restored key and its submitted values', async () => {
  const { journal, identity } = await pending('/me/sessions/42/sets', {
    expectedVersion: 1, exerciseEntryKeys: ['entry-0001'], restoreRecordKeys: ['record-001'],
  })
  const api = new RecordingApi('https://api.example.org', identity, async () => ({ statusCode: 200, headers: headers(),
    data: detail({ version: 2, exerciseEntryKeys: ['entry-0001'], sets: [serverSet] }) }),
  { subjectId: '101', generation: 'session-1' })
  assert.equal((await api.replayPending(journal, eventId)).state, 'unresolved')
  assert.equal((await journal.readPending(eventId)).state, 'uncertain')

  const valid = await pending('/me/sessions/42/sets', {
    expectedVersion: 1, exerciseEntryKeys: ['entry-0001'], restoreRecordKeys: ['record-001'], sets: [sentSet],
  })
  let returned = detail({ version: 2, exerciseEntryKeys: ['entry-0001'], sets: [serverSet] })
  const validApi = new RecordingApi('https://api.example.org', valid.identity, async () => ({ statusCode: 200, headers: headers(), data: returned }),
    { subjectId: '101', generation: 'session-1' })
  assert.equal((await validApi.replayPending(valid.journal, eventId)).state, 'committed')
  returned = detail({ version: 2, exerciseEntryKeys: ['entry-0001'], sets: [{ ...serverSet, recordKey: 'record-other' }] })
  assert.equal((await validApi.replayPending(valid.journal, eventId)).state, 'unresolved')
})

test('set receipt must match the recording session event, not a different operation identifier', async () => {
  const { journal, identity } = await pending('/me/sessions/42/sets', {
    expectedVersion: 1, exerciseEntryKeys: ['entry-0001'], sets: [sentSet],
  })
  const api = new RecordingApi('https://api.example.org', identity, async () => ({ statusCode: 200, headers: headers(),
    data: detail({ clientEventId: 'other-event-001', version: 2, exerciseEntryKeys: ['entry-0001'], sets: [serverSet] }) }),
  { subjectId: '101', generation: 'session-1' })
  assert.equal((await api.replayPending(journal, eventId)).state, 'unresolved')
})

test('uni request preserves one replay marker and rejects duplicates', async () => {
  const previous = globalThis.uni
  try {
    globalThis.uni = { request: options => options.success({ statusCode: 201, data: detail(), header: headers() }) }
    const result = await createUniRequestPort()({ url: 'https://api.example.org/me/sessions/replay', method: 'POST', header: {}, data: '{}' })
    assert.equal(result.headers['idempotency-replayed'], 'true')
    globalThis.uni = { request: options => options.success({ statusCode: 201, data: detail(), header: { ...headers(), 'idempotency-replayed': 'true' } }) }
    await assert.rejects(createUniRequestPort()({ url: 'https://api.example.org/me/sessions/replay', method: 'POST', header: {}, data: '{}' }),
      error => error instanceof RecordingApiError && error.code === 'reconcile_required')
  } finally { globalThis.uni = previous }
})
