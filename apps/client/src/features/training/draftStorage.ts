import { restoreDraft, type RecordingDraft } from './recording'

/** This is only a local persistence port. It never authorizes server access. */
export interface DraftStorage {
  read(key: string): Promise<unknown>
  write(key: string, value: string): Promise<void>
  /** Capture an already-authorized write before it enters the per-key queue. */
  prepareWrite?(key: string, value: string): () => Promise<void>
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
// A newer local write may come from another controller for the same draft.
// This process-wide monotonic fence conservatively invalidates all older
// proofs, without retaining an unbounded per-draft revision registry.
let persistenceWriteRevision = 0
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

declare const draftPersistenceProofBrand: unique symbol

/**
 * Opaque proof of one successful write in the current JavaScript runtime.
 * It is intentionally not serializable and does not prove cross-process or
 * cross-storage atomicity.
 */
export type DraftPersistenceProof = { readonly [draftPersistenceProofBrand]: true }

type DraftPersistenceProofRecord = {
  controller: DraftController
  subjectId: string
  generation: string
  revision: number
  writeRevision: number
  snapshot: string
}

const draftPersistenceProofs = new WeakMap<object, DraftPersistenceProofRecord>()

type DraftEnvelope = { schema: 1; subjectId: string; draft: RecordingDraft }

function cloneDraft(draft: RecordingDraft): RecordingDraft {
  // RecordingDraft is a JSON-shaped value. Returning a copy prevents callers
  // from mutating controller state without going through update().
  return JSON.parse(JSON.stringify(draft)) as RecordingDraft
}

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
  private currentPersistenceProof: object | null = null

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
    return cloneDraft(this.currentDraft)
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
    this.invalidatePersistenceProof()
    this.assertCurrent()
    const current = cloneDraft(this.currentDraft)
    const originalClientEventId = current.clientEventId
    const originalTimeZone = current.timeZone
    const next = transform(current)
    if (next.clientEventId !== originalClientEventId || next.timeZone !== originalTimeZone) {
      throw new Error('不能改变记录身份或已冻结的时区')
    }
    this.currentDraft = restoreDraft(next, todayInTimezone(next.timeZone, this.now()))
    return this.persist()
  }

  /** Stores an immutable snapshot. A later edit cannot be overwritten by an older write. */
  async persist(): Promise<boolean> {
    this.invalidatePersistenceProof()
    return (await this.persistSnapshot(false)).saved
  }

  /**
   * Persist the exact current draft and return an opaque in-process handle only
   * if that write completes successfully and is still the latest revision.
   * A later update/persist invalidates the handle before starting its write.
   */
  async persistWithProof(): Promise<DraftPersistenceProof | null> {
    this.invalidatePersistenceProof()
    return (await this.persistSnapshot(true)).proof
  }

  /** Check an opaque proof against this controller's live lease and exact current bytes. */
  isCurrentPersistenceProof(proof: unknown): proof is DraftPersistenceProof {
    if (!proof || typeof proof !== 'object' || this.currentPersistenceProof !== proof) return false
    const record = draftPersistenceProofs.get(proof)
    if (!record || record.controller !== this || record.subjectId !== this.subjectId ||
      record.generation !== this.generation || record.revision !== this.revision ||
      record.writeRevision !== persistenceWriteRevision || this.status !== 'saved_locally') return false
    try {
      this.assertCurrent()
      return JSON.stringify({ schema: 1, subjectId: this.subjectId, draft: this.currentDraft }) === record.snapshot
    } catch { return false }
  }

  private invalidatePersistenceProof(): void {
    if (this.currentPersistenceProof) draftPersistenceProofs.delete(this.currentPersistenceProof)
    this.currentPersistenceProof = null
  }

  private async persistSnapshot(issueProof: boolean): Promise<{ saved: boolean; proof: DraftPersistenceProof | null }> {
    this.assertCurrent()
    const revision = ++this.revision
    if (!Number.isSafeInteger(persistenceWriteRevision + 1)) throw new Error('本机写入版本不可用')
    const writeRevision = ++persistenceWriteRevision
    const envelope: DraftEnvelope = { schema: 1, subjectId: this.subjectId, draft: cloneDraft(this.currentDraft) }
    const snapshot = JSON.stringify(envelope)
    let commit: () => Promise<void>
    try {
      commit = this.storage.prepareWrite ? this.storage.prepareWrite(this.key, snapshot) : () => this.storage.write(this.key, snapshot)
      if (typeof commit !== 'function') throw new Error('本机草稿写入不可用')
    } catch {
      failedSnapshotsByKey.set(this.key, snapshot)
      this.status = 'storage_error'
      return { saved: false, proof: null }
    }
    this.status = 'saving'
    // Authorization was checked before the snapshot was queued. Finishing an
    // accepted local write after expiry preserves the draft; it grants no read
    // access or server authorization. Per-key ordering prevents a prior session
    // from overwriting a later session's write.
    const operation = enqueueWrite(this.key, async () => {
      try {
        await commit()
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
        return { saved: false, proof: null }
      }
      if (revision !== this.revision) return { saved: false, proof: null }
      if (issueProof && writeRevision !== persistenceWriteRevision) {
        this.status = 'not_saved'
        return { saved: false, proof: null }
      }
      this.status = 'saved_locally'
      if (!issueProof) return { saved: true, proof: null }
      const handle = Object.freeze({})
      draftPersistenceProofs.set(handle, { controller: this, subjectId: this.subjectId,
        generation: this.generation, revision, writeRevision, snapshot })
      this.currentPersistenceProof = handle
      return { saved: true, proof: handle as DraftPersistenceProof }
    } catch {
      if (revision === this.revision) this.status = 'storage_error'
      return { saved: false, proof: null }
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
