<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, shallowRef } from 'vue'
import RecordingEditor from './RecordingEditor.vue'
import RecordingCompleted from './RecordingCompleted.vue'
import RecordingConflictReview from './RecordingConflictReview.vue'
import TrainingHistory from './TrainingHistory.vue'
import { todayInTimezone } from './draftStorage'
import type { BatchSetField, RecordingDraft, RecordingMode, InputField, V2Measure } from './recording'
import type { SideState, V2SideName } from './recordingV2Measure'
import { discoverAppTrainingDrafts, createAppTrainingEditor, attachAppTrainingEditor, nextAppTrainingRequestKey, restoreAppCloudRecordingV2 } from '../../platform/app/nativeTrainingEditor'
import type { AuthSessionCoordinator } from '../auth/sessionCoordinator'
import type { EditorResult, TrainingEditorController } from './trainingEditorController'
import type { RecordingApi } from './recordingApi'
import type { RecordingV2Detail } from './recordingV2Projection'
import { syncRecordingV2, type RecordingV2SyncState } from './syncRecordingV2'
import { reviewRecordingV2Conflict, resolveRecordingV2Conflict, type RecordingConflictReview as ConflictReview } from './recordingV2Conflict'
import type { ConflictChoice } from './recordingV2ConflictPlan'

const props = defineProps<{ session: AuthSessionCoordinator; timeZone: string; recordingApi?: RecordingApi | null; exerciseLabels?: Record<string, string> }>()
const emit = defineEmits<{ exit: []; 'session-changed': []; 'draft-closed': [] }>()

type DraftItem = { eventId: string; date: string | null; exerciseCount: number; localError: boolean }
const initialSession = props.session.snapshot()
const controller = shallowRef<TrainingEditorController | null>(null)
const draft = shallowRef<RecordingDraft | null>(null)
const editorView = ref<InstanceType<typeof RecordingEditor> | null>(null)
const showCompletion = ref(false)
const cloudDetail = shallowRef<RecordingV2Detail | null>(null)
const conflictReview = shallowRef<ConflictReview | null>(null)
const conflictChoices = ref<Record<string, ConflictChoice>>({})
const conflictBusy = ref(false)
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
const syncState = ref<RecordingV2SyncState | 'syncing'>('pending')
const localSaveFailureMessage = '本机保存尚未确认，输入暂留在当前页面；关闭应用可能丢失，请先重试。'
let alive = true
let sessionInvalid = false
let stopWatching: (() => void) | null = null
let syncTimer: ReturnType<typeof setTimeout> | null = null
let syncRunning = false
let syncAgain = false
let editRevision = 0
let networkListener: ((result: { isConnected: boolean }) => void) | null = null

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
  cloudDetail.value = null
  conflictReview.value = null
  conflictChoices.value = {}
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
    if (showCompletion.value && !draft.value.completionRequested) showCompletion.value = false
    return true
  } catch {
    changedSession()
    return false
  }
}

function scheduleSync(target: TrainingEditorController, immediate = false): void {
  if (!current() || controller.value !== target || !props.recordingApi || conflictReview.value || conflictBusy.value || target.draft.recordFormat !== 2 ||
      (!target.draft.syncPending && !target.draft.server &&
        !target.draft.exercises.some(item => item.sets.length || item.removedSets?.length))) return
  syncAgain = true
  if (syncRunning) return
  if (syncTimer) clearTimeout(syncTimer)
  if (immediate) { syncTimer = null; void runSync(target); return }
  syncTimer = setTimeout(() => { syncTimer = null; void runSync(target) }, 650)
}

