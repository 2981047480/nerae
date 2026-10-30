import assert from 'node:assert/strict'
import { registerHooks } from 'node:module'
import { test } from 'node:test'
import { addExercise, confirmSet, createRecordingDraft, removeUnsubmittedSet, switchMode, updateSet } from './recording.ts'

const hooks = registerHooks({
  resolve(specifier, context, nextResolve) {
    if (specifier === './recording' && /\/(recordingApi|recordingProjection|draftStorage)\.ts$/.test(context.parentURL || '')) {
      return nextResolve('./recording.ts', context)
    }
    if (specifier === './recordingApi' && context.parentURL?.endsWith('/recordingProjection.ts')) {
      return nextResolve('./recordingApi.ts', context)
    }
    return nextResolve(specifier, context)
  },
})
const { applySetsAcknowledgement, prepareOrderedAfterWorkout, prepareOrderedConfirmedSet, restoreConfirmedSession } = await import('./recordingProjection.ts')
const { RecordingApi } = await import('./recordingApi.ts')
const { DraftController } = await import('./draftStorage.ts')
hooks.deregister()

const today = '2026-10-30'
const lease = { subjectId: '101', generation: 'login-1' }
const credential = { ...lease, accessToken: 'synthetic-session-token' }
const auth = { current: () => credential, isCurrent: (id, generation) => id === lease.subjectId && generation === lease.generation }
const detail = overrides => ({
  id: '42', clientEventId: 'event-0001', date: today, timeZone: 'Asia/Shanghai', status: 'in_progress', source: 'free', version: 2,
  createdAt: '2026-10-30T01:00:00Z', updatedAt: '2026-10-30T01:00:00Z', recordedAt: null,
  exerciseEntryKeys: ['entry-0001'], sets: [serverA], ...overrides,
})
const serverA = { recordKey: 'record-001', exerciseEntryKey: 'entry-0001', exerciseName: '深蹲',
  resolvedExerciseName: '深蹲', setIndex: 1, kind: 'bodyweight', reps: 8 }
const scoped = overrides => new RecordingApi('https://api.example.org', auth, async () => ({ statusCode: 200, data: detail(overrides) }), lease).readSession('42')
function draft() {
  let value = createRecordingDraft('event-0001', today, 'Asia/Shanghai', today)
  value = addExercise(value, 'entry-0001', { source: 'custom', exerciseName: '深蹲' }, 'bodyweight', 'record-001')
  value = updateSet(value, 'entry-0001', 'record-001', 'reps', '8')
  value = confirmSet(value, 'entry-0001', 'record-001').draft
  value = { ...value, server: { id: '42', version: 2 }, exercises: [{ ...value.exercises[0],
    sets: value.exercises[0].sets.map(set => ({ ...set, persisted: true, dirty: false })) }] }
  value = addExercise(value, 'entry-0002', { source: 'custom', exerciseName: '划船' }, 'bodyweight', 'record-002')
  value = updateSet(value, 'entry-0002', 'record-002', 'reps', '6')
  value = addExercise(value, 'entry-0003', { source: 'custom', exerciseName: '俯卧撑' }, 'bodyweight', 'record-003')
  value = updateSet(value, 'entry-0003', 'record-003', 'reps', '9')
  return value
}

function perSet(value, entry, key) {
  value = switchMode(value, 'per_set')
  return confirmSet(value, entry, key).draft
}

test('per-set projection submits only the confirmed group and retains existing server entries in order', async () => {
  const local = perSet(draft(), 'entry-0002', 'record-002')
  const before = JSON.stringify(local)
  const result = prepareOrderedConfirmedSet(local, await scoped(), lease, auth, 'entry-0002', 'record-002', today)
  assert.equal(result.ok, true)
  assert.equal(result.state, 'send')
  assert.deepEqual(result.exerciseEntryKeys, ['entry-0001', 'entry-0002'])
  assert.deepEqual(result.sets.map(set => set.recordKey), ['record-002'])
  assert.equal(result.expectedVersion, 2)
  assert.equal(JSON.stringify(local), before)
  assert.equal(prepareOrderedConfirmedSet(local, await scoped(), lease, auth, 'entry-0003', 'record-003', today).ok, false)
  assert.deepEqual(prepareOrderedConfirmedSet(local, await scoped(), lease, auth, 'entry-0001', 'record-001', today), { ok: true, state: 'already_saved' })
})

