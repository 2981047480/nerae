import assert from 'node:assert/strict'
import { test } from 'node:test'
import { SecureJournalStorageError, SecureOperationJournalStorage } from './secureOperationJournalStorage.ts'

const registryKey = (subject = '101', event = 'event-0001') =>
  `nerae.workout.operation.v1.${subject}.${encodeURIComponent(event)}`
const freeCreateBody = event => JSON.stringify({ source: 'free', date: '2026-10-31', clientEventId: event, timeZone: 'Asia/Shanghai' })
const plannedCreateBody = event => JSON.stringify({ source: 'plan', date: '2026-10-31', clientEventId: event,
  timeZone: 'Asia/Shanghai', planId: '20', planVersion: 3, planDayId: '9', occurrenceId: '11' })
const setBatchBody = JSON.stringify({ expectedVersion: 1, exerciseEntryKeys: ['entry-0001'], sets: [{
  recordKey: 'record-001', exerciseEntryKey: 'entry-0001', exerciseName: '深蹲', kind: 'weight', setIndex: 1,
  weightKg: 42.5, reps: 8, rpe: null, completed: true,
}], deleteRecordKeys: ['record-002'], restoreRecordKeys: ['record-001'] })
const finishBody = JSON.stringify({ expectedVersion: 2, date: '2026-10-31', durationMin: null, rpe: 7.5, note: null })
const operation = (subjectId = '101', clientEventId = 'event-0001', revision = 1, path = '/me/sessions/55/sets', body = setBatchBody) => JSON.stringify({
  schema: 1, subjectId, clientEventId, createdGeneration: 'session-1', revision,
  state: 'ready', createdAt: 1000, expiresAt: 2000,
  request: { method: 'POST', path, body, idempotencyKey: 'idem-key-0001' },
})

function auth(subjectId = '101', generation = 'session-1') {
  return {
    subjectId,
    generation,
    current() { return this.subjectId ? { subjectId: this.subjectId, generation: this.generation } : null },
    isCurrent(subject, lease) { return this.subjectId === subject && this.generation === lease },
  }
}
const makeStore = (backend, lease = auth()) => new SecureOperationJournalStorage(backend, lease)

function native(initial = null) {
  return {
    value: initial,
    calls: 0,
    failNext: null,
    readTrainingOperationJournal() {
      return { ok: true, errorCode: '', found: this.value !== null, value: this.value ?? '' }
    },
    compareAndSetTrainingOperationJournal(found, expected, value) {
      this.calls++
      if (this.failNext === 'unavailable') {
        this.failNext = null
        return { ok: false, errorCode: 'unavailable', found: false, value: '' }
      }
      if (this.failNext === 'throw-after-commit') {
        this.failNext = null
        if ((this.value !== null) === found && (this.value ?? '') === expected) this.value = value
        throw new Error('unknown native outcome')
      }
      if ((this.value !== null) !== found || (this.value ?? '') !== expected) {
        return { ok: false, errorCode: 'conflict', found: false, value: '' }
      }
      this.value = value
      return { ok: true, errorCode: '', found: false, value: '' }
    },
  }
}

test('implements read, createIfAbsent, compareAndSwap and logical delete over one registry', async () => {
  const backend = native()
  const store = makeStore(backend)
  const key = registryKey()
  const first = operation()
  assert.equal(await store.read(key), null)
  assert.equal(await store.createIfAbsent(key, first), true)
  assert.equal(await store.read(key), first)
  assert.equal(await store.createIfAbsent(key, first), false)
  assert.equal(await store.compareAndSwap(key, operation('101', 'event-0001', 9), null), false)
  const next = operation('101', 'event-0001', 2)
  assert.equal(await store.compareAndSwap(key, first, next), true)
  assert.equal(await store.read(key), next)
  assert.equal(await store.compareAndSwap(key, next, null), true)
  assert.equal(await store.read(key), null)
  assert.ok(backend.value, 'logical deletion retains the native slot')
  assert.deepEqual(JSON.parse(backend.value), { schema: 1, entries: {} })
})

test('isolates event keys and retries unrelated native CAS conflicts', async () => {
  const backend = native()
  const store = makeStore(backend)
  const keys = [registryKey('101', 'event-0001'), registryKey('101', 'event-0002')]
  const values = [operation('101', 'event-0001'), operation('101', 'event-0002')]
  await Promise.all(keys.map((key, index) => store.createIfAbsent(key, values[index])))
  assert.equal(await store.read(keys[0]), values[0])
  assert.equal(await store.read(keys[1]), values[1])
  assert.equal(Object.keys(JSON.parse(backend.value).entries).length, 2)
  assert.ok(backend.calls >= 2)
})

