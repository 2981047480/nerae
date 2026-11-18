import type { RecordingDraft } from './recording'

/** V2 reserves a backwards-incompatible envelope for recoverable draft state. */
export const CURRENT_DRAFT_SCHEMA = 2

export function encodeDraftEnvelope(subjectId: string, draft: RecordingDraft): string {
  return JSON.stringify({ schema: draft.recordFormat === 2 ? (draft.syncTracking === 1 ? 4 : 3) : CURRENT_DRAFT_SCHEMA, subjectId, draft })
}

/** V1 never carried a removed-group snapshot; fail closed on ambiguous payloads. */
export function supportsDraftEnvelope(schema: unknown, rawDraft: unknown): boolean {
  if (schema !== 1 && schema !== CURRENT_DRAFT_SCHEMA && schema !== 3 && schema !== 4) return false
  if (!rawDraft || typeof rawDraft !== 'object' || Array.isArray(rawDraft)) return false
  const draft = rawDraft as Record<string, unknown>
  if ((schema === 3 || schema === 4) !== (draft.recordFormat === 2)) return false
  if ((schema === 4) !== (draft.syncTracking === 1)) return false
  if (schema !== 4 && draft.syncTracking !== undefined) return false
  if (schema === 1) {
    const exercises = (rawDraft as Record<string, unknown>).exercises
    if (Array.isArray(exercises) && exercises.some(exercise => exercise && typeof exercise === 'object' &&
      Object.prototype.hasOwnProperty.call(exercise, 'removedSets'))) return false
  }
  return true
}