test('after-workout save includes complete groups and excludes blank entries from the order', async () => {
  let local = draft()
  local = addExercise(local, 'entry-0004', { source: 'custom', exerciseName: '硬拉' }, 'bodyweight', 'record-004')
  const result = prepareOrderedAfterWorkout(local, await scoped(), lease, auth, today)
  assert.equal(result.ok, true)
  assert.deepEqual(result.exerciseEntryKeys, ['entry-0001', 'entry-0002', 'entry-0003'])
  assert.deepEqual(result.sets.map(set => set.recordKey), ['record-001', 'record-002', 'record-003'])
})

test('per-set projection keeps another unsaved edit local and does not upload it', async () => {
  let local = draft()
  local = updateSet(local, 'entry-0001', 'record-001', 'reps', '10')
  local = perSet(local, 'entry-0002', 'record-002')
  const result = prepareOrderedConfirmedSet(local, await scoped(), lease, auth, 'entry-0002', 'record-002', today)
  assert.equal(result.ok, true)
  assert.deepEqual(result.sets.map(set => set.recordKey), ['record-002'])
  assert.equal(local.exercises[0].sets[0].dirty, true)
  assert.equal(local.exercises[0].sets[0].reps, '10')
})

test('stale, incomplete or inconsistent snapshots require reconciliation without mutating input', async () => {
  const local = perSet(draft(), 'entry-0002', 'record-002')
  const before = JSON.stringify(local)
  const snapshots = await Promise.all([
    scoped({ version: 3 }),
    scoped({ sets: [], exerciseEntryKeys: [] }),
    scoped({ sets: [{ ...serverA, reps: 7 }] }),
    scoped({ sets: [{ ...serverA, setIndex: 2 }] }),
    scoped({ source: 'plan' }),
    scoped({ status: 'skipped', sets: [], exerciseEntryKeys: [] }),
  ])
  for (const snapshot of snapshots) {
    const result = prepareOrderedConfirmedSet(local, snapshot, lease, auth, 'entry-0002', 'record-002', today)
    assert.equal(result.ok, false)
    assert.equal(result.reason, 'reconcile_required')
  }
  const missingFlag = JSON.parse(before)
  missingFlag.exercises[0].sets[0].persisted = false
  assert.equal(prepareOrderedConfirmedSet(missingFlag, await scoped(), lease, auth, 'entry-0002', 'record-002', today).reason, 'reconcile_required')
  assert.equal(JSON.stringify(local), before)
})

test('writes reject a local client event identity that differs from the issued session snapshot', async () => {
  const snapshot = await scoped()
  const afterWorkout = { ...draft(), clientEventId: 'event-0002' }
  const afterBefore = JSON.stringify(afterWorkout)
  assert.equal(prepareOrderedAfterWorkout(afterWorkout, snapshot, lease, auth, today).reason, 'reconcile_required')
  assert.equal(JSON.stringify(afterWorkout), afterBefore)

  const perSetDraft = perSet(draft(), 'entry-0002', 'record-002')
  const wrongEvent = { ...perSetDraft, clientEventId: 'event-0002' }
  const perSetBefore = JSON.stringify(wrongEvent)
  assert.equal(prepareOrderedConfirmedSet(wrongEvent, snapshot, lease, auth, 'entry-0002', 'record-002', today).reason,
    'reconcile_required')
  assert.equal(JSON.stringify(wrongEvent), perSetBefore)
})

test('snapshot and draft leases must match the current authenticated login', async () => {
  const local = perSet(draft(), 'entry-0002', 'record-002')
  const issued = await scoped()
  const other = { ...issued, lease: { subjectId: '202', generation: 'login-2' } }
  assert.equal(prepareOrderedConfirmedSet(local, other, lease, auth, 'entry-0002', 'record-002', today).reason, 'session_changed')
  assert.equal(prepareOrderedConfirmedSet(local, { ...issued }, lease, auth, 'entry-0002', 'record-002', today).reason, 'reconcile_required')
  const switched = { current: () => ({ ...credential, generation: 'login-2' }), isCurrent: () => false }
  assert.equal(prepareOrderedConfirmedSet(local, await scoped(), lease, switched, 'entry-0002', 'record-002', today).reason, 'session_changed')
})

