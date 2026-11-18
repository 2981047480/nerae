import { validRecordingV2Set } from './recordingV2Values'
import {
  exerciseMeasure, isV2Measure, measureUsesBodyweight, measureUsesSharedWeight, measurementCarry,
  type SideState, type V2Measure, type V2SideName,
} from './recordingV2Measure'

export type { V2Measure } from './recordingV2Measure'

/** Shared editing rules for the uni-app training-record feature.
 * The caller owns authenticated storage and network submission. This module
 * never claims that an edited or locally confirmed set was saved remotely.
 */

export type RecordingMode = 'after_workout' | 'per_set'
export type SetKind = 'weight' | 'perSide' | 'bodyweight' | 'bodyweightPerSide' | 'duration' | 'durationPerSide' | 'breath'
export type InputField = 'weightKg' | 'reps' | 'seconds' | 'rpe' | 'leftWeightKg' | 'leftReps' | 'rightWeightKg' | 'rightReps' | 'bodyweightKg' | 'adjustmentKg'
export type BatchSetField = 'weightKg' | 'reps'

export type ExerciseIdentity =
  | { source: 'catalog'; exerciseId: string }
  | { source: 'custom'; exerciseName: string }

export interface DraftSet {
  recordKey: string
  /** Stable server order number; removing another draft row does not renumber it. */
  setIndex: number
  weightKg: string
  reps: string
  seconds: string
  rpe: string
  /** Confirmed by the user in this editor, not a server acknowledgement. */
  confirmed: boolean
  /** A server has acknowledged that this key exists, not necessarily this edit. */
  persisted: boolean
  /** Current values differ from the last acknowledged server values. */
  dirty: boolean
  /** Format-2 side and bodyweight fields. Absent means that part was not entered. */
  leftWeightKg?: string
  leftReps?: string
  leftState?: 'unknown' | 'performed' | 'not_performed'
  rightWeightKg?: string
  rightReps?: string
  rightState?: 'unknown' | 'performed' | 'not_performed'
  bodyweightKg?: string
  adjustmentKg?: string
}

/** The group and its display position at removal are kept in the protected draft. */
export interface RemovedDraftSet {
  set: DraftSet
  removedPosition: number
}

export interface DraftExercise {
  exerciseEntryKey: string
  identity: ExerciseIdentity
  /** Frozen server display name for catalog actions; absent in older drafts. */
  resolvedExerciseName?: string
  kind: SetKind
  /** Format-2 weight meaning. Absent means the typed kilograms are a combined total. */
  measure?: V2Measure
  sets: DraftSet[]
  removedSets?: RemovedDraftSet[]
}

export interface RecordingDraft {
  clientEventId: string
  date: string
  timeZone: string
  mode: RecordingMode
  /** V2 drafts are not readable by older clients. */
  recordFormat?: 2
  /** Present only after the native schema-4 client has verified a clean server baseline. */
  syncTracking?: 1
  /** User requested completion; it remains pending until the server confirms. */
  completionRequested?: boolean
  /** Added only after a real server response; version is needed for edits. */
  /** Status belongs to the confirmed server snapshot, never to local timing. */
  server?: { id: string; version: number; status?: 'in_progress' | 'completed' }
  /** Opaque durable intent; ordinary editing cannot create or modify it. */
  syncPending?: string
  finish: { durationMin?: number | null; rpe?: number | null; note?: string | null }
  exercises: DraftExercise[]
}

export interface SetWrite {
  exerciseId?: string
  exerciseName?: string
  exerciseEntryKey: string
  recordKey: string
  setIndex: number
  kind: SetKind
  weightKg?: number
  reps?: number
  seconds?: number
  rpe?: number
  completed: true
}

export interface GroupIssue {
  exerciseEntryKey: string
  recordKey: string
  message: string
}

export type SavePreparation =
  | { ok: true; sets: SetWrite[] }
  | { ok: false; issues: GroupIssue[]; message: string }

export type ConfirmedSetPreparation =
  | { ok: true; state: 'send'; set: SetWrite }
  | { ok: true; state: 'already_saved' }
  | { ok: false; message: string }

export type PerSetFinishPreparation =
  | { ok: true }
  | { ok: false; issues: GroupIssue[]; message: string }

function assertKey(key: string): void {
  if (typeof key !== 'string' || Array.from(key).length < 8 || Array.from(key).length > 64 || !key.trim()) {
    throw new Error('记录标识无效')
  }
}

