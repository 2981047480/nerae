<script setup lang="ts">
import { computed, nextTick, ref, watch } from 'vue'
import {
  Bookmark,
  BookmarkCheck,
  ChevronDown,
  Compass,
  Search,
  SlidersHorizontal,
  X,
} from '@lucide/vue'
import PhotoFrame from '../components/PhotoFrame.vue'
import BaseSheet from '../components/BaseSheet.vue'
import {
  activeFilterCount,
  applyFilterDraft,
  currentFilters,
  exploreFeatured,
  exploreRest,
  filteredExplore,
  filterDraftResultCount,
  filterGroups,
  openSheet,
  relaxFilters,
  resetFilters,
  setExploreMode,
  state,
  toggleExpanded,
  toggleFavorite,
  ui,
} from '../state/store'
import type { ExploreItem } from '../types'

const searchInput = ref<HTMLInputElement | null>(null)

const items = filteredExplore
const featured = exploreFeatured
const rest = exploreRest
const mode = computed(() => state.explore.mode)
const favorites = computed(() => state.favorites)
const totalCount = computed(() => filteredExplore.value.length)

const activeFilterText = computed(() => {
  const parts: string[] = []
  const filters = currentFilters.value
  if (filters.goal !== 'all') {
    const group = filterGroups.value.find((item) => item.key === 'goal')
    const option = group?.options.find((item) => item.value === filters.goal)
    if (option) parts.push(option.label)
  }
  if (mode.value === 'training') {
    if (filters.equipment !== 'all') {
      const group = filterGroups.value.find((item) => item.key === 'equipment')
      const option = group?.options.find((item) => item.value === filters.equipment)
      if (option) parts.push(option.label)
    }
    if (filters.duration !== 'all') {
      const group = filterGroups.value.find((item) => item.key === 'duration')
      const option = group?.options.find((item) => item.value === filters.duration)
      if (option) parts.push(option.label)
    }
  }
  return parts.join(' · ')
})

const emptyTitle = computed(() =>
  mode.value === 'training' ? '没有符合条件的训练内容' : '没有符合条件的饮食内容',
)

const emptyActionLabel = computed(() =>
  activeFilterCount.value > 0 ? '放宽一个条件' : '清除搜索',
)

function isFavorite(id: string): boolean {
  return favorites.value.includes(id)
}

function itemMeta(item: ExploreItem): string {
  if (item.type === 'diet') {
    return `${item.countLabel} · ${item.perWeek}`
  }
  return `${item.countLabel} · ${item.perWeek} · ${item.durationMin} 分钟`
}

function equipmentLabel(value: string): string {
  return (
    {
      dumbbell: '哑铃',
      machine: '器械',
      none: '自重',
    }[value] ?? value
  )
}

function toggleSearch(): void {
  ui.searchOpen = !ui.searchOpen
  if (ui.searchOpen) {
    nextTick(() => searchInput.value?.focus())
  } else {
    ui.exploreQuery = ''
  }
}

function clearQuery(): void {
  ui.exploreQuery = ''
  searchInput.value?.focus()
}

watch(
  () => ui.searchOpen,
  (open) => {
    if (open) nextTick(() => searchInput.value?.focus())
  },
)

function selectMode(next: 'training' | 'diet'): void {
  setExploreMode(next)
}
</script>

