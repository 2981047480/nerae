<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, watch } from 'vue'
import AppHeader from './components/AppHeader.vue'
import TabBar from './components/TabBar.vue'
import PreviewControl from './components/PreviewControl.vue'
import BaseSheet from './components/BaseSheet.vue'
import TodayView from './views/TodayView.vue'
import ExploreView from './views/ExploreView.vue'
import PlanDetailView from './views/PlanDetailView.vue'
import TrainingView from './views/TrainingView.vue'
import RecordsView from './views/RecordsView.vue'
import MessagesView from './views/MessagesView.vue'
import IncomingPlanView from './views/IncomingPlanView.vue'
import MyPlanView from './views/MyPlanView.vue'
import StudentPlanView from './views/StudentPlanView.vue'
import AiAssistView from './views/AiAssistView.vue'
import DietView from './views/DietView.vue'
import CoachDetailView from './views/CoachDetailView.vue'
import CoachTodoView from './views/CoachTodoView.vue'
import CoachStudentsView from './views/CoachStudentsView.vue'
import CoachStudentDetailView from './views/CoachStudentDetailView.vue'
import CoachServicesView from './views/CoachServicesView.vue'
import CoachReviewView from './views/CoachReviewView.vue'
import {
  closeSheet,
  consumeScrollPosition,
  isComplete,
  motionReduced,
  recordsForSheet,
  state,
  ui,
  cancelAccess,
  completeLocalSignIn,
  confirmAdultAccess,
  declareMinor,
} from './state/store'
import { formatTime, setValueLabel } from './utils/format'
import { CheckCircle2 } from '@lucide/vue'

const viewMap = {
  today: TodayView,
  explore: ExploreView,
  planDetail: PlanDetailView,
  records: RecordsView,
  messages: MessagesView,
  incomingPlan: IncomingPlanView,
  myPlan: MyPlanView,
  studentPlan: StudentPlanView,
  aiAssist: AiAssistView,
  diet: DietView,
  coachDetail: CoachDetailView,
  coachTodo: CoachTodoView,
  coachStudents: CoachStudentsView,
  coachStudentDetail: CoachStudentDetailView,
  coachServices: CoachServicesView,
  coachPlan: CoachReviewView,
} as const

const currentView = computed(() => viewMap[state.screen as keyof typeof viewMap] ?? TodayView)

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
  document.querySelectorAll('.session-focus-ghost, .plan-focus-ghost').forEach((ghost) => ghost.remove())
}

let planBridgeToken = 0

function runPlanBridge(origin: { x: number; y: number; width: number; height: number; text: string; image?: string }): void {
  if (typeof document === 'undefined' || motionReduced.value || !origin.image) return
  const target = document.querySelector<HTMLElement>('[data-plan-hero]')
  if (!target) return
  const to = target.getBoundingClientRect()
  if (!to.width || !to.height) return
  const ghost = document.createElement('div')
  ghost.className = 'plan-focus-ghost'
  ghost.dataset.bridge = 'plan'
  ghost.setAttribute('aria-hidden', 'true')
  ghost.style.left = `${origin.x}px`
  ghost.style.top = `${origin.y}px`
  ghost.style.width = `${origin.width}px`
  ghost.style.height = `${origin.height}px`
  ghost.style.backgroundImage = `url(${origin.image})`
  document.body.appendChild(ghost)
  if (typeof ghost.animate !== 'function') {
    ghost.remove()
    return
  }
  const animation = ghost.animate(
    [
      {
        left: `${origin.x}px`,
        top: `${origin.y}px`,
        width: `${origin.width}px`,
        height: `${origin.height}px`,
        opacity: 1,
      },
      {
        left: `${to.left}px`,
        top: `${to.top}px`,
        width: `${to.width}px`,
        height: `${to.height}px`,
        opacity: 0,
      },
    ],
    { duration: 280, easing: 'cubic-bezier(0.2, 0.8, 0.2, 1)', fill: 'forwards' },
  )
  animation.onfinish = () => ghost.remove()
  window.setTimeout(() => ghost.remove(), 520)
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
    planBridgeToken += 1
    if (screen === 'planDetail' && previous && !motionReduced.value && origin?.image) {
      const token = planBridgeToken
      const started = performance.now()
      const waitForHero = (): void => {
        if (token !== planBridgeToken || state.screen !== 'planDetail' || motionReduced.value) return
        if (document.querySelector('[data-plan-hero]')) {
          runPlanBridge(origin)
          return
        }
        if (performance.now() - started < 600) requestAnimationFrame(waitForHero)
      }
      nextTick(() => requestAnimationFrame(waitForHero))
    }
  },
)

const isTraining = computed(() => state.screen === 'training')
const isCoach = computed(() => state.actor === 'coach')
const showTabbar = computed(() =>
  state.screen === 'messages' ||
  ['today', 'explore', 'records', 'coachTodo', 'coachStudents', 'coachServices'].includes(state.screen),
)
</script>

