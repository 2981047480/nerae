import assert from 'node:assert/strict'
import { test } from 'node:test'
import { OperationJournalError, TrainingOperationJournal } from './operationJournal.ts'
import { RecordingApi } from './recordingApi.ts'

class MemoryJournalStorage {
  values = new Map()
  failCreate = false
  failCas = false
  writes = 0

  async read(key) { return this.values.get(key) }
  async createIfAbsent(key, value) {
    if (this.failCreate) throw new Error('private storage failure')
    if (this.values.has(key)) return false
    this.values.set(key, value)
    this.writes++
    return true
  }
  async compareAndSwap(key, expected, next) {
    if (this.failCas) throw new Error('private storage failure')
    if (this.values.get(key) !== expected) return false
    if (next === null) this.values.delete(key)
    else this.values.set(key, next)
    this.writes++
    return true
  }
}

function makeAuth(subjectId = '101', generation = 'session-1') {
  let current = { subjectId, generation }
  return {
    current: () => current && { ...current },
    isCurrent: (id, gen) => current?.subjectId === id && current?.generation === gen,
    set(value) { current = value && { ...value } },
  }
}

const body = JSON.stringify({ source: 'free', date: '2026-10-30', timeZone: 'Asia/Shanghai', clientEventId: 'event-0001' })
const createRequest = (overrides = {}) => ({
  method: 'POST', path: '/me/sessions', body, idempotencyKey: 'create-key-0001', ...overrides,
})
const makeJournal = (storage, auth, now = () => 1_798_000_000_000) =>
  new TrainingOperationJournal(storage, auth, { retentionMs: 86_400_000, now })
const isJournalError = code => error => error instanceof OperationJournalError && error.code === code

function matchingSnapshot(auth, overrides = {}, eventId = 'event-0001') {
  const credential = auth.current()
  const apiAuth = {
    current: () => ({ ...credential, accessToken: 'synthetic-session-token' }),
    isCurrent: (subjectId, generation) => subjectId === credential.subjectId && generation === credential.generation,
  }
  const api = new RecordingApi('https://api.example.org', apiAuth, async () => ({
    statusCode: 201,
    data: {
      id: '42', clientEventId: eventId, date: '2026-10-30', timeZone: 'Asia/Shanghai',
      status: 'in_progress', source: 'free', version: 1,
      createdAt: '2026-10-30T08:00:00Z', updatedAt: '2026-10-30T08:00:00Z', recordedAt: null,
      sets: [], exerciseEntryKeys: [], ...overrides,
    },
  }), { subjectId: credential.subjectId, generation: credential.generation })
  return api.createFreeSession({ date: '2026-10-30', timeZone: 'Asia/Shanghai', clientEventId: eventId }, 'create-key-0001')
}

test('staging durably stores exact request before a dispatch permit and never stores credentials', async () => {
  const storage = new MemoryJournalStorage()
  const auth = makeAuth()
  const journal = makeJournal(storage, auth)
  const staged = await journal.stage('event-0001', createRequest())
  assert.equal(staged.state, 'ready')
  assert.equal(storage.writes, 1)
  const permit = await journal.beginSend('event-0001')
  assert.deepEqual(permit.operation.request, createRequest())
  assert.equal(permit.isCurrent(), true)
  assert.equal(JSON.stringify(permit).includes('accessToken'), false)
  assert.equal([...storage.values.values()].some(value => value.includes('bearer-secret')), false)
  assert.equal([...storage.values.values()].some(value => value.includes('Authorization')), false)
  await journal.markUncertain(permit)
  assert.equal((await journal.readPending('event-0001')).state, 'uncertain')
})

test('same event and exact request is idempotent; any changed key, body or route conflicts', async () => {
  const storage = new MemoryJournalStorage()
  const journal = makeJournal(storage, makeAuth())
  await journal.stage('event-0001', createRequest())
  assert.equal((await journal.stage('event-0001', createRequest())).request.body, body)
  for (const changed of [
    createRequest({ idempotencyKey: 'create-key-0002' }),
    createRequest({ body: JSON.stringify({ ...JSON.parse(body), date: '2026-10-29' }) }),
    createRequest({ path: '/me/sessions/42/sets' }),
  ]) await assert.rejects(journal.stage('event-0001', changed), isJournalError('conflict'))
  assert.equal(storage.values.size, 1)
})

