<script setup lang="ts">
import { computed } from 'vue'
import { SlidersHorizontal } from '@lucide/vue'
import BaseSheet from './BaseSheet.vue'
import {
  closeSheet,
  coacheeContext,
  jumpToScenario,
  openSheet,
  previewScenarios,
  resetPreview,
  setReduceMotion,
  setTextScale,
  state,
  ui,
} from '../state/store'

function selectStudent(key: 'today' | 'explore' | 'training'): void {
  jumpToScenario(key)
}

function selectCoach(key: 'coach-edit' | 'coach-review'): void {
  jumpToScenario(key)
}

function setRole(role: 'student' | 'coach'): void {
  if (role === 'coach') jumpToScenario('coach-review')
  else jumpToScenario('today')
}

const props = defineProps<{ dark?: boolean }>()
const triggerClass = computed(() => (props.dark ? 'preview-trigger preview-trigger--dark' : 'preview-trigger'))

const credits = [
  {
    name: 'Nenad Stojkovic',
    license: 'CC BY 2.0',
    href: 'https://commons.wikimedia.org/wiki/File:Attractive_sporty_woman_doing_overhead_press_in_gym_with_barbell.jpg',
  },
  {
    name: 'Shixart1985',
    license: 'CC BY 2.0',
    href: 'https://commons.wikimedia.org/wiki/File:Athlete_in_the_gym_doing_exercises.jpg',
  },
  {
    name: 'Ella Olsson',
    license: 'CC BY 2.0',
    href: 'https://commons.wikimedia.org/wiki/File:Healthy_salad_bowl_(42231145040).jpg',
  },
  {
    name: 'Federica Diliberto',
    license: 'CC0',
    href: 'https://commons.wikimedia.org/wiki/File:Healthy_Breakfast_(Unsplash).jpg',
  },
]
</script>

