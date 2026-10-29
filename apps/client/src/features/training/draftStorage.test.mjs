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
