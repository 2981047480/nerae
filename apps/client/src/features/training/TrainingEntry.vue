<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, shallowRef } from 'vue'
import RecordingEditor from './RecordingEditor.vue'
import { todayInTimezone } from './draftStorage'
import type { RecordingDraft, RecordingMode, InputField } from './recording'
import { discoverAppTrainingDrafts, createAppTrainingEditor, attachAppTrainingEditor } from '../../platform/app/nativeTrainingEditor'
import type { AuthSessionCoordinator } from '../auth/sessionCoordinator'
import type { EditorResult, TrainingEditorController } from './trainingEditorController'

const props = defineProps<{ session: AuthSessionCoordinator; timeZone: string; exerciseLabels?: Record<string, string> }>()
const emit = defineEmits<{ exit: []; 'session-changed': [] }>()

type DraftItem = { eventId: string; date: string | null; exerciseCount: number; localError: boolean }
const initialSession = props.session.snapshot()
const controller = shallowRef<TrainingEditorController | null>(null)
const draft = shallowRef<RecordingDraft | null>(null)
const items = shallowRef<DraftItem[]>([])
const nextCursor = ref<string | null>(null)
const loading = ref(false)
const opening = ref(false)
const pending = ref(0)
const localStatus = ref<'not_saved' | 'saving' | 'saved_locally' | 'storage_error'>('not_saved')
const error = ref('')
const recoveryWarning = ref(false)
const enteringExercise = ref(false)
const exerciseName = ref('')
const dateRefresh = ref(0)
let alive = true
let sessionInvalid = false
let stopWatching: (() => void) | null = null

function current(): boolean {
  return alive && !sessionInvalid && Boolean(initialSession && initialSession.user.ageVerified === true &&
    props.session.isCurrent(initialSession.subjectId, initialSession.generation))
}

function changedSession(): void {
  if (!alive || sessionInvalid) return
  sessionInvalid = true
  items.value = []
  nextCursor.value = null
  draft.value = null
  const previous = controller.value
  controller.value = null
  if (previous) void previous.close()
  error.value = '登录状态已变化，请重新登录。'
  emit('session-changed')
}

function refresh(target: TrainingEditorController): boolean {
  if (!current() || controller.value !== target) return false
  try {
    draft.value = target.draft
    localStatus.value = target.localStatus
    return true
  } catch {
    changedSession()
    return false
  }
}

async function inspectDraft(eventId: string): Promise<DraftItem | null> {
  const result = await attachAppTrainingEditor(props.session, eventId)
  if (!result.ok) return { eventId, date: null, exerciseCount: 0, localError: true }
  try {
    if (!current()) return null
    const value = result.controller.draft
    return { eventId, date: value.date, exerciseCount: value.exercises.length,
      localError: result.controller.localStatus === 'storage_error' }
  } catch {
    return { eventId, date: null, exerciseCount: 0, localError: true }
  } finally {
    await result.controller.close()
  }
}

async function discover(reset = true): Promise<void> {
  if (loading.value || opening.value || controller.value || !current() || (!reset && !nextCursor.value)) return
  loading.value = true
  error.value = ''
  try {
    const page = await discoverAppTrainingDrafts(props.session, reset ? '' : nextCursor.value || '')
    if (!current()) return
    if (!page.ok) {
      if (page.reason === 'session_changed') changedSession()
      else error.value = '暂时无法读取本机草稿，请重试。'
      return
    }
    const found: DraftItem[] = []
    for (const eventId of page.eventIds) {
      const item = await inspectDraft(eventId)
      if (!current()) return
      if (item) found.push(item)
    }
    const merged = reset ? found : [...items.value, ...found]
    items.value = [...new Map(merged.map(item => [item.eventId, item])).values()]
    nextCursor.value = page.nextCursor
    recoveryWarning.value = (reset ? false : recoveryWarning.value) || page.hasUnclassifiedItems
  } catch {
    if (current()) error.value = '暂时无法读取本机草稿，请重试。'
  } finally {
    loading.value = false
  }
}

