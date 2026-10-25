<script setup lang="ts">
import { computed } from 'vue'
import { ArrowRight, CalendarDays, FileText, MessageCircle } from '@lucide/vue'
import {
  goToConversation,
  goToIncomingPlan,
  goToMyPlan,
  goToRecords,
  state,
} from '../state/store'

const plan = computed(() => state.student.plan)
const incoming = computed(() => state.student.incoming)
const version = computed(() => state.student.planVersion)
const hasNewVersion = computed(() => incoming.value?.status === 'new' && incoming.value.version > version.value)
const totalExercises = computed(() => plan.value.days.reduce((sum, day) => sum + day.exercises.length, 0))

function talkToCoach(): void {
  goToConversation('conv-gu-yan')
}
</script>

<template>
  <div class="screen screen--narrow student-plan">
    <header class="screen-head">
      <div class="screen-heading">
        <p class="screen-kicker">来自顾言 · 教练交付</p>
        <h1 class="screen-title">{{ plan.title }}</h1>
        <p class="screen-sub num">
          现用版本 {{ version }} · {{ plan.days.length }} 个训练日 · {{ totalExercises }} 个动作
        </p>
      </div>
      <div class="screen-tools">
        <button type="button" class="link-btn" @click="goToMyPlan">我的内容</button>
      </div>
    </header>

    <div v-if="hasNewVersion" class="banner banner--warning" role="status">
      <div>
        <p class="banner-title">收到版本 {{ incoming?.version }}，尚未采用</p>
        <p class="banner-body">
          现用安排仍是版本 {{ version }}。教练的新版本不会自动替换；你可以先看具体变化再决定。
        </p>
        <button type="button" class="btn btn--sm banner-action" @click="goToIncomingPlan()">
          查看新版本
          <ArrowRight :size="14" aria-hidden="true" />
        </button>
      </div>
    </div>

    <section class="group" aria-labelledby="plan-days-title">
      <header class="group-head">
        <h2 id="plan-days-title" class="group-title">训练日</h2>
        <span class="group-meta">版本 {{ version }} · 只读</span>
      </header>
      <div v-for="day in plan.days" :key="day.id" class="plan-day">
        <h3 class="day-title">{{ day.title }}</h3>
        <ol class="day-exercises">
          <li v-for="exercise in day.exercises" :key="exercise.id" class="day-exercise">
            <span class="exercise-name">{{ exercise.name }}</span>
            <span class="exercise-scheme num">{{ exercise.sets }} 组 × {{ exercise.target }} · 休息 {{ exercise.restSeconds }} 秒</span>
            <span v-if="exercise.note" class="exercise-note">{{ exercise.note }}</span>
          </li>
        </ol>
      </div>
    </section>

    <section class="group" aria-labelledby="schedule-title">
      <header class="group-head">
        <h2 id="schedule-title" class="group-title">未来安排</h2>
        <span class="group-meta">固定日期，不自动顺延</span>
      </header>
      <ul class="schedule-list">
        <li v-for="slot in state.student.schedule" :key="slot.date">
          <span class="schedule-date">{{ slot.dateLabel }}</span>
          <span class="schedule-day">{{ slot.dayTitle }}</span>
          <span class="schedule-version num">版本 {{ slot.planVersion }}</span>
        </li>
      </ul>
      <p class="schedule-note">更改安排只影响未来；已完成的训练记录保持原样。</p>
    </section>

    <section class="plan-links" aria-labelledby="links-title">
      <h2 id="links-title" class="group-title">相关</h2>
      <button type="button" class="plan-link" @click="goToRecords">
        <span><FileText :size="15" aria-hidden="true" /> 训练记录与反馈</span>
        <ArrowRight :size="15" aria-hidden="true" />
      </button>
      <button type="button" class="plan-link" @click="talkToCoach">
        <span><MessageCircle :size="15" aria-hidden="true" /> 给教练发消息</span>
        <ArrowRight :size="15" aria-hidden="true" />
      </button>
      <button type="button" class="plan-link" @click="goToMyPlan">
        <span><CalendarDays :size="15" aria-hidden="true" /> 我自己的内容 · 版本 {{ state.ownPlan.version }}</span>
        <ArrowRight :size="15" aria-hidden="true" />
      </button>
    </section>

    <p class="plan-boundary">这里是教练交付、你正在使用的安排；与「我的内容」分开，编辑不会覆盖教练计划。</p>
  </div>
</template>

<style scoped>
.student-plan {
  padding-bottom: calc(96px + env(safe-area-inset-bottom));
}

.banner-action {
  margin-top: var(--sp-2);
}

.plan-day {
  padding: var(--sp-4) 0;
  border-top: 1px solid var(--line);
  border-bottom: 1px solid var(--line);
}

.plan-day + .plan-day {
  border-top: 0;
}

.day-title {
  font-size: var(--text-16);
  font-weight: 600;
}

.day-exercises {
  margin-top: var(--sp-2);
}

.day-exercise {
  display: grid;
  grid-template-columns: minmax(0, 1fr) auto;
  gap: 2px var(--sp-3);
  padding: var(--sp-2) 0;
  border-top: 1px solid var(--line);
}

.exercise-name {
  font-size: var(--text-14);
}

.exercise-scheme {
  color: var(--muted);
  font-size: var(--text-12);
  text-align: right;
}

.exercise-note {
  grid-column: 1 / -1;
  color: var(--muted);
  font-size: var(--text-12);
  line-height: 1.55;
}

.schedule-list {
  margin-top: var(--sp-2);
}

.schedule-list li {
  display: grid;
  grid-template-columns: 112px minmax(0, 1fr) auto;
  gap: var(--sp-2);
  align-items: baseline;
  padding: 7px 0;
  border-bottom: 1px solid var(--line);
  font-size: var(--text-13);
}

.schedule-date {
  color: var(--muted);
  font-size: var(--text-12);
}

.schedule-version {
  color: var(--muted);
  font-size: var(--text-12);
}

.schedule-note {
  margin-top: var(--sp-3);
  color: var(--muted);
  font-size: var(--text-12);
  line-height: 1.55;
}

.plan-links {
  margin-top: var(--sp-6);
}

.plan-link {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--sp-3);
  width: 100%;
  min-height: 48px;
  padding: 8px 0;
  border-bottom: 1px solid var(--line);
  text-align: left;
  font-size: var(--text-14);
}

.plan-link span {
  display: inline-flex;
  align-items: center;
  gap: 6px;
}

.plan-boundary {
  margin-top: var(--sp-5);
  color: var(--muted);
  font-size: var(--text-12);
  line-height: 1.6;
}
</style>
