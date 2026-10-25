<script setup lang="ts">
import { computed, ref } from 'vue'
import {
  Bookmark,
  BookmarkCheck,
  CalendarDays,
  Check,
  ChevronDown,
  Clock3,
  Share2,
} from '@lucide/vue'
import PhotoFrame from '../components/PhotoFrame.vue'
import BaseSheet from '../components/BaseSheet.vue'
import {
  activePlanDay,
  closeSheet,
  confirmReplaceAndStart,
  goBack,
  openSheet,
  planImpact,
  planStartBlocked,
  requestStartPlan,
  selectedPlanDays,
  selectedPlanItem,
  selectPlanDay,
  state,
  toggleFavorite,
  ui,
} from '../state/store'

const expanded = ref<string | null>(null)
const shareCopied = ref(false)

const item = selectedPlanItem
const days = selectedPlanDays
const day = activePlanDay

const isFavorite = computed(() => (item.value ? state.favorites.includes(item.value.id) : false))
const dayTotals = computed(() => {
  const current = day.value
  if (!current) return { exercises: 0, sets: 0 }
  return {
    exercises: current.exercises.length,
    sets: current.exercises.reduce((sum, exercise) => sum + (Number.parseInt(exercise.sets, 10) || 0), 0),
  }
})

const equipmentLabel = computed(() => {
  const value = item.value?.equipment
  return value === 'dumbbell' ? '哑铃' : value === 'machine' ? '器械' : value === 'none' ? '自重' : '按内容'
})

function start(): void {
  if (!item.value) return
  requestStartPlan(item.value.id)
}

function toggleExercise(id: string): void {
  expanded.value = expanded.value === id ? null : id
}

async function copyPreviewLink(): Promise<void> {
  shareCopied.value = false
  try {
    await navigator.clipboard.writeText(window.location.href)
    shareCopied.value = true
  } catch {
    shareCopied.value = false
  }
}
</script>

