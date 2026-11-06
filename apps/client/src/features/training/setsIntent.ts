import type { RecordingDraft } from './recording'
import type { JournalRequest } from './operationJournal'
import type { SubmittedSetInput } from './recordingProjection'

/** Exact serialized request retained with the owner's local draft. */
export function setsMarker(subjectId: string, eventId: string, request: JournalRequest, submitted: SubmittedSetInput[]): string {
  return JSON.stringify({ schema: 1, subjectId, clientEventId: eventId, state: 'ready', request: {
    method: request.method, path: request.path, idempotencyKey: request.idempotencyKey, body: request.body,
  }, submitted })
}

export interface PendingSets {
  readonly request: JournalRequest
  readonly expectedVersion: number
  readonly exerciseEntryKeys: string[]
  readonly sets: unknown[]
  readonly submitted: SubmittedSetInput[]
}

/** Reject changed owner, session, version, noncanonical JSON or unsupported operations. */
export function pendingSets(draft: RecordingDraft, subjectId: string): PendingSets | null {
  if (!draft.server || typeof draft.syncPending !== 'string') return null
  try {
    const marker: unknown = JSON.parse(draft.syncPending)
    if (!marker || typeof marker !== 'object' || Array.isArray(marker)) return null
    const value = marker as Record<string, unknown>
    const raw = value.request
    if (value.schema !== 1 || value.subjectId !== subjectId || value.clientEventId !== draft.clientEventId ||
      value.state !== 'ready' || !raw || typeof raw !== 'object' || Array.isArray(raw)) return null
    const candidate = raw as Record<string, unknown>
    if (candidate.method !== 'POST' || candidate.path !== `/me/sessions/${draft.server.id}/sets` ||
      typeof candidate.idempotencyKey !== 'string' || !/^[A-Za-z0-9._~-]{8,128}$/.test(candidate.idempotencyKey) ||
      typeof candidate.body !== 'string') return null
    const body: unknown = JSON.parse(candidate.body)
    if (!body || typeof body !== 'object' || Array.isArray(body) || JSON.stringify(body) !== candidate.body) return null
    const input = body as Record<string, unknown>
    if (Object.keys(input).sort().join(',') !== 'exerciseEntryKeys,expectedVersion,sets' ||
      input.expectedVersion !== draft.server.version || !Array.isArray(input.exerciseEntryKeys) ||
      !input.exerciseEntryKeys.every(key => typeof key === 'string') || !Array.isArray(input.sets) ||
      input.sets.length === 0 || input.sets.length > 200 || !Array.isArray(value.submitted) ||
      value.submitted.length !== input.sets.length) return null
    const submitted: SubmittedSetInput[] = []
    for (let index = 0; index < value.submitted.length; index++) {
      const rawInput = value.submitted[index]
      const rawSet = input.sets[index]
      if (!rawInput || typeof rawInput !== 'object' || Array.isArray(rawInput) || !rawSet ||
        typeof rawSet !== 'object' || Array.isArray(rawSet)) return null
      const item = rawInput as Record<string, unknown>
      const set = rawSet as Record<string, unknown>
      if (Object.keys(item).sort().join(',') !== 'confirmed,exerciseEntryKey,recordKey,reps,rpe,seconds,weightKg' ||
        typeof item.exerciseEntryKey !== 'string' || typeof item.recordKey !== 'string' ||
        item.exerciseEntryKey !== set.exerciseEntryKey || item.recordKey !== set.recordKey ||
        typeof item.weightKg !== 'string' || typeof item.reps !== 'string' || typeof item.seconds !== 'string' ||
        typeof item.rpe !== 'string' || typeof item.confirmed !== 'boolean') return null
      submitted.push({ exerciseEntryKey: item.exerciseEntryKey, recordKey: item.recordKey, weightKg: item.weightKg,
        reps: item.reps, seconds: item.seconds, rpe: item.rpe, confirmed: item.confirmed })
    }
    const request: JournalRequest = { method: 'POST', path: candidate.path, idempotencyKey: candidate.idempotencyKey, body: candidate.body }
    if (draft.syncPending !== setsMarker(subjectId, draft.clientEventId, request, submitted)) return null
    return { request, expectedVersion: draft.server.version, exerciseEntryKeys: input.exerciseEntryKeys as string[], sets: input.sets, submitted }
  } catch { return null }
}
