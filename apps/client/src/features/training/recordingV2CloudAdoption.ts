import type { DraftExercise, DraftSet, RecordingDraft } from './recording'
import type { RecordingV2Detail } from './recordingV2Projection'
import { validRecordingV2Set } from './recordingV2Values'
import {
  draftFieldsFromMeasurement, exerciseMeasure, isV2Measure, setValueSignature, type V2Measure, type V2Measurement,
} from './recordingV2Measure'

type RemoteSet = RecordingV2Detail['sets'][number]
function projectSet(set: RemoteSet): { measure: V2Measure; draft: DraftSet } | null {
  if (!isV2Measure(set.measurement.method)) return null
  const fields = draftFieldsFromMeasurement(set.measurement as V2Measurement)
  if (!fields) return null
  return { measure: set.measurement.method, draft: { recordKey: set.recordKey, setIndex: set.setIndex, weightKg: '', reps: '',
    seconds: '', rpe: set.rpe ?? '', confirmed: set.confirmed, persisted: true, dirty: false, ...fields } }
}

/**
 * A read-only cloud superset may be adopted only when all known local groups
 * were acknowledged and remain untouched. Existing local removals are retained
 * as tombstones; mismatched locations or unsupported data fail closed.
 */
export function adoptCloudRecordingV2(draft: RecordingDraft, remote: RecordingV2Detail): RecordingDraft | null {
  const previous = draft.server?.status
  if (draft.recordFormat !== 2 || draft.syncTracking !== 1 || draft.syncPending !== undefined || !draft.server ||
      draft.server.id !== remote.id || draft.clientEventId !== remote.clientEventId ||
      remote.source !== 'free' || remote.version <= draft.server.version || draft.timeZone !== remote.timeZone ||
      draft.date !== remote.date || draft.mode !== remote.mode || !previous ||
      Boolean(draft.completionRequested) !== (previous === 'completed') ||
      (previous !== remote.status && !(previous === 'in_progress' && remote.status === 'completed'))) return null
  if (remote.entries.some(entry => entry.source !== 'custom' || entry.removedPosition !== undefined)) return null
  const entries = new Map(remote.entries.map((entry,index)=>[entry.exerciseEntryKey,{entry,index}]))
  const active = new Map(remote.sets.map(set=>[set.recordKey,set]))
  const removed = new Map(remote.removedSets.map(item=>[item.set.recordKey,item]))
  const localEntries = new Map<string,DraftExercise>()
  let lastPosition = -1
  for (const exercise of draft.exercises) {
    const found = entries.get(exercise.exerciseEntryKey)
    if (!found || found.index <= lastPosition || exercise.identity.source !== 'custom' || exercise.kind !== 'weight' ||
        found.entry.exerciseName !== exercise.identity.exerciseName || localEntries.has(exercise.exerciseEntryKey)) return null
    lastPosition = found.index;localEntries.set(exercise.exerciseEntryKey,exercise)
    for (const set of exercise.sets) {
      const cloud = active.get(set.recordKey)
      const projected = cloud && projectSet(cloud)
      if (!cloud || !projected || projected.measure !== exerciseMeasure(exercise) || cloud.exerciseEntryKey !== exercise.exerciseEntryKey || cloud.setIndex !== set.setIndex ||
          !set.persisted || set.dirty || set.confirmed !== cloud.confirmed ||
          !validRecordingV2Set(exercise,set) || setValueSignature(set, exerciseMeasure(exercise)) !== setValueSignature(projected.draft, projected.measure)) return null
    }
    for (const item of exercise.removedSets || []) {
      const cloud = removed.get(item.set.recordKey)
      const projected = cloud && projectSet(cloud.set)
      if (!cloud || !projected || projected.measure !== exerciseMeasure(exercise) || cloud.set.exerciseEntryKey !== exercise.exerciseEntryKey || cloud.set.setIndex !== item.set.setIndex ||
          cloud.removedPosition !== item.removedPosition || !item.set.persisted || item.set.dirty ||
          item.set.confirmed !== cloud.set.confirmed || !validRecordingV2Set(exercise,item.set) ||
          setValueSignature(item.set, exerciseMeasure(exercise)) !== setValueSignature(projected.draft, projected.measure)) return null
    }
  }
  const activeByEntry = new Map<string,RemoteSet[]>()
  for (const set of remote.sets) {
    const group=activeByEntry.get(set.exerciseEntryKey) || [];group.push(set);activeByEntry.set(set.exerciseEntryKey,group)
  }
  const removedByEntry = new Map<string,RecordingV2Detail['removedSets']>()
  for (const item of remote.removedSets) {
    const key=item.set.exerciseEntryKey;const group=removedByEntry.get(key) || [];group.push(item);removedByEntry.set(key,group)
  }
  const exercises: DraftExercise[] = []
  for (const entry of remote.entries) {
    const previousExercise=localEntries.get(entry.exerciseEntryKey)
    const sets:DraftSet[]=[];const removedSets:NonNullable<DraftExercise['removedSets']>=[]
    const seenMeasures = new Set<V2Measure>()
    const accept = (projected: { measure: V2Measure; draft: DraftSet } | null): DraftSet | null => {
      if (!projected) return null
      const expected = previousExercise ? exerciseMeasure(previousExercise) : projected.measure
      if (projected.measure !== expected) return null
      if (!previousExercise && seenMeasures.size && !seenMeasures.has(projected.measure)) return null
      seenMeasures.add(projected.measure)
      return projected.draft
    }
    for (const set of (activeByEntry.get(entry.exerciseEntryKey) || []).sort((a,b)=>a.setIndex-b.setIndex)) {
      const drafted = accept(projectSet(set)); if (!drafted) return null; sets.push(drafted)
    }
    for (const item of (removedByEntry.get(entry.exerciseEntryKey) || []).sort((a,b)=>a.set.setIndex-b.set.setIndex)) {
      const drafted = accept(projectSet(item.set)); if (!drafted) return null
      removedSets.push({set:drafted,removedPosition:item.removedPosition})
    }
    const measure = previousExercise ? exerciseMeasure(previousExercise) : ([...seenMeasures][0] ?? 'total')
    exercises.push(previousExercise ? {...previousExercise,sets,removedSets} : {
      exerciseEntryKey:entry.exerciseEntryKey,identity:{source:'custom',exerciseName:entry.exerciseName},kind:'weight',
      ...(measure === 'total' ? {} : { measure }), sets, removedSets,
    })
  }
  // Whole-action deletion is not yet supported by this client. If added later,
  // the draft must retain its tombstone before cloud supersets can be adopted.
  return {...draft,exercises,completionRequested:remote.status === 'completed',
    server:{id:remote.id,version:remote.version,status:remote.status}}
}