function assertIdentity(identity: ExerciseIdentity): ExerciseIdentity {
  if (identity.source === 'catalog') {
    if (!/^[1-9]\d{0,19}$/.test(identity.exerciseId)) throw new Error('动作标识无效')
    return { source: 'catalog', exerciseId: identity.exerciseId }
  }
  if (identity.source !== 'custom' || typeof identity.exerciseName !== 'string') throw new Error('动作来源无效')
  const name = identity.exerciseName.trim()
  if (!name || Array.from(name).length > 60) throw new Error('动作名称应为 1 至 60 个字符')
  return { source: 'custom', exerciseName: name }
}

function findExercise(draft: RecordingDraft, key: string): DraftExercise {
  const exercise = draft.exercises.find(item => item.exerciseEntryKey === key)
  if (!exercise) throw new Error('动作不存在')
  return exercise
}

function findSet(exercise: DraftExercise, key: string): DraftSet {
  const set = exercise.sets.find(item => item.recordKey === key)
  if (!set) throw new Error('记录组不存在')
  return set
}

function emptySet(recordKey: string, setIndex: number): DraftSet {
  assertKey(recordKey)
  if (!Number.isSafeInteger(setIndex) || setIndex < 1) throw new Error('训练组序号无效')
  return { recordKey, setIndex, weightKg: '', reps: '', seconds: '', rpe: '', confirmed: false, persisted: false, dirty: false }
}

function sessionBodyweight(draft: RecordingDraft): string {
  for (const exercise of draft.exercises) {
    if (!measureUsesBodyweight(exerciseMeasure(exercise))) continue
    for (const set of [...exercise.sets, ...(exercise.removedSets || []).map(item => item.set)]) {
      const value = set.bodyweightKg?.trim()
      if (value) return set.bodyweightKg!
    }
  }
  return ''
}

function touchSet(set: DraftSet, field: InputField, value: string): DraftSet {
  const next: DraftSet = { ...set, [field]: value, confirmed: false, dirty: true }
  const side = field.startsWith('left') ? 'left' : field.startsWith('right') ? 'right' : null
  if (!side) return next
  const other = side === 'left' ? 'right' : 'left'
  const otherState = (other === 'left' ? next.leftState : next.rightState) ?? 'unknown'
  // The first side the user types is copied to the other side until that side is edited on its own.
  if (otherState === 'unknown') {
    if (field.endsWith('WeightKg')) {
      if (other === 'left') next.leftWeightKg = value
      else next.rightWeightKg = value
    } else if (other === 'left') next.leftReps = value
    else next.rightReps = value
  }
  const ownWeight = (side === 'left' ? next.leftWeightKg : next.rightWeightKg) ?? ''
  const ownReps = (side === 'left' ? next.leftReps : next.rightReps) ?? ''
  const ownState = ownWeight.trim() || ownReps.trim() ? 'performed' : 'unknown'
  if (side === 'left') {
    if (ownState === 'unknown') delete next.leftState
    else next.leftState = ownState
  } else if (ownState === 'unknown') delete next.rightState
  else next.rightState = ownState
  return next
}

function ensureUnusedSetKey(draft: RecordingDraft, recordKey: string): void {
  assertKey(recordKey)
  if (draft.exercises.some(exercise => exercise.sets.some(set => set.recordKey === recordKey) ||
    exercise.removedSets?.some(item => item.set.recordKey === recordKey))) {
    throw new Error('记录组标识已存在')
  }
}

function validDate(date: string): boolean {
  if (!/^\d{4}-\d{2}-\d{2}$/.test(date)) return false
  const parsed = new Date(`${date}T00:00:00.000Z`)
  return !Number.isNaN(parsed.getTime()) && parsed.toISOString().slice(0, 10) === date
}

/** The caller supplies today's date in the frozen timezone; no training time is inferred. */
export function createRecordingDraft(clientEventId: string, date: string, timeZone: string, today: string): RecordingDraft {
  assertKey(clientEventId)
  if (!validDate(date) || !validDate(today) || date > today) throw new Error('训练日期无效或晚于今天')
  if (!timeZone || timeZone.length > 64) throw new Error('训练时区无效')
  try { new Intl.DateTimeFormat('en', { timeZone }) }
  catch { throw new Error('训练时区无效') }
  return { clientEventId, date, timeZone, mode: 'after_workout', finish: {}, exercises: [] }
}

export function switchMode(draft: RecordingDraft, mode: RecordingMode): RecordingDraft {
  if (mode !== 'after_workout' && mode !== 'per_set') throw new Error('记录方式无效')
  return draft.mode === mode ? draft : { ...draft, mode }
}

