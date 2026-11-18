<script setup lang="ts">
import { computed, ref } from 'vue'
import {
  ArrowRight,
  ChevronDown,
  FileText,
  MessageCircle,
  ShieldCheck,
  ShieldOff,
} from '@lucide/vue'
import {
  continueStudentPlan,
  healthAuthorizedFor,
  planWorkFor,
  replyToStudent,
  selectedStudent,
  selectedStudentEvidence,
  selectedStudentRestriction,
} from '../state/store'

const student = selectedStudent
const evidence = selectedStudentEvidence
const restriction = selectedStudentRestriction
const healthAuthorized = computed(() =>
  student.value ? healthAuthorizedFor(student.value) : false,
)
const expanded = ref<string | null>(null)

const relationLabel = computed(() => {
  if (!student.value) return ''
  if (student.value.relationStatus === 'active') return '服务进行中'
  if (student.value.relationStatus === 'pending') return '服务关系待确认'
  return '服务已结束'
})

const canAct = computed(
  () => Boolean(student.value) && student.value?.relationStatus === 'active' && !student.value?.serviceEnded,
)

function toggle(id: string): void {
  expanded.value = expanded.value === id ? null : id
}

function reply(): void {
  if (student.value) replyToStudent(student.value.id)
}

function plan(): void {
  if (student.value) continueStudentPlan(student.value.id)
}
</script>

