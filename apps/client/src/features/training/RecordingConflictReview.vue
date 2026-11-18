<script setup lang="ts">
import { computed } from 'vue'
import type { RecordingConflictReview } from './recordingV2Conflict'
import type { ConflictChoice } from './recordingV2ConflictPlan'
const props = defineProps<{ review:RecordingConflictReview; choices:Record<string, ConflictChoice>; busy:boolean; error:string }>()
const emit = defineEmits<{ choose:[key:string, value:ConflictChoice]; cancel:[]; confirm:[] }>()
const complete = computed(() => props.review.rows.every(row => props.choices[row.key] === 'local' || props.choices[row.key] === 'cloud'))
</script>

<template>
  <view class="conflict-review">
    <view class="conflict-review__intro">
      <text class="conflict-review__title">检查记录差异</text>
      <text class="conflict-review__caption">本机和另一台设备的内容不同，请选择每一项要保留的内容。</text>
      <text v-if="review.hasCloudAdditions" class="conflict-review__caption">另一台设备新增的动作和组会一并保留。</text>
      <text v-if="error" class="conflict-review__error" role="alert">{{ error }}</text>
    </view>
    <scroll-view class="conflict-review__list" scroll-y>
      <text v-if="review.rows.length === 0" class="conflict-review__empty">两边的内容一致，可以继续同步。</text>
      <view v-for="row in review.rows" :key="row.key" class="conflict-review__item">
        <text class="conflict-review__label">{{ row.label }}</text>
        <view class="conflict-review__options">
          <button class="conflict-review__option" :class="{ 'conflict-review__option--selected': choices[row.key] === 'local' }"
            :aria-pressed="choices[row.key] === 'local'" :disabled="busy" @tap="emit('choose',row.key,'local')">
            <view class="conflict-review__option-heading"><text class="conflict-review__source">本机</text>
              <text v-if="choices[row.key] === 'local'" class="conflict-review__selected">✓ 已选</text></view>
            <text class="conflict-review__value">{{ row.local }}</text>
          </button>
          <button class="conflict-review__option" :class="{ 'conflict-review__option--selected': choices[row.key] === 'cloud' }"
            :aria-pressed="choices[row.key] === 'cloud'" :disabled="busy" @tap="emit('choose',row.key,'cloud')">
            <view class="conflict-review__option-heading"><text class="conflict-review__source">另一台设备</text>
              <text v-if="choices[row.key] === 'cloud'" class="conflict-review__selected">✓ 已选</text></view>
            <text class="conflict-review__value">{{ row.cloud }}</text>
          </button>
        </view>
      </view>
    </scroll-view>
    <view class="conflict-review__actions">
      <button class="conflict-review__cancel" :disabled="busy" @tap="emit('cancel')">稍后处理</button>
      <button class="conflict-review__confirm" :disabled="busy || !complete" @tap="emit('confirm')">{{ busy ? '正在保存…' : '保存选择' }}</button>
    </view>
  </view>
</template>

<style scoped>
.conflict-review { display:flex; flex-direction:column; height:calc(100vh - 88rpx - env(safe-area-inset-top)); background:var(--nerae-surface); color:var(--nerae-text); }
.conflict-review__intro { display:flex; flex-direction:column; gap:14rpx; padding:36rpx 36rpx 24rpx; }
.conflict-review__title { font-family:"Songti SC", "Noto Serif SC", serif; font-size:40rpx; font-weight:700; }
.conflict-review__caption, .conflict-review__empty, .conflict-review__error { color:var(--nerae-text-secondary); font-size:24rpx; line-height:1.6; }
.conflict-review__error { color:var(--nerae-text); }
.conflict-review__list { flex:1; min-height:0; box-sizing:border-box; padding:0 36rpx; }
.conflict-review__item { padding:22rpx 0; border-top:1px solid var(--nerae-border-decorative); }
.conflict-review__label { display:block; margin-bottom:16rpx; font-size:27rpx; font-weight:600; overflow-wrap:anywhere; }
.conflict-review__options { display:flex; gap:14rpx; }
.conflict-review__option { display:flex; flex:1; flex-direction:column; align-items:flex-start; gap:10rpx; min-width:0; margin:0; padding:20rpx; border:1px solid var(--nerae-border-decorative); border-radius:16rpx; background:var(--nerae-surface-subtle); color:var(--nerae-text); text-align:left; white-space:normal; line-height:1.45; }
.conflict-review__option--selected { border-color:var(--nerae-text); background:var(--nerae-surface); }
.conflict-review__option-heading { display:flex; align-items:center; justify-content:space-between; gap:8rpx; width:100%; }
.conflict-review__source { font-size:22rpx; color:var(--nerae-text-secondary); }
.conflict-review__selected { font-size:21rpx; color:var(--nerae-text); }
.conflict-review__value { font-size:24rpx; overflow-wrap:anywhere; }
.conflict-review__actions { display:flex; gap:16rpx; padding:20rpx 36rpx calc(20rpx + env(safe-area-inset-bottom)); }
.conflict-review__actions button { margin:0; min-height:84rpx; border-radius:18rpx; font-size:26rpx; }
.conflict-review__cancel { flex:1; background:var(--nerae-surface-subtle); color:var(--nerae-text); }
.conflict-review__confirm { flex:1.4; background:var(--nerae-primary); color:var(--nerae-primary-text); }
.conflict-review__confirm[disabled] { opacity:.45; }
button::after { border:0; }
</style>
