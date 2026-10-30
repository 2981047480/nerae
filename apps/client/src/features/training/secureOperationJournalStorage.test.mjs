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
  const store = new SecureOperationJournalStorage(backend)
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

test('isolates subject/event keys and retries unrelated native CAS conflicts', async () => {
  const backend = native()
  const store = new SecureOperationJournalStorage(backend)
  const keys = [registryKey('101', 'event-0001'), registryKey('202', 'event-0002')]
  const values = [operation('101', 'event-0001'), operation('202', 'event-0002')]
  await Promise.all(keys.map((key, index) => store.createIfAbsent(key, values[index])))
  assert.equal(await store.read(keys[0]), values[0])
  assert.equal(await store.read(keys[1]), values[1])
  assert.equal(Object.keys(JSON.parse(backend.value).entries).length, 2)
  assert.ok(backend.calls >= 2)
})

test('rejects cross-owner operations and malformed/noncanonical storage keys', async () => {
  const store = new SecureOperationJournalStorage(native())
  await assert.rejects(store.createIfAbsent(registryKey('101'), operation('202')), error => error.code === 'invalid_operation')
  await assert.rejects(store.read('nerae.workout.operation.v1.101.event-0001%2f'), error => error.code === 'invalid_operation')
  await assert.rejects(store.read('other-slot'), error => error.code === 'invalid_operation')
})

test('keeps canonical Unicode event IDs bound to their exact owner key', async () => {
  const event = '事件-id-01'
  const key = registryKey('101', event)
  const value = operation('101', event)
  const store = new SecureOperationJournalStorage(native())
  assert.equal(await store.createIfAbsent(key, value), true)
  assert.equal(await store.read(key), value)
})

test('rejects malformed and duplicate-key registries without overwriting the native value', async () => {
  for (const value of ['{broken', '{"schema":1,"schema":1,"entries":{}}', JSON.stringify({ schema: 2, entries: {} })]) {
    const backend = native(value)
    const store = new SecureOperationJournalStorage(backend)
    await assert.rejects(store.read(registryKey()), error => error.code === 'corrupt')
    assert.equal(backend.value, value)
    assert.equal(backend.calls, 0)
  }
})

test('fails closed on native unavailable and indeterminate write results', async () => {
  const backend = native()
  const store = new SecureOperationJournalStorage(backend)
  backend.failNext = 'unavailable'
  await assert.rejects(store.createIfAbsent(registryKey(), operation()), error => error.code === 'unavailable')
  backend.failNext = 'throw-after-commit'
  await assert.rejects(store.createIfAbsent(registryKey(), operation()), error => error.code === 'unavailable')
  assert.equal(await store.read(registryKey()), operation(), 'caller can inspect committed state after unknown outcome')
})

test('enforces whole-registry 64 KiB and entry-count limits before native write', async () => {
  const backend = native()
  const store = new SecureOperationJournalStorage(backend)
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
  const store = new SecureOperationJournalStorage(backend)
  assert.equal(await store.createIfAbsent(registryKey(), operation()), true)

  const alwaysConflicts = native()
  alwaysConflicts.compareAndSetTrainingOperationJournal = () => ({ ok: false, errorCode: 'conflict', found: false, value: '' })
  await assert.rejects(new SecureOperationJournalStorage(alwaysConflicts).createIfAbsent(registryKey(), operation()),
    error => error.code === 'unavailable')
})

test('unknown native outcomes are not reported as conflicts or successes', () => {
  assert.throws(() => new SecureOperationJournalStorage(null), error => error instanceof SecureJournalStorageError && error.code === 'unavailable')
})
