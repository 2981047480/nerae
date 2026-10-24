<script setup lang="ts">
import { computed } from 'vue'
import { CalendarDays, Compass } from '@lucide/vue'
import { goToExplore, goToToday, state } from '../state/store'

const onToday = computed(() => state.screen === 'today')
const onExplore = computed(() => state.screen === 'explore')
</script>

<template>
  <nav class="tabbar" aria-label="主导航">
    <button
      type="button"
      class="tab"
      :class="{ 'tab--active': onToday }"
      :aria-current="onToday ? 'page' : undefined"
      @click="goToToday"
    >
      <CalendarDays :size="20" :stroke-width="1.8" aria-hidden="true" />
      <span>今天</span>
    </button>
    <button
      type="button"
      class="tab"
      :class="{ 'tab--active': onExplore }"
      :aria-current="onExplore ? 'page' : undefined"
      @click="goToExplore"
    >
      <Compass :size="20" :stroke-width="1.8" aria-hidden="true" />
      <span>发现</span>
    </button>
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
  grid-template-columns: 1fr 1fr;
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
