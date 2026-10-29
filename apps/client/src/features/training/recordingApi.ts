import type { SetKind, SetWrite } from './recording'

/** Credentials come from the real identity module and are never stored here. */
export interface RecordingCredential {
  subjectId: string
  generation: string
  accessToken: string
}

export interface RecordingAuth {
  current(): RecordingCredential | null
  isCurrent(subjectId: string, generation: string): boolean
}

export interface HttpRequest {
  url: string
  method: 'GET' | 'POST'
  header: Record<string, string>
  data?: string
}

export interface HttpResponse {
  statusCode: number
  data: unknown
}

export type RequestPort = (request: HttpRequest) => Promise<HttpResponse>

export type RecordingApiErrorCode =
  | 'unauthenticated' | 'session_changed' | 'forbidden' | 'not_found'
  | 'version_conflict' | 'conflict' | 'invalid_request' | 'rate_limited'
  | 'unavailable' | 'network' | 'invalid_response'

export class RecordingApiError extends Error {
  readonly code: RecordingApiErrorCode
  readonly status?: number
  constructor(code: RecordingApiErrorCode, status?: number) {
    super(code)
    this.name = 'RecordingApiError'
    this.code = code
    this.status = status
  }
}

export interface WorkoutSessionDetail {
  id: string
  date: string
  timeZone: string
  status: 'in_progress' | 'completed' | 'skipped'
  source: 'free' | 'plan'
  version: number
  createdAt: string
  updatedAt: string
  recordedAt: string | null
  sets: ServerSet[]
}

export interface ServerSet extends Omit<SetWrite, 'rpe' | 'completed'> {
  rpe?: number | null
  completed?: true
  resolvedExerciseName: string
}

export type SetPayload = Omit<SetWrite, 'rpe'> & { rpe?: number | null }

export interface FreeSessionInput {
  date: string
  timeZone: string
  clientEventId: string
}

export interface FinishInput {
  expectedVersion: number
  date?: string
  durationMin?: number | null
  rpe?: number | null
  note?: string | null
}

function object(value: unknown): value is Record<string, unknown> {
  return value !== null && typeof value === 'object' && !Array.isArray(value)
}

function validId(value: unknown): value is string {
  return typeof value === 'string' && /^[1-9]\d{0,19}$/.test(value)
}

function validKey(value: unknown, max = 64): value is string {
  return typeof value === 'string' && Array.from(value).length >= 8 && Array.from(value).length <= max &&
    !/[\r\n]/.test(value) && Boolean(value.trim())
}

function validVersion(value: unknown): value is number {
  return Number.isSafeInteger(value) && Number(value) >= 1
}

function validDate(value: unknown): value is string {
  if (typeof value !== 'string' || !/^\d{4}-\d{2}-\d{2}$/.test(value)) return false
  const date = new Date(`${value}T00:00:00.000Z`)
  return !Number.isNaN(date.getTime()) && date.toISOString().slice(0, 10) === value
}

function validDateTime(value: unknown): value is string {
  if (typeof value !== 'string') return false
  const parts = /^(\d{4}-\d{2}-\d{2})T(\d{2}):(\d{2}):(\d{2})(?:\.\d+)?(?:Z|[+-]\d{2}:\d{2})$/.exec(value)
  return Boolean(parts && validDate(parts[1]) && Number(parts[2]) < 24 && Number(parts[3]) < 60 &&
    Number(parts[4]) < 60 && !Number.isNaN(Date.parse(value)))
}

function validTimeZone(value: unknown): value is string {
  if (typeof value !== 'string' || !value.trim() || value.length > 64) return false
  try { new Intl.DateTimeFormat('en', { timeZone: value }); return true }
  catch { return false }
}

function decimal(value: unknown, min: number, max: number, places: number, exclusiveMin = false): boolean {
  if (typeof value !== 'number' || !Number.isFinite(value) || value > max ||
    (exclusiveMin ? value <= min : value < min)) return false
  const scaled = value * 10 ** places
  return Math.abs(scaled - Math.round(scaled)) < 1e-8
}

function validMeasurements(set: Record<string, unknown>, kind: SetKind): boolean {
  if (set.rpe !== undefined && set.rpe !== null && !decimal(set.rpe, 1, 10, 1)) return false
  if (kind === 'weight' || kind === 'perSide') {
    return decimal(set.weightKg, 0, 300, 2, true) && Number.isInteger(set.reps) &&
      Number(set.reps) >= 1 && Number(set.reps) <= 100 && set.seconds === undefined
  }
  if (kind === 'duration' || kind === 'durationPerSide') {
    return set.weightKg === undefined && set.reps === undefined && Number.isInteger(set.seconds) &&
      Number(set.seconds) >= 5 && Number(set.seconds) <= 600
  }
  return set.weightKg === undefined && set.seconds === undefined && Number.isInteger(set.reps) &&
    Number(set.reps) >= 1 && Number(set.reps) <= 100
}

