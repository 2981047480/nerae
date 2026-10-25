<script setup lang="ts">
import { computed, ref } from 'vue'
import {
  ArrowRight,
  Check,
  CircleAlert,
  Lock,
  MessageCircle,
  RefreshCw,
  Sparkles,
  Wrench,
} from '@lucide/vue'
import BaseSheet from '../components/BaseSheet.vue'
import {
  acceptAiInput,
  activeAiTemplate,
  aiApplied,
  aiCanApply,
  aiStatus,
  aiStatusCopy,
  applyAiCandidate,
  closeSheet,
  goToMyPlan,
  refreshAiCandidate,
  requestAiApply,
  requestCoachAdjustment,
  selectAiTemplate,
  setAiIntent,
  state,
  ui,
} from '../state/store'
import { AI_TEMPLATES } from '../data/extended'

const template = activeAiTemplate
const status = aiStatus
const statusCopy = aiStatusCopy
const intent = ref(state.ai.intent)

const ownTemplates = computed(() => AI_TEMPLATES.filter((item) => item.context === 'own'))
const coachTemplates = computed(() => AI_TEMPLATES.filter((item) => item.context === 'coach'))
const isCoachPlan = computed(() => state.ai.context === 'coach' || template.value.context === 'coach')
const showCandidate = computed(() => status.value === 'ready' || status.value === 'duplicate')

function choose(id: string): void {
  selectAiTemplate(id)
  intent.value = state.ai.intent
}

function updateIntent(): void {
  setAiIntent(intent.value)
}

function apply(): void {
  requestAiApply()
}
</script>

