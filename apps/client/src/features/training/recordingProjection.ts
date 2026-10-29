import {
  prepareConfirmedSet, prepareSave, restoreDraft, validateDraftSet,
  type DraftExercise, type DraftSet, type GroupIssue, type RecordingDraft, type SetWrite,
} from './recording'
import {
  acknowledgesSets, isIssuedSnapshot, matchesSetPayload, parseSessionDetail,
  type RecordingAuth, type RecordingLease, type ScopedSessionDetail, type ServerSet, type WorkoutSessionDetail,
} from './recordingApi'

type ProjectionFailure = {
  ok: false
  reason: 'session_changed' | 'reconcile_required' | 'invalid_draft'
  message: string
  issues?: GroupIssue[]
}
type PreparedBatch = {
  ok: true
  state: 'send'
  sessionId: string
  expectedVersion: number
  exerciseEntryKeys: string[]
  sets: SetWrite[]
}
export type OrderedPreparation = ProjectionFailure | PreparedBatch | { ok: true; state: 'already_saved' }

type ValidBase = { ok: true; draft: RecordingDraft; snapshot: WorkoutSessionDetail }
type PreparationProof = { baseline: ScopedSessionDetail; lease: RecordingLease; clientEventId: string; submitted: Map<string, DraftSet> }
const preparationProofs = new WeakMap<object, PreparationProof>()

export type AcknowledgementResult = ProjectionFailure | { ok: true; draft: RecordingDraft }

function failure(reason: ProjectionFailure['reason']): ProjectionFailure {
  if (reason === 'session_changed') return { ok: false, reason, message: '账号已变化，请重新登录后恢复记录' }
  if (reason === 'invalid_draft') return { ok: false, reason, message: '草稿无法校验，本机输入仍被保留' }
  return { ok: false, reason, message: '记录需要先核对最新版本，本机输入仍被保留' }
}

function sameIdentity(exercise: DraftExercise, set: ServerSet): boolean {
  return exercise.kind === set.kind && (exercise.identity.source === 'catalog'
    ? set.exerciseId === exercise.identity.exerciseId && set.exerciseName === undefined
    : set.exerciseName === exercise.identity.exerciseName && set.exerciseId === undefined)
}

/** Only a snapshot captured under the same live login may inform a write. */
function validateBase(
  draft: RecordingDraft, snapshot: ScopedSessionDetail, lease: RecordingLease, auth: RecordingAuth, today: string,
): ValidBase | ProjectionFailure {
  if (!lease || !auth || !snapshot || !snapshot.lease ||
    lease.subjectId !== snapshot.lease.subjectId || lease.generation !== snapshot.lease.generation ||
    !auth.isCurrent(lease.subjectId, lease.generation)) return failure('session_changed')
  if (!isIssuedSnapshot(snapshot)) return failure('reconcile_required')
  const current = auth.current()
  if (!current || current.subjectId !== lease.subjectId || current.generation !== lease.generation) {
    return failure('session_changed')
  }
  let local: RecordingDraft
  let remote: WorkoutSessionDetail
  try {
    local = restoreDraft(draft, today)
    remote = parseSessionDetail(snapshot.detail)
  } catch { return failure('invalid_draft') }
  if (!local.server || local.server.id !== remote.id || local.server.version !== remote.version ||
    local.timeZone !== remote.timeZone || remote.source !== 'free' || remote.status === 'skipped') return failure('reconcile_required')

  const serverRows = new Map(remote.sets.map(set => [set.recordKey, set]))
  const seen = new Set<string>()
  for (const exercise of local.exercises) {
    for (const group of exercise.sets) {
      const saved = serverRows.get(group.recordKey)
      if (group.persisted !== Boolean(saved)) return failure('reconcile_required')
      if (!saved) continue
      if (saved.exerciseEntryKey !== exercise.exerciseEntryKey || saved.setIndex !== group.setIndex ||
        !sameIdentity(exercise, saved)) return failure('reconcile_required')
      if (!group.dirty) {
        const checked = validateDraftSet(exercise, group)
        if (!checked.value || !matchesSetPayload(checked.value, saved)) return failure('reconcile_required')
      }
      seen.add(group.recordKey)
    }
  }
  if (seen.size !== serverRows.size) return failure('reconcile_required')
  const knownEntries = new Set(remote.exerciseEntryKeys)
  const localExistingOrder = local.exercises.map(exercise => exercise.exerciseEntryKey).filter(key => knownEntries.has(key))
  if (localExistingOrder.length !== remote.exerciseEntryKeys.length ||
    localExistingOrder.some((key, index) => key !== remote.exerciseEntryKeys[index])) return failure('reconcile_required')
  return { ok: true, draft: local, snapshot: remote }
}

