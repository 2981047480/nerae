<script setup lang="ts">
import { computed, onMounted } from 'vue'
import {
  ArrowLeft,
  ArrowRight,
  Check,
  ChevronRight,
  CircleAlert,
  MessageCircle,
  RotateCcw,
  Send,
  ShieldOff,
  Trash2,
} from '@lucide/vue'
import BaseSheet from '../components/BaseSheet.vue'
import {
  closeConversation,
  closeSheet,
  confirmHealthRevoke,
  currentConversation,
  discardFailedMessage,
  goToCoachDetail,
  goToCoachStudentDetail,
  goToExplore,
  messagesReadOnly,
  messagesRelationPending,
  openConversation,
  openMessageRef,
  retryMessage,
  sendMessage,
  setMessageDraft,
  state,
  ui,
  conversationList,
  activeRelation,
  conversationUnread,
  conversationHealthVisible,
  messageTextForActor,
  messageRefsForActor,
  visibleMessagesForActor,
  lastVisibleMessage,
} from '../state/store'
import type { ChatMessage, CoachProfile, Conversation, ServicePackage } from '../types'
import { formatDateTime, formatTime } from '../utils/format'

const isCoach = computed(() => state.actor === 'coach')
const selected = currentConversation
const hasThread = computed(() => Boolean(selected.value))

const displayConversation = computed<Conversation | null>(
  () => selected.value ?? conversationList.value[0] ?? null,
)

const currentDraft = computed(
  () => displayConversation.value?.drafts[isCoach.value ? 'coach' : 'student'] ?? '',
)

function studentNameFor(conversation: Conversation | null): string {
  if (!conversation) return ''
  return state.students.find((student) => student.id === conversation.studentId)?.name ?? '学员'
}

function conversationName(conversation: Conversation): string {
  return isCoach.value ? studentNameFor(conversation) : conversation.coachName
}

const threadMemberName = computed(() =>
  displayConversation.value ? conversationName(displayConversation.value) : '',
)

const displayRelation = computed(() => {
  const conversation = displayConversation.value
  if (!conversation) return null
  return (
    state.relations.find((relation) => relation.id === conversation.relationId) ??
    activeRelation.value
  )
})

const displayCoach = computed<CoachProfile | null>(
  () => state.coaches.find((coach) => coach.id === displayConversation.value?.coachId) ?? null,
)

const currentPackage = computed<ServicePackage | null>(() => {
  const coach = displayCoach.value
  const conversation = displayConversation.value
  if (!coach || !conversation) return null
  return coach.packages.find((pack) => pack.id === conversation.packageId) ?? null
})

function priceLabel(pack: ServicePackage | null): string {
  if (!pack) return ''
  if (!pack.priceYuan) return '免费'
  return `¥${pack.priceYuan} ${pack.priceUnit}`.trim()
}

const currentPackageConfirmed = computed(
  () =>
    displayRelation.value?.status === 'active' &&
    displayRelation.value.packageId === displayConversation.value?.packageId &&
    Boolean(
      currentPackage.value &&
        displayRelation.value.serviceVersion === currentPackage.value.version,
    ),
)

const agreedSeparate = computed(() => {
  const relation = displayRelation.value
  if (!relation || relation.status !== 'active') return false
  if (relation.packageId !== displayConversation.value?.packageId) return true
  return Boolean(currentPackage.value && relation.serviceVersion !== currentPackage.value.version)
})

const relationLabel = computed(() => {
  const relation = displayRelation.value
  if (!relation) return '服务关系未建立'
  if (relation.status === 'active') return '服务进行中'
  if (relation.status === 'pending') return '服务约定待确认'
  return '服务已结束'
})

const healthLabel = computed(() =>
  conversationHealthVisible(displayConversation.value)
    ? '已授权查看健康内容'
    : '健康内容不可见',
)

const readOnly = computed(() => messagesReadOnly.value)
const pendingRelation = computed(() => messagesRelationPending.value)

const listEmpty = computed(() => conversationList.value.length === 0)

function lastLine(conversation: Conversation): string {
  const message = lastVisibleMessage(conversation)
  if (!message) return '还没有消息'
  const text = messageTextForActor(conversation, message).trim()
  if (message.redacted || !text) return '健康内容已不可见'
  return text.length > 34 ? `${text.slice(0, 34)}…` : text
}

function unreadFor(conversation: Conversation): number {
  return conversationUnread(conversation)
}

