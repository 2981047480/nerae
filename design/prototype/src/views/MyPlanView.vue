<script setup lang="ts">
import { formatTime } from '../utils/format'
import { computed, nextTick } from 'vue'
import {
  AlertTriangle,
  ArrowRight,
  CheckCircle2,
  FileText,
  Globe2,
  Plus,
  Save,
  Sparkles,
  Trash2,
  Undo2,
} from '@lucide/vue'
import BaseSheet from '../components/BaseSheet.vue'
import {
  addOwnExercise,
  closeSheet,
  confirmShareReview,
  goToAiAssist,
  goToExplore,
  openShareReview,
  ownPlanChanges,
  ownPlanContent,
  ownPlanDirty,
  ownPlanHash,
  ownPlanIssues,
  ownPlanPublicPreview,
  ownPlanStatusLabel,
  removeOwnExercise,
  resolveOwnConflict,
  saveOwnPlan,
  setOwnDay,
  state,
  ui,
  undoOwnRemove,
  updateOwnDayTitle,
  updateOwnField,
  updateOwnHealthNote,
  updateOwnTitle,
} from '../state/store'
import type { PlanValidationIssue } from '../types'

const content = ownPlanContent
const issues = ownPlanIssues
const currentDay = computed(
  () => content.value.days.find((day) => day.id === ui.ownDayId) ?? content.value.days[0] ?? null,
)
const statusLabel = ownPlanStatusLabel
const isEmpty = computed(() => content.value.days.length === 0)
const conflict = computed(() => state.preview.myPlan === 'conflict' && Boolean(state.ownPlan.remoteVersion))
const missing = computed(() => issues.value.length > 0)
const pending = computed(() => state.ownPlan.status === 'pending_review')
const takenDown = computed(() => state.ownPlan.status === 'taken_down')

function issueFor(dayId: string, exerciseId: string | null, field: string): PlanValidationIssue | null {
  return (
    issues.value.find(
      (issue) =>
        issue.dayId === dayId &&
        (exerciseId === null || issue.exerciseId === exerciseId) &&
        issue.field === field,
    ) ?? null
  )
}

function fieldId(exerciseId: string, field: string): string {
  return `own-${exerciseId}-${field}`
}

function focusIssue(issue: PlanValidationIssue): void {
  if (issue.dayId) setOwnDay(issue.dayId)
  nextTick(() => {
    const target =
      issue.exerciseId && issue.field !== 'instructions'
        ? document.getElementById(fieldId(issue.exerciseId, issue.field))
        : document.querySelector<HTMLElement>('.own-day-title')
    target?.scrollIntoView({ block: 'center', behavior: state.reduceMotion ? 'auto' : 'smooth' })
    target?.focus({ preventScroll: true })
  })
}

function review(): void {
  openShareReview()
}

function resolve(choice: 'local' | 'remote'): void {
  resolveOwnConflict(choice)
}
</script>

