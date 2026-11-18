import { normalizeRecordingV2Request, validV2Key, type JournalRequest } from './operationJournal'
import type { RecordingDraft } from './recording'

/** Exact request bytes retained inside the account-bound encrypted draft. */
export function recordingV2Marker(subjectId: string, eventId: string, input: JournalRequest): string {
  if (!/^[1-9]\d{0,19}$/.test(subjectId) || !validV2Key(eventId)) throw new Error('invalid recording intent')
  const request = normalizeRecordingV2Request(input, eventId)
  return JSON.stringify({ schema: 2, subjectId, clientEventId: eventId, request: {
    method: request.method, path: request.path, idempotencyKey: request.idempotencyKey, body: request.body,
  } })
}

export interface PendingRecordingV2 { readonly request: JournalRequest }

/** The marker is a local intent, never evidence that a server write succeeded. */
export function pendingRecordingV2(draft: RecordingDraft, subjectId: string): PendingRecordingV2 | null {
  if (draft.recordFormat !== 2 || typeof draft.syncPending !== 'string') return null
  try {
    const value: unknown = JSON.parse(draft.syncPending)
    if (!value || typeof value !== 'object' || Array.isArray(value)) return null
    const marker = value as Record<string, unknown>
    if (Object.keys(marker).sort().join(',') !== 'clientEventId,request,schema,subjectId' || marker.schema !== 2 ||
        marker.subjectId !== subjectId || marker.clientEventId !== draft.clientEventId) return null
    const request = normalizeRecordingV2Request(marker.request, draft.clientEventId)
    if (draft.syncPending !== recordingV2Marker(subjectId, draft.clientEventId, request)) return null
    const body = JSON.parse(request.body) as Record<string, unknown>
    if (request.path === '/me/recordings') {
      if (draft.server || body.timeZone !== draft.timeZone || body.clientEventId !== draft.clientEventId) return null
    } else {
      if (!draft.server || (request.path !== `/me/recordings/${draft.server.id}/sets` &&
          request.path !== `/me/recordings/${draft.server.id}/finish`) || body.expectedVersion !== draft.server.version) return null
    }
    return { request }
  } catch { return null }
}
