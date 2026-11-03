<script setup lang="ts">
import { onBeforeUnmount, onMounted, shallowRef } from 'vue'
import type { AppAuthRuntime } from '../../platform/app/nativeAuthRuntime'
import type { PublicSession } from './sessionCoordinator'
import { AuthEntryController, type AuthEntryState } from './authEntryController'

const props = withDefaults(defineProps<{ runtime: AppAuthRuntime; allowCancel?: boolean }>(), { allowCancel: true })
const emit = defineEmits<{ authenticated: [session: PublicSession]; cancel: [] }>()
const controller = new AuthEntryController(props.runtime, session => emit('authenticated', session))
const state = shallowRef<AuthEntryState>(controller.state)
controller.subscribe(next => { state.value = next })
let cancelEmitted = false

async function cancel(): Promise<void> {
  if (cancelEmitted) return
  const result = await controller.cancel()
  if (!result.leave || result.outcome === 'unavailable') return
  cancelEmitted = true
  controller.dispose()
  emit('cancel')
}

function inputValue(event: unknown): string {
  if (event === null || typeof event !== 'object' || !('detail' in event)) return ''
  const detail = event.detail
  return detail !== null && typeof detail === 'object' && 'value' in detail && typeof detail.value === 'string'
    ? detail.value
    : ''
}

onMounted(() => { void controller.start() })
onBeforeUnmount(() => controller.dispose())
</script>

<template>
  <view class="auth-entry">
    <view class="auth-entry__brand"><text class="auth-entry__mark">N</text><text>NERAE</text></view>
    <text class="auth-entry__eyebrow">安全恢复</text>
    <text class="auth-entry__title">记录由你掌握</text>
    <text class="auth-entry__intro">登录后可以安全保存和恢复训练记录。</text>

    <view v-if="state.status === 'restoring'" class="auth-entry__notice" aria-live="polite">
      <text>{{ state.message }}</text>
    </view>

    <view v-else-if="state.status === 'email' || state.status === 'sending_code' || state.status === 'retry_challenge'"
      class="auth-entry__form">
      <text class="auth-entry__label">邮箱</text>
      <input class="auth-entry__input" type="text" inputmode="email" autocomplete="email"
        placeholder="请输入邮箱地址" :value="state.email" :disabled="state.status !== 'email'"
        @input="controller.setEmail(inputValue($event))" />
      <text v-if="state.message" class="auth-entry__message" aria-live="polite">{{ state.message }}</text>
      <button class="auth-entry__primary" :disabled="state.status === 'sending_code' || !state.email"
        @click="controller.requestCode()">
        {{ state.status === 'sending_code' ? '正在发送…' : state.status === 'retry_challenge' ? '重试发送验证码' : '获取验证码' }}
      </button>
    </view>

    <view v-else-if="state.status === 'waiting_code' || state.status === 'submitting_code' || state.status === 'retry_login'"
      class="auth-entry__form">
      <text class="auth-entry__label">请查看此邮箱</text>
      <text class="auth-entry__email">{{ state.email }}</text>
      <text v-if="state.status === 'retry_login'" class="auth-entry__message">登录结果尚未确认。重新验证会继续处理当前验证码，不会再次发送邮件。</text>
      <input v-else class="auth-entry__input auth-entry__code" type="number" inputmode="numeric" autocomplete="one-time-code"
        placeholder="请输入邮箱验证码" :value="state.code" :disabled="state.status === 'submitting_code'"
        @input="controller.setCode(inputValue($event))" />
      <text v-if="state.message" class="auth-entry__message" aria-live="polite">{{ state.message }}</text>
      <button v-if="state.status === 'retry_login'" class="auth-entry__primary"
        @click="controller.submitCode()">重新验证</button>
      <button v-else class="auth-entry__primary" :disabled="state.status === 'submitting_code' || state.code.length < 6"
        @click="controller.submitCode()">
        {{ state.status === 'submitting_code' ? '正在验证…' : '登录并继续' }}
      </button>
      <button v-if="state.status === 'waiting_code'" class="auth-entry__secondary" @click="controller.resetLogin()">更换邮箱</button>
    </view>

    <view v-else-if="state.status === 'approval_required' || state.status === 'checking_approval'" class="auth-entry__notice">
      <text class="auth-entry__notice-title">需要已登录设备确认</text>
      <text class="auth-entry__comparison-label">请在已登录设备核对</text>
      <text class="auth-entry__comparison-code">{{ state.comparisonCode }}</text>
      <text>{{ state.message }}</text>
      <button class="auth-entry__primary" :disabled="state.status === 'checking_approval'" @click="controller.checkApproval()">
        {{ state.status === 'checking_approval' ? '正在检查…' : '检查确认状态' }}
      </button>
      <button class="auth-entry__secondary" :disabled="state.status === 'checking_approval'" @click="controller.resetLogin()">返回登录</button>
    </view>

    <view v-else-if="state.status === 'confirm_age' || state.status === 'confirming_age'" class="auth-entry__notice">
      <text class="auth-entry__notice-title">继续前请确认</text>
      <text>此声明由本人作出，不等同于证件核验。</text>
      <text v-if="state.message" class="auth-entry__message" aria-live="polite">{{ state.message }}</text>
      <button class="auth-entry__primary" :disabled="state.status === 'confirming_age'"
        @click="controller.confirmAdult()">
        {{ state.status === 'confirming_age' ? '正在确认…' : '我确认已满 18 周岁' }}
      </button>
    </view>

    <view v-else-if="state.status === 'authenticated'" class="auth-entry__notice auth-entry__success" aria-live="polite">
      <text class="auth-entry__notice-title">已安全登录</text>
      <text>可以继续回到刚才的任务。</text>
    </view>

    <view v-else-if="state.status === 'cancel_failed' || state.status === 'cancelling'" class="auth-entry__notice" aria-live="polite">
      <text class="auth-entry__notice-title">{{ state.status === 'cancelling' ? '正在安全取消' : '尚未确认安全取消' }}</text>
      <text>{{ state.message }}</text>
      <button v-if="state.status === 'cancel_failed' && state.cancelPurpose === 'restart'"
        class="auth-entry__primary" @click="controller.resetLogin()">重试安全清理并重新登录</button>
      <button v-else-if="state.status === 'cancel_failed'" class="auth-entry__primary" @click="cancel">重试安全取消</button>
    </view>

    <view v-else class="auth-entry__notice" aria-live="polite">
      <text class="auth-entry__notice-title">{{ state.status === 'session_changed' ? '登录状态已变化' : '暂时无法继续' }}</text>
      <text>{{ state.message }}</text>
      <button v-if="state.status === 'unavailable'" class="auth-entry__primary" @click="controller.retryRestore()">重新检查</button>
      <button v-else class="auth-entry__secondary" @click="controller.resetLogin()">重新开始登录</button>
    </view>
    <button v-if="allowCancel &amp;&amp; !['authenticated', 'cancel_failed', 'cancelling'].includes(state.status)"
      class="auth-entry__browse" @click="cancel">返回</button>
  </view>
