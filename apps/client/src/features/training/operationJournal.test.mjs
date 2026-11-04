import assert from 'node:assert/strict'
import { test } from 'node:test'
import { inspect } from 'node:util'
import { OperationJournalError, TrainingOperationJournal } from './operationJournal.ts'
import { RecordingApi } from './recordingApi.ts'

class MemoryJournalStorage {
  values = new Map()
  failCreate = false
  failCas = false
  failCasAfterCommit = false
  writes = 0
  dispatched = new Set()

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
    if (this.failCasAfterCommit) {
      this.failCasAfterCommit = false
      throw new Error('native outcome unknown')
    }
    return true
  }

  // Synthetic implementation of the native monotonic claim contract.
  async claimFirstSend(key, expected, next) {
    if (this.failCas) throw new Error('private storage failure')
    const identity = `${key}\0${JSON.parse(expected).request.idempotencyKey}`
    if (this.values.get(key) !== expected || this.dispatched.has(identity)) return false
    this.dispatched.add(identity)
    return this.compareAndSwap(key, expected, next)
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
    headers: { 'Idempotency-Key-Expires-At': '2030-11-01T00:00:00Z' },
    data: {
      id: '42', clientEventId: eventId, date: '2026-10-30', timeZone: 'Asia/Shanghai',
      status: 'in_progress', source: 'free', version: 1,
      createdAt: '2026-10-30T08:00:00Z', updatedAt: '2026-10-30T08:00:00Z', recordedAt: null,
      sets: [], exerciseEntryKeys: [], ...overrides,
    },
  }), { subjectId: credential.subjectId, generation: credential.generation })
  return api.createFreeSession({ date: '2026-10-30', timeZone: 'Asia/Shanghai', clientEventId: eventId }, 'create-key-0001')
}

function existingSessionSnapshot(auth, overrides = {}) {
  const credential = auth.current()
  const apiAuth = {
    current: () => ({ ...credential, accessToken: 'synthetic-session-token' }),
    isCurrent: (subjectId, generation) => subjectId === credential.subjectId && generation === credential.generation,
  }
  const api = new RecordingApi('https://api.example.org', apiAuth, async () => ({
    statusCode: 200,
    data: {
      id: '42', clientEventId: 'event-0001', date: '2026-10-30', timeZone: 'Asia/Shanghai',
      status: 'in_progress', source: 'free', version: 3,
      createdAt: '2026-10-30T08:00:00Z', updatedAt: '2026-10-30T08:00:00Z', recordedAt: null,
      sets: [{ recordKey: 'record-001', exerciseEntryKey: 'entry-0001', exerciseName: '深蹲', resolvedExerciseName: '深蹲',
        setIndex: 1, kind: 'weight', weightKg: 40, reps: 8 }], exerciseEntryKeys: ['entry-0001'], ...overrides,
    },
  }), { subjectId: credential.subjectId, generation: credential.generation })
  return api.readSession('42')
}

test('staging durably stores exact request before a dispatch permit and never stores credentials', async () => {
  const storage = new MemoryJournalStorage()
  const auth = makeAuth()
  const journal = makeJournal(storage, auth)
  const staged = await journal.stage('event-0001', createRequest())
  assert.equal(staged.state, 'ready')
  assert.equal(Object.prototype.propertyIsEnumerable.call(staged, 'request'), false)
  assert.equal(JSON.stringify(staged).includes('Asia/Shanghai'), false)
  assert.equal(inspect(staged).includes('Asia/Shanghai'), false)
  assert.equal(storage.writes, 1)
  const permit = await journal.beginSend('event-0001')
  assert.deepEqual(permit.operation.request, createRequest())
  assert.equal(permit.isCurrent(), true)
  assert.equal(JSON.stringify(permit).includes(body), false)
  assert.equal(inspect(permit).includes('Asia/Shanghai'), false)
  assert.equal(JSON.stringify(permit).includes('accessToken'), false)
  assert.equal([...storage.values.values()].some(value => value.includes('bearer-secret')), false)
  assert.equal([...storage.values.values()].some(value => value.includes('Authorization')), false)
  await journal.markUncertain(permit)
  assert.equal((await journal.readPending('event-0001')).state, 'uncertain')
})

