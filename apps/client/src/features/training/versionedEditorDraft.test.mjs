import test from 'node:test'
import assert from 'node:assert/strict'
import { SecureDraftStorage } from './secureDraftStorage.ts'
import { TrainingEditorController } from './trainingEditorController.ts'
import { DraftController } from './draftStorage.ts'
import { VersionedEditorDraft } from './versionedEditorDraft.ts'
import { RecordingApi } from './recordingApi.ts'
import { updateSet } from './recording.ts'

function nativePort() {
  const rows = new Map()
  const state = { beforeCommit: false, afterCommit: false, readsUnavailable: false, onCAS: null }
  const ok = (found, value, revision) => ({ ok: true, errorCode: '', found, value, revision })
  const failed = { ok: false, errorCode: 'unavailable', found: false, value: '', revision: '' }
  return {
    rows, state,
    discoverTrainingDrafts() { return { ok: true, errorCode: '', found: true, value: JSON.stringify({ eventIds: [], nextCursor: null, hasUnclassifiedItems: false }) } },
    readVersionedTrainingDraft(key) {
      if (state.readsUnavailable) return failed
      const row = rows.get(key)
      return row ? ok(true, row.value, row.revision) : ok(false, '', '0')
    },
    compareAndSetVersionedTrainingDraft(key, revision, found, before, after) {
      if (state.beforeCommit) { state.beforeCommit = false; return failed }
      const row = rows.get(key)
      if ((row?.revision ?? '0') !== revision || !!row !== found || (row?.value ?? '') !== before) {
        return { ...failed, errorCode: 'conflict' }
      }
      const next = (BigInt(revision) + 1n).toString()
      rows.set(key, { value: after, revision: next })
      state.onCAS?.()
      if (state.afterCommit) { state.afterCommit = false; return failed }
      return ok(false, '', next)
    },
  }
}

function createRequest(eventId, date = '2026-11-03', subjectId = '101', generation = 'generation-1') {
  const auth = { current: () => ({ subjectId, generation, accessToken: 'synthetic-token' }),
    isCurrent: (subject, value) => subject === subjectId && value === generation }
  const api = new RecordingApi('https://api.example.org', auth, async () => { throw new Error('must not send') },
    { subjectId, generation })
  return api.prepareFreeSessionRequest({ date, timeZone: 'Asia/Shanghai', clientEventId: eventId }, 'create-key-0001')
}

test('a prepared creation can be staged once, survives reopening and freezes the creation date', async () => {
  const port = nativePort()
  const auth = { generation: 'generation-1', isCurrent: (subject, generation) => subject === '101' && generation === 'generation-1' }
  const storage = new SecureDraftStorage(port, '101', auth)
  const now = () => new Date('2026-11-03T01:00:00Z')
  const ids = { nextId: (() => { const values = ['event-stage-01', 'entry-0001', 'record-001']; return () => values.shift() })() }
  const created = await TrainingEditorController.create({ storage, auth, subjectId: '101', date: '2026-11-03', timeZone: 'Asia/Shanghai', now, ids })
  assert.equal(created.ok, true)
  const editor = created.controller
  assert.equal((await editor.addCustomExercise('深蹲', 'weight')).ok, true)
  const request = createRequest('event-stage-01')
  assert.deepEqual(await editor.stageFreeSession(request), { ok: true, changed: true, localStatus: 'saved_locally' })
  assert.deepEqual(await editor.stageFreeSession(request), { ok: true, changed: false, localStatus: 'saved_locally' })
  assert.equal((await editor.setDate('2026-11-02')).ok, false)
  assert.equal((await editor.editGroup('entry-0001', 'record-001', 'reps', '9')).ok, true)
  const row = [...port.rows.values()][0]
  const saved = JSON.parse(row.value).draft
  assert.deepEqual(JSON.parse(saved.syncPending), { schema: 1, subjectId: '101', clientEventId: 'event-stage-01',
    state: 'ready', request })
  assert.equal(saved.date, '2026-11-03')
  assert.equal(saved.exercises[0].sets[0].reps, '9')
  await editor.close()
  const reopened = await TrainingEditorController.attach('101', 'event-stage-01', storage, now, auth, ids)
  assert.equal(reopened.ok, true)
  assert.equal(reopened.controller.draft.syncPending, saved.syncPending)
  assert.equal(reopened.controller.draft.exercises[0].sets[0].reps, '9')
  assert.deepEqual(await reopened.controller.stageFreeSession(createRequest('event-stage-01')),
    { ok: true, changed: false, localStatus: 'saved_locally' })
  await reopened.controller.close()
})

