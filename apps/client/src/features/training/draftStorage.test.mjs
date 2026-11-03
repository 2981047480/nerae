import assert from 'node:assert/strict'
import { registerHooks } from 'node:module'
import { test } from 'node:test'
import { addExercise, createRecordingDraft, switchMode, updateSet } from './recording.ts'

// Node's built-in TypeScript support requires an extension; uni-app/Vite does not.
const hooks = registerHooks({
  resolve(specifier, context, nextResolve) {
    if (specifier === './recording' && context.parentURL?.endsWith('/draftStorage.ts')) {
      return nextResolve('./recording.ts', context)
    }
    return nextResolve(specifier, context)
  },
})
const { DraftController, todayInTimezone } = await import('./draftStorage.ts')
hooks.deregister()

const now = () => new Date('2026-10-29T00:30:00Z')
const makeDraft = () => addExercise(
  createRecordingDraft('event-0001', '2026-10-29', 'Asia/Shanghai', '2026-10-29'),
  'entry-0001', { source: 'custom', exerciseName: '深蹲' }, 'weight', 'record-001',
)
function memoryStore() {
  const data = new Map()
  return {
    data,
    async read(key) { if (!data.has(key)) throw new Error('missing'); return data.get(key) },
    async write(key, value) { data.set(key, value) },
  }
}

test('real user input and the selected mode restore under the same authenticated subject', async () => {
  const storage = memoryStore()
  const auth = { generation: 'session-1', isCurrent: (id, generation) => id === '101' && generation === 'session-1' }
  const controller = new DraftController('101', makeDraft(), storage, now, auth)
  assert.equal(await controller.update(draft => switchMode(draft, 'per_set')), true)
  assert.equal(await controller.update(draft => updateSet(draft, 'entry-0001', 'record-001', 'reps', '8')), true)
  assert.equal(controller.status, 'saved_locally')
  assert.deepEqual(controller.recordingLease, { subjectId: '101', generation: 'session-1' })
  const restored = await DraftController.load('101', 'event-0001', storage, now, auth)
  assert.equal(restored.draft.mode, 'per_set')
  assert.equal(restored.draft.exercises[0].sets[0].recordKey, 'record-001')
  assert.equal(restored.draft.exercises[0].sets[0].reps, '8')
  assert.equal(restored.status, 'saved_locally')
})

test('Unicode event IDs use character counts consistently for local draft persistence', async () => {
  const clientEventId = '🧭'.repeat(64)
  const storage = memoryStore()
  const auth = { generation: 'session-unicode', isCurrent: (id, generation) => id === '101' && generation === 'session-unicode' }
  const draft = createRecordingDraft(clientEventId, '2026-10-29', 'Asia/Shanghai', '2026-10-29')
  const controller = new DraftController('101', draft, storage, now, auth)
  assert.equal(await controller.persist(), true)
  const restored = await DraftController.load('101', clientEventId, storage, now, auth)
  assert.equal(restored.draft.clientEventId, clientEventId)
  assert.equal(restored.status, 'saved_locally')
})

test('a failed local write keeps current input and a later retry can succeed', async () => {
  const storage = memoryStore()
  const originalWrite = storage.write
  storage.write = async () => { throw new Error('device storage full') }
  const controller = new DraftController('101', makeDraft(), storage, now, { generation: 'session-1', isCurrent: () => true })
  assert.equal(await controller.update(draft => updateSet(draft, 'entry-0001', 'record-001', 'reps', '9')), false)
  assert.equal(controller.status, 'storage_error')
  assert.equal(controller.draft.exercises[0].sets[0].reps, '9')
  storage.write = originalWrite
  assert.equal(await controller.persist(), true)
  assert.equal(controller.status, 'saved_locally')
})

