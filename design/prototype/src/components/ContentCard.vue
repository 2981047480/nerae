<script setup lang="ts">
import { ArrowUpRight, Bookmark, BookmarkCheck } from '@lucide/vue'
import PhotoFrame from './PhotoFrame.vue'
import { openPlanFromExplore, state, toggleFavorite } from '../state/store'
import type { ExploreItem } from '../types'
defineProps<{ item: ExploreItem; featured?: boolean; hideSave?: boolean }>()
</script>
<template>
  <article class="content-card" :class="{ 'content-card--featured': featured }">
    <button type="button" class="content-card-open" :aria-label="`查看计划 ${item.title}`" @click="openPlanFromExplore(item.id)">
      <div class="content-card-image"><PhotoFrame :src="item.image" :alt="item.imageAlt" :eager="featured" /><span class="content-card-category">{{ item.type === 'diet' ? '饮食计划' : '训练计划' }}</span></div>
      <div class="content-card-copy"><p class="content-card-source">{{ item.source }}</p><h2>{{ item.title }}</h2><p class="content-card-description">{{ item.summary }}</p><div class="content-card-meta"><span>{{ item.countLabel }} · {{ item.perWeek }}</span><ArrowUpRight :size="19" aria-hidden="true" /></div></div>
    </button>
    <button v-if="!hideSave" type="button" class="content-card-save" :aria-label="`${state.favorites.includes(item.id) ? '取消收藏' : '收藏'} ${item.title}`" :aria-pressed="state.favorites.includes(item.id)" @click="toggleFavorite(item.id)"><BookmarkCheck v-if="state.favorites.includes(item.id)" :size="18" aria-hidden="true" /><Bookmark v-else :size="18" aria-hidden="true" /></button>
  </article>
</template>