test('a stage write with an unknown result retains its marker while later input is saved', async () => {
  const port = nativePort()
  const auth = { generation: 'generation-1', isCurrent: (subject, generation) => subject === '101' && generation === 'generation-1' }
  const storage = new SecureDraftStorage(port, '101', auth)
  const now = () => new Date('2026-11-03T01:00:00Z')
  const ids = { nextId: (() => { const values = ['event-stage-unknown', 'entry-0001', 'record-001']; return () => values.shift() })() }
  const created = await TrainingEditorController.create({ storage, auth, subjectId: '101', date: '2026-11-03', timeZone: 'Asia/Shanghai', now, ids })
  assert.equal(created.ok, true)
  const editor = created.controller
  assert.equal((await editor.addCustomExercise('深蹲', 'weight')).ok, true)
  port.state.afterCommit = true
  const request = createRequest('event-stage-unknown')
  assert.equal((await editor.stageFreeSession(request)).reason, 'storage_error')
  assert.equal((await editor.setDate('2026-11-02')).ok, false)
  assert.equal((await editor.editGroup('entry-0001', 'record-001', 'reps', '9')).ok, true)
  const saved = JSON.parse([...port.rows.values()][0].value).draft
  assert.deepEqual(JSON.parse(saved.syncPending).request, request)
  assert.equal(saved.exercises[0].sets[0].reps, '9')
  await editor.close()
})

test('a request that exceeds capacity cannot install a marker or freeze the existing draft', async () => {
  const port = nativePort()
  const auth = { generation: 'generation-1', isCurrent: (subject, generation) => subject === '101' && generation === 'generation-1' }
  const storage = new SecureDraftStorage(port, '101', auth)
  const now = () => new Date('2026-11-03T01:00:00Z')
  const ids = { nextId: (() => { const values = ['event-stage-capacity', 'entry-0001', 'record-001']; return () => values.shift() })() }
  const created = await TrainingEditorController.create({ storage, auth, subjectId: '101', date: '2026-11-03', timeZone: 'Asia/Shanghai', now, ids })
  assert.equal(created.ok, true)
  const editor = created.controller
  assert.equal((await editor.addCustomExercise('深蹲', 'weight')).ok, true)
  const baseLength = Buffer.byteLength([...port.rows.values()][0].value, 'utf8')
  assert.equal((await editor.editGroup('entry-0001', 'record-001', 'reps', '9'.repeat(65536 - baseLength - 100))).ok, true)
  const before = [...port.rows.values()][0].value
  assert.equal((await editor.stageFreeSession(createRequest('event-stage-capacity'))).ok, false)
  assert.equal([...port.rows.values()][0].value, before)
  assert.equal(Object.hasOwn(editor.draft, 'syncPending'), false)
  assert.equal((await editor.setDate('2026-11-02')).ok, true)
  await editor.close()
})

test('ordinary transforms cannot erase, replace or add a pending marker in-place', async () => {
  const port = nativePort()
  const auth = { generation: 'generation-1', isCurrent: (subject, generation) => subject === '101' && generation === 'generation-1' }
  const storage = new SecureDraftStorage(port, '101', auth)
  const now = () => new Date('2026-11-03T01:00:00Z')
  const ids = { nextId: () => 'event-stage-mutate' }
  const created = await TrainingEditorController.create({ storage, auth, subjectId: '101', date: '2026-11-03', timeZone: 'Asia/Shanghai', now, ids })
  assert.equal(created.ok, true)
  await created.controller.close()
  const local = await VersionedEditorDraft.load('101', 'event-stage-mutate', storage, now, auth)
  await assert.rejects(local.update(draft => { draft.syncPending = 'forged'; return draft }), /待核对/)
  assert.equal((await local.stageFreeSession(createRequest('event-stage-mutate'))).saved, true)
  const before = [...port.rows.values()][0].value
  await assert.rejects(local.update(draft => { delete draft.syncPending; return draft }), /待核对/)
  await assert.rejects(local.update(draft => { draft.syncPending = 'changed'; return draft }), /待核对/)
  await assert.rejects(local.update(draft => { draft.server = { id: '42', version: 1 }; return draft }), /待核对/)
  await assert.rejects(local.update(draft => { draft.clientEventId = 'event-changed'; return draft }), /记录身份/)
  assert.equal([...port.rows.values()][0].value, before)
  local.release()
})

test('another subject, a forged request or a mismatched body cannot stage an intent', async () => {
  const port = nativePort()
  const auth = { generation: 'generation-1', isCurrent: (subject, generation) => subject === '101' && generation === 'generation-1' }
  const storage = new SecureDraftStorage(port, '101', auth)
  const now = () => new Date('2026-11-03T01:00:00Z')
  const ids = { nextId: () => 'event-stage-owner' }
  const created = await TrainingEditorController.create({ storage, auth, subjectId: '101', date: '2026-11-03', timeZone: 'Asia/Shanghai', now, ids })
  assert.equal(created.ok, true)
  const before = [...port.rows.values()][0].value
  const other = createRequest('event-stage-owner', '2026-11-03', '202')
  const correct = createRequest('event-stage-owner')
  assert.equal((await created.controller.stageFreeSession(other)).ok, false)
  assert.equal((await created.controller.stageFreeSession({ ...correct })).ok, false)
  assert.equal((await created.controller.stageFreeSession(createRequest('event-stage-owner', '2026-11-02'))).ok, false)
  assert.equal([...port.rows.values()][0].value, before)
  assert.equal(Object.hasOwn(created.controller.draft, 'syncPending'), false)
  assert.equal((await created.controller.stageFreeSession(correct)).ok, true)
  await created.controller.close()
})