<template>
  <div class="explore-root">
  <div class="page explore">
    <header class="explore-head">
      <div class="explore-heading">
        <h1 class="explore-title">发现</h1>
        <p class="explore-sub num">
          {{ mode === 'training' ? `${totalCount} 份训练内容` : `${totalCount} 份饮食内容` }}
        </p>
      </div>
      <div class="explore-tools">
        <button
          type="button"
          class="icon-btn tool-btn"
          :aria-label="ui.searchOpen ? '关闭搜索' : '搜索内容'"
          :aria-expanded="ui.searchOpen"
          @click="toggleSearch"
        >
          <X v-if="ui.searchOpen" :size="19" aria-hidden="true" />
          <Search v-else :size="19" aria-hidden="true" />
        </button>
        <button
          type="button"
          class="filter-btn"
          :aria-label="activeFilterCount ? `筛选，已选 ${activeFilterCount} 项` : '筛选内容'"
          @click="openSheet('filter')"
        >
          <SlidersHorizontal :size="17" aria-hidden="true" />
          <span>筛选</span>
          <span v-if="activeFilterCount" class="filter-count num">{{ activeFilterCount }}</span>
        </button>
      </div>
    </header>

    <div class="mode-tabs" role="tablist" aria-label="内容类型">
      <button
        type="button"
        role="tab"
        class="mode-tab"
        :class="{ 'mode-tab--active': mode === 'training' }"
        :aria-selected="mode === 'training'"
        @click="selectMode('training')"
      >
        训练
      </button>
      <button
        type="button"
        role="tab"
        class="mode-tab"
        :class="{ 'mode-tab--active': mode === 'diet' }"
        :aria-selected="mode === 'diet'"
        @click="selectMode('diet')"
      >
        饮食
      </button>
    </div>

    <Transition name="search">
      <div v-if="ui.searchOpen" class="search-row">
        <Search :size="17" aria-hidden="true" class="search-icon" />
        <input
          ref="searchInput"
          v-model="ui.exploreQuery"
          type="text"
          class="search-input"
          role="searchbox"
          placeholder="搜索内容、动作或原则"
          aria-label="搜索训练或饮食内容"
          autocomplete="off"
        />
        <button v-if="ui.exploreQuery" type="button" class="icon-btn search-clear" aria-label="清除搜索" @click="clearQuery">
          <X :size="17" aria-hidden="true" />
        </button>
      </div>
    </Transition>

    <div class="filter-summary">
      <p class="filter-text">
        <template v-if="activeFilterText">{{ activeFilterText }}</template>
        <template v-else>全部内容</template>
      </p>
      <button
        v-if="activeFilterCount"
        type="button"
        class="clear-link"
        @click="resetFilters()"
      >
        清除筛选
      </button>
    </div>

    <template v-if="items.length">
      <article v-if="featured" class="featured">
        <div class="featured-media">
          <PhotoFrame
            :src="featured.image"
            :alt="featured.imageAlt"
            eager
            fallback-label="内容图片暂不可用"
          />
          <div class="featured-top">
            <span class="featured-badge">{{ featured.source }}</span>
            <button
              type="button"
              class="bookmark-btn"
              :aria-label="isFavorite(featured.id) ? `取消收藏 ${featured.title}` : `收藏 ${featured.title}`"
              :aria-pressed="isFavorite(featured.id)"
              @click="toggleFavorite(featured.id)"
            >
              <BookmarkCheck v-if="isFavorite(featured.id)" :size="19" aria-hidden="true" />
              <Bookmark v-else :size="19" aria-hidden="true" />
            </button>
          </div>
        </div>
        <div class="featured-body">
          <p class="item-overline">
            {{ featured.type === 'training' ? '训练内容' : '饮食内容' }}
            <span v-if="featured.type === 'training'"> · {{ equipmentLabel(featured.equipment) }}</span>
          </p>
          <h2 class="featured-title">{{ featured.title }}</h2>
          <p class="item-meta num">{{ itemMeta(featured) }}</p>
          <p class="item-summary">{{ featured.summary }}</p>
          <button
            type="button"
            class="expand-btn"
            :aria-expanded="ui.expandedId === featured.id"
            :aria-controls="`content-${featured.id}`"
            @click="toggleExpanded(featured.id)"
          >
            {{ ui.expandedId === featured.id ? '收起内容' : '查看完整内容' }}
            <ChevronDown
              :size="16"
              aria-hidden="true"
              :class="{ 'expand-icon--open': ui.expandedId === featured.id }"
            />
          </button>
          <Transition name="expand">
            <div
              v-if="ui.expandedId === featured.id"
              :id="`content-${featured.id}`"
              class="item-detail"
            >
              <div class="fit-block">
                <h3 class="detail-title">{{ featured.type === 'training' ? '适用与条件' : '适用说明' }}</h3>
                <ul class="fit-list">
                  <li v-for="line in featured.fit" :key="line">{{ line }}</li>
                </ul>
              </div>
              <div class="content-block">
                <h3 class="detail-title">
                  {{ featured.type === 'training' ? `训练内容 · ${featured.contents.length} 个动作` : '内容原则' }}
                </h3>
                <ol class="content-list">
                  <li v-for="(line, index) in featured.contents" :key="line.name" class="content-row">
                    <span class="content-index num">{{ String(index + 1).padStart(2, '0') }}</span>
                    <span class="content-name">{{ line.name }}</span>
                    <span class="content-detail num">{{ line.detail }}</span>
                  </li>
                </ol>
              </div>
              <p class="item-source">{{ featured.source }}</p>
            </div>
          </Transition>
        </div>
      </article>

      <ul class="item-list">
        <li v-for="item in rest" :key="item.id" class="item">
          <div class="item-main">
            <div class="item-thumb">
              <PhotoFrame
                :src="item.image"
                :alt="item.imageAlt"
                fallback-label="图片暂不可用"
              />
            </div>
            <div class="item-text">
              <p class="item-overline">
                {{ item.type === 'training' ? '训练内容' : '饮食内容' }}
                <span v-if="item.type === 'training'"> · {{ equipmentLabel(item.equipment) }}</span>
              </p>
              <h2 class="item-title">{{ item.title }}</h2>
              <p class="item-meta num">{{ itemMeta(item) }}</p>
              <p class="item-summary">{{ item.summary }}</p>
              <div class="item-actions">
                <button
                  type="button"
                  class="expand-btn"
                  :aria-expanded="ui.expandedId === item.id"
                  :aria-controls="`content-${item.id}`"
                  @click="toggleExpanded(item.id)"
                >
                  {{ ui.expandedId === item.id ? '收起内容' : '查看完整内容' }}
                  <ChevronDown
                    :size="16"
                    aria-hidden="true"
                    :class="{ 'expand-icon--open': ui.expandedId === item.id }"
                  />
                </button>
                <button
                  type="button"
                  class="bookmark-btn bookmark-btn--row"
                  :aria-label="isFavorite(item.id) ? `取消收藏 ${item.title}` : `收藏 ${item.title}`"
                  :aria-pressed="isFavorite(item.id)"
                  @click="toggleFavorite(item.id)"
                >
                  <BookmarkCheck v-if="isFavorite(item.id)" :size="18" aria-hidden="true" />
                  <Bookmark v-else :size="18" aria-hidden="true" />
                </button>
              </div>
            </div>
          </div>
          <Transition name="expand">
            <div v-if="ui.expandedId === item.id" :id="`content-${item.id}`" class="item-detail item-detail--row">
              <div class="fit-block">
                <h3 class="detail-title">{{ item.type === 'training' ? '适用与条件' : '适用说明' }}</h3>
                <ul class="fit-list">
                  <li v-for="line in item.fit" :key="line">{{ line }}</li>
                </ul>
              </div>
              <div class="content-block">
                <h3 class="detail-title">
                  {{ item.type === 'training' ? `训练内容 · ${item.contents.length} 个动作` : '内容原则' }}
                </h3>
                <ol class="content-list">
                  <li v-for="(line, index) in item.contents" :key="line.name" class="content-row">
                    <span class="content-index num">{{ String(index + 1).padStart(2, '0') }}</span>
                    <span class="content-name">{{ line.name }}</span>
                    <span class="content-detail num">{{ line.detail }}</span>
                  </li>
                </ol>
              </div>
              <p class="item-source">{{ item.source }}</p>
            </div>
          </Transition>
        </li>
      </ul>
    </template>

    <section v-else class="empty" aria-labelledby="empty-title">
      <Compass :size="30" aria-hidden="true" class="empty-icon" />
      <h2 id="empty-title" class="empty-title">{{ emptyTitle }}</h2>
      <p class="empty-text">
        <template v-if="activeFilterText">当前条件：{{ activeFilterText }}。</template>
        可以放宽条件，或看看全部内容。
      </p>
      <div class="empty-actions">
        <button type="button" class="btn btn--ghost btn--sm" @click="relaxFilters()">
          {{ emptyActionLabel }}
        </button>
        <button type="button" class="btn btn--sm" @click="resetFilters()">清除全部条件</button>
      </div>
    </section>
  </div>

  <BaseSheet :open="ui.sheet === 'filter'" title="筛选内容" @close="ui.sheet = null">
    <div v-for="group in filterGroups" :key="group.key" class="filter-group">
      <h3 class="filter-label">{{ group.label }}</h3>
      <div class="filter-options">
        <button
          v-for="option in group.options"
          :key="option.value"
          type="button"
          class="filter-option"
          :class="{ 'filter-option--active': ui.filterDraft[group.key as 'goal' | 'equipment' | 'duration'] === option.value }"
          :aria-pressed="ui.filterDraft[group.key as 'goal' | 'equipment' | 'duration'] === option.value"
          @click="ui.filterDraft[group.key as 'goal' | 'equipment' | 'duration'] = option.value"
        >
          {{ option.label }}
        </button>
      </div>
    </div>
    <template #footer>
      <div class="filter-footer">
        <button type="button" class="reset-filters" @click="ui.filterDraft.goal = 'all'; ui.filterDraft.equipment = 'all'; ui.filterDraft.duration = 'all'">
          重置
        </button>
        <button type="button" class="btn btn--accent" @click="applyFilterDraft()">
          查看 {{ filterDraftResultCount }} 份内容
        </button>
      </div>
    </template>
  </BaseSheet>
  </div>
