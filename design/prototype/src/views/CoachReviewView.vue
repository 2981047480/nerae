<script setup lang="ts">
import { computed, nextTick, onMounted, ref, watch } from 'vue'
import {
  AlertCircle,
  Check,
  CheckCircle2,
  Plus,
  Trash2,
} from '@lucide/vue'
import BaseSheet from '../components/BaseSheet.vue'
import {
  activeCoachDay,
  activeDayTotals,
  addExercise,
  coachChanges,
  coachDirty,
  coachDeliveries,
  coachIssues,
  coachPlanTotals,
  coachPublishDisabled,
  coachStatus,
  coachSafetyBlock,
  coachTechError,
  coachConflictPreview,
  coachLatestDelivery,
  confirmPublish,
  consumeFocusRequest,
  focusValidationIssue,
  requestPublish,
  removeExercise,
  saveDraft,
  selectCoachDay,
  setCoachMode,
  storageState,
  retryPersist,
  updateDayTitle,
  updatePlanField,
  selectedStudent,
  selectedStudentEvidence,
  selectedStudentHealthAuthorized,
  state,
  ui,
  undoRemoveExercise,
} from '../state/store'
import { changeDisplayValue, changesForExercise, dayTotals, planTotals } from '../utils/plan'
import { formatDateTime as formatDateTimeValue } from '../utils/format'
import { isElementActuallyVisible } from '../utils/dom'
import type { PlanChange, PlanExercise, PlanValidationIssue } from '../types'

const day = activeCoachDay
const totals = activeDayTotals
const allTotals = coachPlanTotals
const changes = coachChanges
const issues = coachIssues
const status = coachStatus
const dirty = coachDirty
const studentName = computed(() => selectedStudent.value?.name ?? '学员')
const studentEvidence = selectedStudentEvidence
const safetyBlock = coachSafetyBlock
const techError = coachTechError
const conflictPreview = coachConflictPreview
const previewMode = computed(() => state.preview.coachPlan)
const latestDelivery = coachLatestDelivery
const isReview = computed(() => ui.coachMode === 'review')
const statusRow = ref<HTMLElement | null>(null)
let deliveredFocusPending = false

const lastDelivery = computed(() => coachDeliveries.value[0] ?? null)
const publishedFirst = computed(
  () => state.coach.published.days[0]?.exercises[0] ?? null,
)
const deliveryFirst = computed(
  () => lastDelivery.value?.content.days[0]?.exercises[0] ?? null,
)

watch(
  () => coachLatestDelivery.value?.version,
  (next, previous) => {
    if (next && next !== previous) deliveredFocusPending = true
  },
)

watch(
  () => ui.sheet,
  (sheet) => {
    if (sheet !== null || !deliveredFocusPending) return
    deliveredFocusPending = false
    nextTick(() => {
      statusRow.value?.focus()
    })
  },
)

const dayChanges = computed(() => {
  if (!day.value) return []
  return changes.value.filter((change) => change.dayId === day.value?.id)
})

const planSummary = computed(() => ({
  version: state.coach.draftVersion,
  publishedVersion: state.coach.publishedVersion,
  activeVersion: state.coach.activeStudentVersion,
  deliveredVersion: coachLatestDelivery.value?.version ?? null,
  title: state.coach.draft.title,
  cycle: state.coach.draft.cycle,
  totals: { ...allTotals.value, days: state.coach.draft.days.length },
}))

const activeIssueCount = computed(() => issues.value.length)
const publishSheetOpen = computed(() => ui.sheet === 'publish')

const evidence = computed(() =>
  studentEvidence.value.slice(0, 4).map((record) => ({
    date: record.dateLabel,
    title: record.title,
    detail: record.summary,
  })),
)

function issueFor(exerciseId: string, field: string): PlanValidationIssue | undefined {
  return issues.value.find(
    (issue) =>
      issue.dayId === day.value?.id &&
      issue.exerciseId === exerciseId &&
      issue.field === field,
  )
}

function rowChanges(exercise: PlanExercise): PlanChange[] {
  if (!day.value) return []
  return changesForExercise(changes.value, day.value.id, exercise.id)
}

function rowHasChange(exercise: PlanExercise): boolean {
  return rowChanges(exercise).length > 0
}

function displayChange(change: PlanChange): { before: string; after: string } {
  return changeDisplayValue(change)
}

function onFieldInput(
  exerciseId: string,
  field: 'name' | 'sets' | 'target' | 'restSeconds' | 'note',
  event: Event,
): void {
  if (!day.value) return
  const value = (event.target as HTMLInputElement | HTMLTextAreaElement).value
  updatePlanField(day.value.id, exerciseId, field, value)
}

function onDayTitleInput(event: Event): void {
  if (!day.value) return
  updateDayTitle(day.value.id, (event.target as HTMLInputElement).value)
}


const dayTitleIssue = computed(() =>
  issues.value.find((issue) => issue.dayId === day.value?.id && issue.field === 'dayTitle'),
)

function fieldId(exerciseId: string, field: string): string {
  return `${day.value?.id ?? ''}:${exerciseId}:${field}`
}

function focusField(exerciseId: string, field: string): void {
  const selector = field === 'dayTitle'
    ? `[data-field-id="day:${day.value?.id ?? ''}"]`
    : `[data-field-id="${fieldId(exerciseId, field)}"]`
  const element = document.querySelector<HTMLElement>(selector)
  element?.focus()
  element?.scrollIntoView({ block: 'center', behavior: 'auto' })
}

