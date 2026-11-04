<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref, shallowRef } from 'vue'
import type { AuthSessionCoordinator } from '../auth/sessionCoordinator'
import { RecordingApiError, type RecordingApi, type ServerSet, type WorkoutSessionDetail, type WorkoutSessionSummary } from './recordingApi'

const props = defineProps<{ session: AuthSessionCoordinator; api: RecordingApi }>()
const emit = defineEmits<{ 'session-changed': [] }>()
const owner = props.session.snapshot()
const items = shallowRef<WorkoutSessionSummary[]>([])
const selected = shallowRef<WorkoutSessionDetail | null>(null)
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
    const result = await props.api.listSessions({ page: next, pageSize: 20 })
    if (!validLease(result.lease)) return
    const previous = reset ? [] : items.value
    items.value = [...new Map([...previous, ...result.items].map(item => [item.id, item])).values()]
    page.value = result.page
    hasMore.value = result.page * result.pageSize < result.total
  } catch (problem) { failed(problem) }
  finally { if (alive) loading.value = false }
}

async function open(id: string): Promise<void> {
  if (loading.value || !current()) return
  loading.value = true
  error.value = ''
  try {
    const result = await props.api.readSession(id)
    if (validLease(result.lease)) selected.value = result.detail
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
        <text class="history-detail__date">{{ selected.date }}</text>
        <text v-if="selected.sets.length === 0" class="training-history__caption">这条记录暂无已保存的组。</text>
        <view v-for="set in selected.sets" :key="set.recordKey" class="history-detail__set">
          <text>{{ set.resolvedExerciseName || set.exerciseName || '动作' }} · 第 {{ set.setIndex }} 组</text>
          <text>{{ description(set) }}</text>
        </view>
      </view>
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
        <button class="history-button" :disabled="loading" @tap="open(item.id)">查看</button>
      </view>
      <button v-if="hasMore" class="history-button" :disabled="loading" @tap="load()">加载更多云端记录</button>
      <button v-if="error" class="history-button" :disabled="loading" @tap="load(true)">重试云端记录</button>
    </template>
  </view>
</template>

<style scoped>
.training-history { display: flex; flex-direction: column; gap: 16rpx; padding: 28rpx 0 8rpx; }
.training-history__title { font-size: 29rpx; font-weight: 600; }
.training-history__caption { color: #707a73; font-size: 23rpx; line-height: 1.5; }
.training-history__error { color: #9c3c2d; font-size: 24rpx; }
.history-row, .history-detail { display: flex; gap: 16rpx; padding: 24rpx; border-radius: 20rpx; background: #fff; }
.history-row { align-items: center; justify-content: space-between; }
.history-row__text, .history-detail { display: flex; flex-direction: column; }
.history-row__text { gap: 8rpx; }
.history-detail__date { font-size: 28rpx; font-weight: 600; }
.history-detail__set { display: flex; flex-direction: column; gap: 6rpx; color: #35443b; font-size: 24rpx; }
.history-button { margin: 0; background: #fff; color: #35443b; font-size: 24rpx; }
.history-button::after { border: 0; }
</style>
