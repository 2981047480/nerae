<script setup lang="ts">
import {
  setValueLabel,
  summarizeRecordSets,
} from '../utils/format'
import { computed, ref } from 'vue'
import { ArrowRight, ChartNoAxesCombined, ChevronDown, MessageSquareQuote } from '@lucide/vue'
import {
  goToDiet,
  recordChart,
  recordChartExercise,
  recordExerciseOptions,
  recordSummary,
  recordViewRows,
  recordsFeedback,
  setRecordsExercise,
  setRecordsRange,
  state,
  ui,
} from '../state/store'
import type { TrainingRecord } from '../types'

const range = computed(() => ui.recordsRange)
const exerciseId = computed(() => ui.recordsExerciseId)
const points = computed(() =>
  state.preview.records === 'insufficient' ? [] : recordChart.value,
)
const summary = recordSummary
const feedback = recordsFeedback
const expandedRecord = ref<string | null>(
  ui.recordsFocusExplicit ? ui.recordsFocusId : 'record-20261022',
)

const displayRows = recordViewRows
const focusUnavailable = computed(
  () => ui.recordsFocusExplicit && !ui.recordsFocusId,
)

const chartGeometry = computed(() => {
  const values = points.value.map((point) => point.value)
  if (!values.length) return null
  const unit = points.value[0]?.unit ?? 'kg'
  const min = Math.min(...values)
  const max = Math.max(...values)
  const axisStep = unit.startsWith('秒') ? 5 : 1
  const low = Math.floor(min / axisStep) * axisStep
  let high = Math.ceil(max / axisStep) * axisStep
  if (high === low) high = low + axisStep
  const width = 340
  const height = 170
  const padLeft = 42
  const padTop = 22
  const plotBottom = height - 24
  const plotRight = width - 10
  const times = points.value.map((point) => Date.parse(`${point.date}T00:00:00+08:00`))
  const timeMin = Math.min(...times)
  const timeMax = Math.max(...times)
  const coords = values.map((value, index) => ({
    x:
      values.length === 1
        ? (padLeft + plotRight) / 2
        : padLeft + ((times[index]! - timeMin) / (timeMax - timeMin || 1)) * (plotRight - padLeft),
    y: padTop + (1 - (value - low) / (high - low || 1)) * (plotBottom - padTop),
    value,
    label: points.value[index]!.label,
    date: points.value[index]!.date,
  }))
  const ticks: Array<{ value: number; y: number }> = []
  const tickStep = unit.startsWith('秒') ? Math.max(5, Math.ceil((high - low) / 4 / 5) * 5) : Math.max(1, Math.ceil((high - low) / 4))
  for (let value = low; value <= high + 0.0001; value += tickStep) {
    ticks.push({
      value,
      y: padTop + (1 - (value - low) / (high - low || 1)) * (plotBottom - padTop),
    })
  }
  return { width, height, low, high, padLeft, padTop, plotBottom, plotRight, coords, ticks, unit }
})

const chartKey = computed(() => `${range.value}-${exerciseId.value}-${points.value.length}`)

const chartUnit = computed(() => summary.value.unit)

const measureSource = computed(() => {
  const labels: Record<string, string> = {
    '秒': '最长完成秒数',
    '秒/侧': '最长完成秒数（每侧）',
    '次': '最高次数',
    '次/侧': '最高次数（每侧）',
    '次呼吸': '最多呼吸次数',
    'kg': '最高重量',
    'kg/侧': '最高重量（每侧）',
  }
  return labels[chartUnit.value] ?? '最高值'
})

const deltaLabel = computed(() => {
  const current = summary.value
  if (!current.first || !current.last) return ''
  if (current.points.length < 2) return '只有一次记录，暂不判断趋势'
  if (current.delta === null) return ''
  if (current.delta === 0) return '与范围内第一次相同'
  const sign = current.delta > 0 ? '+' : ''
  return `比 ${current.first.label} ${sign}${current.delta} ${current.unit}`
})

const rangeLabel = computed(() => summary.value.rangeLabel)

function setRange(next: '4w' | '8w' | '12w'): void {
  setRecordsRange(next)
  expandedRecord.value = null
}

function setExercise(event: Event): void {
  setRecordsExercise((event.target as HTMLSelectElement).value)
  expandedRecord.value = null
}