test('staging freezes the date immediately even while the native write is delayed', async () => {
  const port = nativePort()
  const auth = { generation: 'generation-1', isCurrent: (subject, generation) => subject === '101' && generation === 'generation-1' }
  const storage = new SecureDraftStorage(port, '101', auth)
  const now = () => new Date('2026-11-03T01:00:00Z')
  const ids = { nextId: () => 'event-stage-delayed' }
  const created = await TrainingEditorController.create({ storage, auth, subjectId: '101', date: '2026-11-03', timeZone: 'Asia/Shanghai', now, ids })
  assert.equal(created.ok, true)
  let release, started
  const gate = new Promise(resolve => { release = resolve })
  const began = new Promise(resolve => { started = resolve })
  const originalPrepare = storage.prepareVersionedWrite.bind(storage)
  storage.prepareVersionedWrite = (...args) => {
    const commit = originalPrepare(...args)
    return async expected => { started(); await gate; return commit(expected) }
  }
  const staging = created.controller.stageFreeSession(createRequest('event-stage-delayed'))
  await began
  assert.equal((await created.controller.setDate('2026-11-02')).ok, false)
  release()
  assert.equal((await staging).ok, true)
  const saved = JSON.parse([...port.rows.values()][0].value).draft
  assert.equal(saved.date, '2026-11-03')
  assert.equal(JSON.parse(saved.syncPending).state, 'ready')
  await created.controller.close()
})

test('staging during a local save reports pending and succeeds after that save finishes', async () => {
  const port = nativePort()
  const auth = { generation: 'generation-1', isCurrent: (subject, generation) => subject === '101' && generation === 'generation-1' }
  const storage = new SecureDraftStorage(port, '101', auth)
  const now = () => new Date('2026-11-03T01:00:00Z')
  const ids = { nextId: (() => { const values = ['event-stage-busy', 'entry-0001', 'record-001']; return () => values.shift() })() }
  const created = await TrainingEditorController.create({ storage, auth, subjectId: '101', date: '2026-11-03', timeZone: 'Asia/Shanghai', now, ids })
  assert.equal(created.ok, true)
  assert.equal((await created.controller.addCustomExercise('深蹲', 'weight')).ok, true)
  let release, started
  const gate = new Promise(resolve => { release = resolve })
  const began = new Promise(resolve => { started = resolve })
  const originalPrepare = storage.prepareVersionedWrite.bind(storage)
  let waitOnce = true
  storage.prepareVersionedWrite = (...args) => {
    const commit = originalPrepare(...args)
    return async expected => { if (waitOnce) { waitOnce = false; started(); await gate } return commit(expected) }
  }
  const editing = created.controller.editGroup('entry-0001', 'record-001', 'reps', '8')
  await began
  const request = createRequest('event-stage-busy')
  assert.equal((await created.controller.stageFreeSession(request)).reason, 'pending')
  release()
  assert.equal((await editing).ok, true)
  assert.equal((await created.controller.stageFreeSession(request)).ok, true)
  assert.equal(created.controller.draft.exercises[0].sets[0].reps, '8')
  await created.controller.close()
})

test('replacing the bearer under the same generation invalidates a prepared request before staging', async () => {
  const port = nativePort()
  let token = 'first-synthetic-token'
  const auth = { generation: 'generation-1', isCurrent: (subject, generation) => subject === '101' && generation === 'generation-1' }
  const recordAuth = { current: () => ({ subjectId: '101', generation: 'generation-1', accessToken: token }), isCurrent: auth.isCurrent }
  const api = new RecordingApi('https://api.example.org', recordAuth, async () => { throw new Error('must not send') },
    { subjectId: '101', generation: 'generation-1' })
  const request = api.prepareFreeSessionRequest({ date: '2026-11-03', timeZone: 'Asia/Shanghai', clientEventId: 'event-stage-token' }, 'create-key-0001')
  const storage = new SecureDraftStorage(port, '101', auth)
  const created = await TrainingEditorController.create({ storage, auth, subjectId: '101', date: '2026-11-03', timeZone: 'Asia/Shanghai',
    now: () => new Date('2026-11-03T01:00:00Z'), ids: { nextId: () => 'event-stage-token' } })
  assert.equal(created.ok, true)
  const before = [...port.rows.values()][0].value
  token = 'replacement-synthetic-token'
  assert.equal((await created.controller.stageFreeSession(request)).ok, false)
  assert.equal([...port.rows.values()][0].value, before)
  await assert.rejects(api.sendPreparedFreeSessionRequest(request), error => error.code === 'session_changed')
  await created.controller.close()
})

