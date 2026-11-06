import test from 'node:test'
import assert from 'node:assert/strict'
import { SecureDraftStorage } from './secureDraftStorage.ts'
import { TrainingEditorController } from './trainingEditorController.ts'
import { SecureOperationJournalStorage } from './secureOperationJournalStorage.ts'
import { OperationJournalError, TrainingOperationJournal } from './operationJournal.ts'
import { RecordingApi } from './recordingApi.ts'
import { dispatchFirstFreeSession } from './firstFreeSessionDispatch.ts'
import { syncFreeSessionCreate } from './syncFreeSessionCreate.ts'

let fixtureSequence = 0
const date = '2026-11-03'
const now = () => new Date('2026-11-03T01:00:00Z')
const ok = (found = false, value = '') => ({ ok: true, errorCode: '', found, value })
const fail = errorCode => ({ ok: false, errorCode, found: false, value: '', revision: '' })

function fixture() {
  const eventId = `event-dispatch-${String(++fixtureSequence).padStart(4, '0')}`
  let generation = 'generation-1'
  let token = 'synthetic-token-1'
  let clock = 1_000
  const events = []
  const auth = { get generation() { return generation },
    current: () => ({ subjectId: '101', generation, accessToken: token }),
    isCurrent: (subject, candidate) => subject === '101' && candidate === generation,
    change() { generation = 'generation-2'; token = 'synthetic-token-2' },
    rotateToken() { token = 'synthetic-token-rotated' } }
  const rows = new Map()
  const draftNative = {
    rows,
    discoverTrainingDrafts() { return ok(true, JSON.stringify({ eventIds: [], nextCursor: null, hasUnclassifiedItems: false })) },
    readVersionedTrainingDraft(key) {
      const row = rows.get(key)
      return row ? { ...ok(true, row.value), revision: row.revision } : { ...ok(), revision: '0' }
    },
    compareAndSetVersionedTrainingDraft(key, revision, found, before, value) {
      const row = rows.get(key)
      if ((row?.revision ?? '0') !== revision || !!row !== found || (row?.value ?? '') !== before) return fail('conflict')
      const next = (BigInt(revision) + 1n).toString()
      rows.set(key, { value, revision: next })
      const draft = JSON.parse(value).draft
      if (draft.syncPending) events.push('draft-ready')
      return { ...ok(), revision: next }
    },
  }
  const draftStorage = new SecureDraftStorage(draftNative, '101', auth)
  const nativeJournal = {
    value: null, revision: 0n, claimed: new Set(), failClaim: false, failReads: 0, onClaim: null,
    readTrainingOperationJournal() {
      if (this.failReads > 0) { this.failReads--; return fail('unavailable') }
      return ok(this.value !== null, this.value ?? '')
    },
    compareAndSetTrainingOperationJournal(found, before, value) {
      if ((this.value !== null) !== found || (this.value ?? '') !== before) return { ok: false, errorCode: 'conflict', found: false, value: '' }
      this.value = value; this.revision++
      const entry = Object.values(JSON.parse(value).entries)[0]
      if (entry && JSON.parse(entry).state === 'ready') events.push('journal-ready')
      if (entry && JSON.parse(entry).state === 'uncertain') events.push('journal-uncertain')
      return ok()
    },
    readVersionedTrainingOperationJournal() { return { ...this.readTrainingOperationJournal(), revision: String(this.revision) } },
    compareAndSetVersionedTrainingOperationJournal(revision, found, before, nextFound, next) {
      if (String(this.revision) !== revision || (this.value !== null) !== found || (this.value ?? '') !== before || !nextFound) return fail('conflict')
      const result = this.compareAndSetTrainingOperationJournal(found, before, next)
      return result.ok ? { ...result, revision: String(this.revision) } : fail('conflict')
    },
    claimTrainingOperationDispatch(revision, before, key, next) {
      if (this.failClaim) return fail('unavailable')
      if (String(this.revision) !== revision || this.value !== before || this.claimed.has(key)) return fail('conflict')
      this.claimed.add(key)
      this.value = next; this.revision++
      events.push('native-claim')
      this.onClaim?.()
      return { ...ok(), revision: String(this.revision) }
    },
  }
  const journal = new TrainingOperationJournal(new SecureOperationJournalStorage(nativeJournal, auth), auth,
    { retentionMs: 60_000, now: () => clock })
  let transport = async () => ({ statusCode: 201, headers: { 'Idempotency-Key-Expires-At': '2030-01-01T00:00:00Z' },
    data: { id: '42', clientEventId: eventId, date, timeZone: 'Asia/Shanghai', status: 'in_progress', source: 'free', version: 1,
      createdAt: '2026-11-03T00:00:00Z', updatedAt: '2026-11-03T00:00:00Z', recordedAt: null, sets: [], exerciseEntryKeys: [] } })
  let calls = 0
  const api = new RecordingApi('https://api.example.org', auth, async request => {
    calls++; events.push('transport'); return transport(request)
  }, { subjectId: '101', generation: 'generation-1' })
  const prepared = () => api.prepareFreeSessionRequest({ date, timeZone: 'Asia/Shanghai', clientEventId: eventId }, 'create-key-0001')
  const ids = { nextId: (() => { const values = [eventId, 'entry-0001', 'record-001']; return () => values.shift() })() }
  return { auth, eventId, events, rows, draftNative, draftStorage, nativeJournal, journal, api, prepared, ids,
    calls: () => calls, setTransport(next) { transport = next }, expirePermit() { clock = 61_000 } }
}