test('rejects cross-owner operations and malformed/noncanonical storage keys', async () => {
  const store = makeStore(native())
  await assert.rejects(store.createIfAbsent(registryKey('101'), operation('202')), error => error.code === 'invalid_operation')
  await assert.rejects(store.read('nerae.workout.operation.v1.101.event-0001%2f'), error => error.code === 'invalid_operation')
  await assert.rejects(store.read('other-slot'), error => error.code === 'invalid_operation')
  await assert.rejects(store.read(registryKey('202', 'event-0002')), error => error.code === 'session_changed')
  await assert.rejects(store.createIfAbsent(registryKey('202', 'event-0002'), operation('202', 'event-0002')),
    error => error.code === 'session_changed')
})

test('keeps canonical Unicode event IDs bound to their exact owner key', async () => {
  const event = '事件-id-01'
  const key = registryKey('101', event)
  const value = operation('101', event)
  const store = makeStore(native())
  assert.equal(await store.createIfAbsent(key, value), true)
  assert.equal(await store.read(key), value)
})

test('accepts current free and planned creation fields, nested set writes, and optional finish values', async () => {
  const backend = native()
  const store = makeStore(backend)
  const cases = [
    [registryKey('101', 'event-0001'), operation('101', 'event-0001', 1, '/me/sessions', freeCreateBody('event-0001'))],
    [registryKey('101', 'event-0002'), operation('101', 'event-0002', 1, '/me/sessions', plannedCreateBody('event-0002'))],
    [registryKey('101', 'event-0003'), operation('101', 'event-0003', 1, '/me/sessions/55/sets', setBatchBody)],
    [registryKey('101', 'event-0004'), operation('101', 'event-0004', 1, '/me/sessions/55/finish', finishBody)],
    [registryKey('101', 'event-0005'), operation('101', 'event-0005', 1, '/me/sessions/55/sets', JSON.stringify({
      expectedVersion: 2, exerciseEntryKeys: ['entry-0001', 'entry-0002'], sets: [
        { recordKey: 'record-001', exerciseEntryKey: 'entry-0001', exerciseName: '深蹲', kind: 'weight', setIndex: 1,
          weightKg: 42.5, reps: 8, completed: true },
        { recordKey: 'record-002', exerciseEntryKey: 'entry-0002', exerciseId: '77', kind: 'bodyweight', setIndex: 2,
          reps: 12, completed: true },
      ],
    }))],
  ]
  for (const [key, value] of cases) assert.equal(await store.createIfAbsent(key, value), true)
  for (const [key, value] of cases) assert.equal(await store.read(key), value)
})

test('rejects unknown fields independently for create, set batch, nested set, and finish bodies', async () => {
  const cases = [
    ['event-0001', '/me/sessions', { source: 'free', date: '2026-10-31', clientEventId: 'event-0001', deviceProof: 'synthetic-proof' }],
    ['event-0002', '/me/sessions', { source: 'free', date: '2026-10-31', clientEventId: 'event-0002', approvalSecret: 'synthetic-secret' }],
    ['event-0006', '/me/sessions', { source: 'plan', date: '2026-10-31', clientEventId: 'event-0006',
      planId: 20, planVersion: 3, planDayId: '9' }],
    ['event-0003', '/me/sessions/55/sets', { expectedVersion: 1, exerciseEntryKeys: [], deviceProof: 'synthetic-proof' }],
    ['event-0004', '/me/sessions/55/sets', { expectedVersion: 1, exerciseEntryKeys: ['entry-0001'], sets: [{
      recordKey: 'record-001', exerciseEntryKey: 'entry-0001', exerciseName: '深蹲', kind: 'weight', setIndex: 1,
      weightKg: 42.5, reps: 8, completed: true, approvalSecret: 'synthetic-secret',
    }] }],
    ['event-0007', '/me/sessions/55/sets', { expectedVersion: 1, exerciseEntryKeys: ['entry-0001'], sets: [{
      recordKey: 'record-001', exerciseEntryKey: 'entry-0001', exerciseName: '深蹲', kind: 'weight', setIndex: 1,
      weightKg: 42.5, reps: 8, completed: true, meta: { sessionToken: 'synthetic-token' },
    }] }],
    ['event-0005', '/me/sessions/55/finish', { expectedVersion: 2, note: 'ok', token: 'synthetic-token' }],
  ]
  for (const [event, path, body] of cases) {
    const backend = native()
    const store = makeStore(backend)
    const value = operation('101', event, 1, path, JSON.stringify(body))
    await assert.rejects(store.createIfAbsent(registryKey('101', event), value),
      error => error.code === 'invalid_operation')
    assert.equal(backend.calls, 0)
  }
})

