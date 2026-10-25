<script setup lang="ts">
import { computed } from 'vue'
import {
  ArrowRight,
  Check,
  Eye,
  EyeOff,
  Save,
  ShieldCheck,
} from '@lucide/vue'
import BaseSheet from '../components/BaseSheet.vue'
import { formatDateTime } from '../utils/format'
import {
  closeSheet,
  previewServiceAsStudent,
  saveServices,
  selectServicePackage,
  selectedServiceDraft,
  serviceAgreement,
  serviceDirty,
  serviceDraftList,
  servicePriceIssues,
  serviceValid,
  state,
  toggleServiceVisible,
  ui,
  updateServiceField,
} from '../state/store'

const drafts = serviceDraftList
const draft = selectedServiceDraft
const dirty = serviceDirty
const agreement = serviceAgreement

const packageTabs = computed(() =>
  drafts.value.map((item) => ({
    id: item.packageId,
    name:
      state.coaches
        .find((coach) => coach.id === 'gu-yan')
        ?.packages.find((pack) => pack.id === item.packageId)?.name ?? item.packageId,
    visible: item.visible,
  })),
)

const previewPackage = computed(() => draft.value)
const previewName = computed(
  () =>
    state.coaches
      .find((coach) => coach.id === 'gu-yan')
      ?.packages.find((pack) => pack.id === draft.value?.packageId)?.name ?? '服务包',
)

const failed = computed(() => state.preview.coachServices === 'failed')
const saving = computed(() => state.preview.coachServices === 'saving')
const priceError = computed(() =>
  draft.value ? servicePriceIssues.value[draft.value.packageId] ?? null : null,
)
const priceUnitOptions = ['/ 月', '/ 次', '/ 周', '/ 项']
const previewPrice = computed(() => {
  if (!previewPackage.value) return ''
  if (priceError.value) return '价格待修正'
  const value = previewPackage.value.priceYuan
  if (!value) return `免费 · ${previewPackage.value.cycle}`
  return `¥${value} ${previewPackage.value.priceUnit} · ${previewPackage.value.cycle}`.trim()
})

function save(): void {
  saveServices()
}
</script>

