import type { DraftExercise, DraftSet, RecordingDraft } from './recording'
import { prepareRecordingV2Batch, rebaseAcknowledgedRecordingV2, type RecordingV2Detail } from './recordingV2Projection'
import { validRecordingV2Set } from './recordingV2Values'

export type ConflictChoice = 'local' | 'cloud'
export interface RecordingConflictRow { key: string; label: string; local: string; cloud: string }
interface Pair { local: DraftSet; cloud: RecordingV2Detail['sets'][number] }
interface Plan { rows: RecordingConflictRow[]; sets: Map<string, Pair>; remoteCompletion: boolean; draft: RecordingDraft }

function values(set: DraftSet): string {
  return JSON.stringify([set.weightKg.trim(), set.reps.trim(), set.rpe.trim(), set.confirmed])
}
function cloudSet(set: RecordingV2Detail['sets'][number]): DraftSet | null {
  if (set.measurement.method !== 'total' || set.measurement.bodyweightKg !== undefined ||
      set.measurement.adjustmentKg !== undefined || set.measurement.left !== undefined || set.measurement.right !== undefined) return null
  return { recordKey: set.recordKey, setIndex: set.setIndex, weightKg: set.measurement.weightKg ?? '',
    reps: set.measurement.reps === undefined ? '' : String(set.measurement.reps), seconds: '', rpe: set.rpe ?? '',
    confirmed: set.confirmed, persisted: true, dirty: false }
}
function describe(set: DraftSet, showRpe: boolean, showConfirmation: boolean): string {
  const weight = set.weightKg.trim() ? `${set.weightKg.trim()} kg` : '重量未填'
  const reps = set.reps.trim() ? `${set.reps.trim()} 次` : '次数未填'
  const rpe = showRpe ? (set.rpe.trim() ? ` · 主观强度 ${set.rpe.trim()}` : ' · 主观强度未填') : ''
  const confirmed = showConfirmation ? ` · ${set.confirmed ? '已单独记下' : '未单独记下'}` : ''
  return `${weight} × ${reps}${rpe}${confirmed}`
}
function sameKeys(local: DraftSet[], remote: RecordingV2Detail['sets']): boolean {
  return local.length === remote.length && local.every((item, index) =>
    item.recordKey === remote[index].recordKey && item.setIndex === remote[index].setIndex)
}

/** Include remote-only additions for review without changing any local values.
 * Local additions/removals/reordering still require a separate recovery path. */
function includeCloudAdditions(draft: RecordingDraft, remote: RecordingV2Detail): RecordingDraft | null {
  if (draft.syncTracking !== 1 || remote.entries.some(entry => entry.source !== 'custom' || entry.removedPosition !== undefined)) return null
  const remoteEntries = new Map(remote.entries.map((entry,index) => [entry.exerciseEntryKey, {entry,index}]))
  if (remoteEntries.size !== remote.entries.length) return null
  const remoteActive = new Map(remote.sets.map(set => [set.recordKey,set]))
  const remoteRemoved = new Map(remote.removedSets.map(item => [item.set.recordKey,item]))
  if (remoteActive.size !== remote.sets.length || remoteRemoved.size !== remote.removedSets.length ||
      [...remoteActive.keys()].some(key => remoteRemoved.has(key))) return null
  const activeByEntry = new Map<string,RecordingV2Detail['sets']>()
  for (const set of remote.sets) {
    if (!remoteEntries.has(set.exerciseEntryKey)) return null
    const sets = activeByEntry.get(set.exerciseEntryKey) || []
    sets.push(set); activeByEntry.set(set.exerciseEntryKey,sets)
  }
  const localEntries = new Map<string,DraftExercise>(); const localKeys = new Set<string>()
  let last = -1
  for (const exercise of draft.exercises) {
    const found = remoteEntries.get(exercise.exerciseEntryKey)
    if (!found || found.index <= last || localEntries.has(exercise.exerciseEntryKey) || exercise.identity.source !== 'custom' ||
        exercise.kind !== 'weight' || found.entry.exerciseName !== exercise.identity.exerciseName) return null
    last = found.index; localEntries.set(exercise.exerciseEntryKey,exercise)
    let index = 0
    for (const set of exercise.sets) {
      const cloud = remoteActive.get(set.recordKey)
      if (!set.persisted || localKeys.has(set.recordKey) || set.setIndex <= index || !cloud ||
          cloud.exerciseEntryKey !== exercise.exerciseEntryKey || cloud.setIndex !== set.setIndex) return null
      index = set.setIndex; localKeys.add(set.recordKey)
    }
    for (const item of exercise.removedSets || []) {
      const cloud = remoteRemoved.get(item.set.recordKey)
      if (!item.set.persisted || localKeys.has(item.set.recordKey) || !cloud ||
          cloud.set.exerciseEntryKey !== exercise.exerciseEntryKey || cloud.set.setIndex !== item.set.setIndex ||
          cloud.removedPosition !== item.removedPosition) return null
      localKeys.add(item.set.recordKey)
    }
  }
  // A new cloud tombstone is not an addition of visible content; keep the
  // existing fail-closed behavior until deletion conflicts are designed.
  if ([...remoteRemoved.keys()].some(key => !localKeys.has(key))) return null
  const exercises: DraftExercise[] = []
  for (const entry of remote.entries) {
    const existing = localEntries.get(entry.exerciseEntryKey)
    const additions: DraftSet[] = []
    for (const set of activeByEntry.get(entry.exerciseEntryKey) || []) {
      if (localKeys.has(set.recordKey)) continue
      const value = cloudSet(set)
      if (!value) return null
      additions.push(value)
    }
    const sets = [...(existing?.sets || []),...additions].sort((a,b) => a.setIndex - b.setIndex)
    if (new Set(sets.map(set => set.setIndex)).size !== sets.length) return null
    exercises.push(existing ? {...existing,sets} : {
      exerciseEntryKey:entry.exerciseEntryKey,identity:{source:'custom',exerciseName:entry.exerciseName},kind:'weight',sets,removedSets:[],
    })
  }
  return {...draft,exercises}
}

