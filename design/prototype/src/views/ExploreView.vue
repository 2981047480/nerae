<script setup lang="ts">
import { computed } from 'vue'
import { Search, SlidersHorizontal, ArrowUpRight, Check } from '@lucide/vue'
import BaseSheet from '../components/BaseSheet.vue'
import ContentCard from '../components/ContentCard.vue'
import PhotoFrame from '../components/PhotoFrame.vue'
import { state, ui, filteredExplore, filteredCoaches, activeFilterCount, filterGroups, filterDraftResultCount, applyFilterDraft, openSheet, resetFilters, relaxFilters, setExploreMode, goToCoachDetail, applyPreviewState } from '../state/store'
import type { CoachProfile } from '../types'
const mode=computed(()=>state.explore.mode)
const loading=computed(()=>state.preview.explore==='loading')
const error=computed(()=>state.preview.explore==='error')
function price(coach: CoachProfile) {
  const pack = service(coach)
  return pack ? (pack.priceYuan > 0 ? `¥${pack.priceYuan} ${pack.priceUnit}` : '免费') : '查看服务'
}
const featured = computed(() => filteredExplore.value[0] ?? null)
const remaining = computed(() => filteredExplore.value.slice(1))
function service(coach: CoachProfile) { return coach.packages.find(pack => pack.visible && pack.priceYuan > 0) ?? coach.packages.find(pack => pack.visible) }
function cover(coach: CoachProfile) { return coach.id === 'he-an' ? '/assets/explore-stretch.webp' : coach.id === 'jiang-yao' ? '/assets/gym-interior.webp' : '/assets/training-dumbbells.webp' }
</script>
<template><div class="screen library-page studio-discover">
  <header class="studio-page-head"><div><p class="studio-kicker">发现 · 按自己的节奏开始</p><h1>{{ mode === 'coach' ? '找到愿意一起投入的教练。' : mode === 'diet' ? '吃好一点，慢慢成为习惯。' : '找到适合自己的练法。' }}</h1><p>免费计划、饮食灵感，与愿意认真回应的教练。</p></div></header>
  <nav class="library-tabs" role="tablist" aria-label="内容类型"><button v-for="tab in ([{key:'training',label:'训练'},{key:'diet',label:'饮食'},{key:'coach',label:'教练'}] as const)" :key="tab.key" type="button" role="tab" :aria-selected="mode===tab.key" @click="setExploreMode(tab.key)">{{ tab.label }}</button></nav>
  <div class="library-toolbar"><label v-if="mode!=='coach'" class="library-search"><Search :size="18"/><input v-model="ui.exploreQuery" type="search" aria-label="搜索训练或饮食内容" placeholder="搜索计划、动作或目标" /></label><span v-else class="library-count">找到 {{ filteredCoaches.length }} 位教练</span><button class="library-filter" type="button" @click="openSheet('filter')"><SlidersHorizontal :size="17"/>筛选<span v-if="activeFilterCount">{{ activeFilterCount }}</span></button></div>
  <div class="library-caption"><p>{{ mode==='coach'?'按服务内容了解教练':`免费${mode==='training'?'训练':'饮食'}计划 · ${filteredExplore.length} 份内容` }}</p><button v-if="activeFilterCount || ui.exploreQuery" type="button" class="link-btn" @click="resetFilters()">清除条件</button></div>
  <div v-if="loading" class="library-grid" aria-busy="true" aria-label="正在加载"><div v-for="n in 4" :key="n" class="library-skeleton"/></div>
  <div v-else-if="error" class="log-empty"><h2>内容暂时没有加载出来</h2><p>收藏和筛选仍保留。</p><button class="btn" type="button" @click="applyPreviewState('explore','normal')">重试加载</button></div>
  <template v-else-if="mode==='coach' && filteredCoaches.length">
    <ul class="studio-coach-grid"><li v-for="coach in filteredCoaches" :key="coach.id" class="studio-provider"><button class="studio-provider-open" type="button" :aria-label="`查看教练 ${coach.name}`" @click="goToCoachDetail(coach.id)"><div class="studio-provider-image"><PhotoFrame :src="cover(coach)" alt="训练内容示意，不代表教练本人或服务场地" /><span>{{ coach.modes[0] }}</span></div><div class="studio-provider-copy"><div class="studio-provider-name"><h2>{{ coach.name }}</h2><span class="num">{{ price(coach) }}</span></div><h3>{{ coach.title }}</h3><p>{{ service(coach)?.summary ?? coach.intro }}</p><ul v-if="service(coach)?.includes.length" class="studio-provider-includes"><li v-for="include in service(coach)?.includes" :key="include"><Check :size="13" aria-hidden="true" />{{ include }}</li></ul><div class="studio-provider-foot"><span>{{ coach.response }}</span><strong>了解服务<ArrowUpRight :size="16" aria-hidden="true" /></strong></div></div></button></li></ul>
    <p class="studio-photo-note">图片为训练内容示意；人物、服务与报价为本地样例。资质与认证尚未核验。</p>
  </template>
  <template v-else-if="mode!=='coach' && featured">
    <div class="studio-discover-feature"><ContentCard :item="featured" featured /></div>
    <header v-if="remaining.length" class="studio-section-head studio-catalog-head"><div><h2>{{ mode === 'diet' ? '把饮食放回日常' : '不同目标，都有起点' }}</h2><p class="studio-kicker">{{ remaining.length }} 份免费{{ mode === 'diet' ? '饮食' : '训练' }}内容，查看完整安排后再选择</p></div></header>
    <div class="studio-catalog-grid"><ContentCard v-for="item in remaining" :key="item.id" :item="item" /></div>
  </template>
  <div v-else class="log-empty"><h2>没有符合条件的{{ mode==='coach'?'教练':'计划' }}</h2><p>试试放宽条件，或换一个关键词。</p><div class="btn-row"><button class="btn btn--ghost" type="button" @click="relaxFilters()">放宽一个条件</button><button class="btn" type="button" @click="resetFilters()">查看全部</button></div></div>
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
</div></template>
