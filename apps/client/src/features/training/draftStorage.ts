import { restoreDraft, type RecordingDraft } from './recording'

/** This is only a local persistence port. It never authorizes server access. */
export interface DraftStorage {
  read(key: string): Promise<unknown>
  write(key: string, value: string): Promise<void>
}

export interface AuthLease {
  /** Unique to this authenticated session and invalidated on logout or switch. */
  readonly generation: string
  /** Backed by the app's verified session, never by user-entered identity. */
  isCurrent(subjectId: string, generation: string): boolean
}

// Storage is shared by controller instances in one app runtime. Serializing by
// key ensures an already-started old-session write finishes before a new write.
const writesByKey = new Map<string, Promise<void>>()
// An unsuccessful disk write must not make an older disk snapshot look current
// to another controller in the same runtime. This is not durable storage.
const failedSnapshotsByKey = new Map<string, string>()

function enqueueWrite(key: string, operation: () => Promise<void>): Promise<void> {
  const preceding = writesByKey.get(key) || Promise.resolve()
  const next = preceding.catch(() => undefined).then(operation)
  writesByKey.set(key, next)
  const cleanup = () => { if (writesByKey.get(key) === next) writesByKey.delete(key) }
  void next.then(cleanup, cleanup)
  return next
}

export type LocalDraftStatus = 'not_saved' | 'saving' | 'saved_locally' | 'storage_error'

type DraftEnvelope = { schema: 1; subjectId: string; draft: RecordingDraft }

function storageKey(subjectId: string, clientEventId: string): string {
  if (!/^[1-9]\d{0,19}$/.test(subjectId)) throw new Error('需要已验证的账号才能保存草稿')
  if (Array.from(clientEventId).length < 8 || Array.from(clientEventId).length > 64 || !clientEventId.trim()) {
    throw new Error('记录标识无效')
  }
  return `nerae.workout.draft.v1.${subjectId}.${clientEventId}`
}

/** Compute the date in the session's frozen timezone; this is not a timer. */
export function todayInTimezone(timeZone: string, now: Date): string {
  if (Number.isNaN(now.getTime())) throw new Error('当前日期不可用')
  let parts: Intl.DateTimeFormatPart[]
  try {
    parts = new Intl.DateTimeFormat('en-US', {
      timeZone, year: 'numeric', month: '2-digit', day: '2-digit', calendar: 'gregory',
    }).formatToParts(now)
  } catch { throw new Error('训练时区不可用') }
  const year = parts.find(part => part.type === 'year')?.value
  const month = parts.find(part => part.type === 'month')?.value
  const day = parts.find(part => part.type === 'day')?.value
  if (!year || !month || !day) throw new Error('当前日期不可用')
  return `${year.padStart(4, '0')}-${month}-${day}`
}

/**
 * Caller must supply the current authenticated subject and stop using the
 * controller on logout or subject change. The server still verifies all access.
 * Local writes accepted while the lease was current finish in order even if
 * the session expires. A new session cannot read them until it authenticates
 * as the same subject. Failures keep the latest draft in memory.
 */
export class DraftController {
  readonly subjectId: string
  readonly key: string
  private currentDraft: RecordingDraft
  status: LocalDraftStatus = 'not_saved'
  private revision = 0
  private readonly storage: DraftStorage
  private readonly now: () => Date
  private readonly auth: AuthLease
  private readonly generation: string

  constructor(subjectId: string, draft: RecordingDraft, storage: DraftStorage, now: () => Date, auth: AuthLease) {
    this.subjectId = subjectId
    this.key = storageKey(subjectId, draft.clientEventId)
    this.storage = storage
    this.now = now
    this.auth = auth
    this.generation = auth.generation
    if (!this.generation) throw new Error('登录会话不可用')
    this.assertCurrent()
    this.currentDraft = restoreDraft(draft, todayInTimezone(draft.timeZone, now()))
  }

  get draft(): RecordingDraft {
    this.assertCurrent()
    return this.currentDraft
  }

  /** Bind network operations to the same authenticated owner as this draft. */
  get recordingLease(): { subjectId: string; generation: string } {
    this.assertCurrent()
    return { subjectId: this.subjectId, generation: this.generation }
  }

  private assertCurrent(): void {
    if (!this.auth.isCurrent(this.subjectId, this.generation)) throw new Error('账号已变化，请重新登录后恢复草稿')
  }

