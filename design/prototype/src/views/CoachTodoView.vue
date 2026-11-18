<script setup lang="ts">
import { computed, ref } from 'vue'
import { ArrowRight, Check, RotateCcw, MessageCircle, ChevronRight } from '@lucide/vue'
import { state, openTodo, doneTodo, openTodoItem, completeTodo, restoreTodo, goToCoachStudents, healthAuthorizedFor } from '../state/store'
const filter=ref('all')
const labels:Record<string,string>={all:'全部',reply:'待回复',delivery:'待跟进',draft:'计划草稿',authorization:'授权',relation:'服务关系'}
const all=computed(()=>state.preview.coachTodo==='empty'?[]:openTodo.value)
const items=computed(()=>filter.value==='all'?all.value:all.value.filter(i=>i.reason===filter.value))
function privateItem(item:{healthRelated?:boolean;studentId:string}) {const s=state.students.find(s=>s.id===item.studentId);return Boolean(item.healthRelated&&s&&!healthAuthorizedFor(s))}
</script>
<template><div class="screen worklist-page">
  <header class="journal-head"><div><p class="eyebrow">顾言 · 教练</p><h1>待办</h1><p class="worklist-sub">{{ all.length ? `${all.length} 件事，等你接着处理。` : '今天的事情都处理好了。' }}</p></div><button class="journal-plan-link" type="button" @click="goToCoachStudents">查看学员<ChevronRight :size="16"/></button></header>
  <div class="worklist-columns"><section><nav class="worklist-filters" aria-label="筛选待办"><button v-for="key in ['all','reply','delivery','draft']" :key="key" type="button" :aria-pressed="filter===key" @click="filter=key">{{ labels[key] }}<span v-if="key==='all'" class="num">{{ all.length }}</span></button></nav>
    <ul v-if="items.length" class="worklist"><li v-for="item in items" :key="item.id"><div class="worklist-person"><span class="initial-avatar">{{ item.studentName.slice(-1) }}</span><span>{{ item.studentName }}</span><span class="small-label">{{ labels[item.reason] }}</span><time>{{ item.dueLabel }}</time></div><h2>{{ privateItem(item)?'健康相关内容未授权':item.title }}</h2><p>{{ privateItem(item)?'普通计划与服务仍可处理，健康内容需要单独授权。':item.detail }}</p><div class="worklist-actions"><button type="button" class="worklist-open" @click="openTodoItem(item.id)">{{ item.reason==='reply'?'回复学员':item.reason==='draft'?'继续编辑':'查看详情' }}<ArrowRight :size="16"/></button><button type="button" class="worklist-done" @click="completeTodo(item.id)"><Check :size="15"/>标记已处理</button></div></li></ul>
    <div v-else class="log-empty"><MessageCircle :size="28"/><h2>{{ all.length?'这类事项已处理完':'今天没有待办' }}</h2><p>有新的消息或计划变动时，会出现在这里。</p><button class="btn btn--ghost" type="button" @click="goToCoachStudents">查看学员</button></div>
  </section><aside class="worklist-aside"><h2>已处理 <span class="num">{{ doneTodo.length }}</span></h2><div v-for="item in doneTodo" :key="item.id" class="worklist-done-row"><span><strong>{{ item.studentName }}</strong><small>{{ privateItem(item)?'健康相关内容未授权':item.title }}</small></span><button type="button" :aria-label="`恢复 ${item.studentName} 的待办`" @click="restoreTodo(item.id)"><RotateCcw :size="16"/></button></div><p v-if="!doneTodo.length" class="muted">还没有已处理的事项。</p><p class="worklist-note">标记仅整理待办，消息和计划仍然保留。</p></aside></div>
</div></template>
