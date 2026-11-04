<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import type { DraftExercise, GroupIssue, InputField, RecordingDraft, RecordingMode, SetKind } from './recording'

/** The parent owns authenticated draft persistence, IDs and all server writes. */
const props = defineProps<{
  draft: RecordingDraft
  latestAllowedDate: string
  exerciseLabels: Record<string, string>
  issues: GroupIssue[]
  statusMessage: string
  saving: boolean
  canSave: boolean
}>()

const emit = defineEmits<{
  (event: 'change-mode', value: RecordingMode): void
  (event: 'prepare-date'): void
  (event: 'change-date', value: string): void
  (event: 'add-exercise'): void
  (event: 'add-set', exerciseEntryKey: string): void
  (event: 'edit-set', exerciseEntryKey: string, recordKey: string, field: InputField, value: string): void
  (event: 'confirm-set', exerciseEntryKey: string, recordKey: string): void
  (event: 'remove-set', exerciseEntryKey: string, recordKey: string): void
  (event: 'save'): void
}>()

const kindLabels: Record<SetKind, string> = {
  weight: '重量与次数', perSide: '每侧重量与次数', bodyweight: '自重次数',
  bodyweightPerSide: '每侧自重次数', duration: '实际时长',
  durationPerSide: '每侧实际时长', breath: '呼吸次数',
}

const selectedExerciseKey = ref('')
watch(() => props.draft.exercises.map(item => item.exerciseEntryKey), (keys, previous = []) => {
  const added = keys.find(key => !previous.includes(key))
  if (props.draft.mode === 'per_set' && added) selectedExerciseKey.value = added
  else if (!keys.includes(selectedExerciseKey.value)) selectedExerciseKey.value = keys[0] || ''
}, { immediate: true })
const visibleExercises = computed(() => props.draft.mode === 'per_set'
  ? props.draft.exercises.filter(item => item.exerciseEntryKey === selectedExerciseKey.value)
  : props.draft.exercises)

function nameOf(exercise: DraftExercise): string {
  if (exercise.resolvedExerciseName) return exercise.resolvedExerciseName
  if (exercise.identity.source === 'custom') return exercise.identity.exerciseName
  return props.exerciseLabels[exercise.exerciseEntryKey] || '动作信息暂不可用'
}

function valueOf(event: unknown): string {
  if (!event || typeof event !== 'object' || !('detail' in event)) return ''
  const detail = event.detail
  if (!detail || typeof detail !== 'object' || !('value' in detail)) return ''
  return typeof detail.value === 'string' ? detail.value : String(detail.value ?? '')
}

function issueFor(exerciseEntryKey: string, recordKey: string): string {
  return props.issues.find(item => item.exerciseEntryKey === exerciseEntryKey && item.recordKey === recordKey)?.message || ''
}

function hasWeight(kind: SetKind): boolean { return kind === 'weight' || kind === 'perSide' }
function hasSeconds(kind: SetKind): boolean { return kind === 'duration' || kind === 'durationPerSide' }
function isPerSide(kind: SetKind): boolean { return kind === 'perSide' || kind === 'bodyweightPerSide' || kind === 'durationPerSide' }
</script>