<template>
  <div class="preview-root">
    <button
      :class="triggerClass"
      type="button"
      aria-label="打开预览控制"
      aria-haspopup="dialog"
      :aria-expanded="ui.sheet === 'preview'"
      @click="openSheet('preview')"
    >
      <SlidersHorizontal :size="13" aria-hidden="true" />
      <span>预览</span>
    </button>

    <BaseSheet :open="ui.sheet === 'preview'" title="预览" @close="closeSheet">
      <p class="preview-note">本地交互预览 · 人物与内容为样例，记录只保存在本机浏览器。</p>

      <section class="preview-section">
        <h3 class="preview-label">角色</h3>
        <div class="role-row" role="group" aria-label="切换预览角色">
          <button
            type="button"
            class="role-btn"
            :class="{ 'role-btn--active': state.actor === 'student' }"
            :aria-pressed="state.actor === 'student'"
            @click="setRole('student')"
          >
            学员 {{ coacheeContext.student }}
          </button>
          <button
            type="button"
            class="role-btn"
            :class="{ 'role-btn--active': state.actor === 'coach' }"
            :aria-pressed="state.actor === 'coach'"
            @click="setRole('coach')"
          >
            教练 {{ coacheeContext.coach }}
          </button>
        </div>
      </section>

      <section class="preview-section">
        <h3 class="preview-label">情境</h3>
        <div class="scenario-grid">
          <button
            v-for="item in previewScenarios.student"
            :key="item.key"
            type="button"
            class="scenario"
            @click="selectStudent(item.key)"
          >
            <span class="scenario-title">{{ item.label }}</span>
            <span class="scenario-meta">{{ item.meta }}</span>
          </button>
          <button
            v-for="item in previewScenarios.coach"
            :key="item.key"
            type="button"
            class="scenario"
            @click="selectCoach(item.key)"
          >
            <span class="scenario-title">{{ item.label }}</span>
            <span class="scenario-meta">{{ item.meta }}</span>
          </button>
        </div>
      </section>

      <section class="preview-section switch-row">
        <div>
          <h3 class="preview-label">减少动态效果</h3>
          <p class="preview-help">同时跟随系统的减少动态效果设置。</p>
        </div>
        <button
          type="button"
          class="switch"
          role="switch"
          :aria-checked="state.reduceMotion"
          aria-label="减少动态效果"
          @click="setReduceMotion(!state.reduceMotion)"
        >
          <span class="switch-knob" :class="{ 'switch-knob--on': state.reduceMotion }" />
        </button>
      </section>

      <section class="preview-section">
        <h3 class="preview-label">文字大小</h3>
        <p class="preview-help">预览检查用，不是系统字体设置。选择后覆盖全页正文与控件文字。</p>
        <div class="text-scale-row" role="group" aria-label="预览文字大小">
          <button
            type="button"
            class="text-scale-btn"
            :class="{ 'text-scale-btn--active': state.textScale === 1 }"
            :aria-pressed="state.textScale === 1"
            @click="setTextScale(1)"
          >
            100%
          </button>
          <button
            type="button"
            class="text-scale-btn"
            :class="{ 'text-scale-btn--active': state.textScale === 2 }"
            :aria-pressed="state.textScale === 2"
            @click="setTextScale(2)"
          >
            200%
          </button>
        </div>
      </section>

      <details class="credits">
        <summary>图像署名</summary>
        <ul>
          <li v-for="credit in credits" :key="credit.name">
            <a :href="credit.href" target="_blank" rel="noreferrer">{{ credit.name }}</a>
            <span> · {{ credit.license }}</span>
          </li>
        </ul>
        <p>图像经等比缩放并转为 WebP；用途与其它来源见 ASSETS.md。人物照片仅用于训练与内容语境，不表示真实身份、认证或背书。</p>
      </details>

      <section class="preview-section reset-section">
        <template v-if="!ui.resetConfirm">
          <button type="button" class="reset-link" @click="ui.resetConfirm = true">重置预览数据</button>
        </template>
        <div v-else class="reset-confirm">
          <p>会清除本机的训练记录、收藏和教练草稿，文字大小与动效设置也回到初始状态。</p>
          <div class="reset-actions">
            <button type="button" class="btn btn--ghost btn--sm" @click="ui.resetConfirm = false">取消</button>
            <button type="button" class="btn btn--sm reset-btn" @click="resetPreview()">确认重置</button>
          </div>
        </div>
      </section>
    </BaseSheet>
  </div>
</template>

<style scoped>
.preview-trigger {
  position: fixed;
  z-index: 40;
  top: calc(12px + env(safe-area-inset-top));
  right: 12px;
  display: inline-flex;
  align-items: center;
  gap: 5px;
  min-height: 44px;
  padding: 0 11px;
  border: 1px dashed rgba(25, 47, 57, 0.32);
  border-radius: var(--radius-md);
  background: rgba(243, 242, 238, 0.94);
  color: var(--muted);
  font-size: var(--text-12);
  line-height: 1;
  letter-spacing: 0.02em;
  transition:
    color 140ms var(--ease-out),
    border-color 140ms var(--ease-out);
}

.preview-trigger:hover {
  color: var(--ink);
  border-color: rgba(25, 47, 57, 0.55);
}

.preview-trigger:focus-visible {
  outline-offset: 2px;
}

@media (min-width: 768px) {
  .preview-trigger {
    top: calc(20px + env(safe-area-inset-top));
    right: 20px;
  }
}

.preview-trigger--dark {
  color: var(--focus-muted);
  border-color: rgba(241, 240, 234, 0.32);
  background: rgba(22, 42, 51, 0.9);
}

.preview-note {
  margin-bottom: var(--sp-5);
  padding: 10px 12px;
  border-left: 2px solid var(--line-strong);
  color: var(--muted);
  font-size: var(--text-13);
  line-height: 1.55;
}

.preview-section {
  padding: var(--sp-4) 0;
  border-top: 1px solid var(--line);
}

.preview-section:first-of-type {
  border-top: 0;
  padding-top: 0;
}

.preview-label {
  margin-bottom: var(--sp-3);
  color: var(--ink);
  font-size: var(--text-14);
  font-weight: 600;
}

