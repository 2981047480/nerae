<script setup lang="ts">
import { computed, ref } from 'vue'
import { ArrowRight, Check, CheckCircle2, ChevronRight, Dumbbell } from '@lucide/vue'
import PhotoFrame from '../components/PhotoFrame.vue'
import {
  enterTraining,
  openSheet,
  sessionGroups,
  todayFacts,
  ui,
  weekSummary,
} from '../state/store'
import { NEXT_SESSION_LABEL, PREVIEW_DATE_LABEL, PREVIEW_WEEK_LABEL } from '../data/sample'
import { formatWeight } from '../utils/format'

const facts = todayFacts
const groups = sessionGroups

const heroTitle = ref<HTMLElement | null>(null)

const currentExerciseName = computed(() => facts.value.currentExercise)
const nextSetNumber = computed(() => facts.value.nextSet)

const equipmentText = computed(() => {
  const labels = new Set<string>()
  for (const group of sessionGroups.value) {
    for (const part of group.exercise.equipment.split(' · ')) labels.add(part)
  }
  return [...labels].join(' · ')
})

function startTraining(): void {
  const rect = heroTitle.value?.getBoundingClientRect()
  if (rect && typeof rect.width === 'number' && rect.width > 0) {
    ui.focusOrigin = {
      x: rect.x,
      y: rect.y,
      width: rect.width,
      height: rect.height,
      text: facts.value.title,
    }
  } else {
    ui.focusOrigin = null
  }
  enterTraining()
}

const week = computed(() => weekSummary.value)

function setsDots(progress: { done: number; total: number }) {
  return Array.from({ length: progress.total }, (_, index) => ({
    key: index,
    state:
      index < progress.done
        ? 'done'
        : index === progress.done
          ? 'current'
          : 'pending',
  }))
}
</script>

