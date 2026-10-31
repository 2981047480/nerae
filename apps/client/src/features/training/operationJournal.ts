/**
 * Durable intent journal for one pending training write per account/event.
 * This module does not send requests, hold credentials, validate HTTP replies,
 * or grant server access. The injected storage must provide protected-at-rest
 * storage and atomic create/compare-and-swap semantics.
 */

export interface JournalCredential {
  readonly subjectId: string
  readonly generation: string
}

export interface JournalAuth {
  current(): JournalCredential | null
  isCurrent(subjectId: string, generation: string): boolean
}

export interface JournalRequest {
  readonly method: 'POST'
  readonly path: string
  /** Exact canonical JSON string to be reused on retry. */
  readonly body: string
  readonly idempotencyKey: string
}

export interface JournalStorage {
  read(key: string): Promise<unknown>
  /** Atomically persist only when key is absent. */
  createIfAbsent(key: string, value: string): Promise<boolean>
  /** Atomically replace/delete only when current bytes equal expectedValue. */
  compareAndSwap(key: string, expectedValue: string, nextValue: string | null): Promise<boolean>
}

export type JournalErrorCode =
  | 'unauthenticated' | 'session_changed' | 'invalid_operation' | 'conflict'
  | 'busy' | 'unavailable' | 'corrupt' | 'expired' | 'reconciliation_required'

export class OperationJournalError extends Error {
  readonly code: JournalErrorCode

  constructor(code: JournalErrorCode) {
    super(messageFor(code))
    this.name = 'OperationJournalError'
    this.code = code
  }
}

export type JournalState = 'ready' | 'in_flight' | 'uncertain'

export interface PendingOperation {
  readonly clientEventId: string
  readonly subjectId: string
  /** The lease that first staged the operation; a new lease for the same subject may recover it. */
  readonly createdGeneration: string
  readonly state: JournalState
  readonly createdAt: number
  readonly expiresAt: number
  readonly request: JournalRequest
}

export type ReconciliationDecision =
  | { readonly verdict: 'applied' }
  | { readonly verdict: 'unknown' }
export type ReconciliationResult = { readonly state: 'still_uncertain' }

type StoredOperation = {
  schema: 1
  subjectId: string
  clientEventId: string
  createdGeneration: string
  revision: number
  state: JournalState
  createdAt: number
  expiresAt: number
  attemptGeneration?: string
  request: JournalRequest
}

type PermitProof = { key: string; storedBytes: string; subjectId: string; generation: string; eventId: string; active: boolean }
const permits = new WeakMap<object, PermitProof>()

export interface DispatchPermit {
  readonly operation: PendingOperation
  /** Check immediately before invoking the authenticated transport/API. No token is included. */
  isCurrent(): boolean
}

const BODY_LIMIT_BYTES = 512 * 1024

function messageFor(code: JournalErrorCode): string {
  switch (code) {
    case 'unauthenticated': return '请登录后继续保存训练记录'
    case 'session_changed': return '账号或登录会话已变化，请重新恢复记录'
    case 'invalid_operation': return '待发记录无效，未发送'
    case 'conflict': return '已有待核对的训练写入，请先恢复或核对'
    case 'busy': return '该训练写入正在处理中'
    case 'unavailable': return '本机待发记录暂不可用，输入仍保留'
    case 'corrupt': return '本机待发记录无法校验，原数据仍被保留'
    case 'expired': return '待发记录已超过本机重试期限，请先从服务端核对'
    case 'reconciliation_required': return '写入结果尚未确认，原请求和训练内容仍保留'
  }
}

function validSubject(value: unknown): value is string {
  return typeof value === 'string' && /^[1-9]\d{0,19}$/.test(value)
}

function validGeneration(value: unknown): value is string {
  return typeof value === 'string' && value.length > 0 && value.length <= 128 && !/[\r\n\s]/.test(value)
}

