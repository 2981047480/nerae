import assert from 'node:assert/strict'
import { test } from 'node:test'
import { addExercise, createRecordingDraft, switchMode, updateSet } from './recording.ts'
import { DraftController } from './draftStorage.ts'
import { VersionedEditorDraft } from './versionedEditorDraft.ts'
import { SecureDraftStorage } from './secureDraftStorage.ts'

const key = 'nerae.workout.draft.v1.101.event-0001'
const draft = () => addExercise(createRecordingDraft('event-0001', '2026-11-03', 'Asia/Shanghai', '2026-11-03'),
  'entry-0001', { source: 'custom', exerciseName: '深蹲' }, 'weight', 'record-001')
const envelope = value => JSON.stringify({ schema: 1, subjectId: '101', draft: value })
const ok = (found = false, value = '') => ({ ok: true, errorCode: '', found, value })
const fail = errorCode => ({ ok: false, errorCode, found: false, value: '' })
function native() {
  const values = new Map()
  const revisions = new Map()
  return {
    values, revisions, uncertain: false,
    discoverTrainingDrafts(subject, cursor) {
      const prefix = `nerae.workout.draft.v1.${subject}.`
      const all = [...values.keys()].filter(key => key.startsWith(prefix)).map(key => key.slice(prefix.length)).sort().filter(id => !cursor || id > cursor)
      const eventIds = all.slice(0, 100)
      return ok(true, JSON.stringify({ eventIds, nextCursor: all.length > 100 ? eventIds.at(-1) : null, hasUnclassifiedItems: false }))
    },
    readVersionedTrainingDraft(key) {
      return values.has(key) ? { ...ok(true, values.get(key)), revision: revisions.get(key) || '0' } : { ...ok(), revision: '0' }
    },
    compareAndSetVersionedTrainingDraft(key, revision, found, before, value) {
      const actualRevision = revisions.get(key) || '0'
      if (values.has(key) !== found || actualRevision !== revision || (values.get(key) || '') !== before) return { ...fail('conflict'), revision: '' }
      const next = (BigInt(revision) + 1n).toString()
      values.set(key, value); revisions.set(key, next)
      return this.uncertain ? { ...fail('unavailable'), revision: '' } : { ...ok(), revision: next }
    },
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
  const controller = await VersionedEditorDraft.load('101', 'event-0001', storage, now, auth)
  assert.equal(await controller.update(value => updateSet(value, 'entry-0001', 'record-001', 'reps', '8')), true)
  assert.equal(await controller.update(value => switchMode(value, 'per_set')), true)
  assert.equal(await controller.update(value => updateSet(value, 'entry-0001', 'record-001', 'weightKg', '42.5')), true)
  controller.release()
  const restored = await VersionedEditorDraft.load('101', 'event-0001', storage, now, auth)
  assert.equal(restored.draft.mode, 'per_set')
  assert.equal(restored.draft.exercises[0].sets[0].reps, '8')
  assert.equal(restored.draft.exercises[0].sets[0].weightKg, '42.5')
  assert.equal(await restored.update(value => switchMode(value, 'after_workout')), true)
  assert.equal(restored.draft.exercises[0].sets[0].reps, '8')
  restored.release()
  active = 'other'
  await assert.rejects(storage.read(key), /账号已变化/)
})

test('versioned writes bind one snapshot and cannot claim success after logout', async () => {
  let active = 'one'
  const auth = { generation: 'one', isCurrent: (subject, generation) => subject === '101' && generation === active }
  const port = native()
  const storage = new SecureDraftStorage(port, '101', auth)
  await storage.createIfAbsent(key, envelope(draft()))
  const before = await storage.readVersioned(key)
  const value = envelope(updateSet(draft(), 'entry-0001', 'record-001', 'reps', '8'))
  const commit = storage.prepareVersionedWrite(key, value)
  assert.throws(() => storage.prepareWrite(key, value), /版本化/)
  active = 'other'
  await assert.rejects(commit(before), /账号已变化/)
  await assert.rejects(commit(before), /已使用/)
  assert.equal(port.values.get(key), value)
})