async function open(eventId: string): Promise<void> {
  if (opening.value || loading.value || controller.value || !current()) return
  opening.value = true
  error.value = ''
  try {
    const result = await attachAppTrainingEditor(props.session, eventId)
    if (!result.ok) {
      if (current()) error.value = '暂时无法打开这条草稿，请重试。'
      return
    }
    if (!current()) { await result.controller.close(); return }
    controller.value = result.controller
    if (!refresh(result.controller)) await result.controller.close()
  } catch {
    if (current()) error.value = '暂时无法打开这条草稿，请重试。'
  } finally {
    opening.value = false
  }
}

async function create(): Promise<void> {
  if (opening.value || loading.value || controller.value || !current()) return
  opening.value = true
  error.value = ''
  try {
    const date = todayInTimezone(props.timeZone, new Date())
    const result = await createAppTrainingEditor(props.session, date, props.timeZone)
    if (!result.ok) {
      if (current()) error.value = result.reason === 'outcome_unknown'
        ? '新草稿的保存结果尚未确认，请先重新查看草稿。'
        : '暂时无法新建记录，请检查本机存储后重试。'
      return
    }
    if (!current()) { await result.controller.close(); return }
    controller.value = result.controller
    if (!refresh(result.controller)) await result.controller.close()
  } catch {
    if (current()) error.value = '暂时无法新建记录，请检查本机存储后重试。'
  } finally {
    opening.value = false
  }
}

async function change(action: (target: TrainingEditorController) => Promise<EditorResult>, structural = false): Promise<void> {
  const target = controller.value
  if (!target || !current() || (structural && pending.value > 0)) return
  pending.value++
  error.value = ''
  try {
    // Mutations update the controller before their first await. Refresh now so
    // successive input events are not blocked while earlier writes are queued.
    const operation = action(target)
    refresh(target)
    const result = await operation
    if (!refresh(target)) return
    if (!result.ok && result.reason !== 'pending' && result.reason !== 'superseded') {
      error.value = result.reason === 'storage_error' ? '本机保存尚未确认，输入暂留在当前页面。' :
        result.message || '暂时无法完成，请检查输入后重试。'
    }
  } catch {
    if (refresh(target)) error.value = '操作未完成，请重试。'
  } finally {
    pending.value--
    refresh(target)
  }
}

function changeMode(mode: RecordingMode): void { void change(target => target.setMode(mode)) }
function changeDate(date: string): void { void change(target => target.setDate(date)) }
function addSet(exercise: string): void { void change(target => target.addGroup(exercise), true) }
function editSet(exercise: string, record: string, field: InputField, value: string): void {
  void change(target => target.editGroup(exercise, record, field, value))
}
function confirmSet(exercise: string, record: string): void { void change(target => target.confirmGroup(exercise, record), true) }
function removeSet(exercise: string, record: string): void { void change(target => target.removeGroup(exercise, record), true) }
function cancelExercise(): void {
  if (pending.value > 0) return
  exerciseName.value = ''
  enteringExercise.value = false
  error.value = ''
}
function inputValue(event: unknown): string {
  if (!event || typeof event !== 'object' || !('detail' in event)) return ''
  const detail = event.detail
  return detail && typeof detail === 'object' && 'value' in detail && typeof detail.value === 'string' ? detail.value : ''
}
async function addExercise(): Promise<void> {
  const name = exerciseName.value.trim()
  if (pending.value > 0) return
  if (!name || Array.from(name).length > 60) {
    error.value = '请输入 1 至 60 个字的动作名称。'
    return
  }
  const target = controller.value
  if (!target || !current()) return
  const before = new Set(target.draft.exercises.map(item => item.exerciseEntryKey))
  await change(item => item.addCustomExercise(name, 'weight'), true)
  if (current() && controller.value === target && target.draft.exercises.some(item =>
    !before.has(item.exerciseEntryKey))) {
    exerciseName.value = ''
    enteringExercise.value = false
  }
}