function validEventId(value: unknown): value is string {
  return typeof value === 'string' && Array.from(value).length >= 8 && Array.from(value).length <= 64 &&
    Boolean(value.trim()) && !/[\uD800-\uDFFF]/.test(value.replace(/[\uD800-\uDBFF][\uDC00-\uDFFF]/g, ''))
}

function validRequestKey(value: unknown): value is string {
  return typeof value === 'string' && /^[A-Za-z0-9._~-]{8,128}$/.test(value)
}

function validPath(value: unknown): value is string {
  return typeof value === 'string' && (value === '/me/sessions' ||
    /^\/me\/sessions\/[1-9]\d{0,19}\/(sets|finish)$/.test(value))
}

function utf8Length(value: string): number {
  let size = 0
  for (let i = 0; i < value.length; i++) {
    const code = value.charCodeAt(i)
    if (code <= 0x7f) size++
    else if (code <= 0x7ff) size += 2
    else if (code >= 0xd800 && code <= 0xdbff) {
      const next = value.charCodeAt(i + 1)
      if (next < 0xdc00 || next > 0xdfff) throw new OperationJournalError('invalid_operation')
      size += 4
      i++
    } else if (code >= 0xdc00 && code <= 0xdfff) throw new OperationJournalError('invalid_operation')
    else size += 3
    if (size > BODY_LIMIT_BYTES) throw new OperationJournalError('invalid_operation')
  }
  return size
}

const FREE_SESSION_KEYS = ['source', 'date', 'timeZone', 'clientEventId'] as const
const PLANNED_SESSION_KEYS = ['source', 'date', 'timeZone', 'planId', 'planVersion', 'planDayId', 'clientEventId', 'occurrenceId'] as const
const SET_BATCH_KEYS = ['expectedVersion', 'exerciseEntryKeys', 'sets', 'deleteRecordKeys', 'restoreRecordKeys'] as const
const SET_WRITE_KEYS = ['recordKey', 'exerciseEntryKey', 'setIndex', 'kind', 'exerciseId', 'exerciseName', 'weightKg', 'reps', 'seconds', 'rpe', 'completed'] as const
const FINISH_KEYS = ['expectedVersion', 'durationMin', 'date', 'rpe', 'note'] as const

function inspectBoundedBody(value: unknown, depth = 0): void {
  if (depth > 32) throw new OperationJournalError('invalid_operation')
  if (Array.isArray(value)) {
    if (value.length > 5000) throw new OperationJournalError('invalid_operation')
    for (const item of value) inspectBoundedBody(item, depth + 1)
  } else if (value !== null && typeof value === 'object') {
    for (const child of Object.values(value as Record<string, unknown>)) inspectBoundedBody(child, depth + 1)
  }
}

function hasOnlyAllowedKeys(value: unknown, allowed: readonly string[], required: readonly string[]): value is Record<string, unknown> {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return false
  const record = value as Record<string, unknown>
  const keys = Object.keys(record)
  return keys.every(key => allowed.includes(key)) && required.every(key => Object.prototype.hasOwnProperty.call(record, key))
}

function optionalValue(record: Record<string, unknown>, key: string, valid: (value: unknown) => boolean): boolean {
  return !Object.prototype.hasOwnProperty.call(record, key) || valid(record[key])
}

function stringArray(value: unknown): value is string[] {
  return Array.isArray(value) && value.every(item => typeof item === 'string')
}

