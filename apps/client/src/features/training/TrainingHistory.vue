<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, shallowRef } from 'vue'
import type { AuthSessionCoordinator } from '../auth/sessionCoordinator'
import { RecordingApiError, type RecordingApi, type ServerSet, type WorkoutSessionDetail } from './recordingApi'
import type { RecordingV2Detail } from './recordingV2Projection'
import type { RecordingV2Page } from './recordingV2Response'

const props = defineProps<{ session: AuthSessionCoordinator; api: RecordingApi }>()
const emit = defineEmits<{ 'session-changed': []; 'edit-v2': [id: string, clientEventId: string] }>()
const owner = props.session.snapshot()
const items = shallowRef<RecordingV2Page['items']>([])
const selected = shallowRef<{ kind: 'legacy'; detail: WorkoutSessionDetail } | { kind: 'v2'; detail: RecordingV2Detail } | null>(null)
const v2Sections = computed(() => {
  if (selected.value?.kind !== 'v2') return []
  const detail = selected.value.detail
  return detail.entries.filter(entry => entry.removedPosition === undefined).map(entry => ({
    ...entry, sets: detail.sets.filter(set => set.exerciseEntryKey === entry.exerciseEntryKey),
  }))
})
const v2SetCount = computed(() => v2Sections.value.reduce((total, entry) => total + entry.sets.length, 0))
const loading = ref(false)
const error = ref('')
const page = ref(0)
const hasMore = ref(false)
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
  items.value = []
  selected.value = null
  hasMore.value = false
  emit('session-changed')
}

function validLease(lease: { subjectId: string; generation: string }): boolean {
  return Boolean(owner && lease.subjectId === owner.subjectId && lease.generation === owner.generation && current())
}

function failed(problem: unknown): void {
  if (!current()) return
  if (problem instanceof RecordingApiError &&
    (problem.code === 'unauthenticated' || problem.code === 'session_changed' || problem.code === 'forbidden')) {
    invalidate()
  } else {
    error.value = '暂时无法读取云端记录，请稍后重试。'
  }
}

async function load(reset = false): Promise<void> {
  if (loading.value || !current() || (!reset && !hasMore.value)) return
  loading.value = true
  error.value = ''
  const next = reset ? 1 : page.value + 1
  try {
    const result = await props.api.listRecordingsV2({ page: next, pageSize: 20 })
    if (!validLease(result.lease)) return
    const previous = reset ? [] : items.value
    items.value = [...new Map([...previous, ...result.items].map(item => [item.id, item])).values()]
    page.value = result.page
    hasMore.value = result.page * result.pageSize < result.total
  } catch (problem) { failed(problem) }
  finally { if (alive) loading.value = false }
}

async function open(item: RecordingV2Page['items'][number]): Promise<void> {
  if (loading.value || !current()) return
  loading.value = true
  error.value = ''
  try {
    if (item.recordFormat === 2) {
      const result = await props.api.readRecordingV2(item.id)
      if (validLease(result.lease)) selected.value = { kind: 'v2', detail: result.detail }
    } else {
      const result = await props.api.readSession(item.id)
      if (validLease(result.lease)) selected.value = { kind: 'legacy', detail: result.detail }
    }
  } catch (problem) { failed(problem) }
  finally { if (alive) loading.value = false }
}

function description(set: ServerSet): string {
  const values: string[] = []
  if (set.weightKg !== undefined) values.push(`${set.weightKg} kg`)
  if (set.reps !== undefined) values.push(`${set.reps} 次`)
  if (set.seconds !== undefined) values.push(`${set.seconds} 秒`)
  return values.length ? values.join(' · ') : '已保存'
}

function v2Description(set: RecordingV2Detail['sets'][number]): string {
  const value = set.measurement
  const weight = (item: string | undefined): string => item === undefined ? '重量未填' : `${item} kg`
  const reps = (item: number | undefined): string => item === undefined ? '次数未填' : `${item} 次`
  const side = (item: typeof value.left): string => {
    if (!item || item.state === 'unknown') return '未填写'
    if (item.state === 'not_performed') return '未做'
    return `${weight(item.weightKg)} × ${reps(item.reps)}`
  }
  if (value.method === 'sides') return `左 ${side(value.left)} · 右 ${side(value.right)}`
  if (value.method === 'pair_each') return `每只 ${weight(value.weightKg)} × ${reps(value.reps)}`
  if (value.method === 'bodyweight') return `自重 ${weight(value.bodyweightKg)} × ${reps(value.reps)}`
  if (value.method === 'bodyweight_extra' || value.method === 'bodyweight_assisted') {
    const label = value.method === 'bodyweight_extra' ? '负重' : '助力'
    return `自重 ${weight(value.bodyweightKg)} · ${label} ${weight(value.adjustmentKg)} × ${reps(value.reps)}`
  }
  return `${weight(value.weightKg)} × ${reps(value.reps)}`
}