<template>
  <div v-if="item" class="screen screen--wide plan-detail">
    <section class="plan-hero" aria-labelledby="plan-title">
      <div class="plan-hero-media" data-plan-hero>
        <PhotoFrame :src="item.image" :alt="item.imageAlt" eager fallback-label="训练图片暂不可用" />
        <div class="plan-hero-scrim" aria-hidden="true" />
        <div class="plan-hero-text">
          <p class="plan-kicker">{{ item.source }} · {{ item.perWeek }}</p>
          <h1 id="plan-title" class="plan-title">{{ item.title }}</h1>
          <p class="plan-meta num">
            {{ item.countLabel }} · {{ item.durationMin }} 分钟 · {{ equipmentLabel }}
          </p>
        </div>
      </div>

      <div class="plan-hero-side">
        <p class="plan-summary">{{ item.summary }}</p>
        <ul class="fit-list">
          <li v-for="line in item.fit" :key="line">{{ line }}</li>
        </ul>
        <div class="plan-actions">
          <button
            type="button"
            class="btn btn--ghost btn--sm"
            :aria-pressed="isFavorite"
            @click="toggleFavorite(item.id)"
          >
            <BookmarkCheck v-if="isFavorite" :size="16" aria-hidden="true" />
            <Bookmark v-else :size="16" aria-hidden="true" />
            {{ isFavorite ? '已收藏' : '收藏' }}
          </button>
          <button type="button" class="btn btn--ghost btn--sm" @click="openSheet('share')">
            <Share2 :size="16" aria-hidden="true" />
            分享
          </button>
        </div>
      </div>
    </section>

    <div v-if="planStartBlocked" class="banner banner--error" role="alert">
      <div>
        <p class="banner-title">这份内容已经下架</p>
        <p class="banner-body">已开始的训练记录不会改变，但不能从这里开始新的训练。可以回到发现看看其他内容。</p>
      </div>
    </div>

    <div class="plan-body">
      <section class="plan-content" aria-labelledby="content-title">
        <header class="group-head">
          <h2 id="content-title" class="group-title">训练内容</h2>
          <span class="group-meta num">{{ dayTotals.exercises }} 个动作 · {{ dayTotals.sets }} 组</span>
        </header>

        <div v-if="days.length > 1" class="day-tabs" role="tablist" aria-label="训练日">
          <button
            v-for="entry in days"
            :key="entry.id"
            type="button"
            role="tab"
            class="day-tab"
            :class="{ 'day-tab--active': entry.id === day?.id }"
            :aria-selected="entry.id === day?.id"
            @click="selectPlanDay(entry.id)"
          >
            {{ entry.title }}
          </button>
        </div>

        <ol v-if="day" class="exercise-list">
          <li v-for="(exercise, index) in day.exercises" :key="exercise.id" class="exercise-item">
            <span class="exercise-index num">{{ String(index + 1).padStart(2, '0') }}</span>
            <div class="exercise-main">
              <div class="exercise-title-row">
                <h3 class="exercise-name">{{ exercise.name }}</h3>
                <span class="exercise-scheme num">{{ exercise.sets }} 组 × {{ exercise.target }}</span>
              </div>
              <p class="exercise-rest num">
                <Clock3 :size="13" aria-hidden="true" />
                组间休息 {{ exercise.restSeconds }} 秒
              </p>
              <button
                type="button"
                class="exercise-toggle"
                :aria-expanded="expanded === exercise.id"
                @click="toggleExercise(exercise.id)"
              >
                {{ expanded === exercise.id ? '收起说明' : '动作说明' }}
                <ChevronDown :size="15" aria-hidden="true" :class="{ 'is-open': expanded === exercise.id }" />
              </button>
              <p v-if="expanded === exercise.id && exercise.note" class="exercise-note">
                {{ exercise.note }}
              </p>
            </div>
          </li>
        </ol>

        <div class="source-block">
          <p class="source-line">{{ item.source }}</p>
          <p class="source-note">
            出现疼痛或不适时停止，不替代个人健康判断。
          </p>
        </div>
      </section>

      <aside class="plan-side">
        <section class="side-panel" aria-labelledby="start-title">
          <h2 id="start-title" class="side-title">开始这次训练</h2>
          <dl class="kv">
            <dt>频率</dt>
            <dd>{{ item.perWeek }}</dd>
            <dt>单次</dt>
            <dd class="num">约 {{ item.durationMin }} 分钟</dd>
            <dt>器械</dt>
            <dd>{{ equipmentLabel }}</dd>
          </dl>
          <p class="side-day">今天开始：{{ activePlanDay?.title }}</p>
          <p v-if="state.student.planVersion" class="side-note">
            当前我的计划为版本 {{ state.student.planVersion }}。开始后会保存到我的计划，并只改变未来安排。
          </p>
          <button class="btn btn--accent btn--block start-cta" type="button" :disabled="planStartBlocked" @click="start">
            开始这次训练
          </button>
          <p class="side-fine">
            已有安排时会先说明替换影响，不会重写已完成的训练。
          </p>
        </section>

        <section class="side-panel side-panel--quiet" aria-labelledby="fit-title">
          <h2 id="fit-title" class="side-title">适用与条件</h2>
          <ul class="fit-list">
            <li v-for="line in item.fit" :key="line">{{ line }}</li>
          </ul>
          <p v-if="!state.auth.signedIn" class="access-note">
            当前为匿名浏览。开始训练需要登录，并确认已满 18 岁。
          </p>
          <p v-else-if="state.auth.age === 'minor'" class="access-note">
            Nerae 不向未满 18 岁的人提供训练服务；你仍可以继续浏览内容。
          </p>
          <p v-else-if="state.auth.age === 'unknown'" class="access-note">
            开始训练前需要确认年龄；换一份内容不会跳过这一步。
          </p>
        </section>
      </aside>
    </div>

    <BaseSheet :open="ui.sheet === 'replace'" title="替换当前安排" @close="closeSheet">
      <p class="sheet-day">今天开始：{{ activePlanDay?.title }}</p>
      <p class="sheet-copy">
        今天已有安排或已完成的训练。开始这份内容会替换<strong>未来</strong>的安排；已记录的训练历史保留，不会被改写。
      </p>
      <ul class="impact-list">
        <li v-for="slot in planImpact.after" :key="slot.date">
          <span>{{ slot.dateLabel }}</span>
          <span>{{ slot.dayTitle }}</span>
        </li>
      </ul>
      <p class="sheet-copy sheet-copy--muted">
        今天的当前训练会被结束并改为新的安排；已有组记录仍保留在记录中。
      </p>
      <template #footer>
        <div class="sheet-actions">
          <button type="button" class="btn btn--ghost" @click="closeSheet">取消</button>
          <button type="button" class="btn btn--accent" @click="confirmReplaceAndStart">替换未来安排并开始</button>
        </div>
      </template>
    </BaseSheet>

    <BaseSheet :open="ui.sheet === 'share'" title="分享这份内容" @close="closeSheet">
      <p class="sheet-copy">分享只会生成当前本地预览的地址，不会发布到公开内容，也不会上传任何记录。</p>
      <div class="share-card">
        <p class="share-name">{{ item.title }}</p>
        <p class="share-meta num">{{ item.countLabel }} · {{ item.perWeek }} · {{ item.source }}</p>
      </div>
      <p v-if="shareCopied" class="share-copied" role="status">
        <Check :size="14" aria-hidden="true" />
        已复制本地预览地址
      </p>
      <template #footer>
        <div class="sheet-actions">
          <button type="button" class="btn btn--ghost" @click="closeSheet">关闭</button>
          <button type="button" class="btn" @click="copyPreviewLink">复制预览地址</button>
        </div>
      </template>
    </BaseSheet>
  </div>

  <div v-else class="screen">
    <div class="empty-state">
      <CalendarDays :size="28" aria-hidden="true" class="empty-icon" />
      <h1 class="empty-title">没有可查看的训练内容</h1>
      <p class="empty-text">回到发现选择一份训练，再查看完整内容。</p>
      <div class="empty-actions">
        <button type="button" class="btn" @click="goBack">回到发现</button>
      </div>
    </div>
  </div>
