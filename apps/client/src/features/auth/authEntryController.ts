import type { AppAuthRuntime } from '../../platform/app/nativeAuthRuntime'
import type { PublicSession } from './sessionCoordinator'

export type AuthEntryStatus = 'restoring' | 'email' | 'sending_code' | 'waiting_code' | 'retry_challenge' |
  'submitting_code' | 'retry_login' | 'approval_required' | 'confirm_age' | 'confirming_age' |
  'unavailable' | 'session_changed' | 'authenticated'

export interface AuthEntryState {
  readonly status: AuthEntryStatus
  readonly email: string
  readonly code: string
  readonly message: string
  readonly expiresAt?: string
  readonly comparisonCode?: string
  readonly session?: PublicSession
}

type Runtime = Pick<AppAuthRuntime, 'session' | 'login' | 'ageDeclaration'>
type Listener = (state: AuthEntryState) => void

function initialState(status: AuthEntryStatus = 'restoring', message = ''): AuthEntryState {
  return Object.freeze({ status, email: '', code: '', message })
}

function publicSession(runtime: Runtime, expectedSubject?: string): PublicSession | null {
  try {
    const session = runtime.session.snapshot()
    if (!session || session.status !== 'authenticated' ||
      (expectedSubject !== undefined && session.subjectId !== expectedSubject) ||
      !runtime.session.isCurrent(session.subjectId, session.generation)) return null
    return session
  } catch { return null }
}

/** UI state adapter around the existing App authentication runtime; it owns no credentials. */
export class AuthEntryController {
  readonly #runtime: Runtime
  readonly #listeners = new Set<Listener>()
  #onAuthenticated: ((session: PublicSession) => void) | null
  #state = initialState()
  #started = false
  #busy = false
  #disposed = false
  #pendingEmail = ''
  #pendingCode = ''
  #unwatch: (() => void) | null = null

  constructor(runtime: Runtime, onAuthenticated: (session: PublicSession) => void = () => undefined) {
    if (!runtime?.session || typeof runtime.session.restore !== 'function' ||
      typeof runtime.session.snapshot !== 'function' || typeof runtime.session.isCurrent !== 'function' ||
      typeof runtime.session.captureInstallGuard !== 'function' ||
      typeof runtime.session.subscribeInstallGuardInvalidation !== 'function' ||
      !runtime.login || typeof runtime.login.requestCode !== 'function' ||
      typeof runtime.login.retryChallenge !== 'function' || typeof runtime.login.submitCode !== 'function' ||
      typeof runtime.login.retryLogin !== 'function' || typeof runtime.login.reset !== 'function' ||
      !runtime.ageDeclaration || typeof runtime.ageDeclaration.declare !== 'function' ||
      typeof onAuthenticated !== 'function') throw new Error('authentication entry unavailable')
    this.#runtime = runtime
    this.#onAuthenticated = onAuthenticated
  }

