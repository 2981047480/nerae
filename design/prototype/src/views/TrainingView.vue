<script setup lang="ts">
import { computed, ref } from 'vue'
import {
  ArrowLeft,
  Check,
  ChevronDown,
  Clock3,
  Minus,
  Pause,
  Play,
  Plus,
  SkipForward,
} from '@lucide/vue'
import PhotoFrame from '../components/PhotoFrame.vue'
import {
  adjustInput,
  completeCurrentSet,
  currentEntry,
  currentInput,
  doneSets,
  extendRest,
  isComplete,
  leaveTraining,
  pauseRest,
  progressPercent,
  restClock,
  restNextLabel,
  restRemainingMs,
  resumeRest,
  sessionGroups,
  setInputValue,
  skipRest,
  state,
  storageState,
  retryPersist,
  totalSets,
  ui,
} from '../state/store'
import {
  hasErrors,
  referenceText,
} from '../utils/training'
import { formatTime, setValueLabel } from '../utils/format'

const detailsOpen = ref(false)

const entry = currentEntry
const input = currentInput
const rest = computed(() => state.session.rest)
const isResting = computed(() => Boolean(rest.value))

const errorList = computed(() => Object.values(ui.trainingErrors).filter(Boolean) as string[])
const hasError = computed(() => hasErrors(ui.trainingErrors))

const lastCompleted = computed(() => {
  const all = state.session.exercises.flatMap((exercise) =>
    exercise.sets
      .filter((set) => set.status === 'done' && set.completedAt)
      .map((set) => ({ exercise, set })),
  )
  all.sort(
    (a, b) => Date.parse(a.set.completedAt ?? '') - Date.parse(b.set.completedAt ?? ''),
  )
  return all[all.length - 1] ?? null
})

const currentSets = computed(() => entry.value?.exercise.sets ?? [])

const progressLabel = computed(() => `${doneSets.value} / ${totalSets.value} 组`)

const restPercent = computed(() => {
  const currentRest = rest.value
  if (!currentRest) return 0
  const total = currentRest.durationMs || 1
  return Math.min(100, Math.max(0, ((total - restRemainingMs.value) / total) * 100))
})

const restPaused = computed(() => Boolean(rest.value?.paused))

const completionFacts = computed(() => {
  const completed = state.session.exercises.reduce(
    (sum, exercise) => sum + exercise.sets.filter((set) => set.status === 'done').length,
    0,
  )
  return { sets: completed, total: totalSets.value, exercises: state.session.exercises.length }
})

function onInput(field: 'weightKg' | 'reps' | 'seconds', event: Event): void {
  const value = (event.target as HTMLInputElement).value
  if (!entry.value) return
  setInputValue(entry.value.set.id, field, value)
}

function submitSet(): void {
  completeCurrentSet()
}

function formatSetSummary(): string {
  if (!lastCompleted.value) return ''
  return setValueLabel(lastCompleted.value.exercise.kind, lastCompleted.value.set)
}

function completionTime(): string {
  return state.session.completedAt ? formatTime(state.session.completedAt) : '—'
}

const exerciseValues = computed(() =>
  sessionGroups.value.map((group) => {
    const completedSets = group.sets.filter((set) => set.status === 'done')
    return {
      ...group,
      completedSets,
      target: `${group.exercise.sets.length} 组 × ${group.exercise.targetText}`,
    }
  }),
)

const focus = computed(() => {
  const value = entry.value
  if (!value) return null
  return {
    exercise: value.exercise,
    set: value.set,
    exerciseIndex: state.session.exercises.findIndex((item) => item.id === value.exercise.id) + 1,
    doneCount: state.inputs[value.set.id] ? value.exercise.sets.filter((item) => item.status === 'done').length : 0,
  }
})

const focusExerciseNumber = computed(() => {
  const value = focus.value
  if (!value) return 0
  return state.session.exercises.findIndex((item) => item.id === value.exercise.id) + 1
})

</script>

