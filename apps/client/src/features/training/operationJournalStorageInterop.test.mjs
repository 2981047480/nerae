import assert from 'node:assert/strict'
import { test } from 'node:test'
import { inspect } from 'node:util'
import { OperationJournalError, TrainingOperationJournal } from './operationJournal.ts'
import { SecureOperationJournalStorage } from './secureOperationJournalStorage.ts'

const eventId = 'event-interop-01'
const body = JSON.stringify({ source: 'free', date: '2026-10-31', timeZone: 'Asia/Shanghai', clientEventId: eventId })
const request = Object.freeze({ method: 'POST', path: '/me/sessions', body, idempotencyKey: 'idem-interop-0001' })
const keyFor = (subject, event = eventId) => `nerae.workout.operation.v1.${subject}.${encodeURIComponent(event)}`
const journalFor = (storage, auth, now = () => 1_798_100_000_000) =>
  new TrainingOperationJournal(storage, auth, { retentionMs: 86_400_000, now })

function makeAuth(subjectId = '101', generation = 'gen-a') {
  let credential = { subjectId, generation, accessToken: 'synthetic-token-never-persist' }
  return {
    current: () => credential && { ...credential },
    isCurrent: (subject, generationValue) => credential?.subjectId === subject && credential?.generation === generationValue,
    set(next) { credential = next && { ...next, accessToken: 'synthetic-token-never-persist' } },
  }
}

function nativePort(initial = null) {
  return {
    value: initial,
    revision: 0n,
    dispatched: new Set(),
    history: [],
    nextMode: null,
    afterRead: null,
    afterCAS: null,
    readTrainingOperationJournal() {
      const result = { ok: true, errorCode: '', found: this.value !== null, value: this.value ?? '' }
      const afterRead = this.afterRead
      this.afterRead = null
      afterRead?.()
      return result
    },
    compareAndSetTrainingOperationJournal(expectedFound, expectedValue, nextValue) {
      const matched = (this.value !== null) === expectedFound && (this.value ?? '') === expectedValue
      this.history.push({ expectedFound, expectedValue, nextValue, matched })
      if (!matched) return { ok: false, errorCode: 'conflict', found: false, value: '' }
      this.value = nextValue
      this.revision++
      const afterCAS = this.afterCAS
      this.afterCAS = null
      afterCAS?.()
      if (this.nextMode === 'commit_then_throw') {
        this.nextMode = null
        throw new Error('synthetic unknown native result')
      }
      if (this.nextMode === 'commit_then_error') {
        this.nextMode = null
        return { ok: false, errorCode: 'unavailable', found: false, value: '' }
      }
      return { ok: true, errorCode: '', found: false, value: '' }
    },
    readVersionedTrainingOperationJournal() {
      return { ...this.readTrainingOperationJournal(), revision: String(this.revision) }
    },
    claimTrainingOperationDispatch(revision, expectedValue, key, nextValue) {
      if (String(this.revision) !== revision || this.value !== expectedValue) {
        return { ok: false, errorCode: 'conflict', found: false, value: '', revision: '' }
      }
      const entry = JSON.parse(expectedValue).entries[key]
      const identity = `${key}\0${JSON.parse(entry).request.idempotencyKey}`
      if (this.dispatched.has(identity)) return { ok: false, errorCode: 'conflict', found: false, value: '', revision: '' }
      this.dispatched.add(identity)
      const result = this.compareAndSetTrainingOperationJournal(true, expectedValue, nextValue)
      return { ...result, revision: result.ok ? String(this.revision) : '' }
    },
  }
}

function makeStorage(native, auth) { return new SecureOperationJournalStorage(native, auth) }
function isCode(code) { return error => error instanceof OperationJournalError && error.code === code }

test('adapter stages before the synthetic monotonic claim and only then returns a permit', async () => {
  const native = nativePort()
  const auth = makeAuth()
  const storage = makeStorage(native, auth)
  const journal = journalFor(storage, auth)
  const staged = await journal.stage(eventId, request)
  assert.equal(staged.state, 'ready')
  assert.equal(native.history.length, 1)
  const firstStored = JSON.parse(native.history[0].nextValue)
  assert.equal(firstStored.entries[keyFor('101')], [...Object.values(firstStored.entries)][0])
  assert.equal(JSON.parse(firstStored.entries[keyFor('101')]).state, 'ready')

  const permit = await journal.beginSend(eventId)
  assert.equal(native.history.length, 2)
  const latest = JSON.parse(native.value).entries[keyFor('101')]
  assert.equal(JSON.parse(latest).state, 'in_flight')
  assert.equal(JSON.parse(latest).attemptGeneration, 'gen-a')
  assert.equal(permit.isCurrent(), true)
  assert.deepEqual(permit.operation.request, request)
  assert.equal(native.history[1].expectedValue, native.history[0].nextValue)
  await journal.markUncertain(permit)
})

