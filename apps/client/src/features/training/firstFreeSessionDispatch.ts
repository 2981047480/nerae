import { OperationJournalError, TrainingOperationJournal, type JournalRequest } from './operationJournal'
import { acknowledgesFreeSession, isPreparedFreeSessionForApi, RecordingApi,
  type PreparedFreeSessionRequest, type RecordingAuth, type ScopedSessionDetail } from './recordingApi'
import type { RecordingDraft } from './recording'
import { TrainingEditorController } from './trainingEditorController'

export type FirstFreeSessionDispatchResult =
  | { readonly state: 'receipt_candidate'; readonly receipt: ScopedSessionDetail }
  | { readonly state: 'retry_preflight'; readonly retry: () => Promise<FirstFreeSessionDispatchResult> }
  | { readonly state: 'retry_in_progress' }
  | { readonly state: 'not_dispatched' | 'reconcile_required' | 'session_changed' }

function sameRequest(first: JournalRequest, second: JournalRequest): boolean {
  return first.method === second.method && first.path === second.path &&
    first.idempotencyKey === second.idempotencyKey && first.body === second.body
}

function hasReadyMarker(draft: RecordingDraft, subjectId: string, request: PreparedFreeSessionRequest): boolean {
  try {
    return !draft.server && request.body === JSON.stringify({ source: 'free', date: draft.date,
      timeZone: draft.timeZone, clientEventId: draft.clientEventId }) &&
      draft.syncPending === JSON.stringify({ schema: 1, subjectId, clientEventId: draft.clientEventId,
        state: 'ready', request: { method: request.method, path: request.path,
          idempotencyKey: request.idempotencyKey, body: request.body } })
  } catch { return false }
}

/**
 * Send the first free-session request only after both protected records and
 * the native first-send claim are confirmed. A successful response remains a
 * candidate: no draft merge, journal settlement or remote-sync claim occurs.
 */
export async function dispatchFirstFreeSession(
  editor: TrainingEditorController, journal: TrainingOperationJournal, api: RecordingApi,
  auth: RecordingAuth, request: PreparedFreeSessionRequest,
): Promise<FirstFreeSessionDispatchResult> {
  let subjectId: string
  let generation: string
  let accessToken: string
  try {
    const credential = auth.current()
    if (!credential) return { state: 'session_changed' }
    subjectId = credential.subjectId
    generation = credential.generation
    accessToken = credential.accessToken
    if (!/^[1-9]\d{0,19}$/.test(subjectId) || !generation || !accessToken ||
      !auth.isCurrent(subjectId, generation)) return { state: 'session_changed' }
  } catch { return { state: 'session_changed' } }
  const isCurrent = (): boolean => {
    try {
      const current = auth.current()
      return Boolean(current && current.subjectId === subjectId && current.generation === generation &&
        current.accessToken === accessToken && auth.isCurrent(subjectId, generation))
    } catch { return false }
  }
  if (!(editor instanceof TrainingEditorController) || !(journal instanceof TrainingOperationJournal) ||
    !(api instanceof RecordingApi) || !isPreparedFreeSessionForApi(request, api, subjectId, generation)) {
    return { state: isCurrent() ? 'not_dispatched' : 'session_changed' }
  }
  let claimAttempted = false
  try {
    const stagedDraft = await editor.stageFreeSession(request)
    if (!isCurrent()) return { state: 'session_changed' }
    if (!stagedDraft.ok) return { state: 'not_dispatched' }
    const before = editor.draft
    if (!hasReadyMarker(before, subjectId, request)) return { state: 'not_dispatched' }
    const eventId = before.clientEventId
    const stagedJournal = await journal.stage(eventId, request)
    if (!isCurrent()) return { state: 'session_changed' }
    if (stagedJournal.subjectId !== subjectId || stagedJournal.clientEventId !== eventId ||
      !sameRequest(stagedJournal.request, request)) return { state: 'not_dispatched' }
    if (stagedJournal.state !== 'ready') return { state: 'reconcile_required' }
    // Verify the protected draft again before creating the native first-send mark.
    const confirmed = await editor.stageFreeSession(request)
    if (!isCurrent()) return { state: 'session_changed' }
    if (!confirmed.ok || !hasReadyMarker(editor.draft, subjectId, request)) return { state: 'not_dispatched' }
    claimAttempted = true
    const permit = await journal.beginSend(eventId)
    let attempting = false
    const attempt = async (): Promise<FirstFreeSessionDispatchResult> => {
      if (attempting) return { state: 'retry_in_progress' }
      attempting = true
      try {
        const receipt = await journal.dispatchFirst(permit, request, async (exact, lease, permitCurrent) => {
          if (!isCurrent() || lease.subjectId !== subjectId || lease.generation !== generation ||
            !sameRequest(exact, request)) throw new OperationJournalError('session_changed')
          // Ordinary edits may continue but must retain the exact request,
          // date, event and authenticated owner before the transport starts.
          const finalDraft = await editor.stageFreeSession(request)
          if (!finalDraft.ok || !isCurrent() || !permitCurrent() ||
            !hasReadyMarker(editor.draft, subjectId, request) ||
            !isPreparedFreeSessionForApi(request, api, subjectId, generation)) {
            throw new OperationJournalError('reconciliation_required')
          }
          return api.sendPreparedFreeSessionRequest(request, () => isCurrent() &&
            hasReadyMarker(editor.draft, subjectId, request) && permitCurrent())
        })
        if (!isCurrent()) return { state: 'session_changed' }
        if (!acknowledgesFreeSession(receipt, request) || receipt.lease.subjectId !== subjectId ||
          receipt.lease.generation !== generation || receipt.detail.clientEventId !== eventId) {
          return { state: 'reconcile_required' }
        }
        return { state: 'receipt_candidate', receipt }
      } catch (error) {
        // This permit is still unconsumed only if dispatchFirst failed before
        // invoking its callback. Retain it in memory for the exact same
        // operation; no second native claim or HTTP request is created.
        if (isCurrent() && error instanceof OperationJournalError && error.code === 'unavailable' &&
          permit.isCurrent() && isPreparedFreeSessionForApi(request, api, subjectId, generation)) {
          return { state: 'retry_preflight', retry: attempt }
        }
        // Revoke an unconsumed permit on non-retryable failures. If the local
        // transition itself fails, the in-flight record remains for recovery.
        try { await journal.markUncertain(permit) } catch { /* retained for reconciliation */ }
        return { state: isCurrent() ? 'reconcile_required' : 'session_changed' }
      } finally { attempting = false }
    }
    return await attempt()
  } catch {
    if (!isCurrent()) return { state: 'session_changed' }
    return { state: claimAttempted ? 'reconcile_required' : 'not_dispatched' }
  }
}