export function addExercise(
  draft: RecordingDraft,
  exerciseEntryKey: string,
  identity: ExerciseIdentity,
  kind: SetKind,
  firstRecordKey: string,
): RecordingDraft {
  assertKey(exerciseEntryKey)
  ensureUnusedSetKey(draft, firstRecordKey)
  if (draft.exercises.some(item => item.exerciseEntryKey === exerciseEntryKey)) {
    throw new Error('动作条目标识已存在')
  }
  if (!['weight', 'perSide', 'bodyweight', 'bodyweightPerSide', 'duration', 'durationPerSide', 'breath'].includes(kind)) {
    throw new Error('动作计量方式无效')
  }
  return {
    ...draft,
    exercises: [...draft.exercises, {
      exerciseEntryKey,
      identity: assertIdentity(identity),
      kind,
      sets: [emptySet(firstRecordKey, 1)],
    }],
  }
}

export function addSet(draft: RecordingDraft, exerciseEntryKey: string, recordKey: string): RecordingDraft {
  const target = findExercise(draft, exerciseEntryKey)
  ensureUnusedSetKey(draft, recordKey)
  const maximum = [...target.sets, ...(target.removedSets || []).map(item => item.set)]
    .reduce((largest, set) => Math.max(largest, set.setIndex), 0)
  if (!Number.isSafeInteger(maximum) || maximum >= Number.MAX_SAFE_INTEGER) throw new Error('训练组序号不可用')
  const created = emptySet(recordKey, maximum + 1)
  if (draft.recordFormat === 2 && measureUsesBodyweight(exerciseMeasure(target))) {
    const known = sessionBodyweight(draft)
    if (known) created.bodyweightKg = known
  }
  const changed = { ...target, sets: [...target.sets, created] }
  return { ...draft, exercises: draft.exercises.map(item => item === target ? changed : item) }
}

/** Copy input into a new final group; it has never been confirmed or uploaded. */
export function copySet(draft: RecordingDraft, exerciseEntryKey: string, sourceKey: string, recordKey: string): RecordingDraft {
  const source = findSet(findExercise(draft, exerciseEntryKey), sourceKey)
  const added = addSet(draft, exerciseEntryKey, recordKey)
  const target = findExercise(added, exerciseEntryKey)
  const carried = measurementCarry(source)
  const changed = { ...target, sets: target.sets.map(set => set.recordKey === recordKey ? {
    ...set, ...carried, weightKg: source.weightKg, reps: source.reps, seconds: source.seconds, rpe: source.rpe,
    confirmed: false, persisted: false,
    dirty: Boolean(source.weightKg || source.reps || source.seconds || source.rpe || Object.values(carried).some(Boolean)),
  } : set) }
  return { ...added, exercises: added.exercises.map(item => item === target ? changed : item) }
}

export function updateSet(
  draft: RecordingDraft,
  exerciseEntryKey: string,
  recordKey: string,
  field: InputField,
  value: string,
): RecordingDraft {
  const target = findExercise(draft, exerciseEntryKey)
  const set = findSet(target, recordKey)
  if (!['weightKg', 'reps', 'seconds', 'rpe', 'leftWeightKg', 'leftReps', 'rightWeightKg', 'rightReps', 'bodyweightKg', 'adjustmentKg'].includes(field) || typeof value !== 'string') {
    throw new Error('记录输入无效')
  }
  if (field === 'bodyweightKg' && draft.recordFormat === 2 && measureUsesBodyweight(exerciseMeasure(target))) {
    if ((set.bodyweightKg ?? '') === value && draft.exercises.every(exercise => !measureUsesBodyweight(exerciseMeasure(exercise)) ||
        [...exercise.sets, ...(exercise.removedSets || []).map(item => item.set)].every(item => (item.bodyweightKg ?? '') === value))) return draft
    const apply = (item: DraftSet): DraftSet => (item.bodyweightKg ?? '') === value ? item : { ...item, bodyweightKg: value, dirty: true }
    return {
      ...draft,
      exercises: draft.exercises.map(exercise => measureUsesBodyweight(exerciseMeasure(exercise)) ? {
        ...exercise,
        sets: exercise.sets.map(apply),
        ...(exercise.removedSets ? { removedSets: exercise.removedSets.map(item => ({ ...item, set: apply(item.set) })) } : {}),
      } : exercise),
    }
  }
  if (set[field] === value) return draft
  const updated = touchSet(set, field, value)
  const changed = { ...target, sets: target.sets.map(item => item === set ? updated : item) }
  return { ...draft, exercises: draft.exercises.map(item => item === target ? changed : item) }
}

