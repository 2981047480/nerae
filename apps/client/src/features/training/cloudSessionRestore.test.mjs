import assert from 'node:assert/strict'
import { registerHooks } from 'node:module'
import { test } from 'node:test'

const hooks = registerHooks({
  resolve(specifier, context, nextResolve) {
    if (specifier === './recording' && /\/(recordingApi|recordingProjection|draftStorage|trainingEditorController|versionedEditorDraft|cloudSessionRestore)\.ts$/.test(context.parentURL || '')) {
      return nextResolve('./recording.ts', context)
    }
    if (specifier === './recordingApi' && /\/(recordingProjection|versionedEditorDraft|cloudSessionRestore)\.ts$/.test(context.parentURL || '')) {
      return nextResolve('./recordingApi.ts', context)
    }
    if (specifier === './recordingProjection' && context.parentURL?.endsWith('/cloudSessionRestore.ts')) {
      return nextResolve('./recordingProjection.ts', context)
    }
    if (specifier === './trainingEditorController' && context.parentURL?.endsWith('/cloudSessionRestore.ts')) {
      return nextResolve('./trainingEditorController.ts', context)
    }
    if (specifier === './draftStorage' && /\/(trainingEditorController|versionedEditorDraft|cloudSessionRestore)\.ts$/.test(context.parentURL || '')) {
      return nextResolve('./draftStorage.ts', context)
    }
    if (specifier === './versionedEditorDraft' && context.parentURL?.endsWith('/trainingEditorController.ts')) {
      return nextResolve('./versionedEditorDraft.ts', context)
    }
    if (specifier === './operationJournal' && context.parentURL?.endsWith('/recordingApi.ts')) {
      return nextResolve('./operationJournal.ts', context)
    }
    return nextResolve(specifier, context)
  },
})
const { RecordingApi } = await import('./recordingApi.ts')
const { SecureDraftStorage } = await import('./secureDraftStorage.ts')
const { TrainingEditorController } = await import('./trainingEditorController.ts')
const { restoreCloudSessionToLocalDraft } = await import('./cloudSessionRestore.ts')
hooks.deregister()

const lease = { subjectId: '101', generation: 'login-1' }
let activeGeneration = lease.generation
const auth = {
  generation: lease.generation,
  current: () => ({ ...lease, generation: activeGeneration, accessToken: 'synthetic-token' }),
  isCurrent: (subject, generation) => subject === lease.subjectId && generation === activeGeneration,
}
const draftLease = { generation: lease.generation, isCurrent: auth.isCurrent }
const now = () => new Date('2026-11-03T01:00:00Z')
const ids = { nextId: () => 'event-unused' }
const key = 'nerae.workout.draft.v1.101.event-0001'
const detail = (overrides = {}) => ({
  id: '42', clientEventId: 'event-0001', date: '2026-11-03', timeZone: 'Asia/Shanghai', status: 'in_progress', source: 'free', version: 2,
  createdAt: '2026-11-03T00:00:00Z', updatedAt: '2026-11-03T00:01:00Z', recordedAt: null,
  exerciseEntryKeys: ['entry-0001'], sets: [{ recordKey: 'record-001', exerciseEntryKey: 'entry-0001', exerciseName: '深蹲',
    resolvedExerciseName: '深蹲', setIndex: 1, kind: 'bodyweight', reps: 8 }], ...overrides,
})
const ok = (found = false, value = '') => ({ ok: true, errorCode: '', found, value })
const fail = errorCode => ({ ok: false, errorCode, found: false, value: '' })

