<script setup lang="ts">
import { computed } from 'vue'
import { ArrowRight, FileText, MessageCircle, RefreshCw } from '@lucide/vue'
import BaseSheet from '../components/BaseSheet.vue'
import { formatDateTime } from '../utils/format'
import {
  closeSheet,
  confirmAdoption,
  goToConversation,
  goToIncomingPlan,
  goToMessages,
  goToToday,
  incomingAvailable,
  incomingCoachName,
  incomingIsHistorical,
  incomingStudentName,
  incomingChanges,
  incomingDelivery,
  incomingImpact,
  incomingStatusLabel,
  keepCurrentPlan,
  openAdoptSheet,
  setMessageDraft,
  state,
  ui,
} from '../state/store'

const incoming = incomingDelivery
const changes = incomingChanges
const statusLabel = incomingStatusLabel
const available = incomingAvailable

const historical = incomingIsHistorical
const readOnly = computed(() => ui.incomingReadOnly)
const missing = computed(() => ui.incomingRefMissing)
const isAdopted = computed(() => incoming.value?.status === 'adopted')
const isExpired = computed(
  () => state.preview.incomingPlan === 'expired' || incoming.value?.status === 'expired',
)
const noPermission = computed(() => state.preview.incomingPlan === 'no-permission')
const conflict = computed(() => state.preview.incomingPlan === 'conflict')
const content = computed(() => incoming.value?.content ?? null)
const changeCount = computed(() => changes.value.length)

function openAdopt(): void {
  if (conflict.value) ui.conflictResolved = true
  openAdoptSheet()
}

function replyCoach(): void {
  const conversation = state.conversations.find((item) => item.id === 'conv-gu-yan')
  if (conversation) {
    setMessageDraft(
      conversation.id,
      `关于版本 ${incoming.value?.version ?? ''} 的计划，我想先确认一下再决定。`,
    )
  }
  goToConversation('conv-gu-yan')
}

function askAgain(): void {
  const conversation = state.conversations.find((item) => item.id === 'conv-gu-yan')
  if (conversation) {
    setMessageDraft(conversation.id, '这一版计划已经过期了，可以重新发一次吗？')
  }
  goToConversation('conv-gu-yan')
}
</script>

