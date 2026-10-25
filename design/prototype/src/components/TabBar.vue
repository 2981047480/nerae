<script setup lang="ts">
import { computed } from 'vue'
import {
  CalendarDays,
  ChartNoAxesCombined,
  Compass,
  ListChecks,
  MessageCircle,
  ScrollText,
  Users,
} from '@lucide/vue'
import {
  goToCoachServices,
  goToCoachStudents,
  goToCoachTodo,
  goToExplore,
  goToMessages,
  goToRecords,
  goToToday,
  state,
} from '../state/store'

const isCoach = computed(() => state.actor === 'coach')

const studentRoot = computed(() => {
  if (state.screen === 'today') return 'today'
  if (state.screen === 'explore') return 'explore'
  if (state.screen === 'records') return 'records'
  if (state.screen === 'messages') return 'messages'
  return ''
})
const coachRoot = computed(() => {
  if (state.screen === 'coachTodo') return 'todo'
  if (state.screen === 'coachStudents') return 'students'
  if (state.screen === 'messages') return 'messages'
  if (state.screen === 'coachServices') return 'services'
  return ''
})

const show = computed(() =>
  isCoach.value
    ? ['coachTodo', 'coachStudents', 'messages', 'coachServices', 'coachPlan'].includes(state.screen)
    : ['today', 'explore', 'records', 'messages'].includes(state.screen),
)
</script>

<template>
  <nav v-if="show" class="tabbar" :aria-label="isCoach ? '教练主导航' : '学员主导航'">
    <template v-if="isCoach">
      <button type="button" class="tab" :class="{ 'tab--active': coachRoot === 'todo' }" :aria-current="coachRoot === 'todo' ? 'page' : undefined" @click="goToCoachTodo">
        <ListChecks :size="21" :stroke-width="1.8" aria-hidden="true" />
        <span>待办</span>
      </button>
      <button type="button" class="tab" :class="{ 'tab--active': coachRoot === 'students' }" :aria-current="coachRoot === 'students' ? 'page' : undefined" @click="goToCoachStudents">
        <Users :size="21" :stroke-width="1.8" aria-hidden="true" />
        <span>学员</span>
      </button>
      <button type="button" class="tab" :class="{ 'tab--active': coachRoot === 'messages' }" :aria-current="coachRoot === 'messages' ? 'page' : undefined" @click="goToMessages">
        <MessageCircle :size="21" :stroke-width="1.8" aria-hidden="true" />
        <span>消息</span>
      </button>
      <button type="button" class="tab" :class="{ 'tab--active': coachRoot === 'services' }" :aria-current="coachRoot === 'services' ? 'page' : undefined" @click="goToCoachServices">
        <ScrollText :size="21" :stroke-width="1.8" aria-hidden="true" />
        <span>服务</span>
      </button>
    </template>
    <template v-else>
      <button type="button" class="tab" :class="{ 'tab--active': studentRoot === 'today' }" :aria-current="studentRoot === 'today' ? 'page' : undefined" @click="goToToday">
        <CalendarDays :size="21" :stroke-width="1.8" aria-hidden="true" />
        <span>今天</span>
      </button>
      <button type="button" class="tab" :class="{ 'tab--active': studentRoot === 'explore' }" :aria-current="studentRoot === 'explore' ? 'page' : undefined" @click="goToExplore">
        <Compass :size="21" :stroke-width="1.8" aria-hidden="true" />
        <span>发现</span>
      </button>
      <button type="button" class="tab" :class="{ 'tab--active': studentRoot === 'records' }" :aria-current="studentRoot === 'records' ? 'page' : undefined" @click="goToRecords">
        <ChartNoAxesCombined :size="21" :stroke-width="1.8" aria-hidden="true" />
        <span>记录</span>
      </button>
      <button type="button" class="tab" :class="{ 'tab--active': studentRoot === 'messages' }" :aria-current="studentRoot === 'messages' ? 'page' : undefined" @click="goToMessages">
        <MessageCircle :size="21" :stroke-width="1.8" aria-hidden="true" />
        <span>消息</span>
      </button>
    </template>
  </nav>
</template>

<style scoped>
.tabbar {
  position: fixed;
  z-index: 30;
  left: 0;
  right: 0;
  bottom: 0;
  display: grid;
  grid-template-columns: repeat(4, 1fr);
  border-top: 1px solid var(--line);
  background: var(--canvas);
  padding-bottom: env(safe-area-inset-bottom);
}

.tab {
  display: inline-flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 3px;
  min-height: 60px;
  min-width: 44px;
  color: var(--muted);
  font-size: var(--text-12);
  line-height: 1.3;
  transition: color 140ms var(--ease-out);
}

.tab--active {
  color: var(--ink);
  font-weight: 600;
}

@media (min-width: 768px) {
  .tabbar {
    display: none;
  }
}
</style>