test('reloading after a failed write restores the unsaved input instead of silently showing an older snapshot', async () => {
  const storage = memoryStore()
  let generation = 'session-1'
  const lease = () => ({ generation, isCurrent: (id, captured) => id === '101' && captured === generation })
  const controller = new DraftController('101', makeDraft(), storage, now, lease())
  assert.equal(await controller.update(draft => updateSet(draft, 'entry-0001', 'record-001', 'reps', '8')), true)
  const originalWrite = storage.write
  storage.write = async () => { throw new Error('device storage full') }
  assert.equal(await controller.update(draft => updateSet(draft, 'entry-0001', 'record-001', 'reps', '9')), false)
  generation = 'session-2'
  const restored = await DraftController.load('101', 'event-0001', storage, now, lease())
  assert.equal(restored.draft.exercises[0].sets[0].reps, '9')
  assert.equal(restored.status, 'storage_error')
  storage.write = originalWrite
  assert.equal(await restored.persist(), true)
  const saved = await DraftController.load('101', 'event-0001', storage, now, lease())
  assert.equal(saved.draft.exercises[0].sets[0].reps, '9')
  assert.equal(saved.status, 'saved_locally')
})

test('overlapping writes are serialized so an old snapshot cannot overwrite a newer edit', async () => {
  const data = new Map()
  const starts = []
  const releases = []
  const storage = {
    async read(key) { return data.get(key) },
    write(key, value) {
      starts.push(JSON.parse(value).draft.exercises[0].sets[0].reps)
      return new Promise(resolve => releases.push(() => { data.set(key, value); resolve() }))
    },
  }
  const auth = { generation: 'session-1', isCurrent: () => true }
  const controller = new DraftController('101', makeDraft(), storage, now, auth)
  const first = controller.update(draft => updateSet(draft, 'entry-0001', 'record-001', 'reps', '8'))
  const second = controller.update(draft => updateSet(draft, 'entry-0001', 'record-001', 'reps', '9'))
  await new Promise(resolve => setImmediate(resolve))
  assert.deepEqual(starts, ['8'])
  releases[0]()
  assert.equal(await first, false)
  await new Promise(resolve => setImmediate(resolve))
  assert.equal(controller.status, 'saving')
  assert.deepEqual(starts, ['8', '9'])
  releases[1]()
  assert.equal(await second, true)
  const restored = await DraftController.load('101', 'event-0001', storage, now, auth)
  assert.equal(restored.draft.exercises[0].sets[0].reps, '9')
})

for (const olderFails of [false, true]) {
  test(`a newer preparation failure survives an older queued ${olderFails ? 'failure' : 'success'}`, async () => {
    const event = olderFails ? 'event-prepare-failure' : 'event-prepare-success'
    const data = new Map()
    let rejectPreparation = true
    let release
    const storage = {
      async read(key) { return data.get(key) },
      async write() { throw new Error('prepared writes expected') },
      prepareWrite(key, value) {
        const reps = JSON.parse(value).draft.exercises[0].sets[0].reps
        if (reps === '9' && rejectPreparation) throw new Error('cannot prepare latest write')
        if (reps === '8') return () => new Promise((resolve, reject) => {
          release = () => olderFails ? reject(new Error('older write failed')) : (data.set(key, value), resolve())
        })
        return async () => { data.set(key, value) }
      },
    }
    const auth = { generation: 'session-prepare', isCurrent: () => true }
    const controller = new DraftController('101', { ...makeDraft(), clientEventId: event }, storage, now, auth)
    const older = controller.update(draft => updateSet(draft, 'entry-0001', 'record-001', 'reps', '8'))
    await new Promise(resolve => setImmediate(resolve))
    assert.equal(typeof release, 'function')
    assert.equal(await controller.update(draft => updateSet(draft, 'entry-0001', 'record-001', 'reps', '9')), false)
    release()
    assert.equal(await older, false)
    const recovered = await DraftController.load('101', event, storage, now, auth)
    assert.equal(recovered.draft.exercises[0].sets[0].reps, '9')
    assert.equal(recovered.status, 'storage_error')
    rejectPreparation = false
    assert.equal(await recovered.persist(), true)
    const saved = await DraftController.load('101', event, storage, now, auth)
    assert.equal(saved.draft.exercises[0].sets[0].reps, '9')
    assert.equal(saved.status, 'saved_locally')
  })
}

