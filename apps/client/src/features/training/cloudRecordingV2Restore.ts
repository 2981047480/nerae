import { todayInTimezone, type AuthLease, type DraftStorage } from './draftStorage'
import { RecordingApi, type RecordingAuth, type RecordingLease } from './recordingApi'
import { projectRecordingV2ToDraft } from './recordingV2Restore'
import { TrainingEditorController, type InitialDraftStorage, type SecureRecordingIdProvider } from './trainingEditorController'

export type CloudRecordingV2RestoreResult =
  | { ok: true; controller: TrainingEditorController }
  | { ok: false; reason: 'invalid_input' | 'session_changed' | 'read_failed' | 'unsupported' | 'invalid_snapshot' |
      'local_unsynced' | 'storage_unavailable' | 'local_exists' | 'outcome_unknown' }

export interface CloudRecordingV2RestoreOptions {
  sessionId: string
  lease: RecordingLease
  auth: RecordingAuth
  draftLease: AuthLease
  api: RecordingApi
  storage: DraftStorage & { readonly versioned?: boolean; createIfAbsent?: (key: string, value: string) => Promise<'created' | 'exists'> }
  ids: SecureRecordingIdProvider
  now: () => Date
}

/** Restore a fresh authenticated cloud snapshot only into an empty native slot. */
export async function restoreCloudRecordingV2ToLocalDraft(options: CloudRecordingV2RestoreOptions): Promise<CloudRecordingV2RestoreResult> {
  const { sessionId, lease, auth, draftLease, api, storage, ids, now } = options
  if (!/^[1-9]\d{0,19}$/.test(sessionId) || !/^[1-9]\d{0,19}$/.test(lease?.subjectId || '') ||
      typeof lease?.generation !== 'string' || !lease.generation || typeof draftLease?.isCurrent !== 'function' ||
      typeof ids?.nextId !== 'function' || typeof now !== 'function' || !(api instanceof RecordingApi)) return { ok: false, reason: 'invalid_input' }
  let token: string
  try {
    const credential = auth.current()
    if (!credential || typeof credential.accessToken !== 'string' || !credential.accessToken) return { ok: false, reason: 'session_changed' }
    token = credential.accessToken
  } catch { return { ok: false, reason: 'session_changed' } }
  const isCurrent = (): boolean => {
    try {
      const credential = auth.current()
      return Boolean(credential && credential.subjectId === lease.subjectId && credential.generation === lease.generation &&
        credential.accessToken === token && auth.isCurrent(lease.subjectId, lease.generation) &&
        draftLease.generation === lease.generation && draftLease.isCurrent(lease.subjectId, lease.generation))
    } catch { return false }
  }
  if (!isCurrent() || !RecordingApi.prototype.isBoundToAuth.call(api, auth, lease)) return { ok: false, reason: 'session_changed' }
  if (storage?.versioned !== true || typeof storage.createIfAbsent !== 'function' ||
      typeof storage.readVersioned !== 'function' || typeof storage.prepareVersionedWrite !== 'function') return { ok: false, reason: 'storage_unavailable' }
  let snapshot
  try { snapshot = await RecordingApi.prototype.readRecordingV2.call(api, sessionId) }
  catch { return { ok: false, reason: isCurrent() ? 'read_failed' : 'session_changed' } }
  if (!isCurrent()) return { ok: false, reason: 'session_changed' }
  let today: string
  try { today = todayInTimezone(snapshot.detail.timeZone, now()) } catch { return { ok: false, reason: 'invalid_snapshot' } }
  const projected = projectRecordingV2ToDraft(snapshot, auth, today)
  if (!projected.ok) return { ok: false, reason: projected.reason }
  const boundLease: AuthLease = { generation: lease.generation,
    isCurrent: (subject, generation) => subject === lease.subjectId && generation === lease.generation && isCurrent() }
  const attached = await TrainingEditorController.restoreIntoEmptySlot(lease.subjectId, projected.draft,
    storage as InitialDraftStorage, now, boundLease, ids)
  if (!isCurrent()) {
    if (attached.ok) { try { await attached.controller.close() } catch { /* preserve persisted slot */ } }
    return { ok: false, reason: 'session_changed' }
  }
  if (!attached.ok) return { ok: false, reason: attached.reason }
  return { ok: true, controller: attached.controller }
}