<template>
  <div class="today">
    <div class="today-grid">
      <div class="today-main">
        <article class="hero" :class="{ 'hero--complete': facts.complete }">
          <div class="hero-media">
            <PhotoFrame
              src="/assets/hero-today.webp"
              alt="训练者在健身房完成上肢推举动作"
              eager
              fallback-label="训练图片暂不可用"
            />
            <div class="hero-scrim" aria-hidden="true" />
            <div class="hero-content">
              <p class="hero-kicker">今天 · {{ PREVIEW_DATE_LABEL }}</p>
              <h1 ref="heroTitle" class="hero-title" data-today-session-title>{{ facts.title }}</h1>
              <p class="hero-meta num">
                约 32 分钟 · 4 个动作 · {{ facts.total }} 组
              </p>

              <template v-if="!facts.complete">
                <div class="hero-progress">
                  <div class="progress-track" aria-hidden="true">
                    <span class="progress-fill" :style="{ width: `${Math.max(4, facts.percent)}%` }" />
                  </div>
                  <p class="hero-progress-text num">
                    已完成 {{ facts.done }} / {{ facts.total }} 组
                  </p>
                </div>
                <button class="btn btn--accent btn--block hero-cta" type="button" @click="startTraining">
                  <span class="hero-cta-label">
                    继续
                    <span class="hero-cta-name">{{ currentExerciseName }}</span>
                    <span class="hero-cta-set num">
                      <span class="hero-cta-nowrap">· 第 {{ nextSetNumber }} 组</span>
                    </span>
                  </span>
                  <ArrowRight :size="18" aria-hidden="true" />
                </button>
              </template>

              <template v-else>
                <div class="hero-complete-status">
                  <CheckCircle2 :size="18" aria-hidden="true" />
                  <span>今天已完成</span>
                </div>
                <p class="hero-next num">下次训练 · {{ NEXT_SESSION_LABEL }}</p>
                <button class="btn btn--ghost hero-secondary" type="button" @click="openSheet('records')">
                  查看本次记录
                  <ChevronRight :size="16" aria-hidden="true" />
                </button>
              </template>
            </div>
          </div>
        </article>

        <section class="arrangement" aria-labelledby="arrangement-title">
          <header class="section-head">
            <h2 id="arrangement-title" class="section-title">本次安排</h2>
            <span class="section-meta num">4 个动作 · {{ facts.total }} 组</span>
          </header>
          <ol class="exercise-list">
            <li
              v-for="(group, index) in groups"
              :key="group.exercise.id"
              class="exercise-row"
              :class="{ 'exercise-row--current': group.progress.done < group.progress.total && facts.currentExercise === group.exercise.name && !facts.complete }"
            >
              <span class="exercise-index num">{{ String(index + 1).padStart(2, '0') }}</span>
              <div class="exercise-body">
                <div class="exercise-line">
                  <h3 class="exercise-name">{{ group.exercise.name }}</h3>
                  <span class="exercise-progress num" :aria-label="`${group.progress.done} / ${group.progress.total} 组已记录`">
                    {{ group.progress.done }} / {{ group.progress.total }} 组
                  </span>
                </div>
                <p class="exercise-meta num">
                  {{ group.exercise.sets.length }} 组 × {{ group.exercise.targetText }}
                  <span class="exercise-equipment">· {{ group.exercise.equipment }}</span>
                </p>
                <div class="exercise-bottom">
                  <span class="dots" aria-hidden="true">
                    <span
                      v-for="dot in setsDots(group.progress)"
                      :key="dot.key"
                      class="dot"
                      :class="`dot--${dot.state}`"
                    />
                  </span>
                  <span
                    v-if="group.progress.done < group.progress.total && facts.currentExercise === group.exercise.name && !facts.complete"
                    class="current-label"
                  >
                    <Dumbbell :size="12" aria-hidden="true" />
                    进行中
                  </span>
                  <span v-else-if="group.progress.done === group.progress.total" class="done-label">
                    <Check :size="12" aria-hidden="true" />
                    已完成
                  </span>
                  <span v-else class="pending-label">待开始</span>
                </div>
                <p
                  v-if="facts.currentExercise === group.exercise.name && !facts.complete && group.exercise.lastRef"
                  class="exercise-reference num"
                >
                  上次 {{ group.exercise.lastRef.dateLabel }} ·
                  <template v-if="group.exercise.kind === 'duration'">
                    {{ group.exercise.lastRef.seconds }} 秒
                  </template>
                  <template v-else>
                    {{ formatWeight(group.exercise.lastRef.weightKg ?? 0) }} kg ×
                    {{ group.exercise.lastRef.reps }} 次{{ group.exercise.kind === 'perSide' ? '/侧' : '' }}
                  </template>
                </p>
              </div>
            </li>
          </ol>
        </section>
      </div>

      <aside class="today-side">
        <section class="side-section" aria-labelledby="week-title">
          <header class="section-head">
            <h2 id="week-title" class="section-title">本周</h2>
            <span class="section-meta num">{{ PREVIEW_WEEK_LABEL }}</span>
          </header>
          <ol class="week-strip">
            <li
              v-for="day in week.days"
              :key="day.key"
              class="week-day"
              :class="{
                'week-day--today': day.state === 'today',
                'week-day--done': day.state === 'done',
                'week-day--training': day.training,
              }"
            >
              <span class="week-label">{{ day.label }}</span>
              <span class="week-date num">{{ day.date }}</span>
              <span class="week-dot" aria-hidden="true" />
            </li>
          </ol>
          <p class="week-note num">
            {{ week.doneCount }} 次训练已完成
            <span v-if="!facts.complete">· 今天进行中</span>
            <span v-else>· 下次 {{ NEXT_SESSION_LABEL }}</span>
          </p>
        </section>

        <section class="side-section side-facts" aria-labelledby="facts-title">
          <h2 id="facts-title" class="section-title">本次训练</h2>
          <dl class="fact-list">
            <div class="fact-row">
              <dt>器械</dt>
              <dd>{{ equipmentText }}</dd>
            </div>
            <div class="fact-row">
              <dt>组间休息</dt>
              <dd class="num">60–90 秒</dd>
            </div>
            <div class="fact-row">
              <dt>上次训练</dt>
              <dd class="num">10月22日 · 上肢力量</dd>
            </div>
          </dl>
          <p class="side-source">训练内容来自我的计划 · 版本 {{ facts.version }}</p>
        </section>
      </aside>
    </div>
  </div>
</template>

<style scoped>
.today {
  width: 100%;
  max-width: var(--container);
  margin-inline: auto;
  padding: 4px var(--gutter) var(--sp-8);
}

.today-grid {
  display: grid;
  gap: var(--sp-7);
}

.hero {
  position: relative;
}