<template>
  <div class="screen screen--wide my-plan">
    <header class="screen-head">
      <div class="screen-heading">
        <p class="screen-kicker">我的内容 · 只属于自己的计划</p>
        <h1 class="screen-title">我的计划</h1>
        <p class="screen-sub num">
          版本 {{ state.ownPlan.version }}
          <template v-if="state.ownPlan.savedAt"> · 已保存 {{ formatTime(state.ownPlan.savedAt) }}</template>
        </p>
      </div>
      <div class="screen-tools">
        <span class="pill" :class="pending ? 'pill--warning' : takenDown ? 'pill--error' : 'pill--neutral'">
          {{ statusLabel }}
        </span>
      </div>
    </header>

    <div v-if="isEmpty" class="empty-state">
      <FileText :size="28" aria-hidden="true" class="empty-icon" />
      <h2 class="empty-title">还没有自己的计划</h2>
      <p class="empty-text">
        可以从发现里的训练内容复制一份，再按自己的时间调整；也可以直接新建训练日与动作。
      </p>
      <div class="empty-actions">
        <button type="button" class="btn" @click="goToExplore">去发现内容</button>
      </div>
    </div>

    <template v-else>
      <div v-if="conflict" class="banner banner--warning" role="alert">
        <div>
          <p class="banner-title">这个内容已在另一处更新</p>
          <p class="banner-body">
            本地版本 {{ state.ownPlan.version }}，另一处为版本 {{ state.ownPlan.remoteVersion }}。
            修改仍保留在这里，不会静默覆盖；先决定保留哪一份。
          </p>
          <div class="banner-actions">
            <button type="button" class="btn btn--sm" @click="resolve('local')">保留我的修改并保存</button>
            <button type="button" class="btn btn--ghost btn--sm" @click="resolve('remote')">载入最新版本</button>
          </div>
        </div>
      </div>

      <div v-if="state.preview.myPlan === 'saving'" class="banner" role="status">
        <div>
          <p class="banner-title">正在保存到本机</p>
          <p class="banner-body">保存完成后会更新版本与保存时间；没有改动时不会增加版本。</p>
        </div>
      </div>

      <div v-if="state.preview.myPlan === 'save-failed'" class="banner banner--error" role="alert">
        <div>
          <p class="banner-title">没有保存成功</p>
          <p class="banner-body">修改仍保留在页面中，可以重试；原来的已保存版本没有被覆盖。</p>
          <div class="banner-actions">
            <button type="button" class="btn btn--sm" @click="saveOwnPlan">重试保存</button>
          </div>
        </div>
      </div>

      <div v-if="takenDown" class="banner banner--error" role="alert">
        <div>
          <p class="banner-title">这个计划已下架</p>
          <p class="banner-body">{{ state.ownPlan.takenDownReason }} 修改并重新提交审阅后才会再次进入审核。</p>
        </div>
      </div>

      <div v-if="pending" class="banner banner--positive" role="status">
        <div>
          <p class="banner-title">已提交审阅，尚未公开</p>
          <p class="banner-body">
            审核通过前，公开内容里不会出现这份计划。继续修改会让之前的审阅确认失效。
          </p>
        </div>
      </div>

      <div v-if="missing" class="banner banner--warning" role="alert">
        <div>
          <p class="banner-title">还有 {{ issues.length }} 处必要内容没有填写</p>
          <ul class="issue-list">
            <li v-for="issue in issues" :key="`${issue.dayId}-${issue.exerciseId}-${issue.field}`">
              <button type="button" class="issue-link" @click="focusIssue(issue)">
                {{ issue.dayTitle }}<template v-if="issue.exerciseName"> · {{ issue.exerciseName }}</template>
                ：{{ issue.message }}
              </button>
            </li>
          </ul>
          <p class="banner-body">缺项只影响这个计划，不会阻止你查看或开始已经存在的训练。</p>
        </div>
      </div>

      <div class="plan-editor">
        <section class="editor">
          <div class="title-fields">
            <label class="field">
              <span class="field-label">计划名称</span>
              <input class="input" :value="state.ownPlan.title" @input="updateOwnTitle(($event.target as HTMLInputElement).value)" />
            </label>
            <label class="field">
              <span class="field-label">周期</span>
              <input class="input" :value="state.ownPlan.cycle" @input="state.ownPlan.cycle = ($event.target as HTMLInputElement).value" />
            </label>
          </div>

          <div class="day-tabs" role="tablist" aria-label="我的训练日">
            <button
              v-for="day in content.days"
              :key="day.id"
              type="button"
              role="tab"
              class="day-tab"
              :class="{ 'day-tab--active': day.id === currentDay?.id }"
              :aria-selected="day.id === currentDay?.id"
              @click="setOwnDay(day.id)"
            >
              {{ day.title || '未命名训练日' }}
            </button>
          </div>

          <section v-if="currentDay" class="day-editor" :aria-label="currentDay.title">
            <label class="field day-title-field">
              <span class="field-label">训练日名称</span>
              <input
                class="input own-day-title"
                :value="currentDay.title"
                :aria-invalid="Boolean(issueFor(currentDay.id, null, 'dayTitle'))"
                @input="updateOwnDayTitle(currentDay.id, ($event.target as HTMLInputElement).value)"
              />
              <span v-if="issueFor(currentDay.id, null, 'dayTitle')" class="field-error">
                {{ issueFor(currentDay.id, null, 'dayTitle')?.message }}
              </span>
            </label>

            <ul class="own-exercises">
              <li v-for="(exercise, index) in currentDay.exercises" :key="exercise.id" class="own-exercise">
                <div class="own-exercise-head">
                  <span class="exercise-index num">{{ String(index + 1).padStart(2, '0') }}</span>
                  <label class="field own-name-field">
                    <span class="field-label">动作名称</span>
                    <input
                      :id="fieldId(exercise.id, 'name')"
                      class="input"
                      :value="exercise.name"
                      :aria-invalid="Boolean(issueFor(currentDay.id, exercise.id, 'name'))"
                      @input="updateOwnField(currentDay.id, exercise.id, 'name', ($event.target as HTMLInputElement).value)"
                    />
                  </label>
                  <button type="button" class="remove-btn" aria-label="移除动作" @click="removeOwnExercise(currentDay.id, exercise.id)">
                    <Trash2 :size="16" aria-hidden="true" />
                  </button>
                </div>
                <div class="scheme-grid">
                  <label class="field">
                    <span class="field-label">组数</span>
                    <input
                      :id="fieldId(exercise.id, 'sets')"
                      class="input num"
                      inputmode="numeric"
                      :value="exercise.sets"
                      :aria-invalid="Boolean(issueFor(currentDay.id, exercise.id, 'sets'))"
                      @input="updateOwnField(currentDay.id, exercise.id, 'sets', ($event.target as HTMLInputElement).value)"
                    />
                  </label>
                  <label class="field">
                    <span class="field-label">目标次数 / 时长</span>
                    <input
                      :id="fieldId(exercise.id, 'target')"
                      class="input"
                      :value="exercise.target"
                      :aria-invalid="Boolean(issueFor(currentDay.id, exercise.id, 'target'))"
                      @input="updateOwnField(currentDay.id, exercise.id, 'target', ($event.target as HTMLInputElement).value)"
                    />
                  </label>
                  <label class="field">
                    <span class="field-label">组间休息（秒）</span>
                    <input
                      :id="fieldId(exercise.id, 'restSeconds')"
                      class="input num"
                      inputmode="numeric"
                      :value="exercise.restSeconds"
                      :aria-invalid="Boolean(issueFor(currentDay.id, exercise.id, 'restSeconds'))"
                      @input="updateOwnField(currentDay.id, exercise.id, 'restSeconds', ($event.target as HTMLInputElement).value)"
                    />
                  </label>
                  <label class="field field--wide">
                    <span class="field-label">备注（会随计划公开）</span>
                    <textarea
                      :id="fieldId(exercise.id, 'note')"
                      class="textarea"
                      rows="2"
                      :value="exercise.note"
                      :aria-invalid="Boolean(issueFor(currentDay.id, exercise.id, 'note'))"
                      @input="updateOwnField(currentDay.id, exercise.id, 'note', ($event.target as HTMLTextAreaElement).value)"
                    />
                  </label>
                </div>
                <p v-if="issueFor(currentDay.id, exercise.id, 'name')" class="field-error">
                  {{ issueFor(currentDay.id, exercise.id, 'name')?.message }}
                </p>
                <p v-if="issueFor(currentDay.id, exercise.id, 'sets')" class="field-error">
                  {{ issueFor(currentDay.id, exercise.id, 'sets')?.message }}
                </p>
                <p v-if="issueFor(currentDay.id, exercise.id, 'target')" class="field-error">
                  {{ issueFor(currentDay.id, exercise.id, 'target')?.message }}
                </p>
                <p v-if="issueFor(currentDay.id, exercise.id, 'restSeconds')" class="field-error">
                  {{ issueFor(currentDay.id, exercise.id, 'restSeconds')?.message }}
                </p>
              </li>
            </ul>

            <button type="button" class="btn btn--ghost btn--sm add-exercise" @click="addOwnExercise(currentDay.id)">
              <Plus :size="16" aria-hidden="true" />
              添加动作
            </button>

            <div v-if="ui.removeUndo && ui.removeUndo.dayId === currentDay.id" class="undo-row" role="status">
              <span>已移除 {{ ui.removeUndo.name }}</span>
              <button type="button" class="text-link" @click="undoOwnRemove">
                <Undo2 :size="14" aria-hidden="true" />
                撤销
              </button>
            </div>
          </section>

          <label class="field health-field">
            <span class="field-label">只给自己看的健康备注</span>
            <textarea
              class="textarea"
              :value="state.ownPlan.healthNote"
              placeholder="例如肩部不适时避免哪些动作"
              @input="updateOwnHealthNote(($event.target as HTMLTextAreaElement).value)"
            />
            <span class="field-hint">不会出现在分享预览或公开内容中。</span>
          </label>

          <div class="editor-actions">
            <button type="button" class="btn" :disabled="!ownPlanDirty" @click="saveOwnPlan">
              <Save :size="16" aria-hidden="true" />
              {{ ownPlanDirty ? '保存修改' : '已保存' }}
            </button>
            <button type="button" class="btn btn--ghost" @click="goToAiAssist()">
              <Sparkles :size="16" aria-hidden="true" />
              用已有模板调整
            </button>
          </div>
          <p v-if="ui.saveNote" class="save-note" :class="{ 'save-note--error': ui.saveNoteTone === 'error' }" role="status">
            {{ ui.saveNote }}
          </p>
        </section>

        <aside class="review-side">
          <section class="side-panel" aria-labelledby="review-title">
            <h2 id="review-title" class="side-title">审阅与分享</h2>
            <p class="side-copy">
              分享会把当前内容提交审阅，并展示公开后别人能看到的范围。提交不等于已经公开。
            </p>
            <dl class="kv review-facts">
              <dt>当前版本</dt>
              <dd class="num">版本 {{ state.ownPlan.version }}</dd>
              <dt>变化</dt>
              <dd class="num">{{ ownPlanChanges.length }} 处</dd>
              <dt>可见范围</dt>
              <dd>{{ state.ownPlan.visibility === 'public' ? '公开' : '仅自己' }}</dd>
            </dl>
            <button type="button" class="btn btn--accent btn--block" @click="review">
              审阅并提交
            </button>
            <p v-if="ownPlanDirty" class="side-warning">有未保存修改；审阅前会先保存。</p>
            <p v-if="state.ownPlan.confirmedReviewHash && state.ownPlan.confirmedReviewHash !== ownPlanHash" class="side-warning">
              内容已变化，之前的审阅确认已失效。
            </p>
          </section>

          <section class="side-panel side-panel--quiet" aria-labelledby="changes-title">
            <h2 id="changes-title" class="side-title">相对上次保存</h2>
            <ul v-if="ownPlanChanges.length" class="side-changes">
              <li v-for="(change, index) in ownPlanChanges" :key="index">
                <template v-if="change.kind === 'field'">
                  {{ change.exerciseName }} · {{ change.label }}：{{ change.before || '无' }} → {{ change.after || '无' }}
                </template>
                <template v-else>{{ change.exerciseName }} · {{ change.kind === 'added' ? '新增' : '移除' }}</template>
              </li>
            </ul>
            <p v-else class="side-copy">与上次保存一致。</p>
          </section>
        </aside>
      </div>
    </template>

    <BaseSheet :open="ui.sheet === 'share'" title="审阅公开内容" @close="closeSheet">
      <p class="sheet-copy">
        下面是公开后其他人能看到的内容。<strong>健康备注不会公开。</strong>
      </p>
      <div class="share-preview">
        <p class="share-title">{{ ownPlanPublicPreview.title }}</p>
        <p class="share-meta num">{{ ownPlanPublicPreview.cycle }} · {{ ownPlanPublicPreview.days.length }} 个训练日</p>
        <div v-for="day in ownPlanPublicPreview.days" :key="day.id" class="share-day">
          <p class="share-day-title">{{ day.title }}</p>
          <ul>
            <li v-for="exercise in day.exercises" :key="exercise.id" class="share-exercise">
              <span class="share-exercise-main">
                <span class="share-exercise-name">{{ exercise.name }}</span>
                <span class="share-exercise-scheme num">{{ exercise.sets }} 组 × {{ exercise.target }} · 组间休息 {{ exercise.restSeconds }} 秒</span>
                <span v-if="exercise.note" class="share-exercise-note">动作备注（公开）：{{ exercise.note }}</span>
              </span>
            </li>
          </ul>
        </div>
        <p v-if="ownPlanPublicPreview.healthNoteExcluded" class="share-excluded">
          <CheckCircle2 :size="14" aria-hidden="true" />
          健康备注已排除
        </p>
      </div>
      <p class="sheet-copy sheet-copy--muted">
        提交后进入待审核；当前版本仍只自己可见，不会立刻出现在公开内容中。
      </p>
      <div v-if="ui.saveNote && ui.saveNoteTone === 'error'" class="form-error" role="alert">{{ ui.saveNote }}</div>
      <template #footer>
        <div class="sheet-actions">
          <button type="button" class="btn btn--ghost" @click="closeSheet">取消</button>
          <button type="button" class="btn btn--accent" @click="confirmShareReview">确认内容并提交审阅</button>
        </div>
      </template>
    </BaseSheet>

    <p v-if="missing && state.preview.myPlan === 'missing'" class="sr-only">
      <AlertTriangle /> 必要内容未填完
    </p>
    <p v-if="state.preview.myPlan === 'pending'" class="sr-only"><Globe2 /> 待审核</p>
    <p class="sr-only"><ArrowRight /> 我的计划</p>
  </div>
