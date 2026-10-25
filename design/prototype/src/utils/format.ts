import type { LastReference, LoadKind } from '../types'

const SHANGHAI = 'Asia/Shanghai'

const timeFormatter = new Intl.DateTimeFormat('zh-CN', {
  hour: '2-digit',
  minute: '2-digit',
  hour12: false,
  timeZone: SHANGHAI,
})

const dateTimeFormatter = new Intl.DateTimeFormat('zh-CN', {
  month: 'long',
  day: 'numeric',
  hour: '2-digit',
  minute: '2-digit',
  hour12: false,
  timeZone: SHANGHAI,
})

export function formatTime(iso: string): string {
  return timeFormatter.format(new Date(iso))
}

export function formatDateTime(iso: string): string {
  return dateTimeFormatter.format(new Date(iso))
}

export function formatWeight(value: number): string {
  if (!Number.isFinite(value)) return '—'
  return Number.isInteger(value) ? String(value) : String(Math.round(value * 100) / 100)
}

export function formatClock(milliseconds: number): string {
  const totalSeconds = Math.max(0, Math.ceil(milliseconds / 1000))
  const minutes = Math.floor(totalSeconds / 60)
  const seconds = totalSeconds % 60
  return `${String(minutes).padStart(2, '0')}:${String(seconds).padStart(2, '0')}`
}

export function formatMinutes(minutes: number): string {
  return minutes >= 60 ? `${Math.floor(minutes / 60)} 小时 ${minutes % 60} 分` : `${minutes} 分钟`
}

export function lastReferenceLabel(ref: LastReference, kind?: LoadKind): string {
  const parts: string[] = []
  if (ref.weightKg !== undefined && ref.weightKg > 0) parts.push(`${formatWeight(ref.weightKg)} kg`)
  if (ref.reps !== undefined) {
    const unit = kind === 'breath' ? '次呼吸' : '次'
    parts.push(`${ref.reps} ${unit}${ref.perSide ? '/侧' : ''}`)
  }
  if (ref.seconds !== undefined) {
    parts.push(`${ref.seconds} 秒${ref.perSide && kind !== undefined ? '/侧' : ''}`)
  }
  return parts.join(' × ')
}

export function isDurationKind(kind: LoadKind): boolean {
  return kind === 'duration' || kind === 'durationPerSide'
}

export function isPerSideKind(kind: LoadKind): boolean {
  return kind === 'perSide' || kind === 'bodyweightPerSide' || kind === 'durationPerSide'
}

export function isBodyweightKind(kind: LoadKind): boolean {
  return kind === 'bodyweight' || kind === 'bodyweightPerSide' || kind === 'breath'
}

export function setValueLabel(
  kind: LoadKind,
  set: { weightKg: number | null; reps: number | null; seconds: number | null },
): string {
  if (isDurationKind(kind)) {
    return set.seconds === null ? '—' : `${set.seconds} 秒${isPerSideKind(kind) ? '/侧' : ''}`
  }
  if (set.reps === null) return '—'
  if (kind === 'breath') return `${set.reps} 次呼吸`
  if (isBodyweightKind(kind)) {
    return `自重 × ${set.reps} 次${isPerSideKind(kind) ? '/侧' : ''}`
  }
  if (set.weightKg === null) return '—'
  return `${formatWeight(set.weightKg)} kg × ${set.reps} 次${isPerSideKind(kind) ? '/侧' : ''}`
}

export function unitFor(kind: LoadKind): string {
  if (isDurationKind(kind)) return '秒'
  if (kind === 'breath') return '次呼吸'
  if (isBodyweightKind(kind)) return '次'
  return 'kg'
}

export function repUnitFor(kind: LoadKind): string {
  if (kind === 'breath') return '次呼吸'
  return isPerSideKind(kind) ? '次/侧' : '次'
}


/** S05 折叠摘要只描述真实存在的某一组，绝不把不同组的重量/次数拼在一起。 */
export function summarizeRecordSets(
  sets: Array<{
    kind: LoadKind
    weightKg: number | null
    reps: number | null
    seconds: number | null
  }>,
): string {
  if (!sets.length) return ''
  const kinds = new Set(sets.map((set) => set.kind))
  if (kinds.size > 1) return '多种记录方式 · 查看明细'
  const kind = sets[0]!.kind
  const side = isPerSideKind(kind) ? '/侧' : ''
  if (isDurationKind(kind)) {
    const best = Math.max(...sets.map((set) => set.seconds ?? 0))
    return `${best} 秒${side} · ${sets.length} 组`
  }
  if (kind === 'breath') {
    const best = Math.max(...sets.map((set) => set.reps ?? 0))
    return `${best} 次呼吸 · ${sets.length} 组`
  }
  if (isBodyweightKind(kind)) {
    const best = Math.max(...sets.map((set) => set.reps ?? 0))
    return `自重 × ${best} 次${side} · ${sets.length} 组`
  }
  const representative = sets.reduce((best, set) =>
    (set.weightKg ?? 0) > (best.weightKg ?? 0) ? set : best,
  )
  return `${formatWeight(representative.weightKg ?? 0)} kg × ${representative.reps ?? 0} 次${side} · ${sets.length} 组`
}
