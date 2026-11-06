import {
  addExercise,
  addSet,
  confirmSet,
  createRecordingDraft,
  removeUnsubmittedSet,
  restoreDraft,
  switchMode,
  updateSet,
  type InputField,
  type RecordingDraft,
  type RecordingMode,
  type SetKind,
} from './recording'
import { DraftController, todayInTimezone, type AuthLease, type DraftStorage, type LocalDraftStatus } from './draftStorage'
import { DraftStageBusyError, DraftStageStorageError, supportsVersionedDraft, VersionedEditorDraft } from './versionedEditorDraft'
import type { PreparedFreeSessionRequest, ScopedSessionDetail } from './recordingApi'

/** The host must implement this with the platform's secure random ID source. */
export interface SecureRecordingIdProvider {
  nextId(): string
}

export type EditorFailure = 'invalid_input' | 'session_changed' | 'id_unavailable' | 'storage_error' | 'pending' | 'superseded' | 'closed'
export type EditorResult =
  | { ok: true; changed: boolean; localStatus: LocalDraftStatus }
  | { ok: false; reason: EditorFailure; changed?: boolean; message?: string; localStatus: LocalDraftStatus }

export interface NewTrainingEditorOptions {
  subjectId: string
  date: string
  timeZone: string
  storage: InitialDraftStorage
  now: () => Date
  auth: AuthLease
  ids: SecureRecordingIdProvider
}

/** Creation must use a platform adapter with an atomic no-overwrite operation. */
export interface InitialDraftStorage extends DraftStorage {
  /** `created` is committed and readable after restart; `exists` preserves old bytes. */
  createIfAbsent(key: string, value: string): Promise<'created' | 'exists'>
}

export type CreateTrainingEditorResult =
  | { ok: true; controller: TrainingEditorController }
  | { ok: false; reason: 'invalid_input' | 'id_unavailable' | 'already_exists' | 'already_open' | 'storage_unavailable' | 'session_changed' }
  | { ok: false; reason: 'outcome_unknown'; eventId: string }
export type AttachTrainingEditorResult =
  | { ok: true; controller: TrainingEditorController }
  | { ok: false; reason: 'storage_unavailable' | 'session_changed' | 'already_open' | 'invalid_input' }
export type RestoreTrainingEditorResult =
  | { ok: true; controller: TrainingEditorController }
  | { ok: false; reason: 'local_exists' | 'local_unsynced' | 'storage_unavailable' | 'outcome_unknown' | 'session_changed' | 'invalid_input' }

class EditorIDError extends Error {}
class EditorValidationError extends Error {}

class EditorOwnership {
  readonly key: string
  readonly subjectId: string
  readonly generation: string
  private readonly auth: AuthLease
  private active = true
  private closing = false
  private inFlight = 0
  private closePromise?: Promise<void>
  private resolveClose?: () => void
  private cleanup?: () => void

  constructor(
    key: string,
    subjectId: string,
    generation: string,
    auth: AuthLease,
  ) {
    this.key = key
    this.subjectId = subjectId
    this.generation = generation
    this.auth = auth
  }

  isCurrent(): boolean {
    try { return this.auth.isCurrent(this.subjectId, this.generation) }
    catch { return false }
  }

  assertActive(): void {
    if (!this.active || this.closing || editorOwners.get(this.key) !== this || !this.isCurrent()) {
      throw new Error('编辑器已关闭或登录会话已变化')
    }
  }

  begin(): (() => void) | null {
    if (!this.active || this.closing || editorOwners.get(this.key) !== this || !this.isCurrent()) return null
    this.inFlight++
    let released = false
    return () => {
      if (released) return
      released = true
      this.inFlight--
      if (this.closing && this.inFlight === 0) this.finishClose()
    }
  }

  close(): Promise<void> {
    if (this.closePromise) return this.closePromise
    this.closing = true
    this.closePromise = new Promise(resolve => {
      this.resolveClose = resolve
      if (this.inFlight === 0) this.finishClose()
    })
    return this.closePromise
  }

  isClosing(): boolean { return this.closing }

  setCleanup(cleanup: () => void): void {
    if (!this.active || this.closing || this.cleanup) throw new Error('编辑器已关闭')
    this.cleanup = cleanup
  }

  private finishClose(): void {
    if (!this.active || this.inFlight !== 0) return
    this.active = false
    this.cleanup?.()
    this.cleanup = undefined
    if (editorOwners.get(this.key) === this) editorOwners.delete(this.key)
    this.resolveClose?.()
    this.resolveClose = undefined
  }
}

