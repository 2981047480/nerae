// Run only against the isolated TLS server and synthetic sessions provided by
// the Go test. This exercises the real client adapter, not an installed app.
import assert from 'node:assert/strict'
import { registerHooks } from 'node:module'
import { pathToFileURL } from 'node:url'
import path from 'node:path'

const required = ['NERAE_INTEROP_URL', 'NERAE_INTEROP_CLIENT_DIR', 'NERAE_INTEROP_SUBJECT_A',
  'NERAE_INTEROP_SUBJECT_B', 'NERAE_INTEROP_TOKEN_A', 'NERAE_INTEROP_TOKEN_AGAIN',
  'NERAE_INTEROP_TOKEN_B', 'NERAE_INTEROP_DATE']
if (required.some(name => !process.env[name])) throw new Error('isolated write interop setup incomplete')
const hooks = registerHooks({
  resolve(specifier, context, nextResolve) {
    if (specifier === './recording' && /\/(recordingApi|recordingProjection|draftStorage)\.ts$/.test(context.parentURL || '')) {
      return nextResolve('./recording.ts', context)
    }
    if (specifier === './recordingApi' && context.parentURL?.endsWith('/recordingProjection.ts')) {
      return nextResolve('./recordingApi.ts', context)
    }
    if (specifier === './operationJournal' && context.parentURL?.endsWith('/recordingApi.ts')) {
      return nextResolve('./operationJournal.ts', context)
    }
    if (/^\.\/(recording|recordingApi|recordingProjection|trainingEditorController|versionedEditorDraft|draftStorage|operationJournal|secureDraftStorage|cloudSessionRestore|freeSessionIntent|syncFreeSessionCreate)$/.test(specifier) &&
      context.parentURL?.includes('/features/training/')) {
      return nextResolve(`${specifier}.ts`, context)
    }
    return nextResolve(specifier, context)
  },
})
const featureDir = process.env.NERAE_INTEROP_CLIENT_DIR
const { RecordingApi } = await import(pathToFileURL(path.join(featureDir, 'recordingApi.ts')).href)
const { restoreConfirmedSession } = await import(pathToFileURL(path.join(featureDir, 'recordingProjection.ts')).href)
const { TrainingOperationJournal } = await import(pathToFileURL(path.join(featureDir, 'operationJournal.ts')).href)
const { TrainingEditorController } = await import(pathToFileURL(path.join(featureDir, 'trainingEditorController.ts')).href)
const { SecureDraftStorage } = await import(pathToFileURL(path.join(featureDir, 'secureDraftStorage.ts')).href)
const { dispatchFirstFreeSession } = await import(pathToFileURL(path.join(featureDir, 'firstFreeSessionDispatch.ts')).href)
const { restoreCloudSessionToLocalDraft } = await import(pathToFileURL(path.join(featureDir, 'cloudSessionRestore.ts')).href)
const { syncFreeSessionCreate } = await import(pathToFileURL(path.join(featureDir, 'syncFreeSessionCreate.ts')).href)
hooks.deregister()
const request = async options => {
  const response = await fetch(options.url, { method: options.method, headers: options.header, body: options.data })
  const data = await response.json()
  const headers = {}
  for (const name of ['idempotency-key-expires-at', 'idempotency-replayed', 'content-type', 'cache-control', 'x-request-id']) {
    const value = response.headers.get(name)
    if (value !== null) headers[name] = value
  }
  return { statusCode: response.status, data, headers }
}
const assertWriteExpiry = result => {
  assert.match(result.idempotencyKeyExpiresAt, /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d{1,9})?Z$/)
}
const make = (subjectId, accessToken, generation) => {
  const lease = { subjectId, generation }
  const credential = { ...lease, accessToken }
  const auth = { generation, current: () => credential, isCurrent: (id, g) => id === subjectId && g === generation }
  return { api: new RecordingApi(process.env.NERAE_INTEROP_URL, auth, request, lease), auth }
}
const first = make(process.env.NERAE_INTEROP_SUBJECT_A, process.env.NERAE_INTEROP_TOKEN_A, 'local-test-a')
const again = make(process.env.NERAE_INTEROP_SUBJECT_A, process.env.NERAE_INTEROP_TOKEN_AGAIN, 'local-test-again')
const other = make(process.env.NERAE_INTEROP_SUBJECT_B, process.env.NERAE_INTEROP_TOKEN_B, 'local-test-b')
const createInput = { date: process.env.NERAE_INTEROP_DATE, timeZone: 'UTC', clientEventId: 'client-write-event-001' }
const initial = await first.api.createFreeSession(createInput, 'client-create-key-001')
assertWriteExpiry(initial)
assert.equal(initial.detail.version, 1)
assert.equal(initial.detail.sets.length, 0)
const initialReplay = await first.api.createFreeSession(createInput, 'client-create-key-001')
assert.equal(initialReplay.idempotencyKeyExpiresAt, initial.idempotencyKeyExpiresAt)
assert.deepEqual(initialReplay.detail, initial.detail)
const firstSet = { exerciseName: '合成测试卧推', exerciseEntryKey: 'client-entry-001', recordKey: 'client-set-001',
  setIndex: 1, kind: 'weight', weightKg: 42.5, reps: 8, completed: true }