<template>
  <div class="screen screen--narrow ai-assist">
    <header class="screen-head">
      <div class="screen-heading">
        <p class="screen-kicker">基于已有内容 · 不生成个人处方</p>
        <h1 class="screen-title">整理这份计划</h1>
        <p class="screen-sub">我的计划 · 版本 {{ state.ownPlan.version }}</p>
      </div>
      <span class="ai-source">
        <Sparkles :size="14" aria-hidden="true" />
        {{ template.sourceLabel }}
      </span>
    </header>

    <section class="ai-intent" aria-labelledby="intent-title">
      <h2 id="intent-title" class="group-title">从已有模板开始</h2>
      <div class="template-list" role="group" aria-label="选择已有模板">
        <button
          v-for="item in ownTemplates"
          :key="item.id"
          type="button"
          class="template-btn"
          :class="{ 'template-btn--active': !isCoachPlan && state.ai.templateId === item.id }"
          :aria-pressed="!isCoachPlan && state.ai.templateId === item.id"
          @click="choose(item.id)"
        >
          <span class="template-name">{{ item.title }}</span>
          <span class="template-note">{{ item.intent }}</span>
        </button>
        <button
          v-for="item in coachTemplates"
          :key="item.id"
          type="button"
          class="template-btn"
          :class="{ 'template-btn--active': isCoachPlan }"
          :aria-pressed="isCoachPlan"
          @click="choose(item.id)"
        >
          <span class="template-name">{{ item.title }}</span>
          <span class="template-note">教练交付的计划</span>
        </button>
      </div>

      <label class="field intent-field">
        <span class="field-label">你想调整什么</span>
        <input
          v-model="intent"
          class="input"
          type="text"
          placeholder="例如：这周想做得稳一点"
          @change="updateIntent"
        />
      </label>
    </section>

    <div v-if="isCoachPlan" class="banner banner--warning" role="alert">
      <div>
        <p class="banner-title"><Lock :size="15" aria-hidden="true" /> 教练计划不能由学员自助修改</p>
        <p class="banner-body">
          这是教练交付的内容。你可以把具体想法发给教练，由教练核对后调整；这样不会覆盖教练已经确认过的版本。
        </p>
        <div class="banner-actions">
          <button type="button" class="btn btn--sm" @click="requestCoachAdjustment">
            <MessageCircle :size="15" aria-hidden="true" />
            把想法发给教练
          </button>
          <button type="button" class="btn btn--ghost btn--sm" @click="goToMyPlan">查看我的计划</button>
        </div>
      </div>
    </div>

    <section v-else class="ai-result" aria-labelledby="result-title">
      <div class="result-head">
        <h2 id="result-title" class="group-title">候选与来源</h2>
        <span class="pill" :class="status === 'ready' ? 'pill--positive' : status === 'safety_block' ? 'pill--error' : status === 'needs_input' || status === 'quota' || status === 'expired' ? 'pill--warning' : 'pill--neutral'">
          {{ statusCopy.title }}
        </span>
      </div>

      <div
        class="status-block"
        :class="{
          'status-block--error': statusCopy.tone === 'error',
          'status-block--warning': statusCopy.tone === 'warning',
          'status-block--positive': statusCopy.tone === 'positive',
        }"
        role="status"
      >
        <p class="status-title">{{ statusCopy.title }}</p>
        <p class="status-text">{{ statusCopy.text }}</p>

        <div v-if="status === 'needs_input'" class="status-form">
          <label class="field">
            <span class="field-label">可用器械与需要避开的部位</span>
            <input class="input" placeholder="例如：一对哑铃，右肩不适" />
          </label>
          <button type="button" class="btn btn--sm" @click="acceptAiInput">补充后继续</button>
        </div>

        <div v-if="status === 'tech_error'" class="status-actions">
          <button type="button" class="btn btn--sm" @click="refreshAiCandidate">
            <RefreshCw :size="15" aria-hidden="true" />
            重试
          </button>
          <button type="button" class="btn btn--ghost btn--sm" @click="goToMyPlan">手动调整</button>
        </div>

        <div v-if="status === 'quota'" class="status-actions">
          <button type="button" class="btn btn--ghost btn--sm" @click="goToMyPlan">
            <Wrench :size="15" aria-hidden="true" />
            手动调整计划
          </button>
        </div>

        <div v-if="status === 'expired'" class="status-actions">
          <button type="button" class="btn btn--sm" @click="refreshAiCandidate">基于当前草稿重新查看</button>
        </div>

        <div v-if="status === 'duplicate'" class="status-actions">
          <button type="button" class="btn btn--ghost btn--sm" @click="goToMyPlan">去我的计划查看</button>
        </div>
      </div>

      <template v-if="showCandidate">
        <h3 class="candidate-title">候选改动 · {{ template.changes.length }} 处</h3>
        <ul class="candidate-list">
          <li v-for="(change, index) in template.changes" :key="index" class="candidate-row">
            <span class="candidate-index num">{{ String(index + 1).padStart(2, '0') }}</span>
            <div class="candidate-main">
              <p class="candidate-where">{{ change.dayTitle }} · {{ change.exerciseName }}</p>
              <p class="candidate-change">{{ change.label }}：{{ change.before }} → {{ change.after }}</p>
            </div>
          </li>
        </ul>
        <p class="candidate-note">{{ template.note }}</p>
        <p class="candidate-source">
          来源：{{ template.sourceLabel }} · 模板版本 {{ template.templateVersion }} · 候选固定。
        </p>
      </template>

      <p v-if="aiApplied" class="applied-line" role="status">
        <Check :size="15" aria-hidden="true" />
        已改到我的计划草稿；现用安排未改变。保存后可在我的计划中继续安排；公开审阅是另一个选择。
      </p>

      <div class="ai-actions">
        <button class="btn btn--accent" type="button" :disabled="!aiCanApply" @click="apply">
          应用到计划草稿
        </button>
        <button class="btn btn--ghost" type="button" @click="goToMyPlan">返回我的计划</button>
      </div>
    </section>

    <section class="ai-guard" aria-labelledby="guard-title">
      <h2 id="guard-title" class="group-title">边界</h2>
      <ul class="guard-list">
        <li>
          <CircleAlert :size="15" aria-hidden="true" />
          候选只来自已有模板，不从零生成个人处方。
        </li>
        <li>
          <CircleAlert :size="15" aria-hidden="true" />
          候选不合适时可以返回我的计划手动调整，或换一条已有模板。
        </li>
        <li>
          <ArrowRight :size="15" aria-hidden="true" />
          应用候选只改我的计划草稿，不改变当前训练安排。
        </li>
      </ul>
    </section>

    <BaseSheet :open="ui.sheet === 'applyAi'" title="应用这条候选" @close="closeSheet">
      <p class="sheet-copy">
        这条候选会改动我的计划草稿中的 <strong>{{ template.changes.length }} 处</strong>，不会改变今天正在进行的训练，也不会自动公开。
      </p>
      <ul class="apply-list">
        <li v-for="(change, index) in template.changes" :key="index">
          <span>{{ change.exerciseName }} · {{ change.label }}</span>
          <span>{{ change.before }} → {{ change.after }}</span>
        </li>
      </ul>
      <p class="sheet-copy sheet-copy--muted">
        来源：{{ template.sourceLabel }}。同一条候选只应用一次；应用后先保存草稿，公开审阅另行选择。
      </p>
      <template #footer>
        <div class="sheet-actions">
          <button type="button" class="btn btn--ghost" @click="closeSheet">取消</button>
          <button type="button" class="btn btn--accent" @click="applyAiCandidate">应用到草稿</button>
        </div>
      </template>
    </BaseSheet>
  </div>
</template>

<style scoped>
.ai-assist {
  padding-bottom: calc(96px + env(safe-area-inset-bottom));
}