async function runSync(target: TrainingEditorController): Promise<void> {
  if (syncRunning || !props.recordingApi || !current() || controller.value !== target) return
  syncRunning = true
  try {
    do {
      syncAgain = false
      const revision = editRevision
      const wasComplete = target.draft.completionRequested === true
      syncState.value = 'syncing'
      const result = await syncRecordingV2(target, props.recordingApi, props.session, nextAppTrainingRequestKey)
      if (!refresh(target)) return
      if (result.state === 'session_changed') { changedSession(); return }
      if (revision !== editRevision) {
        syncState.value = 'pending'
        if (pending.value === 0) syncAgain = true
      } else {
        syncState.value = result.state
        cloudDetail.value = result.state === 'synced' ? result.snapshot?.detail || null : null
        if (!wasComplete && result.state === 'synced' && target.draft.completionRequested) showCompletion.value = true
      }
      if (result.state === 'conflict' || result.state === 'invalid' || result.state === 'unsupported' || result.state === 'local_error') syncAgain = false
    } while (syncAgain && current() && controller.value === target)
  } finally {
    syncRunning = false
    if (syncAgain && current() && controller.value && controller.value !== target) scheduleSync(controller.value, true)
  }
}

async function prepareNewFormat(target: TrainingEditorController): Promise<void> {
  const value = target.draft
  if (value.recordFormat === 2 || value.server || value.syncPending !== undefined) return
  const result = await target.enableV2Recording()
  if (!result.ok && current()) error.value = result.message || '本机草稿暂不支持安全同步。'
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
  syncState.value = 'pending'
  cloudDetail.value = null
  error.value = ''
  try {
    const result = await attachAppTrainingEditor(props.session, eventId)
    if (!result.ok) {
      if (current()) error.value = '暂时无法打开这条草稿，请重试。'
      return
    }
    if (!current()) { await result.controller.close(); return }
    await prepareNewFormat(result.controller)
    if (!current()) { await result.controller.close(); return }
    controller.value = result.controller
    if (!refresh(result.controller)) await result.controller.close()
    else { showCompletion.value = Boolean(result.controller.draft.completionRequested); scheduleSync(result.controller, true) }
  } catch {
    if (current()) error.value = '暂时无法打开这条草稿，请重试。'
  } finally {
    opening.value = false
  }
}

async function openCloud(sessionId: string, eventId: string): Promise<void> {
  if (opening.value || loading.value || controller.value || !current() || !props.recordingApi) return
  opening.value = true
  syncState.value = 'pending'
  cloudDetail.value = null
  showCompletion.value = false
  error.value = ''
  try {
    const restored = await restoreAppCloudRecordingV2(props.session, props.recordingApi, sessionId)
    if (!current()) { if (restored.ok) await restored.controller.close(); return }
    let target: TrainingEditorController
    if (restored.ok) target = restored.controller
    else if (restored.reason === 'local_exists' || restored.reason === 'local_unsynced') {
      const existing = await attachAppTrainingEditor(props.session, eventId)
      if (!existing.ok) { error.value = '本机已有这条训练，暂时无法打开，请稍后重试。'; return }
      target = existing.controller
      if (target.draft.recordFormat !== 2 || target.draft.server?.id !== sessionId) {
        await target.close();error.value = '本机已有内容需要核对，已保留原记录。';return
      }
    } else {
      error.value = restored.reason === 'unsupported' ? '这条训练暂不支持在此设备编辑。' :
        '暂时无法打开云端记录，请检查网络后重试。'
      return
    }
    if (!current()) { await target.close(); return }
    controller.value = target
    if (!refresh(target)) await target.close()
    else scheduleSync(target, true)
  } catch {
    if (current()) error.value = '暂时无法打开云端记录，请稍后重试。'
  } finally { opening.value = false }
}

async function create(): Promise<void> {
  if (opening.value || loading.value || controller.value || !current()) return
  opening.value = true
  syncState.value = 'pending'
  cloudDetail.value = null
  showCompletion.value = false
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
    await prepareNewFormat(result.controller)
    if (!current()) { await result.controller.close(); return }
    controller.value = result.controller
    if (!refresh(result.controller)) await result.controller.close()
  } catch {
    if (current()) error.value = '暂时无法新建记录，请检查本机存储后重试。'
  } finally {
    opening.value = false
  }
}