<template>
  <div class="screen screen--wide coach-services">
    <header class="screen-head">
      <div class="screen-heading">
        <p class="screen-kicker">顾言 · 教练 · 自主定价</p>
        <h1 class="screen-title">服务内容</h1>
        <p class="screen-sub">保存会更新学员看到的服务说明与价格；不会自动成交，平台内不收款。</p>
      </div>
      <div class="screen-tools">
        <span class="pill" :class="dirty ? 'pill--warning' : 'pill--positive'">
          {{ dirty ? '有未保存改动' : '已保存' }}
        </span>
      </div>
    </header>

    <div v-if="saving" class="banner" role="status">
      <div>
        <p class="banner-title">正在保存到本机</p>
        <p class="banner-body">保存完成后会显示新的保存时间。</p>
      </div>
    </div>

    <div v-if="failed" class="banner banner--error" role="alert">
      <div>
        <p class="banner-title">没有保存成功</p>
        <p class="banner-body">修改仍保留在页面中，可以重试；现有服务约定不受影响。</p>
      </div>
    </div>

    <div class="services-layout">
      <section class="service-editor" aria-labelledby="editor-title">
        <header class="group-head">
          <h2 id="editor-title" class="group-title">服务包</h2>
          <span class="group-meta num">{{ drafts.length }} 个服务包</span>
        </header>

        <div class="package-tabs" role="tablist" aria-label="服务包">
          <button
            v-for="pack in packageTabs"
            :key="pack.id"
            type="button"
            role="tab"
            class="package-tab"
            :class="{ 'package-tab--active': draft?.packageId === pack.id }"
            :aria-selected="draft?.packageId === pack.id"
            @click="selectServicePackage(pack.id)"
          >
            <span>{{ pack.name }}</span>
            <span class="package-tab-state">{{ pack.visible ? '学员可见' : '隐藏' }}</span>
          </button>
        </div>

        <div v-if="draft" class="service-form">
          <div class="form-head">
            <div>
              <h3 class="form-title">{{ previewName }}</h3>
              <p class="form-meta num">
                版本 {{ draft.version }} · 最后保存 {{ state.services.savedAt ? formatDateTime(state.services.savedAt) : '—' }}
              </p>
            </div>
            <span class="pill" :class="draft.visible ? 'pill--positive' : 'pill--neutral'">
              {{ draft.visible ? '学员可见' : '学员不可见' }}
            </span>
          </div>

          <label class="field">
            <span class="field-label">服务说明</span>
            <textarea
              class="textarea"
              :value="draft.description"
              @input="updateServiceField(draft.packageId, 'description', ($event.target as HTMLTextAreaElement).value)"
            />
          </label>

          <label class="field">
            <span class="field-label">具体范围</span>
            <textarea
              class="textarea"
              :value="draft.scope"
              @input="updateServiceField(draft.packageId, 'scope', ($event.target as HTMLTextAreaElement).value)"
            />
          </label>

          <div class="form-grid">
            <label class="field">
              <span class="field-label">适用目标</span>
              <input
                class="input"
                :value="draft.goalsText"
                @input="updateServiceField(draft.packageId, 'goalsText', ($event.target as HTMLInputElement).value)"
              />
            </label>
            <label class="field">
              <span class="field-label">响应约定</span>
              <input
                class="input"
                :value="draft.response"
                @input="updateServiceField(draft.packageId, 'response', ($event.target as HTMLInputElement).value)"
              />
            </label>
            <label class="field">
              <span class="field-label">价格（元）</span>
              <input
                class="input num"
                type="text"
                inputmode="numeric"
                :value="draft.priceInput"
                :aria-invalid="Boolean(priceError)"
                aria-describedby="price-help"
                @input="updateServiceField(draft.packageId, 'priceInput', ($event.target as HTMLInputElement).value)"
              />
              <span id="price-help" class="field-hint">0 表示免费；只能是整数</span>
              <span v-if="priceError" class="field-error">{{ priceError }}</span>
            </label>
            <label class="field">
              <span class="field-label">计价单位</span>
              <select
                class="select"
                :value="draft.priceUnit"
                @change="updateServiceField(draft.packageId, 'priceUnit', ($event.target as HTMLSelectElement).value)"
              >
                <option v-for="unit in priceUnitOptions" :key="unit" :value="unit">{{ unit }}</option>
              </select>
            </label>
            <label class="field">
              <span class="field-label">服务周期</span>
              <input
                class="input"
                :value="draft.cycle"
                @input="updateServiceField(draft.packageId, 'cycle', ($event.target as HTMLInputElement).value)"
              />
            </label>
          </div>

          <div class="visibility-row">
            <div>
              <p class="visibility-title">学员是否能看到这个服务包</p>
              <p class="visibility-note">隐藏只影响新的浏览；已确认的服务约定保持不变。</p>
            </div>
            <button
              type="button"
              class="switch"
              role="switch"
              :aria-checked="draft.visible"
              aria-label="学员可见"
              @click="toggleServiceVisible(draft.packageId)"
            >
              <span class="switch-knob" :class="{ 'switch-knob--on': draft.visible }" />
            </button>
          </div>

          <div class="form-actions">
            <button type="button" class="btn" :disabled="!dirty || !serviceValid" @click="save">
              <Save :size="16" aria-hidden="true" />
              保存服务内容
            </button>
            <button type="button" class="btn btn--ghost" @click="previewServiceAsStudent(draft.packageId)">
              <Eye :size="16" aria-hidden="true" />
              预览学员看到的
            </button>
          </div>
          <p v-if="ui.saveNote" class="save-note" role="status">{{ ui.saveNote }}</p>
        </div>
      </section>

      <aside class="services-side">
        <section class="side-panel" aria-labelledby="agreement-title">
          <h2 id="agreement-title" class="side-title">已有约定</h2>
          <template v-if="agreement && agreement.status === 'active'">
            <p class="agreement-name">林澈 · {{ agreement.agreedName }}</p>
            <dl class="kv">
              <dt>确认价格</dt>
              <dd class="num">¥{{ agreement.agreedPriceYuan }} {{ agreement.agreedPriceUnit }}</dd>
              <dt>服务周期</dt>
              <dd>{{ agreement.agreedCycle }}</dd>
              <dt>确认时间</dt>
              <dd class="num">{{ agreement.agreedAt.slice(0, 10) }}</dd>
              <dt>约定服务版本</dt>
              <dd class="num">版本 {{ agreement.serviceVersion }}</dd>
            </dl>
            <p class="side-note">
              <ShieldCheck :size="14" aria-hidden="true" />
              修改服务说明或价格不会改写这份约定；新价格只影响之后的咨询与确认。
            </p>
          </template>
          <p v-else class="side-copy">还没有已确认的服务约定。</p>
        </section>

        <section class="side-panel side-panel--quiet" aria-labelledby="preview-title">
          <h2 id="preview-title" class="side-title">学员看到什么</h2>
          <p class="side-copy">
            预览会展示服务内容、周期、价格语境与响应约定；不会创建关系、收款或发送消息。
          </p>
          <button v-if="draft" type="button" class="btn btn--block" @click="previewServiceAsStudent(draft.packageId)">
            <Eye :size="16" aria-hidden="true" />
            预览学员视图
          </button>
        </section>

        <section class="side-panel side-panel--quiet" aria-labelledby="boundary-title">
          <h2 id="boundary-title" class="side-title">边界</h2>
          <ul class="boundary-list">
            <li><Check :size="13" aria-hidden="true" /> 平台内不收款，续费不在站内自动发生</li>
            <li><Check :size="13" aria-hidden="true" /> 是否展示由“学员可见”控制；保存不会自动成交</li>
            <li><Check :size="13" aria-hidden="true" /> 服务确认与健康授权分开</li>
          </ul>
        </section>
      </aside>
    </div>

    <BaseSheet :open="ui.sheet === 'studentPreview'" title="学员看到的内容" @close="closeSheet">
      <div v-if="previewPackage" class="preview-card">
        <p class="preview-kicker">顾言 · 教练</p>
        <h3 class="preview-name">{{ previewName }}</h3>
        <p class="preview-price num">{{ previewPrice }}</p>
        <p class="preview-desc">{{ previewPackage.description }}</p>
        <dl class="kv">
          <dt>适用目标</dt>
          <dd>{{ previewPackage.goalsText }}</dd>
          <dt>具体范围</dt>
          <dd>{{ previewPackage.scope }}</dd>
          <dt>响应</dt>
          <dd>{{ previewPackage.response }}</dd>
          <dt>周期</dt>
          <dd>{{ previewPackage.cycle }}</dd>
          <dt>可见</dt>
          <dd>{{ previewPackage.visible ? '学员可见' : '当前隐藏' }}</dd>
        </dl>
        <p class="preview-note">
          <ArrowRight :size="14" aria-hidden="true" />
          学员可以带着这个服务包发起咨询；咨询不会自动建立服务关系。
        </p>
        <p class="preview-note preview-note--muted">
          <EyeOff :size="14" aria-hidden="true" />
          预览不会向学员发送通知；服务约定仍需双方确认。
        </p>
      </div>
      <template #footer>
        <div class="sheet-actions">
          <button type="button" class="btn btn--ghost" @click="closeSheet">关闭</button>
        </div>
      </template>
    </BaseSheet>
  </div>
