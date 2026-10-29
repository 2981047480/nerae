/** Shared editing rules for the uni-app training-record feature.
 * The caller owns authenticated storage and network submission. This module
 * never claims that an edited or locally confirmed set was saved remotely.
 */

export type RecordingMode = 'after_workout' | 'per_set'
export type SetKind = 'weight' | 'perSide' | 'bodyweight' | 'bodyweightPerSide' | 'duration' | 'durationPerSide' | 'breath'
export type InputField = 'weightKg' | 'reps' | 'seconds' | 'rpe'

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
}

export interface DraftExercise {
  exerciseEntryKey: string
  identity: ExerciseIdentity
  kind: SetKind
  sets: DraftSet[]
}

export interface RecordingDraft {
  clientEventId: string
  date: string
  timeZone: string
  mode: RecordingMode
  /** Added only after a real server response; version is needed for edits. */
  server?: { id: string; version: number }
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
  if (typeof key !== 'string' || key.length < 8 || key.length > 64 || !key.trim()) {
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

function ensureUnusedSetKey(draft: RecordingDraft, recordKey: string): void {
  assertKey(recordKey)
  if (draft.exercises.some(exercise => exercise.sets.some(set => set.recordKey === recordKey))) {
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
  const maximum = target.sets.reduce((largest, set) => Math.max(largest, set.setIndex), 0)
  if (!Number.isSafeInteger(maximum) || maximum >= Number.MAX_SAFE_INTEGER) throw new Error('训练组序号不可用')
  const changed = { ...target, sets: [...target.sets, emptySet(recordKey, maximum + 1)] }
  return { ...draft, exercises: draft.exercises.map(item => item === target ? changed : item) }
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
  if (!['weightKg', 'reps', 'seconds', 'rpe'].includes(field) || typeof value !== 'string') {
    throw new Error('记录输入无效')
  }
  if (set[field] === value) return draft
  const updated = { ...set, [field]: value, confirmed: false, dirty: true }
  const changed = { ...target, sets: target.sets.map(item => item === set ? updated : item) }
  return { ...draft, exercises: draft.exercises.map(item => item === target ? changed : item) }
}


/** A previously uploaded set needs the server deletion workflow, not local removal. */
export function removeUnsubmittedSet(draft: RecordingDraft, exerciseEntryKey: string, recordKey: string): RecordingDraft {
  const target = findExercise(draft, exerciseEntryKey)
  const set = findSet(target, recordKey)
  if (set.persisted) throw new Error('已同步的组需要通过删除流程处理')
  const changed = { ...target, sets: target.sets.filter(item => item !== set) }
  return { ...draft, exercises: draft.exercises.map(item => item === target ? changed : item) }
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

function validation(exercise: DraftExercise, set: DraftSet): { value?: SetWrite; message?: string; blank?: boolean } {
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
  const checked = validation(target, set)
  if (checked.blank || checked.message) return { ok: false, message: checked.message || '先填写这组的实际数据' }
  if (set.confirmed) return { ok: true, draft }
  const changed = { ...target, sets: target.sets.map(item => item === set ? { ...set, confirmed: true } : item) }
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
    const checked = validation(exercise, set)
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
      const checked = validation(exercise, set)
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
  createRecordingDraft(value.clientEventId, value.date, value.timeZone, today)
  if (value.server !== undefined && (!object(value.server) || typeof value.server.id !== 'string' ||
    !/^[1-9]\d{0,19}$/.test(value.server.id) || !Number.isSafeInteger(value.server.version) ||
    Number(value.server.version) < 1)) throw new Error('草稿的服务端版本无效')
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
    if (!['weight', 'perSide', 'bodyweight', 'bodyweightPerSide', 'duration', 'durationPerSide', 'breath'].includes(exercise.kind)) {
      throw new Error('动作计量方式无效')
    }
    // Old drafts cannot prove whether a write reached the server before a
    // local acknowledgement. Preserve them and reconcile before assigning indexes.
    if (exercise.sets.some(group => !object(group))) throw new Error('训练组草稿无效')
    if (exercise.sets.some(group => object(group) && group.setIndex === undefined)) {
      throw new Error('旧草稿需要核对训练组序号，原数据仍被保留')
    }
    let previousIndex = 0
    for (const group of exercise.sets) {
      if (!object(group) || typeof group.recordKey !== 'string' || typeof group.weightKg !== 'string' ||
        typeof group.reps !== 'string' || typeof group.seconds !== 'string' || typeof group.rpe !== 'string' ||
        typeof group.confirmed !== 'boolean' || typeof group.persisted !== 'boolean' || typeof group.dirty !== 'boolean') {
        throw new Error('训练组草稿无效')
      }
      assertKey(group.recordKey)
      if (group.persisted && value.server === undefined) throw new Error('已保存的组缺少服务端记录')
      if (groups.has(group.recordKey)) throw new Error('训练组标识重复')
      groups.add(group.recordKey)
      const index = group.setIndex
      if (!Number.isSafeInteger(index) || Number(index) <= previousIndex) throw new Error('训练组序号无效或顺序重复')
      previousIndex = Number(index)
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
      const checked = validation(exercise, set)
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