test('projection feeds the bound HTTP adapter without submitting unconfirmed groups', async () => {
  const local = perSet(draft(), 'entry-0002', 'record-002')
  const requests = []
  const serverB = { recordKey: 'record-002', exerciseEntryKey: 'entry-0002', exerciseName: '划船',
    resolvedExerciseName: '划船', setIndex: 1, kind: 'bodyweight', reps: 6 }
  const api = new RecordingApi('https://api.example.org', auth, async request => {
    requests.push(request)
    return request.method === 'GET' ? { statusCode: 200, data: detail() }
      : { statusCode: 200, data: detail({ version: 3, exerciseEntryKeys: ['entry-0001', 'entry-0002'], sets: [serverA, serverB] }) }
  }, lease)
  const snapshot = await api.readSession('42')
  const result = prepareOrderedConfirmedSet(local, snapshot, lease, auth, 'entry-0002', 'record-002', today)
  assert.equal(result.state, 'send')
  const saved = await api.saveSets(result.sessionId, result.expectedVersion, result.sets, result.exerciseEntryKeys, 'update-0001', snapshot)
  assert.equal(saved.detail.version, 3)
  assert.deepEqual(JSON.parse(requests[1].data).sets.map(set => set.recordKey), ['record-002'])
  assert.deepEqual(JSON.parse(requests[1].data).exerciseEntryKeys, ['entry-0001', 'entry-0002'])
  assert.equal(Object.isFrozen(snapshot.detail.sets[0]), true)
  assert.throws(() => { snapshot.detail.sets[0].reps = 99 }, TypeError)
})

test('switching login between projection and submission blocks the old draft before network access', async () => {
  let generation = 'login-1'
  let requests = 0
  const currentAuth = {
    current: () => ({ ...credential, generation }),
    isCurrent: (id, captured) => id === '101' && captured === generation,
  }
  const api = new RecordingApi('https://api.example.org', currentAuth, async () => {
    requests++
    return { statusCode: 200, data: detail() }
  }, lease)
  const snapshot = await api.readSession('42')
  const local = perSet(draft(), 'entry-0002', 'record-002')
  const prepared = prepareOrderedConfirmedSet(local, snapshot, lease, currentAuth, 'entry-0002', 'record-002', today)
  assert.equal(prepared.state, 'send')
  generation = 'login-2'
  assert.throws(() => api.saveSets(prepared.sessionId, prepared.expectedVersion, prepared.sets, prepared.exerciseEntryKeys, 'update-0001', snapshot),
    error => error instanceof Error && error.code === 'session_changed')
  assert.equal(requests, 1)
  assert.equal(local.exercises[1].sets[0].reps, '6')
})

test('local reordering of existing server entries cannot be sent as a side effect of confirming a new group', async () => {
  let local = draft()
  local = { ...local, exercises: [local.exercises[1], local.exercises[0], local.exercises[2]] }
  local = { ...local, exercises: local.exercises.map(exercise => exercise.exerciseEntryKey === 'entry-0002'
    ? { ...exercise, sets: exercise.sets.map(set => ({ ...set, persisted: true, dirty: false, confirmed: true })) } : exercise) }
  local = perSet(local, 'entry-0003', 'record-003')
  const serverB = { recordKey: 'record-002', exerciseEntryKey: 'entry-0002', exerciseName: '划船',
    resolvedExerciseName: '划船', setIndex: 1, kind: 'bodyweight', reps: 6 }
  const snapshot = await scoped({ exerciseEntryKeys: ['entry-0001', 'entry-0002'], sets: [serverA, serverB] })
  const result = prepareOrderedConfirmedSet(local, snapshot, lease, auth, 'entry-0003', 'record-003', today)
  assert.equal(result.ok, false)
  assert.equal(result.reason, 'reconcile_required')
})