<template>
  <view class="record-editor">
    <view class="record-header">
      <picker mode="date" :value="draft.date" :end="latestAllowedDate" @touchstart="emit('prepare-date')" @change="emit('change-date', valueOf($event))">
        <view class="date-picker" aria-label="选择训练日期">
          <text>{{ draft.date }} ﹀</text>
        </view>
      </picker>
      <text class="record-header__title">记下实际训练</text>
      <text class="record-header__description">把训练记下来，让下一次更有方向。</text>
    </view>

    <view class="record-body">
      <view class="mode-section">
        <view class="mode-options">
          <button class="mode-option" :class="{ 'mode-option--active': draft.mode === 'after_workout' }"
            :aria-pressed="draft.mode === 'after_workout'" @tap="emit('change-mode', 'after_workout')">
            <text class="mode-option__title">训练后记录</text>
          </button>
          <button class="mode-option" :class="{ 'mode-option--active': draft.mode === 'per_set' }"
            :aria-pressed="draft.mode === 'per_set'" @tap="emit('change-mode', 'per_set')">
            <text class="mode-option__title">每组完成后记录</text>
          </button>
        </view>
        <text class="mode-note">空白组不会计入；切换方式不改变已填内容。</text>
      </view>

      <view class="exercise-section">
        <view class="section-heading">
          <view><text class="section-title">训练动作</text><text class="section-caption">按实际完成情况填写</text></view>
          <button class="text-button" @tap="emit('add-exercise')">＋ 添加动作</button>
        </view>

        <view v-if="draft.exercises.length === 0" class="empty-state">
          <text class="empty-state__title">还没有动作</text>
          <text class="empty-state__detail">添加一个实际练过的动作，开始记录。</text>
          <button class="empty-state__button" @tap="emit('add-exercise')">添加动作</button>
        </view>

        <scroll-view v-if="draft.mode === 'per_set' &amp;&amp; draft.exercises.length > 0" class="exercise-tabs" scroll-x>
          <view class="exercise-tabs__list">
            <button v-for="exercise in draft.exercises" :key="exercise.exerciseEntryKey" class="exercise-tabs__button"
              :class="{ 'exercise-tabs__button--active': exercise.exerciseEntryKey === selectedExerciseKey }"
              :aria-pressed="exercise.exerciseEntryKey === selectedExerciseKey" @tap="selectedExerciseKey = exercise.exerciseEntryKey">
              {{ nameOf(exercise) }}
            </button>
          </view>
        </scroll-view>

        <view v-for="exercise in visibleExercises" :key="exercise.exerciseEntryKey" class="exercise-card">
          <view class="exercise-card__header">
            <view class="exercise-card__name-block">
              <text class="exercise-card__name">{{ nameOf(exercise) }}</text>
              <text class="exercise-card__kind">{{ kindLabels[exercise.kind] }}</text>
            </view>
            <text class="exercise-card__count">{{ exercise.sets.length }} 组</text>
          </view>

          <view class="set-columns">
            <text class="set-column__number">组</text>
            <text v-if="hasWeight(exercise.kind)" class="set-column__input">{{ isPerSide(exercise.kind) ? '每侧重量 kg' : '重量 kg' }}</text>
            <text class="set-column__input">{{ hasSeconds(exercise.kind) ? (isPerSide(exercise.kind) ? '每侧秒数' : '实际秒数') : (isPerSide(exercise.kind) ? '每侧次数' : exercise.kind === 'breath' ? '呼吸次数' : '次数') }}</text>
            <text v-if="draft.mode === 'per_set'" class="set-column__confirm">操作</text>
            <text class="set-column__remove" />
          </view>
          <view v-for="(set, index) in exercise.sets" :key="set.recordKey" class="set-row">
            <view class="set-row__values">
              <text class="set-column__number">{{ index + 1 }}</text>
              <input v-if="hasWeight(exercise.kind)" class="set-input" type="digit" :value="set.weightKg" placeholder="输入"
                :aria-label="isPerSide(exercise.kind) ? '每侧重量，千克' : '重量，千克'"
                @input="emit('edit-set', exercise.exerciseEntryKey, set.recordKey, 'weightKg', valueOf($event))" />
              <input v-if="!hasSeconds(exercise.kind)" class="set-input" type="number" :value="set.reps" placeholder="输入"
                :aria-label="isPerSide(exercise.kind) ? '每侧次数' : exercise.kind === 'breath' ? '呼吸次数' : '次数'"
                @input="emit('edit-set', exercise.exerciseEntryKey, set.recordKey, 'reps', valueOf($event))" />
              <input v-else class="set-input" type="number" :value="set.seconds" placeholder="输入"
                :aria-label="isPerSide(exercise.kind) ? '每侧实际秒数' : '实际秒数'"
                @input="emit('edit-set', exercise.exerciseEntryKey, set.recordKey, 'seconds', valueOf($event))" />
              <button v-if="draft.mode === 'per_set'" class="confirm-button" :class="{ 'confirm-button--done': set.confirmed }" :disabled="saving"
                @tap="emit('confirm-set', exercise.exerciseEntryKey, set.recordKey)">{{ set.confirmed ? '已记下' : '记下' }}</button>
              <button class="remove-button" :disabled="saving || set.persisted" :aria-label="`移除第 ${index + 1} 组`"
                @tap="emit('remove-set', exercise.exerciseEntryKey, set.recordKey)">×</button>
            </view>
            <text v-if="set.persisted &amp;&amp; !set.dirty" class="set-row__confirmed">已保存</text>
            <text v-else-if="set.persisted &amp;&amp; set.dirty" class="set-row__confirmed">已修改，待保存</text>
            <text v-if="issueFor(exercise.exerciseEntryKey, set.recordKey)" class="set-error" role="alert">
              {{ issueFor(exercise.exerciseEntryKey, set.recordKey) }}
            </text>
          </view>
          <button class="add-set-button" :disabled="saving" @tap="emit('add-set', exercise.exerciseEntryKey)">＋ 添加一组</button>
        </view>
      </view>
      <view class="record-footer-space" />
    </view>

    <view class="record-footer">
      <text v-if="statusMessage" class="record-footer__status" role="status">{{ statusMessage }}</text>
      <button class="save-button" :disabled="saving || !canSave" @tap="emit('save')">
        {{ saving ? '正在保存…' : '保存记录' }}
      </button>
    </view>
  </view>
