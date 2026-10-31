import assert from 'node:assert/strict'
import { test } from 'node:test'
import { addExercise, createRecordingDraft, updateSet } from './recording.ts'
import { DraftController } from './draftStorage.ts'
import { TrainingEditorController } from './trainingEditorController.ts'

const now = () => new Date('2026-10-29T03:00:00Z')

function storage({ fail = false } = {}) {
  const data = new Map()
  let writes = 0
  let failing = fail
  return {
    data,
    get writes() { return writes },
    setFail(value) { failing = value },
    async read(key) { if (!data.has(key)) throw new Error('missing'); return data.get(key) },
    async write(key, value) {
      writes++
      if (failing) throw new Error('sensitive adapter error should not escape')
      data.set(key, value)
    },
    async createIfAbsent(key, value) {
      writes++
      if (data.has(key)) return 'exists'
      if (failing) throw new Error('sensitive adapter error should not escape')
      data.set(key, value)
      return 'created'
    },
  }
}

function ids(values = []) {
  let index = 0
  return { nextId() { if (index >= values.length) return `generated-${String(++index).padStart(4, '0')}`; return values[index++] } }
}

function authState() {
  let subject = '101'
  let generation = 'login-1'
  return {
    auth: { get generation() { return generation }, isCurrent(id, captured) { return id === subject && captured === generation } },
    switchTo(nextSubject, nextGeneration) { subject = nextSubject; generation = nextGeneration },
  }
}

function createOptions(store, lease, idProvider) {
  return {
    subjectId: '101', date: '2026-10-28', timeZone: 'Asia/Shanghai',
    storage: store, now, auth: lease.auth, ids: idProvider,
  }
}

async function fresh({ store = storage(), lease = authState(), idProvider = ids() } = {}) {
  const created = await TrainingEditorController.create({
    subjectId: '101', date: '2026-10-28', timeZone: 'Asia/Shanghai',
    storage: store, now, auth: lease.auth, ids: idProvider,
  })
  assert.equal(created.ok, true, created.ok ? '' : created.reason)
  return { controller: created.controller, store, lease }
}

test('mode and date changes preserve the same user-entered groups and keys', async () => {
  const { controller } = await fresh({ idProvider: ids(['event-0001', 'entry-0001', 'record-001']) })
  assert.equal((await controller.addCustomExercise('深蹲', 'weight')).ok, true)
  await controller.editGroup('entry-0001', 'record-001', 'weightKg', '40')
  await controller.editGroup('entry-0001', 'record-001', 'reps', '8')
  const before = controller.draft
  const switched = await controller.setMode('per_set')
  assert.deepEqual(switched, { ok: true, changed: true, localStatus: 'saved_locally' })
  assert.equal((await controller.setMode('per_set')).changed, false)
  assert.equal((await controller.setDate('2026-10-27')).changed, true)
  const after = controller.draft
  assert.equal(after.date, '2026-10-27')
  assert.equal(after.clientEventId, before.clientEventId)
  assert.equal(after.exercises[0].sets[0].recordKey, before.exercises[0].sets[0].recordKey)
  assert.equal(after.exercises[0].sets[0].weightKg, '40')
  assert.equal(after.exercises[0].sets[0].reps, '8')
})

test('invalid and future dates leave the current draft unchanged', async () => {
  const { controller } = await fresh()
  const original = controller.draft
  assert.deepEqual(await controller.setDate('2026-02-30'), { ok: false, reason: 'invalid_input', localStatus: 'saved_locally' })
  assert.equal((await controller.setDate('2026-10-30')).ok, false)
  assert.deepEqual(controller.draft, original)
})

test('exercise and group IDs come from the injected source and invalid identifiers fail closed', async () => {
  const { controller } = await fresh({ idProvider: ids(['event-0001', 'entry-0001', 'record-001', 'record-002']) })
  assert.equal((await controller.addCustomExercise('推举', 'weight')).ok, true)
  assert.equal((await controller.addGroup('entry-0001')).ok, true)
  assert.deepEqual(controller.draft.exercises[0].sets.map(set => set.recordKey), ['record-001', 'record-002'])
  const bad = await fresh({ idProvider: ids(['event-badid1', 'short']) })
  assert.equal((await bad.controller.addCustomExercise('硬拉', 'weight')).ok, false)
  assert.equal(bad.controller.draft.exercises.length, 0)
})