// This registry arbitrates editor ownership only in the current JS runtime.
const editorOwners = new Map<string, EditorOwnership>()

async function acquireEditorOwnership(key: string, subjectId: string, generation: string, auth: AuthLease): Promise<EditorOwnership | null> {
  for (;;) {
    const existing = editorOwners.get(key)
    if (!existing) {
      const owner = new EditorOwnership(key, subjectId, generation, auth)
      editorOwners.set(key, owner)
      return owner
    }
    if (existing.isClosing()) {
      await existing.close()
      continue
    }
    if (existing.isCurrent()) return null
    await existing.close()
  }
}

function draftStorageKey(subjectId: string, clientEventId: string): string {
  if (!/^[1-9]\d{0,19}$/.test(subjectId) || !validKey(clientEventId)) throw new Error('invalid input')
  return `nerae.workout.draft.v1.${subjectId}.${clientEventId}`
}

function validKey(value: unknown): value is string {
  return typeof value === 'string' && Array.from(value).length >= 8 && Array.from(value).length <= 64 && !!value.trim()
}

function validDate(value: string): boolean {
  if (!/^\d{4}-\d{2}-\d{2}$/.test(value)) return false
  const parsed = new Date(`${value}T00:00:00.000Z`)
  return Number.isFinite(parsed.getTime()) && parsed.toISOString().slice(0, 10) === value
}

/**
 * Local-only editor orchestration. It does not call the server or mark edits
 * as remotely saved. Every read and mutation is bound to the captured
 * authenticated subject and generation.
 */
export class TrainingEditorController {
  private readonly local: DraftController | VersionedEditorDraft
  private readonly ids: SecureRecordingIdProvider
  private readonly now: () => Date
  private readonly ownership: EditorOwnership

  private constructor(
    local: DraftController | VersionedEditorDraft,
    ids: SecureRecordingIdProvider,
    now: () => Date,
    ownership: EditorOwnership,
  ) {
    this.local = local
    this.ids = ids
    this.now = now
    this.ownership = ownership
  }

  /** Install a validated cloud draft only when both local slots are empty. */
  static async restoreIntoEmptySlot(
    subjectId: string, draft: RecordingDraft, storage: InitialDraftStorage, now: () => Date,
    auth: AuthLease, ids: SecureRecordingIdProvider,
  ): Promise<RestoreTrainingEditorResult> {
    let generation: string
    try {
      generation = auth.generation
      if (!generation || !auth.isCurrent(subjectId, generation)) return { ok: false, reason: 'session_changed' }
    } catch { return { ok: false, reason: 'session_changed' } }
    if (!supportsVersionedDraft(storage) || typeof storage.createIfAbsent !== 'function') {
      return { ok: false, reason: 'storage_unavailable' }
    }
    const state: { ownership: EditorOwnership | null; releaseOperation: (() => void) | null;
      releaseReservation: (() => void) | null; handedOff: boolean } = {
      ownership: null, releaseOperation: null, releaseReservation: null, handedOff: false,
    }
    let outcome: RestoreTrainingEditorResult | undefined
    try {
      const restore = async (): Promise<RestoreTrainingEditorResult> => {
        const checked = restoreDraft(draft, todayInTimezone(draft.timeZone, now()))
        const key = draftStorageKey(subjectId, checked.clientEventId)
        const owner = await acquireEditorOwnership(key, subjectId, generation, auth)
        state.ownership = owner
        if (!owner) return { ok: false, reason: 'local_exists' }
        state.releaseOperation = owner.begin()
        if (!state.releaseOperation || !owner.isCurrent()) return { ok: false, reason: 'session_changed' }
        // A dirty in-memory slot can survive a failed write after its editor
        // closes. Reserve before touching native storage so it remains intact.
        state.releaseReservation = VersionedEditorDraft.reserveEmptyForCreation(subjectId, checked.clientEventId)
        if (!state.releaseReservation) return { ok: false, reason: 'local_unsynced' }
        const value = JSON.stringify({ schema: 1, subjectId, draft: checked })
        let result: unknown
        try { result = await storage.createIfAbsent(key, value) }
        catch { return { ok: false, reason: owner.isCurrent() ? 'outcome_unknown' : 'session_changed' } }
        if (!owner.isCurrent()) return { ok: false, reason: 'session_changed' }
        if (result === 'exists') return { ok: false, reason: 'local_exists' }
        if (result !== 'created') return { ok: false, reason: 'outcome_unknown' }
        // Release and acquire the regular open reservation without yielding.
        state.releaseReservation()
        state.releaseReservation = null
        let local: VersionedEditorDraft
        try { local = await VersionedEditorDraft.created(subjectId, checked, storage, now, auth) }
        catch { return { ok: false, reason: owner.isCurrent() ? 'outcome_unknown' : 'session_changed' } }
        try { owner.setCleanup(() => local.release()) }
        catch { local.release(); return { ok: false, reason: 'session_changed' } }
        if (!owner.isCurrent()) return { ok: false, reason: 'session_changed' }
        const controller = new TrainingEditorController(local, ids, now, owner)
        state.handedOff = true
        return { ok: true, controller }
      }
      outcome = await restore()
    } catch {
      let current = false
      try { current = auth.isCurrent(subjectId, generation) } catch { /* fail closed */ }
      outcome = { ok: false, reason: current ? 'invalid_input' : 'session_changed' }
    } finally {
      state.releaseReservation?.()
      state.releaseOperation?.()
      if (state.ownership && !state.handedOff) await state.ownership.close()
    }
    if (!outcome) return { ok: false, reason: 'storage_unavailable' }
    let current = false
    try { current = auth.isCurrent(subjectId, generation) } catch { /* fail closed */ }
    if (!current) {
      if (state.ownership && state.handedOff) await state.ownership.close()
      return { ok: false, reason: 'session_changed' }
    }
    return outcome
  }