function recordExerciseValue(record: TrainingRecord): string {
  const selected =
    recordExerciseOptions.value.find((option) => option.id === exerciseId.value) ??
    recordExerciseOptions.value[0]
  if (!selected) return '没有这个动作'
  const sets = record.sets.filter((set) => set.exerciseName.trim() === selected.name)
  if (!sets.length) return '本次未记录这个动作'
  return summarizeRecordSets(sets)
}

function toggleRecord(id: string): void {
  expandedRecord.value = expandedRecord.value === id ? null : id
}

function jumpToFeedback(): void {
  const target = feedback.value?.record.id
  if (target) {
    expandedRecord.value = target
    const node = document.getElementById(`record-${target}`)
    node?.scrollIntoView({ block: 'center', behavior: state.reduceMotion ? 'auto' : 'smooth' })
  }
}
</script>

<template>
  <div class="screen screen--wide records studio-records">
    <header class="screen-head">
      <div class="screen-heading">
        <p class="screen-kicker">来自已确认的组记录</p>
        <h1 class="screen-title">看见自己的训练积累</h1>
        <p class="screen-sub">每一次实际记录，都能回头看、接着练。</p>
      </div>
      <div class="screen-tools">
        <button type="button" class="link-btn" @click="goToDiet()">
          饮食自评
          <ArrowRight :size="14" aria-hidden="true" />
        </button>
      </div>
    </header>

    <p v-if="focusUnavailable" class="records-ref-missing" role="status">
      这条训练引用对应的记录不可用；未展开其他记录。
    </p>

    <div class="records-toolbar">
      <div class="range-tabs" role="group" aria-label="时间范围">
        <button type="button" class="range-tab" :class="{ 'range-tab--active': range === '4w' }" :aria-pressed="range === '4w'" @click="setRange('4w')">近 4 周</button>
        <button type="button" class="range-tab" :class="{ 'range-tab--active': range === '8w' }" :aria-pressed="range === '8w'" @click="setRange('8w')">近 8 周</button>
        <button type="button" class="range-tab" :class="{ 'range-tab--active': range === '12w' }" :aria-pressed="range === '12w'" @click="setRange('12w')">近 12 周</button>
      </div>
      <label class="exercise-select">
        <span class="sr-only">选择动作</span>
        <select class="select" :value="exerciseId" @change="setExercise">
          <option v-for="option in recordExerciseOptions" :key="option.id" :value="option.id">{{ option.name }}</option>
        </select>
      </label>
    </div>

    <section v-if="displayRows.length" class="studio-record-summary" :aria-label="`${rangeLabel}记录汇总`">
      <div><span>{{ rangeLabel }} · 训练记录</span><strong class="num">{{ summary.count }} <small>次</small></strong></div>
      <div><span>实际保存的组</span><strong class="num">{{ summary.sets }} <small>组</small></strong></div>
      <div><span>{{ recordChartExercise }} · 最近最高值</span><strong v-if="summary.last" class="num">{{ summary.last.value }} <small>{{ summary.unit }}</small></strong><strong v-else>—</strong></div>
    </section>

    <div class="records-grid">
      <section class="trend-panel" aria-labelledby="trend-title">
        <header class="group-head">
          <h2 id="trend-title" class="group-title">{{ recordChartExercise }}</h2>
          <span class="group-meta num">{{ rangeLabel }} · {{ points.length }} 次记录 · 最高值</span>
        </header>

        <div v-if="points.length && chartGeometry" class="chart-wrap">
          <Transition name="chart-fade" mode="out-in">
            <svg :key="chartKey" class="chart" viewBox="0 0 340 170" role="img" :aria-label="`${recordChartExercise}在${rangeLabel}的${measureSource}变化`">
              <g v-for="tick in chartGeometry.ticks" :key="tick.value">
                <line :x1="chartGeometry.padLeft" :y1="tick.y" :x2="chartGeometry.plotRight" :y2="tick.y" class="axis-grid" />
                <text :x="chartGeometry.padLeft - 7" :y="tick.y + 3" class="axis-label" text-anchor="end">{{ tick.value }}</text>
              </g>
              <text x="4" y="12" class="axis-unit">{{ chartGeometry.unit }}</text>
              <line :x1="chartGeometry.padLeft" :y1="chartGeometry.padTop" :x2="chartGeometry.padLeft" :y2="chartGeometry.plotBottom" class="axis" />
              <polyline
                v-if="chartGeometry.coords.length > 1"
                :points="chartGeometry.coords.map((point) => `${point.x},${point.y}`).join(' ')"
                class="trend-line"
              />
              <g v-for="(point, index) in chartGeometry.coords" :key="point.date">
                <circle :cx="point.x" :cy="point.y" r="3.5" class="trend-dot" />
                <text
                  :x="point.x + (index === 0 ? 7 : index === chartGeometry.coords.length - 1 ? -7 : 0)"
                  :y="point.y - 9"
                  class="trend-value"
                  :text-anchor="index === 0 ? 'start' : index === chartGeometry.coords.length - 1 ? 'end' : 'middle'"
                >{{ point.value }}</text>
                <text
                  :x="point.x"
                  :y="chartGeometry.height - 7"
                  class="trend-date"
                  :text-anchor="index === 0 ? 'start' : index === chartGeometry.coords.length - 1 ? 'end' : 'middle'"
                >{{ point.label }}</text>
              </g>
            </svg>
          </Transition>
        </div>

        <div v-else class="insufficient">
          <ChartNoAxesCombined :size="26" aria-hidden="true" class="empty-icon" />
          <p class="insufficient-title">这个范围里没有足够数据</p>
          <p class="insufficient-text">
            {{ state.preview.records === 'insufficient' ? '所选动作没有已确认的完整记录。' : '换一个时间范围或动作，或者先完成一次训练。' }}
          </p>
          <div class="empty-actions">
            <button type="button" class="btn btn--ghost btn--sm" @click="setRange('12w')">看近 12 周</button>
          </div>
        </div>

        <div v-if="points.length" class="trend-summary">
          <p class="trend-line-text">
            <template v-if="summary.points.length === 1">只有一次记录，暂不判断趋势。</template>
            <template v-else>{{ deltaLabel }}</template>
          </p>
          <p class="trend-source">
            来源：已确认组记录 · {{ measureSource }} · 只比较同一动作与同一计量单位；其他数据保留在明细中。
          </p>
        </div>
      </section>

      <aside class="records-side">
        <section v-if="feedback" class="feedback-panel" aria-labelledby="feedback-title">
          <div class="feedback-head">
            <MessageSquareQuote :size="16" aria-hidden="true" />
            <h2 id="feedback-title" class="side-title">最近点评</h2>
          </div>
          <p class="feedback-text">{{ feedback.feedback.text }}</p>
          <p class="feedback-meta">{{ feedback.feedback.author }} · {{ feedback.feedback.dateLabel }} · {{ feedback.feedback.sourceLabel }}</p>
          <button type="button" class="link-btn" @click="jumpToFeedback">
            查看原记录
            <ArrowRight :size="14" aria-hidden="true" />
          </button>
        </section>
        <section class="side-panel" aria-labelledby="recent-title">
          <h2 id="recent-title" class="side-title">最近训练</h2>
          <ul class="recent-list">
            <li v-for="record in displayRows.slice(0, 4)" :key="record.id">
              <span class="recent-date">{{ record.dateLabel }}</span>
              <span class="recent-title">{{ record.dayTitle }}</span>
              <span class="recent-meta num">{{ record.sets.length }} 组{{ record.partial ? ' · 部分组记录' : '' }}</span>
            </li>
            <li v-if="!displayRows.length" class="recent-empty">这个范围没有训练记录。</li>
          </ul>
        </section>
      </aside>
    </div>

    <section class="group" aria-labelledby="detail-title">
      <header class="group-head">
        <h2 id="detail-title" class="group-title">记录明细</h2>
        <span class="group-meta num">{{ displayRows.length }} 次</span>
      </header>

      <ul v-if="displayRows.length" class="record-list">
        <li
          v-for="record in displayRows"
          :id="`record-${record.id}`"
          :key="record.id"
          class="record-item"
          :class="{ 'record-item--open': expandedRecord === record.id }"
        >
          <button type="button" class="record-head" :aria-expanded="expandedRecord === record.id" @click="toggleRecord(record.id)">
            <span class="record-date">{{ record.dateLabel }}</span>
            <span class="record-main">
              <span class="record-title">
                {{ record.dayTitle }}
                <span v-if="record.partial" class="tag tag--accent">部分组记录</span>
                <span v-if="record.lateRecorded" class="tag">补记</span>
              </span>
              <span class="record-summary num">{{ recordExerciseValue(record) }}</span>
              <span v-if="record.lateRecorded && record.lateNote" class="record-late">{{ record.lateNote }} · 按训练日期归档。</span>
              <span v-if="record.partial && record.partialNote" class="record-late">{{ record.partialNote }}</span>
            </span>
            <span class="record-more">
              {{ expandedRecord === record.id ? '收起' : '明细' }}
              <ChevronDown :size="15" aria-hidden="true" :class="{ 'is-open': expandedRecord === record.id }" />
            </span>
          </button>

          <Transition name="expand">
            <div v-if="expandedRecord === record.id" class="record-detail">
              <ul class="set-table">
                <li v-for="set in record.sets" :key="set.id" class="set-line">
                  <span class="set-name">{{ set.exerciseName }}</span>
                  <span class="set-value num">
                    {{ setValueLabel(set.kind, set) }}
                  </span>
                </li>
              </ul>
              <div v-if="record.feedback" class="record-feedback">
                <p class="record-feedback-title">{{ record.feedback.author }} · {{ record.feedback.dateLabel }}</p>
                <p>{{ record.feedback.text }}</p>
              </div>
              <p class="record-source">计划版本 {{ record.planVersion }} · 按实际填写保存，日期为训练日期。</p>
            </div>
          </Transition>
        </li>
      </ul>

      <div v-else class="empty-state">
        <ChartNoAxesCombined :size="28" aria-hidden="true" class="empty-icon" />
        <h2 class="empty-title">还没有训练记录</h2>
        <p class="empty-text">保存训练记录后，会出现在这里。</p>
        <div class="empty-actions">
          <button type="button" class="btn" @click="setRange('12w')">查看近 12 周</button>
        </div>
      </div>
    </section>

    <p class="records-note">
      这里只聚合已确认的组记录。饮食自评是是否按计划的自我报告，不是实际摄入；训练量也不是身体结果。
    </p>
  </div>
