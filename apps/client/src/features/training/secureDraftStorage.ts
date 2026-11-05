import type { InitialDraftStorage } from './trainingEditorController'
import type { AuthLease, DraftVersionState } from './draftStorage'

/** Synchronous native operations on one protected draft slot. */
export interface NativeDraftPort {
  discoverTrainingDrafts(subjectId: string, cursor: string): unknown
  readVersionedTrainingDraft(key: string): unknown
  compareAndSetVersionedTrainingDraft(key: string, expectedRevision: string, expectedFound: boolean,
    expectedValue: string, value: string): unknown
}

type NativeResult = { ok: boolean; errorCode: string; found: boolean; value: string }
type VersionedNativeResult = NativeResult & { revision: string }
const MAX_BYTES = 64 * 1024
const MAX_REVISION = '9223372036854775807'

function byteLength(value: string): number {
  let count = 0
  for (let i = 0; i < value.length; i++) {
    const unit = value.charCodeAt(i)
    if (unit <= 0x7f) count++
    else if (unit <= 0x7ff) count += 2
    else if (unit >= 0xd800 && unit <= 0xdbff) {
      const low = value.charCodeAt(++i)
      if (!(low >= 0xdc00 && low <= 0xdfff)) throw new Error('本机草稿格式无效')
      count += 4
    } else if (unit >= 0xdc00 && unit <= 0xdfff) throw new Error('本机草稿格式无效')
    else count += 3
    if (count > MAX_BYTES) throw new Error('本机草稿超过容量限制')
  }
  return count
}

// JSON.parse accepts escaped lone surrogates, which cannot round-trip safely
// through native Unicode strings. Validate keys as well as values.
function assertUnicodeStrings(value: unknown): void {
  const pending: unknown[] = [value]
  while (pending.length) {
    const current = pending.pop()
    if (typeof current === 'string') byteLength(current)
    else if (Array.isArray(current)) pending.push(...current)
    else if (current && typeof current === 'object') {
      for (const [key, item] of Object.entries(current)) pending.push(key, item)
    }
  }
}

function result(value: unknown, versioned = false, isRead = true): NativeResult | VersionedNativeResult {
  if (!value || typeof value !== 'object' || Array.isArray(value)) throw new Error('本机草稿暂不可用')
  const candidate = value as Record<string, unknown>
  const fields = versioned ? 'errorCode,found,ok,revision,value' : 'errorCode,found,ok,value'
  if (Object.keys(candidate).sort().join(',') !== fields ||
    typeof candidate.ok !== 'boolean' || typeof candidate.errorCode !== 'string' ||
    typeof candidate.found !== 'boolean' || typeof candidate.value !== 'string' ||
    (versioned && typeof candidate.revision !== 'string') ||
    (candidate.ok && candidate.errorCode !== '') || (!candidate.ok && (!candidate.errorCode || candidate.found || candidate.value))) {
    throw new Error('本机草稿暂不可用')
  }
  if (versioned && candidate.ok && (isRead
    ? !validState(candidate as unknown as VersionedNativeResult)
    : candidate.found || candidate.value !== '' || !validRevision(candidate.revision) || candidate.revision === '0')) {
    throw new Error('本机草稿暂不可用')
  }
  if (versioned && !candidate.ok && candidate.revision !== '') throw new Error('本机草稿暂不可用')
  return candidate as NativeResult | VersionedNativeResult
}

function validRevision(value: unknown): value is string {
  return typeof value === 'string' && /^(0|[1-9][0-9]{0,18})$/.test(value) &&
    (value.length < 19 || value <= MAX_REVISION)
}

function validState(value: VersionedNativeResult): boolean {
  return value.ok && value.errorCode === '' && validRevision(value.revision) &&
    (value.found ? value.value.length > 0 : value.value === '' && value.revision === '0')
}