const firstSaved = await first.api.saveSets(initial.detail.id, 1, [firstSet], ['client-entry-001'], 'client-set-key-001', initial)
assertWriteExpiry(firstSaved)
assert.equal(firstSaved.detail.version, 2)
assert.equal(firstSaved.detail.sets.length, 1)
const savedReplay = await first.api.saveSets(initial.detail.id, 1, [firstSet], ['client-entry-001'], 'client-set-key-001', initial)
assert.equal(savedReplay.idempotencyKeyExpiresAt, firstSaved.idempotencyKeyExpiresAt)
assert.deepEqual(savedReplay.detail, firstSaved.detail)
const secondSet = { ...firstSet, recordKey: 'client-set-002', setIndex: 2, weightKg: 45, reps: 6 }
const secondSaved = await first.api.saveSets(initial.detail.id, 2, [secondSet], ['client-entry-001'], 'client-set-key-002', firstSaved)
assertWriteExpiry(secondSaved)
assert.equal(secondSaved.detail.version, 3)
assert.equal(secondSaved.detail.sets.length, 2)
const finished = await first.api.finishSession(initial.detail.id, { expectedVersion: 3 }, 'client-finish-key-001', secondSaved)
assertWriteExpiry(finished)
assert.equal(finished.detail.status, 'completed')
assert.equal(finished.detail.version, 4)
const readBack = await again.api.readSession(initial.detail.id)
assert.equal(Object.hasOwn(readBack, 'idempotencyKeyExpiresAt'), false)
const restored = restoreConfirmedSession(readBack, again.auth, process.env.NERAE_INTEROP_DATE)
assert.equal(restored.ok, true)
assert.equal(restored.draft.clientEventId, createInput.clientEventId)
assert.equal(restored.draft.server.status, 'completed')
assert.deepEqual(restored.draft.exercises[0].sets.map(set => [set.setIndex, set.weightKg, set.reps]), [[1, '42.5', '8'], [2, '45', '6']])
await assert.rejects(other.api.readSession(initial.detail.id), error => error.code === 'not_found')

// The other mode sends a complete batch after the workout, using the same
// persistence API and without inventing start, end or duration values.
const after = await first.api.createFreeSession({ ...createInput, clientEventId: 'after-workout-event-001' }, 'after-create-key-001')
assertWriteExpiry(after)
const afterSets = [
  { ...firstSet, exerciseEntryKey: 'after-entry-001', recordKey: 'after-set-001', exerciseName: '合成测试推举' },
  { exerciseName: '合成测试深蹲', exerciseEntryKey: 'after-entry-002', recordKey: 'after-set-002',
    setIndex: 1, kind: 'bodyweight', reps: 10, completed: true },
]
const afterSaved = await first.api.saveSets(after.detail.id, 1, afterSets,
  ['after-entry-002', 'after-entry-001'], 'after-batch-key-001', after)
