import { restoreDraft, type RecordingDraft } from './recording'
import { encodeDraftEnvelope, supportsDraftEnvelope } from './draftEnvelope'
import { todayInTimezone, type AuthLease, type DraftStorage, type DraftVersionState, type LocalDraftStatus } from './draftStorage'
import { acknowledgesFreeSession, encodePendingSetsBody, isPreparedFreeSessionFor, isPreparedSetsFor, isIssuedRecordingV2Snapshot, acknowledgesStoredRecordingV2, RecordingApi,
  type PreparedFreeSessionRequest, type PreparedSetsRequest, type RecordingAuth, type ScopedSessionDetail, type ScopedRecordingV2Detail, type ScopedRecordingV2Receipt } from './recordingApi'
import { prepareRecordingV2Batch, rebaseAcknowledgedRecordingV2 } from './recordingV2Projection'
import { pendingRecordingV2, recordingV2Marker } from './recordingV2Intent'
import { applyRecordingV2ConflictChoices, type ConflictChoice } from './recordingV2ConflictPlan'
import { adoptCloudRecordingV2 } from './recordingV2CloudAdoption'
import type { JournalRequest } from './operationJournal'
import { freeSessionMarker, pendingFreeSession } from './freeSessionIntent'
import { applyPendingSetsAcknowledgement, captureOrderedInputs, type OrderedPreparation } from './recordingProjection'
import { pendingSets, setsMarker } from './setsIntent'

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

export class DraftStageBusyError extends Error {}
export class DraftStageStorageError extends Error {}

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
  return encodeDraftEnvelope(subjectId, draft)
}

function pendingMarker(draft: RecordingDraft): { present: boolean; value: unknown } {
  return { present: Object.prototype.hasOwnProperty.call(draft, 'syncPending'),
    value: draft.syncPending }
}

