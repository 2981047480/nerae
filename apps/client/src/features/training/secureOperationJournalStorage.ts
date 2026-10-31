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

export interface JournalAuthCredential {
  readonly subjectId: string
  readonly generation: string
}

/** Deliberately mirrors RecordingAuth without exposing or reading its token. */
export interface JournalAuth {
  current(): JournalAuthCredential | null
  isCurrent(subjectId: string, generation: string): boolean
}

export type SecureJournalStorageErrorCode = 'unavailable' | 'corrupt' | 'invalid_operation' | 'too_large' | 'session_changed'

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
const FREE_CREATE_KEYS = ['clientEventId', 'date', 'source', 'timeZone'] as const
const PLANNED_CREATE_KEYS = ['clientEventId', 'date', 'occurrenceId', 'planDayId', 'planId', 'planVersion', 'source', 'timeZone'] as const
const SET_BATCH_KEYS = ['deleteRecordKeys', 'expectedVersion', 'exerciseEntryKeys', 'restoreRecordKeys', 'sets'] as const
const SET_WRITE_KEYS = ['completed', 'exerciseEntryKey', 'exerciseId', 'exerciseName', 'kind', 'recordKey', 'reps', 'rpe', 'seconds', 'setIndex', 'weightKg'] as const
const FINISH_KEYS = ['date', 'durationMin', 'expectedVersion', 'note', 'rpe'] as const

type Registry = { schema: 1; entries: Record<string, string> }

function utf8Length(value: string): number {
  let bytes = 0
  for (let i = 0; i < value.length; i++) {
    const code = value.charCodeAt(i)
    if (code <= 0x7f) bytes += 1
    else if (code <= 0x7ff) bytes += 2
    else if (code >= 0xd800 && code <= 0xdbff) {
      const low = value.charCodeAt(i + 1)
      if (!(low >= 0xdc00 && low <= 0xdfff)) throw new SecureJournalStorageError('invalid_operation')
      bytes += 4
      i++
    } else if (code >= 0xdc00 && code <= 0xdfff) throw new SecureJournalStorageError('invalid_operation')
    else bytes += 3
    if (bytes > MAX_BYTES) throw new SecureJournalStorageError('too_large')
  }
  return bytes
}

function assertWellFormedJsonStrings(value: unknown): void {
  const pending: unknown[] = [value]
  while (pending.length > 0) {
    const current = pending.pop()
    if (typeof current === 'string') {
      // JSON.parse accepts escaped lone UTF-16 surrogates. They are not valid
      // Unicode scalar strings and can be lossy across native/JSON boundaries.
      for (let i = 0; i < current.length; i++) {
        const code = current.charCodeAt(i)
        if (code >= 0xd800 && code <= 0xdbff) {
          const low = current.charCodeAt(i + 1)
          if (!(low >= 0xdc00 && low <= 0xdfff)) throw new SecureJournalStorageError('invalid_operation')
          i++
        } else if (code >= 0xdc00 && code <= 0xdfff) {
          throw new SecureJournalStorageError('invalid_operation')
        }
      }
    } else if (Array.isArray(current)) {
      for (const item of current) pending.push(item)
    } else if (current && typeof current === 'object') {
      for (const [key, item] of Object.entries(current)) pending.push(key, item)
    }
  }
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
  assertWellFormedJsonStrings(parsed)
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
  assertWellFormedJsonStrings(body)
  if (!body || typeof body !== 'object' || Array.isArray(body) || JSON.stringify(body) !== req.body) {
    throw new SecureJournalStorageError('invalid_operation')
  }
  validateRequestBody(req.path, body as Record<string, unknown>, owner.eventId)
}

function exactKeys(value: Record<string, unknown>, keys: readonly string[]): boolean {
  const actual = Object.keys(value).sort()
  const expected = [...keys].sort()
  return actual.length === expected.length && actual.every((key, index) => key === expected[index])
}

function objectWithAllowedKeys(value: unknown, allowed: readonly string[], required: readonly string[] = []): value is Record<string, unknown> {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return false
  const record = value as Record<string, unknown>
  const keys = Object.keys(record)
  return keys.every(key => allowed.includes(key)) && required.every(key => Object.prototype.hasOwnProperty.call(record, key))
}

function validString(value: unknown, min: number, max: number): value is string {
  return typeof value === 'string' && Array.from(value).length >= min && Array.from(value).length <= max
}