</template>

<style scoped>
.coach-services {
  padding-bottom: calc(96px + env(safe-area-inset-bottom));
}

.services-layout {
  display: grid;
  gap: var(--sp-6);
  margin-top: var(--sp-4);
}

.package-tabs {
  display: flex;
  flex-wrap: wrap;
  gap: 4px;
}

.package-tab {
  display: flex;
  flex-direction: column;
  gap: 1px;
  min-height: 48px;
  padding: 6px 12px;
  border: 1px solid var(--line);
  border-radius: var(--radius-md);
  color: var(--muted);
  font-size: var(--text-13);
  text-align: left;
}

.package-tab--active {
  border-color: var(--ink);
  background: var(--ink);
  color: #fff;
}

.package-tab-state {
  font-size: var(--text-12);
}

.service-form {
  margin-top: var(--sp-4);
  padding-top: var(--sp-4);
  border-top: 1px solid var(--line);
}

.form-head {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: var(--sp-3);
  margin-bottom: var(--sp-3);
}

.form-title {
  font-size: var(--text-17);
  font-weight: 600;
}

.form-meta {
  color: var(--muted);
  font-size: var(--text-12);
}

.service-form .field + .field {
  margin-top: var(--sp-3);
}

.form-grid {
  margin-top: var(--sp-3);
}