async function pendingPerSetWrite(local = perSet(draft(), 'entry-0002', 'record-002'), duringRequest = value => value) {
  let release
  const api = new RecordingApi('https://api.example.org', auth, request => {
    if (request.method === 'GET') return Promise.resolve({ statusCode: 200, data: detail() })
    return new Promise(resolve => { release = resolve })
  }, lease)
  const baseline = await api.readSession('42')
  const prepared = prepareOrderedConfirmedSet(local, baseline, lease, auth, 'entry-0002', 'record-002', today)
  assert.equal(prepared.state, 'send')
  const pending = api.saveSets(prepared.sessionId, prepared.expectedVersion, prepared.sets, prepared.exerciseEntryKeys, 'update-0001', baseline)
  const edited = duringRequest(local)
  const saved = { recordKey: 'record-002', exerciseEntryKey: 'entry-0002', exerciseName: '划船',
    resolvedExerciseName: '划船', setIndex: 1, kind: 'bodyweight', reps: 6 }
  release({ statusCode: 200, data: detail({ version: 3, exerciseEntryKeys: ['entry-0001', 'entry-0002'], sets: [serverA, saved] }) })
  return { api, baseline, prepared, response: await pending, local, edited }
}

test('a matching per-set acknowledgement marks only the submitted group saved', async () => {
  const { prepared, response, local } = await pendingPerSetWrite()
  const result = applySetsAcknowledgement(local, prepared, response, auth, today)
  assert.equal(result.ok, true)
  assert.equal(result.draft.server.version, 3)
  assert.deepEqual({ persisted: result.draft.exercises[1].sets[0].persisted, dirty: result.draft.exercises[1].sets[0].dirty,
    confirmed: result.draft.exercises[1].sets[0].confirmed }, { persisted: true, dirty: false, confirmed: true })
  assert.equal(result.draft.exercises[2].sets[0].persisted, false)
  assert.equal(result.draft.exercises[2].sets[0].reps, '9')
  assert.equal(local.server.version, 2)
  assert.equal(local.exercises[1].sets[0].persisted, false)
})

test('edits made while a per-set request is in flight remain visible and unsaved after its acknowledgement', async () => {
  const local = perSet(draft(), 'entry-0002', 'record-002')
  const { prepared, response, edited } = await pendingPerSetWrite(local, value => {
    value = updateSet(value, 'entry-0002', 'record-002', 'reps', '7')
    return updateSet(value, 'entry-0003', 'record-003', 'reps', '10')
  })
  const result = applySetsAcknowledgement(edited, prepared, response, auth, today)
  assert.equal(result.ok, true)
  assert.equal(result.draft.server.version, 3)
  assert.deepEqual({ reps: result.draft.exercises[1].sets[0].reps, persisted: result.draft.exercises[1].sets[0].persisted,
    dirty: result.draft.exercises[1].sets[0].dirty, confirmed: result.draft.exercises[1].sets[0].confirmed },
    { reps: '7', persisted: true, dirty: true, confirmed: false })
  assert.equal(result.draft.exercises[2].sets[0].reps, '10')
  assert.equal(result.draft.exercises[2].sets[0].persisted, false)
})

test('a removed submitted group or changed session requires reconciliation without mutating the draft', async () => {
  const { prepared, response, local } = await pendingPerSetWrite()
  const removed = removeUnsubmittedSet(local, 'entry-0002', 'record-002')
  const before = JSON.stringify(removed)
  assert.equal(applySetsAcknowledgement(removed, prepared, response, auth, today).reason, 'reconcile_required')
  assert.equal(JSON.stringify(removed), before)
  const wrongEvent = { ...local, clientEventId: 'event-0002' }
  assert.equal(applySetsAcknowledgement(wrongEvent, prepared, response, auth, today).reason, 'reconcile_required')
  const otherVersion = { ...local, server: { ...local.server, version: 4 } }
  assert.equal(applySetsAcknowledgement(otherVersion, prepared, response, auth, today).reason, 'reconcile_required')
  const switched = { current: () => ({ ...credential, generation: 'login-2' }), isCurrent: () => false }
  assert.equal(applySetsAcknowledgement(local, prepared, response, switched, today).reason, 'session_changed')
})