</template>

<style scoped>
.records {
  padding-bottom: calc(96px + env(safe-area-inset-bottom));
}

.records-ref-missing {
  margin: 0 0 var(--sp-3);
  padding: 10px 12px;
  border-left: 2px solid var(--line-strong);
  background: var(--canvas);
  color: var(--muted);
  font-size: var(--text-13);
  line-height: 1.55;
}

.records-toolbar {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: space-between;
  gap: var(--sp-3);
  margin: var(--sp-4) 0 var(--sp-5);
}

.exercise-select {
  min-width: 160px;
}

.exercise-select .select {
  min-height: 44px;
  padding-block: 5px;
}

.records-grid {
  display: grid;
  gap: var(--sp-6);
}

.trend-panel {
  padding: var(--sp-4);
  border: 1px solid var(--line);
  border-radius: var(--radius-lg);
  background: var(--surface);
}

.chart-wrap {
  margin-top: var(--sp-3);
}

.chart {
  width: 100%;
  height: auto;
  overflow: visible;
}

.axis {
  stroke: var(--line-strong);
  stroke-width: 1;
}

.axis-grid {
  stroke: var(--line);
  stroke-width: 0.75;
  stroke-dasharray: 2 3;
}

.axis-label,
.axis-unit {
  fill: var(--muted);
  font-size: 8px;
  font-family: var(--font-sans);
}