for (const newWriteFails of [false, true]) {
  test(`a load in progress observes a newer ${newWriteFails ? 'failed' : 'successful'} write`, async () => {
    const event = newWriteFails ? 'event-read-failure' : 'event-read-success'
    const initial = { ...makeDraft(), clientEventId: event }
    const key = `nerae.workout.draft.v1.101.${event}`
    let stored = JSON.stringify({ schema: 1, subjectId: '101', draft: initial })
    let releaseRead
    let firstRead = true
    const storage = {
      read() {
        const captured = stored
        if (!firstRead) return Promise.resolve(captured)
        firstRead = false
        return new Promise(resolve => { releaseRead = () => resolve(captured) })
      },
      async write(_key, value) {
        if (newWriteFails) throw new Error('write failed')
        stored = value
      },
    }
    const auth = { generation: 'session-read-race', isCurrent: () => true }
    const controller = new DraftController('101', initial, storage, now, auth)
    assert.equal(controller.key, key)
    const loading = DraftController.load('101', event, storage, now, auth)
    await new Promise(resolve => setImmediate(resolve))
    assert.equal(typeof releaseRead, 'function')
    assert.equal(await controller.update(draft => updateSet(draft, 'entry-0001', 'record-001', 'reps', '9')), !newWriteFails)
    releaseRead()
    const recovered = await loading
    assert.equal(recovered.draft.exercises[0].sets[0].reps, '9')
    assert.equal(recovered.status, newWriteFails ? 'storage_error' : 'saved_locally')
  })
}

test('writes to another draft do not invalidate a pending read', async () => {
  const targetEvent = 'event-read-unrelated'
  const otherEvent = 'event-write-unrelated'
  const target = { ...makeDraft(), clientEventId: targetEvent }
  const data = new Map([[`nerae.workout.draft.v1.101.${targetEvent}`, JSON.stringify({ schema: 1, subjectId: '101', draft: target })]])
  let reads = 0
  let releaseRead
  const storage = {
    read(key) {
      reads++
      const value = data.get(key)
      if (reads !== 1) return Promise.resolve(value)
      return new Promise(resolve => { releaseRead = () => resolve(value) })
    },
    async write(key, value) { data.set(key, value) },
  }
  const auth = { generation: 'session-unrelated-race', isCurrent: () => true }
  const loading = DraftController.load('101', targetEvent, storage, now, auth)
  await new Promise(resolve => setImmediate(resolve))
  const other = new DraftController('101', { ...makeDraft(), clientEventId: otherEvent }, storage, now, auth)
  for (let value = 1; value <= 9; value++) {
    assert.equal(await other.update(draft => updateSet(draft, 'entry-0001', 'record-001', 'reps', String(value))), true)
  }
  releaseRead()
  const loaded = await loading
  assert.equal(loaded.draft.clientEventId, targetEvent)
  assert.equal(reads, 1)
})

test('switching accounts blocks access while previously accepted local writes finish for the old subject', async () => {
  let active = '101'
  const auth = { generation: 'session-1', isCurrent: (id, generation) => id === active && generation === 'session-1' }
  let writes = 0
  let release
  const values = []
  const storage = {
    async read() { throw new Error('not needed') },
    write(key, value) {
      assert.match(key, /^nerae\.workout\.draft\.v1\.101\./)
      values.push(JSON.parse(value).draft.exercises[0].sets[0].reps)
      writes++
      if (writes === 1) return new Promise(resolve => { release = resolve })
      return Promise.resolve()
    },
  }
  const controller = new DraftController('101', makeDraft(), storage, now, auth)
  const first = controller.update(draft => updateSet(draft, 'entry-0001', 'record-001', 'reps', '8'))
  const second = controller.update(draft => updateSet(draft, 'entry-0001', 'record-001', 'reps', '9'))
  await new Promise(resolve => setImmediate(resolve))
  active = '202'
  release()
  assert.equal(await first, false)
  assert.equal(await second, false)
  assert.equal(writes, 2)
  assert.deepEqual(values, ['8', '9'])
  assert.throws(() => controller.draft, /账号已变化/)
  assert.throws(() => controller.recordingLease, /账号已变化/)
})

test('corrupt or wrong-subject data is rejected without replacing stored bytes', async () => {
  const storage = memoryStore()
  const auth = { generation: 'session-1', isCurrent: () => true }
  const controller = new DraftController('101', makeDraft(), storage, now, auth)
  const corrupt = '{incomplete'
  storage.data.set(controller.key, corrupt)
  await assert.rejects(DraftController.load('101', 'event-0001', storage, now, auth), /原数据仍被保留/)
  assert.equal(storage.data.get(controller.key), corrupt)
  const wrong = JSON.stringify({ schema: 1, subjectId: '202', draft: makeDraft() })
  storage.data.set(controller.key, wrong)
  await assert.rejects(DraftController.load('101', 'event-0001', storage, now, auth), /不属于当前账号/)
  assert.equal(storage.data.get(controller.key), wrong)
})