</template>

<style scoped>
.my-plan {
  padding-bottom: calc(96px + env(safe-area-inset-bottom));
}

.banner-actions {
  display: flex;
  flex-wrap: wrap;
  gap: var(--sp-2);
  margin-top: var(--sp-3);
}

.issue-list {
  margin-top: var(--sp-2);
}

.issue-link {
  display: block;
  width: 100%;
  padding: 5px 0;
  color: var(--warning);
  font-size: var(--text-13);
  line-height: 1.5;
  text-align: left;
}

.plan-editor {
  display: grid;
  gap: var(--sp-6);
  margin-top: var(--sp-5);
}

.title-fields {
  display: grid;
  gap: var(--sp-3);
}

.day-tabs {
  display: flex;
  flex-wrap: wrap;
  gap: 4px;
  margin-top: var(--sp-4);
}

.day-tab {
  min-height: 44px;
  padding: 0 13px;
  border: 1px solid var(--line);
  border-radius: var(--radius-md);
  color: var(--muted);
  font-size: var(--text-14);
}

.day-tab--active {
  border-color: var(--ink);
  background: var(--ink);
  color: #fff;
  font-weight: 600;
}

.day-editor {
  margin-top: var(--sp-4);
}

.day-title-field {
  max-width: 420px;
}

