<script setup lang="ts">
import { onBeforeUnmount, shallowRef } from 'vue'
import { onBackPress } from '@dcloudio/uni-app'
import AuthEntry from '../../features/auth/AuthEntry.vue'
import DeviceApprovalPanel from '../../features/auth/DeviceApprovalPanel.vue'
import TrainingEntry from '../../features/training/TrainingEntry.vue'
import { AppEntrySession } from '../../features/auth/appEntrySession'
import type { PublicSession } from '../../features/auth/sessionCoordinator'
import type { AppAuthRuntime } from '../../platform/app/nativeAuthRuntime'
// #ifdef APP-PLUS
import { createAppAuthRuntime } from '../../platform/app/nativeAuthRuntime'
// #endif

let isApp = false
let runtime: AppAuthRuntime | null = null
// #ifdef APP-PLUS
isApp = true
try {
  runtime = createAppAuthRuntime(import.meta.env.VITE_NERAE_API_BASE ?? '')
} catch {
  // Never fall back to a development endpoint or downgrade transport security.
  runtime = null
}
// #endif

const authenticated = shallowRef<PublicSession | null>(null)
const sessionChanged = shallowRef(false)
const entryGeneration = shallowRef(0)
const trainingOpen = shallowRef(false)
const trainingTimeZone = shallowRef('')
const trainingEntry = shallowRef<InstanceType<typeof TrainingEntry> | null>(null)
const trainingError = shallowRef('')
const gate = runtime ? new AppEntrySession(runtime.session, onSessionChanged) : null

function onSessionChanged(): void {
  gate?.clear()
  trainingOpen.value = false
  trainingEntry.value = null
  authenticated.value = null
  sessionChanged.value = true
}

function startTraining(): void {
  const session = authenticated.value
  if (!runtime || !session || !runtime.session.isCurrent(session.subjectId, session.generation)) {
    onSessionChanged()
    return
  }
  try {
    const zone = Intl.DateTimeFormat().resolvedOptions().timeZone
    if (typeof zone !== 'string' || !zone || zone.length > 64) throw new Error('unavailable time zone')
    new Intl.DateTimeFormat('en', { timeZone: zone })
    trainingTimeZone.value = zone
    trainingError.value = ''
    trainingOpen.value = true
  } catch {
    trainingError.value = '暂时无法确定设备时区，请检查系统设置后重试。'
  }
}

function exitTraining(): void {
  if (!trainingEntry.value || !trainingEntry.value.canLeave()) return
  trainingOpen.value = false
  trainingEntry.value = null
}

onBackPress(() => {
  if (!trainingOpen.value) return false
  exitTraining()
  return true
})

function onAuthenticated(session: PublicSession): void {
  if (!gate?.accept(session)) {
    onSessionChanged()
    return
  }
  authenticated.value = session
  sessionChanged.value = false
}

onBeforeUnmount(() => gate?.dispose())
</script>

<template>
  <view class="entry-page" :class="{ 'entry-page--training': trainingOpen }">
    <view v-if="!runtime" class="entry-page__notice" role="alert">
      <text class="entry-page__title">暂时无法登录</text>
      <text>{{ isApp ? '服务暂不可用，请稍后再试。' : '当前平台的登录暂不可用。' }}</text>
    </view>
    <view v-else-if="sessionChanged" class="entry-page__notice" role="alert">
      <text class="entry-page__title">登录状态已变化</text>
      <text>请重新检查登录状态后继续。</text>
      <button class="entry-page__button" @click="sessionChanged = false">重新检查</button>
    </view>
    <AuthEntry v-else-if="!authenticated" :key="entryGeneration" :runtime="runtime" :allow-cancel="false"
      @authenticated="onAuthenticated" @cancel="entryGeneration++" />
    <TrainingEntry v-else-if="trainingOpen" ref="trainingEntry" :session="runtime.session" :time-zone="trainingTimeZone"
      @exit="exitTraining" @session-changed="onSessionChanged" />
    <view v-else class="entry-page__content">
      <view class="entry-page__notice">
        <text class="entry-page__title">已登录</text>
        <text>训练草稿保存在当前设备，尚未同步。</text>
        <text v-if="trainingError" role="alert">{{ trainingError }}</text>
        <button class="entry-page__button" @click="startTraining">开始记录</button>
      </view>
      <DeviceApprovalPanel :session="runtime.session" :approvals="runtime.approvals" @session-changed="onSessionChanged" />
    </view>
  </view>
</template>

<style scoped>
.entry-page { box-sizing: border-box; min-height: 100vh; padding: calc(48rpx + env(safe-area-inset-top)) 32rpx 48rpx; background: #f3f4f1; color: #252c29; }
.entry-page--training { padding: env(safe-area-inset-top) 0 0; }
.entry-page__content, .entry-page__notice { display: flex; flex-direction: column; gap: 18rpx; }
.entry-page__content { gap: 24rpx; }
.entry-page__notice { padding: 28rpx; border-radius: 24rpx; background: #fff; color: #606a63; font-size: 25rpx; line-height: 1.55; }
.entry-page__title { color: #252c29; font-size: 32rpx; font-weight: 700; }
.entry-page__button { margin: 0; width: 100%; min-height: 88rpx; border-radius: 18rpx; background: #f08b65; color: #252c29; font-size: 27rpx; }
.entry-page__button::after { border: 0; }
</style>
