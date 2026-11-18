import assert from 'node:assert/strict'
import { test } from 'node:test'
import { addExercise, addSet, createRecordingDraft, setExerciseMeasure, setSideState, updateSet } from './recording.ts'
import { prepareRecordingV2Batch } from './recordingV2Projection.ts'
import { measurementFromSet, draftFieldsFromMeasurement, validV2DraftSet } from './recordingV2Measure.ts'
import { recordingV2ConflictRows } from './recordingV2ConflictPlan.ts'

const today = '2026-11-16'
function opened() {
  const value = { ...createRecordingDraft('event-0001', '2026-11-16', 'Asia/Shanghai', today), recordFormat: 2,
    server: { id: '101', version: 1, status: 'in_progress' } }
  return addExercise(value, 'entry-0001', { source: 'custom', exerciseName: '卧推' }, 'weight', 'record-001')
}
const remote = () => ({ id: '101', version: 1, source: 'free', date: '2026-11-16', timeZone: 'Asia/Shanghai',
  clientEventId: 'event-0001', status: 'in_progress', mode: 'after_workout', createdAt: '2026-11-16T00:00:00Z',
  updatedAt: '2026-11-16T00:00:00Z', entries: [{ exerciseEntryKey: 'entry-0001', source: 'custom', exerciseName: '卧推' }],
  sets: [{ recordKey: 'record-001', exerciseEntryKey: 'entry-0001', setIndex: 1, confirmed: false, measurement: { method: 'total', weightKg: '12', reps: 10 } }],
  removedSets: [] })

test('each implement is sent as pair_each and counted by the server contract, not doubled in the field', () => {
  let draft = setExerciseMeasure(updateSet(updateSet(opened(), 'entry-0001', 'record-001', 'weightKg', '12'), 'entry-0001', 'record-001', 'reps', '10'), 'entry-0001', 'pair_each')
  const result = prepareRecordingV2Batch(draft, remote())
  assert.equal(result.ok, true)
  assert.deepEqual(result.batch.sets[0].measurement, { method: 'pair_each', weightKg: '12', reps: 10 })
})

test('the first side fills the other until that side is edited or marked not performed', () => {
  let draft = setExerciseMeasure(opened(), 'entry-0001', 'sides')
  draft = updateSet(draft, 'entry-0001', 'record-001', 'leftWeightKg', '12')
  draft = updateSet(draft, 'entry-0001', 'record-001', 'leftReps', '10')
  assert.equal(draft.exercises[0].sets[0].rightWeightKg, '12')
  assert.equal(draft.exercises[0].sets[0].rightReps, '10')
  draft = updateSet(draft, 'entry-0001', 'record-001', 'rightWeightKg', '10')
  draft = updateSet(draft, 'entry-0001', 'record-001', 'leftWeightKg', '14')
  assert.equal(draft.exercises[0].sets[0].rightWeightKg, '10')
  draft = setSideState(draft, 'entry-0001', 'record-001', 'right', 'not_performed')
  const measurement = measurementFromSet(draft.exercises[0], draft.exercises[0].sets[0])
  assert.deepEqual(measurement.right, { state: 'not_performed' })
  assert.equal(measurement.left.weightKg, '14')
  assert.equal(measurement.right.weightKg, undefined)
})

test('one bodyweight edit updates every bodyweight set in this session and leaves a hand-entered result', () => {
  let draft = addExercise(setExerciseMeasure(opened(), 'entry-0001', 'bodyweight_assisted'), 'entry-0002', { source: 'custom', exerciseName: '引体' }, 'weight', 'record-002')
  draft = setExerciseMeasure(draft, 'entry-0002', 'direct')
  draft = updateSet(draft, 'entry-0002', 'record-002', 'weightKg', '50')
  draft = addSet(draft, 'entry-0001', 'record-003')
  draft = updateSet(draft, 'entry-0001', 'record-001', 'bodyweightKg', '70')
  assert.equal(draft.exercises[0].sets[0].bodyweightKg, '70')
  assert.equal(draft.exercises[0].sets[1].bodyweightKg, '70')
  assert.equal(draft.exercises[1].sets[0].weightKg, '50')
  assert.equal(draft.exercises[1].sets[0].bodyweightKg, undefined)
  draft = updateSet(updateSet(draft, 'entry-0001', 'record-001', 'adjustmentKg', '80'), 'entry-0001', 'record-001', 'reps', '10')
  assert.equal(validV2DraftSet(draft.exercises[0], draft.exercises[0].sets[0]), false)
})