<template>
  <div class="training focus-scope on-dark">
    <header class="training-top">
      <button class="training-exit" type="button" @click="leaveTraining">
        <ArrowLeft :size="19" aria-hidden="true" />
        <span>退出</span>
      </button>
      <div class="training-title">
        <span class="training-plan">{{ state.session.planTitle }}</span>
        <span class="training-day" data-training-title>{{ state.session.dayTitle }}</span>
      </div>
      <span class="training-progress num">{{ progressLabel }}</span>
    </header>
    <div class="training-bar" aria-hidden="true">
      <span :style="{ width: `${isComplete ? 100 : progressPercent}%` }" />
    </div>

    <main id="main" class="training-main" tabindex="-1">
      <template v-if="isComplete">
        <section class="complete-block" aria-labelledby="complete-title">
          <p class="focus-kicker">{{ state.session.dateLabel }} · 最后一组 {{ completionTime() }}</p>
          <h1 id="complete-title" class="complete-title">训练完成</h1>
          <p class="complete-facts num">
            {{ completionFacts.sets }} 组记录完成 · {{ completionFacts.exercises }} 个动作
          </p>
          <p v-if="storageState.ok" class="complete-saved">
            <Check :size="15" aria-hidden="true" />
            记录已保存在本机
          </p>
          <div v-else class="storage-alert" role="alert">
            <p>{{ storageState.message }}</p>
            <button type="button" class="storage-retry" @click="retryPersist()">重试保存</button>
          </div>
          <button class="btn btn--accent complete-cta" type="button" @click="leaveTraining">
            回到今天
          </button>
        </section>

        <section class="records" aria-labelledby="complete-records-title">
          <header class="section-head">
            <h2 id="complete-records-title" class="section-title">本次记录</h2>
            <span class="section-meta num">{{ completionFacts.sets }} / {{ completionFacts.total }} 组</span>
          </header>
          <div v-for="group in exerciseValues" :key="group.exercise.id" class="record-group">
            <div class="record-head">
              <h3 class="record-name">{{ group.exercise.name }}</h3>
              <span class="record-target num">{{ group.target }}</span>
            </div>
            <ul class="record-sets">
              <li v-for="set in group.completedSets" :key="set.id" class="record-row">
                <span class="record-set num">第 {{ set.index }} 组</span>
                <span class="record-value num">{{ setValueLabel(group.exercise.kind, set) }}</span>
                <span class="record-time num">{{ set.completedAt ? formatTime(set.completedAt) : '' }}</span>
              </li>
              <li v-if="!group.completedSets.length" class="record-empty">没有记录</li>
            </ul>
          </div>
        </section>
      </template>

      <template v-else-if="isResting">
        <section class="rest-block" aria-labelledby="rest-title">
          <p class="focus-kicker">组间休息</p>
          <h1 id="rest-title" class="rest-title" aria-live="polite">
            <span class="num">{{ restClock }}</span>
            <span v-if="restPaused" class="rest-paused">已暂停</span>
          </h1>
          <div class="rest-track" aria-hidden="true">
            <span :style="{ width: `${restPercent}%` }" />
          </div>
          <p class="rest-next">
            下一组 · <strong>{{ restNextLabel }}</strong>
          </p>
          <p v-if="lastCompleted" class="rest-recorded num">
            <Check :size="14" aria-hidden="true" />
            {{ lastCompleted.exercise.name }} 第 {{ lastCompleted.set.index }} 组已记录 · {{ formatSetSummary() }}
          </p>
          <p class="rest-default">休息结束后自动进入下一组，不需要额外操作。</p>
          <div class="rest-actions">
            <button class="btn btn--ghost" type="button" @click="restPaused ? resumeRest() : pauseRest()">
              <Play v-if="restPaused" :size="16" aria-hidden="true" />
              <Pause v-else :size="16" aria-hidden="true" />
              {{ restPaused ? '继续计时' : '暂停' }}
            </button>
            <button class="btn btn--ghost" type="button" @click="extendRest(15)">
              <Clock3 :size="16" aria-hidden="true" />
              加 15 秒
            </button>
          </div>
          <button class="rest-skip" type="button" @click="skipRest">
            <SkipForward :size="15" aria-hidden="true" />
            提前结束休息
          </button>
        </section>

        <section class="records records--compact" aria-labelledby="rest-records-title">
          <header class="section-head">
            <h2 id="rest-records-title" class="section-title">本次记录</h2>
            <span class="section-meta num">{{ doneSets }} / {{ totalSets }} 组</span>
          </header>
          <div v-for="group in exerciseValues" :key="group.exercise.id" class="record-group">
            <div class="record-head">
              <h3 class="record-name">{{ group.exercise.name }}</h3>
              <span class="record-target num">{{ group.progress.done }} / {{ group.progress.total }} 组</span>
            </div>
            <ul class="record-sets">
              <li v-for="set in group.completedSets" :key="set.id" class="record-row">
                <span class="record-set num">第 {{ set.index }} 组</span>
                <span class="record-value num">{{ setValueLabel(group.exercise.kind, set) }}</span>
                <span class="record-time num">{{ set.completedAt ? formatTime(set.completedAt) : '' }}</span>
              </li>
            </ul>
          </div>
        </section>
      </template>

      <template v-else>
        <section v-if="focus" class="focus-block" aria-labelledby="exercise-title">
          <p class="focus-kicker num">
            动作 {{ focusExerciseNumber }} / {{ state.session.exercises.length }} · 第 {{ focus.set.index }} 组
          </p>
          <div class="focus-head">
            <div class="focus-heading">
              <h1 id="exercise-title" class="focus-title">{{ focus.exercise.name }}</h1>
              <p class="focus-reference num">{{ referenceText(focus.exercise) }}</p>
              <p class="focus-target num">
                目标 {{ focus.exercise.targetText }} · 休息 {{ focus.exercise.restSeconds }} 秒
              </p>
            </div>
            <figure class="focus-photo">
              <PhotoFrame src="/assets/training-dumbbells.webp" alt="训练用的哑铃" />
            </figure>
          </div>

          <div class="metrics" :class="{ 'metrics--single': focus.exercise.kind === 'duration' }">
            <div v-if="focus.exercise.kind !== 'duration'" class="metric">
              <div class="metric-head">
                <span class="metric-label">重量</span>
                <span class="metric-hint">kg</span>
              </div>
              <div class="metric-value-row">
                <input
                  class="metric-input num"
                  :value="input?.weightKg ?? ''"
                  inputmode="decimal"
                  autocomplete="off"
                  aria-label="重量，千克"
                  :aria-invalid="Boolean(ui.trainingErrors.weightKg)"
                  @input="onInput('weightKg', $event)"
                />
                <span class="metric-unit">kg</span>
              </div>
              <div class="metric-controls">
                <button type="button" aria-label="减少重量" @click="adjustInput('weightKg', -1.25)">
                  <Minus :size="18" aria-hidden="true" />
                </button>
                <button type="button" aria-label="增加重量" @click="adjustInput('weightKg', 1.25)">
                  <Plus :size="18" aria-hidden="true" />
                </button>
              </div>
            </div>

            <div v-if="focus.exercise.kind !== 'duration'" class="metric">
              <div class="metric-head">
                <span class="metric-label">次数<template v-if="focus.exercise.kind === 'perSide'">（每侧）</template></span>
                <span class="metric-hint">次</span>
              </div>
              <div class="metric-value-row">
                <input
                  class="metric-input num"
                  :value="input?.reps ?? ''"
                  inputmode="numeric"
                  autocomplete="off"
                  aria-label="完成次数"
                  :aria-invalid="Boolean(ui.trainingErrors.reps)"
                  @input="onInput('reps', $event)"
                />
                <span class="metric-unit">次</span>
              </div>
              <div class="metric-controls">
                <button type="button" aria-label="减少一次" @click="adjustInput('reps', -1)">
                  <Minus :size="18" aria-hidden="true" />
                </button>
                <button type="button" aria-label="增加一次" @click="adjustInput('reps', 1)">
                  <Plus :size="18" aria-hidden="true" />
                </button>
              </div>
            </div>

            <div v-if="focus.exercise.kind === 'duration'" class="metric metric--duration">
              <div class="metric-head">
                <span class="metric-label">完成时长</span>
                <span class="metric-hint">秒</span>
              </div>
              <div class="metric-value-row">
                <input
                  class="metric-input num"
                  :value="input?.seconds ?? ''"
                  inputmode="numeric"
                  autocomplete="off"
                  aria-label="完成秒数"
                  :aria-invalid="Boolean(ui.trainingErrors.seconds)"
                  @input="onInput('seconds', $event)"
                />
                <span class="metric-unit">秒</span>
              </div>
              <div class="metric-controls">
                <button type="button" aria-label="减少五秒" @click="adjustInput('seconds', -5)">
                  <Minus :size="18" aria-hidden="true" />
                </button>
                <button type="button" aria-label="增加五秒" @click="adjustInput('seconds', 5)">
                  <Plus :size="18" aria-hidden="true" />
                </button>
              </div>
            </div>
          </div>

          <div v-if="errorList.length" class="form-alert" role="alert">
            <p>{{ errorList[0] }}</p>
          </div>

          <button class="btn btn--accent complete-cta" type="button" :aria-disabled="hasError" @click="submitSet">
            <Check :size="18" aria-hidden="true" />
            完成第 {{ focus.set.index }} 组
          </button>
          <p class="save-hint">记录会保存在本机，退出后可从这里继续。</p>
          <div v-if="!storageState.ok" class="storage-alert" role="alert">
            <p>{{ storageState.message }}</p>
            <button type="button" class="storage-retry" @click="retryPersist()">重试保存</button>
          </div>

          <section class="set-list" aria-labelledby="set-list-title">
            <header class="section-head">
              <h2 id="set-list-title" class="section-title">本次记录</h2>
              <span class="section-meta num">{{ focus.exercise.sets.filter((set) => set.status === 'done').length }} / {{ focus.exercise.sets.length }} 组</span>
            </header>
            <ul>
              <li
                v-for="set in currentSets"
                :key="set.id"
                class="set-row"
                :class="{ 'set-row--done': set.status === 'done', 'set-row--just': ui.justCompletedSetId === set.id }"
              >
                <span class="set-index num">第 {{ set.index }} 组</span>
                <span class="set-value num">{{ set.status === 'done' ? setValueLabel(focus.exercise.kind, set) : '待记录' }}</span>
                <span class="set-state">
                  <Check v-if="set.status === 'done'" :size="14" aria-hidden="true" />
                  <span v-if="set.completedAt" class="set-time num">{{ formatTime(set.completedAt) }}</span>
                </span>
              </li>
            </ul>
          </section>

          <section class="details">
            <button
              type="button"
              class="details-toggle"
              :aria-expanded="detailsOpen"
              aria-controls="exercise-details"
              @click="detailsOpen = !detailsOpen"
            >
              <span>动作说明 · {{ focus.exercise.equipment }}</span>
              <ChevronDown :size="16" aria-hidden="true" :class="{ 'details-icon--open': detailsOpen }" />
            </button>
            <div v-show="detailsOpen" id="exercise-details" class="details-body">
              <p>{{ focus.exercise.cue }}</p>
            </div>
          </section>
        </section>
      </template>
    </main>
  </div>