  /** Begin a user-requested local draft and persist the initial empty snapshot. */
  static async create(options: NewTrainingEditorOptions): Promise<CreateTrainingEditorResult> {
    let generation: string
    try {
      generation = options.auth.generation
      if (!generation || !options.auth.isCurrent(options.subjectId, generation)) return { ok: false, reason: 'session_changed' }
    } catch { return { ok: false, reason: 'session_changed' } }
    let eventId: string
    let ownership: EditorOwnership | null = null
    let release: (() => void) | null = null
    let handedOff = false
    let outcome: CreateTrainingEditorResult | undefined
    try {
      try { eventId = options.ids.nextId() } catch { throw new EditorIDError() }
      if (!validKey(eventId)) throw new EditorIDError()
      const today = todayInTimezone(options.timeZone, options.now())
      const draft = createRecordingDraft(eventId, options.date, options.timeZone, today)
      const key = draftStorageKey(options.subjectId, eventId)
      if (typeof options.storage.createIfAbsent !== 'function') {
        outcome = { ok: false, reason: 'storage_unavailable' }
      } else {
        ownership = await acquireEditorOwnership(key, options.subjectId, generation, options.auth)
        if (!ownership) {
          outcome = { ok: false, reason: 'already_open' }
        } else {
          release = ownership.begin()
          if (!release) {
            outcome = ownership.isCurrent()
              ? { ok: false, reason: 'already_open' }
              : { ok: false, reason: 'session_changed' }
          } else {
            // The initial snapshot is inserted by one atomic operation. Never fall
            // back to DraftController.persist(), which is an unconditional write.
            const envelope = JSON.stringify({ schema: 1, subjectId: options.subjectId, draft })
            if (!this.isCurrent(options, generation)) throw new Error('session changed')
            let result: unknown
            let unknown = false
            try { result = await options.storage.createIfAbsent(key, envelope) }
            catch { unknown = true }

            if (!this.isCurrent(options, generation) || !ownership.isCurrent()) {
              outcome = { ok: false, reason: 'session_changed' }
            } else if (unknown || (result !== 'created' && result !== 'exists')) {
              outcome = { ok: false, reason: 'outcome_unknown', eventId }
            } else if (result === 'exists') {
              outcome = { ok: false, reason: 'already_exists' }
            } else {
              let local: DraftController | VersionedEditorDraft | undefined
              try {
                if (supportsVersionedDraft(options.storage)) {
                  local = await VersionedEditorDraft.created(options.subjectId, draft, options.storage, options.now, options.auth)
                  const versioned = local
                  ownership.setCleanup(() => versioned.release())
                } else {
                  local = new DraftController(options.subjectId, draft, options.storage, options.now, options.auth)
                  local.status = 'saved_locally'
                }
              } catch { /* creation may have committed; report an unknown outcome */ }
              if (!this.isCurrent(options, generation) || !ownership.isCurrent()) {
                outcome = { ok: false, reason: 'session_changed' }
              } else if (!local) {
                outcome = { ok: false, reason: 'outcome_unknown', eventId }
              } else {
                const controller = new TrainingEditorController(local, options.ids, options.now, ownership)
                handedOff = true
                outcome = { ok: true, controller }
              }
            }
          }
        }
      }
    } catch (error) {
      outcome = error instanceof EditorIDError
        ? { ok: false, reason: 'id_unavailable' }
        : this.isCurrent(options, generation)
          ? { ok: false, reason: 'invalid_input' }
          : { ok: false, reason: 'session_changed' }
    } finally {
      release?.()
      if (ownership && !handedOff) await ownership.close()
    }
    if (!outcome) return { ok: false, reason: 'storage_unavailable' }
    // The final lease check runs after ownership cleanup's await. In particular,
    // never disclose an uncertain event ID to a session that replaced its owner.
    if (!this.isCurrent(options, generation)) {
      if (ownership && handedOff) void ownership.close()
      return { ok: false, reason: 'session_changed' }
    }
    return outcome
  }

