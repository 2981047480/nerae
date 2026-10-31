import { AuthApiError, type ApprovalRequired, type Authenticated, type Challenge, type EmailAuthApi } from '../../api/auth/emailAuthApi'
import type { DeviceIdentityCoordinator, ProtectedDeviceIdentity } from './deviceIdentityCoordinator'
import type { AuthSessionCoordinator, PublicSession, SessionInstallGuard } from './sessionCoordinator'

type ChallengeAttempt = { email: string; identity: ProtectedDeviceIdentity; key: string; guard: SessionInstallGuard }
type LoginAttempt = { challengeId: string; code: string; deviceProof: string; key: string }
type Proof = { forSubmission(): string }
export type EmailLoginAttemptHandle = object

export type EmailLoginFlowResult =
  | Readonly<{ status: 'waiting_code'; expiresAt: string }>
  | Readonly<{ status: 'authenticated'; session: PublicSession }>
  | Readonly<{ status: 'approval_required'; comparisonCode: string; expiresAt: string }>
  | Readonly<{ status: 'busy' | 'invalid_email' | 'invalid_code' | 'device_unavailable' | 'random_unavailable' |
      'retry_challenge' | 'retry_login' | 'rate_limited' | 'verification_rejected' | 'session_changed' | 'restart_required' | 'not_ready' }>

type AuthApiPort = Pick<EmailAuthApi, 'requestChallenge' | 'login'>
type DevicePort = Pick<DeviceIdentityCoordinator, 'initializeFirstForEmail'>
type SessionPort = Pick<AuthSessionCoordinator, 'captureInstallGuard' | 'isInstallGuardCurrent' |
  'subscribeInstallGuardInvalidation' | 'cancelInstallGuard' | 'installAuthenticatedIfCurrent' | 'isCurrent'>

/**
 * Coordinates a single in-memory email login. It retains an uncertain request's
 * exact body inputs and key for explicit retry; it does not persist OTPs or
 * approval secrets across process termination. A restart requires a new challenge.
 */