test('a bearer replacement while staging prevents a success report and preserves the pending bytes', async () => {
  const port = nativePort()
  let token = 'first-synthetic-token'
  const auth = { generation: 'generation-1', isCurrent: (subject, generation) => subject === '101' && generation === 'generation-1' }
  const recordAuth = { current: () => ({ subjectId: '101', generation: 'generation-1', accessToken: token }), isCurrent: auth.isCurrent }
  const api = new RecordingApi('https://api.example.org', recordAuth, async () => { throw new Error('must not send') },
    { subjectId: '101', generation: 'generation-1' })
  const input = { date: '2026-11-03', timeZone: 'Asia/Shanghai', clientEventId: 'event-stage-token-race' }
  const request = api.prepareFreeSessionRequest(input, 'create-key-0001')
  const storage = new SecureDraftStorage(port, '101', auth)
  const created = await TrainingEditorController.create({ storage, auth, subjectId: '101', date: input.date, timeZone: input.timeZone,
    now: () => new Date('2026-11-03T01:00:00Z'), ids: { nextId: () => input.clientEventId } })
  assert.equal(created.ok, true)
  let release, started
  const gate = new Promise(resolve => { release = resolve })
  const began = new Promise(resolve => { started = resolve })
  const originalPrepare = storage.prepareVersionedWrite.bind(storage)
  storage.prepareVersionedWrite = (...args) => {
    const commit = originalPrepare(...args)
    return async expected => { started(); await gate; return commit(expected) }
  }
  const staging = created.controller.stageFreeSession(request)
  await began
  token = 'replacement-synthetic-token'
  release()
  assert.equal((await staging).ok, false)
  const saved = JSON.parse([...port.rows.values()][0].value).draft
  assert.deepEqual(JSON.parse(saved.syncPending).request, request)
  const currentApi = new RecordingApi('https://api.example.org', recordAuth, async () => { throw new Error('must not send') },
    { subjectId: '101', generation: 'generation-1' })
  assert.equal((await created.controller.stageFreeSession(currentApi.prepareFreeSessionRequest(input, 'create-key-0001'))).ok, true)
  await created.controller.close()
})

test('a new login cannot stage an old prepared request, but can restore the same pending bytes', async () => {
  const port = nativePort()
  let generation = 'generation-1'
  const auth = { get generation() { return generation }, isCurrent: (subject, value) => subject === '101' && value === generation }
  const recordAuth = { current: () => ({ subjectId: '101', generation, accessToken: 'synthetic-token' }), isCurrent: auth.isCurrent }
  const api = new RecordingApi('https://api.example.org', recordAuth, async () => { throw new Error('must not send') },
    { subjectId: '101', generation })
  const request = api.prepareFreeSessionRequest({ date: '2026-11-03', timeZone: 'Asia/Shanghai', clientEventId: 'event-stage-relogin' }, 'create-key-0001')
  const now = () => new Date('2026-11-03T01:00:00Z')
  const ids = { nextId: () => 'event-stage-relogin' }
  const storage = new SecureDraftStorage(port, '101', auth)
  const created = await TrainingEditorController.create({ storage, auth, subjectId: '101', date: '2026-11-03', timeZone: 'Asia/Shanghai', now, ids })
  assert.equal(created.ok, true)
  assert.equal((await created.controller.stageFreeSession(request)).ok, true)
  const marker = created.controller.draft.syncPending
  await created.controller.close()
  generation = 'generation-2'
  const reopened = await TrainingEditorController.attach('101', 'event-stage-relogin', new SecureDraftStorage(port, '101', auth), now, auth, ids)
  assert.equal(reopened.ok, true)
  assert.equal((await reopened.controller.stageFreeSession(request)).ok, false)
  const currentApi = new RecordingApi('https://api.example.org', recordAuth, async () => { throw new Error('must not send') },
    { subjectId: '101', generation })
  const currentRequest = currentApi.prepareFreeSessionRequest({ date: '2026-11-03', timeZone: 'Asia/Shanghai', clientEventId: 'event-stage-relogin' }, 'create-key-0001')
  assert.deepEqual(await reopened.controller.stageFreeSession(currentRequest), { ok: true, changed: false, localStatus: 'saved_locally' })
  assert.equal(reopened.controller.draft.syncPending, marker)
  await reopened.controller.close()
})

