<script setup lang="ts">
import { computed } from 'vue'
import { ChevronRight, EyeOff, Users } from '@lucide/vue'
import { healthAuthorizedFor, openStudent, planWorkFor, state, visibleStudents } from '../state/store'

const students = visibleStudents
const restriction = computed(() => {
  if (state.preview.coachStudents === 'unauthorized') {
    return '健康内容未授权时，不显示健康记录、饮食观察或旧的健康摘要；计划与服务记录仍可见。'
  }
  if (state.preview.coachStudents === 'ended') {
    return '服务关系已结束时，历史内容只读；不能继续编辑计划或查看健康内容。'
  }
  if (state.preview.coachStudents === 'stale') {
    return '这里展示最近一次可见记录；更晚的数据尚未同步，不会用旧数据伪装成最新。'
  }
  return ''
})

function relationLabel(status: string): string {
  if (status === 'active') return '在读'
  if (status === 'pending') return '待确认'
  return '已结束'
}
</script>

<template>
  <div class="screen screen--wide coach-students">
    <header class="screen-head">
      <div class="screen-heading">
        <p class="screen-kicker">顾言 · 教练</p>
        <h1 class="screen-title">学员</h1>
        <p class="screen-sub num">{{ students.length }} 位学员 · 健康内容按授权显示</p>
      </div>
    </header>

    <div v-if="restriction" class="banner" :class="state.preview.coachStudents === 'ended' ? 'banner--warning' : ''" role="status">
      <div>
        <p class="banner-title">
          {{ state.preview.coachStudents === 'unauthorized' ? '健康内容不可见' : state.preview.coachStudents === 'ended' ? '服务关系已结束' : '数据可能不是最新' }}
          <EyeOff v-if="state.preview.coachStudents === 'unauthorized'" :size="14" aria-hidden="true" />
        </p>
        <p class="banner-body">{{ restriction }}</p>
      </div>
    </div>

    <ul v-if="students.length" class="student-list">
      <li v-for="student in students" :key="student.id">
        <button type="button" class="student-row" @click="openStudent(student.id)">
          <span class="avatar avatar--muted">{{ student.name.slice(0, 1) }}</span>
          <span class="student-main">
            <span class="student-head">
              <span class="student-name">{{ student.name }}</span>
              <span class="pill" :class="student.relationStatus === 'active' ? 'pill--positive' : student.relationStatus === 'pending' ? 'pill--warning' : 'pill--neutral'">
                {{ relationLabel(student.relationStatus) }}
              </span>
            </span>
            <span class="student-service">
              {{ student.packageName }}
              <template v-if="student.planVersion">
                · 现用 v{{ student.planVersion }}
                <template v-if="student.deliveredVersion && student.deliveredVersion !== student.planVersion">
                  · 已交付 v{{ student.deliveredVersion }}
                </template>
              </template>
              <template v-else> · 服务未建立</template>
            </span>
            <span class="student-fact">
              {{ student.lastFactHealth && !healthAuthorizedFor(student) ? '健康内容未授权，最近训练不可见' : student.lastFact }}
              <span v-if="student.relationStatus === 'pending'" class="student-pending"> · 还没有可查看的计划与记录</span>
            </span>
            <span v-if="planWorkFor(student)" class="student-work">
              {{ planWorkFor(student).startsWith('已交付') ? '进展：' : '待处理：' }}{{ student.pendingWorkHealth && !healthAuthorizedFor(student) ? '健康相关内容未授权' : planWorkFor(student) }}
            </span>
          </span>
          <span class="student-side">
            <span class="student-health">
              {{ healthAuthorizedFor(student) ? '健康已授权' : '健康不可见' }}
            </span>
            <ChevronRight :size="18" aria-hidden="true" />
          </span>
        </button>
      </li>
    </ul>

    <div v-else class="empty-state">
      <Users :size="28" aria-hidden="true" class="empty-icon" />
      <h2 class="empty-title">还没有学员</h2>
      <p class="empty-text">服务关系建立后，学员会出现在这里。</p>
    </div>

    <p class="students-note">
      返回学员列表时保留当前筛选与位置。
    </p>
  </div>
</template>

<style scoped>
.coach-students {
  padding-bottom: calc(96px + env(safe-area-inset-bottom));
}

.student-list {
  margin-top: var(--sp-4);
  border-top: 1px solid var(--line);
}

.student-row {
  display: flex;
  align-items: flex-start;
  gap: var(--sp-3);
  width: 100%;
  padding: var(--sp-4) 0;
  border-bottom: 1px solid var(--line);
  text-align: left;
}

.student-main {
  display: flex;
  flex-direction: column;
  gap: 2px;
  min-width: 0;
  flex: 1;
}

.student-head {
  display: flex;
  align-items: center;
  gap: var(--sp-2);
}

.student-name {
  font-size: var(--text-16);
  font-weight: 600;
}

.student-service {
  color: var(--muted);
  font-size: var(--text-13);
}

.student-fact {
  color: var(--muted);
  font-size: var(--text-13);
  line-height: 1.55;
}

.student-pending {
  color: var(--warning);
}

.student-work {
  margin-top: 2px;
  color: var(--warning);
  font-size: var(--text-12);
  line-height: 1.5;
}

.student-side {
  display: flex;
  flex-direction: column;
  align-items: flex-end;
  gap: var(--sp-2);
  flex: none;
  color: var(--muted);
}

.student-health {
  font-size: var(--text-12);
  white-space: nowrap;
}

.students-note {
  margin-top: var(--sp-5);
  color: var(--muted);
  font-size: var(--text-12);
  line-height: 1.6;
}

@media (min-width: 768px) {
  .student-row:hover {
    background: rgba(var(--ink-rgb), 0.02);
  }
}
</style>