/** Change how this exercise's kilograms are interpreted. Stored numbers stay; the meaning change is synced. */
export function setExerciseMeasure(draft: RecordingDraft, exerciseEntryKey: string, measure: V2Measure): RecordingDraft {
  if (draft.recordFormat !== 2) throw new Error('记录格式不支持此操作')
  if (!isV2Measure(measure)) throw new Error('重量含义无效')
  const target = findExercise(draft, exerciseEntryKey)
  if (target.kind !== 'weight') throw new Error('此动作不能改重量含义')
  if (exerciseMeasure(target) === measure) return draft
  const known = sessionBodyweight(draft)
  const apply = (set: DraftSet): DraftSet => {
    const next: DraftSet = { ...set, dirty: true }
    if (measureUsesBodyweight(measure) && !next.bodyweightKg?.trim() && known) next.bodyweightKg = known
    return next
  }
  const { measure: _previous, ...rest } = target
  const changed: DraftExercise = {
    ...rest,
    ...(measure === 'total' ? {} : { measure }),
    sets: target.sets.map(apply),
    ...(target.removedSets ? { removedSets: target.removedSets.map(item => ({ ...item, set: apply(item.set) })) } : {}),
  }
  return { ...draft, exercises: draft.exercises.map(item => item === target ? changed : item) }
}

/** Mark one side not performed, or return it to an empty entry. Old numbers are not restored. */
export function setSideState(draft: RecordingDraft, exerciseEntryKey: string, recordKey: string, side: V2SideName, state: SideState): RecordingDraft {
  if (draft.recordFormat !== 2) throw new Error('记录格式不支持此操作')
  if (side !== 'left' && side !== 'right') throw new Error('记录输入无效')
  if (state !== 'unknown' && state !== 'performed' && state !== 'not_performed') throw new Error('记录输入无效')
  const target = findExercise(draft, exerciseEntryKey)
  if (exerciseMeasure(target) !== 'sides') throw new Error('当前重量含义不能标记单侧')
  const set = findSet(target, recordKey)
  const key = side === 'left' ? 'leftState' : 'rightState'
  if ((set[key] ?? 'unknown') === state) return draft
  const updated: DraftSet = { ...set, [key]: state, confirmed: false, dirty: true }
  // 未做 and a return to 未填写 both drop that side's numbers. The wire never stores them for 未做.
  if (state === 'not_performed' || state === 'unknown') {
    if (side === 'left') { updated.leftWeightKg = ''; updated.leftReps = '' }
    else { updated.rightWeightKg = ''; updated.rightReps = '' }
  }
  if (state === 'unknown') delete updated[key]
  const changed = { ...target, sets: target.sets.map(item => item === set ? updated : item) }
  return { ...draft, exercises: draft.exercises.map(item => item === target ? changed : item) }
}

/** A previously uploaded set needs the server deletion workflow, not local removal. */
export function removeUnsubmittedSet(draft: RecordingDraft, exerciseEntryKey: string, recordKey: string): RecordingDraft {
  const target = findExercise(draft, exerciseEntryKey)
  const set = findSet(target, recordKey)
  if (set.persisted) throw new Error('已同步的组需要通过删除流程处理')
  const removedPosition = target.sets.findIndex(item => item === set) + 1
  const changed = { ...target, sets: target.sets.filter(item => item !== set),
    removedSets: [...(target.removedSets || []), { set: { ...set }, removedPosition }] }
  return { ...draft, exercises: draft.exercises.map(item => item === target ? changed : item) }
}

/** Remove a v2 group locally while retaining its identity and full values. */
export function removeRecordedSetV2(draft: RecordingDraft, exerciseEntryKey: string, recordKey: string): RecordingDraft {
  if (draft.recordFormat !== 2 || !draft.server) throw new Error('记录格式不支持此操作')
  const target = findExercise(draft, exerciseEntryKey)
  const set = findSet(target, recordKey)
  const removedPosition = target.sets.findIndex(item => item === set) + 1
  const changed = { ...target, sets: target.sets.filter(item => item !== set),
    removedSets: [...(target.removedSets || []), { set: { ...set, dirty:true }, removedPosition }] }
  const exercises = draft.exercises.map(item => item === target ? changed : item)
  return { ...draft, exercises, ...(exercises.every(item => item.sets.length === 0) ? { completionRequested: false } : {}) }
}

