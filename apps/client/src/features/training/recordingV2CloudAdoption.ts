import type { DraftExercise, DraftSet, RecordingDraft } from './recording'
import type { RecordingV2Detail } from './recordingV2Projection'
import { validRecordingV2Set } from './recordingV2Values'

type RemoteSet = RecordingV2Detail['sets'][number]
function projectSet(set: RemoteSet): DraftSet | null {
  const value = set.measurement
  if (value.method !== 'total' || value.bodyweightKg !== undefined || value.adjustmentKg !== undefined ||
      value.left !== undefined || value.right !== undefined) return null
  return {recordKey:set.recordKey,setIndex:set.setIndex,weightKg:value.weightKg ?? '',
    reps:value.reps === undefined ? '' : String(value.reps),seconds:'',rpe:set.rpe ?? '',
    confirmed:set.confirmed,persisted:true,dirty:false}
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
      if (!cloud || cloud.exerciseEntryKey !== exercise.exerciseEntryKey || cloud.setIndex !== set.setIndex ||
          !set.persisted || set.dirty || set.confirmed !== cloud.confirmed ||
          !validRecordingV2Set(exercise,set) || !projectSet(cloud)) return null
    }
    for (const item of exercise.removedSets || []) {
      const cloud = removed.get(item.set.recordKey)
      const projected = cloud && projectSet(cloud.set)
      if (!cloud || cloud.set.exerciseEntryKey !== exercise.exerciseEntryKey || cloud.set.setIndex !== item.set.setIndex ||
          cloud.removedPosition !== item.removedPosition || !item.set.persisted || item.set.dirty ||
          item.set.confirmed !== cloud.set.confirmed || !validRecordingV2Set(exercise,item.set) || !projected ||
          item.set.weightKg.trim() !== projected.weightKg.trim() || item.set.reps.trim() !== projected.reps.trim() ||
          item.set.rpe.trim() !== projected.rpe.trim()) return null
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
    for (const set of (activeByEntry.get(entry.exerciseEntryKey) || []).sort((a,b)=>a.setIndex-b.setIndex)) {
      const projected=projectSet(set);if (!projected) return null;sets.push(projected)
    }
    for (const item of (removedByEntry.get(entry.exerciseEntryKey) || []).sort((a,b)=>a.set.setIndex-b.set.setIndex)) {
      const projected=projectSet(item.set);if (!projected) return null
      removedSets.push({set:projected,removedPosition:item.removedPosition})
    }
    exercises.push(previousExercise ? {...previousExercise,sets,removedSets} : {
      exerciseEntryKey:entry.exerciseEntryKey,identity:{source:'custom',exerciseName:entry.exerciseName},kind:'weight',sets,removedSets,
    })
  }
  // Whole-action deletion is not yet supported by this client. If added later,
  // the draft must retain its tombstone before cloud supersets can be adopted.
  return {...draft,exercises,completionRequested:remote.status === 'completed',
    server:{id:remote.id,version:remote.version,status:remote.status}}
}
