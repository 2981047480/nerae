import { freeSessionMarker, pendingFreeSession } from './freeSessionIntent'
import { RecordingApi, RecordingApiError, type RecordingAuth, type PreparedFreeSessionRequest } from './recordingApi'
import { TrainingEditorController } from './trainingEditorController'

export type FreeSessionCreateResult =
  | { readonly state: 'created' | 'existing' | 'busy' | 'local_error' | 'retry' | 'reconcile_required' | 'session_changed' }

// This prevents duplicate work in one runtime. Server-side event identity is
// still responsible for safe retry after a process restart or lost response.
const activeEditors = new WeakSet<TrainingEditorController>()

/** Persist and retry the exact free-session create request for one draft. */
export async function syncFreeSessionCreate(
  editor: TrainingEditorController, api: RecordingApi, auth: RecordingAuth, nextKey: () => string,
): Promise<FreeSessionCreateResult> {
  if (!(editor instanceof TrainingEditorController) || !(api instanceof RecordingApi) || typeof nextKey !== 'function') {
    return { state: 'reconcile_required' }
  }
  if (activeEditors.has(editor)) return { state: 'busy' }
  activeEditors.add(editor)
  try {
    const credential = auth.current()
    if (!credential || !credential.accessToken || !auth.isCurrent(credential.subjectId, credential.generation) ||
      !RecordingApi.prototype.isBoundToAuth.call(api, auth, credential)) return { state: 'session_changed' }
    const { subjectId, generation, accessToken } = credential
    const isCurrent = (): boolean => {
      try {
        const current = auth.current()
        return !!current && current.subjectId === subjectId && current.generation === generation &&
          current.accessToken === accessToken && auth.isCurrent(subjectId, generation)
      } catch { return false }
    }
    const initial = editor.draft
    if (!isCurrent()) return { state: 'session_changed' }
    if (editor.localStatus !== 'saved_locally') return { state: 'local_error' }
    if (initial.server) return { state: Object.hasOwn(initial, 'syncPending') ? 'reconcile_required' : 'existing' }

    const hasPending = Object.hasOwn(initial, 'syncPending')
    const pending = hasPending ? pendingFreeSession(initial, subjectId) : null
    if (hasPending && !pending) return { state: 'reconcile_required' }
    let request: PreparedFreeSessionRequest
    try {
      const key = pending ? pending.idempotencyKey : nextKey()
      request = api.prepareFreeSessionRequest({ date: initial.date, timeZone: initial.timeZone,
        clientEventId: initial.clientEventId }, key)
    } catch { return { state: isCurrent() ? 'local_error' : 'session_changed' } }
    if (pending && (request.body !== pending.body || request.idempotencyKey !== pending.idempotencyKey)) {
      return { state: 'reconcile_required' }
    }
    const staged = await editor.stageFreeSession(request)
    if (!isCurrent()) return { state: 'session_changed' }
    if (!staged.ok) return { state: staged.reason === 'storage_error' || staged.reason === 'pending' ? 'local_error' : 'reconcile_required' }
    const exact = () => {
      try {
        const draft = editor.draft
        return !draft.server && draft.syncPending === freeSessionMarker(subjectId, draft.clientEventId, request) &&
          pendingFreeSession(draft, subjectId) !== null
      } catch { return false }
    }
    if (!exact() || !isCurrent()) return { state: isCurrent() ? 'reconcile_required' : 'session_changed' }
    let response
    try { response = await api.sendPreparedFreeSessionRequest(request, () => isCurrent() && exact()) }
    catch (error) {
      if (!isCurrent() || (error instanceof RecordingApiError && error.code === 'unauthenticated')) {
        return { state: 'session_changed' }
      }
      if (error instanceof RecordingApiError && ['conflict', 'version_conflict', 'invalid_request', 'forbidden', 'not_found'].includes(error.code)) {
        return { state: 'reconcile_required' }
      }
      return { state: 'retry' }
    }
    if (!isCurrent()) return { state: 'session_changed' }
    const applied = await editor.acknowledgeFreeSession(request, response)
    if (!isCurrent()) return { state: 'session_changed' }
    if (!applied.ok) return { state: applied.reason === 'storage_error' || applied.reason === 'pending' ? 'local_error' : 'reconcile_required' }
    return { state: 'created' }
  } catch { return { state: 'session_changed' } }
  finally { activeEditors.delete(editor) }
}
