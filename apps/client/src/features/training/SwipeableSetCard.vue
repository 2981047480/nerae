<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'

const props = defineProps<{ open: boolean; removable: boolean; busy: boolean; label: string }>()
const emit = defineEmits<{ reveal: []; dismiss: []; edit: []; copy: []; remove: [] }>()
const TRACK_WIDTH = 80
const dragOffset = ref<number | null>(null)
let start: { x: number; y: number; base: number } | null = null
let horizontal = false
let suppressTap = false
let suppressHandle: ReturnType<typeof setTimeout> | null = null

const offset = computed(() => dragOffset.value === null ? (props.open ? -TRACK_WIDTH : 0) : dragOffset.value)
function point(event: unknown, field: 'touches' | 'changedTouches'): { x: number; y: number } | null {
  if (!event || typeof event !== 'object' || !(field in event)) return null
  const touches = (event as Record<string, unknown>)[field]
  if (!touches || typeof touches !== 'object' || !('length' in touches) || touches.length !== 1 || !('0' in touches)) return null
  const item = touches[0]
  if (!item || typeof item !== 'object' || !('clientX' in item) || !('clientY' in item)) return null
  return typeof item.clientX === 'number' && Number.isFinite(item.clientX) && typeof item.clientY === 'number' && Number.isFinite(item.clientY)
    ? { x: item.clientX, y: item.clientY } : null
}
function begin(event: unknown): void {
  const touch = point(event, 'touches')
  if (!touch || !props.removable || props.busy) { start = null; return }
  start = { ...touch, base: props.open ? -TRACK_WIDTH : 0 }
  horizontal = false
}
function move(event: unknown): void {
  const touch = point(event, 'touches')
  if (!start || !touch) return
  const dx = touch.x - start.x
  const dy = touch.y - start.y
  if (!horizontal && Math.abs(dy) > 8 && Math.abs(dy) >= Math.abs(dx)) { start = null; return }
  if (!horizontal && Math.abs(dx) > 8 && Math.abs(dx) > Math.abs(dy) * 1.2) horizontal = true
  if (horizontal) dragOffset.value = Math.max(-TRACK_WIDTH, Math.min(0, start.base + dx))
}
function end(): void {
  if (horizontal && dragOffset.value !== null) {
    if (dragOffset.value < -TRACK_WIDTH / 2) emit('reveal')
    else emit('dismiss')
    suppressTap = true
    if (suppressHandle) clearTimeout(suppressHandle)
    suppressHandle = setTimeout(() => { suppressTap = false; suppressHandle = null }, 400)
  }
  start = null
  horizontal = false
  dragOffset.value = null
}
function cancel(): void { start = null; horizontal = false; dragOffset.value = null }
function activate(): void {
  if (suppressTap) { suppressTap = false; return }
  if (props.open) emit('dismiss')
  else emit('edit')
}
function revealByLongPress(): void {
  if (!props.removable || props.busy) return
  suppressTap = true
  if (suppressHandle) clearTimeout(suppressHandle)
  suppressHandle = setTimeout(() => { suppressTap = false; suppressHandle = null }, 400)
  emit('reveal')
}
function copy(): void {
  if (suppressTap) { suppressTap = false; return }
  if (!props.busy && !props.open) emit('copy')
}
watch(() => props.busy || !props.removable, disabled => { if (disabled) cancel() })
onBeforeUnmount(() => { if (suppressHandle) clearTimeout(suppressHandle) })
</script>

<template>
  <view class="swipe-card" @touchstart="begin" @touchmove="move" @touchend="end" @touchcancel="cancel">
    <view class="swipe-card__rail">
      <button class="swipe-card__remove" :disabled="!open || busy || !removable"
        :aria-hidden="!open" :aria-label="`移除${label}`" @tap.stop="emit('remove')">移除</button>
    </view>
    <button class="swipe-card__face" :class="{ 'swipe-card__face--dragging': dragOffset !== null }"
      :style="{ transform: `translateX(${offset}px)` }"
      :aria-label="`${label}，${removable ? '点击编辑，长按显示移除' : '点击编辑'}`"
      @tap="activate" @longpress="revealByLongPress">
      <view class="swipe-card__contents" :class="{ 'swipe-card__contents--dragging': dragOffset !== null }"
        :style="{ transform: `translateX(${-offset}px)` }"><slot /></view>
    </button>
    <button v-if="!open &amp;&amp; dragOffset === null" class="swipe-card__copy" :disabled="busy"
      :aria-label="`复制${label}`" @tap.stop="copy"><view class="swipe-card__copy-icon"><view /></view></button>
  </view>
</template>

<style scoped>
.swipe-card { position: relative; width: 100%; min-width: 0; overflow: hidden; border-radius: 18rpx; touch-action: pan-y; }
.swipe-card__rail { position: absolute; inset: 0; display: flex; justify-content: flex-end; background: var(--nerae-surface-subtle); border-radius: 18rpx; }
.swipe-card__remove { display: flex; width: 80px; min-height: 96rpx; align-items: center; justify-content: center; margin: 0; padding: 0; border: 0; border-radius: 0; background: transparent; color: var(--nerae-text); font-size: 25rpx; line-height: 1.2; }
.swipe-card__remove[disabled] { opacity: 1; color: var(--nerae-text); }
.swipe-card__face { position: relative; z-index: 1; display: flex; width: 100%; min-width: 0; min-height: 96rpx; align-items: center; overflow: hidden; margin: 0; padding: 18rpx 84rpx 18rpx 20rpx; box-sizing: border-box; border: 1px solid var(--nerae-border-decorative); border-radius: 18rpx; background: var(--nerae-surface); color: var(--nerae-text); line-height: 1.2; text-align: left; transition: transform 190ms ease-out; }
.swipe-card__face:active { background: var(--nerae-surface-pressed); }
.swipe-card__contents { display: flex; flex: 1; min-width: 0; align-items: center; gap: 18rpx; transition: transform 190ms ease-out; }
.swipe-card__face--dragging, .swipe-card__contents--dragging { transition: none; }
.swipe-card__copy { position: absolute; z-index: 2; top: 0; right: 0; display: flex; width: 80rpx; height: 100%; min-height: 96rpx; align-items: center; justify-content: center; margin: 0; padding: 0; background: transparent; color: var(--nerae-text-secondary); }
.swipe-card__copy[disabled] { opacity: 0.45; }
.swipe-card__copy-icon { position: relative; width: 28rpx; height: 28rpx; border: 1.5px solid currentColor; border-radius: 4rpx; }
.swipe-card__copy-icon view { position: absolute; top: -7rpx; right: -7rpx; width: 24rpx; height: 24rpx; border: 1.5px solid currentColor; border-radius: 4rpx; background: var(--nerae-surface); }
button::after { border: 0; }
@media (prefers-reduced-motion: reduce) { .swipe-card__face, .swipe-card__contents { transition: none; } }
</style>