/** Restore an unsent group using its original stable order and untouched input. */
export function restoreUnsubmittedSet(draft: RecordingDraft, exerciseEntryKey: string, recordKey: string): RecordingDraft {
  const target = findExercise(draft, exerciseEntryKey)
  const removed = target.removedSets?.find(item => item.set.recordKey === recordKey)
  if (!removed) throw new Error('已移除的组不存在')
  if ((removed.set.persisted && draft.recordFormat !== 2) || target.sets.some(set => set.recordKey === recordKey || set.setIndex === removed.set.setIndex)) {
    throw new Error('训练组需要先核对后恢复')
  }
  const sets = [...target.sets, { ...removed.set,
    ...(draft.recordFormat === 2 && removed.set.persisted ? {dirty:true} : {}) }].sort((a, b) => a.setIndex - b.setIndex)
  const changed = { ...target, sets, removedSets: target.removedSets!.filter(item => item !== removed) }
  return { ...draft, exercises: draft.exercises.map(item => item === target ? changed : item) }
}
/** A batch selection is invalidated if the groups the user saw have changed. */
export function batchSetSnapshot(exercise: DraftExercise): string {
  return JSON.stringify([exercise.exerciseEntryKey, exercise.identity, exercise.kind, exercise.measure ?? 'total',
    exercise.sets.map(set => [set.recordKey, set.setIndex, set.weightKg, set.reps, set.confirmed])])
}

/** Blank fields are not applied implicitly; the chosen values must be valid. */
export function validBatchSetFields(exercise: DraftExercise, source: DraftSet, fields: BatchSetField[]): boolean {
  return exercise.kind === 'weight' && measureUsesSharedWeight(exerciseMeasure(exercise)) && fields.length > 0 && new Set(fields).size === fields.length &&
    fields.every(field => (field === 'weightKg' || field === 'reps') && Boolean(source[field].trim())) &&
    validRecordingV2Set(exercise, {...source, weightKg: fields.includes('weightKg') ? source.weightKg : '',
      reps: fields.includes('reps') ? source.reps : '', seconds: '', rpe: ''})
}

/** Update the selected active groups and create any new groups in one draft mutation. */
export function applyBatchSetValues(draft: RecordingDraft, exerciseEntryKey: string, sourceKey: string,
  recordKeys: string[], fields: BatchSetField[], newRecordKeys: string[], expectedSnapshot: string): RecordingDraft {
  const exercise = findExercise(draft, exerciseEntryKey)
  if (draft.recordFormat !== 2 || exercise.kind !== 'weight' || !measureUsesSharedWeight(exerciseMeasure(exercise)) || batchSetSnapshot(exercise) !== expectedSnapshot ||
      !Array.isArray(recordKeys) || !Array.isArray(newRecordKeys) || !Array.isArray(fields) ||
      recordKeys.length + newRecordKeys.length < 1 || recordKeys.length + newRecordKeys.length > 200 ||
      new Set(recordKeys).size !== recordKeys.length || new Set(newRecordKeys).size !== newRecordKeys.length ||
      !fields.length || new Set(fields).size !== fields.length ||
      fields.some(field => field !== 'weightKg' && field !== 'reps')) throw new Error('组或选择已变化，请重新选择')
  const source = findSet(exercise, sourceKey)
  if (!validBatchSetFields(exercise, source, fields)) throw new Error('请先填写有效的重量或次数')
  for (const key of recordKeys) findSet(exercise, key)
  for (const key of newRecordKeys) ensureUnusedSetKey(draft, key)
  if (newRecordKeys.some(key => recordKeys.includes(key))) throw new Error('训练组标识重复')
  let next = draft
  for (const key of newRecordKeys) next = addSet(next, exerciseEntryKey, key)
  for (const key of [...recordKeys, ...newRecordKeys]) {
    for (const field of fields) next = updateSet(next, exerciseEntryKey, key, field, source[field])
  }
  return next
}

function decimal(value: string, maxPlaces: number, minimum: number, maximum: number): number | undefined {
  const raw = value.trim()
  const pattern = maxPlaces ? new RegExp(`^\\d+(?:\\.\\d{1,${maxPlaces}})?$`) : /^\d+$/
  if (!pattern.test(raw)) return undefined
  const result = Number(raw)
  if (!Number.isFinite(result) || result < minimum || result > maximum) return undefined
  if (!maxPlaces && !Number.isSafeInteger(result)) return undefined
  return result
}

