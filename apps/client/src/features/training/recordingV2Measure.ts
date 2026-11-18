import type { DraftExercise, DraftSet } from './recording'

/** How a format-2 weight exercise interprets the kilograms the user typed. */
export const V2_MEASURES = ['total', 'pair_each', 'sides', 'bodyweight', 'bodyweight_extra', 'bodyweight_assisted', 'direct'] as const
export type V2Measure = typeof V2_MEASURES[number]
export type SideState = 'unknown' | 'performed' | 'not_performed'
export type V2SideName = 'left' | 'right'

export interface V2Side {
  state: SideState
  weightKg?: string
  reps?: number
}

export interface V2Measurement {
  method: V2Measure
  weightKg?: string
  reps?: number
  bodyweightKg?: string
  adjustmentKg?: string
  left?: V2Side
  right?: V2Side
}

export const MEASURE_LABELS: Record<V2Measure, string> = {
  total: '合计重量',
  pair_each: '每只器械',
  sides: '左右分开',
  bodyweight: '自重',
  bodyweight_extra: '自重加负重',
  bodyweight_assisted: '自重减助力',
  direct: '手填记录重量',
}

const OPTIONAL_FIELDS = ['leftWeightKg', 'leftReps', 'rightWeightKg', 'rightReps', 'bodyweightKg', 'adjustmentKg'] as const
type OptionalField = typeof OPTIONAL_FIELDS[number]

const weightPattern = /^\d{1,6}(?:\.\d{1,2})?$/
const rpePattern = /^(?:[1-9](?:\.\d)?|10(?:\.0)?)$/

export function isV2Measure(value: unknown): value is V2Measure {
  return typeof value === 'string' && (V2_MEASURES as readonly string[]).includes(value)
}

export function exerciseMeasure(exercise: Pick<DraftExercise, 'measure'>): V2Measure {
  return exercise.measure ?? 'total'
}

export function measureUsesBodyweight(measure: V2Measure): boolean {
  return measure === 'bodyweight' || measure === 'bodyweight_extra' || measure === 'bodyweight_assisted'
}

export function measureUsesSharedWeight(measure: V2Measure): boolean {
  return measure === 'total' || measure === 'pair_each' || measure === 'direct'
}

function text(value: string | undefined): string {
  return (value ?? '').trim()
}

function optionalWeight(value: string | undefined, positive: boolean): boolean {
  const raw = text(value)
  if (!raw) return true
  if (!weightPattern.test(raw)) return false
  return !positive || Number(raw) > 0
}

function optionalReps(value: string | undefined): boolean {
  const raw = text(value)
  return !raw || (/^\d+$/.test(raw) && Number(raw) >= 1 && Number(raw) <= 100)
}

function decimalCompare(left: string, right: string): number {
  const [leftWhole, leftFraction = ''] = left.split('.')
  const [rightWhole, rightFraction = ''] = right.split('.')
  const scale = Math.max(leftFraction.length, rightFraction.length)
  const expand = (whole: string, fraction: string): bigint => BigInt(whole) * 10n ** BigInt(scale) + BigInt(fraction.padEnd(scale, '0') || '0')
  const a = expand(leftWhole, leftFraction)
  const b = expand(rightWhole, rightFraction)
  return a < b ? -1 : a > b ? 1 : 0
}

function sideState(value: SideState | undefined): boolean {
  return value === undefined || value === 'unknown' || value === 'performed' || value === 'not_performed'
}

/** Present values must match the contract. Empty fields stay empty and are not zero. */
export function validV2DraftSet(exercise: DraftExercise, set: DraftSet): boolean {
  if (exercise.kind !== 'weight' || typeof set.weightKg !== 'string' || typeof set.reps !== 'string' ||
      typeof set.seconds !== 'string' || typeof set.rpe !== 'string' || set.seconds.trim() || !isV2Measure(exerciseMeasure(exercise))) return false
  if (!optionalWeight(set.weightKg, false) || !optionalReps(set.reps) || (text(set.rpe) && !rpePattern.test(text(set.rpe)))) return false
  if (!optionalWeight(set.leftWeightKg, false) || !optionalWeight(set.rightWeightKg, false) ||
      !optionalWeight(set.bodyweightKg, true) || !optionalWeight(set.adjustmentKg, false) ||
      !optionalReps(set.leftReps) || !optionalReps(set.rightReps) || !sideState(set.leftState) || !sideState(set.rightState)) return false
  const measure = exerciseMeasure(exercise)
  if ((measure === 'bodyweight_assisted') && text(set.bodyweightKg) && text(set.adjustmentKg) &&
      decimalCompare(text(set.adjustmentKg), text(set.bodyweightKg)) > 0) return false
  return true
}

