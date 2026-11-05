import { restoreDraft, type RecordingDraft } from './recording'
import { todayInTimezone, type AuthLease, type DraftStorage, type DraftVersionState, type LocalDraftStatus } from './draftStorage'

type VersionedStorage = DraftStorage & Required<Pick<DraftStorage, 'readVersioned' | 'prepareVersionedWrite'>>
type Attempt = { before: DraftVersionState; value: string }
type Slot = {
  owner: symbol | null
  latest: string
  base: DraftVersionState
  uncertain: Attempt | null
  sequence: number
  status: LocalDraftStatus
  running: Promise<boolean> | null
}

// Only unresolved local edits are kept after an editor closes. This is
// process memory, not crash-safe persistence.
const slots = new Map<string, Slot>()
const reservations = new Set<string>()
const MAX_REVISION = '9223372036854775807'

export function supportsVersionedDraft(storage: DraftStorage): storage is VersionedStorage {
  return storage.versioned === true && typeof storage.readVersioned === 'function' && typeof storage.prepareVersionedWrite === 'function'
}

function nextRevision(value: string): string | null {
  if (!/^(0|[1-9]\d{0,18})$/.test(value) || (value.length === 19 && value > MAX_REVISION) || value === MAX_REVISION) return null
  const digits = Array.from(value)
  for (let i = digits.length - 1; i >= 0; i--) {
    if (digits[i] !== '9') { digits[i] = String.fromCharCode(digits[i].charCodeAt(0) + 1); return digits.join('') }
    digits[i] = '0'
  }
  return '1' + digits.join('')
}

function sameState(a: DraftVersionState, b: DraftVersionState): boolean {
  return a.found === b.found && a.revision === b.revision && a.value === b.value
}

function draftKey(subjectId: string, eventId: string): string {
  if (!/^[1-9]\d{0,19}$/.test(subjectId) || Array.from(eventId).length < 8 ||
    Array.from(eventId).length > 64 || !eventId.trim()) throw new Error('记录标识无效')
  return 'nerae.workout.draft.v1.' + subjectId + '.' + eventId
}

function envelope(subjectId: string, draft: RecordingDraft): string {
  return JSON.stringify({ schema: 1, subjectId, draft })
}

function decode(raw: string, subjectId: string, eventId: string, now: () => Date): RecordingDraft {
  let parsed: unknown
  try { parsed = JSON.parse(raw) } catch { throw new Error('本机草稿无法读取') }
  if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) throw new Error('本机草稿格式无效')
  const value = parsed as Record<string, unknown>
  if (value.schema !== 1 || value.subjectId !== subjectId || !value.draft || typeof value.draft !== 'object' ||
    Array.isArray(value.draft)) throw new Error('本机草稿不属于当前账号')
  const draft = value.draft as Record<string, unknown>
  if (draft.clientEventId !== eventId || typeof draft.timeZone !== 'string') throw new Error('本机草稿不属于当前记录')
  return restoreDraft(draft, todayInTimezone(draft.timeZone, now()))
}

/** Single-owner editor backed by one versioned native draft slot. */
export class VersionedEditorDraft {
  readonly key: string
  private readonly subjectId: string
  private readonly eventId: string
  private readonly storage: VersionedStorage
  private readonly auth: AuthLease
  private readonly generation: string
  private readonly now: () => Date
  private readonly slot: Slot
  private readonly owner: symbol

  private constructor(subjectId: string, eventId: string, storage: VersionedStorage, now: () => Date,
    auth: AuthLease, slot: Slot, owner: symbol) {
    this.subjectId = subjectId
    this.eventId = eventId
    this.key = draftKey(subjectId, eventId)
    this.storage = storage
    this.auth = auth
    this.generation = auth.generation
    this.now = now
    this.slot = slot
    this.owner = owner
    this.assertCurrent()
  }

  static async load(subjectId: string, eventId: string, storage: VersionedStorage,
    now: () => Date, auth: AuthLease): Promise<VersionedEditorDraft> {
    return this.open(subjectId, eventId, storage, now, auth)
  }

  static async created(subjectId: string, draft: RecordingDraft, storage: VersionedStorage,
    now: () => Date, auth: AuthLease): Promise<VersionedEditorDraft> {
    return this.open(subjectId, draft.clientEventId, storage, now, auth, envelope(subjectId, draft))
  }