</template>

<style scoped>
.record-editor { min-height: 100vh; background: #f3f4f1; color: #252c29; }
.record-header { display: flex; flex-direction: column; align-items: flex-start; gap: 12rpx; padding: 12rpx 32rpx 0; }
.record-header picker { align-self: center; }
.record-header__title { margin-top: 18rpx; font-size: 42rpx; line-height: 1.22; font-weight: 650; }
.record-header__description { color: #606a63; font-size: 24rpx; line-height: 1.5; }
.date-picker { display: flex; align-items: center; justify-content: center; min-height: 88rpx; padding: 0 26rpx; border: 1px solid #d9ded9; border-radius: 44rpx; background: #fff; color: #465149; font-size: 23rpx; }
.record-body { padding: 24rpx 32rpx 0; }
.mode-section, .exercise-section { margin-bottom: 32rpx; }
.section-title { display: block; font-size: 32rpx; font-weight: 650; line-height: 1.35; }
.mode-options { display: flex; gap: 4rpx; padding: 5rpx; border-radius: 40rpx; background: #e5e9e1; }
.mode-option { display: flex; flex: 1; min-width: 0; min-height: 88rpx; align-items: center; justify-content: center; margin: 0; padding: 10rpx; border: 0; border-radius: 44rpx; background: transparent; color: #465149; line-height: 1.35; }
.mode-option::after, .text-button::after, .empty-state__button::after, .remove-button::after, .confirm-button::after, .add-set-button::after, .save-button::after { border: 0; }
.mode-option--active { background: #252c29; color: #fff; }
.mode-option__title { font-size: 24rpx; font-weight: 650; }
.mode-note { font-size: 22rpx; color: #606a63; line-height: 1.4; }
.mode-note { display: block; margin-top: 14rpx; }
.section-heading { display: flex; justify-content: space-between; align-items: center; gap: 20rpx; }
.section-caption { display: block; margin-top: 6rpx; font-size: 23rpx; color: #606a63; }
.exercise-tabs { width: 100%; margin-top: 20rpx; white-space: nowrap; }
.exercise-tabs__list { display: flex; gap: 12rpx; }
.exercise-tabs__button { flex: 0 0 auto; max-width: 220rpx; overflow: hidden; text-overflow: ellipsis; margin: 0; padding: 0 24rpx; min-height: 88rpx; line-height: 88rpx; border-radius: 15rpx; background: #e7eae4; color: #465149; font-size: 24rpx; }
.exercise-tabs__button--active { background: #252c29; color: #fff; }
.exercise-tabs__button::after { border: 0; }
.text-button { min-height: 88rpx; margin: 0; padding: 0 4rpx; background: transparent; color: #8d412d; font-size: 25rpx; font-weight: 650; line-height: 88rpx; }
.empty-state { display: flex; flex-direction: column; align-items: center; gap: 12rpx; margin-top: 22rpx; padding: 56rpx 24rpx; border: 1px solid #e2e5e0; border-radius: 24rpx; background: #fff; text-align: center; }
.empty-state__title { font-size: 29rpx; font-weight: 650; }
.empty-state__detail { color: #606a63; font-size: 24rpx; line-height: 1.5; }
.empty-state__button { min-height: 88rpx; margin-top: 12rpx; padding: 0 34rpx; border-radius: 16rpx; background: #f08b65; color: #252c29; font-size: 26rpx; line-height: 88rpx; }
.exercise-card { margin-top: 22rpx; padding: 28rpx; border: 1px solid #e2e5e0; border-radius: 24rpx; background: #fff; }
.exercise-card__header { display: flex; align-items: center; justify-content: space-between; gap: 16rpx; }
.exercise-card__name-block { min-width: 0; flex: 1; }
.exercise-card__name { display: block; overflow-wrap: anywhere; font-size: 30rpx; font-weight: 650; line-height: 1.4; }
.exercise-card__kind { display: block; margin-top: 6rpx; color: #606a63; font-size: 23rpx; }
.exercise-card__count { flex-shrink: 0; color: #606a63; font-size: 23rpx; }
.set-columns, .set-row__values { display: flex; align-items: center; gap: 10rpx; }
.set-columns { margin-top: 22rpx; color: #606a63; font-size: 22rpx; }
.set-column__number { flex: 0 0 38rpx; text-align: center; }
.set-column__input { flex: 1; min-width: 0; }
.set-column__confirm { flex: 0 0 92rpx; text-align: center; }
.set-column__remove { flex: 0 0 88rpx; }
.set-row { margin-top: 12rpx; }
.set-row__confirmed { display: block; margin: 6rpx 58rpx 0; color: #606a63; font-size: 21rpx; }
.set-input { flex: 1; min-width: 0; height: 88rpx; box-sizing: border-box; padding: 0 10rpx; border: 1px solid #d9ded9; border-radius: 12rpx; background: #fafbf9; font-size: 26rpx; text-align: center; }
.remove-button { flex: 0 0 88rpx; min-width: 88rpx; min-height: 88rpx; margin: 0; padding: 0; background: transparent; color: #7b857d; font-size: 32rpx; line-height: 88rpx; }
.set-error { display: block; margin-top: 12rpx; color: #a33121; font-size: 23rpx; line-height: 1.4; }
.confirm-button { flex: 0 0 92rpx; min-width: 92rpx; min-height: 88rpx; margin: 0; padding: 0; border-radius: 12rpx; background: #f08b65; color: #252c29; font-size: 22rpx; line-height: 88rpx; }
.confirm-button--done { background: #e6ebdc; color: #52604d; }
.add-set-button { min-height: 76rpx; margin: 18rpx 0 0; border-radius: 14rpx; color: #252c29; font-size: 25rpx; line-height: 76rpx; }
.add-set-button { width: 100%; border: 1px dashed #b9c1ba; background: #fff; }
.record-footer-space { height: 160rpx; }
.record-footer { position: fixed; right: 0; bottom: 0; left: 0; padding: 20rpx 32rpx calc(20rpx + env(safe-area-inset-bottom)); background: #fff; border-top: 1px solid #e2e5e0; }
.record-footer__status { display: block; margin-bottom: 10rpx; color: #606a63; font-size: 22rpx; line-height: 1.4; }
.save-button { min-height: 96rpx; margin: 0; border-radius: 18rpx; background: #f08b65; color: #252c29; font-size: 29rpx; font-weight: 650; line-height: 96rpx; }
.save-button[disabled] { background: #dce0db; color: #788079; }
</style>