</template>

<style scoped>
.explore {
  padding-top: var(--sp-3);
}

.explore-head {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: var(--sp-4);
}

.explore-title {
  font-size: clamp(1.75rem, 8vw, 2rem);
  line-height: 1.2;
  font-weight: 600;
  letter-spacing: -0.02em;
}

.explore-sub {
  margin-top: 2px;
  color: var(--muted);
  font-size: var(--text-13);
}

.explore-tools {
  display: flex;
  align-items: center;
  gap: var(--sp-1);
}

.tool-btn {
  width: 44px;
  height: 44px;
}

.filter-btn {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  min-height: 44px;
  padding: 0 12px;
  border: 1px solid var(--line-strong);
  border-radius: var(--radius-md);
  color: var(--ink);
  font-size: var(--text-13);
  transition: background-color 140ms var(--ease-out);
}

.filter-btn:hover {
  background: rgba(25, 47, 57, 0.04);
}

.filter-count {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  min-width: 18px;
  height: 18px;
  padding: 0 5px;
  border-radius: 999px;
  background: var(--ink);
  color: var(--surface);
  font-size: var(--text-12);
  line-height: 1;
}

.mode-tabs {
  display: flex;
  gap: var(--sp-6);
  margin-top: var(--sp-5);
  border-bottom: 1px solid var(--line);
}

