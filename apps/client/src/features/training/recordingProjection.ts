import {
  prepareConfirmedSet, prepareSave, restoreDraft, validateDraftSet,
  type DraftExercise, type DraftSet, type ExerciseIdentity, type GroupIssue, type RecordingDraft, type SetWrite,
} from './recording'
import {
  acknowledgesPendingSets, acknowledgesSets, encodePendingSetsBody, isIssuedSnapshot, matchesSetPayload, parseSessionDetail,
  type RecordingAuth, type RecordingLease, type ScopedSessionDetail, type ServerSet, type SetPayload, type WorkoutSessionDetail,
} from './recordingApi'
import { pendingSets } from './setsIntent'

type ProjectionFailure = {
  ok: false
  reason: 'session_changed' | 'reconcile_required' | 'invalid_draft'
  message: string
  issues?: GroupIssue[]
}
export type ConfirmedRestoreResult =
  | { ok: true; draft: RecordingDraft }
  | { ok: false; reason: 'invalid_snapshot' | 'session_changed' | 'reconcile_required' | 'local_unsynced' | 'unsupported'; message: string }
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

export interface SubmittedSetInput {
  exerciseEntryKey: string
  recordKey: string
  weightKg: string
  reps: string
  seconds: string
  rpe: string
  confirmed: boolean
}

/** Capture exact user inputs only from an unchanged, adapter-issued preparation. */
export function captureOrderedInputs(prepared: OrderedPreparation, draft: RecordingDraft,
  baseline: ScopedSessionDetail, auth: RecordingAuth, today: string): SubmittedSetInput[] | null {
  try {
    if (!prepared || !prepared.ok || prepared.state !== 'send') return null
    const proof = preparationProofs.get(prepared)
    if (!proof || proof.baseline !== baseline || proof.clientEventId !== draft.clientEventId ||
      !validateBase(draft, baseline, proof.lease, auth, today).ok) return null
    const captured: SubmittedSetInput[] = []
    for (const write of prepared.sets) {
      const current = draft.exercises.find(exercise => exercise.exerciseEntryKey === write.exerciseEntryKey)
        ?.sets.find(set => set.recordKey === write.recordKey)
      const submitted = proof.submitted.get(write.recordKey)
      if (!current || !submitted || JSON.stringify(current) !== JSON.stringify(submitted)) return null
      captured.push({ exerciseEntryKey: write.exerciseEntryKey, recordKey: write.recordKey, weightKg: submitted.weightKg,
        reps: submitted.reps, seconds: submitted.seconds, rpe: submitted.rpe, confirmed: submitted.confirmed })
    }
    return captured
  } catch { return null }
}

export type AcknowledgementResult = ProjectionFailure | { ok: true; draft: RecordingDraft }

function failure(reason: ProjectionFailure['reason']): ProjectionFailure {
  if (reason === 'session_changed') return { ok: false, reason, message: '账号已变化，请重新登录后恢复记录' }
  if (reason === 'invalid_draft') return { ok: false, reason, message: '草稿无法校验，本机输入仍被保留' }
  return { ok: false, reason, message: '记录需要先核对最新版本，本机输入仍被保留' }
}