function applyFocusRequest(): void {
  const request = ui.focusRequest
  if (!request) return
  if (request.exerciseId === '' && request.field === 'name') {
    consumeFocusRequest()
    const addButton = document.querySelector<HTMLElement>('[data-add-exercise]')
    addButton?.focus()
    return
  }
  if (request.dayId !== day.value?.id) return
  nextTick(() => {
    const consumed = consumeFocusRequest()
    if (!consumed) return
    focusField(consumed.exerciseId, consumed.field)
  })
}

watch(
  () => ui.focusRequest,
  () => applyFocusRequest(),
)

watch(
  () => ui.coachDayId,
  () => {
    applyFocusRequest()
  },
)

onMounted(() => applyFocusRequest())

function removeWithUndo(exercise: PlanExercise): void {
  if (!day.value) return
  removeExercise(day.value.id, exercise.id)
}

function handleSave(): void {
  saveDraft()
}

function publishReturnFocus(): HTMLElement | null {
  const triggers = Array.from(document.querySelectorAll<HTMLElement>('[data-publish-trigger]'))
  const visible = triggers.find((element) => isElementActuallyVisible(element))
  return visible ?? statusRow.value
}

function handleRetrySave(): void {
  if (dirty.value) {
    saveDraft()
    return
  }
  retryPersist()
}

function handlePublish(): void {
  requestPublish()
}

function toggleMode(mode: 'edit' | 'review'): void {
  setCoachMode(mode)
}

function selectDay(id: string): void {
  selectCoachDay(id)
}

function locateFirstIssue(): void {
  const first = issues.value[0]
  if (first) focusValidationIssue(first)
}

function issueLabel(issue: PlanValidationIssue): string {
  if (issue.field === 'dayTitle') return `${issue.dayTitle}：${issue.message}`
  return `${issue.exerciseName ?? issue.dayTitle}：${issue.message}`
}

const statusIcon = computed(() => {
  if (status.value.tone === 'positive') return CheckCircle2
  if (status.value.tone === 'warning') return AlertCircle
  return Check
})

const dateOfSave = computed(() => {
  if (!state.coach.savedAt) return ''
  return formatDateTimeValue(state.coach.savedAt)
})
</script>