function messageVisibleFor(message: ChatMessage): boolean {
  if (!displayConversation.value) return true
  return messageTextForActor(displayConversation.value, message) === message.text
}

const displayMessages = computed(() =>
  displayConversation.value ? visibleMessagesForActor(displayConversation.value) : [],
)

function timeLabel(iso: string): string {
  const value = new Date(iso)
  const now = new Date()
  const sameDay =
    value.getFullYear() === now.getFullYear() &&
    value.getMonth() === now.getMonth() &&
    value.getDate() === now.getDate()
  return sameDay ? formatTime(iso) : formatDateTime(iso)
}

function openThread(id: string): void {
  openConversation(id)
}

function send(): void {
  if (!selected.value) return
  sendMessage(selected.value.id)
}

function retry(message: ChatMessage): void {
  if (!selected.value) return
  retryMessage(message.id, selected.value.id)
}

function discard(message: ChatMessage): void {
  if (!selected.value) return
  discardFailedMessage(message.id, selected.value.id)
}

function openService(): void {
  if (!displayConversation.value) return
  if (isCoach.value) goToCoachStudentDetail(displayConversation.value.studentId)
  else goToCoachDetail(displayConversation.value.coachId, displayConversation.value.packageId)
}

function goFindCoaches(): void {
  state.explore.mode = 'coach'
  state.actor = 'student'
  goToExplore()
}

function replyFromCoach(): void {
  if (selected.value) ui.activeConversationId = selected.value.id
}

onMounted(() => {
  if (typeof window !== 'undefined' && window.innerWidth >= 900 && !ui.activeConversationId) {
    const first = conversationList.value[0]
    if (first) openConversation(first.id)
  }
  replyFromCoach()
})
</script>