function incrementRevision(value: string): string {
  if (!validRevision(value) || value === MAX_REVISION) throw new Error('本机草稿版本不可用')
  const digits = Array.from(value, char => char.charCodeAt(0))
  for (let index = digits.length - 1; index >= 0; index--) {
    if (digits[index] < 57) { digits[index]++; return String.fromCharCode(...digits) }
    digits[index] = 48
  }
  const next = `1${String.fromCharCode(...digits)}`
  if (!validRevision(next)) throw new Error('本机草稿版本不可用')
  return next
}

/**
 * One adapter is bound to a verified subject/session. Every mutation uses the
 * native revision and exact predecessor bytes observed by the versioned editor.
 */
export class SecureDraftStorage implements InitialDraftStorage {
  readonly versioned = true
  #port: NativeDraftPort
  #subjectId: string
  #auth: AuthLease
  #generation: string

  constructor(port: NativeDraftPort, subjectId: string, auth: AuthLease) {
    if (!port || typeof port.discoverTrainingDrafts !== 'function' || typeof port.readVersionedTrainingDraft !== 'function' ||
      typeof port.compareAndSetVersionedTrainingDraft !== 'function' || !/^[1-9]\d{0,19}$/.test(subjectId) ||
      !auth || typeof auth.isCurrent !== 'function' || typeof auth.generation !== 'string' || !auth.generation ||
      !auth.isCurrent(subjectId, auth.generation)) throw new Error('本机草稿暂不可用')
    this.#port = port
    this.#subjectId = subjectId
    this.#auth = auth
    this.#generation = auth.generation
  }