function setPayload(raw: unknown): SetPayload {
  if (!object(raw) || !validKey(raw.recordKey) || !validKey(raw.exerciseEntryKey) ||
    !Number.isSafeInteger(raw.setIndex) || Number(raw.setIndex) < 1 || raw.completed !== true ||
    !['weight', 'perSide', 'bodyweight', 'bodyweightPerSide', 'duration', 'durationPerSide', 'breath'].includes(String(raw.kind)) ||
    !validMeasurements(raw, raw.kind as SetKind)) throw new RecordingApiError('invalid_request')
  const hasId = validId(raw.exerciseId)
  const hasName = typeof raw.exerciseName === 'string' && Boolean(raw.exerciseName.trim()) && Array.from(raw.exerciseName).length <= 60
  if (hasId === hasName || (raw.exerciseId !== undefined && !hasId) || (raw.exerciseName !== undefined && !hasName)) {
    throw new RecordingApiError('invalid_request')
  }
  return {
    recordKey: raw.recordKey,
    exerciseEntryKey: raw.exerciseEntryKey,
    setIndex: Number(raw.setIndex),
    kind: raw.kind as SetKind,
    completed: true,
    ...(hasId ? { exerciseId: raw.exerciseId as string } : { exerciseName: raw.exerciseName as string }),
    ...(raw.weightKg === undefined ? {} : { weightKg: raw.weightKg as number }),
    ...(raw.reps === undefined ? {} : { reps: raw.reps as number }),
    ...(raw.seconds === undefined ? {} : { seconds: raw.seconds as number }),
    ...(raw.rpe === undefined ? {} : { rpe: raw.rpe as number | null }),
  }
}

/** Fail closed on a malformed response before its version or IDs reach a draft. */
function sessionDetail(raw: unknown): WorkoutSessionDetail {
  if (!object(raw) || !validId(raw.id) || !validDate(raw.date) || !validTimeZone(raw.timeZone) ||
    !['in_progress', 'completed', 'skipped'].includes(String(raw.status)) ||
    !['free', 'plan'].includes(String(raw.source)) || !validVersion(raw.version) ||
    !validDateTime(raw.createdAt) || !validDateTime(raw.updatedAt) ||
    !(raw.recordedAt === null || validDateTime(raw.recordedAt)) || !Array.isArray(raw.sets)) {
    throw new RecordingApiError('invalid_response')
  }
  if ((raw.status === 'completed' && (raw.sets.length === 0 || raw.recordedAt === null)) ||
    (raw.status === 'skipped' && raw.sets.length !== 0)) throw new RecordingApiError('invalid_response')
  const keys = new Set<string>()
  for (const set of raw.sets) {
    if (!object(set) || !validKey(set.recordKey) || !validKey(set.exerciseEntryKey) ||
      !Number.isSafeInteger(set.setIndex) || Number(set.setIndex) < 1 ||
      typeof set.resolvedExerciseName !== 'string' || !set.resolvedExerciseName.trim() ||
      Array.from(set.resolvedExerciseName).length > 128 ||
      !['weight', 'perSide', 'bodyweight', 'bodyweightPerSide', 'duration', 'durationPerSide', 'breath'].includes(String(set.kind)) ||
      (set.completed !== undefined && set.completed !== true) || keys.has(set.recordKey) ||
      !validMeasurements(set, set.kind as SetKind)) {
      throw new RecordingApiError('invalid_response')
    }
    const hasId = validId(set.exerciseId)
    const hasName = typeof set.exerciseName === 'string' && Boolean(set.exerciseName.trim()) && Array.from(set.exerciseName).length <= 60
    if (hasId === hasName || (set.exerciseId !== undefined && !hasId) || (set.exerciseName !== undefined && !hasName)) {
      throw new RecordingApiError('invalid_response')
    }
    keys.add(set.recordKey)
  }
  return raw as unknown as WorkoutSessionDetail
}

function responseError(status: number): RecordingApiError {
  if (status === 401) return new RecordingApiError('unauthenticated', status)
  if (status === 403) return new RecordingApiError('forbidden', status)
  if (status === 404) return new RecordingApiError('not_found', status)
  if (status === 409) return new RecordingApiError('conflict', status)
  if (status === 412) return new RecordingApiError('version_conflict', status)
  if (status === 429) return new RecordingApiError('rate_limited', status)
  if (status === 400 || status === 422) return new RecordingApiError('invalid_request', status)
  return new RecordingApiError('unavailable', status)
}

function validIdempotencyKey(value: unknown): value is string {
  return typeof value === 'string' && /^[A-Za-z0-9._~-]{8,128}$/.test(value)
}

/** Only HTTPS is accepted; the app must provide a real, configured API base. */
function apiBase(value: string): string {
  let parsed: URL
  try { parsed = new URL(value) } catch { throw new Error('服务地址无效') }
  if (parsed.protocol !== 'https:' || !parsed.hostname || parsed.username || parsed.password || parsed.search || parsed.hash) {
    throw new Error('服务地址必须为不含凭据的 HTTPS 地址')
  }
  return parsed.toString().replace(/\/$/, '')
}

/** Thin real-HTTP adapter. It does not persist a queue or claim successful sync. */
export class RecordingApi {
  private readonly base: string
  private readonly auth: RecordingAuth
  private readonly request: RequestPort
  constructor(baseUrl: string, auth: RecordingAuth, request: RequestPort) {
    this.base = apiBase(baseUrl)
    this.auth = auth
    this.request = request
  }