.mode-tab {
  position: relative;
  min-width: 44px;
  min-height: 46px;
  color: var(--muted);
  font-size: var(--text-15);
  font-weight: 500;
  transition: color 140ms var(--ease-out);
}

.mode-tab--active {
  color: var(--ink);
  font-weight: 600;
}

.mode-tab--active::after {
  content: '';
  position: absolute;
  left: 0;
  right: 0;
  bottom: -1px;
  height: 2px;
  background: var(--ink);
}

.search-row {
  display: flex;
  align-items: center;
  gap: var(--sp-2);
  margin-top: var(--sp-3);
  padding: 0 var(--sp-3);
  border: 1px solid var(--line-strong);
  border-radius: var(--radius-md);
  background: var(--surface);
}

.search-icon {
  color: var(--muted);
  flex: none;
}

.search-input {
  width: 100%;
  min-height: 46px;
  border: 0;
  background: transparent;
  color: var(--ink);
  font-size: var(--text-15);
}

.search-input:focus {
  outline: none;
}

.search-row:focus-within {
  border-color: var(--ink);
}

.search-clear {
  width: 44px;
  height: 44px;
  flex: none;
}

.search-enter-active,
.search-leave-active {
  transition:
    opacity 180ms var(--ease-out),
    transform 180ms var(--ease-out);
}

.search-enter-from,
.search-leave-to {
  opacity: 0;
  transform: translateY(-4px);
}

