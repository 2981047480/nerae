import assert from 'node:assert/strict'
import { test } from 'node:test'
import { SecureJournalStorageError, SecureOperationJournalStorage } from './secureOperationJournalStorage.ts'

const registryKey = (subject = '101', event = 'event-0001') =>
  `nerae.workout.operation.v1.${subject}.${encodeURIComponent(event)}`
const operation = (subjectId = '101', clientEventId = 'event-0001', revision = 1) => JSON.stringify({
  schema: 1, subjectId, clientEventId, createdGeneration: 'session-1', revision,
  state: 'ready', createdAt: 1000, expiresAt: 2000,
  request: { method: 'POST', path: '/me/sessions/55/sets', body: '{"expectedVersion":1}', idempotencyKey: 'idem-key-0001' },
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
  const key = registryKey()
  const valueAt = size => JSON.stringify({
    schema: 1, subjectId: '101', clientEventId: 'event-0001', createdGeneration: 'session-1', revision: 1,
    state: 'ready', createdAt: 1000, expiresAt: 2000,
    request: { method: 'POST', path: '/me/sessions/55/sets',
      body: JSON.stringify({ payload: 'x'.repeat(size) }), idempotencyKey: 'idem-key-0001' },
  })
  const registryBytes = value => Buffer.byteLength(JSON.stringify({ schema: 1, entries: { [key]: value } }), 'utf8')
  let low = 0
  let high = 65_536
  while (low < high) {
    const middle = Math.ceil((low + high) / 2)
    if (registryBytes(valueAt(middle)) <= 65_536) low = middle
    else high = middle - 1
  }
  assert.equal(registryBytes(valueAt(low)), 65_536)
  const backend = native()
  const store = makeStore(backend)
  assert.equal(await store.createIfAbsent(key, valueAt(low)), true)
  assert.equal(Buffer.byteLength(backend.value, 'utf8'), 65_536)
  const secondKey = registryKey('101', 'event-0002')
  await assert.rejects(store.createIfAbsent(secondKey, operation('101', 'event-0002')), error => error.code === 'too_large')
  assert.equal(Buffer.byteLength(backend.value, 'utf8'), 65_536)
})

test('unknown native outcomes are not reported as conflicts or successes', () => {
  assert.throws(() => new SecureOperationJournalStorage(null, auth()), error => error instanceof SecureJournalStorageError && error.code === 'unavailable')
})
