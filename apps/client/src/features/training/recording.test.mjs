import assert from 'node:assert/strict'
import { test } from 'node:test'
import {
  addExercise, addSet, confirmSet, createRecordingDraft, prepareSave as prepareSaveAt,
  removeUnsubmittedSet, restoreDraft, switchMode, updateSet,
} from './recording.ts'

const prepareSave = draft => prepareSaveAt(draft, '2026-10-29')
const fresh = () => createRecordingDraft('event-0001', '2026-10-28', 'Asia/Shanghai', '2026-10-29')
const withExercise = (kind = 'weight') => addExercise(fresh(), 'entry-0001', { source: 'custom', exerciseName: '深蹲' }, kind, 'record-001')
const setInput = (draft, key, field, value) => updateSet(draft, 'entry-0001', key, field, value)

test('switching modes preserves the same session, group keys, inputs and confirmations', () => {
  let draft = withExercise()
  draft = setInput(setInput(draft, 'record-001', 'weightKg', '40.5'), 'record-001', 'reps', '8')
  const marked = confirmSet(draft, 'entry-0001', 'record-001')
  assert.equal(marked.ok, true)
  const alternate = switchMode(marked.draft, 'per_set')
  const back = switchMode(alternate, 'after_workout')
  assert.equal(back.clientEventId, 'event-0001')
  assert.deepEqual(back.exercises, marked.draft.exercises)
  assert.equal(back.exercises[0].sets[0].confirmed, true)
  assert.equal(back.exercises[0].sets[0].recordKey, 'record-001')
})

test('blank groups are omitted while a half-filled group blocks the whole explicit save', () => {
  let draft = withExercise()
  draft = addSet(draft, 'entry-0001', 'record-002')
  draft = setInput(setInput(draft, 'record-001', 'weightKg', '40'), 'record-001', 'reps', '8')
  const ready = prepareSave(draft)
  assert.equal(ready.ok, true)
  assert.deepEqual(ready.sets.map(item => item.recordKey), ['record-001'])
  draft = setInput(draft, 'record-002', 'weightKg', '20')
  const bad = prepareSave(draft)
  assert.equal(bad.ok, false)
  assert.equal(bad.issues[0].recordKey, 'record-002')
  assert.equal(draft.exercises[0].sets[0].confirmed, false)
})

test('confirming a single group does not confirm another; editing it removes local confirmation', () => {
  let draft = switchMode(withExercise(), 'per_set')
  draft = addSet(draft, 'entry-0001', 'record-002')
  draft = setInput(setInput(draft, 'record-001', 'weightKg', '40'), 'record-001', 'reps', '8')
  const marked = confirmSet(draft, 'entry-0001', 'record-001')
  assert.equal(marked.ok, true)
  assert.equal(marked.draft.exercises[0].sets[0].confirmed, true)
  assert.equal(marked.draft.exercises[0].sets[1].confirmed, false)
  assert.equal(confirmSet(marked.draft, 'entry-0001', 'record-002').ok, false)
  const edited = setInput(marked.draft, 'record-001', 'reps', '9')
  assert.equal(edited.exercises[0].sets[0].confirmed, false)
  assert.equal(edited.exercises[0].sets[0].recordKey, 'record-001')
})

test('explicit save includes complete unconfirmed rows and remains repeatable without changing keys', () => {
  let draft = withExercise('durationPerSide')
  draft = setInput(draft, 'record-001', 'seconds', '45')
  const first = prepareSave(draft)
  const second = prepareSave(draft)
  assert.equal(first.ok, true)
  assert.deepEqual(first, second)
  assert.deepEqual(first.sets[0], {
    exerciseEntryKey: 'entry-0001', recordKey: 'record-001', setIndex: 1,
    kind: 'durationPerSide', completed: true, exerciseName: '深蹲', seconds: 45,
  })
  assert.equal(draft.exercises[0].sets[0].confirmed, false)
})

test('measurements follow the contract and never invent a duration or a missing field', () => {
  for (const [kind, field, good, bad] of [
    ['weight', 'weightKg', '0.01', '0'],
    ['perSide', 'weightKg', '300', '300.01'],
    ['bodyweight', 'reps', '1', '0'],
    ['bodyweightPerSide', 'reps', '100', '101'],
    ['breath', 'reps', '5', '1.5'],
    ['duration', 'seconds', '5', '4'],
    ['durationPerSide', 'seconds', '600', '601'],
  ]) {
    let draft = withExercise(kind)
    if (kind === 'weight' || kind === 'perSide') draft = setInput(draft, 'record-001', 'reps', '8')
    const valid = prepareSave(setInput(draft, 'record-001', field, good))
    assert.equal(valid.ok, true, kind)
    assert.equal('seconds' in valid.sets[0], kind.startsWith('duration'))
    assert.equal(prepareSave(setInput(draft, 'record-001', field, bad)).ok, false, kind)
  }
  let precision = withExercise()
  precision = setInput(setInput(precision, 'record-001', 'weightKg', '1.001'), 'record-001', 'reps', '8')
  assert.equal(prepareSave(precision).ok, false)
  assert.equal(prepareSave(setInput(precision, 'record-001', 'weightKg', '1e2')).ok, false)
})