function sameIdentity(exercise: DraftExercise, set: ServerSet): boolean {
  return exercise.kind === set.kind && (exercise.identity.source === 'catalog'
    ? set.exerciseId === exercise.identity.exerciseId && set.exerciseName === undefined &&
      (exercise.resolvedExerciseName === undefined || exercise.resolvedExerciseName === set.resolvedExerciseName)
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
    local.clientEventId !== remote.clientEventId ||
    (local.server.status !== undefined && local.server.status !== remote.status) ||
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
  if (response.detail.clientEventId !== proof.clientEventId) return failure('reconcile_required')
  if (response.detail.status === 'skipped') return failure('reconcile_required')
  const draft: RecordingDraft = { ...base.draft, server: { id: response.detail.id, version: response.detail.version,
    status: response.detail.status },
    exercises: base.draft.exercises.map(exercise => ({ ...exercise,
      ...(exercise.identity.source === 'catalog' && response.detail.sets.some(set =>
        set.exerciseEntryKey === exercise.exerciseEntryKey)
        ? { resolvedExerciseName: response.detail.sets.find(set => set.exerciseEntryKey === exercise.exerciseEntryKey)?.resolvedExerciseName }
        : {}),
      sets: exercise.sets.map(group => changed.get(group.recordKey) || group) })) }
  try { restoreDraft(draft, today) } catch { return failure('invalid_draft') }
  if (!auth.isCurrent(proof.lease.subjectId, proof.lease.generation)) return failure('session_changed')
  return { ok: true, draft }
}

/** Apply an exact pending set receipt, keeping edits made after submission. */
export function applyPendingSetsAcknowledgement(
  current: RecordingDraft, response: ScopedSessionDetail, auth: RecordingAuth, today: string,
): AcknowledgementResult {
  let credential
  try {
    credential = auth.current()
    if (!credential || !auth.isCurrent(credential.subjectId, credential.generation)) return failure('session_changed')
  } catch { return failure('session_changed') }
  let local: RecordingDraft
  try { local = restoreDraft(current, today) } catch { return failure('invalid_draft') }
  const marker = local.syncPending
  const pending = pendingSets(local, credential.subjectId)
  if (!pending || typeof marker !== 'string' || !local.server ||
    !acknowledgesPendingSets(response, marker, credential.subjectId, credential.generation) ||
    response.lease.subjectId !== credential.subjectId || response.lease.generation !== credential.generation) {
    return failure('reconcile_required')
  }
  let detail: WorkoutSessionDetail
  try {
    if (encodePendingSetsBody(pending.expectedVersion, pending.exerciseEntryKeys, pending.sets) !== pending.request.body) {
      return failure('reconcile_required')
    }
    detail = parseSessionDetail(response.detail)
  } catch { return failure('reconcile_required') }
  if (detail.id !== local.server.id || detail.clientEventId !== local.clientEventId || detail.source !== 'free' ||
    detail.date !== local.date || detail.timeZone !== local.timeZone || detail.status === 'skipped' ||
    (local.server.status !== undefined && detail.status !== local.server.status) ||
    (detail.version !== pending.expectedVersion && detail.version !== pending.expectedVersion + 1) ||
    detail.exerciseEntryKeys.length !== pending.exerciseEntryKeys.length ||
    detail.exerciseEntryKeys.some((key, index) => key !== pending.exerciseEntryKeys[index])) return failure('reconcile_required')
  const submitted = new Map((pending.sets as SetPayload[]).map(set => [set.recordKey, set]))
  const originalInputs = new Map(pending.submitted.map(set => [set.recordKey, set]))
  const remote = new Map(detail.sets.map(set => [set.recordKey, set]))
  const seen = new Set<string>()
  const changed = new Map<string, DraftSet>()
  for (const exercise of local.exercises) {
    for (const group of exercise.sets) {
      const saved = remote.get(group.recordKey)
      const write = submitted.get(group.recordKey)
      if (!saved) {
        if (group.persisted || write) return failure('reconcile_required')
        continue
      }
      if ((!group.persisted && !write) || saved.exerciseEntryKey !== exercise.exerciseEntryKey ||
        saved.setIndex !== group.setIndex || !sameIdentity(exercise, saved)) return failure('reconcile_required')
      const checked = validateDraftSet(exercise, group)
      if (write) {
        if (!matchesSetPayload(write, saved)) return failure('reconcile_required')
        const matches = Boolean(checked.value && matchesSetPayload(checked.value, saved))
        const original = originalInputs.get(group.recordKey)
        if (!original) return failure('reconcile_required')
        const sameInput = group.weightKg === original.weightKg && group.reps === original.reps &&
          group.seconds === original.seconds && group.rpe === original.rpe && group.confirmed === original.confirmed
        changed.set(group.recordKey, { ...group, persisted: true, dirty: !matches,
          confirmed: sameInput ? true : group.confirmed })
      } else if (!group.dirty && (!checked.value || !matchesSetPayload(checked.value, saved))) {
        return failure('reconcile_required')
      }
      seen.add(group.recordKey)
    }
  }
  if (seen.size !== remote.size || [...submitted.keys()].some(key => !seen.has(key))) return failure('reconcile_required')
  const knownEntries = new Set(detail.exerciseEntryKeys)
  const localOrder = local.exercises.map(exercise => exercise.exerciseEntryKey).filter(key => knownEntries.has(key))
  if (localOrder.length !== detail.exerciseEntryKeys.length ||
    localOrder.some((key, index) => key !== detail.exerciseEntryKeys[index])) return failure('reconcile_required')
  const { syncPending: _pending, ...rest } = local
  const draft: RecordingDraft = { ...rest, server: { id: detail.id, version: detail.version, status: detail.status },
    exercises: local.exercises.map(exercise => ({ ...exercise,
      ...(exercise.identity.source === 'catalog' && detail.sets.some(set => set.exerciseEntryKey === exercise.exerciseEntryKey)
        ? { resolvedExerciseName: detail.sets.find(set => set.exerciseEntryKey === exercise.exerciseEntryKey)?.resolvedExerciseName }
        : {}),
      sets: exercise.sets.map(group => changed.get(group.recordKey) || group) })) }
  try { restoreDraft(draft, today) } catch { return failure('invalid_draft') }
  try {
    if (!auth.isCurrent(credential.subjectId, credential.generation) ||
      !acknowledgesPendingSets(response, marker, credential.subjectId, credential.generation)) return failure('session_changed')
  } catch { return failure('session_changed') }
  return { ok: true, draft }
}

function restoreFailure(reason: Exclude<ConfirmedRestoreResult, { ok: true }>['reason']): ConfirmedRestoreResult {
  const messages = {
    invalid_snapshot: '服务端记录快照无效，请重新读取',
    session_changed: '账号或登录会话已变化，请重新登录后读取',
    reconcile_required: '本机记录与服务端版本或身份不一致，请核对后恢复',
    local_unsynced: '本机有尚未同步的输入，已保留；请先协调再恢复',
    unsupported: '该记录包含当前编辑模型无法无损表达的数据',
  } as const
  return { ok: false, reason, message: messages[reason] }
}

/**
 * Restore only a live, adapter-issued server snapshot. Existing local input is
 * never replaced when it is unsubmitted or differs from the confirmed server
 * state. This restores confirmed data only; it cannot recover a remote draft
 * that was never submitted.
 */
export function restoreConfirmedSession(
  snapshot: ScopedSessionDetail,
  auth: RecordingAuth,
  today: string,
  existingDraft?: RecordingDraft,
): ConfirmedRestoreResult {
  if (!isIssuedSnapshot(snapshot)) return restoreFailure('invalid_snapshot')
  const lease = snapshot.lease
  const credential = auth?.current()
  if (!lease || !credential || credential.subjectId !== lease.subjectId || credential.generation !== lease.generation ||
    !auth.isCurrent(lease.subjectId, lease.generation)) return restoreFailure('session_changed')

  let detail: WorkoutSessionDetail
  try { detail = parseSessionDetail(snapshot.detail) }
  catch { return restoreFailure('invalid_snapshot') }
  if (detail.source !== 'free' || detail.status === 'skipped') return restoreFailure('unsupported')

  let mode: RecordingDraft['mode'] = 'after_workout'
  if (existingDraft !== undefined) {
    let local: RecordingDraft
    try { local = restoreDraft(existingDraft, today) }
    catch { return restoreFailure('local_unsynced') }
    // A server snapshot alone cannot prove what happened to a local request
    // awaiting reconciliation. Reconstructing the draft would discard it.
    if (Object.prototype.hasOwnProperty.call(local, 'syncPending')) return restoreFailure('local_unsynced')
    if (local.clientEventId !== detail.clientEventId || local.date !== detail.date || local.timeZone !== detail.timeZone) {
      return restoreFailure('reconcile_required')
    }
    if (local.exercises.some(exercise => exercise.sets.length === 0 ||
      exercise.sets.some(set => !set.persisted || set.dirty))) {
      return restoreFailure('local_unsynced')
    }
    if (local.server && (local.server.id !== detail.id || local.server.version !== detail.version ||
      (local.server.status !== undefined && local.server.status !== detail.status))) return restoreFailure('reconcile_required')
    for (const field of ['durationMin', 'rpe', 'note'] as const) {
      if (local.finish[field] !== undefined && local.finish[field] !== detail[field]) return restoreFailure('local_unsynced')
    }

    const serverRows = new Map(detail.sets.map(set => [set.recordKey, set]))
    let localCount = 0
    for (const exercise of local.exercises) {
      for (const group of exercise.sets) {
        localCount++
        const serverSet = serverRows.get(group.recordKey)
        if (!serverSet || serverSet.exerciseEntryKey !== exercise.exerciseEntryKey || serverSet.setIndex !== group.setIndex ||
          !sameIdentity(exercise, serverSet)) return restoreFailure('reconcile_required')
        const checked = validateDraftSet(exercise, group)
        if (!checked.value || !matchesSetPayload(checked.value, serverSet)) return restoreFailure('reconcile_required')
      }
    }
    if (localCount !== detail.sets.length ||
      local.exercises.filter(exercise => exercise.sets.length > 0).map(exercise => exercise.exerciseEntryKey)
        .some((key, index) => key !== detail.exerciseEntryKeys[index]) ||
      local.exercises.filter(exercise => exercise.sets.length > 0).length !== detail.exerciseEntryKeys.length) {
      return restoreFailure('reconcile_required')
    }
    mode = local.mode
  }

  const byEntry = new Map<string, ServerSet[]>()
  for (const set of detail.sets) {
    const rows = byEntry.get(set.exerciseEntryKey) || []
    if (rows.some(row => row.kind !== set.kind)) return restoreFailure('unsupported')
    rows.push(set)
    byEntry.set(set.exerciseEntryKey, rows)
  }
  const exercises: DraftExercise[] = detail.exerciseEntryKeys.map(exerciseEntryKey => {
    const rows = byEntry.get(exerciseEntryKey) || []
    const first = rows[0]
    if (!first) throw new Error('validated entry must contain a set')
    const identity: ExerciseIdentity = first.exerciseId !== undefined
      ? { source: 'catalog', exerciseId: first.exerciseId }
      : { source: 'custom', exerciseName: first.exerciseName as string }
    return {
      exerciseEntryKey,
      identity,
      resolvedExerciseName: first.resolvedExerciseName,
      kind: first.kind,
      sets: rows.map(set => ({
        recordKey: set.recordKey,
        setIndex: set.setIndex,
        weightKg: set.weightKg === undefined ? '' : String(set.weightKg),
        reps: set.reps === undefined ? '' : String(set.reps),
        seconds: set.seconds === undefined ? '' : String(set.seconds),
        rpe: set.rpe === undefined || set.rpe === null ? '' : String(set.rpe),
        confirmed: true,
        persisted: true,
        dirty: false,
      })),
    }
  })
  const draft: RecordingDraft = {
    clientEventId: detail.clientEventId,
    date: detail.date,
    timeZone: detail.timeZone,
    mode,
    server: { id: detail.id, version: detail.version, status: detail.status },
    finish: {
      ...(detail.durationMin === undefined ? {} : { durationMin: detail.durationMin }),
      ...(detail.rpe === undefined ? {} : { rpe: detail.rpe }),
      ...(detail.note === undefined ? {} : { note: detail.note }),
    },
    exercises,
  }
  let restored: RecordingDraft
  try { restored = restoreDraft(draft, today) }
  catch { return restoreFailure('invalid_snapshot') }
  const latestCredential = auth.current()
  const leaseStillCurrent = auth.isCurrent(lease.subjectId, lease.generation)
  if (!latestCredential || latestCredential.subjectId !== lease.subjectId ||
    latestCredential.generation !== lease.generation || !leaseStillCurrent) return restoreFailure('session_changed')
  return { ok: true, draft: restored }
}