  /** Load and validate the existing envelope under the current auth lease. */
  static async attach(
    subjectId: string,
    clientEventId: string,
    storage: DraftStorage,
    now: () => Date,
    auth: AuthLease,
    ids: SecureRecordingIdProvider,
  ): Promise<AttachTrainingEditorResult> {
    let generation: string
    try {
      generation = auth.generation
      if (!generation || !auth.isCurrent(subjectId, generation)) return { ok: false, reason: 'session_changed' }
    } catch { return { ok: false, reason: 'session_changed' } }
    let ownership: EditorOwnership | null = null
    let release: (() => void) | null = null
    let handedOff = false
    let outcome: AttachTrainingEditorResult | undefined
    try {
      const key = draftStorageKey(subjectId, clientEventId)
      ownership = await acquireEditorOwnership(key, subjectId, generation, auth)
      if (!ownership) {
        outcome = { ok: false, reason: 'already_open' }
      } else {
        release = ownership.begin()
        if (!release) {
          outcome = ownership.isCurrent()
            ? { ok: false, reason: 'already_open' }
            : { ok: false, reason: 'session_changed' }
        } else {
          const local = supportsVersionedDraft(storage)
            ? await VersionedEditorDraft.load(subjectId, clientEventId, storage, now, auth)
            : await DraftController.load(subjectId, clientEventId, storage, now, auth)
          if (local instanceof VersionedEditorDraft) ownership.setCleanup(() => local.release())
          if (!auth.isCurrent(subjectId, generation) || !ownership.isCurrent()) {
            outcome = { ok: false, reason: 'session_changed' }
          } else {
            try { void local.draft }
            catch { outcome = { ok: false, reason: 'session_changed' } }
            if (!outcome) {
              const controller = new TrainingEditorController(local, ids, now, ownership)
              handedOff = true
              outcome = { ok: true, controller }
            }
          }
        }
      }
    } catch {
      let current = false
      try { current = auth.isCurrent(subjectId, generation) } catch { /* fail closed */ }
      outcome = !current
        ? { ok: false, reason: 'session_changed' }
        : ownership ? { ok: false, reason: 'storage_unavailable' } : { ok: false, reason: 'invalid_input' }
    } finally {
      release?.()
      if (ownership && !handedOff) await ownership.close()
    }
    if (!outcome) return { ok: false, reason: 'storage_unavailable' }
    let current = false
    try { current = auth.isCurrent(subjectId, generation) } catch { /* fail closed */ }
    if (!current) {
      if (ownership && handedOff) void ownership.close()
      return { ok: false, reason: 'session_changed' }
    }
    return outcome
  }

  private static isCurrent(options: NewTrainingEditorOptions, generation: string): boolean {
    try {
      return options.auth.isCurrent(options.subjectId, generation)
    } catch { return false }
  }

  get draft() {
    this.ownership.assertActive()
    return this.local.draft
  }
  get localStatus(): LocalDraftStatus { return this.local.status }

  /**
   * Close blocks new edits and releases ownership after in-flight operations settle.
   * It does not retry or confirm persistence; callers must inspect operation results
   * and localStatus before leaving. Failed snapshots may only remain in this process.
   */
  close(): Promise<void> { return this.ownership.close() }

