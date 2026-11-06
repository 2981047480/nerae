import type { RecordingDraft } from './recording'
import type { PreparedFreeSessionRequest } from './recordingApi'

/** The exact request remains in the protected draft until locally acknowledged. */
export function freeSessionMarker(subjectId: string, clientEventId: string, request: PreparedFreeSessionRequest): string {
  return JSON.stringify({ schema: 1, subjectId, clientEventId, state: 'ready', request: {
    method: request.method, path: request.path, idempotencyKey: request.idempotencyKey, body: request.body,
  } })
}

/** Recover only the canonical free-session request belonging to this draft. */
export function pendingFreeSession(draft: RecordingDraft, subjectId: string): PreparedFreeSessionRequest | null {
  if (typeof draft.syncPending !== 'string' || draft.server) return null
  try {
    const value: unknown = JSON.parse(draft.syncPending)
    if (!value || typeof value !== 'object' || Array.isArray(value)) return null
    const marker = value as Record<string, unknown>
    const input = marker.request
    if (marker.schema !== 1 || marker.subjectId !== subjectId || marker.clientEventId !== draft.clientEventId ||
      marker.state !== 'ready' || !input || typeof input !== 'object' || Array.isArray(input)) return null
    const request = input as Record<string, unknown>
    if (request.method !== 'POST' || request.path !== '/me/sessions' ||
      typeof request.idempotencyKey !== 'string' || !/^[A-Za-z0-9._~-]{8,128}$/.test(request.idempotencyKey) ||
      typeof request.body !== 'string' || request.body !== JSON.stringify({ source: 'free', date: draft.date,
        timeZone: draft.timeZone, clientEventId: draft.clientEventId })) return null
    const exact: PreparedFreeSessionRequest = { method: 'POST', path: '/me/sessions',
      idempotencyKey: request.idempotencyKey, body: request.body }
    return draft.syncPending === freeSessionMarker(subjectId, draft.clientEventId, exact) ? exact : null
  } catch { return null }
}