.filter-summary {
  display: flex;
  align-items: baseline;
  justify-content: space-between;
  gap: var(--sp-3);
  margin-top: var(--sp-3);
  min-height: 24px;
}

.filter-text {
  color: var(--muted);
  font-size: var(--text-13);
  line-height: 1.5;
}

.filter-query {
  color: var(--ink);
}

.clear-link {
  min-height: 32px;
  color: var(--ink);
  font-size: var(--text-13);
  text-decoration: underline;
  text-underline-offset: 0.18em;
}

.featured {
  margin-top: var(--sp-4);
}

.featured-media {
  position: relative;
  aspect-ratio: 16 / 10;
  border-radius: var(--radius-lg);
  overflow: hidden;
  background: var(--ink);
}

.featured-top {
  position: absolute;
  inset: 12px 12px auto;
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: var(--sp-3);
}

.featured-badge {
  min-width: 0;
  display: inline-flex;
  align-items: center;
  min-height: 28px;
  padding: 0 9px;
  border-radius: var(--radius-sm);
  background: rgba(8, 18, 23, 0.62);
  color: var(--focus-text);
  font-size: var(--text-12);
  line-height: 1.3;
  letter-spacing: 0.01em;
}

.bookmark-btn {
  display: inline-flex;
  flex: 0 0 44px;
  align-items: center;
  justify-content: center;
  width: 44px;
  min-width: 44px;
  height: 44px;
  min-height: 44px;
  border-radius: var(--radius-md);
  background: rgba(8, 18, 23, 0.5);
  color: var(--focus-text);
  transition: background-color 140ms var(--ease-out);
}

.bookmark-btn:hover {
  background: rgba(8, 18, 23, 0.72);
}

.bookmark-btn--row {
  background: transparent;
  color: var(--muted);
  flex: none;
}

.bookmark-btn--row:hover {
  background: rgba(25, 47, 57, 0.05);
  color: var(--ink);
}

.featured-body {
  padding-top: var(--sp-4);
}

.item-overline {
  color: var(--muted);
  font-size: var(--text-12);
  line-height: 1.4;
  letter-spacing: 0.02em;
}

.featured-title {
  margin-top: 3px;
  font-size: clamp(1.5rem, 7vw, 1.875rem);
  line-height: 1.25;
  font-weight: 600;
  letter-spacing: -0.02em;
}

.item-meta {
  margin-top: 5px;
  color: var(--muted);
  font-size: var(--text-13);
  line-height: 1.5;
}

.item-summary {
  margin-top: 8px;
  max-width: 58ch;
  color: var(--ink);
  font-size: var(--text-14);
  line-height: 1.65;
}

.expand-btn {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  min-height: 44px;
  color: var(--ink);
  font-size: var(--text-13);
  font-weight: 500;
}

.expand-btn:hover {
  text-decoration: underline;
  text-underline-offset: 0.18em;
}

.expand-icon--open {
  transform: rotate(180deg);
}

.item-list {
  margin-top: var(--sp-5);
  border-top: 1px solid var(--line);
}

.item {
  border-bottom: 1px solid var(--line);
  padding: var(--sp-4) 0;
}

.item-main {
  display: flex;
  gap: var(--sp-4);
}

.item-thumb {
  flex: none;
  width: 92px;
  height: 92px;
  border-radius: var(--radius-md);
  overflow: hidden;
  background: var(--line);
}

.item-text {
  min-width: 0;
  flex: 1;
}

.item-title {
  margin-top: 2px;
  font-size: var(--text-17);
  line-height: 1.35;
  font-weight: 600;
}

.item-actions {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--sp-3);
  margin-top: 2px;
}

.item-detail {
  margin-top: var(--sp-4);
  padding-top: var(--sp-4);
  border-top: 1px solid var(--line);
}

.item-detail--row {
  margin-left: 0;
}

.detail-title {
  color: var(--ink);
  font-size: var(--text-13);
  font-weight: 600;
  line-height: 1.4;
}

.fit-block {
  margin-bottom: var(--sp-4);
}

.fit-list {
  margin-top: 6px;
  display: grid;
  gap: 3px;
}