function project(base: ValidBase, writes: SetWrite[], baseline: ScopedSessionDetail, lease: RecordingLease): OrderedPreparation {
  const projected = new Map<string, { exerciseEntryKey: string; setIndex: number }>()
  for (const set of base.snapshot.sets) projected.set(set.recordKey, { exerciseEntryKey: set.exerciseEntryKey, setIndex: set.setIndex })
  for (const write of writes) projected.set(write.recordKey, { exerciseEntryKey: write.exerciseEntryKey, setIndex: write.setIndex })
  const indexes = new Map<string, Set<number>>()
  const activeEntries = new Set<string>()
  for (const set of projected.values()) {
    activeEntries.add(set.exerciseEntryKey)
    const used = indexes.get(set.exerciseEntryKey) || new Set<number>()
    if (used.has(set.setIndex)) return failure('reconcile_required')
    used.add(set.setIndex)
    indexes.set(set.exerciseEntryKey, used)
  }
  const order = base.draft.exercises.map(exercise => exercise.exerciseEntryKey).filter(key => activeEntries.has(key))
  if (order.length !== activeEntries.size || order.length > 2000) return failure('reconcile_required')
  const submitted = new Map<string, DraftSet>()
  for (const write of writes) {
    const group = base.draft.exercises.find(exercise => exercise.exerciseEntryKey === write.exerciseEntryKey)
      ?.sets.find(set => set.recordKey === write.recordKey)
    if (!group) return failure('reconcile_required')
    submitted.set(write.recordKey, { ...group })
  }
  const sets = writes.map(write => Object.freeze({ ...write }))
  Object.freeze(sets)
  Object.freeze(order)
  const prepared: PreparedBatch = Object.freeze({ ok: true, state: 'send', sessionId: base.snapshot.id,
    expectedVersion: base.snapshot.version, exerciseEntryKeys: order, sets })
  preparationProofs.set(prepared, { baseline, lease: { subjectId: lease.subjectId, generation: lease.generation },
    clientEventId: base.draft.clientEventId, submitted })
  return prepared
}

/** Training-after mode submits the complete nonblank valid groups only after explicit save. */
export function prepareOrderedAfterWorkout(
  draft: RecordingDraft, snapshot: ScopedSessionDetail, lease: RecordingLease, auth: RecordingAuth, today: string,
): OrderedPreparation {
  const base = validateBase(draft, snapshot, lease, auth, today)
  if (!base.ok) return base
  const prepared = prepareSave(base.draft, today)
  if (!prepared.ok) return { ok: false, reason: 'invalid_draft', message: prepared.message, issues: prepared.issues }
  return project(base, prepared.sets, snapshot, lease)
}

/** Per-set mode submits only the one group the user explicitly confirmed. */
export function prepareOrderedConfirmedSet(
  draft: RecordingDraft, snapshot: ScopedSessionDetail, lease: RecordingLease, auth: RecordingAuth,
  exerciseEntryKey: string, recordKey: string, today: string,
): OrderedPreparation {
  const base = validateBase(draft, snapshot, lease, auth, today)
  if (!base.ok) return base
  const prepared = prepareConfirmedSet(base.draft, exerciseEntryKey, recordKey, today)
  if (!prepared.ok) return { ok: false, reason: 'invalid_draft', message: prepared.message }
  if (prepared.state === 'already_saved') return { ok: true, state: 'already_saved' }
  return project(base, [prepared.set], snapshot, lease)
}


/**
 * Merge only an acknowledgement of this exact prepared write. Call inside the
 * draft controller's update transform so it sees the latest in-memory draft;
 * the caller must persist the result and handle local write failures separately.
 * No network retry, recovery or durable outbox is implemented here.
 */
export function applySetsAcknowledgement(
  current: RecordingDraft, prepared: OrderedPreparation, response: ScopedSessionDetail,
  auth: RecordingAuth, today: string,
): AcknowledgementResult {
  if (!prepared || !prepared.ok || prepared.state !== 'send') return failure('reconcile_required')
  const proof = preparationProofs.get(prepared)
  if (!proof || !acknowledgesSets(response, proof.baseline, prepared.expectedVersion, prepared.sets, prepared.exerciseEntryKeys)) {
    return failure('reconcile_required')
  }
  const base = validateBase(current, proof.baseline, proof.lease, auth, today)
  if (!base.ok) return base
  if (base.draft.clientEventId !== proof.clientEventId || response.lease.subjectId !== proof.lease.subjectId ||
    response.lease.generation !== proof.lease.generation) return failure('reconcile_required')
  const actual = new Map(response.detail.sets.map(set => [set.recordKey, set]))
  const writes = new Map(prepared.sets.map(set => [set.recordKey, set]))
  const changed = new Map<string, DraftSet>()
  for (const write of prepared.sets) {
    const exercise = base.draft.exercises.find(item => item.exerciseEntryKey === write.exerciseEntryKey)
    const group = exercise?.sets.find(item => item.recordKey === write.recordKey)
    const submitted = proof.submitted.get(write.recordKey)
    const saved = actual.get(write.recordKey)
    if (!exercise || !group || !submitted || !saved || group.setIndex !== write.setIndex ||
      !sameIdentity(exercise, saved)) return failure('reconcile_required')
    const validated = validateDraftSet(exercise, group)
    const sameAsSaved = Boolean(validated.value && matchesSetPayload(validated.value, saved))
    const sameInput = group.weightKg === submitted.weightKg && group.reps === submitted.reps &&
      group.seconds === submitted.seconds && group.rpe === submitted.rpe && group.confirmed === submitted.confirmed
    changed.set(write.recordKey, { ...group, persisted: true, dirty: !sameAsSaved,
      confirmed: sameInput ? true : group.confirmed })
  }
  if (changed.size !== writes.size) return failure('reconcile_required')
  const draft: RecordingDraft = { ...base.draft, server: { id: response.detail.id, version: response.detail.version },
    exercises: base.draft.exercises.map(exercise => ({ ...exercise,
      sets: exercise.sets.map(group => changed.get(group.recordKey) || group) })) }
  try { restoreDraft(draft, today) } catch { return failure('invalid_draft') }
  if (!auth.isCurrent(proof.lease.subjectId, proof.lease.generation)) return failure('session_changed')
  return { ok: true, draft }
}