.hero-media {
  position: relative;
  height: clamp(340px, min(112vw, 72dvh), 560px);
  overflow: hidden;
  border-radius: var(--radius-lg);
  background: var(--ink);
}

.hero-media :deep(.photo) {
  height: 100%;
}

.hero-media :deep(img) {
  object-position: 50% 32%;
}

.hero--complete .hero-media :deep(img) {
  filter: saturate(0.92);
}

.hero-scrim {
  position: absolute;
  inset: 0;
  background:
    linear-gradient(180deg, rgba(8, 18, 23, 0.06) 30%, rgba(8, 18, 23, 0.76) 100%),
    linear-gradient(90deg, rgba(8, 18, 23, 0.28) 0%, rgba(8, 18, 23, 0) 60%);
}

.hero-content {
  position: absolute;
  left: 0;
  right: 0;
  bottom: 0;
  padding: 22px;
  color: var(--focus-text);
}

.hero-kicker {
  color: rgba(241, 240, 234, 0.82);
  font-size: var(--text-13);
  line-height: 1.5;
  letter-spacing: 0.02em;
}

.hero-title {
  margin-top: 4px;
  color: var(--focus-text);
  font-size: clamp(2rem, 8.6vw, 2.5rem);
  font-weight: 600;
  line-height: 1.14;
  letter-spacing: -0.02em;
}

.hero-meta {
  margin-top: 6px;
  color: rgba(241, 240, 234, 0.78);
  font-size: var(--text-13);
  line-height: 1.5;
}

.hero-progress {
  margin-top: 18px;
}

.progress-track {
  height: 2px;
  border-radius: 2px;
  background: rgba(241, 240, 234, 0.3);
  overflow: hidden;
}

.progress-fill {
  display: block;
  height: 100%;
  background: var(--accent);
  border-radius: 2px;
  transition: width 300ms var(--ease-out);
}

.hero-progress-text {
  margin-top: 8px;
  color: rgba(241, 240, 234, 0.86);
  font-size: var(--text-13);
  line-height: 1.5;
}

.hero-cta {
  margin-top: 16px;
}

.hero-cta-label {
  display: inline-flex;
  align-items: baseline;
  justify-content: center;
  flex-wrap: wrap;
  gap: 0 4px;
  line-height: 1.25;
}

.hero-cta-nowrap {
  white-space: nowrap;
}

.hero-complete-status {
  display: inline-flex;
  align-items: center;
  gap: 7px;
  margin-top: 18px;
  color: var(--focus-text);
  font-size: var(--text-15);
  font-weight: 600;
}

.hero-next {
  margin-top: 6px;
  color: rgba(241, 240, 234, 0.82);
  font-size: var(--text-13);
  line-height: 1.5;
}

.hero-secondary {
  margin-top: 14px;
  min-height: 44px;
  padding: 0 14px;
  border-color: rgba(241, 240, 234, 0.45);
  color: var(--focus-text);
  background: rgba(10, 24, 30, 0.18);
}

.hero-secondary:hover {
  background: rgba(10, 24, 30, 0.32);
}

.arrangement {
  margin-top: var(--sp-7);
}

.section-head {
  display: flex;
  align-items: baseline;
  justify-content: space-between;
  gap: var(--sp-3);
  margin-bottom: var(--sp-3);
}

.section-title {
  font-size: var(--text-17);
  font-weight: 600;
}

.section-meta {
  color: var(--muted);
  font-size: var(--text-13);
  white-space: nowrap;
}

.exercise-list {
  border-top: 1px solid var(--line);
}

.exercise-row {
  display: flex;
  gap: var(--sp-4);
  padding: var(--sp-4) 0;
  border-bottom: 1px solid var(--line);
}

.exercise-row--current {
  position: relative;
}

.exercise-row--current::before {
  content: '';
  position: absolute;
  left: -10px;
  top: 18px;
  width: 2px;
  height: 24px;
  background: var(--accent);
}

.exercise-index {
  width: 24px;
  flex: none;
  padding-top: 2px;
  color: var(--muted);
  font-size: var(--text-12);
  line-height: 1.5;
}

.exercise-body {
  min-width: 0;
  flex: 1;
}

.exercise-line {
  display: flex;
  align-items: baseline;
  justify-content: space-between;
  gap: var(--sp-3);
}