test('extra measurements and invalid optional ratings do not leak into a write', () => {
  let draft = withExercise('bodyweight')
  draft = setInput(draft, 'record-001', 'reps', '10')
  assert.equal(prepareSave(setInput(draft, 'record-001', 'seconds', '10')).ok, false)
  assert.equal(prepareSave(setInput(draft, 'record-001', 'rpe', '10.1')).ok, false)
  assert.equal(prepareSave(setInput(draft, 'record-001', 'rpe', '7.25')).ok, false)
  const good = prepareSave(setInput(draft, 'record-001', 'rpe', '7.5'))
  assert.equal(good.ok, true)
  assert.equal(good.sets[0].rpe, 7.5)
})

test('stable identifiers must be unique across exercise entries and groups', () => {
  const draft = withExercise()
  assert.throws(() => addSet(draft, 'entry-0001', 'record-001'), /已存在/)
  assert.throws(() => addExercise(draft, 'entry-0001', { source: 'custom', exerciseName: '划船' }, 'weight', 'record-002'), /已存在/)
  assert.throws(() => addExercise(draft, 'entry-0002', { source: 'custom', exerciseName: '划船' }, 'weight', 'record-001'), /已存在/)
  assert.throws(() => addSet(draft, 'entry-0001', 'short'), /无效/)
})

test('custom names and catalog IDs are validated without using sample data', () => {
  assert.throws(() => addExercise(fresh(), 'entry-0001', { source: 'custom', exerciseName: '  ' }, 'weight', 'record-001'), /名称/)
  assert.throws(() => addExercise(fresh(), 'entry-0001', { source: 'catalog', exerciseId: '0' }, 'weight', 'record-001'), /标识/)
  const draft = addExercise(fresh(), 'entry-0001', { source: 'custom', exerciseName: ' 深蹲 ' }, 'weight', 'record-001')
  assert.equal(draft.exercises[0].identity.exerciseName, '深蹲')
})

test('invalid or future training dates and invalid timezones are rejected', () => {
  assert.throws(() => createRecordingDraft('event-0001', '2026-02-30', 'Asia/Shanghai', '2026-10-29'), /日期/)
  assert.throws(() => createRecordingDraft('event-0001', '2026-10-30', 'Asia/Shanghai', '2026-10-29'), /日期/)
  assert.throws(() => createRecordingDraft('event-0001', '2026-10-28', 'No/Such_Timezone', '2026-10-29'), /时区/)
})

test('unsubmitted rows can be removed, but synced rows need the real deletion workflow', () => {
  let draft = withExercise()
  draft = addSet(draft, 'entry-0001', 'record-002')
  const removed = removeUnsubmittedSet(draft, 'entry-0001', 'record-002')
  assert.deepEqual(removed.exercises[0].sets.map(item => item.recordKey), ['record-001'])
  const synced = { ...removed, exercises: [{ ...removed.exercises[0], sets: [{ ...removed.exercises[0].sets[0], persisted: true }] }] }
  assert.throws(() => removeUnsubmittedSet(synced, 'entry-0001', 'record-001'), /删除流程/)
})

test('empty records and batches over the contract limit do not produce a save', () => {
  assert.equal(prepareSave(fresh()).ok, false)
  let draft = withExercise('bodyweight')
  draft = setInput(draft, 'record-001', 'reps', '1')
  for (let i = 2; i <= 201; i++) {
    const key = `record-${String(i).padStart(3, '0')}`
    draft = setInput(addSet(draft, 'entry-0001', key), key, 'reps', '1')
  }
  const result = prepareSave(draft)
  assert.equal(result.ok, false)
  assert.match(result.message, /200/)
})


test('editing a previously acknowledged group marks its current values dirty', () => {
  let draft = withExercise('bodyweight')
  draft = setInput(draft, 'record-001', 'reps', '8')
  const acknowledged = {
    ...draft,
    server: { id: '101', version: 3 },
    finish: { durationMin: null, note: '备注' },
    exercises: [{ ...draft.exercises[0], sets: [{ ...draft.exercises[0].sets[0], persisted: true, dirty: false, confirmed: true }] }],
  }
  const changed = setInput(acknowledged, 'record-001', 'reps', '9')
  assert.equal(changed.exercises[0].sets[0].persisted, true)
  assert.equal(changed.exercises[0].sets[0].dirty, true)
  assert.equal(changed.exercises[0].sets[0].confirmed, false)
  assert.deepEqual(switchMode(changed, 'per_set').server, { id: '101', version: 3 })
  assert.deepEqual(changed.finish, { durationMin: null, note: '备注' })
})

test('recovery rejects duplicate identifiers, malformed state and invalid server versions', () => {
  let draft = withExercise('bodyweight')
  draft = setInput(draft, 'record-001', 'reps', '8')
  const recovered = restoreDraft(JSON.stringify(draft), '2026-10-29')
  assert.equal(prepareSave(recovered).ok, true)
  const corrupt = JSON.parse(JSON.stringify(draft))
  corrupt.exercises[0].sets.push({ ...corrupt.exercises[0].sets[0] })
  assert.throws(() => restoreDraft(corrupt, '2026-10-29'), /重复/)
  assert.equal(prepareSave(corrupt).ok, false)
  const badVersion = { ...draft, server: { id: '101', version: 0 } }
  assert.throws(() => restoreDraft(badVersion, '2026-10-29'), /版本/)
  assert.throws(() => restoreDraft('{broken', '2026-10-29'), /无法读取/)
})

test('a save rechecks the current date instead of trusting a modified draft', () => {
  const invalid = { ...withExercise(), date: '2026-10-30' }
  assert.equal(prepareSave(invalid).ok, false)
})