<template>
  <div class="screen screen--narrow incoming">
    <header class="screen-head">
      <div class="screen-heading">
        <p class="screen-kicker">
          {{ readOnly ? `交付给 ${incomingStudentName}` : incomingCoachName }} · {{ content?.title ?? '训练内容' }}
        </p>
        <h1 class="screen-title">{{ readOnly ? '教练查看交付版本' : '收到新版计划' }}</h1>
        <p class="screen-sub num">
          <template v-if="incoming">版本 {{ incoming.version }} · 交付于 {{ formatDateTime(incoming.deliveredAt) }}</template>
          <template v-else>暂无新版本</template>
        </p>
      </div>
      <span class="pill" :class="isAdopted ? 'pill--positive' : isExpired || noPermission ? 'pill--warning' : 'pill--neutral'">
        {{ statusLabel }}
      </span>
    </header>

    <div v-if="missing" class="banner banner--error" role="alert">
      <div>
        <p class="banner-title">这段引用不可用</p>
        <p class="banner-body">引用的计划版本或所属学员无法读取，没有打开最新版本或其他人的内容。</p>
        <div class="empty-actions">
          <button type="button" class="btn btn--ghost btn--sm" @click="goToMessages">回到消息</button>
          <button v-if="!readOnly" type="button" class="btn btn--accent btn--sm" @click="goToIncomingPlan()">查看最新版本</button>
        </div>
      </div>
    </div>

    <div v-else-if="readOnly" class="banner" role="status">
      <div>
        <p class="banner-title">只读交付快照</p>
        <p class="banner-body">这是发给 {{ incomingStudentName }} 的版本 {{ incoming?.version }}；教练查看交付不代表该学员已采用。</p>
      </div>
    </div>

    <div v-else-if="noPermission" class="banner banner--error" role="alert">
      <div>
        <p class="banner-title">无法查看这份计划</p>
        <p class="banner-body">
          与服务关系或健康内容授权有关。普通消息仍可查看，但计划内容不会显示，也不会保留旧版摘要。
        </p>
        <button type="button" class="btn btn--ghost btn--sm banner-action" @click="goToMessages">回到消息</button>
      </div>
    </div>

    <div v-else-if="historical" class="banner banner--warning" role="status">
      <div>
        <p class="banner-title">这是已被替代的历史版本</p>
        <p class="banner-body">
          版本 {{ incoming?.version }} 已被版本 {{ state.student.incoming?.version }} 替代，不能直接采用；下面是当时交付的快照，不会被新内容覆盖。
        </p>
        <button type="button" class="btn btn--ghost btn--sm banner-action" @click="goToIncomingPlan()">查看最新版本</button>
      </div>
    </div>

    <div v-else-if="isExpired" class="banner banner--warning" role="alert">
      <div>
        <p class="banner-title">这份版本已经过期</p>
        <p class="banner-body">旧版不能直接采用，现用安排保持不变。可以请教练重新发送当前版本。</p>
        <button type="button" class="btn btn--ghost btn--sm banner-action" @click="askAgain">请教练重新发送</button>
      </div>
    </div>

    <div v-else-if="isAdopted" class="banner banner--positive" role="status">
      <div>
        <p class="banner-title">已采用版本 {{ incoming?.version }}</p>
        <p class="banner-body">
          10月27日起按新安排；已完成的训练与今天正在进行的会话保持原样。
        </p>
        <button type="button" class="btn btn--ghost btn--sm banner-action" @click="goToToday">回到今天</button>
      </div>
    </div>

    <div v-else-if="conflict" class="banner banner--warning" role="alert">
      <div>
        <p class="banner-title">这份计划基于另一个版本</p>
        <p class="banner-body">
          你当前计划已经变化，不能自动覆盖。下面展示完整内容与具体变化；核对后再决定是否采用。
        </p>
      </div>
    </div>

    <template v-if="!noPermission && incoming && content">
      <section v-if="!readOnly" class="version-row" aria-label="版本关系">
        <div class="version-cell">
          <p class="overline-label">现用</p>
          <p class="version-name num">版本 {{ state.student.planVersion }}</p>
          <p class="version-meta">{{ state.student.plan.title }}</p>
        </div>
        <ArrowRight :size="18" aria-hidden="true" class="version-arrow" />
        <div class="version-cell version-cell--new">
          <p class="overline-label">新交付</p>
          <p class="version-name num">版本 {{ incoming.version }}</p>
          <p class="version-meta">{{ content.title }}</p>
        </div>
      </section>

      <section class="group" aria-labelledby="changes-title">
        <header class="group-head">
          <h2 id="changes-title" class="group-title">具体变化</h2>
          <span class="group-meta num">{{ changeCount }} 处</span>
        </header>
        <ul class="change-list">
          <li v-for="(change, index) in changes" :key="index" class="change-row">
            <span class="change-index num">{{ String(index + 1).padStart(2, '0') }}</span>
            <div class="change-main">
              <p class="change-where">{{ change.dayTitle }} · {{ change.exerciseName }}</p>
              <p class="change-what">
                <template v-if="change.kind === 'field'">
                  {{ change.label }}：{{ change.before || '无' }} → {{ change.after || '无' }}
                </template>
                <template v-else>
                  {{ change.kind === 'added' ? '新增动作' : '移除动作' }}
                </template>
              </p>
            </div>
          </li>
          <li v-if="!changes.length" class="change-empty">与现用版本没有内容变化。</li>
        </ul>
      </section>

      <section class="group" aria-labelledby="full-title">
        <header class="group-head">
          <h2 id="full-title" class="group-title">完整内容</h2>
          <span class="group-meta num">{{ content.days.length }} 个训练日</span>
        </header>
        <div v-for="day in content.days" :key="day.id" class="content-day">
          <h3 class="day-title">{{ day.title }}</h3>
          <ol class="content-exercises">
            <li v-for="exercise in day.exercises" :key="exercise.id" class="content-exercise">
              <span class="content-name">{{ exercise.name }}</span>
              <span class="content-scheme num">{{ exercise.sets }} 组 × {{ exercise.target }} · 休息 {{ exercise.restSeconds }} 秒</span>
              <span v-if="exercise.note" class="content-note">{{ exercise.note }}</span>
            </li>
          </ol>
        </div>
      </section>

      <section v-if="!readOnly && !historical" class="group" aria-labelledby="impact-title">
        <header class="group-head">
          <h2 id="impact-title" class="group-title">采用后的影响</h2>
        </header>
        <dl class="kv">
          <dt>未来安排</dt>
          <dd>{{ incomingImpact.sessions }} 次 · 10月27日起</dd>
          <dt>今天</dt>
          <dd>今天的训练与已记录组不变</dd>
          <dt>历史记录</dt>
          <dd>保留原版本快照与已确认组</dd>
        </dl>
        <p class="impact-note">
          采用只会更新未来的训练安排；不采用时，现用安排保持不变。
        </p>
      </section>

      <div v-if="!isAdopted && !isExpired && !historical && !readOnly && !missing" class="sticky-action">
        <button class="btn btn--accent" type="button" :disabled="!available" @click="openAdopt">
          采用这份安排
        </button>
        <div class="secondary-row">
          <button type="button" class="link-btn" @click="keepCurrentPlan">暂时保留现有安排</button>
          <button type="button" class="link-btn" @click="replyCoach">
            <MessageCircle :size="15" aria-hidden="true" />
            回复教练
          </button>
        </div>
      </div>
      <div v-else-if="isAdopted" class="adopted-facts">
        <FileText :size="16" aria-hidden="true" />
        版本 {{ incoming.version }} 已写入未来安排；今天这次训练没有改变。
      </div>
    </template>

    <div v-else-if="!noPermission" class="empty-state">
      <RefreshCw :size="28" aria-hidden="true" class="empty-icon" />
      <h2 class="empty-title">还没有收到新版本</h2>
      <p class="empty-text">教练交付新计划后会出现在这里。当前安排不会因为等待而改变。</p>
      <div class="empty-actions">
        <button type="button" class="btn" @click="goToMessages">回到消息</button>
      </div>
    </div>

    <BaseSheet :open="ui.sheet === 'adopt'" title="采用这份安排" @close="closeSheet">
      <p class="sheet-copy">
        采用 <strong>版本 {{ incoming?.version }}</strong> 后，10月27日起的未来安排会按新内容更新；今天的训练与已完成的记录保持原样。
      </p>
      <ul class="adopt-summary">
        <li><span>具体变化</span><span class="num">{{ changeCount }} 处</span></li>
        <li><span>未来安排</span><span class="num">{{ incomingImpact.sessions }} 次</span></li>
        <li><span>今天</span><span>不改变</span></li>
      </ul>
      <p v-if="conflict" class="sheet-warning">
        这份内容与现用版本存在冲突；你已经在完整内容中核对过差异。确认后仍以这份版本替换未来安排。
      </p>
      <div v-if="ui.saveNote && ui.saveNoteTone === 'error'" class="form-error" role="alert">{{ ui.saveNote }}</div>
      <template #footer>
        <div class="sheet-actions">
          <button type="button" class="btn btn--ghost" @click="closeSheet">取消</button>
          <button type="button" class="btn btn--accent" @click="confirmAdoption">确认采用版本 {{ incoming?.version }}</button>
        </div>
      </template>
    </BaseSheet>

  </div>