  private static async open(subjectId: string, eventId: string, storage: VersionedStorage,
    now: () => Date, auth: AuthLease, createdValue?: string): Promise<VersionedEditorDraft> {
    const key = draftKey(subjectId, eventId)
    const generation = auth.generation
    if (!generation || !auth.isCurrent(subjectId, generation) || reservations.has(key)) throw new Error('本机草稿暂不可用')
    reservations.add(key)
    const owner = Symbol('draft-owner')
    try {
      let slot = slots.get(key)
      if (slot) {
        if (createdValue !== undefined || slot.owner || slot.running) throw new Error('本机草稿正在使用')
        decode(slot.latest, subjectId, eventId, now)
        slot.owner = owner
      } else {
        const observed = await storage.readVersioned(key)
        if (!auth.isCurrent(subjectId, generation) || !observed.found || !observed.value ||
          (createdValue !== undefined && (observed.revision !== '1' || observed.value !== createdValue))) {
          throw new Error('本机草稿保存结果未确认')
        }
        decode(observed.value, subjectId, eventId, now)
        slot = { owner, latest: observed.value, base: { ...observed }, uncertain: null,
          sequence: 0, status: 'saved_locally', running: null }
        slots.set(key, slot)
      }
      return new VersionedEditorDraft(subjectId, eventId, storage, now, auth, slot, owner)
    } catch (error) {
      const slot = slots.get(key)
      if (slot?.owner === owner) slot.owner = null
      throw error
    } finally { reservations.delete(key) }
  }

  get draft(): RecordingDraft {
    this.assertCurrent()
    return decode(this.slot.latest, this.subjectId, this.eventId, this.now)
  }

  get status(): LocalDraftStatus { return this.slot.status }

  async update(transform: (draft: RecordingDraft) => RecordingDraft): Promise<boolean> {
    const current = this.draft
    const next = transform(current)
    if (next.clientEventId !== current.clientEventId || next.timeZone !== current.timeZone) {
      throw new Error('不能改变记录身份或已冻结的时区')
    }
    const checked = restoreDraft(next, todayInTimezone(next.timeZone, this.now()))
    this.slot.latest = envelope(this.subjectId, checked)
    this.slot.sequence++
    try { this.storage.prepareVersionedWrite(this.key, this.slot.latest) }
    catch { this.slot.status = 'storage_error'; return false }
    return this.persist()
  }

  async persist(): Promise<boolean> {
    this.assertCurrent()
    const sequence = this.slot.sequence
    const value = this.slot.latest
    if (!this.slot.running) {
      this.slot.status = 'saving'
      const running = this.drain()
      this.slot.running = running
      void running.then(() => { if (this.slot.running === running) this.slot.running = null },
        () => { if (this.slot.running === running) this.slot.running = null })
    }
    const saved = await this.slot.running
    if (!this.isCurrent()) return false
    return saved && this.slot.sequence === sequence && this.slot.latest === value &&
      !this.slot.uncertain && this.slot.base.value === value && this.slot.status === 'saved_locally'
  }

  /** Called only after the editor has stopped all accepted operations. */
  release(): void {
    if (this.slot.owner !== this.owner) return
    this.slot.owner = null
    if (!this.slot.running && !this.slot.uncertain && this.slot.status === 'saved_locally' &&
      this.slot.latest === this.slot.base.value && slots.get(this.key) === this.slot) slots.delete(this.key)
  }

  private isCurrent(): boolean {
    try { return this.slot.owner === this.owner && this.auth.isCurrent(this.subjectId, this.generation) }
    catch { return false }
  }
  private assertCurrent(): void {
    if (!this.isCurrent()) throw new Error('账号已变化，请重新登录后恢复草稿')
  }

  private async drain(): Promise<boolean> {
    try {
      for (;;) {
        this.assertCurrent()
        if (this.slot.uncertain) {
          const attempt = this.slot.uncertain
          const observed = await this.storage.readVersioned(this.key)
          this.assertCurrent()
          const next = nextRevision(attempt.before.revision)
          if (!sameState(observed, attempt.before) &&
            !(next !== null && observed.found && observed.revision === next && observed.value === attempt.value)) {
            throw new Error('本机草稿版本需要核对')
          }
          this.slot.base = { ...observed }
          this.slot.uncertain = null
        }
        if (this.slot.latest === this.slot.base.value) {
          const confirmed = { ...this.slot.base }
          const observed = await this.storage.readVersioned(this.key)
          this.assertCurrent()
          if (!sameState(observed, confirmed)) throw new Error('本机草稿版本需要核对')
          if (this.slot.latest !== confirmed.value) continue
          this.slot.status = 'saved_locally'
          return true
        }
        const value = this.slot.latest
        const before = { ...this.slot.base }
        const commit = this.storage.prepareVersionedWrite(this.key, value)
        this.slot.uncertain = { before, value }
        const saved = await commit(before)
        this.assertCurrent()
        const next = nextRevision(before.revision)
        if (!saved.found || saved.value !== value || next === null || saved.revision !== next) {
          throw new Error('本机草稿保存结果未确认')
        }
        this.slot.base = { ...saved }
        this.slot.uncertain = null
      }
    } catch {
      this.slot.status = 'storage_error'
      return false
    }
  }
}
