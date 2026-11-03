import { createDeviceKeyAtReservedAlias, readExistingDevicePublicKey, signExistingDeviceProof, type DeviceKeyResult } from '@/uni_modules/nerae-device-key'
import { generateSecureRandomRequestKey } from '@/uni_modules/nerae-random'
import { compareAndSetDeviceIdentityRegistry, readDeviceIdentityRegistry, type SecureVaultResult } from '@/uni_modules/nerae-secure-vault'
import {
  DeviceIdentityCoordinator, DeviceIdentityRegistrySnapshot, ReservedDeviceAlias, identityWasIssuedBy, normalizeEmailForDeviceRegistry,
  type ProtectedDeviceIdentity,
  type DeviceKeyPortResult, type EmailRegistryKey, type ProtectedDeviceIdentityRegistry,
} from '../../features/auth/deviceIdentityCoordinator'
import { approvalSecretsForSecureStorage, challengeNonceForSigning, type ApprovalRequired, type Challenge } from '../../api/auth/emailAuthApi'

type Entry = { email: string; alias: string; status: 'reserved' | 'active' }
type Registry = { schema: 1; entries: Entry[] }
type Observed = { email: string; found: boolean; raw: string; entries: Entry[] }

function decodeRegistry(raw: string): Registry {
  // Email and alias are ASCII; this bounds the encoded JSON to the vault's 64 KiB limit.
  if (!raw || raw.length > 64 * 1024) throw new Error('device registry unavailable')
  let value: unknown
  try { value = JSON.parse(raw) } catch { throw new Error('device registry unavailable') }
  if (!value || typeof value !== 'object' || Array.isArray(value) || JSON.stringify(value) !== raw) {
    throw new Error('device registry unavailable')
  }
  const candidate = value as Record<string, unknown>
  if (Object.keys(candidate).join(',') !== 'schema,entries' || candidate.schema !== 1 || !Array.isArray(candidate.entries)) {
    throw new Error('device registry unavailable')
  }
  const emails = new Set<string>()
  const aliases = new Set<string>()
  const entries: Entry[] = []
  for (const item of candidate.entries) {
    if (!item || typeof item !== 'object' || Array.isArray(item)) throw new Error('device registry unavailable')
    const entry = item as Record<string, unknown>
    if (Object.keys(entry).join(',') !== 'email,alias,status' || typeof entry.email !== 'string' || typeof entry.alias !== 'string' ||
      (entry.status !== 'reserved' && entry.status !== 'active') ||
      normalizeEmailForDeviceRegistry(entry.email)?.forProtectedRegistry() !== entry.email ||
      emails.has(entry.email) || aliases.has(entry.alias)) throw new Error('device registry unavailable')
    try { new ReservedDeviceAlias(entry.alias) } catch { throw new Error('device registry unavailable') }
    emails.add(entry.email)
    aliases.add(entry.alias)
    entries.push({ email: entry.email, alias: entry.alias, status: entry.status })
  }
  return { schema: 1, entries }
}

function validRead(result: SecureVaultResult): boolean {
  return !!result && result.ok === true && result.errorCode === '' && typeof result.found === 'boolean' &&
    typeof result.value === 'string' && (result.found ? result.value.length > 0 : result.value === '')
}

/** Registry CAS is native and process-local; there is no plaintext uni storage fallback. */
class NativeDeviceRegistry implements ProtectedDeviceIdentityRegistry {
  #observed = new WeakMap<DeviceIdentityRegistrySnapshot, Observed>()
  #nextRevision = 0

