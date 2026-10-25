<script setup lang="ts">
import { computed } from 'vue'
import {
  ArrowRight,
  CalendarClock,
  Check,
  MapPin,
  MessageCircle,
  ShieldCheck,
} from '@lucide/vue'
import {
  goBack,
  openPlanFromExplore,
  prefillConsultation,
  selectedCoach,
  state,
  ui,
} from '../state/store'

const coach = selectedCoach

const relation = computed(() => state.relations.find((item) => item.coachId === coach.value?.id) ?? null)
const packages = computed(() => coach.value?.packages.filter((pack) => pack.visible) ?? [])
const activePackage = computed(
  () => packages.value.find((pack) => pack.id === ui.selectedPackageId) ?? packages.value[0] ?? null,
)
const unavailable = computed(() => state.preview.coachDetail === 'unavailable')

function selectPackage(id: string): void {
  ui.selectedPackageId = id
}

function consult(): void {
  if (!coach.value || !activePackage.value) return
  prefillConsultation(coach.value.id, activePackage.value.id)
}

function openSample(planId: string): void {
  openPlanFromExplore(planId)
}

function priceLabel(value: number, unit: string): string {
  if (!value) return '免费'
  return `¥${value}${unit ? ` ${unit}` : ''}`
}
</script>

<template>
  <div v-if="coach" class="screen screen--wide coach-detail">
    <section class="coach-hero">
      <div class="avatar coach-avatar" aria-hidden="true">{{ coach.name.slice(0, 1) }}</div>
      <div class="coach-identity">
        <p class="coach-kicker">教练 · {{ coach.modes.join(' · ') }}</p>
        <h1 class="coach-name">{{ coach.name }}</h1>
        <p class="coach-title">{{ coach.title }}</p>
        <p class="coach-intro">{{ coach.intro }}</p>
        <div class="coach-meta">
          <span class="meta-item"><CalendarClock :size="14" aria-hidden="true" />{{ coach.response }}</span>
          <span v-if="coach.city && !coach.supportsOnline" class="meta-item"><MapPin :size="14" aria-hidden="true" />{{ coach.city }} · 线下</span>
          <span v-else-if="coach.city" class="meta-item"><MapPin :size="14" aria-hidden="true" />{{ coach.city }} · 线下需确认</span>
        </div>
        <p class="coach-verification">{{ coach.verification }}</p>
      </div>
      <div class="coach-actions">
        <button class="btn btn--accent" type="button" :disabled="!activePackage" @click="consult">
          <MessageCircle :size="16" aria-hidden="true" />
          预约咨询
        </button>
        <p class="coach-action-note">咨询为站内文字；不会自动建立服务关系，也不收款。</p>
      </div>
    </section>

    <div v-if="unavailable" class="banner banner--warning" role="alert">
      <div>
        <p class="banner-title">外部联系渠道尚未核定</p>
        <p class="banner-body">可以继续使用站内咨询；服务范围与价格以当前说明为准。</p>
      </div>
    </div>

    <div class="coach-body">
      <section class="coach-main" aria-labelledby="services-title">
        <header class="group-head">
          <h2 id="services-title" class="group-title">服务与价格</h2>
          <span class="group-meta num">{{ packages.length }} 个服务包</span>
        </header>

        <div v-if="relation?.status === 'active'" class="agreement">
          <ShieldCheck :size="16" aria-hidden="true" />
          <p v-if="relation.agreedNeedsReview">
            你有一份已确认的服务约定<template v-if="relation.agreedPriceKnown">（¥{{ relation.agreedPriceYuan }}）</template>，{{ relation.agreedAtKnown ? `${relation.agreedAt.slice(0, 10)} 确认` : '确认时间需要核对' }}。
            原约定的价格、计价单位或周期需要核对；当前报价不会替换它。
          </p>
          <p v-else>
            你的当前约定：{{ relation.agreedName }} · ¥{{ relation.agreedPriceYuan }} {{ relation.agreedPriceUnit }} · {{ relation.agreedCycle }}（{{ relation.agreedAt.slice(0, 10) }} 确认）。
            服务说明或价格之后变化，不会改写已确认的约定。
          </p>
        </div>

        <div class="package-list" role="radiogroup" aria-label="选择服务包">
          <button
            v-for="pack in packages"
            :key="pack.id"
            type="button"
            role="radio"
            class="package"
            :class="{ 'package--active': activePackage?.id === pack.id }"
            :aria-checked="activePackage?.id === pack.id"
            @click="selectPackage(pack.id)"
          >
            <span class="package-head">
              <span class="package-name">{{ pack.name }}</span>
              <span class="package-price num">{{ priceLabel(pack.priceYuan, pack.priceUnit) }}</span>
            </span>
            <span class="package-summary">{{ pack.summary }}</span>
            <span class="package-meta num">{{ pack.cycle }} · {{ pack.goals.join(' / ') }}</span>
            <span v-if="pack.includes.length" class="package-includes">
              {{ pack.includes.join('；') }}
            </span>
          </button>
        </div>

        <section v-if="activePackage" class="service-sample" aria-labelledby="sample-title">
          <h2 id="sample-title" class="sample-title">可核对的交付样例</h2>
          <p class="sample-summary">{{ activePackage.sampleNote }}</p>
          <div v-if="activePackage.samplePlanId" class="sample-link-row">
            <button type="button" class="sample-link" @click="openSample(activePackage.samplePlanId!)">
              <span>
                <span class="sample-link-label">{{ coach.samples.find((sample) => sample.planId === activePackage?.samplePlanId)?.label ?? '查看完整训练日' }}</span>
                <span class="sample-link-note">{{ coach.samples.find((sample) => sample.planId === activePackage?.samplePlanId)?.note ?? '完整动作、组次与来源' }}</span>
              </span>
              <ArrowRight :size="16" aria-hidden="true" />
            </button>
          </div>
          <p class="sample-note">样例只说明交付内容，不承诺个人效果。</p>
        </section>

        <section class="other-samples" aria-labelledby="samples-title">
          <h2 id="samples-title" class="sample-title">其他样例</h2>
          <ul class="sample-list">
            <li v-for="sample in coach.samples" :key="sample.planId">
              <button type="button" class="sample-row" @click="openSample(sample.planId)">
                <span class="sample-row-label">{{ sample.label }}</span>
                <span class="sample-row-note">{{ sample.note }}</span>
                <ArrowRight :size="15" aria-hidden="true" />
              </button>
            </li>
          </ul>
        </section>
      </section>

      <aside class="coach-side">
        <section class="side-panel" aria-labelledby="response-title">
          <h2 id="response-title" class="side-title">沟通与响应</h2>
          <dl class="kv">
            <dt>方式</dt>
            <dd>{{ coach.modes.join(' · ') }}</dd>
            <dt>回复</dt>
            <dd>{{ activePackage?.response ?? coach.response }}</dd>
            <dt>适合</dt>
            <dd>{{ coach.focus.join(' · ') }}</dd>
            <dt>评价</dt>
            <dd>暂无足够评价</dd>
          </dl>
          <p class="side-note">咨询会带入当前选中的服务包，已知目标与时间可修改；备注可留空。</p>
          <button type="button" class="btn btn--block consult-secondary" :disabled="!activePackage" @click="consult">
            带着「{{ activePackage?.name ?? '服务包' }}」咨询
          </button>
        </section>

        <section class="side-panel side-panel--quiet" aria-labelledby="confirm-title">
          <h2 id="confirm-title" class="side-title">服务确认与授权</h2>
          <ul class="confirm-list">
            <li>
              <Check :size="14" aria-hidden="true" />
              服务确认由双方明确完成，不会因咨询自动建立关系
            </li>
            <li>
              <Check :size="14" aria-hidden="true" />
              健康内容授权另行确认，可单独撤回
            </li>
            <li>
              <Check :size="14" aria-hidden="true" />
              平台内不收款；服务约定由双方在站内确认
            </li>
          </ul>
        </section>
      </aside>
    </div>
  </div>

  <div v-else class="screen">
    <div class="empty-state">
      <h1 class="empty-title">没有找到这位教练</h1>
      <p class="empty-text">回到发现查看可比较的教练与服务。</p>
      <div class="empty-actions">
        <button type="button" class="btn" @click="goBack">返回</button>
      </div>
    </div>
  </div>