export function validateDraftSet(exercise: DraftExercise, set: DraftSet): { value?: SetWrite; message?: string; blank?: boolean } {
  const weight = set.weightKg.trim()
  const reps = set.reps.trim()
  const seconds = set.seconds.trim()
  const rpe = set.rpe.trim()
  if (!weight && !reps && !seconds && !rpe) return { blank: true }
  const identity = assertIdentity(exercise.identity)
  const result: SetWrite = {
    exerciseEntryKey: exercise.exerciseEntryKey,
    recordKey: set.recordKey,
    setIndex: set.setIndex,
    kind: exercise.kind,
    completed: true,
    ...(identity.source === 'catalog' ? { exerciseId: identity.exerciseId } : { exerciseName: identity.exerciseName }),
  }
  if (exercise.kind === 'weight' || exercise.kind === 'perSide') {
    const kg = decimal(weight, 2, 0, 300)
    const count = decimal(reps, 0, 1, 100)
    if (kg === undefined || kg === 0 || count === undefined || seconds) {
      return { message: '填写大于 0 且不超过 300 kg 的重量，以及 1–100 次' }
    }
    result.weightKg = kg
    result.reps = count
  } else if (exercise.kind === 'duration' || exercise.kind === 'durationPerSide') {
    const duration = decimal(seconds, 0, 5, 600)
    if (duration === undefined || weight || reps) return { message: '填写 5–600 秒的实际时长' }
    result.seconds = duration
  } else {
    const count = decimal(reps, 0, 1, 100)
    if (count === undefined || weight || seconds) return { message: '填写 1–100 次实际次数' }
    result.reps = count
  }
  if (rpe) {
    const rating = decimal(rpe, 1, 1, 10)
    if (rating === undefined) return { message: '主观感受填写 1–10，最多一位小数' }
    result.rpe = rating
  }
  return { value: result }
}

/** Confirm exactly one complete set in the current draft, without network writes. */
export function confirmSet(
  draft: RecordingDraft,
  exerciseEntryKey: string,
  recordKey: string,
): { ok: true; draft: RecordingDraft } | { ok: false; message: string } {
  const target = findExercise(draft, exerciseEntryKey)
  const set = findSet(target, recordKey)
  if (draft.recordFormat === 2) {
    if (!validRecordingV2Set(target, set)) return { ok: false, message: '请检查重量和次数的格式' }
  } else {
    const checked = validateDraftSet(target, set)
    if (checked.blank || checked.message) return { ok: false, message: checked.message || '先填写这组的实际数据' }
  }
  if (set.confirmed) return { ok: true, draft }
  const changed = { ...target, sets: target.sets.map(item => item === set ? { ...set, confirmed: true,
    ...(draft.recordFormat === 2 ? {dirty:true} : {}) } : item) }
  return { ok: true, draft: { ...draft, exercises: draft.exercises.map(item => item === target ? changed : item) } }
}

/** Prepare only the group explicitly confirmed in per-set mode; no network write occurs here. */
export function prepareConfirmedSet(
  draft: RecordingDraft,
  exerciseEntryKey: string,
  recordKey: string,
  today: string,
): ConfirmedSetPreparation {
  try {
    draft = restoreDraft(draft, today)
    if (draft.mode !== 'per_set') return { ok: false, message: '请先切换到逐组记录' }
    const exercise = findExercise(draft, exerciseEntryKey)
    const set = findSet(exercise, recordKey)
    if (!set.confirmed) return { ok: false, message: '请先记下这一组' }
    const checked = validateDraftSet(exercise, set)
    if (!checked.value) return { ok: false, message: checked.message || '先填写这组的实际数据' }
    if (set.persisted && !set.dirty) return { ok: true, state: 'already_saved' }
    return { ok: true, state: 'send', set: checked.value }
  } catch {
    return { ok: false, message: '草稿无法校验，请重新读取记录后重试' }
  }
}

/** Finishing is allowed only after each nonblank group has been acknowledged by the server. */
export function preparePerSetFinish(draft: RecordingDraft, today: string): PerSetFinishPreparation {
  try { draft = restoreDraft(draft, today) }
  catch { return { ok: false, issues: [], message: '草稿无法校验，请重新读取记录后重试' } }
  if (draft.mode !== 'per_set') return { ok: false, issues: [], message: '当前不是逐组记录' }
  const issues: GroupIssue[] = []
  let savedGroups = 0
  for (const exercise of draft.exercises) {
    exercise.sets.forEach(set => {
      const checked = validateDraftSet(exercise, set)
      const message = checked.message ||
        (checked.blank && (set.persisted || set.confirmed) ? '已记录的组不能留空，请补齐或通过删除流程移除' : undefined) ||
        (checked.value && !set.confirmed ? '请先记下这一组' : undefined) ||
        (checked.value && (!set.persisted || set.dirty) ? '这一组还未保存，请重试' : undefined)
      if (message) issues.push({ exerciseEntryKey: exercise.exerciseEntryKey, recordKey: set.recordKey, message })
      else if (checked.value) savedGroups++
    })
  }
  if (issues.length) return { ok: false, issues, message: '请先记下、保存或移除未完成的组' }
  if (!draft.server || !savedGroups) return { ok: false, issues: [], message: '至少保存一组实际数据后才能完成' }
  return { ok: true }
}