test('a change of account while reading cannot reveal the previous subject draft', async () => {
  let active = '101'
  let release
  const auth = { generation: 'session-1', isCurrent: (id, generation) => id === active && generation === 'session-1' }
  const storage = { read: () => new Promise(resolve => { release = resolve }), async write() {} }
  const loading = DraftController.load('101', 'event-0001', storage, now, auth)
  active = '202'
  release(JSON.stringify({ schema: 1, subjectId: '101', draft: makeDraft() }))
  await assert.rejects(loading, /账号已变化/)
})

test('training day is evaluated in the frozen session timezone', () => {
  assert.equal(todayInTimezone('Asia/Shanghai', now()), '2026-10-29')
  assert.equal(todayInTimezone('America/Los_Angeles', now()), '2026-10-28')
  assert.throws(() => todayInTimezone('No/Such_Zone', now()), /时区/)
})


test('an old session cannot overwrite a new login for the same subject', async () => {
  let active = '101'
  let generation = 'session-old'
  const lease = () => ({ generation, isCurrent: (id, captured) => id === active && captured === generation })
  const data = new Map()
  const writes = []
  let releaseFirst
  const storage = {
    async read(key) { return data.get(key) },
    write(key, value) {
      const reps = JSON.parse(value).draft.exercises[0].sets[0].reps
      writes.push(reps)
      if (writes.length === 1) return new Promise(resolve => { releaseFirst = () => { data.set(key, value); resolve() } })
      data.set(key, value)
      return Promise.resolve()
    },
  }
  const old = new DraftController('101', makeDraft(), storage, now, lease())
  const oldFirst = old.update(draft => updateSet(draft, 'entry-0001', 'record-001', 'reps', '8'))
  const oldQueued = old.update(draft => updateSet(draft, 'entry-0001', 'record-001', 'reps', '9'))
  await new Promise(resolve => setImmediate(resolve))
  active = '202'; generation = 'session-other'
  active = '101'; generation = 'session-new'
  const current = new DraftController('101', makeDraft(), storage, now, lease())
  const currentSave = current.update(draft => updateSet(draft, 'entry-0001', 'record-001', 'reps', '7'))
  releaseFirst()
  assert.equal(await oldFirst, false)
  assert.equal(await oldQueued, false)
  assert.equal(await currentSave, true)
  assert.deepEqual(writes, ['8', '9', '7'])
  const restored = await DraftController.load('101', 'event-0001', storage, now, lease())
  assert.equal(restored.draft.exercises[0].sets[0].reps, '7')
})

test('session expiry preserves the latest queued draft for the same subject after reauthentication', async () => {
  let generation = 'session-old'
  const lease = () => ({ generation, isCurrent: (id, captured) => id === '101' && captured === generation })
  const data = new Map()
  let releaseFirst
  let writeCount = 0
  const storage = {
    async read(key) { return data.get(key) },
    write(key, value) {
      writeCount++
      if (writeCount === 1) return new Promise(resolve => { releaseFirst = () => { data.set(key, value); resolve() } })
      data.set(key, value)
      return Promise.resolve()
    },
  }
  const old = new DraftController('101', makeDraft(), storage, now, lease())
  const first = old.update(draft => updateSet(draft, 'entry-0001', 'record-001', 'reps', '8'))
  const last = old.update(draft => updateSet(draft, 'entry-0001', 'record-001', 'reps', '9'))
  await new Promise(resolve => setImmediate(resolve))
  generation = 'session-new'
  const loading = DraftController.load('101', 'event-0001', storage, now, lease())
  releaseFirst()
  assert.equal(await first, false)
  assert.equal(await last, false)
  const restored = await loading
  assert.equal(restored.draft.exercises[0].sets[0].reps, '9')
  assert.throws(() => old.draft, /账号已变化/)
})