function validKeyString(value: unknown): value is string {
  return validString(value, 8, 64) && !/[\r\n]/.test(value) && Boolean(value.trim())
}

function validPositiveInteger(value: unknown): value is number {
  return Number.isSafeInteger(value) && Number(value) >= 1
}

function validDecimalPlaces(value: number, places: number): boolean {
  if (!Number.isFinite(value) || !Number.isSafeInteger(places) || places < 0) return false
  // Operation bodies must already be canonical JSON (checked above), so the
  // ECMAScript JSON number serialization is the exact decimal representation
  // whose precision we need to bound. Do not use a floating-point epsilon:
  // that can admit values with extra decimal places near an allowed value.
  const decimal = JSON.stringify(value)
  if (typeof decimal !== 'string') return false
  const match = /^-?(?:\d+)(?:\.(\d+))?(?:e([+-]?\d+))?$/i.exec(decimal)
  if (!match) return false
  const fractionalDigits = match[1]?.length ?? 0
  const exponent = match[2] === undefined ? 0 : Number(match[2])
  if (!Number.isSafeInteger(exponent)) return false
  return fractionalDigits - exponent <= places
}

function validNumericId(value: unknown): value is string {
  return typeof value === 'string' && /^[1-9]\d{0,19}$/.test(value)
}

function validUniqueKeyArray(value: unknown, minItems: number, maxItems: number): value is string[] {
  return Array.isArray(value) && value.length >= minItems && value.length <= maxItems &&
    value.every(validKeyString) && new Set(value).size === value.length
}

/** Field-shape guard only; OpenAPI/server validation remains authoritative. */
function validateRequestBody(path: string, body: Record<string, unknown>, eventId: string): void {
  if (path === '/me/sessions') {
    const common = body.source === 'free' ? FREE_CREATE_KEYS : body.source === 'plan' ? PLANNED_CREATE_KEYS : null
    if (!common) throw new SecureJournalStorageError('invalid_operation')
    const required = body.source === 'free'
      ? ['source', 'date', 'clientEventId']
      : ['source', 'date', 'planId', 'planVersion', 'planDayId', 'clientEventId']
    if (!objectWithAllowedKeys(body, common, required) || body.clientEventId !== eventId ||
      typeof body.date !== 'string' || !validString(body.clientEventId, 8, 64) ||
      (body.timeZone !== undefined && !validString(body.timeZone, 1, 64))) {
      throw new SecureJournalStorageError('invalid_operation')
    }
    if (body.source === 'plan' && (!validNumericId(body.planId) || !validNumericId(body.planDayId) ||
      !validPositiveInteger(body.planVersion) || body.occurrenceId !== undefined && !validNumericId(body.occurrenceId))) {
      throw new SecureJournalStorageError('invalid_operation')
    }
    return
  }

  if (path.endsWith('/sets')) {
    if (!objectWithAllowedKeys(body, SET_BATCH_KEYS, ['expectedVersion', 'exerciseEntryKeys']) ||
      !validPositiveInteger(body.expectedVersion) || !validUniqueKeyArray(body.exerciseEntryKeys, 0, 2000)) {
      throw new SecureJournalStorageError('invalid_operation')
    }
    if (body.sets !== undefined && (!Array.isArray(body.sets) || body.sets.length < 1 || body.sets.length > 200 ||
      !body.sets.every(validateSetWrite))) throw new SecureJournalStorageError('invalid_operation')
    if (body.deleteRecordKeys !== undefined && !validUniqueKeyArray(body.deleteRecordKeys, 1, 200)) {
      throw new SecureJournalStorageError('invalid_operation')
    }
    if (body.restoreRecordKeys !== undefined && (!body.sets || !validUniqueKeyArray(body.restoreRecordKeys, 1, 200))) {
      throw new SecureJournalStorageError('invalid_operation')
    }
    const sets = (body.sets || []) as Record<string, unknown>[]
    const deletes = (body.deleteRecordKeys || []) as string[]
    const restores = (body.restoreRecordKeys || []) as string[]
    if (sets.length + deletes.length > 200) throw new SecureJournalStorageError('invalid_operation')
    const recordKeys = new Set<string>()
    const indexes = new Map<string, Set<number>>()
    const identities = new Map<string, string>()
    for (const set of sets) {
      const recordKey = set.recordKey as string
      const entryKey = set.exerciseEntryKey as string
      const setIndex = set.setIndex as number
      if (recordKeys.has(recordKey)) throw new SecureJournalStorageError('invalid_operation')
      recordKeys.add(recordKey)
      const identity = set.exerciseId === undefined ? `name:${set.exerciseName}` : `id:${set.exerciseId}`
      const existingIdentity = identities.get(entryKey)
      if (existingIdentity !== undefined && existingIdentity !== identity) throw new SecureJournalStorageError('invalid_operation')
      identities.set(entryKey, identity)
      const entryIndexes = indexes.get(entryKey) || new Set<number>()
      if (entryIndexes.has(setIndex)) throw new SecureJournalStorageError('invalid_operation')
      entryIndexes.add(setIndex)
      indexes.set(entryKey, entryIndexes)
    }
    if (sets.some(set => !((body.exerciseEntryKeys as string[]).includes(set.exerciseEntryKey as string))) ||
      deletes.some(key => recordKeys.has(key)) || restores.some(key => !recordKeys.has(key)) ||
      deletes.some(key => restores.includes(key))) throw new SecureJournalStorageError('invalid_operation')
    return
  }

  if (path.endsWith('/finish')) {
    if (!objectWithAllowedKeys(body, FINISH_KEYS, ['expectedVersion']) || !validPositiveInteger(body.expectedVersion) ||
      body.date !== undefined && typeof body.date !== 'string' ||
      body.durationMin !== undefined && body.durationMin !== null && (!Number.isSafeInteger(body.durationMin) || Number(body.durationMin) < 0) ||
      body.rpe !== undefined && body.rpe !== null && (typeof body.rpe !== 'number' || !Number.isFinite(body.rpe) || body.rpe < 1 || body.rpe > 10 || !validDecimalPlaces(body.rpe, 1)) ||
      body.note !== undefined && body.note !== null && !validString(body.note, 0, 2000)) {
      throw new SecureJournalStorageError('invalid_operation')
    }
    return
  }
  throw new SecureJournalStorageError('invalid_operation')
}