test('concurrent staging and dispatch use atomic storage to keep one operation and one permit', async () => {
  const storage = new MemoryJournalStorage()
  const auth = makeAuth()
  const first = makeJournal(storage, auth)
  const second = makeJournal(storage, auth)
  const staged = await Promise.all([
    first.stage('event-0001', createRequest()),
    second.stage('event-0001', createRequest()),
  ])
  assert.equal(staged[0].request.idempotencyKey, staged[1].request.idempotencyKey)
  assert.equal(storage.values.size, 1)
  const attempts = await Promise.allSettled([
    first.beginSend('event-0001'),
    second.beginSend('event-0001'),
  ])
  assert.equal(attempts.filter(result => result.status === 'fulfilled').length, 1)
  assert.equal(attempts.filter(result => result.status === 'rejected').length, 1)
  const permit = attempts.find(result => result.status === 'fulfilled').value
  await first.markUncertain(permit)
})

test('request validation rejects duplicate JSON keys, credential fields, malformed routes and oversized bodies', async () => {
  const journal = makeJournal(new MemoryJournalStorage(), makeAuth())
  for (const request of [
    createRequest({ body: '{"source":"free","source":"plan","date":"2026-10-30","timeZone":"Asia/Shanghai","clientEventId":"event-0001"}' }),
    createRequest({ body: JSON.stringify({ source: 'free', date: '2026-10-30', timeZone: 'Asia/Shanghai', clientEventId: 'event-0001', accessToken: 'secret' }) }),
    createRequest({ path: '/me/sessions/42/sets?debug=1' }),
    createRequest({ body: JSON.stringify({ source: 'free', date: '2026-10-30', timeZone: 'Asia/Shanghai', clientEventId: 'event-0001', note: '😀'.repeat(140_000) }) }),
  ]) await assert.rejects(journal.stage('event-0001', request), isJournalError('invalid_operation'))
  assert.equal((await journal.readPending('event-0001')), null)
})

test('network ambiguity preserves the same bytes and key; it never treats raw 2xx as an acknowledgement', async () => {
  const storage = new MemoryJournalStorage()
  const auth = makeAuth()
  const journal = makeJournal(storage, auth)
  await journal.stage('event-0001', createRequest())
  const first = await journal.beginSend('event-0001')
  const firstRequest = first.operation.request
  // A network exception or unvalidated 2xx is deliberately not passed as proof.
  await journal.markUncertain(first)
  const second = await journal.beginSend('event-0001')
  assert.deepEqual(second.operation.request, firstRequest)
  await journal.markUncertain(second)
  assert.equal((await journal.readPending('event-0001')).state, 'uncertain')
})

test('only caller reconciliation with an issued same-account snapshot clears an applied operation', async () => {
  const storage = new MemoryJournalStorage()
  const auth = makeAuth()
  const journal = makeJournal(storage, auth)
  await journal.stage('event-0001', createRequest())
  const permit = await journal.beginSend('event-0001')
  await journal.markUncertain(permit)
  let inspected
  const snapshot = await matchingSnapshot(auth)
  const result = await journal.reconcile('event-0001', async operation => {
    inspected = operation
    return { verdict: 'applied', snapshot }
  })
  assert.equal(inspected.request.body, body)
  assert.equal(result.state, 'operation_cleared')
  assert.equal(result.snapshot.detail.id, '42')
  assert.equal(await journal.readPending('event-0001'), null)
})

test('raw response objects and snapshots for another subject or event cannot settle an operation', async () => {
  const storage = new MemoryJournalStorage()
  const auth = makeAuth()
  const journal = makeJournal(storage, auth)
  await journal.stage('event-0001', createRequest())
  const permit = await journal.beginSend('event-0001')
  await journal.markUncertain(permit)
  await assert.rejects(journal.reconcile('event-0001', async () => ({ verdict: 'applied', snapshot: { statusCode: 201, body: {} } })), isJournalError('invalid_operation'))
  const otherAuth = makeAuth('202', 'other-session')
  const otherSnapshot = await matchingSnapshot(otherAuth)
  await assert.rejects(journal.reconcile('event-0001', async () => ({ verdict: 'applied', snapshot: otherSnapshot })), isJournalError('invalid_operation'))
  const wrongEvent = await matchingSnapshot(auth, {}, 'event-0002')
  await assert.rejects(journal.reconcile('event-0001', async () => ({ verdict: 'applied', snapshot: wrongEvent })), isJournalError('invalid_operation'))
  assert.equal((await journal.readPending('event-0001')).state, 'uncertain')
})

