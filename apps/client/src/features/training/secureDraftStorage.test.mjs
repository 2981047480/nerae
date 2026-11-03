import assert from 'node:assert/strict'
import { test } from 'node:test'
import { addExercise, createRecordingDraft, switchMode, updateSet } from './recording.ts'
import { DraftController } from './draftStorage.ts'
import { SecureDraftStorage } from './secureDraftStorage.ts'

const key = 'nerae.workout.draft.v1.101.event-0001'
const draft = () => addExercise(createRecordingDraft('event-0001', '2026-11-03', 'Asia/Shanghai', '2026-11-03'),
  'entry-0001', { source: 'custom', exerciseName: '深蹲' }, 'weight', 'record-001')
const envelope = value => JSON.stringify({ schema: 1, subjectId: '101', draft: value })
const ok = (found = false, value = '') => ({ ok: true, errorCode: '', found, value })
const fail = errorCode => ({ ok: false, errorCode, found: false, value: '' })
function native() {
  const values = new Map()
  return {
    values, uncertain: false,
    readTrainingDraft(key) { return values.has(key) ? ok(true, values.get(key)) : ok() },
    createTrainingDraft(key, value) { if (values.has(key)) return fail('conflict'); values.set(key, value); return ok() },
    writeTrainingDraft(key, value) {
      if (!values.has(key)) return fail('conflict')
      values.set(key, value)
      return this.uncertain ? fail('unavailable') : ok()
    },
  }
}
const now = () => new Date('2026-11-03T01:00:00Z')

test('one protected draft preserves both recording modes and reopens for the same account', async () => {
  let active = 'one'
  const auth = { generation: 'one', isCurrent: (subject, generation) => subject === '101' && generation === active }
  const port = native()
  const storage = new SecureDraftStorage(port, '101', auth)
  assert.equal(await storage.createIfAbsent(key, envelope(draft())), 'created')
  assert.equal(await storage.createIfAbsent(key, envelope(draft())), 'exists')
  const controller = await DraftController.load('101', 'event-0001', storage, now, auth)
  assert.equal(await controller.update(value => updateSet(value, 'entry-0001', 'record-001', 'reps', '8')), true)
  assert.equal(await controller.update(value => switchMode(value, 'per_set')), true)
  assert.equal(await controller.update(value => updateSet(value, 'entry-0001', 'record-001', 'weightKg', '42.5')), true)
  const restored = await DraftController.load('101', 'event-0001', storage, now, auth)
  assert.equal(restored.draft.mode, 'per_set')
  assert.equal(restored.draft.exercises[0].sets[0].reps, '8')
  assert.equal(restored.draft.exercises[0].sets[0].weightKg, '42.5')
  assert.equal(await restored.update(value => switchMode(value, 'after_workout')), true)
  assert.equal(restored.draft.exercises[0].sets[0].reps, '8')
  active = 'other'
  await assert.rejects(storage.read(key), /账号已变化/)
})

test('prepared writes bind one exact snapshot and remain ordered after the account changes', async () => {
  let active = 'one'
  const auth = { generation: 'one', isCurrent: (subject, generation) => subject === '101' && generation === active }
  const port = native()
  const storage = new SecureDraftStorage(port, '101', auth)
  assert.equal(await storage.createIfAbsent(key, envelope(draft())), 'created')
  const firstValue = envelope(updateSet(draft(), 'entry-0001', 'record-001', 'reps', '8'))
  const secondValue = envelope(updateSet(draft(), 'entry-0001', 'record-001', 'reps', '9'))
  const first = storage.prepareWrite(key, firstValue)
  const second = storage.prepareWrite(key, secondValue)
  assert.equal(JSON.stringify(first), undefined)
  active = 'other'
  assert.throws(() => storage.prepareWrite(key, firstValue), /账号已变化/)
  await first()
  await second()
  await assert.rejects(first(), /已使用/)
  assert.equal(port.values.get(key), secondValue)
})

