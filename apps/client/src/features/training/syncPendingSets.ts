import { todayInTimezone } from './draftStorage'
import type { GroupIssue } from './recording'
import { RecordingApi, RecordingApiError, type RecordingAuth } from './recordingApi'
import { prepareOrderedAfterWorkout, prepareOrderedConfirmedSet } from './recordingProjection'
import { pendingSets } from './setsIntent'
import { TrainingEditorController, type EditorResult } from './trainingEditorController'

export type SetsSelection =
  | { readonly mode: 'after_workout' }
  | { readonly mode: 'per_set'; readonly exerciseEntryKey: string; readonly recordKey: string }

export type SyncSetsResult =
  | { readonly state: 'saved' | 'recovered' | 'already_saved' | 'busy' | 'local_error' | 'retry' | 'reconcile_required' | 'session_changed' }
  | { readonly state: 'invalid'; readonly message: string; readonly issues?: GroupIssue[] }

// This prevents duplicate work in one runtime. The persisted original request
// and the server's expectedVersion protect retries across restarts and devices.
const activeEditors = new WeakSet<TrainingEditorController>()

/** Save one explicitly selected batch or recover the draft's exact pending batch. */
export async function syncPendingSets(
  editor: TrainingEditorController, api: RecordingApi, auth: RecordingAuth, selection: SetsSelection,
  nextKey: () => string, now: () => Date = () => new Date(),
): Promise<SyncSetsResult> {
  if (!(editor instanceof TrainingEditorController) || !(api instanceof RecordingApi) ||
    !selection || !['after_workout', 'per_set'].includes(selection.mode) ||
    (selection.mode === 'per_set' && (typeof selection.exerciseEntryKey !== 'string' || typeof selection.recordKey !== 'string')) ||
    typeof nextKey !== 'function' || typeof now !== 'function') return { state: 'reconcile_required' }
  if (activeEditors.has(editor)) return { state: 'busy' }
  activeEditors.add(editor)
  let isCurrent = (): boolean => false
  try {
    const credential = auth.current()
    if (!credential || !credential.accessToken || !auth.isCurrent(credential.subjectId, credential.generation) ||
      !RecordingApi.prototype.isBoundToAuth.call(api, auth, credential)) return { state: 'session_changed' }
    const { subjectId, generation, accessToken } = credential
    isCurrent = () => {
      try {
        const current = auth.current()
        return !!current && current.subjectId === subjectId && current.generation === generation &&
          current.accessToken === accessToken && auth.isCurrent(subjectId, generation)
      } catch { return false }
    }
    const initial = editor.draft
    if (!isCurrent()) return { state: 'session_changed' }
    if (editor.localStatus !== 'saved_locally') return { state: 'local_error' }
    if (!initial.server) return { state: 'reconcile_required' }
    const recovering = Object.prototype.hasOwnProperty.call(initial, 'syncPending')
    const originalPending = recovering ? pendingSets(initial, subjectId) : null
    if (recovering && !originalPending) return { state: 'reconcile_required' }
    const initialMarker = initial.syncPending
    const baseline = await api.readSession(initial.server.id)
    if (!isCurrent()) return { state: 'session_changed' }
    const latest = editor.draft
    if (!latest.server || latest.server.id !== initial.server.id || latest.clientEventId !== initial.clientEventId ||
      latest.syncPending !== initialMarker || editor.localStatus !== 'saved_locally') return { state: 'local_error' }

    if (!recovering) {
      if (latest.server.version !== baseline.detail.version || latest.mode !== selection.mode) return { state: 'reconcile_required' }
      const today = todayInTimezone(latest.timeZone, now())
      const lease = { subjectId, generation }
      const prepared = selection.mode === 'after_workout'
        ? prepareOrderedAfterWorkout(latest, baseline, lease, auth, today)
        : prepareOrderedConfirmedSet(latest, baseline, lease, auth, selection.exerciseEntryKey, selection.recordKey, today)
      if (!prepared.ok) return prepared.reason === 'invalid_draft'
        ? { state: 'invalid', message: prepared.message, ...(prepared.issues ? { issues: prepared.issues } : {}) }
        : { state: prepared.reason }
      if (prepared.state === 'already_saved') return { state: 'already_saved' }
      const request = api.preparePendingSetsRequest(prepared.sessionId, { expectedVersion: prepared.expectedVersion,
        exerciseEntryKeys: prepared.exerciseEntryKeys, sets: prepared.sets }, nextKey(), baseline)
      const staged = await editor.stageSetsRequest(request, prepared, baseline, api, auth)
      if (!isCurrent()) return { state: 'session_changed' }
      if (!staged.ok) return editorFailure(staged)
    }
    const staged = editor.draft
    const pending = pendingSets(staged, subjectId)
    const marker = staged.syncPending
    if (!pending || typeof marker !== 'string' || editor.localStatus !== 'saved_locally') return { state: 'local_error' }
    if (recovering && marker !== initialMarker) return { state: 'reconcile_required' }
    const exact = (): boolean => {
      try {
        const current = editor.draft
        return current.syncPending === marker && pendingSets(current, subjectId) !== null &&
          editor.localStatus === 'saved_locally'
      } catch { return false }
    }
    if (!isCurrent()) return { state: 'session_changed' }
    if (!exact()) return { state: 'local_error' }
    const response = baseline.detail.version === pending.expectedVersion
      ? await api.sendPendingSets(staged, baseline, () => isCurrent() && exact())
      : await api.replayDraftSets(staged)
    if (!isCurrent()) return { state: 'session_changed' }
    if (!response) return { state: 'reconcile_required' }
    if (!exact()) return { state: 'local_error' }
    const applied = await editor.acknowledgeSets(response, auth)
    if (!isCurrent()) return { state: 'session_changed' }
    if (!applied.ok) return editorFailure(applied)
    // A recovered batch may be different from the caller's latest selection.
    return { state: recovering ? 'recovered' : 'saved' }
  } catch (error) {
    if (!isCurrent() || (error instanceof RecordingApiError &&
      (error.code === 'unauthenticated' || error.code === 'session_changed'))) return { state: 'session_changed' }
    if (error instanceof RecordingApiError &&
      ['conflict', 'version_conflict', 'invalid_request', 'invalid_response', 'forbidden', 'not_found'].includes(error.code)) {
      return { state: 'reconcile_required' }
    }
    return { state: 'retry' }
  } finally { activeEditors.delete(editor) }
}

function editorFailure(result: EditorResult): SyncSetsResult {
  if (result.ok) return { state: 'reconcile_required' }
  if (result.reason === 'session_changed' || result.reason === 'closed') return { state: 'session_changed' }
  if (result.reason === 'storage_error' || result.reason === 'pending' || result.reason === 'superseded') return { state: 'local_error' }
  return { state: 'reconcile_required' }
}
