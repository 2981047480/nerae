<script setup lang="ts">
import { nextTick, onBeforeUnmount, onMounted, watch } from 'vue'
import AppHeader from './components/AppHeader.vue'
import TabBar from './components/TabBar.vue'
import PreviewControl from './components/PreviewControl.vue'
import BaseSheet from './components/BaseSheet.vue'
import TodayView from './views/TodayView.vue'
import ExploreView from './views/ExploreView.vue'
import TrainingView from './views/TrainingView.vue'
import CoachReviewView from './views/CoachReviewView.vue'
import {
  closeSheet,
  consumeScrollPosition,
  isComplete,
  motionReduced,
  recordsForSheet,
  state,
  ui,
} from './state/store'
import { formatTime, setValueLabel } from './utils/format'
import { CheckCircle2 } from '@lucide/vue'

function restoreScroll(): void {
  if (typeof window === 'undefined') return
  const target = consumeScrollPosition(state.screen)
  window.scrollTo(0, target)
}

watch(
  () => state.screen,
  async () => {
    await nextTick()
    requestAnimationFrame(() => {
      requestAnimationFrame(restoreScroll)
    })
  },
)

onMounted(restoreScroll)

function applyDocumentMotion(reduced: boolean): void {
  if (typeof document === 'undefined') return
  document.documentElement.dataset.motion = reduced ? 'reduced' : 'full'
}

function applyDocumentTextScale(scale: 1 | 2): void {
  if (typeof document === 'undefined') return
  document.documentElement.dataset.textScale = scale === 2 ? '200' : '100'
}

function removeFocusGhosts(): void {
  document.querySelectorAll('.session-focus-ghost').forEach((ghost) => ghost.remove())
}

watch(
  motionReduced,
  (reduced) => {
    applyDocumentMotion(reduced)
    if (reduced) removeFocusGhosts()
  },
  { immediate: true },
)

watch(
  () => state.textScale,
  (scale) => {
    applyDocumentTextScale(scale)
  },
  { immediate: true },
)

onBeforeUnmount(() => {
  document.documentElement.dataset.motion = 'full'
  document.documentElement.dataset.textScale = '100'
})

function runFocusBridge(origin: { x: number; y: number; width: number; height: number; text: string }): void {
  if (typeof document === 'undefined' || motionReduced.value) return
  const target = document.querySelector<HTMLElement>('[data-training-title]')
  if (!target) return
  const to = target.getBoundingClientRect()
  if (!to.width || !to.height) return
  const ghost = document.createElement('div')
  ghost.className = 'session-focus-ghost'
  ghost.textContent = origin.text
  ghost.setAttribute('aria-hidden', 'true')
  ghost.style.left = `${origin.x}px`
  ghost.style.top = `${origin.y}px`
  ghost.style.width = `${origin.width}px`
  ghost.style.height = `${origin.height}px`
  ghost.style.fontSize = `${Math.max(28, Math.min(80, origin.height * 0.72))}px`
  document.body.appendChild(ghost)
  if (typeof ghost.animate !== 'function') {
    ghost.remove()
    return
  }
  const scale = Math.max(0.28, to.height / origin.height)
  const dx = to.left - origin.x
  const dy = to.top - origin.y
  const animation = ghost.animate(
    [
      { transform: 'translate(0, 0) scale(1)', opacity: 1 },
      { transform: `translate(${dx}px, ${dy}px) scale(${scale})`, opacity: 0 },
    ],
    {
      duration: 260,
      easing: 'cubic-bezier(0.2, 0.8, 0.2, 1)',
      fill: 'forwards',
    },
  )
  animation.onfinish = () => ghost.remove()
  window.setTimeout(() => ghost.remove(), 500)
}

watch(
  () => state.screen,
  (screen, previous) => {
    const origin = ui.focusOrigin
    ui.focusOrigin = null
    if (screen !== 'training') removeFocusGhosts()
    if (screen === 'training' && previous && !motionReduced.value && origin) {
      nextTick(() => {
        requestAnimationFrame(() => runFocusBridge(origin))
      })
    }
  },
)

const isTraining = () => state.screen === 'training'
const isCoach = () => state.actor === 'coach'
const isStudentShell = () => !isTraining() && !isCoach()
</script>