</template>

<style scoped>
.coach-detail {
  padding-top: 0;
  padding-bottom: calc(96px + env(safe-area-inset-bottom));
}

.coach-hero {
  display: grid;
  gap: var(--sp-4);
  padding: var(--sp-5) 0 var(--sp-6);
  border-bottom: 1px solid var(--line);
}

.coach-avatar {
  width: 64px;
  height: 64px;
  font-size: var(--text-24);
}

.coach-kicker {
  color: var(--muted);
  font-size: var(--text-12);
  letter-spacing: 0.02em;
}

.coach-name {
  margin-top: 1px;
  font-size: clamp(1.75rem, 6vw, 2.25rem);
  line-height: 1.15;
}

.coach-title {
  margin-top: 2px;
  color: var(--muted);
  font-size: var(--text-14);
}

.coach-intro {
  margin-top: var(--sp-3);
  max-width: 58ch;
  font-size: var(--text-15);
  line-height: 1.7;
}

.coach-meta {
  display: flex;
  flex-wrap: wrap;
  gap: var(--sp-2) var(--sp-4);
  margin-top: var(--sp-3);
}

.meta-item {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  color: var(--muted);
  font-size: var(--text-12);
}

.coach-verification {
  margin-top: var(--sp-3);
  color: var(--muted);
  font-size: var(--text-12);
  line-height: 1.55;
}