test('rejects escaped lone surrogates after parsing at operation and request-body depth', async () => {
  const key = registryKey()
  const malformedOperations = [
    operation('101', 'event-0001', 1, '/me/sessions', freeCreateBody('event-0001')).replace('session-1', '\ud800'),
    operation('101', 'event-0001', 1, '/me/sessions', freeCreateBody('event-0001')).replace('session-1', '\udc00'),
    operation('101', 'event-0001', 1, '/me/sessions/55/finish', '{"expectedVersion":1,"note":"\\ud800"}'),
  ]
  for (const [index, value] of malformedOperations.entries()) {
    const backend = native()
    await assert.rejects(makeStore(backend).createIfAbsent(key, value), error => error.code === 'invalid_operation', `escaped surrogate case ${index}`)
    assert.equal(backend.calls, 0)
  }
  const corruptRegistry = JSON.stringify({ schema: 1, entries: { [key]: malformedOperations[0] } })
  const backend = native(corruptRegistry)
  await assert.rejects(makeStore(backend).read(key), error => error.code === 'corrupt')
  assert.equal(backend.value, corruptRegistry)
  assert.equal(backend.calls, 0)
})

test('enforces kind-specific required and forbidden metrics, decimal precision, and completed groups', async () => {
  const base = { recordKey: 'record-001', exerciseEntryKey: 'entry-0001', exerciseName: '深蹲', setIndex: 1, completed: true }
  const valid = [
    { ...base, kind: 'weight', weightKg: 42.5, reps: 8, rpe: 7.5 },
    { ...base, kind: 'weight', weightKg: 12.55, reps: 8, rpe: 7.5 },
    { ...base, kind: 'perSide', weightKg: 20, reps: 8 },
    { ...base, kind: 'bodyweight', reps: 12 },
    { ...base, kind: 'bodyweightPerSide', reps: 12 },
    { ...base, kind: 'breath', reps: 10 },
    { ...base, kind: 'duration', seconds: 30 },
    { ...base, kind: 'durationPerSide', seconds: 30 },
  ]
  for (const [index, set] of valid.entries()) {
    const event = `event-${String(index).padStart(4, '0')}`
    const body = JSON.stringify({ expectedVersion: 1, exerciseEntryKeys: ['entry-0001'], sets: [set] })
    assert.equal(await makeStore(native()).createIfAbsent(registryKey('101', event), operation('101', event, 1, '/me/sessions/55/sets', body)), true)
  }
  const invalid = [
    { ...base, kind: 'weight', reps: 8 },
    { ...base, kind: 'weight', weightKg: 42.501, reps: 8 },
    { ...base, kind: 'weight', weightKg: 42.50000000005, reps: 8 },
    { ...base, kind: 'weight', weightKg: 12.55, reps: 8, rpe: 7.500000000005 },
    { ...base, kind: 'weight', weightKg: 42.5, reps: 8, seconds: 30 },
    { ...base, kind: 'bodyweight', reps: 8, weightKg: 0 },
    { ...base, kind: 'breath', seconds: 30 },
    { ...base, kind: 'duration', reps: 8, seconds: 30 },
    { ...base, kind: 'durationPerSide', seconds: 30, weightKg: 1 },
    { ...base, kind: 'bodyweightPerSide', reps: 8, completed: false },
    { ...base, kind: 'bodyweight', reps: 8, rpe: 7.55 },
  ]
  for (const [index, set] of invalid.entries()) {
    const event = `event-${String(index + 10).padStart(4, '0')}`
    const body = JSON.stringify({ expectedVersion: 1, exerciseEntryKeys: ['entry-0001'], sets: [set] })
    const backend = native()
    await assert.rejects(makeStore(backend).createIfAbsent(registryKey('101', event), operation('101', event, 1, '/me/sessions/55/sets', body)),
      error => error.code === 'invalid_operation')
    assert.equal(backend.calls, 0)
  }
})

