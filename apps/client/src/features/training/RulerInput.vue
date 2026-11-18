<script setup lang="ts">
import { computed, ref } from 'vue'
import { valueAfterRulerDrag } from './ruler'

const props = withDefaults(defineProps<{
  label: string
  unit: string
  value: string
  kind: 'digit' | 'number'
  step?: number
}>(), { step: 1 })
const emit = defineEmits<{ change: [value: string] }>()
const focused = ref(false)
const preview = ref<string | null>(null)
let startX: number | null = null
let initialValue = ''
let tickWidth = 16
const displayed = computed(() => preview.value ?? props.value)

function inputValue(event: unknown): string {
  if (!event || typeof event !== 'object' || !('detail' in event)) return ''
  const detail = event.detail
  return detail && typeof detail === 'object' && 'value' in detail && typeof detail.value === 'string' ? detail.value : ''
}

function xOf(event: unknown): number | null {
  if (!event || typeof event !== 'object' || !('touches' in event)) return null
  const touches = event.touches
  if (!touches || typeof touches !== 'object' || !('length' in touches) || touches.length !== 1 || !('0' in touches)) return null
  const touch = touches[0]
  if (!touch || typeof touch !== 'object') return null
  const value = 'clientX' in touch ? touch.clientX : 'pageX' in touch ? touch.pageX : null
  return typeof value === 'number' && Number.isFinite(value) ? value : null
}

function startDrag(event: unknown): void {
  startX = xOf(event)
  initialValue = props.value
  preview.value = null
  try { tickWidth = Math.max(1, uni.getSystemInfoSync().windowWidth * 0.9 / 21) } catch { tickWidth = 16 }
}

function moveDrag(event: unknown): void {
  const x = xOf(event)
  if (startX === null || x === null) return
  preview.value = valueAfterRulerDrag(initialValue, x - startX, tickWidth, props.step)
}

function finishDrag(): void {
  if (startX !== null && preview.value !== null && preview.value !== props.value) emit('change', preview.value)
  startX = null
  preview.value = null
}

function markLabel(index: number): string {
  if (index % 5 !== 0 || !displayed.value.trim()) return ''
  const center = Number(displayed.value)
  if (!Number.isFinite(center)) return ''
  const result = center + (index - 10) * props.step
  if (result < 0 || !Number.isFinite(result)) return ''
  return String(Number(result.toFixed(4)))
}
</script>

<template>
  <view class="ruler-field">
    <view class="ruler-field__heading">
      <text class="ruler-field__label">{{ label }}</text>
      <text class="ruler-field__step">每格 {{ step }} {{ unit }}</text>
    </view>
    <view class="ruler-field__value" :class="{ 'ruler-field__value--focused': focused }">
      <input class="ruler-field__input" :type="kind" :value="displayed" placeholder="未填"
        :aria-label="`${label}，${unit}`" @focus="focused = true" @blur="focused = false"
        @input="emit('change', inputValue($event))" />
      <text class="ruler-field__unit">{{ unit }}</text>
    </view>
    <view class="ruler" aria-hidden="true" @touchstart="startDrag" @touchmove.stop.prevent="moveDrag"
      @touchend="finishDrag" @touchcancel="finishDrag">
      <view v-for="index in 21" :key="index" class="ruler__mark">
        <view class="ruler__line" :class="{ 'ruler__line--major': (index - 1) % 5 === 0, 'ruler__line--center': index === 11 }" />
        <text class="ruler__number">{{ markLabel(index - 1) }}</text>
      </view>
    </view>
  </view>
</template>

<style scoped>
.ruler-field { display: flex; flex-direction: column; gap: 8rpx; }
.ruler-field__heading { display: flex; justify-content: space-between; gap: 20rpx; }
.ruler-field__label { color: var(--nerae-text); font-size: 25rpx; font-weight: 600; }
.ruler-field__step { color: var(--nerae-text-secondary); font-size: 22rpx; }
.ruler-field__value { display: flex; min-height: 84rpx; align-items: baseline; justify-content: center; gap: 8rpx; border: 2rpx solid transparent; border-radius: 14rpx; }
.ruler-field__value--focused { border-color: var(--nerae-border-control); }
.ruler-field__input { width: 200rpx; height: 84rpx; color: var(--nerae-text); font-size: 50rpx; font-weight: 650; line-height: 84rpx; text-align: right; }
.ruler-field__unit { color: var(--nerae-text-secondary); font-size: 25rpx; }
.ruler { display: flex; height: 78rpx; align-items: flex-start; touch-action: pan-y; }
.ruler__mark { position: relative; display: flex; flex: 1; min-width: 0; height: 78rpx; flex-direction: column; align-items: center; justify-content: flex-start; }
.ruler__line { width: 1px; height: 16rpx; margin-top: 13rpx; background: var(--nerae-border-control); }
.ruler__line--major { height: 29rpx; margin-top: 6rpx; }
.ruler__line--center { width: 3px; height: 40rpx; margin-top: 0; border-radius: 2px; background: var(--nerae-text); }
.ruler__number { position: absolute; top: 43rpx; color: var(--nerae-text-secondary); font-size: 20rpx; white-space: nowrap; }
</style>