.coach-actions {
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  gap: 4px;
}

.coach-action-note {
  color: var(--muted);
  font-size: var(--text-12);
  line-height: 1.5;
}

.coach-body {
  display: grid;
  gap: var(--sp-6);
  margin-top: var(--sp-6);
}

.agreement {
  display: flex;
  align-items: flex-start;
  gap: 7px;
  padding: var(--sp-3);
  border-radius: var(--radius-md);
  background: rgba(39, 111, 83, 0.06);
  color: var(--positive);
  font-size: var(--text-13);
  line-height: 1.55;
}

.package-list {
  display: grid;
  gap: 6px;
  margin-top: var(--sp-3);
}

.package {
  display: flex;
  flex-direction: column;
  gap: 3px;
  padding: var(--sp-4);
  border: 1px solid var(--line);
  border-radius: var(--radius-lg);
  background: var(--surface);
  text-align: left;
}

.package--active {
  border-color: var(--ink);
  box-shadow: inset 0 0 0 1px var(--ink);
}

.package-head {
  display: flex;
  align-items: baseline;
  justify-content: space-between;
  gap: var(--sp-3);
}

.package-name {
  font-size: var(--text-16);
  font-weight: 600;
}

.package-price {
  flex: none;
  color: var(--ink);
  font-size: var(--text-16);
  font-weight: 600;
}

.package-summary {
  color: var(--muted);
  font-size: var(--text-13);
  line-height: 1.6;
}

.package-meta {
  color: var(--muted);
  font-size: var(--text-12);
}

.package-includes {
  margin-top: 3px;
  color: var(--muted);
  font-size: var(--text-12);
  line-height: 1.55;
}

.service-sample,
.other-samples {
  margin-top: var(--sp-5);
}

.sample-title {
  font-size: var(--text-15);
  font-weight: 600;
}

.sample-summary {
  margin-top: 3px;
  color: var(--muted);
  font-size: var(--text-13);
  line-height: 1.6;
}

.sample-link-row {
  margin-top: var(--sp-2);
}

.sample-link,
.sample-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--sp-3);
  width: 100%;
  padding: var(--sp-3);
  border: 1px solid var(--line);
  border-radius: var(--radius-md);
  text-align: left;
}

.sample-link-label,
.sample-row-label {
  display: block;
  font-size: var(--text-14);
  font-weight: 500;
}

.sample-link-note,
.sample-row-note {
  display: block;
  margin-top: 1px;
  color: var(--muted);
  font-size: var(--text-12);
  line-height: 1.45;
}

.sample-note {
  margin-top: var(--sp-2);
  color: var(--muted);
  font-size: var(--text-12);
  line-height: 1.55;
}

.sample-list {
  margin-top: var(--sp-2);
}

.sample-list li + li {
  margin-top: 6px;
}

.coach-side {
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
  margin-bottom: var(--sp-2);
  font-size: var(--text-16);
  font-weight: 600;
}

.side-note {
  margin-top: var(--sp-3);
  color: var(--muted);
  font-size: var(--text-12);
  line-height: 1.55;
}

.consult-secondary {
  margin-top: var(--sp-2);
}

.confirm-list li {
  display: flex;
  align-items: flex-start;
  gap: 6px;
  padding: 5px 0;
  color: var(--muted);
  font-size: var(--text-12);
  line-height: 1.55;
}

@media (min-width: 768px) {
  .coach-hero {
    grid-template-columns: auto minmax(0, 1fr) auto;
    align-items: flex-start;
    gap: var(--sp-5);
  }

  .coach-actions {
    align-items: flex-end;
  }

  .coach-body {
    grid-template-columns: minmax(0, 1.6fr) minmax(280px, 0.8fr);
    gap: var(--sp-8);
    align-items: start;
  }
}
</style>