test('not-applied permits only the exact same request retry; unknown stays pending', async () => {
  const storage = new MemoryJournalStorage()
  const auth = makeAuth()
  const journal = makeJournal(storage, auth)
  await journal.stage('event-0001', createRequest())
  const permit = await journal.beginSend('event-0001')
  await journal.markUncertain(permit)
  assert.deepEqual(await journal.reconcile('event-0001', async () => ({ verdict: 'unknown' })), { state: 'still_uncertain' })
  assert.deepEqual(await journal.reconcile('event-0001', async () => ({ verdict: 'not_applied' })), { state: 'retry_ready' })
  const retry = await journal.beginSend('event-0001')
  assert.deepEqual(retry.operation.request, createRequest())
})

test('same subject may recover after a new login generation, while the old lease cannot read or send', async () => {
  const storage = new MemoryJournalStorage()
  const oldAuth = makeAuth('101', 'session-1')
  const oldJournal = makeJournal(storage, oldAuth)
  await oldJournal.stage('event-0001', createRequest())
  oldAuth.set({ subjectId: '202', generation: 'other' })
  await assert.rejects(oldJournal.readPending('event-0001'), isJournalError('session_changed'))
  await assert.rejects(oldJournal.beginSend('event-0001'), isJournalError('session_changed'))

  const newAuth = makeAuth('101', 'session-2')
  const recovered = makeJournal(storage, newAuth)
  const pending = await recovered.readPending('event-0001')
  assert.equal(pending.createdGeneration, 'session-1')
  const permit = await recovered.beginSend('event-0001')
  assert.deepEqual(permit.operation.request, createRequest())
  assert.equal(permit.isCurrent(), true)
})

test('an operation is scoped to subject plus event and never crosses accounts', async () => {
  const storage = new MemoryJournalStorage()
  await makeJournal(storage, makeAuth('101', 'one')).stage('event-0001', createRequest())
  const other = makeJournal(storage, makeAuth('202', 'two'))
  assert.equal(await other.readPending('event-0001'), null)
  await other.stage('event-0001', createRequest())
  assert.equal(storage.values.size, 2)
})

test('storage failure, concurrent send, corrupted records and expiry fail closed without deleting evidence', async () => {
  const storage = new MemoryJournalStorage()
  const auth = makeAuth()
  const now = { value: 10_000_000 }
  const journal = makeJournal(storage, auth, () => now.value)
  storage.failCreate = true
  await assert.rejects(journal.stage('event-0001', createRequest()), isJournalError('unavailable'))
  storage.failCreate = false
  await journal.stage('event-0001', createRequest())
  const permit = await journal.beginSend('event-0001')
  const concurrent = makeJournal(storage, auth, () => now.value)
  await assert.rejects(concurrent.beginSend('event-0001'), isJournalError('busy'))
  await journal.markUncertain(permit)

  const key = [...storage.values.keys()][0]
  const valid = storage.values.get(key)
  storage.values.set(key, '{"schema":1,"subjectId":"101","accessToken":"secret"}')
  await assert.rejects(journal.readPending('event-0001'), isJournalError('corrupt'))
  storage.values.set(key, valid)
  now.value += 86_400_001
  await assert.rejects(journal.beginSend('event-0001'), isJournalError('expired'))
  assert.equal(storage.values.has(key), true)
})

test('auth change during persistence prevents a dispatchable operation result', async () => {
  let release
  let enter
  const entered = new Promise(resolve => { enter = resolve })
  let releaseGate
  const gate = new Promise(resolve => { releaseGate = resolve })
  const storage = new MemoryJournalStorage()
  const auth = makeAuth()
  const original = storage.createIfAbsent.bind(storage)
  storage.createIfAbsent = async (...args) => {
    const result = await original(...args)
    enter()
    await gate
    return result
  }
  const journal = makeJournal(storage, auth)
  const staging = journal.stage('event-0001', createRequest())
  release = releaseGate
  await entered
  auth.set({ subjectId: '202', generation: 'next' })
  release()
  await assert.rejects(staging, isJournalError('session_changed'))
  assert.equal(storage.values.size, 1)
  assert.equal((await makeJournal(storage, makeAuth('101', 'new-login')).readPending('event-0001')).request.body, body)
})
