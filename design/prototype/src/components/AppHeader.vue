<script setup lang="ts">
import { computed } from 'vue'
import Wordmark from './Wordmark.vue'
import { COACH_NAME, PREVIEW_DATE_LABEL } from '../data/sample'
import { goHome, goToExplore, goToToday, state } from '../state/store'

const isCoach = computed(() => state.actor === 'coach')
const onExplore = computed(() => state.screen === 'explore')
const onToday = computed(() => state.screen === 'today')
</script>

<template>
  <header class="app-header">
    <div class="brand-row">
      <button class="brand" type="button" aria-label="回到主要页面" @click="goHome">
        <Wordmark />
      </button>
      <span v-if="isCoach" class="header-meta">{{ COACH_NAME }} · 教练</span>
      <span v-else class="header-meta num">{{ PREVIEW_DATE_LABEL }}</span>
    </div>
    <nav v-if="!isCoach" class="desktop-nav" aria-label="主导航">
      <button
        type="button"
        class="nav-link"
        :class="{ 'nav-link--active': onToday }"
        :aria-current="onToday ? 'page' : undefined"
        @click="goToToday"
      >
        今天
      </button>
      <button
        type="button"
        class="nav-link"
        :class="{ 'nav-link--active': onExplore }"
        :aria-current="onExplore ? 'page' : undefined"
        @click="goToExplore"
      >
        发现
      </button>
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
  align-items: baseline;
  gap: var(--sp-4);
  min-width: 0;
}

.brand {
  display: inline-flex;
  align-items: center;
  min-height: 44px;
  color: var(--ink);
}

.header-meta {
  color: var(--muted);
  font-size: var(--text-13);
  line-height: 1.4;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.desktop-nav {
  display: none;
  align-items: center;
  gap: var(--sp-6);
}

.nav-link {
  position: relative;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  min-width: 44px;
  min-height: 44px;
  color: var(--muted);
  font-size: var(--text-15);
  font-weight: 500;
  transition: color 140ms var(--ease-out);
}

.nav-link:hover {
  color: var(--ink);
}

.nav-link--active {
  color: var(--ink);
  font-weight: 600;
}

.nav-link--active::after {
  content: '';
  position: absolute;
  left: 0;
  right: 0;
  bottom: 6px;
  height: 1px;
  background: var(--ink);
}

@media (max-width: 359px) {
  .app-header {
    padding-right: calc(var(--gutter) + 76px);
  }

  .header-meta {
    font-size: var(--text-12);
  }
}

@media (min-width: 768px) {
  .app-header {
    padding-top: calc(20px + env(safe-area-inset-top));
    padding-bottom: 12px;
    padding-right: calc(var(--gutter) + 84px);
  }

  .desktop-nav {
    display: flex;
  }
}
</style>