test('a read snapshot, copied preparation or copied acknowledgement cannot stand in for this write', async () => {
  const { api, prepared, response, local } = await pendingPerSetWrite()
  const read = await api.readSession('42')
  assert.equal(applySetsAcknowledgement(local, prepared, read, auth, today).reason, 'reconcile_required')
  assert.equal(applySetsAcknowledgement(local, { ...prepared }, response, auth, today).reason, 'reconcile_required')
  assert.equal(applySetsAcknowledgement(local, prepared, { ...response }, auth, today).reason, 'reconcile_required')
  assert.equal(Object.isFrozen(prepared.sets), true)
  assert.throws(() => { prepared.sets[0].reps = 99 }, TypeError)
})

test('after-workout acknowledgement keeps newer edits and confirms unchanged submitted groups', async () => {
  const local = draft()
  let release
  const api = new RecordingApi('https://api.example.org', auth, request => {
    if (request.method === 'GET') return Promise.resolve({ statusCode: 200, data: detail() })
    return new Promise(resolve => { release = resolve })
  }, lease)
  const baseline = await api.readSession('42')
  const prepared = prepareOrderedAfterWorkout(local, baseline, lease, auth, today)
  const pending = api.saveSets(prepared.sessionId, prepared.expectedVersion, prepared.sets, prepared.exerciseEntryKeys, 'update-0001', baseline)
  let edited = updateSet(local, 'entry-0003', 'record-003', 'reps', '11')
  edited = addExercise(edited, 'entry-0004', { source: 'custom', exerciseName: '硬拉' }, 'bodyweight', 'record-004')
  const b = { recordKey: 'record-002', exerciseEntryKey: 'entry-0002', exerciseName: '划船', resolvedExerciseName: '划船', setIndex: 1, kind: 'bodyweight', reps: 6 }
  const c = { recordKey: 'record-003', exerciseEntryKey: 'entry-0003', exerciseName: '俯卧撑', resolvedExerciseName: '俯卧撑', setIndex: 1, kind: 'bodyweight', reps: 9 }
  release({ statusCode: 200, data: detail({ version: 3, exerciseEntryKeys: ['entry-0001', 'entry-0002', 'entry-0003'], sets: [serverA, b, c] }) })
  const result = applySetsAcknowledgement(edited, prepared, await pending, auth, today)
  assert.equal(result.ok, true)
  assert.equal(result.draft.exercises[1].sets[0].confirmed, true)
  assert.equal(result.draft.exercises[1].sets[0].dirty, false)
  assert.equal(result.draft.exercises[2].sets[0].reps, '11')
  assert.equal(result.draft.exercises[2].sets[0].dirty, true)
  assert.equal(result.draft.exercises[2].sets[0].confirmed, false)
  assert.equal(result.draft.exercises[3].sets[0].persisted, false)
})


test('merging into the controller keeps the latest edit visible if local persistence fails', async () => {
  const local = perSet(draft(), 'entry-0002', 'record-002')
  const { prepared, response } = await pendingPerSetWrite(local)
  const controller = new DraftController('101', local, { read: async () => null, write: async () => { throw new Error('disk full') } },
    () => new Date('2026-10-30T12:00:00Z'), { generation: lease.generation, isCurrent: auth.isCurrent })
  await controller.update(value => updateSet(value, 'entry-0002', 'record-002', 'reps', '7'))
  const persisted = await controller.update(value => {
    const result = applySetsAcknowledgement(value, prepared, response, auth, today)
    assert.equal(result.ok, true)
    return result.draft
  })
  assert.equal(persisted, false)
  assert.equal(controller.status, 'storage_error')
  assert.equal(controller.draft.server.version, 3)
  assert.equal(controller.draft.exercises[1].sets[0].reps, '7')
  assert.equal(controller.draft.exercises[1].sets[0].dirty, true)
})