  static async load(subjectId: string, clientEventId: string, storage: DraftStorage, now: () => Date, auth: AuthLease): Promise<DraftController> {
    const key = storageKey(subjectId, clientEventId)
    const generation = auth.generation
    if (!generation || !auth.isCurrent(subjectId, generation)) throw new Error('账号已变化，请重新登录后恢复草稿')
    const pending = writesByKey.get(key)
    if (pending) await pending.catch(() => undefined)
    if (!auth.isCurrent(subjectId, generation)) throw new Error('账号已变化，请重新登录后恢复草稿')
    const failedSnapshot = failedSnapshotsByKey.get(key)
    const raw = failedSnapshot === undefined ? await storage.read(key) : failedSnapshot
    if (!auth.isCurrent(subjectId, generation)) throw new Error('账号已变化，请重新登录后恢复草稿')
    let envelope: unknown = raw
    if (typeof raw === 'string') {
      try { envelope = JSON.parse(raw) } catch { throw new Error('本机草稿无法读取，原数据仍被保留') }
    }
    if (!envelope || typeof envelope !== 'object' || Array.isArray(envelope)) {
      throw new Error('本机草稿格式无效，原数据仍被保留')
    }
    const value = envelope as Record<string, unknown>
    if (value.schema !== 1 || value.subjectId !== subjectId || !value.draft || typeof value.draft !== 'object') {
      throw new Error('本机草稿不属于当前账号或版本不受支持')
    }
    const candidate = value.draft as Record<string, unknown>
    if (candidate.clientEventId !== clientEventId || typeof candidate.timeZone !== 'string') {
      throw new Error('本机草稿与当前记录不匹配')
    }
    const restored = restoreDraft(candidate, todayInTimezone(candidate.timeZone, now()))
    const controller = new DraftController(subjectId, restored, storage, now, auth)
    controller.status = failedSnapshot === undefined ? 'saved_locally' : 'storage_error'
    return controller
  }

  /** Update visible state first; a failed device write never discards the input. */
  async update(transform: (draft: RecordingDraft) => RecordingDraft): Promise<boolean> {
    this.assertCurrent()
    const next = transform(this.draft)
    if (next.clientEventId !== this.draft.clientEventId || next.timeZone !== this.draft.timeZone) {
      throw new Error('不能改变记录身份或已冻结的时区')
    }
    this.currentDraft = restoreDraft(next, todayInTimezone(next.timeZone, this.now()))
    return this.persist()
  }

  /** Stores an immutable snapshot. A later edit cannot be overwritten by an older write. */
  async persist(): Promise<boolean> {
    this.assertCurrent()
    const revision = ++this.revision
    const envelope: DraftEnvelope = { schema: 1, subjectId: this.subjectId, draft: this.draft }
    const snapshot = JSON.stringify(envelope)
    this.status = 'saving'
    // Authorization was checked before the snapshot was queued. Finishing an
    // accepted local write after expiry preserves the draft; it grants no read
    // access or server authorization. Per-key ordering prevents a prior session
    // from overwriting a later session's write.
    const operation = enqueueWrite(this.key, async () => {
      try {
        await this.storage.write(this.key, snapshot)
        failedSnapshotsByKey.delete(this.key)
      } catch (error) {
        failedSnapshotsByKey.set(this.key, snapshot)
        throw error
      }
    })
    try {
      await operation
      if (!this.auth.isCurrent(this.subjectId, this.generation)) {
        if (revision === this.revision) this.status = 'not_saved'
        return false
      }
      if (revision !== this.revision) return false
      this.status = 'saved_locally'
      return true
    } catch {
      if (revision === this.revision) this.status = 'storage_error'
      return false
    }
  }
}

/** Uses uni-app's real platform storage on iOS, Android and WeChat. */
export function createUniDraftStorage(): DraftStorage {
  return {
    read(key) {
      return new Promise((resolve, reject) => {
        uni.getStorage({
          key,
          success: result => resolve(result.data),
          fail: () => reject(new Error('读取本机草稿失败')),
        })
      })
    },
    write(key, value) {
      return new Promise((resolve, reject) => {
        uni.setStorage({
          key,
          data: value,
          success: () => resolve(),
          fail: () => reject(new Error('保存本机草稿失败')),
        })
      })
    },
  }
}