<template>
  <div class="coach-root">
  <div class="page coach">
    <header class="coach-head">
      <div class="coach-heading">
        <p class="overline">学员计划</p>
        <h1 class="coach-title">{{ planSummary.title }} · {{ planSummary.cycle }}</h1>
        <p class="coach-context">
          <span>{{ studentName }}</span>
          <span class="dot-sep" aria-hidden="true">·</span>
          <span class="num">现用版本 {{ planSummary.activeVersion }}</span>
          <span class="dot-sep" aria-hidden="true">·</span>
          <span class="num">草稿版本 {{ planSummary.version }}</span>
        </p>
      </div>
      <div class="coach-actions">
        <button type="button" class="btn btn--ghost" @click="handleSave">保存草稿</button>
        <button
          type="button"
          class="btn btn--accent"
          data-publish-trigger
          :disabled="coachPublishDisabled"
          @click="handlePublish"
        >
          {{ coachPublishDisabled ? `已发布版本 ${planSummary.deliveredVersion}` : `发布给${studentName}` }}
        </button>
      </div>
    </header>

    <div
      ref="statusRow"
      class="status-row"
      :class="[
        `status-row--${status.tone}`,
        { 'status-row--error': ui.saveNoteTone === 'error' || !storageState.ok },
      ]"
      aria-live="polite"
      tabindex="-1"
    >
      <component :is="statusIcon" :size="15" aria-hidden="true" />
      <p>{{ ui.saveNote ?? status.text }}</p>
      <span v-if="activeIssueCount" class="status-pending num">{{ activeIssueCount }} 项待补充</span>
      <button
        v-if="!storageState.ok || ui.saveNoteTone === 'error'"
        type="button"
        class="status-retry"
        @click="handleRetrySave()"
      >
        重试保存
      </button>
    </div>

    <div v-if="safetyBlock" class="state-banner state-banner--error" role="alert">
      <div>
        <p class="state-banner-title">安全阻断 · 暂不能发布</p>
        <p class="state-banner-text">{{ safetyBlock }}</p>
        <p class="state-banner-text">当前草稿仍保留。先核对动作范围与是否需要专业判断；安全阻断不会被当成技术失败或自动绕过。</p>
      </div>
    </div>

    <div v-else-if="techError" class="state-banner state-banner--warning" role="alert">
      <div>
        <p class="state-banner-title">技术失败 · 没有发布</p>
        <p class="state-banner-text">{{ techError }}</p>
        <button type="button" class="btn btn--ghost btn--sm state-banner-action" @click="handleRetrySave()">重试保存</button>
      </div>
    </div>

    <div v-else-if="conflictPreview" class="state-banner state-banner--warning" role="alert">
      <div>
        <p class="state-banner-title">版本冲突 · 本地版本 {{ conflictPreview.localVersion }} / 另一处版本 {{ conflictPreview.remoteVersion }}</p>
        <p class="state-banner-text">{{ conflictPreview.text }}</p>
        <p class="state-banner-text">下面的完整方案仍可查看与编辑；差异没有被静默覆盖。</p>
      </div>
    </div>

    <div v-else-if="previewMode === 'publishing'" class="state-banner" role="status">
      <div>
        <p class="state-banner-title">正在保存并更新交付状态</p>
        <p class="state-banner-text">保存完成后会显示交付版本与保存时间；学员在你发布前仍使用现用版本。</p>
      </div>
    </div>

    <div v-else-if="previewMode === 'ai-draft'" class="state-banner state-banner--warning" role="status">
      <div>
        <p class="state-banner-title">已有模板整理的草稿 · 待你核对</p>
        <p class="state-banner-text">这份草稿有 {{ changes.length }} 处相对现用版本的变化说明，来自已有模板，不会自动发布，也不代替安全判断。完整方案仍可编辑。</p>
      </div>
    </div>

    <div v-else-if="previewMode === 'delivered' && latestDelivery" class="state-banner state-banner--positive" role="status">
      <div>
        <p class="state-banner-title">版本 {{ latestDelivery.version }} 已交付 {{ studentName }}</p>
        <p class="state-banner-text">学员现用版本 {{ planSummary.activeVersion }} 仍未改变，等待学员采用。发布不会自动替换学员安排。</p>
      </div>
    </div>

    <div v-if="ui.publishBlocked && activeIssueCount" class="issue-banner" role="alert">
      <div class="issue-text">
        <p class="issue-title">还有 {{ activeIssueCount }} 项需要补充，补齐后才能发布。</p>
        <ul class="issue-list">
          <li v-for="issue in issues.slice(0, 3)" :key="`${issue.dayId}-${issue.exerciseId}-${issue.field}`">
            {{ issueLabel(issue) }}
          </li>
          <li v-if="activeIssueCount > 3">另有 {{ activeIssueCount - 3 }} 项</li>
        </ul>
      </div>
      <button type="button" class="btn btn--ghost btn--sm" @click="locateFirstIssue">定位</button>
    </div>

    <div class="coach-toolbar">
      <div class="day-tabs" role="tablist" aria-label="训练日">
        <button
          v-for="item in state.coach.draft.days"
          :key="item.id"
          type="button"
          role="tab"
          class="day-tab"
          :class="{ 'day-tab--active': item.id === ui.coachDayId }"
          :aria-selected="item.id === ui.coachDayId"
          @click="selectDay(item.id)"
        >
          {{ item.title }}
          <span class="day-tab-count num">{{ dayTotals(item).sets }} 组</span>
        </button>
      </div>
      <div class="toolbar-right">
        <span v-if="changes.length" class="change-count num">{{ changes.length }} 处改动</span>
        <div class="mode-switch" role="group" aria-label="查看方式">
          <button
            type="button"
            class="mode-btn"
            :class="{ 'mode-btn--active': ui.coachMode === 'edit' }"
            :aria-pressed="ui.coachMode === 'edit'"
            @click="toggleMode('edit')"
          >
            编辑
          </button>
          <button
            type="button"
            class="mode-btn"
            :class="{ 'mode-btn--active': ui.coachMode === 'review' }"
            :aria-pressed="ui.coachMode === 'review'"
            @click="toggleMode('review')"
          >
            审阅
          </button>
        </div>
      </div>
    </div>

    <div class="coach-grid">
      <div class="plan-panel">
        <section v-if="day" class="plan-surface" aria-labelledby="day-title">
          <header class="day-head">
            <p class="day-overline">
              训练日 {{ state.coach.draft.days.findIndex((item) => item.id === day?.id) + 1 }} /
              {{ state.coach.draft.days.length }}
            </p>
            <template v-if="!isReview">
              <label class="sr-only" :for="`day-title-${day.id}`">训练日名称</label>
              <input
                :id="`day-title-${day.id}`"
                class="day-title-input"
                :value="day.title"
                maxlength="24"
                :data-field-id="`day:${day.id}`"
                @input="onDayTitleInput"
              />
              <p v-if="dayTitleIssue" class="field-error" role="alert">
                {{ dayTitleIssue.message }}
              </p>
            </template>
            <h2 v-else id="day-title" class="day-title">{{ day.title }}</h2>
            <p class="day-meta num">{{ totals.exercises }} 个动作 · {{ totals.sets }} 组</p>
          </header>

          <div v-if="isReview && dayChanges.length" class="change-summary">
            <div class="change-summary-head">
              <h3 class="change-title">本次改动 · {{ dayChanges.length }} 处</h3>
              <p class="change-sub">对照现用版本 {{ planSummary.publishedVersion }}</p>
            </div>
            <ul class="change-list">
              <li
                v-for="(change, index) in dayChanges"
                :key="`${change.dayId}-${change.exerciseId}-${change.kind === 'field' ? change.field : change.kind}-${index}`"
                class="change-item"
              >
                <template v-if="change.kind === 'field'">
                  <span class="change-where">
                    {{ change.field === 'dayTitle' ? change.dayTitle : change.exerciseName || '未命名动作' }}
                    · {{ change.label }}
                  </span>
                  <span class="change-values num">
                    <s>{{ displayChange(change).before }}</s>
                    <span class="change-arrow" aria-hidden="true">→</span>
                    <strong>{{ displayChange(change).after }}</strong>
                  </span>
                </template>
                <template v-else>
                  <span class="change-where">{{ change.exerciseName }}</span>
                  <span class="change-values">{{ change.kind === 'added' ? '本次新增' : '本次移除' }}</span>
                </template>
              </li>
            </ul>
          </div>

          <ol class="plan-list">
            <li
              v-for="(exercise, index) in day.exercises"
              :key="exercise.id"
              class="plan-row"
              :class="{
                'plan-row--changed': isReview && rowHasChange(exercise),
                'plan-row--flash': isReview && ui.flashDiff && rowHasChange(exercise),
              }"
            >
              <template v-if="!isReview">
                <div class="row-head">
                  <span class="row-index num">{{ String(index + 1).padStart(2, '0') }}</span>
                  <label class="sr-only" :for="`exercise-name-${exercise.id}`">动作名称</label>
                  <input
                    :id="`exercise-name-${exercise.id}`"
                    class="name-input"
                    :value="exercise.name"
                    maxlength="24"
                    :data-field-id="fieldId(exercise.id, 'name')"
                    @input="onFieldInput(exercise.id, 'name', $event)"
                  />
                  <button
                    type="button"
                    class="icon-btn remove-btn"
                    :aria-label="`移除动作 ${exercise.name || '未命名动作'}`"
                    @click="removeWithUndo(exercise)"
                  >
                    <Trash2 :size="17" aria-hidden="true" />
                  </button>
                </div>
                <p v-if="issueFor(exercise.id, 'name')" class="field-error" role="alert">
                  {{ issueFor(exercise.id, 'name')?.message }}
                </p>

                <div class="field-grid">
                  <label class="field">
                    <span class="field-label">组数</span>
                    <span class="input-wrap">
                      <input
                        :value="exercise.sets"
                        inputmode="numeric"
                        maxlength="2"
                        aria-label="组数"
                        :data-field-id="fieldId(exercise.id, 'sets')"
                        @input="onFieldInput(exercise.id, 'sets', $event)"
                      />
                      <span class="unit">组</span>
                    </span>
                    <span v-if="issueFor(exercise.id, 'sets')" class="field-error" role="alert">
                      {{ issueFor(exercise.id, 'sets')?.message }}
                    </span>
                  </label>
                  <label class="field">
                    <span class="field-label">目标次数 / 时长</span>
                    <span class="input-wrap">
                      <input
                        :value="exercise.target"
                        maxlength="20"
                        aria-label="目标次数或时长"
                        :data-field-id="fieldId(exercise.id, 'target')"
                        @input="onFieldInput(exercise.id, 'target', $event)"
                      />
                    </span>
                    <span v-if="issueFor(exercise.id, 'target')" class="field-error" role="alert">
                      {{ issueFor(exercise.id, 'target')?.message }}
                    </span>
                  </label>
                  <label class="field">
                    <span class="field-label">组间休息</span>
                    <span class="input-wrap">
                      <input
                        :value="exercise.restSeconds"
                        inputmode="numeric"
                        maxlength="3"
                        aria-label="组间休息秒数"
                        :data-field-id="fieldId(exercise.id, 'restSeconds')"
                        @input="onFieldInput(exercise.id, 'restSeconds', $event)"
                      />
                      <span class="unit">秒</span>
                    </span>
                    <span v-if="issueFor(exercise.id, 'restSeconds')" class="field-error" role="alert">
                      {{ issueFor(exercise.id, 'restSeconds')?.message }}
                    </span>
                  </label>
                </div>

                <label class="field-note">
                  <span class="field-label">备注</span>
                  <textarea
                    :value="exercise.note"
                    rows="2"
                    maxlength="80"
                    aria-label="动作备注"
                    :data-field-id="fieldId(exercise.id, 'note')"
                    @input="onFieldInput(exercise.id, 'note', $event)"
                  />
                </label>
                <p v-if="issueFor(exercise.id, 'note')" class="field-error" role="alert">
                  {{ issueFor(exercise.id, 'note')?.message }}
                </p>
              </template>

              <template v-else>
                <div class="review-row">
                  <span class="row-index num">{{ String(index + 1).padStart(2, '0') }}</span>
                  <div class="review-body">
                    <div class="review-head">
                      <h3 class="review-name">{{ exercise.name || '未命名动作' }}</h3>
                      <span v-if="rowHasChange(exercise)" class="changed-tag">已调整</span>
                    </div>
                    <p class="review-meta num">
                      {{ exercise.sets || '—' }} 组 × {{ exercise.target || '未填目标' }} ·
                      休息 {{ exercise.restSeconds || '—' }} 秒
                    </p>
                    <p v-if="exercise.note" class="review-note">{{ exercise.note }}</p>
                  </div>
                </div>
              </template>
            </li>
          </ol>

          <div v-if="!isReview" class="plan-footer">
            <button type="button" class="add-exercise" data-add-exercise @click="day && addExercise(day.id)">
              <Plus :size="16" aria-hidden="true" />
              添加动作
            </button>
          </div>

          <div v-if="ui.removeUndo" class="undo-bar" role="status">
            <span>已移除「{{ ui.removeUndo.name }}」</span>
            <button type="button" @click="undoRemoveExercise()">撤销</button>
          </div>
        </section>
      </div>

      <aside class="evidence" aria-labelledby="evidence-title">
        <section class="evidence-section">
          <h2 id="evidence-title" class="evidence-heading">判断依据</h2>
          <ul v-if="evidence.length" class="evidence-list">
            <li v-for="row in evidence" :key="row.title" class="evidence-item">
              <p class="evidence-date">{{ row.date }}</p>
              <p class="evidence-title">{{ row.title }}</p>
              <p class="evidence-detail num">{{ row.detail }}</p>
            </li>
          </ul>
          <p v-else class="evidence-detail evidence-detail--empty">
            {{ selectedStudentHealthAuthorized ? '还没有可查看的训练证据。' : '健康内容未授权，暂不显示训练证据。' }}
          </p>
        </section>
        <section class="evidence-section">
          <h2 class="evidence-heading">版本与范围</h2>
          <dl class="version-list">
            <div class="version-row">
              <dt>现用</dt>
              <dd class="num">版本 {{ planSummary.activeVersion }} · {{ publishedFirst?.name ?? '—' }} {{ publishedFirst?.sets ?? '—' }} 组</dd>
            </div>
            <div class="version-row">
              <dt>草稿</dt>
              <dd class="num">版本 {{ planSummary.version }}</dd>
            </div>
            <div class="version-row">
              <dt>已交付</dt>
              <dd class="num">
                {{ planSummary.deliveredVersion ? `版本 ${planSummary.deliveredVersion} · ${deliveryFirst?.name ?? '—'} ${deliveryFirst?.sets ?? '—'} 组` : '尚未发布' }}
              </dd>
            </div>
            <div class="version-row">
              <dt>全案</dt>
              <dd class="num">{{ planSummary.totals.days }} 天 · {{ planSummary.totals.exercises }} 个动作 · {{ planSummary.totals.sets }} 组</dd>
            </div>
          </dl>
          <p v-if="lastDelivery" class="evidence-note">
            已交付版本 {{ lastDelivery.version }} 的快照共 {{ planTotals(lastDelivery.content).sets }} 组，后续编辑不会改动它。
          </p>
          <p class="evidence-note">
            发布后，{{ studentName }}的现用版本 {{ planSummary.activeVersion }} 保持不变；采用新版本后，安排才会更新。
          </p>
        </section>
        <p v-if="state.coach.savedAt" class="saved-at num">上次保存 {{ dateOfSave }}</p>
      </aside>
    </div>

    <div class="mobile-actions">
      <button type="button" class="btn btn--ghost" @click="handleSave">保存</button>
      <button
        type="button"
        class="btn btn--accent"
        data-publish-trigger
        :disabled="coachPublishDisabled"
        @click="handlePublish"
      >
        {{ coachPublishDisabled ? `已发布版本 ${planSummary.deliveredVersion}` : `发布给${studentName}` }}
      </button>
    </div>
  </div>

  <BaseSheet
    :open="publishSheetOpen"
    :title="`发布给 ${studentName}`"
    :return-focus="publishReturnFocus"
    @close="ui.sheet = null"
  >
    <div class="publish-summary">
      <dl class="publish-facts">
        <div class="publish-row">
          <dt>对象</dt>
          <dd>{{ planSummary.title }} · {{ planSummary.cycle }}</dd>
        </div>
        <div class="publish-row">
          <dt>交付版本</dt>
          <dd class="num"><span class="keep-phrase">草稿版本 {{ planSummary.version }}</span></dd>
        </div>
        <div class="publish-row">
          <dt>现用版本</dt>
          <dd class="num"><span class="keep-phrase">版本 {{ planSummary.activeVersion }}</span>（保持不变）</dd>
        </div>
        <div class="publish-row">
          <dt>本次改动</dt>
          <dd class="num">{{ changes.length }} 处 · {{ planSummary.totals.days }} 个训练日</dd>
        </div>
      </dl>
      <div class="publish-impact">
        <p>{{ studentName }}会收到完整方案和改动说明；在采用之前，现用安排和已有训练记录都不会改变。</p>
      </div>
      <div v-if="!storageState.ok || ui.saveNoteTone === 'error'" class="publish-error" role="alert">
        <p>{{ storageState.message || ui.saveNote }}</p>
        <button type="button" class="btn btn--ghost btn--sm" @click="handleRetrySave()">重试保存</button>
      </div>
    </div>
    <template #footer>
      <div class="publish-actions">
        <button type="button" class="btn btn--ghost" @click="ui.sheet = null">取消</button>
        <button type="button" class="btn btn--accent" @click="confirmPublish()">
          发布<span class="keep-phrase">版本 {{ planSummary.version }}</span>
        </button>
      </div>
    </template>
  </BaseSheet>
  </div>
