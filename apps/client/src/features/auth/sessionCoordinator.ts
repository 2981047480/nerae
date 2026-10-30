import {
  sessionTokenForSecureStorage,
  type AuthUser,
  type Authenticated,
} from '../../api/auth/emailAuthApi'
import type { RecordingAuth, RecordingCredential } from '../training/recordingApi'
import type { AuthLease } from '../training/draftStorage'

/** Shape returned by the iOS/Android secure-vault UTS functions. */
export interface SecureAuthenticationStateResult {
  ok: boolean
  errorCode: string
  found: boolean
  value: string
}

/** The platform must inject its OS-backed vault. There is deliberately no web or uni-storage fallback. */
export interface SecureAuthenticationStateRepository {
  readAuthenticationState(): SecureAuthenticationStateResult | Promise<SecureAuthenticationStateResult>
  writeAuthenticationState(value: string): SecureAuthenticationStateResult | Promise<SecureAuthenticationStateResult>
  clearAuthenticationState(): SecureAuthenticationStateResult | Promise<SecureAuthenticationStateResult>
}

export type PersistedCredentialCheck =
  | { status: 'valid'; user: AuthUser }
  | { status: 'expired' | 'revoked' }
  | { status: 'unavailable' }

/** A server-backed check is required before a persisted credential becomes active again. */
export type PersistedCredentialVerifier = (accessToken: string, expectedSubjectId: string) =>
  Promise<PersistedCredentialCheck> | PersistedCredentialCheck

export interface PublicSession {
  readonly status: 'authenticated'
  readonly subjectId: string
  readonly generation: string
  readonly user: AuthUser
}

export type SessionCoordinatorState = 'signed_out' | 'restoring' | 'installing' | 'signing_out' | 'authenticated' | 'unavailable'
export type SessionCoordinatorResult =
  | { status: 'authenticated'; session: PublicSession }
  | { status: 'signed_out'; reason: 'missing' | 'invalid' | 'expired' | 'logged_out' }
  | { status: 'unavailable'; reason: 'storage' | 'verification' }
  | { status: 'superseded' }
  | { status: 'rejected' }

type PersistedSession = { schema: 1; subjectId: string; accessToken: string }
type PersistedEntry = PersistedSession | { schema: 1; signedOut: true }
type ActiveSession = PublicSession & { accessToken: string }

let nextGeneration = 0
type InstallGuardState = { owner: AuthSessionCoordinator; revision: number }
type InstallGuardWatch = { guard: SessionInstallGuard; invalidate: () => void }
const installGuards = new WeakMap<object, InstallGuardState>()
export type SessionInstallGuard = object

function object(value: unknown): value is Record<string, unknown> {
  return value !== null && typeof value === 'object' && !Array.isArray(value)
}

function copyUser(value: unknown): AuthUser | undefined {
  if (!object(value) || typeof value.id !== 'string' || !/^[1-9][0-9]{0,19}$/.test(value.id) ||
    typeof value.nickname !== 'string' || !Array.isArray(value.roles) ||
    !value.roles.every(role => role === 'student' || role === 'coach') ||
    ('ageVerified' in value && typeof value.ageVerified !== 'boolean') ||
    ('ageDeclarationVersion' in value && (!Number.isSafeInteger(value.ageDeclarationVersion) || Number(value.ageDeclarationVersion) < 1)) ||
    ('status' in value && value.status !== 'active') ||
    ('avatarUrl' in value && value.avatarUrl !== null && typeof value.avatarUrl !== 'string')) return undefined

  return Object.freeze({
    id: value.id,
    nickname: value.nickname,
    roles: Object.freeze([...value.roles]) as readonly ('student' | 'coach')[],
    ...('ageVerified' in value ? { ageVerified: value.ageVerified as boolean } : {}),
    ...('ageDeclarationVersion' in value ? { ageDeclarationVersion: value.ageDeclarationVersion as number } : {}),
    ...('status' in value ? { status: 'active' as const } : {}),
    ...('avatarUrl' in value ? { avatarUrl: value.avatarUrl as string | null } : {}),
  })
}

function validToken(value: unknown): value is string {
  return typeof value === 'string' && value.length > 0 && value.length <= 8192 && /^[\x21-\x7e]+$/.test(value)
}