test('persistence proof is opaque and bound to one controller, subject, generation, revision and exact draft', async () => {
  const storage = memoryStore()
  let generation = 'proof-session-1'
  const auth = { generation, isCurrent: (id, captured) => id === '101' && captured === generation }
  const controller = new DraftController('101', makeDraft(), storage, now, auth)
  const proof = await controller.persistWithProof()
  assert.ok(proof)
  assert.deepEqual(Object.keys(proof), [])
  assert.equal(JSON.stringify(proof), '{}')
  assert.equal(controller.isCurrentPersistenceProof(proof), true)

  const otherController = new DraftController('101', makeDraft(), storage, now, auth)
  assert.equal(otherController.isCurrentPersistenceProof(proof), false)
  assert.equal(controller.isCurrentPersistenceProof({}), false)

  controller.draft.exercises[0].sets[0].reps = '8'
  assert.equal(controller.draft.exercises[0].sets[0].reps, '')
  assert.equal(controller.isCurrentPersistenceProof(proof), true)
  await controller.update(draft => updateSet(draft, 'entry-0001', 'record-001', 'reps', '8'))
  assert.equal(controller.isCurrentPersistenceProof(proof), false)

  generation = 'proof-session-2'
  assert.equal(controller.isCurrentPersistenceProof(proof), false)
})

test('any subsequent ordinary edit or persist invalidates a successful proof', async () => {
  const storage = memoryStore()
  const auth = { generation: 'proof-session', isCurrent: () => true }
  const controller = new DraftController('101', makeDraft(), storage, now, auth)
  let proof = await controller.persistWithProof()
  assert.equal(controller.isCurrentPersistenceProof(proof), true)
  assert.equal(await controller.update(draft => updateSet(draft, 'entry-0001', 'record-001', 'reps', '8')), true)
  assert.equal(controller.isCurrentPersistenceProof(proof), false)

  proof = await controller.persistWithProof()
  assert.equal(controller.isCurrentPersistenceProof(proof), true)
  assert.equal(await controller.persist(), true)
  assert.equal(controller.isCurrentPersistenceProof(proof), false)
})

test('in-place transforms cannot change the frozen event identity or timezone', async () => {
  const storage = memoryStore()
  const auth = { generation: 'proof-session', isCurrent: () => true }
  const controller = new DraftController('101', makeDraft(), storage, now, auth)
  await assert.rejects(controller.update(draft => {
    draft.clientEventId = 'event-0002'
    return draft
  }), /不能改变记录身份/)
  assert.equal(controller.draft.clientEventId, 'event-0001')

  await assert.rejects(controller.update(draft => {
    draft.timeZone = 'America/Los_Angeles'
    return draft
  }), /不能改变记录身份/)
  assert.equal(controller.draft.timeZone, 'Asia/Shanghai')
})

test('failed or uncertain write never produces a proof and invalidates an earlier proof', async () => {
  const storage = memoryStore()
  const auth = { generation: 'proof-session', isCurrent: () => true }
  const controller = new DraftController('101', makeDraft(), storage, now, auth)
  const previousProof = await controller.persistWithProof()
  assert.equal(controller.isCurrentPersistenceProof(previousProof), true)

  // Simulate a native write that may have persisted before reporting failure.
  storage.write = async (key, value) => {
    storage.data.set(key, value)
    throw new Error('write acknowledgement lost')
  }
  assert.equal(await controller.persistWithProof(), null)
  assert.equal(controller.status, 'storage_error')
  assert.equal(controller.isCurrentPersistenceProof(previousProof), false)
})

test('a newer concurrent persist invalidates an earlier proof even when writes finish in order', async () => {
  const data = new Map()
  const releases = []
  let writes = 0
  const storage = {
    async read(key) { return data.get(key) },
    write(key, value) {
      writes++
      return new Promise(resolve => releases.push(() => { data.set(key, value); resolve() }))
    },
  }
  const auth = { generation: 'proof-session', isCurrent: () => true }
  const controller = new DraftController('101', makeDraft(), storage, now, auth)
  const older = controller.persistWithProof()
  await new Promise(resolve => setImmediate(resolve))
  const newer = controller.persistWithProof()
  releases[0]()
  assert.equal(await older, null)
  await new Promise(resolve => setImmediate(resolve))
  assert.equal(writes, 2)
  releases[1]()
  const latestProof = await newer
  assert.ok(latestProof)
  assert.equal(controller.isCurrentPersistenceProof(latestProof), true)
})