function volumeDescription(volume: NonNullable<RecordingV2Detail['entries'][number]['volume']>): string {
  const parts: string[] = []
  if (volume.calculatedKg !== undefined) {
    const amount = volume.calculatedKg.replace(/\.00$/, '').replace(/(\.\d)0$/, '$1')
    parts.push(`已计算 ${amount} kg`)
  }
  if (volume.partialSets > 0) parts.push(`${volume.partialSets} 组仅计部分`)
  if (volume.excludedSets > 0) parts.push(`${volume.excludedSets} 组未计入`)
  return parts.join('，')
}

onMounted(() => {
  if (!current()) { invalidate(); return }
  try {
    const guard = props.session.captureInstallGuard()
    if (!guard) { invalidate(); return }
    unsubscribe = props.session.subscribeInstallGuardInvalidation(guard, invalidate)
    if (!current()) { invalidate(); return }
    void load(true)
  } catch { invalidate() }
})
onBeforeUnmount(() => {
  alive = false
  unsubscribe?.()
  unsubscribe = null
  items.value = []
  selected.value = null
})
</script>

<template>
  <view class="training-history">
    <text class="training-history__title">云端训练记录</text>
    <text class="training-history__caption">显示已保存到账号的训练，本机草稿单独保留。</text>
    <text v-if="error" class="training-history__error" role="alert">{{ error }}</text>
    <template v-if="selected">
      <view class="history-detail">
        <text class="history-detail__date">{{ selected.detail.date }}</text>
        <template v-if="selected.kind === 'legacy'">
          <text v-if="selected.detail.sets.length === 0" class="training-history__caption">这条记录暂无已保存的组。</text>
          <view v-for="set in selected.detail.sets" :key="set.recordKey" class="history-detail__set">
            <text>{{ set.resolvedExerciseName || set.exerciseName || '动作' }} · 第 {{ set.setIndex }} 组</text>
            <text>{{ description(set) }}</text>
          </view>
        </template>
        <template v-else>
          <text class="training-history__caption">{{ v2Sections.length }} 个动作 · {{ v2SetCount }} 组</text>
          <text v-if="v2Sections.length === 0" class="training-history__caption">这条记录暂无动作。</text>
          <view v-for="entry in v2Sections" :key="entry.exerciseEntryKey" class="history-detail__action">
            <text class="history-detail__action-name">{{ entry.exerciseName }}</text>
            <text v-if="entry.sets.length === 0" class="training-history__caption">暂无记录组</text>
            <view v-for="(set, index) in entry.sets" :key="set.recordKey" class="history-detail__set">
              <text>第 {{ index + 1 }} 组</text>
              <text>{{ v2Description(set) }}</text>
            </view>
            <text v-if="entry.volume &amp;&amp; volumeDescription(entry.volume)" class="history-detail__volume">{{ volumeDescription(entry.volume) }}</text>
          </view>
        </template>
      </view>
      <button v-if="selected.kind === 'v2'" class="history-button" :disabled="loading"
        @tap="emit('edit-v2', selected.detail.id, selected.detail.clientEventId)">编辑记录</button>
      <button class="history-button" :disabled="loading" @tap="selected = null">返回云端列表</button>
    </template>
    <template v-else>
      <text v-if="loading && items.length === 0" class="training-history__caption">正在读取云端记录…</text>
      <text v-else-if="!error && items.length === 0" class="training-history__caption">还没有已保存到云端的训练。</text>
      <view v-for="item in items" :key="item.id" class="history-row">
        <view class="history-row__text">
          <text>{{ item.date }}</text>
          <text class="training-history__caption">{{ item.status === 'completed' ? '已完成' : item.status === 'skipped' ? '未训练' : '未完成' }}</text>
        </view>
        <button class="history-button" :disabled="loading" @tap="open(item)">查看</button>
      </view>
      <button v-if="hasMore" class="history-button" :disabled="loading" @tap="load()">加载更多云端记录</button>
      <button v-if="error" class="history-button" :disabled="loading" @tap="load(true)">重试云端记录</button>
    </template>
  </view>
</template>

<style scoped>
.training-history { display: flex; flex-direction: column; gap: 16rpx; padding: 28rpx 0 8rpx; }
.training-history__title { font-size: 29rpx; font-weight: 600; }
.training-history__caption { color: var(--nerae-text-secondary); font-size: 23rpx; line-height: 1.5; }
.training-history__error { color: var(--nerae-text); font-size: 24rpx; }
.history-row, .history-detail { display: flex; gap: 16rpx; padding: 24rpx; border: 1px solid var(--nerae-border-decorative); border-radius: 20rpx; background: var(--nerae-surface); }
.history-row { align-items: center; justify-content: space-between; }
.history-row__text, .history-detail { display: flex; flex-direction: column; }
.history-row__text { gap: 8rpx; }
.history-detail__date { font-size: 28rpx; font-weight: 600; }
.history-detail__action { display: flex; flex-direction: column; gap: 12rpx; }
.history-detail__action-name { font-weight: 600; }
.history-detail__volume { padding: 16rpx 0 0; color: var(--nerae-text-secondary); font-size: 23rpx; }
.history-detail__set { display: flex; flex-direction: column; gap: 6rpx; color: var(--nerae-text); font-size: 24rpx; }
.history-button { margin: 0; background: var(--nerae-surface-subtle); color: var(--nerae-text); font-size: 24rpx; }
.history-button::after { border: 0; }
</style>
