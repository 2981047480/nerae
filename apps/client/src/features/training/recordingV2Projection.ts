import type { components } from '../../../../../packages/contracts/src/generated-types'
import type { DraftExercise, DraftSet, RecordingDraft } from './recording'
import { exerciseMeasure, measurementFromSet, measurementsEqual, type V2Measurement } from './recordingV2Measure'
import { validRecordingV2Set } from './recordingV2Values'

export type RecordingV2Detail = components['schemas']['RecordingV2Detail']
export type RecordingV2Batch = components['schemas']['RecordingV2Batch']
type V2Set = components['schemas']['RecordingV2Set']
type V2Removal = components['schemas']['RecordingV2Removal']

export type RecordingV2Preparation =
  | { ok: true; batch: RecordingV2Batch; changed: boolean }
  | { ok: false; reason: 'version_conflict' | 'reconcile_required' | 'unsupported' | 'invalid_input' }

const failure = (reason: 'version_conflict' | 'reconcile_required' | 'unsupported' | 'invalid_input'): RecordingV2Preparation => ({ ok: false, reason })
const validKey = (key: string): boolean => typeof key === 'string' && Array.from(key).length >= 8 && Array.from(key).length <= 64 && Boolean(key.trim())

function group(exercise: DraftExercise, set: DraftSet): V2Set | null {
  if (!set || !validKey(set.recordKey) || !Number.isSafeInteger(set.setIndex) || set.setIndex < 1 ||
      typeof set.confirmed !== 'boolean' || !validRecordingV2Set(exercise, set)) return null
  const measurement = measurementFromSet(exercise, set)
  if (!measurement) return null
  const rpe = set.rpe.trim()
  return {
    recordKey: set.recordKey, exerciseEntryKey: exercise.exerciseEntryKey, setIndex: set.setIndex, confirmed: set.confirmed,
    ...(rpe ? { rpe } : {}),
    measurement,
  }
}

function sameSet(left: V2Set, right: V2Set): boolean {
  return left.recordKey === right.recordKey && left.exerciseEntryKey === right.exerciseEntryKey && left.setIndex === right.setIndex &&
    left.confirmed === right.confirmed && left.rpe === right.rpe &&
    measurementsEqual(left.measurement as V2Measurement, right.measurement as V2Measurement)
}

/**
 * Project the current protected draft against a freshly read server version.
 * This pure function does not authenticate, persist an intent, dispatch a
 * request, or settle a receipt. The caller must do those operations first.
 */
