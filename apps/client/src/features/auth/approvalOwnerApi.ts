import type { AuthSessionCoordinator } from './sessionCoordinator'

export interface ApprovalOwnerRequest {
  url: string
  method: 'GET' | 'POST'
  header: Record<string, string>
  data?: string
}
export interface ApprovalOwnerResponse {
  statusCode: number
  headers?: Readonly<Record<string, string>>
  data: unknown
}
export type ApprovalOwnerTransport = (request: ApprovalOwnerRequest) => Promise<ApprovalOwnerResponse>
export interface PendingDeviceApproval {
  readonly id: string
  readonly comparisonCode: string
  readonly createdAt: string
  readonly expiresAt: string
}
export type ApprovalListResult =
  | Readonly<{ status: 'ready'; items: readonly PendingDeviceApproval[] }>
  | Readonly<{ status: 'not_authenticated' | 'session_changed' | 'unavailable' | 'decision_pending' | 'busy' }>
export type ApprovalDecisionResult = Readonly<{ status: 'approved' | 'denied' | 'not_authenticated' | 'session_changed' |
  'invalid_confirmation' | 'invalid_item' | 'decision_pending' | 'retry' | 'rate_limited' | 'needs_review' | 'busy' | 'unavailable' }>
export type PendingApprovalDecision = Readonly<{ decision: 'approve' | 'deny'; comparisonCode: string }>

type SessionPort = Pick<AuthSessionCoordinator, 'current' | 'isCurrent' | 'captureInstallGuard' |
  'subscribeInstallGuardInvalidation'>
type ItemBinding = { subjectId: string; generation: string; revision: number }
type PendingDecision = {
  subjectId: string
  generation: string
  accessToken: string
  path: string
  body: string
  key: string
  expected: 'approved' | 'denied'
  decision: 'approve' | 'deny'
  comparisonCode: string
  unwatch: (() => void) | null
}

function object(value: unknown): value is Record<string, unknown> {
  return value !== null && typeof value === 'object' && !Array.isArray(value)
}
function exactKeys(value: Record<string, unknown>, keys: string[]): boolean {
  const actual = Object.keys(value)
  return actual.length === keys.length && keys.every(key => Object.prototype.hasOwnProperty.call(value, key))
}
function validDate(value: unknown): value is string {
  if (typeof value !== 'string') return false
  const match = /^(\d{4}-\d{2}-\d{2})[Tt](\d{2}):(\d{2}):(\d{2})(?:\.\d+)?([Zz]|[+-](\d{2}):(\d{2}))$/.exec(value)
  if (!match || Number(match[2]) > 23 || Number(match[3]) > 59 || Number(match[4]) > 59 ||
    (match[6] !== undefined && (Number(match[6]) > 23 || Number(match[7]) > 59))) return false
  const day = new Date(`${match[1]}T00:00:00.000Z`)
  return !Number.isNaN(day.getTime()) && day.toISOString().slice(0, 10) === match[1] && !Number.isNaN(Date.parse(value))
}
function protectedResponse(response: ApprovalOwnerResponse): boolean {
  if (!object(response) || !Number.isInteger(response.statusCode) || !object(response.headers)) return false
  const fields = Object.entries(response.headers)
  const cache = fields.filter(([name]) => name.toLowerCase() === 'cache-control')
  const content = fields.filter(([name]) => name.toLowerCase() === 'content-type')
  return cache.length === 1 && typeof cache[0][1] === 'string' && cache[0][1].trim().toLowerCase() === 'no-store' &&
    content.length === 1 && typeof content[0][1] === 'string' && /^application\/json(?:\s*;\s*charset=utf-8)?$/i.test(content[0][1].trim())
}
function requestKey(value: unknown): value is string {
  const alphabet = 'ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_'
  return typeof value === 'string' && /^[A-Za-z0-9_-]{43}$/.test(value) && alphabet.indexOf(value[42]) % 4 === 0
}

/**
 * Old-device approval client. List items are bound to a verified session and
 * most recent list; an uncertain decision retains its exact key/body in memory.
 */
export class ApprovalOwnerApi {
  #base: string
  #transport: ApprovalOwnerTransport
  #session: SessionPort
  #random: () => string
  #issued = new WeakMap<object, ItemBinding>()
  #revision = 0
  #busy = false
  #pending: PendingDecision | null = null

