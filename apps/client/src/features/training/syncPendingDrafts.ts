import type { RecordingAuth } from './recordingApi'
import type { TrainingEditorController } from './trainingEditorController'
import type { RecordingV2SyncResult } from './syncRecordingV2'

type Discovery =
  | { ok: true; eventIds: string[]; nextCursor: string | null; hasUnclassifiedItems: boolean }
  | { ok: false; reason: string }
type Attachment = { ok: true; controller: TrainingEditorController } | { ok: false; reason: string }

export interface PendingDraftSyncPorts {
  auth: RecordingAuth
  discover(cursor: string): Promise<Discovery>
  attach(eventId: string): Promise<Attachment>
  synchronize(controller: TrainingEditorController): Promise<RecordingV2SyncResult>
}

export interface PendingDraftSyncSummary {
  state: 'synced' | 'pending' | 'attention' | 'session_changed'
  conflicts: number
}

/** One foreground pass; storage and owner leases remain the source of truth. */
export async function syncPendingDrafts(ports: PendingDraftSyncPorts): Promise<PendingDraftSyncSummary> {
  let credential
  try { credential = ports.auth.current() } catch { return { state: 'session_changed', conflicts: 0 } }
  if (!credential) return { state: 'session_changed', conflicts: 0 }
  const captured = { subjectId: credential.subjectId, generation: credential.generation, token: credential.accessToken }
  const current = (): boolean => {
    try {
      const value = ports.auth.current()
      return Boolean(value && value.subjectId === captured.subjectId && value.generation === captured.generation &&
        value.accessToken === captured.token && ports.auth.isCurrent(captured.subjectId, captured.generation))
    } catch { return false }
  }
  let cursor = ''
  let pending = false
  let attention = false
  let conflicts = 0
  const seenEvents = new Set<string>()
  const seenCursors = new Set<string>()
  try {
    for (let pageIndex = 0; pageIndex < 1000; pageIndex++) {
      if (!current()) return { state: 'session_changed', conflicts }
      const page = await ports.discover(cursor)
      if (!current()) return { state: 'session_changed', conflicts }
      if (!page.ok) return { state: page.reason === 'session_changed' ? 'session_changed' : 'attention', conflicts }
      if (!Array.isArray(page.eventIds) || page.eventIds.length > 100) return { state: 'attention', conflicts }
      if (page.hasUnclassifiedItems) attention = true
      for (const eventId of page.eventIds) {
        if (!current()) return { state: 'session_changed', conflicts }
        if (typeof eventId !== 'string' || seenEvents.has(eventId)) { attention = true; continue }
        seenEvents.add(eventId)
        const attached = await ports.attach(eventId)
        if (!current()) {
          if (attached.ok) await attached.controller.close()
          return { state: 'session_changed', conflicts }
        }
        if (!attached.ok) {
          if (attached.reason === 'session_changed') return { state: 'session_changed', conflicts }
          if (attached.reason === 'already_open') pending = true
          else attention = true
          continue
        }
        try {
          const draft = attached.controller.draft
          const hasWork = Boolean(draft.syncPending || draft.server ||
            draft.exercises.some(item => item.sets.length || item.removedSets?.length))
          if (!hasWork) continue
          if (draft.recordFormat !== 2) { attention = true; continue }
          const result = await ports.synchronize(attached.controller)
          if (!current() || result.state === 'session_changed') return { state: 'session_changed', conflicts }
          if (result.state === 'conflict') { conflicts++; attention = true }
          else if (result.state === 'unsupported' || result.state === 'invalid' || result.state === 'local_error') attention = true
          else if (result.state !== 'synced') pending = true
        } catch { attention = true }
        finally { await attached.controller.close() }
      }
      if (page.nextCursor === null) return { state: attention ? 'attention' : pending ? 'pending' : 'synced', conflicts }
      if (typeof page.nextCursor !== 'string' || !page.nextCursor || seenCursors.has(page.nextCursor)) return { state: 'attention', conflicts }
      cursor = page.nextCursor
      seenCursors.add(cursor)
    }
  } catch { return { state: current() ? 'attention' : 'session_changed', conflicts } }
  return { state: 'pending', conflicts }
}