/** Compare equal structures or a safe cloud-only superset. Pending writes remain blocked. */
function conflictPlan(draft: RecordingDraft, remote: RecordingV2Detail): Plan | null {
  const localCompleted = Boolean(draft.completionRequested)
  const remoteCompleted = remote.status === 'completed'
  const remoteCompletion = !localCompleted && remoteCompleted && draft.server?.status === 'in_progress'
  if (draft.recordFormat !== 2 || draft.syncPending !== undefined || !draft.server ||
      draft.server.id !== remote.id || draft.clientEventId !== remote.clientEventId ||
      draft.timeZone !== remote.timeZone || remote.source !== 'free' || remote.version <= draft.server.version ||
      (localCompleted !== remoteCompleted && !remoteCompletion)) return null
  let working = draft
  const activeCounts = new Map<string,number>()
  for (const set of remote.sets) activeCounts.set(set.exerciseEntryKey,(activeCounts.get(set.exerciseEntryKey) || 0) + 1)
  const needsExpansion = draft.exercises.length !== remote.entries.length || draft.exercises.some(exercise =>
    exercise.sets.length !== (activeCounts.get(exercise.exerciseEntryKey) || 0))
  if (needsExpansion) {
    const expanded = includeCloudAdditions(draft,remote)
    if (!expanded) return null
    working = expanded
  }
  const rows: RecordingConflictRow[] = []
  const sets = new Map<string, Pair>()
  if (draft.date !== remote.date) rows.push({ key:'date', label:'训练日期', local:draft.date, cloud:remote.date })
  if (draft.mode !== remote.mode) rows.push({ key:'mode', label:'记录方式',
    local:draft.mode === 'after_workout' ? '训练后记录' : '每组记录',
    cloud:remote.mode === 'after_workout' ? '训练后记录' : '每组记录' })
  const seen = new Set<string>()
  const nameCounts = new Map<string, number>(); const nameOccurrences = new Map<string, number>()
  const activeByEntry = new Map<string, RecordingV2Detail['sets']>()
  const removedByEntry = new Map<string, RecordingV2Detail['removedSets']>()
  for (const exercise of working.exercises) if (exercise.identity.source === 'custom') {
    const name = exercise.identity.exerciseName; nameCounts.set(name, (nameCounts.get(name) || 0) + 1)
  }
  for (const set of remote.sets) {
    const list = activeByEntry.get(set.exerciseEntryKey) || []; list.push(set); activeByEntry.set(set.exerciseEntryKey, list)
  }
  for (const item of remote.removedSets) {
    const key = item.set.exerciseEntryKey; const list = removedByEntry.get(key) || []; list.push(item); removedByEntry.set(key, list)
  }
  for (let i = 0; i < working.exercises.length; i++) {
    const exercise: DraftExercise = working.exercises[i]; const entry = remote.entries[i]
    if (exercise.identity.source !== 'custom' || exercise.kind !== 'weight' || entry.source !== 'custom' ||
        entry.removedPosition !== undefined || exercise.exerciseEntryKey !== entry.exerciseEntryKey ||
        exercise.identity.exerciseName !== entry.exerciseName) return null
    const active = (activeByEntry.get(entry.exerciseEntryKey) || []).sort((a,b) => a.setIndex - b.setIndex)
    if (!sameKeys(exercise.sets, active)) return null
    const occurrence = (nameOccurrences.get(entry.exerciseName) || 0) + 1
    nameOccurrences.set(entry.exerciseName, occurrence)
    const segment = (nameCounts.get(entry.exerciseName) || 0) > 1 ? ` · 第 ${occurrence} 段` : ''
    for (let j = 0; j < active.length; j++) {
      const local = exercise.sets[j]; const cloud = active[j]; const projected = cloudSet(cloud)
      if (!projected || !validRecordingV2Set(exercise, local) || seen.has(local.recordKey)) return null
      seen.add(local.recordKey)
      const key = `set:${local.recordKey}`; sets.set(key, { local, cloud })
      const showRpe = local.rpe.trim() !== projected.rpe.trim()
      const showConfirmation = local.confirmed !== projected.confirmed
      if (values(local) !== values(projected)) rows.push({ key, label:`${entry.exerciseName}${segment} · 第 ${j + 1} 组`,
        local:describe(local, showRpe, showConfirmation), cloud:describe(projected, showRpe, showConfirmation) })
    }
    const removed = removedByEntry.get(entry.exerciseEntryKey) || []
    const removedByKey = new Map(removed.map(item => [item.set.recordKey, item]))
    if ((exercise.removedSets?.length || 0) !== removed.length) return null
    for (const item of exercise.removedSets || []) {
      const other = removedByKey.get(item.set.recordKey)
      const projected = other && cloudSet(other.set)
      if (!other || !projected || seen.has(item.set.recordKey) || item.set.setIndex !== other.set.setIndex ||
          item.removedPosition !== other.removedPosition || values(item.set) !== values(projected)) return null
      seen.add(item.set.recordKey)
    }
  }
  if (seen.size !== remote.sets.length + remote.removedSets.length) return null
  return { rows, sets, remoteCompletion, draft:working }
}

