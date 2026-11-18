import assert from 'node:assert/strict'
import { test } from 'node:test'
import { addExercise, addSet, applyBatchSetValues, batchSetSnapshot, createRecordingDraft, updateSet } from './recording.ts'

function fixture() {
  let draft = { ...createRecordingDraft('event-0001', '2026-11-16', 'Asia/Shanghai', '2026-11-16'), recordFormat: 2 }
  draft = addExercise(draft, 'entry-0001', { source: 'custom', exerciseName: '深蹲' }, 'weight', 'record-001')
  draft = addSet(draft, 'entry-0001', 'record-002')
  draft = addSet(draft, 'entry-0001', 'record-003')
  draft = updateSet(draft, 'entry-0001', 'record-001', 'weightKg', '45')
  draft = updateSet(draft, 'entry-0001', 'record-001', 'reps', '8')
  draft = updateSet(draft, 'entry-0001', 'record-002', 'weightKg', '50')
  draft = updateSet(draft, 'entry-0001', 'record-002', 'reps', '6')
  draft = updateSet(draft, 'entry-0001', 'record-003', 'weightKg', '30')
  return draft
}

test('selected groups get only the selected field and new groups are created together', () => {
  const initial = fixture()
  const snapshot = batchSetSnapshot(initial.exercises[0])
  const result = applyBatchSetValues(initial, 'entry-0001', 'record-001', ['record-001', 'record-003'],
    ['weightKg'], ['record-004', 'record-005'], snapshot)
  assert.deepEqual(result.exercises[0].sets.map(s => [s.weightKg, s.reps]), [
    ['45', '8'], ['50', '6'], ['45', ''], ['45', ''], ['45', ''],
  ])
  assert.deepEqual(result.exercises[0].sets.map(s => s.recordKey), ['record-001','record-002','record-003','record-004','record-005'])
  assert.deepEqual(initial.exercises[0].sets.map(s => s.weightKg), ['45','50','30'])
})

test('copying both fields retains each selected group identity and does not mark it confirmed', () => {
  const initial = fixture()
  const result = applyBatchSetValues(initial, 'entry-0001', 'record-001', ['record-002'],
    ['weightKg','reps'], [], batchSetSnapshot(initial.exercises[0]))
  assert.equal(result.exercises[0].sets[1].recordKey, 'record-002')
  assert.equal(result.exercises[0].sets[1].weightKg, '45')
  assert.equal(result.exercises[0].sets[1].reps, '8')
  assert.equal(result.exercises[0].sets[1].confirmed, false)
})

test('stale selection, duplicate keys, blank or invalid source fields do not alter a draft', () => {
  const initial = fixture()
  const snapshot = batchSetSnapshot(initial.exercises[0])
  const changed = updateSet(initial, 'entry-0001', 'record-002', 'reps', '7')
  assert.throws(() => applyBatchSetValues(changed, 'entry-0001', 'record-001', ['record-002'], ['weightKg'], [], snapshot))
  assert.throws(() => applyBatchSetValues(initial, 'entry-0001', 'record-001', ['record-002','record-002'], ['weightKg'], [], snapshot))
  assert.throws(() => applyBatchSetValues(initial, 'entry-0001', 'record-001', ['record-002'], ['reps'], ['record-002'], snapshot))
  const blank = updateSet(initial, 'entry-0001', 'record-001', 'weightKg', '')
  assert.throws(() => applyBatchSetValues(blank, 'entry-0001', 'record-001', ['record-002'], ['weightKg'], [], batchSetSnapshot(blank.exercises[0])))
  const invalid = updateSet(initial, 'entry-0001', 'record-001', 'reps', '101')
  assert.throws(() => applyBatchSetValues(invalid, 'entry-0001', 'record-001', ['record-002'], ['reps'], [], batchSetSnapshot(invalid.exercises[0])))
  assert.equal(initial.exercises[0].sets.length, 3)
  assert.equal(initial.exercises[0].sets[1].weightKg, '50')
})