async function closeEditor(): Promise<void> {
  const target = controller.value
  if (!target || !current()) return
  if (!canLeave()) return
  opening.value = true
  try {
    await target.close()
    if (!current()) return
    controller.value = null
    draft.value = null
    cancelExercise()
  } catch {
    if (current()) error.value = '暂时无法关闭记录，请重试。'
  } finally {
    opening.value = false
  }
  if (current() && !controller.value) await discover()
}

/** The owning page must consult this before navigation or a system back action. */
function canLeave(): boolean {
  const target = controller.value
  if (opening.value || (target && (pending.value > 0 || target.localStatus !== 'saved_locally'))) {
    error.value = '本机保存尚未确认，请先重试保存再离开。'
    return false
  }
  return true
}
defineExpose({ canLeave })

const statusMessage = computed(() => {
  if (error.value) return error.value
  if (localStatus.value === 'storage_error') return '本机保存尚未确认，输入暂留在当前页面。'
  if (pending.value > 0 || localStatus.value === 'saving') return '正在保存到本机…'
  if (localStatus.value === 'saved_locally') return '已保存在本机，尚未同步。'
  return '本机保存尚未确认。'
})
const latestAllowedDate = computed(() => {
  void dateRefresh.value
  try { return todayInTimezone(draft.value?.timeZone || props.timeZone, new Date()) } catch { return '' }
})

onMounted(() => {
  if (!current()) { changedSession(); return }
  try {
    const guard = props.session.captureInstallGuard()
    if (!guard) { changedSession(); return }
    stopWatching = props.session.subscribeInstallGuardInvalidation(guard, changedSession)
    if (!current()) { changedSession(); return }
    void discover()
  } catch { changedSession() }
})
onBeforeUnmount(() => {
  alive = false
  stopWatching?.()
  stopWatching = null
  const previous = controller.value
  controller.value = null
  draft.value = null
  if (previous) void previous.close()
})
</script>

<template>
  <view class="training-entry">
    <template v-if="!draft">
      <view class="training-entry__heading">
        <text class="training-entry__eyebrow">NERAE · 训练记录</text>
        <text class="training-entry__title">记下实际训练</text>
        <text class="training-entry__caption">草稿保存在这台设备，当前尚未同步。</text>
      </view>
      <view class="training-entry__body">
        <text v-if="error" class="training-entry__error" role="alert">{{ error }}</text>
        <text v-if="recoveryWarning" class="training-entry__error">有部分本机草稿暂时无法识别，原数据仍保留。</text>
        <button class="training-entry__primary" :disabled="loading || opening || sessionInvalid" @tap="create">新建记录</button>
        <text v-if="loading" class="training-entry__caption">正在查找本机草稿…</text>
        <text v-else-if="!error &amp;&amp; !recoveryWarning &amp;&amp; items.length === 0" class="training-entry__empty">还没有本机草稿。</text>
        <view v-for="item in items" :key="item.eventId" class="draft-row">
          <view class="draft-row__text">
            <text class="draft-row__title">{{ item.date || '无法读取的草稿' }}</text>
            <text class="draft-row__detail">{{ item.date ? `${item.exerciseCount} 个动作` : '请稍后重试' }}{{ item.localError ? ' · 保存待确认' : '' }}</text>
          </view>
          <button class="draft-row__button" :disabled="opening || loading || sessionInvalid" @tap="open(item.eventId)">继续</button>
        </view>
        <button v-if="nextCursor" class="training-entry__secondary" :disabled="loading || opening" @tap="discover(false)">加载更多</button>
        <button v-if="error &amp;&amp; !sessionInvalid" class="training-entry__secondary" :disabled="loading || opening" @tap="discover()">重新查找</button>
        <button class="training-entry__secondary" :disabled="opening || loading" @tap="emit('exit')">返回</button>
      </view>
    </template>
    <template v-else>
      <view class="editor-toolbar">
        <button :disabled="pending > 0" aria-label="返回草稿列表" @tap="closeEditor">‹</button>
        <text>训练记录</text>
        <view class="editor-toolbar__spacer" />
      </view>
      <view v-if="enteringExercise" class="exercise-form">
        <text class="exercise-form__title">添加训练动作</text>
        <input :value="exerciseName" maxlength="60" placeholder="例如：深蹲" @input="exerciseName = inputValue($event)" />
        <view class="exercise-form__actions">
          <button :disabled="pending > 0" @tap="cancelExercise">取消</button>
          <button :disabled="!exerciseName.trim() || pending > 0" @tap="addExercise">添加</button>
        </view>
      </view>
      <button v-if="localStatus === 'storage_error'" class="training-entry__retry" :disabled="pending > 0"
        @tap="change(target => target.retryLocalSave(), true)">重试本机保存</button>
      <RecordingEditor :draft="draft" :latest-allowed-date="latestAllowedDate" :exercise-labels="exerciseLabels || {}" :issues="[]"
        :status-message="statusMessage" :saving="pending > 0" :can-save="false"
        @change-mode="changeMode" @prepare-date="dateRefresh++" @change-date="changeDate" @add-exercise="enteringExercise = true" @add-set="addSet"
        @edit-set="editSet" @confirm-set="confirmSet" @remove-set="removeSet" />
    </template>
  </view>