<template>
  <div v-if="student" class="screen screen--narrow student-detail">
    <section class="student-hero">
      <div class="avatar student-avatar" aria-hidden="true">{{ student.name.slice(0, 1) }}</div>
      <div class="student-identity">
        <p class="student-kicker">学员 · {{ student.packageName }}</p>
        <h1 class="student-name">{{ student.name }}</h1>
        <div class="student-meta">
          <span class="pill" :class="student.relationStatus === 'active' ? 'pill--positive' : student.relationStatus === 'pending' ? 'pill--warning' : 'pill--neutral'">
            {{ relationLabel }}
          </span>
          <span class="meta-item">
            现用版本 {{ student.planVersion || '未建立' }}
            <template v-if="student.deliveredVersion && student.deliveredVersion !== student.planVersion">
              · 已交付版本 {{ student.deliveredVersion }}
            </template>
          </span>
          <span v-if="!student.lastFactHealth || healthAuthorized" class="meta-item">最近：{{ student.lastFactAt }}</span>
        </div>
        <p v-if="!student.lastFactHealth || healthAuthorized" class="student-last">{{ student.lastFact }}</p>
        <p v-else class="student-last">健康内容未授权，最近训练与饮食观察不可见。</p>
      </div>
      <div v-if="canAct" class="student-actions">
        <button type="button" class="btn btn--accent btn--sm" @click="reply">
          <MessageCircle :size="15" aria-hidden="true" />
          回复
        </button>
        <button type="button" class="btn btn--ghost btn--sm" @click="plan">继续计划草稿</button>
      </div>
    </section>

    <div v-if="restriction" class="banner" :class="{ 'banner--warning': !healthAuthorized }" role="status">
      <div>
        <p class="banner-title">
          <ShieldOff v-if="!healthAuthorized" :size="14" aria-hidden="true" />
          {{ healthAuthorized ? '当前可以查看的范围' : '健康内容不可见' }}
        </p>
        <p class="banner-body">{{ restriction }}</p>
      </div>
    </div>

    <div class="detail-grid">
      <section class="detail-main" aria-labelledby="evidence-title">
        <header class="group-head">
          <h2 id="evidence-title" class="group-title">最近证据</h2>
          <span class="group-meta num">{{ evidence.length }} 项可见</span>
        </header>

        <ul v-if="evidence.length" class="evidence-list">
          <li v-for="record in evidence" :key="record.id" class="evidence-item">
            <button
              type="button"
              class="evidence-head"
              :aria-expanded="expanded === record.id"
              :disabled="record.kind === 'plan' && !canAct"
              @click="toggle(record.id)"
            >
              <span class="evidence-kind">
                {{ record.kind === 'training' ? '训练' : record.kind === 'feedback' ? '反馈' : record.kind === 'plan' ? '计划' : '饮食观察' }}
              </span>
              <span class="evidence-main">
                <span class="evidence-title">{{ record.title }}</span>
                <span class="evidence-summary">{{ record.summary }}</span>
                <span class="evidence-date">{{ record.dateLabel }}</span>
              </span>
              <ChevronDown :size="15" aria-hidden="true" :class="{ 'is-open': expanded === record.id }" />
            </button>
            <Transition name="expand">
              <div v-if="expanded === record.id" class="evidence-detail">
                <p>{{ record.summary }}</p>
                <p class="evidence-note">
                  原始组记录可在学员训练记录中查看；已确认组不会被改写。
                </p>
                <button v-if="record.kind === 'plan' && canAct" type="button" class="link-btn" @click="plan">
                  打开完整计划
                  <ArrowRight :size="14" aria-hidden="true" />
                </button>
              </div>
            </Transition>
          </li>
        </ul>

        <div v-else class="empty-state">
          <FileText :size="26" aria-hidden="true" class="empty-icon" />
          <h2 class="empty-title">还没有可见记录</h2>
          <p class="empty-text">
            {{ healthAuthorized ? '学员还没有完成记录。' : '健康记录未授权，当前不可见。' }}
          </p>
        </div>

        <section v-if="planWorkFor(student)" class="pending-work" aria-labelledby="pending-title">
          <h2 id="pending-title" class="pending-title">
            {{ planWorkFor(student).startsWith('已交付') ? '计划进展' : '未完成工作' }}
          </h2>
          <p class="pending-text">
            {{ student.pendingWorkHealth && !healthAuthorized ? '健康相关内容未授权，暂不显示待回复的训练反馈。' : planWorkFor(student) }}
          </p>
          <button
            v-if="canAct && !planWorkFor(student).startsWith('已交付')"
            type="button"
            class="link-btn"
            @click="reply"
          >
            继续处理
            <ArrowRight :size="14" aria-hidden="true" />
          </button>
        </section>
      </section>

      <aside class="detail-side">
        <section class="side-panel" aria-labelledby="service-title">
          <h2 id="service-title" class="side-title">服务与授权</h2>
          <dl class="kv">
            <dt>服务</dt>
            <dd>{{ student.packageName }}</dd>
            <dt>关系</dt>
            <dd>{{ relationLabel }}</dd>
            <dt>计划</dt>
            <dd>
              {{ student.planVersion ? `现用版本 ${student.planVersion}` : '未建立' }}
              <template v-if="student.deliveredVersion && student.deliveredVersion !== student.planVersion">
                · 已交付版本 {{ student.deliveredVersion }}
              </template>
            </dd>
            <dt>最近事实</dt>
            <dd>{{ student.lastFactHealth && !healthAuthorized ? '健康内容未授权' : student.lastFact }}</dd>
          </dl>
          <div class="grant-block">
            <p class="grant-title">
              <ShieldCheck v-if="healthAuthorized" :size="14" aria-hidden="true" />
              <ShieldOff v-else :size="14" aria-hidden="true" />
              {{ healthAuthorized ? '健康内容已授权' : '健康内容未授权' }}
            </p>
            <ul class="grant-list">
              <li>计划与服务记录：{{ student.relationStatus === 'pending' ? '确认后可见' : '可见' }}</li>
              <li>训练记录：{{ healthAuthorized ? '可见' : '不可见' }}</li>
              <li>饮食观察：{{ healthAuthorized ? '可见' : '不可见' }}</li>
            </ul>
            <p class="grant-note">教练不能代替学员开启或恢复授权；只能看到被授权的范围。</p>
          </div>
        </section>

        <section class="side-panel side-panel--quiet" aria-labelledby="context-title">
          <h2 id="context-title" class="side-title">回复上下文</h2>
          <p class="side-copy">
            {{ healthAuthorized ? '回复会带入这位学员与当前服务，并保留最近训练引用。' : '回复会带入这位学员与当前服务；健康内容未授权，不显示训练引用。' }}
          </p>
          <button v-if="canAct" type="button" class="btn btn--block" @click="reply">进入这位学员的消息</button>
          <p v-else class="side-copy">服务关系未建立或已结束，当前不能发起新的计划或健康查看。</p>
        </section>
      </aside>
    </div>

    <p class="student-note">
      未授权时不显示健康正文；关系结束后内容只读。
    </p>
  </div>

  <div v-else class="screen">
    <div class="empty-state">
      <h1 class="empty-title">没有找到这位学员</h1>
      <p class="empty-text">回到学员列表继续查找。</p>
    </div>
  </div>
</template>

<style scoped>
.student-detail {
  padding-bottom: calc(96px + env(safe-area-inset-bottom));
}