function decodePersistedEntry(raw: unknown): PersistedEntry | undefined {
  if (typeof raw !== 'string' || raw.length > 16 * 1024) return undefined
  try {
    const parsed: unknown = JSON.parse(raw)
    if (!object(parsed) || parsed.schema !== 1 || JSON.stringify(parsed) !== raw) return undefined
    if (Object.keys(parsed).length === 2 && parsed.signedOut === true && Object.prototype.hasOwnProperty.call(parsed, 'signedOut')) {
      return { schema: 1, signedOut: true }
    }
    if (Object.keys(parsed).length === 3 && ['schema', 'subjectId', 'accessToken'].every(key => Object.prototype.hasOwnProperty.call(parsed, key)) &&
      typeof parsed.subjectId === 'string' && /^[1-9][0-9]{0,19}$/.test(parsed.subjectId) && validToken(parsed.accessToken)) {
      return { schema: 1, subjectId: parsed.subjectId, accessToken: parsed.accessToken }
    }
    return undefined
  } catch {
    return undefined
  }
}

function encodePersistedSession(subjectId: string, accessToken: string): string {
  return JSON.stringify({ schema: 1, subjectId, accessToken } satisfies PersistedSession)
}

const signedOutTombstone = JSON.stringify({ schema: 1, signedOut: true })

function successfulVaultMutation(value: unknown): boolean {
  return object(value) && value.ok === true && value.errorCode === '' && value.found === false && value.value === ''
}

function validVaultRead(value: unknown): value is SecureAuthenticationStateResult {
  return object(value) && typeof value.ok === 'boolean' && typeof value.errorCode === 'string' &&
    typeof value.found === 'boolean' && typeof value.value === 'string' &&
    (value.ok
      ? value.errorCode === '' && (value.found ? value.value.length > 0 : value.value === '')
      : value.errorCode.length > 0 && !value.found && value.value === '')
}

function newSessionGeneration(): string {
  if (nextGeneration >= Number.MAX_SAFE_INTEGER) throw new Error('session generation unavailable')
  nextGeneration++
  return `session-${nextGeneration}`
}

/**
 * Keeps a verified login in memory and the opaque bearer value only in the injected
 * platform vault. The supplied verifier is required for secure restoration; no
 * client-side deadline is inferred without the original request start time.
 */
export class AuthSessionCoordinator implements RecordingAuth {
  #repository: SecureAuthenticationStateRepository
  #verifyPersisted: PersistedCredentialVerifier
  #active: ActiveSession | null = null
  #installGuardWatches = new Set<InstallGuardWatch>()
  private stateValue: SessionCoordinatorState = 'signed_out'
  private revision = 0
  private storageTail: Promise<void> = Promise.resolve()

  constructor(repository: SecureAuthenticationStateRepository, verifyPersisted: PersistedCredentialVerifier) {
    if (!repository || typeof repository.readAuthenticationState !== 'function' ||
      typeof repository.writeAuthenticationState !== 'function' || typeof repository.clearAuthenticationState !== 'function' ||
      typeof verifyPersisted !== 'function') throw new Error('secure session dependencies unavailable')
    this.#repository = repository
    this.#verifyPersisted = verifyPersisted
  }

  get state(): SessionCoordinatorState { return this.stateValue }

