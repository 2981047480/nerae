const BASE64URL = 'ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_'

export type DevicePublicKey = Readonly<{ kty: 'EC'; crv: 'P-256'; x: string; y: string }>
export type DeviceIdentityFailureCode = 'invalid_email' | 'not_initialized' | 'storage_unavailable' |
  'storage_corrupt' | 'reservation_conflict' | 'reservation_incomplete' | 'activation_conflict' |
  'missing_key' | 'key_unavailable' | 'creation_conflict' | 'creation_uncertain' | 'activation_uncertain'
export type DeviceIdentityOutcome =
  | Readonly<{ status: 'ready'; identity: ProtectedDeviceIdentity }>
  | Readonly<{ status: DeviceIdentityFailureCode }>

/** Fixed safe error class. It never retains a cause, address, alias, or native error. */
export class DeviceIdentityError extends Error {
  readonly code: DeviceIdentityFailureCode
  constructor(code: DeviceIdentityFailureCode) {
    super(code)
    this.name = 'DeviceIdentityError'
    this.code = code
  }
}

/** A normalized address used only as a secure-registry lookup key. String/JSON output is redacted. */
export class EmailRegistryKey {
  #value: string
  constructor(value: string) { this.#value = value }
  forProtectedRegistry(): string { return this.#value }
  toString(): string { return '<redacted email registry key>' }
  toJSON(): string { return '<redacted>' }
}

/** Validated opaque alias. Native adapters must explicitly request its value to call the key bridge. */
export class ReservedDeviceAlias {
  #value: string
  constructor(value: string) {
    if (!isCanonicalAlias(value)) throw new DeviceIdentityError('storage_corrupt')
    this.#value = value
  }
  forDeviceKeyBridge(): string { return this.#value }
  toString(): string { return '<redacted device alias>' }
  toJSON(): string { return '<redacted>' }
}

/** Opaque CAS snapshot. Registry adapters construct it from their protected storage read. */
export class DeviceIdentityRegistrySnapshot {
  #revision: string
  #status: 'missing' | 'reserved' | 'active'
  #alias: ReservedDeviceAlias | null
  private constructor(revision: string, status: 'missing' | 'reserved' | 'active', alias: ReservedDeviceAlias | null) {
    if (!/^[A-Za-z0-9_-]{1,128}$/.test(revision)) throw new DeviceIdentityError('storage_unavailable')
    if ((status === 'missing') !== (alias === null)) throw new DeviceIdentityError('storage_corrupt')
    this.#revision = revision
    this.#status = status
    this.#alias = alias
    Object.freeze(this)
  }
  static missing(revision: string): DeviceIdentityRegistrySnapshot {
    return new DeviceIdentityRegistrySnapshot(revision, 'missing', null)
  }
  static reserved(revision: string, alias: string | ReservedDeviceAlias): DeviceIdentityRegistrySnapshot {
    return new DeviceIdentityRegistrySnapshot(revision, 'reserved',
      alias instanceof ReservedDeviceAlias ? alias : new ReservedDeviceAlias(alias))
  }
  static active(revision: string, alias: string | ReservedDeviceAlias): DeviceIdentityRegistrySnapshot {
    return new DeviceIdentityRegistrySnapshot(revision, 'active',
      alias instanceof ReservedDeviceAlias ? alias : new ReservedDeviceAlias(alias))
  }
  get status(): 'missing' | 'reserved' | 'active' { return this.#status }
  get found(): boolean { return this.#status !== 'missing' }
  aliasForCoordinator(): ReservedDeviceAlias | null { return this.#alias }
  revisionForCompareAndSet(): string { return this.#revision }
  toJSON(): Readonly<{ status: 'missing' | 'reserved' | 'active' }> { return { status: this.#status } }
  toString(): string { return '<redacted device identity registry snapshot>' }
}

export interface ProtectedDeviceIdentityRegistry {
  /** Must be linearizable and must not log the supplied key or returned snapshot. */
  read(key: EmailRegistryKey): Promise<DeviceIdentityRegistrySnapshot>
  /**
   * Must atomically compare the full registry revision and transition only missing -> reserved or
   * matching reserved -> active. It must reject aliases already associated with another email key.
   * Never infer active from the existence of a native key.
   */
  compareAndSet(
    key: EmailRegistryKey,
    expected: DeviceIdentityRegistrySnapshot,
    nextStatus: 'reserved' | 'active',
    alias: ReservedDeviceAlias,
  ): Promise<'stored' | 'conflict' | 'unavailable' | 'indeterminate'>
}

export interface SecureAliasRandomPort {
  /** Platform CSPRNG: return an unpadded base64url encoding of exactly 32 random bytes. */
  generateAlias(): Promise<ReservedDeviceAlias>
}

export type DeviceKeyPortResult =
  | Readonly<{ status: 'ready'; alias: ReservedDeviceAlias; publicKey: DevicePublicKey }>
  | Readonly<{ status: 'missing' | 'alias_conflict' | 'unavailable' }>

export interface DeviceKeyPort {
  /** Maps only to native createDeviceKeyAtReservedAlias; never to createNewDeviceKey. */
  createAtReservedAlias(alias: ReservedDeviceAlias): Promise<DeviceKeyPortResult>
  /** Maps only to native readExistingDevicePublicKey. */
  readExisting(alias: ReservedDeviceAlias): Promise<DeviceKeyPortResult>
}

/** Safe public identity handle: protocol fields require explicit property access and JSON omits them. */
export class ProtectedDeviceIdentity {
  #alias: ReservedDeviceAlias
  #publicKey: DevicePublicKey
  readonly status = 'ready'
  constructor(alias: ReservedDeviceAlias, publicKey: DevicePublicKey) {
    this.#alias = alias
    this.#publicKey = Object.freeze({ kty: 'EC', crv: 'P-256', x: publicKey.x, y: publicKey.y })
    Object.freeze(this)
  }
  get aliasForProtocol(): string { return this.#alias.forDeviceKeyBridge() }
  get publicKeyForProtocol(): DevicePublicKey { return this.#publicKey }
  toJSON(): Readonly<{ status: 'ready' }> { return { status: 'ready' } }
  toString(): string { return '<redacted device identity>' }
}

function atext(char: string): boolean {
  const code = char.charCodeAt(0)
  return code >= 33 && code <= 126 && !'()<>[]:;@\\,"'.includes(char)
}

function validDotAtom(value: string): boolean {
  return value.length > 0 && !value.startsWith('.') && !value.endsWith('.') && !value.includes('..') &&
    [...value].every(char => char === '.' || atext(char))
}

function validIPv4(value: string): boolean {
  const parts = value.split('.')
  return parts.length === 4 && parts.every(part => /^\d{1,3}$/.test(part) &&
    (part === '0' || !part.startsWith('0')) && Number(part) <= 255)
}

function validDomainLiteral(value: string): boolean {
  if (value.length < 3 || value[0] !== '[' || value[value.length - 1] !== ']') return false
  const content = value.slice(1, -1)
  if (!content || [...content].some(char => {
    const code = char.charCodeAt(0)
    return code < 33 || code > 126 || '[]\\'.includes(char)
  })) return false
  // Go accepts an "IPv6:" literal, then lowercases it to "ipv6:"; its next
  // NormalizeEmail call rejects that lowercase prefix. Exclude this non-stable form.
  if (content.startsWith('IPv6:') || content.startsWith('ipv6:')) return false
  return validIPv4(content)
}

/** Mirrors auth/domain.NormalizeEmail's ASCII addr-spec, preserving local-part case. */
function normalizeEmailValue(value: unknown): string | null {
  if (typeof value !== 'string' || value.length === 0 || value.length > 254 || (value.match(/@/g) ?? []).length !== 1 ||
    [...value].some(char => char.charCodeAt(0) <= 32 || char.charCodeAt(0) >= 127)) return null
  const separator = value.indexOf('@')
  const local = value.slice(0, separator)
  const domain = value.slice(separator + 1)
  // Go net/mail unquotes quoted local parts in Address; NormalizeEmail then rejects
  // them because parsed.Address no longer equals the original input.
  if (!validDotAtom(local) || !(validDotAtom(domain) || validDomainLiteral(domain))) return null
  return `${local}@${domain.toLowerCase()}`
}

/** Returns an opaque normalized lookup key; direct string/JSON output remains redacted. */
export function normalizeEmailForDeviceRegistry(value: unknown): EmailRegistryKey | null {
  const normalized = normalizeEmailValue(value)
  return normalized === null ? null : new EmailRegistryKey(normalized)
}

function encodeBase64URL(bytes: Uint8Array): string {
  let result = ''
  for (let index = 0; index < bytes.length; index += 3) {
    const a = bytes[index]
    const hasB = index + 1 < bytes.length
    const hasC = index + 2 < bytes.length
    const b = hasB ? bytes[index + 1] : 0
    const c = hasC ? bytes[index + 2] : 0
    result += BASE64URL[a >> 2]
    result += BASE64URL[((a & 3) << 4) | (b >> 4)]
    if (hasB) result += BASE64URL[((b & 15) << 2) | (c >> 6)]
    if (hasC) result += BASE64URL[c & 63]
  }
  return result
}

function isCanonicalAlias(value: unknown): value is string {
  if (typeof value !== 'string' || !/^[A-Za-z0-9_-]{43}$/.test(value)) return false
  // 32 bytes leave four data bits in the final base64url sextet; two low bits are padding.
  return BASE64URL.indexOf(value[42]) % 4 === 0
}

function validPublicKey(value: unknown): value is DevicePublicKey {
  if (!value || typeof value !== 'object') return false
  const candidate = value as Record<string, unknown>
  return candidate.kty === 'EC' && candidate.crv === 'P-256' &&
    isCanonicalAlias(candidate.x) && isCanonicalAlias(candidate.y)
}

function failure(status: DeviceIdentityFailureCode): DeviceIdentityOutcome {
  return Object.freeze({ status })
}

const issuedIdentities = new WeakMap<ProtectedDeviceIdentity, DeviceIdentityCoordinator>()

/** Check the in-process issuer before allowing a platform operation on a handle. */
export function identityWasIssuedBy(identity: unknown, owner: DeviceIdentityCoordinator): identity is ProtectedDeviceIdentity {
  return identity instanceof ProtectedDeviceIdentity && issuedIdentities.get(identity) === owner
}

function ready(owner: DeviceIdentityCoordinator, alias: ReservedDeviceAlias, publicKey: DevicePublicKey): DeviceIdentityOutcome {
  const identity = new ProtectedDeviceIdentity(alias, publicKey)
  issuedIdentities.set(identity, owner)
  return Object.freeze({ status: 'ready', identity })
}

function isSnapshot(value: unknown): value is DeviceIdentityRegistrySnapshot {
  return value instanceof DeviceIdentityRegistrySnapshot
}

/**
 * Coordinates one explicit first-use reservation with native create-once. The protected registry
 * must provide linearizable CAS/read-after-write. The per-instance mutex also serializes same-email
 * operations in this JS runtime; independent runtimes rely on registry CAS and may fail closed while
 * the winning runtime is between reservation and key creation.
 */
export class DeviceIdentityCoordinator {
  #registry: ProtectedDeviceIdentityRegistry
  #random: SecureAliasRandomPort
  #deviceKeys: DeviceKeyPort
  #locks = new Map<string, Promise<void>>()

  constructor(registry: ProtectedDeviceIdentityRegistry, random: SecureAliasRandomPort, deviceKeys: DeviceKeyPort) {
    if (!registry || typeof registry.read !== 'function' || typeof registry.compareAndSet !== 'function' ||
      !random || typeof random.generateAlias !== 'function' || !deviceKeys ||
      typeof deviceKeys.createAtReservedAlias !== 'function' || typeof deviceKeys.readExisting !== 'function') {
      throw new DeviceIdentityError('storage_unavailable')
    }
    this.#registry = registry
    this.#random = random
    this.#deviceKeys = deviceKeys
  }

  /** Read-only path for existing accounts and restart recovery. It never generates or creates. */
  async readForEmail(email: unknown): Promise<DeviceIdentityOutcome> {
    const key = normalizeEmailForDeviceRegistry(email)
    if (key === null) return failure('invalid_email')
    return this.#withEmailLock(key.forProtectedRegistry(), async () => {
      const snapshot = await this.#readSnapshot(key)
      if (!isSnapshot(snapshot)) return failure('storage_unavailable')
      if (!snapshot.found) return failure('not_initialized')
      if (snapshot.status === 'reserved') return failure('reservation_incomplete')
      return this.#readActive(snapshot)
    })
  }

  /** Explicit first-use action. Existing mappings and CAS winners are always read-only. */
  async initializeFirstForEmail(email: unknown): Promise<DeviceIdentityOutcome> {
    const key = normalizeEmailForDeviceRegistry(email)
    if (key === null) return failure('invalid_email')
    return this.#withEmailLock(key.forProtectedRegistry(), async () => {
      const snapshot = await this.#readSnapshot(key)
      if (!isSnapshot(snapshot)) return failure('storage_unavailable')
      if (snapshot.status === 'active') return this.#readActive(snapshot)
      if (snapshot.status === 'reserved') return failure('reservation_incomplete')

      let generated: unknown
      try { generated = await this.#random.generateAlias() } catch { return failure('key_unavailable') }
      if (!(generated instanceof ReservedDeviceAlias)) return failure('key_unavailable')
      const alias = generated

      let reservation: 'stored' | 'conflict' | 'unavailable' | 'indeterminate'
      try { reservation = await this.#registry.compareAndSet(key, snapshot, 'reserved', alias) } catch {
        return failure('storage_unavailable')
      }
      if (reservation === 'conflict') {
        const winner = await this.#readSnapshot(key)
        if (!isSnapshot(winner)) return failure('storage_unavailable')
        if (!winner.found) return failure('reservation_conflict')
        if (winner.status === 'reserved') return failure('reservation_incomplete')
        return this.#readActive(winner)
      }
      if (reservation !== 'stored') return failure('storage_unavailable')

      // Reservation is durable before native creation. Any failure leaves the alias reserved;
      // a later call can only read that alias and must never generate a replacement.
      let created: DeviceKeyPortResult
      try { created = await this.#deviceKeys.createAtReservedAlias(alias) } catch {
        return failure('creation_uncertain')
      }
      if (created.status === 'alias_conflict') return failure('creation_conflict')
      if (created.status !== 'ready') return failure('creation_uncertain')
      if (!(created.alias instanceof ReservedDeviceAlias) ||
        created.alias.forDeviceKeyBridge() !== alias.forDeviceKeyBridge() || !validPublicKey(created.publicKey)) {
        return failure('creation_uncertain')
      }
      return this.#activateCreatedReservation(key, alias, created.publicKey)
    })
  }

  async #readSnapshot(key: EmailRegistryKey): Promise<unknown> {
    try { return await this.#registry.read(key) } catch { return undefined }
  }

  async #activateCreatedReservation(
    key: EmailRegistryKey,
    alias: ReservedDeviceAlias,
    publicKey: DevicePublicKey,
  ): Promise<DeviceIdentityOutcome> {
    // This private path is called only while this invocation still holds the explicit native
    // create-success result. A reserved snapshot from a later invocation cannot reach it.
    let expected = await this.#readSnapshot(key)
    if (!isSnapshot(expected)) return failure('storage_unavailable')
    if (expected.status !== 'reserved' ||
      expected.aliasForCoordinator()?.forDeviceKeyBridge() !== alias.forDeviceKeyBridge()) {
      return failure('activation_conflict')
    }

    for (let attempt = 0; attempt < 3; attempt++) {
      let activated: 'stored' | 'conflict' | 'unavailable' | 'indeterminate'
      try { activated = await this.#registry.compareAndSet(key, expected, 'active', alias) } catch {
        return failure('activation_uncertain')
      }
      if (activated === 'stored') return ready(this, alias, publicKey)
      if (activated === 'indeterminate') return failure('activation_uncertain')
      if (activated === 'unavailable') return failure('storage_unavailable')
      if (attempt === 2) return failure('activation_conflict')

      // CAS may conflict because another email updated the shared registry revision. Only this
      // invocation's create-success proof permits rereading the same reservation and retrying.
      expected = await this.#readSnapshot(key)
      if (!isSnapshot(expected)) return failure('storage_unavailable')
      if (expected.status !== 'reserved' ||
        expected.aliasForCoordinator()?.forDeviceKeyBridge() !== alias.forDeviceKeyBridge()) {
        return failure('activation_conflict')
      }
    }
    return failure('activation_conflict')
  }

  async #readActive(snapshot: DeviceIdentityRegistrySnapshot): Promise<DeviceIdentityOutcome> {
    if (snapshot.status !== 'active') return failure('reservation_incomplete')
    const alias = snapshot.aliasForCoordinator()
    if (!alias) return failure('storage_corrupt')
    let result: DeviceKeyPortResult
    try { result = await this.#deviceKeys.readExisting(alias) } catch { return failure('key_unavailable') }
    if (result.status === 'missing') return failure('missing_key')
    if (result.status !== 'ready') return failure('key_unavailable')
    if (!(result.alias instanceof ReservedDeviceAlias) ||
      result.alias.forDeviceKeyBridge() !== alias.forDeviceKeyBridge() || !validPublicKey(result.publicKey)) return failure('key_unavailable')
    return ready(this, alias, result.publicKey)
  }

  async #withEmailLock<T>(key: string, operation: () => Promise<T>): Promise<T> {
    const previous = this.#locks.get(key) ?? Promise.resolve()
    let release!: () => void
    const current = new Promise<void>(resolve => { release = resolve })
    this.#locks.set(key, current)
    await previous
    try { return await operation() } finally {
      release()
      if (this.#locks.get(key) === current) this.#locks.delete(key)
    }
  }
}

/** Helper for native random adapters; validates representation, not entropy. */
export function aliasFromRandomBytes(bytes: Uint8Array): ReservedDeviceAlias | null {
  if (!(bytes instanceof Uint8Array) || bytes.length !== 32) return null
  const encoded = encodeBase64URL(bytes)
  return isCanonicalAlias(encoded) ? new ReservedDeviceAlias(encoded) : null
}
