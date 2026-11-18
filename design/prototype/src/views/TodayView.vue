<script setup lang="ts">
import { computed } from 'vue'
import { ArrowRight, ArrowUpRight, Check, ChevronDown, ChevronRight, CalendarDays, FileText, MessageCircle } from '@lucide/vue'
import PhotoFrame from '../components/PhotoFrame.vue'
import ContentCard from '../components/ContentCard.vue'
import { EXPLORE_ITEMS, PREVIEW_DATE_LABEL, STUDENT_NAME } from '../data/sample'
import { state, todayFacts, sessionGroups, weekSummary, requestContinueTraining, goToMyPlan, goToDiet, goToRecords, goToExplore, goToIncomingPlan, goToCoachDetail, goToConversation, incomingDelivery, recoverTodayContent, recordRows, setExploreMode } from '../state/store'
const facts = todayFacts
const groups = sessionGroups
const special = computed(() => ['rest_day', 'cycle_end', 'unscheduled'].includes(state.todayMode))
const saved = computed(() => state.session.status === 'complete')
const recent = computed(() => recordRows.value.slice(0, 2))
const incoming = computed(() => state.student.incoming?.status === 'new' && incomingDelivery.value)
const relation = computed(() => state.relations.find(item => item.status === 'active') ?? null)
const coach = computed(() => state.coaches.find(item => item.id === relation.value?.coachId) ?? null)
const conversation = computed(() => state.conversations.find(item => item.relationId === relation.value?.id))
const feedback = computed(() => recordRows.value.find(item => item.feedback?.author === coach.value?.name))
const totalRecordedSets = computed(() => recordRows.value.reduce((sum, item) => sum + item.sets.length, 0))
const inspiration = EXPLORE_ITEMS.filter(item => item.status !== 'removed' && item.type === 'training').slice(0, 3)
function exploreCoaches(): void { goToExplore(); setExploreMode('coach') }
</script>
<template>
  <div class="screen studio-home">
    <header class="studio-page-head">
      <div><p class="studio-kicker">{{ PREVIEW_DATE_LABEL }} · {{ state.student.plan.cycle }}</p><h1>{{ STUDENT_NAME }}，今天练得如何？</h1><p>把训练记下来，让下一次更有方向。</p></div>
      <button class="studio-plan-link" type="button" @click="goToMyPlan"><CalendarDays :size="17" aria-hidden="true" />我的计划<ArrowUpRight :size="17" aria-hidden="true" /></button>
    </header>
    <div class="studio-main-grid">
      <div class="studio-training-column">
        <section class="studio-workout" aria-labelledby="workout-title">
          <div class="studio-workout-body">
            <p class="studio-workout-label">{{ special ? '按自己的节奏' : `${facts.planTitle} · 本次训练` }}</p>
            <h2 id="workout-title">{{ special ? (state.todayMode === 'rest_day' ? '今天，好好休息。' : state.todayMode === 'cycle_end' ? '一个周期的积累。' : '从自己的节奏开始。') : facts.title }}</h2>
            <p class="studio-workout-description">{{ special ? '可以补记训练，也可以为下一次做准备。' : `${facts.exerciseCount} 个动作 · ${facts.total} 组安排` }}</p>
            <span class="studio-record-status"><Check v-if="saved" :size="14" aria-hidden="true" />{{ saved ? `已保存 ${facts.done} 组实际记录` : facts.done ? `已记 ${facts.done} 组，草稿已保留` : '练完再记，按实际完成填写' }}</span>
            <div v-if="state.todayMode === 'offline'" class="studio-workout-error" role="alert"><p>计划暂不可用，草稿仍保留。</p><button type="button" class="btn" @click="recoverTodayContent">重新加载</button></div>
            <button v-else class="btn studio-record-cta journal-primary" type="button" @click="requestContinueTraining">{{ saved ? '查看 / 修改记录' : facts.done ? '继续填写' : '记录训练' }}<ArrowRight :size="18" aria-hidden="true" /></button>
            <p class="studio-record-help">训练后一起记 / 每组完成后记</p>
          </div>
          <div class="studio-workout-visual"><PhotoFrame src="/assets/hero-today.webp" alt="训练者完成上肢推举，训练内容示意" eager /><span>STRENGTH / 力量</span></div>
        </section>
        <details v-if="!special" class="studio-exercises"><summary><span>本次动作安排 <small>{{ groups.map(group => group.exercise.name).slice(0, 2).join('、') }}等</small></span><ChevronDown :size="16" aria-hidden="true" /></summary><div class="studio-exercise-list"><div v-for="(group, index) in groups" :key="group.exercise.id"><span class="num">{{ String(index + 1).padStart(2, '0') }}</span><strong>{{ group.exercise.name }}</strong><small>{{ group.sets.length }} 组 · {{ group.exercise.targetText }}</small></div></div></details>
        <section class="studio-week" aria-label="本周训练安排"><header><h2>本周节奏</h2><span>{{ weekSummary.label }}</span></header><div class="studio-week-days"><div v-for="day in weekSummary.days" :key="day.key" :class="{ 'is-today': day.key === 'sat', 'is-done': day.state === 'done' }"><span>{{ day.label.replace('周', '') }}</span><strong class="num">{{ day.date }}</strong><Check v-if="day.state === 'done'" :size="13" aria-label="已记录" /><i v-else :class="{ 'has-training': day.training }" /></div></div></section>
      </div>
      <aside class="studio-coach-panel">
        <template v-if="coach && relation">
          <div class="studio-coach-heading"><span class="studio-avatar" aria-hidden="true">{{ coach.name.slice(-1) }}</span><div><p>与你一起，持续调整</p><h2>{{ coach.name }} <small>你的教练</small></h2></div><button type="button" class="icon-btn" :aria-label="`查看${coach.name}的服务`" @click="goToCoachDetail(coach.id)"><ArrowUpRight :size="19" aria-hidden="true" /></button></div>
          <template v-if="feedback?.feedback"><p class="studio-feedback-date">最近的训练点评 · {{ feedback.dateLabel }}</p><blockquote>{{ feedback.feedback.text }}</blockquote><button type="button" class="studio-text-action" @click="goToRecords">回看这次训练<ArrowRight :size="16" aria-hidden="true" /></button></template>
          <template v-else><p class="studio-feedback-date">当前服务 · {{ relation.agreedCycle }}</p><h3>{{ relation.agreedName }}</h3><p class="studio-coach-description">这里会保留教练对训练的具体反馈，方便下次回看。</p></template>
          <button v-if="incoming" type="button" class="studio-delivery" @click="goToIncomingPlan()"><FileText :size="19" aria-hidden="true" /><span><strong>新的计划，等你看一看</strong><small>版本 {{ incomingDelivery?.version }} · 由你决定是否采用</small></span><ChevronRight :size="16" aria-hidden="true" /></button>
          <div class="studio-coach-footer"><span>{{ coach.response }}</span><button v-if="conversation" type="button" @click="goToConversation(conversation.id)"><MessageCircle :size="15" aria-hidden="true" />联系教练</button></div>
        </template>
        <template v-else><p class="studio-kicker">让训练更有方向</p><h2>需要的时候，找一位教练。</h2><p class="studio-coach-description">先看训练计划样例、服务内容与回复节奏，再决定是否咨询。</p><button type="button" class="studio-text-action" @click="exploreCoaches">了解教练与服务<ArrowRight :size="17" aria-hidden="true" /></button></template>
      </aside>
    </div>
    <div class="studio-secondary-grid">
      <section class="studio-history"><header class="studio-section-head"><div><p class="studio-kicker">每一次，都有迹可循</p><h2>你的训练积累</h2></div><button type="button" class="studio-text-action" @click="goToRecords">查看记录<ArrowUpRight :size="17" aria-hidden="true" /></button></header><div class="studio-history-summary"><p><strong class="num">{{ recordRows.length }}</strong><span>次训练记录</span></p><p><strong class="num">{{ totalRecordedSets }}</strong><span>组实际记录</span></p></div><div class="studio-history-rows"><button v-for="record in recent" :key="record.id" type="button" @click="goToRecords"><span>{{ record.dateLabel }}</span><strong>{{ record.dayTitle }}</strong><small>{{ record.sets.length }} 组</small><ChevronRight :size="15" aria-hidden="true" /></button><p v-if="!recent.length" class="muted">保存第一份记录，积累就从这里开始。</p></div></section>
      <button type="button" class="studio-diet" @click="goToDiet()"><div class="studio-diet-copy"><p class="studio-kicker">训练之外</p><h2>吃好，也是一种<br />长期的练习。</h2><p>{{ state.diet.report.status ? '今天的饮食自评已记录' : '查看饮食安排，简单记下今天的感受' }}</p><span>饮食计划与自评<ArrowUpRight :size="18" aria-hidden="true" /></span></div><PhotoFrame src="/assets/diet-featured.webp" alt="蔬菜沙拉与水果，饮食内容示意" /></button>
    </div>
    <section class="studio-inspiration"><header class="studio-section-head"><div><p class="studio-kicker">免费内容库</p><h2>为下一次训练，找一点灵感</h2></div><button type="button" class="studio-text-action" @click="goToExplore">探索全部<ArrowUpRight :size="18" aria-hidden="true" /></button></header><div class="studio-content-grid"><ContentCard v-for="item in inspiration" :key="item.id" :item="item" hide-save /></div></section>
    <p class="studio-preview-note">本地交互样机 · 计划、人物及服务为样例 · 记录保存在当前浏览器</p>
  </div>
</template>
