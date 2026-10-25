<script setup lang="ts">
import { computed } from 'vue'
import {
  ArrowRight,
  Check,
  ClipboardList,
  EyeOff,
  MessageCircle,
  RotateCcw,
} from '@lucide/vue'
import {
  completeTodo,
  doneTodo,
  goToCoachStudents,
  healthAuthorizedFor,
  openTodoItem,
  openTodo,
  restoreTodo,
  state,
  todoCounts,
} from '../state/store'

const rawOpen = openTodo
const open = computed(() => (state.preview.coachTodo === 'empty' ? [] : rawOpen.value))
const done = doneTodo
const counts = computed(() =>
  state.preview.coachTodo === 'empty'
    ? { reply: 0, delivery: 0, authorization: 0, relation: 0, draft: 0 }
    : todoCounts.value,
)

const reasonLabels: Record<string, string> = {
  reply: '待回复',
  delivery: '待跟进',
  authorization: '授权',
  relation: '服务关系',
  draft: '草稿',
}

const reasonOrder = ['reply', 'delivery', 'authorization', 'relation', 'draft']

const grouped = computed(() =>
  reasonOrder
    .map((reason) => ({
      reason,
      label: reasonLabels[reason]!,
      items: open.value.filter((item) => item.reason === reason),
    }))
    .filter((group) => group.items.length > 0),
)

const isEmpty = computed(() => open.value.length === 0)

function openItem(id: string): void {
  openTodoItem(id)
}

function finish(id: string): void {
  completeTodo(id)
}

function sensitiveTodo(item: { healthRelated?: boolean; studentId: string }): boolean {
  const student = state.students.find((entry) => entry.id === item.studentId)
  return Boolean(item.healthRelated && student && !healthAuthorizedFor(student))
}
</script>

<template>
  <div class="screen screen--wide coach-todo">
    <header class="screen-head">
      <div class="screen-heading">
        <p class="screen-kicker">顾言 · 教练 · 10月24日 周六</p>
        <h1 class="screen-title">待办</h1>
        <p class="screen-sub">
          {{ open.length }} 项待处理
          <template v-if="done.length"> · {{ done.length }} 项已处理</template>
        </p>
      </div>
      <div class="screen-tools">
        <div class="todo-counts">
          <span v-for="(count, reason) in counts" :key="reason" class="todo-count">
            <span class="todo-count-num num">{{ count }}</span>
            <span>{{ reasonLabels[reason] }}</span>
          </span>
        </div>
      </div>
    </header>

    <div v-if="isEmpty" class="empty-state">
      <ClipboardList :size="28" aria-hidden="true" class="empty-icon" />
      <h2 class="empty-title">今天没有待处理事项</h2>
      <p class="empty-text">
        目前没有待处理项。可以查看学员与已交付计划，或者等服务内的新反馈。
      </p>
      <div class="empty-actions">
        <button type="button" class="btn" @click="goToCoachStudents">查看学员</button>
      </div>
    </div>

    <div v-else class="todo-layout">
      <section class="todo-main" aria-labelledby="todo-title">
        <h2 id="todo-title" class="sr-only">今日待办</h2>
        <div v-for="group in grouped" :key="group.reason" class="todo-group">
          <header class="group-head">
            <h2 class="group-title">{{ group.label }}</h2>
            <span class="group-meta num">{{ group.items.length }} 项</span>
          </header>
          <ul class="todo-list">
            <li v-for="item in group.items" :key="item.id" class="todo-row">
              <div class="todo-row-main">
                <div class="todo-row-head">
                  <span class="todo-student">{{ item.studentName }}</span>
                  <span class="todo-due">{{ item.dueLabel }}</span>
                </div>
                <p class="todo-title">{{ sensitiveTodo(item) ? '健康相关内容未授权' : item.title }}</p>
                <p class="todo-detail">
                  {{ sensitiveTodo(item) ? '学员未授权健康内容；普通安排与服务仍可处理。' : item.detail }}
                </p>
                <p v-if="item.reason === 'authorization'" class="todo-hint">
                  <EyeOff :size="13" aria-hidden="true" />
                  健康内容未授权时不可见；计划与服务记录仍可查看。
                </p>
              </div>
              <div class="todo-row-actions">
                <button type="button" class="btn btn--sm" @click="openItem(item.id)">
                  {{ item.reason === 'reply' ? '进入回复' : item.reason === 'delivery' ? '查看学员' : '打开' }}
                  <ArrowRight :size="14" aria-hidden="true" />
                </button>
                <button type="button" class="todo-done" @click="finish(item.id)">
                  <Check :size="13" aria-hidden="true" />
                  标记已处理
                </button>
              </div>
            </li>
          </ul>
        </div>
      </section>

      <aside class="todo-side">
        <section class="side-panel" aria-labelledby="done-title">
          <h2 id="done-title" class="side-title">已处理</h2>
          <ul v-if="done.length" class="done-list">
            <li v-for="item in done" :key="item.id">
              <span class="done-main">
                <span class="done-student">{{ item.studentName }}</span>
                <span class="done-title">{{ item.title }}</span>
              </span>
              <button type="button" class="done-restore" @click="restoreTodo(item.id)">
                <RotateCcw :size="13" aria-hidden="true" />
                恢复
              </button>
            </li>
          </ul>
          <p v-else class="side-copy">还没有标记已处理的事项。</p>
          <p class="side-note">标记已处理只改变队列状态，不会删除学员记录、消息或已交付计划。</p>
        </section>

        <section class="side-panel side-panel--quiet" aria-labelledby="context-title">
          <h2 id="context-title" class="side-title">今天的上下文</h2>
          <dl class="kv">
            <dt>在读学员</dt>
            <dd class="num">{{ state.students.filter((student) => student.relationStatus === 'active').length }} 位</dd>
            <dt>待确认</dt>
            <dd class="num">{{ state.students.filter((student) => student.relationStatus === 'pending').length }} 位</dd>
            <dt>已结束</dt>
            <dd class="num">{{ state.students.filter((student) => student.relationStatus === 'ended').length }} 位</dd>
          </dl>
          <p class="side-note">按当前服务关系统计；已结束关系不计入在读。</p>
        </section>
      </aside>
    </div>

    <p class="todo-source">
      <MessageCircle :size="14" aria-hidden="true" />
      队列来自学员消息、已交付计划、授权状态与未保存草稿。
    </p>
  </div>