.exercise-name {
  font-size: var(--text-17);
  font-weight: 600;
  line-height: 1.4;
}

.exercise-progress {
  flex: none;
  color: var(--muted);
  font-size: var(--text-12);
}

.exercise-meta {
  margin-top: 2px;
  color: var(--muted);
  font-size: var(--text-13);
  line-height: 1.5;
}

.exercise-equipment {
  white-space: nowrap;
}

.exercise-bottom {
  display: flex;
  align-items: center;
  gap: var(--sp-3);
  margin-top: 8px;
}

.dots {
  display: inline-flex;
  align-items: center;
  gap: 5px;
}

.dot {
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: var(--line-strong);
}

.dot--done {
  background: var(--ink);
}

.dot--current {
  width: 8px;
  height: 8px;
  background: transparent;
  border: 1.5px solid var(--accent);
}

.current-label,
.done-label,
.pending-label {
  display: inline-flex;
  align-items: center;
  gap: 3px;
  font-size: var(--text-12);
  line-height: 1.4;
}

.current-label {
  color: var(--ink);
  font-weight: 600;
}

.done-label {
  color: var(--positive);
}

.pending-label {
  color: var(--muted);
}

.exercise-reference {
  margin-top: 6px;
  color: var(--muted);
  font-size: var(--text-12);
  line-height: 1.5;
}

.today-side {
  display: grid;
  gap: var(--sp-6);
  align-content: start;
}

.side-section {
  padding-top: var(--sp-4);
  border-top: 1px solid var(--line);
}

.side-facts {
  padding-top: var(--sp-4);
}

.week-strip {
  display: grid;
  grid-template-columns: repeat(7, 1fr);
  gap: 2px;
}

.week-day {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 2px;
  padding: 8px 0 6px;
  border-bottom: 1px solid transparent;
}

.week-day--today {
  border-bottom-color: var(--accent);
}

.week-label {
  color: var(--muted);
  font-size: var(--text-12);
  line-height: 1.3;
  white-space: nowrap;
}

.week-date {
  color: var(--muted);
  font-size: var(--text-14);
  line-height: 1.4;
}

.week-day--done .week-label,
.week-day--done .week-date,
.week-day--today .week-label,
.week-day--today .week-date {
  color: var(--ink);
}

.week-day--today .week-date {
  font-weight: 600;
}

.week-dot {
  width: 4px;
  height: 4px;
  border-radius: 50%;
  margin-top: 2px;
  background: transparent;
}

.week-day--training .week-dot {
  background: var(--ink);
}

.week-day--today .week-dot {
  background: var(--accent);
}

.week-note {
  margin-top: 10px;
  color: var(--muted);
  font-size: var(--text-13);
  line-height: 1.5;
}

.fact-list {
  margin-top: 2px;
}

.fact-row {
  display: grid;
  grid-template-columns: 68px minmax(0, 1fr);
  align-items: baseline;
  gap: var(--sp-3);
  padding: 9px 0;
  border-bottom: 1px solid var(--line);
}

.fact-row dt {
  color: var(--muted);
  font-size: var(--text-13);
}

.fact-row dd {
  margin: 0;
  color: var(--ink);
  font-size: var(--text-14);
  line-height: 1.55;
  text-align: left;
  overflow-wrap: anywhere;
}

.side-source {
  margin-top: var(--sp-4);
  color: var(--muted);
  font-size: var(--text-12);
  line-height: 1.5;
}

@media (min-width: 768px) {
  .today {
    padding-top: var(--sp-2);
    padding-bottom: var(--sp-10);
  }

  .today-grid {
    grid-template-columns: minmax(0, 1.62fr) minmax(270px, 0.82fr);
    gap: var(--sp-9);
    align-items: start;
  }

  .hero-media {
    height: clamp(480px, 46vw, 580px);
  }

  .hero-content {
    padding: 32px;
  }

  .hero-title {
    font-size: clamp(2.375rem, 3.6vw, 2.875rem);
  }

  .hero-cta {
    width: auto;
    min-width: 220px;
    padding-inline: 28px;
  }

  .today-side {
    padding-top: 4px;
  }

  .week-label {
    font-size: var(--text-12);
  }
}

@media (min-width: 1100px) {
  .hero-media {
    height: 600px;
  }
}
</style>