function makeNative() {
  const values = new Map()
  const revisions = new Map()
  return {
    values,
    revisions,
    readVersionedTrainingDraft(slot) {
      return values.has(slot) ? { ...ok(true, values.get(slot)), revision: revisions.get(slot) } : { ...ok(), revision: '0' }
    },
    compareAndSetVersionedTrainingDraft(slot, revision, found, before, value) {
      if (values.has(slot) !== found || (revisions.get(slot) || '0') !== revision || (values.get(slot) || '') !== before) {
        return { ...fail('conflict'), revision: '' }
      }
      const next = (BigInt(revision) + 1n).toString()
      values.set(slot, value)
      revisions.set(slot, next)
      return { ...ok(), revision: next }
    },
    discoverTrainingDrafts() { return ok(true, JSON.stringify({ eventIds: [], nextCursor: null, hasUnclassifiedItems: false })) },
  }
}

function setup(request, storageOverride, authObject = auth, leaseObject = draftLease, eventId = 'event-0001') {
  activeGeneration = lease.generation
  const calls = []
  const api = new RecordingApi('https://api.example.org', authObject, async requestArgs => {
    calls.push(requestArgs)
    const result = await request(requestArgs)
    return result?.data && typeof result.data === 'object'
      ? { ...result, data: { ...result.data, clientEventId: eventId } }
      : result
  }, lease)
  const native = makeNative()
  const storage = storageOverride || new SecureDraftStorage(native, lease.subjectId, leaseObject)
  return { calls, api, native, storage, eventId, key: `nerae.workout.draft.v1.${lease.subjectId}.${eventId}` }
}

const response = (overrides = {}) => ({ statusCode: 200, data: detail(overrides) })
const run = (setup, authObject = auth, leaseObject = draftLease) => restoreCloudSessionToLocalDraft({ sessionId: '42', lease,
  auth: authObject, draftLease: leaseObject, api: setup.api, storage: setup.storage, ids, now })

test('reads one real API snapshot, creates the absent versioned slot and attaches the existing editor', async () => {
  const env = setup(response, undefined, auth, draftLease, 'event-success-01')
  const result = await run(env)
  assert.equal(result.ok, true)
  assert.equal(env.calls.length, 1)
  assert.equal(env.calls[0].method, 'GET')
  assert.equal(env.calls[0].url, 'https://api.example.org/me/sessions/42')
  assert.equal(result.controller.draft.clientEventId, env.eventId)
  assert.equal(result.controller.draft.server.version, 2)
  assert.equal(result.controller.draft.exercises[0].sets[0].reps, '8')
  assert.equal(env.native.values.has(env.key), true)
  await result.controller.close()
})

test('uses the server snapshot timezone to reject a date that is still in the future there', async () => {
  const env = setup(() => response({ timeZone: 'America/Los_Angeles' }), undefined, auth, draftLease, 'event-timezone-01')
  const result = await run(env)
  assert.deepEqual(result, { ok: false, reason: 'invalid_snapshot' })
  assert.equal(env.native.values.size, 0)
})

test('rejects a facade that returns a previously issued snapshot without a new RecordingApi read', async () => {
  const env = setup(response, undefined, auth, draftLease, 'event-facade-01')
  const previous = await env.api.readSession('42')
  const result = await restoreCloudSessionToLocalDraft({ sessionId: '42', lease, auth, draftLease, api: {
    readSession: async () => previous,
  }, storage: env.storage, ids, now })
  assert.deepEqual(result, { ok: false, reason: 'invalid_input' })
  assert.equal(env.calls.length, 1)
  assert.equal(env.native.values.size, 0)
})

test('calls the RecordingApi prototype method even if an instance method is shadowed', async () => {
  let reads = 0
  const env = setup(() => {
    reads++
    return response({ sets: [{ ...detail().sets[0], reps: reads === 1 ? 8 : 9 }] })
  }, undefined, auth, draftLease, 'event-shadow-01')
  const previous = await env.api.readSession('42')
  env.api.readSession = async () => previous
  const result = await run(env)
  assert.equal(result.ok, true)
  assert.equal(reads, 2)
  assert.equal(result.controller.draft.exercises[0].sets[0].reps, '9')
  await result.controller.close()
})

