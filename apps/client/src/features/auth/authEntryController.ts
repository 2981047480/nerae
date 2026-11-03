import type { AppAuthRuntime } from '../../platform/app/nativeAuthRuntime'
import type { EmailLoginAttemptHandle } from './emailLoginFlow'
import type { PublicSession } from './sessionCoordinator'

export type AuthEntryStatus = 'restoring' | 'email' | 'sending_code' | 'waiting_code' | 'retry_challenge' |
  'submitting_code' | 'retry_login' | 'approval_required' | 'checking_approval' | 'confirm_age' | 'confirming_age' |
  'unavailable' | 'cancel_failed' | 'cancelling' | 'session_changed' | 'authenticated'

export interface AuthEntryState {
  readonly status: AuthEntryStatus
  readonly email: string
  readonly code: string
  readonly message: string
  readonly expiresAt?: string
  readonly comparisonCode?: string
  readonly cancelPurpose?: 'leave' | 'restart'
  readonly session?: PublicSession
}

type Runtime = Pick<AppAuthRuntime, 'session' | 'login' | 'ageDeclaration'>
type Listener = (state: AuthEntryState) => void
type CancelOutcome = 'cancelled' | 'superseded' | 'unavailable'
export interface AuthEntryCancelResult { readonly outcome: CancelOutcome; readonly leave: boolean }