async function editorFor(f) {
  const result = await TrainingEditorController.create({ subjectId: '101', date, timeZone: 'Asia/Shanghai',
    storage: f.draftStorage, now, auth: f.auth, ids: f.ids })
  assert.equal(result.ok, true)
  assert.equal((await result.controller.addCustomExercise('深蹲', 'weight')).ok, true)
  assert.equal((await result.controller.editGroup('entry-0001', 'record-001', 'reps', '8')).ok, true)
  return result.controller
}

test('a real editor, protected adapters and API send the staged creation once and keep it unresolved', async () => {
  const f = fixture()
  const editor = await editorFor(f)
  const request = f.prepared()
  const result = await dispatchFirstFreeSession(editor, f.journal, f.api, f.auth, request)
  assert.equal(result.state, 'receipt_candidate')
  assert.equal(result.receipt.detail.id, '42')
  assert.equal(f.calls(), 1)
  assert.ok(f.events.indexOf('draft-ready') < f.events.indexOf('journal-ready'))
  assert.ok(f.events.indexOf('journal-ready') < f.events.indexOf('native-claim'))
  assert.ok(f.events.indexOf('native-claim') < f.events.indexOf('transport'))
  assert.ok(f.events.indexOf('transport') < f.events.indexOf('journal-uncertain'))
  assert.equal((await f.journal.readPending(f.eventId)).state, 'uncertain')
  assert.equal(JSON.parse(editor.draft.syncPending).state, 'ready')
  assert.equal(editor.draft.server, undefined)
  assert.equal(editor.draft.exercises[0].sets[0].reps, '8')
  assert.notEqual((await dispatchFirstFreeSession(editor, f.journal, f.api, f.auth, request)).state, 'receipt_candidate')
  assert.equal(f.calls(), 1)
  await editor.close()
})

test('a lost response retains both exact requests and refuses a new first send', async () => {
  const f = fixture()
  f.setTransport(async () => { throw new Error('connection lost') })
  const editor = await editorFor(f)
  assert.equal((await dispatchFirstFreeSession(editor, f.journal, f.api, f.auth, f.prepared())).state, 'reconcile_required')
  const pending = await f.journal.readPending(f.eventId)
  assert.equal(pending.state, 'uncertain')
  assert.equal(JSON.parse(editor.draft.syncPending).request.body, pending.request.body)
  assert.equal((await dispatchFirstFreeSession(editor, f.journal, f.api, f.auth, f.prepared())).state, 'reconcile_required')
  assert.equal(f.calls(), 1)
  await editor.close()
})

test('a temporary read failure after claim can retry the same unconsumed permit once', async () => {
  const f = fixture()
  const editor = await editorFor(f)
  f.nativeJournal.onClaim = () => { f.nativeJournal.failReads = 1 }
  const result = await dispatchFirstFreeSession(editor, f.journal, f.api, f.auth, f.prepared())
  assert.equal(result.state, 'retry_preflight')
  assert.equal(f.calls(), 0)
  assert.equal(f.nativeJournal.claimed.size, 1)
  const [first, second] = await Promise.all([result.retry(), result.retry()])
  assert.equal(first.state, 'receipt_candidate')
  assert.equal(second.state, 'retry_in_progress')
  assert.equal(f.calls(), 1)
  assert.equal((await result.retry()).state, 'reconcile_required')
  assert.equal(f.calls(), 1)
  assert.equal((await f.journal.readPending(f.eventId)).state, 'uncertain')
  await editor.close()
})