export function recordingV2ConflictRows(draft: RecordingDraft, remote: RecordingV2Detail): RecordingConflictRow[] | null {
  const plan = conflictPlan(draft, remote)
  if (!plan || (plan.remoteCompletion && !canAdoptPlan(draft, plan))) return null
  return plan.rows
}

function canAdoptPlan(draft: RecordingDraft, plan: Plan): boolean {
  if (plan.rows.length === 0) return true
  if (draft.syncTracking !== 1) return false
  if (plan.rows.some(row => row.key === 'date' || row.key === 'mode')) return false
  for (const pair of plan.sets.values()) {
    // Confirmation is also checked explicitly so that an older or inconsistent
    // draft cannot silently lose its local confirmation state.
    if (!pair.local.persisted || pair.local.dirty || pair.local.confirmed !== pair.cloud.confirmed) return false
  }
  return draft.exercises.every(exercise => (exercise.removedSets || []).every(item => item.set.persisted && !item.set.dirty))
}

/** Only pull changed group values when every local group is acknowledged and untouched. */
export function canAdoptCloudRecordingV2(draft: RecordingDraft, remote: RecordingV2Detail): boolean {
  const plan = conflictPlan(draft, remote)
  return Boolean(plan && canAdoptPlan(draft, plan))
}

/** Explicit choices are projected against the reviewed server version, never sent here. */
export function applyRecordingV2ConflictChoices(draft: RecordingDraft, remote: RecordingV2Detail,
  choices: Record<string, ConflictChoice>): RecordingDraft | null {
  const plan = conflictPlan(draft, remote)
  const rowKeys = new Set(plan?.rows.map(row => row.key))
  if (!plan || !choices || typeof choices !== 'object' || Array.isArray(choices) ||
      (plan.remoteCompletion && (!canAdoptPlan(draft, plan) || plan.rows.some(row => choices[row.key] !== 'cloud'))) ||
      Object.keys(choices).length !== plan.rows.length ||
      plan.rows.some(row => choices[row.key] !== 'local' && choices[row.key] !== 'cloud') ||
      Object.keys(choices).some(key => !rowKeys.has(key))) return null
  const exercises = plan.draft.exercises.map(exercise => ({ ...exercise, sets: exercise.sets.map(set => {
    const key = `set:${set.recordKey}`; const pair = plan.sets.get(key)
    if (choices[key] !== 'cloud' || !pair) return set
    const selected = cloudSet(pair.cloud)
    return selected ? { ...set, weightKg:selected.weightKg, reps:selected.reps, rpe:selected.rpe, confirmed:selected.confirmed } : set
  }) }))
  const chosen: RecordingDraft = { ...draft, exercises, ...(plan.remoteCompletion ? {completionRequested:true} : {}),
    date: choices.date === 'cloud' ? remote.date : draft.date,
    mode: choices.mode === 'cloud' ? remote.mode : draft.mode }
  const rebased = rebaseAcknowledgedRecordingV2(chosen, remote)
  if (!rebased || !prepareRecordingV2Batch(rebased, remote).ok) return null
  return rebased
}