test('current native port without monotonic claim never issues a permit or changes a staged request', async () => {
  const native = nativePort()
  native.claimTrainingOperationDispatch = undefined
  const auth = makeAuth()
  const journal = journalFor(makeStorage(native, auth), auth)
  await journal.stage(eventId, request)
  const stored = native.value
  await assert.rejects(journal.beginSend(eventId), isCode('unavailable'))
  assert.equal(native.value, stored)
  assert.equal((await journal.readPending(eventId)).state, 'ready')
  assert.equal(native.history.length, 1)
})

test('synthetic native claim refuses restored ready bytes after an old CAS or delete/recreate', async () => {
  const native = nativePort()
  const auth = makeAuth()
  const storage = makeStorage(native, auth)
  const first = journalFor(storage, auth)
  await first.stage(eventId, request)
  const key = keyFor('101')
  const ready = await storage.read(key)
  await first.beginSend(eventId)
  const inFlight = await storage.read(key)
  assert.equal(await storage.compareAndSwap(key, inFlight, ready), true)
  const restarted = journalFor(makeStorage(native, auth), auth)
  await assert.rejects(restarted.beginSend(eventId), isCode('conflict'))
  assert.equal(await storage.read(key), ready)
  assert.equal(await storage.compareAndSwap(key, ready, null), true)
  assert.equal(await storage.createIfAbsent(key, ready), true)
  await assert.rejects(restarted.beginSend(eventId), isCode('conflict'))
  assert.equal(await storage.read(key), ready)
})

test('competing adapters can obtain only one synthetic first-send permit', async () => {
  const native = nativePort()
  const auth = makeAuth()
  const first = journalFor(makeStorage(native, auth), auth)
  const second = journalFor(makeStorage(native, auth), auth)
  await first.stage(eventId, request)
  const outcomes = await Promise.allSettled([first.beginSend(eventId), second.beginSend(eventId)])
  assert.equal(outcomes.filter(outcome => outcome.status === 'fulfilled').length, 1)
  assert.equal(outcomes.filter(outcome => outcome.status === 'rejected').length, 1)
  assert.equal(native.dispatched.size, 1)
})

test('adapter rejects a claim that changes request content or skips the expected transition', async () => {
  const native = nativePort()
  const auth = makeAuth()
  const storage = makeStorage(native, auth)
  const journal = journalFor(storage, auth)
  await journal.stage(eventId, request)
  const key = keyFor('101')
  const ready = await storage.read(key)
  const parsed = JSON.parse(ready)
  const malformed = JSON.stringify({ ...parsed, revision: 2, state: 'in_flight',
    request: { ...parsed.request, idempotencyKey: 'changed-key-001' }, attemptGeneration: 'gen-a' })
  await assert.rejects(storage.claimFirstSend(key, ready, malformed), error => error.code === 'invalid_operation')
  const wrongLease = JSON.stringify({ ...parsed, revision: 2, state: 'in_flight', attemptGeneration: 'other-session' })
  await assert.rejects(storage.claimFirstSend(key, ready, wrongLease), error => error.code === 'invalid_operation')
  assert.equal(native.dispatched.size, 0)
  assert.equal(await storage.read(key), ready)
})

test('unknown native CAS outcome yields no permit and preserves the exact request for inspection', async () => {
  const native = nativePort()
  const auth = makeAuth()
  const journal = journalFor(makeStorage(native, auth), auth)
  await journal.stage(eventId, request)
  const before = JSON.parse(native.value).entries[keyFor('101')]
  native.nextMode = 'commit_then_throw'
  await assert.rejects(journal.beginSend(eventId), isCode('unavailable'))
  const persisted = JSON.parse(native.value).entries[keyFor('101')]
  assert.notEqual(persisted, before)
  assert.equal(JSON.parse(persisted).state, 'in_flight')
  assert.deepEqual(JSON.parse(persisted).request, request)
  assert.equal(native.history.length, 2)
  await assert.rejects(journal.beginSend(eventId), isCode('reconciliation_required'))
  assert.equal(native.history.length, 2, 'no second CAS / permit after the uncertain outcome')
})