.fit-list li {
  position: relative;
  padding-left: 13px;
  color: var(--muted);
  font-size: var(--text-13);
  line-height: 1.6;
}

.fit-list li::before {
  content: '';
  position: absolute;
  left: 0;
  top: 0.62em;
  width: 4px;
  height: 4px;
  border-radius: 50%;
  background: var(--line-strong);
}

.content-block {
  margin-bottom: var(--sp-3);
}

.content-list {
  margin-top: 6px;
  border-top: 1px solid var(--line);
}

.content-row {
  display: grid;
  grid-template-columns: 24px minmax(0, 1fr);
  gap: 2px var(--sp-3);
  padding: 8px 0;
  border-bottom: 1px solid var(--line);
}

.content-index {
  grid-row: span 2;
  color: var(--muted);
  font-size: var(--text-12);
  line-height: 1.5;
}

.content-name {
  color: var(--ink);
  font-size: var(--text-14);
  line-height: 1.5;
}

.content-detail {
  color: var(--muted);
  font-size: var(--text-13);
  line-height: 1.5;
}

.item-source {
  color: var(--muted);
  font-size: var(--text-12);
  line-height: 1.5;
}

.expand-enter-active,
.expand-leave-active {
  transition:
    opacity 180ms var(--ease-out),
    transform 180ms var(--ease-out);
}

.expand-enter-from,
.expand-leave-to {
  opacity: 0;
  transform: translateY(-4px);
}

.empty {
  margin-top: var(--sp-6);
  padding: var(--sp-7) var(--sp-5);
  border: 1px solid var(--line);
  border-radius: var(--radius-lg);
  background: var(--surface);
  text-align: left;
}

.empty-icon {
  color: var(--muted);
}

.empty-title {
  margin-top: var(--sp-3);
  font-size: var(--text-20);
  line-height: 1.35;
  font-weight: 600;
}

.empty-text {
  margin-top: var(--sp-2);
  max-width: 48ch;
  color: var(--muted);
  font-size: var(--text-14);
  line-height: 1.65;
}

.empty-actions {
  display: flex;
  flex-wrap: wrap;
  gap: var(--sp-2);
  margin-top: var(--sp-5);
}

.filter-group + .filter-group {
  margin-top: var(--sp-5);
}

.filter-label {
  margin-bottom: var(--sp-2);
  font-size: var(--text-14);
  font-weight: 600;
}

.filter-options {
  display: flex;
  flex-wrap: wrap;
  gap: var(--sp-2);
}

.filter-option {
  min-height: 44px;
  padding: 0 14px;
  border: 1px solid var(--line);
  border-radius: var(--radius-md);
  color: var(--ink);
  font-size: var(--text-14);
  transition:
    background-color 140ms var(--ease-out),
    border-color 140ms var(--ease-out);
}

.filter-option--active {
  border-color: var(--ink);
  background: var(--ink);
  color: var(--surface);
}

.filter-footer {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--sp-3);
}

.reset-filters {
  min-height: 44px;
  color: var(--muted);
  font-size: var(--text-14);
  text-decoration: underline;
  text-underline-offset: 0.18em;
}

@media (min-width: 768px) {
  .explore-title {
    font-size: 2.125rem;
  }

  .featured {
    display: grid;
    grid-template-columns: minmax(0, 1.5fr) minmax(260px, 0.8fr);
    gap: var(--sp-6);
    align-items: start;
    margin-top: var(--sp-5);
  }

  .featured-media {
    aspect-ratio: 16 / 11;
  }

  .featured-body {
    padding-top: 4px;
  }

  .item-list {
    display: grid;
    grid-template-columns: 1fr 1fr;
    column-gap: var(--sp-7);
    border-top: 1px solid var(--line);
  }

  .item {
    display: flex;
    flex-direction: column;
  }

  .item-thumb {
    width: 104px;
    height: 104px;
  }

  .item-detail--row {
    width: 100%;
  }
}

@media (min-width: 1100px) {
  .featured {
    grid-template-columns: minmax(0, 1.65fr) minmax(320px, 0.75fr);
    gap: var(--sp-8);
  }
}
</style>
