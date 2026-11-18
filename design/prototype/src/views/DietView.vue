<script setup lang="ts">
import { formatTime } from '../utils/format'
import { computed } from 'vue'
import { ArrowRight, BookOpen, Check, Info, ShieldOff } from '@lucide/vue'
import {
  dietCoachVisibility,
  dietInsufficient,
  dietItem,
  dietReport,
  goToExplore,
  goToRecords,
  saveDietReport,
  setDietObservation,
  setDietStatus,
  state,
} from '../state/store'

const item = dietItem
const report = dietReport
const failed = computed(() => state.diet.saveState === 'failed')
const revoked = computed(() => state.preview.diet === 'revoked')
const risk = computed(() => state.diet.contentRisk)
const insufficient = computed(() => state.preview.diet === 'insufficient')

const statusLabel = computed(() => {
  if (report.value.status === 'on_plan') return '按计划'
  if (report.value.status === 'partial') return '部分按计划'
  if (report.value.status === 'off_plan') return '未按计划'
  return '还没有自评'
})

const coachVisibility = dietCoachVisibility

function choose(status: 'on_plan' | 'partial' | 'off_plan'): void {
  setDietStatus(status)
}
</script>

<template>
  <div v-if="item" class="screen screen--narrow diet">
    <header class="screen-head">
      <div class="screen-heading">
        <p class="screen-kicker">{{ item.source }} · 非医疗内容</p>
        <h1 class="screen-title">{{ item.title }}</h1>
        <p class="screen-sub">{{ item.countLabel }} · {{ item.perWeek }}</p>
      </div>
      <button type="button" class="link-btn" @click="goToRecords">
        训练记录
        <ArrowRight :size="14" aria-hidden="true" />
      </button>
    </header>

    <div v-if="risk" class="banner banner--warning" role="alert">
      <div>
        <p class="banner-title">这份内容需要专业判断</p>
        <p class="banner-body">
          这里不提供个人热量、疾病饮食或替代治疗的建议。可以先看看内容原则；需要个人方案时咨询专业人员或教练。
        </p>
      </div>
    </div>

    <div v-if="revoked" class="banner banner--warning" role="status">
      <div>
        <p class="banner-title"><ShieldOff :size="15" aria-hidden="true" /> 健康内容授权已撤回</p>
        <p class="banner-body">
          教练看不到这项自评与补充观察；你自己的记录仍保留在这里，可以继续编辑。
        </p>
      </div>
    </div>

    <div class="diet-grid">
      <section class="principles" aria-labelledby="principles-title">
        <header class="group-head">
          <h2 id="principles-title" class="group-title">内容原则</h2>
          <span class="group-meta">来源：{{ item.source }}</span>
        </header>
        <p class="diet-summary">{{ item.summary }}</p>
        <ol class="principle-list">
          <li v-for="(line, index) in item.contents" :key="line.name" class="principle">
            <span class="principle-index num">{{ String(index + 1).padStart(2, '0') }}</span>
            <div>
              <p class="principle-name">{{ line.name }}</p>
              <p class="principle-detail">{{ line.detail }}</p>
            </div>
          </li>
        </ol>
        <div class="scope-block">
          <BookOpen :size="15" aria-hidden="true" />
          <p>
            这里只有可读的原则与示例，没有实际食物记录、份量或确认摄入；不会给出热量或营养估算，也不把一次自评当成身体结论。
          </p>
        </div>
        <button type="button" class="btn btn--ghost btn--sm" @click="goToExplore">看看其他饮食内容</button>
      </section>

      <section class="self-report" aria-labelledby="report-title">
        <header class="group-head">
          <h2 id="report-title" class="group-title">今天是否按计划</h2>
          <span class="group-meta">10月24日 · {{ statusLabel }}</span>
        </header>

        <div class="report-status" role="radiogroup" aria-label="今天的饮食自评">
          <button
            type="button"
            role="radio"
            class="report-option"
            :class="{ 'report-option--active': report.status === 'on_plan' }"
            :aria-checked="report.status === 'on_plan'"
            @click="choose('on_plan')"
          >
            按计划
          </button>
          <button
            type="button"
            role="radio"
            class="report-option"
            :class="{ 'report-option--active': report.status === 'partial' }"
            :aria-checked="report.status === 'partial'"
            @click="choose('partial')"
          >
            部分按计划
          </button>
          <button
            type="button"
            role="radio"
            class="report-option"
            :class="{ 'report-option--active': report.status === 'off_plan' }"
            :aria-checked="report.status === 'off_plan'"
            @click="choose('off_plan')"
          >
            未按计划
          </button>
        </div>

        <label class="field observation-field">
          <span class="field-label">补充观察（可选）</span>
          <textarea
            class="textarea"
            :value="report.observation"
            placeholder="例如：晚餐蔬菜偏少，其他按计划"
            @input="setDietObservation(($event.target as HTMLTextAreaElement).value)"
          />
          <span class="field-hint">只记录你自己的观察，不是摄入计算。</span>
        </label>

        <div class="report-meta">
          <p v-if="report.savedAt" class="saved-line" role="status">
            <Check :size="14" aria-hidden="true" />
            已保存 · {{ formatTime(report.savedAt) }}
          </p>
          <p class="visibility-line">
            {{ coachVisibility }}
          </p>
        </div>

        <div v-if="failed || state.preview.diet === 'save-failed'" class="banner banner--error" role="alert">
          <div>
            <p class="banner-title">没有保存成功</p>
            <p class="banner-body">你填写的内容仍保留在页面中，可以重试；这不是健康结论。</p>
          </div>
        </div>

        <div v-if="insufficient" class="insufficient-note">
          <Info :size="15" aria-hidden="true" />
          <p>{{ dietInsufficient }}</p>
        </div>

        <button type="button" class="btn btn--accent btn--block save-report" @click="saveDietReport">
          保存今天的自评
        </button>
        <p class="report-note">自评只表示今天是否按计划，不会自动改变训练或计划。</p>
      </section>
    </div>

    <section class="diet-detail" aria-labelledby="detail-title">
      <header class="group-head">
        <h2 id="detail-title" class="group-title">适用与限制</h2>
      </header>
      <ul class="fit-list">
        <li v-for="line in item.fit" :key="line">{{ line }}</li>
      </ul>
      <p class="diet-source">{{ item.source }} · 不作为个人营养评估或医疗建议。</p>
    </section>
  </div>

  <div v-else class="screen">
    <div class="empty-state">
      <h1 class="empty-title">没有找到这份饮食内容</h1>
      <p class="empty-text">它可能已经下架；回到发现看看其他内容。</p>
      <div class="empty-actions">
        <button type="button" class="btn" @click="goToExplore">回到发现</button>
      </div>
    </div>
  </div>
