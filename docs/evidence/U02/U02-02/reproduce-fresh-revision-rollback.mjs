import assert from 'node:assert/strict'
import { TrainingOperationJournal } from '../../../../apps/client/src/features/training/operationJournal.ts'
import { SecureOperationJournalStorage } from '../../../../apps/client/src/features/training/secureOperationJournalStorage.ts'

const eventId = 'event-0001'
const key = `nerae.workout.operation.v1.101.${encodeURIComponent(eventId)}`
const body = JSON.stringify({ source: 'free', date: '2026-10-31', clientEventId: eventId, timeZone: 'Asia/Shanghai' })
const request = { method: 'POST', path: '/me/sessions', body, idempotencyKey: 'idem-key-0001' }

function native() {
  return {
    value: null,
    revision: 0n,
    readTrainingOperationJournal() {
      return { ok: true, errorCode: '', found: this.value !== null, value: this.value ?? '' }
    },
    compareAndSetTrainingOperationJournal(found, expected, next) {
      if ((this.value !== null) !== found || (this.value ?? '') !== expected) {
        return { ok: false, errorCode: 'conflict', found: false, value: '' }
      }
      this.value = next
      this.revision++
      return { ok: true, errorCode: '', found: false, value: '' }
    },
    readVersionedTrainingOperationJournal() {
      return { ...this.readTrainingOperationJournal(), revision: String(this.revision) }
    },
    compareAndSetVersionedTrainingOperationJournal(revision, found, expected, newFound, next) {
      if (String(this.revision) !== revision || (this.value !== null) !== found || (this.value ?? '') !== expected) {
        return { ok: false, errorCode: 'conflict', found: false, value: '', revision: '' }
      }
      this.value = newFound ? next : null
      this.revision++
      return { ok: true, errorCode: '', found: false, value: '', revision: String(this.revision) }
    },
  }
}

const credentials = {
  subjectId: '101', generation: 'session-1',
  current() { return { subjectId: this.subjectId, generation: this.generation } },
  isCurrent(subject, generation) { return this.subjectId === subject && this.generation === generation },
}
const backend = native()
const storage = new SecureOperationJournalStorage(backend, credentials)
const makeJournal = () => new TrainingOperationJournal(storage, credentials,
  { retentionMs: 60_000, now: () => 1_000 })

const first = makeJournal()
await first.stage(eventId, request)
const stagedReadyBytes = await storage.read(key)
const originalPermit = await first.beginSend(eventId)
const dispatchedBytes = await storage.read(key)
assert.notEqual(dispatchedBytes, stagedReadyBytes)

// Reproduce a legacy caller restoring the old complete registry value. The
// native slot revision advances even though the per-event bytes return to R.
assert.equal(await storage.compareAndSwap(key, dispatchedBytes, stagedReadyBytes), true)
assert.equal(await storage.read(key), stagedReadyBytes)
const afterRollback = backend.revision
const freshSnapshot = await storage.readSnapshot(key)
assert.equal(freshSnapshot.value, stagedReadyBytes)

// A new revision-bound snapshot can now CAS the old ready bytes because they
// are the current contents. This demonstrates that revision alone has no
// native-owned per-event history.
const versionedResult = await storage.compareAndSwapSnapshot(freshSnapshot, dispatchedBytes)
assert.ok(versionedResult, 'fresh snapshot should CAS current ready bytes')
assert.ok(backend.revision > afterRollback)
const currentFlightBytes = await storage.read(key)
assert.equal(await storage.compareAndSwap(key, currentFlightBytes, stagedReadyBytes), true)

const restarted = makeJournal()
let permit
try { permit = await restarted.beginSend(eventId) }
catch (error) {
  if (['conflict', 'reconciliation_required', 'unavailable'].includes(error?.code)) {
    console.log(`PASS: fresh journal refused restored ready operation (${error.code})`)
    process.exitCode = 0
  } else throw error
}
if (permit) {
  assert.equal(permit.operation.state, 'in_flight')
  console.error('EXPECTED FAILURE: fresh journal issued a second permit after restoring previously dispatched ready bytes')
  process.exitCode = 1
}
void originalPermit
