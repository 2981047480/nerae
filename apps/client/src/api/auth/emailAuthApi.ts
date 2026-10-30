/**
 * Email authentication contract. No platform transport is supplied here.
 * A production port MUST validate TLS, prevent every redirect (including
 * cross-origin and HTTPS downgrade), avoid persistence/caching/logging, bound
 * response size, and preserve the exact request body and headers. Implementing
 * a port for uni.request requires independent platform evidence first.
 */
export interface AuthHttpRequest {
  url: string
  method: 'POST'
  headers: Readonly<Record<string, string>>
  body: string
}
export interface AuthHttpResponse {
  status: number
  headers: Readonly<Record<string, unknown>>
  body: unknown
}
export type SecureAuthTransport = (request: AuthHttpRequest) => Promise<AuthHttpResponse>

export type AuthErrorCode = 'invalid_configuration' | 'invalid_request' | 'unauthorized' | 'forbidden' |
  'not_found' | 'conflict' | 'expired' | 'rate_limited' | 'unavailable' | 'network' | 'invalid_response'
export class AuthApiError extends Error {
  readonly code: AuthErrorCode
  readonly status?: number
  constructor(code: AuthErrorCode, status?: number) {
    super(code)
    this.name = 'AuthApiError'
    this.code = code
    this.status = status
  }
}

export interface DevicePublicKey { kty: 'EC'; crv: 'P-256'; x: string; y: string }
export interface ChallengeInput { email: string; devicePublicKey: DevicePublicKey; deviceLabel?: string }
export interface LoginInput { challengeId: string; code: string; deviceProof: string }
export interface ExchangeInput { approvalId: string; approvalSecret: string; deviceProof: string }
export interface Challenge { readonly challengeId: string; readonly expiresAt: string; readonly retryAfter?: string }
export interface AuthUser {
  readonly id: string
  readonly nickname: string
  readonly roles: readonly ('student' | 'coach')[]
  readonly ageVerified?: boolean
  readonly ageDeclarationVersion?: number
  readonly status?: 'active'
  readonly avatarUrl?: string | null
}
export interface Authenticated {
  readonly status: 'authenticated'
  readonly expiresIn: number
  readonly user: AuthUser
  readonly onboarded?: boolean
}
export interface ApprovalRequired {
  readonly status: 'approval_required'
  readonly approvalId: string
  readonly comparisonCode: string
  readonly expiresAt: string
}
export interface ApprovalState { readonly status: 'pending' | 'denied' | 'expired' }
export type LoginOutcome = Authenticated | ApprovalRequired
export type ExchangeOutcome = Authenticated | ApprovalState

const challenges = new WeakMap<object, string>()
const sessions = new WeakMap<object, string>()
const approvals = new WeakMap<object, { approvalSecret: string; nonce: string }>()

/** These explicit accessors return secrets. Only pass them into secure storage or the signing flow. */
export function challengeNonceForSigning(value: Challenge): string {
  const nonce = challenges.get(value)
  if (!nonce) throw new AuthApiError('invalid_response')
  return nonce
}
export function sessionTokenForSecureStorage(value: Authenticated): string {
  const token = sessions.get(value)
  if (!token) throw new AuthApiError('invalid_response')
  return token
}
export function approvalSecretsForSecureStorage(value: ApprovalRequired): Readonly<{ approvalSecret: string; nonce: string }> {
  const secrets = approvals.get(value)
  if (!secrets) throw new AuthApiError('invalid_response')
  return Object.freeze({ ...secrets })
}