function validSetWrite(value: unknown): boolean {
  if (!hasOnlyAllowedKeys(value, SET_WRITE_KEYS, ['recordKey', 'exerciseEntryKey', 'setIndex', 'kind'])) return false
  const hasExerciseId = Object.prototype.hasOwnProperty.call(value, 'exerciseId')
  const hasExerciseName = Object.prototype.hasOwnProperty.call(value, 'exerciseName')
  return typeof value.recordKey === 'string' && typeof value.exerciseEntryKey === 'string' &&
    Number.isSafeInteger(value.setIndex) && Number(value.setIndex) >= 1 && typeof value.kind === 'string' &&
    hasExerciseId !== hasExerciseName &&
    optionalValue(value, 'exerciseId', item => typeof item === 'string') &&
    optionalValue(value, 'exerciseName', item => typeof item === 'string') &&
    optionalValue(value, 'weightKg', item => typeof item === 'number' && Number.isFinite(item)) &&
    optionalValue(value, 'reps', item => Number.isSafeInteger(item)) &&
    optionalValue(value, 'seconds', item => Number.isSafeInteger(item)) &&
    optionalValue(value, 'rpe', item => item === null || typeof item === 'number' && Number.isFinite(item)) &&
    optionalValue(value, 'completed', item => item === true)
}

function validateRequestBody(path: string, value: unknown, eventId: string): void {
  if (!value || typeof value !== 'object' || Array.isArray(value)) throw new OperationJournalError('invalid_operation')
  const body = value as Record<string, unknown>
  if (path === '/me/sessions') {
    if (body.source === 'free') {
      if (!hasOnlyAllowedKeys(body, FREE_SESSION_KEYS, ['source', 'date', 'clientEventId']) ||
        typeof body.date !== 'string' || typeof body.clientEventId !== 'string' || body.clientEventId !== eventId ||
        !optionalValue(body, 'timeZone', item => typeof item === 'string')) throw new OperationJournalError('invalid_operation')
      return
    }
    if (body.source === 'plan' && hasOnlyAllowedKeys(body, PLANNED_SESSION_KEYS,
      ['source', 'date', 'planId', 'planVersion', 'planDayId', 'clientEventId']) &&
      typeof body.date === 'string' && typeof body.planId === 'string' &&
      Number.isSafeInteger(body.planVersion) && Number(body.planVersion) >= 1 && typeof body.planDayId === 'string' &&
      typeof body.clientEventId === 'string' && body.clientEventId === eventId &&
      optionalValue(body, 'timeZone', item => typeof item === 'string') &&
      optionalValue(body, 'occurrenceId', item => typeof item === 'string')) return
    throw new OperationJournalError('invalid_operation')
  }

  if (path.endsWith('/sets')) {
    if (!hasOnlyAllowedKeys(body, SET_BATCH_KEYS, ['expectedVersion', 'exerciseEntryKeys']) ||
      !Number.isSafeInteger(body.expectedVersion) || Number(body.expectedVersion) < 1 || !stringArray(body.exerciseEntryKeys) ||
      !optionalValue(body, 'sets', item => Array.isArray(item) && item.every(validSetWrite)) ||
      !optionalValue(body, 'deleteRecordKeys', stringArray) ||
      !optionalValue(body, 'restoreRecordKeys', stringArray)) throw new OperationJournalError('invalid_operation')
    return
  }

  if (path.endsWith('/finish')) {
    if (!hasOnlyAllowedKeys(body, FINISH_KEYS, ['expectedVersion']) ||
      !Number.isSafeInteger(body.expectedVersion) || Number(body.expectedVersion) < 1 ||
      !optionalValue(body, 'durationMin', item => item === null || Number.isSafeInteger(item)) ||
      !optionalValue(body, 'date', item => typeof item === 'string') ||
      !optionalValue(body, 'rpe', item => item === null || typeof item === 'number' && Number.isFinite(item)) ||
      !optionalValue(body, 'note', item => item === null || typeof item === 'string')) throw new OperationJournalError('invalid_operation')
    return
  }
  throw new OperationJournalError('invalid_operation')
}