function decode(raw: string, subjectId: string, eventId: string, now: () => Date): RecordingDraft {
  let parsed: unknown
  try { parsed = JSON.parse(raw) } catch { throw new Error('本机草稿无法读取') }
  if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) throw new Error('本机草稿格式无效')
  const value = parsed as Record<string, unknown>
  if (!supportsDraftEnvelope(value.schema, value.draft) || value.subjectId !== subjectId) {
    throw new Error('本机草稿不属于当前账号或版本不受支持')
  }
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

  /** Reserve an empty process-local slot before an atomic external restore. */
  static reserveEmptyForCreation(subjectId: string, eventId: string): (() => void) | null {
    const key = draftKey(subjectId, eventId)
    if (slots.has(key) || reservations.has(key)) return null
    reservations.add(key)
    let active = true
    return () => {
      if (!active) return
      active = false
      reservations.delete(key)
    }
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
    const pending = pendingMarker(current)
    const eventId = current.clientEventId
    const timeZone = current.timeZone
    const date = current.date
    const server = JSON.stringify(current.server)
    const next = transform(current)
    if (next.clientEventId !== eventId || next.timeZone !== timeZone) {
      throw new Error('不能改变记录身份或已冻结的时区')
    }
    if ((current.recordFormat === 2 && next.recordFormat !== 2) ||
        (current.server && current.recordFormat !== 2 && next.recordFormat === 2)) {
      throw new Error('不能改变已有训练记录的格式')
    }
    if (current.syncTracking !== next.syncTracking) throw new Error('同步基线不能由普通编辑更改')
    const proposedPending = pendingMarker(next)
    if (pending.present !== proposedPending.present || (pending.present &&
      (typeof pending.value !== 'string' || proposedPending.value !== pending.value || (next.date !== date && current.recordFormat !== 2) ||
        JSON.stringify(next.server) !== server))) {
      throw new Error('待核对的训练请求不能由普通编辑更改')
    }
    const pendingBatch = pendingSets(current, this.subjectId)
    if (pendingBatch) {
      for (const item of pendingBatch.submitted) {
        const beforeExercise = current.exercises.find(exercise => exercise.exerciseEntryKey === item.exerciseEntryKey)
        const afterExercise = next.exercises.find(exercise => exercise.exerciseEntryKey === item.exerciseEntryKey)
        const beforeGroup = beforeExercise?.sets.find(group => group.recordKey === item.recordKey)
        const afterGroup = afterExercise?.sets.find(group => group.recordKey === item.recordKey)
        if (!beforeExercise || !afterExercise || !beforeGroup || !afterGroup ||
          beforeExercise.kind !== afterExercise.kind || JSON.stringify(beforeExercise.identity) !== JSON.stringify(afterExercise.identity) ||
          beforeGroup.setIndex !== afterGroup.setIndex) throw new Error('正在同步的训练组不能删除或改变身份')
      }
    }
    const checked = restoreDraft(next, todayInTimezone(next.timeZone, this.now()))
    this.slot.latest = envelope(this.subjectId, checked)
    this.slot.sequence++
    try { this.storage.prepareVersionedWrite(this.key, this.slot.latest) }
    catch { this.slot.status = 'storage_error'; return false }
    return this.persist()
  }

  /** Save one exact v2 intent in the same versioned slot as the user's draft. */
  async stageRecordingV2(request: JournalRequest, baseline: ScopedRecordingV2Detail | null,
    api: RecordingApi, auth: RecordingAuth): Promise<{ saved: boolean; changed: boolean }> {
    this.assertCurrent()
    const draft = this.draft
    if (draft.recordFormat !== 2 || !(api instanceof RecordingApi) ||
        !RecordingApi.prototype.isBoundToAuth.call(api, auth, { subjectId: this.subjectId, generation: this.generation })) {
      throw new Error('训练请求不属于当前账号')
    }
    let marker: string
    try { marker = recordingV2Marker(this.subjectId, this.eventId, request) }
    catch { throw new Error('待发训练请求无效') }
    const pending = pendingMarker(draft)
    if (pending.present) {
      if (pending.value !== marker || !pendingRecordingV2(draft, this.subjectId)) throw new Error('已有待核对的训练请求')
      return { saved: await this.persist(), changed: false }
    }
    if (this.slot.running || this.slot.status === 'saving') throw new DraftStageBusyError('本机草稿正在保存')
    if (this.slot.uncertain || this.slot.status !== 'saved_locally' || this.slot.latest !== this.slot.base.value) {
      throw new DraftStageStorageError('本机草稿尚未确认')
    }
    const proposal = { ...draft, syncPending: marker }
    if (!pendingRecordingV2(proposal, this.subjectId)) throw new Error('待发训练请求无效')
    if (request.path === '/me/recordings') {
      const expected = JSON.stringify({ source: 'free', date: draft.date, timeZone: draft.timeZone,
        clientEventId: draft.clientEventId, mode: draft.mode })
      if (baseline !== null || request.body !== expected) throw new Error('创建请求与草稿不一致')
    } else {
      if (!baseline || !isIssuedRecordingV2Snapshot(baseline, api) || baseline.lease.subjectId !== this.subjectId ||
          baseline.lease.generation !== this.generation) throw new Error('服务端快照不可用')
      const prepared = prepareRecordingV2Batch(draft, baseline.detail)
      if (!prepared.ok) throw new Error('记录需要核对')
      if (request.path.endsWith('/sets')) {
        if (!prepared.changed || request.body !== JSON.stringify(prepared.batch)) throw new Error('训练组与请求不一致')
      } else if (!request.path.endsWith('/finish') || !draft.completionRequested || prepared.changed ||
          !draft.exercises.some(exercise => exercise.sets.length > 0) ||
          request.body !== JSON.stringify({ expectedVersion: baseline.detail.version })) throw new Error('完成请求与草稿不一致')
    }
    const staged = envelope(this.subjectId, proposal)
    try { this.storage.prepareVersionedWrite(this.key, staged) }
    catch { throw new DraftStageStorageError('本机待发请求无法保存') }
    this.slot.latest = staged
    this.slot.sequence++
    return { saved: await this.persist(), changed: true }
  }

  /** Atomically apply a request-bound receipt without replacing newer local edits. */
  async acknowledgeRecordingV2(response: ScopedRecordingV2Receipt, snapshot: ScopedRecordingV2Detail,
    api: RecordingApi, auth: RecordingAuth): Promise<boolean> {
    this.assertCurrent()
    const draft = this.draft
    const marker = draft.syncPending
    const pending = pendingRecordingV2(draft, this.subjectId)
    if (!pending || typeof marker !== 'string' || !acknowledgesStoredRecordingV2(response, marker, api, auth) ||
        !isIssuedRecordingV2Snapshot(snapshot, api) || response.lease.subjectId !== this.subjectId ||
        response.lease.generation !== this.generation || snapshot.lease.subjectId !== this.subjectId ||
        snapshot.lease.generation !== this.generation || snapshot.detail.id !== response.receipt.id ||
        snapshot.detail.version !== response.receipt.version || snapshot.detail.clientEventId !== this.eventId) {
      throw new Error('训练结果需要核对')
    }
    const submittedBody = JSON.parse(pending.request.body) as { date?: unknown }
    if (submittedBody.date !== undefined && snapshot.detail.date !== submittedBody.date) throw new Error('训练日期需要核对')
    if (pending.request.path === '/me/recordings') {
      if (response.receipt.version !== 1 || snapshot.detail.status !== 'in_progress' || snapshot.detail.entries.length !== 0 ||
          snapshot.detail.sets.length !== 0 || snapshot.detail.removedSets.length !== 0) throw new Error('训练创建结果需要核对')
    } else if (pending.request.path.endsWith('/finish') && snapshot.detail.status !== 'completed') {
      throw new Error('训练完成结果需要核对')
    }
    if (this.slot.running || this.slot.status === 'saving') throw new DraftStageBusyError('本机草稿正在保存')
    if (this.slot.uncertain || this.slot.status !== 'saved_locally' || this.slot.latest !== this.slot.base.value) {
      throw new DraftStageStorageError('本机草稿尚未确认')
    }
    const rebased = rebaseAcknowledgedRecordingV2(draft, snapshot.detail)
    if (!rebased) throw new Error('训练内容需要核对')
    const { syncPending: _pending, ...next } = rebased
    const checked = restoreDraft(next, todayInTimezone(next.timeZone, this.now()))
    const encoded = envelope(this.subjectId, checked)
    try { this.storage.prepareVersionedWrite(this.key, encoded) }
    catch { throw new DraftStageStorageError('本机草稿无法保存') }
    this.slot.latest = encoded
    this.slot.sequence++
    return this.persist()
  }

  /** Save explicit conflict choices against a verified snapshot without dispatching a write. */
  async resolveRecordingV2Conflict(snapshot: ScopedRecordingV2Detail, expectedDraft: string,
    choices: Record<string, ConflictChoice>, api: RecordingApi, auth: RecordingAuth): Promise<boolean> {
    this.assertCurrent()
    const draft = this.draft
    if (typeof expectedDraft !== 'string' || JSON.stringify(draft) !== expectedDraft || draft.syncPending !== undefined ||
        !(api instanceof RecordingApi) || !RecordingApi.prototype.isBoundToAuth.call(api, auth, {subjectId:this.subjectId,generation:this.generation}) ||
        !isIssuedRecordingV2Snapshot(snapshot, api) || snapshot.lease.subjectId !== this.subjectId ||
        snapshot.lease.generation !== this.generation || !auth.isCurrent(this.subjectId, this.generation)) {
      throw new Error('训练版本已变化，需要重新核对')
    }
    if (this.slot.running || this.slot.status === 'saving') throw new DraftStageBusyError('本机草稿正在保存')
    if (this.slot.uncertain || this.slot.status !== 'saved_locally' || this.slot.latest !== this.slot.base.value) {
      throw new DraftStageStorageError('本机草稿尚未确认')
    }
    const resolved = applyRecordingV2ConflictChoices(draft, snapshot.detail, choices)
    if (!resolved) throw new Error('训练差异需要重新核对')
    const checked = restoreDraft(resolved, todayInTimezone(resolved.timeZone, this.now()))
    const encoded = envelope(this.subjectId, checked)
    try { this.storage.prepareVersionedWrite(this.key, encoded) }
    catch { throw new DraftStageStorageError('本机草稿无法保存') }
    this.slot.latest = encoded
    this.slot.sequence++
    return this.persist()
  }

  /** Upgrade only an exactly synchronized snapshot to tracked schema 4. */
  async markRecordingV2Tracking(snapshot: ScopedRecordingV2Detail, api: RecordingApi, auth: RecordingAuth): Promise<boolean> {
    this.assertCurrent()
    const draft = this.draft
    if (draft.syncPending !== undefined || !(api instanceof RecordingApi) ||
        !RecordingApi.prototype.isBoundToAuth.call(api, auth, {subjectId:this.subjectId,generation:this.generation}) ||
        !isIssuedRecordingV2Snapshot(snapshot, api) || snapshot.lease.subjectId !== this.subjectId ||
        snapshot.lease.generation !== this.generation || !auth.isCurrent(this.subjectId,this.generation)) throw new Error('同步基线不可用')
    if (this.slot.running || this.slot.status === 'saving') throw new DraftStageBusyError('本机草稿正在保存')
    if (this.slot.uncertain || this.slot.status !== 'saved_locally' || this.slot.latest !== this.slot.base.value) {
      throw new DraftStageStorageError('本机草稿尚未确认')
    }
    const prepared = prepareRecordingV2Batch(draft, snapshot.detail)
    if (!prepared.ok || prepared.changed || Boolean(draft.completionRequested) !== (snapshot.detail.status === 'completed')) {
      throw new Error('同步基线需要重新核对')
    }
    const rebased = rebaseAcknowledgedRecordingV2(draft, snapshot.detail)
    if (!rebased) throw new Error('同步基线需要重新核对')
    const checked = restoreDraft({...rebased,syncTracking:1}, todayInTimezone(draft.timeZone,this.now()))
    const encoded = envelope(this.subjectId, checked)
    try { this.storage.prepareVersionedWrite(this.key,encoded) }
    catch { throw new DraftStageStorageError('本机草稿无法保存') }
    this.slot.latest = encoded;this.slot.sequence++
    return this.persist()
  }

  /** Adopt an authenticated cloud superset only if no local changes can be lost. */
  async adoptCloudRecordingV2(snapshot: ScopedRecordingV2Detail, expectedDraft: string,
    api: RecordingApi, auth: RecordingAuth): Promise<boolean> {
    this.assertCurrent()
    const draft = this.draft
    if (JSON.stringify(draft) !== expectedDraft || !(api instanceof RecordingApi) ||
        !RecordingApi.prototype.isBoundToAuth.call(api, auth, {subjectId:this.subjectId,generation:this.generation}) ||
        !isIssuedRecordingV2Snapshot(snapshot,api) || snapshot.lease.subjectId !== this.subjectId ||
        snapshot.lease.generation !== this.generation || !auth.isCurrent(this.subjectId,this.generation)) throw new Error('训练版本已变化')
    if (this.slot.running || this.slot.status === 'saving') throw new DraftStageBusyError('本机草稿正在保存')
    if (this.slot.uncertain || this.slot.status !== 'saved_locally' || this.slot.latest !== this.slot.base.value) {
      throw new DraftStageStorageError('本机草稿尚未确认')
    }
    const adopted = adoptCloudRecordingV2(draft,snapshot.detail)
    if (!adopted) throw new Error('训练内容需要重新核对')
    const checked = restoreDraft(adopted,todayInTimezone(adopted.timeZone,this.now()))
    const encoded = envelope(this.subjectId,checked)
    try { this.storage.prepareVersionedWrite(this.key,encoded) }
    catch { throw new DraftStageStorageError('本机草稿无法保存') }
    this.slot.latest = encoded;this.slot.sequence++
    return this.persist()
  }

  /** Persist a create intent only. This method neither claims nor sends it. */
  async stageFreeSession(request: PreparedFreeSessionRequest): Promise<{ saved: boolean; changed: boolean }> {
    this.assertCurrent()
    const draft = this.draft
    let marker: string
    try {
      if (!isPreparedFreeSessionFor(request, this.subjectId, this.generation) ||
        Object.keys(request).sort().join(',') !== 'body,idempotencyKey,method,path') throw new Error('invalid')
      const { method, path, idempotencyKey, body } = request
      if (method !== 'POST' || path !== '/me/sessions' || typeof idempotencyKey !== 'string' ||
        !/^[A-Za-z0-9._~-]{8,128}$/.test(idempotencyKey) || typeof body !== 'string' ||
        body !== JSON.stringify({ source: 'free', date: draft.date, timeZone: draft.timeZone,
          clientEventId: draft.clientEventId }) || draft.server) throw new Error('invalid')
      marker = freeSessionMarker(this.subjectId, this.eventId, request)
    } catch { throw new Error('待发训练请求无效') }
    const pending = pendingMarker(draft)
    if (pending.present) {
      if (pending.value !== marker) throw new Error('已有待核对的训练请求')
      const saved = await this.persist()
      return { saved: saved && isPreparedFreeSessionFor(request, this.subjectId, this.generation), changed: false }
    }
    if (this.slot.running || this.slot.status === 'saving') throw new DraftStageBusyError('本机草稿正在保存')
    if (this.slot.uncertain || this.slot.status !== 'saved_locally' ||
      this.slot.latest !== this.slot.base.value) throw new DraftStageStorageError('本机草稿尚未确认')
    const staged = envelope(this.subjectId, { ...draft, syncPending: marker })
    // Validate the full escaped envelope before changing the live draft.
    // A deterministic size/format error must not install an unsaveable marker.
    try { this.storage.prepareVersionedWrite(this.key, staged) }
    catch { throw new Error('本机待发请求无法保存') }
    this.slot.latest = staged
    this.slot.sequence++
    const saved = await this.persist()
    return { saved: saved && isPreparedFreeSessionFor(request, this.subjectId, this.generation), changed: true }
  }

  /** Atomically bind a verified pristine create response and clear its intent. */
  async acknowledgeFreeSession(request: PreparedFreeSessionRequest, response: ScopedSessionDetail): Promise<boolean> {
    this.assertCurrent()
    const current = this.draft
    const pending = pendingFreeSession(current, this.subjectId)
    const detail = response?.detail
    if (!pending || current.syncPending !== freeSessionMarker(this.subjectId, this.eventId, request) ||
      !acknowledgesFreeSession(response, request) || response.lease.subjectId !== this.subjectId ||
      response.lease.generation !== this.generation || !detail || detail.clientEventId !== this.eventId ||
      detail.date !== current.date || detail.timeZone !== current.timeZone || detail.source !== 'free' ||
      detail.status !== 'in_progress' || detail.version !== 1 || detail.recordedAt !== null ||
      detail.sets.length !== 0 || detail.exerciseEntryKeys.length !== 0 ||
      [detail.durationMin, detail.rpe, detail.note].some(value => value !== null && value !== undefined)) {
      throw new Error('训练创建结果需要核对')
    }
    if (this.slot.running || this.slot.status === 'saving') throw new DraftStageBusyError('本机草稿正在保存')
    if (this.slot.uncertain || this.slot.status !== 'saved_locally' || this.slot.latest !== this.slot.base.value) {
      throw new DraftStageStorageError('本机草稿尚未确认')
    }
    const { syncPending: _pending, ...rest } = current
    const proposed = { ...rest, server: { id: detail.id, version: detail.version, status: detail.status } }
    const checked = restoreDraft(proposed, todayInTimezone(proposed.timeZone, this.now()))
    const value = envelope(this.subjectId, checked)
    try { this.storage.prepareVersionedWrite(this.key, value) }
    catch { throw new DraftStageStorageError('本机草稿无法保存') }
    // Both changes enter the same versioned slot; later local input remains.
    this.slot.latest = value
    this.slot.sequence++
    return this.persist()
  }

  /** Stage one validated set request before any network call. */
  async stageSetsRequest(request: PreparedSetsRequest, prepared: OrderedPreparation, baseline: ScopedSessionDetail,
    api: RecordingApi, auth: RecordingAuth): Promise<boolean> {
    this.assertCurrent()
    const draft = this.draft
    const today = todayInTimezone(draft.timeZone, this.now())
    const submitted = captureOrderedInputs(prepared, draft, baseline, auth, today)
    if (!submitted || !prepared.ok || prepared.state !== 'send' ||
      !isPreparedSetsFor(request, this.subjectId, this.generation, api) || !draft.server ||
      request.path !== `/me/sessions/${draft.server.id}/sets` || prepared.expectedVersion !== draft.server.version ||
      request.body !== encodePendingSetsBody(prepared.expectedVersion, prepared.exerciseEntryKeys, prepared.sets)) {
      throw new Error('待发训练组请求无效')
    }
    const marker = setsMarker(this.subjectId, this.eventId, request, submitted)
    const pending = pendingMarker(draft)
    if (pending.present) {
      if (pending.value !== marker) throw new Error('已有待核对的训练请求')
      return this.persist()
    }
    if (this.slot.running || this.slot.status === 'saving') throw new DraftStageBusyError('本机草稿正在保存')
    if (this.slot.uncertain || this.slot.status !== 'saved_locally' || this.slot.latest !== this.slot.base.value) {
      throw new DraftStageStorageError('本机草稿尚未确认')
    }
    const staged = envelope(this.subjectId, { ...draft, syncPending: marker })
    try { this.storage.prepareVersionedWrite(this.key, staged) }
    catch { throw new DraftStageStorageError('本机待发请求无法保存') }
    this.slot.latest = staged
    this.slot.sequence++
    const saved = await this.persist()
    return saved && isPreparedSetsFor(request, this.subjectId, this.generation, api)
  }

  /** Persist a request-bound set receipt and clear its intent in one CAS. */
  async acknowledgeSets(response: ScopedSessionDetail, auth: RecordingAuth): Promise<boolean> {
    this.assertCurrent()
    const current = this.draft
    const result = applyPendingSetsAcknowledgement(current, response, auth, todayInTimezone(current.timeZone, this.now()))
    if (!result.ok) throw new Error(result.message)
    if (this.slot.running || this.slot.status === 'saving') throw new DraftStageBusyError('本机草稿正在保存')
    if (this.slot.uncertain || this.slot.status !== 'saved_locally' || this.slot.latest !== this.slot.base.value) {
      throw new DraftStageStorageError('本机草稿尚未确认')
    }
    const value = envelope(this.subjectId, result.draft)
    try { this.storage.prepareVersionedWrite(this.key, value) }
    catch { throw new DraftStageStorageError('本机草稿无法保存') }
    this.slot.latest = value
    this.slot.sequence++
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