</template>

<style scoped>
.coach {
  min-width: 0;
  max-width: 100%;
  padding-top: var(--sp-3);
  padding-bottom: calc(96px + env(safe-area-inset-bottom));
}

@media (min-width: 900px) {
  .coach {
    padding-bottom: var(--sp-10);
  }
}

.coach-head {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: var(--sp-5);
}

.coach-title {
  margin-top: 2px;
  font-size: clamp(1.625rem, 6.4vw, 2rem);
  line-height: 1.22;
  font-weight: 600;
  letter-spacing: -0.02em;
}

.coach-context {
  display: flex;
  flex-wrap: wrap;
  gap: 4px 8px;
  margin-top: 6px;
  color: var(--muted);
  font-size: var(--text-13);
  line-height: 1.5;
}

.dot-sep {
  color: var(--line-strong);
}

.coach-actions {
  display: none;
  flex: none;
  gap: var(--sp-2);
}

.status-row {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 6px;
  margin-top: var(--sp-4);
  padding: 8px 0;
  border-top: 1px solid var(--line);
  border-bottom: 1px solid var(--line);
  color: var(--muted);
  font-size: var(--text-13);
  line-height: 1.5;
}

.status-row--positive {
  color: var(--positive);
}

.status-row--warning {
  color: var(--warning);
}