test('a queued authorized draft write completes after logout without overwriting a later accepted edit', async () => {
  let active = true
  const auth = { generation: 'one', isCurrent: (subject, generation) => active && subject === '101' && generation === 'one' }
  const port = native()
  const storage = new SecureDraftStorage(port, '101', auth)
  await storage.createIfAbsent(key, envelope(draft()))
  let release
  const slow = { read: storage.read.bind(storage), write: (draftKey, value) => new Promise(resolve => {
    release = () => { port.writeTrainingDraft(draftKey, value); resolve() }
  }) }
  const first = new DraftController('101', draft(), slow, now, auth)
  const second = new DraftController('101', draft(), storage, now, auth)
  const writeOne = first.update(value => updateSet(value, 'entry-0001', 'record-001', 'reps', '8'))
  const writeTwo = second.update(value => updateSet(value, 'entry-0001', 'record-001', 'reps', '9'))
  await new Promise(resolve => setImmediate(resolve))
  active = false
  release()
  assert.equal(await writeOne, false)
  assert.equal(await writeTwo, false)
  assert.equal(JSON.parse(port.values.get(key)).draft.exercises[0].sets[0].reps, '9')
  assert.throws(() => second.draft, /账号已变化/)
  await assert.rejects(storage.write(key, envelope(draft())), /账号已变化/)
})

test('draft keys, subject, event and per-draft UTF-8 limit are checked before native writes', async () => {
  const port = native()
  const auth = { generation: 'one', isCurrent: () => true }
  const storage = new SecureDraftStorage(port, '101', auth)
  await assert.rejects(storage.createIfAbsent('nerae.workout.draft.v1.202.event-0001', envelope(draft())), /不属于当前账号/)
  await assert.rejects(storage.createIfAbsent(key, JSON.stringify({ schema: 1, subjectId: '202', draft: draft() })), /不属于当前记录/)
  await assert.rejects(storage.createIfAbsent(key, JSON.stringify({ schema: 1, subjectId: '101', draft: { ...draft(), clientEventId: 'event-0002' } })), /不属于当前记录/)
  await assert.rejects(storage.createIfAbsent(key, JSON.stringify({ schema: 1, subjectId: '101', draft: { ...draft(), note: '汉'.repeat(22000) } })), /容量限制/)
  assert.equal(port.values.size, 0)
})

test('escaped invalid Unicode cannot cross the native boundary, while emoji round trips', async () => {
  const port = native()
  const storage = new SecureDraftStorage(port, '101', { generation: 'one', isCurrent: () => true })
  for (const bad of [
    { ...draft(), note: '\ud800' },
    { ...draft(), exercises: [{ ...draft().exercises[0], exerciseName: '\udc00' }] },
    { ...draft(), ['\ud800']: 'value' },
  ]) await assert.rejects(storage.createIfAbsent(key, envelope(bad)), /格式无效/)
  assert.equal(port.values.size, 0)
  const valid = envelope({ ...draft(), note: '运动 🏋️' })
  assert.equal(await storage.createIfAbsent(key, valid), 'created')
  assert.equal(await storage.read(key), valid)
})

test('uncertain native write keeps the last input and never reports it saved', async () => {
  const port = native()
  const auth = { generation: 'one', isCurrent: () => true }
  const storage = new SecureDraftStorage(port, '101', auth)
  await storage.createIfAbsent(key, envelope(draft()))
  const controller = await DraftController.load('101', 'event-0001', storage, now, auth)
  port.uncertain = true
  assert.equal(await controller.update(value => updateSet(value, 'entry-0001', 'record-001', 'reps', '9')), false)
  assert.equal(controller.status, 'storage_error')
  assert.equal(JSON.parse(port.values.get(key)).draft.exercises[0].sets[0].reps, '9')
  assert.equal(controller.draft.exercises[0].sets[0].reps, '9')
  port.uncertain = false
  assert.equal(await controller.persist(), true)
})

test('unsupported native slots fail without creating or pretending to save', async () => {
  const port = { readTrainingDraft: () => fail('unsupported_platform'), createTrainingDraft: () => fail('unsupported_platform'),
    writeTrainingDraft: () => fail('unsupported_platform') }
  const storage = new SecureDraftStorage(port, '101', { generation: 'one', isCurrent: () => true })
  await assert.rejects(storage.createIfAbsent(key, envelope(draft())), /未确认/)
  await assert.rejects(storage.read(key), /读取本机草稿失败/)
})
