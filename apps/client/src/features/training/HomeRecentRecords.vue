<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref, shallowRef } from 'vue'
import type { AuthSessionCoordinator } from '../auth/sessionCoordinator'
import { RecordingApiError, type RecordingApi, type ServerSet, type WorkoutSessionDetail } from './recordingApi'
import type { RecordingV2Detail } from './recordingV2Projection'
import type { V2Measurement, V2Side } from './recordingV2Measure'

const props = defineProps<{ session: AuthSessionCoordinator; api: RecordingApi }>()
const emit = defineEmits<{ 'session-changed': [] }>()
const owner = props.session.snapshot()

type RecentRow = { key: string; name: string; lines: string[] }
type RecentRecord = {
  date: string
  status: 'in_progress' | 'completed' | 'skipped'
  rows: RecentRow[]
}

const record = shallowRef<RecentRecord | null>(null)
const loading = ref(false)
const error = ref('')
const replaced = ref(false)
let alive = true
let invalid = false
let unsubscribe: (() => void) | null = null

function current(): boolean {
  try { return alive && !invalid && Boolean(owner && props.session.isCurrent(owner.subjectId, owner.generation)) }
  catch { return false }
}

function invalidate(): void {
  if (!alive || invalid) return
  invalid = true
  replaced.value = true
  record.value = null
  error.value = ''
  emit('session-changed')
}

function validLease(lease: { subjectId: string; generation: string }): boolean {
  return Boolean(owner && lease.subjectId === owner.subjectId && lease.generation === owner.generation && current())
}

function rejectLease(): void {
  record.value = null
  error.value = '暂时无法读取云端记录，请稍后重试。'
}

function failed(problem: unknown): void {
  if (!current()) return
  record.value = null
  if (problem instanceof RecordingApiError &&
    (problem.code === 'unauthenticated' || problem.code === 'session_changed' || problem.code === 'forbidden')) {
    invalidate()
  } else {
    error.value = '暂时无法读取云端记录，请稍后重试。'
  }
}

function weightText(item: string | undefined): string {
  return item === undefined || item === '' ? '重量未填' : `${item} kg`
}

function repsText(item: number | undefined): string {
  return item === undefined ? '次数未填' : `${item} 次`
}

function sideText(item: V2Side | undefined): string {
  if (!item || item.state === 'unknown') return '未填写'
  if (item.state === 'not_performed') return '未做'
  return `${weightText(item.weightKg)} × ${repsText(item.reps)}`
}

function measurementLine(value: V2Measurement | undefined): string {
  if (!value) return '已保存'
  if (value.method === 'sides') return `左 ${sideText(value.left)} · 右 ${sideText(value.right)}`
  if (value.method === 'pair_each') return `每只 ${weightText(value.weightKg)} × ${repsText(value.reps)}`
  if (value.method === 'bodyweight') return `自重 ${weightText(value.bodyweightKg)} × ${repsText(value.reps)}`
  if (value.method === 'bodyweight_extra' || value.method === 'bodyweight_assisted') {
    const label = value.method === 'bodyweight_extra' ? '负重' : '助力'
    return `自重 ${weightText(value.bodyweightKg)} · ${label} ${weightText(value.adjustmentKg)} × ${repsText(value.reps)}`
  }
  return `${weightText(value.weightKg)} × ${repsText(value.reps)}`
}

function legacyLine(set: ServerSet): string {
  if (set.weightKg !== undefined && set.reps !== undefined && set.seconds === undefined) return `${set.weightKg} kg × ${set.reps} 次`
  const values: string[] = []
  if (set.weightKg !== undefined) values.push(`${set.weightKg} kg`)
  if (set.reps !== undefined) values.push(`${set.reps} 次`)
  if (set.seconds !== undefined) values.push(`${set.seconds} 秒`)
  return values.length ? values.join(' · ') : '已保存'
}

function orderedSets<T extends { setIndex: number }>(sets: T[]): T[] {
  return sets.map((set, index) => ({ set, index })).sort((left, right) => left.set.setIndex - right.set.setIndex || left.index - right.index).map(item => item.set)
}

function presentV2(detail: RecordingV2Detail): RecentRecord {
  const rows = detail.entries.filter(entry => entry.removedPosition === undefined).map(entry => {
    const sets = orderedSets(detail.sets.filter(set => set.exerciseEntryKey === entry.exerciseEntryKey))
    return {
      key: entry.exerciseEntryKey,
      name: entry.exerciseName,
      lines: sets.length ? sets.map(set => measurementLine(set.measurement)) : ['暂无记录组'],
    }
  })
  return { date: detail.date, status: detail.status, rows }
}

