import assert from 'node:assert/strict'
import { test } from 'node:test'
import { SecureOperationJournalStorage } from './secureOperationJournalStorage.ts'

const key = 'nerae.workout.operation.v1.101.event-0001'
const otherKey = 'nerae.workout.operation.v1.101.event-0002'
const operation = (event = 'event-0001', revision = 1) => JSON.stringify({
  schema: 1, subjectId: '101', clientEventId: event, createdGeneration: 'session-1', revision,
  state: 'ready', createdAt: 1000, expiresAt: 2000,
  request: { method: 'POST', path: '/me/sessions', idempotencyKey: 'idem-key-0001',
    body: JSON.stringify({ source: 'free', date: '2026-10-31', clientEventId: event }) },
})
function auth() {
  return { subjectId: '101', generation: 'session-1', current() { return { subjectId: this.subjectId, generation: this.generation } },
    isCurrent(subject, generation) { return subject === this.subjectId && generation === this.generation } }
}
function native() {
  return {
    value: null, revision: 0n, outcome: '',
    readTrainingOperationJournal() { return { ok: true, errorCode: '', found: this.value !== null, value: this.value ?? '' } },
    compareAndSetTrainingOperationJournal(found, value, next) {
      if ((this.value !== null) !== found || (this.value ?? '') !== value) return { ok: false, errorCode: 'conflict', found: false, value: '' }
      this.value = next; this.revision++
      return { ok: true, errorCode: '', found: false, value: '' }
    },
    readVersionedTrainingOperationJournal() {
      return { ...this.readTrainingOperationJournal(), revision: String(this.revision) }
    },
    compareAndSetVersionedTrainingOperationJournal(revision, found, value, newFound, newValue) {
      if (String(this.revision) !== revision || (this.value !== null) !== found || (this.value ?? '') !== value) {
        return { ok: false, errorCode: 'conflict', found: false, value: '', revision: '' }
      }
      this.value = newFound ? newValue : null; this.revision++
      if (this.outcome === 'unknown') throw Error('synthetic unknown after commit')
      if (this.outcome === 'wrong-revision') return { ok: true, errorCode: '', found: false, value: '', revision: '999' }
      return { ok: true, errorCode: '', found: false, value: '', revision: String(this.revision) }
    },
  }
}
const isCode = code => error => error.code === code

test('versioned snapshot updates once and cannot be forged or used by another adapter', async () => {
  const backend = native(), credentials = auth()
  const store = new SecureOperationJournalStorage(backend, credentials)
  const other = new SecureOperationJournalStorage(backend, credentials)
  assert.equal(await store.readSnapshot(key), null)
  await store.createIfAbsent(key, operation())
  const snapshot = await store.readSnapshot(key)
  assert.equal(snapshot.value, operation())
  await assert.rejects(store.compareAndSwapSnapshot({ value: snapshot.value }, null), isCode('invalid_operation'))
  await assert.rejects(other.compareAndSwapSnapshot(snapshot, null), isCode('invalid_operation'))
  assert.equal(await store.compareAndSwapSnapshot(snapshot, operation('event-0001', 2)), true)
  await assert.rejects(store.compareAndSwapSnapshot(snapshot, null), isCode('invalid_operation'))
  assert.equal(await store.read(key), operation('event-0001', 2))
})

test('old snapshot fails after A→B→A, identical writes, and delete/recreate', async () => {
  const backend = native(), store = new SecureOperationJournalStorage(backend, auth())
  const first = operation(), second = operation('event-0001', 2)
  await store.createIfAbsent(key, first)
  let snapshot = await store.readSnapshot(key)
  await store.compareAndSwap(key, first, second)
  await store.compareAndSwap(key, second, first)
  assert.equal(await store.compareAndSwapSnapshot(snapshot, null), false)
  assert.equal(await store.read(key), first)
  snapshot = await store.readSnapshot(key)
  await store.compareAndSwap(key, first, first)
  assert.equal(await store.compareAndSwapSnapshot(snapshot, null), false)
  snapshot = await store.readSnapshot(key)
  await store.compareAndSwap(key, first, null)
  await store.createIfAbsent(key, first)
  assert.equal(await store.compareAndSwapSnapshot(snapshot, null), false)
  assert.equal(await store.read(key), first)
})

test('any other entry mutation conservatively invalidates the slot snapshot', async () => {
  const backend = native(), store = new SecureOperationJournalStorage(backend, auth())
  await store.createIfAbsent(key, operation())
  const snapshot = await store.readSnapshot(key)
  await store.createIfAbsent(otherKey, operation('event-0002'))
  assert.equal(await store.compareAndSwapSnapshot(snapshot, null), false)
  assert.equal(await store.read(key), operation())
})

test('changed credentials, uncertain commit and malformed revision fail closed', async () => {
  const backend = native(), credentials = auth(), store = new SecureOperationJournalStorage(backend, credentials)
  await store.createIfAbsent(key, operation())
  let snapshot = await store.readSnapshot(key)
  credentials.generation = 'new-session'
  await assert.rejects(store.compareAndSwapSnapshot(snapshot, null), isCode('session_changed'))
  credentials.generation = 'session-1'
  await assert.rejects(store.compareAndSwapSnapshot(snapshot, null), isCode('invalid_operation'))
  snapshot = await store.readSnapshot(key)
  credentials.subjectId = '202'
  await assert.rejects(store.compareAndSwapSnapshot(snapshot, null), isCode('session_changed'))
  credentials.subjectId = '101'
  await assert.rejects(store.compareAndSwapSnapshot(snapshot, null), isCode('invalid_operation'))
  snapshot = await store.readSnapshot(key)
  backend.outcome = 'unknown'
  await assert.rejects(store.compareAndSwapSnapshot(snapshot, null), isCode('unavailable'))
  await assert.rejects(store.compareAndSwapSnapshot(snapshot, null), isCode('invalid_operation'))
  assert.equal(await store.read(key), null, 'unknown can mean native commit, never report success')
  backend.outcome = ''
  await store.createIfAbsent(key, operation())
  snapshot = await store.readSnapshot(key)
  backend.outcome = 'wrong-revision'
  await assert.rejects(store.compareAndSwapSnapshot(snapshot, null), isCode('unavailable'))
  await assert.rejects(store.compareAndSwapSnapshot(snapshot, null), isCode('invalid_operation'))
})

test('unsupported versioning and invalid native revision fail closed', async () => {
  const backend = native(), store = new SecureOperationJournalStorage(backend, auth())
  await store.createIfAbsent(key, operation())
  const original = backend.readVersionedTrainingOperationJournal
  for (const revision of ['', '00', '-1', '9223372036854775808', '0']) {
    backend.readVersionedTrainingOperationJournal = () => ({ ok: true, errorCode: '', found: true, value: backend.value, revision })
    await assert.rejects(store.readSnapshot(key), isCode('unavailable'))
  }
  backend.readVersionedTrainingOperationJournal = undefined
  await assert.rejects(store.readSnapshot(key), isCode('unavailable'))
  backend.readVersionedTrainingOperationJournal = original
  assert.equal((await store.readSnapshot(key)).value, operation())
})