test('storage without a monotonic first-send claim preserves the request and issues no permit', async () => {
  const storage = new MemoryJournalStorage()
  storage.claimFirstSend = undefined
  const journal = makeJournal(storage, makeAuth())
  await journal.stage('event-0001', createRequest())
  await assert.rejects(journal.beginSend('event-0001'), isJournalError('unavailable'))
  const pending = await journal.readPending('event-0001')
  assert.equal(pending.state, 'ready')
  assert.deepEqual(pending.request, createRequest())
  assert.equal(storage.writes, 1)
})

test('dispatch permission expires before sending and retains the in-flight request for recovery', async () => {
  const storage = new MemoryJournalStorage()
  const now = { value: 10_000_000 }
  const journal = makeJournal(storage, makeAuth(), () => now.value)
  const staged = await journal.stage('event-0001', createRequest())
  now.value = staged.expiresAt - 1
  const permit = await journal.beginSend('event-0001')
  assert.equal(permit.isCurrent(), true)
  now.value = staged.expiresAt
  assert.equal(permit.isCurrent(), false)
  now.value = staged.expiresAt - 1
  assert.equal(permit.isCurrent(), false, 'a clock rollback cannot revive an expired permit')
  assert.equal((await journal.readPending('event-0001')).state, 'in_flight')
  await journal.markUncertain(permit)
  assert.equal((await journal.readPending('event-0001')).state, 'uncertain')
  await assert.rejects(journal.beginSend('event-0001'), isJournalError('reconciliation_required'))
})

test('invalid or backward clocks invalidate dispatch permission without discarding the request', async () => {
  for (const invalid of [NaN, -1, null]) {
    const storage = new MemoryJournalStorage()
    const now = { value: 10_000_000 }
    const journal = makeJournal(storage, makeAuth(), () => {
      if (now.value === null) throw new Error('clock unavailable')
      return now.value
    })
    await journal.stage('event-0001', createRequest())
    const permit = await journal.beginSend('event-0001')
    now.value = invalid
    assert.equal(permit.isCurrent(), false)
    now.value = 10_000_000
    assert.equal(permit.isCurrent(), false)
    await journal.markUncertain(permit)
    assert.equal((await journal.readPending('event-0001')).state, 'uncertain')
  }
  const now = { value: 10_000_000 }
  const journal = makeJournal(new MemoryJournalStorage(), makeAuth(), () => now.value)
  await journal.stage('event-0001', createRequest())
  const permit = await journal.beginSend('event-0001')
  now.value--
  assert.equal(permit.isCurrent(), false)
  now.value++
  assert.equal(permit.isCurrent(), false)
})

test('a dispatch claim that completes after its deadline cannot be used to send', async () => {
  const storage = new MemoryJournalStorage()
  const now = { value: 10_000_000 }
  const journal = makeJournal(storage, makeAuth(), () => now.value)
  const staged = await journal.stage('event-0001', createRequest())
  const claim = storage.claimFirstSend.bind(storage)
  storage.claimFirstSend = async (...args) => {
    const changed = await claim(...args)
    now.value = staged.expiresAt
    return changed
  }
  const permit = await journal.beginSend('event-0001')
  assert.equal(permit.isCurrent(), false)
  await journal.markUncertain(permit)
  assert.equal((await journal.readPending('event-0001')).state, 'uncertain')
})