</template>

<style scoped>
.plan-detail {
  padding-top: 0;
}

.plan-hero {
  display: grid;
  gap: var(--sp-5);
}

.plan-hero-media {
  position: relative;
  min-height: 320px;
  height: clamp(320px, 54vw, 470px);
  overflow: hidden;
  border-radius: var(--radius-lg);
  background: var(--ink);
}

.plan-hero-media :deep(.photo),
.plan-hero-media :deep(img) {
  height: 100%;
}

.plan-hero-scrim {
  position: absolute;
  inset: 0;
  background: linear-gradient(180deg, rgba(8, 18, 23, 0.05) 32%, rgba(8, 18, 23, 0.78) 100%);
}

.plan-hero-text {
  position: absolute;
  left: 0;
  right: 0;
  bottom: 0;
  padding: 22px;
  color: var(--focus-text);
}

.plan-kicker {
  color: rgba(241, 240, 234, 0.8);
  font-size: var(--text-13);
}

.plan-title {
  margin-top: 4px;
  color: var(--focus-text);
  font-size: clamp(1.75rem, 6.4vw, 2.5rem);
  line-height: 1.18;
  letter-spacing: -0.02em;
}

.plan-meta {
  margin-top: 5px;
  color: rgba(241, 240, 234, 0.8);
  font-size: var(--text-13);
}

.plan-hero-side {
  min-width: 0;
}

.plan-summary {
  font-size: var(--text-16);
  line-height: 1.7;
}

.fit-list {
  margin-top: var(--sp-3);
  border-top: 1px solid var(--line);
}

.fit-list li {
  padding: 8px 0;
  border-bottom: 1px solid var(--line);
  color: var(--muted);
  font-size: var(--text-13);
  line-height: 1.55;
}

.plan-actions {
  display: flex;
  gap: var(--sp-2);
  margin-top: var(--sp-3);
}

.plan-body {
  display: grid;
  gap: var(--sp-6);
  margin-top: var(--sp-6);
}

.day-tabs {
  display: flex;
  flex-wrap: wrap;
  gap: 4px;
  margin-bottom: var(--sp-3);
}

.day-tab {
  min-height: 44px;
  padding: 0 13px;
  border: 1px solid var(--line);
  border-radius: var(--radius-md);
  color: var(--muted);
  font-size: var(--text-14);
}

.day-tab--active {
  border-color: var(--ink);
  background: var(--ink);
  color: #fff;
  font-weight: 600;
}

.exercise-list {
  border-top: 1px solid var(--line);
}

.exercise-item {
  display: flex;
  gap: var(--sp-4);
  padding: var(--sp-4) 0;
  border-bottom: 1px solid var(--line);
}

.exercise-index {
  width: 24px;
  flex: none;
  padding-top: 3px;
  color: var(--muted);
  font-size: var(--text-12);
}

.exercise-main {
  min-width: 0;
  flex: 1;
}

.exercise-title-row {
  display: flex;
  align-items: baseline;
  justify-content: space-between;
  gap: var(--sp-3);
}