.trend-date {
  fill: var(--muted);
  font-size: 8px;
  font-family: var(--font-sans);
}

.trend-line {
  fill: none;
  stroke: var(--ink);
  stroke-width: 1.5;
}

.trend-dot {
  fill: var(--accent);
  stroke: var(--surface);
  stroke-width: 1.5;
}

.trend-value {
  fill: var(--ink);
  font-size: 9px;
  font-family: var(--font-sans);
}

.chart-fade-enter-active,
.chart-fade-leave-active {
  transition: opacity 180ms var(--ease-out);
}

.chart-fade-enter-from,
.chart-fade-leave-to {
  opacity: 0;
}

.insufficient {
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  gap: 4px;
  padding: var(--sp-5) 0;
}

.insufficient-title {
  font-size: var(--text-15);
  font-weight: 600;
}

.insufficient-text {
  max-width: 44ch;
  color: var(--muted);
  font-size: var(--text-13);
  line-height: 1.6;
}

.trend-summary {
  margin-top: var(--sp-3);
  padding-top: var(--sp-3);
  border-top: 1px solid var(--line);
}

.trend-line-text {
  font-size: var(--text-15);
  font-weight: 500;
}

.trend-source {
  margin-top: 3px;
  color: var(--muted);
  font-size: var(--text-12);
  line-height: 1.55;
}