async function change(action: (target: TrainingEditorController) => Promise<EditorResult>, structural = false): Promise<EditorResult | undefined> {
  const target = controller.value
  if (!target || !current() || (structural && pending.value > 0)) return
  pending.value++
  editRevision++
  syncState.value = 'pending'
  cloudDetail.value = null
  error.value = ''
  try {
    // Mutations update the controller before their first await. Refresh now so
    // successive input events are not blocked while earlier writes are queued.
    const operation = action(target)
    refresh(target)
    const result = await operation
    if (!refresh(target)) return
    if (!result.ok && result.reason !== 'pending' && result.reason !== 'superseded') {
      error.value = result.reason === 'storage_error' ? localSaveFailureMessage :
        result.message || '暂时无法完成，请检查输入后重试。'
    }
    return result
  } catch {
    if (refresh(target)) error.value = '操作未完成，请重试。'
  } finally {
    pending.value--
    if (refresh(target) && pending.value === 0) scheduleSync(target)
  }
}

async function finish(): Promise<void> {
  const target = controller.value
  if (!target || !current() || pending.value > 0) return
  const result = await change(item => item.requestV2Completion(), true)
  if (result?.ok && current() && controller.value === target && target.draft.completionRequested) {
    showCompletion.value = true
    scheduleSync(target, true)
  }
}

function conflictFailure(reason: string): string {
  if (reason === 'pending') return '有一笔同步结果尚未确认，本机内容已保留；请联网后重试。'
  if (reason === 'stale') return '记录刚刚又有变化，两边内容已保留，请重新查看差异。'
  if (reason === 'unsupported') return '两边的动作或组结构不同，内容已保留，暂不能在这里合并。'
  if (reason === 'local_error') return localSaveFailureMessage
  if (reason === 'invalid_choice') return '请为每一项选择要保留的内容。'
  return '暂时无法核对记录，请检查网络后重试；本机内容已保留。'
}
async function openConflict(): Promise<void> {
  const target = controller.value; const api = props.recordingApi
  if (!target || !api || !current() || pending.value > 0 || syncRunning || conflictBusy.value) return
  if (syncTimer) { clearTimeout(syncTimer); syncTimer = null }
  syncAgain = false; conflictBusy.value = true; error.value = ''
  try {
    const result = await reviewRecordingV2Conflict(target, api, props.session)
    if (!current() || controller.value !== target) return
    if (!result.ok) {
      if (result.reason === 'session_changed') { changedSession(); return }
      error.value = conflictFailure(result.reason); return
    }
    conflictChoices.value = {}; conflictReview.value = result.review
  } finally { conflictBusy.value = false }
}
function chooseConflict(key: string, value: ConflictChoice): void {
  if (!conflictBusy.value && pending.value === 0 && localStatus.value === 'saved_locally' && conflictReview.value?.rows.some(row => row.key === key)) conflictChoices.value = {...conflictChoices.value,[key]:value}
}
function cancelConflict(): void {
  if (conflictBusy.value || pending.value > 0 || localStatus.value !== 'saved_locally') return
  conflictReview.value = null; conflictChoices.value = {}; error.value = ''
}
async function confirmConflict(): Promise<void> {
  const target = controller.value; const api = props.recordingApi; const review = conflictReview.value
  if (!target || !api || !review || !current() || conflictBusy.value || pending.value > 0 || localStatus.value !== 'saved_locally') return
  conflictBusy.value = true; error.value = ''
  let shouldSync = false
  try {
    const result = await resolveRecordingV2Conflict(target, api, props.session, review, conflictChoices.value)
    if (!current() || controller.value !== target) return
    if (!result.ok) {
      if (result.reason === 'session_changed') { changedSession(); return }
      error.value = conflictFailure(result.reason)
      if (result.reason === 'stale') { conflictReview.value = null; conflictChoices.value = {} }
      refresh(target)
      return
    }
    conflictReview.value = null; conflictChoices.value = {}; syncState.value = 'pending'
    refresh(target); shouldSync = true
  } finally {
    conflictBusy.value = false
    if (shouldSync && current() && controller.value === target) scheduleSync(target, true)
  }
}
async function retryLocalSave(): Promise<void> {
  const target = controller.value; const reviewing = Boolean(conflictReview.value)
  const result = await change(item => item.retryLocalSave(), true)
  if (result?.ok && reviewing && target && current() && controller.value === target) {
    conflictReview.value = null; conflictChoices.value = {}; syncState.value = 'pending'
    scheduleSync(target, true)
  }
}