test('cross-device restore preserves same-named actions, numeric values, ordering and sparse set indexes', async () => {
  const firstSparse = { ...serverA, setIndex: 2, kind: 'weight', weightKg: 50, reps: 8 }
  const secondSparse = { ...serverA, recordKey: 'record-002', setIndex: 7, kind: 'weight', weightKg: 52.5, reps: 6 }
  const sameNameSecond = { ...serverA, recordKey: 'record-007', exerciseEntryKey: 'entry-0002', setIndex: 3, reps: 5 }
  const snapshot = await scoped({ sets: [firstSparse, secondSparse, sameNameSecond],
    exerciseEntryKeys: ['entry-0001', 'entry-0002'], durationMin: 42, rpe: 7.5, note: '服务端已确认' })
  const result = restoreConfirmedSession(snapshot, auth, today)
  assert.equal(result.ok, true)
  assert.equal(result.draft.clientEventId, 'event-0001')
  assert.equal(result.draft.server.status, 'in_progress')
  assert.equal(result.draft.exercises.length, 2)
  assert.deepEqual(result.draft.exercises.map(exercise => exercise.identity), [
    { source: 'custom', exerciseName: '深蹲' }, { source: 'custom', exerciseName: '深蹲' },
  ])
  assert.deepEqual(result.draft.exercises[0].sets.map(group => [group.recordKey, group.setIndex, group.weightKg, group.reps]), [
    ['record-001', 2, '50', '8'], ['record-002', 7, '52.5', '6'],
  ])
  assert.deepEqual(result.draft.exercises[1].sets.map(group => [group.recordKey, group.setIndex, group.reps]), [['record-007', 3, '5']])
  assert.ok(result.draft.exercises.flatMap(exercise => exercise.sets).every(group => group.persisted && !group.dirty && group.confirmed))
  assert.deepEqual(result.draft.finish, { durationMin: 42, rpe: 7.5, note: '服务端已确认' })
})

test('cross-device restore preserves completed status without deriving end time from recordedAt', async () => {
  const completed = await scoped({ status: 'completed', recordedAt: '2026-10-30T02:03:04Z', sets: [serverA],
    exerciseEntryKeys: ['entry-0001'], durationMin: null, rpe: null, note: null })
  const result = restoreConfirmedSession(completed, auth, today)
  assert.equal(result.ok, true)
  assert.equal(result.draft.server.status, 'completed')
  assert.equal(result.draft.finish.durationMin, null)
  assert.equal(result.draft.finish.rpe, null)
  assert.equal(result.draft.finish.note, null)
  assert.equal('recordedAt' in result.draft, false)
  assert.equal('completedAt' in result.draft, false)
})

test('cross-device restore retains the frozen catalog display name', async () => {
  const catalogSet = { ...serverA, exerciseName: undefined, exerciseId: '17', resolvedExerciseName: '冻结名称' }
  const snapshot = await scoped({ sets: [catalogSet], exerciseEntryKeys: ['entry-0001'] })
  const result = restoreConfirmedSession(snapshot, auth, today)
  assert.equal(result.ok, true)
  assert.deepEqual(result.draft.exercises[0].identity, { source: 'catalog', exerciseId: '17' })
  assert.equal(result.draft.exercises[0].resolvedExerciseName, '冻结名称')
})

test('cross-device restore rejects mixed set kinds and unsupported statuses but accepts an empty draft', async () => {
  const mixed = await scoped({ sets: [serverA, { ...serverA, recordKey: 'record-002', setIndex: 2,
    kind: 'weight', weightKg: 20, reps: 8 }], exerciseEntryKeys: ['entry-0001'] })
  assert.equal(restoreConfirmedSession(mixed, auth, today).reason, 'unsupported')
  assert.equal(restoreConfirmedSession(await scoped({ source: 'plan' }), auth, today).reason, 'unsupported')
  assert.equal(restoreConfirmedSession(await scoped({ status: 'skipped', sets: [], exerciseEntryKeys: [] }), auth, today).reason, 'unsupported')
  const completedEmpty = new RecordingApi('https://api.example.org', auth,
    async () => ({ statusCode: 200, data: detail({ status: 'completed', sets: [], exerciseEntryKeys: [], recordedAt: '2026-10-30T02:03:04Z' }) }), lease)
  await assert.rejects(completedEmpty.readSession('42'), error => error.code === 'invalid_response')
  const empty = restoreConfirmedSession(await scoped({ sets: [], exerciseEntryKeys: [] }), auth, today)
  assert.equal(empty.ok, true)
  assert.deepEqual(empty.draft.exercises, [])
})