function object(value: unknown): value is Record<string, unknown> {
  return value !== null && typeof value === 'object' && !Array.isArray(value)
}

/** Reject damaged or cross-version cached data before it reaches the editor. */
export function restoreDraft(raw: unknown, today: string): RecordingDraft {
  let value: unknown = raw
  if (typeof raw === 'string') {
    try { value = JSON.parse(raw) } catch { throw new Error('草稿内容无法读取') }
  }
  if (!object(value) || typeof value.clientEventId !== 'string' || typeof value.date !== 'string' ||
    typeof value.timeZone !== 'string' || (value.mode !== 'after_workout' && value.mode !== 'per_set') ||
    !object(value.finish) || !Array.isArray(value.exercises)) throw new Error('草稿结构无效')
  if (Object.prototype.hasOwnProperty.call(value, 'syncPending') && typeof value.syncPending !== 'string') {
    throw new Error('草稿待发状态无效')
  }
  createRecordingDraft(value.clientEventId, value.date, value.timeZone, today)
  if ((value.recordFormat !== undefined && value.recordFormat !== 2) ||
      (value.completionRequested !== undefined && (value.recordFormat !== 2 || typeof value.completionRequested !== 'boolean'))) {
    throw new Error('草稿记录格式无效')
  }
  if (value.server !== undefined && (!object(value.server) || typeof value.server.id !== 'string' ||
    !/^[1-9]\d{0,19}$/.test(value.server.id) || !Number.isSafeInteger(value.server.version) ||
    Number(value.server.version) < 1 || (value.server.status !== undefined &&
      value.server.status !== 'in_progress' && value.server.status !== 'completed'))) throw new Error('草稿的服务端版本无效')
  if (value.syncTracking !== undefined && (value.syncTracking !== 1 || value.recordFormat !== 2 ||
      !object(value.server) || (value.server.status !== 'in_progress' && value.server.status !== 'completed'))) {
    throw new Error('草稿同步版本无效')
  }
  const finish = value.finish
  if (finish.durationMin !== undefined && finish.durationMin !== null &&
    (!Number.isSafeInteger(finish.durationMin) || Number(finish.durationMin) < 0)) throw new Error('草稿中的实际时长无效')
  if (finish.rpe !== undefined && finish.rpe !== null &&
    (typeof finish.rpe !== 'number' || !Number.isFinite(finish.rpe) || finish.rpe < 1 || finish.rpe > 10 ||
      Math.abs(finish.rpe * 10 - Math.round(finish.rpe * 10)) > 1e-8)) throw new Error('草稿中的主观感受无效')
  if (finish.note !== undefined && finish.note !== null &&
    (typeof finish.note !== 'string' || Array.from(finish.note).length > 2000)) throw new Error('草稿中的备注无效')
  const entries = new Set<string>()
  const groups = new Set<string>()
  for (const exercise of value.exercises) {
    if (!object(exercise) || typeof exercise.exerciseEntryKey !== 'string' || !object(exercise.identity) ||
      typeof exercise.kind !== 'string' || !Array.isArray(exercise.sets)) throw new Error('动作草稿无效')
    assertKey(exercise.exerciseEntryKey)
    if (entries.has(exercise.exerciseEntryKey)) throw new Error('动作条目标识重复')
    entries.add(exercise.exerciseEntryKey)
    if (exercise.identity.source === 'catalog' && typeof exercise.identity.exerciseId === 'string') {
      assertIdentity({ source: 'catalog', exerciseId: exercise.identity.exerciseId })
    } else if (exercise.identity.source === 'custom' && typeof exercise.identity.exerciseName === 'string') {
      assertIdentity({ source: 'custom', exerciseName: exercise.identity.exerciseName })
    } else throw new Error('动作来源无效')
    if (exercise.resolvedExerciseName !== undefined &&
      (typeof exercise.resolvedExerciseName !== 'string' || !exercise.resolvedExerciseName.trim() ||
        Array.from(exercise.resolvedExerciseName).length > 128 ||
        (exercise.identity.source === 'custom' && exercise.resolvedExerciseName !== exercise.identity.exerciseName))) {
      throw new Error('动作显示名称无效')
    }
    if (!['weight', 'perSide', 'bodyweight', 'bodyweightPerSide', 'duration', 'durationPerSide', 'breath'].includes(exercise.kind)) {
      throw new Error('动作计量方式无效')
    }
    if (exercise.measure !== undefined && (value.recordFormat !== 2 || exercise.kind !== 'weight' || !isV2Measure(exercise.measure))) {
      throw new Error('动作计量方式无效')
    }
    // Old drafts cannot prove whether a write reached the server before a
    // local acknowledgement. Preserve them and reconcile before assigning indexes.
    if (exercise.sets.some(group => !object(group))) throw new Error('训练组草稿无效')
    if (exercise.sets.some(group => object(group) && group.setIndex === undefined)) {
      throw new Error('旧草稿需要核对训练组序号，原数据仍被保留')
    }
    const indexes = new Set<number>()
    const validateGroup = (group: unknown): number => {
      if (!object(group) || typeof group.recordKey !== 'string' || typeof group.weightKg !== 'string' ||
        typeof group.reps !== 'string' || typeof group.seconds !== 'string' || typeof group.rpe !== 'string' ||
        typeof group.confirmed !== 'boolean' || typeof group.persisted !== 'boolean' || typeof group.dirty !== 'boolean') {
        throw new Error('训练组草稿无效')
      }
      assertKey(group.recordKey)
      for (const key of ['leftWeightKg', 'leftReps', 'rightWeightKg', 'rightReps', 'bodyweightKg', 'adjustmentKg'] as const) {
        if (Object.prototype.hasOwnProperty.call(group, key) && typeof group[key] !== 'string') throw new Error('训练组草稿无效')
        if (Object.prototype.hasOwnProperty.call(group, key) && value.recordFormat !== 2) throw new Error('训练组草稿无效')
      }
      for (const key of ['leftState', 'rightState'] as const) {
        if (!Object.prototype.hasOwnProperty.call(group, key)) continue
        if (value.recordFormat !== 2 || !['unknown', 'performed', 'not_performed'].includes(String(group[key]))) throw new Error('训练组草稿无效')
      }
      if (group.persisted && value.server === undefined) throw new Error('已保存的组缺少服务端记录')
      if (groups.has(group.recordKey)) throw new Error('训练组标识重复')
      groups.add(group.recordKey)
      const index = group.setIndex
      if (!Number.isSafeInteger(index) || Number(index) < 1 || indexes.has(Number(index))) throw new Error('训练组序号无效或顺序重复')
      indexes.add(Number(index))
      return Number(index)
    }
    let previousIndex = 0
    for (const group of exercise.sets) {
      const index = validateGroup(group)
      if (index <= previousIndex) throw new Error('训练组序号无效或顺序重复')
      previousIndex = index
    }
    if (exercise.removedSets !== undefined) {
      if (!Array.isArray(exercise.removedSets)) throw new Error('已移除的组无效')
      for (const removed of exercise.removedSets) {
        if (!object(removed) || !Number.isSafeInteger(removed.removedPosition) || Number(removed.removedPosition) < 1 ||
          !object(removed.set) || (removed.set.persisted !== false && (value.recordFormat !== 2 || removed.set.persisted !== true))) throw new Error('已移除的组无效')
        validateGroup(removed.set)
      }
    }
  }
  return value as unknown as RecordingDraft
}