assertWriteExpiry(afterSaved)
assert.equal(afterSaved.detail.version, 2)
assert.deepEqual(afterSaved.detail.exerciseEntryKeys, ['after-entry-002', 'after-entry-001'])
const afterFinished = await first.api.finishSession(after.detail.id, { expectedVersion: 2 }, 'after-finish-key-001', afterSaved)
assertWriteExpiry(afterFinished)
assert.equal(afterFinished.detail.status, 'completed')
const afterRead = await again.api.readSession(after.detail.id)
assert.equal(Object.hasOwn(afterRead, 'idempotencyKeyExpiresAt'), false)
const afterRestored = restoreConfirmedSession(afterRead, again.auth, process.env.NERAE_INTEROP_DATE)
assert.equal(afterRestored.ok, true)
assert.deepEqual(afterRestored.draft.exercises.map(item => item.exerciseEntryKey), ['after-entry-002', 'after-entry-001'])
assert.equal(afterRestored.draft.server.status, 'completed')
await assert.rejects(other.api.readSession(after.detail.id), error => error.code === 'not_found')

// Commit the original create on the real local server, discard its response,
// then use a second session of the same subject for replay-only recovery.
const storage = {
  values: new Map(),
  // This test-only in-memory claim establishes the fixture's sent state. It
  // does not implement or verify the native durable dispatch marker.
  dispatched: new Set(),
  async read(key) { return this.values.get(key) },
  async createIfAbsent(key, value) { if (this.values.has(key)) return false; this.values.set(key, value); return true },
  async compareAndSwap(key, before, after) {
    if (this.values.get(key) !== before) return false
    if (after === null) this.values.delete(key); else this.values.set(key, after)
    return true
  },
  async claimFirstSend(key, before, after) {
    const marker = `${key}\0${JSON.parse(before).request.idempotencyKey}`
    if (this.values.get(key) !== before || this.dispatched.has(marker)) return false
    this.values.set(key, after)
    this.dispatched.add(marker)
    return true
  },
}
const lostInput = { date: process.env.NERAE_INTEROP_DATE, timeZone: 'UTC', clientEventId: 'lost-client-event-001' }
const originalBody = JSON.stringify({ source: 'free', ...lostInput })
const journal = new TrainingOperationJournal(storage, first.auth, { retentionMs: 60_000 })
await journal.stage(lostInput.clientEventId, { method: 'POST', path: '/me/sessions', body: originalBody, idempotencyKey: 'lost-create-key-001' })
const permit = await journal.beginSend(lostInput.clientEventId)
const lostApi = new RecordingApi(process.env.NERAE_INTEROP_URL, first.auth, async options => {
  assert.equal(options.data, originalBody)
  const committed = await request(options)
  assert.equal(committed.statusCode, 201)
  throw new Error('synthetic response lost after server commit')
}, { subjectId: process.env.NERAE_INTEROP_SUBJECT_A, generation: 'local-test-a' })
await assert.rejects(lostApi.createFreeSession(lostInput, 'lost-create-key-001'), error => error.code === 'reconcile_required')
await journal.markUncertain(permit)
const beforeReplay = [...storage.values.values()][0]
const secondJournal = new TrainingOperationJournal(storage, again.auth, { retentionMs: 60_000 })
const receipt = await again.api.replayPending(secondJournal, lostInput.clientEventId)
assert.equal(receipt.state, 'committed')
assert.equal(receipt.detail.clientEventId, lostInput.clientEventId)
assert.equal([...storage.values.values()][0], beforeReplay)
assert.equal((await secondJournal.readPending(lostInput.clientEventId)).state, 'uncertain')
const changedDuringReplay = new RecordingApi(process.env.NERAE_INTEROP_URL, again.auth, async options => {
  const response = await request(options)
  const key = [...storage.values.keys()][0]
  const current = JSON.parse(storage.values.get(key))
  current.revision++
  storage.values.set(key, JSON.stringify(current))
  return response
}, { subjectId: process.env.NERAE_INTEROP_SUBJECT_A, generation: 'local-test-again' })
assert.equal((await changedDuringReplay.replayPending(secondJournal, lostInput.clientEventId)).state, 'unresolved')
console.log('PASS: lost create response recovered as a read-only receipt using a second synthetic session; journal retained')
console.log('PASS: changed pending operation rejects an otherwise valid local TLS replay receipt')