test('rejects noncanonical exponent spellings and canonical exponential numbers outside bounds', async () => {
  const base = { recordKey: 'record-001', exerciseEntryKey: 'entry-0001', exerciseName: '深蹲', kind: 'weight', setIndex: 1, reps: 8, completed: true }
  const rawExponentBody = '{"expectedVersion":1,"exerciseEntryKeys":["entry-0001"],"sets":[{"recordKey":"record-001","exerciseEntryKey":"entry-0001","exerciseName":"深蹲","kind":"weight","setIndex":1,"weightKg":42.5e0,"reps":8,"completed":true}]}'
  const rawBackend = native()
  await assert.rejects(makeStore(rawBackend).createIfAbsent(registryKey('101', 'event-0001'),
    operation('101', 'event-0001', 1, '/me/sessions/55/sets', rawExponentBody)), error => error.code === 'invalid_operation')
  assert.equal(rawBackend.calls, 0)

  for (const weightKg of [1e-7, 1e21]) {
    const body = JSON.stringify({ expectedVersion: 1, exerciseEntryKeys: ['entry-0001'], sets: [{ ...base, weightKg }] })
    const backend = native()
    await assert.rejects(makeStore(backend).createIfAbsent(registryKey('101', 'event-0002'),
      operation('101', 'event-0002', 1, '/me/sessions/55/sets', body)), error => error.code === 'invalid_operation')
    assert.equal(backend.calls, 0)
  }
})

test('rejects statically detectable batch key, order-membership, index, and batch-size conflicts', async () => {
  const baseSet = (recordKey, exerciseEntryKey = 'entry-0001', setIndex = 1) => ({
    recordKey, exerciseEntryKey, exerciseName: '深蹲', kind: 'weight', setIndex, weightKg: 42.5, reps: 8, completed: true,
  })
  const cases = [
    { exerciseEntryKeys: ['entry-0001'], sets: [baseSet('record-001'), baseSet('record-001', 'entry-0001', 2)] },
    { exerciseEntryKeys: ['entry-0001'], sets: [baseSet('record-001'), baseSet('record-002')] },
    { exerciseEntryKeys: ['entry-0001'], sets: [baseSet('record-001'), baseSet('record-002', 'entry-0001', 1)] },
    { exerciseEntryKeys: ['entry-0001'], sets: [baseSet('record-001'), { ...baseSet('record-002', 'entry-0001', 2), exerciseId: '77', exerciseName: undefined }] },
    { exerciseEntryKeys: ['entry-0002'], sets: [baseSet('record-001')] },
    { exerciseEntryKeys: ['entry-0001'], sets: [baseSet('record-001')], deleteRecordKeys: ['record-001'] },
    { exerciseEntryKeys: ['entry-0001'], sets: [baseSet('record-001')], restoreRecordKeys: ['record-002'] },
    { exerciseEntryKeys: ['entry-0001'], sets: Array.from({ length: 101 }, (_, i) => baseSet(`record-${String(i).padStart(3, '0')}`, 'entry-0001', i + 1)),
      deleteRecordKeys: Array.from({ length: 100 }, (_, i) => `deleted-${String(i).padStart(3, '0')}`) },
  ]
  for (const [index, batch] of cases.entries()) {
    const event = `event-${String(index).padStart(4, '0')}`
    const backend = native()
    const body = JSON.stringify({ expectedVersion: 1, ...batch })
    await assert.rejects(makeStore(backend).createIfAbsent(registryKey('101', event), operation('101', event, 1, '/me/sessions/55/sets', body)),
      error => error.code === 'invalid_operation')
    assert.equal(backend.calls, 0)
  }
  const validRestore = { expectedVersion: 1, exerciseEntryKeys: ['entry-0001'], sets: [baseSet('record-001')], restoreRecordKeys: ['record-001'] }
  assert.equal(await makeStore(native()).createIfAbsent(registryKey('101', 'event-0100'),
    operation('101', 'event-0100', 1, '/me/sessions/55/sets', JSON.stringify(validRestore))), true)
  const atLimit = { expectedVersion: 1, exerciseEntryKeys: ['entry-0001'],
    sets: Array.from({ length: 100 }, (_, i) => baseSet(`record-${String(i).padStart(3, '0')}`, 'entry-0001', i + 1)),
    deleteRecordKeys: Array.from({ length: 100 }, (_, i) => `deleted-${String(i).padStart(3, '0')}`) }
  assert.equal(await makeStore(native()).createIfAbsent(registryKey('101', 'event-0101'),
    operation('101', 'event-0101', 1, '/me/sessions/55/sets', JSON.stringify(atLimit))), true)
})