test('cross-device restore rejects forged snapshots, other accounts, changed sessions and identity/version conflicts', async () => {
  const issued = await scoped()
  assert.equal(restoreConfirmedSession({ ...issued }, auth, today).reason, 'invalid_snapshot')
  const otherLease = { subjectId: '202', generation: 'login-2' }
  const otherAuth = { current: () => ({ ...otherLease, accessToken: 'other-token' }),
    isCurrent: (id, generation) => id === otherLease.subjectId && generation === otherLease.generation }
  const otherSnapshot = await new RecordingApi('https://api.example.org', otherAuth,
    async () => ({ statusCode: 200, data: detail() }), otherLease).readSession('42')
  assert.equal(restoreConfirmedSession(otherSnapshot, auth, today).reason, 'session_changed')
  const switched = { current: () => ({ ...credential, generation: 'login-2' }), isCurrent: () => false }
  assert.equal(restoreConfirmedSession(issued, switched, today).reason, 'session_changed')

  const local = restoreConfirmedSession(issued, auth, today).draft
  assert.equal(restoreConfirmedSession(await scoped({ version: 3 }), auth, today, local).reason, 'reconcile_required')
  assert.equal(restoreConfirmedSession(await scoped({ clientEventId: 'event-0002' }), auth, today, local).reason, 'reconcile_required')
})

test('cross-device restore preserves unsynced local drafts and requires coordination', async () => {
  const remote = await scoped()
  let local = createRecordingDraft('event-0001', today, 'Asia/Shanghai', today)
  local = addExercise(local, 'entry-0001', { source: 'custom', exerciseName: '深蹲' }, 'bodyweight', 'record-001')
  local = updateSet(local, 'entry-0001', 'record-001', 'reps', '9')
  const before = JSON.stringify(local)
  const result = restoreConfirmedSession(remote, auth, today, local)
  assert.equal(result.ok, false)
  assert.equal(result.reason, 'local_unsynced')
  assert.equal(JSON.stringify(local), before)

  const dirty = restoreConfirmedSession(remote, auth, today).draft
  const edited = updateSet(dirty, 'entry-0001', 'record-001', 'reps', '10')
  const dirtyBefore = JSON.stringify(edited)
  assert.equal(restoreConfirmedSession(remote, auth, today, edited).reason, 'local_unsynced')
  assert.equal(JSON.stringify(edited), dirtyBefore)

  const emptyAction = removeUnsubmittedSet(local, 'entry-0001', 'record-001')
  const emptyActionBefore = JSON.stringify(emptyAction)
  assert.equal(emptyAction.exercises[0].sets.length, 0)
  assert.equal(restoreConfirmedSession(remote, auth, today, emptyAction).reason, 'local_unsynced')
  assert.equal(JSON.stringify(emptyAction), emptyActionBefore)
})

test('restore rechecks the live lease before returning and leaves the supplied draft untouched on a session race', async () => {
  const snapshot = await scoped()
  const existing = restoreConfirmedSession(snapshot, auth, today).draft
  const before = JSON.stringify(existing)
  let generation = 'login-1'
  let checks = 0
  const racingAuth = {
    current: () => ({ ...credential, generation }),
    isCurrent: (subjectId, capturedGeneration) => {
      checks++
      if (checks === 1) { generation = 'login-2'; return true }
      return subjectId === '101' && capturedGeneration === generation
    },
  }
  const result = restoreConfirmedSession(snapshot, racingAuth, today, existing)
  assert.equal(result.ok, false)
  assert.equal(result.reason, 'session_changed')
  assert.equal(checks, 2)
  assert.equal(JSON.stringify(existing), before)
})

test('a matching clean local draft keeps its selected mode on restore', async () => {
  const remote = await scoped()
  const local = switchMode(restoreConfirmedSession(remote, auth, today).draft, 'per_set')
  const restored = restoreConfirmedSession(remote, auth, today, local)
  assert.equal(restored.ok, true)
  assert.equal(restored.draft.mode, 'per_set')
  assert.equal(restored.draft.server.id, '42')
  assert.equal(restored.draft.server.version, 2)
})