test('requires the RecordingApi to use the same auth object and captured account lease', async () => {
  const otherAuth = { ...auth, current: () => ({ ...auth.current() }) }
  const env = setup(response, undefined, otherAuth, draftLease, 'event-auth-lease-01')
  const result = await run(env, auth)
  assert.deepEqual(result, { ok: false, reason: 'session_changed' })
  assert.equal(env.calls.length, 0)

  const wrongLeaseCalls = []
  const wrongLeaseApi = new RecordingApi('https://api.example.org', auth, async requestArgs => {
    wrongLeaseCalls.push(requestArgs)
    return response()
  }, { subjectId: '202', generation: 'login-2' })
  const wrongLeaseResult = await restoreCloudSessionToLocalDraft({ sessionId: '42', lease, auth, draftLease, api: wrongLeaseApi,
    storage: env.storage, ids, now })
  assert.deepEqual(wrongLeaseResult, { ok: false, reason: 'session_changed' })
  assert.equal(wrongLeaseCalls.length, 0)

  const staleDraftLease = { ...draftLease, generation: 'login-old',
    isCurrent: (subject, generation) => subject === lease.subjectId && ['login-old', lease.generation].includes(generation) }
  const staleGenerationEnv = setup(response, undefined, auth, staleDraftLease, 'event-stale-lease-01')
  const staleGenerationResult = await run(staleGenerationEnv, auth, staleDraftLease)
  assert.deepEqual(staleGenerationResult, { ok: false, reason: 'session_changed' })
  assert.equal(staleGenerationEnv.calls.length, 0)
})

test('closes and awaits the attached controller when auth changes just after attach succeeds', async () => {
  const env = setup(response, undefined, auth, draftLease, 'event-race-close-01')
  const originalAttach = TrainingEditorController.restoreIntoEmptySlot
  TrainingEditorController.restoreIntoEmptySlot = async function (...args) {
    const attached = await originalAttach.apply(this, args)
    if (attached.ok) activeGeneration = 'login-2'
    return attached
  }
  let result
  try { result = await run(env) }
  finally { TrainingEditorController.restoreIntoEmptySlot = originalAttach }
  assert.deepEqual(result, { ok: false, reason: 'session_changed' })
  assert.equal(env.native.values.has(env.key), true)
  activeGeneration = lease.generation
  const attachedAgain = await TrainingEditorController.attach(lease.subjectId, env.eventId, env.storage, now, draftLease, ids)
  assert.equal(attachedAgain.ok, true)
  await attachedAgain.controller.close()
})

test('an existing draft wins without changing its bytes or attaching it', async () => {
  const env = setup(response, undefined, auth, draftLease, 'event-existing-01')
  const marker = JSON.stringify({ schema: 1, subjectId: '101', clientEventId: env.eventId, state: 'ready', request: {
    method: 'POST', path: '/me/sessions', idempotencyKey: 'existing-create-key-01', body: JSON.stringify({
      source: 'free', date: '2026-11-03', timeZone: 'Asia/Shanghai', clientEventId: env.eventId,
    }),
  } })
  const original = JSON.stringify({ schema: 1, subjectId: '101', draft: {
    clientEventId: env.eventId, date: '2026-11-03', timeZone: 'Asia/Shanghai', mode: 'after_workout',
    finish: {}, exercises: [], syncPending: marker,
  } })
  env.native.values.set(env.key, original)
  env.native.revisions.set(env.key, '1')
  const result = await run(env)
  assert.deepEqual(result, { ok: false, reason: 'local_exists' })
  assert.equal(env.native.values.get(env.key), original)
  assert.equal(JSON.parse(env.native.values.get(env.key)).draft.syncPending, marker)
})