.exercise-name {
  font-size: var(--text-16);
  font-weight: 600;
}

.exercise-scheme {
  flex: none;
  color: var(--muted);
  font-size: var(--text-13);
}

.exercise-rest {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  margin-top: 3px;
  color: var(--muted);
  font-size: var(--text-12);
}

.exercise-toggle {
  display: inline-flex;
  align-items: center;
  gap: 3px;
  min-height: 44px;
  margin-top: 2px;
  color: var(--ink);
  font-size: var(--text-13);
}

.exercise-toggle .is-open {
  transform: rotate(180deg);
}

.exercise-note {
  padding: var(--sp-2) var(--sp-3);
  border-left: 2px solid var(--line-strong);
  color: var(--muted);
  font-size: var(--text-13);
  line-height: 1.65;
}

.source-block {
  margin-top: var(--sp-5);
}

.source-line {
  font-size: var(--text-13);
  font-weight: 500;
}

.source-note {
  margin-top: 3px;
  color: var(--muted);
  font-size: var(--text-12);
  line-height: 1.6;
}

.plan-side {
  display: grid;
  gap: var(--sp-4);
  align-content: start;
}

.side-panel {
  padding: var(--sp-4);
  border: 1px solid var(--line);
  border-radius: var(--radius-lg);
  background: var(--surface);
}

.side-panel--quiet {
  background: rgba(255, 255, 255, 0.55);
}

.side-title {
  margin-bottom: var(--sp-3);
  font-size: var(--text-16);
  font-weight: 600;
}

.side-day {
  margin-top: var(--sp-2);
  font-size: var(--text-13);
  font-weight: 500;
}

.sheet-day {
  display: inline-flex;
  margin-bottom: var(--sp-2);
  padding: 3px 8px;
  border-radius: 4px;
  background: rgba(25, 47, 57, 0.06);
  font-size: var(--text-13);
  font-weight: 500;
}

.side-note,
.side-fine {
  margin-top: var(--sp-3);
  color: var(--muted);
  font-size: var(--text-12);
  line-height: 1.6;
}

.start-cta {
  margin-top: var(--sp-3);
}

.access-note {
  margin-top: var(--sp-3);
  color: var(--warning);
  font-size: var(--text-12);
  line-height: 1.6;
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

.impact-list {
  margin-top: var(--sp-3);
  border-top: 1px solid var(--line);
}

.impact-list li {
  display: flex;
  justify-content: space-between;
  gap: var(--sp-3);
  padding: 8px 0;
  border-bottom: 1px solid var(--line);
  font-size: var(--text-13);
}

.share-card {
  margin-top: var(--sp-3);
  padding: var(--sp-4);
  border-radius: var(--radius-md);
  background: rgba(25, 47, 57, 0.05);
}

.share-name {
  font-size: var(--text-16);
  font-weight: 600;
}

.share-meta {
  margin-top: 3px;
  color: var(--muted);
  font-size: var(--text-12);
}

.share-copied {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  margin-top: var(--sp-2);
  color: var(--positive);
  font-size: var(--text-13);
}

@media (min-width: 768px) {
  .plan-hero {
    grid-template-columns: minmax(0, 1.6fr) minmax(280px, 0.8fr);
    align-items: stretch;
    gap: var(--sp-6);
  }

  .plan-hero-side {
    display: flex;
    flex-direction: column;
    justify-content: flex-end;
    padding-top: var(--sp-4);
  }

  .plan-body {
    grid-template-columns: minmax(0, 1.7fr) minmax(300px, 0.85fr);
    gap: var(--sp-8);
  }

  .plan-hero-text {
    padding: 30px;
  }
}
</style>

<style>
html[data-text-scale='200'] .plan-hero-media {
  min-height: 0;
  height: auto;
}

html[data-text-scale='200'] .plan-hero-media .photo,
html[data-text-scale='200'] .plan-hero-media img {
  height: 260px;
}

html[data-text-scale='200'] .plan-hero-scrim {
  display: none;
}

html[data-text-scale='200'] .plan-hero-text {
  position: static;
  padding: 20px;
}

html[data-text-scale='200'] .exercise-item,
html[data-text-scale='200'] .exercise-main {
  min-width: 0;
}

html[data-text-scale='200'] .exercise-title-row {
  flex-direction: column;
  align-items: flex-start;
  gap: 2px;
}

html[data-text-scale='200'] .exercise-scheme {
  flex: 0 1 auto;
}
</style>