.visibility-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--sp-4);
  margin-top: var(--sp-4);
  padding: var(--sp-3);
  border-radius: var(--radius-md);
  background: rgba(25, 47, 57, 0.04);
}

.visibility-title {
  font-size: var(--text-14);
  font-weight: 500;
}

.visibility-note {
  color: var(--muted);
  font-size: var(--text-12);
  line-height: 1.5;
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
  background: #fff;
  transition: transform 160ms var(--ease-out);
}

.switch-knob--on {
  transform: translateX(20px);
  background: var(--ink);
}

.switch[aria-checked='true']::before {
  background: var(--accent);
}

.form-actions {
  display: flex;
  flex-wrap: wrap;
  gap: var(--sp-2);
  margin-top: var(--sp-4);
}

.save-note {
  margin-top: var(--sp-2);
  color: var(--positive);
  font-size: var(--text-13);
}

.services-side {
  display: grid;
  gap: var(--sp-4);
  align-content: start;
}

.side-panel {
  padding: var(--sp-4);
  border: 1px solid var(--line);
  border-radius: var(--radius-lg);
  background: rgba(255, 255, 255, 0.6);
}

.side-title {
  margin-bottom: var(--sp-2);
  font-size: var(--text-15);
  font-weight: 600;
}

.agreement-name {
  font-size: var(--text-14);
  font-weight: 500;
}

.side-note {
  display: flex;
  align-items: flex-start;
  gap: 5px;
  margin-top: var(--sp-3);
  color: var(--positive);
  font-size: var(--text-12);
  line-height: 1.55;
}

.side-copy {
  color: var(--muted);
  font-size: var(--text-13);
  line-height: 1.6;
}

.boundary-list li {
  display: flex;
  align-items: flex-start;
  gap: 5px;
  padding: 4px 0;
  color: var(--muted);
  font-size: var(--text-12);
  line-height: 1.5;
}

.preview-card {
  padding: var(--sp-4);
  border-radius: var(--radius-md);
  background: rgba(25, 47, 57, 0.045);
}

.preview-kicker {
  color: var(--muted);
  font-size: var(--text-12);
}

.preview-name {
  margin-top: 1px;
  font-size: var(--text-18);
  font-weight: 600;
}

.preview-price {
  margin-top: 2px;
  color: var(--ink);
  font-size: var(--text-16);
  font-weight: 600;
}

.preview-desc {
  margin: var(--sp-2) 0;
  color: var(--muted);
  font-size: var(--text-13);
  line-height: 1.6;
}

.preview-note {
  display: flex;
  align-items: flex-start;
  gap: 5px;
  margin-top: var(--sp-2);
  color: var(--muted);
  font-size: var(--text-12);
  line-height: 1.55;
}

.preview-note--muted {
  color: var(--muted);
}

.sheet-actions {
  display: flex;
  justify-content: flex-end;
  gap: var(--sp-2);
}

@media (min-width: 768px) {
  .services-layout {
    grid-template-columns: minmax(0, 1.8fr) minmax(280px, 0.8fr);
    gap: var(--sp-8);
    align-items: start;
  }
}
</style>
