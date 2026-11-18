<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { batchSetSnapshot, validBatchSetFields, type BatchSetField, type DraftExercise, type DraftSet, type GroupIssue, type InputField, type RecordingDraft, type RecordingMode, type SetKind, type V2Measure } from './recording'
import { describeDraftSet, exerciseMeasure, MEASURE_LABELS, measureUsesSharedWeight, V2_MEASURES, type SideState, type V2SideName } from './recordingV2Measure'
import RulerInput from './RulerInput.vue'
import SwipeableSetCard from './SwipeableSetCard.vue'

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
  (event: 'copy-set', exerciseEntryKey: string, recordKey: string): void
  (event: 'edit-set', exerciseEntryKey: string, recordKey: string, field: InputField, value: string): void
  (event: 'set-measure', exerciseEntryKey: string, measure: V2Measure): void
  (event: 'set-side', exerciseEntryKey: string, recordKey: string, side: V2SideName, state: SideState): void
  (event: 'apply-batch', exerciseEntryKey: string, sourceKey: string, recordKeys: string[], fields: BatchSetField[], newCount: number, snapshot: string): void
  (event: 'confirm-set', exerciseEntryKey: string, recordKey: string): void
  (event: 'remove-set', exerciseEntryKey: string, recordKey: string): void
  (event: 'restore-set', exerciseEntryKey: string, recordKey: string): void
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

const editing = ref<{ exerciseEntryKey: string; recordKey: string } | null>(null)
const revealedSet = ref('')
const expandedRemoved = ref<string[]>([])
const pendingRemoval = ref<{ exerciseEntryKey: string; recordKey: string } | null>(null)
const undoTarget = ref<{ exerciseEntryKey: string; recordKey: string } | null>(null)
let undoHandle: ReturnType<typeof setTimeout> | null = null

function requestRemove(exerciseEntryKey: string, recordKey: string): void {
  pendingRemoval.value = { exerciseEntryKey, recordKey }
  revealedSet.value = ''
  emit('remove-set', exerciseEntryKey, recordKey)
}
function restoreSet(exerciseEntryKey: string, recordKey: string): void {
  emit('restore-set', exerciseEntryKey, recordKey)
  if (undoTarget.value?.recordKey === recordKey) undoTarget.value = null
}
watch(() => props.draft, draft => {
  const target = pendingRemoval.value
  if (!target || !draft.exercises.find(item => item.exerciseEntryKey === target.exerciseEntryKey)?.removedSets
    ?.some(item => item.set.recordKey === target.recordKey)) return
  pendingRemoval.value = null
  undoTarget.value = target
  if (undoHandle) clearTimeout(undoHandle)
  undoHandle = setTimeout(() => { undoTarget.value = null; undoHandle = null }, 5000)
})
onBeforeUnmount(() => { if (undoHandle) clearTimeout(undoHandle) })
function toggleRemoved(key: string): void {
  expandedRemoved.value = expandedRemoved.value.includes(key)
    ? expandedRemoved.value.filter(item => item !== key) : [...expandedRemoved.value, key]
}
function removedSummary(set: DraftSet, exercise: DraftExercise): string {
  if (props.draft.recordFormat === 2 && exercise.kind === 'weight') return describeDraftSet(exercise, set)
  const reps = hasSeconds(exercise.kind) ? `${set.seconds || '未填'} 秒` : `${set.reps || '未填'} 次`
  return hasWeight(exercise.kind) ? `${set.weightKg || '未填'} kg × ${reps}` : reps
}
function editingMeasure(): V2Measure {
  return editingExercise.value ? exerciseMeasure(editingExercise.value) : 'total'
}
function measureIndex(exercise: DraftExercise): number {
  return Math.max(0, V2_MEASURES.indexOf(exerciseMeasure(exercise)))
}
function onMeasure(exerciseEntryKey: string, event: unknown): void {
  const raw = valueOf(event)
  const index = Number(raw)
  const measure = V2_MEASURES[index]
  if (!measure || props.saving) return
  emit('set-measure', exerciseEntryKey, measure)
}
function sideMarked(set: DraftSet, side: V2SideName): boolean {
  return (side === 'left' ? set.leftState : set.rightState) === 'not_performed'
}
const awaitingConfirmation = ref(false)
const editingExercise = computed(() => props.draft.exercises.find(item => item.exerciseEntryKey === editing.value?.exerciseEntryKey))
const editingSet = computed(() => editingExercise.value?.sets.find(item => item.recordKey === editing.value?.recordKey))
const editingNumber = computed(() => editingExercise.value?.sets.findIndex(item => item.recordKey === editing.value?.recordKey) ?? -1)
const batchFieldOptions: BatchSetField[] = ['weightKg', 'reps']
const batching = ref(false)
const batchFields = ref<BatchSetField[]>([])
const batchGroups = ref<string[]>([])
const batchNewCount = ref(0)
const batchSnapshot = ref('')
const batchStale = computed(() => !editingExercise.value || batchSetSnapshot(editingExercise.value) !== batchSnapshot.value)
const batchTotal = computed(() => batchGroups.value.length + batchNewCount.value)
const batchFieldsValid = computed(() => Boolean(editingExercise.value && editingSet.value &&
  validBatchSetFields(editingExercise.value, editingSet.value, batchFields.value)))
