import test from 'node:test'
import assert from 'node:assert/strict'
import { SecureDraftStorage } from './secureDraftStorage.ts'
import { TrainingEditorController } from './trainingEditorController.ts'
import { RecordingApi } from './recordingApi.ts'
import { syncFreeSessionCreate } from './syncFreeSessionCreate.ts'
import { syncPendingSets } from './syncPendingSets.ts'

const now = () => new Date('2026-11-03T01:00:00Z')
const date = '2026-11-03'
const expiry = '2030-01-01T00:00:00Z'
const ok = (found = false, value = '') => ({ ok: true, errorCode: '', found, value })
const fail = errorCode => ({ ok: false, errorCode, found: false, value: '', revision: '' })
let serial = 0

function fixture() {
  const eventId = `event-sets-${String(++serial).padStart(4, '0')}`
  let generation = 'login-1'
  let token = 'synthetic-token-1'
  const auth = { get generation() { return generation }, current: () => ({ subjectId: '101', generation, accessToken: token }),
    isCurrent: (subject, value) => subject === '101' && value === generation,
    change() { generation = 'login-2'; token = 'synthetic-token-2' }, rotate() { token = 'synthetic-token-rotated' } }
  const rows = new Map()
  const native = {
    failWrites: false,
    discoverTrainingDrafts() { return ok(true, JSON.stringify({ eventIds: [], nextCursor: null, hasUnclassifiedItems: false })) },
    readVersionedTrainingDraft(key) {
      const row = rows.get(key)
      return row ? { ...ok(true, row.value), revision: row.revision } : { ...ok(), revision: '0' }
    },
    compareAndSetVersionedTrainingDraft(key, revision, found, before, value) {
      if (this.failWrites) return fail('unavailable')
      const row = rows.get(key)
      if ((row?.revision ?? '0') !== revision || !!row !== found || (row?.value ?? '') !== before) return fail('conflict')
      const next = String(BigInt(revision) + 1n)
      rows.set(key, { value, revision: next })
      return { ...ok(), revision: next }
    },
  }
  const ids = { nextId: (() => { const values = [eventId, 'entry-0001', 'record-001', 'record-002', 'record-003']; return () => values.shift() })() }
  const server = { id: '42', clientEventId: eventId, date, timeZone: 'Asia/Shanghai', status: 'in_progress', source: 'free', version: 1,
    createdAt: '2026-11-03T00:00:00Z', updatedAt: '2026-11-03T00:00:00Z', recordedAt: null, sets: [], exerciseEntryKeys: [] }
  const clone = value => JSON.parse(JSON.stringify(value))
  const requests = []
  let receipt
  let drop = false
  let dropBefore = false
  let onWrite = null
  let noReceipt = false
  async function transport(request) {
    requests.push(request)
    if (request.method === 'GET') return { statusCode: 200, data: clone(server) }
    if (request.url.endsWith('/me/sessions')) return { statusCode: 201, data: clone(server), headers: { 'Idempotency-Key-Expires-At': expiry } }
    if (request.url.endsWith('/sets/replay')) {
      if (noReceipt || !receipt || request.data !== receipt.body || request.header['Idempotency-Key'] !== receipt.key) return { statusCode: 503 }
      return { statusCode: 200, data: clone(receipt.data), headers: { 'Idempotency-Key-Expires-At': expiry,
        'Idempotency-Replayed': 'true', 'Content-Type': 'application/json', 'Cache-Control': 'no-store',
        'X-Request-ID': '0123456789abcdef0123456789abcdef' } }
    }
    assert.ok(request.url.endsWith('/sets'))
    if (dropBefore) { dropBefore = false; throw new Error('synthetic request not delivered') }
    const body = JSON.parse(request.data)
    if (body.expectedVersion !== server.version) return { statusCode: 412 }
    const old = new Map(server.sets.map(set => [set.recordKey, set]))
    for (const set of body.sets) old.set(set.recordKey, { ...set, resolvedExerciseName: set.exerciseName || '动作名称' })
    server.sets = body.exerciseEntryKeys.flatMap(key => [...old.values()].filter(set => set.exerciseEntryKey === key)
      .sort((a, b) => a.setIndex - b.setIndex))
    server.exerciseEntryKeys = [...body.exerciseEntryKeys]
    server.version++
    receipt = { key: request.header['Idempotency-Key'], body: request.data, data: clone(server) }
    if (onWrite) await onWrite()
    if (drop) { drop = false; throw new Error('synthetic lost response') }
    return { statusCode: 200, data: clone(server), headers: { 'Idempotency-Key-Expires-At': expiry } }
  }
  const makeApi = () => new RecordingApi('https://api.example.org', auth, transport, { subjectId: '101', generation })
  const storage = () => new SecureDraftStorage(native, '101', auth)
  return { auth, rows, native, ids, eventId, server, requests, makeApi, storage,
    dropNext() { drop = true }, dropBeforeNext() { dropBefore = true }, whenWrite(fn) { onWrite = fn }, hideReceipt() { noReceipt = true } }
}

