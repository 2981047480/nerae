import assert from 'node:assert/strict'
import { test } from 'node:test'
import { addExercise, addSet, createRecordingDraft, removeUnsubmittedSet, switchMode, updateSet } from './recording.ts'
import { prepareRecordingV2Batch } from './recordingV2Projection.ts'

const fresh = () => {
  let draft = createRecordingDraft('event-0001', '2026-10-28', 'Asia/Shanghai', '2026-10-29')
  draft = addExercise(draft, 'entry-0001', { source: 'custom', exerciseName: '深蹲' }, 'weight', 'record-001')
  return { ...draft, server: { id: '101', version: 1, status: 'in_progress' } }
}
const baseline = () => ({ id: '101', version: 1, source: 'free', date: '2026-10-28', timeZone: 'Asia/Shanghai',
  clientEventId: 'event-0001', status: 'in_progress', mode: 'after_workout', createdAt: '2026-10-28T00:00:00Z',
  updatedAt: '2026-10-28T00:00:00Z', entries: [], sets: [], removedSets: [] })

test('a filled group and a blank group remain separate and are both sent', () => {
  let draft = addSet(fresh(), 'entry-0001', 'record-002')
  draft = updateSet(updateSet(draft, 'entry-0001', 'record-001', 'weightKg', '45'), 'entry-0001', 'record-001', 'reps', '8')
  const result = prepareRecordingV2Batch(draft, baseline())
  assert.equal(result.ok, true)
  assert.equal(result.batch.expectedVersion, 1)
  assert.deepEqual(result.batch.exerciseEntryKeys, ['entry-0001'])
  assert.deepEqual(result.batch.sets.map(s => s.measurement), [{ method: 'total', weightKg: '45', reps: 8 }, { method: 'total' }])
})

test('an offline removed group keeps its latest values and visible position', () => {
  let draft = addSet(fresh(), 'entry-0001', 'record-002')
  draft = updateSet(draft, 'entry-0001', 'record-002', 'weightKg', '14')
  draft = removeUnsubmittedSet(draft, 'entry-0001', 'record-002')
  const result = prepareRecordingV2Batch(draft, baseline())
  assert.equal(result.ok, true)
  assert.deepEqual(result.batch.removeRecords, [{ recordKey: 'record-002', removedPosition: 2 }])
  assert.equal(result.batch.sets.find(s => s.recordKey === 'record-002').measurement.weightKg, '14')
})

test('mode switch preserves all groups without requiring confirmation', () => {
  const draft = switchMode(fresh(), 'per_set')
  const result = prepareRecordingV2Batch(draft, baseline())
  assert.equal(result.ok, true)
  assert.equal(result.batch.mode, 'per_set')
  assert.equal(result.batch.sets[0].confirmed, false)
})

test('a changed remote version, unsupported identity, or invalid input cannot overwrite data', () => {
  const changed = baseline(); changed.version = 2
  assert.equal(prepareRecordingV2Batch(fresh(), changed).reason, 'version_conflict')
  const catalog = fresh(); catalog.exercises[0].identity = { source: 'catalog', exerciseId: '101' }
  assert.equal(prepareRecordingV2Batch(catalog, baseline()).reason, 'unsupported')
  const invalid = updateSet(fresh(), 'entry-0001', 'record-001', 'weightKg', '12.999')
  assert.equal(prepareRecordingV2Batch(invalid, baseline()).reason, 'invalid_input')
})

test('a known removed group can be restored and edited in the same batch', () => {
  const draft = fresh()
  draft.server.version = 2
  draft.exercises[0].sets[0].weightKg = '55'
  draft.exercises[0].sets[0].reps = '8'
  const remote = baseline(); remote.version = 2
  remote.entries = [{ exerciseEntryKey: 'entry-0001', source: 'custom', exerciseName: '深蹲' }]
  remote.removedSets = [{ removedPosition: 1, set: { recordKey: 'record-001', exerciseEntryKey: 'entry-0001', setIndex: 1,
    confirmed: false, measurement: { method: 'total', weightKg: '50', reps: 8 } } }]
  const result = prepareRecordingV2Batch(draft, remote)
  assert.equal(result.ok, true)
  assert.deepEqual(result.batch.restoreRecordKeys, ['record-001'])
  assert.equal(result.batch.sets[0].measurement.weightKg, '55')
})

test('an unknown server group must be reconciled, never silently discarded', () => {
  const draft = fresh(); draft.server.version = 2
  const remote = baseline(); remote.version = 2
  remote.entries = [{ exerciseEntryKey: 'entry-0001', source: 'custom', exerciseName: '深蹲' }]
  remote.sets = [{ recordKey: 'record-999', exerciseEntryKey: 'entry-0001', setIndex: 1, confirmed: false, measurement: { method: 'total' } }]
  assert.equal(prepareRecordingV2Batch(draft, remote).reason, 'reconcile_required')
})

test('explicit zero remains different from an empty value', () => {
  const draft = updateSet(fresh(), 'entry-0001', 'record-001', 'weightKg', '0')
  const result = prepareRecordingV2Batch(draft, baseline())
  assert.equal(result.ok, true)
  assert.equal(result.batch.sets[0].measurement.weightKg, '0')
})

test('untrusted or incomplete snapshots fail closed without throwing', () => {
  for (const broken of [null, {}, { ...baseline(), entries: [null] },
    { ...baseline(), entries: [{ exerciseEntryKey: 'entry-0001', source: 'custom', exerciseName: '深蹲' }], sets: [null] }]) {
    assert.doesNotThrow(() => prepareRecordingV2Batch(fresh(), broken))
    assert.equal(prepareRecordingV2Batch(fresh(), broken).ok, false)
  }
})

test('editing a restored group and removing it again preserves the new removed snapshot', () => {
  let draft = fresh(); draft.server.version = 2
  draft = updateSet(draft, 'entry-0001', 'record-001', 'weightKg', '65')
  draft = removeUnsubmittedSet(draft, 'entry-0001', 'record-001')
  const remote = baseline(); remote.version = 2
  remote.entries = [{ exerciseEntryKey: 'entry-0001', source: 'custom', exerciseName: '深蹲' }]
  remote.removedSets = [{ removedPosition: 1, set: { recordKey: 'record-001', exerciseEntryKey: 'entry-0001', setIndex: 1,
    confirmed: false, measurement: { method: 'total', weightKg: '60' } } }]
  const result = prepareRecordingV2Batch(draft, remote)
  assert.equal(result.ok, true)
  assert.deepEqual(result.batch.restoreRecordKeys, ['record-001'])
  assert.deepEqual(result.batch.removeRecords, [{ recordKey: 'record-001', removedPosition: 1 }])
  assert.equal(result.batch.sets[0].measurement.weightKg, '65')
})


test('date edits produce an explicit versioned batch and keep newer local input',()=>{
  const draft=fresh();draft.date='2026-10-27'
  const result=prepareRecordingV2Batch(draft,baseline())
  assert.equal(result.ok,true);assert.equal(result.changed,true);assert.equal(result.batch.date,'2026-10-27')
  const remote=baseline();remote.version=2
  assert.equal(prepareRecordingV2Batch(draft,remote).reason,'version_conflict')
})