export function prepareRecordingV2Batch(draft: RecordingDraft, remote: RecordingV2Detail): RecordingV2Preparation {
  if (!draft || !remote || !draft.server || draft.server.id !== remote.id || draft.clientEventId !== remote.clientEventId ||
      !Number.isSafeInteger(draft.server.version) || !Number.isSafeInteger(remote.version)) return failure('reconcile_required')
  if (draft.server.version !== remote.version) return failure('version_conflict')
  if (draft.timeZone !== remote.timeZone || remote.source !== 'free') return failure('unsupported')
  if (!Array.isArray(draft.exercises) || !Array.isArray(remote.entries) || !Array.isArray(remote.sets) || !Array.isArray(remote.removedSets)) return failure('reconcile_required')
  if (draft.mode !== 'after_workout' && draft.mode !== 'per_set') return failure('invalid_input')
  const remoteEntries = new Map<string, typeof remote.entries[number]>()
  for (const entry of remote.entries) {
    if (!entry || typeof entry !== 'object' || !validKey(entry.exerciseEntryKey) || remoteEntries.has(entry.exerciseEntryKey) || entry.source !== 'custom' || typeof entry.exerciseName !== 'string') return failure('reconcile_required')
    if (entry.removedPosition !== undefined) return failure('unsupported')
    remoteEntries.set(entry.exerciseEntryKey, entry)
  }
  const remoteActive = new Map<string, V2Set>()
  const remoteRemoved = new Map<string, { set: V2Set; removedPosition: number }>()
  for (const set of remote.sets) {
    if (!set || typeof set !== 'object' || !set.measurement || typeof set.measurement !== 'object' || !validKey(set.recordKey) || remoteActive.has(set.recordKey) || !remoteEntries.has(set.exerciseEntryKey)) return failure('reconcile_required')
    remoteActive.set(set.recordKey, set)
  }
  for (const item of remote.removedSets) {
    if (!item || !item.set || !item.set.measurement || typeof item.set.measurement !== 'object' || !validKey(item.set.recordKey) || remoteActive.has(item.set.recordKey) || remoteRemoved.has(item.set.recordKey) ||
        !remoteEntries.has(item.set.exerciseEntryKey) || !Number.isSafeInteger(item.removedPosition) || item.removedPosition < 1) return failure('reconcile_required')
    remoteRemoved.set(item.set.recordKey, item)
  }
  const addEntries: NonNullable<RecordingV2Batch['addEntries']> = []
  const sets: V2Set[] = []
  const removeRecords: V2Removal[] = []
  const restoreRecordKeys: string[] = []
  const order: string[] = []
  const seenEntries = new Set<string>()
  const seenGroups = new Set<string>()
  for (const exercise of draft.exercises) {
    if (!exercise || !validKey(exercise.exerciseEntryKey) || seenEntries.has(exercise.exerciseEntryKey)) return failure('invalid_input')
    if (!exercise.identity || exercise.identity.source !== 'custom' || exercise.kind !== 'weight') return failure('unsupported')
    const name = exercise.identity.exerciseName
    if (typeof name !== 'string' || !name.trim() || Array.from(name).length > 60) return failure('invalid_input')
    if (!Array.isArray(exercise.sets) || (exercise.removedSets !== undefined && !Array.isArray(exercise.removedSets))) return failure('invalid_input')
    const existing = remoteEntries.get(exercise.exerciseEntryKey)
    if (existing && existing.exerciseName !== name) return failure('reconcile_required')
    if (!existing) addEntries.push({ exerciseEntryKey: exercise.exerciseEntryKey, exerciseName: name })
    seenEntries.add(exercise.exerciseEntryKey)
    order.push(exercise.exerciseEntryKey)
    const indexes = new Set<number>()
    const consume = (set: DraftSet, removedPosition?: number): RecordingV2Preparation | null => {
      const mapped = group(exercise, set)
      if (!mapped || seenGroups.has(set.recordKey) || indexes.has(set.setIndex)) return failure('invalid_input')
      indexes.add(set.setIndex); seenGroups.add(set.recordKey)
      const live = remoteActive.get(set.recordKey)
      const tombstone = remoteRemoved.get(set.recordKey)
      const previous = live || tombstone?.set
      if (previous && (previous.exerciseEntryKey !== exercise.exerciseEntryKey || previous.setIndex !== set.setIndex)) return failure('reconcile_required')
      if (previous && previous.measurement.method !== exerciseMeasure(exercise)) {
        if (!set.dirty) return failure('reconcile_required')
      }
      if (removedPosition === undefined) {
        if (tombstone) restoreRecordKeys.push(set.recordKey)
        if (!previous || !sameSet(mapped, previous)) sets.push(mapped)
      } else {
        if (!Number.isSafeInteger(removedPosition) || removedPosition < 1) return failure('invalid_input')
        if (tombstone && (!sameSet(mapped, tombstone.set) || tombstone.removedPosition !== removedPosition)) {
          restoreRecordKeys.push(set.recordKey)
          sets.push(mapped)
          removeRecords.push({ recordKey: set.recordKey, removedPosition })
        } else if (!tombstone) {
          if (!previous || !sameSet(mapped, previous)) sets.push(mapped)
          removeRecords.push({ recordKey: set.recordKey, removedPosition })
        }
      }
      return null
    }
    for (const set of exercise.sets) { const error = consume(set); if (error) return error }
    for (const item of exercise.removedSets || []) { const error = consume(item.set, item.removedPosition); if (error) return error }
  }
  if ([...remoteEntries.keys()].some(key => !seenEntries.has(key)) ||
      [...remoteActive.keys(), ...remoteRemoved.keys()].some(key => !seenGroups.has(key))) return failure('reconcile_required')
  if (sets.length + removeRecords.length + restoreRecordKeys.length > 200 || order.length > 2000) return failure('unsupported')
  const batch: RecordingV2Batch = { expectedVersion: remote.version, exerciseEntryKeys: order,
    ...(draft.date !== remote.date ? { date: draft.date } : {}), ...(draft.mode !== remote.mode ? { mode: draft.mode } : {}), ...(addEntries.length ? { addEntries } : {}),
    ...(sets.length ? { sets } : {}), ...(removeRecords.length ? { removeRecords } : {}),
    ...(restoreRecordKeys.length ? { restoreRecordKeys } : {}) }
  const changed = Boolean(batch.date || batch.mode || addEntries.length || sets.length || removeRecords.length || restoreRecordKeys.length ||
    order.length !== remote.entries.length || order.some((key, index) => key !== remote.entries[index].exerciseEntryKey))
  return { ok: true, batch, changed }
}