function repsNumber(value: string | undefined): number | undefined {
  const raw = text(value)
  return raw ? Number(raw) : undefined
}

function sidePayload(weight: string | undefined, reps: string | undefined, state: SideState | undefined): V2Side | undefined {
  if (state === 'not_performed') return { state: 'not_performed' }
  const weightKg = text(weight)
  const count = repsNumber(reps)
  if (!weightKg && count === undefined) return undefined
  return { state: 'performed', ...(weightKg ? { weightKg } : {}), ...(count !== undefined ? { reps: count } : {}) }
}

/** Build the wire measurement. Invalid input returns null instead of a partial write. */
export function measurementFromSet(exercise: DraftExercise, set: DraftSet): V2Measurement | null {
  if (!validV2DraftSet(exercise, set)) return null
  const method = exerciseMeasure(exercise)
  const reps = repsNumber(set.reps)
  const weightKg = text(set.weightKg)
  const bodyweightKg = text(set.bodyweightKg)
  const adjustmentKg = text(set.adjustmentKg)
  const rpe = text(set.rpe)
  if (rpe && !rpePattern.test(rpe)) return null
  if (method === 'sides') {
    const left = sidePayload(set.leftWeightKg, set.leftReps, set.leftState)
    const right = sidePayload(set.rightWeightKg, set.rightReps, set.rightState)
    return { method, ...(left ? { left } : {}), ...(right ? { right } : {}) }
  }
  if (method === 'bodyweight') return { method, ...(bodyweightKg ? { bodyweightKg } : {}), ...(reps !== undefined ? { reps } : {}) }
  if (method === 'bodyweight_extra' || method === 'bodyweight_assisted') {
    return { method, ...(bodyweightKg ? { bodyweightKg } : {}), ...(adjustmentKg ? { adjustmentKg } : {}), ...(reps !== undefined ? { reps } : {}) }
  }
  return { method, ...(weightKg ? { weightKg } : {}), ...(reps !== undefined ? { reps } : {}) }
}

function canonicalSide(side: V2Side | undefined): string {
  if (!side || (side.state === 'unknown' && !side.weightKg && side.reps === undefined)) return ''
  return `${side.state}|${side.weightKg ?? ''}|${side.reps ?? ''}`
}

export function measurementsEqual(left: V2Measurement, right: V2Measurement): boolean {
  return left.method === right.method && (left.weightKg ?? '') === (right.weightKg ?? '') && (left.reps ?? '') === (right.reps ?? '') &&
    (left.bodyweightKg ?? '') === (right.bodyweightKg ?? '') && (left.adjustmentKg ?? '') === (right.adjustmentKg ?? '') &&
    canonicalSide(left.left) === canonicalSide(right.left) && canonicalSide(right.right) === canonicalSide(left.right)
}

function sideFields(prefix: 'left' | 'right', side: V2Side | undefined): Partial<DraftSet> {
  if (!side || side.state === 'unknown') return {}
  const weightKey = prefix === 'left' ? 'leftWeightKg' : 'rightWeightKg'
  const repsKey = prefix === 'left' ? 'leftReps' : 'rightReps'
  const stateKey = prefix === 'left' ? 'leftState' : 'rightState'
  if (side.state === 'not_performed') return { [stateKey]: 'not_performed', [weightKey]: '', [repsKey]: '' }
  return {
    [stateKey]: 'performed',
    [weightKey]: side.weightKg ?? '',
    [repsKey]: side.reps === undefined ? '' : String(side.reps),
  }
}

/** Project one server measurement onto draft fields. Unknown methods return null. */
export function draftFieldsFromMeasurement(measurement: V2Measurement): Partial<DraftSet> | null {
  if (!isV2Measure(measurement.method)) return null
  const blank: Partial<DraftSet> = { weightKg: '', reps: '' }
  if (measurement.method === 'sides') return { ...blank, ...sideFields('left', measurement.left), ...sideFields('right', measurement.right) }
  if (measurement.method === 'bodyweight' || measurement.method === 'bodyweight_extra' || measurement.method === 'bodyweight_assisted') {
    return {
      ...blank,
      ...(measurement.bodyweightKg !== undefined ? { bodyweightKg: measurement.bodyweightKg } : {}),
      ...((measurement.method !== 'bodyweight' && measurement.adjustmentKg !== undefined) ? { adjustmentKg: measurement.adjustmentKg } : {}),
      reps: measurement.reps === undefined ? '' : String(measurement.reps),
    }
  }
  return {
    weightKg: measurement.weightKg ?? '',
    reps: measurement.reps === undefined ? '' : String(measurement.reps),
  }
}