.status-row--error {
  color: var(--error);
}

.status-row:focus {
  outline: 2px solid var(--ink);
  outline-offset: 2px;
}

.status-retry {
  min-height: 44px;
  margin-left: auto;
  color: var(--error);
  font-size: var(--text-13);
  font-weight: 500;
  text-decoration: underline;
  text-underline-offset: 0.18em;
}

.status-pending {
  margin-left: auto;
  color: var(--warning);
  font-size: var(--text-12);
  white-space: nowrap;
}

.issue-banner {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: var(--sp-4);
  margin-top: var(--sp-3);
  padding: var(--sp-3) var(--sp-4);
  border: 1px solid #e2cfc1;
  border-radius: var(--radius-md);
  background: #faf3ed;
}

.issue-title {
  color: var(--ink);
  font-size: var(--text-14);
  line-height: 1.5;
  font-weight: 500;
}

.issue-list {
  margin-top: 4px;
  color: var(--muted);
  font-size: var(--text-13);
  line-height: 1.55;
}

.issue-banner .btn {
  flex: none;
}

.coach-toolbar {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: space-between;
  gap: var(--sp-3);
  margin-top: var(--sp-4);
}

.day-tabs {
  display: flex;
  gap: var(--sp-4);
  min-width: 0;
  overflow-x: auto;
  scrollbar-width: none;
}