.own-exercises {
  margin-top: var(--sp-4);
  border-top: 1px solid var(--line);
}

.own-exercise {
  padding: var(--sp-4) 0;
  border-bottom: 1px solid var(--line);
}

.own-exercise-head {
  display: flex;
  align-items: flex-end;
  gap: var(--sp-3);
}

.exercise-index {
  flex: none;
  padding-bottom: 11px;
  color: var(--muted);
  font-size: var(--text-12);
}

.own-name-field {
  flex: 1;
}

.remove-btn {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 44px;
  height: 44px;
  flex: none;
  border-radius: var(--radius-md);
  color: var(--muted);
}

.remove-btn:hover {
  color: var(--error);
  background: rgba(162, 63, 61, 0.06);
}

.scheme-grid {
  display: grid;
  grid-template-columns: 90px minmax(0, 1fr) 120px;
  gap: var(--sp-3);
  margin-top: var(--sp-2);
}

.scheme-grid .field--wide {
  grid-column: 1 / -1;
}

.add-exercise {
  margin-top: var(--sp-3);
}

.undo-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--sp-3);
  margin-top: var(--sp-3);
  padding: var(--sp-2) var(--sp-3);
  border-radius: var(--radius-md);
  background: rgba(25, 47, 57, 0.05);
  color: var(--muted);
  font-size: var(--text-13);
}