test('rapid edits retain the newest input and local write ordering', async () => {
  const { controller, store } = await fresh({ idProvider: ids(['event-0001', 'entry-0001', 'record-001']) })
  await controller.addCustomExercise('深蹲', 'weight')
  const first = controller.editGroup('entry-0001', 'record-001', 'reps', '8')
  const second = controller.editGroup('entry-0001', 'record-001', 'reps', '9')
  assert.equal(controller.draft.exercises[0].sets[0].reps, '9')
  const [firstResult, secondResult] = await Promise.all([first, second])
  assert.equal(firstResult.ok, false)
  assert.equal(firstResult.reason, 'superseded')
  assert.equal(secondResult.ok, true)
  assert.equal(JSON.parse([...store.data.values()][0]).draft.exercises[0].sets[0].reps, '9')
})

test('confirmation is local, validates complete groups, and is idempotent', async () => {
  const { controller } = await fresh({ idProvider: ids(['event-0001', 'entry-0001', 'record-001']) })
  await controller.addCustomExercise('深蹲', 'weight')
  await controller.setMode('per_set')
  const incomplete = await controller.confirmGroup('entry-0001', 'record-001')
  assert.equal(incomplete.ok, false)
  assert.match(incomplete.message, /填写/)
  await controller.editGroup('entry-0001', 'record-001', 'weightKg', '40')
  await controller.editGroup('entry-0001', 'record-001', 'reps', '8')
  assert.equal((await controller.confirmGroup('entry-0001', 'record-001')).changed, true)
  assert.equal((await controller.confirmGroup('entry-0001', 'record-001')).changed, false)
  assert.equal(controller.draft.exercises[0].sets[0].confirmed, true)
  assert.equal((await controller.editGroup('entry-0001', 'record-001', 'reps', '9')).changed, true)
  assert.equal(controller.draft.exercises[0].sets[0].confirmed, false)
})

test('remove is idempotent and only unsubmitted groups can be removed', async () => {
  const store = storage()
  const lease = authState()
  const localDraft = addExercise(createRecordingDraft('event-saved1', '2026-10-28', 'Asia/Shanghai', '2026-10-29'),
    'entry-saved1', { source: 'custom', exerciseName: '深蹲' }, 'weight', 'record-001')
  const persistedDraft = { ...localDraft, server: { id: '102', version: 1 }, exercises: [{
    ...localDraft.exercises[0], sets: [{ ...localDraft.exercises[0].sets[0], persisted: true }],
  }] }
  const local = new DraftController('101', persistedDraft, store, now, lease.auth)
  assert.equal(await local.persist(), true)
  const attached = await TrainingEditorController.attach('101', 'event-saved1', store, now, lease.auth, ids())
  assert.equal(attached.ok, true)
  const controller = attached.controller
  assert.equal((await controller.removeGroup('entry-saved1', 'record-001')).ok, false)
  const empty = await fresh({ idProvider: ids(['event-empty1', 'entry-empty1', 'record-empty1', 'record-empty2']) })
  await empty.controller.addCustomExercise('硬拉', 'weight')
  assert.equal((await empty.controller.removeGroup('entry-empty1', 'record-empty1')).changed, true)
  assert.equal((await empty.controller.removeGroup('entry-empty1', 'record-empty1')).changed, false)
})

test('persisted groups can be corrected and reconfirmed while deletion stays blocked', async () => {
  const store = storage()
  const lease = authState()
  let draft = addExercise(createRecordingDraft('event-saved2', '2026-10-28', 'Asia/Shanghai', '2026-10-29'),
    'entry-saved2', { source: 'custom', exerciseName: '深蹲' }, 'weight', 'record-002')
  draft = updateSet(updateSet(draft, 'entry-saved2', 'record-002', 'weightKg', '40'), 'entry-saved2', 'record-002', 'reps', '8')
  draft = { ...draft, mode: 'per_set', server: { id: '103', version: 1 }, exercises: [{
    ...draft.exercises[0], sets: [{ ...draft.exercises[0].sets[0], persisted: true, confirmed: true, dirty: false }],
  }] }
  const local = new DraftController('101', draft, store, now, lease.auth)
  assert.equal(await local.persist(), true)
  const attached = await TrainingEditorController.attach('101', 'event-saved2', store, now, lease.auth, ids())
  assert.equal(attached.ok, true)
  const controller = attached.controller
  const edited = await controller.editGroup('entry-saved2', 'record-002', 'reps', '9')
  assert.equal(edited.ok, true)
  assert.equal(controller.draft.exercises[0].sets[0].dirty, true)
  assert.equal(controller.draft.exercises[0].sets[0].confirmed, false)
  assert.equal((await controller.confirmGroup('entry-saved2', 'record-002')).ok, true)
  assert.equal(controller.draft.exercises[0].sets[0].confirmed, true)
  assert.equal((await controller.removeGroup('entry-saved2', 'record-002')).ok, false)
})