function normalizeRequest(value: unknown, eventId: string): JournalRequest {
  if (!value || typeof value !== 'object' || Array.isArray(value)) throw new OperationJournalError('invalid_operation')
  const candidate = value as Record<string, unknown>
  if (Object.keys(candidate).sort().join(',') !== 'body,idempotencyKey,method,path' || candidate.method !== 'POST' ||
    !validPath(candidate.path) || !validRequestKey(candidate.idempotencyKey) || typeof candidate.body !== 'string') {
    throw new OperationJournalError('invalid_operation')
  }
  utf8Length(candidate.body)
  let parsed: unknown
  try { parsed = JSON.parse(candidate.body) } catch { throw new OperationJournalError('invalid_operation') }
  if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) throw new OperationJournalError('invalid_operation')
  // RecordingApi serializes contract bodies with JSON.stringify. Requiring that
  // exact representation rejects duplicate JSON keys without rewriting bytes.
  try { if (JSON.stringify(parsed) !== candidate.body) throw new OperationJournalError('invalid_operation') }
  catch (error) {
    if (error instanceof OperationJournalError) throw error
    throw new OperationJournalError('invalid_operation')
  }
  inspectBoundedBody(parsed)
  validateRequestBody(candidate.path, parsed, eventId)
  return Object.freeze({ method: 'POST', path: candidate.path, body: candidate.body, idempotencyKey: candidate.idempotencyKey })
}

function storageKey(subjectId: string, eventId: string): string {
  let encoded: string
  try { encoded = encodeURIComponent(eventId) } catch { throw new OperationJournalError('invalid_operation') }
  return `nerae.workout.operation.v1.${subjectId}.${encoded}`
}

function exactKeys(value: Record<string, unknown>, allowed: readonly string[]): boolean {
  return Object.keys(value).sort().join(',') === [...allowed].sort().join(',')
}

function decode(raw: unknown): StoredOperation {
  if (typeof raw !== 'string') throw new OperationJournalError('corrupt')
  let parsed: unknown
  try { parsed = JSON.parse(raw) } catch { throw new OperationJournalError('corrupt') }
  if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) throw new OperationJournalError('corrupt')
  const value = parsed as Record<string, unknown>
  if (!exactKeys(value, ['schema', 'subjectId', 'clientEventId', 'createdGeneration', 'revision', 'state',
    'createdAt', 'expiresAt', 'attemptGeneration', 'request']) &&
    !exactKeys(value, ['schema', 'subjectId', 'clientEventId', 'createdGeneration', 'revision', 'state',
      'createdAt', 'expiresAt', 'request'])) throw new OperationJournalError('corrupt')
  if (value.schema !== 1 || !validSubject(value.subjectId) || !validEventId(value.clientEventId) ||
    !validGeneration(value.createdGeneration) || !Number.isSafeInteger(value.revision) || Number(value.revision) < 1 ||
    !['ready', 'in_flight', 'uncertain'].includes(String(value.state)) ||
    !Number.isSafeInteger(value.createdAt) || !Number.isSafeInteger(value.expiresAt) ||
    Number(value.expiresAt) <= Number(value.createdAt) ||
    (value.attemptGeneration !== undefined && !validGeneration(value.attemptGeneration)) ||
    (value.state === 'in_flight') !== (value.attemptGeneration !== undefined)) throw new OperationJournalError('corrupt')
  let request: JournalRequest
  try { request = normalizeRequest(value.request, value.clientEventId) }
  catch { throw new OperationJournalError('corrupt') }
  return {
    schema: 1, subjectId: value.subjectId, clientEventId: value.clientEventId,
    createdGeneration: value.createdGeneration, revision: Number(value.revision), state: value.state as JournalState,
    createdAt: Number(value.createdAt), expiresAt: Number(value.expiresAt),
    ...(value.attemptGeneration === undefined ? {} : { attemptGeneration: value.attemptGeneration as string }), request,
  }
}

function freezeOperation(value: StoredOperation): PendingOperation {
  const operation = { clientEventId: value.clientEventId, subjectId: value.subjectId,
    createdGeneration: value.createdGeneration, state: value.state, createdAt: value.createdAt,
    expiresAt: value.expiresAt } as unknown as PendingOperation
  Object.defineProperty(operation, 'request', { value: Object.freeze({ ...value.request }), enumerable: false,
    writable: false, configurable: false })
  return Object.freeze(operation)
}

