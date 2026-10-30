/**
 * Adapts the single protected native training-journal value to the per-key
 * JournalStorage contract used by TrainingOperationJournal (2d405b4).
 * Deleting a logical entry never clears the native slot.
 */

export interface NativeTrainingJournalPort {
  readTrainingOperationJournal(): unknown
  compareAndSetTrainingOperationJournal(expectedFound: boolean, expectedValue: string, value: string): unknown
}

export interface JournalStorage {
  read(key: string): Promise<unknown>
  createIfAbsent(key: string, value: string): Promise<boolean>
  compareAndSwap(key: string, expectedValue: string, nextValue: string | null): Promise<boolean>
}

export type SecureJournalStorageErrorCode = 'unavailable' | 'corrupt' | 'invalid_operation' | 'too_large'

export class SecureJournalStorageError extends Error {
  readonly code: SecureJournalStorageErrorCode

  constructor(code: SecureJournalStorageErrorCode) {
    super(code)
    this.name = 'SecureJournalStorageError'
    this.code = code
  }
}

const MAX_BYTES = 65_536
const MAX_ENTRIES = 64
const MAX_CAS_ATTEMPTS = 8
const REGISTRY_KEYS = ['entries', 'schema'] as const
const FORBIDDEN_BODY_KEYS = new Set(['authorization', 'accesstoken', 'refreshtoken', 'token', 'password', 'secret', 'proof'])

type Registry = { schema: 1; entries: Record<string, string> }

function utf8Length(value: string): number {
  let bytes = 0
  for (let i = 0; i < value.length; i++) {
    const code = value.charCodeAt(i)
    if (code <= 0x7f) bytes += 1
    else if (code <= 0x7ff) bytes += 2
    else if (code >= 0xd800 && code <= 0xdbff) {
      const low = value.charCodeAt(i + 1)
      if (low < 0xdc00 || low > 0xdfff) throw new SecureJournalStorageError('invalid_operation')
      bytes += 4
      i++
    } else if (code >= 0xdc00 && code <= 0xdfff) throw new SecureJournalStorageError('invalid_operation')
    else bytes += 3
    if (bytes > MAX_BYTES) throw new SecureJournalStorageError('too_large')
  }
  return bytes
}

function parseStorageKey(key: unknown): { subjectId: string; eventId: string } {
  if (typeof key !== 'string') throw new SecureJournalStorageError('invalid_operation')
  const match = /^nerae\.workout\.operation\.v1\.([1-9]\d{0,19})\.(.+)$/.exec(key)
  if (!match) throw new SecureJournalStorageError('invalid_operation')
  let eventId: string
  try { eventId = decodeURIComponent(match[2]) } catch { throw new SecureJournalStorageError('invalid_operation') }
  let canonicalKey: string
  try { canonicalKey = encodeURIComponent(eventId) } catch { throw new SecureJournalStorageError('invalid_operation') }
  if (Array.from(eventId).length < 8 || Array.from(eventId).length > 64 || !eventId.trim() ||
    /[\uD800-\uDFFF]/.test(eventId.replace(/[\uD800-\uDBFF][\uDC00-\uDFFF]/g, '')) || canonicalKey !== match[2]) {
    throw new SecureJournalStorageError('invalid_operation')
  }
  return { subjectId: match[1], eventId }
}