export class EmailLoginFlow {
  #api: AuthApiPort
  #devices: DevicePort
  #session: SessionPort
  #random: () => string
  #sign: (identity: ProtectedDeviceIdentity, challenge: Challenge) => Proof | null
  #busy = false
  #challengeAttempt: ChallengeAttempt | null = null
  #challenge: Challenge | null = null
  #loginAttempt: LoginAttempt | null = null
  #approval: ApprovalRequired | null = null
  #unwatchGuard: (() => void) | null = null
  #scope: { handle: EmailLoginAttemptHandle; guard: SessionInstallGuard | null; cancelling?: boolean;
    cancelPending?: Promise<'cancelled' | 'superseded' | 'unavailable'> } | null = null

  constructor(api: AuthApiPort, devices: DevicePort, session: SessionPort, random: () => string,
    sign: (identity: ProtectedDeviceIdentity, challenge: Challenge) => Proof | null) {
    if (!api || typeof api.requestChallenge !== 'function' || typeof api.login !== 'function' ||
      !devices || typeof devices.initializeFirstForEmail !== 'function' ||
      !session || typeof session.captureInstallGuard !== 'function' || typeof session.isInstallGuardCurrent !== 'function' ||
      typeof session.subscribeInstallGuardInvalidation !== 'function' || typeof session.cancelInstallGuard !== 'function' ||
      typeof session.installAuthenticatedIfCurrent !== 'function' ||
      typeof session.isCurrent !== 'function' || typeof random !== 'function' || typeof sign !== 'function') {
      throw new Error('email login dependencies unavailable')
    }
    this.#api = api
    this.#devices = devices
    this.#session = session
    this.#random = random
    this.#sign = sign
  }

  /** Reserve one caller-owned login attempt; handles contain no credentials. */
  openAttempt(): EmailLoginAttemptHandle | null {
    if (this.#busy || this.#scope || this.#challengeAttempt || this.#challenge || this.#loginAttempt || this.#approval) return null
    const handle = Object.freeze({})
    this.#scope = { handle, guard: null }
    return handle
  }

  /** Cancel only the matching attempt; the underlying HTTP request may still finish. */
  cancelAttempt(handle: EmailLoginAttemptHandle): Promise<'cancelled' | 'superseded' | 'unavailable'> {
    if (!this.#scope || this.#scope.handle !== handle) return Promise.resolve('superseded')
    const scope = this.#scope
    if (scope.cancelPending) return scope.cancelPending
    const guard = scope.guard
    this.#clearTransient()
    if (!guard) { this.#scope = null; return Promise.resolve('cancelled') }
    scope.cancelling = true
    const pending = this.#session.cancelInstallGuard(guard).catch(() => 'unavailable' as const).then(result => {
      if (this.#scope === scope) {
        scope.cancelPending = undefined
        if (result !== 'unavailable') this.#scope = null
      }
      return result
    })
    scope.cancelPending = pending
    return pending
  }

  /** Release a finished attempt without affecting an authenticated session. */
  releaseAttempt(handle: EmailLoginAttemptHandle): boolean {
    if (!this.#scope || this.#scope.handle !== handle || this.#scope.cancelling || this.#busy ||
      this.#challengeAttempt || this.#challenge || this.#loginAttempt || this.#approval) return false
    this.#scope = null
    return true
  }

  #allows(handle?: EmailLoginAttemptHandle): boolean {
    return this.#scope ? !this.#scope.cancelling && this.#scope.handle === handle : handle === undefined
  }

  /** Explicitly discard local transient state only when no request is in flight. */
  reset(): boolean {
    if (this.#busy) return false
    this.#clearTransient()
    return true
  }

  #clearTransient(): void {
    const unwatch = this.#unwatchGuard
    this.#unwatchGuard = null
    if (unwatch) unwatch()
    this.#challengeAttempt = null
    this.#challenge = null
    this.#loginAttempt = null
    this.#approval = null
  }

  #sessionChanged(): EmailLoginFlowResult {
    this.#clearTransient()
    return { status: 'session_changed' }
  }

  /** Begin once, then call retryChallenge if the outcome is unknown. */
  async requestCode(email: unknown, handle?: EmailLoginAttemptHandle): Promise<EmailLoginFlowResult> {
    if (!this.#allows(handle)) return { status: 'not_ready' }
    if (this.#busy) return { status: 'busy' }
    if (this.#challengeAttempt && !this.#session.isInstallGuardCurrent(this.#challengeAttempt.guard)) this.#clearTransient()
    if (this.#challengeAttempt || this.#challenge || this.#loginAttempt || this.#approval) return { status: 'restart_required' }
    if (typeof email !== 'string' || !email) return { status: 'invalid_email' }
    this.#busy = true
    try {
      const guard = this.#session.captureInstallGuard()
      if (!guard) return { status: 'session_changed' }
      if (this.#scope && this.#scope.handle === handle) this.#scope.guard = guard
      this.#unwatchGuard = this.#session.subscribeInstallGuardInvalidation(guard, () => this.#clearTransient())
      const device = await this.#devices.initializeFirstForEmail(email)
      if (!this.#session.isInstallGuardCurrent(guard)) return this.#sessionChanged()
      if (device.status === 'invalid_email') { this.#clearTransient(); return { status: 'invalid_email' } }
      if (device.status !== 'ready') { this.#clearTransient(); return { status: 'device_unavailable' } }
      const key = this.#newRequestKey()
      if (!key) { this.#clearTransient(); return { status: 'random_unavailable' } }
      this.#challengeAttempt = { email, identity: device.identity, key, guard }
      return await this.#sendChallenge()
    } catch { this.#clearTransient(); return { status: 'device_unavailable' } }
    finally { this.#busy = false }
  }

  async retryChallenge(handle?: EmailLoginAttemptHandle): Promise<EmailLoginFlowResult> {
    if (!this.#allows(handle)) return { status: 'not_ready' }
    if (this.#busy) return { status: 'busy' }
    if (!this.#challengeAttempt || this.#challenge || this.#loginAttempt || this.#approval) return { status: 'not_ready' }
    this.#busy = true
    try { return await this.#sendChallenge() }
    finally { this.#busy = false }
  }

  async submitCode(code: unknown, handle?: EmailLoginAttemptHandle): Promise<EmailLoginFlowResult> {
    if (!this.#allows(handle)) return { status: 'not_ready' }
    if (this.#busy) return { status: 'busy' }
    if (!this.#challengeAttempt || !this.#challenge || this.#approval) return { status: 'not_ready' }
    if (!this.#session.isInstallGuardCurrent(this.#challengeAttempt.guard)) return this.#sessionChanged()
    if (this.#loginAttempt) return { status: 'retry_login' }
    if (typeof code !== 'string' || !/^[0-9]{6,10}$/.test(code)) return { status: 'invalid_code' }
    this.#busy = true
    try {
      const proof = this.#sign(this.#challengeAttempt.identity, this.#challenge)
      if (!proof || typeof proof.forSubmission !== 'function') return { status: 'device_unavailable' }
      const deviceProof = proof.forSubmission()
      if (typeof deviceProof !== 'string' || deviceProof.length < 32 || deviceProof.length > 4096 ||
        !/^[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+$/.test(deviceProof)) return { status: 'device_unavailable' }
      const key = this.#newRequestKey()
      if (!key) return { status: 'random_unavailable' }
      this.#loginAttempt = { challengeId: this.#challenge.challengeId, code, deviceProof, key }
      return await this.#sendLogin()
    } catch { return { status: 'device_unavailable' } }
    finally { this.#busy = false }
  }

  async retryLogin(handle?: EmailLoginAttemptHandle): Promise<EmailLoginFlowResult> {
    if (!this.#allows(handle)) return { status: 'not_ready' }
    if (this.#busy) return { status: 'busy' }
    if (!this.#loginAttempt || this.#approval) return { status: 'not_ready' }
    this.#busy = true
    try { return await this.#sendLogin() }
    finally { this.#busy = false }
  }

  #newRequestKey(): string | null {
    try {
      const value = this.#random()
      // A 32-byte unpadded base64url encoding. Entropy is provided by the native port.
      const alphabet = 'ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_'
      return typeof value === 'string' && /^[A-Za-z0-9_-]{43}$/.test(value) && alphabet.indexOf(value[42]) % 4 === 0 ? value : null
    } catch { return null }
  }

  async #sendChallenge(): Promise<EmailLoginFlowResult> {
    const attempt = this.#challengeAttempt
    if (!attempt) return { status: 'not_ready' }
    if (!this.#session.isInstallGuardCurrent(attempt.guard)) return this.#sessionChanged()
    try {
      const challenge = await this.#api.requestChallenge({ email: attempt.email, devicePublicKey: attempt.identity.publicKeyForProtocol }, attempt.key)
      if (!this.#session.isInstallGuardCurrent(attempt.guard)) return this.#sessionChanged()
      this.#challenge = challenge
      return { status: 'waiting_code', expiresAt: challenge.expiresAt }
    } catch (error) {
      if (!this.#session.isInstallGuardCurrent(attempt.guard)) return this.#sessionChanged()
      if (error instanceof AuthApiError && error.code === 'rate_limited') return { status: 'rate_limited' }
      if (error instanceof AuthApiError && ['invalid_request', 'conflict', 'expired'].includes(error.code)) return { status: 'restart_required' }
      return { status: 'retry_challenge' }
    }
  }

  async #sendLogin(): Promise<EmailLoginFlowResult> {
    const attempt = this.#loginAttempt
    const challengeAttempt = this.#challengeAttempt
    if (!attempt || !challengeAttempt) return { status: 'not_ready' }
    if (!this.#session.isInstallGuardCurrent(challengeAttempt.guard)) return this.#sessionChanged()
    let outcome: Authenticated | ApprovalRequired
    try {
      outcome = await this.#api.login({ challengeId: attempt.challengeId, code: attempt.code, deviceProof: attempt.deviceProof }, attempt.key)
    } catch (error) {
      if (!this.#session.isInstallGuardCurrent(challengeAttempt.guard)) return this.#sessionChanged()
      if (error instanceof AuthApiError && error.code === 'unauthorized') {
        this.#loginAttempt = null
        return { status: 'verification_rejected' }
      }
      if (error instanceof AuthApiError && error.code === 'rate_limited') return { status: 'rate_limited' }
      if (error instanceof AuthApiError && ['invalid_request', 'conflict', 'expired'].includes(error.code)) return { status: 'restart_required' }
      return { status: 'retry_login' }
    }
    if (!this.#session.isInstallGuardCurrent(challengeAttempt.guard)) return this.#sessionChanged()
    if (outcome.status === 'approval_required') {
      this.#approval = outcome
      this.#loginAttempt = null
      return { status: 'approval_required', comparisonCode: outcome.comparisonCode, expiresAt: outcome.expiresAt }
    }
    try {
      const installed = await this.#session.installAuthenticatedIfCurrent(outcome, challengeAttempt.guard)
      if (installed.status === 'superseded') return this.#sessionChanged()
      if (installed.status !== 'authenticated') return { status: 'retry_login' }
      if (!this.#session.isCurrent(installed.session.subjectId, installed.session.generation)) return this.#sessionChanged()
      this.#clearTransient()
      return { status: 'authenticated', session: installed.session }
    } catch {
      return this.#session.isInstallGuardCurrent(challengeAttempt.guard) ? { status: 'retry_login' } : this.#sessionChanged()
    }
  }
}