  private async call(method: 'GET' | 'POST', path: string, expectedStatus: number, expectedId?: string,
    data?: unknown, idempotencyKey?: string): Promise<WorkoutSessionDetail> {
    const credential = this.auth.current()
    if (!credential) throw new RecordingApiError('unauthenticated')
    // Capture scalar values: an auth store may mutate its current object while
    // a network request is in flight.
    const { subjectId, generation, accessToken } = credential
    if (!validId(subjectId) || typeof generation !== 'string' || !generation ||
      typeof accessToken !== 'string' || !accessToken ||
      /[\r\n\s]/.test(accessToken) || !this.auth.isCurrent(subjectId, generation)) {
      throw new RecordingApiError('unauthenticated')
    }
    if (method === 'POST' && !validIdempotencyKey(idempotencyKey)) throw new RecordingApiError('invalid_request')
    const header: Record<string, string> = { Authorization: `Bearer ${accessToken}`, Accept: 'application/json' }
    if (method === 'POST') {
      header['Content-Type'] = 'application/json'
      header['Idempotency-Key'] = idempotencyKey as string
    }
    let encoded: string | undefined
    if (data !== undefined) {
      try { encoded = JSON.stringify(data) } catch { throw new RecordingApiError('invalid_request') }
      if (typeof encoded !== 'string') throw new RecordingApiError('invalid_request')
    }
    let response: HttpResponse
    try { response = await this.request({ url: `${this.base}${path}`, method, header, ...(encoded === undefined ? {} : { data: encoded }) }) }
    catch {
      if (!this.auth.isCurrent(subjectId, generation)) throw new RecordingApiError('session_changed')
      throw new RecordingApiError('network')
    }
    if (!this.auth.isCurrent(subjectId, generation)) throw new RecordingApiError('session_changed')
    if (!Number.isInteger(response.statusCode)) throw new RecordingApiError('invalid_response')
    if (response.statusCode < 200 || response.statusCode >= 300) throw responseError(response.statusCode)
    if (response.statusCode !== expectedStatus) throw new RecordingApiError('invalid_response')
    const detail = sessionDetail(response.data)
    if (expectedId !== undefined && detail.id !== expectedId) throw new RecordingApiError('invalid_response')
    return detail
  }

  createFreeSession(input: FreeSessionInput, idempotencyKey: string): Promise<WorkoutSessionDetail> {
    if (!validKey(input.clientEventId) || !validDate(input.date) || !validTimeZone(input.timeZone)) {
      throw new RecordingApiError('invalid_request')
    }
    return this.call('POST', '/me/sessions', 201, undefined, {
      source: 'free', date: input.date, timeZone: input.timeZone, clientEventId: input.clientEventId,
    }, idempotencyKey)
  }

  readSession(id: string): Promise<WorkoutSessionDetail> {
    if (!validId(id)) throw new RecordingApiError('invalid_request')
    return this.call('GET', `/me/sessions/${id}`, 200, id)
  }

  saveSets(id: string, expectedVersion: number, sets: SetPayload[], idempotencyKey: string): Promise<WorkoutSessionDetail> {
    if (!validId(id) || !validVersion(expectedVersion) || !Array.isArray(sets) || sets.length < 1 || sets.length > 200) {
      throw new RecordingApiError('invalid_request')
    }
    const cleanSets = sets.map(setPayload)
    if (new Set(cleanSets.map(set => set.recordKey)).size !== cleanSets.length) throw new RecordingApiError('invalid_request')
    return this.call('POST', `/me/sessions/${id}/sets`, 200, id, { expectedVersion, sets: cleanSets }, idempotencyKey)
  }

  finishSession(id: string, input: FinishInput, idempotencyKey: string): Promise<WorkoutSessionDetail> {
    if (!validId(id) || !validVersion(input.expectedVersion) ||
      (input.date !== undefined && !validDate(input.date)) ||
      (input.durationMin !== undefined && input.durationMin !== null &&
        (!Number.isSafeInteger(input.durationMin) || input.durationMin < 0)) ||
      (input.rpe !== undefined && input.rpe !== null && !decimal(input.rpe, 1, 10, 1)) ||
      (input.note !== undefined && input.note !== null &&
        (typeof input.note !== 'string' || Array.from(input.note).length > 2000))) {
      throw new RecordingApiError('invalid_request')
    }
    const body: FinishInput = { expectedVersion: input.expectedVersion }
    for (const field of ['date', 'durationMin', 'rpe', 'note'] as const) {
      if (input[field] !== undefined) Object.assign(body, { [field]: input[field] })
    }
    return this.call('POST', `/me/sessions/${id}/finish`, 200, id, body, idempotencyKey)
  }
}

/** Adapter to the actual uni-app network API on each target platform. */
export function createUniRequestPort(): RequestPort {
  return request => new Promise((resolve, reject) => {
    uni.request({
      ...request,
      success: result => resolve({ statusCode: result.statusCode, data: result.data }),
      fail: () => reject(new Error('network unavailable')),
    })
  })
}