  /** Retry the current device snapshot without altering entered values or claiming server sync. */
  async retryLocalSave(): Promise<EditorResult> {
    return this.withOwnership(async () => {
      try {
        const saved = await this.local.persist()
        // Recheck the captured lease even if the device write itself succeeded.
        try { void this.local.draft }
        catch { return { ok: false, reason: 'session_changed', localStatus: this.local.status } }
        if (saved && this.local.status === 'saved_locally') return this.success(false)
        if (this.local.status === 'storage_error') return { ok: false, reason: 'storage_error', localStatus: this.local.status }
        if (this.local.status === 'saving' || this.local.status === 'not_saved') {
          return { ok: false, reason: 'pending', localStatus: this.local.status }
        }
        return { ok: false, reason: 'superseded', localStatus: this.local.status }
      } catch (error) { return this.failure(error) }
    })
  }

  /** Stage one creation request in the protected draft without sending it. */
  async stageFreeSession(request: PreparedFreeSessionRequest): Promise<EditorResult> {
    return this.withOwnership(async () => {
      if (!(this.local instanceof VersionedEditorDraft)) return this.invalid()
      try {
        const staged = await this.local.stageFreeSession(request)
        try { void this.local.draft }
        catch { return { ok: false, reason: 'session_changed', localStatus: this.local.status } }
        if (staged.saved && this.local.status === 'saved_locally') return this.success(staged.changed)
        if (this.local.status === 'storage_error') return { ok: false, reason: 'storage_error', changed: staged.changed, localStatus: this.local.status }
        return { ok: false, reason: 'pending', changed: staged.changed, localStatus: this.local.status }
      } catch (error) {
        if (error instanceof DraftStageBusyError) return { ok: false, reason: 'pending', localStatus: this.local.status }
        if (error instanceof DraftStageStorageError) return { ok: false, reason: 'storage_error', localStatus: this.local.status }
        return this.failure(error)
      }
    })
  }

  /** Apply a verified creation response without replacing later local input. */
  async acknowledgeFreeSession(request: PreparedFreeSessionRequest, response: ScopedSessionDetail): Promise<EditorResult> {
    return this.withOwnership(async () => {
      if (!(this.local instanceof VersionedEditorDraft)) return this.invalid()
      try {
        const saved = await this.local.acknowledgeFreeSession(request, response)
        try { void this.local.draft }
        catch { return { ok: false, reason: 'session_changed', localStatus: this.local.status } }
        if (saved && this.local.status === 'saved_locally') return this.success(true)
        if (this.local.status === 'storage_error') return { ok: false, reason: 'storage_error', changed: true, localStatus: this.local.status }
        return { ok: false, reason: 'pending', changed: true, localStatus: this.local.status }
      } catch (error) {
        if (error instanceof DraftStageBusyError) return { ok: false, reason: 'pending', localStatus: this.local.status }
        if (error instanceof DraftStageStorageError) return { ok: false, reason: 'storage_error', localStatus: this.local.status }
        return this.failure(error)
      }
    })
  }

  async setMode(mode: RecordingMode): Promise<EditorResult> {
    return this.withOwnership(() => this.mutate(draft => switchMode(draft, mode)))
  }

  async setDate(date: string): Promise<EditorResult> {
    return this.withOwnership(async () => {
      try {
        const current = this.local.draft
        if (!validDate(date) || date > todayInTimezone(current.timeZone, this.now())) return this.invalid()
        return await this.mutate(draft => draft.date === date ? draft : { ...draft, date })
      } catch (error) { return this.failure(error) }
    })
  }

  async addCustomExercise(name: string, kind: SetKind): Promise<EditorResult> {
    return this.withOwnership(async () => {
      try {
        const draft = this.local.draft
        const entryKey = this.newUniqueKey(draft.exercises.map(item => item.exerciseEntryKey))
        const recordKeys = draft.exercises.flatMap(item => item.sets.map(set => set.recordKey))
        const firstRecordKey = this.newUniqueKey(recordKeys)
        return await this.mutate(current => addExercise(current, entryKey, { source: 'custom', exerciseName: name }, kind, firstRecordKey))
      } catch (error) { return this.failure(error) }
    })
  }

  async addGroup(exerciseEntryKey: string): Promise<EditorResult> {
    return this.withOwnership(async () => {
      try {
        const draft = this.local.draft
        const key = this.newUniqueKey(draft.exercises.flatMap(item => item.sets.map(set => set.recordKey)))
        return await this.mutate(current => addSet(current, exerciseEntryKey, key))
      } catch (error) { return this.failure(error) }
    })
  }