</template>

<style scoped>
.incoming {
  padding-bottom: calc(96px + env(safe-area-inset-bottom));
}

.version-row {
  display: flex;
  align-items: center;
  gap: var(--sp-3);
  margin-top: var(--sp-5);
  padding: var(--sp-4);
  border: 1px solid var(--line);
  border-radius: var(--radius-lg);
  background: var(--surface);
}

.version-cell {
  flex: 1;
  min-width: 0;
}

.version-cell--new .version-name {
  color: var(--ink);
}

.version-name {
  margin-top: 2px;
  font-size: var(--text-17);
  font-weight: 600;
}

.version-meta {
  color: var(--muted);
  font-size: var(--text-13);
  line-height: 1.4;
}

.version-arrow {
  flex: none;
  color: var(--muted);
}

.change-list {
  border-top: 1px solid var(--line);
}

.change-row {
  display: flex;
  gap: var(--sp-3);
  padding: var(--sp-3) 0;
  border-bottom: 1px solid var(--line);
}

.change-index {
  width: 24px;
  flex: none;
  padding-top: 2px;
  color: var(--muted);
  font-size: var(--text-12);
}

.change-main {
  min-width: 0;
}

.change-where {
  font-size: var(--text-14);
  font-weight: 500;
}

.change-what {
  margin-top: 2px;
  color: var(--muted);
  font-size: var(--text-13);
  line-height: 1.55;
  overflow-wrap: anywhere;
}