test('a session change after a failed restore cannot reveal the previous account draft state', async () => {
  const env = setup(response, undefined, auth, draftLease, 'event-existing-switch-01')
  const original = JSON.stringify({ schema: 1, subjectId: lease.subjectId, draft: { clientEventId: env.eventId } })
  env.native.values.set(env.key, original)
  env.native.revisions.set(env.key, '1')
  const restore = TrainingEditorController.restoreIntoEmptySlot
  TrainingEditorController.restoreIntoEmptySlot = async function (...args) {
    const result = await restore.apply(this, args)
    assert.deepEqual(result, { ok: false, reason: 'local_exists' })
    activeGeneration = 'login-2'
    return result
  }
  let result
  try { result = await run(env) }
  finally { TrainingEditorController.restoreIntoEmptySlot = restore }
  assert.deepEqual(result, { ok: false, reason: 'session_changed' })
  assert.equal(env.native.values.get(env.key), original)
})

test('competing restores have one atomic creator and never overwrite the winner', async () => {
  const env = setup(response, undefined, auth, draftLease, 'event-competition-01')
  const results = await Promise.all([run(env), run(env)])
  assert.equal(results.filter(value => value.ok).length, 1)
  assert.equal(results.filter(value => !value.ok && value.reason === 'local_exists').length, 1)
  const winner = results.find(value => value.ok)
  await winner.controller.close()
  assert.equal(env.native.values.size, 1)
})

test('a login switch while the server read is pending fails closed before local creation', async () => {
  const env = setup(async () => {
    activeGeneration = 'login-2'
    return response()
  }, undefined, auth, draftLease, 'event-switch-01')
  const result = await run(env)
  assert.deepEqual(result, { ok: false, reason: 'session_changed' })
  assert.equal(env.native.values.size, 0)
})

test('an unclear native create result is never attached or reported as restored', async () => {
  const env = setup(response, undefined, auth, draftLease, 'event-unknown-01')
  env.native.compareAndSetVersionedTrainingDraft = function (slot, revision, found, before, value) {
    const actualRevision = this.revisions.get(slot) || '0'
    if (this.values.has(slot) !== found || actualRevision !== revision || (this.values.get(slot) || '') !== before) {
      return { ...fail('conflict'), revision: '' }
    }
    this.values.set(slot, value)
    this.revisions.set(slot, (BigInt(revision) + 1n).toString())
    return { ...fail('unavailable'), revision: '' }
  }
  const result = await run(env)
  assert.deepEqual(result, { ok: false, reason: 'outcome_unknown' })
  assert.equal(env.native.values.has(env.key), true)
})

test('non-versioned storage is rejected before a server read', async () => {
  const env = setup(async () => { throw new Error('must not read') }, { createIfAbsent: async () => 'created' },
    auth, draftLease, 'event-storage-01')
  const result = await run(env)
  assert.deepEqual(result, { ok: false, reason: 'storage_unavailable' })
  assert.equal(env.calls.length, 0)
})

test('retained unsaved input blocks cloud creation after its backing vault slot disappears', async () => {
  const eventId = 'event-retained-01'
  const retainedKey = `nerae.workout.draft.v1.${lease.subjectId}.${eventId}`
  const env = setup(() => response({ clientEventId: eventId }), undefined, auth, draftLease, eventId)
  const original = JSON.stringify({ schema: 1, subjectId: lease.subjectId, draft: {
    clientEventId: eventId, date: '2026-11-03', timeZone: 'Asia/Shanghai', mode: 'after_workout',
    finish: {}, exercises: [],
  } })
  env.native.values.set(retainedKey, original)
  env.native.revisions.set(retainedKey, '1')
  const attached = await TrainingEditorController.attach(lease.subjectId, eventId, env.storage, now, draftLease, ids)
  assert.equal(attached.ok, true)

  const compareAndSet = env.native.compareAndSetVersionedTrainingDraft
  let failNextWrite = true
  env.native.compareAndSetVersionedTrainingDraft = function (...args) {
    if (failNextWrite) {
      failNextWrite = false
      return fail('unavailable')
    }
    return compareAndSet.apply(this, args)
  }
  const edit = await attached.controller.setDate('2026-11-02')
  assert.equal(edit.ok, false)
  const retainedInput = attached.controller.draft.date
  assert.equal(retainedInput, '2026-11-02')
  await attached.controller.close()

  // Model an external vault reset while the unresolved edit remains in the
  // process-local versioned slot.
  env.native.values.delete(retainedKey)
  env.native.revisions.delete(retainedKey)
  const restored = await run(env)
  if (restored.ok) await restored.controller.close()

  const reattached = await TrainingEditorController.attach(lease.subjectId, eventId, env.storage, now, draftLease, ids)
  assert.equal(reattached.ok, true)
  const stillRetained = reattached.controller.draft.date
  await reattached.controller.close()
  assert.deepEqual({ ok: restored.ok, reason: restored.reason, createdSlot: env.native.values.has(retainedKey), stillRetained }, {
    ok: false, reason: 'local_unsynced', createdSlot: false, stillRetained: retainedInput,
  })
})

