<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import SwipeableSetCard from './SwipeableSetCard.vue'
import type { RecordingDraft } from './recording'
import { describeDraftSet } from './recordingV2Measure'
import type { RecordingV2Detail } from './recordingV2Projection'

const props = defineProps<{ draft: RecordingDraft; statusMessage: string; busy: boolean; cloud: RecordingV2Detail | null }>()
const emit = defineEmits<{
  edit: []
  'edit-set': [exercise: string, record: string]
  'copy-set': [exercise: string, record: string]
  'remove-set': [exercise: string, record: string]
  'restore-set': [exercise: string, record: string]
}>()

const activeActions = computed(() => new Set(props.draft.exercises.filter(item => item.sets.length > 0).map(item =>
  JSON.stringify(item.identity.source === 'custom' ? ['custom', item.identity.exerciseName] : ['catalog', item.identity.exerciseId]))).size)
const groupCount = computed(() => props.draft.exercises.reduce((count, item) => count + item.sets.length, 0))
const revealed = ref('')
const expanded = ref<string[]>([])
const undo = ref<{ exercise: string; record: string } | null>(null)
const awaitingRemoval = ref<{ exercise: string; record: string } | null>(null)
const offscreen = ref<number[]>([])
const scrollTarget = ref('')
let undoTimer: ReturnType<typeof setTimeout> | null = null
let measureTimer: ReturnType<typeof setTimeout> | null = null
let alive = true

function remove(exercise: string, record: string): void {
  awaitingRemoval.value = { exercise, record }
  revealed.value = ''
  emit('remove-set', exercise, record)
}
function restore(exercise: string, record: string): void {
  if (undo.value?.record === record) undo.value = null
  emit('restore-set', exercise, record)
}
function toggleRemoved(key: string): void {
  expanded.value = expanded.value.includes(key) ? expanded.value.filter(item => item !== key) : [...expanded.value, key]
  scheduleMeasure()
}
function formatKg(value: string): string { return value.replace(/\.00$/, '').replace(/(\.\d)0$/, '$1') }
function volume(key: string): string {
  const summary = props.cloud?.entries.find(entry => entry.exerciseEntryKey === key)?.volume
  if (!summary) return ''
  const parts: string[] = []
  if (summary.calculatedKg !== undefined) parts.push(`已计算 ${formatKg(summary.calculatedKg)} kg`)
  if (summary.partialSets) parts.push(`${summary.partialSets} 组仅计部分`)
  if (summary.excludedSets) parts.push(`${summary.excludedSets} 组未计入`)
  const group = props.cloud?.actionVolumes?.find(item => item.exerciseEntryKeys.includes(key))
  return parts.length && group && group.exerciseEntryKeys.length > 1 ? `本段：${parts.join('，')}` : parts.join('，')
}
function actionVolume(key: string): string {
  const group = props.cloud?.actionVolumes?.find(item => item.exerciseEntryKeys.includes(key))
  if (!group || group.exerciseEntryKeys.length < 2 || group.exerciseEntryKeys[group.exerciseEntryKeys.length - 1] !== key) return ''
  const summary = group.volume
  const parts = [summary.calculatedKg === undefined ? '暂无可计算数据' : `${formatKg(summary.calculatedKg)} kg`]
  if (summary.partialSets) parts.push(`${summary.partialSets} 组仅计部分`)
  if (summary.excludedSets) parts.push(`${summary.excludedSets} 组未计入`)
  return `本动作累计 ${parts.join('，')}`
}
type Bounds = { top?: number; bottom?: number }
function scheduleMeasure(): void {
  if (measureTimer) return
  measureTimer = setTimeout(() => {
    measureTimer = null
    void nextTick(() => {
      if (!alive || typeof uni === 'undefined' || typeof uni.createSelectorQuery !== 'function') return
      const query = uni.createSelectorQuery()
      query.select('.completed__list').boundingClientRect()
      query.selectAll('.completed__action').boundingClientRect()
      query.exec(result => {
        if (!alive || !Array.isArray(result)) return
        const viewport = result[0] as Bounds | null
        const sections = result[1] as Bounds[] | null
        if (!viewport || !Array.isArray(sections) || typeof viewport.top !== 'number' || typeof viewport.bottom !== 'number') return
        offscreen.value = sections.flatMap((item, index) => typeof item.top === 'number' && typeof item.bottom === 'number' &&
          (item.bottom <= viewport.top! || item.top >= viewport.bottom!) ? [index] : [])
      })
    })
  }, 80)
}
function jump(index: number): void { scrollTarget.value = `completed-action-${index}` }