test('storage failure keeps edited input and reports only local storage status', async () => {
  const store = storage()
  const { controller } = await fresh({ store, idProvider: ids(['event-0001', 'entry-0001', 'record-001']) })
  store.setFail(true)
  const added = await controller.addCustomExercise('深蹲', 'weight')
  assert.equal(added.ok, false)
  assert.equal(added.reason, 'storage_error')
  assert.equal(added.changed, true)
  assert.equal(added.localStatus, 'storage_error')
  await controller.editGroup('entry-0001', 'record-001', 'reps', '8')
  assert.equal(controller.draft.exercises[0].sets[0].reps, '8')
  assert.equal(controller.localStatus, 'storage_error')
  assert.equal(JSON.stringify(added).includes('sensitive adapter error'), false)
})

test('session switch blocks reads and edits without exposing the prior draft', async () => {
  const lease = authState()
  const { controller } = await fresh({ lease })
  lease.switchTo('202', 'login-2')
  assert.throws(() => controller.draft, /账号已变化/)
  const result = await controller.setMode('per_set')
  assert.equal(result.ok, false)
  assert.equal(result.reason, 'session_changed')
})

test('session switch during an in-flight local write is not reported as saved', async () => {
  const lease = authState()
  let release
  const store = storage()
  store.write = () => new Promise(resolve => { release = resolve })
  let creation = await TrainingEditorController.create({
    subjectId: '101', date: '2026-10-28', timeZone: 'Asia/Shanghai', storage: store, now,
    auth: lease.auth, ids: ids(['event-switch1']),
  })
  assert.equal(creation.ok, true)
  const controller = creation.controller
  const mutation = controller.setMode('per_set')
  while (!release) await new Promise(resolve => setImmediate(resolve))
  lease.switchTo('202', 'login-2')
  release()
  const result = await mutation
  assert.equal(result.ok, false)
  assert.equal(result.reason, 'session_changed')
})

test('invalid exercise data and unavailable ID sources do not mutate state or leak input', async () => {
  const { controller } = await fresh({ idProvider: ids(['event-0001', 'entry-0001', 'record-001']) })
  const invalid = await controller.addCustomExercise(' ', 'weight')
  assert.deepEqual(invalid, { ok: false, reason: 'invalid_input', localStatus: 'saved_locally' })
  assert.equal(controller.draft.exercises.length, 0)
  let calls = 0
  const noIDs = await fresh({ idProvider: { nextId() { if (++calls === 1) return 'event-private1'; throw new Error('private material') } } })
  const outcome = await noIDs.controller.addCustomExercise('深蹲', 'weight')
  assert.equal(outcome.reason, 'id_unavailable')
  assert.equal(JSON.stringify(outcome).includes('private material'), false)
})

test('create refuses an existing event key without replacing its original envelope', async () => {
  const store = storage()
  const lease = authState()
  const key = 'nerae.workout.draft.v1.101.event-reuse1'
  const original = JSON.stringify({ schema: 1, subjectId: '101', marker: 'preserve-existing-bytes' })
  store.data.set(key, original)
  const result = await TrainingEditorController.create(createOptions(store, lease, ids(['event-reuse1'])))
  assert.deepEqual(result, { ok: false, reason: 'already_exists' })
  assert.equal(store.data.get(key), original)
})

test('concurrent same-ID creates have one atomic winner and never overwrite it', async () => {
  const store = storage()
  const lease = authState()
  const first = TrainingEditorController.create(createOptions(store, lease, ids(['event-race01'])))
  const second = TrainingEditorController.create(createOptions(store, lease, ids(['event-race01'])))
  const results = await Promise.all([first, second])
  assert.equal(results.filter(result => result.ok).length, 1)
  assert.equal(results.filter(result => !result.ok && result.reason === 'already_exists').length, 1)
  assert.equal(store.writes, 2)
  const saved = JSON.parse(store.data.get('nerae.workout.draft.v1.101.event-race01'))
  assert.equal(saved.draft.clientEventId, 'event-race01')
})