test('same subject on a new generation may inspect uncertain bytes but cannot re-dispatch, even after clock rollback', async () => {
  const native = nativePort()
  const originalAuth = makeAuth('101', 'gen-a')
  const original = journalFor(makeStorage(native, originalAuth), originalAuth)
  await original.stage(eventId, request)
  const permit = await original.beginSend(eventId)
  await original.markUncertain(permit)
  const uncertainBytes = JSON.parse(native.value).entries[keyFor('101')]

  const restoredAuth = makeAuth('101', 'gen-b')
  let now = 1_700_000_000_000 // far earlier than the journal's creation time
  const restored = journalFor(makeStorage(native, restoredAuth), restoredAuth, () => now)
  const pending = await restored.readPending(eventId)
  assert.equal(pending.state, 'uncertain')
  assert.deepEqual(pending.request, request)
  await assert.rejects(restored.beginSend(eventId), isCode('reconciliation_required'))
  now = 1
  await assert.rejects(restored.beginSend(eventId), isCode('reconciliation_required'))
  assert.equal(JSON.parse(native.value).entries[keyFor('101')], uncertainBytes)
})

test('another account cannot read or send an existing subject registry entry', async () => {
  const native = nativePort()
  const firstAuth = makeAuth('101', 'gen-a')
  const first = journalFor(makeStorage(native, firstAuth), firstAuth)
  await first.stage(eventId, request)

  const otherAuth = makeAuth('202', 'gen-b')
  const other = journalFor(makeStorage(native, otherAuth), otherAuth)
  assert.equal(await other.readPending(eventId), null)
  await assert.rejects(other.beginSend(eventId), isCode('conflict'))
  assert.ok(JSON.parse(native.value).entries[keyFor('101')])
  assert.equal(JSON.parse(native.value).entries[keyFor('202')], undefined)
})

test('persisted native registry contains journal request data but no authentication credential material', async () => {
  const native = nativePort()
  const auth = makeAuth()
  const journal = journalFor(makeStorage(native, auth), auth)
  await journal.stage(eventId, request)
  const serialized = native.value
  const storedOperation = JSON.parse(serialized).entries[keyFor('101')]
  const storedRequest = JSON.parse(storedOperation).request
  assert.equal(storedRequest.body, body)
  assert.equal(storedRequest.idempotencyKey, request.idempotencyKey)
  for (const forbidden of ['synthetic-token-never-persist', 'accessToken', 'Authorization', 'Bearer']) {
    assert.equal(serialized.includes(forbidden), false, forbidden)
  }
  const pending = await journal.readPending(eventId)
  assert.equal(JSON.stringify(pending).includes(body), false)
  assert.equal(inspect(pending).includes(body), false)
})

test('native CAS error response after commit also fails closed and never produces a permit', async () => {
  const native = nativePort()
  const auth = makeAuth()
  const journal = journalFor(makeStorage(native, auth), auth)
  await journal.stage(eventId, request)
  native.nextMode = 'commit_then_error'
  await assert.rejects(journal.beginSend(eventId), isCode('unavailable'))
  assert.equal(JSON.parse(JSON.parse(native.value).entries[keyFor('101')]).state, 'in_flight')
  await assert.rejects(journal.beginSend(eventId), isCode('reconciliation_required'))
})

test('generation change during adapter read is preserved and does not consume the first-send state', async () => {
  const native = nativePort()
  const auth = makeAuth()
  const journal = journalFor(makeStorage(native, auth), auth)
  await journal.stage(eventId, request)
  const writesBeforeRead = native.history.length

  native.afterRead = () => auth.set({ subjectId: '101', generation: 'gen-b' })
  await assert.rejects(journal.beginSend(eventId), isCode('session_changed'))
  assert.equal(native.history.length, writesBeforeRead)
  assert.equal(JSON.parse(JSON.parse(native.value).entries[keyFor('101')]).state, 'ready')

  const recovered = journalFor(makeStorage(native, auth), auth)
  const permit = await recovered.beginSend(eventId)
  assert.equal(permit.isCurrent(), true)
  assert.deepEqual(permit.operation.request, request)
  await recovered.markUncertain(permit)
})

test('generation change after committing in-flight CAS returns session_changed and never grants a second permit', async () => {
  const native = nativePort()
  const auth = makeAuth()
  const journal = journalFor(makeStorage(native, auth), auth)
  await journal.stage(eventId, request)
  native.afterCAS = () => auth.set({ subjectId: '101', generation: 'gen-b' })

  await assert.rejects(journal.beginSend(eventId), isCode('session_changed'))
  assert.equal(native.history.length, 2)
  const stored = JSON.parse(native.value).entries[keyFor('101')]
  assert.equal(JSON.parse(stored).state, 'in_flight')
  assert.deepEqual(JSON.parse(stored).request, request)

  const recovered = journalFor(makeStorage(native, auth), auth)
  assert.equal((await recovered.readPending(eventId)).state, 'in_flight')
  await assert.rejects(recovered.beginSend(eventId), isCode('reconciliation_required'))
  assert.equal(native.history.length, 2, 'no new permit or CAS after the committed state became ambiguous')
})