</template>

<style scoped>
.training-entry { min-height: 100vh; background: #f3f4f1; color: #252c29; }
.training-entry__heading { display: flex; flex-direction: column; gap: 14rpx; padding: 52rpx 40rpx 38rpx; background: #252c29; color: #fff; }
.training-entry__eyebrow { color: #c4cbc5; font-size: 21rpx; letter-spacing: 3rpx; }
.training-entry__title { font-size: 46rpx; font-weight: 650; }
.training-entry__caption { color: #707a73; font-size: 25rpx; line-height: 1.5; }
.training-entry__heading .training-entry__caption { color: #d4d9d5; }
.training-entry__body { display: flex; flex-direction: column; gap: 20rpx; padding: 36rpx 32rpx; }
.training-entry__error { color: #9c3c2d; font-size: 24rpx; line-height: 1.5; }
.training-entry__empty { padding: 32rpx 0; color: #707a73; font-size: 25rpx; }
.training-entry__primary, .training-entry__secondary, .training-entry__retry { width: 100%; margin: 0; min-height: 88rpx; border-radius: 18rpx; font-size: 27rpx; }
.training-entry__primary, .training-entry__retry { background: #f08b65; color: #252c29; }
.training-entry__secondary { background: #fff; color: #35443b; }
.draft-row, .editor-toolbar { display: flex; align-items: center; justify-content: space-between; gap: 20rpx; padding: 24rpx; background: #fff; border-radius: 20rpx; }
.draft-row__text { display: flex; flex-direction: column; gap: 8rpx; }
.draft-row__title { font-size: 28rpx; font-weight: 600; }
.draft-row__detail, .editor-toolbar { color: #606a63; font-size: 23rpx; }
.draft-row__button { margin: 0; background: #f3f4f1; font-size: 24rpx; }
.editor-toolbar { padding: 0 24rpx; min-height: 88rpx; border-radius: 0; background: #f3f4f1; color: #252c29; font-size: 27rpx; font-weight: 650; }
.editor-toolbar button, .editor-toolbar__spacer { flex: 0 0 88rpx; width: 88rpx; }
.editor-toolbar button { min-height: 88rpx; margin: 0; padding: 0; background: transparent; color: #252c29; font-size: 42rpx; line-height: 88rpx; }
.exercise-form { display: flex; flex-direction: column; gap: 18rpx; margin: 24rpx; padding: 24rpx; border-radius: 20rpx; background: #fff; }
.exercise-form__title { font-size: 28rpx; font-weight: 600; }
.exercise-form input { min-height: 84rpx; padding: 0 20rpx; border: 1px solid #dce2dc; border-radius: 16rpx; }
.exercise-form__actions { display: flex; gap: 16rpx; }
.exercise-form__actions button { flex: 1; font-size: 25rpx; }
button::after { border: 0; }
</style>