.student-hero {
  display: flex;
  align-items: flex-start;
  gap: var(--sp-3);
  padding: var(--sp-2) 0 var(--sp-5);
  border-bottom: 1px solid var(--line);
}

.student-avatar {
  width: 56px;
  height: 56px;
  font-size: var(--text-20);
}

.student-identity {
  min-width: 0;
  flex: 1;
}

.student-kicker {
  color: var(--muted);
  font-size: var(--text-12);
}

.student-name {
  margin-top: 1px;
  font-size: clamp(1.5rem, 5vw, 1.875rem);
  line-height: 1.2;
}

.student-meta {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--sp-2);
  margin-top: 4px;
}

.meta-item {
  color: var(--muted);
  font-size: var(--text-12);
}

.student-last {
  margin-top: var(--sp-2);
  color: var(--muted);
  font-size: var(--text-13);
  line-height: 1.55;
}

.student-actions {
  display: flex;
  flex-direction: column;
  gap: 6px;
  flex: none;
}

.detail-grid {
  display: grid;
  gap: var(--sp-6);
  margin-top: var(--sp-5);
}

.evidence-list {
  border-top: 1px solid var(--line);
}

.evidence-item {
  border-bottom: 1px solid var(--line);
}

.evidence-head {
  display: flex;
  align-items: flex-start;
  gap: var(--sp-3);
  width: 100%;
  padding: var(--sp-3) 0;
  text-align: left;
}

.evidence-kind {
  width: 52px;
  flex: none;
  padding-top: 2px;
  color: var(--muted);
  font-size: var(--text-12);
}

.evidence-main {
  display: flex;
  flex-direction: column;
  gap: 1px;
  min-width: 0;
  flex: 1;
}

.evidence-title {
  font-size: var(--text-15);
  font-weight: 600;
}

.evidence-summary {
  color: var(--muted);
  font-size: var(--text-13);
  line-height: 1.55;
}

.evidence-date {
  color: var(--muted);
  font-size: var(--text-12);
}

.evidence-head .is-open {
  transform: rotate(180deg);
}

.evidence-detail {
  padding: 0 0 var(--sp-4) 68px;
  color: var(--muted);
  font-size: var(--text-13);
  line-height: 1.6;
}

.evidence-note {
  margin-top: 3px;
  font-size: var(--text-12);
}

.pending-work {
  margin-top: var(--sp-5);
  padding: var(--sp-4);
  border-radius: var(--radius-md);
  background: rgba(128, 92, 34, 0.06);
}

.pending-title {
  font-size: var(--text-14);
  font-weight: 600;
  color: var(--warning);
}

.pending-text {
  margin-top: 2px;
  color: var(--muted);
  font-size: var(--text-13);
  line-height: 1.55;
}

.detail-side {
  display: grid;
  gap: var(--sp-4);
  align-content: start;
}

.side-panel {
  padding: var(--sp-4);
  border: 1px solid var(--line);
  border-radius: var(--radius-lg);
  background: color-mix(in srgb, var(--surface) 60%, transparent);
}

.side-title {
  margin-bottom: var(--sp-2);
  font-size: var(--text-15);
  font-weight: 600;
}

.grant-block {
  margin-top: var(--sp-3);
  padding-top: var(--sp-3);
  border-top: 1px solid var(--line);
}

.grant-title {
  display: flex;
  align-items: center;
  gap: 5px;
  font-size: var(--text-13);
  font-weight: 500;
}

.grant-list {
  margin-top: 4px;
}

.grant-list li {
  padding: 2px 0;
  color: var(--muted);
  font-size: var(--text-12);
}

.grant-note {
  margin-top: 4px;
  color: var(--muted);
  font-size: var(--text-12);
  line-height: 1.55;
}

.side-copy {
  color: var(--muted);
  font-size: var(--text-13);
  line-height: 1.6;
}

.student-note {
  margin-top: var(--sp-5);
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

@media (max-width: 500px) {
  .student-hero {
    flex-wrap: wrap;
  }

  .student-actions {
    flex-direction: row;
    flex-wrap: wrap;
    width: 100%;
  }
}

@media (min-width: 768px) {
  .detail-grid {
    grid-template-columns: minmax(0, 1.6fr) minmax(280px, 0.8fr);
    gap: var(--sp-8);
    align-items: start;
  }
}

.student-detail {
  animation: student-context-in 180ms var(--ease-out);
}

@keyframes student-context-in {
  from {
    opacity: 0;
    transform: translateY(4px);
  }
  to {
    opacity: 1;
    transform: translateY(0);
  }
}
</style>