test('marking uncertain invalidates the permit before the storage write finishes', async () => {
  const storage = new MemoryJournalStorage()
  const journal = makeJournal(storage, makeAuth())
  await journal.stage('event-0001', createRequest())
  const permit = await journal.beginSend('event-0001')
  const compareAndSwap = storage.compareAndSwap.bind(storage)
  let resume
  let reportStarted
  const started = new Promise(resolve => { reportStarted = resolve })
  const gate = new Promise(resolve => { resume = resolve })
  storage.compareAndSwap = async (...args) => {
    reportStarted()
    await gate
    return compareAndSwap(...args)
  }
  const marking = journal.markUncertain(permit)
  await started
  try {
    assert.equal(permit.isCurrent(), false)
    await assert.rejects(journal.markUncertain(permit), isJournalError('conflict'))
  } finally {
    resume()
    await marking
  }
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
    createRequest({ path: '/me/sessions/42/sets', body: JSON.stringify({ expectedVersion: 1, exerciseEntryKeys: ['entry-0001'], sets: [{
      recordKey: 'record-001', exerciseEntryKey: 'entry-0001', exerciseName: '深蹲', setIndex: 1,
      kind: 'weight', weightKg: 42.5, reps: 8, completed: true,
    }] }) }),
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

test('journal entry accepts only path-specific contract fields and rejects credential variants at every nesting level', async () => {
  const valid = [
    {
      eventId: 'event-0002', path: '/me/sessions', idempotencyKey: 'plan-key-0001',
      body: JSON.stringify({ source: 'plan', date: '2026-10-30', planId: '12', planVersion: 2,
        planDayId: '3', clientEventId: 'event-0002', timeZone: 'Asia/Shanghai', occurrenceId: '9' }),
    },
    {
      eventId: 'event-0003', path: '/me/sessions/42/sets', idempotencyKey: 'sets-key-0001',
      body: JSON.stringify({ expectedVersion: 1, exerciseEntryKeys: ['entry-0001'], sets: [{
        recordKey: 'record-001', exerciseEntryKey: 'entry-0001', exerciseName: '深蹲', setIndex: 1,
        kind: 'weight', weightKg: 42.5, reps: 8, rpe: 7.5, completed: true,
      }] }),
    },
    {
      eventId: 'event-0004', path: '/me/sessions/42/finish', idempotencyKey: 'finish-key-001',
      body: JSON.stringify({ expectedVersion: 2, durationMin: null, date: '2026-10-30', rpe: 7.5, note: null }),
    },
  ]
  const validStorage = new MemoryJournalStorage()
  const validJournal = makeJournal(validStorage, makeAuth())
  for (const item of valid) {
    await validJournal.stage(item.eventId, { method: 'POST', path: item.path, body: item.body, idempotencyKey: item.idempotencyKey })
  }
  assert.equal(validStorage.writes, valid.length)

  const invalid = [
    { eventId: 'event-0010', path: '/me/sessions', body: { source: 'free', date: '2026-10-30', clientEventId: 'event-0010', deviceProof: 'opaque' } },
    { eventId: 'event-0011', path: '/me/sessions', body: { source: 'plan', date: '2026-10-30', planId: '12', planVersion: 2, planDayId: '3', clientEventId: 'event-0011', approvalSecret: 'opaque' } },
    { eventId: 'event-0012', path: '/me/sessions/42/sets', body: { expectedVersion: 1, exerciseEntryKeys: ['entry-0001'], deviceProof: 'opaque' } },
    { eventId: 'event-0013', path: '/me/sessions/42/sets', body: { expectedVersion: 1, exerciseEntryKeys: ['entry-0001'], sets: [{
      recordKey: 'record-001', exerciseEntryKey: 'entry-0001', exerciseName: '深蹲', setIndex: 1,
      kind: 'weight', weightKg: 42.5, reps: 8, approvalSecret: 'opaque',
    }] } },
    { eventId: 'event-0014', path: '/me/sessions/42/sets', body: { expectedVersion: 1, exerciseEntryKeys: ['entry-0001'], sets: [{
      recordKey: 'record-001', exerciseEntryKey: 'entry-0001', exerciseName: '深蹲', setIndex: 1,
      kind: 'weight', weightKg: 42.5, reps: 8, deviceProof: 'opaque',
    }] } },
    { eventId: 'event-0015', path: '/me/sessions/42/sets', body: { expectedVersion: 1, exerciseEntryKeys: ['entry-0001'], sets: [{
      recordKey: 'record-001', exerciseEntryKey: 'entry-0001', exerciseName: '深蹲', setIndex: 1,
      kind: 'weight', weightKg: 42.5, reps: 8, resolvedExerciseName: '冻结名称',
    }] } },
    { eventId: 'event-0016', path: '/me/sessions/42/finish', body: { expectedVersion: 2, note: '已完成', accessToken: 'opaque' } },
  ]
  const invalidStorage = new MemoryJournalStorage()
  const invalidJournal = makeJournal(invalidStorage, makeAuth())
  for (const [index, item] of invalid.entries()) {
    const request = { method: 'POST', path: item.path, body: JSON.stringify(item.body), idempotencyKey: `deny-key-${index.toString().padStart(4, '0')}` }
    await assert.rejects(invalidJournal.stage(item.eventId, request), isJournalError('invalid_operation'))
  }
  assert.equal(invalidStorage.writes, 0)
  assert.equal(invalidStorage.values.size, 0)
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
  await assert.rejects(journal.beginSend('event-0001'), isJournalError('reconciliation_required'))
  assert.equal((await journal.readPending('event-0001')).state, 'uncertain')
  assert.deepEqual((await journal.readPending('event-0001')).request, firstRequest)
})

test('an old same-account, same-event snapshot cannot clear a newer write intent', async () => {
  const storage = new MemoryJournalStorage()
  const auth = makeAuth()
  const journal = makeJournal(storage, auth)
  const updateBody = JSON.stringify({ expectedVersion: 3, exerciseEntryKeys: ['entry-0001'],
    sets: [{ recordKey: 'record-001', exerciseEntryKey: 'entry-0001', exerciseName: '深蹲',
      setIndex: 1, kind: 'weight', weightKg: 50, reps: 8, completed: true }] })
  const updateRequest = { method: 'POST', path: '/me/sessions/42/sets', body: updateBody, idempotencyKey: 'update-key-0001' }
  await journal.stage('event-0001', updateRequest)
  const permit = await journal.beginSend('event-0001')
  await journal.markUncertain(permit)
  // This synthetic server response passed through RecordingApi, but is old v3.
  // It has the same owner, event, and resource; the pending write expected v4.
  const stale = await existingSessionSnapshot(auth)
  assert.equal(stale.detail.version, 3)
  const result = await journal.reconcile('event-0001', async operation => {
    assert.deepEqual(operation.request, updateRequest)
    return { verdict: 'applied', evidence: stale }
  })
  assert.equal(result.state, 'still_uncertain')
  assert.equal((await journal.readPending('event-0001')).request.body, updateBody)
})

test('raw responses and issued snapshots cannot settle without request-bound replay evidence', async () => {
  const storage = new MemoryJournalStorage()
  const auth = makeAuth()
  const journal = makeJournal(storage, auth)
  await journal.stage('event-0001', createRequest())
  const permit = await journal.beginSend('event-0001')
  await journal.markUncertain(permit)
  assert.equal((await journal.reconcile('event-0001', async () => ({ verdict: 'applied', evidence: { statusCode: 201, body: {} } }))).state, 'still_uncertain')
  const otherAuth = makeAuth('202', 'other-session')
  const otherSnapshot = await matchingSnapshot(otherAuth)
  assert.equal((await journal.reconcile('event-0001', async () => ({ verdict: 'applied', evidence: otherSnapshot }))).state, 'still_uncertain')
  const wrongEvent = await matchingSnapshot(auth, {}, 'event-0002')
  assert.equal((await journal.reconcile('event-0001', async () => ({ verdict: 'applied', evidence: wrongEvent }))).state, 'still_uncertain')
  assert.equal((await journal.readPending('event-0001')).state, 'uncertain')
})

test('a bare not-applied verdict cannot authorize retry or change the stored operation', async () => {
  const storage = new MemoryJournalStorage()
  const auth = makeAuth()
  const journal = makeJournal(storage, auth)
  await journal.stage('event-0001', createRequest())
  const permit = await journal.beginSend('event-0001')
  await journal.markUncertain(permit)
  const key = [...storage.values.keys()][0]
  const before = storage.values.get(key)
  const writes = storage.writes
  await assert.rejects(journal.reconcile('event-0001', async () => ({ verdict: 'not_applied' })), isJournalError('invalid_operation'))
  assert.equal(storage.values.get(key), before)
  assert.equal(storage.writes, writes)
  await assert.rejects(journal.beginSend('event-0001'), isJournalError('reconciliation_required'))
  assert.equal((await journal.readPending('event-0001')).state, 'uncertain')
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
  const original = await makeJournal(storage, makeAuth('101', 'one')).stage('event-0001', createRequest())
  const other = makeJournal(storage, makeAuth('202', 'two'))
  assert.equal(await other.readPending('event-0001'), null)
  await assert.rejects(other.beginSend('event-0001'), isJournalError('conflict'))
  await other.stage('event-0001', createRequest())
  assert.equal(storage.values.size, 2)
  assert.equal((await makeJournal(storage, makeAuth('101', 'one')).readPending('event-0001')).request.body, original.request.body)
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
  await assert.rejects(concurrent.beginSend('event-0001'), isJournalError('reconciliation_required'))
  await journal.markUncertain(permit)

  const key = [...storage.values.keys()][0]
  const valid = storage.values.get(key)
  storage.values.set(key, '{"schema":1,"subjectId":"101","accessToken":"secret"}')
  await assert.rejects(journal.readPending('event-0001'), isJournalError('corrupt'))
  storage.values.set(key, valid)
  now.value += 86_400_001
  await assert.rejects(journal.beginSend('event-0001'), isJournalError('reconciliation_required'))
  assert.equal(storage.values.has(key), true)
})

test('clock rollback and a new lease cannot make an uncertain request dispatchable', async () => {
  const storage = new MemoryJournalStorage()
  const firstAuth = makeAuth('101', 'session-1')
  const now = { value: 20_000_000 }
  const first = makeJournal(storage, firstAuth, () => now.value)
  await first.stage('event-0001', createRequest())
  const permit = await first.beginSend('event-0001')
  await first.markUncertain(permit)
  const key = [...storage.values.keys()][0]
  const before = storage.values.get(key)
  const writes = storage.writes

  // Advance beyond the local retention horizon, then roll the wall clock back.
  // The recovered lease must not use that clock to authorize a replay.
  now.value += 86_400_001
  firstAuth.set({ subjectId: '101', generation: 'session-2' })
  now.value = 1
  const recovered = makeJournal(storage, firstAuth, () => now.value)
  await assert.rejects(recovered.beginSend('event-0001'), isJournalError('reconciliation_required'))
  assert.equal(storage.values.get(key), before)
  assert.equal(storage.writes, writes)
  assert.deepEqual((await recovered.readPending('event-0001')).request, createRequest())
})

test('local retention can refuse a never-sent first dispatch but never deletes the staged request', async () => {
  const storage = new MemoryJournalStorage()
  const auth = makeAuth()
  const now = { value: 30_000_000 }
  const journal = makeJournal(storage, auth, () => now.value)
  await journal.stage('event-0001', createRequest())
  const key = [...storage.values.keys()][0]
  const before = storage.values.get(key)
  now.value += 86_400_001
  await assert.rejects(journal.beginSend('event-0001'), isJournalError('expired'))
  assert.equal(storage.values.get(key), before)
  assert.equal((await journal.readPending('event-0001')).request.idempotencyKey, createRequest().idempotencyKey)
})

test('a crash-shaped in-flight record and an indeterminate CAS never yield a second permit', async () => {
  const storage = new MemoryJournalStorage()
  const auth = makeAuth('101', 'session-1')
  const first = makeJournal(storage, auth)
  await first.stage('event-0001', createRequest())
  storage.failCasAfterCommit = true
  await assert.rejects(first.beginSend('event-0001'), isJournalError('unavailable'))
  const key = [...storage.values.keys()][0]
  const firstStored = JSON.parse(storage.values.get(key))
  assert.equal(firstStored.state, 'in_flight')
  assert.deepEqual(firstStored.request, createRequest())

  // Simulate process restart with the same lease generation: no process-local
  // busy state remains, but the durable marker still forbids a second send.
  const restarted = makeJournal(storage, auth, () => { throw new Error('wall clock unavailable') })
  await assert.rejects(restarted.beginSend('event-0001'), isJournalError('reconciliation_required'))
  assert.equal((await restarted.readPending('event-0001')).state, 'in_flight')
  storage.failCas = true
  await assert.rejects(restarted.reconcile('event-0001', async () => ({ verdict: 'unknown' })), isJournalError('unavailable'))
  // Reconciliation may fail when CAS outcome is unknown; the original body
  // remains and no dispatch permit is returned.
  const stillStored = JSON.parse(storage.values.get(key))
  assert.deepEqual(stillStored.request, createRequest())

  auth.set({ subjectId: '101', generation: 'session-2' })
  const nextLease = makeJournal(storage, auth, () => 1)
  await assert.rejects(nextLease.beginSend('event-0001'), isJournalError('reconciliation_required'))
  assert.deepEqual((await nextLease.readPending('event-0001')).request, createRequest())
})

test('concurrent recovery attempts cannot resend an uncertain operation or alter its request', async () => {
  const storage = new MemoryJournalStorage()
  const auth = makeAuth('101', 'session-1')
  const first = makeJournal(storage, auth)
  await first.stage('event-0001', createRequest())
  const permit = await first.beginSend('event-0001')
  await first.markUncertain(permit)
  auth.set({ subjectId: '101', generation: 'session-2' })
  const before = storage.values.get([...storage.values.keys()][0])
  const writes = storage.writes
  const recoveredA = makeJournal(storage, auth, () => 1)
  const recoveredB = makeJournal(storage, auth, () => 1)
  const outcomes = await Promise.allSettled([
    recoveredA.beginSend('event-0001'),
    recoveredB.beginSend('event-0001'),
  ])
  assert.ok(outcomes.every(result => result.status === 'rejected' && result.reason.code === 'reconciliation_required'))
  assert.equal(storage.values.get([...storage.values.keys()][0]), before)
  assert.equal(storage.writes, writes)
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