test('same account and generation with a changed token cannot continue an older restore', async () => {
  let token = 'old-token'
  const rotatingAuth = {
    ...auth,
    current: () => ({ ...lease, accessToken: token }),
  }
  const tokenLease = { generation: lease.generation, isCurrent: rotatingAuth.isCurrent }
  const env = setup(response, undefined, rotatingAuth, tokenLease, 'event-token-01')
  const restored = await restoreCloudSessionToLocalDraft({ sessionId: '42', lease, auth: rotatingAuth, draftLease: tokenLease,
    api: env.api, storage: env.storage, ids, now: () => {
      token = 'rotated-token'
      return now()
    } })
  if (restored.ok) await restored.controller.close()
  assert.deepEqual({ ok: restored.ok, reason: restored.reason, stored: env.native.values.has(env.key) }, {
    ok: false, reason: 'session_changed', stored: false,
  })
})

test('a login switch after native creation releases the restore reservation', async () => {
  const eventId = 'event-after-create-switch-01'
  const env = setup(response, undefined, auth, draftLease, eventId)
  const create = env.storage.createIfAbsent.bind(env.storage)
  env.storage.createIfAbsent = async (...args) => {
    const result = await create(...args)
    activeGeneration = 'login-2'
    return result
  }
  const restored = await run(env)
  assert.deepEqual({ ok: restored.ok, reason: restored.reason, stored: env.native.values.has(env.key) },
    { ok: false, reason: 'session_changed', stored: true })
  activeGeneration = lease.generation
  const attached = await TrainingEditorController.attach(lease.subjectId, eventId, env.storage, now, draftLease, ids)
  assert.equal(attached.ok, true)
  assert.equal(attached.controller.draft.server.version, 2)
  await attached.controller.close()
})

test('an exception after native commit is unknown but releases both local reservations', async () => {
  const eventId = 'event-lost-ack-restore-01'
  const env = setup(response, undefined, auth, draftLease, eventId)
  const create = env.storage.createIfAbsent.bind(env.storage)
  env.storage.createIfAbsent = async (...args) => {
    await create(...args)
    throw new Error('lost acknowledgement')
  }
  const first = await run(env)
  assert.deepEqual({ ok: first.ok, reason: first.reason, stored: env.native.values.has(env.key) },
    { ok: false, reason: 'outcome_unknown', stored: true })
  const attached = await TrainingEditorController.attach(lease.subjectId, eventId, env.storage, now, draftLease, ids)
  assert.equal(attached.ok, true)
  await attached.controller.close()
})

test('a pre-commit exception releases the reservation so restore can retry', async () => {
  const eventId = 'event-precommit-retry-01'
  const env = setup(response, undefined, auth, draftLease, eventId)
  const create = env.storage.createIfAbsent.bind(env.storage)
  env.storage.createIfAbsent = async (...args) => {
    env.storage.createIfAbsent = create
    throw new Error('before commit')
  }
  const first = await run(env)
  assert.deepEqual(first, { ok: false, reason: 'outcome_unknown' })
  const second = await run(env)
  assert.equal(second.ok, true)
  await second.controller.close()
})