const canApplyBatch = computed(() => !props.saving && !batchStale.value && batchFieldsValid.value &&
  batchTotal.value > 0 && batchTotal.value <= 200)

function startBatch(): void {
  if (!editing.value || !editingSet.value || !editingExercise.value || props.saving) return
  batchSnapshot.value = batchSetSnapshot(editingExercise.value)
  batchGroups.value = [editing.value.recordKey]
  batchFields.value = (['weightKg', 'reps'] as BatchSetField[]).filter(field => Boolean(editingSet.value?.[field].trim()))
  batchNewCount.value = 0
  batching.value = true
}
function toggleBatchGroup(key: string): void {
  if (batchStale.value || props.saving) return
  if (batchGroups.value.includes(key)) batchGroups.value = batchGroups.value.filter(item => item !== key)
  else if (batchTotal.value < 200) batchGroups.value = [...batchGroups.value, key]
}
function toggleBatchField(field: BatchSetField): void {
  if (batchStale.value || props.saving || !editingSet.value?.[field].trim()) return
  batchFields.value = batchFields.value.includes(field)
    ? batchFields.value.filter(item => item !== field) : [...batchFields.value, field]
}
function applyBatchSelection(): void {
  if (!canApplyBatch.value || !editing.value) return
  emit('apply-batch', editing.value.exerciseEntryKey, editing.value.recordKey, [...batchGroups.value],
    [...batchFields.value], batchNewCount.value, batchSnapshot.value)
  closeSet()
}
let sheetTouchStart: { x: number; y: number } | null = null

function sheetTouchPoint(event: unknown, field: 'touches' | 'changedTouches'): { x: number; y: number } | null {
  if (!event || typeof event !== 'object' || !(field in event)) return null
  const touches = (event as Record<string, unknown>)[field]
  if (!touches || typeof touches !== 'object' || !('length' in touches) || touches.length !== 1 || !('0' in touches)) return null
  const touch = touches[0]
  if (!touch || typeof touch !== 'object' || !('clientX' in touch) || !('clientY' in touch)) return null
  return typeof touch.clientX === 'number' && Number.isFinite(touch.clientX) &&
    typeof touch.clientY === 'number' && Number.isFinite(touch.clientY)
    ? { x: touch.clientX, y: touch.clientY } : null
}

function startSheetDrag(event: unknown): void { sheetTouchStart = sheetTouchPoint(event, 'touches') }
function cancelSheetDrag(): void { sheetTouchStart = null }
function finishSheetDrag(event: unknown): void {
  const end = sheetTouchPoint(event, 'changedTouches')
  const start = sheetTouchStart
  sheetTouchStart = null
  if (!start || !end) return
  const dx = end.x - start.x
  const dy = end.y - start.y
  if (dy >= 70 && dy > Math.abs(dx) * 1.5) closeSet()
}

function openSet(exerciseEntryKey: string, recordKey: string): void {
  revealedSet.value = ''
  editing.value = { exerciseEntryKey, recordKey }
  awaitingConfirmation.value = false
}
defineExpose({ openSet })

function closeSet(): void {
  editing.value = null
  awaitingConfirmation.value = false
  batching.value = false
  sheetTouchStart = null
}

function finishSetInput(): void {
  const current = editing.value
  if (!current || !editingSet.value) return
  if (props.draft.mode !== 'per_set' || editingSet.value.confirmed) {
    closeSet()
    return
  }
  awaitingConfirmation.value = true
  emit('confirm-set', current.exerciseEntryKey, current.recordKey)
}