  async read(key: EmailRegistryKey): Promise<DeviceIdentityRegistrySnapshot> {
    const email = key.forProtectedRegistry()
    if (normalizeEmailForDeviceRegistry(email)?.forProtectedRegistry() !== email) throw new Error('device registry unavailable')
    const result = readDeviceIdentityRegistry()
    if (!validRead(result)) throw new Error('device registry unavailable')
    const entries = result.found ? decodeRegistry(result.value).entries : []
    if (this.#nextRevision >= Number.MAX_SAFE_INTEGER) throw new Error('device registry unavailable')
    const revision = String(++this.#nextRevision)
    const entry = entries.find(item => item.email === email)
    const snapshot = !entry ? DeviceIdentityRegistrySnapshot.missing(revision)
      : entry.status === 'reserved' ? DeviceIdentityRegistrySnapshot.reserved(revision, entry.alias)
      : DeviceIdentityRegistrySnapshot.active(revision, entry.alias)
    this.#observed.set(snapshot, { email, found: result.found, raw: result.value, entries })
    return snapshot
  }

  async compareAndSet(key: EmailRegistryKey, expected: DeviceIdentityRegistrySnapshot, nextStatus: 'reserved' | 'active', alias: ReservedDeviceAlias):
    Promise<'stored' | 'conflict' | 'unavailable' | 'indeterminate'> {
    const previous = this.#observed.get(expected)
    this.#observed.delete(expected)
    if (!previous || previous.email !== key.forProtectedRegistry()) return 'conflict'
    const value = alias.forDeviceKeyBridge()
    let entries: Entry[]
    if (nextStatus === 'reserved') {
      if (expected.status !== 'missing' || previous.entries.some(entry => entry.email === previous.email || entry.alias === value)) {
        return 'conflict'
      }
      entries = [...previous.entries, { email: previous.email, alias: value, status: 'reserved' }]
    } else if (nextStatus === 'active') {
      const match = previous.entries.find(entry => entry.email === previous.email)
      if (expected.status !== 'reserved' || !match || match.status !== 'reserved' || match.alias !== value ||
        previous.entries.some(entry => entry.email !== previous.email && entry.alias === value)) return 'conflict'
      entries = previous.entries.map(entry => entry.email === previous.email ? { ...entry, status: 'active' } : entry)
    } else return 'conflict'
    const next = JSON.stringify({ schema: 1, entries } satisfies Registry)
    if (next.length > 64 * 1024) return 'unavailable'
    let result: SecureVaultResult
    try { result = compareAndSetDeviceIdentityRegistry(previous.found, previous.raw, next) }
    catch { return 'indeterminate' }
    if (!result || result.found !== false || result.value !== '') return 'indeterminate'
    if (result.ok === true && result.errorCode === '') return 'stored'
    if (result.ok === false && result.errorCode === 'conflict') return 'conflict'
    // Native failures can follow a committed write; do not create a key until
    // a future read has established the persisted reservation.
    return 'indeterminate'
  }
}

function keyResult(result: DeviceKeyResult, alias: ReservedDeviceAlias): DeviceKeyPortResult {
  if (!result || result.ok !== true) return { status: result?.errorCode === 'missing_key' ? 'missing'
    : result?.errorCode === 'alias_conflict' ? 'alias_conflict' : 'unavailable' }
  if (result.errorCode !== '' || result.alias !== alias.forDeviceKeyBridge() || result.proof !== '' ||
    !result.publicKey || result.publicKey.kty !== 'EC' || result.publicKey.crv !== 'P-256') return { status: 'unavailable' }
  return { status: 'ready', alias, publicKey: { kty: 'EC', crv: 'P-256', x: result.publicKey.x, y: result.publicKey.y } }
}

let coordinator: DeviceIdentityCoordinator | undefined

/** One coordinator per App JS process; native vault CAS does not cover other processes. */
export function createAppDeviceIdentityCoordinator(): DeviceIdentityCoordinator {
  if (coordinator) return coordinator
  coordinator = new DeviceIdentityCoordinator(new NativeDeviceRegistry(), {
    async generateAlias() {
      const result = generateSecureRandomRequestKey()
      if (!result || result.ok !== true || result.errorCode !== '') throw new Error('secure randomness unavailable')
      return new ReservedDeviceAlias(result.value)
    },
  }, {
    async createAtReservedAlias(alias) { return keyResult(createDeviceKeyAtReservedAlias(alias.forDeviceKeyBridge()), alias) },
    async readExisting(alias) { return keyResult(readExistingDevicePublicKey(alias.forDeviceKeyBridge()), alias) },
  })
  return coordinator
}

/** The proof is kept out of ordinary JSON and must be explicitly obtained for the login request. */
export interface AppEmailLoginProof { forSubmission(): string }

class NativeEmailLoginProof implements AppEmailLoginProof {
  #value: string
  constructor(value: string) { this.#value = value; Object.freeze(this) }
  forSubmission(): string { return this.#value }
  toJSON(): string { return '<redacted>' }
  toString(): string { return '<redacted email device proof>' }
}

/** Sign an adapter-issued challenge using only the already-existing device key. */
export function signAppEmailLoginChallenge(identity: ProtectedDeviceIdentity, challenge: Challenge): AppEmailLoginProof | null {
  try {
    if (!coordinator || !identityWasIssuedBy(identity, coordinator) || !challenge || typeof challenge.challengeId !== 'string') return null
    const nonce = challengeNonceForSigning(challenge)
    return signExisting(identity, 'email_login', challenge.challengeId, nonce)
  } catch {
    return null
  }
}

/** Sign only an adapter-issued approval using the existing device identity. */
export function signAppApprovalExchange(identity: ProtectedDeviceIdentity, approval: ApprovalRequired): AppEmailLoginProof | null {
  try {
    if (!coordinator || !identityWasIssuedBy(identity, coordinator) || !approval || typeof approval.approvalId !== 'string') return null
    const { nonce } = approvalSecretsForSecureStorage(approval)
    return signExisting(identity, 'approval_exchange', approval.approvalId, nonce)
  } catch {
    return null
  }
}

function signExisting(identity: ProtectedDeviceIdentity, purpose: 'email_login' | 'approval_exchange', resource: string, nonce: string): AppEmailLoginProof | null {
  try {
    const alias = identity.aliasForProtocol
    const result = signExistingDeviceProof(alias, purpose, resource, nonce)
    if (!result || result.ok !== true || result.errorCode !== '' || result.alias !== alias ||
      !result.publicKey || result.publicKey.kty !== 'EC' || result.publicKey.crv !== 'P-256' ||
      result.publicKey.x !== '' || result.publicKey.y !== '' || typeof result.proof !== 'string' ||
      result.proof.length < 32 || result.proof.length > 4096 ||
      !/^[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+$/.test(result.proof)) return null
    return new NativeEmailLoginProof(result.proof)
  } catch {
    return null
  }
}