<template>
  <a class="skip-link" href="#main">跳到主要内容</a>
  <div
    class="app-root"
    :data-motion="motionReduced ? 'reduced' : 'full'"
    :data-focus-screen="isTraining ? 'true' : 'false'"
  >
    <template v-if="isTraining">
      <Transition name="focus" appear>
        <TrainingView :key="state.screen" />
      </Transition>
    </template>
    <template v-else>
      <AppHeader />
      <main id="main" tabindex="-1" :class="isCoach ? 'coach-main' : 'student-main'">
        <Transition name="view" mode="out-in" appear>
          <component :is="currentView" :key="state.screen" />
        </Transition>
      </main>
      <TabBar v-if="showTabbar" />
    </template>

    <PreviewControl :dark="isTraining" />

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

    <BaseSheet
      :open="ui.sheet === 'access'"
      :title="ui.accessStep === 'signin' ? '登录后开始训练' : ui.accessStep === 'age' ? '确认年龄' : '暂不能开始训练'"
      @close="cancelAccess"
    >
      <template v-if="ui.accessStep === 'signin'">
        <p class="access-copy">开始训练需要登录。取消会回到同一份内容与同一训练日，不会创建训练或副本。</p>
        <p class="access-copy access-copy--muted">登录只用于本次开始；已保存的浏览位置与筛选不会丢失。</p>
      </template>
      <template v-else-if="ui.accessStep === 'age'">
        <p class="access-copy">Nerae 不向未满 18 岁的人提供训练服务。请确认你的年龄；这一步不能通过换一份内容跳过。</p>
      </template>
      <template v-else>
        <p class="access-copy">未满 18 岁不能开始训练或建立服务关系。你仍可以继续浏览免费内容，已保存的收藏与筛选保留。</p>
      </template>
      <template #footer>
        <div v-if="ui.accessStep === 'signin'" class="access-actions">
          <button type="button" class="btn btn--ghost" @click="cancelAccess">取消</button>
          <button type="button" class="btn btn--accent" @click="completeLocalSignIn">使用样例身份继续</button>
        </div>
        <div v-else-if="ui.accessStep === 'age'" class="access-actions">
          <button type="button" class="btn btn--ghost" @click="cancelAccess">取消</button>
          <button type="button" class="btn btn--ghost" @click="declareMinor">未满 18 岁</button>
          <button type="button" class="btn btn--accent" @click="confirmAdultAccess">已满 18 岁，继续</button>
        </div>
        <div v-else class="access-actions">
          <button type="button" class="btn" @click="cancelAccess">继续浏览</button>
        </div>
      </template>
    </BaseSheet>

    <Transition name="notice">
      <p
        v-if="ui.saveNote"
        class="app-notice"
        :class="`app-notice--${ui.saveNoteTone ?? 'neutral'}`"
        role="status"
        aria-live="polite"
      >
        {{ ui.saveNote }}
      </p>
    </Transition>
  </div>
</template>

<style scoped>
.skip-link {
  position: absolute;
  left: 12px;
  top: 0;
  z-index: 200;
  min-height: 44px;
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

.access-copy {
  font-size: var(--text-14);
  line-height: 1.7;
}

.access-copy--muted {
  margin-top: var(--sp-2);
  color: var(--muted);
  font-size: var(--text-13);
}

.access-actions {
  display: flex;
  flex-wrap: wrap;
  justify-content: flex-end;
  gap: var(--sp-2);
}

.app-notice {
  position: fixed;
  z-index: 70;
  left: 50%;
  bottom: calc(78px + env(safe-area-inset-bottom));
  transform: translateX(-50%);
  width: min(92vw, 560px);
  margin: 0;
  padding: 10px 14px;
  border: 1px solid var(--line-strong);
  border-radius: var(--radius-md);
  background: var(--surface);
  box-shadow: 0 10px 30px rgba(14, 27, 32, 0.14);
  color: var(--ink);
  font-size: var(--text-13);
  line-height: 1.5;
  text-align: center;
}

.app-notice--error {
  border-color: rgba(162, 63, 61, 0.45);
  color: var(--error);
}

.app-notice--positive {
  border-color: rgba(39, 111, 83, 0.4);
  color: var(--positive);
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

@media (min-width: 768px) {
  .access-copy {
  font-size: var(--text-14);
  line-height: 1.7;
}

.access-copy--muted {
  margin-top: var(--sp-2);
  color: var(--muted);
  font-size: var(--text-13);
}

.access-actions {
  display: flex;
  flex-wrap: wrap;
  justify-content: flex-end;
  gap: var(--sp-2);
}

.app-notice {
    bottom: 24px;
  }
}
</style>
