import { RecordingApi, RecordingApiError, type RecordingAuth, type ScopedRecordingV2Detail, type ScopedRecordingV2Receipt } from './recordingApi'
import { pendingRecordingV2 } from './recordingV2Intent'
import { prepareRecordingV2Batch } from './recordingV2Projection'
import type { JournalRequest } from './operationJournal'
import type { TrainingEditorController } from './trainingEditorController'
import { reviewRecordingV2Conflict, resolveRecordingV2Conflict } from './recordingV2Conflict'
import { adoptCloudRecordingV2 } from './recordingV2CloudAdoption'

export type RecordingV2SyncState = 'synced' | 'pending' | 'conflict' | 'unsupported' | 'invalid' | 'session_changed' | 'local_error' | 'busy'
export interface RecordingV2SyncResult { state: RecordingV2SyncState; snapshot?: ScopedRecordingV2Detail }

const active = new WeakSet<TrainingEditorController>()

/**
 * Reconcile one protected draft in order. A stored request is immutable until a
 * request-bound receipt and a fresh matching snapshot have both been verified.
 */
export async function syncRecordingV2(editor: TrainingEditorController, api: RecordingApi,
  auth: RecordingAuth, nextKey: () => string): Promise<RecordingV2SyncResult> {
  if (active.has(editor)) return { state: 'busy' }
  active.add(editor)
  try {
    const credential = auth.current()
    if (!credential || !api.isBoundToAuth(auth, credential)) return { state: 'session_changed' }
    const lease = { subjectId: credential.subjectId, generation: credential.generation, accessToken: credential.accessToken }
    const current = (): boolean => {
      try {
        const now = auth.current()
        return Boolean(now && now.subjectId === lease.subjectId && now.generation === lease.generation &&
          now.accessToken === lease.accessToken && auth.isCurrent(lease.subjectId, lease.generation))
      } catch { return false }
    }
    const applyLocalResult = (result: { ok: boolean; reason?: string }): RecordingV2SyncResult => {
      if (result.reason === 'session_changed' || !current()) return { state: 'session_changed' }
      if (result.reason === 'storage_error') return { state: 'local_error' }
      if (result.reason === 'invalid_input') return { state: 'conflict' }
      return { state: 'pending' }
    }
    for (let step = 0; step < 24; step++) {
      if (!current()) return { state: 'session_changed' }
      if (editor.localStatus !== 'saved_locally') return { state: editor.localStatus === 'storage_error' ? 'local_error' : 'pending' }
      const draft = editor.draft
      if (draft.recordFormat !== 2) return { state: 'unsupported' }
      if (draft.syncPending !== undefined) {
        const pending = pendingRecordingV2(draft, lease.subjectId)
        if (!pending) return { state: 'conflict' }
        const marker = draft.syncPending
        const guard = (): boolean => {
          try { return current() && editor.localStatus === 'saved_locally' && editor.draft.syncPending === marker }
          catch { return false }
        }
        const replay = await api.replayStoredRecordingV2(draft, guard)
        if (!guard()) return { state: current() ? 'pending' : 'session_changed' }
        let response: ScopedRecordingV2Receipt
        if (replay.state === 'candidate') response = replay.response
        else {
          // A missing receipt alone does not authorize a write over a newer
          // server version. Keep the original local intent for review instead.
          if (draft.server) {
            const remote = await api.readRecordingV2(draft.server.id)
            if (!guard()) return { state: current() ? 'pending' : 'session_changed' }
            if (remote.detail.clientEventId !== draft.clientEventId || remote.detail.version !== draft.server.version) return { state: 'conflict' }
          }
          response = await api.sendStoredRecordingV2(editor.draft, guard)
        }
        if (!guard()) return { state: current() ? 'pending' : 'session_changed' }
        const remote = await api.readRecordingV2(response.receipt.id)
        if (!guard()) return { state: current() ? 'pending' : 'session_changed' }
        if (remote.detail.version !== response.receipt.version || remote.detail.clientEventId !== draft.clientEventId) return { state: 'conflict' }
        const saved = await editor.acknowledgeRecordingV2(response, remote, api, auth)
        if (!saved.ok) return applyLocalResult(saved)
        continue
      }
      if (!draft.server) {
        const request: JournalRequest = { method: 'POST', path: '/me/recordings', idempotencyKey: nextKey(),
          body: JSON.stringify({ source: 'free', date: draft.date, timeZone: draft.timeZone, clientEventId: draft.clientEventId, mode: draft.mode }) }
        const staged = await editor.stageRecordingV2(request, null, api, auth)
        if (!staged.ok) return applyLocalResult(staged)
        continue
      }
      const remote = await api.readRecordingV2(draft.server.id)
      if (!current()) return { state: 'session_changed' }
      const latest = editor.draft
      if (latest.syncPending !== undefined) continue
      const prepared = prepareRecordingV2Batch(latest, remote.detail)
      if (!prepared.ok) {
        if (prepared.reason === 'invalid_input') return { state: 'invalid' }
        if (prepared.reason === 'unsupported') return { state: 'unsupported' }
        if (prepared.reason === 'version_conflict') {
          const expected = JSON.stringify(latest)
          if (adoptCloudRecordingV2(latest, remote.detail)) {
            const fresh = await api.readRecordingV2(remote.detail.id)
            if (!current()) return { state: 'session_changed' }
            if (JSON.stringify(editor.draft) !== expected || JSON.stringify(fresh.detail) !== JSON.stringify(remote.detail)) return { state: 'conflict' }
            const adopted = await editor.adoptCloudRecordingV2(fresh,expected,api,auth)
            if (!adopted.ok) return applyLocalResult(adopted)
            continue
          }
          // Pull another device's changes only if the protected local groups
          // are acknowledged and unchanged, with no metadata or shape conflict.
          const review = await reviewRecordingV2Conflict(editor, api, auth)
          if (!current()) return { state: 'session_changed' }
          if (review.ok && review.review.canAdoptCloud) {
            const choices = Object.fromEntries(review.review.rows.map(row => [row.key,'cloud' as const]))
            const result = await resolveRecordingV2Conflict(editor, api, auth, review.review, choices)
            if (result.ok) continue
            if (result.reason === 'session_changed') return { state: 'session_changed' }
            if (result.reason === 'local_error') return { state: 'local_error' }
            if (result.reason === 'unavailable') return { state: 'pending' }
          }
        }
        return { state: 'conflict' }
      }
      let request: JournalRequest | undefined
      if (prepared.changed) request = { method: 'POST', path: `/me/recordings/${draft.server.id}/sets`,
        idempotencyKey: nextKey(), body: JSON.stringify(prepared.batch) }
      else if (latest.completionRequested && remote.detail.status !== 'completed') request = {
        method: 'POST', path: `/me/recordings/${draft.server.id}/finish`, idempotencyKey: nextKey(),
        body: JSON.stringify({ expectedVersion: remote.detail.version }),
      }
      else if (!latest.completionRequested && remote.detail.status === 'completed') return { state: 'conflict' }
      else if (latest.syncTracking !== 1) {
        const marked = await editor.markRecordingV2Tracking(remote,api,auth)
        if (!marked.ok) return applyLocalResult(marked)
        continue
      } else return { state: 'synced', snapshot: remote }
      const staged = await editor.stageRecordingV2(request, remote, api, auth)
      if (!staged.ok) return applyLocalResult(staged)
    }
    return { state: 'pending' }
  } catch (error) {
    if (error instanceof RecordingApiError) {
      if (error.code === 'session_changed' || error.code === 'unauthenticated' || error.code === 'forbidden') return { state: 'session_changed' }
      if (error.code === 'not_found' || error.code === 'conflict' || error.code === 'version_conflict') return { state: 'conflict' }
      if (error.code === 'invalid_request') return { state: 'invalid' }
    }
    return { state: 'pending' }
  } finally { active.delete(editor) }
}