.health-field {
  margin-top: var(--sp-5);
}

.field-hint {
  color: var(--muted);
  font-size: var(--text-12);
  line-height: 1.5;
}

.editor-actions {
  display: flex;
  flex-wrap: wrap;
  gap: var(--sp-2);
  margin-top: var(--sp-4);
}

.save-note {
  margin-top: var(--sp-2);
  color: var(--positive);
  font-size: var(--text-13);
}

.save-note--error {
  color: var(--error);
}

.review-side {
  display: grid;
  gap: var(--sp-4);
  align-content: start;
}

.side-panel {
  padding: var(--sp-4);
  border: 1px solid var(--line);
  border-radius: var(--radius-lg);
  background: var(--surface);
}

.side-panel--quiet {
  background: rgba(255, 255, 255, 0.55);
}

.side-title {
  margin-bottom: var(--sp-2);
  font-size: var(--text-16);
  font-weight: 600;
}

.side-copy {
  color: var(--muted);
  font-size: var(--text-13);
  line-height: 1.6;
}

.review-facts {
  margin: var(--sp-3) 0;
}

.side-warning {
  margin-top: var(--sp-2);
  color: var(--warning);
  font-size: var(--text-12);
  line-height: 1.55;
}

.side-changes {
  margin-top: var(--sp-2);
}