async function editCompletedSet(exercise: string, record: string): Promise<void> {
  showCompletion.value = false
  await nextTick()
  editorView.value?.openSet(exercise, record)
}

function changeMode(mode: RecordingMode): void { void change(target => target.setMode(mode)) }
function changeDate(date: string): void { void change(target => target.setDate(date)) }
function addSet(exercise: string): void { void change(target => target.addGroup(exercise), true) }
function copySet(exercise: string, record: string): void { void change(target => target.copyGroup(exercise, record), true) }
function editSet(exercise: string, record: string, field: InputField, value: string): void {
  void change(target => target.editGroup(exercise, record, field, value))
}
function setMeasure(exercise: string, measure: V2Measure): void { void change(target => target.setMeasure(exercise, measure), true) }
function setSide(exercise: string, record: string, side: V2SideName, state: SideState): void {
  void change(target => target.setGroupSide(exercise, record, side, state))
}
function applyBatch(exercise: string, source: string, records: string[], fields: BatchSetField[], newCount: number, snapshot: string): void {
  void change(target => target.applyBatchGroups(exercise, source, records, fields, newCount, snapshot), true)
}
function confirmSet(exercise: string, record: string): void { void change(target => target.confirmGroup(exercise, record), true) }
function removeSet(exercise: string, record: string): void { void change(target => target.removeGroup(exercise, record), true) }
function restoreSet(exercise: string, record: string): void { void change(target => target.restoreGroup(exercise, record), true) }
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
  if (syncTimer) { clearTimeout(syncTimer); syncTimer = null }
  syncAgain = false
  opening.value = true
  try {
    await target.close()
    if (!current()) return
    controller.value = null
    draft.value = null
    cloudDetail.value = null
    conflictReview.value = null
    conflictChoices.value = {}
    showCompletion.value = false
    emit('draft-closed')
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
  if (conflictBusy.value) { error.value = '正在核对记录，请稍候。'; return false }
  if (opening.value || (target && (pending.value > 0 || target.localStatus !== 'saved_locally'))) {
    error.value = '本机保存尚未确认，请先重试保存再离开。'
    return false
  }
  return true
}
defineExpose({ canLeave })

const statusMessage = computed(() => {
  if (error.value) return error.value
  if (localStatus.value === 'storage_error') return localSaveFailureMessage
  if (pending.value > 0 || localStatus.value === 'saving') return '正在保存到本机…'
  if (localStatus.value === 'saved_locally') {
    if (syncState.value === 'syncing') return '已保存在本机，正在同步…'
    if (syncState.value === 'synced') return draft.value?.completionRequested ? '训练已完成，已同步。' : '已同步。'
    if (syncState.value === 'conflict') return '另一台设备的记录有变化，本机内容已保留，请先核对。'
    if (syncState.value === 'unsupported') return '本机内容已保留，当前内容暂不支持同步。'
    if (syncState.value === 'invalid') return '请检查组内输入，本机内容已保留。'
    if (syncState.value === 'local_error') return localSaveFailureMessage
    return draft.value?.completionRequested ? '训练已保存在本机，待同步。' : '已保存在本机，尚未同步。'
  }
  return '本机保存尚未确认。'
})
const latestAllowedDate = computed(() => {
  void dateRefresh.value
  try { return todayInTimezone(draft.value?.timeZone || props.timeZone, new Date()) } catch { return '' }
})
const canComplete = computed(() => Boolean(draft.value && draft.value.recordFormat === 2 &&
  draft.value.exercises.some(item => item.sets.length > 0)))