  get state(): AuthEntryState { return this.#state }

  subscribe(listener: Listener): () => void {
    if (this.#disposed) return () => undefined
    if (typeof listener !== 'function') throw new Error('authentication entry unavailable')
    this.#listeners.add(listener)
    listener(this.#state)
    return () => { this.#listeners.delete(listener) }
  }

  async start(): Promise<void> {
    if (this.#disposed || this.#started || this.#busy) return
    this.#started = true
    this.#busy = true
    this.#set(initialState('restoring', '正在安全检查已保存的登录状态'))
    try {
      const result = await this.#runtime.session.restore()
      if (result.status === 'authenticated') {
        const session = publicSession(this.#runtime, result.session.subjectId)
        if (!session || session.generation !== result.session.generation) {
          this.#set(initialState('unavailable', '暂时无法确认当前账号，请重试'))
          return
        }
        this.#watchSession()
        this.#acceptSession(session)
        return
      }
      if (result.status === 'signed_out') {
        this.#set(initialState('email', '输入邮箱以安全保存和恢复训练记录'))
        return
      }
      this.#set(initialState('unavailable', '暂时无法验证登录状态。请检查网络后重试；当前不会清除已保存凭据。'))
    } catch {
      this.#set(initialState('unavailable', '暂时无法验证登录状态。请稍后重试；当前不会清除已保存凭据。'))
    } finally { this.#busy = false }
  }

  setEmail(email: string): void {
    if (this.#disposed || this.#busy || !['email', 'waiting_code'].includes(this.#state.status)) return
    this.#set({ ...this.#state, email, message: '' })
  }

  setCode(code: string): void {
    if (this.#disposed || this.#busy || this.#state.status !== 'waiting_code') return
    this.#set({ ...this.#state, code: code.replace(/[^0-9]/g, '').slice(0, 10), message: '' })
  }

  async retryRestore(): Promise<void> {
    if (this.#disposed || this.#busy) return
    this.#started = false
    await this.start()
  }

  async requestCode(): Promise<void> {
    if (this.#disposed || this.#busy || !['email', 'retry_challenge'].includes(this.#state.status)) return
    const retrying = this.#state.status === 'retry_challenge'
    this.#pendingEmail = this.#state.email
    this.#busy = true
    this.#watchSession()
    this.#set({ ...this.#state, status: 'sending_code', message: '正在安全请求验证码' })
    try {
      const result = retrying
        ? await this.#runtime.login.retryChallenge()
        : await this.#runtime.login.requestCode(this.#pendingEmail)
      if (this.#disposed) return
      const email = this.#pendingEmail
      if (result.status === 'waiting_code') {
        this.#set({ status: 'waiting_code', email, code: '', message: '如果邮件未到，请检查垃圾邮件；重试会复用原请求。', expiresAt: result.expiresAt })
      } else if (result.status === 'retry_challenge') {
        this.#set({ status: 'retry_challenge', email, code: '', message: '请求结果暂不明确。可重试同一请求，不会另发一封。' })
      } else if (result.status === 'rate_limited') {
        this.#set({ status: 'retry_challenge', email, code: '', message: '请求过于频繁，请稍后重试同一验证码请求；邮箱和请求内容保持不变。' })
      } else if (result.status === 'approval_required') {
        this.#approvalRequired(result.comparisonCode)
      } else if (result.status === 'session_changed') {
        this.#sessionChanged()
      } else if (result.status === 'restart_required') {
        try { this.#runtime.login.reset() } catch { /* Keep state neutral. */ }
        this.#stopWatching()
        this.#set(initialState('email', this.#messageFor(result.status)))
      } else {
        this.#set({ status: 'email', email, code: '', message: this.#messageFor(result.status) })
      }
    } catch {
      if (!this.#disposed) this.#set({ status: 'retry_challenge', email: this.#pendingEmail, code: '', message: '请求结果暂不明确。可重试同一请求。' })
    } finally { this.#pendingEmail = ''; this.#pendingCode = ''; this.#busy = false }
  }

  async retryChallenge(): Promise<void> {
    if (this.#disposed || this.#state.status !== 'retry_challenge') return
    await this.requestCode()
  }

  async submitCode(): Promise<void> {
    if (this.#disposed || this.#busy || !['waiting_code', 'retry_login'].includes(this.#state.status)) return
    const retrying = this.#state.status === 'retry_login'
    this.#pendingEmail = this.#state.email
    this.#pendingCode = retrying ? '' : this.#state.code
    this.#busy = true
    this.#set({ ...this.#state, status: 'submitting_code', message: '正在验证验证码' })
    try {
      const result = retrying
        ? await this.#runtime.login.retryLogin()
        : await this.#runtime.login.submitCode(this.#pendingCode)
      if (this.#disposed) return
      const email = this.#pendingEmail
      if (result.status === 'authenticated') {
        const current = publicSession(this.#runtime, result.session.subjectId)
        if (!current || current.generation !== result.session.generation) {
          this.#sessionChanged()
          return
        }
        this.#watchSession()
        this.#acceptSession(current)
      } else if (result.status === 'approval_required') {
        this.#approvalRequired(result.comparisonCode)
      } else if (result.status === 'retry_login') {
        this.#set({ status: 'retry_login', email, code: '', message: '登录结果暂不明确。重试会复用原请求；请勿重新发送验证码。' })
      } else if (result.status === 'rate_limited') {
        this.#set({ status: 'retry_login', email, code: '', message: '登录请求过于频繁，请稍后重试同一登录请求；不会重新发送验证码。' })
      } else if (result.status === 'restart_required') {
        try { this.#runtime.login.reset() } catch { /* Keep state neutral. */ }
        this.#stopWatching()
        this.#set(initialState('email', this.#messageFor(result.status)))
      } else {
        this.#set({ status: result.status === 'session_changed' ? 'session_changed' : 'waiting_code', email,
          code: '', message: this.#messageFor(result.status), expiresAt: this.#state.expiresAt })
      }
    } catch {
      if (!this.#disposed) this.#set({ status: 'retry_login', email: this.#pendingEmail, code: '', message: '登录结果暂不明确。重试会复用原请求；请勿重新发送验证码。' })
    } finally { this.#pendingEmail = ''; this.#pendingCode = ''; this.#busy = false }
  }

  async confirmAdult(): Promise<void> {
    if (this.#disposed || this.#busy || this.#state.status !== 'confirm_age' || !this.#state.session) return
    const expected = this.#state.session
    this.#busy = true
    this.#set({ ...this.#state, status: 'confirming_age', message: '正在提交你的明确确认' })
    try {
      const result = await this.#runtime.ageDeclaration.declare(true)
      const current = publicSession(this.#runtime, expected.subjectId)
      if (!current) {
        this.#sessionChanged()
        return
      }
      if (['declared', 'already_declared'].includes(result.status) && current.user.ageVerified === true) {
        this.#acceptSession(current)
        return
      }
      this.#watchSession()
      const message = result.status === 'invalid_confirmation'
        ? '需要由你明确确认已满 18 周岁。'
        : result.status === 'version_conflict'
          ? '声明状态已变化，请查看当前状态后再确认。'
          : result.status === 'session_changed' || result.status === 'not_authenticated'
            ? '登录状态已变化，请重新验证。'
            : '暂时无法确认成年声明是否已保存。请重新检查状态。'
      this.#set({ status: 'confirm_age', email: '', code: '', session: current, message })
    } catch {
      const current = publicSession(this.#runtime, expected.subjectId)
      if (!current) this.#sessionChanged()
      else this.#set({ status: 'confirm_age', email: '', code: '', session: current,
        message: '暂时无法确认声明结果。请检查当前状态后再继续。' })
    } finally { this.#busy = false }
  }

  resetLogin(): void {
    if (this.#disposed || this.#busy) return
    try { this.#runtime.login.reset() } catch { /* Keep the UI neutral. */ }
    this.#stopWatching()
    this.#set(initialState('email', '可以重新开始登录。'))
  }

  #acceptSession(session: PublicSession): void {
    this.#watchSession()
    if (session.user.ageVerified === true) {
      this.#set({ status: 'authenticated', email: '', code: '', message: '登录成功。', session })
      this.#emit(session)
      return
    }
    this.#set({ status: 'confirm_age', email: '', code: '', message: '继续前请由你确认已满 18 周岁。', session })
  }

  #approvalRequired(comparisonCode: string): void {
    this.#set({ status: 'approval_required', email: '', code: '', comparisonCode,
      message: '此设备需要由已登录设备确认。当前暂不支持在这里完成批准，也不会创建登录会话。' })
  }

  #watchSession(): void {
    if (this.#disposed || this.#unwatch) return
    const guard = this.#runtime.session.captureInstallGuard()
    if (!guard) return
    try {
      this.#unwatch = this.#runtime.session.subscribeInstallGuardInvalidation(guard, () => {
        if (this.#disposed) return
        this.#pendingEmail = ''
        this.#pendingCode = ''
        this.#unwatch = null
        const current = this.#state
        if (this.#busy) this.#set({ ...current, email: '', code: '', message: '登录状态已变化，请重新验证。' })
        else this.#sessionChanged()
      })
    } catch { this.#unwatch = null }
  }

  #stopWatching(): void {
    const unwatch = this.#unwatch
    this.#unwatch = null
    if (unwatch) { try { unwatch() } catch { /* Best-effort cancellation. */ } }
  }

  #sessionChanged(): void {
    this.#stopWatching()
    this.#pendingEmail = ''
    this.#pendingCode = ''
    this.#set(initialState('session_changed', '登录状态已变化，请重新验证。'))
  }

  /** Release UI/session subscriptions on unmount. A busy flow keeps its exact retry internally. */
  dispose(): void {
    if (this.#disposed) return
    this.#disposed = true
    this.#stopWatching()
    this.#pendingEmail = ''
    this.#pendingCode = ''
    this.#onAuthenticated = null
    if (!this.#busy) {
      try { this.#runtime.login.reset() } catch { /* Best-effort removal of transient credentials. */ }
    }
    this.#listeners.clear()
    this.#state = initialState('email')
  }

  #emit(session: PublicSession): void {
    if (this.#disposed) return
    if (!this.#runtime.session.isCurrent(session.subjectId, session.generation) || session.user.ageVerified !== true) return
    const listener = this.#onAuthenticated
    if (!listener) return
    try { listener(session) } catch { /* Parent listeners cannot alter authentication state. */ }
  }

  #messageFor(status: string): string {
    switch (status) {
      case 'invalid_email': return '请检查邮箱地址。'
      case 'invalid_code': return '请输入有效验证码。'
      case 'rate_limited': return '请求过于频繁，请稍后再试。'
      case 'session_changed': return '登录状态已变化，请重新验证。'
      case 'restart_required': return '本次验证已失效，请重新请求验证码。'
      case 'verification_rejected': return '验证码无效或已过期，请重新请求。'
      case 'device_unavailable': return '本机安全设备暂不可用，未发送登录请求。'
      case 'random_unavailable': return '本机安全服务暂不可用，未发送请求。'
      default: return '服务暂不可用，请稍后重试。'
    }
  }

  #set(state: AuthEntryState): void {
    if (this.#disposed) return
    this.#state = Object.freeze({ ...state })
    for (const listener of [...this.#listeners]) { try { listener(this.#state) } catch { /* Isolate UI observers. */ } }
  }
}