  async discover(cursor = ''): Promise<{ eventIds: string[]; nextCursor: string | null; hasUnclassifiedItems: boolean }> {
    if (cursor) this.#key(`nerae.workout.draft.v1.${this.#subjectId}.${cursor}`)
    this.#assertCurrent()
    let discovered: NativeResult
    try { discovered = result(this.#port.discoverTrainingDrafts(this.#subjectId, cursor)) } catch { throw new Error('查找本机草稿失败') }
    this.#assertCurrent()
    if (!discovered.ok || !discovered.found || !discovered.value || discovered.value.length > 128 * 1024) throw new Error('查找本机草稿失败')
    let parsed: unknown
    try { parsed = JSON.parse(discovered.value) } catch { throw new Error('查找本机草稿失败') }
    if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) throw new Error('查找本机草稿失败')
    const list = parsed as Record<string, unknown>
    if (Object.keys(list).sort().join(',') !== 'eventIds,hasUnclassifiedItems,nextCursor' || !Array.isArray(list.eventIds) ||
      list.eventIds.length > 100 || typeof list.hasUnclassifiedItems !== 'boolean' ||
      !(list.nextCursor === null || typeof list.nextCursor === 'string')) throw new Error('查找本机草稿失败')
    const eventIds: string[] = []
    for (const event of list.eventIds) {
      if (typeof event !== 'string') throw new Error('查找本机草稿失败')
      this.#key(`nerae.workout.draft.v1.${this.#subjectId}.${event}`)
      if (eventIds.includes(event)) throw new Error('查找本机草稿失败')
      eventIds.push(event)
    }
    if (list.nextCursor !== null && (eventIds.length !== 100 || list.nextCursor !== eventIds[eventIds.length - 1])) {
      throw new Error('查找本机草稿失败')
    }
    return { eventIds, nextCursor: list.nextCursor, hasUnclassifiedItems: list.hasUnclassifiedItems }
  }

  async read(key: string): Promise<unknown> {
    return (await this.readVersioned(key)).value
  }

  async readVersioned(key: string): Promise<DraftVersionState> {
    const event = this.#key(key)
    this.#assertCurrent()
    let read: VersionedNativeResult
    try { read = result(this.#port.readVersionedTrainingDraft(key), true) as VersionedNativeResult }
    catch { throw new Error('读取本机草稿失败') }
    this.#assertCurrent()
    if (!read.ok || !read.found || !read.value) throw new Error('读取本机草稿失败')
    this.#payload(read.value, event)
    return { found: read.found, value: read.value, revision: read.revision }
  }

  async createIfAbsent(key: string, value: string): Promise<'created' | 'exists'> {
    const event = this.#key(key)
    this.#payload(value, event)
    this.#assertCurrent()
    let created: VersionedNativeResult
    try {
      created = result(this.#port.compareAndSetVersionedTrainingDraft(key, '0', false, '', value), true, false) as VersionedNativeResult
    } catch { throw new Error('创建本机草稿结果未确认') }
    if (!created.ok) {
      if (created.errorCode === 'conflict') return 'exists'
      throw new Error('创建本机草稿结果未确认')
    }
    if (created.found || created.value || created.revision !== '1') throw new Error('创建本机草稿结果未确认')
    const readback = await this.readVersioned(key)
    if (readback.revision !== created.revision || readback.value !== value) throw new Error('创建本机草稿结果未确认')
    this.#assertCurrent()
    return 'created'
  }

  prepareVersionedWrite(key: string, value: string): (expected: DraftVersionState) => Promise<DraftVersionState> {
    const event = this.#key(key)
    this.#payload(value, event)
    this.#assertCurrent()
    let consumed = false
    return async (expected: DraftVersionState): Promise<DraftVersionState> => {
      if (consumed) throw new Error('本机草稿写入已使用')
      consumed = true
      if (!expected?.found || !expected.value || !validRevision(expected.revision)) throw new Error('本机草稿版本不可用')
      this.#payload(expected.value, event)
      const nextRevision = incrementRevision(expected.revision)
      let written: VersionedNativeResult
      try {
        written = result(this.#port.compareAndSetVersionedTrainingDraft(key, expected.revision, true,
          expected.value, value), true, false) as VersionedNativeResult
      } catch { throw new Error('保存本机草稿结果未确认') }
      this.#assertCurrent()
      if (!written.ok || written.found || written.value || written.revision !== nextRevision) {
        throw new Error('保存本机草稿结果未确认')
      }
      const readback = await this.readVersioned(key)
      if (!this.#auth.isCurrent(this.#subjectId, this.#generation)) throw new Error('账号已变化，请重新登录后恢复草稿')
      if (readback.revision !== nextRevision || readback.value !== value) throw new Error('保存本机草稿结果未确认')
      return readback
    }
  }

  /** Compatibility write is deliberately unavailable: callers must supply a CAS baseline. */
  prepareWrite(_key: string, _value: string): () => Promise<void> {
    throw new Error('本机草稿需要版本化写入')
  }

  async write(key: string, value: string): Promise<void> {
    void key
    void value
    throw new Error('本机草稿需要版本化写入')
  }

  #assertCurrent(): void {
    if (!this.#auth.isCurrent(this.#subjectId, this.#generation)) throw new Error('账号已变化，请重新登录后恢复草稿')
  }

  #key(key: string): string {
    const prefix = `nerae.workout.draft.v1.${this.#subjectId}.`
    if (typeof key !== 'string' || !key.startsWith(prefix)) throw new Error('本机草稿不属于当前账号')
    const event = key.slice(prefix.length)
    if ([...event].length < 8 || [...event].length > 64 || !event.trim()) throw new Error('本机草稿标识无效')
    byteLength(event)
    return event
  }

  #payload(value: string, event: string): void {
    if (typeof value !== 'string' || !value || byteLength(value) > MAX_BYTES) throw new Error('本机草稿格式无效')
    let parsed: unknown
    try { parsed = JSON.parse(value) } catch { throw new Error('本机草稿格式无效') }
    assertUnicodeStrings(parsed)
    if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed) || JSON.stringify(parsed) !== value) throw new Error('本机草稿格式无效')
    const envelope = parsed as Record<string, unknown>
    if (Object.keys(envelope).sort().join(',') !== 'draft,schema,subjectId' || envelope.schema !== 1 || envelope.subjectId !== this.#subjectId ||
      !envelope.draft || typeof envelope.draft !== 'object' || Array.isArray(envelope.draft) ||
      (envelope.draft as Record<string, unknown>).clientEventId !== event) throw new Error('本机草稿不属于当前记录')
  }
}