async function editorFor(f, mode = 'after_workout') {
  const created = await TrainingEditorController.create({ subjectId: '101', date, timeZone: 'Asia/Shanghai',
    storage: f.storage(), now, auth: f.auth, ids: f.ids })
  assert.equal(created.ok, true)
  const editor = created.controller
  assert.equal((await editor.addCustomExercise('深蹲', 'bodyweight')).ok, true)
  assert.equal((await editor.editGroup('entry-0001', 'record-001', 'reps', '8')).ok, true)
  assert.equal((await editor.addGroup('entry-0001')).ok, true)
  assert.equal((await editor.editGroup('entry-0001', 'record-002', 'reps', '10')).ok, true)
  if (mode === 'per_set') {
    assert.equal((await editor.setMode(mode)).ok, true)
    assert.equal((await editor.confirmGroup('entry-0001', 'record-001')).ok, true)
  }
  const api = f.makeApi()
  assert.equal((await syncFreeSessionCreate(editor, api, f.auth, () => 'create-key-0001')).state, 'created')
  return { editor, api }
}

const batch = { mode: 'after_workout' }
const one = { mode: 'per_set', exerciseEntryKey: 'entry-0001', recordKey: 'record-001' }
const save = (editor, api, f, selection = batch) => syncPendingSets(editor, api, f.auth, selection, () => 'sets-key-0001', now)
const writes = f => f.requests.filter(request => request.url.endsWith('/sets'))

test('after-workout save persists the exact staged batch and acknowledges both groups', async () => {
  const f = fixture()
  const { editor, api } = await editorFor(f)
  f.whenWrite(() => {
    const stored = JSON.parse([...f.rows.values()][0].value).draft
    assert.equal(JSON.parse(stored.syncPending).request.body, writes(f)[0].data)
  })
  assert.equal((await save(editor, api, f)).state, 'saved')
  assert.equal(writes(f).length, 1)
  assert.deepEqual(JSON.parse(writes(f)[0].data).sets.map(set => set.recordKey), ['record-001', 'record-002'])
  assert.equal(editor.draft.server.version, 2)
  assert.equal(editor.draft.syncPending, undefined)
  assert.ok(editor.draft.exercises[0].sets.every(set => set.persisted && !set.dirty && set.confirmed))
  assert.deepEqual(JSON.parse([...f.rows.values()][0].value).draft, editor.draft)
  await editor.close()
})

test('per-set save uploads only the confirmed group and leaves the next group local', async () => {
  const f = fixture()
  const { editor, api } = await editorFor(f, 'per_set')
  assert.equal((await save(editor, api, f, one)).state, 'saved')
  assert.deepEqual(JSON.parse(writes(f)[0].data).sets.map(set => set.recordKey), ['record-001'])
  assert.equal(editor.draft.exercises[0].sets[0].persisted, true)
  assert.equal(editor.draft.exercises[0].sets[1].persisted, false)
  assert.equal(editor.draft.exercises[0].sets[1].reps, '10')
  assert.equal((await save(editor, api, f, one)).state, 'already_saved')
  assert.equal(writes(f).length, 1)
  await editor.close()
})

