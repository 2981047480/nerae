import assert from 'node:assert/strict'
import { registerHooks } from 'node:module'
import { test } from 'node:test'

const state = { value: null, revision: 0, claimMode: 'success', claims: [] }
const failure = errorCode => ({ ok: false, errorCode, found: false, value: '', revision: '' })
globalThis.__neraeJournalPort = {
  readTrainingOperationJournal() {
    return { ok: true, errorCode: '', found: state.value !== null, value: state.value ?? '' }
  },
  compareAndSetTrainingOperationJournal(found, expected, next) {
    if ((state.value !== null) !== found || (state.value ?? '') !== expected) return failure('conflict')
    state.value = next
    state.revision++
    return { ok: true, errorCode: '', found: false, value: '' }
  },
  readVersionedTrainingOperationJournal() {
    return { ...this.readTrainingOperationJournal(), revision: String(state.revision) }
  },
  compareAndSetVersionedTrainingOperationJournal() { throw new Error('not used by this test') },
  claimTrainingOperationDispatch(revision, expected, key, next) {
    state.claims.push({ revision, expected, key, next })
    if (state.claimMode === 'unsupported') return failure('unsupported_platform')
    if (revision !== String(state.revision) || expected !== state.value) return failure('conflict')
    state.value = next
    state.revision++
    if (state.claimMode === 'unknown') return failure('unavailable')
    return { ok: true, errorCode: '', found: false, value: '', revision: String(state.revision) }
  },
}
registerHooks({ resolve(specifier, context, nextResolve) {
  if (specifier !== '@/uni_modules/nerae-secure-vault') return nextResolve(specifier, context)
  const names = ['readTrainingOperationJournal', 'compareAndSetTrainingOperationJournal',
    'readVersionedTrainingOperationJournal', 'compareAndSetVersionedTrainingOperationJournal',
    'claimTrainingOperationDispatch']
  const source = names.map(name => `export const ${name} = (...args) => globalThis.__neraeJournalPort.${name}(...args)`).join('\n')
  return { url: `data:text/javascript,${encodeURIComponent(source)}`, shortCircuit: true }
} })
const { createNativeTrainingJournalStorage } = await import('./nativeTrainingJournalStorage.ts')
const { TrainingOperationJournal } = await import('../../features/training/operationJournal.ts')
const auth = { current: () => ({ subjectId: '101', generation: 'gen-a' }),
  isCurrent: (subject, generation) => subject === '101' && generation === 'gen-a' }
const eventId = 'event-bridge-01'
const request = { method: 'POST', path: '/me/sessions', idempotencyKey: 'idem-bridge-0001',
  body: JSON.stringify({ source: 'free', date: '2026-11-03', clientEventId: eventId }) }

function setup(mode) {
  Object.assign(state, { value: null, revision: 0, claimMode: mode, claims: [] })
  const storage = createNativeTrainingJournalStorage(auth)
  return new TrainingOperationJournal(storage, auth, { retentionMs: 86_400_000, now: () => 1_798_100_000_000 })
}

test('app journal factory forwards an exact staged operation to the native claim port', async () => {
  const journal = setup('success')
  await journal.stage(eventId, request)
  const staged = state.value
  const permit = await journal.beginSend(eventId)
  assert.equal(state.claims.length, 1)
  assert.equal(state.claims[0].revision, '1')
  assert.equal(state.claims[0].expected, staged)
  assert.equal(state.claims[0].key, `nerae.workout.operation.v1.101.${eventId}`)
  assert.equal(state.claims[0].next, state.value)
  assert.deepEqual(permit.operation.request, request)
  assert.equal(permit.isCurrent(), true)
})

test('unsupported native claim leaves the staged request unsent', async () => {
  const journal = setup('unsupported')
  await journal.stage(eventId, request)
  const staged = state.value
  await assert.rejects(journal.beginSend(eventId), error => error.code === 'unavailable')
  assert.equal(state.value, staged)
  assert.equal((await journal.readPending(eventId)).state, 'ready')
})

test('an unknown native claim outcome never issues a permit or repeats the send', async () => {
  const journal = setup('unknown')
  await journal.stage(eventId, request)
  await assert.rejects(journal.beginSend(eventId), error => error.code === 'unavailable')
  assert.equal((await journal.readPending(eventId)).state, 'in_flight')
  await assert.rejects(journal.beginSend(eventId), error => error.code === 'reconciliation_required')
  assert.equal(state.claims.length, 1)
})
