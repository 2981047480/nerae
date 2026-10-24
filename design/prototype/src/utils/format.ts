import type { LastReference, LoadKind, SessionSet } from '../types'

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

export function lastReferenceLabel(ref: LastReference): string {
  const parts: string[] = []
  if (ref.weightKg !== undefined) parts.push(`${formatWeight(ref.weightKg)} kg`)
  if (ref.reps !== undefined) parts.push(`${ref.reps} 次${ref.perSide ? '/侧' : ''}`)
  if (ref.seconds !== undefined) parts.push(`${ref.seconds} 秒`)
  return parts.join(' × ')
}

export function setValueLabel(kind: LoadKind, set: SessionSet): string {
  if (kind === 'duration') {
    return set.seconds === null ? '—' : `${set.seconds} 秒`
  }
  if (set.weightKg === null || set.reps === null) return '—'
  return `${formatWeight(set.weightKg)} kg × ${set.reps} 次${kind === 'perSide' ? '/侧' : ''}`
}

export function unitFor(kind: LoadKind): string {
  return kind === 'duration' ? '秒' : 'kg'
}

export function repUnitFor(kind: LoadKind): string {
  return kind === 'perSide' ? '次/侧' : '次'
}