test('an unknown existing marker is never replaced, including an empty marker', async () => {
  const port = nativePort()
  const auth = { generation: 'generation-1', isCurrent: (subject, generation) => subject === '101' && generation === 'generation-1' }
  const storage = new SecureDraftStorage(port, '101', auth)
  const now = () => new Date('2026-11-03T01:00:00Z')
  const ids = { nextId: () => 'event-stage-existing' }
  const created = await TrainingEditorController.create({ storage, auth, subjectId: '101', date: '2026-11-03', timeZone: 'Asia/Shanghai', now, ids })
  assert.equal(created.ok, true)
  await created.controller.close()
  const row = [...port.rows.values()][0]
  const saved = JSON.parse(row.value)
  saved.draft.syncPending = ''
  row.value = JSON.stringify(saved)
  row.revision = (BigInt(row.revision) + 1n).toString()
  const before = row.value
  const reopened = await TrainingEditorController.attach('101', 'event-stage-existing', storage, now, auth, ids)
  assert.equal(reopened.ok, true)
  assert.equal((await reopened.controller.stageFreeSession(createRequest('event-stage-existing'))).ok, false)
  assert.equal((await reopened.controller.setDate('2026-11-02')).ok, false)
  assert.equal([...port.rows.values()][0].value, before)
  await reopened.controller.close()
})

test('unversioned draft storage cannot silently stage a network intent', async () => {
  const rows = new Map()
  const storage = { read: async key => rows.get(key), write: async (key, value) => { rows.set(key, value) },
    createIfAbsent: async (key, value) => { if (rows.has(key)) return 'exists'; rows.set(key, value); return 'created' } }
  const auth = { generation: 'generation-1', isCurrent: (subject, generation) => subject === '101' && generation === 'generation-1' }
  const created = await TrainingEditorController.create({ storage, auth, subjectId: '101', date: '2026-11-03', timeZone: 'Asia/Shanghai',
    now: () => new Date('2026-11-03T01:00:00Z'), ids: { nextId: () => 'event-stage-generic' } })
  assert.equal(created.ok, true)
  const before = [...rows.values()][0]
  assert.equal((await created.controller.stageFreeSession(createRequest('event-stage-generic'))).ok, false)
  assert.equal([...rows.values()][0], before)
  await created.controller.close()
})

test('transient unavailable reads must not evict the only possibly committed predecessor', async () => {
  const port = nativePort()
  const auth = { generation: 'generation-1', isCurrent(subjectId, generation) { return subjectId === '101' && generation === 'generation-1' } }
  const storage = new SecureDraftStorage(port, '101', auth)
  const identifiers = ['event-history-01', 'entry-0001', 'record-001']
  const created = await TrainingEditorController.create({
    storage, auth, subjectId: '101', date: '2026-11-03', timeZone: 'Asia/Shanghai',
    now: () => new Date('2026-11-03T01:00:00Z'), ids: { nextId: () => identifiers.shift() },
  })
  assert.equal(created.ok, true)
  const editor = created.controller
  assert.equal((await editor.addCustomExercise('深蹲', 'weight')).ok, true)
  port.state.afterCommit = true
  const first = await editor.editGroup('entry-0001', 'record-001', 'reps', '8')
  assert.equal(first.reason, 'storage_error')
  port.state.readsUnavailable = true
  const edits = []
  for (let value = 9; value <= 17; value++) {
    edits.push(await editor.editGroup('entry-0001', 'record-001', 'reps', String(value)))
  }
  const results = edits
  assert.equal(results.every(item => !item.ok), true)
  port.state.readsUnavailable = false
  const retried = await editor.retryLocalSave()
  const row = [...port.rows.values()][0]
  assert.equal(retried.ok, true, 'restored reads must permit retry of the current input')
  assert.equal(editor.draft.exercises[0].sets[0].reps, '17')
  assert.equal(JSON.parse(row.value).draft.exercises[0].sets[0].reps, '17')
  await editor.close()
  const reopened = await TrainingEditorController.attach('101', 'event-history-01', storage,
    () => new Date('2026-11-03T01:00:00Z'), auth, { nextId: () => { throw new Error('unused') } })
  assert.equal(reopened.ok, true)
  assert.equal(reopened.controller.draft.exercises[0].sets[0].reps, '17')
  await reopened.controller.close()
})

test('session change during a queued write retains the latest input for the same subject', async () => {
  const port = nativePort()
  let generation = 'generation-1'
  const auth = { get generation() { return generation }, isCurrent(subjectId, value) { return subjectId === '101' && value === generation } }
  const storage = new SecureDraftStorage(port, '101', auth)
  const now = () => new Date('2026-11-03T01:00:00Z')
  const identifiers = ['event-session-01', 'entry-0001', 'record-001']
  const ids = { nextId: () => identifiers.shift() }
  const created = await TrainingEditorController.create({
    storage, auth, subjectId: '101', date: '2026-11-03', timeZone: 'Asia/Shanghai', now, ids,
  })
  assert.equal(created.ok, true)
  const editor = created.controller
  assert.equal((await editor.addCustomExercise('深蹲', 'weight')).ok, true)
  let release
  const gate = new Promise(resolve => { release = resolve })
  let started
  const began = new Promise(resolve => { started = resolve })
  let first = true
  const prepare = storage.prepareVersionedWrite.bind(storage)
  storage.prepareVersionedWrite = (...args) => {
    const commit = prepare(...args)
    return async expected => {
      if (first) { first = false; started(); await gate }
      return commit(expected)
    }
  }
  const older = editor.editGroup('entry-0001', 'record-001', 'reps', '8')
  await began
  const newer = editor.editGroup('entry-0001', 'record-001', 'reps', '9')
  port.state.onCAS = () => { port.state.onCAS = null; generation = 'generation-2' }
  release()
  const oldResults = await Promise.all([older, newer])
  assert.equal(oldResults.every(result => result.ok === false), true)
  await editor.close()
  const reopened = await TrainingEditorController.attach('101', 'event-session-01', new SecureDraftStorage(port, '101', auth), now, auth, ids)
  assert.equal(reopened.ok, true)
  const recoveredBeforeRetry = reopened.controller.draft.exercises[0].sets[0].reps
  const initialStatus = reopened.controller.localStatus
  const retry = await reopened.controller.retryLocalSave()
  const persistedReps = JSON.parse([...port.rows.values()][0].value).draft.exercises[0].sets[0].reps
  await reopened.controller.close()
  assert.equal(recoveredBeforeRetry, '9')
  assert.equal(initialStatus, 'storage_error')
  assert.equal(retry.ok, true)
  assert.equal(persistedReps, '9')
})