onMounted(() => {
  if (!current()) { changedSession(); return }
  try {
    const guard = props.session.captureInstallGuard()
    if (!guard) { changedSession(); return }
    stopWatching = props.session.subscribeInstallGuardInvalidation(guard, changedSession)
    if (!current()) { changedSession(); return }
    if (typeof uni !== 'undefined' && typeof uni.onNetworkStatusChange === 'function') {
      networkListener = result => { if (result.isConnected && controller.value) scheduleSync(controller.value, true) }
      uni.onNetworkStatusChange(networkListener)
    }
    void discover()
  } catch { changedSession() }
})
onBeforeUnmount(() => {
  alive = false
  stopWatching?.()
  stopWatching = null
  if (syncTimer) clearTimeout(syncTimer)
  syncTimer = null
  if (networkListener && typeof uni !== 'undefined' && typeof uni.offNetworkStatusChange === 'function') uni.offNetworkStatusChange(networkListener)
  networkListener = null
  const previous = controller.value
  controller.value = null
  draft.value = null
  conflictReview.value = null
  if (previous) void previous.close()
})
</script>

<template>
  <view class="training-entry">
    <template v-if="!draft">
      <view class="training-entry__heading">
        <text class="training-entry__eyebrow">NERAE · 训练记录</text>
        <text class="training-entry__title">记下实际训练</text>
        <text class="training-entry__caption">查看本机草稿和已同步的训练。</text>
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
        <TrainingHistory v-if="recordingApi" :session="session" :api="recordingApi" @session-changed="changedSession" @edit-v2="openCloud" />
        <button class="training-entry__secondary" :disabled="opening || loading" @tap="emit('exit')">返回</button>
      </view>
    </template>
    <template v-else>
      <view class="editor-toolbar">
        <button :disabled="pending > 0" aria-label="返回草稿列表" @tap="closeEditor">‹</button>
        <text>训练记录</text>
        <button v-if="syncState === 'conflict' &amp;&amp; !conflictReview" class="editor-toolbar__conflict"
          :disabled="conflictBusy || pending > 0" @tap="openConflict">查看差异</button>
        <view v-else class="editor-toolbar__spacer" />
      </view>
      <view v-if="enteringExercise &amp;&amp; !conflictReview" class="exercise-form">
        <text class="exercise-form__title">添加训练动作</text>
        <input :value="exerciseName" maxlength="60" placeholder="例如：深蹲" @input="exerciseName = inputValue($event)" />
        <view class="exercise-form__actions">
          <button :disabled="pending > 0" @tap="cancelExercise">取消</button>
          <button :disabled="!exerciseName.trim() || pending > 0" @tap="addExercise">添加</button>
        </view>
      </view>
      <button v-if="localStatus === 'storage_error'" class="training-entry__retry" :disabled="pending > 0"
        @tap="retryLocalSave">重试本机保存</button>
      <RecordingConflictReview v-if="conflictReview" :review="conflictReview" :choices="conflictChoices" :busy="conflictBusy || pending > 0 || localStatus !== 'saved_locally'" :error="error"
        @choose="chooseConflict" @cancel="cancelConflict" @confirm="confirmConflict" />
      <RecordingCompleted v-else-if="showCompletion" :draft="draft" :status-message="statusMessage" :busy="pending > 0" :cloud="cloudDetail"
        @edit="showCompletion = false" @edit-set="editCompletedSet" @copy-set="copySet" @remove-set="removeSet" @restore-set="restoreSet" />
      <RecordingEditor v-else ref="editorView" :draft="draft" :latest-allowed-date="latestAllowedDate" :exercise-labels="exerciseLabels || {}" :issues="[]"
        :status-message="statusMessage" :saving="pending > 0" :can-save="canComplete"
        @change-mode="changeMode" @prepare-date="dateRefresh++" @change-date="changeDate" @add-exercise="enteringExercise = true" @add-set="addSet" @copy-set="copySet"
        @edit-set="editSet" @set-measure="setMeasure" @set-side="setSide" @apply-batch="applyBatch" @confirm-set="confirmSet" @remove-set="removeSet" @restore-set="restoreSet" @save="finish" />
    </template>
  </view>