  constructor(baseUrl: string, transport: ApprovalOwnerTransport, session: SessionPort, random: () => string) {
    try {
      if (typeof baseUrl !== 'string' || baseUrl.length > 2048 || !/^https:\/\//i.test(baseUrl) || /[\u0000-\u0020\u007f\\?#]/.test(baseUrl)) throw 0
      const url = new URL(baseUrl)
      if (url.protocol !== 'https:' || !url.hostname || url.username || url.password || url.search || url.hash) throw 0
      this.#base = url.toString().replace(/\/$/, '')
    } catch { throw new Error('approval service unavailable') }
    if (typeof transport !== 'function' || !session || typeof session.current !== 'function' ||
      typeof session.isCurrent !== 'function' || typeof session.captureInstallGuard !== 'function' ||
      typeof session.subscribeInstallGuardInvalidation !== 'function' || typeof random !== 'function') {
      throw new Error('approval service unavailable')
    }
    this.#transport = transport
    this.#session = session
    this.#random = random
  }

  async list(): Promise<ApprovalListResult> {
    if (this.#busy) return { status: 'busy' }
    if (this.#pending) return { status: 'decision_pending' }
    const credential = this.#session.current()
    if (!credential || !this.#session.isCurrent(credential.subjectId, credential.generation)) return { status: 'not_authenticated' }
    const { subjectId, generation, accessToken } = credential
    const current = () => this.#current(subjectId, generation, accessToken)
    // A refresh invalidates older, potentially stale choices even on failure.
    this.#issued = new WeakMap()
    this.#busy = true
    try {
      let response: ApprovalOwnerResponse
      try {
        response = await this.#transport({ url: `${this.#base}/me/login-approvals`, method: 'GET',
          header: { Accept: 'application/json', Authorization: `Bearer ${accessToken}`, 'Cache-Control': 'no-store' } })
      } catch { return current() ? { status: 'unavailable' } : { status: 'session_changed' } }
      if (!current()) return { status: 'session_changed' }
      // A protected 401 requests a fresh /me verification; it does not itself
      // erase credentials or decide the outcome of any submitted operation.
      if (protectedResponse(response) && response.statusCode === 401) return { status: 'not_authenticated' }
      if (!protectedResponse(response) || response.statusCode !== 200 || !object(response.data) ||
        !exactKeys(response.data, ['items']) || !Array.isArray(response.data.items) || response.data.items.length > 100 ||
        this.#revision >= Number.MAX_SAFE_INTEGER) return { status: 'unavailable' }
      const ids = new Set<string>()
      const items: PendingDeviceApproval[] = []
      for (const raw of response.data.items) {
        if (!object(raw) || !exactKeys(raw, ['id', 'comparisonCode', 'createdAt', 'expiresAt']) ||
          typeof raw.id !== 'string' || !/^[A-Za-z0-9_-]{22,128}$/.test(raw.id) || ids.has(raw.id) ||
          typeof raw.comparisonCode !== 'string' || !/^[0-9]{4,8}$/.test(raw.comparisonCode) ||
          !validDate(raw.createdAt) || !validDate(raw.expiresAt) || Date.parse(raw.expiresAt) <= Date.parse(raw.createdAt)) {
          return { status: 'unavailable' }
        }
        ids.add(raw.id)
        items.push(Object.freeze({ id: raw.id, comparisonCode: raw.comparisonCode, createdAt: raw.createdAt, expiresAt: raw.expiresAt }))
      }
      const revision = ++this.#revision
      for (const item of items) this.#issued.set(item, { subjectId, generation, revision })
      return Object.freeze({ status: 'ready', items: Object.freeze(items) })
    } finally { this.#busy = false }
  }

  /** Approving requires an explicit user confirmation after comparing codes. */
  async decide(item: PendingDeviceApproval, decision: 'approve' | 'deny', confirmed: unknown = false): Promise<ApprovalDecisionResult> {
    if (this.#busy) return { status: 'busy' }
    if (this.#pending) return { status: 'decision_pending' }
    if (decision !== 'approve' && decision !== 'deny') return { status: 'invalid_confirmation' }
    if (decision === 'approve' && confirmed !== true) return { status: 'invalid_confirmation' }
    const credential = this.#session.current()
    if (!credential || !this.#session.isCurrent(credential.subjectId, credential.generation)) return { status: 'not_authenticated' }
    const binding = item && typeof item === 'object' ? this.#issued.get(item) : undefined
    if (!binding || binding.revision !== this.#revision || binding.subjectId !== credential.subjectId ||
      binding.generation !== credential.generation) return { status: 'invalid_item' }
    let key: string
    try { key = this.#random() } catch { return { status: 'unavailable' } }
    if (!requestKey(key)) return { status: 'unavailable' }
    const guard = this.#session.captureInstallGuard()
    if (!guard || !this.#current(credential.subjectId, credential.generation, credential.accessToken)) return { status: 'session_changed' }
    const attempt: PendingDecision = { subjectId: credential.subjectId, generation: credential.generation, accessToken: credential.accessToken,
      path: `/me/login-approvals/${item.id}/decision`, body: JSON.stringify(decision === 'approve'
        ? { decision, comparisonCode: item.comparisonCode, confirmed: true } : { decision }),
      key, decision, comparisonCode: item.comparisonCode, expected: decision === 'approve' ? 'approved' : 'denied', unwatch: null }
    this.#pending = attempt
    try {
      const unwatch = this.#session.subscribeInstallGuardInvalidation(guard, () => {
        if (this.#pending === attempt) this.#clearPending()
      })
      if (this.#pending === attempt) attempt.unwatch = unwatch
      else unwatch()
    } catch { this.#clearPending(); return { status: 'session_changed' } }
    if (this.#pending !== attempt || !this.#current(attempt.subjectId, attempt.generation, attempt.accessToken)) {
      this.#clearPending()
      return { status: 'session_changed' }
    }
    return this.#send(attempt)
  }

  /** Retry only the original decision after an unknown response or limit. */
  async retryDecision(): Promise<ApprovalDecisionResult> {
    if (this.#busy) return { status: 'busy' }
    const attempt = this.#pending
    if (!attempt) return { status: 'unavailable' }
    if (!this.#current(attempt.subjectId, attempt.generation, attempt.accessToken)) {
      this.#clearPending()
      return { status: 'session_changed' }
    }
    return this.#send(attempt)
  }

  /** Non-secret context for showing a retry after the panel is reopened. */
  pendingDecision(): PendingApprovalDecision | null {
    const attempt = this.#pending
    if (!attempt || !this.#current(attempt.subjectId, attempt.generation, attempt.accessToken)) return null
    return Object.freeze({ decision: attempt.decision, comparisonCode: attempt.comparisonCode })
  }

  /**
   * A rejected or lost response may follow a committed decision. The caller
   * may explicitly stop waiting, but this never claims that it did not commit.
   */
  stopWaiting(acknowledged: unknown): 'unresolved' | 'invalid_confirmation' | 'busy' | 'unavailable' {
    if (this.#busy) return 'busy'
    if (acknowledged !== true) return 'invalid_confirmation'
    if (!this.#pending) return 'unavailable'
    this.#clearPending()
    this.#issued = new WeakMap()
    return 'unresolved'
  }

  #current(subjectId: string, generation: string, accessToken: string): boolean {
    const credential = this.#session.current()
    return this.#session.isCurrent(subjectId, generation) && credential?.subjectId === subjectId &&
      credential.generation === generation && credential.accessToken === accessToken
  }

  #clearPending(): void {
    const attempt = this.#pending
    this.#pending = null
    if (attempt?.unwatch) { try { attempt.unwatch() } catch { /* best-effort subscription removal */ } }
  }

  async #send(attempt: PendingDecision): Promise<ApprovalDecisionResult> {
    if (this.#pending !== attempt || !this.#current(attempt.subjectId, attempt.generation, attempt.accessToken)) return { status: 'session_changed' }
    this.#busy = true
    try {
      let response: ApprovalOwnerResponse
      try {
        response = await this.#transport({ url: `${this.#base}${attempt.path}`, method: 'POST',
          header: { Accept: 'application/json', Authorization: `Bearer ${attempt.accessToken}`, 'Cache-Control': 'no-store',
            'Content-Type': 'application/json', 'Idempotency-Key': attempt.key }, data: attempt.body })
      } catch {
        return this.#pending === attempt && this.#current(attempt.subjectId, attempt.generation, attempt.accessToken)
          ? { status: 'retry' } : { status: 'session_changed' }
      }
      if (this.#pending !== attempt || !this.#current(attempt.subjectId, attempt.generation, attempt.accessToken)) return { status: 'session_changed' }
      if (!protectedResponse(response)) return { status: 'retry' }
      if (response.statusCode === 429) return { status: 'rate_limited' }
      if ([401, 403, 409, 410].includes(response.statusCode)) return { status: 'needs_review' }
      if (response.statusCode !== 200 || !object(response.data) || !exactKeys(response.data, ['status']) || response.data.status !== attempt.expected) {
        return { status: 'retry' }
      }
      this.#clearPending()
      // Previous list items cannot be reused after a terminal decision.
      this.#issued = new WeakMap()
      return { status: attempt.expected }
    } finally { this.#busy = false }
  }
}