test('a preflight retry never sends after a session change or expiration', async () => {
  for (const outcome of ['session', 'expired']) {
    const f = fixture()
    const editor = await editorFor(f)
    f.nativeJournal.onClaim = () => { f.nativeJournal.failReads = 1 }
    const result = await dispatchFirstFreeSession(editor, f.journal, f.api, f.auth, f.prepared())
    assert.equal(result.state, 'retry_preflight')
    if (outcome === 'session') f.auth.change()
    else f.expirePermit()
    assert.equal((await result.retry()).state, outcome === 'session' ? 'session_changed' : 'reconcile_required')
    assert.equal(f.calls(), 0)
    await editor.close()
  }
})

test('an error after entering the dispatch callback cannot produce a preflight retry', async () => {
  const f = fixture()
  const editor = await editorFor(f)
  let callbackCalls = 0
  f.api.sendPreparedFreeSessionRequest = () => {
    callbackCalls++
    throw new OperationJournalError('unavailable')
  }
  const result = await dispatchFirstFreeSession(editor, f.journal, f.api, f.auth, f.prepared())
  assert.equal(result.state, 'reconcile_required')
  assert.equal(callbackCalls, 1)
  assert.equal(f.calls(), 0)
  assert.equal((await f.journal.readPending(f.eventId)).state, 'uncertain')
  await editor.close()
})

test('a request owned by another API is rejected before staging or sending', async () => {
  const f = fixture()
  const editor = await editorFor(f)
  const other = new RecordingApi('https://api.example.org', f.auth, async () => { throw new Error('must not send') },
    { subjectId: '101', generation: 'generation-1' })
  const request = other.prepareFreeSessionRequest({ date, timeZone: 'Asia/Shanghai', clientEventId: f.eventId }, 'create-key-0001')
  assert.equal((await dispatchFirstFreeSession(editor, f.journal, f.api, f.auth, request)).state, 'not_dispatched')
  assert.equal(editor.draft.syncPending, undefined)
  assert.equal(f.nativeJournal.value, null)
  assert.equal(f.calls(), 0)
  await editor.close()
})

test('an unavailable native claim never sends the staged request', async () => {
  const f = fixture()
  const editor = await editorFor(f)
  f.nativeJournal.failClaim = true
  assert.equal((await dispatchFirstFreeSession(editor, f.journal, f.api, f.auth, f.prepared())).state, 'reconcile_required')
  assert.equal((await f.journal.readPending(f.eventId)).state, 'ready')
  assert.equal(JSON.parse(editor.draft.syncPending).state, 'ready')
  assert.equal(f.calls(), 0)
  await editor.close()
})

test('a session switch during native claim never sends or releases old account data', async () => {
  const f = fixture()
  const editor = await editorFor(f)
  f.nativeJournal.onClaim = () => f.auth.change()
  assert.equal((await dispatchFirstFreeSession(editor, f.journal, f.api, f.auth, f.prepared())).state, 'session_changed')
  assert.equal(f.calls(), 0)
  assert.equal(JSON.parse(Object.values(JSON.parse(f.nativeJournal.value).entries)[0]).state, 'in_flight')
  await editor.close()
})

test('a native draft changed after claim is not used to send an old request', async () => {
  const f = fixture()
  const editor = await editorFor(f)
  f.nativeJournal.onClaim = () => {
    const row = [...f.rows.values()][0]
    const value = JSON.parse(row.value)
    value.draft.date = '2026-11-02'
    row.value = JSON.stringify(value)
    row.revision = (BigInt(row.revision) + 1n).toString()
  }
  assert.equal((await dispatchFirstFreeSession(editor, f.journal, f.api, f.auth, f.prepared())).state, 'reconcile_required')
  assert.equal(f.calls(), 0)
  assert.equal((await f.journal.readPending(f.eventId)).state, 'uncertain')
  await editor.close()
})

test('a permit that expires immediately before transport never sends', async () => {
  const f = fixture()
  const editor = await editorFor(f)
  const send = f.api.sendPreparedFreeSessionRequest.bind(f.api)
  f.api.sendPreparedFreeSessionRequest = (...args) => { f.expirePermit(); return send(...args) }
  assert.equal((await dispatchFirstFreeSession(editor, f.journal, f.api, f.auth, f.prepared())).state, 'reconcile_required')
  assert.equal(f.calls(), 0)
  assert.equal((await f.journal.readPending(f.eventId)).state, 'uncertain')
  await editor.close()
})