export function inferMeasure(methods: readonly string[]): V2Measure | 'mixed' | null {
  const known = methods.filter(method => method !== '')
  if (known.some(method => !isV2Measure(method))) return null
  const unique = new Set(known)
  if (unique.size > 1) return 'mixed'
  return (known[0] as V2Measure | undefined) ?? 'total'
}

function weightText(value: string | undefined): string {
  return text(value) ? `${text(value)} kg` : '重量未填'
}

/** Exact kilogram arithmetic for display. Missing or illegal text stays missing. */
function scaledKilograms(value: string | undefined): bigint | null {
  const raw = text(value)
  if (!weightPattern.test(raw)) return null
  const [whole, fraction = ''] = raw.split('.')
  return BigInt(whole) * 100n + BigInt((fraction + '00').slice(0, 2))
}

function kilogramsFromScaled(value: bigint): string {
  const whole = value / 100n
  const fraction = value % 100n
  if (fraction === 0n) return `${whole}`
  if (fraction % 10n === 0n) return `${whole}.${fraction / 10n}`
  return `${whole}.${fraction.toString().padStart(2, '0')}`
}

function recordWeightText(bodyweight: string | undefined, adjustment: string | undefined, subtract: boolean): string | null {
  const base = scaledKilograms(bodyweight)
  const extra = scaledKilograms(adjustment)
  if (base === null || extra === null) return null
  const total = subtract ? base - extra : base + extra
  if (total < 0n) return null
  return `${kilogramsFromScaled(total)} kg`
}

function sideText(weight: string | undefined, reps: string | undefined, state: SideState | undefined): string {
  if (state === 'not_performed') return '未做'
  if (!text(weight) && !text(reps)) return '未填写'
  return `${weightText(weight)} × ${text(reps) ? `${text(reps)} 次` : '次数未填'}`
}

/** Short set text for cards, conflict rows and the completion list. */
export function describeDraftSet(exercise: Pick<DraftExercise, 'measure' | 'kind'>, set: DraftSet): string {
  const measure = exercise.kind === 'weight' ? exerciseMeasure(exercise) : 'total'
  if (measure === 'sides') return `左 ${sideText(set.leftWeightKg, set.leftReps, set.leftState)} · 右 ${sideText(set.rightWeightKg, set.rightReps, set.rightState)}`
  const reps = text(set.reps) ? `${text(set.reps)} 次` : '次数未填'
  if (measure === 'pair_each') return `每只 ${weightText(set.weightKg)} × ${reps}`
  if (measure === 'direct') return `记录重量 ${weightText(set.weightKg)} × ${reps}`
  if (measure === 'bodyweight') return `自重 ${weightText(set.bodyweightKg)} × ${reps}`
  if (measure === 'bodyweight_extra' || measure === 'bodyweight_assisted') {
    const label = measure === 'bodyweight_extra' ? '负重' : '助力'
    const sign = measure === 'bodyweight_extra' ? '＋' : '－'
    const record = recordWeightText(set.bodyweightKg, set.adjustmentKg, measure === 'bodyweight_assisted')
    const parts = `自重 ${weightText(set.bodyweightKg)}${sign}${label} ${weightText(set.adjustmentKg)}`
    return `${record ? `${parts}＝记录重量 ${record}` : parts} × ${reps}`
  }
  return `${weightText(set.weightKg)} × ${reps}`
}

/** Compare the measurement that would be saved, not leftover draft text the wire drops. */
export function setValueSignature(set: DraftSet, measure: V2Measure): string {
  const exercise: DraftExercise = {
    exerciseEntryKey: 'entry-signature', identity: { source: 'custom', exerciseName: '签名' },
    kind: 'weight', ...(measure === 'total' ? {} : { measure }), sets: [],
  }
  return JSON.stringify([measurementFromSet(exercise, set), text(set.rpe), set.confirmed])
}

export function measurementCarry(source: DraftSet): Partial<DraftSet> {
  const carry: Partial<DraftSet> = {}
  for (const key of OPTIONAL_FIELDS) {
    const value = source[key]
    if (value !== undefined) carry[key] = value
  }
  if (source.leftState !== undefined) carry.leftState = source.leftState
  if (source.rightState !== undefined) carry.rightState = source.rightState
  return carry
}

/** Replace measurement fields and drop ones the selected source does not carry. */
export function applyMeasurementFields(target: DraftSet, source: DraftSet): DraftSet {
  const next: DraftSet = { ...target, ...measurementCarry(source), weightKg: source.weightKg, reps: source.reps }
  for (const key of OPTIONAL_FIELDS) {
    if (source[key] === undefined) delete next[key]
  }
  if (source.leftState === undefined) delete next.leftState
  if (source.rightState === undefined) delete next.rightState
  return next
}

export function optionalMeasurementKeys(): readonly OptionalField[] {
  return OPTIONAL_FIELDS
}