.ai-source {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  flex: none;
  padding: 5px 9px;
  border: 1px solid var(--line);
  border-radius: 999px;
  color: var(--muted);
  font-size: var(--text-12);
}

.ai-intent {
  margin-top: var(--sp-5);
}

.template-list {
  display: grid;
  gap: 6px;
  margin-top: var(--sp-2);
}

.template-btn {
  display: flex;
  flex-direction: column;
  gap: 2px;
  padding: 10px 12px;
  border: 1px solid var(--line);
  border-radius: var(--radius-md);
  text-align: left;
}

.template-btn--active {
  border-color: var(--ink);
  background: rgba(25, 47, 57, 0.04);
}

.template-name {
  font-size: var(--text-14);
  font-weight: 500;
}

.template-note {
  color: var(--muted);
  font-size: var(--text-12);
  line-height: 1.45;
}

.intent-field {
  margin-top: var(--sp-3);
}

.ai-result {
  margin-top: var(--sp-6);
}

.result-head {
  display: flex;
  align-items: baseline;
  justify-content: space-between;
  gap: var(--sp-3);
}

.status-block {
  margin-top: var(--sp-3);
  padding: var(--sp-4);
  border: 1px solid var(--line);
  border-radius: var(--radius-md);
  background: rgba(255, 255, 255, 0.6);
}

.status-block--error {
  border-color: rgba(162, 63, 61, 0.35);
  background: rgba(162, 63, 61, 0.05);
}

.status-block--warning {
  border-color: rgba(128, 92, 34, 0.35);
  background: rgba(128, 92, 34, 0.05);
}

.status-block--positive {
  border-color: rgba(39, 111, 83, 0.3);
  background: rgba(39, 111, 83, 0.04);
}

.status-title {
  font-size: var(--text-15);
  font-weight: 600;
}

.status-text {
  margin-top: 3px;
  color: var(--muted);
  font-size: var(--text-13);
  line-height: 1.6;
}

.status-form {
  display: flex;
  align-items: flex-end;
  gap: var(--sp-2);
  margin-top: var(--sp-3);
}

.status-form .field {
  flex: 1;
}

.status-actions {
  display: flex;
  flex-wrap: wrap;
  gap: var(--sp-2);
  margin-top: var(--sp-3);
}

.candidate-title {
  margin-top: var(--sp-5);
  font-size: var(--text-15);
  font-weight: 600;
}

.candidate-list {
  margin-top: var(--sp-2);
  border-top: 1px solid var(--line);
}

.candidate-row {
  display: flex;
  gap: var(--sp-3);
  padding: var(--sp-3) 0;
  border-bottom: 1px solid var(--line);
}

.candidate-index {
  width: 24px;
  flex: none;
  padding-top: 2px;
  color: var(--muted);
  font-size: var(--text-12);
}

.candidate-main {
  min-width: 0;
}

.candidate-where {
  font-size: var(--text-14);
  font-weight: 500;
}

.candidate-change {
  margin-top: 2px;
  color: var(--muted);
  font-size: var(--text-13);
  line-height: 1.55;
  overflow-wrap: anywhere;
}

.candidate-note {
  margin-top: var(--sp-3);
  font-size: var(--text-14);
  line-height: 1.65;
}

.candidate-source {
  margin-top: var(--sp-2);
  color: var(--muted);
  font-size: var(--text-12);
  line-height: 1.55;
}

.applied-line {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  margin-top: var(--sp-3);
  color: var(--positive);
  font-size: var(--text-13);
}

.ai-actions {
  display: flex;
  flex-wrap: wrap;
  gap: var(--sp-2);
  margin-top: var(--sp-4);
}

.ai-guard {
  margin-top: var(--sp-7);
  padding-top: var(--sp-4);
  border-top: 1px solid var(--line);
}

.guard-list li {
  display: flex;
  align-items: flex-start;
  gap: 6px;
  padding: 5px 0;
  color: var(--muted);
  font-size: var(--text-12);
  line-height: 1.55;
}

.banner-actions {
  display: flex;
  flex-wrap: wrap;
  gap: var(--sp-2);
  margin-top: var(--sp-3);
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

.apply-list {
  margin-top: var(--sp-3);
  border-top: 1px solid var(--line);
}

.apply-list li {
  display: flex;
  flex-direction: column;
  gap: 2px;
  padding: 8px 0;
  border-bottom: 1px solid var(--line);
  font-size: var(--text-13);
}

.apply-list li span:last-child {
  color: var(--muted);
}

.sheet-actions {
  display: flex;
  justify-content: flex-end;
  gap: var(--sp-2);
}

@media (min-width: 600px) {
  .template-list {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
}
</style>