</template>

<style scoped>
.auth-entry { padding: 28rpx 24rpx 32rpx; border-radius: 28rpx; background: #fff; color: #252c29; display: flex; flex-direction: column; gap: 16rpx; }
.auth-entry__brand { display: flex; align-items: center; gap: 10rpx; color: #42534a; font-size: 22rpx; font-weight: 700; letter-spacing: 3rpx; }
.auth-entry__mark { display: inline-flex; align-items: center; justify-content: center; width: 44rpx; height: 44rpx; border-radius: 14rpx; background: #e8eee8; color: #315647; font-size: 24rpx; }
.auth-entry__eyebrow { color: #707a73; font-size: 22rpx; letter-spacing: 2rpx; }
.auth-entry__title { font-size: 42rpx; line-height: 1.25; font-weight: 700; letter-spacing: -1rpx; }
.auth-entry__intro { color: #68716b; font-size: 26rpx; line-height: 1.65; }
.auth-entry__form, .auth-entry__notice { display: flex; flex-direction: column; gap: 18rpx; }
.auth-entry__label { color: #48544d; font-size: 24rpx; font-weight: 600; }
.auth-entry__input { box-sizing: border-box; width: 100%; height: 92rpx; padding: 0 24rpx; border: 1rpx solid #dce2dc; border-radius: 18rpx; background: #fbfcfa; font-size: 28rpx; }
.auth-entry__code { letter-spacing: 8rpx; }
.auth-entry__email { color: #315647; font-size: 25rpx; word-break: break-all; }
.auth-entry__message { color: #68716b; font-size: 23rpx; line-height: 1.5; }
.auth-entry__notice { padding: 24rpx; border-radius: 20rpx; background: #f2f5f1; color: #58645c; font-size: 25rpx; line-height: 1.55; }
.auth-entry__notice-title { color: #252c29; font-size: 29rpx; font-weight: 700; }
.auth-entry__comparison-label { color: #58645c; font-size: 23rpx; }
.auth-entry__comparison-code { color: #252c29; font-size: 44rpx; font-weight: 700; letter-spacing: 8rpx; }
.auth-entry__success { background: #eef5ef; }
.auth-entry__primary, .auth-entry__secondary { margin: 0; width: 100%; height: 92rpx; line-height: 92rpx; border-radius: 18rpx; font-size: 27rpx; font-weight: 650; }
.auth-entry__primary { border: 0; background: #ff9a75; color: #252c29; }
.auth-entry__primary[disabled] { opacity: .55; }
.auth-entry__secondary { border: 1rpx solid #cfd7d0; background: #fff; color: #35443b; }
.auth-entry__browse { margin: 2rpx 0 0; width: 100%; height: 72rpx; line-height: 72rpx; color: #58645c; font-size: 23rpx; background: transparent; }
button::after { border: 0; }
</style>