.preview-help {
  color: var(--muted);
  font-size: var(--text-12);
  line-height: 1.5;
}

.role-row {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 1px;
  padding: 3px;
  border: 1px solid var(--line);
  border-radius: var(--radius-md);
  background: var(--canvas);
}

.role-btn {
  min-height: 44px;
  border-radius: 6px;
  color: var(--muted);
  font-size: var(--text-14);
  line-height: 1.3;
  transition:
    background-color 140ms var(--ease-out),
    color 140ms var(--ease-out);
}

.role-btn--active {
  background: var(--surface);
  color: var(--ink);
  font-weight: 600;
  box-shadow: 0 1px 2px rgba(25, 47, 57, 0.08);
}

.scenario-grid {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: var(--sp-2);
}

@media (min-width: 420px) {
  .scenario-grid {
    grid-template-columns: 1fr 1fr 1fr;
  }
}

.scenario {
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  gap: 2px;
  min-height: 62px;
  padding: 10px 12px;
  border: 1px solid var(--line);
  border-radius: var(--radius-md);
  text-align: left;
  transition:
    border-color 140ms var(--ease-out),
    background-color 140ms var(--ease-out);
}

.scenario:hover {
  border-color: var(--line-strong);
  background: var(--canvas);
}

.scenario-title {
  color: var(--ink);
  font-size: var(--text-14);
  font-weight: 600;
  line-height: 1.35;
}

.scenario-meta {
  color: var(--muted);
  font-size: var(--text-12);
  line-height: 1.35;
}

.switch-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--sp-4);
}

.switch-row .preview-label {
  margin-bottom: 2px;
}

.switch {
  position: relative;
  flex: none;
  width: 52px;
  height: 44px;
  padding: 0;
}

.switch::before {
  content: '';
  position: absolute;
  left: 3px;
  right: 3px;
  top: 10px;
  bottom: 10px;
  border-radius: 999px;
  background: var(--line);
  transition: background-color 160ms var(--ease-out);
}

.switch[aria-checked='true']::before {
  background: var(--ink);
}

.switch-knob {
  position: absolute;
  top: 13px;
  left: 6px;
  width: 18px;
  height: 18px;
  border-radius: 50%;
  background: var(--surface);
  transition: transform 160ms var(--ease-out);
}

.switch-knob--on {
  transform: translateX(20px);
}

.text-scale-row {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 1px;
  margin-top: var(--sp-3);
  padding: 3px;
  border: 1px solid var(--line);
  border-radius: var(--radius-md);
  background: var(--canvas);
}

.text-scale-btn {
  min-height: 44px;
  border-radius: 6px;
  color: var(--muted);
  font-size: var(--text-14);
  line-height: 1.3;
}

.text-scale-btn--active {
  background: var(--surface);
  color: var(--ink);
  font-weight: 600;
  box-shadow: 0 1px 2px rgba(25, 47, 57, 0.08);
}

.credits {
  padding: var(--sp-3) 0;
  border-top: 1px solid var(--line);
  color: var(--muted);
  font-size: var(--text-12);
  line-height: 1.55;
}

.credits summary {
  min-height: 32px;
  display: flex;
  align-items: center;
  color: var(--ink);
  font-size: var(--text-13);
  cursor: pointer;
}

.credits ul {
  margin: 6px 0;
}

.credits a {
  color: var(--ink);
}

.credits p {
  margin-top: 6px;
}

.reset-section {
  display: flex;
  justify-content: flex-start;
}

.reset-link {
  min-height: 44px;
  color: var(--error);
  font-size: var(--text-14);
  text-align: left;
}

.reset-confirm p {
  margin-bottom: var(--sp-3);
  color: var(--ink);
  font-size: var(--text-14);
  line-height: 1.5;
}

.reset-actions {
  display: flex;
  gap: var(--sp-2);
}

.reset-btn {
  background: var(--ink);
  color: var(--surface);
}
</style>