.day-tabs::-webkit-scrollbar {
  display: none;
}

.day-tab {
  display: inline-flex;
  align-items: baseline;
  gap: 6px;
  flex: none;
  min-height: 44px;
  color: var(--muted);
  font-size: var(--text-14);
  font-weight: 500;
  border-bottom: 2px solid transparent;
  transition:
    color 140ms var(--ease-out),
    border-color 140ms var(--ease-out);
}

.day-tab--active {
  color: var(--ink);
  border-bottom-color: var(--ink);
  font-weight: 600;
}

.day-tab-count {
  color: var(--muted);
  font-size: var(--text-12);
}

.toolbar-right {
  display: flex;
  align-items: center;
  gap: var(--sp-3);
}

.change-count {
  color: var(--muted);
  font-size: var(--text-12);
  white-space: nowrap;
}

.mode-switch {
  display: inline-flex;
  gap: 1px;
  padding: 2px;
  border: 1px solid var(--line);
  border-radius: var(--radius-md);
  background: var(--canvas);
}

.mode-btn {
  min-height: 44px;
  padding: 0 12px;
  border-radius: 6px;
  color: var(--muted);
  font-size: var(--text-13);
  line-height: 1.3;
}

.mode-btn--active {
  background: var(--surface);
  color: var(--ink);
  font-weight: 600;
  box-shadow: 0 1px 2px rgba(var(--ink-rgb), 0.08);
}

.coach-grid {
  display: grid;
  min-width: 0;
  gap: var(--sp-6);
  margin-top: var(--sp-3);
  align-items: start;
}

.coach-grid > *,
.plan-panel,
.plan-surface,
.plan-row,
.row-head,
.field-grid,
.field,
.field-note,
.input-wrap,
.review-row,
.review-body,
.day-tabs {
  min-width: 0;
}

.plan-panel,
.plan-surface {
  width: 100%;
  max-width: 100%;
}

.plan-surface {
  border: 1px solid var(--line);
  border-radius: var(--radius-lg);
  background: var(--surface);
  overflow: hidden;
}

.day-head {
  padding: var(--sp-5) var(--sp-5) var(--sp-4);
  border-bottom: 1px solid var(--line);
}

.day-overline {
  color: var(--muted);
  font-size: var(--text-12);
  line-height: 1.4;
  letter-spacing: 0.02em;
}

.day-title {
  margin-top: 3px;
  font-size: var(--text-24);
  line-height: 1.25;
  font-weight: 600;
}

.day-title-input {
  display: block;
  width: 100%;
  min-height: 44px;
  margin-top: 2px;
  padding: 6px 0 5px;
  border: 0;
  border-bottom: 1px solid transparent;
  border-radius: 0;
  background: transparent;
  color: var(--ink);
  font-size: var(--text-24);
  line-height: 1.3;
  font-weight: 600;
}

.day-title-input:focus {
  outline: none;
  border-bottom-color: var(--ink);
}

.day-meta {
  margin-top: 4px;
  color: var(--muted);
  font-size: var(--text-13);
}

.change-summary {
  padding: var(--sp-4) var(--sp-5);
  border-bottom: 1px solid var(--line);
  background: #faf8f3;
}

.change-summary-head {
  display: flex;
  align-items: baseline;
  justify-content: space-between;
  gap: var(--sp-3);
}

.change-title {
  font-size: var(--text-15);
  font-weight: 600;
}

.change-sub {
  color: var(--muted);
  font-size: var(--text-13);
  white-space: nowrap;
}

.change-list {
  margin-top: var(--sp-2);
  display: grid;
  gap: 6px;
}