watch(() => props.draft, value => {
  const target = awaitingRemoval.value
  if (target && value.exercises.find(item => item.exerciseEntryKey === target.exercise)?.removedSets?.some(item => item.set.recordKey === target.record)) {
    awaitingRemoval.value = null
    undo.value = target
    if (undoTimer) clearTimeout(undoTimer)
    undoTimer = setTimeout(() => { undo.value = null; undoTimer = null }, 5000)
  }
  scheduleMeasure()
})
onMounted(scheduleMeasure)
onBeforeUnmount(() => {
  alive = false
  if (undoTimer) clearTimeout(undoTimer)
  if (measureTimer) clearTimeout(measureTimer)
})
</script>

<template>
  <view class="completed">
    <view class="completed__heading">
      <view>
        <text class="completed__title">已完成</text>
        <text class="completed__status">{{ statusMessage }}</text>
      </view>
      <button class="completed__edit" :disabled="busy" @tap="emit('edit')">编辑</button>
    </view>
    <view class="completed__summary">
      <text><text class="completed__summary-number">{{ activeActions }}</text> 个动作</text>
      <text><text class="completed__summary-number">{{ groupCount }}</text> 组</text>
    </view>
    <scroll-view v-if="offscreen.length" class="completed__navigation" scroll-x>
      <view class="completed__navigation-inner">
        <button v-for="index in offscreen" :key="draft.exercises[index].exerciseEntryKey" @tap="jump(index)">
          {{ draft.exercises[index].identity.source === 'custom' ? draft.exercises[index].identity.exerciseName : draft.exercises[index].resolvedExerciseName || '动作' }}
        </button>
      </view>
    </scroll-view>
    <scroll-view class="completed__list" scroll-y scroll-with-animation :scroll-into-view="scrollTarget" @scroll="scheduleMeasure">
      <view v-for="(exercise, actionIndex) in draft.exercises" :id="`completed-action-${actionIndex}`"
        :key="exercise.exerciseEntryKey" class="completed__action">
        <text class="completed__action-name">{{ exercise.identity.source === 'custom' ? exercise.identity.exerciseName : exercise.resolvedExerciseName || '动作' }}</text>
        <text v-if="exercise.sets.length === 0" class="completed__empty">当前 0 组</text>
        <SwipeableSetCard v-for="(set, index) in exercise.sets" :key="set.recordKey"
          :open="revealed === set.recordKey" :removable="true" :busy="busy" :label="`第 ${index + 1} 组`"
          @reveal="revealed = set.recordKey" @dismiss="revealed = ''" @edit="emit('edit-set', exercise.exerciseEntryKey, set.recordKey)"
          @copy="emit('copy-set', exercise.exerciseEntryKey, set.recordKey)" @remove="remove(exercise.exerciseEntryKey, set.recordKey)">
          <text class="completed__set-number">第 {{ index + 1 }} 组</text>
          <text class="completed__set-value">{{ describeDraftSet(exercise, set) }}</text>
        </SwipeableSetCard>
        <text v-if="volume(exercise.exerciseEntryKey)" class="completed__volume">{{ volume(exercise.exerciseEntryKey) }}</text>
        <text v-if="actionVolume(exercise.exerciseEntryKey)" class="completed__volume">{{ actionVolume(exercise.exerciseEntryKey) }}</text>
        <view v-if="exercise.removedSets?.length" class="completed__removed">
          <button class="completed__removed-toggle" @tap="toggleRemoved(exercise.exerciseEntryKey)">已移除 {{ exercise.removedSets.length }} 组</button>
          <view v-if="expanded.includes(exercise.exerciseEntryKey)">
            <view v-for="item in exercise.removedSets" :key="item.set.recordKey" class="completed__removed-row">
              <text>移除时为第 {{ item.removedPosition }} 组 · {{ describeDraftSet(exercise, item.set) }}</text>
              <button :disabled="busy" @tap="restore(exercise.exerciseEntryKey, item.set.recordKey)">恢复</button>
            </view>
          </view>
        </view>
      </view>
      <view class="completed__bottom-space" />
    </scroll-view>
    <view v-if="undo" class="completed__undo" role="status">
      <text>已移除一组</text><button :disabled="busy" @tap="restore(undo.exercise, undo.record)">撤销</button>
    </view>
  </view>