</template>

<style scoped>
.coach-todo {
  padding-bottom: calc(96px + env(safe-area-inset-bottom));
}

.todo-counts {
  display: flex;
  flex-wrap: wrap;
  gap: var(--sp-2);
}

.todo-count {
  display: inline-flex;
  align-items: baseline;
  gap: 4px;
  padding: 4px 8px;
  border: 1px solid var(--line);
  border-radius: var(--radius-md);
  color: var(--muted);
  font-size: var(--text-12);
  white-space: nowrap;
}

.todo-count-num {
  color: var(--ink);
  font-size: var(--text-15);
  font-weight: 600;
}

.todo-layout {
  display: grid;
  gap: var(--sp-6);
  margin-top: var(--sp-4);
}

.todo-group + .todo-group {
  margin-top: var(--sp-5);
}

.todo-list {
  border-top: 1px solid var(--line);
}

.todo-row {
  display: flex;
  flex-direction: column;
  gap: var(--sp-3);
  padding: var(--sp-4) 0;
  border-bottom: 1px solid var(--line);
}

.todo-row-main {
  min-width: 0;
}

.todo-row-head {
  display: flex;
  align-items: baseline;
  gap: var(--sp-2);
}

.todo-student {
  font-size: var(--text-14);
  font-weight: 600;
}

.todo-due {
  color: var(--muted);
  font-size: var(--text-12);
}

.todo-title {
  margin-top: 1px;
  font-size: var(--text-15);
  font-weight: 500;
}

.todo-detail {
  margin-top: 2px;
  color: var(--muted);
  font-size: var(--text-13);
  line-height: 1.55;
}

.todo-hint {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  margin-top: 4px;
  color: var(--warning);
  font-size: var(--text-12);
  line-height: 1.5;
}

.todo-row-actions {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--sp-3);
}

.todo-done {
  display: inline-flex;
  align-items: center;
  gap: 3px;
  min-height: 44px;
  color: var(--muted);
  font-size: var(--text-12);
}

.todo-done:hover {
  color: var(--ink);
}

.todo-side {
  display: grid;
  gap: var(--sp-4);
  align-content: start;
}

.side-panel {
  padding: var(--sp-4);
  border: 1px solid var(--line);
  border-radius: var(--radius-lg);
  background: rgba(255, 255, 255, 0.6);
}

.side-title {
  margin-bottom: var(--sp-2);
  font-size: var(--text-15);
  font-weight: 600;
}

.side-copy {
  color: var(--muted);
  font-size: var(--text-13);
  line-height: 1.6;
}

.side-note {
  margin-top: var(--sp-3);
  color: var(--muted);
  font-size: var(--text-12);
  line-height: 1.55;
}

.done-list li {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: var(--sp-2);
  padding: 7px 0;
  border-bottom: 1px solid var(--line);
}

.done-main {
  display: flex;
  flex-direction: column;
  min-width: 0;
}

.done-student {
  font-size: var(--text-13);
  font-weight: 500;
}

.done-title {
  color: var(--muted);
  font-size: var(--text-12);
}

.done-restore {
  display: inline-flex;
  align-items: center;
  gap: 3px;
  min-height: 44px;
  flex: none;
  color: var(--muted);
  font-size: var(--text-12);
}

.todo-source {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  margin-top: var(--sp-6);
  color: var(--muted);
  font-size: var(--text-12);
  line-height: 1.5;
}

@media (min-width: 768px) {
  .todo-layout {
    grid-template-columns: minmax(0, 1.7fr) minmax(280px, 0.8fr);
    gap: var(--sp-8);
    align-items: start;
  }

  .todo-row {
    flex-direction: row;
    justify-content: space-between;
    align-items: flex-start;
  }

  .todo-row-actions {
    flex-direction: column;
    align-items: flex-end;
    flex: none;
  }
}
</style>