test('the first side entered is copied to the other side until that side is edited', () => {
  let draft = setExerciseMeasure(opened(), 'entry-0001', 'sides')
  draft = updateSet(draft, 'entry-0001', 'record-001', 'rightWeightKg', '12')
  draft = updateSet(draft, 'entry-0001', 'record-001', 'rightReps', '10')
  assert.equal(draft.exercises[0].sets[0].leftWeightKg, '12')
  assert.equal(draft.exercises[0].sets[0].leftReps, '10')
  draft = updateSet(draft, 'entry-0001', 'record-001', 'leftWeightKg', '14')
  assert.equal(draft.exercises[0].sets[0].leftWeightKg, '14')
  assert.equal(draft.exercises[0].sets[0].rightWeightKg, '12')
})

test('returning a not-performed side to entry does not revive its previous numbers', () => {
  let draft = setExerciseMeasure(opened(), 'entry-0001', 'sides')
  draft = updateSet(draft, 'entry-0001', 'record-001', 'leftWeightKg', '12')
  draft = updateSet(draft, 'entry-0001', 'record-001', 'leftReps', '10')
  draft = setSideState(draft, 'entry-0001', 'record-001', 'right', 'not_performed')
  draft = updateSet(draft, 'entry-0001', 'record-001', 'leftWeightKg', '14')
  draft = setSideState(draft, 'entry-0001', 'record-001', 'right', 'unknown')
  const measurement = measurementFromSet(draft.exercises[0], draft.exercises[0].sets[0])
  assert.equal(measurement.left.weightKg, '14')
  assert.equal(measurement.left.reps, 10)
  assert.equal(measurement.right, undefined)
  assert.equal(draft.exercises[0].sets[0].rightWeightKg, '')
  assert.equal(draft.exercises[0].sets[0].rightReps, '')
})

test('a not-performed side ignores stored weight when it is restored or compared', () => {
  const fields = draftFieldsFromMeasurement({
    method: 'sides',
    left: { state: 'performed', weightKg: '12', reps: 10 },
    right: { state: 'not_performed', weightKg: '99', reps: 8 },
  })
  assert.equal(fields.rightState, 'not_performed')
  assert.equal(fields.rightWeightKg, '')
  assert.equal(fields.rightReps, '')
  const stale = {
    recordKey: 'record-001', setIndex: 1, weightKg: '', reps: '', seconds: '', rpe: '',
    confirmed: false, persisted: true, dirty: false,
    leftWeightKg: '12', leftReps: '10', leftState: 'performed',
    rightWeightKg: '12', rightReps: '10', rightState: 'not_performed',
  }
  const draft = {
    ...opened(),
    exercises: [{ exerciseEntryKey: 'entry-0001', identity: { source: 'custom', exerciseName: '划船' }, kind: 'weight', measure: 'sides', sets: [stale], removedSets: [] }],
  }
  const wire = {
    recordKey: 'record-001', exerciseEntryKey: 'entry-0001', setIndex: 1, confirmed: false,
    measurement: { method: 'sides', left: { state: 'performed', weightKg: '12', reps: 10 }, right: { state: 'not_performed' } },
  }
  const remote = {
    id: '101', version: 2, source: 'free', date: draft.date, timeZone: draft.timeZone, clientEventId: draft.clientEventId,
    status: 'in_progress', mode: draft.mode, createdAt: '2026-11-16T00:00:00Z', updatedAt: '2026-11-16T00:00:00Z',
    entries: [{ exerciseEntryKey: 'entry-0001', source: 'custom', exerciseName: '划船' }],
    sets: [wire], removedSets: [],
  }
  const active = recordingV2ConflictRows(draft, remote)
  assert.ok(Array.isArray(active))
  assert.equal(active.length, 0)
  const removedDraft = {
    ...draft,
    exercises: [{ ...draft.exercises[0], sets: [], removedSets: [{ set: stale, removedPosition: 1 }] }],
  }
  const removedRemote = { ...remote, sets: [], removedSets: [{ removedPosition: 1, set: wire }] }
  const removed = recordingV2ConflictRows(removedDraft, removedRemote)
  assert.ok(Array.isArray(removed))
  assert.equal(removed.length, 0)
})