function presentLegacy(detail: WorkoutSessionDetail): RecentRecord {
  const keys = [...detail.exerciseEntryKeys]
  for (const set of detail.sets) if (!keys.includes(set.exerciseEntryKey)) keys.push(set.exerciseEntryKey)
  const rows = keys.map(key => {
    const sets = orderedSets(detail.sets.filter(set => set.exerciseEntryKey === key))
    const named = sets.find(set => set.resolvedExerciseName || set.exerciseName)
    return {
      key,
      name: named?.resolvedExerciseName || named?.exerciseName || '动作',
      lines: sets.length ? sets.map(legacyLine) : ['暂无记录组'],
    }
  })
  return { date: detail.date, status: detail.status, rows }
}

async function load(): Promise<void> {
  if (loading.value || !current()) return
  loading.value = true
  error.value = ''
  try {
    const page = await props.api.listRecordingsV2({ page: 1, pageSize: 1 })
    if (!current()) return
    if (!validLease(page.lease)) { rejectLease(); return }
    const item = page.items[0]
    if (!item) { record.value = null; return }
    if (item.recordFormat === 2) {
      const result = await props.api.readRecordingV2(item.id)
      if (!current()) return
      if (!validLease(result.lease)) { rejectLease(); return }
      record.value = presentV2(result.detail)
    } else {
      const result = await props.api.readSession(item.id)
      if (!current()) return
      if (!validLease(result.lease)) { rejectLease(); return }
      record.value = presentLegacy(result.detail)
    }
  } catch (problem) { failed(problem) }
  finally { if (alive) loading.value = false }
}

onMounted(() => {
  if (!current()) { invalidate(); return }
  try {
    const guard = props.session.captureInstallGuard()
    if (!guard) { invalidate(); return }
    unsubscribe = props.session.subscribeInstallGuardInvalidation(guard, invalidate)
    if (!current()) { invalidate(); return }
    void load()
  } catch { invalidate() }
})
onBeforeUnmount(() => {
  alive = false
  unsubscribe?.()
  unsubscribe = null
  record.value = null
})
</script>

<template>
  <view v-if="!replaced" class="recent-records">
    <text class="recent-records__title">最近记录</text>
    <template v-if="loading">
      <text role="status">正在读取云端记录…</text>
    </template>
    <template v-else-if="error">
      <text class="recent-records__error" role="alert">{{ error }}</text>
      <button class="recent-records__retry" @tap="load">重试云端记录</button>
    </template>
    <template v-else>
      <text class="recent-records__caption">显示最近一次已保存到账号的训练，本机草稿不在这里。</text>
      <template v-if="record">
        <text class="recent-records__meta">{{ record.date }}</text>
        <text class="recent-records__meta">{{ record.status === 'completed' ? '已完成' : record.status === 'skipped' ? '未训练' : '未完成' }}</text>
        <text v-if="record.rows.length === 0" class="recent-records__caption">这条记录暂无动作。</text>
        <view v-for="row in record.rows" :key="row.key" class="recent-records__row">
          <text class="recent-records__name">{{ row.name }}</text>
          <view class="recent-records__values">
            <text v-for="(line, index) in row.lines" :key="`${row.key}-${index}`" class="recent-records__value">{{ line }}</text>
          </view>
        </view>
      </template>
      <text v-else>还没有已保存到云端的训练。</text>
    </template>
  </view>
</template>

<style scoped>
.recent-records { display: flex; flex-direction: column; gap: 16rpx; padding: 28rpx; border: 1rpx solid var(--nerae-border-decorative); border-radius: 24rpx; background: var(--nerae-surface); }
.recent-records__title { color: var(--nerae-text); font-size: 32rpx; font-weight: 700; }
.recent-records__caption, .recent-records__meta { color: var(--nerae-text-secondary); font-size: 23rpx; line-height: 1.5; }
.recent-records__error { color: var(--nerae-text); font-size: 24rpx; }
.recent-records__row { display: flex; flex-wrap: wrap; align-items: flex-start; justify-content: space-between; gap: 12rpx 24rpx; }
.recent-records__name { flex: 1 1 auto; min-width: 0; color: var(--nerae-text); font-size: 28rpx; line-height: 1.4; }
.recent-records__values { display: flex; flex: 0 1 auto; flex-direction: column; align-items: flex-end; gap: 6rpx; margin-left: auto; max-width: 100%; }
.recent-records__value { color: var(--nerae-text); font-size: 28rpx; font-variant-numeric: tabular-nums; line-height: 1.4; text-align: right; }
.recent-records__retry { margin: 0; background: var(--nerae-surface-subtle); color: var(--nerae-text); font-size: 24rpx; }
.recent-records__retry::after { border: 0; }
</style>