function opaque<T extends object>(value: T): Readonly<T> {
  Object.defineProperty(value, 'toJSON', { value: () => { throw new Error('Sensitive authentication result') } })
  return Object.freeze(value)
}
function record(value: unknown): value is Record<string, unknown> {
  return value !== null && typeof value === 'object' && !Array.isArray(value)
}
function keys(value: Record<string, unknown>, required: string[], optional: string[] = []): boolean {
  const actual = Object.keys(value)
  return required.every(key => Object.prototype.hasOwnProperty.call(value, key)) &&
    actual.every(key => required.includes(key) || optional.includes(key))
}
function key(value: unknown): value is string {
  // This checks format, not entropy. The caller must use a platform CSPRNG.
  if (typeof value !== 'string' || !/^[A-Za-z0-9_-]{22,128}$/.test(value) || value.length % 4 === 1) return false
  const last = 'ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_'.indexOf(value[value.length - 1])
  return value.length % 4 === 0 || (value.length % 4 === 2 ? last % 16 === 0 : last % 4 === 0)
}
function id(value: unknown): value is string {
  return typeof value === 'string' && /^[A-Za-z0-9_-]{22,128}$/.test(value)
}
function secret(value: unknown): value is string {
  return typeof value === 'string' && /^[A-Za-z0-9_-]{43,256}$/.test(value)
}
function proof(value: unknown): value is string {
  return typeof value === 'string' && value.length >= 32 && value.length <= 4096 &&
    /^[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+$/.test(value)
}
function email(value: unknown): value is string {
  return typeof value === 'string' && value.length <= 254 && /^[!-?A-~]+@[!-?A-~]+$/.test(value)
}
function dateTime(value: unknown): value is string {
  if (typeof value !== 'string') return false
  const match = /^(\d{4}-\d{2}-\d{2})[Tt](\d{2}):(\d{2}):(\d{2})(?:\.\d+)?([Zz]|[+-](\d{2}):(\d{2}))$/.exec(value)
  if (!match || Number(match[2]) > 23 || Number(match[3]) > 59 || Number(match[4]) > 59 ||
    (match[6] !== undefined && (Number(match[6]) > 23 || Number(match[7]) > 59))) return false
  const day = new Date(`${match[1]}T00:00:00.000Z`)
  return !Number.isNaN(day.getTime()) && day.toISOString().slice(0, 10) === match[1] && !Number.isNaN(Date.parse(value))
}
function baseUrl(value: string): string {
  try {
    if (typeof value !== 'string' || value.length > 2048 || !/^https:\/\//i.test(value) || /[\x00-\x20\x7f\\?#]/.test(value)) throw 0
    const url = new URL(value)
    if (url.protocol !== 'https:' || !url.hostname || url.username || url.password || url.search || url.hash) throw 0
    return url.toString().replace(/\/$/, '')
  } catch { throw new AuthApiError('invalid_configuration') }
}
function validPublicKey(value: unknown): value is DevicePublicKey {
  return record(value) && keys(value, ['kty', 'crv', 'x', 'y']) && value.kty === 'EC' && value.crv === 'P-256' &&
    typeof value.x === 'string' && /^[A-Za-z0-9_-]{43}$/.test(value.x) &&
    typeof value.y === 'string' && /^[A-Za-z0-9_-]{43}$/.test(value.y)
}
function parseChallenge(value: unknown): Challenge {
  if (!record(value) || !keys(value, ['challengeId', 'nonce', 'expiresAt'], ['retryAfter']) || !id(value.challengeId) ||
    !secret(value.nonce) || !dateTime(value.expiresAt) || ('retryAfter' in value && !dateTime(value.retryAfter))) {
    throw new AuthApiError('invalid_response')
  }
  const result = opaque({ challengeId: value.challengeId, expiresAt: value.expiresAt,
    ...('retryAfter' in value ? { retryAfter: value.retryAfter as string } : {}) })
  challenges.set(result, value.nonce)
  return result
}
function parseUser(value: unknown): AuthUser {
  if (!record(value) || typeof value.id !== 'string' || !/^[1-9][0-9]{0,19}$/.test(value.id) ||
    typeof value.nickname !== 'string' || !Array.isArray(value.roles) ||
    !value.roles.every(role => role === 'student' || role === 'coach') ||
    ('ageVerified' in value && typeof value.ageVerified !== 'boolean') ||
    ('ageDeclarationVersion' in value && (!Number.isSafeInteger(value.ageDeclarationVersion) || Number(value.ageDeclarationVersion) < 1)) ||
    ('status' in value && value.status !== 'active') ||
    ('avatarUrl' in value && value.avatarUrl !== null && typeof value.avatarUrl !== 'string')) {
    throw new AuthApiError('invalid_response')
  }
  const roles = Object.freeze([...value.roles]) as readonly ('student' | 'coach')[]
  return Object.freeze({ id: value.id, nickname: value.nickname, roles,
    ...('ageVerified' in value ? { ageVerified: value.ageVerified as boolean } : {}),
    ...('ageDeclarationVersion' in value ? { ageDeclarationVersion: value.ageDeclarationVersion as number } : {}),
    ...('status' in value ? { status: 'active' as const } : {}),
    ...('avatarUrl' in value ? { avatarUrl: value.avatarUrl as string | null } : {}) })
}
function parseAuthenticated(value: Record<string, unknown>): Authenticated {
  if (!keys(value, ['status', 'token', 'expiresIn', 'user'], ['onboarded']) || value.status !== 'authenticated' ||
    typeof value.token !== 'string' || value.token.length < 1 || value.token.length > 8192 || !/^[\x21-\x7e]+$/.test(value.token) ||
    !Number.isSafeInteger(value.expiresIn) || Number(value.expiresIn) < 1 ||
    ('onboarded' in value && typeof value.onboarded !== 'boolean')) throw new AuthApiError('invalid_response')
  const result = opaque({ status: 'authenticated' as const, expiresIn: value.expiresIn as number, user: parseUser(value.user),
    ...('onboarded' in value ? { onboarded: value.onboarded as boolean } : {}) })
  sessions.set(result, value.token)
  return result
}
function parseApproval(value: Record<string, unknown>): ApprovalRequired {
  if (!keys(value, ['status', 'approvalId', 'approvalSecret', 'nonce', 'comparisonCode', 'expiresAt']) ||
    value.status !== 'approval_required' || !id(value.approvalId) || !secret(value.approvalSecret) || !secret(value.nonce) ||
    typeof value.comparisonCode !== 'string' || !/^[0-9]{4,8}$/.test(value.comparisonCode) || !dateTime(value.expiresAt)) {
    throw new AuthApiError('invalid_response')
  }
  const result = opaque({ status: 'approval_required' as const, approvalId: value.approvalId,
    comparisonCode: value.comparisonCode, expiresAt: value.expiresAt })
  approvals.set(result, { approvalSecret: value.approvalSecret, nonce: value.nonce })
  return result
}
function responseError(status: number): AuthApiError {
  if (status === 400 || status === 422) return new AuthApiError('invalid_request', status)
  if (status === 401) return new AuthApiError('unauthorized', status)
  if (status === 403) return new AuthApiError('forbidden', status)
  if (status === 404) return new AuthApiError('not_found', status)
  if (status === 409) return new AuthApiError('conflict', status)
  if (status === 410) return new AuthApiError('expired', status)
  if (status === 429) return new AuthApiError('rate_limited', status)
  return new AuthApiError('unavailable', status)
}
function noStore(headers: unknown): boolean {
  if (!record(headers)) return false
  const found = Object.entries(headers).filter(([name]) => name.toLowerCase() === 'cache-control')
  return found.length === 1 && typeof found[0][1] === 'string' && found[0][1].trim().toLowerCase() === 'no-store'
}

/**
 * All request identifiers and device proofs are supplied by the caller.
 * Never automatically retry: after a transport error or ambiguous response,
 * preserve the same idempotency key AND exact submitted body for reconciliation.
 */
export class EmailAuthApi {
  private readonly base: string
  private readonly transport: SecureAuthTransport
  constructor(base: string, transport: SecureAuthTransport) {
    this.base = baseUrl(base)
    if (typeof transport !== 'function') throw new AuthApiError('invalid_configuration')
    this.transport = transport
  }
  private async send(path: string, body: object, idempotencyKey: string, expectedStatus: number): Promise<unknown> {
    if (!key(idempotencyKey)) throw new AuthApiError('invalid_request')
    let encoded: string
    try { encoded = JSON.stringify(body) } catch { throw new AuthApiError('invalid_request') }
    let response: AuthHttpResponse
    try {
      response = await this.transport({ url: `${this.base}${path}`, method: 'POST',
        headers: Object.freeze({ Accept: 'application/json', 'Content-Type': 'application/json',
          'Cache-Control': 'no-store', 'Idempotency-Key': idempotencyKey }), body: encoded })
    } catch { throw new AuthApiError('network') }
    try {
      if (!record(response) || !Number.isInteger(response.status) || response.status < 100 || response.status > 599) {
        throw new AuthApiError('invalid_response')
      }
      if (!noStore(response.headers)) throw new AuthApiError('invalid_response')
      if (response.status < 200 || response.status >= 300) throw responseError(response.status)
      if (response.status !== expectedStatus) throw new AuthApiError('invalid_response')
      return response.body
    } catch (error) {
      if (error instanceof AuthApiError) throw error
      throw new AuthApiError('invalid_response')
    }
  }
  async requestChallenge(input: ChallengeInput, idempotencyKey: string): Promise<Challenge> {
    let payload: ChallengeInput
    try {
      if (!record(input) || !keys(input, ['email', 'devicePublicKey'], ['deviceLabel']) || !email(input.email) ||
        !validPublicKey(input.devicePublicKey) || ('deviceLabel' in input &&
          (typeof input.deviceLabel !== 'string' || [...input.deviceLabel].length < 1 || [...input.deviceLabel].length > 80))) {
        throw new AuthApiError('invalid_request')
      }
      const { kty, crv, x, y } = input.devicePublicKey
      payload = { email: input.email, devicePublicKey: { kty, crv, x, y },
        ...('deviceLabel' in input ? { deviceLabel: input.deviceLabel } : {}) }
    } catch { throw new AuthApiError('invalid_request') }
    const body = await this.send('/auth/email/challenges', payload, idempotencyKey, 202)
    try { return parseChallenge(body) } catch { throw new AuthApiError('invalid_response') }
  }
  async login(input: LoginInput, idempotencyKey: string): Promise<LoginOutcome> {
    let payload: LoginInput
    try {
      if (!record(input) || !keys(input, ['challengeId', 'code', 'deviceProof']) || !id(input.challengeId) ||
        typeof input.code !== 'string' || !/^[0-9]{6,10}$/.test(input.code) || !proof(input.deviceProof)) {
        throw new AuthApiError('invalid_request')
      }
      payload = { challengeId: input.challengeId, code: input.code, deviceProof: input.deviceProof }
    } catch { throw new AuthApiError('invalid_request') }
    const body = await this.send('/auth/email-login', payload, idempotencyKey, 200)
    try {
      if (!record(body)) throw 0
      if (body.status === 'authenticated') return parseAuthenticated(body)
      if (body.status === 'approval_required') return parseApproval(body)
      throw 0
    } catch { throw new AuthApiError('invalid_response') }
  }
  async exchange(input: ExchangeInput, idempotencyKey: string): Promise<ExchangeOutcome> {
    let payload: { approvalSecret: string; deviceProof: string }
    let approvalId: string
    try {
      if (!record(input) || !keys(input, ['approvalId', 'approvalSecret', 'deviceProof']) || !id(input.approvalId) ||
        !secret(input.approvalSecret) || !proof(input.deviceProof)) throw new AuthApiError('invalid_request')
      approvalId = input.approvalId
      payload = { approvalSecret: input.approvalSecret, deviceProof: input.deviceProof }
    } catch { throw new AuthApiError('invalid_request') }
    const body = await this.send(`/auth/login-approvals/${approvalId}/exchange`, payload, idempotencyKey, 200)
    try {
      if (!record(body)) throw 0
      if (body.status === 'authenticated') return parseAuthenticated(body)
      if ((body.status === 'pending' || body.status === 'denied' || body.status === 'expired') && keys(body, ['status'])) {
        return Object.freeze({ status: body.status })
      }
      throw 0
    } catch { throw new AuthApiError('invalid_response') }
  }
}