</template>

<style scoped>
.diet {
  padding-bottom: calc(96px + env(safe-area-inset-bottom));
}

.diet-grid {
  display: grid;
  gap: var(--sp-6);
  margin-top: var(--sp-5);
}

.principles,
.self-report {
  min-width: 0;
}

.diet-summary {
  color: var(--muted);
  font-size: var(--text-14);
  line-height: 1.65;
}

.principle-list {
  margin-top: var(--sp-3);
  border-top: 1px solid var(--line);
}

.principle {
  display: flex;
  gap: var(--sp-3);
  padding: var(--sp-3) 0;
  border-bottom: 1px solid var(--line);
}

.principle-index {
  width: 24px;
  flex: none;
  padding-top: 2px;
  color: var(--muted);
  font-size: var(--text-12);
}

.principle-name {
  font-size: var(--text-14);
  font-weight: 500;
}

.principle-detail {
  margin-top: 2px;
  color: var(--muted);
  font-size: var(--text-13);
  line-height: 1.6;
}

.scope-block {
  display: flex;
  align-items: flex-start;
  gap: 7px;
  margin: var(--sp-4) 0;
  padding: var(--sp-3);
  border-radius: var(--radius-md);
  background: rgba(var(--ink-rgb), 0.045);
  color: var(--muted);
  font-size: var(--text-12);
  line-height: 1.6;
}

.self-report {
  padding: var(--sp-4);
  border: 1px solid var(--line);
  border-radius: var(--radius-lg);
  background: var(--surface);
}

.report-status {
  display: grid;
  gap: 6px;
  margin-top: var(--sp-2);
}

.report-option {
  min-height: 46px;
  padding: 0 13px;
  border: 1px solid var(--line);
  border-radius: var(--radius-md);
  color: var(--muted);
  font-size: var(--text-14);
  text-align: left;
}

.report-option--active {
  border-color: var(--ink);
  background: rgba(var(--ink-rgb), 0.05);
  color: var(--ink);
  font-weight: 600;
}

.observation-field {
  margin-top: var(--sp-4);
}

.field-hint {
  color: var(--muted);
  font-size: var(--text-12);
  line-height: 1.5;
}

.report-meta {
  margin-top: var(--sp-3);
}

.saved-line {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  color: var(--positive);
  font-size: var(--text-13);
}

.visibility-line {
  margin-top: 3px;
  color: var(--muted);
  font-size: var(--text-12);
  line-height: 1.55;
}

.insufficient-note {
  display: flex;
  align-items: flex-start;
  gap: 6px;
  margin-top: var(--sp-3);
  padding: var(--sp-2) var(--sp-3);
  border-left: 2px solid var(--line-strong);
  color: var(--muted);
  font-size: var(--text-12);
  line-height: 1.55;
}

.save-report {
  margin-top: var(--sp-4);
}

.report-note {
  margin-top: var(--sp-2);
  color: var(--muted);
  font-size: var(--text-12);
  line-height: 1.55;
}

.diet-detail {
  margin-top: var(--sp-7);
  padding-top: var(--sp-4);
  border-top: 1px solid var(--line);
}

.fit-list {
  margin-top: var(--sp-2);
}

.fit-list li {
  padding: 6px 0;
  border-bottom: 1px solid var(--line);
  color: var(--muted);
  font-size: var(--text-13);
  line-height: 1.55;
}

.diet-source {
  margin-top: var(--sp-3);
  color: var(--muted);
  font-size: var(--text-12);
  line-height: 1.55;
}

@media (max-width: 767px) {
  .self-report {
    order: -1;
  }
}

@media (min-width: 768px) {
  .diet-grid {
    grid-template-columns: minmax(0, 1.35fr) minmax(300px, 0.9fr);
    gap: var(--sp-8);
    align-items: start;
  }
}
</style>