test('a lost response after a new login recovers the original receipt without another write', async () => {
  const f = fixture()
  const { editor, api } = await editorFor(f)
  f.dropNext()
  assert.equal((await save(editor, api, f)).state, 'retry')
  const marker = JSON.parse(editor.draft.syncPending)
  await editor.close()
  f.auth.change()
  const reopened = await TrainingEditorController.attach('101', f.eventId, f.storage(), now, f.auth, f.ids)
  assert.equal(reopened.ok, true)
  const result = await syncPendingSets(reopened.controller, f.makeApi(), f.auth, batch,
    () => { throw new Error('must not create another key') }, now)
  assert.equal(result.state, 'recovered')
  const replay = f.requests.find(request => request.url.endsWith('/sets/replay'))
  assert.equal(replay.data, marker.request.body)
  assert.equal(replay.header['Idempotency-Key'], marker.request.idempotencyKey)
  assert.equal(writes(f).length, 1)
  assert.equal(reopened.controller.draft.server.version, 2)
  assert.equal(reopened.controller.draft.syncPending, undefined)
  await reopened.controller.close()
})

test('an undelivered request retries the same body and key after reopening', async () => {
  const f = fixture()
  const { editor, api } = await editorFor(f)
  f.dropBeforeNext()
  assert.equal((await save(editor, api, f)).state, 'retry')
  const marker = JSON.parse(editor.draft.syncPending)
  await editor.close()
  const reopened = await TrainingEditorController.attach('101', f.eventId, f.storage(), now, f.auth, f.ids)
  assert.equal(reopened.ok, true)
  const result = await syncPendingSets(reopened.controller, f.makeApi(), f.auth, batch,
    () => { throw new Error('must not create another key') }, now)
  assert.equal(result.state, 'recovered')
  assert.equal(writes(f).length, 2)
  for (const request of writes(f)) {
    assert.equal(request.data, marker.request.body)
    assert.equal(request.header['Idempotency-Key'], marker.request.idempotencyKey)
  }
  assert.equal(f.server.version, 2)
  assert.equal(reopened.controller.draft.syncPending, undefined)
  await reopened.controller.close()
})

test('edits and a new group made during upload survive and are not marked confirmed', async () => {
  const f = fixture()
  const { editor, api } = await editorFor(f, 'per_set')
  let release
  let started
  const sent = new Promise(resolve => { started = resolve })
  f.whenWrite(async () => { started(); await new Promise(resolve => { release = resolve }) })
  const saving = save(editor, api, f, one)
  await sent
  assert.equal((await save(editor, api, f, one)).state, 'busy')
  assert.equal((await editor.editGroup('entry-0001', 'record-001', 'reps', '9')).ok, true)
  assert.equal((await editor.addGroup('entry-0001')).ok, true)
  release()
  assert.equal((await saving).state, 'saved')
  const groups = editor.draft.exercises[0].sets
  assert.equal(groups[0].reps, '9')
  assert.equal(groups[0].confirmed, false)
  assert.equal(groups[0].dirty, true)
  assert.equal(groups[2].persisted, false)
  assert.equal(writes(f).length, 1)
  await editor.close()
})

test('equivalent numeric input edited during upload remains unconfirmed', async () => {
  const f = fixture()
  const { editor, api } = await editorFor(f, 'per_set')
  let release
  let started
  const sent = new Promise(resolve => { started = resolve })
  f.whenWrite(async () => { started(); await new Promise(resolve => { release = resolve }) })
  const saving = save(editor, api, f, one)
  await sent
  assert.equal((await editor.editGroup('entry-0001', 'record-001', 'reps', '08')).ok, true)
  release()
  assert.equal((await saving).state, 'saved')
  assert.equal(editor.draft.exercises[0].sets[0].reps, '08')
  assert.equal(editor.draft.exercises[0].sets[0].confirmed, false)
  await editor.close()
})

