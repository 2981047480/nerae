import type { InitialDraftStorage } from './trainingEditorController'
import type { AuthLease } from './draftStorage'

/** Synchronous native operations on one protected draft slot. */
export interface NativeDraftPort {
  readTrainingDraft(key: string): unknown
  createTrainingDraft(key: string, value: string): unknown
  writeTrainingDraft(key: string, value: string): unknown
}

type NativeResult = { ok: boolean; errorCode: string; found: boolean; value: string }
const MAX_BYTES = 64 * 1024

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

function result(value: unknown): NativeResult {
  if (!value || typeof value !== 'object' || Array.isArray(value)) throw new Error('本机草稿暂不可用')
  const candidate = value as Record<string, unknown>
  if (Object.keys(candidate).sort().join(',') !== 'errorCode,found,ok,value' ||
    typeof candidate.ok !== 'boolean' || typeof candidate.errorCode !== 'string' ||
    typeof candidate.found !== 'boolean' || typeof candidate.value !== 'string' ||
    (candidate.ok && candidate.errorCode !== '') || (!candidate.ok && (!candidate.errorCode || candidate.found || candidate.value))) {
    throw new Error('本机草稿暂不可用')
  }
  return candidate as NativeResult
}

/**
 * One adapter is bound to a verified subject/session. Read and creation require
 * the active lease; a one-use prepared write may finish after logout because
 * DraftController already accepted and ordered that exact snapshot.
 */
export class SecureDraftStorage implements InitialDraftStorage {
  #port: NativeDraftPort
  #subjectId: string
  #auth: AuthLease
  #generation: string

  constructor(port: NativeDraftPort, subjectId: string, auth: AuthLease) {
    if (!port || typeof port.readTrainingDraft !== 'function' || typeof port.createTrainingDraft !== 'function' ||
      typeof port.writeTrainingDraft !== 'function' || !/^[1-9]\d{0,19}$/.test(subjectId) ||
      !auth || typeof auth.isCurrent !== 'function' || typeof auth.generation !== 'string' || !auth.generation ||
      !auth.isCurrent(subjectId, auth.generation)) throw new Error('本机草稿暂不可用')
    this.#port = port
    this.#subjectId = subjectId
    this.#auth = auth
    this.#generation = auth.generation
  }

  async read(key: string): Promise<unknown> {
    const event = this.#key(key)
    this.#assertCurrent()
    let read: NativeResult
    try { read = result(this.#port.readTrainingDraft(key)) } catch { throw new Error('读取本机草稿失败') }
    this.#assertCurrent()
    if (!read.ok || !read.found || !read.value) throw new Error('读取本机草稿失败')
    this.#payload(read.value, event)
    return read.value
  }

  async createIfAbsent(key: string, value: string): Promise<'created' | 'exists'> {
    const event = this.#key(key)
    this.#payload(value, event)
    this.#assertCurrent()
    let created: NativeResult
    try { created = result(this.#port.createTrainingDraft(key, value)) } catch { throw new Error('创建本机草稿结果未确认') }
    this.#assertCurrent()
    if (created.ok && !created.found && !created.value) return 'created'
    if (!created.ok && created.errorCode === 'conflict') return 'exists'
    throw new Error('创建本机草稿结果未确认')
  }

  prepareWrite(key: string, value: string): () => Promise<void> {
    const event = this.#key(key)
    this.#payload(value, event)
    this.#assertCurrent()
    let consumed = false
    return async () => {
      if (consumed) throw new Error('本机草稿写入已使用')
      consumed = true
      let written: NativeResult
      try { written = result(this.#port.writeTrainingDraft(key, value)) } catch { throw new Error('保存本机草稿结果未确认') }
      if (!written.ok || written.found || written.value) throw new Error('保存本机草稿结果未确认')
    }
  }

  async write(key: string, value: string): Promise<void> {
    return this.prepareWrite(key, value)()
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
