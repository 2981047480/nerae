import { restoreDraft, type DraftExercise, type DraftSet, type RecordingDraft } from './recording'
import { isIssuedRecordingV2Snapshot, type RecordingAuth, type ScopedRecordingV2Detail } from './recordingApi'
import type { RecordingV2Detail } from './recordingV2Projection'
import { draftFieldsFromMeasurement, inferMeasure, isV2Measure, type V2Measurement } from './recordingV2Measure'

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
    if (value.entries.some(entry => entry.source !== 'custom' || entry.removedPosition !== undefined)) return { ok: false, reason: 'unsupported' }
    const methodsByEntry = new Map<string, string[]>()
    for (const set of [...value.sets, ...value.removedSets.map(item => item.set)]) {
      if (!isV2Measure(set.measurement.method)) return { ok: false, reason: 'unsupported' }
      const list = methodsByEntry.get(set.exerciseEntryKey) || []
      list.push(set.measurement.method)
      methodsByEntry.set(set.exerciseEntryKey, list)
    }
    const exercises: DraftExercise[] = []
    for (const entry of value.entries) {
      const inferred = inferMeasure(methodsByEntry.get(entry.exerciseEntryKey) || [])
      if (inferred === null || inferred === 'mixed') return { ok: false, reason: 'unsupported' }
      exercises.push({ exerciseEntryKey: entry.exerciseEntryKey,
        identity: { source: 'custom', exerciseName: entry.exerciseName }, kind: 'weight',
        ...(inferred === 'total' ? {} : { measure: inferred }), sets: [], removedSets: [] })
    }
    const byKey = new Map(exercises.map(item => [item.exerciseEntryKey, item]))
    const mapSet = (set: V2Set, persisted: boolean): DraftSet | null => {
      const fields = draftFieldsFromMeasurement(set.measurement as V2Measurement)
      if (!fields) return null
      return { recordKey: set.recordKey, setIndex: set.setIndex, weightKg: '', reps: '', seconds: '', rpe: set.rpe ?? '',
        confirmed: set.confirmed, persisted, dirty: false, ...fields }
    }
    for (const set of value.sets) {
      const exercise = byKey.get(set.exerciseEntryKey)
      if (!exercise) return { ok: false, reason: 'invalid_snapshot' }
      const mapped = mapSet(set, true)
      if (!mapped) return { ok: false, reason: 'unsupported' }
      exercise.sets.push(mapped)
    }
    for (const removed of value.removedSets) {
      const exercise = byKey.get(removed.set.exerciseEntryKey)
      if (!exercise) return { ok: false, reason: 'invalid_snapshot' }
      // Preserve server acknowledgement and the removed snapshot for later recovery.
      const mapped = mapSet(removed.set, true)
      if (!mapped) return { ok: false, reason: 'unsupported' }
      exercise.removedSets!.push({ set: mapped, removedPosition: removed.removedPosition })
    }
    const draft = restoreDraft({ clientEventId: value.clientEventId, date: value.date, timeZone: value.timeZone,
      mode: value.mode, recordFormat: 2, syncTracking: 1, completionRequested: value.status === 'completed',
      server: { id: value.id, version: value.version, status: value.status }, finish: {}, exercises }, today)
    if (!auth.isCurrent(snapshot.lease.subjectId, snapshot.lease.generation)) return { ok: false, reason: 'session_changed' }
    return { ok: true, draft }
  } catch { return { ok: false, reason: 'invalid_snapshot' } }
}
