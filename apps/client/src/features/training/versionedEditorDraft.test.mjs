import test from 'node:test'
import assert from 'node:assert/strict'
import { SecureDraftStorage } from './secureDraftStorage.ts'
import { TrainingEditorController } from './trainingEditorController.ts'
import { DraftController } from './draftStorage.ts'
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