/** Preserve local edits while advancing only to a separately verified server snapshot. */
export function rebaseAcknowledgedRecordingV2(draft: RecordingDraft, remote: RecordingV2Detail): RecordingDraft | null {
  if (draft.recordFormat !== 2 || draft.clientEventId !== remote.clientEventId ||
      draft.timeZone !== remote.timeZone || remote.source !== 'free') return null
  const entries = new Map(remote.entries.map(entry => [entry.exerciseEntryKey, entry]))
  if (remote.entries.some(entry => entry.source !== 'custom' || entry.removedPosition !== undefined)) return null
  const active = new Map(remote.sets.map(set => [set.recordKey, set]))
  const removed = new Map(remote.removedSets.map(item => [item.set.recordKey, item.set]))
  const seenEntries = new Set<string>(); const seenGroups = new Set<string>()
  const exercises: DraftExercise[] = []
  for (const exercise of draft.exercises) {
    if (exercise.identity.source !== 'custom' || exercise.kind !== 'weight') return null
    const entry = entries.get(exercise.exerciseEntryKey)
    if (entry && entry.exerciseName !== exercise.identity.exerciseName) return null
    seenEntries.add(exercise.exerciseEntryKey)
    const mapGroup = (set: DraftSet, isRemoved: boolean): DraftSet | null => {
      if (seenGroups.has(set.recordKey)) return null
      seenGroups.add(set.recordKey)
      const server = active.get(set.recordKey) || removed.get(set.recordKey)
      if (!server) return set.persisted ? null : { ...set }
      if (server.exerciseEntryKey !== exercise.exerciseEntryKey || server.setIndex !== set.setIndex || server.measurement.method !== exerciseMeasure(exercise)) return null
      const mapped = group(exercise, set)
      const sameLocation = isRemoved ? removed.has(set.recordKey) : active.has(set.recordKey)
      return { ...set, persisted: true, dirty: !(sameLocation && mapped && sameSet(mapped, server)) }
    }
    const sets: DraftSet[] = []
    for (const set of exercise.sets) { const next = mapGroup(set, false); if (!next) return null; sets.push(next) }
    const removedSets: NonNullable<DraftExercise['removedSets']> = []
    for (const item of exercise.removedSets || []) {
      const next = mapGroup(item.set, true); if (!next) return null
      removedSets.push({ ...item, set: next })
    }
    exercises.push({ ...exercise, sets, ...(exercise.removedSets === undefined ? {} : { removedSets }) })
  }
  if ([...entries.keys()].some(key => !seenEntries.has(key)) ||
      [...active.keys(), ...removed.keys()].some(key => !seenGroups.has(key))) return null
  return { ...draft, exercises, server: { id: remote.id, version: remote.version, status: remote.status } }
}