.change-empty {
  padding: var(--sp-4) 0;
  color: var(--muted);
  font-size: var(--text-13);
}

.content-day {
  padding: var(--sp-3) 0;
  border-bottom: 1px solid var(--line);
}

.day-title {
  font-size: var(--text-15);
  font-weight: 600;
}

.content-exercises {
  margin-top: 6px;
}

.content-exercise {
  display: grid;
  grid-template-columns: minmax(0, 1fr) auto;
  gap: 2px var(--sp-3);
  padding: 6px 0;
}

.content-name {
  font-size: var(--text-14);
}

.content-scheme {
  color: var(--muted);
  font-size: var(--text-12);
  text-align: right;
}

.content-note {
  grid-column: 1 / -1;
  color: var(--muted);
  font-size: var(--text-12);
  line-height: 1.55;
}

.impact-note {
  margin-top: var(--sp-3);
  color: var(--muted);
  font-size: var(--text-12);
  line-height: 1.6;
}

.secondary-row {
  display: flex;
  flex-wrap: wrap;
  justify-content: center;
  gap: var(--sp-4);
  margin-top: var(--sp-2);
}

.banner-action {
  margin-top: var(--sp-2);
}

.sheet-copy {
  font-size: var(--text-14);
  line-height: 1.65;
}

.adopt-summary {
  margin-top: var(--sp-3);
  border-top: 1px solid var(--line);
}

.adopt-summary li {
  display: flex;
  justify-content: space-between;
  gap: var(--sp-3);
  padding: 8px 0;
  border-bottom: 1px solid var(--line);
  font-size: var(--text-13);
}

.sheet-warning {
  margin-top: var(--sp-3);
  padding: var(--sp-3);
  border-radius: var(--radius-md);
  background: rgba(128, 92, 34, 0.08);
  color: var(--warning);
  font-size: var(--text-13);
  line-height: 1.55;
}

.form-error {
  margin-top: var(--sp-2);
  color: var(--error);
  font-size: var(--text-13);
}

.sheet-actions {
  display: flex;
  justify-content: flex-end;
  gap: var(--sp-2);
}

.adopted-facts {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  margin-top: var(--sp-4);
  color: var(--positive);
  font-size: var(--text-13);
}

.sticky-action {
  position: static;
  bottom: auto;
  max-width: 100%;
  margin-top: var(--sp-5);
  padding: 0;
  background: none;
}

.sticky-action .btn {
  width: 100%;
}

@media (min-width: 768px) {
  .sticky-action {
    max-width: 380px;
  }
}
</style>