test('a low-level controller cannot bypass a versioned editor owner', async () => {
  const port = nativePort()
  const auth = { generation: 'generation-1', isCurrent: (subjectId, generation) => subjectId === '101' && generation === 'generation-1' }
  const storage = new SecureDraftStorage(port, '101', auth)
  const now = () => new Date('2026-11-03T01:00:00Z')
  const ids = { nextId: (() => { const values = ['event-owner-01', 'entry-0001', 'record-001']; return () => values.shift() })() }
  const created = await TrainingEditorController.create({ storage, auth, subjectId: '101', date: '2026-11-03', timeZone: 'Asia/Shanghai', now, ids })
  assert.equal(created.ok, true)
  await assert.rejects(DraftController.load('101', 'event-owner-01', storage, now, auth), /版本化编辑器/)
  assert.equal((await created.controller.addCustomExercise('深蹲', 'weight')).ok, true)
  await created.controller.close()
})

test('a valid correction remains saveable after a newer preparation failure and an older successful write', async () => {
  const port = nativePort()
  const auth = { generation: 'generation-1', isCurrent(subjectId, generation) { return subjectId === '101' && generation === 'generation-1' } }
  const storage = new SecureDraftStorage(port, '101', auth)
  const identifiers = ['event-0001', 'entry-0001', 'record-001']
  const options = {
    storage, auth, subjectId: '101',
    now: () => new Date('2026-11-03T01:00:00Z'),
    ids: { nextId: () => identifiers.shift() },
  }
  const created = await TrainingEditorController.create({ ...options, date: '2026-11-03', timeZone: 'Asia/Shanghai' })
  assert.equal(created.ok, true)
  const editor = created.controller
  assert.equal((await editor.addCustomExercise('深蹲', 'weight')).ok, true)

  let release
  const gate = new Promise(resolve => { release = resolve })
  let started
  const firstStarted = new Promise(resolve => { started = resolve })
  let shouldWait = true
  const originalPrepare = storage.prepareVersionedWrite.bind(storage)
  storage.prepareVersionedWrite = (...args) => {
    const commit = originalPrepare(...args)
    return async expected => {
      if (shouldWait) {
        shouldWait = false
        started()
        await gate
      }
      return commit(expected)
    }
  }

  const older = editor.editGroup('entry-0001', 'record-001', 'reps', '8')
  await firstStarted
  const invalid = await editor.editGroup('entry-0001', 'record-001', 'reps', '9'.repeat(65536))
  assert.equal(invalid.ok, false)
  release()
  const olderResult = await older
  await editor.close()

  const attached = await TrainingEditorController.attach('101', 'event-0001', storage, options.now, auth, options.ids)
  assert.equal(attached.ok, true)
  const reopened = attached.controller
  const correction = await reopened.editGroup('entry-0001', 'record-001', 'reps', '10')
  const row = [...port.rows.values()][0]
  const persistedReps = JSON.parse(row.value).draft.exercises[0].sets[0].reps
  await reopened.close()
  assert.equal(olderResult.ok, false)
  assert.equal(correction.ok, true, 'a valid correction must be saveable after reopening')
  assert.equal(persistedReps, '10')
})