test('a submitted group cannot be removed while its server acknowledgement is pending', async () => {
  const f = fixture()
  const { editor, api } = await editorFor(f, 'per_set')
  let release
  let started
  const sent = new Promise(resolve => { started = resolve })
  f.whenWrite(async () => { started(); await new Promise(resolve => { release = resolve }) })
  const saving = save(editor, api, f, one)
  await sent
  const removed = await editor.removeGroup('entry-0001', 'record-001')
  assert.equal(removed.ok, false)
  assert.match(removed.message, /正在同步/)
  assert.equal((await editor.removeGroup('entry-0001', 'record-002')).ok, true)
  release()
  assert.equal((await saving).state, 'saved')
  assert.equal(editor.draft.exercises[0].sets.length, 1)
  assert.equal(editor.draft.exercises[0].sets[0].persisted, true)
  assert.equal(editor.draft.syncPending, undefined)
  await editor.close()
})

test('a missing receipt after remote version changes retains the pending request and inputs', async () => {
  const f = fixture()
  const { editor, api } = await editorFor(f)
  f.dropNext()
  assert.equal((await save(editor, api, f)).state, 'retry')
  const marker = editor.draft.syncPending
  f.hideReceipt()
  assert.equal((await save(editor, api, f)).state, 'reconcile_required')
  assert.equal(editor.draft.syncPending, marker)
  assert.equal(editor.draft.exercises[0].sets[0].reps, '8')
  assert.equal(writes(f).length, 1)
  await editor.close()
})

test('an interrupted local stage never sends a batch', async () => {
  const f = fixture()
  const { editor, api } = await editorFor(f)
  f.native.failWrites = true
  assert.equal((await save(editor, api, f)).state, 'local_error')
  assert.equal(writes(f).length, 0)
  assert.equal(editor.draft.exercises[0].sets[0].reps, '8')
  f.native.failWrites = false
  assert.equal((await editor.retryLocalSave()).ok, true)
  await editor.close()
})

test('a staged request exceeding protected storage capacity leaves the saved input intact', async () => {
  const f = fixture()
  const { editor, api } = await editorFor(f)
  const longValue = `8${' '.repeat(35000)}`
  assert.equal((await editor.editGroup('entry-0001', 'record-001', 'reps', longValue)).ok, true)
  assert.equal((await save(editor, api, f)).state, 'local_error')
  assert.equal(writes(f).length, 0)
  assert.equal(editor.draft.syncPending, undefined)
  assert.equal(editor.draft.exercises[0].sets[0].reps, longValue)
  assert.equal(JSON.parse([...f.rows.values()][0].value).draft.exercises[0].sets[0].reps, longValue)
  await editor.close()
})

test('a failed local acknowledgement retains the durable request until local retry', async () => {
  const f = fixture()
  const { editor, api } = await editorFor(f)
  f.whenWrite(() => { f.native.failWrites = true })
  assert.equal((await save(editor, api, f)).state, 'local_error')
  const stored = JSON.parse([...f.rows.values()][0].value).draft
  assert.ok(stored.syncPending)
  assert.equal(stored.server.version, 1)
  assert.equal(editor.localStatus, 'storage_error')
  f.native.failWrites = false
  assert.equal((await editor.retryLocalSave()).ok, true)
  assert.equal(JSON.parse([...f.rows.values()][0].value).draft.server.version, 2)
  assert.equal(JSON.parse([...f.rows.values()][0].value).draft.syncPending, undefined)
  await editor.close()
})

test('a replaced token after the response leaves the original intent pending', async () => {
  const f = fixture()
  const { editor, api } = await editorFor(f)
  f.whenWrite(() => f.auth.rotate())
  assert.equal((await save(editor, api, f)).state, 'session_changed')
  const stored = JSON.parse([...f.rows.values()][0].value).draft
  assert.ok(stored.syncPending)
  assert.equal(stored.server.version, 1)
  assert.equal(stored.exercises[0].sets[0].persisted, false)
  await editor.close()
})
