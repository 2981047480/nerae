<script setup lang="ts">
import { computed } from 'vue'
import { SlidersHorizontal } from '@lucide/vue'
import BaseSheet from './BaseSheet.vue'
import { paletteOptions, selectedPalette, setPalette } from '../state/palette'
import {
  applyPreviewState,
  closeSheet,
  coacheeContext,
  jumpToScenario,
  openSheet,
  previewScenarios,
  previewStateFor,
  PREVIEW_STATES,
  resetPreview,
  setReduceMotion,
  setTextScale,
  state,
  ui,
} from '../state/store'
import type { PreviewScenario } from '../state/store'

const props = defineProps<{ dark?: boolean }>()
const triggerClass = computed(() => (props.dark ? 'preview-trigger preview-trigger--dark' : 'preview-trigger'))

function setRole(role: 'student' | 'coach'): void {
  if (role === 'coach') jumpToScenario('coach-todo')
  else jumpToScenario('today')
}

function selectScenario(key: string): void {
  jumpToScenario(key as PreviewScenario)
}

const currentStates = computed(() => PREVIEW_STATES[state.screen] ?? [])
const currentState = computed(() => previewStateFor(state.screen))

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

      <section v-if="paletteOptions.length > 1" class="preview-section palette-section">
        <h3 class="preview-label">色彩方向</h3>
        <p class="preview-help">切换配色，比较同一页面。记录和填写位置都会保留。</p>
        <div class="palette-options" role="group" aria-label="色彩方向">
          <button v-for="option in paletteOptions" :key="option.id" type="button" class="palette-option"
            :aria-pressed="selectedPalette===option.id" :aria-label="option.title"
            @click="setPalette(option.id);closeSheet()">
            <span class="palette-swatches" aria-hidden="true"><i :style="{background:option.colors.canvas}"/><i :style="{background:option.colors.ink}"/><i :style="{background:option.colors.action}"/></span>
            <span><strong>{{ option.title }}<small v-if="option.recommended">推荐</small></strong><span>{{ option.summary }}</span></span>
            <span class="palette-check" aria-hidden="true">{{ selectedPalette===option.id?'✓':'' }}</span>
          </button>
        </div>
      </section>

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
            @click="selectScenario(item.key)"
          >
            <span class="scenario-title">{{ item.label }}</span>
            <span class="scenario-meta">{{ item.meta }}</span>
          </button>
        </div>
        <div class="scenario-grid scenario-grid--coach">
          <button
            v-for="item in previewScenarios.coach"
            :key="item.key"
            type="button"
            class="scenario"
            @click="selectScenario(item.key)"
          >
            <span class="scenario-title">{{ item.label }}</span>
            <span class="scenario-meta">{{ item.meta }}</span>
          </button>
        </div>
      </section>

      <section v-if="currentStates.length" class="preview-section">
        <h3 class="preview-label">当前页状态</h3>
        <div class="state-wrap">
          <button
            v-for="option in currentStates"
            :key="option.key"
            type="button"
            class="state-chip"
            :class="{ 'state-chip--active': currentState === option.key }"
            :aria-pressed="currentState === option.key"
            @click="applyPreviewState(state.screen, option.key)"
          >
            {{ option.label }}
          </button>
        </div>
        <p class="preview-help">切换只改变当前样例状态；重置可回到初始数据。</p>
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
          <p>会清除本机的训练记录、收藏、消息、计划和自评，文字大小与动效设置也回到初始状态。</p>
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
.palette-options { display:grid; gap:8px; margin-top:14px; }
.palette-option { display:flex; align-items:center; gap:12px; padding:14px 12px; border:1px solid var(--line); border-radius:10px; text-align:left; min-height:76px; }
.palette-option[aria-pressed=true] { border-color:var(--accent); background:var(--accent-soft); }
.palette-swatches { display:flex; flex-shrink:0; border:1px solid var(--line-strong); border-radius:6px; overflow:hidden; }
.palette-swatches i { width:16px; height:32px; }
.palette-option > span:nth-child(2) { flex:1; min-width:0; }
.palette-option strong { display:block; font-size:var(--text-14); font-weight:600; }
.palette-option strong small { margin-left:8px; font-size:var(--text-12); font-weight:400; color:var(--accent); }
.palette-option strong + span { display:block; margin-top:4px; color:var(--muted); font-size:var(--text-12); line-height:1.5; }
.palette-check { width:18px; flex-shrink:0; color:var(--accent); }

.preview-root {
  position: relative;
  z-index: 60;
}

.preview-trigger {
  position: fixed;
  z-index: 60;
  top: calc(10px + env(safe-area-inset-top));
  right: 12px;
  display: inline-flex;
  align-items: center;
  gap: 5px;
  min-height: 44px;
  padding: 0 12px;
  border: 1px solid var(--line-strong);
  border-radius: 999px;
  background: rgba(243, 242, 238, 0.92);
  color: var(--muted);
  font-size: var(--text-12);
  font-weight: 500;
  backdrop-filter: blur(6px);
}