test('keeps old valid same-subject journal rows readable after reauthentication', async () => {
  const key = registryKey('101', 'event-0001')
  const value = operation('101', 'event-0001', 1, '/me/sessions', freeCreateBody('event-0001'))
  const backend = native(JSON.stringify({ schema: 1, entries: { [key]: value } }))
  assert.equal(await makeStore(backend, auth('101', 'session-2')).read(key), value)
  assert.equal(backend.calls, 0)
})

test('does not discard legacy stored payloads with newly forbidden unknown fields', async () => {
  const key = registryKey()
  const value = operation('101', 'event-0001', 1, '/me/sessions/55/sets', JSON.stringify({
    expectedVersion: 1, exerciseEntryKeys: [], deviceProof: 'synthetic-proof',
  }))
  const original = JSON.stringify({ schema: 1, entries: { [key]: value } })
  const backend = native(original)
  await assert.rejects(makeStore(backend).read(key), error => error.code === 'corrupt')
  assert.equal(backend.value, original)
  assert.equal(backend.calls, 0)
})

test('rejects malformed and duplicate-key registries without overwriting the native value', async () => {
  for (const value of ['{broken', '{"schema":1,"schema":1,"entries":{}}', JSON.stringify({ schema: 2, entries: {} })]) {
    const backend = native(value)
    const store = makeStore(backend)
    await assert.rejects(store.read(registryKey()), error => error.code === 'corrupt')
    assert.equal(backend.value, value)
    assert.equal(backend.calls, 0)
  }
})

test('fails closed on native unavailable and indeterminate write results', async () => {
  const backend = native()
  const store = makeStore(backend)
  backend.failNext = 'unavailable'
  await assert.rejects(store.createIfAbsent(registryKey(), operation()), error => error.code === 'unavailable')
  backend.failNext = 'throw-after-commit'
  await assert.rejects(store.createIfAbsent(registryKey(), operation()), error => error.code === 'unavailable')
  assert.equal(await store.read(registryKey()), operation(), 'caller can inspect committed state after unknown outcome')
})

test('enforces whole-registry 64 KiB and entry-count limits before native write', async () => {
  const backend = native()
  const store = makeStore(backend)
  const key = registryKey()
  const oversized = JSON.stringify({
    ...JSON.parse(operation()),
    request: { method: 'POST', path: '/me/sessions/55/sets', body: JSON.stringify({ payload: 'x'.repeat(65_400) }), idempotencyKey: 'idem-key-0001' },
  })
  await assert.rejects(store.createIfAbsent(key, oversized), error => error.code === 'too_large')
  assert.equal(backend.calls, 0)
  const many = Object.create(null)
  for (let i = 0; i < 65; i++) {
    const event = `event-${String(i).padStart(4, '0')}`
    many[registryKey('101', event)] = operation('101', event)
  }
  backend.value = JSON.stringify({ schema: 1, entries: many })
  await assert.rejects(store.read(key), error => error.code === 'corrupt')
})

test('native CAS conflicts are retried, but repeated conflicts are unavailable', async () => {
  const backend = native()
  const originalCAS = backend.compareAndSetTrainingOperationJournal
  let conflicts = 0
  backend.compareAndSetTrainingOperationJournal = function (found, expected, next) {
    if (conflicts++ < 2) return { ok: false, errorCode: 'conflict', found: false, value: '' }
    return originalCAS.call(this, found, expected, next)
  }
  const store = makeStore(backend)
  assert.equal(await store.createIfAbsent(registryKey(), operation()), true)

  const alwaysConflicts = native()
  alwaysConflicts.compareAndSetTrainingOperationJournal = () => ({ ok: false, errorCode: 'conflict', found: false, value: '' })
  await assert.rejects(makeStore(alwaysConflicts).createIfAbsent(registryKey(), operation()),
    error => error.code === 'unavailable')
})