test('commit-then-throw is unknown, returns no controller, and preserves the possible initial record', async () => {
  const store = storage()
  const lease = authState()
  const originalCreateIfAbsent = store.createIfAbsent.bind(store)
  store.createIfAbsent = async (key, value) => {
    store.data.set(key, value)
    throw new Error('commit outcome unknown')
  }
  const options = createOptions(store, lease, ids(['event-unknown1']))
  const first = await TrainingEditorController.create(options)
  assert.deepEqual(first, { ok: false, reason: 'outcome_unknown', eventId: 'event-unknown1' })
  const possible = store.data.get('nerae.workout.draft.v1.101.event-unknown1')
  assert.equal(typeof possible, 'string')
  store.createIfAbsent = originalCreateIfAbsent
  const retry = await TrainingEditorController.attach('101', first.eventId, store, now, lease.auth, ids())
  assert.equal(retry.ok, true)
  assert.equal(retry.controller.draft.clientEventId, first.eventId)
  assert.equal(store.data.get('nerae.workout.draft.v1.101.event-unknown1'), possible)
})

test('session change during atomic creation fails closed and preserves a committed candidate', async () => {
  const store = storage()
  const lease = authState()
  const original = store.createIfAbsent.bind(store)
  store.createIfAbsent = async (key, value) => {
    const result = await original(key, value)
    lease.switchTo('202', 'login-2')
    return result
  }
  const result = await TrainingEditorController.create(createOptions(store, lease, ids(['event-switched1'])))
  assert.deepEqual(result, { ok: false, reason: 'session_changed' })
  assert.equal('eventId' in result, false)
  assert.equal(typeof store.data.get('nerae.workout.draft.v1.101.event-switched1'), 'string')
})

test('plain read/write storage without atomic createIfAbsent cannot start a new draft', async () => {
  const store = storage()
  delete store.createIfAbsent
  const lease = authState()
  const result = await TrainingEditorController.create(createOptions(store, lease, ids(['event-noatom1'])))
  assert.deepEqual(result, { ok: false, reason: 'storage_unavailable' })
  assert.equal(store.writes, 0)
  assert.equal(store.data.size, 0)
})

test('attach rejects unavailable or malformed stored envelopes without returning raw errors', async () => {
  const lease = authState()
  const store = storage()
  store.read = async () => { throw new Error('private storage detail') }
  const result = await TrainingEditorController.attach('101', 'event-missing1', store, now, lease.auth, ids())
  assert.deepEqual(result, { ok: false, reason: 'storage_unavailable' })
  assert.equal(JSON.stringify(result).includes('private storage detail'), false)
  const noRecord = await TrainingEditorController.attach('101', 'event-missing2', storage(), now, lease.auth, ids())
  assert.deepEqual(noRecord, { ok: false, reason: 'storage_unavailable' })
})

test('attach rechecks the originally captured generation after asynchronous load', async () => {
  const lease = authState()
  const store = storage()
  const draft = createRecordingDraft('event-attach1', '2026-10-28', 'Asia/Shanghai', '2026-10-29')
  store.data.set('nerae.workout.draft.v1.101.event-attach1', JSON.stringify({ schema: 1, subjectId: '101', draft }))
  let clockCalls = 0
  const clock = () => {
    if (++clockCalls === 2) lease.switchTo('202', 'login-2')
    return now()
  }
  const result = await TrainingEditorController.attach('101', 'event-attach1', store, clock, lease.auth, ids())
  assert.deepEqual(result, { ok: false, reason: 'session_changed' })
  assert.equal('controller' in result, false)
})

test('atomic create adapter must return the exact success marker before a controller is issued', async () => {
  const store = storage()
  store.createIfAbsent = async () => 'maybe'
  const result = await TrainingEditorController.create(createOptions(store, authState(), ids(['event-malformed1'])))
  assert.deepEqual(result, { ok: false, reason: 'outcome_unknown', eventId: 'event-malformed1' })
  assert.equal(store.data.size, 0)
})

test('known limitation: independently attached controllers overwrite rather than merge snapshots', async () => {
  const store = storage()
  const lease = authState()
  const created = await TrainingEditorController.create(createOptions(store, lease, ids(['event-shared1'])))
  assert.equal(created.ok, true)
  const first = created.controller
  const attached = await TrainingEditorController.attach('101', 'event-shared1', store, now, lease.auth, ids())
  assert.equal(attached.ok, true)
  const second = attached.controller
  assert.equal((await first.addCustomExercise('深蹲', 'weight')).ok, true)
  assert.equal((await second.addCustomExercise('硬拉', 'weight')).ok, true)
  const persisted = JSON.parse(store.data.get('nerae.workout.draft.v1.101.event-shared1'))
  assert.deepEqual(persisted.draft.exercises.map(exercise => exercise.identity.exerciseName), ['硬拉'])
  assert.deepEqual(first.draft.exercises.map(exercise => exercise.identity.exerciseName), ['深蹲'])
})
