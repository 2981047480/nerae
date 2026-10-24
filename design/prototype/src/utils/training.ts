import type {
  LastReference,
  LoadKind,
  SessionExercise,
  SessionSet,
  SessionState,
  SetInput,
  SetInputErrors,
} from '../types'
import { formatWeight, lastReferenceLabel } from './format'

export interface SetEntry {
  exercise: SessionExercise
  set: SessionSet
}

export function flattenSets(session: SessionState): SetEntry[] {
  return session.exercises.flatMap((exercise) =>
    exercise.sets.map((set) => ({ exercise, set })),
  )
}

export function totalSetCount(session: SessionState): number {
  return session.exercises.reduce((sum, exercise) => sum + exercise.sets.length, 0)
}

export function doneSetCount(session: SessionState): number {
  return flattenSets(session).filter((entry) => entry.set.status === 'done').length
}

export function firstPendingEntry(session: SessionState): SetEntry | null {
  return flattenSets(session).find((entry) => entry.set.status === 'pending') ?? null
}

export function pendingEntries(session: SessionState): SetEntry[] {
  return flattenSets(session).filter((entry) => entry.set.status === 'pending')
}

export function completedEntries(session: SessionState): SetEntry[] {
  return flattenSets(session).filter((entry) => entry.set.status === 'done')
}

export function exerciseProgress(exercise: SessionExercise): { done: number; total: number } {
  return {
    done: exercise.sets.filter((set) => set.status === 'done').length,
    total: exercise.sets.length,
  }
}

export function emptyInput(): SetInput {
  return { weightKg: '', reps: '', seconds: '' }
}

export function inputFromValues(
  kind: LoadKind,
  values: { weightKg?: number | null; reps?: number | null; seconds?: number | null } | null,
): SetInput {
  if (!values) return emptyInput()
  if (kind === 'duration') {
    return { weightKg: '', reps: '', seconds: values.seconds == null ? '' : String(values.seconds) }
  }
  return {
    weightKg: values.weightKg == null ? '' : String(values.weightKg),
    reps: values.reps == null ? '' : String(values.reps),
    seconds: '',
  }
}

export function referenceInput(kind: LoadKind, ref: LastReference): SetInput {
  return inputFromValues(kind, {
    weightKg: ref.weightKg ?? null,
    reps: ref.reps ?? null,
    seconds: ref.seconds ?? null,
  })
}

export function latestSetOfExercise(exercise: SessionExercise): SessionSet | null {
  const completed = exercise.sets.filter((set) => set.status === 'done')
  return completed.length ? completed[completed.length - 1] : null
}

export function suggestedInput(exercise: SessionExercise): SetInput {
  const latest = latestSetOfExercise(exercise)
  if (latest) {
    return inputFromValues(exercise.kind, {
      weightKg: latest.weightKg,
      reps: latest.reps,
      seconds: latest.seconds,
    })
  }
  return referenceInput(exercise.kind, exercise.lastRef)
}

export function parseNumber(raw: string): number {
  const normalized = raw.replace(',', '.').trim()
  if (!normalized) return Number.NaN
  return Number(normalized)
}

export function validateSetInput(kind: LoadKind, input: SetInput): SetInputErrors {
  const errors: SetInputErrors = {}
  if (kind === 'duration') {
    const seconds = parseNumber(input.seconds)
    if (!input.seconds.trim()) errors.seconds = '请输入实际完成的秒数'
    else if (!Number.isFinite(seconds) || !Number.isInteger(seconds)) {
      errors.seconds = '秒数需要是整数'
    } else if (seconds < 5) errors.seconds = '至少记录 5 秒'
    else if (seconds > 600) errors.seconds = '时长不能超过 600 秒'
    return errors
  }

  const rawWeight = input.weightKg.trim().replace(',', '.')
  const weight = parseNumber(rawWeight)
  const decimals = rawWeight.includes('.') ? rawWeight.split('.')[1]?.length ?? 0 : 0
  if (!input.weightKg.trim()) errors.weightKg = '请输入本次使用的重量'
  else if (!Number.isFinite(weight)) errors.weightKg = '重量需要是数字'
  else if (weight <= 0) errors.weightKg = '重量要大于 0'
  else if (weight > 300) errors.weightKg = '重量看起来过大，请核对'
  else if (decimals > 2) errors.weightKg = '重量最多保留两位小数'

  const reps = parseNumber(input.reps)
  if (!input.reps.trim()) errors.reps = '请输入完成的次数'
  else if (!Number.isFinite(reps) || !Number.isInteger(reps)) errors.reps = '次数需要是整数'
  else if (reps < 1) errors.reps = '次数至少为 1'
  else if (reps > 100) errors.reps = '次数不能超过 100'

  return errors
}

export function hasErrors(errors: SetInputErrors): boolean {
  return Object.values(errors).some(Boolean)
}

export function appliedValues(
  kind: LoadKind,
  input: SetInput,
): { weightKg: number | null; reps: number | null; seconds: number | null } {
  if (kind === 'duration') {
    return { weightKg: null, reps: null, seconds: Math.round(parseNumber(input.seconds)) }
  }
  return {
    weightKg: Math.round(parseNumber(input.weightKg) * 100) / 100,
    reps: Math.round(parseNumber(input.reps)),
    seconds: null,
  }
}

export function referenceText(exercise: SessionExercise): string {
  return `上次 · ${exercise.lastRef.dateLabel} · ${lastReferenceLabel(exercise.lastRef)}`
}

export function latestEventAt(session: SessionState): string | null {
  const times: number[] = []
  for (const exercise of session.exercises) {
    for (const set of exercise.sets) {
      if (set.status === 'done' && set.completedAt) {
        const parsed = Date.parse(set.completedAt)
        if (Number.isFinite(parsed)) times.push(parsed)
      }
    }
  }
  if (session.completedAt) {
    const parsed = Date.parse(session.completedAt)
    if (Number.isFinite(parsed)) times.push(parsed)
  }
  return times.length ? new Date(Math.max(...times)).toISOString() : null
}

/** 样例会话使用固定预览日的时间线：新记录在已有事件之后递增，不与真实墙钟混排。 */
export function nextEventTime(session: SessionState, restSeconds: number): string {
  const latest = latestEventAt(session)
  const base = latest ? Date.parse(latest) : Date.parse(session.startedAt)
  return new Date(base + (Math.max(45, restSeconds + 45) * 1000)).toISOString()
}

export function setAriaLabel(kind: LoadKind, set: SessionSet): string {
  if (set.status === 'pending') return `第 ${set.index} 组，待记录`
  if (kind === 'duration') return `第 ${set.index} 组，已记录 ${set.seconds ?? 0} 秒`
  const weight = set.weightKg === null ? '' : `${formatWeight(set.weightKg)} 千克`
  const side = kind === 'perSide' ? '，每侧' : ''
  return `第 ${set.index} 组，已记录 ${weight} ${set.reps ?? 0} 次${side}`
}