test('input during delayed recovery is persisted before the editor can report success', async () => {
  const port = nativePort()
  const auth = { generation: 'generation-1', isCurrent: (subject, generation) => subject === '101' && generation === 'generation-1' }
  const storage = new SecureDraftStorage(port, '101', auth)
  const now = () => new Date('2026-11-03T01:00:00Z')
  const ids = { nextId: (() => { const values = ['event-delayed-01', 'entry-0001', 'record-001']; return () => values.shift() })() }
  const created = await TrainingEditorController.create({ storage, auth, subjectId: '101', date: '2026-11-03', timeZone: 'Asia/Shanghai', now, ids })
  assert.equal(created.ok, true)
  const editor = created.controller
  assert.equal((await editor.addCustomExercise('深蹲', 'weight')).ok, true)
  port.state.afterCommit = true
  assert.equal((await editor.editGroup('entry-0001', 'record-001', 'reps', '8')).reason, 'storage_error')
  let release, started
  const gate = new Promise(resolve => { release = resolve })
  const began = new Promise(resolve => { started = resolve })
  const read = storage.readVersioned.bind(storage)
  let first = true
  storage.readVersioned = async key => {
    if (first) { first = false; started(); await gate }
    return read(key)
  }
  const retry = editor.retryLocalSave()
  await began
  const newer = editor.editGroup('entry-0001', 'record-001', 'reps', '9')
  release()
  assert.equal((await retry).ok, false)
  assert.equal((await newer).ok, true)
  await editor.close()
  const reopened = await TrainingEditorController.attach('101', 'event-delayed-01', storage, now, auth, ids)
  assert.equal(reopened.ok, true)
  assert.equal(reopened.controller.draft.exercises[0].sets[0].reps, '9')
  assert.equal(reopened.controller.localStatus, 'saved_locally')
  await reopened.controller.close()
})

test('an unknown write followed by an extra native revision never overwrites the newer state', async () => {
  const port = nativePort()
  const auth = { generation: 'generation-1', isCurrent: (subject, generation) => subject === '101' && generation === 'generation-1' }
  const storage = new SecureDraftStorage(port, '101', auth)
  const now = () => new Date('2026-11-03T01:00:00Z')
  const ids = { nextId: (() => { const values = ['event-conflict-01', 'entry-0001', 'record-001']; return () => values.shift() })() }
  const created = await TrainingEditorController.create({ storage, auth, subjectId: '101', date: '2026-11-03', timeZone: 'Asia/Shanghai', now, ids })
  assert.equal(created.ok, true)
  const editor = created.controller
  assert.equal((await editor.addCustomExercise('深蹲', 'weight')).ok, true)
  port.state.afterCommit = true
  assert.equal((await editor.editGroup('entry-0001', 'record-001', 'reps', '8')).reason, 'storage_error')
  const row = [...port.rows.values()][0]
  row.revision = (BigInt(row.revision) + 1n).toString()
  assert.equal((await editor.editGroup('entry-0001', 'record-001', 'reps', '9')).reason, 'storage_error')
  assert.equal(editor.draft.exercises[0].sets[0].reps, '9')
  assert.equal(JSON.parse(row.value).draft.exercises[0].sets[0].reps, '8')
  await editor.close()
})

test('ordinary edits preserve a pending marker supplied by the durable store', async () => {
  const port = nativePort()
  const auth = { generation: 'generation-1', isCurrent: (subject, generation) => subject === '101' && generation === 'generation-1' }
  const storage = new SecureDraftStorage(port, '101', auth)
  const now = () => new Date('2026-11-03T01:00:00Z')
  const ids = { nextId: (() => { const values = ['event-pending-01', 'entry-0001', 'record-001']; return () => values.shift() })() }
  const created = await TrainingEditorController.create({ storage, auth, subjectId: '101', date: '2026-11-03', timeZone: 'Asia/Shanghai', now, ids })
  assert.equal(created.ok, true)
  assert.equal((await created.controller.addCustomExercise('深蹲', 'weight')).ok, true)
  await created.controller.close()
  const row = [...port.rows.values()][0]
  const saved = JSON.parse(row.value)
  const marker = JSON.stringify({ state: 'uncertain', request: 'original-bytes' })
  saved.draft.syncPending = marker
  row.value = JSON.stringify(saved)
  row.revision = (BigInt(row.revision) + 1n).toString()
  const attached = await TrainingEditorController.attach('101', 'event-pending-01', storage, now, auth, ids)
  assert.equal(attached.ok, true)
  assert.equal((await attached.controller.editGroup('entry-0001', 'record-001', 'reps', '8')).ok, true)
  assert.equal((await attached.controller.setMode('per_set')).ok, true)
  const storedDraft = JSON.parse([...port.rows.values()][0].value).draft
  assert.equal(storedDraft.syncPending, marker)
  assert.equal(storedDraft.exercises[0].sets[0].reps, '8')
  await attached.controller.close()
})