function validateOperation(key: string, value: unknown): asserts value is string {
  if (typeof value !== 'string') throw new SecureJournalStorageError('invalid_operation')
  utf8Length(value)
  let parsed: unknown
  try { parsed = JSON.parse(value) } catch { throw new SecureJournalStorageError('invalid_operation') }
  if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed) || JSON.stringify(parsed) !== value) {
    throw new SecureJournalStorageError('invalid_operation')
  }
  const item = parsed as Record<string, unknown>
  const owner = parseStorageKey(key)
  const itemKeys = Object.keys(item)
  const keysWithAttempt = ['attemptGeneration', 'clientEventId', 'createdAt', 'createdGeneration', 'expiresAt', 'request', 'revision', 'schema', 'state', 'subjectId']
  const keysWithoutAttempt = keysWithAttempt.filter(field => field !== 'attemptGeneration')
  if (!(itemKeys.length === keysWithAttempt.length && keysWithAttempt.every(field => itemKeys.includes(field))) &&
    !(itemKeys.length === keysWithoutAttempt.length && keysWithoutAttempt.every(field => itemKeys.includes(field)))) {
    throw new SecureJournalStorageError('invalid_operation')
  }
  if (item.subjectId !== owner.subjectId || item.clientEventId !== owner.eventId || item.schema !== 1 ||
    typeof item.createdGeneration !== 'string' || item.createdGeneration.length < 1 || item.createdGeneration.length > 128 ||
    /[\r\n\s]/.test(item.createdGeneration) || !Number.isSafeInteger(item.revision) || Number(item.revision) < 1 ||
    !['ready', 'in_flight', 'uncertain'].includes(String(item.state)) ||
    !Number.isSafeInteger(item.createdAt) || !Number.isSafeInteger(item.expiresAt) || Number(item.expiresAt) <= Number(item.createdAt) ||
    (item.attemptGeneration !== undefined && (typeof item.attemptGeneration !== 'string' || item.attemptGeneration.length < 1 ||
      item.attemptGeneration.length > 128 || /[\r\n\s]/.test(item.attemptGeneration))) ||
    (item.state === 'in_flight') !== (item.attemptGeneration !== undefined)) {
    throw new SecureJournalStorageError('invalid_operation')
  }
  const request = item.request
  if (!request || typeof request !== 'object' || Array.isArray(request)) throw new SecureJournalStorageError('invalid_operation')
  const req = request as Record<string, unknown>
  if (!exactKeys(req, ['body', 'idempotencyKey', 'method', 'path']) || req.method !== 'POST' ||
    typeof req.path !== 'string' || !(req.path === '/me/sessions' || /^\/me\/sessions\/[1-9]\d{0,19}\/(sets|finish)$/.test(req.path)) ||
    typeof req.idempotencyKey !== 'string' || !/^[A-Za-z0-9._~-]{8,128}$/.test(req.idempotencyKey) || typeof req.body !== 'string') {
    throw new SecureJournalStorageError('invalid_operation')
  }
  utf8Length(req.body)
  let body: unknown
  try { body = JSON.parse(req.body) } catch { throw new SecureJournalStorageError('invalid_operation') }
  if (!body || typeof body !== 'object' || Array.isArray(body) || JSON.stringify(body) !== req.body) {
    throw new SecureJournalStorageError('invalid_operation')
  }
  inspectBody(body)
  if (req.path === '/me/sessions' && (body as Record<string, unknown>).clientEventId !== owner.eventId) {
    throw new SecureJournalStorageError('invalid_operation')
  }
}

function exactKeys(value: Record<string, unknown>, keys: readonly string[]): boolean {
  const actual = Object.keys(value).sort()
  const expected = [...keys].sort()
  return actual.length === expected.length && actual.every((key, index) => key === expected[index])
}

function inspectBody(value: unknown, depth = 0): void {
  if (depth > 32) throw new SecureJournalStorageError('invalid_operation')
  if (Array.isArray(value)) {
    if (value.length > 5000) throw new SecureJournalStorageError('invalid_operation')
    for (const item of value) inspectBody(item, depth + 1)
  } else if (value !== null && typeof value === 'object') {
    for (const [field, child] of Object.entries(value as Record<string, unknown>)) {
      if (FORBIDDEN_BODY_KEYS.has(field.toLowerCase())) throw new SecureJournalStorageError('invalid_operation')
      inspectBody(child, depth + 1)
    }
  }
}

function encodeRegistry(entries: Record<string, string>): string {
  if (Object.keys(entries).length > MAX_ENTRIES) throw new SecureJournalStorageError('too_large')
  const sorted: Record<string, string> = {}
  for (const key of Object.keys(entries).sort()) {
    validateOperation(key, entries[key])
    sorted[key] = entries[key]
  }
  const encoded = JSON.stringify({ schema: 1, entries: sorted })
  utf8Length(encoded)
  return encoded
}

function decodeRegistry(value: string): Registry {
  utf8Length(value)
  let parsed: unknown
  try { parsed = JSON.parse(value) } catch { throw new SecureJournalStorageError('corrupt') }
  if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed) || JSON.stringify(parsed) !== value) {
    throw new SecureJournalStorageError('corrupt')
  }
  const registry = parsed as Record<string, unknown>
  if (!exactKeys(registry, REGISTRY_KEYS) || registry.schema !== 1 || !registry.entries ||
    typeof registry.entries !== 'object' || Array.isArray(registry.entries)) {
    throw new SecureJournalStorageError('corrupt')
  }
  const rawEntries = registry.entries as Record<string, unknown>
  if (Object.keys(rawEntries).length > MAX_ENTRIES) throw new SecureJournalStorageError('corrupt')
  const entries: Record<string, string> = {}
  for (const [key, item] of Object.entries(rawEntries)) {
    try { validateOperation(key, item) } catch { throw new SecureJournalStorageError('corrupt') }
    entries[key] = item
  }
  return { schema: 1, entries }
}