// Keep only the opaque attempt handle across a component remount after an
// unavailable vault-cleanup result. OTP, proof, and request data stay in flow.
const attemptByLogin = new WeakMap<object, EmailLoginAttemptHandle>()

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
  #sessionChangedDuringOperation = false
  #watchInvalidatedByInstall = false
  #attempt: EmailLoginAttemptHandle | null = null
  #unknownAttempt = false
  #cancelRequested = false
  #cancelPromise: Promise<CancelOutcome> | null = null
  #cancelMode: 'leave' | 'restart' | null = null
  #unwatch: (() => void) | null = null

  constructor(runtime: Runtime, onAuthenticated: (session: PublicSession) => void = () => undefined) {
    if (!runtime?.session || typeof runtime.session.restore !== 'function' ||
      typeof runtime.session.snapshot !== 'function' || typeof runtime.session.isCurrent !== 'function' ||
      typeof runtime.session.captureInstallGuard !== 'function' ||
      typeof runtime.session.subscribeInstallGuardInvalidation !== 'function' ||
      !runtime.login || typeof runtime.login.requestCode !== 'function' ||
      typeof runtime.login.retryChallenge !== 'function' || typeof runtime.login.submitCode !== 'function' ||
      typeof runtime.login.retryLogin !== 'function' || typeof runtime.login.openAttempt !== 'function' ||
      typeof runtime.login.cancelAttempt !== 'function' || typeof runtime.login.releaseAttempt !== 'function' ||
      !runtime.ageDeclaration || typeof runtime.ageDeclaration.declare !== 'function' ||
      typeof onAuthenticated !== 'function') throw new Error('authentication entry unavailable')
    this.#runtime = runtime
    this.#onAuthenticated = onAuthenticated
    this.#attempt = attemptByLogin.get(runtime.login) ?? null
    if (this.#attempt) {
      this.#cancelMode = 'leave'
      this.#state = Object.freeze({ ...initialState('cancel_failed', '上次界面退出前未能确认本机凭据已清理。请重试安全取消。'), cancelPurpose: 'leave' })
    }
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
    if (this.#attempt) {
      this.#set({ ...initialState('cancel_failed', '上次界面退出前未能确认本机凭据已清理。请重试安全取消。'), cancelPurpose: 'leave' })
      return
    }
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
    const handle = this.#attempt ?? (retrying ? null : this.#runtime.login.openAttempt())
    if (!handle) {
      this.#unknownAttempt = true
      this.#set({ ...this.#state, status: 'unavailable', message: '登录尝试暂不可用。请重试安全取消或重新打开此页面。' })
      return
    }
    this.#attempt = handle
    attemptByLogin.set(this.#runtime.login, handle)
    this.#pendingEmail = this.#state.email
    this.#sessionChangedDuringOperation = false
    this.#busy = true
    this.#watchSession()
    this.#set({ ...this.#state, status: 'sending_code', message: '正在安全请求验证码' })
    try {
      const result = retrying
        ? await this.#runtime.login.retryChallenge(handle)
        : await this.#runtime.login.requestCode(this.#pendingEmail, handle)
      if (this.#disposed || this.#cancelRequested) return
      if (this.#sessionChangedDuringOperation) { this.#sessionChanged(); return }
      const email = this.#pendingEmail
      if (result.status === 'waiting_code') {
        this.#set({ status: 'waiting_code', email, code: '', message: '如果邮件未到，请检查垃圾邮件；重试会继续处理当前验证码，避免重复发送。', expiresAt: result.expiresAt })
      } else if (result.status === 'retry_challenge') {
        this.#set({ status: 'retry_challenge', email, code: '', message: '发送结果暂不明确。可以重试发送验证码；系统会继续处理当前验证码，避免重复发送。' })
      } else if (result.status === 'rate_limited') {
        this.#set({ status: 'retry_challenge', email, code: '', message: '请求过于频繁，请稍后重试发送验证码；系统会继续处理当前验证码，避免重复发送。' })
      } else if (result.status === 'approval_required') {
        this.#approvalRequired(result.comparisonCode)
      } else if (result.status === 'session_changed') {
        this.#sessionChanged()
      } else if (result.status === 'restart_required') {
        const cancelled = await this.#cancelAttempt('restart')
        if (cancelled !== 'unavailable') this.#set({ ...initialState('email', this.#messageFor(result.status)), email })
      } else {
        if (['invalid_email', 'device_unavailable', 'random_unavailable'].includes(result.status)) {
          this.#releaseEmptyAttempt(handle)
        }
        this.#set({ status: 'email', email, code: '', message: this.#messageFor(result.status) })
      }
    } catch {
      if (!this.#disposed && !this.#cancelRequested) {
        if (this.#sessionChangedDuringOperation) this.#sessionChanged()
        else this.#set({ status: 'retry_challenge', email: this.#pendingEmail, code: '', message: '发送结果暂不明确。可以重试发送验证码；系统会继续处理当前验证码。' })
      }
    } finally {
      this.#pendingEmail = ''; this.#pendingCode = ''; this.#busy = false
      if (!this.#disposed && this.#state.status === 'session_changed' && this.#attempt && !this.#cancelRequested) this.#releaseAttempt(this.#attempt)
    }
  }

  async retryChallenge(): Promise<void> {
    if (this.#disposed || this.#state.status !== 'retry_challenge') return
    await this.requestCode()
  }

  async submitCode(): Promise<void> {
    if (this.#disposed || this.#busy || !['waiting_code', 'retry_login'].includes(this.#state.status)) return
    const retrying = this.#state.status === 'retry_login'
    const handle = this.#attempt
    if (!handle) {
      this.#set({ ...this.#state, status: 'unavailable', message: '登录尝试已失效。请重新打开此页面。' })
      return
    }
    this.#pendingEmail = this.#state.email
    this.#pendingCode = retrying ? '' : this.#state.code
    this.#sessionChangedDuringOperation = false
    this.#busy = true
    this.#set({ ...this.#state, status: 'submitting_code', message: '正在验证验证码' })
    try {
      const result = retrying
        ? await this.#runtime.login.retryLogin(handle)
        : await this.#runtime.login.submitCode(this.#pendingCode, handle)
      if (this.#disposed || this.#cancelRequested) return
      if (this.#sessionChangedDuringOperation) { this.#sessionChanged(); return }
      const email = this.#pendingEmail
      if (result.status === 'authenticated') {
        if (!this.#releaseAttempt(handle)) {
          this.#set({ status: 'unavailable', email: '', code: '', message: '登录结果已验证，但安全交接未完成。请重新检查登录状态。' })
          return
        }
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
        this.#set({ status: 'retry_login', email, code: '', message: '登录结果尚未确认。请重新验证；不会再次发送验证码邮件。' })
      } else if (result.status === 'rate_limited') {
        this.#set({ status: 'retry_login', email, code: '', message: '登录请求过于频繁，请稍后重新验证；不会再次发送验证码邮件。' })
      } else if (result.status === 'restart_required') {
        const cancelled = await this.#cancelAttempt('restart')
        if (cancelled !== 'unavailable') this.#set({ ...initialState('email', this.#messageFor(result.status)), email })
      } else if (result.status === 'session_changed') {
        this.#sessionChanged()
      } else {
        if (['invalid_email', 'device_unavailable', 'random_unavailable'].includes(result.status)) {
          this.#releaseEmptyAttempt(handle)
        }
        this.#set({ status: 'waiting_code', email,
          code: '', message: this.#messageFor(result.status), expiresAt: this.#state.expiresAt })
      }
    } catch {
      if (!this.#disposed && !this.#cancelRequested) {
        if (this.#sessionChangedDuringOperation) this.#sessionChanged()
        else this.#set({ status: 'retry_login', email: this.#pendingEmail, code: '', message: '登录结果尚未确认。请重新验证；不会再次发送验证码邮件。' })
      }
    } finally {
      this.#pendingEmail = ''; this.#pendingCode = ''; this.#busy = false
      if (!this.#disposed && this.#state.status === 'session_changed' && this.#attempt && !this.#cancelRequested) this.#releaseAttempt(this.#attempt)
      else if (this.#watchInvalidatedByInstall && !this.#unwatch && this.#state.status !== 'session_changed') this.#watchSession()
    }
  }

  async checkApproval(): Promise<void> {
    if (this.#disposed || this.#busy || this.#state.status !== 'approval_required') return
    const handle = this.#attempt
    const comparisonCode = this.#state.comparisonCode
    if (!handle || typeof this.#runtime.login.checkApproval !== 'function') {
      this.#set({ ...this.#state, message: '当前无法查询设备确认状态，请重新登录。' })
      return
    }
    this.#sessionChangedDuringOperation = false
    this.#busy = true
    this.#set({ ...this.#state, status: 'checking_approval', message: '正在检查设备确认状态' })
    try {
      const result = await this.#runtime.login.checkApproval(handle)
      if (this.#disposed || this.#cancelRequested) return
      if (this.#sessionChangedDuringOperation) { this.#sessionChanged(); return }
      if (result.status === 'authenticated') {
        if (!this.#releaseAttempt(handle)) {
          this.#set(initialState('unavailable', '登录结果已验证，但安全交接未完成。请重新检查登录状态。'))
          return
        }
        const current = publicSession(this.#runtime, result.session.subjectId)
        if (!current || current.generation !== result.session.generation) { this.#sessionChanged(); return }
        this.#watchSession()
        this.#acceptSession(current)
      } else if (result.status === 'approval_denied' || result.status === 'approval_expired') {
        if (!this.#releaseAttempt(handle)) {
          this.#set(initialState('unavailable', '本次登录尝试暂时无法安全结束，请重新检查。'))
          return
        }
        this.#stopWatching()
        this.#set(initialState('email', result.status === 'approval_denied' ? '设备确认已拒绝，请重新登录。' : '设备确认已过期，请重新登录。'))
      } else if (result.status === 'session_changed') {
        this.#sessionChanged()
      } else if (result.status === 'restart_required') {
        await this.#cancelAttempt('restart')
      } else {
        const message = result.status === 'approval_pending' ? '尚未收到确认。请在已登录设备确认后再次检查。'
          : result.status === 'rate_limited' ? '检查过于频繁，请稍后重试。'
          : result.status === 'device_unavailable' || result.status === 'random_unavailable' ? '本机安全服务暂不可用，请稍后重试。'
          : '暂时无法确认结果，请重试检查。'
        this.#set({ status: 'approval_required', email: '', code: '', comparisonCode, message })
      }
    } catch {
      if (!this.#disposed && !this.#cancelRequested) {
        if (this.#sessionChangedDuringOperation) this.#sessionChanged()
        else this.#set({ status: 'approval_required', email: '', code: '', comparisonCode, message: '暂时无法确认结果，请重试检查。' })
      }
    } finally {
      this.#busy = false
      if (!this.#disposed && this.state.status === 'session_changed' && this.#attempt && !this.#cancelRequested) this.#releaseAttempt(this.#attempt)
      else if (this.#watchInvalidatedByInstall && !this.#unwatch && this.state.status !== 'session_changed') this.#watchSession()
    }
  }

  async confirmAdult(): Promise<void> {
    if (this.#disposed || this.#busy || this.#state.status !== 'confirm_age' || !this.#state.session) return
    const expected = this.#state.session
    this.#busy = true
    this.#set({ ...this.#state, status: 'confirming_age', message: '正在提交你的明确确认' })
    try {
      const result = await this.#runtime.ageDeclaration.declare(true)
      if (this.#disposed) return
      if (this.#sessionChangedDuringOperation) { this.#sessionChanged(); return }
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
      if (this.#disposed) return
      if (this.#sessionChangedDuringOperation) { this.#sessionChanged(); return }
      const current = publicSession(this.#runtime, expected.subjectId)
      if (!current) this.#sessionChanged()
      else this.#set({ status: 'confirm_age', email: '', code: '', session: current,
        message: '暂时无法确认声明结果。请检查当前状态后再继续。' })
    } finally {
      this.#busy = false
    }
  }

  resetLogin(): void {
    if (this.#disposed || this.#busy || (this.#cancelRequested && this.#cancelMode !== 'restart')) return
    if (this.#unknownAttempt) {
      this.#set({ status: 'cancel_failed', email: '', code: '', cancelPurpose: 'restart',
        message: '当前页面无法安全接管另一登录尝试。尚未清理，请重新打开页面。' })
      return
    }
    if (!this.#attempt) {
      this.#stopWatching()
      this.#set(initialState('email', '可以重新开始登录。'))
      return
    }
    this.#cancelMode = 'restart'
    void this.#cancelAttempt('restart')
  }

  /** Cancel only this UI's login attempt. An unavailable vault barrier stays retryable. */
  async cancel(): Promise<AuthEntryCancelResult> {
    const mode = this.#cancelMode ?? 'leave'
    const outcome = await this.#cancelAttempt(mode)
    return { outcome, leave: mode === 'leave' && outcome !== 'unavailable' }
  }

  async #cancelAttempt(mode: 'leave' | 'restart'): Promise<CancelOutcome> {
    if (this.#disposed) return 'unavailable'
    if (this.#cancelPromise) return this.#cancelPromise
    const handle = this.#attempt
    if (!handle) {
      if (this.#unknownAttempt) {
        this.#cancelMode = mode
        this.#set({ status: 'cancel_failed', email: '', code: '', cancelPurpose: mode,
          message: '另一登录尝试正在处理，当前页面无法安全接管。尚未返回，请重新打开后重试。' })
        return 'unavailable'
      }
      if (mode === 'leave') return 'cancelled'
      this.#set(initialState('email', '可以重新开始登录。'))
      return 'cancelled'
    }
    this.#cancelMode = mode
    this.#cancelRequested = true
    this.#pendingEmail = ''
    this.#pendingCode = ''
    this.#stopWatching()
    this.#set({ status: 'cancelling', email: '', code: '', cancelPurpose: mode, message: '正在安全清理本次登录尝试' })
    let pending: Promise<CancelOutcome>
    try { pending = Promise.resolve(this.#runtime.login.cancelAttempt(handle)) }
    catch { pending = Promise.resolve('unavailable') }
    this.#cancelPromise = pending
    let outcome: CancelOutcome
    try { outcome = await pending } catch { outcome = 'unavailable' }
    if (this.#cancelPromise === pending) this.#cancelPromise = null
    if (this.#disposed) return outcome
    if (outcome === 'unavailable') {
      this.#set({ status: 'cancel_failed', email: '', code: '', cancelPurpose: mode,
        message: '无法确认本机凭据已安全清理。请重试安全取消；尚未返回。' })
      return outcome
    }
    this.#forgetAttempt(handle)
    this.#attempt = null
    if (mode === 'restart') {
      this.#cancelRequested = false
      this.#cancelMode = null
      this.#set(initialState('email', '可以重新开始登录。'))
    }
    return outcome
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
      message: '请在已登录设备核对并确认，然后回来检查。' })
  }

  #watchSession(): void {
    if (this.#disposed || this.#unwatch) return
    const guard = this.#runtime.session.captureInstallGuard()
    if (!guard) return
    this.#watchInvalidatedByInstall = false
    try {
      this.#unwatch = this.#runtime.session.subscribeInstallGuardInvalidation(guard, () => {
        if (this.#disposed || this.#cancelRequested) return
        this.#unwatch = null
        const current = this.#state
        // A guarded login install invalidates the UI's older guard. Keep its
        // input until the flow reports whether this was its own install or a
        // competing session operation; an install failure must remain retryable.
        if (this.#busy && (current.status === 'submitting_code' || current.status === 'checking_approval') && this.#runtime.session.state === 'installing') {
          this.#watchInvalidatedByInstall = true
          return
        }
        if (this.#busy) this.#sessionChangedDuringOperation = true
        this.#sessionChanged()
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
    this.#watchInvalidatedByInstall = false
    if (!this.#busy && this.#attempt) this.#releaseAttempt(this.#attempt)
    this.#set(initialState('session_changed', '登录状态已变化，请重新验证。'))
  }

  #releaseAttempt(handle: EmailLoginAttemptHandle): boolean {
    let released = false
    try { released = this.#runtime.login.releaseAttempt(handle) } catch { released = false }
    if (released) {
      this.#forgetAttempt(handle)
      if (this.#attempt === handle) this.#attempt = null
    }
    return released
  }

  #releaseEmptyAttempt(handle: EmailLoginAttemptHandle): void {
    this.#releaseAttempt(handle)
  }

  #forgetAttempt(handle: EmailLoginAttemptHandle): void {
    if (attemptByLogin.get(this.#runtime.login) === handle) attemptByLogin.delete(this.#runtime.login)
  }

  /** Release UI/session subscriptions on unmount. A busy flow keeps its exact retry internally. */
  dispose(): Promise<void> {
    if (this.#disposed) return Promise.resolve()
    const handle = this.#attempt
    let cancellation: Promise<CancelOutcome> | null = null
    if (handle) {
      this.#cancelRequested = true
      if (this.#cancelPromise) {
        cancellation = this.#cancelPromise
        void cancellation.then(outcome => {
          if (outcome === 'unavailable') attemptByLogin.set(this.#runtime.login, handle)
          else this.#forgetAttempt(handle)
        }).catch(() => { attemptByLogin.set(this.#runtime.login, handle) })
      } else {
        let pending: Promise<CancelOutcome>
        try { pending = Promise.resolve(this.#runtime.login.cancelAttempt(handle)) }
        catch { pending = Promise.resolve('unavailable') }
        this.#cancelPromise = pending
        cancellation = pending
        void pending.then(outcome => {
          if (outcome === 'unavailable') attemptByLogin.set(this.#runtime.login, handle)
          else this.#forgetAttempt(handle)
        }).catch(() => { attemptByLogin.set(this.#runtime.login, handle) })
      }
    }
    this.#disposed = true
    this.#stopWatching()
    this.#pendingEmail = ''
    this.#pendingCode = ''
    this.#onAuthenticated = null
    this.#sessionChangedDuringOperation = false
    this.#watchInvalidatedByInstall = false
    this.#listeners.clear()
    this.#state = initialState('email')
    return cancellation ? cancellation.then(() => undefined, () => undefined) : Promise.resolve()
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
      case 'verification_rejected': return '验证码不正确或已失效，请检查后重试。'
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
