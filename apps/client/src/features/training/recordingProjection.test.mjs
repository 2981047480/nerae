import assert from 'node:assert/strict'
import { registerHooks } from 'node:module'
import { test } from 'node:test'
import { addExercise, confirmSet, createRecordingDraft, switchMode, updateSet } from './recording.ts'

const hooks = registerHooks({
  resolve(specifier, context, nextResolve) {
    if (specifier === './recording' && /\/(recordingApi|recordingProjection)\.ts$/.test(context.parentURL || '')) {
      return nextResolve('./recording.ts', context)
    }
    if (specifier === './recordingApi' && context.parentURL?.endsWith('/recordingProjection.ts')) {
      return nextResolve('./recordingApi.ts', context)
    }
    return nextResolve(specifier, context)
  },
})
const { prepareOrderedAfterWorkout, prepareOrderedConfirmedSet } = await import('./recordingProjection.ts')
const { RecordingApi } = await import('./recordingApi.ts')
hooks.deregister()

const today = '2026-10-30'
const lease = { subjectId: '101', generation: 'login-1' }
const credential = { ...lease, accessToken: 'synthetic-session-token' }
const auth = { current: () => credential, isCurrent: (id, generation) => id === lease.subjectId && generation === lease.generation }
const detail = overrides => ({
  id: '42', date: today, timeZone: 'Asia/Shanghai', status: 'in_progress', source: 'free', version: 2,
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