test('low-level controllers cannot bypass the versioned editor', async () => {
  const auth = { generation: 'one', isCurrent: () => true }
  const port = native()
  const storage = new SecureDraftStorage(port, '101', auth)
  await storage.createIfAbsent(key, envelope(draft()))
  await assert.rejects(DraftController.load('101', 'event-0001', storage, now, auth), /版本化编辑器/)
  assert.equal(port.values.get(key), envelope(draft()))
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
  const controller = await VersionedEditorDraft.load('101', 'event-0001', storage, now, auth)
  port.uncertain = true
  assert.equal(await controller.update(value => updateSet(value, 'entry-0001', 'record-001', 'reps', '9')), false)
  assert.equal(controller.status, 'storage_error')
  assert.equal(JSON.parse(port.values.get(key)).draft.exercises[0].sets[0].reps, '9')
  assert.equal(controller.draft.exercises[0].sets[0].reps, '9')
  port.uncertain = false
  assert.equal(await controller.persist(), true)
  controller.release()
})

test('unsupported native slots fail without creating or pretending to save', async () => {
  const port = { discoverTrainingDrafts: () => fail('unsupported_platform'), readVersionedTrainingDraft: () => ({ ...fail('unsupported_platform'), revision: '' }),
    compareAndSetVersionedTrainingDraft: () => ({ ...fail('unsupported_platform'), revision: '' }) }
  const storage = new SecureDraftStorage(port, '101', { generation: 'one', isCurrent: () => true })
  await assert.rejects(storage.createIfAbsent(key, envelope(draft())), /未确认/)
  await assert.rejects(storage.read(key), /读取本机草稿失败/)
  await assert.rejects(storage.discover(), /查找本机草稿失败/)
})

test('discovery filters by subject, preserves recovery warning and rejects malformed native results', async () => {
  const port = native()
  let active = '101'
  const lease = { generation: 'one', isCurrent: subject => subject === active }
  const storage = new SecureDraftStorage(port, '101', lease)
  await storage.createIfAbsent(key, envelope(draft()))
  port.values.set('nerae.workout.draft.v1.202.event-0002', 'foreign')
  assert.deepEqual(await storage.discover(), { eventIds: ['event-0001'], nextCursor: null, hasUnclassifiedItems: false })
  port.discoverTrainingDrafts = () => ok(true, JSON.stringify({ eventIds: ['event-0001'], nextCursor: null, hasUnclassifiedItems: true }))
  assert.deepEqual(await storage.discover(), { eventIds: ['event-0001'], nextCursor: null, hasUnclassifiedItems: true })
  for (const result of [
    { eventIds: ['event-0001', 'event-0001'], nextCursor: null, hasUnclassifiedItems: false },
    { eventIds: ['short'], nextCursor: null, hasUnclassifiedItems: false },
    { eventIds: ['event-0001'], nextCursor: null, hasUnclassifiedItems: false, extra: 'private' },
    { eventIds: Array(101).fill('event-0001'), nextCursor: null, hasUnclassifiedItems: false },
    { eventIds: ['event-0001'], nextCursor: 'event-0001', hasUnclassifiedItems: false },
  ]) {
    port.discoverTrainingDrafts = () => ok(true, JSON.stringify(result))
    await assert.rejects(storage.discover())
  }
  port.discoverTrainingDrafts = () => { active = '202'; return ok(true, JSON.stringify({ eventIds: ['event-0001'], nextCursor: null, hasUnclassifiedItems: false })) }
  await assert.rejects(storage.discover(), /账号已变化/)
})

test('discovery cursor exposes every matching draft without mixing another subject', async () => {
  const port = native()
  for (let index = 0; index < 235; index++) {
    port.values.set(`nerae.workout.draft.v1.101.event-${String(index).padStart(4, '0')}`, 'fixture')
  }
  port.values.set('nerae.workout.draft.v1.202.event-foreign', 'fixture')
  const storage = new SecureDraftStorage(port, '101', { generation: 'one', isCurrent: () => true })
  const first = await storage.discover()
  const second = await storage.discover(first.nextCursor)
  const third = await storage.discover(second.nextCursor)
  assert.deepEqual([first.eventIds.length, second.eventIds.length, third.eventIds.length], [100, 100, 35])
  assert.equal(third.nextCursor, null)
  assert.equal(new Set([...first.eventIds, ...second.eventIds, ...third.eventIds]).size, 235)
})