function encode(value: StoredOperation): string { return JSON.stringify(value) }

export interface OperationJournalOptions {
  /** Local retention bound; it never authorizes replay of an attempted operation. */
  retentionMs: number
  now?: () => number
}

export class TrainingOperationJournal {
  readonly #storage: JournalStorage
  readonly #auth: JournalAuth
  readonly #boundLease: JournalCredential
  readonly #retentionMs: number
  readonly #now: () => number
  readonly #busy = new Set<string>()

  constructor(storage: JournalStorage, auth: JournalAuth, options: OperationJournalOptions) {
    if (!storage || typeof storage.read !== 'function' || typeof storage.createIfAbsent !== 'function' ||
      typeof storage.compareAndSwap !== 'function' || !auth || typeof auth.current !== 'function' ||
      typeof auth.isCurrent !== 'function' || !options || !Number.isSafeInteger(options.retentionMs) ||
      options.retentionMs < 60_000 || options.retentionMs > 30 * 24 * 60 * 60 * 1000) {
      throw new OperationJournalError('unavailable')
    }
    this.#storage = storage
    this.#auth = auth
    this.#retentionMs = options.retentionMs
    this.#now = options.now || Date.now
    let initial: JournalCredential | null
    let active = false
    try { initial = auth.current() } catch { initial = null }
    try {
      active = Boolean(initial && validSubject(initial.subjectId) && validGeneration(initial.generation) &&
        auth.isCurrent(initial.subjectId, initial.generation))
    } catch { active = false }
    if (!initial || !active) throw new OperationJournalError('unauthenticated')
    this.#boundLease = { subjectId: initial.subjectId, generation: initial.generation }
  }