</template>

<style scoped>
.training-entry { min-height: 100vh; background: var(--nerae-surface); color: var(--nerae-text); }
.training-entry__heading { display: flex; flex-direction: column; gap: 14rpx; padding: 52rpx 40rpx 38rpx; background: var(--nerae-surface); color: var(--nerae-text); }
.training-entry__eyebrow { color: var(--nerae-text-secondary); font-size: 21rpx; letter-spacing: 3rpx; }
.training-entry__title { font-family: "Songti SC", "Noto Serif SC", serif; font-size: 46rpx; font-weight: 700; }
.training-entry__caption { color: var(--nerae-text-secondary); font-size: 25rpx; line-height: 1.5; }
.training-entry__heading .training-entry__caption { color: var(--nerae-text-secondary); }
.training-entry__body { display: flex; flex-direction: column; gap: 20rpx; padding: 36rpx 32rpx; }
.training-entry__error { color: var(--nerae-text); font-size: 24rpx; line-height: 1.5; }
.training-entry__empty { padding: 32rpx 0; color: var(--nerae-text-secondary); font-size: 25rpx; }
.training-entry__primary, .training-entry__secondary, .training-entry__retry { width: 100%; margin: 0; min-height: 88rpx; border-radius: 18rpx; font-size: 27rpx; }
.training-entry__primary, .training-entry__retry { background: var(--nerae-primary); color: var(--nerae-primary-text); }
.training-entry__secondary { background: var(--nerae-surface-subtle); color: var(--nerae-text); }
.draft-row, .editor-toolbar { display: flex; align-items: center; justify-content: space-between; gap: 20rpx; padding: 24rpx; background: var(--nerae-surface); border-radius: 20rpx; }
.draft-row__text { display: flex; flex-direction: column; gap: 8rpx; }
.draft-row__title { font-size: 28rpx; font-weight: 600; }
.draft-row__detail, .editor-toolbar { color: var(--nerae-text-secondary); font-size: 23rpx; }
.draft-row__button { margin: 0; background: var(--nerae-surface-subtle); font-size: 24rpx; }
.editor-toolbar { padding: 0 24rpx; min-height: 88rpx; border-radius: 0; background: var(--nerae-surface); color: var(--nerae-text); font-size: 27rpx; font-weight: 650; }
.editor-toolbar button, .editor-toolbar__spacer { flex: 0 0 88rpx; width: 88rpx; }
.editor-toolbar button { min-height: 88rpx; margin: 0; padding: 0; background: transparent; color: var(--nerae-text); font-size: 42rpx; line-height: 88rpx; }
.editor-toolbar .editor-toolbar__conflict { flex:0 0 auto; width:auto; font-size:24rpx; }
.exercise-form { display: flex; flex-direction: column; gap: 18rpx; margin: 24rpx; padding: 24rpx; border: 1px solid var(--nerae-border-decorative); border-radius: 20rpx; background: var(--nerae-surface); }
.exercise-form__title { font-size: 28rpx; font-weight: 600; }
.exercise-form input { min-height: 84rpx; padding: 0 20rpx; border: 1px solid var(--nerae-border-control); border-radius: 16rpx; }
.exercise-form__actions { display: flex; gap: 16rpx; }
.exercise-form__actions button { flex: 1; font-size: 25rpx; }
button::after { border: 0; }
</style>