test('a newer same-draft write from another controller invalidates the earlier proof immediately', async () => {
  const storage = memoryStore()
  const auth = { generation: 'proof-session', isCurrent: () => true }
  const first = new DraftController('101', makeDraft(), storage, now, auth)
  const firstProof = await first.persistWithProof()
  assert.equal(first.isCurrentPersistenceProof(firstProof), true)

  let release
  storage.write = () => new Promise(resolve => { release = resolve })
  const second = new DraftController('101', makeDraft(), storage, now, auth)
  const newerWrite = second.persistWithProof()
  await new Promise(resolve => setImmediate(resolve))
  assert.equal(typeof release, 'function')
  assert.equal(first.isCurrentPersistenceProof(firstProof), false)
  release()
  const secondProof = await newerWrite
  assert.equal(second.isCurrentPersistenceProof(secondProof), true)
})

test('a queued write from another controller prevents an older write from returning a proof', async () => {
  const data = new Map()
  const releases = []
  const storage = {
    async read(key) { return data.get(key) },
    write(key, value) {
      return new Promise(resolve => releases.push(() => { data.set(key, value); resolve() }))
    },
  }
  const auth = { generation: 'proof-session', isCurrent: () => true }
  const first = new DraftController('101', makeDraft(), storage, now, auth)
  const second = new DraftController('101', makeDraft(), storage, now, auth)
  const oldWrite = first.persistWithProof()
  await new Promise(resolve => setImmediate(resolve))
  const newerWrite = second.persistWithProof()
  await new Promise(resolve => setImmediate(resolve))
  assert.equal(releases.length, 1)
  releases[0]()
  assert.equal(await oldWrite, null)
  await new Promise(resolve => setImmediate(resolve))
  assert.equal(releases.length, 2)
  releases[1]()
  const newestProof = await newerWrite
  assert.ok(newestProof)
  assert.equal(second.isCurrentPersistenceProof(newestProof), true)
})

test('an unrelated newer key blocks proof issuance but leaves proof-writing controller out of saving state', async () => {
  const releases = []
  const storage = {
    async read() { return undefined },
    write(key, value) {
      if (key.includes('.101.')) return new Promise(resolve => releases.push(() => resolve()))
      return Promise.resolve()
    },
  }
  const auth = { generation: 'proof-session', isCurrent: () => true }
  const first = new DraftController('101', makeDraft(), storage, now, auth)
  const pendingProof = first.persistWithProof()
  await new Promise(resolve => setImmediate(resolve))
  const otherDraft = createRecordingDraft('event-0002', '2026-10-29', 'Asia/Shanghai', '2026-10-29')
  const unrelated = new DraftController('202', otherDraft, storage, now, auth)
  assert.equal(await unrelated.persist(), true)
  releases[0]()
  assert.equal(await pendingProof, null)
  assert.equal(first.status, 'not_saved')
})

test('ordinary persist keeps its boolean success when another key writes concurrently', async () => {
  const releases = []
  const storage = {
    async read() { return undefined },
    write(key) {
      if (key.includes('.101.')) return new Promise(resolve => releases.push(resolve))
      return Promise.resolve()
    },
  }
  const auth = { generation: 'proof-session', isCurrent: () => true }
  const first = new DraftController('101', makeDraft(), storage, now, auth)
  const pendingSave = first.persist()
  await new Promise(resolve => setImmediate(resolve))
  const otherDraft = createRecordingDraft('event-0002', '2026-10-29', 'Asia/Shanghai', '2026-10-29')
  const unrelated = new DraftController('202', otherDraft, storage, now, auth)
  assert.equal(await unrelated.persist(), true)
  releases[0]()
  assert.equal(await pendingSave, true)
  assert.equal(first.status, 'saved_locally')
})

test('loading an old server-linked draft does not overwrite it when stable order requires reconciliation', async () => {
  const storage = memoryStore()
  const auth = { generation: 'session-1', isCurrent: () => true }
  const controller = new DraftController('101', makeDraft(), storage, now, auth)
  const legacy = JSON.parse(JSON.stringify(makeDraft()))
  legacy.server = { id: '101', version: 2 }
  delete legacy.exercises[0].sets[0].setIndex
  const original = JSON.stringify({ schema: 1, subjectId: '101', draft: legacy })
  storage.data.set(controller.key, original)
  await assert.rejects(DraftController.load('101', 'event-0001', storage, now, auth), /核对训练组序号/)
  assert.equal(storage.data.get(controller.key), original)
})