</template>

<style scoped>
.training {
  min-height: 100dvh;
  background: var(--focus-canvas);
  color: var(--focus-text);
}

.training-top {
  position: sticky;
  top: 0;
  z-index: 20;
  display: grid;
  grid-template-columns: 84px minmax(0, 1fr) auto;
  align-items: center;
  gap: var(--sp-2);
  width: 100%;
  max-width: 900px;
  margin-inline: auto;
  padding: calc(8px + env(safe-area-inset-top)) var(--sp-4) 8px;
  background: var(--focus-canvas);
}

@media (max-width: 1080px) {
  .training-top {
    padding-right: 92px;
  }
}

@media (max-width: 560px) {
  .training-progress {
    display: none;
  }
}

.training-exit {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  min-height: 44px;
  color: var(--focus-text);
  font-size: var(--text-14);
  font-weight: 500;
}

.training-title {
  min-width: 0;
  text-align: center;
  line-height: 1.25;
}

.training-plan {
  display: block;
  color: var(--focus-muted);
  font-size: var(--text-12);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.training-day {
  display: block;
  margin-top: 1px;
  color: var(--focus-text);
  font-size: var(--text-14);
  font-weight: 600;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.training-progress {
  color: var(--focus-muted);
  font-size: var(--text-12);
  white-space: nowrap;
}

.training-bar {
  height: 2px;
  background: var(--focus-surface);
}

.training-bar span {
  display: block;
  height: 100%;
  background: var(--accent);
  transition: width 300ms var(--ease-out);
}

.training-main {
  width: 100%;
  max-width: 720px;
  margin-inline: auto;
  padding: var(--sp-5) var(--gutter) calc(var(--sp-9) + env(safe-area-inset-bottom));
}

.focus-kicker {
  color: var(--focus-muted);
  font-size: var(--text-13);
  line-height: 1.5;
  letter-spacing: 0.01em;
}

.focus-head {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: var(--sp-4);
  margin-top: 6px;
}

.focus-heading {
  min-width: 0;
}

.focus-title {
  color: var(--focus-text);
  font-size: clamp(1.875rem, 8vw, 2.5rem);
  font-weight: 600;
  line-height: 1.18;
  letter-spacing: -0.02em;
}

.focus-reference {
  margin-top: 8px;
  color: var(--focus-muted);
  font-size: var(--text-14);
  line-height: 1.5;
}

.focus-target {
  margin-top: 2px;
  color: var(--focus-muted);
  font-size: var(--text-13);
  line-height: 1.5;
}

.focus-photo {
  flex: none;
  width: 64px;
  height: 64px;
  margin: 2px 0 0;
  border-radius: var(--radius-md);
  overflow: hidden;
  background: var(--focus-surface);
}

@media (min-width: 480px) {
  .focus-photo {
    width: 88px;
    height: 88px;
  }
}

.metrics {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: var(--sp-3);
  margin-top: var(--sp-6);
}

.metrics--single {
  grid-template-columns: minmax(0, 1fr);
}

.metric {
  padding: 14px 14px 0;
  border: 1px solid var(--focus-line);
  border-radius: var(--radius-lg);
  background: var(--focus-surface);
  overflow: hidden;
}

.metric--duration {
  max-width: 100%;
}

.metric-head {
  display: flex;
  align-items: baseline;
  justify-content: space-between;
  gap: var(--sp-2);
}

.metric-label {
  color: var(--focus-muted);
  font-size: var(--text-13);
  line-height: 1.4;
}

.metric-hint {
  color: var(--focus-muted);
  font-size: var(--text-12);
}

.metric-value-row {
  display: flex;
  align-items: baseline;
  gap: 6px;
  margin-top: 2px;
}

.metric-input {
  width: 100%;
  min-width: 0;
  padding: 0;
  border: 0;
  border-bottom: 1px solid transparent;
  border-radius: 0;
  background: transparent;
  color: var(--focus-text);
  font-size: clamp(2.125rem, 9vw, 2.75rem);
  line-height: 1.2;
  font-weight: 500;
  text-align: left;
}

.metric-input:focus {
  outline: none;
  border-bottom-color: var(--accent);
}

.metric-input[aria-invalid='true'] {
  border-bottom-color: var(--error-on-dark);
}

.metric-unit {
  flex: none;
  color: var(--focus-muted);
  font-size: var(--text-14);
}

.metric-controls {
  display: grid;
  grid-template-columns: 1fr 1fr;
  margin: 10px -14px 0;
  border-top: 1px solid var(--focus-line);
}

.metric-controls button {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  min-height: 48px;
  color: var(--focus-text);
}

.metric-controls button + button {
  border-left: 1px solid var(--focus-line);
}

.metric-controls button:active {
  background: rgba(241, 240, 234, 0.08);
}

.form-alert {
  margin-top: var(--sp-3);
  padding: 10px 12px;
  border-left: 2px solid var(--error-on-dark);
  color: var(--error-on-dark);
  font-size: var(--text-13);
  line-height: 1.5;
}

.complete-cta {
  width: 100%;
  margin-top: var(--sp-5);
  min-height: 56px;
  font-size: var(--text-16);
}

.storage-alert {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--sp-3);
  margin-top: var(--sp-3);
  padding: 10px 12px;
  border-left: 2px solid var(--error-on-dark);
  color: var(--error-on-dark);
  font-size: var(--text-13);
  line-height: 1.5;
}

.storage-retry {
  flex: none;
  min-height: 44px;
  color: var(--focus-text);
  font-size: var(--text-13);
  text-decoration: underline;
  text-underline-offset: 0.18em;
}

.save-hint {
  margin-top: 10px;
  color: var(--focus-muted);
  font-size: var(--text-12);
  line-height: 1.5;
  text-align: center;
}

.set-list {
  margin-top: var(--sp-7);
  padding-top: var(--sp-4);
  border-top: 1px solid var(--focus-line);
}

.section-head {
  display: flex;
  align-items: baseline;
  justify-content: space-between;
  gap: var(--sp-3);
  margin-bottom: 4px;
}

.section-title {
  color: var(--focus-text);
  font-size: var(--text-16);
  font-weight: 600;
}

.section-meta {
  color: var(--focus-muted);
  font-size: var(--text-12);
  white-space: nowrap;
}

.set-row {
  display: grid;
  grid-template-columns: 72px minmax(0, 1fr) auto;
  align-items: center;
  gap: var(--sp-3);
  min-height: 46px;
  border-bottom: 1px solid var(--focus-line);
  color: var(--focus-muted);
  font-size: var(--text-13);
}

.set-row--done {
  color: var(--focus-text);
}

.set-row--just {
  animation: set-confirm 160ms var(--ease-out);
}

@keyframes set-confirm {
  0% {
    background: rgba(201, 120, 87, 0.22);
  }
  100% {
    background: transparent;
  }
}

.set-index {
  color: var(--focus-muted);
  font-size: var(--text-12);
}

.set-value {
  font-size: var(--text-14);
}

.set-state {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  color: var(--positive-on-dark);
}

.set-time {
  color: var(--focus-muted);
  font-size: var(--text-12);
}

.details {
  margin-top: var(--sp-4);
  border-top: 1px solid var(--focus-line);
}

.details-toggle {
  display: flex;
  align-items: center;
  justify-content: space-between;
  width: 100%;
  min-height: 52px;
  color: var(--focus-muted);
  font-size: var(--text-14);
  text-align: left;
}

.details-icon--open {
  transform: rotate(180deg);
}

.details-body {
  max-width: 62ch;
  padding-bottom: var(--sp-2);
  color: var(--focus-muted);
  font-size: var(--text-14);
  line-height: 1.65;
}

/* 休息 */
.rest-block {
  padding: var(--sp-5) 0 var(--sp-6);
}

.rest-title {
  display: flex;
  align-items: baseline;
  gap: 12px;
  margin-top: 4px;
  color: var(--focus-text);
  font-size: clamp(3.5rem, 16vw, 4.5rem);
  font-weight: 500;
  line-height: 1.05;
  letter-spacing: -0.02em;
}

.rest-paused {
  font-size: var(--text-14);
  font-weight: 500;
  letter-spacing: 0;
  color: var(--focus-muted);
}

.rest-track {
  height: 2px;
  margin-top: var(--sp-5);
  background: var(--focus-line);
  overflow: hidden;
}

.rest-track span {
  display: block;
  height: 100%;
  background: var(--accent);
  transition: width 900ms linear;
}

.rest-next {
  margin-top: var(--sp-4);
  color: var(--focus-muted);
  font-size: var(--text-15);
  line-height: 1.5;
}

.rest-next strong {
  color: var(--focus-text);
  font-weight: 600;
}

.rest-recorded {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  margin-top: 8px;
  color: var(--positive-on-dark);
  font-size: var(--text-13);
}

.rest-default {
  margin-top: var(--sp-4);
  color: var(--focus-muted);
  font-size: var(--text-13);
  line-height: 1.55;
}

.rest-actions {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: var(--sp-2);
  margin-top: var(--sp-6);
}

.rest-skip {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  min-height: 44px;
  margin-top: var(--sp-2);
  color: var(--focus-muted);
  font-size: var(--text-13);
  text-decoration: underline;
  text-underline-offset: 0.18em;
}

.records {
  margin-top: var(--sp-7);
}

.records--compact {
  border-top: 1px solid var(--focus-line);
  padding-top: var(--sp-4);
}

.record-group {
  border-top: 1px solid var(--focus-line);
}

.record-head {
  display: flex;
  align-items: baseline;
  justify-content: space-between;
  gap: var(--sp-3);
  padding: 12px 0 4px;
}

.record-name {
  color: var(--focus-text);
  font-size: var(--text-15);
  font-weight: 600;
}

.record-target {
  color: var(--focus-muted);
  font-size: var(--text-12);
  white-space: nowrap;
}

.record-row {
  display: grid;
  grid-template-columns: 64px minmax(0, 1fr) auto;
  gap: var(--sp-3);
  min-height: 40px;
  align-items: center;
  border-bottom: 1px solid rgba(53, 80, 91, 0.55);
  font-size: var(--text-13);
}

.record-set {
  color: var(--focus-muted);
  font-size: var(--text-12);
}

.record-value {
  color: var(--focus-text);
}

.record-time {
  color: var(--focus-muted);
  font-size: var(--text-12);
}

.record-empty {
  padding: 8px 0;
  color: var(--focus-muted);
  font-size: var(--text-13);
}

/* 完成 */
.complete-block {
  padding: var(--sp-5) 0 var(--sp-6);
}

.complete-title {
  margin-top: 4px;
  color: var(--focus-text);
  font-size: clamp(2.125rem, 9vw, 2.75rem);
  font-weight: 600;
  line-height: 1.15;
  letter-spacing: -0.02em;
}

.complete-facts {
  margin-top: 10px;
  color: var(--focus-muted);
  font-size: var(--text-15);
  line-height: 1.5;
}

.complete-saved {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  margin-top: 6px;
  color: var(--positive-on-dark);
  font-size: var(--text-13);
}

.complete-cta {
  margin-top: var(--sp-5);
}

@media (min-width: 768px) {
  .training-main {
    padding-top: var(--sp-6);
  }

  .focus-title {
    font-size: 2.5rem;
  }

  .metrics {
    gap: var(--sp-4);
  }

  .complete-cta {
    width: auto;
    min-width: 220px;
  }

}
</style>