<template>
  <div class="screen screen--wide messages-page">
    <header class="screen-head messages-head" :class="{ 'messages-head--thread': hasThread }">
      <div class="screen-heading">
        <p class="screen-kicker">{{ isCoach ? '教练工作台' : '与教练的对话' }}</p>
        <h1 class="screen-title">消息</h1>
      </div>
    </header>

    <div class="messages-layout" :class="{ 'messages-layout--thread': hasThread }">
      <section class="conversation-list" aria-labelledby="list-title">
        <h2 id="list-title" class="sr-only">对话列表</h2>
        <ul v-if="!listEmpty" class="list">
          <li v-for="conversation in conversationList" :key="conversation.id">
            <button
              type="button"
              class="conversation"
              :class="{ 'conversation--active': selected?.id === conversation.id, 'conversation--unread': unreadFor(conversation) > 0 }"
              :aria-current="selected?.id === conversation.id ? 'true' : undefined"
              @click="openThread(conversation.id)"
            >
              <span class="avatar avatar--muted">{{ conversationName(conversation).slice(0, 1) }}</span>
              <span class="conversation-main">
                <span class="conversation-head">
                  <span class="conversation-name">{{ conversationName(conversation) }}</span>
                  <span class="conversation-time num">{{ timeLabel(lastVisibleMessage(conversation)?.sentAt ?? conversation.updatedAt) }}</span>
                </span>
                <span class="conversation-service">{{ conversation.serviceLabel }}</span>
                <span class="conversation-last">{{ lastLine(conversation) }}</span>
              </span>
              <span v-if="unreadFor(conversation)" class="unread num">{{ unreadFor(conversation) }}</span>
            </button>
          </li>
        </ul>
        <div v-else class="empty-state">
          <MessageCircle :size="28" aria-hidden="true" class="empty-icon" />
          <h2 class="empty-title">还没有对话</h2>
          <p class="empty-text">可以先看看教练与服务，发起咨询后再在这里继续。</p>
          <div class="empty-actions">
            <button type="button" class="btn" @click="goFindCoaches">看看教练</button>
          </div>
        </div>
      </section>

      <section v-if="displayConversation" class="thread" aria-labelledby="thread-title">
        <header class="thread-head">
          <button v-if="hasThread" type="button" class="thread-back" aria-label="返回对话列表" @click="closeConversation">
            <ArrowLeft :size="18" aria-hidden="true" />
          </button>
          <div class="thread-title-wrap">
            <h2 id="thread-title" class="thread-title">{{ threadMemberName }}</h2>
            <p class="thread-service">
              {{ displayConversation.serviceLabel }}
              <button type="button" class="thread-service-link" @click="openService">
                {{ isCoach ? '查看学员' : '查看服务' }}
                <ChevronRight :size="13" aria-hidden="true" />
              </button>
            </p>
          </div>
          <span class="pill" :class="displayRelation?.status === 'active' ? 'pill--positive' : displayRelation?.status === 'pending' ? 'pill--warning' : 'pill--neutral'">
            {{ relationLabel }}
          </span>
        </header>

        <details class="relation-panel">
          <summary class="relation-summary">
            服务与授权
            <span class="relation-summary-meta">{{ currentPackage?.name ?? relationLabel }} · {{ healthLabel }}</span>
          </summary>
          <div class="relation-body">
            <div v-if="currentPackage" class="relation-row">
              <span class="relation-key">当前服务</span>
              <span class="relation-value">
                {{ currentPackage.name }} · {{ priceLabel(currentPackage) }}
                <span class="relation-state">{{ currentPackageConfirmed ? '已确认' : '尚未确认' }}</span>
              </span>
            </div>
            <div v-if="agreedSeparate" class="relation-row">
              <span class="relation-key">已有约定</span>
              <span v-if="displayRelation?.agreedNeedsReview" class="relation-value">
                原约定{{ displayRelation?.agreedPriceKnown ? ` ¥${displayRelation?.agreedPriceYuan}` : '' }} · 价格/单位/周期需要核对
                <span class="relation-state">
                  {{ displayRelation?.agreedAtKnown ? `已于 ${displayRelation?.agreedAt.slice(5, 10)} 确认，保持不变` : '确认时间也需要核对' }}
                </span>
              </span>
              <span v-else class="relation-value">
                {{ displayRelation?.agreedName }} · ¥{{ displayRelation?.agreedPriceYuan }} {{ displayRelation?.agreedPriceUnit }} · {{ displayRelation?.agreedCycle }}
                <span class="relation-state">已于 {{ displayRelation?.agreedAt.slice(5, 10) }} 确认，保持不变</span>
              </span>
            </div>
            <div class="relation-row">
              <span class="relation-key">健康内容</span>
              <span class="relation-value" :class="{ 'relation-value--off': !conversationHealthVisible(displayConversation) }">
                {{ healthLabel }}
              </span>
              <button
                v-if="conversationHealthVisible(displayConversation) && !isCoach"
                type="button"
                class="relation-action"
                @click="ui.sheet = 'revoke'"
              >
                撤回授权
              </button>
            </div>
          </div>
        </details>

        <div v-if="readOnly" class="banner banner--warning thread-banner" role="status">
          <div>
            <p class="banner-title">这段对话只读</p>
            <p class="banner-body">服务关系已结束或已撤回，不能再发送新消息；历史内容保留。</p>
          </div>
        </div>

        <div v-else-if="pendingRelation" class="banner thread-banner" role="status">
          <div>
            <p class="banner-title">服务关系待确认</p>
            <p class="banner-body">可以继续普通咨询；计划与健康内容在服务确认前不显示。</p>
          </div>
        </div>

        <div v-if="displayRelation?.healthRevokedAt" class="banner thread-banner" role="status">
          <div>
            <p class="banner-title">健康内容授权已撤回</p>
            <p class="banner-body">
              <template v-if="isCoach">学员已撤回健康内容授权；此前与健康有关的原文、训练引用与观察不再显示。普通安排与服务沟通仍可继续。</template>
              <template v-else>教练侧不再显示此前与健康有关的原文与引用；你自己的记录和消息仍保留。撤回不会删除你的训练记录。</template>
            </p>
          </div>
        </div>

        <TransitionGroup name="message" tag="ol" class="message-flow" aria-live="polite">
          <li
            v-for="message in displayMessages"
            :key="message.id"
            class="message"
            :class="{
              'message--mine': isCoach ? message.author === 'coach' : message.author === 'student',
              'message--failed': message.status === 'failed',
            }"
          >
            <div class="message-meta">
              <span class="message-author">{{ message.authorName }}</span>
              <span class="message-time num">{{ timeLabel(message.sentAt) }}</span>
            </div>
            <div
              v-if="message.redacted || !messageVisibleFor(message)"
              class="message-bubble message-bubble--redacted"
            >
              <ShieldOff :size="14" aria-hidden="true" />
              健康内容已不可见
            </div>
            <div v-else class="message-bubble">{{ messageTextForActor(displayConversation, message) }}</div>
            <div v-if="messageRefsForActor(displayConversation, message).length" class="message-refs">
              <button
                v-for="ref in messageRefsForActor(displayConversation, message)"
                :key="`${ref.kind}-${ref.id}`"
                type="button"
                class="ref-card"
                @click="openMessageRef(ref, displayConversation.id)"
              >
                <span class="ref-kind">
                  {{ ref.kind === 'plan' ? '计划' : ref.kind === 'training' ? '训练' : '服务' }}
                </span>
                <span class="ref-main">
                  <span class="ref-label">{{ ref.label }}</span>
                  <span class="ref-meta">{{ ref.meta }}</span>
                </span>
                <ArrowRight :size="15" aria-hidden="true" />
              </button>
            </div>
            <p v-if="message.status === 'sending'" class="message-status">发送中…</p>
            <div v-if="message.status === 'failed' && message.author === state.actor" class="message-failed" role="alert">
              <span><CircleAlert :size="14" aria-hidden="true" /> 未发送</span>
              <button type="button" class="message-retry" @click="retry(message)">
                <RotateCcw :size="13" aria-hidden="true" />
                重试
              </button>
              <button type="button" class="message-retry" @click="discard(message)">
                <Trash2 :size="13" aria-hidden="true" />
                删除
              </button>
            </div>
          </li>
          <li v-if="!displayMessages.length" class="message-empty">
            还没有消息。可以从具体服务或训练发起咨询，已知内容会带入。
          </li>
        </TransitionGroup>

        <footer v-if="!readOnly" class="composer">
          <label class="sr-only" for="message-input">写消息</label>
          <textarea
            id="message-input"
            class="composer-input"
            rows="2"
            placeholder="写一条消息"
            :value="currentDraft"
            @input="setMessageDraft(displayConversation.id, ($event.target as HTMLTextAreaElement).value)"
            @keydown.ctrl.enter.prevent="send"
            @keydown.meta.enter.prevent="send"
          />
          <button
            type="button"
            class="send-btn"
            :disabled="!currentDraft.trim()"
            aria-label="发送消息"
            @click="send"
          >
            <Send :size="18" aria-hidden="true" />
          </button>
          <p class="composer-note">
            发送失败会保留原文，重试不会新增一条消息。
          </p>
        </footer>
        <div v-else class="composer composer--disabled">
          <p>服务已结束，不能继续发送消息。</p>
        </div>
      </section>

      <section v-else class="thread thread--placeholder" aria-label="对话">
        <div class="thread-placeholder">
          <MessageCircle :size="28" aria-hidden="true" class="empty-icon" />
          <p>选择一段对话查看消息与引用。</p>
        </div>
      </section>
    </div>

    <BaseSheet :open="ui.sheet === 'revoke'" title="撤回健康内容授权" @close="closeSheet">
      <p class="sheet-copy">
        撤回后，教练将看不到未来的健康内容、饮食观察与相关消息；教练侧不再显示此前发送的健康原文与引用。你自己的记录与消息仍保留。
      </p>
      <p class="sheet-copy sheet-copy--muted">
        普通训练安排沟通不受影响；健康授权由你决定，教练不能替你恢复。你的训练记录仍保留在本机。
      </p>
      <template #footer>
        <div class="sheet-actions">
          <button type="button" class="btn btn--ghost" @click="closeSheet">取消</button>
          <button type="button" class="btn btn--danger" @click="confirmHealthRevoke">确认撤回健康授权</button>
        </div>
      </template>
    </BaseSheet>

    <p class="sr-only"><Check /><ShieldOff /></p>
  </div>