test('a transport guard must synchronously return true', async () => {
  for (const guard of [() => false, () => Promise.resolve(true), () => { throw new Error('private guard failure') }]) {
    const f = fixture()
    await assert.rejects(f.api.sendPreparedFreeSessionRequest(f.prepared(), guard), error => error.code === 'reconcile_required')
    assert.equal(f.calls(), 0)
  }
})

function createdResponse(f, changes = {}) {
  return { statusCode: 201, headers: { 'Idempotency-Key-Expires-At': '2030-01-01T00:00:00Z' },
    data: { id: '42', clientEventId: f.eventId, date, timeZone: 'Asia/Shanghai', status: 'in_progress', source: 'free', version: 1,
      createdAt: '2026-11-03T00:00:00Z', updatedAt: '2026-11-03T00:00:00Z', recordedAt: null,
      sets: [], exerciseEntryKeys: [], ...changes } }
}

test('creation response updates the same protected draft without erasing entered groups', async () => {
  const f = fixture()
  const editor = await editorFor(f)
  const result = await syncFreeSessionCreate(editor, f.api, f.auth, () => 'create-key-0001')
  assert.equal(result.state, 'created')
  assert.equal(f.calls(), 1)
  assert.deepEqual(editor.draft.server, { id: '42', version: 1, status: 'in_progress' })
  assert.equal(editor.draft.syncPending, undefined)
  assert.equal(editor.draft.exercises[0].sets[0].reps, '8')
  const stored = JSON.parse([...f.rows.values()][0].value).draft
  assert.equal(stored.syncPending, undefined)
  assert.deepEqual(stored.server, editor.draft.server)
  assert.equal(stored.exercises[0].sets[0].reps, '8')
  await editor.close()
})

test('lost response followed by a new login retries the same request and keeps local input', async () => {
  const f = fixture()
  const requests = []
  f.setTransport(async request => { requests.push(request); throw new Error('response lost') })
  const editor = await editorFor(f)
  assert.equal((await syncFreeSessionCreate(editor, f.api, f.auth, () => 'create-key-0001')).state, 'retry')
  const marker = editor.draft.syncPending
  assert.ok(marker)
  await editor.close()
  f.auth.change()
  const reopened = await TrainingEditorController.attach('101', f.eventId,
    new SecureDraftStorage(f.draftNative, '101', f.auth), now, f.auth, f.ids)
  assert.equal(reopened.ok, true)
  const nextApi = new RecordingApi('https://api.example.org', f.auth, async request => {
    requests.push(request)
    return createdResponse(f)
  }, { subjectId: '101', generation: 'generation-2' })
  const retry = await syncFreeSessionCreate(reopened.controller, nextApi, f.auth, () => { throw new Error('must reuse key') })
  assert.equal(retry.state, 'created')
  assert.equal(requests.length, 2)
  assert.equal(requests[0].data, requests[1].data)
  assert.equal(requests[0].header['Idempotency-Key'], requests[1].header['Idempotency-Key'])
  assert.equal(JSON.parse(marker).request.body, requests[1].data)
  assert.equal(reopened.controller.draft.exercises[0].sets[0].reps, '8')
  assert.equal(reopened.controller.draft.syncPending, undefined)
  await reopened.controller.close()
})

test('edits made while creating a session survive the response', async () => {
  const f = fixture()
  let release
  let started
  const sent = new Promise(resolve => { started = resolve })
  f.setTransport(async () => {
    started()
    await new Promise(resolve => { release = resolve })
    return createdResponse(f)
  })
  const editor = await editorFor(f)
  const saving = syncFreeSessionCreate(editor, f.api, f.auth, () => 'create-key-0001')
  await sent
  assert.equal((await editor.editGroup('entry-0001', 'record-001', 'reps', '9')).ok, true)
  release()
  assert.equal((await saving).state, 'created')
  assert.equal(editor.draft.exercises[0].sets[0].reps, '9')
  assert.equal(JSON.parse([...f.rows.values()][0].value).draft.exercises[0].sets[0].reps, '9')
  await editor.close()
})

