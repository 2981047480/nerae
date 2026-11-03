<script setup lang="ts">
import { onBeforeUnmount, onMounted, shallowRef } from 'vue'
import type { ApprovalOwnerApi } from './approvalOwnerApi'
import { DeviceApprovalController } from './deviceApprovalController'
import type { AuthSessionCoordinator } from './sessionCoordinator'

const props = defineProps<{ approvals: ApprovalOwnerApi; session: AuthSessionCoordinator }>()
const emit = defineEmits<{ 'session-changed': [] }>()
const controller = new DeviceApprovalController(props.session, props.approvals)
const state = shallowRef(controller.state)
let notified = false
controller.subscribe(next => {
  state.value = next
  if (next.status === 'session_changed' && !notified) {
    notified = true
    emit('session-changed')
  }
})
onMounted(() => { void controller.start() })
onBeforeUnmount(() => controller.dispose())
</script>

<template>
  <view class="device-approval">
    <text class="device-approval__title">确认新设备</text>
    <text class="device-approval__message" aria-live="polite">{{ state.message }}</text>
    <view v-if="state.status === 'ready'" class="device-approval__list">
      <view v-for="item in state.items" :key="item.id" class="device-approval__item">
        <text>请核对新设备上的匹配码</text>
        <text class="device-approval__code">{{ item.comparisonCode }}</text>
        <button class="device-approval__primary" @click="controller.decide(item, 'approve', true)">匹配一致，允许登录</button>
        <button class="device-approval__secondary" @click="controller.decide(item, 'deny')">不是我，拒绝登录</button>
      </view>
      <button class="device-approval__secondary" @click="controller.refresh()">刷新</button>
    </view>
    <view v-else-if="state.status === 'retry' && state.pending" class="device-approval__item">
      <text>待确认操作：{{ state.pending.decision === 'approve' ? '允许登录' : '拒绝登录' }}</text>
      <text class="device-approval__code">{{ state.pending.comparisonCode }}</text>
      <button class="device-approval__primary" @click="controller.retry()">重试检查</button>
      <text>如果停止等待，之前的操作仍可能完成。</text>
      <button class="device-approval__secondary" @click="controller.stopWaiting(true)">停止等待</button>
    </view>
    <button v-else-if="state.status === 'done' || state.status === 'unavailable'"
      class="device-approval__secondary" @click="controller.refresh()">重新检查</button>
  </view>
</template>

<style scoped>
.device-approval, .device-approval__list, .device-approval__item { display: flex; flex-direction: column; gap: 18rpx; }
.device-approval { padding: 28rpx; border-radius: 24rpx; background: #fff; color: #252c29; }
.device-approval__title { font-size: 32rpx; font-weight: 700; }
.device-approval__message, .device-approval__item { color: #606a63; font-size: 25rpx; line-height: 1.55; }
.device-approval__item { padding: 24rpx; border-radius: 20rpx; background: #f3f4f1; }
.device-approval__code { color: #252c29; font-size: 42rpx; font-weight: 700; letter-spacing: 8rpx; }
.device-approval__primary, .device-approval__secondary { margin: 0; width: 100%; min-height: 88rpx; line-height: 1.4; padding: 24rpx; border-radius: 18rpx; font-size: 27rpx; }
.device-approval__primary { border: 0; background: #f08b65; color: #252c29; }
.device-approval__secondary { border: 1rpx solid #cfd7d0; background: #fff; color: #35443b; }
button::after { border: 0; }
</style>