// Exercise the new coordinator against the same real local HTTP server. The
// native storage ports below are synthetic; they do not model a device.
const makeNativeDraft = nativeDraftRows => ({
  readVersionedTrainingDraft(key) {
    const row = nativeDraftRows.get(key)
    return { ok: true, errorCode: '', found: !!row, value: row?.value ?? '', revision: row?.revision ?? '0' }
  },
  compareAndSetVersionedTrainingDraft(key, revision, found, before, value) {
    const row = nativeDraftRows.get(key)
    if ((row?.revision ?? '0') !== revision || !!row !== found || (row?.value ?? '') !== before) {
      return { ok: false, errorCode: 'conflict', found: false, value: '', revision: '' }
    }
    const next = (BigInt(revision) + 1n).toString()
    nativeDraftRows.set(key, { value, revision: next })
    return { ok: true, errorCode: '', found: false, value: '', revision: next }
  },
  discoverTrainingDrafts() {
    return { ok: true, errorCode: '', found: true, value: JSON.stringify({ eventIds: [], nextCursor: null, hasUnclassifiedItems: false }) }
  },
})
const nativeDraftRows = new Map()
const nativeDraft = makeNativeDraft(nativeDraftRows)
const draftStorage = new SecureDraftStorage(nativeDraft, process.env.NERAE_INTEROP_SUBJECT_A, first.auth)
const localNow = () => new Date(`${process.env.NERAE_INTEROP_DATE}T12:00:00Z`)
const newEditor = async eventId => {
  const values = [eventId, `${eventId}-entry`, `${eventId}-record`]
  const ids = { nextId: () => values.shift() }
  const created = await TrainingEditorController.create({ subjectId: process.env.NERAE_INTEROP_SUBJECT_A,
    date: process.env.NERAE_INTEROP_DATE, timeZone: 'UTC', storage: draftStorage, now: localNow, auth: first.auth, ids })
  assert.equal(created.ok, true)
  assert.equal((await created.controller.addCustomExercise('合成测试深蹲', 'bodyweight')).ok, true)
  return created.controller
}
const coordinatorEvent = 'coordinator-event-001'
const editor = await newEditor(coordinatorEvent)
const coordinatorJournal = new TrainingOperationJournal(storage, first.auth, { retentionMs: 60_000 })
const prepared = first.api.prepareFreeSessionRequest({ date: process.env.NERAE_INTEROP_DATE, timeZone: 'UTC',
  clientEventId: coordinatorEvent }, 'coordinator-create-key-001')
const candidate = await dispatchFirstFreeSession(editor, coordinatorJournal, first.api, first.auth, prepared)
assert.equal(candidate.state, 'receipt_candidate')
assert.equal(candidate.receipt.detail.clientEventId, coordinatorEvent)
assert.equal((await again.api.readSession(candidate.receipt.detail.id)).detail.clientEventId, coordinatorEvent)
assert.equal((await coordinatorJournal.readPending(coordinatorEvent)).state, 'uncertain')
assert.equal(editor.draft.server, undefined)
assert.equal(editor.draft.exercises.length, 1)
await editor.close()

const lostCoordinatorEvent = 'coordinator-lost-event-001'
const lostEditor = await newEditor(lostCoordinatorEvent)
let coordinatorCalls = 0
const coordinatorLostApi = new RecordingApi(process.env.NERAE_INTEROP_URL, first.auth, async options => {
  coordinatorCalls++
  const response = await request(options)
  assert.equal(response.statusCode, 201)
  throw new Error('synthetic lost response after coordinator commit')
}, { subjectId: process.env.NERAE_INTEROP_SUBJECT_A, generation: 'local-test-a' })
const lostPrepared = coordinatorLostApi.prepareFreeSessionRequest({ date: process.env.NERAE_INTEROP_DATE, timeZone: 'UTC',
  clientEventId: lostCoordinatorEvent }, 'coordinator-lost-key-001')