test('logout or account switch during native read stops before a write', async () => {
  for (const replacement of [null, auth('202', 'session-2')]) {
    const lease = auth()
    const backend = native()
    const originalRead = backend.readTrainingOperationJournal
    backend.readTrainingOperationJournal = function () {
      const result = originalRead.call(this)
      queueMicrotask(() => {
        lease.subjectId = replacement?.subjectId ?? ''
        lease.generation = replacement?.generation ?? ''
      })
      return result
    }
    const store = makeStore(backend, lease)
    await assert.rejects(store.createIfAbsent(registryKey(), operation()), error => error.code === 'session_changed')
    assert.equal(backend.calls, 0)
  }
})

test('session change during CAS reports session_changed and a fresh same-subject generation can recover', async () => {
  const lease = auth()
  const backend = native()
  const originalCAS = backend.compareAndSetTrainingOperationJournal
  backend.compareAndSetTrainingOperationJournal = function (...args) {
    const result = originalCAS.apply(this, args)
    lease.generation = 'session-2'
    return result
  }
  const oldStore = makeStore(backend, lease)
  await assert.rejects(oldStore.createIfAbsent(registryKey(), operation()), error => error.code === 'session_changed')
  assert.equal(await makeStore(backend, auth('101', 'session-2')).read(registryKey()), operation())
  await assert.rejects(oldStore.read(registryKey()), error => error.code === 'session_changed')
})

test('accepts a registry of exactly 65,536 UTF-8 bytes and rejects the next byte', async () => {
  const entries = Object.create(null)
  for (let index = 0; index < 29; index++) {
    const event = `event-${String(index).padStart(4, '0')}`
    entries[registryKey('101', event)] = operation('101', event, 1, '/me/sessions/55/finish',
      JSON.stringify({ expectedVersion: 1, note: 'x'.repeat(1800) }))
  }
  const lastEvent = 'event-0029'
  const lastKey = registryKey('101', lastEvent)
  entries[lastKey] = operation('101', lastEvent, 1, '/me/sessions/55/finish',
    JSON.stringify({ expectedVersion: 1, note: `${'x'.repeat(1362)}${'é'.repeat(50)}` }))
  const registryBytes = value => Buffer.byteLength(value, 'utf8')
  const exactRegistry = JSON.stringify({ schema: 1, entries })
  assert.equal(registryBytes(exactRegistry), 65_536)
  const backend = native()
  const store = makeStore(backend)
  backend.value = JSON.stringify({ schema: 1, entries: Object.create(null) })
  for (const [key, value] of Object.entries(entries)) assert.equal(await store.createIfAbsent(key, value), true)
  assert.equal(Buffer.byteLength(backend.value, 'utf8'), 65_536)
  const secondKey = registryKey('101', 'event-0002')
  const extraEvent = 'event-0030'
  await assert.rejects(store.createIfAbsent(registryKey('101', extraEvent), operation('101', extraEvent, 1, '/me/sessions/55/finish',
    JSON.stringify({ expectedVersion: 1, note: 'x' }))), error => error.code === 'too_large')
  assert.equal(Buffer.byteLength(backend.value, 'utf8'), 65_536)
})

test('unknown native outcomes are not reported as conflicts or successes', () => {
  assert.throws(() => new SecureOperationJournalStorage(null, auth()), error => error instanceof SecureJournalStorageError && error.code === 'unavailable')
})

test('protected storage accepts v2 blank sets and rejects malformed v2 requests', async () => {
  const key = registryKey()
  const bodies = [
    ['/me/recordings', {source:'free',date:'2026-11-04',clientEventId:'event-0001',mode:'after_workout'}],
    ['/me/recordings/55/sets', {expectedVersion:1,exerciseEntryKeys:['entry-0001'],sets:[{recordKey:'record-001',exerciseEntryKey:'entry-0001',setIndex:1,confirmed:false,measurement:{method:'total'}}]}],
    ['/me/recordings/55/finish', {expectedVersion:2}],
  ]
  for (const [path,body] of bodies) {
    const store = makeStore(native()); const value = operation('101','event-0001',1,path,JSON.stringify(body))
    assert.equal(await store.createIfAbsent(key,value),true); assert.equal(await store.read(key),value)
  }
  const store=makeStore(native())
  const invalid=operation('101','event-0001',1,'/me/recordings/55/sets',JSON.stringify({expectedVersion:1,exerciseEntryKeys:[],extra:1}))
  await assert.rejects(store.createIfAbsent(key,invalid),error=>error.code==='invalid_operation')
})