.preview-trigger--dark {
  border-color: var(--focus-line);
  background: rgba(22, 42, 51, 0.9);
  color: var(--focus-muted);
}

.preview-note {
  padding-bottom: var(--sp-3);
  color: var(--muted);
  font-size: var(--text-13);
  line-height: 1.55;
  border-bottom: 1px solid var(--line);
}

.preview-section {
  padding: var(--sp-4) 0;
  border-bottom: 1px solid var(--line);
}

.preview-label {
  font-size: var(--text-14);
  font-weight: 600;
}

.preview-help {
  margin-top: 4px;
  color: var(--muted);
  font-size: var(--text-12);
  line-height: 1.5;
}

.role-row {
  display: flex;
  gap: var(--sp-2);
  margin-top: var(--sp-2);
}

.role-btn {
  flex: 1;
  min-height: 44px;
  padding: 0 10px;
  border: 1px solid var(--line-strong);
  border-radius: var(--radius-md);
  color: var(--muted);
  font-size: var(--text-14);
}

.role-btn--active {
  border-color: var(--ink);
  background: var(--ink);
  color: var(--on-ink);
  font-weight: 600;
}

.scenario-grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 6px;
  margin-top: var(--sp-2);
}

.scenario-grid--coach {
  margin-top: 6px;
}

.scenario {
  display: flex;
  flex-direction: column;
  gap: 2px;
  min-height: 46px;
  padding: 7px 10px;
  border: 1px solid var(--line);
  border-radius: var(--radius-md);
  text-align: left;
}

.scenario:hover {
  border-color: var(--line-strong);
  background: rgba(var(--ink-rgb), 0.035);
}

.scenario-title {
  font-size: var(--text-14);
  font-weight: 500;
}

.scenario-meta {
  color: var(--muted);
  font-size: var(--text-12);
  line-height: 1.35;
}

.state-wrap {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
  margin-top: var(--sp-2);
}

.state-chip {
  min-height: 44px;
  padding: 0 12px;
  border: 1px solid var(--line);
  border-radius: 999px;
  color: var(--muted);
  font-size: var(--text-13);
}

.state-chip--active {
  border-color: var(--ink);
  background: rgba(var(--ink-rgb), 0.07);
  color: var(--ink);
  font-weight: 600;
}

.switch-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--sp-4);
}

.switch {
  position: relative;
  flex: none;
  width: 52px;
  height: 44px;
  border-radius: 999px;
  background: transparent;
}

.switch::before {
  content: '';
  position: absolute;
  left: 2px;
  right: 2px;
  top: 8px;
  height: 28px;
  border-radius: 999px;
  background: var(--line-strong);
  transition: background-color 160ms var(--ease-out);
}

.switch-knob {
  position: absolute;
  top: 12px;
  left: 6px;
  width: 20px;
  height: 20px;
  border-radius: 50%;
  background: var(--surface);
  transition: transform 160ms var(--ease-out);
}

.switch-knob--on {
  transform: translateX(20px);
  background: var(--ink);
}

.switch[aria-checked='true']::before {
  background: var(--accent);
}

.text-scale-row {
  display: flex;
  gap: 6px;
  margin-top: var(--sp-2);
}

.text-scale-btn {
  min-height: 44px;
  padding: 0 16px;
  border: 1px solid var(--line-strong);
  border-radius: var(--radius-md);
  color: var(--muted);
  font-size: var(--text-14);
}

.text-scale-btn--active {
  border-color: var(--ink);
  color: var(--ink);
  font-weight: 600;
}

.credits {
  padding: var(--sp-4) 0;
  border-bottom: 1px solid var(--line);
  color: var(--muted);
  font-size: var(--text-12);
  line-height: 1.6;
}

.credits summary {
  cursor: pointer;
  color: var(--ink);
  font-size: var(--text-13);
}

.credits ul {
  margin-top: 6px;
}

.credits a {
  display: inline-flex;
  align-items: center;
  min-height: 44px;
}

.credits p {
  margin-top: 6px;
}

.reset-section {
  border-bottom: 0;
}

.reset-link {
  min-height: 44px;
  color: var(--error);
  font-size: var(--text-14);
}

.reset-confirm p {
  color: var(--muted);
  font-size: var(--text-13);
  line-height: 1.55;
}

.reset-actions {
  display: flex;
  justify-content: flex-end;
  gap: var(--sp-2);
  margin-top: var(--sp-2);
}

.reset-btn {
  background: var(--error);
}

@media (max-width: 359px) {
  .scenario-grid {
    grid-template-columns: minmax(0, 1fr);
  }
}

@media (min-width: 768px) {
  .preview-trigger {
    top: 16px;
    right: 20px;
  }
}
</style>