<template>
  <a class="skip-link" href="#main">跳到主要内容</a>
  <div
    class="app-root"
    :data-motion="motionReduced ? 'reduced' : 'full'"
    :data-focus-screen="isTraining() ? 'true' : 'false'"
  >
    <template v-if="isTraining()">
      <Transition name="focus" appear>
        <TrainingView :key="state.screen" />
      </Transition>
    </template>
    <template v-else>
      <AppHeader />
      <main id="main" tabindex="-1" :class="isStudentShell() ? 'student-main' : 'coach-main'">
        <Transition name="view" mode="out-in" appear>
          <TodayView v-if="state.screen === 'today'" key="today" />
          <ExploreView v-else-if="state.screen === 'explore'" key="explore" />
          <CoachReviewView v-else-if="state.screen === 'coachPlan'" key="coachPlan" />
        </Transition>
      </main>
      <TabBar v-if="isStudentShell() && (state.screen === 'today' || state.screen === 'explore')" />
    </template>

    <PreviewControl :dark="isTraining()" />

    <BaseSheet :open="ui.sheet === 'records'" title="本次记录" @close="closeSheet">
      <div class="record-sheet">
        <div class="record-sheet-head">
          <p class="record-sheet-date">{{ state.session.dateLabel }} · {{ state.session.dayTitle }}</p>
          <p v-if="isComplete" class="record-sheet-status">
            <CheckCircle2 :size="15" aria-hidden="true" />
            今天已完成
          </p>
        </div>
        <ul class="record-sheet-list">
          <li v-for="group in recordsForSheet" :key="group.id" class="record-sheet-group">
            <div class="record-sheet-title-row">
              <h3 class="record-sheet-title">{{ group.name }}</h3>
              <span class="record-sheet-count num">{{ group.progress.done }} / {{ group.progress.total }} 组</span>
            </div>
            <ul v-if="group.sets.length" class="record-sheet-sets">
              <li v-for="set in group.sets" :key="set.id" class="record-sheet-set">
                <span class="num">第 {{ set.index }} 组</span>
                <span class="num">{{ setValueLabel(group.kind, set) }}</span>
                <span class="record-sheet-time num">{{ set.completedAt ? formatTime(set.completedAt) : '' }}</span>
              </li>
            </ul>
            <p v-else class="record-sheet-empty">没有完成的组</p>
          </li>
        </ul>
        <p v-if="state.session.completedAt" class="record-sheet-foot num">
          最后一组完成于 {{ formatTime(state.session.completedAt) }}
        </p>
      </div>
    </BaseSheet>
  </div>
</template>

<style scoped>
.skip-link {
  position: absolute;
  left: 12px;
  top: 0;
  z-index: 200;
  padding: 10px 14px;
  border-radius: var(--radius-md);
  background: var(--ink);
  color: #fff;
  font-size: var(--text-13);
  transform: translateY(-100%);
  transition: transform 140ms var(--ease-out);
}

.skip-link:focus {
  transform: translateY(12px);
}

.coach-main {
  padding-bottom: var(--sp-6);
}

.record-sheet-head {
  display: flex;
  align-items: baseline;
  justify-content: space-between;
  gap: var(--sp-3);
  padding-bottom: var(--sp-3);
  border-bottom: 1px solid var(--line);
}

.record-sheet-date {
  color: var(--muted);
  font-size: var(--text-13);
}

.record-sheet-status {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  color: var(--positive);
  font-size: var(--text-13);
}

.record-sheet-list {
  margin-top: var(--sp-2);
}

.record-sheet-group {
  padding: var(--sp-3) 0;
  border-bottom: 1px solid var(--line);
}

.record-sheet-title-row {
  display: flex;
  align-items: baseline;
  justify-content: space-between;
  gap: var(--sp-3);
}

.record-sheet-title {
  font-size: var(--text-15);
  font-weight: 600;
}

.record-sheet-count {
  color: var(--muted);
  font-size: var(--text-12);
}

.record-sheet-sets {
  margin-top: 4px;
}

.record-sheet-set {
  display: grid;
  grid-template-columns: 64px minmax(0, 1fr) auto;
  gap: var(--sp-3);
  padding: 4px 0;
  color: var(--muted);
  font-size: var(--text-13);
}

.record-sheet-set span:nth-child(2) {
  color: var(--ink);
  font-size: var(--text-14);
}

.record-sheet-time {
  color: var(--muted);
  font-size: var(--text-12);
}

.record-sheet-empty {
  margin-top: 4px;
  color: var(--muted);
  font-size: var(--text-13);
}

.record-sheet-foot {
  margin-top: var(--sp-4);
  color: var(--muted);
  font-size: var(--text-12);
}
</style>
