import { restoreDraft, type DraftExercise, type DraftSet, type RecordingDraft } from './recording'
import { isIssuedRecordingV2Snapshot, type RecordingAuth, type ScopedRecordingV2Detail } from './recordingApi'
import type { RecordingV2Detail } from './recordingV2Projection'

type V2Set = RecordingV2Detail['sets'][number]
export type RecordingV2RestoreResult =
  | { ok: true; draft: RecordingDraft }
  | { ok: false; reason: 'invalid_snapshot' | 'session_changed' | 'unsupported' }

/** Project an issued, authenticated snapshot without discarding unsupported data. */
export function projectRecordingV2ToDraft(snapshot: ScopedRecordingV2Detail, auth: RecordingAuth, today: string): RecordingV2RestoreResult {
  if (!isIssuedRecordingV2Snapshot(snapshot)) return { ok: false, reason: 'invalid_snapshot' }
  try {
    const credential = auth.current()
    if (!credential || credential.subjectId !== snapshot.lease.subjectId || credential.generation !== snapshot.lease.generation ||
        !auth.isCurrent(snapshot.lease.subjectId, snapshot.lease.generation)) return { ok: false, reason: 'session_changed' }
    const value = snapshot.detail
    if (value.entries.some(entry => entry.source !== 'custom' || entry.removedPosition !== undefined) ||
        [...value.sets, ...value.removedSets.map(item => item.set)].some(set => set.measurement.method !== 'total')) {
      return { ok: false, reason: 'unsupported' }
    }
    const exercises: DraftExercise[] = value.entries.map(entry => ({ exerciseEntryKey: entry.exerciseEntryKey,
      identity: { source: 'custom', exerciseName: entry.exerciseName }, kind: 'weight', sets: [], removedSets: [] }))
    const byKey = new Map(exercises.map(item => [item.exerciseEntryKey, item]))
    const mapSet = (set: V2Set, persisted: boolean): DraftSet => ({ recordKey: set.recordKey, setIndex: set.setIndex,
      weightKg: set.measurement.weightKg ?? '', reps: set.measurement.reps === undefined ? '' : String(set.measurement.reps),
      seconds: '', rpe: set.rpe ?? '', confirmed: set.confirmed, persisted, dirty: false })
    for (const set of value.sets) {
      const exercise = byKey.get(set.exerciseEntryKey)
      if (!exercise) return { ok: false, reason: 'invalid_snapshot' }
      exercise.sets.push(mapSet(set, true))
    }
    for (const removed of value.removedSets) {
      const exercise = byKey.get(removed.set.exerciseEntryKey)
      if (!exercise) return { ok: false, reason: 'invalid_snapshot' }
      // Preserve server acknowledgement and the removed snapshot for later recovery.
      exercise.removedSets!.push({ set: mapSet(removed.set, true), removedPosition: removed.removedPosition })
    }
    const draft = restoreDraft({ clientEventId: value.clientEventId, date: value.date, timeZone: value.timeZone,
      mode: value.mode, recordFormat: 2, syncTracking: 1, completionRequested: value.status === 'completed',
      server: { id: value.id, version: value.version, status: value.status }, finish: {}, exercises }, today)
    if (!auth.isCurrent(snapshot.lease.subjectId, snapshot.lease.generation)) return { ok: false, reason: 'session_changed' }
    return { ok: true, draft }
  } catch { return { ok: false, reason: 'invalid_snapshot' } }
}
