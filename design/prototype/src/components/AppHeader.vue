<script setup lang="ts">
import { computed } from 'vue'
import { ArrowLeft } from '@lucide/vue'
import Wordmark from './Wordmark.vue'
import { COACH_NAME, PREVIEW_DATE_LABEL, STUDENT_NAME } from '../data/sample'
import {
  canGoBack,
  goBack,
  goToCoachServices,
  goToCoachStudents,
  goToCoachTodo,
  goToExplore,
  goToMessages,
  goToRecords,
  goToToday,
  state,
  ui,
} from '../state/store'
import type { Screen } from '../types'

const isCoach = computed(() => state.actor === 'coach')

const titles: Partial<Record<Screen, string>> = {
  planDetail: '计划详情',
  records: '记录',
  messages: '消息',
  incomingPlan: '收到计划',
  myPlan: '我的内容',
  studentPlan: '现用计划',
  aiAssist: '整理建议',
  diet: '饮食与自评',
  coachDetail: '教练详情',
  coachTodo: '待办',
  coachStudents: '学员',
  coachStudentDetail: '学员详情',
  coachServices: '服务内容',
  coachPlan: '计划',
}

const detailTitle = computed(() =>
  state.screen === 'incomingPlan' && ui.incomingReadOnly
    ? '交付版本'
    : titles[state.screen] ?? '',
)
const showBack = computed(() => canGoBack())

const studentRoot = computed(() => {
  if (state.screen === 'today') return 'today'
  if (state.screen === 'explore' || state.screen === 'coachDetail') return 'explore'
  if (state.screen === 'records' || state.screen === 'diet') return 'records'
  if (state.screen === 'messages' || state.screen === 'incomingPlan') return 'messages'
  return ''
})
const coachRoot = computed(() => {
  if (state.screen === 'coachTodo') return 'todo'
  if (state.screen === 'coachStudents' || state.screen === 'coachStudentDetail') return 'students'
  if (state.screen === 'messages') return 'messages'
  if (state.screen === 'coachServices') return 'services'
  return ''
})

function goStudentRoot(key: 'today' | 'explore' | 'records' | 'messages'): void {
  if (key === 'today') goToToday()
  else if (key === 'explore') goToExplore()
  else if (key === 'records') goToRecords()
  else goToMessages()
}

function goCoachRoot(key: 'todo' | 'students' | 'messages' | 'services'): void {
  if (key === 'todo') goToCoachTodo()
  else if (key === 'students') goToCoachStudents()
  else if (key === 'services') goToCoachServices()
  else goToMessages()
}
</script>

<template>
  <header class="app-header">
    <div class="brand-row">
      <button v-if="showBack" type="button" class="back-btn" aria-label="返回上一页" @click="goBack">
        <ArrowLeft :size="20" aria-hidden="true" />
      </button>
      <button v-else class="brand" type="button" aria-label="回到主要页面" @click="isCoach ? goCoachRoot('todo') : goToToday()">
        <Wordmark />
      </button>
      <div class="header-context">
        <p v-if="showBack && detailTitle" class="header-title">{{ detailTitle }}</p>
        <p class="header-meta" :class="{ 'header-meta--title': showBack && detailTitle }">
          <template v-if="isCoach">{{ COACH_NAME }} · 教练</template>
          <template v-else>{{ STUDENT_NAME }} · {{ PREVIEW_DATE_LABEL }}</template>
        </p>
      </div>
    </div>

    <nav v-if="!showBack" class="desktop-nav" :aria-label="isCoach ? '教练主导航' : '学员主导航'">
      <template v-if="isCoach">
        <button type="button" class="nav-link" :class="{ 'nav-link--active': coachRoot === 'todo' }" :aria-current="coachRoot === 'todo' ? 'page' : undefined" @click="goCoachRoot('todo')">待办</button>
        <button type="button" class="nav-link" :class="{ 'nav-link--active': coachRoot === 'students' }" :aria-current="coachRoot === 'students' ? 'page' : undefined" @click="goCoachRoot('students')">学员</button>
        <button type="button" class="nav-link" :class="{ 'nav-link--active': coachRoot === 'messages' }" :aria-current="coachRoot === 'messages' ? 'page' : undefined" @click="goCoachRoot('messages')">消息</button>
        <button type="button" class="nav-link" :class="{ 'nav-link--active': coachRoot === 'services' }" :aria-current="coachRoot === 'services' ? 'page' : undefined" @click="goCoachRoot('services')">服务</button>
      </template>
      <template v-else>
        <button type="button" class="nav-link" :class="{ 'nav-link--active': studentRoot === 'today' }" :aria-current="studentRoot === 'today' ? 'page' : undefined" @click="goStudentRoot('today')">今天</button>
        <button type="button" class="nav-link" :class="{ 'nav-link--active': studentRoot === 'explore' }" :aria-current="studentRoot === 'explore' ? 'page' : undefined" @click="goStudentRoot('explore')">发现</button>
        <button type="button" class="nav-link" :class="{ 'nav-link--active': studentRoot === 'records' }" :aria-current="studentRoot === 'records' ? 'page' : undefined" @click="goStudentRoot('records')">记录</button>
        <button type="button" class="nav-link" :class="{ 'nav-link--active': studentRoot === 'messages' }" :aria-current="studentRoot === 'messages' ? 'page' : undefined" @click="goStudentRoot('messages')">消息</button>
      </template>
    </nav>
  </header>
</template>

<style scoped>
.app-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--sp-4);
  width: 100%;
  max-width: var(--container);
  margin-inline: auto;
  padding: calc(12px + env(safe-area-inset-top)) var(--gutter) 8px;
  padding-right: calc(var(--gutter) + 84px);
}

.brand-row {
  display: flex;
  align-items: center;
  gap: var(--sp-3);
  min-width: 0;
}

.brand,
.back-btn {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  min-width: 44px;
  min-height: 44px;
  color: var(--ink);
}

.back-btn {
  margin-left: -10px;
}

.header-context {
  min-width: 0;
}

.header-title {
  font-size: var(--text-17);
  font-weight: 600;
  line-height: 1.3;
}

.header-meta {
  color: var(--muted);
  font-size: var(--text-13);
  line-height: 1.4;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.header-meta--title {
  font-size: var(--text-12);
}

.desktop-nav {
  display: none;
  align-items: center;
  gap: var(--sp-2);
}

.nav-link {
  min-height: 44px;
  min-width: 44px;
  padding: 0 12px;
  border-radius: var(--radius-md);
  color: var(--muted);
  font-size: var(--text-14);
  font-weight: 500;
}

.nav-link--active {
  color: var(--ink);
  background: rgba(var(--ink-rgb), 0.06);
  font-weight: 600;
}

@media (min-width: 768px) {
  .desktop-nav {
    display: flex;
  }
}
</style>