/** Validate the user's explicit save action; the caller submits and reports the result. */
export function prepareSave(draft: RecordingDraft, today: string): SavePreparation {
  try { draft = restoreDraft(draft, today) }
  catch { return { ok: false, issues: [], message: '草稿无法校验，请重新读取记录后重试' } }
  if (draft.mode !== 'after_workout') return { ok: false, issues: [], message: '逐组记录请逐组记下' }
  const sets: SetWrite[] = []
  const issues: GroupIssue[] = []
  for (const exercise of draft.exercises) {
    exercise.sets.forEach(set => {
      const checked = validateDraftSet(exercise, set)
      const message = checked.message ||
        (checked.blank && (set.persisted || set.confirmed) ? '已记录的组不能留空，请补齐或通过删除流程移除' : undefined)
      if (message) issues.push({ exerciseEntryKey: exercise.exerciseEntryKey, recordKey: set.recordKey, message })
      else if (checked.value) sets.push(checked.value)
    })
  }
  if (issues.length) return { ok: false, issues, message: '请补齐或移除未完成的组' }
  if (!sets.length) return { ok: false, issues: [], message: '至少填写一组实际数据后保存' }
  if (sets.length > 200) return { ok: false, issues: [], message: '一次最多保存 200 组' }
  return { ok: true, sets }
}
