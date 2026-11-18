import { RecordingApi, type RecordingAuth, type ScopedRecordingV2Detail } from './recordingApi'
import type { TrainingEditorController } from './trainingEditorController'
import { canAdoptCloudRecordingV2, recordingV2ConflictRows, type ConflictChoice, type RecordingConflictRow } from './recordingV2ConflictPlan'

export interface RecordingConflictReview { readonly rows: readonly RecordingConflictRow[]; readonly canAdoptCloud: boolean; readonly hasCloudAdditions: boolean }
type Failure = 'pending' | 'unsupported' | 'stale' | 'session_changed' | 'unavailable' | 'local_error' | 'invalid_choice'
type Result<T> = { ok:true; review:T } | { ok:false; reason:Failure }
type Resolution = { ok:true } | { ok:false; reason:Failure }
type ReviewState = { editor:TrainingEditorController; api:RecordingApi; auth:RecordingAuth; local:string;
  remote:ScopedRecordingV2Detail; subject:string; generation:string; token:string }
const issued = new WeakMap<RecordingConflictReview, ReviewState>()

function current(state: ReviewState): boolean {
  try {
    const credential = state.auth.current()
    return Boolean(credential && credential.subjectId === state.subject && credential.generation === state.generation &&
      credential.accessToken === state.token && state.auth.isCurrent(state.subject, state.generation) &&
      state.api.isBoundToAuth(state.auth, credential))
  } catch { return false }
}
function unchanged(state: ReviewState): boolean {
  try { return state.editor.localStatus === 'saved_locally' && JSON.stringify(state.editor.draft) === state.local }
  catch { return false }
}

/** Read both versions without changing either; pending dispatches are not discarded. */
export async function reviewRecordingV2Conflict(editor:TrainingEditorController, api:RecordingApi,
  auth:RecordingAuth): Promise<Result<RecordingConflictReview>> {
  let captured: {subject:string;generation:string;token:string} | undefined
  try {
    const credential = auth.current(); const draft = editor.draft
    if (!credential || !api.isBoundToAuth(auth, credential) || !auth.isCurrent(credential.subjectId, credential.generation)) return {ok:false,reason:'session_changed'}
    captured = {subject:credential.subjectId,generation:credential.generation,token:credential.accessToken}
    if (editor.localStatus !== 'saved_locally') return {ok:false,reason:'local_error'}
    if (draft.syncPending !== undefined) return {ok:false,reason:'pending'}
    if (draft.recordFormat !== 2 || !draft.server) return {ok:false,reason:'unsupported'}
    const local = JSON.stringify(draft)
    const remote = await api.readRecordingV2(draft.server.id)
    const state: ReviewState = { editor,api,auth,local,remote,subject:credential.subjectId,generation:credential.generation,token:credential.accessToken }
    if (!current(state)) return {ok:false,reason:'session_changed'}
    if (!unchanged(state)) return {ok:false,reason:'stale'}
    const rows = recordingV2ConflictRows(draft, remote.detail)
    if (!rows) return {ok:false,reason:'unsupported'}
    const localSetCount = draft.exercises.reduce((sum,exercise) => sum + exercise.sets.length,0)
    const review: RecordingConflictReview = Object.freeze({ rows:Object.freeze(rows.map(row => Object.freeze({...row}))),
      canAdoptCloud:canAdoptCloudRecordingV2(draft,remote.detail),
      hasCloudAdditions:remote.detail.entries.length > draft.exercises.length || remote.detail.sets.length > localSetCount })
    issued.set(review,state)
    return {ok:true,review}
  } catch {
    try {
      const now = auth.current()
      if (!captured || !now || now.subjectId !== captured.subject || now.generation !== captured.generation ||
          now.accessToken !== captured.token || !auth.isCurrent(captured.subject, captured.generation)) return {ok:false,reason:'session_changed'}
    } catch { return {ok:false,reason:'session_changed'} }
    return {ok:false,reason:'unavailable'}
  }
}

/** Confirm every difference against a fresh server version before atomically saving locally. */
export async function resolveRecordingV2Conflict(editor:TrainingEditorController, api:RecordingApi, auth:RecordingAuth,
  review:RecordingConflictReview, choices:Record<string, ConflictChoice>): Promise<Resolution> {
  const state = issued.get(review)
  if (!state || state.editor !== editor || state.api !== api || state.auth !== auth) return {ok:false,reason:'stale'}
  if (!current(state)) return {ok:false,reason:'session_changed'}
  if (!unchanged(state)) return {ok:false,reason:'stale'}
  const rowKeys = new Set(review.rows.map(row => row.key))
  if (!choices || typeof choices !== 'object' || Array.isArray(choices) || Object.keys(choices).length !== review.rows.length ||
      review.rows.some(row => choices[row.key] !== 'local' && choices[row.key] !== 'cloud') ||
      Object.keys(choices).some(key => !rowKeys.has(key))) return {ok:false,reason:'invalid_choice'}
  const selected: Record<string, ConflictChoice> = Object.fromEntries(review.rows.map(row => [row.key, choices[row.key]]))
  try {
    const fresh = await api.readRecordingV2(state.remote.detail.id)
    if (!current(state)) return {ok:false,reason:'session_changed'}
    if (!unchanged(state) || JSON.stringify(fresh.detail) !== JSON.stringify(state.remote.detail)) return {ok:false,reason:'stale'}
    const saved = await editor.resolveRecordingV2Conflict(fresh,state.local,selected,api,auth)
    if (!current(state)) return {ok:false,reason:'session_changed'}
    if (!saved.ok) return {ok:false,reason: saved.reason === 'storage_error' ? 'local_error' : unchanged(state) ? 'unsupported' : 'stale'}
    issued.delete(review)
    return {ok:true}
  } catch { return {ok:false,reason:current(state) ? 'unavailable' : 'session_changed'} }
}