watch(() => editingSet.value?.confirmed, confirmed => {
  if (awaitingConfirmation.value && confirmed) closeSet()
})
watch(() => editing.value && Boolean(editingSet.value), present => {
  if (editing.value && !present) closeSet()
})
watch(() => props.draft.mode, () => { closeSet(); revealedSet.value = '' })

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
      </view>

      <view class="exercise-section">
        <view v-if="draft.exercises.length > 0" class="section-heading">
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
              <text v-if="exercise.kind !== 'weight'" class="exercise-card__kind">{{ kindLabels[exercise.kind] }}</text>
              <picker v-else-if="draft.recordFormat === 2" mode="selector" :range="V2_MEASURES.map(item => MEASURE_LABELS[item])" :value="measureIndex(exercise)" :disabled="saving" @change="onMeasure(exercise.exerciseEntryKey, $event)">
                <text class="exercise-card__kind" role="button">重量含义：{{ MEASURE_LABELS[exerciseMeasure(exercise)] }}</text>
              </picker>
            </view>
            <text class="exercise-card__count">{{ exercise.sets.length }} 组</text>
          </view>

          <view v-for="(set, index) in exercise.sets" :key="set.recordKey" class="set-row">
            <view class="set-row__values">
              <SwipeableSetCard :open="revealedSet === set.recordKey" :removable="!set.persisted || draft.recordFormat === 2" :busy="saving"
                :label="`第 ${index + 1} 组`" @reveal="revealedSet = set.recordKey" @dismiss="revealedSet = ''"
                @edit="openSet(exercise.exerciseEntryKey, set.recordKey)" @copy="emit('copy-set', exercise.exerciseEntryKey, set.recordKey)"
                @remove="requestRemove(exercise.exerciseEntryKey, set.recordKey)">
                <text class="set-card__number">第 {{ index + 1 }} 组</text>
                <view class="set-card__measurements">
                  <text v-if="draft.recordFormat === 2 &amp;&amp; exercise.kind === 'weight' &amp;&amp; exerciseMeasure(exercise) !== 'total'" class="set-card__value">{{ describeDraftSet(exercise, set) }}</text>
                  <template v-else>
                    <template v-if="hasWeight(exercise.kind)">
                      <text class="set-card__value">{{ set.weightKg || '—' }}</text><text class="set-card__unit">kg</text>
                      <text class="set-card__times">×</text>
                    </template>
                    <text class="set-card__value">{{ (hasSeconds(exercise.kind) ? set.seconds : set.reps) || '—' }}</text>
                    <text class="set-card__unit">{{ hasSeconds(exercise.kind) ? '秒' : '次' }}</text>
                  </template>
                </view>
                <text v-if="draft.mode === 'per_set' &amp;&amp; set.confirmed" class="set-card__state">已记下</text>
              </SwipeableSetCard>
            </view>
            <text v-if="set.persisted &amp;&amp; !set.dirty" class="set-row__confirmed">已保存</text>
            <text v-else-if="set.persisted &amp;&amp; set.dirty" class="set-row__confirmed">已修改，待保存</text>
            <text v-if="issueFor(exercise.exerciseEntryKey, set.recordKey)" class="set-error" role="alert">
              {{ issueFor(exercise.exerciseEntryKey, set.recordKey) }}
            </text>
          </view>
          <button class="add-set-button" :disabled="saving" @tap="emit('add-set', exercise.exerciseEntryKey)">＋ 添加一组</button>
          <view v-if="exercise.removedSets &amp;&amp; exercise.removedSets.length" class="removed-groups">
            <button class="removed-groups__toggle" :aria-expanded="expandedRemoved.includes(exercise.exerciseEntryKey)"
              @tap="toggleRemoved(exercise.exerciseEntryKey)">
              已移除 {{ exercise.removedSets.length }} 组 {{ expandedRemoved.includes(exercise.exerciseEntryKey) ? '⌃' : '⌄' }}
            </button>
            <view v-if="expandedRemoved.includes(exercise.exerciseEntryKey)">
              <view v-for="removed in exercise.removedSets" :key="removed.set.recordKey" class="removed-groups__item">
                <view class="removed-groups__details">
                  <text>移除时为第 {{ removed.removedPosition }} 组</text>
                  <text class="removed-groups__values">{{ removedSummary(removed.set, exercise) }}</text>
                </view>
                <button class="removed-groups__restore" :disabled="saving"
                  @tap="restoreSet(exercise.exerciseEntryKey, removed.set.recordKey)">恢复</button>
              </view>
            </view>
          </view>
        </view>
      </view>
      <view class="record-footer-space" />
    </view>

    <view v-if="undoTarget" class="undo-notice" role="status">
      <text>已移除一组</text>
      <button :disabled="saving" @tap="restoreSet(undoTarget.exerciseEntryKey, undoTarget.recordKey)">撤销</button>
    </view>

    <view class="record-footer">
      <text v-if="statusMessage" class="record-footer__status" role="status">{{ statusMessage }}</text>
      <button class="save-button" :disabled="saving || !canSave" @tap="emit('save')">
        {{ saving ? '正在保存…' : '完成' }}
      </button>
    </view>

    <view v-if="editing &amp;&amp; editingExercise &amp;&amp; editingSet" class="set-sheet-layer">
      <view class="set-sheet__scrim" @tap="closeSet" @touchmove.stop.prevent />
      <view class="set-sheet" role="dialog" aria-label="填写训练组">
        <view class="set-sheet__drag-area" @touchstart="startSheetDrag" @touchend="finishSheetDrag" @touchcancel="cancelSheetDrag">
          <view class="set-sheet__grip"><view class="set-sheet__handle" /></view>
          <view class="set-sheet__heading">
            <view class="set-sheet__title-block">
              <text class="set-sheet__title">{{ batching ? '应用到多组' : `第 ${editingNumber + 1} 组` }}</text>
              <text class="set-sheet__subtitle">{{ nameOf(editingExercise) }}</text>
            </view>
            <button class="set-sheet__close" aria-label="关闭填写" @tap="closeSet">×</button>
          </view>
        </view>
        <template v-if="!batching">
          <view class="set-sheet__fields">
            <template v-if="draft.recordFormat === 2 &amp;&amp; editingExercise.kind === 'weight' &amp;&amp; editingMeasure() === 'sides'">
              <RulerInput label="左侧重量" unit="kg" kind="digit" :value="editingSet.leftWeightKg || ''"
                @change="emit('edit-set', editing.exerciseEntryKey, editing.recordKey, 'leftWeightKg', $event)" />
              <RulerInput label="左侧次数" unit="次" kind="number" :value="editingSet.leftReps || ''"
                @change="emit('edit-set', editing.exerciseEntryKey, editing.recordKey, 'leftReps', $event)" />
              <button class="side-toggle" :disabled="saving" :aria-pressed="sideMarked(editingSet, 'left')"
                @tap="emit('set-side', editing.exerciseEntryKey, editing.recordKey, 'left', sideMarked(editingSet, 'left') ? 'unknown' : 'not_performed')">
                {{ sideMarked(editingSet, 'left') ? '左侧改为填写' : '左侧未做' }}
              </button>
              <RulerInput label="右侧重量" unit="kg" kind="digit" :value="editingSet.rightWeightKg || ''"
                @change="emit('edit-set', editing.exerciseEntryKey, editing.recordKey, 'rightWeightKg', $event)" />
              <RulerInput label="右侧次数" unit="次" kind="number" :value="editingSet.rightReps || ''"
                @change="emit('edit-set', editing.exerciseEntryKey, editing.recordKey, 'rightReps', $event)" />
              <button class="side-toggle" :disabled="saving" :aria-pressed="sideMarked(editingSet, 'right')"
                @tap="emit('set-side', editing.exerciseEntryKey, editing.recordKey, 'right', sideMarked(editingSet, 'right') ? 'unknown' : 'not_performed')">
                {{ sideMarked(editingSet, 'right') ? '右侧改为填写' : '右侧未做' }}
              </button>
              <text class="set-sheet__status">先填左侧时，右侧在你单独改过之前会使用相同数值。标成未做的一侧不计入。</text>
            </template>
            <template v-else-if="draft.recordFormat === 2 &amp;&amp; editingExercise.kind === 'weight' &amp;&amp; (editingMeasure() === 'bodyweight' || editingMeasure() === 'bodyweight_extra' || editingMeasure() === 'bodyweight_assisted')">
              <RulerInput label="体重" unit="kg" kind="digit" :value="editingSet.bodyweightKg || ''"
                @change="emit('edit-set', editing.exerciseEntryKey, editing.recordKey, 'bodyweightKg', $event)" />
              <RulerInput v-if="editingMeasure() !== 'bodyweight'" :label="editingMeasure() === 'bodyweight_extra' ? '额外负重' : '助力'" unit="kg" kind="digit" :value="editingSet.adjustmentKg || ''"
                @change="emit('edit-set', editing.exerciseEntryKey, editing.recordKey, 'adjustmentKg', $event)" />
              <RulerInput label="次数" unit="次" kind="number" :value="editingSet.reps"
                @change="emit('edit-set', editing.exerciseEntryKey, editing.recordKey, 'reps', $event)" />
              <text class="set-sheet__status">修改体重会更新本次训练里所有自重组的体重，不改其他训练，也不自动改手填的记录重量。</text>
            </template>
            <template v-else>
              <RulerInput v-if="hasWeight(editingExercise.kind)" :label="draft.recordFormat === 2 &amp;&amp; editingMeasure() === 'pair_each' ? '每只重量' : draft.recordFormat === 2 &amp;&amp; editingMeasure() === 'direct' ? '记录重量' : isPerSide(editingExercise.kind) ? '每侧重量' : '重量'"
                unit="kg" kind="digit" :value="editingSet.weightKg"
                @change="emit('edit-set', editing.exerciseEntryKey, editing.recordKey, 'weightKg', $event)" />
              <RulerInput v-if="hasSeconds(editingExercise.kind)" label="实际时间" unit="秒" kind="number" :value="editingSet.seconds"
                @change="emit('edit-set', editing.exerciseEntryKey, editing.recordKey, 'seconds', $event)" />
              <RulerInput v-else :label="isPerSide(editingExercise.kind) ? '每侧次数' : '次数'" unit="次" kind="number" :value="editingSet.reps"
                @change="emit('edit-set', editing.exerciseEntryKey, editing.recordKey, 'reps', $event)" />
            </template>
          </view>
          <button v-if="draft.recordFormat === 2 &amp;&amp; editingExercise.kind === 'weight' &amp;&amp; measureUsesSharedWeight(editingMeasure())" class="batch-entry"
            :disabled="saving" @tap="startBatch">应用到多组 <text>选择字段与组 ›</text></button>
          <text v-if="statusMessage" class="set-sheet__status" role="status">{{ statusMessage }}</text>
          <button class="set-sheet__done" :disabled="saving" @tap="finishSetInput">
            {{ draft.mode === 'per_set' && !editingSet.confirmed ? '记下' : '完成输入' }}
          </button>
        </template>
        <template v-else>
          <text class="batch-caption">选择要应用的内容和组；未选中的内容保持不变。</text>
          <view class="batch-fields">
            <button v-for="field in batchFieldOptions" :key="field" class="batch-field"
              :class="{ 'batch-field--selected': batchFields.includes(field) }"
              :aria-pressed="batchFields.includes(field)" :disabled="saving || batchStale || !editingSet[field].trim()"
              @tap="toggleBatchField(field)">
              <text>{{ field === 'weightKg' ? '重量' : '次数' }}</text>
              <text>{{ editingSet[field] || '未填' }} {{ editingSet[field] ? (field === 'weightKg' ? 'kg' : '次') : '' }}</text>
              <text>{{ batchFields.includes(field) ? '✓' : '＋' }}</text>
            </button>
          </view>
          <view class="batch-heading"><text>选择已有组</text><text>已选 {{ batchGroups.length }} / {{ editingExercise.sets.length }} 组</text></view>
          <scroll-view class="batch-groups" scroll-y>
            <button v-for="(set, index) in editingExercise.sets" :key="set.recordKey" class="batch-group"
              :class="{ 'batch-group--selected': batchGroups.includes(set.recordKey) }"
              :aria-pressed="batchGroups.includes(set.recordKey)" :disabled="saving || batchStale"
              @tap="toggleBatchGroup(set.recordKey)">
              <text class="batch-group__check">{{ batchGroups.includes(set.recordKey) ? '✓' : '' }}</text>
              <text class="batch-group__number">第 {{ index + 1 }} 组</text>
              <text class="batch-group__value">{{ set.weightKg || '—' }} kg × {{ set.reps || '—' }} 次</text>
            </button>
          </scroll-view>
          <view class="batch-add">
            <view><text class="batch-add__title">新增组</text><text class="batch-add__detail">同时应用所选内容</text></view>
            <view class="batch-stepper">
              <button aria-label="减少新增组数" :disabled="saving || batchStale || batchNewCount === 0" @tap="batchNewCount--">−</button>
              <text>{{ batchNewCount }}</text>
              <button aria-label="增加新增组数" :disabled="saving || batchStale || batchTotal >= 200" @tap="batchNewCount++">＋</button>
            </view>
          </view>
          <text v-if="batchStale" class="batch-warning" role="alert">组内容已变化，请返回并重新选择。</text>
          <text v-else-if="batchFields.length === 0" class="batch-warning">请返回填写，或选择已填写的内容。</text>
          <text v-else-if="!batchFieldsValid" class="batch-warning" role="alert">内容格式不正确，请返回修改。</text>
          <view class="batch-actions">
            <button class="batch-back" :disabled="saving" @tap="batching = false">返回</button>
            <button class="set-sheet__done" :disabled="!canApplyBatch" @tap="applyBatchSelection">应用到 {{ batchTotal }} 组</button>
          </view>
        </template>
      </view>
    </view>
  </view>