function validateNativeResult(raw: unknown, mode: 'read' | 'cas'): { found: boolean; value: string } | false {
  if (!raw || typeof raw !== 'object' || Array.isArray(raw)) throw new SecureJournalStorageError('unavailable')
  const result = raw as Record<string, unknown>
  if (!exactKeys(result, ['ok', 'errorCode', 'found', 'value']) ||
    typeof result.ok !== 'boolean' || typeof result.errorCode !== 'string' ||
    typeof result.found !== 'boolean' || typeof result.value !== 'string') {
    throw new SecureJournalStorageError('unavailable')
  }
  if (!result.ok) {
    if (mode === 'cas' && result.errorCode === 'conflict' && !result.found && result.value === '') return false
    throw new SecureJournalStorageError('unavailable')
  }
  if (result.errorCode !== '') throw new SecureJournalStorageError('unavailable')
  if (mode === 'cas') {
    if (result.found || result.value !== '') throw new SecureJournalStorageError('unavailable')
    return { found: false, value: '' }
  }
  if (result.found === (result.value.length === 0)) throw new SecureJournalStorageError('unavailable')
  return { found: result.found, value: result.value }
}

/** Protected single-slot implementation of the frozen JournalStorage port. */
export class SecureOperationJournalStorage implements JournalStorage {
  readonly #native: NativeTrainingJournalPort

  constructor(native: NativeTrainingJournalPort) {
    if (!native || typeof native.readTrainingOperationJournal !== 'function' ||
      typeof native.compareAndSetTrainingOperationJournal !== 'function') {
      throw new SecureJournalStorageError('unavailable')
    }
    this.#native = native
  }

  async read(key: string): Promise<unknown> {
    parseStorageKey(key)
    const registry = await this.#readRegistry()
    return Object.prototype.hasOwnProperty.call(registry.entries, key) ? registry.entries[key] : null
  }

  async createIfAbsent(key: string, value: string): Promise<boolean> {
    parseStorageKey(key)
    validateOperation(key, value)
    return this.#mutate(key, undefined, value)
  }

  async compareAndSwap(key: string, expectedValue: string, nextValue: string | null): Promise<boolean> {
    parseStorageKey(key)
    validateOperation(key, expectedValue)
    if (nextValue !== null) validateOperation(key, nextValue)
    return this.#mutate(key, expectedValue, nextValue)
  }

  async #readNative(): Promise<{ found: boolean; value: string }> {
    let raw: unknown
    try { raw = this.#native.readTrainingOperationJournal() }
    catch { throw new SecureJournalStorageError('unavailable') }
    const result = validateNativeResult(raw, 'read')
    if (result === false) throw new SecureJournalStorageError('unavailable')
    if (!result.found) return result
    try { decodeRegistry(result.value) } catch (error) {
      if (error instanceof SecureJournalStorageError && error.code === 'too_large') throw error
      throw new SecureJournalStorageError('corrupt')
    }
    return result
  }

  async #readRegistry(): Promise<Registry> {
    const current = await this.#readNative()
    return current.found ? decodeRegistry(current.value) : { schema: 1, entries: {} }
  }

  async #mutate(key: string, expected: string | undefined, next: string | null): Promise<boolean> {
    for (let attempt = 0; attempt < MAX_CAS_ATTEMPTS; attempt++) {
      const current = await this.#readNative()
      const registry = current.found ? decodeRegistry(current.value) : { schema: 1 as const, entries: {} }
      const hasKey = Object.prototype.hasOwnProperty.call(registry.entries, key)
      if (expected === undefined ? hasKey : !hasKey || registry.entries[key] !== expected) return false
      if (next === null && !hasKey) return false

      const updated = { ...registry.entries }
      if (next === null) delete updated[key]
      else updated[key] = next
      const encoded = encodeRegistry(updated)
      let rawResult: unknown
      try {
        rawResult = this.#native.compareAndSetTrainingOperationJournal(current.found, current.value, encoded)
      } catch {
        // The native call may have committed before throwing. Never infer success
        // or conflict from a transport/bridge exception.
        throw new SecureJournalStorageError('unavailable')
      }
      const outcome = validateNativeResult(rawResult, 'cas')
      if (outcome !== false) return true
    }
    throw new SecureJournalStorageError('unavailable')
  }
}