  async stage(clientEventId: string, requestInput: JournalRequest): Promise<PendingOperation> {
    if (!validEventId(clientEventId)) throw new OperationJournalError('invalid_operation')
    const lease = this.#captureLease()
    const request = normalizeRequest(requestInput, clientEventId)
    const now = this.#timestamp()
    const key = storageKey(lease.subjectId, clientEventId)
    const candidate: StoredOperation = { schema: 1, subjectId: lease.subjectId, clientEventId,
      createdGeneration: lease.generation, revision: 1, state: 'ready', createdAt: now,
      expiresAt: now + this.#retentionMs, request }
    if (!Number.isSafeInteger(candidate.expiresAt)) throw new OperationJournalError('invalid_operation')
    let raw: unknown
    try { raw = await this.#storage.read(key) } catch { throw new OperationJournalError('unavailable') }
    this.#assertLease(lease)
    if (raw !== undefined && raw !== null) {
      const existing = decode(raw)
      this.#assertOwner(existing, lease.subjectId, clientEventId)
      if (!this.#sameRequest(existing.request, request)) throw new OperationJournalError('conflict')
      if (now >= existing.expiresAt) throw new OperationJournalError('expired')
      return freezeOperation(existing)
    }
    try {
      const created = await this.#storage.createIfAbsent(key, encode(candidate))
      this.#assertLease(lease)
      if (created) return freezeOperation(candidate)
      const raced = decode(await this.#storage.read(key))
      this.#assertLease(lease)
      this.#assertOwner(raced, lease.subjectId, clientEventId)
      if (!this.#sameRequest(raced.request, request)) throw new OperationJournalError('conflict')
      if (now >= raced.expiresAt) throw new OperationJournalError('expired')
      return freezeOperation(raced)
    } catch (error) {
      if (error instanceof OperationJournalError) throw error
      throw new OperationJournalError('unavailable')
    }
  }

  async readPending(clientEventId: string): Promise<PendingOperation | null> {
    if (!validEventId(clientEventId)) throw new OperationJournalError('invalid_operation')
    const lease = this.#captureLease()
    const key = storageKey(lease.subjectId, clientEventId)
    let raw: unknown
    try { raw = await this.#storage.read(key) } catch { throw new OperationJournalError('unavailable') }
    this.#assertLease(lease)
    if (raw === undefined || raw === null) return null
    const operation = decode(raw)
    this.#assertOwner(operation, lease.subjectId, clientEventId)
    return freezeOperation(operation)
  }

  /**
   * Persist an in-flight/uncertain marker before handing the exact request to a
   * caller. The caller must check permit.isCurrent immediately before sending.
   */
  async beginSend(clientEventId: string): Promise<DispatchPermit> {
    if (!validEventId(clientEventId)) throw new OperationJournalError('invalid_operation')
    const lease = this.#captureLease()
    const key = storageKey(lease.subjectId, clientEventId)
    if (this.#busy.has(key)) throw new OperationJournalError('busy')
    this.#busy.add(key)
    try {
      let raw: unknown
      try { raw = await this.#storage.read(key) } catch { throw new OperationJournalError('unavailable') }
      this.#assertLease(lease)
      if (raw === undefined || raw === null) throw new OperationJournalError('conflict')
      const existing = decode(raw)
      this.#assertOwner(existing, lease.subjectId, clientEventId)
      // Only a newly staged, never-dispatched operation can be issued without
      // server-bound evidence. A persisted in_flight/uncertain marker may mean
      // the server already committed, regardless of clock or local retention.
      if (existing.state !== 'ready' || existing.revision !== 1) {
        throw new OperationJournalError('reconciliation_required')
      }
      const now = this.#timestamp()
      if (now >= existing.expiresAt) throw new OperationJournalError('expired')
      const next: StoredOperation = { ...existing, revision: existing.revision + 1, state: 'in_flight',
        attemptGeneration: lease.generation }
      if (!Number.isSafeInteger(next.revision)) throw new OperationJournalError('conflict')
      const nextBytes = encode(next)
      let changed: boolean
      try { changed = await this.#storage.compareAndSwap(key, String(raw), nextBytes) }
      catch { throw new OperationJournalError('unavailable') }
      this.#assertLease(lease)
      if (!changed) throw new OperationJournalError('conflict')
      const proof: PermitProof = { key, storedBytes: nextBytes, subjectId: lease.subjectId,
        generation: lease.generation, eventId: clientEventId, active: true }
      const permit = Object.freeze({ operation: freezeOperation(next), isCurrent: () => proof.active && this.#leaseIsCurrent(lease) })
      permits.set(permit, proof)
      return permit
    } catch (error) {
      this.#busy.delete(key)
      if (error instanceof OperationJournalError) throw error
      throw new OperationJournalError('unavailable')
    }
  }

  /** Call after network/transport ambiguity. No response body is accepted here. */
  async markUncertain(permit: DispatchPermit): Promise<void> {
    const proof = this.#permitProof(permit)
    try {
      const lease = this.#captureLease()
      if (lease.subjectId !== proof.subjectId || lease.generation !== proof.generation) {
        throw new OperationJournalError('session_changed')
      }
      const operation = decode(proof.storedBytes)
      const next: StoredOperation = { ...operation, revision: operation.revision + 1,
        state: 'uncertain', attemptGeneration: undefined }
      const nextBytes = encode(next)
      let changed: boolean
      try { changed = await this.#storage.compareAndSwap(proof.key, proof.storedBytes, nextBytes) }
      catch { throw new OperationJournalError('unavailable') }
      this.#assertLease(lease)
      if (!changed) throw new OperationJournalError('conflict')
    } finally {
      proof.active = false
      this.#busy.delete(proof.key)
    }
  }

  /**
   * Inspect using a fresh authenticated RecordingApi read. No currently
   * available API result is cryptographically/bijectively bound to the stored
   * request after restart, so an `applied` verdict remains uncertain. A raw
   * status/body is never accepted. Clearing awaits a request-bound replay seam.
   */
  async reconcile(
    clientEventId: string,
    inspect: (operation: PendingOperation) => Promise<ReconciliationDecision>,
  ): Promise<ReconciliationResult> {
    if (!validEventId(clientEventId) || typeof inspect !== 'function') throw new OperationJournalError('invalid_operation')
    const lease = this.#captureLease()
    const key = storageKey(lease.subjectId, clientEventId)
    if (this.#busy.has(key)) throw new OperationJournalError('busy')
    let raw: unknown
    try { raw = await this.#storage.read(key) } catch { throw new OperationJournalError('unavailable') }
    this.#assertLease(lease)
    if (raw === undefined || raw === null) throw new OperationJournalError('conflict')
    const operation = decode(raw)
    this.#assertOwner(operation, lease.subjectId, clientEventId)
    let decision: ReconciliationDecision
    try { decision = await inspect(freezeOperation(operation)) }
    catch { this.#assertLease(lease); return { state: 'still_uncertain' } }
    this.#assertLease(lease)
    if (!decision || !['applied', 'unknown'].includes(decision.verdict)) {
      throw new OperationJournalError('invalid_operation')
    }
    if (decision.verdict === 'applied') {
      // A fresh detail can prove current state but not that this exact body/key
      // caused it. Keep the intent until a replay API returns bound evidence.
      return { state: 'still_uncertain' }
    }
    const next: StoredOperation = { ...operation, revision: operation.revision + 1,
      state: 'uncertain', attemptGeneration: undefined }
    let changed: boolean
    try { changed = await this.#storage.compareAndSwap(key, String(raw), encode(next)) }
    catch { throw new OperationJournalError('unavailable') }
    this.#assertLease(lease)
    if (!changed) throw new OperationJournalError('conflict')
    return { state: 'still_uncertain' }
  }

  #captureLease(): JournalCredential {
    let current: JournalCredential | null
    try { current = this.#auth.current() } catch { current = null }
    if (!current || !validSubject(current.subjectId) || !validGeneration(current.generation) ||
      current.subjectId !== this.#boundLease.subjectId || current.generation !== this.#boundLease.generation ||
      !this.#leaseIsCurrent(current)) throw new OperationJournalError('session_changed')
    return { subjectId: current.subjectId, generation: current.generation }
  }

  #leaseIsCurrent(lease: JournalCredential): boolean {
    try {
      const current = this.#auth.current()
      return Boolean(current && current.subjectId === lease.subjectId && current.generation === lease.generation &&
        lease.subjectId === this.#boundLease.subjectId && lease.generation === this.#boundLease.generation &&
        this.#auth.isCurrent(lease.subjectId, lease.generation))
    } catch { return false }
  }

  #assertLease(lease: JournalCredential): void {
    if (!this.#leaseIsCurrent(lease)) throw new OperationJournalError('session_changed')
  }

  #timestamp(): number {
    let value: number
    try { value = this.#now() } catch { throw new OperationJournalError('unavailable') }
    if (!Number.isSafeInteger(value) || value < 0) throw new OperationJournalError('unavailable')
    return value
  }

  #assertOwner(operation: StoredOperation, subjectId: string, eventId: string): void {
    if (operation.subjectId !== subjectId || operation.clientEventId !== eventId) throw new OperationJournalError('corrupt')
  }

  #sameRequest(left: JournalRequest, right: JournalRequest): boolean {
    return left.method === right.method && left.path === right.path && left.body === right.body &&
      left.idempotencyKey === right.idempotencyKey
  }

  #permitProof(permit: DispatchPermit): PermitProof {
    if (!permit || typeof permit !== 'object') throw new OperationJournalError('conflict')
    const proof = permits.get(permit as object)
    if (!proof?.active) throw new OperationJournalError('conflict')
    return proof
  }
}