</template>

<style scoped>
.record-editor { min-height: 100vh; background: var(--nerae-surface); color: var(--nerae-text); }
.record-header { display: flex; flex-direction: column; align-items: flex-start; gap: 12rpx; padding: 12rpx 32rpx 0; }
.record-header picker { align-self: flex-start; }
.date-picker { display: flex; align-items: center; justify-content: center; min-height: 76rpx; padding: 0 24rpx; border: 1px solid var(--nerae-border-decorative); border-radius: 40rpx; color: var(--nerae-text); font-size: 23rpx; }
.record-body { padding: 24rpx 32rpx 0; }
.mode-section, .exercise-section { margin-bottom: 32rpx; }
.mode-options { display: flex; gap: 4rpx; padding: 5rpx; border: 1px solid var(--nerae-border-decorative); border-radius: 40rpx; }
.mode-option { display: flex; flex: 1; min-width: 0; min-height: 76rpx; align-items: center; justify-content: center; margin: 0; padding: 10rpx; border: 0; border-radius: 40rpx; background: transparent; color: var(--nerae-text); line-height: 1.35; }
.mode-option--active { background: var(--nerae-primary); color: var(--nerae-primary-text); }
.mode-option__title { font-size: 24rpx; font-weight: 650; }
.section-heading { display: flex; justify-content: flex-end; align-items: center; }
.exercise-tabs { width: 100%; margin-top: 20rpx; white-space: nowrap; }
.exercise-tabs__list { display: flex; gap: 12rpx; }
.exercise-tabs__button { flex: 0 0 auto; max-width: 220rpx; overflow: hidden; text-overflow: ellipsis; margin: 0; padding: 0 24rpx; min-height: 80rpx; line-height: 80rpx; border-radius: 16rpx; background: var(--nerae-surface-subtle); color: var(--nerae-text); font-size: 24rpx; }
.exercise-tabs__button--active { background: var(--nerae-primary); color: var(--nerae-primary-text); }
.text-button { min-height: 88rpx; margin: 0; padding: 0 4rpx; background: transparent; color: var(--nerae-text); font-size: 25rpx; font-weight: 650; line-height: 88rpx; }
.empty-state { display: flex; flex-direction: column; align-items: center; gap: 12rpx; margin-top: 22rpx; padding: 56rpx 24rpx; border: 1px solid var(--nerae-border-decorative); border-radius: 24rpx; text-align: center; }
.empty-state__title { font-size: 29rpx; font-weight: 650; }
.empty-state__detail { color: var(--nerae-text-secondary); font-size: 24rpx; line-height: 1.5; }
.empty-state__button { min-height: 88rpx; margin-top: 12rpx; padding: 0 34rpx; border-radius: 16rpx; background: var(--nerae-primary); color: var(--nerae-primary-text); font-size: 26rpx; line-height: 88rpx; }
.exercise-card { margin-top: 32rpx; }
.exercise-card__header { display: flex; align-items: center; justify-content: space-between; gap: 16rpx; margin-bottom: 20rpx; }
.exercise-card__name-block { min-width: 0; flex: 1; }
.exercise-card__name { display: block; overflow-wrap: anywhere; font-family: "Songti SC", "Noto Serif SC", serif; font-size: 34rpx; font-weight: 700; line-height: 1.4; }
.exercise-card__kind { display: block; margin-top: 6rpx; color: var(--nerae-text-secondary); font-size: 23rpx; }
.side-toggle { align-self: flex-start; min-height: 72rpx; margin: 0; padding: 0 8rpx; background: transparent; color: var(--nerae-text); font-size: 24rpx; line-height: 72rpx; }
.exercise-card__count { flex-shrink: 0; color: var(--nerae-text-secondary); font-size: 23rpx; }
.set-row { margin-top: 12rpx; }
.set-row__values { display: flex; align-items: stretch; }
.set-card__number { flex: 0 0 auto; font-size: 24rpx; font-weight: 600; white-space: nowrap; }
.set-card__measurements { display: flex; flex: 1; min-width: 0; align-items: baseline; gap: 5rpx; }
.set-card__value { font-size: 32rpx; font-weight: 650; }
.set-card__unit, .set-card__times { color: var(--nerae-text-secondary); font-size: 23rpx; }
.set-card__times { margin: 0 7rpx; }
.set-card__state { flex-shrink: 0; color: var(--nerae-text-secondary); font-size: 21rpx; }
.set-row__confirmed { display: block; margin: 6rpx 20rpx 0; color: var(--nerae-text-secondary); font-size: 21rpx; }
.removed-groups__toggle { display: inline-flex; align-items: center; min-height: 68rpx; margin: 0; padding: 0 4rpx; background: transparent; color: var(--nerae-text-secondary); font-size: 22rpx; line-height: 1.3; }
.removed-groups__item { display: flex; align-items: center; justify-content: space-between; gap: 16rpx; padding: 12rpx 16rpx; border-radius: 14rpx; background: var(--nerae-surface-subtle); }
.removed-groups__item + .removed-groups__item { margin-top: 8rpx; }
.removed-groups__details { display: flex; flex-direction: column; gap: 4rpx; min-width: 0; color: var(--nerae-text-secondary); font-size: 21rpx; line-height: 1.4; }
.removed-groups__values { color: var(--nerae-text); font-size: 24rpx; }
.removed-groups__restore { flex: 0 0 auto; min-height: 68rpx; margin: 0; padding: 0 16rpx; background: transparent; color: var(--nerae-text); font-size: 24rpx; line-height: 68rpx; }
.undo-notice { position: fixed; z-index: 20; right: 32rpx; bottom: calc(160rpx + env(safe-area-inset-bottom)); left: 32rpx; display: flex; align-items: center; justify-content: space-between; min-height: 82rpx; padding: 0 20rpx 0 26rpx; border-radius: 16rpx; background: var(--nerae-primary); color: var(--nerae-primary-text); font-size: 24rpx; }
.undo-notice button { min-height: 72rpx; margin: 0; padding: 0 12rpx; background: transparent; color: var(--nerae-primary-text); font-size: 24rpx; line-height: 72rpx; }
.set-error { display: block; margin-top: 12rpx; color: var(--nerae-text); font-size: 23rpx; line-height: 1.4; }
.add-set-button { display: inline-flex; min-height: 76rpx; align-items: center; margin: 12rpx 0 0; padding: 0 4rpx; background: transparent; color: var(--nerae-text); font-size: 25rpx; line-height: 76rpx; }
.record-footer-space { height: 160rpx; }
.record-footer { position: fixed; right: 0; bottom: 0; left: 0; padding: 20rpx 32rpx calc(20rpx + env(safe-area-inset-bottom)); background: var(--nerae-surface); border-top: 1px solid var(--nerae-border-decorative); }
.record-footer__status { display: block; margin-bottom: 10rpx; color: var(--nerae-text-secondary); font-size: 22rpx; line-height: 1.4; }
.save-button, .set-sheet__done { min-height: 96rpx; margin: 0; border-radius: 18rpx; background: var(--nerae-primary); color: var(--nerae-primary-text); font-size: 29rpx; font-weight: 650; line-height: 96rpx; }
.save-button[disabled], .set-sheet__done[disabled] { opacity: 1; background: var(--nerae-disabled); color: var(--nerae-primary-text); }
.set-sheet-layer { position: fixed; inset: 0; z-index: 30; }
.set-sheet__scrim { position: absolute; inset: 0; background: rgba(0, 0, 0, 0.36); }
.set-sheet { position: absolute; right: 0; bottom: 0; left: 0; display: flex; flex-direction: column; gap: 24rpx; max-height: 85vh; box-sizing: border-box; overflow-y: auto; padding: 0 32rpx calc(28rpx + env(safe-area-inset-bottom)); border-radius: 32rpx 32rpx 0 0; background: var(--nerae-surface); }
.set-sheet__drag-area { display: flex; flex-direction: column; gap: 6rpx; }
.set-sheet__grip { display: flex; height: 40rpx; align-items: center; justify-content: center; }
.set-sheet__handle { width: 80rpx; height: 8rpx; border-radius: 8rpx; background: var(--nerae-border-decorative); }
.set-sheet__heading { display: flex; align-items: flex-start; justify-content: space-between; gap: 20rpx; }
.set-sheet__title-block { display: flex; min-width: 0; flex-direction: column; gap: 6rpx; }
.set-sheet__title { font-family: "Songti SC", "Noto Serif SC", serif; font-size: 38rpx; font-weight: 700; }
.set-sheet__subtitle { color: var(--nerae-text-secondary); font-size: 24rpx; overflow-wrap: anywhere; }
.set-sheet__close { flex: 0 0 76rpx; width: 76rpx; min-height: 76rpx; margin: 0; padding: 0; border-radius: 40rpx; background: var(--nerae-surface-subtle); color: var(--nerae-text); font-size: 36rpx; line-height: 76rpx; }
.set-sheet__fields { display: flex; flex-direction: column; gap: 22rpx; }
.set-sheet__status { color: var(--nerae-text-secondary); font-size: 22rpx; line-height: 1.5; }
.set-sheet__done { width: 100%; }
.batch-entry { display:flex; width:100%; min-height:80rpx; align-items:center; justify-content:space-between; margin:0; padding:0 18rpx; border:1px solid var(--nerae-border-decorative); border-radius:16rpx; background:var(--nerae-surface); color:var(--nerae-text); font-size:25rpx; }
.batch-entry text { color:var(--nerae-text-secondary); font-size:22rpx; }
.batch-caption, .batch-warning { color:var(--nerae-text-secondary); font-size:23rpx; line-height:1.5; }
.batch-warning { color:var(--nerae-text); }
.batch-fields { display:flex; gap:12rpx; }
.batch-field { display:flex; flex:1; min-width:0; min-height:88rpx; align-items:center; justify-content:space-between; gap:8rpx; margin:0; padding:0 18rpx; border:1px solid var(--nerae-border-decorative); border-radius:14rpx; background:var(--nerae-surface); color:var(--nerae-text); font-size:23rpx; }
.batch-field--selected { background:var(--nerae-surface-subtle); }
.batch-field[disabled], .batch-stepper button[disabled] { opacity:.38; }
.batch-heading { display:flex; justify-content:space-between; align-items:center; font-size:25rpx; font-weight:650; }
.batch-heading text + text { color:var(--nerae-text-secondary); font-size:22rpx; font-weight:400; }
.batch-groups { max-height:28vh; }
.batch-group { display:flex; width:100%; min-height:88rpx; align-items:center; gap:14rpx; margin:0 0 8rpx; padding:0 16rpx; border:1px solid var(--nerae-border-decorative); border-radius:14rpx; background:var(--nerae-surface); color:var(--nerae-text); font-size:23rpx; }
.batch-group--selected { background:var(--nerae-surface-subtle); }
.batch-group__check { display:flex; width:30rpx; height:30rpx; flex:0 0 auto; align-items:center; justify-content:center; border:1px solid var(--nerae-text-secondary); border-radius:7rpx; font-size:20rpx; }
.batch-group--selected .batch-group__check { background:var(--nerae-primary); color:var(--nerae-primary-text); }
.batch-group__number { flex:0 0 auto; }
.batch-group__value { flex:1; text-align:right; color:var(--nerae-text-secondary); }
.batch-add { display:flex; align-items:center; justify-content:space-between; gap:12rpx; }
.batch-add__title, .batch-add__detail { display:block; font-size:24rpx; }
.batch-add__detail { margin-top:6rpx; color:var(--nerae-text-secondary); font-size:21rpx; }
.batch-stepper { display:flex; align-items:center; gap:18rpx; font-size:26rpx; }
.batch-stepper button { display:flex; width:88rpx; min-height:88rpx; align-items:center; justify-content:center; margin:0; padding:0; border-radius:14rpx; background:var(--nerae-surface-subtle); color:var(--nerae-text); font-size:30rpx; line-height:1; }
.batch-actions { display:flex; gap:14rpx; }
.batch-actions .set-sheet__done { flex:1.6; width:auto; }
.batch-back { flex:1; min-height:96rpx; margin:0; border-radius:18rpx; background:var(--nerae-surface-subtle); color:var(--nerae-text); font-size:27rpx; }
button::after { border: 0; }
</style>