  /** Public session metadata contains no bearer token and is safe to render or serialize. */
  snapshot(): PublicSession | null {
    if (!this.#active || this.stateValue !== 'authenticated') return null
    return Object.freeze({ status: 'authenticated', subjectId: this.#active.subjectId,
      generation: this.#active.generation, user: this.#active.user })
  }

  /** RecordingApi's credential port; accessToken is intentionally non-enumerable. */
  current(): RecordingCredential | null {
    const session = this.#active
    if (!session || this.stateValue !== 'authenticated') return null
    const credential = { subjectId: session.subjectId, generation: session.generation } as RecordingCredential
    Object.defineProperty(credential, 'accessToken', { value: session.accessToken, enumerable: false, writable: false })
    Object.defineProperty(credential, 'toJSON', {
      value: () => ({ subjectId: session.subjectId, generation: session.generation }), enumerable: false,
    })
    return Object.freeze(credential)
  }

  isCurrent(subjectId: string, generation: string): boolean {
    return this.stateValue === 'authenticated' && this.#active?.subjectId === subjectId &&
      this.#active.generation === generation
  }

  /** A generation-bound lease for DraftController; old leases fail immediately on switch/logout. */
  draftLease(): AuthLease | null {
    const session = this.#active
    if (!session || this.stateValue !== 'authenticated') return null
    const generation = session.generation
    return Object.freeze({ generation, isCurrent: (subjectId: string, candidate: string) => this.isCurrent(subjectId, candidate) })
  }

  /** Bind an in-flight login to the current session operation revision. */
  captureInstallGuard(): SessionInstallGuard | null {
    if (this.stateValue === 'restoring' || this.stateValue === 'installing' || this.stateValue === 'signing_out') return null
    const guard = Object.freeze({})
    installGuards.set(guard, { owner: this, revision: this.revision })
    return guard
  }

  isInstallGuardCurrent(guard: SessionInstallGuard): boolean {
    if (!guard || typeof guard !== 'object') return false
    const state = installGuards.get(guard)
    return Boolean(state && state.owner === this && state.revision === this.revision)
  }

  /** Notify a pending login immediately when another session operation starts. */
  subscribeInstallGuardInvalidation(guard: SessionInstallGuard, invalidate: () => void): () => void {
    if (!this.isInstallGuardCurrent(guard) || typeof invalidate !== 'function') throw new Error('session guard unavailable')
    const watch = { guard, invalidate }
    this.#installGuardWatches.add(watch)
    return () => { this.#installGuardWatches.delete(watch) }
  }

  /** An older login response cannot resurrect a session after logout or account switch. */
  async installAuthenticatedIfCurrent(result: Authenticated, guard: SessionInstallGuard): Promise<SessionCoordinatorResult> {
    if (!this.isInstallGuardCurrent(guard)) return { status: 'superseded' }
    const before = this.revision
    // installAuthenticated starts its own revision synchronously before its first await.
    const pending = this.#installAuthenticated(result, guard)
    const started = this.revision
    const installed = await pending
    if (installed.status === 'unavailable' && started === before + 1 && this.revision === started) {
      // Only this failed install may renew the guard; an intervening logout,
      // restore or account switch must leave it stale.
      installGuards.set(guard, { owner: this, revision: started })
    }
    if (installed.status === 'authenticated') {
      if (this.revision !== started || !this.isCurrent(installed.session.subjectId, installed.session.generation)) return { status: 'superseded' }
      this.#invalidateInstallGuards(undefined, guard)
      if (this.revision !== started || !this.isCurrent(installed.session.subjectId, installed.session.generation)) return { status: 'superseded' }
    }
    return installed
  }

  /** Accept only an EmailAuthApi-branded authenticated result, never approval-required or raw data. */
  async installAuthenticated(result: Authenticated): Promise<SessionCoordinatorResult> {
    return this.#installAuthenticated(result)
  }

  async #installAuthenticated(result: Authenticated, preservedGuard?: SessionInstallGuard): Promise<SessionCoordinatorResult> {
    let accessToken: string
    let user: AuthUser | undefined
    try {
      user = copyUser(result?.user)
      if (!result || result.status !== 'authenticated' || !user ||
        !Number.isSafeInteger(result.expiresIn) || result.expiresIn < 1) return { status: 'rejected' }
      accessToken = sessionTokenForSecureStorage(result)
      if (!validToken(accessToken)) return { status: 'rejected' }
    } catch {
      return { status: 'rejected' }
    }

    const revision = this.begin('installing', preservedGuard)
    const persisted = encodePersistedSession(user.id, accessToken)
    return this.enqueue(async () => {
      if (!this.isLatest(revision)) return { status: 'superseded' }
      if (!await this.retireVault()) return this.failCurrent(revision, 'storage')
      if (!this.isLatest(revision)) return { status: 'superseded' }
      let written = false
      try { written = successfulVaultMutation(await this.#repository.writeAuthenticationState(persisted)) } catch { written = false }
      if (!written) {
        // The old credential was cleared before writing. Attempt cleanup in case the
        // platform reported failure after partially persisting the new value.
        await this.retireVault()
        return this.failCurrent(revision, 'storage')
      }
      if (!this.isLatest(revision)) return { status: 'superseded' }
      let generation: string
      try { generation = newSessionGeneration() } catch { await this.retireVault(); return this.failCurrent(revision, 'storage') }
      this.#active = Object.freeze({ status: 'authenticated', subjectId: user.id, generation, user, accessToken })
      this.stateValue = 'authenticated'
      return { status: 'authenticated', session: this.snapshot()! }
    })
  }

  /**
   * Re-read from the secure vault and require server confirmation before exposing
   * the credential to RecordingApi. `expiresIn` is not persisted or interpreted.
   */
  async restore(): Promise<SessionCoordinatorResult> {
    const revision = this.begin('restoring')
    return this.enqueue(async () => {
      if (!this.isLatest(revision)) return { status: 'superseded' }
      let read: unknown
      try { read = await this.#repository.readAuthenticationState() } catch { return this.failCurrent(revision, 'storage') }
      if (!this.isLatest(revision)) return { status: 'superseded' }
      if (!validVaultRead(read) || !read.ok) return this.failCurrent(revision, 'storage')
      if (!read.found) {
        this.stateValue = 'signed_out'
        return { status: 'signed_out', reason: 'missing' }
      }
      const persisted = decodePersistedEntry(read.value)
      if (!persisted) return this.clearAndSignOut(revision, 'invalid')
      if ('signedOut' in persisted) {
        await this.clearVault()
        if (!this.isLatest(revision)) return { status: 'superseded' }
        this.stateValue = 'signed_out'
        return { status: 'signed_out', reason: 'logged_out' }
      }

      let check: PersistedCredentialCheck
      try { check = await this.#verifyPersisted(persisted.accessToken, persisted.subjectId) } catch {
        return this.failCurrent(revision, 'verification')
      }
      if (!this.isLatest(revision)) return { status: 'superseded' }
      if (!check || check.status === 'unavailable') return this.failCurrent(revision, 'verification')
      const user = check.status === 'valid' ? copyUser(check.user) : undefined
      if (check.status !== 'valid' || !user || user.id !== persisted.subjectId) {
        return this.clearAndSignOut(revision, check.status === 'expired' || check.status === 'revoked' ? 'expired' : 'invalid')
      }
      let generation: string
      try { generation = newSessionGeneration() } catch { return this.failCurrent(revision, 'storage') }
      this.#active = Object.freeze({ status: 'authenticated', subjectId: user.id, generation, user,
        accessToken: persisted.accessToken })
      this.stateValue = 'authenticated'
      return { status: 'authenticated', session: this.snapshot()! }
    })
  }

  /** Logout invalidates all in-memory leases immediately, before awaiting vault I/O. */
  async logout(): Promise<SessionCoordinatorResult> {
    const revision = this.begin('signing_out')
    return this.enqueue(async () => {
      if (!this.isLatest(revision)) return { status: 'superseded' }
      if (!await this.retireVault()) return this.failCurrent(revision, 'storage')
      if (!this.isLatest(revision)) return { status: 'superseded' }
      this.stateValue = 'signed_out'
      return { status: 'signed_out', reason: 'logged_out' }
    })
  }

  /** Call on an authoritative expiry/revocation response; stale requests cannot clear a newer login. */
  async invalidateIfCurrent(subjectId: string, generation: string): Promise<SessionCoordinatorResult> {
    if (!this.isCurrent(subjectId, generation)) return { status: 'superseded' }
    const revision = this.begin('signing_out')
    return this.enqueue(async () => {
      if (!this.isLatest(revision)) return { status: 'superseded' }
      if (!await this.retireVault()) return this.failCurrent(revision, 'storage')
      if (!this.isLatest(revision)) return { status: 'superseded' }
      this.stateValue = 'signed_out'
      return { status: 'signed_out', reason: 'expired' }
    })
  }

  private begin(state: SessionCoordinatorState, preservedGuard?: SessionInstallGuard): number {
    if (this.revision >= Number.MAX_SAFE_INTEGER) throw new Error('session operation unavailable')
    const revision = ++this.revision
    this.#active = null
    this.stateValue = state
    this.#invalidateInstallGuards(preservedGuard)
    return revision
  }

  #invalidateInstallGuards(preservedGuard?: SessionInstallGuard, onlyGuard?: SessionInstallGuard): void {
    for (const watch of [...this.#installGuardWatches]) {
      if (watch.guard === preservedGuard || (onlyGuard && watch.guard !== onlyGuard)) continue
      // Another listener may synchronously invalidate or unsubscribe this one.
      if (!this.#installGuardWatches.delete(watch)) continue
      try { watch.invalidate() } catch { /* A listener cannot interrupt a session operation. */ }
    }
  }

  private isLatest(revision: number): boolean { return this.revision === revision }

  private failCurrent(revision: number, reason: 'storage' | 'verification'): SessionCoordinatorResult {
    if (this.isLatest(revision)) {
      this.#active = null
      this.stateValue = 'unavailable'
    }
    return this.isLatest(revision) ? { status: 'unavailable', reason } : { status: 'superseded' }
  }

  private async clearAndSignOut(revision: number, reason: 'invalid' | 'expired'): Promise<SessionCoordinatorResult> {
    if (!this.isLatest(revision)) return { status: 'superseded' }
    if (!await this.retireVault()) return this.failCurrent(revision, 'storage')
    if (!this.isLatest(revision)) return { status: 'superseded' }
    this.stateValue = 'signed_out'
    return { status: 'signed_out', reason }
  }

  private async clearVault(): Promise<boolean> {
    try { return successfulVaultMutation(await this.#repository.clearAuthenticationState()) } catch { return false }
  }

  /** Persist a non-secret tombstone before clear, so a failed clear cannot restore an older account. */
  private async retireVault(): Promise<boolean> {
    let tombstoneStored = false
    try {
      tombstoneStored = successfulVaultMutation(await this.#repository.writeAuthenticationState(signedOutTombstone))
    } catch { tombstoneStored = false }
    const cleared = await this.clearVault()
    return tombstoneStored || cleared
  }

  /** Serialize platform reads/writes; stale operations re-check their revision before publishing. */
  private enqueue<T>(operation: () => Promise<T>): Promise<T> {
    const next = this.storageTail.then(operation, operation)
    this.storageTail = next.then(() => undefined, () => undefined)
    return next
  }
}
