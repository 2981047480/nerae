import {
  addExercise,
  addSet,
  confirmSet,
  createRecordingDraft,
  removeUnsubmittedSet,
  switchMode,
  updateSet,
  type InputField,
  type RecordingDraft,
  type RecordingMode,
  type SetKind,
} from './recording'
import { DraftController, todayInTimezone, type AuthLease, type DraftStorage, type LocalDraftStatus } from './draftStorage'

/** The host must implement this with the platform's secure random ID source. */
export interface SecureRecordingIdProvider {
  nextId(): string
}

export type EditorFailure = 'invalid_input' | 'session_changed' | 'id_unavailable' | 'storage_error' | 'pending' | 'superseded'
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
  | { ok: false; reason: 'invalid_input' | 'id_unavailable' | 'already_exists' | 'storage_unavailable' | 'session_changed' }
  | { ok: false; reason: 'outcome_unknown'; eventId: string }
export type AttachTrainingEditorResult =
  | { ok: true; controller: TrainingEditorController }
  | { ok: false; reason: 'storage_unavailable' | 'session_changed' }

class EditorIDError extends Error {}
class EditorValidationError extends Error {}

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
 * as remotely saved. Every read and mutation is checked by DraftController's
 * captured authenticated subject and generation.
 */
export class TrainingEditorController {
  private readonly local: DraftController
  private readonly ids: SecureRecordingIdProvider
  private readonly now: () => Date

  private constructor(
    local: DraftController,
    ids: SecureRecordingIdProvider,
    now: () => Date,
  ) {
    this.local = local
    this.ids = ids
    this.now = now
  }

  /** Begin a user-requested local draft and persist the initial empty snapshot. */
  static async create(options: NewTrainingEditorOptions): Promise<CreateTrainingEditorResult> {
    let generation: string
    try {
      generation = options.auth.generation
      if (!generation || !options.auth.isCurrent(options.subjectId, generation)) return { ok: false, reason: 'session_changed' }
    } catch { return { ok: false, reason: 'session_changed' } }
    let eventId: string
    try {
      try { eventId = options.ids.nextId() } catch { throw new EditorIDError() }
      if (!validKey(eventId)) throw new EditorIDError()
      const today = todayInTimezone(options.timeZone, options.now())
      const draft = createRecordingDraft(eventId, options.date, options.timeZone, today)
      const local = new DraftController(options.subjectId, draft, options.storage, options.now, options.auth)
      if (typeof options.storage.createIfAbsent !== 'function') return { ok: false, reason: 'storage_unavailable' }
      // The initial snapshot is inserted by one atomic operation. Never fall
      // back to DraftController.persist(), which is an unconditional write.
      const envelope = JSON.stringify({ schema: 1, subjectId: options.subjectId, draft })
      local.draft // Recheck the captured authenticated lease immediately before the CAS-like create.
      let result: unknown
      try { result = await options.storage.createIfAbsent(local.key, envelope) }
      catch {
        return this.isCurrent(options, generation)
          ? { ok: false, reason: 'outcome_unknown', eventId }
          : { ok: false, reason: 'session_changed' }
      }
      try { local.draft } catch { return { ok: false, reason: 'session_changed' } }
      if (result === 'exists') return { ok: false, reason: 'already_exists' }
      if (result !== 'created') return { ok: false, reason: 'outcome_unknown', eventId }
      // The port's definite `created` result is the persistence acknowledgement;
      // do not reread by key, which could observe a stale or ambiguous snapshot.
      local.status = 'saved_locally'
      return { ok: true, controller: new TrainingEditorController(local, options.ids, options.now) }
    } catch (error) {
      if (error instanceof EditorIDError) return { ok: false, reason: 'id_unavailable' }
      return this.isCurrent(options, generation) ? { ok: false, reason: 'invalid_input' } : { ok: false, reason: 'session_changed' }
    }
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
    try {
      const local = await DraftController.load(subjectId, clientEventId, storage, now, auth)
      if (!auth.isCurrent(subjectId, generation)) return { ok: false, reason: 'session_changed' }
      try { void local.draft } catch { return { ok: false, reason: 'session_changed' } }
      return { ok: true, controller: new TrainingEditorController(local, ids, now) }
    } catch {
      try {
        if (!auth.isCurrent(subjectId, generation)) return { ok: false, reason: 'session_changed' }
      } catch { return { ok: false, reason: 'session_changed' } }
      return { ok: false, reason: 'storage_unavailable' }
    }
  }

  private static isCurrent(options: NewTrainingEditorOptions, generation: string): boolean {
    try {
      return options.auth.isCurrent(options.subjectId, generation)
    } catch { return false }
  }

  get draft() { return this.local.draft }
  get localStatus(): LocalDraftStatus { return this.local.status }

  async setMode(mode: RecordingMode): Promise<EditorResult> {
    return this.mutate(draft => switchMode(draft, mode))
  }

  async setDate(date: string): Promise<EditorResult> {
    try {
      const current = this.local.draft
      if (!validDate(date) || date > todayInTimezone(current.timeZone, this.now())) return this.invalid()
      return await this.mutate(draft => draft.date === date ? draft : { ...draft, date })
    } catch (error) { return this.failure(error) }
  }

  async addCustomExercise(name: string, kind: SetKind): Promise<EditorResult> {
    try {
      const draft = this.local.draft
      const entryKey = this.newUniqueKey(draft.exercises.map(item => item.exerciseEntryKey))
      const recordKeys = draft.exercises.flatMap(item => item.sets.map(set => set.recordKey))
      const firstRecordKey = this.newUniqueKey(recordKeys)
      return await this.mutate(current => addExercise(current, entryKey, { source: 'custom', exerciseName: name }, kind, firstRecordKey))
    } catch (error) { return this.failure(error) }
  }

  async addGroup(exerciseEntryKey: string): Promise<EditorResult> {
    try {
      const draft = this.local.draft
      const key = this.newUniqueKey(draft.exercises.flatMap(item => item.sets.map(set => set.recordKey)))
      return await this.mutate(current => addSet(current, exerciseEntryKey, key))
    } catch (error) { return this.failure(error) }
  }

  async editGroup(exerciseEntryKey: string, recordKey: string, field: InputField, value: string): Promise<EditorResult> {
    try {
      this.findGroup(this.local.draft, exerciseEntryKey, recordKey)
      return await this.mutate(draft => updateSet(draft, exerciseEntryKey, recordKey, field, value))
    } catch (error) { return this.failure(error) }
  }

  async removeGroup(exerciseEntryKey: string, recordKey: string): Promise<EditorResult> {
    try {
      const draft = this.local.draft
      const exercise = draft.exercises.find(item => item.exerciseEntryKey === exerciseEntryKey)
      if (!exercise) return this.invalid()
      const set = exercise.sets.find(item => item.recordKey === recordKey)
      if (!set) return this.success(false)
      if (set.persisted) return this.invalid()
      return await this.mutate(current => removeUnsubmittedSet(current, exerciseEntryKey, recordKey))
    } catch (error) { return this.failure(error) }
  }

  async confirmGroup(exerciseEntryKey: string, recordKey: string): Promise<EditorResult> {
    return this.mutate(draft => {
      if (draft.mode !== 'per_set') throw new Error('invalid input')
      this.findGroup(draft, exerciseEntryKey, recordKey)
      const result = confirmSet(draft, exerciseEntryKey, recordKey)
      if (!result.ok) throw new EditorValidationError(result.message)
      return result.draft
    })
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