</template>

<style scoped>
.messages-page {
  padding-bottom: calc(96px + env(safe-area-inset-bottom));
}

.messages-layout {
  display: grid;
  gap: var(--sp-5);
  margin-top: var(--sp-4);
}

.conversation-list {
  min-width: 0;
}

.list {
  border-top: 1px solid var(--line);
}

.conversation {
  display: flex;
  align-items: flex-start;
  gap: var(--sp-3);
  width: 100%;
  padding: var(--sp-3) 0;
  border-bottom: 1px solid var(--line);
  text-align: left;
}

.conversation--active {
  background: rgba(var(--ink-rgb), 0.035);
}

.conversation-main {
  display: flex;
  flex-direction: column;
  gap: 1px;
  min-width: 0;
  flex: 1;
}

.conversation-head {
  display: flex;
  align-items: baseline;
  justify-content: space-between;
  gap: var(--sp-2);
}

.conversation-name {
  font-size: var(--text-15);
  font-weight: 600;
}

.conversation-time {
  color: var(--muted);
  font-size: var(--text-12);
}

.conversation-service {
  color: var(--muted);
  font-size: var(--text-12);
  line-height: 1.4;
}

.conversation-last {
  margin-top: 2px;
  color: var(--muted);
  font-size: var(--text-13);
  line-height: 1.45;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.conversation--unread .conversation-name,
.conversation--unread .conversation-last {
  color: var(--ink);
  font-weight: 600;
}

.unread {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  min-width: 20px;
  height: 20px;
  padding: 0 5px;
  border-radius: 10px;
  background: var(--accent);
  color: var(--on-accent);
  font-size: var(--text-12);
  font-weight: 600;
}

.thread {
  display: none;
  min-width: 0;
  border-top: 1px solid var(--line);
}

.messages-layout--thread .thread {
  display: block;
}

.messages-layout--thread .conversation-list {
  display: none;
}

.thread-head {
  display: flex;
  align-items: flex-start;
  gap: var(--sp-2);
  padding: var(--sp-3) 0;
  border-bottom: 1px solid var(--line);
}

.thread-back {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 44px;
  height: 44px;
  margin-left: -10px;
  flex: none;
  color: var(--ink);
}

.thread-title-wrap {
  min-width: 0;
  flex: 1;
}

.thread-title {
  font-size: var(--text-17);
  font-weight: 600;
}

.thread-service {
  color: var(--muted);
  font-size: var(--text-12);
  line-height: 1.5;
}

.thread-service-link {
  display: inline-flex;
  align-items: center;
  gap: 1px;
  min-height: 44px;
  color: var(--ink);
  font-size: var(--text-12);
  text-decoration: underline;
  text-underline-offset: 2px;
}

.relation-panel {
  padding: var(--sp-2) 0;
  border-bottom: 1px solid var(--line);
}

.relation-summary {
  display: flex;
  flex-wrap: wrap;
  align-items: baseline;
  justify-content: space-between;
  gap: 2px var(--sp-3);
  min-height: 44px;
  padding: 8px 0;
  color: var(--ink);
  font-size: var(--text-13);
  font-weight: 500;
  cursor: pointer;
  list-style: none;
}

.relation-summary::-webkit-details-marker {
  display: none;
}

.relation-summary-meta {
  flex: 1 1 100%;
  color: var(--muted);
  font-size: var(--text-12);
  font-weight: 400;
  text-align: left;
}

@media (min-width: 600px) {
  .relation-summary-meta {
    flex: 0 1 auto;
    text-align: right;
  }
}

.relation-body {
  padding: 2px 0 6px;
}

.relation-row {
  display: flex;
  align-items: baseline;
  gap: var(--sp-2);
  padding: 2px 0;
  font-size: var(--text-13);
}

.relation-key {
  width: 68px;
  flex: none;
  color: var(--muted);
}

.relation-value {
  min-width: 0;
  flex: 1;
}

.relation-value--off {
  color: var(--warning);
}

.relation-state {
  display: inline-block;
  margin-left: 6px;
  color: var(--muted);
  font-size: var(--text-12);
}

.relation-action {
  min-height: 44px;
  color: var(--error);
  font-size: var(--text-12);
  text-decoration: underline;
  text-underline-offset: 2px;
}

.relation-note {
  margin-top: 4px;
  color: var(--muted);
  font-size: var(--text-12);
  line-height: 1.55;
}

.thread-banner {
  margin-top: var(--sp-3);
}

.message-flow {
  padding: var(--sp-4) 0;
  border-bottom: 1px solid var(--line);
}

.message {
  margin-bottom: var(--sp-4);
}

.message--mine {
  text-align: right;
}

.message-meta {
  display: flex;
  align-items: baseline;
  gap: var(--sp-2);
  margin-bottom: 3px;
  color: var(--muted);
  font-size: var(--text-12);
}

.message--mine .message-meta {
  justify-content: flex-end;
}

.message-author {
  color: var(--ink);
  font-weight: 500;
}

.message-time {
  font-size: var(--text-12);
}

.message-bubble {
  display: inline-block;
  max-width: 86%;
  padding: 9px 12px;
  border-radius: 12px;
  background: var(--surface);
  border: 1px solid var(--line);
  font-size: var(--text-14);
  line-height: 1.6;
  text-align: left;
  overflow-wrap: anywhere;
}

.message--mine .message-bubble {
  background: var(--ink);
  border-color: var(--ink);
  color: var(--on-ink);
}

.message-bubble--redacted {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  color: var(--muted);
  font-style: normal;
  background: rgba(var(--ink-rgb), 0.04);
  border-style: dashed;
}

.message-refs {
  display: flex;
  flex-direction: column;
  gap: 5px;
  margin-top: 5px;
  align-items: flex-start;
}

.message--mine .message-refs {
  align-items: flex-end;
}

.ref-card {
  display: flex;
  align-items: center;
  gap: var(--sp-2);
  max-width: 100%;
  padding: 8px 10px;
  border: 1px solid var(--line-strong);
  border-radius: var(--radius-md);
  background: color-mix(in srgb, var(--surface) 70%, transparent);
  text-align: left;
}

.ref-kind {
  flex: none;
  color: var(--muted);
  font-size: var(--text-12);
}

.ref-main {
  display: flex;
  flex-direction: column;
  min-width: 0;
}

.ref-label {
  font-size: var(--text-13);
  font-weight: 500;
}

.ref-meta {
  color: var(--muted);
  font-size: var(--text-12);
}

.message-status {
  margin-top: 3px;
  color: var(--muted);
  font-size: var(--text-12);
}

.message-failed {
  display: inline-flex;
  align-items: center;
  gap: var(--sp-3);
  margin-top: 4px;
  color: var(--error);
  font-size: var(--text-12);
}

.message-failed span {
  display: inline-flex;
  align-items: center;
  gap: 4px;
}

.message-retry {
  display: inline-flex;
  align-items: center;
  gap: 3px;
  min-height: 44px;
  color: var(--error);
  font-size: var(--text-12);
  text-decoration: underline;
  text-underline-offset: 2px;
}

.message-empty {
  color: var(--muted);
  font-size: var(--text-13);
  line-height: 1.6;
}

.composer {
  display: flex;
  flex-wrap: wrap;
  align-items: flex-end;
  gap: var(--sp-2);
  padding: var(--sp-3) 0;
  position: sticky;
  bottom: calc(68px + env(safe-area-inset-bottom));
  background: var(--canvas);
}

.composer-input {
  flex: 1;
  min-width: 0;
  min-height: 48px;
  max-height: 160px;
  padding: 10px 12px;
  border: 1px solid var(--line-strong);
  border-radius: var(--radius-md);
  background: var(--surface);
  font-size: var(--text-15);
  line-height: 1.5;
  resize: vertical;
}

.send-btn {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 48px;
  height: 48px;
  flex: none;
  border-radius: var(--radius-md);
  background: var(--ink);
  color: var(--on-ink);
}

.send-btn:disabled {
  opacity: 0.4;
  cursor: not-allowed;
}

.composer-note {
  width: 100%;
  color: var(--muted);
  font-size: var(--text-12);
  line-height: 1.5;
}

.composer--disabled {
  padding: var(--sp-3);
  border-radius: var(--radius-md);
  background: rgba(var(--ink-rgb), 0.04);
  color: var(--muted);
  font-size: var(--text-13);
}

.thread-placeholder {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: var(--sp-2);
  min-height: 260px;
  color: var(--muted);
  font-size: var(--text-13);
  border: 1px dashed var(--line-strong);
  border-radius: var(--radius-lg);
}

.channel-note {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  margin-top: var(--sp-5);
  color: var(--muted);
  font-size: var(--text-12);
  line-height: 1.5;
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

.sheet-actions {
  display: flex;
  justify-content: flex-end;
  gap: var(--sp-2);
}

@media (max-width: 899px) {
  .messages-head--thread {
    display: none;
  }
}

@media (min-width: 900px) {
  .messages-layout {
    grid-template-columns: minmax(260px, 0.7fr) minmax(0, 1.5fr);
    gap: var(--sp-8);
    align-items: start;
  }

  .thread,
  .messages-layout--thread .thread {
    display: block;
  }

  .conversation-list,
  .messages-layout--thread .conversation-list {
    display: block;
  }

  .thread-head {
    padding-top: 0;
  }

  .thread-back {
    display: none;
  }

  .composer {
    bottom: 0;
  }
}

.message-enter-active {
  transition:
    opacity 160ms var(--ease-out),
    transform 160ms var(--ease-out);
}

.message-enter-from {
  opacity: 0;
  transform: translateY(4px);
}

.message-move {
  transition: transform 160ms var(--ease-out);
}
</style>
