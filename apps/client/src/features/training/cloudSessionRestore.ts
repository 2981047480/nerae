import type { AuthLease, DraftStorage } from './draftStorage'
import { todayInTimezone } from './draftStorage'
import { TrainingEditorController, type InitialDraftStorage, type SecureRecordingIdProvider } from './trainingEditorController'
import { RecordingApi, type RecordingAuth, type RecordingLease } from './recordingApi'
import { restoreConfirmedSession } from './recordingProjection'

export type CloudSessionRestoreResult =
  | { ok: true; controller: TrainingEditorController }
  | { ok: false; reason: 'invalid_input' | 'session_changed' | 'read_failed' | 'unsupported' | 'invalid_snapshot' |
      'reconcile_required' | 'local_unsynced' | 'storage_unavailable' | 'local_exists' | 'outcome_unknown' | 'attach_failed' }

export interface CloudSessionRestoreOptions {
  sessionId: string
  lease: RecordingLease
  auth: RecordingAuth
  draftLease: AuthLease
  api: RecordingApi
  storage: DraftStorage & { readonly versioned?: boolean; createIfAbsent?: (key: string, value: string) => Promise<'created' | 'exists'> }
  ids: SecureRecordingIdProvider
  now: () => Date
}

function current(auth: RecordingAuth, draftLease: AuthLease, lease: RecordingLease, accessToken: string): boolean {
  try {
    const credential = auth.current()
    return draftLease.generation === lease.generation && !!credential && credential.subjectId === lease.subjectId &&
      credential.generation === lease.generation && credential.accessToken === accessToken &&
      auth.isCurrent(lease.subjectId, lease.generation) &&
      draftLease.isCurrent(lease.subjectId, lease.generation)
  } catch { return false }
}

/** Restore one authenticated API snapshot into an empty versioned local slot. */
export async function restoreCloudSessionToLocalDraft(options: CloudSessionRestoreOptions): Promise<CloudSessionRestoreResult> {
  const { sessionId, lease, auth, draftLease, api, storage, ids, now } = options
  if (!/^[1-9]\d{0,19}$/.test(sessionId) || !/^[1-9]\d{0,19}$/.test(lease?.subjectId || '') ||
    typeof lease?.generation !== 'string' || !lease.generation || typeof draftLease?.generation !== 'string' ||
    typeof draftLease?.isCurrent !== 'function' || typeof now !== 'function' ||
    typeof ids?.nextId !== 'function') return { ok: false, reason: 'invalid_input' }
  let accessToken: string
  try {
    const credential = auth.current()
    if (!credential || typeof credential.accessToken !== 'string' || !credential.accessToken) return { ok: false, reason: 'session_changed' }
    accessToken = credential.accessToken
  } catch { return { ok: false, reason: 'session_changed' } }
  const isCurrent = () => current(auth, draftLease, lease, accessToken)
  if (!isCurrent()) return { ok: false, reason: 'session_changed' }
  if (!(api instanceof RecordingApi)) return { ok: false, reason: 'invalid_input' }
  if (!RecordingApi.prototype.isBoundToAuth.call(api, auth, lease)) return { ok: false, reason: 'session_changed' }
  if (storage?.versioned !== true || typeof storage.createIfAbsent !== 'function' ||
    typeof storage.readVersioned !== 'function' || typeof storage.prepareVersionedWrite !== 'function') {
    return { ok: false, reason: 'storage_unavailable' }
  }

  let snapshot
  try { snapshot = await RecordingApi.prototype.readSession.call(api, sessionId) }
  catch { return { ok: false, reason: isCurrent() ? 'read_failed' : 'session_changed' } }
  if (!isCurrent()) return { ok: false, reason: 'session_changed' }
  if (!snapshot || typeof snapshot !== 'object' || !snapshot.lease ||
    snapshot.lease.subjectId !== lease.subjectId || snapshot.lease.generation !== lease.generation) {
    return { ok: false, reason: 'invalid_snapshot' }
  }
  if (!snapshot.detail || typeof snapshot.detail !== 'object' || typeof snapshot.detail.timeZone !== 'string') {
    return { ok: false, reason: 'invalid_snapshot' }
  }
  let today: string
  try { today = todayInTimezone(snapshot.detail.timeZone, now()) }
  catch { return { ok: false, reason: 'invalid_snapshot' } }
  const projected = restoreConfirmedSession(snapshot, auth, today)
  if (!projected.ok) return { ok: false, reason: projected.reason }
  if (!isCurrent()) return { ok: false, reason: 'session_changed' }
  // Tie editor operations to the exact credential used for the read, even
  // when the host separates the auth and draft lease interfaces.
  const boundLease: AuthLease = { generation: lease.generation,
    isCurrent: (subject, generation) => subject === lease.subjectId && generation === lease.generation && isCurrent() }
  const attached = await TrainingEditorController.restoreIntoEmptySlot(lease.subjectId, projected.draft,
    storage as InitialDraftStorage, now, boundLease, ids)
  if (!isCurrent()) {
    if (attached.ok) {
      try { await attached.controller.close() } catch { /* fail closed even if cleanup reports an error */ }
    }
    return { ok: false, reason: 'session_changed' }
  }
  if (!attached.ok) return { ok: false, reason: attached.reason }
  return { ok: true, controller: attached.controller }
}