function validateSetWrite(value: unknown): boolean {
  if (!objectWithAllowedKeys(value, SET_WRITE_KEYS, ['recordKey', 'exerciseEntryKey', 'setIndex', 'kind']) ||
    !validKeyString(value.recordKey) || !validKeyString(value.exerciseEntryKey) || !validPositiveInteger(value.setIndex) ||
    !['weight', 'perSide', 'bodyweight', 'bodyweightPerSide', 'duration', 'durationPerSide', 'breath'].includes(String(value.kind))) return false
  const hasExerciseId = value.exerciseId !== undefined
  const hasExerciseName = value.exerciseName !== undefined
  if (hasExerciseId === hasExerciseName ||
    hasExerciseId && !validNumericId(value.exerciseId) ||
    hasExerciseName && (!validString(value.exerciseName, 1, 60) || !value.exerciseName.trim()) ||
    value.weightKg !== undefined && (typeof value.weightKg !== 'number' || !Number.isFinite(value.weightKg) || value.weightKg <= 0 || value.weightKg > 300) ||
    value.reps !== undefined && (!Number.isSafeInteger(value.reps) || Number(value.reps) < 1 || Number(value.reps) > 100) ||
    value.seconds !== undefined && (!Number.isSafeInteger(value.seconds) || Number(value.seconds) < 5 || Number(value.seconds) > 600) ||
    value.rpe !== undefined && value.rpe !== null && (typeof value.rpe !== 'number' || !Number.isFinite(value.rpe) || value.rpe < 1 || value.rpe > 10) ||
    value.completed !== true) return false
  const kind = value.kind
  if (value.rpe !== undefined && value.rpe !== null && !validDecimalPlaces(value.rpe as number, 1)) return false
  if (kind === 'weight' || kind === 'perSide') {
    return typeof value.weightKg === 'number' && Number.isFinite(value.weightKg) && value.weightKg > 0 && value.weightKg <= 300 &&
      Number.isSafeInteger(value.reps) && Number(value.reps) >= 1 && Number(value.reps) <= 100 && value.seconds === undefined &&
      validDecimalPlaces(value.weightKg, 2)
  }
  if (kind === 'duration' || kind === 'durationPerSide') {
    return value.weightKg === undefined && value.reps === undefined && Number.isSafeInteger(value.seconds) &&
      Number(value.seconds) >= 5 && Number(value.seconds) <= 600
  }
  return value.weightKg === undefined && value.seconds === undefined && Number.isSafeInteger(value.reps) &&
    Number(value.reps) >= 1 && Number(value.reps) <= 100
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
  try { assertWellFormedJsonStrings(parsed) } catch { throw new SecureJournalStorageError('corrupt') }
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
  readonly #auth: JournalAuth
  readonly #subjectId: string
  readonly #generation: string

  constructor(native: NativeTrainingJournalPort, auth: JournalAuth) {
    if (!native || typeof native.readTrainingOperationJournal !== 'function' ||
      typeof native.compareAndSetTrainingOperationJournal !== 'function' || !auth ||
      typeof auth.current !== 'function' || typeof auth.isCurrent !== 'function') {
      throw new SecureJournalStorageError('unavailable')
    }
    let credential: JournalAuthCredential | null
    try { credential = auth.current() } catch { credential = null }
    if (!credential || !/^[1-9]\d{0,19}$/.test(credential.subjectId) ||
      typeof credential.generation !== 'string' || credential.generation.length < 1 || credential.generation.length > 128 ||
      /[\r\n\s]/.test(credential.generation)) throw new SecureJournalStorageError('session_changed')
    this.#native = native
    this.#auth = auth
    this.#subjectId = credential.subjectId
    this.#generation = credential.generation
    this.#assertCurrent()
  }

  async read(key: string): Promise<unknown> {
    this.#assertKeyOwner(key)
    this.#assertCurrent()
    const registry = await this.#readRegistry()
    this.#assertCurrent()
    return Object.prototype.hasOwnProperty.call(registry.entries, key) ? registry.entries[key] : null
  }

  async createIfAbsent(key: string, value: string): Promise<boolean> {
    this.#assertKeyOwner(key)
    validateOperation(key, value)
    return this.#mutate(key, undefined, value)
  }

  async compareAndSwap(key: string, expectedValue: string, nextValue: string | null): Promise<boolean> {
    this.#assertKeyOwner(key)
    validateOperation(key, expectedValue)
    if (nextValue !== null) validateOperation(key, nextValue)
    return this.#mutate(key, expectedValue, nextValue)
  }

  async #readNative(): Promise<{ found: boolean; value: string }> {
    this.#assertCurrent()
    let raw: unknown
    try { raw = this.#native.readTrainingOperationJournal() }
    catch {
      this.#assertCurrent()
      throw new SecureJournalStorageError('unavailable')
    }
    this.#assertCurrent()
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
    this.#assertKeyOwner(key)
    for (let attempt = 0; attempt < MAX_CAS_ATTEMPTS; attempt++) {
      const current = await this.#readNative()
      this.#assertCurrent()
      const registry = current.found ? decodeRegistry(current.value) : { schema: 1 as const, entries: {} }
      const hasKey = Object.prototype.hasOwnProperty.call(registry.entries, key)
      if (expected === undefined ? hasKey : !hasKey || registry.entries[key] !== expected) return false
      if (next === null && !hasKey) return false

      const updated = { ...registry.entries }
      if (next === null) delete updated[key]
      else updated[key] = next
      const encoded = encodeRegistry(updated)
      this.#assertCurrent()
      let rawResult: unknown
      try {
        rawResult = this.#native.compareAndSetTrainingOperationJournal(current.found, current.value, encoded)
      } catch {
        // The native call may have committed before throwing. Never infer success
        // or conflict from a transport/bridge exception.
        this.#assertCurrent()
        throw new SecureJournalStorageError('unavailable')
      }
      this.#assertCurrent()
      const outcome = validateNativeResult(rawResult, 'cas')
      if (outcome !== false) return true
    }
    throw new SecureJournalStorageError('unavailable')
  }

  #assertKeyOwner(key: unknown): void {
    const owner = parseStorageKey(key)
    if (owner.subjectId !== this.#subjectId) throw new SecureJournalStorageError('session_changed')
  }

  #assertCurrent(): void {
    try {
      const current = this.#auth.current()
      if (!current || current.subjectId !== this.#subjectId || current.generation !== this.#generation ||
        !this.#auth.isCurrent(this.#subjectId, this.#generation)) throw new SecureJournalStorageError('session_changed')
    } catch (error) {
      if (error instanceof SecureJournalStorageError) throw error
      throw new SecureJournalStorageError('session_changed')
    }
  }
}