  async editGroup(exerciseEntryKey: string, recordKey: string, field: InputField, value: string): Promise<EditorResult> {
    return this.withOwnership(async () => {
      try {
        this.findGroup(this.local.draft, exerciseEntryKey, recordKey)
        return await this.mutate(draft => updateSet(draft, exerciseEntryKey, recordKey, field, value))
      } catch (error) { return this.failure(error) }
    })
  }

  async removeGroup(exerciseEntryKey: string, recordKey: string): Promise<EditorResult> {
    return this.withOwnership(async () => {
      try {
        const draft = this.local.draft
        const exercise = draft.exercises.find(item => item.exerciseEntryKey === exerciseEntryKey)
        if (!exercise) return this.invalid()
        const set = exercise.sets.find(item => item.recordKey === recordKey)
        if (!set) return this.success(false)
        if (set.persisted) return this.invalid()
        return await this.mutate(current => removeUnsubmittedSet(current, exerciseEntryKey, recordKey))
      } catch (error) { return this.failure(error) }
    })
  }

  async confirmGroup(exerciseEntryKey: string, recordKey: string): Promise<EditorResult> {
    return this.withOwnership(() => this.mutate(draft => {
        if (draft.mode !== 'per_set') throw new Error('invalid input')
        this.findGroup(draft, exerciseEntryKey, recordKey)
        const result = confirmSet(draft, exerciseEntryKey, recordKey)
        if (!result.ok) throw new EditorValidationError(result.message)
        return result.draft
      })
    )
  }

  private async withOwnership(operation: () => Promise<EditorResult>): Promise<EditorResult> {
    const release = this.ownership.begin()
    if (!release) {
      return {
        ok: false,
        reason: this.ownership.isCurrent() ? 'closed' : 'session_changed',
        localStatus: this.local.status,
      }
    }
    try { return await operation() }
    finally { release() }
  }

  private async mutate(transform: (draft: RecordingDraft) => RecordingDraft): Promise<EditorResult> {
    try {
      const before = this.local.draft
      const next = transform(before)
      if (next === before) return this.success(false)
      const saved = await this.local.update(() => next)
      let latest: RecordingDraft
      try { latest = this.local.draft } catch { return { ok: false, reason: 'session_changed', localStatus: this.local.status } }
      if (JSON.stringify(latest) !== JSON.stringify(next)) {
        return { ok: false, reason: 'superseded', changed: true, localStatus: this.local.status }
      }
      if (!saved) {
        if (this.local.status === 'storage_error') return { ok: false, reason: 'storage_error', changed: true, localStatus: this.local.status }
        if (this.local.status === 'saving' || this.local.status === 'not_saved') {
          return { ok: false, reason: 'pending', changed: true, localStatus: this.local.status }
        }
        return { ok: false, reason: 'superseded', changed: true, localStatus: this.local.status }
      }
      if (this.local.status === 'storage_error') return { ok: false, reason: 'storage_error', changed: true, localStatus: this.local.status }
      if (this.local.status === 'saving' || this.local.status === 'not_saved') {
        return { ok: false, reason: 'pending', changed: true, localStatus: this.local.status }
      }
      return this.success(true)
    } catch (error) { return this.failure(error) }
  }

  private newUniqueKey(existing: string[]): string {
    let value: string
    try { value = this.ids.nextId() } catch { throw new EditorIDError() }
    if (!validKey(value) || existing.includes(value)) throw new EditorIDError()
    return value
  }

  private findGroup(draft: RecordingDraft, exerciseKey: string, recordKey: string) {
    const exercise = draft.exercises.find(item => item.exerciseEntryKey === exerciseKey)
    const set = exercise?.sets.find(item => item.recordKey === recordKey)
    if (!set) throw new Error('invalid input')
    return set
  }

  private success(changed: boolean): EditorResult { return { ok: true, changed, localStatus: this.local.status } }
  private invalid(message?: string): EditorResult { return { ok: false, reason: 'invalid_input', ...(message ? { message } : {}), localStatus: this.local.status } }
  private failure(error: unknown): EditorResult {
    try { void this.local.draft } catch { return { ok: false, reason: 'session_changed', localStatus: this.local.status } }
    if (error instanceof EditorValidationError) return this.invalid(error.message)
    return { ok: false, reason: error instanceof EditorIDError ? 'id_unavailable' : 'invalid_input', localStatus: this.local.status }
  }
}