test('unknown create can reopen the one actual committed draft and continue editing', async () => {
  const port = nativePort()
  const auth = { generation: 'generation-1', isCurrent(subjectId, generation) { return subjectId === '101' && generation === 'generation-1' } }
  const storage = new SecureDraftStorage(port, '101', auth)
  const now = () => new Date('2026-11-03T01:00:00Z')
  const identifiers = ['event-create-unknown-01', 'entry-0001', 'record-001']
  const ids = { nextId: () => identifiers.shift() }
  port.state.afterCommit = true
  const created = await TrainingEditorController.create({ storage, auth, subjectId: '101',
    date: '2026-11-03', timeZone: 'Asia/Shanghai', now, ids })
  assert.equal(created.ok, false)
  assert.equal(created.reason, 'outcome_unknown')
  assert.equal(created.eventId, 'event-create-unknown-01')
  assert.equal(port.rows.size, 1)
  const recovered = await TrainingEditorController.attach('101', created.eventId, storage, now, auth, ids)
  assert.equal(recovered.ok, true)
  assert.equal(recovered.controller.localStatus, 'saved_locally')
  assert.equal((await recovered.controller.addCustomExercise('深蹲', 'weight')).ok, true)
  assert.equal((await recovered.controller.editGroup('entry-0001', 'record-001', 'reps', '8')).ok, true)
  assert.equal(JSON.parse([...port.rows.values()][0].value).draft.exercises[0].sets[0].reps, '8')
  assert.equal(port.rows.size, 1)
  await recovered.controller.close()
})

test('account change while recovering an unknown write preserves input only for the original owner', async () => {
  const port = nativePort()
  let generation = 'generation-1'
  let subject = '101'
  const auth = { get generation() { return generation }, isCurrent(id, captured) { return id === subject && captured === generation } }
  const storage = new SecureDraftStorage(port, '101', auth)
  const now = () => new Date('2026-11-03T01:00:00Z')
  const identifiers = ['event-switch-recovery-01', 'entry-0001', 'record-001']
  const ids = { nextId: () => identifiers.shift() }
  const created = await TrainingEditorController.create({ storage, auth, subjectId: '101',
    date: '2026-11-03', timeZone: 'Asia/Shanghai', now, ids })
  assert.equal(created.ok, true)
  const editor = created.controller
  assert.equal((await editor.addCustomExercise('深蹲', 'weight')).ok, true)
  port.state.afterCommit = true
  assert.equal((await editor.editGroup('entry-0001', 'record-001', 'reps', '8')).reason, 'storage_error')
  let release
  let started
  const gate = new Promise(resolve => { release = resolve })
  const began = new Promise(resolve => { started = resolve })
  const originalRead = storage.readVersioned.bind(storage)
  let first = true
  storage.readVersioned = async key => {
    const snapshot = await originalRead(key)
    if (first) { first = false; started(); await gate }
    return snapshot
  }
  const recovery = editor.retryLocalSave()
  await began
  const latest = editor.editGroup('entry-0001', 'record-001', 'reps', '9')
  subject = '202'
  generation = 'generation-2'
  release()
  const previousResults = await Promise.all([recovery, latest])
  assert.equal(previousResults.every(value => !value.ok && value.reason === 'session_changed'), true)
  await editor.close()
  const otherStorage = new SecureDraftStorage(port, '202', auth)
  const other = await TrainingEditorController.attach('202', 'event-switch-recovery-01', otherStorage, now, auth, ids)
  assert.equal(other.ok, false)
  subject = '101'
  generation = 'generation-3'
  const recovered = await TrainingEditorController.attach('101', 'event-switch-recovery-01',
    new SecureDraftStorage(port, '101', auth), now, auth, ids)
  assert.equal(recovered.ok, true)
  assert.equal(recovered.controller.draft.exercises[0].sets[0].reps, '9')
  assert.equal(recovered.controller.localStatus, 'storage_error')
  assert.equal((await recovered.controller.retryLocalSave()).ok, true)
  assert.equal(JSON.parse([...port.rows.values()][0].value).draft.exercises[0].sets[0].reps, '9')
  await recovered.controller.close()
})

test('rapid ordinary edits result in only the latest saved input', async () => {
  const port = nativePort()
  const auth = { generation: 'generation-1', isCurrent(subjectId, generation) { return subjectId === '101' && generation === 'generation-1' } }
  const storage = new SecureDraftStorage(port, '101', auth)
  const now = () => new Date('2026-11-03T01:00:00Z')
  const identifiers = ['event-rapid-01', 'entry-0001', 'record-001']
  const ids = { nextId: () => identifiers.shift() }
  const created = await TrainingEditorController.create({ storage, auth, subjectId: '101',
    date: '2026-11-03', timeZone: 'Asia/Shanghai', now, ids })
  assert.equal(created.ok, true)
  const editor = created.controller
  assert.equal((await editor.addCustomExercise('深蹲', 'weight')).ok, true)
  const results = await Promise.all(['8', '9', '10'].map(value => editor.editGroup('entry-0001', 'record-001', 'reps', value)))
  assert.equal(results[2].ok, true)
  assert.equal(editor.localStatus, 'saved_locally')
  assert.equal(JSON.parse([...port.rows.values()][0].value).draft.exercises[0].sets[0].reps, '10')
  await editor.close()
  const reopened = await TrainingEditorController.attach('101', 'event-rapid-01', storage, now, auth, ids)
  assert.equal(reopened.ok, true)
  assert.equal(reopened.controller.draft.exercises[0].sets[0].reps, '10')
  await reopened.controller.close()
})