test('repeated save while creation is in flight does not send a second request', async () => {
  const f = fixture()
  let release
  let started
  const sent = new Promise(resolve => { started = resolve })
  f.setTransport(async () => { started(); await new Promise(resolve => { release = resolve }); return createdResponse(f) })
  const editor = await editorFor(f)
  const first = syncFreeSessionCreate(editor, f.api, f.auth, () => 'create-key-0001')
  await sent
  assert.equal((await syncFreeSessionCreate(editor, f.api, f.auth, () => 'another-create-key')).state, 'busy')
  release()
  assert.equal((await first).state, 'created')
  assert.equal(f.calls(), 1)
  await editor.close()
})

test('a failed local acknowledgement retains the original intent until the local write is confirmed', async () => {
  const f = fixture()
  const editor = await editorFor(f)
  const originalWrite = f.draftNative.compareAndSetVersionedTrainingDraft
  f.setTransport(async () => {
    let failOnce = true
    f.draftNative.compareAndSetVersionedTrainingDraft = function (...args) {
      if (failOnce) { failOnce = false; return fail('unavailable') }
      return originalWrite.apply(this, args)
    }
    return createdResponse(f)
  })
  assert.equal((await syncFreeSessionCreate(editor, f.api, f.auth, () => 'create-key-0001')).state, 'local_error')
  const beforeRetry = JSON.parse([...f.rows.values()][0].value).draft
  assert.ok(beforeRetry.syncPending)
  assert.equal(beforeRetry.server, undefined)
  assert.equal(editor.localStatus, 'storage_error')
  assert.equal((await editor.retryLocalSave()).ok, true)
  const committed = JSON.parse([...f.rows.values()][0].value).draft
  assert.equal(committed.syncPending, undefined)
  assert.equal(committed.server.id, '42')
  assert.equal(committed.exercises[0].sets[0].reps, '8')
  assert.equal(f.calls(), 1)
  await editor.close()
})

test('a token replacement after the response cannot apply the previous acknowledgement', async () => {
  const f = fixture()
  const editor = await editorFor(f)
  const send = f.api.sendPreparedFreeSessionRequest.bind(f.api)
  f.api.sendPreparedFreeSessionRequest = (...args) => send(...args).then(result => {
    f.auth.rotateToken()
    return result
  })
  assert.equal((await syncFreeSessionCreate(editor, f.api, f.auth, () => 'create-key-0001')).state, 'session_changed')
  const stored = JSON.parse([...f.rows.values()][0].value).draft
  assert.ok(stored.syncPending)
  assert.equal(stored.server, undefined)
  await editor.close()
})

test('a newer remote create snapshot cannot overwrite an unresolved local draft', async () => {
  const f = fixture()
  f.setTransport(async () => createdResponse(f, { version: 2, updatedAt: '2026-11-03T00:01:00Z',
    exerciseEntryKeys: ['remote-entry-01'], sets: [{ recordKey: 'remote-record-01', exerciseEntryKey: 'remote-entry-01',
      exerciseName: '远端动作', resolvedExerciseName: '远端动作', setIndex: 1, kind: 'bodyweight', reps: 5 }] }))
  const editor = await editorFor(f)
  const result = await syncFreeSessionCreate(editor, f.api, f.auth, () => 'create-key-0001')
  assert.equal(result.state, 'reconcile_required')
  assert.equal(editor.draft.server, undefined)
  assert.ok(editor.draft.syncPending)
  assert.equal(editor.draft.exercises[0].sets[0].reps, '8')
  await editor.close()
})

test('an account change during create keeps the original draft pending', async () => {
  const f = fixture()
  f.setTransport(async () => { f.auth.change(); return createdResponse(f) })
  const editor = await editorFor(f)
  const result = await syncFreeSessionCreate(editor, f.api, f.auth, () => 'create-key-0001')
  assert.equal(result.state, 'session_changed')
  const stored = JSON.parse([...f.rows.values()][0].value).draft
  assert.ok(stored.syncPending)
  assert.equal(stored.server, undefined)
  await editor.close()
})

test('a forged acknowledgement cannot clear a pending create request', async () => {
  const f = fixture()
  const editor = await editorFor(f)
  const prepared = f.prepared()
  assert.equal((await editor.stageFreeSession(prepared)).ok, true)
  const marker = editor.draft.syncPending
  assert.equal((await editor.acknowledgeFreeSession(prepared, { lease: { subjectId: '101', generation: 'generation-1' },
    detail: createdResponse(f).data })).ok, false)
  assert.equal(editor.draft.syncPending, marker)
  assert.equal(editor.draft.server, undefined)
  await editor.close()
})