</template>

<style scoped>
.completed { height: calc(100vh - 88rpx - env(safe-area-inset-top)); display: flex; flex-direction: column; color: var(--nerae-text); background: var(--nerae-page); }
.completed__heading { display: flex; align-items: flex-start; justify-content: space-between; padding: 32rpx 36rpx 24rpx; }
.completed__heading > view { display: flex; flex-direction: column; gap: 10rpx; }
.completed__title { font-family: "Songti SC", "Noto Serif SC", serif; font-size: 40rpx; font-weight: 700; }
.completed__status, .completed__empty { color: var(--nerae-text-secondary); font-size: 23rpx; }
.completed__edit, .completed__navigation button, .completed__removed button, .completed__undo button { margin: 0; padding: 0; background: transparent; color: var(--nerae-text); font-size: 24rpx; line-height: 1.6; }
.completed__summary { display: flex; gap: 54rpx; margin: 0 36rpx 22rpx; padding: 22rpx 24rpx; border: 1px solid var(--nerae-border-decorative); border-radius: 18rpx; font-size: 25rpx; }
.completed__summary-number { font-size: 32rpx; font-weight: 700; }
.completed__navigation { flex: none; width: 100%; white-space: nowrap; }
.completed__navigation-inner { display: flex; gap: 12rpx; padding: 0 36rpx 18rpx; }
.completed__navigation button { flex: none; padding: 8rpx 18rpx; border-radius: 14rpx; background: var(--nerae-surface-subtle); }
.completed__list { flex: 1; min-height: 0; box-sizing: border-box; padding: 0 36rpx; }
.completed__action { display: flex; flex-direction: column; gap: 14rpx; padding: 22rpx 0 18rpx; }
.completed__action-name { padding-bottom: 6rpx; font-family: "Songti SC", "Noto Serif SC", serif; font-size: 31rpx; font-weight: 700; }
.completed__set-number { flex: none; color: var(--nerae-text-secondary); font-size: 24rpx; }
.completed__set-value { flex: 1; font-size: 27rpx; font-weight: 600; }
.completed__volume { color: var(--nerae-text-secondary); font-size: 23rpx; padding: 4rpx 0 0; }
.completed__removed-row { display: flex; align-items: center; justify-content: space-between; gap: 16rpx; color: var(--nerae-text-secondary); font-size: 22rpx; }
.completed__bottom-space { height: calc(32rpx + env(safe-area-inset-bottom)); }
.completed__undo { position: absolute; left: 36rpx; right: 36rpx; bottom: calc(24rpx + env(safe-area-inset-bottom)); display: flex; justify-content: space-between; padding: 20rpx 24rpx; border-radius: 18rpx; background: var(--nerae-primary); color: var(--nerae-primary-text); font-size: 24rpx; }
.completed__undo button { color: var(--nerae-primary-text); }
button::after { border: 0; }
</style>
