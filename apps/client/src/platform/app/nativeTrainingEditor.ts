import { generateSecureRandomRequestKey } from '@/uni_modules/nerae-random'
import type { AuthSessionCoordinator } from '../../features/auth/sessionCoordinator'
import { TrainingEditorController, type AttachTrainingEditorResult, type CreateTrainingEditorResult } from '../../features/training/trainingEditorController'
import { createNativeTrainingDraftStorage } from './nativeTrainingDraftStorage'

const ids = {
  nextId(): string {
    const value = generateSecureRandomRequestKey()
    if (!value || value.ok !== true || value.errorCode !== '') throw new Error('secure randomness unavailable')
    return value.value
  },
}

export type DiscoverAppTrainingDraftsResult =
  | { ok: true; eventIds: string[]; nextCursor: string | null; hasUnclassifiedItems: boolean }
  | { ok: false; reason: 'session_changed' | 'storage_unavailable' }

/** Find the current subject's protected local drafts; no server sync is implied. */
export async function discoverAppTrainingDrafts(session: AuthSessionCoordinator, cursor = ''): Promise<DiscoverAppTrainingDraftsResult> {
  const snapshot = session?.snapshot()
  const lease = session?.draftLease()
  if (!snapshot || !lease || snapshot.generation !== lease.generation || !lease.isCurrent(snapshot.subjectId, lease.generation)) {
    return { ok: false, reason: 'session_changed' }
  }
  try {
    const result = await createNativeTrainingDraftStorage(snapshot.subjectId, lease).discover(cursor)
    if (!lease.isCurrent(snapshot.subjectId, lease.generation)) return { ok: false, reason: 'session_changed' }
    return { ok: true, ...result }
  } catch {
    return { ok: false, reason: lease.isCurrent(snapshot.subjectId, lease.generation) ? 'storage_unavailable' : 'session_changed' }
  }
}

/** Local draft creation only; it does not claim a server write or sync. */
export async function createAppTrainingEditor(session: AuthSessionCoordinator, date: string, timeZone: string): Promise<CreateTrainingEditorResult> {
  const snapshot = session?.snapshot()
  const lease = session?.draftLease()
  if (!snapshot || !lease || snapshot.generation !== lease.generation || !lease.isCurrent(snapshot.subjectId, lease.generation)) {
    return { ok: false, reason: 'session_changed' }
  }
  try {
    return await TrainingEditorController.create({ subjectId: snapshot.subjectId, date, timeZone, auth: lease,
      storage: createNativeTrainingDraftStorage(snapshot.subjectId, lease), ids, now: () => new Date() })
  } catch { return { ok: false, reason: 'storage_unavailable' } }
}

/** Reopen a known local draft after the same subject has reauthenticated. */
export async function attachAppTrainingEditor(session: AuthSessionCoordinator, eventId: string): Promise<AttachTrainingEditorResult> {
  const snapshot = session?.snapshot()
  const lease = session?.draftLease()
  if (!snapshot || !lease || snapshot.generation !== lease.generation || !lease.isCurrent(snapshot.subjectId, lease.generation)) {
    return { ok: false, reason: 'session_changed' }
  }
  try {
    return await TrainingEditorController.attach(snapshot.subjectId, eventId,
      createNativeTrainingDraftStorage(snapshot.subjectId, lease), () => new Date(), lease, ids)
  } catch { return { ok: false, reason: 'storage_unavailable' } }
}
