<script setup lang="ts">
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

function nameOf(exercise: DraftExercise): string {
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
      <text class="record-header__eyebrow">NERAE · 训练记录</text>
      <text class="record-header__title">记下实际训练</text>
      <text class="record-header__description">按自己的节奏填写，空白组不会计入记录。</text>
      <picker mode="date" :value="draft.date" :end="latestAllowedDate" @change="emit('change-date', valueOf($event))">
        <view class="date-picker" aria-label="选择训练日期">
          <text>训练日期</text><text class="date-picker__value">{{ draft.date }} ›</text>
        </view>
      </picker>
    </view>

    <view class="record-body">
      <view class="mode-section">
        <text class="section-title">记录方式</text>
        <view class="mode-options">
          <button class="mode-option" :class="{ 'mode-option--active': draft.mode === 'after_workout' }"
            :aria-pressed="draft.mode === 'after_workout'" @tap="emit('change-mode', 'after_workout')">
            <text class="mode-option__title">训练后记录</text>
            <text class="mode-option__detail">完成训练后一起填写</text>
          </button>
          <button class="mode-option" :class="{ 'mode-option--active': draft.mode === 'per_set' }"
            :aria-pressed="draft.mode === 'per_set'" @tap="emit('change-mode', 'per_set')">
            <text class="mode-option__title">每组完成后记录</text>
            <text class="mode-option__detail">一组一组记下实际数据</text>
          </button>
        </view>
        <text class="mode-note">切换方式不会改变已有的动作和组。</text>
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

        <view v-for="exercise in draft.exercises" :key="exercise.exerciseEntryKey" class="exercise-card">
          <view class="exercise-card__header">
            <view class="exercise-card__name-block">
              <text class="exercise-card__name">{{ nameOf(exercise) }}</text>
              <text class="exercise-card__kind">{{ kindLabels[exercise.kind] }}</text>
            </view>
            <text class="exercise-card__count">{{ exercise.sets.length }} 组</text>
          </view>

          <view v-for="(set, index) in exercise.sets" :key="set.recordKey" class="set-row">
            <view class="set-row__heading">
              <text class="set-row__number">第 {{ index + 1 }} 组</text>
              <text v-if="set.persisted &amp;&amp; !set.dirty" class="set-row__confirmed">已保存</text>
              <text v-else-if="set.persisted &amp;&amp; set.dirty" class="set-row__confirmed">已修改，待保存</text>
              <text v-else-if="set.confirmed" class="set-row__confirmed">已记下，待保存</text>
              <button class="remove-button" :aria-label="`移除第 ${index + 1} 组`"
                @tap="emit('remove-set', exercise.exerciseEntryKey, set.recordKey)">移除</button>
            </view>
            <view class="set-inputs">
              <view v-if="hasWeight(exercise.kind)" class="input-field">
                <text class="input-field__label">{{ isPerSide(exercise.kind) ? '每侧重量 (kg)' : '重量 (kg)' }}</text>
                <input class="input-field__control" type="digit" :value="set.weightKg" placeholder="输入重量"
                  :aria-label="isPerSide(exercise.kind) ? '每侧重量，千克' : '重量，千克'"
                  @input="emit('edit-set', exercise.exerciseEntryKey, set.recordKey, 'weightKg', valueOf($event))" />
              </view>
              <view v-if="!hasSeconds(exercise.kind)" class="input-field">
                <text class="input-field__label">{{ isPerSide(exercise.kind) ? '每侧次数' : exercise.kind === 'breath' ? '呼吸次数' : '次数' }}</text>
                <input class="input-field__control" type="number" :value="set.reps" placeholder="输入次数"
                  :aria-label="isPerSide(exercise.kind) ? '每侧次数' : '次数'"
                  @input="emit('edit-set', exercise.exerciseEntryKey, set.recordKey, 'reps', valueOf($event))" />
              </view>
              <view v-if="hasSeconds(exercise.kind)" class="input-field">
                <text class="input-field__label">{{ isPerSide(exercise.kind) ? '每侧实际秒数' : '实际秒数' }}</text>
                <input class="input-field__control" type="number" :value="set.seconds" placeholder="输入秒数"
                  :aria-label="isPerSide(exercise.kind) ? '每侧实际秒数' : '实际秒数'"
                  @input="emit('edit-set', exercise.exerciseEntryKey, set.recordKey, 'seconds', valueOf($event))" />
              </view>
            </view>
            <text v-if="issueFor(exercise.exerciseEntryKey, set.recordKey)" class="set-error" role="alert">
              {{ issueFor(exercise.exerciseEntryKey, set.recordKey) }}
            </text>
            <button v-if="draft.mode === 'per_set'" class="confirm-button" :disabled="saving"
              @tap="emit('confirm-set', exercise.exerciseEntryKey, set.recordKey)">
              {{ set.confirmed ? '重新记下这组' : '记下这组' }}
            </button>
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
.record-header { display: flex; flex-direction: column; gap: 14rpx; padding: 52rpx 40rpx 38rpx; color: #fff; background: #252c29; }
.record-header__eyebrow { color: #c4cbc5; font-size: 21rpx; letter-spacing: 3rpx; }
.record-header__title { font-size: 46rpx; line-height: 1.22; font-weight: 650; }
.record-header__description { color: #d4d9d5; font-size: 26rpx; line-height: 1.5; }
.date-picker { display: flex; align-items: center; justify-content: space-between; gap: 20rpx; min-height: 88rpx; margin-top: 16rpx; padding: 0 24rpx; border: 1px solid #68716b; border-radius: 18rpx; font-size: 26rpx; }
.date-picker__value { color: #fff; font-weight: 600; }
.record-body { padding: 38rpx 32rpx 0; }
.mode-section, .exercise-section { margin-bottom: 44rpx; }
.section-title { display: block; font-size: 32rpx; font-weight: 650; line-height: 1.35; }
.mode-options { display: flex; gap: 16rpx; margin-top: 22rpx; }
.mode-option { display: flex; flex: 1; min-width: 0; min-height: 126rpx; flex-direction: column; align-items: flex-start; justify-content: center; gap: 8rpx; margin: 0; padding: 20rpx; border: 1px solid #d9ded9; border-radius: 20rpx; background: #fff; color: #252c29; text-align: left; line-height: 1.35; }
.mode-option::after, .text-button::after, .empty-state__button::after, .remove-button::after, .confirm-button::after, .add-set-button::after, .save-button::after { border: 0; }
.mode-option--active { border: 2px solid #252c29; background: #f7f8f5; }
.mode-option__title { font-size: 27rpx; font-weight: 650; }
.mode-option__detail, .mode-note { font-size: 22rpx; color: #606a63; line-height: 1.4; }
.mode-note { display: block; margin-top: 14rpx; }
.section-heading { display: flex; justify-content: space-between; align-items: center; gap: 20rpx; }
.section-caption { display: block; margin-top: 6rpx; font-size: 23rpx; color: #606a63; }
.text-button { min-height: 88rpx; margin: 0; padding: 0 4rpx; background: transparent; color: #8d412d; font-size: 25rpx; font-weight: 650; line-height: 88rpx; }
.empty-state { display: flex; flex-direction: column; align-items: center; gap: 12rpx; margin-top: 22rpx; padding: 56rpx 24rpx; border: 1px solid #e2e5e0; border-radius: 24rpx; background: #fff; text-align: center; }
.empty-state__title { font-size: 29rpx; font-weight: 650; }
.empty-state__detail { color: #606a63; font-size: 24rpx; line-height: 1.5; }
.empty-state__button { min-height: 88rpx; margin-top: 12rpx; padding: 0 34rpx; border-radius: 16rpx; background: #f08b65; color: #252c29; font-size: 26rpx; line-height: 88rpx; }
.exercise-card { margin-top: 22rpx; padding: 28rpx; border: 1px solid #e2e5e0; border-radius: 24rpx; background: #fff; }
.exercise-card__header, .set-row__heading { display: flex; align-items: center; justify-content: space-between; gap: 16rpx; }
.exercise-card__name-block { min-width: 0; flex: 1; }
.exercise-card__name { display: block; overflow-wrap: anywhere; font-size: 30rpx; font-weight: 650; line-height: 1.4; }
.exercise-card__kind { display: block; margin-top: 6rpx; color: #606a63; font-size: 23rpx; }
.exercise-card__count { flex-shrink: 0; color: #606a63; font-size: 23rpx; }
.set-row { margin-top: 26rpx; padding-top: 22rpx; border-top: 1px solid #edf0eb; }
.set-row__number { font-size: 26rpx; font-weight: 650; }
.set-row__confirmed { flex: 1; color: #606a63; font-size: 22rpx; text-align: right; }
.remove-button { min-height: 88rpx; margin: 0; padding: 0 4rpx; background: transparent; color: #8d412d; font-size: 23rpx; line-height: 88rpx; }
.set-inputs { display: flex; flex-wrap: wrap; gap: 16rpx; }
.input-field { flex: 1; min-width: 200rpx; }
.input-field__label { display: block; margin-bottom: 10rpx; color: #606a63; font-size: 23rpx; }
.input-field__control { min-height: 88rpx; box-sizing: border-box; padding: 0 20rpx; border: 1px solid #d9ded9; border-radius: 14rpx; background: #fafbf9; font-size: 28rpx; }
.set-error { display: block; margin-top: 12rpx; color: #a33121; font-size: 23rpx; line-height: 1.4; }
.confirm-button, .add-set-button { min-height: 88rpx; margin: 18rpx 0 0; border-radius: 14rpx; background: #f2f4f0; color: #252c29; font-size: 25rpx; line-height: 88rpx; }
.add-set-button { width: 100%; border: 1px dashed #b9c1ba; background: #fff; }
.record-footer-space { height: 160rpx; }
.record-footer { position: fixed; right: 0; bottom: 0; left: 0; padding: 20rpx 32rpx calc(20rpx + env(safe-area-inset-bottom)); background: #fff; border-top: 1px solid #e2e5e0; }
.record-footer__status { display: block; margin-bottom: 10rpx; color: #606a63; font-size: 22rpx; line-height: 1.4; }
.save-button { min-height: 96rpx; margin: 0; border-radius: 18rpx; background: #f08b65; color: #252c29; font-size: 29rpx; font-weight: 650; line-height: 96rpx; }
.save-button[disabled] { background: #dce0db; color: #788079; }
</style>