.side-changes li {
  padding: 6px 0;
  border-bottom: 1px solid var(--line);
  color: var(--muted);
  font-size: var(--text-12);
  line-height: 1.55;
}

.sheet-copy {
  font-size: var(--text-14);
  line-height: 1.65;
}

.sheet-copy--muted {
  margin-top: var(--sp-2);
  color: var(--muted);
  font-size: var(--text-13);
}

.share-preview {
  margin-top: var(--sp-3);
  padding: var(--sp-4);
  border-radius: var(--radius-md);
  background: rgba(25, 47, 57, 0.045);
}

.share-title {
  font-size: var(--text-16);
  font-weight: 600;
}

.share-meta {
  color: var(--muted);
  font-size: var(--text-12);
}

.share-day {
  margin-top: var(--sp-3);
}

.share-day-title {
  font-size: var(--text-14);
  font-weight: 600;
}

.share-exercise {
  padding: 5px 0;
  border-top: 1px solid var(--line);
}

.share-exercise-main {
  display: flex;
  flex-direction: column;
  gap: 1px;
  min-width: 0;
}

.share-exercise-name {
  color: var(--ink);
  font-size: var(--text-13);
}

.share-exercise-scheme {
  color: var(--muted);
  font-size: var(--text-12);
}

.share-exercise-note {
  color: var(--muted);
  font-size: var(--text-12);
  line-height: 1.5;
  overflow-wrap: anywhere;
}

.share-excluded {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  margin-top: var(--sp-3);
  color: var(--positive);
  font-size: var(--text-12);
}

.sheet-actions {
  display: flex;
  justify-content: flex-end;
  gap: var(--sp-2);
}

.form-error {
  margin-top: var(--sp-2);
  color: var(--error);
  font-size: var(--text-13);
}

@media (min-width: 700px) {
  .title-fields {
    grid-template-columns: minmax(0, 1.4fr) minmax(0, 0.6fr);
  }
}

@media (min-width: 768px) {
  .plan-editor {
    grid-template-columns: minmax(0, 1.7fr) minmax(280px, 0.8fr);
    gap: var(--sp-7);
    align-items: start;
  }
}

@media (max-width: 380px) {
  .scheme-grid {
    grid-template-columns: minmax(0, 1fr);
  }
}
</style>