.records-side {
  display: grid;
  gap: var(--sp-4);
  align-content: start;
}

.feedback-panel,
.side-panel {
  padding: var(--sp-4);
  border: 1px solid var(--line);
  border-radius: var(--radius-lg);
  background: color-mix(in srgb, var(--surface) 60%, transparent);
}

.feedback-head {
  display: flex;
  align-items: center;
  gap: 6px;
  color: var(--muted);
}

.side-title {
  font-size: var(--text-15);
  font-weight: 600;
}

.feedback-text {
  margin-top: var(--sp-2);
  font-size: var(--text-14);
  line-height: 1.65;
}

.feedback-meta {
  margin-top: var(--sp-2);
  color: var(--muted);
  font-size: var(--text-12);
  line-height: 1.5;
}

.recent-list {
  margin-top: var(--sp-2);
}

.recent-list li {
  display: grid;
  grid-template-columns: 84px minmax(0, 1fr) auto;
  gap: var(--sp-2);
  align-items: baseline;
  padding: 7px 0;
  border-bottom: 1px solid var(--line);
}

.recent-date {
  color: var(--muted);
  font-size: var(--text-12);
}

.recent-title {
  font-size: var(--text-13);
}

.recent-meta {
  color: var(--muted);
  font-size: var(--text-12);
}

.recent-empty {
  color: var(--muted);
  font-size: var(--text-13);
}

.record-list {
  border-top: 1px solid var(--line);
}

.record-item {
  border-bottom: 1px solid var(--line);
}

.record-head {
  display: flex;
  align-items: flex-start;
  gap: var(--sp-3);
  width: 100%;
  padding: var(--sp-3) 0;
  text-align: left;
}

.record-date {
  width: 82px;
  flex: none;
  padding-top: 2px;
  color: var(--muted);
  font-size: var(--text-12);
}

.record-main {
  display: flex;
  flex-direction: column;
  gap: 1px;
  min-width: 0;
  flex: 1;
}

.record-title {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 5px;
  font-size: var(--text-15);
  font-weight: 600;
}

.record-summary {
  color: var(--muted);
  font-size: var(--text-13);
}

.record-late {
  color: var(--warning);
  font-size: var(--text-12);
  line-height: 1.5;
}

.record-more {
  display: inline-flex;
  align-items: center;
  gap: 2px;
  flex: none;
  padding-top: 2px;
  color: var(--muted);
  font-size: var(--text-12);
}

.record-more .is-open {
  transform: rotate(180deg);
}

.record-detail {
  padding: 0 0 var(--sp-4) 94px;
}

.set-table {
  border-top: 1px solid var(--line);
}

.set-line {
  display: grid;
  grid-template-columns: minmax(0, 1fr) auto;
  gap: var(--sp-3);
  padding: 5px 0;
  color: var(--muted);
  font-size: var(--text-12);
  border-bottom: 1px solid var(--line);
}

.set-value {
  color: var(--ink);
  font-size: var(--text-13);
}

.record-feedback {
  margin-top: var(--sp-3);
  padding: var(--sp-3);
  border-radius: var(--radius-md);
  background: rgba(var(--ink-rgb), 0.045);
  font-size: var(--text-13);
  line-height: 1.6;
}

.record-feedback-title {
  margin-bottom: 3px;
  color: var(--muted);
  font-size: var(--text-12);
}

.record-source {
  margin-top: var(--sp-2);
  color: var(--muted);
  font-size: var(--text-12);
}

.records-note {
  margin-top: var(--sp-6);
  color: var(--muted);
  font-size: var(--text-12);
  line-height: 1.6;
}

.expand-enter-active,
.expand-leave-active {
  transition: opacity 160ms var(--ease-out);
}

.expand-enter-from,
.expand-leave-to {
  opacity: 0;
}

@media (max-width: 380px) {
  .record-detail {
    padding-left: 0;
  }
}

@media (min-width: 768px) {
  .records-grid {
    grid-template-columns: minmax(0, 1.6fr) minmax(280px, 0.8fr);
    gap: var(--sp-7);
    align-items: start;
  }
}
</style>