const lostCandidate = await dispatchFirstFreeSession(lostEditor, coordinatorJournal, coordinatorLostApi, first.auth, lostPrepared)
assert.equal(lostCandidate.state, 'reconcile_required')
assert.equal(coordinatorCalls, 1)
assert.equal((await coordinatorJournal.readPending(lostCoordinatorEvent)).state, 'uncertain')
const lostCoordinatorReceipt = await again.api.replayPending(secondJournal, lostCoordinatorEvent)
assert.equal(lostCoordinatorReceipt.state, 'committed')
assert.equal(lostCoordinatorReceipt.detail.clientEventId, lostCoordinatorEvent)
assert.equal((await coordinatorJournal.readPending(lostCoordinatorEvent)).state, 'uncertain')
assert.equal(lostEditor.draft.server, undefined)
await lostEditor.close()
console.log('PASS: first-send coordinator reached isolated TLS server and retained a lost-response receipt')

// A second synthetic session has its own empty protected-storage fixture.
// Restore an already completed server record, including both saved sets.
const secondDeviceRows = new Map()
const secondDraftStorage = new SecureDraftStorage(makeNativeDraft(secondDeviceRows),
  process.env.NERAE_INTEROP_SUBJECT_A, again.auth)
const restoredOnOtherSession = await restoreCloudSessionToLocalDraft({ sessionId: finished.detail.id,
  lease: { subjectId: process.env.NERAE_INTEROP_SUBJECT_A, generation: 'local-test-again' },
  auth: again.auth, draftLease: again.auth, api: again.api, storage: secondDraftStorage,
  ids: { nextId: () => 'unused-restored-id' }, now: localNow })
assert.equal(restoredOnOtherSession.ok, true)
assert.equal(restoredOnOtherSession.controller.draft.clientEventId, createInput.clientEventId)
assert.equal(restoredOnOtherSession.controller.draft.server.version, finished.detail.version)
assert.deepEqual(restoredOnOtherSession.controller.draft.exercises[0].sets.map(set => [set.weightKg, set.reps]),
  [['42.5', '8'], ['45', '6']])
await restoredOnOtherSession.controller.close()
console.log('PASS: second synthetic session restored a server workout into its own local draft')

// The server commits a create but its response is lost. Reopen the protected
// slot under a second synthetic session and send the same key and body again.
const retryEvent = 'sync-retry-create-event-001'
const retryEditor = await newEditor(retryEvent)
const attempts = []
const lostRetryApi = new RecordingApi(process.env.NERAE_INTEROP_URL, first.auth, async options => {
  attempts.push({ body: options.data, key: options.header['Idempotency-Key'] })
  const response = await request(options)
  assert.equal(response.statusCode, 201)
  throw new Error('synthetic lost create response')
}, { subjectId: process.env.NERAE_INTEROP_SUBJECT_A, generation: 'local-test-a' })
assert.equal((await syncFreeSessionCreate(retryEditor, lostRetryApi, first.auth,
  () => 'sync-retry-create-key-001')).state, 'retry')
assert.ok(retryEditor.draft.syncPending)
await retryEditor.close()
const reopenedStorage = new SecureDraftStorage(makeNativeDraft(nativeDraftRows),
  process.env.NERAE_INTEROP_SUBJECT_A, again.auth)
const reopened = await TrainingEditorController.attach(process.env.NERAE_INTEROP_SUBJECT_A, retryEvent,
  reopenedStorage, localNow, again.auth, { nextId: () => 'unused-retry-id' })
assert.equal(reopened.ok, true)
const retryApi = new RecordingApi(process.env.NERAE_INTEROP_URL, again.auth, async options => {
  attempts.push({ body: options.data, key: options.header['Idempotency-Key'] })
  return request(options)
}, { subjectId: process.env.NERAE_INTEROP_SUBJECT_A, generation: 'local-test-again' })
assert.equal((await syncFreeSessionCreate(reopened.controller, retryApi, again.auth,
  () => { throw new Error('a retry must reuse the stored key') })).state, 'created')
assert.deepEqual(attempts[0], attempts[1])
assert.equal(reopened.controller.draft.syncPending, undefined)
assert.equal(reopened.controller.draft.exercises.length, 1)
const retryReadback = await again.api.readSession(reopened.controller.draft.server.id)
assert.equal(retryReadback.detail.clientEventId, retryEvent)
await reopened.controller.close()
console.log('PASS: same-key create recovered after a lost response in a second synthetic session')
console.log('PASS: actual client adapter created, saved, replayed, finished and restored both recording modes over isolated TLS HTTP')