.change-item {
  display: flex;
  flex-wrap: wrap;
  align-items: baseline;
  justify-content: space-between;
  gap: 2px var(--sp-3);
  font-size: var(--text-14);
  line-height: 1.7;
}

.change-where {
  color: var(--ink);
}

.change-values {
  color: var(--muted);
  font-size: var(--text-14);
}

.change-values s {
  color: var(--muted);
}

.change-values strong {
  color: var(--ink);
  font-weight: 600;
}

.change-arrow {
  padding: 0 3px;
}

.plan-row {
  padding: var(--sp-4) var(--sp-5);
  border-bottom: 1px solid var(--line);
}

.plan-row:last-child {
  border-bottom: 0;
}

.plan-row--changed {
  position: relative;
}

.plan-row--changed::before {
  content: '';
  position: absolute;
  left: 0;
  top: 14px;
  bottom: 14px;
  width: 2px;
  background: var(--accent);
}

.plan-row--flash {
  animation: diff-flash 300ms var(--ease-out);
}

@keyframes diff-flash {
  0% {
    background: rgba(201, 120, 87, 0.14);
  }
  100% {
    background: transparent;
  }
}

.row-head {
  display: flex;
  align-items: center;
  gap: var(--sp-3);
}

.row-index {
  flex: none;
  width: 20px;
  color: var(--muted);
  font-size: var(--text-12);
  line-height: 1.5;
}

.name-input {
  flex: 1;
  min-width: 0;
  min-height: 44px;
  padding: 8px 0;
  border: 0;
  border-bottom: 1px solid transparent;
  border-radius: 0;
  background: transparent;
  color: var(--ink);
  font-size: var(--text-17);
  line-height: 1.4;
  font-weight: 600;
}

.name-input:focus {
  outline: none;
  border-bottom-color: var(--ink);
}

.remove-btn {
  flex: none;
  width: 44px;
  height: 44px;
  color: var(--muted);
}

.remove-btn:hover {
  color: var(--error);
  background: rgba(162, 63, 61, 0.06);
}

.field-grid {
  display: grid;
  grid-template-columns: minmax(0, 0.6fr) minmax(0, 1.1fr) minmax(0, 0.75fr);
  gap: var(--sp-3);
  margin-top: var(--sp-3);
  padding-left: 32px;
}

.field {
  min-width: 0;
}

.field-label {
  display: block;
  margin-bottom: 1px;
  color: var(--muted);
  font-size: var(--text-12);
  line-height: 1.4;
  white-space: nowrap;
}

.input-wrap {
  display: flex;
  align-items: baseline;
  gap: 3px;
  border-bottom: 1px solid var(--line-strong);
}

.input-wrap input {
  width: 100%;
  min-width: 0;
  padding: 4px 0;
  border: 0;
  background: transparent;
  color: var(--ink);
  font-size: var(--text-15);
  line-height: 1.4;
}

.input-wrap input:focus {
  outline: none;
}

.input-wrap:focus-within {
  border-bottom-color: var(--ink);
}

.unit {
  flex: none;
  color: var(--muted);
  font-size: var(--text-12);
}

.field-note {
  display: block;
  margin-top: var(--sp-3);
  padding-left: 32px;
}

.field-note textarea {
  display: block;
  width: 100%;
  margin-top: 2px;
  padding: 4px 0;
  border: 0;
  border-bottom: 1px solid var(--line-strong);
  border-radius: 0;
  background: transparent;
  color: var(--ink);
  font-size: var(--text-14);
  line-height: 1.55;
  resize: none;
}

.field-note textarea:focus {
  outline: none;
  border-bottom-color: var(--ink);
}

.field-error {
  display: block;
  margin-top: 3px;
  color: var(--error);
  font-size: var(--text-13);
  line-height: 1.45;
}

.review-row {
  display: flex;
  gap: var(--sp-3);
}

.review-body {
  min-width: 0;
  flex: 1;
}

.review-head {
  display: flex;
  align-items: baseline;
  justify-content: space-between;
  gap: var(--sp-3);
}

.review-name {
  font-size: var(--text-17);
  line-height: 1.4;
  font-weight: 600;
}

.changed-tag {
  flex: none;
  padding: 1px 6px;
  border: 1px solid #e2cfc1;
  border-radius: 4px;
  color: #8c5a3f;
  background: #faf3ed;
  font-size: var(--text-12);
  line-height: 1.5;
}

.review-meta {
  margin-top: 2px;
  color: var(--muted);
  font-size: var(--text-14);
  line-height: 1.55;
}

.review-note {
  margin-top: 6px;
  color: var(--ink);
  font-size: var(--text-16);
  line-height: 1.7;
}

.plan-footer {
  padding: var(--sp-3) var(--sp-5) var(--sp-4);
  border-top: 1px solid var(--line);
}

.add-exercise {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  min-height: 44px;
  color: var(--ink);
  font-size: var(--text-14);
  font-weight: 500;
}

.add-exercise:hover {
  text-decoration: underline;
  text-underline-offset: 0.18em;
}

.undo-bar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--sp-3);
  padding: 10px var(--sp-5);
  border-top: 1px solid var(--line);
  background: var(--canvas);
  color: var(--muted);
  font-size: var(--text-13);
}

