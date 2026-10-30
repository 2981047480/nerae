import type { SecureAuthTransport } from '../../api/auth/emailAuthApi'
import { createCurrentUserVerifier, type CurrentUserResponse } from './meVerifier'
import type { AuthSessionCoordinator } from './sessionCoordinator'

export type AgeAttestationResult = Readonly<{ status: 'declared' | 'already_declared' | 'invalid_confirmation' |
  'not_authenticated' | 'session_changed' | 'version_conflict' | 'reconcile_required' | 'unavailable' }>

function authoritativeVersionConflict(response: CurrentUserResponse): boolean {
  const body = response.body
  const headers = response.headers
  if (!body || typeof body !== 'object' || Array.isArray(body) ||
    !headers || typeof headers !== 'object' || Array.isArray(headers)) return false
  const error = body as Record<string, unknown>
  if (error.code !== 'VERSION_CONFLICT' || typeof error.message !== 'string' ||
    error.message.length < 1 || error.message.length > 2048 ||
    typeof error.requestId !== 'string' || !/^[a-f0-9]{32}$/.test(error.requestId) ||
    ('details' in error && (!error.details || typeof error.details !== 'object' || Array.isArray(error.details)))) return false
  const fields = Object.entries(headers)
  const requestIds = fields.filter(([name]) => name.toLowerCase() === 'x-request-id')
  const contentTypes = fields.filter(([name]) => name.toLowerCase() === 'content-type')
  const cacheControls = fields.filter(([name]) => name.toLowerCase() === 'cache-control')
  return requestIds.length === 1 && requestIds[0][1] === error.requestId &&
    contentTypes.length === 1 && typeof contentTypes[0][1] === 'string' &&
    /^application\/json(?:\s*;\s*charset=utf-8)?$/i.test(contentTypes[0][1].trim()) &&
    cacheControls.length === 1 && typeof cacheControls[0][1] === 'string' &&
    cacheControls[0][1].trim().toLowerCase() === 'no-store'
}

/**
 * Submit only an explicit 18+ self-declaration. A lost response never triggers
 * an automatic write retry; an authenticated refresh determines current state.
 */
export class AgeAttestationApi {
  #url: string
  #transport: SecureAuthTransport
  #session: AuthSessionCoordinator
  #busy = false

  constructor(baseUrl: string, transport: SecureAuthTransport, session: AuthSessionCoordinator) {
    try {
      if (typeof baseUrl !== 'string' || baseUrl.length > 2048 || !/^https:\/\//i.test(baseUrl) ||
        /[\u0000-\u0020\u007f\\?#]/.test(baseUrl)) throw 0
      const url = new URL(baseUrl)
      if (url.protocol !== 'https:' || !url.hostname || url.username || url.password || url.search || url.hash) throw 0
      this.#url = `${url.toString().replace(/\/$/, '')}/me/age-verify`
    } catch { throw new Error('age declaration service unavailable') }
    if (typeof transport !== 'function' || !session || typeof session.current !== 'function' ||
      typeof session.snapshot !== 'function' || typeof session.isCurrent !== 'function' || typeof session.restore !== 'function') {
      throw new Error('age declaration dependencies unavailable')
    }
    this.#transport = transport
    this.#session = session
  }

  async declare(confirmed: unknown): Promise<AgeAttestationResult> {
    if (confirmed !== true) return { status: 'invalid_confirmation' }
    if (this.#busy) return { status: 'unavailable' }
    const credential = this.#session.current()
    const snapshot = this.#session.snapshot()
    if (!credential || !snapshot || snapshot.subjectId !== credential.subjectId || snapshot.generation !== credential.generation ||
      !this.#session.isCurrent(credential.subjectId, credential.generation)) return { status: 'not_authenticated' }
    const version = snapshot.user.ageDeclarationVersion
    if (typeof snapshot.user.ageVerified !== 'boolean' || !Number.isSafeInteger(version) || Number(version) < 1) return { status: 'unavailable' }
    if (snapshot.user.ageVerified) return { status: 'already_declared' }
    if (Number(version) >= Number.MAX_SAFE_INTEGER) return { status: 'unavailable' }
    const { subjectId, generation, accessToken } = credential
    const current = () => this.#session.isCurrent(subjectId, generation) && this.#session.current()?.accessToken === accessToken
    this.#busy = true
    try {
      let response: CurrentUserResponse
      try {
        const raw = await this.#transport({
          url: this.#url, method: 'POST',
          headers: { Accept: 'application/json', 'Content-Type': 'application/json', 'Cache-Control': 'no-store', Authorization: `Bearer ${accessToken}` },
          body: JSON.stringify({ isAdult: true, expectedVersion: version }),
        })
        response = { status: raw.status, headers: raw.headers as Readonly<Record<string, string>>, body: raw.body }
      } catch {
        return await this.#reconcile(subjectId, generation, current, false)
      }
      if (!current()) return { status: 'session_changed' }
      if (response.status === 412) return await this.#reconcile(subjectId, generation, current, authoritativeVersionConflict(response))
      const verify = createCurrentUserVerifier(async () => response)
      const checked = await verify(accessToken, subjectId)
      if (!current()) return { status: 'session_changed' }
      if (checked.status !== 'valid' || checked.user.ageVerified !== true || checked.user.ageDeclarationVersion !== Number(version) + 1) {
        return await this.#reconcile(subjectId, generation, current, false)
      }
      let restored
      try { restored = await this.#session.restore() } catch { return { status: 'reconcile_required' } }
      if (restored.status === 'superseded') return { status: 'session_changed' }
      if (restored.status !== 'authenticated' || restored.session.subjectId !== subjectId ||
        restored.session.user.ageVerified !== true || restored.session.user.ageDeclarationVersion !== checked.user.ageDeclarationVersion) {
        return { status: 'reconcile_required' }
      }
      return { status: 'declared' }
    } finally { this.#busy = false }
  }

  async #reconcile(subjectId: string, generation: string, current: () => boolean, conflict: boolean): Promise<AgeAttestationResult> {
    if (!current() || !this.#session.isCurrent(subjectId, generation)) return { status: 'session_changed' }
    let restored
    try { restored = await this.#session.restore() } catch { return { status: 'reconcile_required' } }
    if (restored.status === 'superseded') return { status: 'session_changed' }
    if (restored.status !== 'authenticated' || restored.session.subjectId !== subjectId) return { status: 'reconcile_required' }
    return { status: conflict ? 'version_conflict' : 'reconcile_required' }
  }
}