.undo-bar button {
  min-height: 44px;
  color: var(--ink);
  font-size: var(--text-13);
  font-weight: 600;
  text-decoration: underline;
  text-underline-offset: 0.18em;
}

.evidence {
  display: grid;
  gap: var(--sp-5);
}

.evidence-section {
  padding-top: var(--sp-4);
  border-top: 1px solid var(--line);
}

.evidence-heading {
  font-size: var(--text-14);
  font-weight: 600;
  line-height: 1.4;
}

.evidence-list {
  margin-top: var(--sp-2);
}

.evidence-item {
  padding: 9px 0;
  border-bottom: 1px solid var(--line);
}

.evidence-date {
  color: var(--muted);
  font-size: var(--text-12);
  line-height: 1.4;
}

.evidence-title {
  color: var(--ink);
  font-size: var(--text-14);
  line-height: 1.5;
  font-weight: 500;
}

.evidence-detail {
  color: var(--muted);
  font-size: var(--text-12);
  line-height: 1.55;
}

.version-list {
  margin-top: var(--sp-2);
}

.version-row {
  display: flex;
  justify-content: space-between;
  gap: var(--sp-3);
  padding: 6px 0;
  border-bottom: 1px solid var(--line);
  font-size: var(--text-13);
  line-height: 1.5;
}

.version-row dt {
  color: var(--muted);
}

.version-row dd {
  margin: 0;
  color: var(--ink);
  text-align: right;
}

.evidence-note {
  margin-top: var(--sp-3);
  color: var(--muted);
  font-size: var(--text-12);
  line-height: 1.6;
}

.saved-at {
  color: var(--muted);
  font-size: var(--text-12);
}

@media (max-width: 380px) {
  .field-grid {
    grid-template-columns: minmax(0, 1fr);
  }
}

.mobile-actions {
  position: fixed;
  z-index: 25;
  left: 0;
  right: 0;
  bottom: 0;
  display: flex;
  gap: var(--sp-2);
  padding: 10px var(--gutter) calc(10px + env(safe-area-inset-bottom));
  border-top: 1px solid var(--line);
  background: rgba(243, 242, 238, 0.98);
}

.mobile-actions .btn {
  flex: 1;
}

.publish-summary {
  padding-bottom: var(--sp-2);
}

.publish-facts {
  border-top: 1px solid var(--line);
}

.publish-row {
  display: flex;
  align-items: baseline;
  justify-content: space-between;
  gap: var(--sp-4);
  padding: 11px 0;
  border-bottom: 1px solid var(--line);
  font-size: var(--text-14);
  line-height: 1.5;
}

.publish-row dt {
  color: var(--muted);
  flex: none;
}

.publish-row dd {
  margin: 0;
  color: var(--ink);
  text-align: right;
}

.publish-impact {
  margin-top: var(--sp-4);
  padding: var(--sp-3) var(--sp-4);
  border-left: 2px solid var(--line-strong);
  background: var(--canvas);
  color: var(--ink);
  font-size: var(--text-13);
  line-height: 1.65;
}

.publish-error {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--sp-3);
  margin-top: var(--sp-3);
  padding: 10px 12px;
  border-left: 2px solid var(--error);
  color: var(--error);
  font-size: var(--text-13);
  line-height: 1.55;
}

.keep-phrase {
  white-space: nowrap;
}

.publish-actions {
  display: flex;
  gap: var(--sp-2);
}

.publish-actions .btn {
  flex: 1;
}

@media (min-width: 620px) {
  .coach-actions {
    display: flex;
  }

  .mobile-actions {
    display: none;
  }

  .field-grid {
    padding-left: 32px;
  }

  .field-note {
    padding-left: 32px;
  }
}

@media (min-width: 900px) {
  .coach {
    padding-top: var(--sp-4);
  }

  .coach-grid {
    grid-template-columns: minmax(0, 1.75fr) minmax(280px, 0.75fr);
    gap: var(--sp-8);
  }

  .evidence {
    position: sticky;
    top: 24px;
    padding-top: 2px;
  }

  .day-head {
    padding: var(--sp-5) var(--sp-6);
  }

  .plan-row {
    padding: var(--sp-4) var(--sp-6);
  }

  .change-summary {
    padding: var(--sp-4) var(--sp-6);
  }

  .plan-footer {
    padding: var(--sp-3) var(--sp-6) var(--sp-4);
  }
}

.state-banner {
  display: flex;
  align-items: flex-start;
  gap: var(--sp-3);
  margin-top: var(--sp-3);
  padding: var(--sp-4);
  border: 1px solid var(--line);
  border-radius: var(--radius-md);
  background: color-mix(in srgb, var(--surface) 60%, transparent);
}

.state-banner--error {
  border-color: rgba(162, 63, 61, 0.4);
  background: rgba(162, 63, 61, 0.06);
}

.state-banner--warning {
  border-color: rgba(128, 92, 34, 0.4);
  background: rgba(128, 92, 34, 0.06);
}

.state-banner--positive {
  border-color: rgba(39, 111, 83, 0.35);
  background: rgba(39, 111, 83, 0.05);
}

.state-banner-title {
  font-size: var(--text-14);
  font-weight: 600;
}

.state-banner-text {
  margin-top: 3px;
  color: var(--muted);
  font-size: var(--text-13);
  line-height: 1.6;
}

.state-banner-action {
  margin-top: var(--sp-2);
}
</style>
