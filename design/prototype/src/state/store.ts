import { computed, reactive, watch } from 'vue'
import type {
  AiStatus,
  CoachMode,
  CoachStudent,
  Conversation,
  CoachRoot,
  ExploreFilters,
  LoadKind,
  FocusRequest,
  PersistedState,
  PlanChange,
  PlanContent,
  PlanExercise,
  PlanValidationIssue,
  PendingAccess,
  PreviewFlags,
  RemoveUndo,
  ScheduleSlot,
  Screen,
  SetInput,
  SheetKind,
  StudentRoot,
  TodayMode,
  UiState,
} from '../types'
import {
  COACH_NAME,
  DEFAULT_FILTERS,
  EXPLORE_ITEMS,
  PREVIEW_DATE_LABEL,
  PREVIEW_WEEK_LABEL,
  STORAGE_KEY,
  STUDENT_NAME,
  WEEK_DAYS,
  createSeedCoachState,
  createSeedSession,
  filterExploreItems,
  TRAINING_DURATION,
  TRAINING_EQUIPMENT,
  TRAINING_GOALS,
  DIET_GOALS,
} from '../data/sample'
import {
  AI_TEMPLATES,
  PREVIEW_TODAY,
  createExtendedSeed,
  createFutureSchedule,
  createSeedOwnPlan,
  createSeedStudentState,
  createSeedZhouCoachState,
} from '../data/extended'
import {
  appliedValues,
  doneSetCount,
  exerciseProgress,
  firstPendingEntry,
  hasErrors,
  inputFromValues,
  suggestedInput,
  emptyInput,
  referenceInput,
  totalSetCount,
  nextEventTime,
  validateSetInput,
  type SetEntry,
} from '../utils/training'
import {
  dayTotals,
  diffPlans,
  planHash,
  planTotals,
  sanitizePlan,
  serializePlan,
  validatePlan,
} from '../utils/plan'
import {
  formatDateTime,
  formatTime,
  isBodyweightKind,
  isDurationKind,
  isPerSideKind,
 } from '../utils/format'

function nowIso(): string {
  return new Date().toISOString()
}

function clonePlain<T>(value: T): T {
  return JSON.parse(JSON.stringify(value)) as T
}

const SCREENS: Screen[] = [
  'today',
  'explore',
  'planDetail',
  'training',
  'records',
  'messages',
  'incomingPlan',
  'myPlan',
  'studentPlan',
  'aiAssist',
  'diet',
  'coachDetail',
  'coachTodo',
  'coachStudents',
  'coachStudentDetail',
  'coachServices',
  'coachPlan',
]

export const STUDENT_ROOTS: StudentRoot[] = ['today', 'explore', 'records', 'messages']
export const COACH_ROOTS: CoachRoot[] = ['coachTodo', 'coachStudents', 'messages', 'coachServices']

const SHARED_ACTOR_SCREENS: Screen[] = ['messages', 'incomingPlan']

const COACH_SCREENS: Screen[] = [
  'coachTodo',
  'coachStudents',
  'coachStudentDetail',
  'coachServices',
  'coachPlan',
]

function isScreen(value: unknown): value is Screen {
  return typeof value === 'string' && (SCREENS as string[]).includes(value)
}

function createSeedState(): PersistedState {
  const session = createSeedSession()
  const inputs: Record<string, SetInput> = {}
  const first = session.exercises[0]?.sets.find((set) => set.status === 'pending')
  const firstExercise = session.exercises[0]
  if (first && firstExercise) {
    inputs[first.id] = emptyInput()
  }
  const extended = createExtendedSeed()
  const coachSeed = createSeedCoachState()
  return {
    schema: 2,
    auth: { signedIn: true, age: 'adult' },
    actor: 'student',
    screen: 'today',
    stack: [],
    sessionSequence: 1,
    recordingMode: 'after',
    selection: {
      selectedPlanId: 'plan-upper-pull',
      planDayId: '',
      selectedCoachId: 'gu-yan',
      selectedPackageId: 'pkg-strength',
      selectedStudentId: 'lin-che',
      activeConversationId: 'conv-gu-yan',
      actor: 'student',
      incomingHistoryVersion: null,
      incomingHistoryStudentId: 'lin-che',
      incomingReadOnly: false,
      incomingRefMissing: false,
      recordsFocusId: null,
      recordsFocusExplicit: false,
      recordsRange: '4w',
      recordsExerciseId: '哑铃卧推',
      ownDayId: 'own-day-a',
      aiTemplateId: 'ai-stable-reps',
      coachDayId: 'day-a',
      coachMode: 'review',
    },
    session,
    inputs,
    favorites: [],
    explore: {
      mode: 'training',
      trainingFilters: { ...DEFAULT_FILTERS },
      dietFilters: { ...DEFAULT_FILTERS },
      coachFilters: { goal: 'all', mode: 'all', price: 'all' },
    },
    coach: coachSeed,
    coachSnapshots: {
      'lin-che': clonePlain(coachSeed),
      'zhou-lan': createSeedZhouCoachState(),
    },
    activeCoachStudentId: 'lin-che',
    ...extended,
    reduceMotion: false,
    textScale: 1,
    updatedAt: nowIso(),
  }
}

function normalizeServiceDraft(
  raw: Partial<import('../types').ServiceDraft> | undefined,
  fallback: import('../types').ServiceDraft,
): import('../types').ServiceDraft {
  const priceYuan =
    typeof raw?.priceYuan === 'number' && Number.isFinite(raw.priceYuan)
      ? raw.priceYuan
      : fallback.priceYuan
  const priceInput =
    typeof raw?.priceInput === 'string'
      ? raw.priceInput
      : String(priceYuan)
  return {
    ...fallback,
    ...(raw ?? {}),
    priceYuan,
    priceInput,
    priceUnit:
      typeof raw?.priceUnit === 'string' && raw.priceUnit
        ? raw.priceUnit
        : fallback.priceUnit,
    scope: typeof raw?.scope === 'string' ? raw.scope : fallback.scope,
    goalsText: typeof raw?.goalsText === 'string' ? raw.goalsText : fallback.goalsText,
    response: typeof raw?.response === 'string' ? raw.response : fallback.response,
    description:
      typeof raw?.description === 'string' ? raw.description : fallback.description,
    cycle: typeof raw?.cycle === 'string' ? raw.cycle : fallback.cycle,
    visible: typeof raw?.visible === 'boolean' ? raw.visible : fallback.visible,
    version: typeof raw?.version === 'number' ? raw.version : fallback.version,
  }
}

function normalizeServices(
  raw: PersistedState['services'] | undefined,
  base: PersistedState['services'],
): PersistedState['services'] {
  if (!raw) return clonePlain(base)
  const keys = new Set([...Object.keys(base.drafts), ...Object.keys(raw.drafts ?? {})])
  const drafts: PersistedState['services']['drafts'] = {}
  const saved: PersistedState['services']['saved'] = {}
  for (const key of keys) {
    const fallback = base.drafts[key] ?? Object.values(base.drafts)[0]!
    drafts[key] = normalizeServiceDraft(raw.drafts?.[key], fallback)
    saved[key] = normalizeServiceDraft(raw.saved?.[key], drafts[key]!)
  }
  return {
    drafts,
    saved,
    savedAt: raw.savedAt ?? base.savedAt,
    version: typeof raw.version === 'number' ? raw.version : base.version,
    previewPackageId: raw.previewPackageId ?? base.previewPackageId,
  }
}

function projectServicesToCoaches(
  coaches: import('../types').CoachProfile[],
  saved: PersistedState['services']['saved'],
): import('../types').CoachProfile[] {
  const next = clonePlain(coaches)
  for (const draft of Object.values(saved)) {
    for (const coach of next) {
      const pack = coach.packages.find((item) => item.id === draft.packageId)
      if (!pack) continue
      pack.summary = draft.description
      pack.priceYuan = draft.priceYuan
      pack.priceUnit = draft.priceUnit
      pack.cycle = draft.cycle
      pack.response = draft.response
      pack.includes = draft.scope
        .split(/[；;]/)
        .map((item) => item.trim())
        .filter(Boolean)
      pack.goals = draft.goalsText
        .split(/[·、,，]/)
        .map((item) => item.trim())
        .filter(Boolean)
      pack.visible = draft.visible
      pack.version = draft.version
    }
  }
  return next
}

function normalizeRelations(
  relations: PersistedState['relations'],
  parsedCoaches: import('../types').CoachProfile[],
  baseCoaches: import('../types').CoachProfile[],
): PersistedState['relations'] {
  return relations.map((relation) => {
    if (relation.agreedName && relation.agreedPriceUnit && relation.agreedCycle) return relation
    const candidates = [...parsedCoaches, ...baseCoaches]
      .flatMap((coach) => coach.packages)
      .filter((pack) => pack.id === relation.packageId)
    const confirmed = candidates.find((pack) => pack.version === relation.serviceVersion)
    return {
      ...relation,
      agreedName: relation.agreedName || confirmed?.name || '原确认服务',
      agreedPriceUnit: relation.agreedPriceUnit || confirmed?.priceUnit || '',
      agreedCycle: relation.agreedCycle || confirmed?.cycle || '',
      agreedNeedsReview: Boolean(
        !(relation.agreedName || confirmed?.name) ||
          !(relation.agreedPriceUnit || confirmed?.priceUnit) ||
          !(relation.agreedCycle || confirmed?.cycle),
      ),
    }
  })
}

const STUDENT_RELATIONS: Record<string, string> = {
  'lin-che': 'rel-gu-yan',
  'zhou-lan': 'rel-zhou-lan',
  'an-ran': 'rel-jiang-yao',
  'chen-mo': 'rel-he-an',
}

function normalizeStudentRecord(raw: CoachStudent & { healthAuthorized?: boolean }): CoachStudent {
  const { healthAuthorized: _ignored, ...student } = raw
  return {
    ...student,
    relationId: student.relationId || STUDENT_RELATIONS[student.id] || '',
  }
}

function normalizeConversationRecord(
  raw: Conversation & {
    unread?: number
    draft?: string
    draftOwnerInferred?: 'student' | 'coach'
  },
  _fallbackDraftOwner?: 'student' | 'coach',
): Conversation {
  const messages = Array.isArray(raw.messages) ? raw.messages : []
  const studentId =
    raw.studentId ||
    (raw.relationId === 'rel-zhou-lan' ? 'zhou-lan' : 'lin-che')
  const legacyUnread = typeof raw.unread === 'number' ? raw.unread : 0
  const legacyReadIndex = Math.max(-1, messages.length - 1 - legacyUnread)
  const readBy = raw.readBy ?? {
    student: legacyReadIndex >= 0 ? messages[legacyReadIndex]?.id ?? null : null,
    coach: messages[messages.length - 1]?.id ?? null,
  }
  const drafts = raw.drafts
    ? { student: raw.drafts.student ?? '', coach: raw.drafts.coach ?? '' }
    : { student: '', coach: '' }
  const legacyDraft = typeof raw.draft === 'string' ? raw.draft : ''
  let unassignedLegacyDraft = raw.unassignedLegacyDraft ?? ''
  if (raw.draftOwnerInferred) {
    const inferred = drafts[raw.draftOwnerInferred]
    if (inferred) {
      unassignedLegacyDraft = unassignedLegacyDraft || inferred
      drafts[raw.draftOwnerInferred] = ''
    }
  }
  if (!unassignedLegacyDraft && legacyDraft) unassignedLegacyDraft = legacyDraft
  const { unread: _unread, draft: _draft, draftOwnerInferred: _owner, ...conversation } =
    raw as Conversation & { unread?: number; draft?: string; draftOwnerInferred?: string }
  return {
    ...conversation,
    studentId,
    readBy,
    drafts,
    unassignedLegacyDraft: unassignedLegacyDraft || undefined,
  }
}

function ensureStudentServiceLinks(target: PersistedState): void {
  for (const student of target.students) {
    if (student.relationStatus !== 'active' || student.serviceEnded) continue
    const relationId = student.relationId || STUDENT_RELATIONS[student.id] || `rel-${student.id}`
    student.relationId = relationId
    if (!target.relations.some((relation) => relation.id === relationId)) {
      target.relations.push({
        id: relationId,
        coachId: 'gu-yan',
        packageId: '',
        status: 'active',
        agreedPriceYuan: 0,
        agreedPriceUnit: '',
        agreedCycle: '',
        agreedName: student.packageName,
        agreedNeedsReview: true,
        agreedPriceKnown: false,
        agreedAtKnown: false,
        agreedAt: '',
        serviceVersion: 0,
        healthAuthorized: false,
        healthRevokedAt: null,
        planVersion: student.planVersion,
        readOnly: false,
      })
    }
    if (!target.conversations.some((conversation) => conversation.studentId === student.id)) {
      target.conversations.push({
        id: `conv-${student.id}`,
        coachId: 'gu-yan',
        coachName: COACH_NAME,
        studentId: student.id,
        serviceLabel: `${student.packageName} · 服务`,
        packageId: null,
        relationId,
        messages: [],
        drafts: { student: '', coach: '' },
        readBy: { student: null, coach: null },
        updatedAt: nowIso(),
        kind: 'service',
      })
    }
  }
}

function loadState(): PersistedState {
  if (typeof window === 'undefined') return createSeedState()
  try {
    const raw = window.localStorage.getItem(STORAGE_KEY)
    if (!raw) return createSeedState()
    const parsed = JSON.parse(raw) as Partial<PersistedState>
    if (
      !parsed ||
      parsed.schema !== 2 ||
      !parsed.session ||
      !Array.isArray(parsed.session.exercises) ||
      !parsed.coach
    ) {
      return createSeedState()
    }
    const base = createSeedState()
    const parsedCoaches =
      Array.isArray(parsed.coaches) && parsed.coaches.length ? parsed.coaches : base.coaches
    const normalizedServices = normalizeServices(parsed.services, base.services)
    const parsedRelations =
      Array.isArray(parsed.relations) && parsed.relations.length
        ? parsed.relations
        : base.relations
    const merged: PersistedState = {
      ...base,
      ...parsed,
      actor: parsed.actor === 'coach' ? 'coach' : 'student',
      screen: isScreen(parsed.screen) ? parsed.screen : 'today',
      stack: Array.isArray(parsed.stack) ? parsed.stack.filter(isScreen) : [],
      selection: { ...base.selection, ...(parsed.selection ?? {}) },
      recordingMode: parsed.recordingMode === 'set' ? 'set' : 'after',
      sessionSequence: typeof parsed.sessionSequence === 'number' ? parsed.sessionSequence : 1,
      auth: parsed.auth ?? base.auth,
      inputs: parsed.inputs ?? base.inputs,
      favorites: Array.isArray(parsed.favorites) ? parsed.favorites : [],
      explore: {
        mode:
          parsed.explore?.mode === 'diet'
            ? 'diet'
            : parsed.explore?.mode === 'coach'
              ? 'coach'
              : 'training',
        trainingFilters: {
          ...base.explore.trainingFilters,
          ...(parsed.explore?.trainingFilters ??
            (parsed.explore as unknown as { filters?: ExploreFilters } | undefined)?.filters ??
            {}),
        },
        dietFilters: {
          ...base.explore.dietFilters,
          ...(parsed.explore?.dietFilters ?? {}),
        },
        coachFilters: {
          ...base.explore.coachFilters,
          ...(parsed.explore?.coachFilters ?? {}),
        },
      },
      coach: {
        ...base.coach,
        ...parsed.coach,
        deliveries: Array.isArray(parsed.coach.deliveries) ? parsed.coach.deliveries : [],
      },
      coachSnapshots:
        parsed.coachSnapshots && typeof parsed.coachSnapshots === 'object'
          ? { ...base.coachSnapshots, ...parsed.coachSnapshots }
          : base.coachSnapshots,
      activeCoachStudentId:
        typeof parsed.activeCoachStudentId === 'string'
          ? parsed.activeCoachStudentId
          : 'lin-che',
      session: {
        ...base.session,
        ...parsed.session,
        conflicts: Array.isArray(parsed.session.conflicts) ? parsed.session.conflicts : [],
      },
      student: parsed.student ?? base.student,
      records: Array.isArray(parsed.records) ? parsed.records : base.records,
      coaches: projectServicesToCoaches(parsedCoaches, normalizedServices.saved),
      relations: normalizeRelations(parsedRelations, parsedCoaches, base.coaches),
      conversations: (Array.isArray(parsed.conversations)
        ? parsed.conversations
        : base.conversations
      ).map((conversation) => normalizeConversationRecord(conversation)),
      todoItems: Array.isArray(parsed.todoItems) ? parsed.todoItems : base.todoItems,
      students: (Array.isArray(parsed.students) ? parsed.students : base.students).map(
        (student) => normalizeStudentRecord(student),
      ),
      ownPlan: parsed.ownPlan ?? base.ownPlan,
      ai: parsed.ai ?? base.ai,
      diet: parsed.diet ?? base.diet,
      services: normalizedServices,
      preview: { ...base.preview, ...(parsed.preview ?? {}) },
      textScale: parsed.textScale === 2 ? 2 : 1,
      updatedAt: nowIso(),
    }
    ensureStudentServiceLinks(merged)
    const rawSelection = (parsed.selection ?? {}) as Partial<PersistedState['selection']>
    const hasSelectionField = (key: keyof PersistedState['selection']): boolean =>
      Object.prototype.hasOwnProperty.call(rawSelection, key)
    const persistedActor =
      rawSelection.actor === 'coach' || rawSelection.actor === 'student'
        ? rawSelection.actor
        : parsed.actor === 'coach'
          ? 'coach'
          : 'student'
    if (SHARED_ACTOR_SCREENS.includes(merged.screen)) {
      merged.actor = persistedActor
    } else {
      merged.actor = actorOfScreen(merged.screen)
    }
    merged.selection.actor = merged.actor
    if (
      merged.screen === 'incomingPlan' &&
      !['incomingHistoryVersion', 'incomingHistoryStudentId', 'incomingReadOnly', 'incomingRefMissing'].every(
        (key) => hasSelectionField(key as keyof PersistedState['selection']),
      )
    ) {
      const conversation = merged.conversations.find(
        (item) => item.id === merged.selection.activeConversationId,
      )
      merged.selection.incomingHistoryVersion = null
      merged.selection.incomingHistoryStudentId = conversation?.studentId ?? ''
      merged.selection.incomingReadOnly = merged.actor === 'coach'
      merged.selection.incomingRefMissing = true
    }
    if (
      merged.screen === 'records' &&
      merged.stack.includes('messages') &&
      !hasSelectionField('recordsFocusExplicit')
    ) {
      merged.selection.recordsFocusExplicit = true
      merged.selection.recordsFocusId = null
    }
    const activeCoachId = merged.activeCoachStudentId
    if (merged.coachSnapshots[activeCoachId]) {
      merged.coach = merged.coachSnapshots[activeCoachId]!
    }
    // Discard obsolete timer state while preserving recorded sets and inputs.
    delete (merged.session as unknown as { rest?: unknown }).rest
    merged.session.trainingDate = merged.session.trainingDate || PREVIEW_TODAY
    return merged
  } catch {
    return createSeedState()
  }
}

function createInitialUi(selection: PersistedState['selection']): UiState {
  return {
    sheet: null,
    resetConfirm: false,
    trainingErrors: {},
    justCompletedSetId: null,
    coachMode: selection.coachMode,
    coachDayId: selection.coachDayId,
    searchOpen: false,
    exploreQuery: '',
    expandedId: null,
    filterDraft: { ...DEFAULT_FILTERS },
    publishBlocked: false,
    flashDiff: false,
    saveNote: null,
    saveNoteTone: null,
    removeUndo: null,
    focusRequest: null,
    focusOrigin: null,
    activeConversationId: selection.activeConversationId,
    incomingHistoryVersion: selection.incomingHistoryVersion,
    incomingHistoryStudentId: selection.incomingHistoryStudentId,
    incomingReadOnly: selection.incomingReadOnly,
    incomingRefMissing: selection.incomingRefMissing,
    recordsFocusId: selection.recordsFocusId,
    recordsFocusExplicit: selection.recordsFocusExplicit,
    selectedPlanId: selection.selectedPlanId,
    selectedCoachId: selection.selectedCoachId,
    selectedPackageId: selection.selectedPackageId,
    selectedStudentId: selection.selectedStudentId,
    recordsRange: selection.recordsRange,
    recordsExerciseId: selection.recordsExerciseId,
    ownDayId: selection.ownDayId,
    planDayId: selection.planDayId,
    aiTemplateId: selection.aiTemplateId,
    dietOpen: false,
    confirmHash: null,
    conflictResolved: false,
    serviceSaving: false,
    lastSentMessageId: null,
    accessStep: 'signin',
    pendingAccess: null,
  }
}

export const state = reactive<PersistedState>(loadState())
export const ui = reactive<UiState>(createInitialUi(state.selection))

/* ------------------------------------------------------------------ */
/* 持久化与计时                                                        */
/* ------------------------------------------------------------------ */

let persistTimer: number | null = null
let completedTimer: number | null = null
let saveNoteTimer: number | null = null
let removeUndoTimer: number | null = null
let flashTimer: number | null = null
let stateGeneration = 0

export const storageState = reactive({ ok: true, message: '' })

function syncSelection(): void {
  const next: PersistedState['selection'] = {
    selectedPlanId: ui.selectedPlanId ?? state.selection.selectedPlanId,
    planDayId: ui.planDayId,
    selectedCoachId: ui.selectedCoachId ?? state.selection.selectedCoachId,
    selectedPackageId: ui.selectedPackageId,
    selectedStudentId: ui.selectedStudentId ?? state.selection.selectedStudentId,
    activeConversationId: ui.activeConversationId,
    actor: state.actor,
    incomingHistoryVersion: ui.incomingHistoryVersion,
    incomingHistoryStudentId: ui.incomingHistoryStudentId,
    incomingReadOnly: ui.incomingReadOnly,
    incomingRefMissing: ui.incomingRefMissing,
    recordsFocusId: ui.recordsFocusId,
    recordsFocusExplicit: ui.recordsFocusExplicit,
    recordsRange: ui.recordsRange,
    recordsExerciseId: ui.recordsExerciseId,
    ownDayId: ui.ownDayId,
    aiTemplateId: ui.aiTemplateId,
    coachDayId: ui.coachDayId,
    coachMode: ui.coachMode,
  }
  if (JSON.stringify(next) !== JSON.stringify(state.selection)) state.selection = next
}

function persistNow(): boolean {
  if (typeof window === 'undefined') return true
  try {
    syncSelection()
    const studentKey = state.activeCoachStudentId
    const coachSerialized = JSON.stringify(state.coach)
    if (JSON.stringify(state.coachSnapshots[studentKey]) !== coachSerialized) {
      state.coachSnapshots[studentKey] = clonePlain(state.coach)
    }
    const payload = { ...state, updatedAt: nowIso() }
    window.localStorage.setItem(STORAGE_KEY, JSON.stringify(payload))
    storageState.ok = true
    storageState.message = ''
    return true
  } catch {
    storageState.ok = false
    storageState.message = '未能保存到本机。请检查浏览器存储空间后重试。'
    return false
  }
}

export function retryPersist(): boolean {
  const saved = persistNow()
  if (saved && (state.preview.training === 'sync-failed' || state.preview.training === 'sync-pending')) {
    state.preview.training = state.session.status === 'complete' ? 'complete' : 'normal'
  }
  return saved
}

function schedulePersist(): void {
  if (typeof window === 'undefined') return
  if (persistTimer !== null) window.clearTimeout(persistTimer)
  const generation = stateGeneration
  persistTimer = window.setTimeout(() => {
    persistTimer = null
    if (generation !== stateGeneration) return
    persistNow()
  }, 120)
}

watch(state, schedulePersist, { deep: true })

if (typeof window !== 'undefined') {
  window.addEventListener('pagehide', persistNow)
}

export const prefersReducedMotion = reactive({ value: false })
if (typeof window !== 'undefined' && window.matchMedia) {
  const query = window.matchMedia('(prefers-reduced-motion: reduce)')
  prefersReducedMotion.value = query.matches
  query.addEventListener('change', (event) => {
    prefersReducedMotion.value = event.matches
  })
}

export const motionReduced = computed(() => state.reduceMotion || prefersReducedMotion.value)

/* ------------------------------------------------------------------ */
/* 导航                                                                */
/* ------------------------------------------------------------------ */

const scrollPositions = new Map<Screen, number>()

function rememberScroll(): void {
  if (typeof window === 'undefined') return
  scrollPositions.set(state.screen, window.scrollY)
}

export function consumeScrollPosition(screen: Screen): number {
  const top = scrollPositions.get(screen) ?? 0
  scrollPositions.delete(screen)
  return top
}

function actorOfScreen(screen: Screen): 'student' | 'coach' {
  return COACH_SCREENS.includes(screen) ? 'coach' : 'student'
}

function navigate(
  screen: Screen,
  options: { root?: boolean; top?: boolean; replace?: boolean } = {},
): void {
  if (state.screen === screen && !options.top) return
  rememberScroll()
  if (options.root) {
    state.stack = []
  } else if (state.screen !== screen) {
    if (options.replace) state.stack = [...state.stack.slice(0, -1), state.screen]
    else state.stack = [...state.stack, state.screen]
  }
  state.actor = SHARED_ACTOR_SCREENS.includes(screen) ? state.actor : actorOfScreen(screen)
  state.screen = screen
  if (options.top) scrollPositions.delete(screen)
  ui.sheet = null
}

export function goBack(): boolean {
  const previous = state.stack[state.stack.length - 1]
  if (!previous) {
    goRoot()
    return false
  }
  state.stack = state.stack.slice(0, -1)
  state.screen = previous
  state.actor = SHARED_ACTOR_SCREENS.includes(previous) ? state.actor : actorOfScreen(previous)
  ui.sheet = null
  return true
}

export function canGoBack(): boolean {
  return state.stack.length > 0
}

export function goRoot(): void {
  navigate(state.actor === 'coach' ? 'coachTodo' : 'today', { root: true, top: true })
}

export function goToToday(): void {
  state.actor = 'student'
  navigate('today', { root: true, top: true })
}

export function goToExplore(): void {
  state.actor = 'student'
  navigate('explore', { root: true, top: true })
}

export function goToRecords(): void {
  state.actor = 'student'
  ui.recordsFocusExplicit = false
  ui.recordsFocusId = null
  navigate('records', { root: true, top: true })
}

export function goToMessages(): void {
  const actor = state.actor
  ui.activeConversationId = null
  navigate('messages', { root: true, top: true })
  state.actor = actor
}

export function goToConversation(id: string): void {
  ui.activeConversationId = id
  const actor = state.actor
  markConversationRead(id)
  navigate('messages', { root: true, top: true })
  state.actor = actor
}

export function goToCoachTodo(): void {
  state.actor = 'coach'
  navigate('coachTodo', { root: true, top: true })
}

export function goToCoachStudents(): void {
  state.actor = 'coach'
  navigate('coachStudents', { root: true, top: true })
}

export function goToCoachServices(): void {
  state.actor = 'coach'
  navigate('coachServices', { root: true, top: true })
}

export function goToCoachPlan(): void {
  state.actor = 'coach'
  navigate('coachPlan', { root: true, top: true })
}

export function goToMyPlan(): void {
  state.actor = 'student'
  navigate('myPlan')
}

export function goToStudentPlan(): void {
  state.actor = 'student'
  navigate('studentPlan')
}

export function goToIncomingPlan(
  version: number | null = null,
  studentId = 'lin-che',
  readOnly = false,
): void {
  state.actor = readOnly ? 'coach' : 'student'
  ui.incomingHistoryStudentId = studentId
  ui.incomingReadOnly = readOnly
  ui.incomingRefMissing = false
  ui.incomingHistoryVersion = version
  if (version !== null) {
    const delivery = deliveriesForStudent(studentId).find((item) => item.version === version)
    ui.incomingRefMissing = !delivery
  }
  navigate('incomingPlan')
  state.actor = readOnly ? 'coach' : 'student'
}

export function goToDiet(itemId = 'diet-training-day'): void {
  state.actor = 'student'
  state.diet.selectedItemId = itemId
  navigate('diet')
}

export function goToAiAssist(templateId = 'ai-stable-reps', context: 'own' | 'coach' = 'own'): void {
  state.actor = 'student'
  state.ai.context = context
  state.ai.templateId = templateId
  state.ai.baseVersion = null
  ui.aiTemplateId = templateId
  const template = AI_TEMPLATES.find((item) => item.id === templateId)
  if (template) {
    state.ai.intent = template.intent
    state.preview.ai = template.status
  }
  navigate('aiAssist')
}

export function goToPlanDetail(planId: string): void {
  state.actor = 'student'
  ui.selectedPlanId = planId
  const item = EXPLORE_ITEMS.find((entry) => entry.id === planId)
  ui.planDayId = item?.days?.[0]?.id ?? ''
  navigate('planDetail')
}

export function goToCoachDetail(coachId: string, packageId?: string | null): void {
  state.actor = 'student'
  const coach = state.coaches.find((item) => item.id === coachId)
  const belongs = coach?.packages.some((pack) => pack.id === ui.selectedPackageId)
  ui.selectedCoachId = coachId
  if (packageId && coach?.packages.some((pack) => pack.id === packageId)) {
    ui.selectedPackageId = packageId
  } else if (!belongs) {
    ui.selectedPackageId = coach?.packages[0]?.id ?? null
  }
  navigate('coachDetail')
}

export function goToCoachStudentDetail(studentId: string): void {
  state.actor = 'coach'
  ui.selectedStudentId = studentId
  navigate('coachStudentDetail')
}

export function activateCoachStudentPlan(studentId: string): void {
  const currentId = state.activeCoachStudentId ?? 'lin-che'
  if (currentId !== studentId) {
    state.coachSnapshots[currentId] = clonePlain(state.coach)
    const snapshot =
      state.coachSnapshots[studentId] ??
      (studentId === 'zhou-lan' ? createSeedZhouCoachState() : createSeedCoachState())
    state.coach = clonePlain(snapshot)
    state.coachSnapshots[studentId] = clonePlain(snapshot)
  }
  state.activeCoachStudentId = studentId
  ui.selectedStudentId = studentId
  ui.coachDayId = state.coach.draft.days[0]?.id ?? ''
  ui.coachMode = 'review'
  ui.publishBlocked = false
  ui.saveNote = null
  ui.removeUndo = null
  state.actor = 'coach'
}

export function goToCoachPlanForStudent(studentId: string): void {
  activateCoachStudentPlan(studentId)
  navigate('coachPlan')
}

export function goHome(): void {
  if (state.actor === 'coach') goToCoachTodo()
  else goToToday()
}

export function enterTraining(): void {
  navigate('training', { top: true })
}

export function leaveTraining(): void {
  state.actor = 'student'
  state.todayMode = state.session.status === 'complete' ? 'complete' : 'in_progress'
  navigate('today', { root: true })
}

export function openSheet(sheet: SheetKind): void {
  ui.sheet = sheet
  if (sheet === 'filter') {
    ui.filterDraft = { ...currentFilters.value }
  }
}

export function closeSheet(): void {
  ui.sheet = null
  ui.resetConfirm = false
}

export function resetPreview(): void {
  stateGeneration += 1
  if (typeof window !== 'undefined') {
    if (persistTimer !== null) window.clearTimeout(persistTimer)
    if (completedTimer !== null) window.clearTimeout(completedTimer)
    if (saveNoteTimer !== null) window.clearTimeout(saveNoteTimer)
    if (removeUndoTimer !== null) window.clearTimeout(removeUndoTimer)
    if (flashTimer !== null) window.clearTimeout(flashTimer)
  }
  persistTimer = null
  completedTimer = null
  saveNoteTimer = null
  removeUndoTimer = null
  flashTimer = null
  Object.assign(state, createSeedState())
  Object.assign(ui, createInitialUi(state.selection))
  persistNow()
  try {
    window.localStorage.removeItem(STORAGE_KEY)
    persistNow()
  } catch {
    /* ignore */
  }
  if (typeof window !== 'undefined') window.scrollTo(0, 0)
}

export function setReduceMotion(value: boolean): void {
  state.reduceMotion = value
}

export function setTextScale(value: 1 | 2): void {
  state.textScale = value
}

/* ------------------------------------------------------------------ */
/* 预览情境与状态                                                      */
/* ------------------------------------------------------------------ */

export type PreviewScenario =
  | 'today'
  | 'explore'
  | 'training'
  | 'records'
  | 'messages'
  | 'planDetail'
  | 'incomingPlan'
  | 'myPlan'
  | 'aiAssist'
  | 'diet'
  | 'coachList'
  | 'coachDetail'
  | 'coach-edit'
  | 'coach-review'
  | 'coach-todo'
  | 'coach-messages'
  | 'coach-students'
  | 'coach-services'

export function jumpToScenario(scenario: PreviewScenario): void {
  const resetNav = () => {
    state.stack = []
    scrollPositions.clear()
    ui.sheet = null
    if (typeof window !== 'undefined') window.scrollTo(0, 0)
  }
  switch (scenario) {
    case 'coach-edit':
    case 'coach-review':
      activateCoachStudentPlan(ui.selectedStudentId ?? 'lin-che')
      state.actor = 'coach'
      ui.coachMode = scenario === 'coach-edit' ? 'edit' : 'review'
      state.screen = 'coachPlan'
      break
    case 'coach-todo':
      state.actor = 'coach'
      state.screen = 'coachTodo'
      break
    case 'coach-messages':
      state.actor = 'coach'
      state.screen = 'messages'
      ui.activeConversationId = null
      break
    case 'coach-students':
      state.actor = 'coach'
      state.screen = 'coachStudents'
      break
    case 'coach-services':
      state.actor = 'coach'
      state.screen = 'coachServices'
      break
    case 'coachList':
      state.actor = 'student'
      state.explore.mode = 'coach'
      state.screen = 'explore'
      break
    case 'coachDetail':
      state.actor = 'student'
      state.screen = 'coachDetail'
      break
    case 'planDetail':
      state.actor = 'student'
      state.screen = 'planDetail'
      break
    case 'aiAssist':
      state.actor = 'student'
      state.screen = 'aiAssist'
      break
    case 'records':
      ui.recordsFocusExplicit = false
      ui.recordsFocusId = null
      state.actor = 'student'
      state.screen = 'records'
      break
    case 'incomingPlan':
      ui.incomingHistoryVersion = null
      ui.incomingHistoryStudentId = 'lin-che'
      ui.incomingReadOnly = false
      ui.incomingRefMissing = false
      state.actor = 'student'
      state.screen = 'incomingPlan'
      break
    default:
      state.actor = 'student'
      state.screen = scenario
  }
  resetNav()
}

export const previewScenarios = computed(() => {
  const student = [
    { key: 'today', label: '今天', meta: todayModeLabel(state.todayMode) },
    { key: 'explore', label: '发现', meta: '训练 / 饮食 / 教练' },
    { key: 'planDetail', label: '计划详情', meta: '采用与替换' },
    { key: 'training', label: '训练记录', meta: `${doneSets.value} / ${totalSets.value} 组` },
    { key: 'records', label: '记录', meta: `${recordRows.value.length} 次训练记录` },
    { key: 'messages', label: '消息', meta: `${unreadTotal.value} 条未读` },
    { key: 'incomingPlan', label: '收到计划', meta: state.student.incoming ? `版本 ${state.student.incoming.version}` : '暂无' },
    { key: 'myPlan', label: '我的计划', meta: `版本 ${state.ownPlan.version}` },
    { key: 'aiAssist', label: 'AI 整理', meta: state.ai.context === 'own' ? '已有模板候选' : '教练计划' },
    { key: 'diet', label: '饮食自评', meta: state.diet.report.status ? '今天已自评' : '今天未自评' },
    { key: 'coachList', label: '教练', meta: `${state.coaches.length} 位样例教练` },
    { key: 'coachDetail', label: '教练详情', meta: '服务包与样例' },
  ] as const
  const coach = [
    { key: 'coach-todo', label: '待办', meta: `${openTodo.value.length} 项待处理` },
    { key: 'coach-students', label: '学员', meta: `${state.students.length} 位样例学员` },
    { key: 'coach-messages', label: '消息', meta: `${unreadTotal.value} 条未读` },
    { key: 'coach-services', label: '服务', meta: `${Object.keys(state.services.drafts).length} 个服务包` },
    { key: 'coach-review', label: '计划审阅', meta: coachChanges.value.length ? `${coachChanges.value.length} 处改动` : '暂无改动' },
    { key: 'coach-edit', label: '计划编辑', meta: `草稿版本 ${state.coach.draftVersion}` },
  ] as const
  return { student, coach }
})

export interface PreviewOption {
  key: string
  label: string
}

export const PREVIEW_STATES: Record<Screen, PreviewOption[]> = {
  today: [
    { key: 'scheduled', label: '已安排' },
    { key: 'in_progress', label: '进行中' },
    { key: 'complete', label: '已完成' },
    { key: 'rest_day', label: '休息日' },
    { key: 'paused', label: '已暂停' },
    { key: 'cycle_end', label: '周期结束' },
    { key: 'unscheduled', label: '未安排' },
    { key: 'offline', label: '内容不可用' },
  ],
  explore: [
    { key: 'normal', label: '正常' },
    { key: 'loading', label: '加载中' },
    { key: 'error', label: '加载失败' },
    { key: 'empty', label: '无结果' },
    { key: 'removed', label: '内容下架' },
  ],
  planDetail: [
    { key: 'normal', label: '已登录成年' },
    { key: 'anonymous', label: '匿名浏览' },
    { key: 'unknown-age', label: '年龄未知' },
    { key: 'minor', label: '未成年' },
    { key: 'removed', label: '已下架' },
  ],
  training: [
    { key: 'normal', label: '正常' },
    { key: 'no-history', label: '无历史' },
    { key: 'sync-pending', label: '待同步' },
    { key: 'sync-failed', label: '同步失败' },
    { key: 'conflict', label: '同组冲突' },
    { key: 'complete', label: '全部完成' },
  ],
  records: [
    { key: 'normal', label: '正常' },
    { key: 'empty', label: '无记录' },
    { key: 'single', label: '仅一条' },
    { key: 'insufficient', label: '信息不足' },
    { key: 'late', label: '补记' },
  ],
  messages: [
    { key: 'normal', label: '正常' },
    { key: 'empty', label: '空' },
    { key: 'sending', label: '发送中' },
    { key: 'failed', label: '发送失败' },
    { key: 'readonly', label: '只读' },
    { key: 'revoked', label: '已撤权' },
    { key: 'pending', label: '关系待确认' },
  ],
  incomingPlan: [
    { key: 'new', label: '新版本' },
    { key: 'adopted', label: '已采用' },
    { key: 'expired', label: '已过期' },
    { key: 'conflict', label: '内容冲突' },
    { key: 'no-permission', label: '无权限' },
  ],
  studentPlan: [],
  myPlan: [
    { key: 'normal', label: '正常' },
    { key: 'empty', label: '空库' },
    { key: 'conflict', label: '版本冲突' },
    { key: 'missing', label: '缺必要项' },
    { key: 'pending', label: '待审核' },
    { key: 'taken_down', label: '已下架' },
    { key: 'saving', label: '保存中' },
    { key: 'save-failed', label: '保存失败' },
  ],
  aiAssist: [
    { key: 'ready', label: '可应用' },
    { key: 'needs_input', label: '需补充' },
    { key: 'safety_block', label: '安全阻断' },
    { key: 'tech_error', label: '技术失败' },
    { key: 'quota', label: '额度不足' },
    { key: 'expired', label: '版本过期' },
    { key: 'duplicate', label: '重复确认' },
  ],
  diet: [
    { key: 'unrecorded', label: '未记录' },
    { key: 'recorded', label: '已自评' },
    { key: 'observation', label: '补充观察' },
    { key: 'insufficient', label: '信息不足' },
    { key: 'save-failed', label: '保存失败' },
    { key: 'risk', label: '内容提示' },
    { key: 'revoked', label: '已撤权' },
  ],
  coachDetail: [
    { key: 'normal', label: '正常' },
    { key: 'unavailable', label: '渠道不可用' },
  ],
  coachTodo: [
    { key: 'normal', label: '正常' },
    { key: 'empty', label: '无待办' },
  ],
  coachStudentDetail: [{ key: 'normal', label: '正常' }],
  coachStudents: [
    { key: 'normal', label: '正常' },
    { key: 'empty', label: '空' },
    { key: 'unauthorized', label: '未授权' },
    { key: 'ended', label: '关系结束' },
    { key: 'stale', label: '数据过期' },
  ],
  coachPlan: [
    { key: 'normal', label: '正常' },
    { key: 'safety', label: '安全阻断' },
    { key: 'tech', label: '技术失败' },
    { key: 'conflict', label: '版本冲突' },
    { key: 'publishing', label: '发布中' },
    { key: 'delivered', label: '已交付' },
    { key: 'ai-draft', label: 'AI 草稿' },
  ],
  coachServices: [
    { key: 'normal', label: '正常' },
    { key: 'saving', label: '保存中' },
    { key: 'failed', label: '保存失败' },
    { key: 'preview', label: '学员预览' },
  ],
}

export function previewStateFor(screen: Screen): string {
  switch (screen) {
    case 'today':
      return state.todayMode
    case 'coachPlan':
      return state.preview.coachPlan
    case 'explore':
      return state.preview.explore
    case 'planDetail':
      return state.preview.planDetail
    case 'training':
      return state.preview.training
    case 'records':
      return state.preview.records
    case 'messages':
      return state.preview.messages
    case 'incomingPlan':
      return state.preview.incomingPlan
    case 'studentPlan':
      return 'current'
    case 'myPlan':
      return state.preview.myPlan
    case 'aiAssist':
      return state.preview.ai
    case 'diet':
      return state.preview.diet
    case 'coachDetail':
      return state.preview.coachDetail
    case 'coachTodo':
      return state.preview.coachTodo
    case 'coachStudents':
      return state.preview.coachStudents
    case 'coachServices':
      return state.preview.coachServices
    default:
      return 'normal'
  }
}

export function todayModeLabel(mode: TodayMode): string {
  return (
    {
      scheduled: '今天已安排',
      in_progress: '记录草稿',
      complete: '今天已完成',
      rest_day: '今天休息',
      paused: '稍后填写',
      cycle_end: '本周期已结束',
      unscheduled: '今天未安排',
      offline: '内容暂不可用',
    }[mode] ?? mode
  )
}

/* ------------------------------------------------------------------ */
/* 训练                                                                */
/* ------------------------------------------------------------------ */

export const totalSets = computed(() => totalSetCount(state.session))
export const doneSets = computed(() => doneSetCount(state.session))
export const progressPercent = computed(() =>
  totalSets.value ? (doneSets.value / totalSets.value) * 100 : 0,
)
export const currentEntry = computed<SetEntry | null>(() => firstPendingEntry(state.session))
export const currentInput = computed<SetInput | null>(() => {
  const entry = currentEntry.value
  if (!entry) return null
  if (!state.inputs[entry.set.id]) {
    state.inputs[entry.set.id] = emptyInput()
  }
  return state.inputs[entry.set.id]
})
export const isComplete = computed(() => state.session.status === 'complete')
export const currentExerciseProgress = computed(() => {
  const entry = currentEntry.value
  return entry ? exerciseProgress(entry.exercise) : null
})

export const activeConflict = computed(() => state.session.conflicts[0] ?? null)

export function setInputValue(setId: string, field: keyof SetInput, value: string): void {
  const row = findLogSet(setId)
  const input = state.inputs[setId] ?? (row ? logInput(row.exercise, row.set) : emptyInput())
  if (input[field] === value) return
  input[field] = value
  state.inputs[setId] = input
  if (row) {
    row.set.status = 'pending'
    row.set.completedAt = null
  }
  state.session.status = 'in_progress'
  state.session.completedAt = null
  state.todayMode = 'in_progress'
  if (Object.keys(ui.trainingErrors).length) {
    const entry = currentEntry.value
    if (entry && entry.set.id === setId) {
      ui.trainingErrors = validateSetInput(entry.exercise.kind, input)
    }
  }
}

/** Compatibility entry for existing callers; no pacing or timer side effects. */
export function completeCurrentSet(): boolean {
  const entry = currentEntry.value
  return entry ? recordLogSet(entry.set.id) : false
}

export const logErrors = reactive<Record<string, import('../types').SetInputErrors>>({})
export const logMessage = reactive({ text: '', error: false })

function findLogSet(id: string): SetEntry | null {
  for (const exercise of state.session.exercises) {
    const set = exercise.sets.find(item => item.id === id)
    if (set) return { exercise, set }
  }
  return null
}

export function localToday(): string {
  const date = new Date()
  return `${date.getFullYear()}-${String(date.getMonth()+1).padStart(2,'0')}-${String(date.getDate()).padStart(2,'0')}`
}

export function logInput(exercise: import('../types').SessionExercise, set: import('../types').SessionSet): SetInput {
  return state.inputs[set.id] ?? (set.status === 'done' ? inputFromValues(exercise.kind, set) : emptyInput())
}

export function setRecordingMode(mode: 'after' | 'set'): void {
  state.recordingMode = mode
  logMessage.text = ''
  persistNow()
}

export function setTrainingDate(value: string): boolean {
  const candidate = new Date(value + 'T12:00:00')
  const valid = /^\d{4}-\d{2}-\d{2}$/.test(value) &&
    Number.isFinite(candidate.getTime()) &&
    `${candidate.getFullYear()}-${String(candidate.getMonth()+1).padStart(2,'0')}-${String(candidate.getDate()).padStart(2,'0')}` === value && value <= localToday()
  if (!valid) {
    logMessage.text = '请选择今天或更早的有效训练日期。'
    logMessage.error = true
    return false
  }
  if(state.session.trainingDate !== value) {
    state.session.status = 'in_progress'
    state.session.completedAt = null
    state.todayMode = 'in_progress'
  }
  state.session.trainingDate = value
  const date = new Date(value + 'T12:00:00')
  state.session.dateLabel = date.toLocaleDateString('zh-CN', {month:'long',day:'numeric',weekday:'short'})
  logMessage.text = ''
  schedulePersist()
  return true
}

export function usePreviousLogSet(setId: string): void {
  const row = findLogSet(setId)
  if (!row) return
  state.inputs[setId] = referenceInput(row.exercise.kind, row.exercise.lastRef)
  delete logErrors[setId]
}

export function recordLogSet(setId: string): boolean {
  const row = findLogSet(setId)
  if (!row || activeConflict.value) return false
  const input = logInput(row.exercise, row.set)
  const errors = validateSetInput(row.exercise.kind, input)
  logErrors[setId] = errors
  ui.trainingErrors = errors
  if (hasErrors(errors)) return false
  const values = appliedValues(row.exercise.kind,input)
  const unchanged = row.set.status === 'done' && Object.entries(values).every(([key,value]) => row.set[key as keyof typeof values] === value)
  Object.assign(row.set,values,{status:'done',completedAt:unchanged ? row.set.completedAt : nowIso()})
  state.inputs[setId] = { ...input }
  state.session.status = 'in_progress'
  state.session.completedAt = null
  state.todayMode = 'in_progress'
  logMessage.text = `${row.exercise.name} · 第 ${row.set.index} 组已记录`
  logMessage.error = false
  const saved = persistNow()
  if (!saved) { logMessage.text = '未能保存到本机，输入仍保留。请重试。'; logMessage.error = true }
  return saved
}

export function addLogSet(exerciseId: string): string | null {
  const exercise = state.session.exercises.find(item => item.id === exerciseId)
  if (!exercise || exercise.sets.length >= 30) return null
  const id = `log-set-${++state.sessionSequence}-${Date.now()}`
  exercise.sets.push({id,index:exercise.sets.length+1,status:'pending',weightKg:null,reps:null,seconds:null,completedAt:null})
  state.inputs[id] = emptyInput()
  state.session.status = 'in_progress'
  logMessage.text = ''
  return id
}

export function removeLogSet(setId: string): void {
  const row = findLogSet(setId)
  if (!row) return
  if (state.session.conflicts.some(item => item.setId === setId)) { logMessage.text = '请先处理这组的记录冲突。'; logMessage.error = true; return }
  row.exercise.sets = row.exercise.sets.filter(item => item.id !== setId)
  row.exercise.sets.forEach((item,index) => { item.index = index+1 })
  delete state.inputs[setId]
  delete logErrors[setId]
  state.session.status = 'in_progress'
}

export function addLogExercise(name: string, kind: LoadKind = 'weight'): string | null {
  const trimmed = name.trim()
  if (!trimmed || trimmed.length > 60 || state.session.exercises.length >= 30) return null
  const id = `log-exercise-${++state.sessionSequence}-${Date.now()}`
  state.session.exercises.push({id,name:trimmed,kind,equipment:'',cue:'',restSeconds:0,
    targetText:'按实际完成填写',lastRef:{dateLabel:'暂无可比较参考'},sets:[]})
  addLogSet(id)
  return id
}

export function removeLogExercise(id: string): void {
  const exercise = state.session.exercises.find(item => item.id === id)
  if (exercise?.sets.some(set => state.session.conflicts.some(item => item.setId === set.id))) { logMessage.text = '请先处理这个动作的记录冲突。'; logMessage.error = true; return }
  exercise?.sets.forEach(set => { delete state.inputs[set.id]; delete logErrors[set.id] })
  state.session.exercises = state.session.exercises.filter(item => item.id !== id)
  state.session.status = 'in_progress'
}

export function saveTrainingLog(): boolean {
  logMessage.text = ''
  logMessage.error = true
  if (activeConflict.value) { logMessage.text = '请先处理记录冲突。'; return false }
  if (!setTrainingDate(state.session.trainingDate ?? localToday())) return false
  const rows = state.session.exercises.flatMap(exercise => exercise.sets.map(set => ({exercise,set})))
  const filled = rows.filter(row => Object.values(logInput(row.exercise,row.set)).some(value => value.trim()))
  if (!filled.length) { logMessage.text = '至少填写一组实际完成的数据。'; return false }
  let invalid = false
  Object.keys(logErrors).forEach(key => delete logErrors[key])
  for (const row of filled) {
    const errors = validateSetInput(row.exercise.kind,logInput(row.exercise,row.set))
    logErrors[row.set.id] = errors
    if (hasErrors(errors)) invalid = true
  }
  if (invalid) { logMessage.text = '还有填写不完整或需要核对的组，请检查标红位置。'; return false }
  const recordedAt = nowIso()
  for (const row of rows) {
    if (!filled.includes(row)) {
      Object.assign(row.set,{status:'pending',weightKg:null,reps:null,seconds:null,completedAt:null})
      continue
    }
    const values = appliedValues(row.exercise.kind,logInput(row.exercise,row.set))
    const unchanged = row.set.status === 'done' && Object.entries(values).every(([key,value]) => row.set[key as keyof typeof values] === value)
    Object.assign(row.set,values,{status:'done',completedAt:unchanged ? row.set.completedAt : recordedAt})
  }
  state.session.status = 'complete'
  state.session.completedAt = recordedAt
  state.todayMode = 'complete'
  state.preview.training = 'complete'
  const record = sessionRecord()!
  state.records = [record, ...state.records.filter(item => item.id !== record.id)]
  const saved = persistNow()
  logMessage.error = !saved
  logMessage.text = saved ? `已保存 ${filled.length} 组，空白组未计入。` : '未能保存到本机，输入仍在当前页面。请重试保存。'
  return saved
}

export function resumePausedTraining(): void {
  state.todayMode = 'in_progress'
  state.preview.today = 'in_progress'
  enterTraining()
}

export function startScheduledTraining(): void {
  // Opening a log never resets an existing draft.
  state.session.status = 'in_progress'
  state.session.completedAt = null
  state.todayMode = 'in_progress'
  state.preview.today = 'in_progress'
  enterTraining()
}

export function recoverTodayContent(): void {
  state.todayMode = 'in_progress'
  state.preview.today = 'in_progress'
}

export function resolveSetConflict(choice: 'local' | 'remote'): boolean {
  const conflict = state.session.conflicts[0]
  if (!conflict) return false
  let found: { exerciseId: string; setIndex: number } | null = null
  for (const exercise of state.session.exercises) {
    const set = exercise.sets.find((item) => item.id === conflict.setId)
    if (set) found = { exerciseId: exercise.id, setIndex: set.index }
  }
  if (!found) return false
  const exercise = state.session.exercises.find((item) => item.id === found!.exerciseId)
  const set = exercise?.sets.find((item) => item.id === conflict.setId)
  if (!exercise || !set) return false
  if (choice === 'remote') {
    const numbers = conflict.remoteText.match(/(\d+(?:\.\d+)?)/g) ?? []
    if (isDurationKind(exercise.kind)) {
      set.seconds = Number(numbers[0] ?? 0) || null
      set.weightKg = null
      set.reps = null
    } else if (isBodyweightKind(exercise.kind)) {
      set.weightKg = null
      set.reps = Number(numbers[0] ?? 0) || null
      set.seconds = null
    } else {
      set.weightKg = Number(numbers[0] ?? 0) || null
      set.reps = Number(numbers[1] ?? 0) || null
      set.seconds = null
    }
  } else {
    const input = logInput(exercise,set)
    const errors = validateSetInput(exercise.kind,input)
    if(hasErrors(errors)) {
      logErrors[set.id] = errors
      logMessage.text = '本机记录尚未填完整，请补齐或采用另一份记录。'
      logMessage.error = true
      return false
    }
    const values = appliedValues(exercise.kind, input)
    set.weightKg = values.weightKg
    set.reps = values.reps
    set.seconds = values.seconds
  }
  set.status = 'done'
  set.completedAt = nowIso()
  state.inputs[set.id] = inputFromValues(exercise.kind,set)
  delete logErrors[set.id]
  logMessage.text = ''
  state.session.conflicts = state.session.conflicts.filter((item) => item.setId !== conflict.setId)
  state.session.status = 'in_progress'
  state.session.completedAt = null
  state.preview.training = 'normal'
  ui.trainingErrors = {}
  return persistNow()
}

function resetSessionToPending(session: PersistedState['session']): void {
  for (const exercise of session.exercises) {
    for (const set of exercise.sets) {
      set.status = 'pending'
      set.weightKg = null
      set.reps = null
      set.seconds = null
      set.completedAt = null
      delete state.inputs[set.id]
    }
  }
  session.conflicts = []
}

function completeAllForPreview(): void {
  const session = createSeedSession()
  for (const exercise of session.exercises) {
    for (const set of exercise.sets) {
      const values = appliedValues(exercise.kind, suggestedInput(exercise))
      set.status = 'done'
      set.weightKg = values.weightKg
      set.reps = values.reps
      set.seconds = values.seconds
      set.completedAt = nextEventTime(session, exercise.restSeconds)
    }
  }
  session.status = 'complete'
  session.completedAt = session.exercises[session.exercises.length - 1]?.sets.at(-1)?.completedAt ?? nowIso()
  state.session = session
  state.inputs = {}
  ui.trainingErrors = {}
  state.todayMode = 'complete'
  state.preview.today = 'complete'
}

export const sessionGroups = computed(() => {
  return state.session.exercises.map((exercise) => {
    const progress = exerciseProgress(exercise)
    return { exercise, progress, sets: exercise.sets }
  })
})

export const todayFacts = computed(() => ({
  title: state.session.dayTitle,
  planTitle: state.session.planTitle,
  version: state.session.planVersion,
  durationMin: state.session.durationMin,
  exerciseCount: state.session.exercises.length,
  done: doneSets.value,
  total: totalSets.value,
  percent: progressPercent.value,
  complete: isComplete.value,
  nextSet: currentEntry.value?.set.index ?? null,
  currentExercise: currentEntry.value?.exercise.name ?? null,
  currentReference: currentEntry.value?.exercise.lastRef ?? null,
}))

export const weekSummary = computed(() => ({
  label: PREVIEW_WEEK_LABEL,
  days: WEEK_DAYS.map((day) => {
    if (day.key === 'sat') {
      return { ...day, state: isComplete.value ? 'done' : 'today' }
    }
    return { ...day, state: day.past && day.training ? 'done' : 'rest' }
  }),
  doneCount: 2 + (isComplete.value ? 1 : 0),
}))

/* ------------------------------------------------------------------ */
/* 记录与个人积累                                                      */
/* ------------------------------------------------------------------ */

function sessionRecord(): import('../types').TrainingRecord | null {
  if (state.session.status !== 'complete') return null
  return {
    id: state.session.id,
    date: state.session.trainingDate ?? PREVIEW_TODAY,
    dateLabel: state.session.dateLabel,
    dayTitle: state.session.dayTitle,
    planVersion: state.session.planVersion,
    completedAt: state.session.completedAt ?? nowIso(),
    lateRecorded: (state.session.trainingDate ?? PREVIEW_TODAY) < localToday(),
    partial: doneSetCount(state.session) < totalSetCount(state.session),
    partialNote: doneSetCount(state.session) < totalSetCount(state.session) ? '仅记录实际完成的组，空白组未计入。' : undefined,
    sets: state.session.exercises.flatMap((exercise) =>
      exercise.sets
        .filter((set) => set.status === 'done' && set.completedAt)
        .map((set) => ({
          id: set.id,
          exerciseId: exercise.id,
          exerciseName: exercise.name,
          kind: exercise.kind,
          weightKg: set.weightKg,
          reps: set.reps,
          seconds: set.seconds,
          completedAt: set.completedAt!,
        })),
    ),
  }
}

export const recordRows = computed(() => {
  const current = sessionRecord()
  const all = current ? [current, ...state.records] : [...state.records]
  return all
    .filter((record, index) => all.findIndex((item) => item.id === record.id) === index)
    .sort((a, b) => Date.parse(b.date) - Date.parse(a.date))
})

function rangeStartMs(range: '4w' | '8w' | '12w'): number {
  const weeks = range === '4w' ? 4 : range === '8w' ? 8 : 12
  return Date.parse(`${PREVIEW_TODAY}T00:00:00+08:00`) - weeks * 7 * 24 * 60 * 60 * 1000
}

export const recordsInRange = computed(() => {
  const start = rangeStartMs(ui.recordsRange)
  return recordRows.value.filter((record) => Date.parse(`${record.date}T00:00:00+08:00`) >= start)
})

export const recordExerciseOptions = computed(() => {
  const map = new Map<string, { id: string; name: string; kind: string }>()
  for (const record of recordRows.value) {
    for (const set of record.sets) {
      const name = set.exerciseName.trim()
      if (!map.has(name)) map.set(name, { id: name, name, kind: set.kind })
    }
  }
  return [...map.values()]
})

export const recordViewRows = computed(() => {
  if (state.preview.records === 'empty' || state.preview.records === 'insufficient') return []
  if (state.preview.records === 'single') return recordsInRange.value.slice(0, 1)
  return recordsInRange.value
})

export const recordChart = computed(() => {
  const option =
    recordExerciseOptions.value.find((item) => item.id === ui.recordsExerciseId) ??
    recordExerciseOptions.value[0]
  if (!option) return []
  const records = recordViewRows.value
    .slice()
    .sort((a, b) => Date.parse(a.date) - Date.parse(b.date))
  const entries = records
    .map((record) => ({
      record,
      sets: record.sets.filter((set) => set.exerciseName.trim() === option.name),
    }))
    .filter((entry) => entry.sets.length > 0)
  const latest = entries[entries.length - 1]
  if (!latest) return []
  const seriesKind =
    latest.sets.every((set) => set.kind === latest.sets[0]!.kind) ? latest.sets[0]!.kind : null
  if (!seriesKind) return []
  const points: Array<{ date: string; label: string; value: number; unit: string; recordId: string }> = []
  for (const { record, sets } of entries) {
    if (!sets.every((set) => set.kind === seriesKind)) continue
    const kind = seriesKind
    let value: number
    let unit: string
    if (isDurationKind(kind)) {
      value = Math.max(...sets.map((set) => set.seconds ?? 0))
      unit = isPerSideKind(kind) ? '秒/侧' : '秒'
    } else if (isBodyweightKind(kind)) {
      value = Math.max(...sets.map((set) => set.reps ?? 0))
      unit = kind === 'breath' ? '次呼吸' : isPerSideKind(kind) ? '次/侧' : '次'
    } else {
      value = Math.max(...sets.map((set) => set.weightKg ?? 0))
      unit = isPerSideKind(kind) ? 'kg/侧' : 'kg'
    }
    points.push({
      date: record.date,
      label: `${record.date.slice(5, 7)}/${record.date.slice(8, 10)}`,
      value,
      unit,
      recordId: record.id,
    })
  }
  return points
})

export const recordChartExercise = computed(
  () =>
    recordExerciseOptions.value.find((item) => item.id === ui.recordsExerciseId)?.name ??
    recordExerciseOptions.value[0]?.name ??
    '动作',
)

export const recordSummary = computed(() => {
  const points = recordChart.value
  const first = points[0]
  const last = points[points.length - 1]
  return {
    count: recordViewRows.value.length,
    sets: recordViewRows.value.reduce((sum, record) => sum + record.sets.length, 0),
    points,
    first,
    last,
    delta:
      points.length >= 2 && first && last
        ? Math.round((last.value - first.value) * 100) / 100
        : null,
    unit: last?.unit ?? 'kg',
    rangeLabel: ui.recordsRange === '4w' ? '近 4 周' : ui.recordsRange === '8w' ? '近 8 周' : '近 12 周',
  }
})

export const recordsFeedback = computed(() => {
  for (const record of recordViewRows.value) {
    if (record.feedback) return { record, feedback: record.feedback }
  }
  return null
})

export function setRecordsRange(range: '4w' | '8w' | '12w'): void {
  ui.recordsRange = range
}

export function setRecordsExercise(exerciseId: string): void {
  ui.recordsExerciseId = exerciseId
}

export function openRecord(recordId: string): void {
  if (recordId.startsWith('session-')) {
    state.screen = 'training'
    state.stack = [...state.stack, 'records']
    return
  }
  const record = recordRows.value.find((item) => item.id === recordId)
  if (!record) return
  ui.recordsExerciseId = record.sets[0]?.exerciseName ?? ui.recordsExerciseId
}

/* ------------------------------------------------------------------ */
/* 发现                                                                */
/* ------------------------------------------------------------------ */

export const currentFilters = computed<ExploreFilters>(() =>
  state.explore.mode === 'diet' ? state.explore.dietFilters : state.explore.trainingFilters,
)

export const filteredExplore = computed(() =>
  state.preview.explore === 'empty'
    ? []
    : filterExploreItems(
        EXPLORE_ITEMS,
        state.explore.mode === 'diet' ? 'diet' : 'training',
        currentFilters.value,
        ui.exploreQuery,
      ),
)
export const exploreFeatured = computed(() => filteredExplore.value[0] ?? null)
export const exploreRest = computed(() => filteredExplore.value.slice(1))
export const activeFilterCount = computed(() => {
  if (state.explore.mode === 'coach') {
    const filters = state.explore.coachFilters
    return [filters.goal, filters.mode, filters.price].filter((value) => value !== 'all').length
  }
  const filters = currentFilters.value
  let count = 0
  if (filters.goal !== 'all') count += 1
  if (state.explore.mode === 'training') {
    if (filters.equipment !== 'all') count += 1
    if (filters.duration !== 'all') count += 1
  }
  return count
})
export const filterGroups = computed(() => {
  if (state.explore.mode === 'coach') {
    return [
      { key: 'goal', label: '目标', options: COACH_GOALS },
      { key: 'equipment', label: '服务方式', options: COACH_MODES },
      { key: 'duration', label: '价格', options: COACH_PRICES },
    ]
  }
  if (state.explore.mode === 'diet') {
    return [{ key: 'goal', label: '场景', options: DIET_GOALS }]
  }
  return [
    { key: 'goal', label: '目标', options: TRAINING_GOALS },
    { key: 'equipment', label: '器械', options: TRAINING_EQUIPMENT },
    { key: 'duration', label: '单次时长', options: TRAINING_DURATION },
  ]
})
export const filterDraftResultCount = computed(() => {
  if (state.explore.mode === 'coach') {
    return filteredCoachesFor(ui.filterDraft.goal, ui.filterDraft.equipment, ui.filterDraft.duration)
      .length
  }
  return filterExploreItems(
    EXPLORE_ITEMS,
    state.explore.mode === 'diet' ? 'diet' : 'training',
    ui.filterDraft,
    ui.exploreQuery,
  ).length
})
export const expandedItem = computed(() => {
  if (!ui.expandedId) return null
  return filteredExplore.value.find((item) => item.id === ui.expandedId) ?? null
})

export const COACH_GOALS = [
  { value: 'all', label: '全部目标' },
  { value: 'strength', label: '力量基础' },
  { value: 'endurance', label: '耐力' },
  { value: 'mobility', label: '活动度' },
]
export const COACH_MODES = [
  { value: 'all', label: '全部方式' },
  { value: 'online', label: '线上' },
  { value: 'offline', label: '线下' },
]
export const COACH_PRICES = [
  { value: 'all', label: '全部价格' },
  { value: 'low', label: '¥200 内' },
  { value: 'mid', label: '¥201–600' },
  { value: 'high', label: '¥601 以上' },
]

function coachGoalTag(coachId: string): string {
  if (coachId === 'gu-yan') return 'strength'
  if (coachId === 'jiang-yao') return 'endurance'
  return 'mobility'
}

function lowestPrice(coach: import('../types').CoachProfile): number {
  const prices = coach.packages.filter((pack) => pack.visible && pack.priceYuan > 0).map((pack) => pack.priceYuan)
  return prices.length ? Math.min(...prices) : 0
}

function filteredCoachesFor(goal: string, mode: string, price: string) {
  return state.coaches.filter((coach) => {
    if (goal !== 'all' && coachGoalTag(coach.id) !== goal) return false
    if (mode === 'online' && !coach.supportsOnline) return false
    if (mode === 'offline' && coach.supportsOnline && !coach.city) return false
    const lowest = lowestPrice(coach)
    if (price === 'low' && lowest > 200) return false
    if (price === 'mid' && (lowest <= 200 || lowest > 600)) return false
    if (price === 'high' && lowest <= 600) return false
    return true
  })
}

export const filteredCoaches = computed(() =>
  state.preview.explore === 'empty'
    ? []
    : filteredCoachesFor(
        state.explore.coachFilters.goal,
        state.explore.coachFilters.mode,
        state.explore.coachFilters.price,
      ),
)

export const selectedCoach = computed(
  () => state.coaches.find((coach) => coach.id === ui.selectedCoachId) ?? state.coaches[0] ?? null,
)

export const selectedPlanItem = computed(
  () =>
    EXPLORE_ITEMS.find((item) => item.id === ui.selectedPlanId && item.type === 'training') ??
    EXPLORE_ITEMS.find((item) => item.type === 'training' && item.status === 'published') ??
    null,
)

export const selectedPlanDays = computed(() => selectedPlanItem.value?.days ?? [])

export const activePlanDay = computed(
  () =>
    selectedPlanDays.value.find((day) => day.id === ui.planDayId) ??
    selectedPlanDays.value[0] ??
    null,
)

export const planStartBlocked = computed(() => selectedPlanItem.value?.status === 'removed')

export function setExploreMode(mode: 'training' | 'diet' | 'coach'): void {
  if (state.explore.mode === mode) return
  state.explore.mode = mode
  ui.expandedId = null
  ui.exploreQuery = ''
}

export function applyFilterDraft(): void {
  if (state.explore.mode === 'coach') {
    state.explore.coachFilters = {
      goal: ui.filterDraft.goal,
      mode: ui.filterDraft.equipment,
      price: ui.filterDraft.duration,
    }
  } else if (state.explore.mode === 'training') {
    state.explore.trainingFilters = { ...ui.filterDraft }
  } else {
    state.explore.dietFilters = { ...ui.filterDraft }
  }
  ui.expandedId = null
  ui.sheet = null
}

export function resetFilters(): void {
  if (state.explore.mode === 'coach') {
    state.explore.coachFilters = { goal: 'all', mode: 'all', price: 'all' }
  } else if (state.explore.mode === 'training') {
    state.explore.trainingFilters = { ...DEFAULT_FILTERS }
  } else {
    state.explore.dietFilters = { ...DEFAULT_FILTERS }
  }
  ui.exploreQuery = ''
  ui.expandedId = null
}

export function relaxFilters(): void {
  if (state.explore.mode === 'coach') {
    const filters = state.explore.coachFilters
    if (filters.goal !== 'all') filters.goal = 'all'
    else if (filters.mode !== 'all') filters.mode = 'all'
    else if (filters.price !== 'all') filters.price = 'all'
    return
  }
  const filters = currentFilters.value
  if (filters.goal !== 'all') filters.goal = 'all'
  else if (state.explore.mode === 'training' && filters.equipment !== 'all') filters.equipment = 'all'
  else if (state.explore.mode === 'training' && filters.duration !== 'all') filters.duration = 'all'
  else ui.exploreQuery = ''
  ui.expandedId = null
}

export function toggleFavorite(id: string): void {
  const index = state.favorites.indexOf(id)
  if (index >= 0) state.favorites.splice(index, 1)
  else state.favorites.push(id)
}

export function toggleExpanded(id: string): void {
  ui.expandedId = ui.expandedId === id ? null : id
}

export function openPlanFromExplore(id: string, origin?: import('../types').FocusOrigin): void {
  const item = EXPLORE_ITEMS.find((entry) => entry.id === id)
  if (!item || item.type !== 'training') return
  ui.selectedPlanId = id
  ui.planDayId = item.days?.[0]?.id ?? ''
  ui.expandedId = null
  ui.focusOrigin = origin ?? null
  navigate('planDetail')
}

export function selectPlanDay(dayId: string): void {
  ui.planDayId = dayId
}

/* ------------------------------------------------------------------ */
/* 计划开始与替换                                                      */
/* ------------------------------------------------------------------ */

function kindOfTarget(target: string, equipment: string, planEquipment: string): LoadKind {
  const perSide = target.includes('/侧')
  if (target.includes('呼吸')) return 'breath'
  if (target.includes('秒')) return perSide ? 'durationPerSide' : 'duration'
  const bodyweight = equipment.includes('瑜伽垫') || planEquipment === 'none'
  if (perSide) return bodyweight ? 'bodyweightPerSide' : 'perSide'
  return bodyweight ? 'bodyweight' : 'weight'
}

function lastReferenceFor(name: string, kind: LoadKind): import('../types').LastReference {
  for (const record of recordRows.value) {
    const sets = record.sets.filter(
      (set) => set.exerciseName === name && set.kind === kind,
    )
    if (!sets.length) continue
    const set = sets[sets.length - 1]!
    const weighted = !isBodyweightKind(kind) && !isDurationKind(kind)
    return {
      dateLabel: record.dateLabel.replace(/ 周./, ''),
      ...(weighted && set.weightKg !== null && set.weightKg > 0
        ? { weightKg: set.weightKg }
        : {}),
      ...(isBodyweightKind(kind) || weighted ? { reps: set.reps ?? undefined } : {}),
      ...(isDurationKind(kind) ? { seconds: set.seconds ?? undefined } : {}),
      perSide: isPerSideKind(kind),
    }
  }
  return { dateLabel: '暂无可比较参考' }
}

export const planImpact = computed(() => {
  const item = selectedPlanItem.value
  if (!item) return { before: [] as ScheduleSlot[], after: [] as ScheduleSlot[], changed: [] as string[] }
  const before = state.student.schedule
  const after = createFutureSchedule({ title: item.title, cycle: '本周', days: item.days ?? [] }, state.student.planVersion + 1)
  const changed = after
    .filter((slot, index) => slot.dayTitle !== before[index]?.dayTitle)
    .map((slot) => `${slot.dateLabel} · ${slot.dayTitle}`)
  return { before, after, changed }
})

function runPendingAccess(): void {
  const pending = ui.pendingAccess
  ui.pendingAccess = null
  if (!pending) return
  if (pending.kind === 'startPlan') {
    requestStartPlan(pending.planId)
    return
  }
  if (pending.kind === 'enterTraining') {
    enterTraining()
    return
  }
  if (pending.kind === 'startScheduled') {
    startScheduledTraining()
    return
  }
  resumePausedTraining()
}

function ensureServiceAccess(pending: NonNullable<PendingAccess>): boolean {
  if (state.auth.signedIn && state.auth.age === 'adult') return true
  ui.pendingAccess = pending
  if (!state.auth.signedIn) {
    ui.accessStep = 'signin'
  } else if (state.auth.age === 'minor') {
    ui.accessStep = 'minor'
  } else {
    ui.accessStep = 'age'
  }
  ui.sheet = 'access'
  return false
}

export function completeLocalSignIn(): void {
  state.auth.signedIn = true
  if (state.auth.age === 'minor') {
    ui.accessStep = 'minor'
    return
  }
  ui.accessStep = 'age'
}

export function confirmAdultAccess(): void {
  state.auth.signedIn = true
  state.auth.age = 'adult'
  ui.accessStep = 'age'
  ui.sheet = null
  runPendingAccess()
}

export function declareMinor(): void {
  state.auth.signedIn = true
  state.auth.age = 'minor'
  ui.accessStep = 'minor'
}

export function cancelAccess(): void {
  ui.pendingAccess = null
  ui.focusOrigin = null
  ui.sheet = null
}

export function requestContinueTraining(): boolean {
  if (!ensureServiceAccess({ kind: 'enterTraining' })) return false
  enterTraining()
  return true
}

export function requestStartScheduled(): boolean {
  if (!ensureServiceAccess({ kind: 'startScheduled' })) return false
  startScheduledTraining()
  return true
}

export function requestResumeTraining(): boolean {
  if (!ensureServiceAccess({ kind: 'resume' })) return false
  resumePausedTraining()
  return true
}

export type StartPlanResult =
  | 'started'
  | 'auth'
  | 'minor'
  | 'replace'
  | 'removed'
  | 'duplicate'

function selectedDayFor(item: NonNullable<typeof selectedPlanItem.value>) {
  return item.days?.find((day) => day.id === ui.planDayId) ?? item.days?.[0] ?? null
}

export function requestStartPlan(planId: string): StartPlanResult {
  const item = EXPLORE_ITEMS.find((entry) => entry.id === planId)
  if (!item || item.type !== 'training') return 'removed'
  if (item.status === 'removed') {
    noteSaved('这份内容已下架，不能开始新的训练。', 'error')
    return 'removed'
  }
  ui.selectedPlanId = planId
  const day = selectedDayFor(item)
  if (!day) {
    noteSaved('这份内容缺少可开始的训练日。', 'error')
    return 'removed'
  }
  if (!ensureServiceAccess({ kind: 'startPlan', planId: item.id })) {
    return state.auth.age === 'minor' ? 'minor' : 'auth'
  }
  const activeSame =
    state.session.status === 'in_progress' &&
    state.session.planId === item.id &&
    state.session.dayId === day.id
  if (activeSame) return startPlanNow(item)
  const hasExistingArrangement =
    state.todayMode !== 'unscheduled' &&
    state.todayMode !== 'cycle_end' &&
    (state.student.schedule.length > 0 ||
      state.session.status === 'in_progress' ||
      state.session.status === 'complete')
  if (hasExistingArrangement) {
    ui.sheet = 'replace'
    return 'replace'
  }
  return startPlanNow(item)
}

export function confirmAdultStart(): void {
  const item = selectedPlanItem.value
  if (!item) return
  ui.confirmHash = `adult-${item.id}`
  ui.sheet = null
  requestStartPlan(item.id)
}

export function cancelPlanStart(): void {
  ui.sheet = null
  ui.confirmHash = null
}

export function confirmReplaceAndStart(): void {
  const item = selectedPlanItem.value
  if (!item) return
  startPlanNow(item)
  ui.sheet = null
}

function archiveSessionBeforeReplace(): void {
  const session = state.session
  const done = session.exercises.flatMap((exercise) =>
    exercise.sets
      .filter((set) => set.status === 'done' && set.completedAt)
      .map((set) => ({
        id: set.id,
        exerciseId: exercise.id,
        exerciseName: exercise.name,
        kind: exercise.kind,
        weightKg: set.weightKg,
        reps: set.reps,
        seconds: set.seconds,
        completedAt: set.completedAt!,
      })),
  )
  if (!done.length) return
  const id = session.id
  const previous = state.records.find(record => record.id === id)
  // A pending edit is a draft. Replacing the plan must retain its last saved
  // value, while incorporating any newly confirmed sets from per-set logging.
  const archivedSets = previous && session.status !== 'complete'
    ? [...new Map([...previous.sets,...done].map(set=>[set.id,set])).values()]
    : done
  state.records = state.records.filter(record => record.id !== id && record.id !== `archived-${id}`)
  const complete = session.status === 'complete'
  const partial = archivedSets.length < totalSetCount(session)
  const archiveDate = !complete && previous ? previous.date : session.trainingDate ?? PREVIEW_TODAY
  state.records.push({
    id,
    date: archiveDate,
    dateLabel: !complete && previous ? previous.dateLabel : session.dateLabel,
    dayTitle: session.dayTitle,
    planVersion: session.planVersion,
    completedAt: session.completedAt ?? done[done.length - 1]!.completedAt,
    lateRecorded: archiveDate < localToday(),
    partial,
    partialNote: complete
      ? (partial ? '仅记录实际完成的组，空白组未计入。' : undefined)
      : `开始其他安排时结束 · 已记录 ${archivedSets.length} / ${totalSetCount(session)} 组，历史保留`,
    sets: archivedSets,
  })
}

function equipmentForExercise(
  item: NonNullable<typeof selectedPlanItem.value>,
  exercise: PlanExercise,
): string {
  if (exercise.equipment) return exercise.equipment
  const name = exercise.name
  if (/高位下拉|坐姿划船|面拉/.test(name)) {
    return /面拉/.test(name) ? '龙门架' : name.includes('划船') ? '坐姿划船器' : '高位下拉器'
  }
  if (/卧推|哑铃划船|肩推|弯举/.test(name)) return '哑铃 · 平凳'
  if (/深蹲|硬拉/.test(name)) return '哑铃'
  if (/平板|死虫|鸟狗|侧桥|猫牛|伸展/.test(name)) return '瑜伽垫'
  return item.equipment === 'dumbbell' ? '哑铃' : '按计划器械'
}

function startPlanNow(item: NonNullable<typeof selectedPlanItem.value>): StartPlanResult {
  const day = selectedDayFor(item)
  if (!day) {
    noteSaved('这份内容缺少可开始的训练日。', 'error')
    return 'removed'
  }
  if (
    state.session.status === 'in_progress' &&
    state.session.planId === item.id &&
    state.session.dayId === day.id
  ) {
    ui.sheet = null
    enterTraining()
    return 'duplicate'
  }
  archiveSessionBeforeReplace()
  state.sessionSequence += 1
  const sessionId = `session-${item.id}-${day.id}-20261024-${state.sessionSequence}`
  const exercises = day.exercises.map((exercise) => {
    const count = Math.max(1, Math.min(8, Number.parseInt(exercise.sets, 10) || 1))
    const equipment = equipmentForExercise(item, exercise)
    const kind = kindOfTarget(exercise.target, equipment, item.equipment)
    return {
      id: `${item.id}-${exercise.id}`,
      name: exercise.name,
      kind,
      equipment,
      cue: exercise.note || '按计划完成；出现疼痛时停止并记录。',
      restSeconds: Number.parseInt(exercise.restSeconds, 10) || 60,
      targetText: exercise.target,
      lastRef: lastReferenceFor(exercise.name, kind),
      sets: Array.from({ length: count }, (_, index) => ({
        id: `s-${item.id}-${exercise.id}-${index + 1}`,
        index: index + 1,
        status: 'pending' as const,
        weightKg: null,
        reps: null,
        seconds: null,
        completedAt: null,
      })),
    }
  })
  const session: PersistedState['session'] = {
    id: sessionId,
    planId: item.id,
    dayId: day.id,
    planVersion: state.student.planVersion + 1,
    planTitle: item.title,
    dayTitle: day.title,
    dateLabel: PREVIEW_DATE_LABEL,
    startedAt: nowIso(),
    durationMin: day.minutes ?? item.durationMin,
    status: 'in_progress',
    completedAt: null,
    exercises,
    trainingDate: localToday(),
    conflicts: [],
  }
  state.session = session
  state.inputs = {}
  ui.trainingErrors = {}
  const first = exercises[0]
  if (first?.sets[0]) state.inputs[first.sets[0].id] = emptyInput()
  state.student.plan = {
    title: item.title,
    cycle: '本周',
    days: structuredClone(item.days ?? []),
  }
  state.student.planVersion = session.planVersion
  state.student.schedule = createFutureSchedule(state.student.plan, state.student.planVersion)
  state.todayMode = 'in_progress'
  state.preview.today = 'in_progress'
  ui.sheet = null
  ui.confirmHash = null
  noteSaved('已准备记录，并保存到我的计划。')
  enterTraining()
  return 'started'
}

/* ------------------------------------------------------------------ */
/* 收到计划与采用                                                      */
/* ------------------------------------------------------------------ */

export function coachStateForStudent(studentId: string) {
  return state.activeCoachStudentId === studentId
    ? state.coach
    : state.coachSnapshots[studentId] ?? null
}

export function deliveriesForStudent(studentId: string): import('../types').PlanDelivery[] {
  return [...(coachStateForStudent(studentId)?.deliveries ?? [])].sort(
    (a, b) => b.version - a.version,
  )
}

export function latestDeliveryForStudent(studentId: string) {
  return deliveriesForStudent(studentId)[0] ?? null
}

export const incomingView = computed(() => {
  if (ui.incomingRefMissing) return null
  const history = ui.incomingHistoryVersion
  if (history !== null) {
    const latest = latestDeliveryForStudent(ui.incomingHistoryStudentId)
    const ownLatest =
      state.actor === 'student' &&
      !ui.incomingReadOnly &&
      ui.incomingHistoryStudentId === 'lin-che' &&
      latest?.version === history &&
      state.student.incoming?.version === history
    if (ownLatest) return state.student.incoming
    const delivery = deliveriesForStudent(ui.incomingHistoryStudentId).find(
      (item) => item.version === history,
    )
    if (!delivery) return null
    return {
      id: `delivery-${delivery.version}`,
      version: delivery.version,
      baseVersion: state.student.planVersion,
      content: delivery.content,
      hash: planHash(delivery.content),
      deliveredAt: delivery.deliveredAt,
      messageId: `m-plan-${delivery.version}`,
      status: 'new' as const,
      available: false,
      coachName: delivery.coachName ?? COACH_NAME,
    }
  }
  return state.student.incoming
})

export const incomingDelivery = incomingView

export const incomingIsHistorical = computed(() => {
  const view = incomingView.value
  const latest = latestDeliveryForStudent(ui.incomingHistoryStudentId)
  return Boolean(view && ui.incomingHistoryVersion !== null && view.version !== latest?.version)
})

export const incomingCoachName = computed(() => {
  const view = incomingView.value
  if (!view) return COACH_NAME
  return ('coachName' in view ? view.coachName : null) ?? COACH_NAME
})

export const incomingStudentName = computed(() => {
  if (!ui.incomingHistoryStudentId) return '学员'
  return (
    state.students.find((student) => student.id === ui.incomingHistoryStudentId)?.name ??
    STUDENT_NAME
  )
})

export const incomingBasePlan = computed(() => {
  if (state.actor === 'student' && ui.incomingHistoryStudentId === 'lin-che') {
    return state.student.plan
  }
  return coachStateForStudent(ui.incomingHistoryStudentId)?.published ?? state.student.plan
})

export const incomingChanges = computed(() => {
  const incoming = incomingView.value
  if (!incoming) return []
  return diffPlans(incomingBasePlan.value, incoming.content)
})

export const incomingAvailable = computed(() => {
  const incoming = incomingView.value
  if (!incoming || incomingIsHistorical.value || ui.incomingReadOnly || ui.incomingRefMissing)
    return false
  if (state.preview.incomingPlan === 'no-permission') return false
  if (
    incoming.status === 'adopted' ||
    state.preview.incomingPlan === 'expired' ||
    incoming.status === 'expired' ||
    !incoming.available
  )
    return false
  return true
})

export const incomingStatusLabel = computed(() => {
  const incoming = incomingView.value
  if (ui.incomingRefMissing) return '引用不可用'
  if (!incoming) return '暂无新版本'
  if (ui.incomingReadOnly) return `版本 ${incoming.version} · 只读快照`
  if (state.preview.incomingPlan === 'no-permission') return '无法查看或采用'
  if (incomingIsHistorical.value) {
    return `版本 ${incoming.version} · 已被版本 ${state.student.incoming?.version ?? ''} 替代`
  }
  if (state.preview.incomingPlan === 'expired' || incoming.status === 'expired') return '已过期'
  if (incoming.status === 'adopted') return '已采用'
  if (state.preview.incomingPlan === 'conflict') return '内容有冲突'
  return `版本 ${incoming.version} · 等待你的决定`
})

export const incomingImpact = computed(() => {
  const incoming = incomingView.value
  if (!incoming) return { sessions: 0, changed: [] as string[] }
  const nextSchedule = createFutureSchedule(incoming.content, incoming.version)
  const changed: string[] = []
  for (const slot of nextSchedule) {
    const previous = state.student.schedule.find((item) => item.date === slot.date)
    if (!previous || previous.dayTitle !== slot.dayTitle) {
      changed.push(`${slot.dateLabel} · ${slot.dayTitle}`)
    }
  }
  return { sessions: nextSchedule.length, changed }
})

export function openAdoptSheet(): void {
  const incoming = state.student.incoming
  if (!incoming || !incomingAvailable.value) {
    noteSaved('当前没有可以采用的版本。', 'error')
    return
  }
  ui.confirmHash = incoming.hash
  ui.conflictResolved = false
  ui.sheet = 'adopt'
}

export function confirmAdoption(): boolean {
  const incoming = state.student.incoming
  if (!incoming || !incomingAvailable.value || incoming.status === 'adopted') return false
  if (ui.confirmHash !== incoming.hash) {
    noteSaved('计划内容已变化，原来的确认不再有效；请重新核对完整内容。', 'error')
    return false
  }
  if (state.preview.incomingPlan === 'conflict' && !ui.conflictResolved) {
    ui.conflictResolved = true
    noteSaved('已记录你的核对。请再次确认采用版本 ' + incoming.version + '。')
    return false
  }
  const previous = {
    plan: state.student.plan,
    planVersion: state.student.planVersion,
    adoptedVersion: state.student.adoptedVersion,
    adoptedAt: state.student.adoptedAt,
    status: incoming.status,
    schedule: state.student.schedule,
  }
  state.student.plan = clonePlain(incoming.content)
  state.student.planVersion = incoming.version
  state.student.adoptedVersion = incoming.version
  state.student.adoptedAt = nowIso()
  state.student.schedule = createFutureSchedule(state.student.plan, incoming.version)
  incoming.status = 'adopted'
  const linCoach =
    state.activeCoachStudentId === 'lin-che' ? state.coach : state.coachSnapshots['lin-che']
  if (linCoach) linCoach.activeStudentVersion = incoming.version
  const relation = state.relations.find((item) => item.id === 'rel-gu-yan')
  if (relation) relation.planVersion = incoming.version
  if (!persistNow()) {
    state.student.plan = previous.plan
    state.student.planVersion = previous.planVersion
    state.student.adoptedVersion = previous.adoptedVersion
    state.student.adoptedAt = previous.adoptedAt
    state.student.schedule = previous.schedule
    incoming.status = previous.status
    noteSaved('未能保存采用状态，请重试。', 'error')
    return false
  }
  ui.sheet = null
  ui.confirmHash = null
  noteSaved(`已采用版本 ${incoming.version}；10月27日起按新安排，今天这次训练保持不变。`, 'positive')
  return true
}

export function keepCurrentPlan(): void {
  ui.sheet = null
  noteSaved('已暂时保留现有安排，教练的交付仍可稍后查看。')
}

export function resetIncomingToNew(): void {
  const seed = createSeedStudentState().incoming
  state.student.incoming = seed
  state.student.plan = clonePlain(seedCoachPublished())
  state.student.planVersion = 6
  state.student.adoptedVersion = null
  state.student.adoptedAt = null
  state.student.schedule = createFutureSchedule(state.student.plan, 6)
  state.coach.activeStudentVersion = 6
}

function seedCoachPublished(): PlanContent {
  return state.coach.published
}

/* ------------------------------------------------------------------ */
/* 我的计划（学员自己的内容）                                          */
/* ------------------------------------------------------------------ */

export const ownPlanContent = computed<PlanContent>(() => ({
  title: state.ownPlan.title,
  cycle: state.ownPlan.cycle,
  days: state.ownPlan.days,
}))

export const ownPlanSaved = computed<PlanContent>(() => {
  try {
    return JSON.parse(state.ownPlan.savedContent) as PlanContent
  } catch {
    return sanitizePlan(ownPlanContent.value)
  }
})

export const ownPlanDirty = computed(
  () => serializePlan(ownPlanContent.value) !== state.ownPlan.savedContent,
)

export const ownPlanIssues = computed<PlanValidationIssue[]>(() =>
  validatePlan(ownPlanContent.value),
)

export const ownPlanChanges = computed<PlanChange[]>(() => {
  const saved = ownPlanSaved.value
  return diffPlans(saved, sanitizePlan(ownPlanContent.value))
})

export const ownPlanHash = computed(() => planHash(ownPlanContent.value))

export const ownPlanStatusLabel = computed(() => {
  if (state.ownPlan.status === 'pending_review') return '已提交审阅 · 尚未公开'
  if (state.ownPlan.status === 'public') return '已公开'
  if (state.ownPlan.status === 'taken_down') return '已下架'
  return '草稿 · 仅自己可见'
})

export const ownPlanReviewInvalid = computed(
  () =>
    state.ownPlan.confirmedReviewHash !== null &&
    state.ownPlan.confirmedReviewHash !== ownPlanHash.value,
)

export const ownPlanPublicPreview = computed(() => {
  const content = sanitizePlan(ownPlanContent.value)
  return {
    title: content.title,
    cycle: content.cycle,
    days: content.days,
    healthNoteExcluded: state.ownPlan.healthNote.trim().length > 0,
    visibility: '公开后，所有人都能看到这些训练日与动作；健康备注不公开。',
  }
})

export function setOwnDay(dayId: string): void {
  ui.ownDayId = dayId
}

function ownExercise(dayId: string, exerciseId: string): PlanExercise | null {
  const day = state.ownPlan.days.find((item) => item.id === dayId)
  return day?.exercises.find((item) => item.id === exerciseId) ?? null
}

function invalidateOwnReview(): void {
  if (state.ownPlan.status === 'pending_review' || state.ownPlan.status === 'public') {
    state.ownPlan.status = 'draft'
    state.ownPlan.confirmedReviewHash = null
    noteSaved('内容已修改，之前的审阅确认已失效。请重新核对。')
  }
}

export function updateOwnField(
  dayId: string,
  exerciseId: string,
  field: 'name' | 'sets' | 'target' | 'restSeconds' | 'note',
  value: string,
): void {
  const exercise = ownExercise(dayId, exerciseId)
  if (!exercise) return
  exercise[field] = value
  invalidateOwnReview()
  noteSaved(null)
}

export function updateOwnDayTitle(dayId: string, value: string): void {
  const day = state.ownPlan.days.find((item) => item.id === dayId)
  if (!day) return
  day.title = value
  invalidateOwnReview()
  noteSaved(null)
}

export function updateOwnTitle(value: string): void {
  state.ownPlan.title = value
  invalidateOwnReview()
  noteSaved(null)
}

export function updateOwnHealthNote(value: string): void {
  state.ownPlan.healthNote = value
  noteSaved(null)
}

export function addOwnExercise(dayId = ui.ownDayId): void {
  const day = state.ownPlan.days.find((item) => item.id === dayId)
  if (!day) return
  const id = `own-new-${Date.now().toString(36)}`
  day.exercises.push({ id, name: '', sets: '3', target: '', restSeconds: '60', note: '' })
  invalidateOwnReview()
  noteSaved(null)
}

export function removeOwnExercise(dayId: string, exerciseId: string): void {
  const day = state.ownPlan.days.find((item) => item.id === dayId)
  if (!day) return
  const index = day.exercises.findIndex((item) => item.id === exerciseId)
  if (index < 0) return
  const [exercise] = day.exercises.splice(index, 1)
  ui.removeUndo = { dayId, index, exercise: exercise!, name: exercise!.name || '未命名动作' }
  if (removeUndoTimer !== null) window.clearTimeout(removeUndoTimer)
  removeUndoTimer = window.setTimeout(() => {
    ui.removeUndo = null
    removeUndoTimer = null
  }, 6000)
  invalidateOwnReview()
}

export function undoOwnRemove(): void {
  const undo = ui.removeUndo
  if (!undo) return
  const day = state.ownPlan.days.find((item) => item.id === undo.dayId)
  if (day && !day.exercises.some((item) => item.id === undo.exercise.id)) {
    day.exercises.splice(Math.min(undo.index, day.exercises.length), 0, undo.exercise)
  }
  ui.removeUndo = null
  if (removeUndoTimer !== null) window.clearTimeout(removeUndoTimer)
  removeUndoTimer = null
}

export function saveOwnPlan(): boolean {
  noteSaved(null)
  if (state.preview.myPlan === 'save-failed') {
    noteSaved('未能保存到本机，修改仍保留在页面中。请重试。', 'error')
    return false
  }
  const sanitized = sanitizePlan(ownPlanContent.value)
  const serialized = serializePlan(sanitized)
  if (serialized === state.ownPlan.savedContent) {
    noteSaved('没有需要保存的改动')
    return false
  }
  if (state.preview.myPlan === 'conflict' && !ui.conflictResolved) {
    noteSaved('这个内容已在另一个版本里更新。请先核对差异，再决定保留哪一份。', 'error')
    return false
  }
  const previous = {
    days: state.ownPlan.days,
    title: state.ownPlan.title,
    version: state.ownPlan.version,
    savedContent: state.ownPlan.savedContent,
    savedAt: state.ownPlan.savedAt,
  }
  state.ownPlan.title = sanitized.title
  state.ownPlan.days = sanitized.days
  state.ownPlan.version = previous.version + 1
  state.ownPlan.savedContent = serialized
  state.ownPlan.savedAt = nowIso()
  if (!persistNow()) {
    state.ownPlan.title = previous.title
    state.ownPlan.days = previous.days
    state.ownPlan.version = previous.version
    state.ownPlan.savedContent = previous.savedContent
    state.ownPlan.savedAt = previous.savedAt
    noteSaved('未能保存到本机，修改仍保留在页面中。请重试。', 'error')
    return false
  }
  noteSaved(`已保存 · ${formatTime(state.ownPlan.savedAt)}`)
  return true
}

export function openShareReview(): void {
  noteSaved(null)
  if (ownPlanIssues.value.length) {
    noteSaved(`先补全 ${ownPlanIssues.value.length} 处必要内容，再提交审阅。`, 'error')
    focusOwnIssue(ownPlanIssues.value[0]!)
    return
  }
  if (ownPlanDirty.value && !saveOwnPlan()) return
  ui.confirmHash = ownPlanHash.value
  ui.sheet = 'share'
}

function focusOwnIssue(issue: PlanValidationIssue): void {
  if (issue.dayId) ui.ownDayId = issue.dayId
}

export function confirmShareReview(): boolean {
  noteSaved(null)
  if (ownPlanIssues.value.length) {
    noteSaved('内容仍缺少必要项，未提交审阅。', 'error')
    ui.sheet = null
    return false
  }
  if (ui.confirmHash !== ownPlanHash.value) {
    noteSaved('内容在确认前发生了变化，原来的确认已失效；请重新核对。', 'error')
    ui.sheet = null
    return false
  }
  state.ownPlan.confirmedReviewHash = ownPlanHash.value
  state.ownPlan.status = 'pending_review'
  state.ownPlan.visibility = 'private'
  state.ownPlan.publishedContent = null
  ui.sheet = null
  noteSaved('已提交审阅，尚未公开。审核通过前不会出现在公开内容中。', 'positive')
  return true
}

export function resolveOwnConflict(choice: 'local' | 'remote'): boolean {
  noteSaved(null)
  if (!state.ownPlan.remoteVersion || !state.ownPlan.remoteContent) return false
  if (choice === 'remote') {
    state.ownPlan.days = clonePlain(state.ownPlan.remoteContent.days)
    state.ownPlan.title = state.ownPlan.remoteContent.title
    state.ownPlan.version = state.ownPlan.remoteVersion
    state.ownPlan.savedContent = serializePlan(state.ownPlan.remoteContent)
    state.ownPlan.savedAt = nowIso()
    state.ownPlan.remoteVersion = null
    state.ownPlan.remoteContent = null
    state.ownPlan.remoteAt = null
    ui.conflictResolved = false
    state.preview.myPlan = 'normal'
    noteSaved('已载入最新版本；本地未保存的修改已放弃。')
    persistNow()
    return true
  }
  ui.conflictResolved = true
  state.preview.myPlan = 'normal'
  const saved = saveOwnPlan()
  if (saved) {
    state.ownPlan.remoteVersion = null
    state.ownPlan.remoteContent = null
    state.ownPlan.remoteAt = null
    noteSaved('已保留你的修改并保存为新版本。')
  }
  return saved
}

export function restoreOwnPlanSeed(): void {
  const seed = createSeedOwnPlan()
  Object.assign(state.ownPlan, seed)
  ui.ownDayId = 'own-day-a'
}

/* ------------------------------------------------------------------ */
/* 消息                                                                */
/* ------------------------------------------------------------------ */

function relationFor(conversationId: string) {
  const conversation = state.conversations.find((item) => item.id === conversationId)
  if (!conversation) return null
  return state.relations.find((item) => item.id === conversation.relationId) ?? null
}

export function conversationForActor(): typeof state.conversations {
  if (state.actor === 'coach') {
    return state.conversations.filter((item) => item.coachId === 'gu-yan')
  }
  return state.conversations.filter((item) => item.studentId === 'lin-che')
}

export function conversationUnread(
  conversation: Conversation,
  actor: 'student' | 'coach' = state.actor,
): number {
  const cursor = conversation.readBy[actor]
  const start = cursor ? conversation.messages.findIndex((message) => message.id === cursor) : -1
  return conversation.messages
    .slice(start + 1)
    .filter((message) => message.status === 'sent' && message.author !== actor).length
}

export const currentConversation = computed(() =>
  conversationForActor().find((item) => item.id === ui.activeConversationId) ?? null,
)

export const activeConversation = computed(() => {
  const list = conversationForActor()
  return list.find((item) => item.id === ui.activeConversationId) ?? list[0] ?? null
})

export const activeRelation = computed(() => {
  const conversation = activeConversation.value
  if (!conversation) return null
  return relationFor(conversation.id)
})

export const conversationList = computed(() =>
  [...conversationForActor()].sort((a, b) => {
    const aTime = lastVisibleMessage(a)?.sentAt ?? a.updatedAt
    const bTime = lastVisibleMessage(b)?.sentAt ?? b.updatedAt
    return Date.parse(bTime) - Date.parse(aTime)
  }),
)

export const unreadTotal = computed(() =>
  conversationForActor().reduce(
    (sum, conversation) => sum + conversationUnread(conversation),
    0,
  ),
)

export function isHealthSensitiveMessage(message: import('../types').ChatMessage): boolean {
  if (message.health) return true
  return Boolean(
    message.refs?.some(
      (ref) => ref.kind === 'training',
    ),
  )
}

export function conversationHealthVisible(conversation: Conversation | null): boolean {
  if (!conversation) return false
  const relation = relationFor(conversation.id)
  return Boolean(
    relation &&
      relation.healthAuthorized &&
      relation.status === 'active' &&
      !relation.readOnly,
  )
}

export function messageVisibleToActor(
  conversation: Conversation,
  message: import('../types').ChatMessage,
): boolean {
  if (message.status !== 'sent' && message.author !== state.actor) return false
  if (state.actor !== 'coach') return true
  if (!isHealthSensitiveMessage(message)) return true
  return conversationHealthVisible(conversation)
}

export function visibleMessagesForActor(
  conversation: Conversation,
): import('../types').ChatMessage[] {
  return conversation.messages.filter(
    (message) => message.status === 'sent' || message.author === state.actor,
  )
}

export function lastVisibleMessage(
  conversation: Conversation,
): import('../types').ChatMessage | undefined {
  return visibleMessagesForActor(conversation).at(-1)
}

export function messageTextForActor(
  conversation: Conversation,
  message: import('../types').ChatMessage,
): string {
  if (message.redacted) return '健康内容已不可见'
  if (!messageVisibleToActor(conversation, message)) return '健康内容已不可见'
  return message.text
}

export function messageRefsForActor(
  conversation: Conversation,
  message: import('../types').ChatMessage,
): import('../types').MessageRef[] {
  if (!messageVisibleToActor(conversation, message)) return []
  return message.refs ?? []
}

export const messagesReadOnly = computed(() => {
  if (state.preview.messages === 'readonly') return true
  const relation = activeRelation.value
  if (!relation) return true
  return relation.readOnly || relation.status === 'ended'
})

export const messagesRelationPending = computed(() => {
  if (state.preview.messages === 'pending') return true
  const relation = activeRelation.value
  return relation?.status === 'pending'
})

export const activeConversationMessages = computed(() => activeConversation.value?.messages ?? [])

export function openConversation(id: string): void {
  const conversation = state.conversations.find((item) => item.id === id)
  if (!conversation) return
  ui.activeConversationId = id
  markConversationRead(id)
  ui.sheet = null
}

export function closeConversation(): void {
  ui.activeConversationId = null
}

export function setMessageDraft(conversationId: string, text: string): void {
  const conversation = state.conversations.find((item) => item.id === conversationId)
  if (conversation) conversation.drafts[state.actor === 'coach' ? 'coach' : 'student'] = text
}

export function sendMessage(conversationId = ui.activeConversationId): boolean {
  const conversation = state.conversations.find((item) => item.id === conversationId)
  if (!conversation) return false
  const relation = relationFor(conversation.id)
  if (!relation || relation.readOnly || relation.status === 'ended') return false
  if (state.preview.messages === 'readonly') return false
  const text = conversation.drafts[state.actor === 'coach' ? 'coach' : 'student'].trim()
  if (!text) return false
  const id = `msg-local-${Date.now().toString(36)}-${conversation.messages.length + 1}`
  const failed = state.preview.messages === 'failed'
  const sending = state.preview.messages === 'sending'
  conversation.messages.push({
    id,
    author: state.actor === 'coach' ? 'coach' : 'student',
    authorName: state.actor === 'coach' ? COACH_NAME : STUDENT_NAME,
    text,
    sentAt: nowIso(),
    status: failed ? 'failed' : sending ? 'sending' : 'sent',
  })
  conversation.drafts[state.actor === 'coach' ? 'coach' : 'student'] = ''
  conversation.updatedAt = nowIso()
  if (conversation.messages.find((message) => message.id === id)?.status === 'sent') {
    conversation.readBy[state.actor === 'coach' ? 'coach' : 'student'] = id
  }
  ui.lastSentMessageId = id
  if (!failed) state.preview.messages = 'normal'
  schedulePersist()
  return true
}

export function retryMessage(messageId: string, conversationId = ui.activeConversationId): boolean {
  const conversation = state.conversations.find((item) => item.id === conversationId)
  const message = conversation?.messages.find((item) => item.id === messageId)
  if (!conversation || !message) return false
  if (message.author !== state.actor) return false
  const relation = relationFor(conversation.id)
  if (!relation || relation.readOnly || relation.status === 'ended') return false
  message.status = 'sent'
  message.sentAt = nowIso()
  conversation.updatedAt = nowIso()
  markConversationRead(conversation.id)
  if (state.preview.messages === 'failed' || state.preview.messages === 'sending') {
    state.preview.messages = 'normal'
  }
  schedulePersist()
  return true
}

export function discardFailedMessage(messageId: string, conversationId = ui.activeConversationId): void {
  const conversation = state.conversations.find((item) => item.id === conversationId)
  if (!conversation) return
  if (conversation.messages.find((item) => item.id === messageId)?.author !== state.actor) return
  conversation.messages = conversation.messages.filter((item) => item.id !== messageId)
  schedulePersist()
}

export function markConversationRead(id: string): void {
  const conversation = state.conversations.find((item) => item.id === id)
  if (!conversation) return
  const lastSent = [...conversation.messages]
    .reverse()
    .find((message) => message.status === 'sent')
  conversation.readBy[state.actor] = lastSent?.id ?? null
  schedulePersist()
}

export function openMessageRef(
  ref: import('../types').MessageRef,
  conversationId?: string | null,
): void {
  const conversation =
    state.conversations.find((item) => item.id === (conversationId ?? ui.activeConversationId)) ??
    null
  const studentId = conversation?.studentId ?? null
  if (ref.kind === 'plan') {
    const version = Number.parseInt(ref.id.replace(/^delivery-/, ''), 10)
    if (!studentId || !Number.isFinite(version)) {
      noteSaved('这条计划引用缺少可识别的学员或版本，未打开最新版本。', 'error')
      return
    }
    if (state.actor === 'student' && studentId !== 'lin-che') {
      noteSaved('不能查看其他学员的计划版本。', 'error')
      return
    }
    goToIncomingPlan(version, studentId, state.actor === 'coach')
    return
  }
  if (ref.kind === 'training') {
    if (!studentId) {
      noteSaved('这条训练引用缺少所属学员，未打开默认记录。', 'error')
      return
    }
    if (state.actor === 'coach') {
      goToCoachStudentDetail(studentId)
      noteSaved('训练引用已定位到这位学员；具体组记录需学员本人授权后查看。', 'neutral')
      return
    }
    const record = recordRows.value.find((item) => item.id === ref.id)
    state.actor = 'student'
    ui.recordsFocusExplicit = true
    ui.recordsFocusId = record?.id ?? null
    if (record) {
      ui.recordsRange = '12w'
      ui.recordsExerciseId = record.sets[0]?.exerciseName ?? ui.recordsExerciseId
    } else {
      noteSaved('这条训练引用对应的记录不可用，未展开其他记录。', 'error')
    }
    navigate('records')
    return
  }
  if (state.actor === 'coach') {
    goToCoachServices()
    noteSaved('服务引用已打开教练服务页；学员侧的具体约定不变。', 'neutral')
    return
  }
  for (const coach of state.coaches) {
    const pack = coach.packages.find((item) => item.id === ref.id)
    if (pack) {
      goToCoachDetail(coach.id, pack.id)
      return
    }
  }
  noteSaved('这条服务引用对应的服务包已不可用。', 'error')
}

export function prefillConsultation(coachId: string, packageId: string): void {
  const coach = state.coaches.find((item) => item.id === coachId)
  const pack = coach?.packages.find((item) => item.id === packageId)
  if (!coach) return
  const packageName = pack?.name ?? '服务咨询'
  let conversation = state.conversations.find(
    (item) => item.coachId === coachId && item.kind === 'consult',
  )
  if (!conversation) {
    const relation = state.relations.find((item) => item.coachId === coachId)
    conversation = {
      id: `conv-consult-${coachId}`,
      coachId,
      coachName: coach.name,
      studentId: 'lin-che',
      serviceLabel: `${packageName} · 咨询`,
      packageId: pack?.id ?? null,
      relationId: relation?.id ?? `rel-consult-${coachId}`,
      messages: [],
      drafts: { student: '', coach: '' },
      readBy: { student: null, coach: null },
      updatedAt: nowIso(),
      kind: 'consult',
    }
    state.conversations.push(conversation)
  }
  conversation.packageId = pack?.id ?? conversation.packageId
  conversation.serviceLabel = `${packageName} · 咨询`
  conversation.drafts.student =
    conversation.drafts.student ||
    `你好，我想了解「${packageName}」。我每周能练 3 次，每次约 40 分钟，想先确认服务范围与时间安排。`
  ui.selectedCoachId = coachId
  ui.selectedPackageId = pack?.id ?? null
  ui.activeConversationId = conversation.id
  state.actor = 'student'
  navigate('messages')
}

export function revokeHealthAuthorization(relationId?: string): boolean {
  const relation =
    (relationId ? state.relations.find((item) => item.id === relationId) : activeRelation.value) ??
    state.relations.find((item) => item.id === 'rel-gu-yan')
  if (!relation || relation.status === 'ended') return false
  relation.healthAuthorized = false
  relation.healthRevokedAt = nowIso()
  state.diet.report.coachVisible = false
  persistNow()
  return true
}

function restoreHealthForPreview(): void {
  const relation =
    activeRelation.value ?? state.relations.find((item) => item.id === 'rel-gu-yan')
  if (!relation) return
  relation.healthAuthorized = true
  relation.healthRevokedAt = null
  relation.readOnly = false
  relation.status = 'active'
  state.diet.report.coachVisible = true
}

export function confirmHealthRevoke(): void {
  revokeHealthAuthorization(activeRelation.value?.id)
  ui.sheet = null
  noteSaved('已撤回健康内容授权。教练看不到此前的健康消息与饮食观察；普通安排沟通仍可继续。')
}

/* ------------------------------------------------------------------ */
/* 教练计划                                                            */
/* ------------------------------------------------------------------ */

export const activeCoachDay = computed(() => {
  return (
    state.coach.draft.days.find((day) => day.id === ui.coachDayId) ??
    state.coach.draft.days[0] ??
    null
  )
})
export const activeDayTotals = computed(() =>
  activeCoachDay.value ? dayTotals(activeCoachDay.value) : { exercises: 0, sets: 0 },
)
export const coachPlanTotals = computed(() => planTotals(state.coach.draft))
export const coachChanges = computed<PlanChange[]>(() =>
  diffPlans(state.coach.published, sanitizePlan(state.coach.draft)),
)
export const coachIssues = computed<PlanValidationIssue[]>(() => validatePlan(state.coach.draft))
export const coachDirty = computed(
  () => serializePlan(state.coach.draft) !== state.coach.savedContent,
)
export const coachDeliveries = computed(() =>
  [...state.coach.deliveries].sort((a, b) => b.version - a.version),
)
export const coachLatestDelivery = computed(() => coachDeliveries.value[0] ?? null)
export const coachSafetyBlock = computed(() => {
  if (state.preview.coachPlan !== 'safety') return null
  return '动作备注里记录了肩部疼痛。先核对动作范围与是否需要专业判断，再发布。'
})
export const coachTechError = computed(() =>
  state.preview.coachPlan === 'tech' ? '未能保存到本机。改动仍保留，可以重试。' : null,
)
export const coachConflictPreview = computed(() =>
  state.preview.coachPlan === 'conflict'
    ? {
        localVersion: state.coach.draftVersion,
        remoteVersion: state.coach.draftVersion + 1,
        text: '这位学员的内容在另一处更新过。当前改动仍保留，发布前需要重新核对差异。',
      }
    : null,
)
function currentCoachStudentName(): string {
  return (
    state.students.find((student) => student.id === state.activeCoachStudentId)?.name ??
    STUDENT_NAME
  )
}

export const coachStatus = computed(() => {
  const coach = state.coach
  const latest = coachLatestDelivery.value
  if (latest && latest.version === coach.draftVersion && !coachDirty.value) {
    return {
      tone: 'positive',
      text: `版本 ${latest.version} 已交付 ${currentCoachStudentName()}，等待采用`,
    }
  }
  if (coachDirty.value) return { tone: 'warning', text: '有未保存的改动' }
  if (latest && coach.draftVersion > latest.version) {
    return { tone: 'neutral', text: `草稿版本 ${coach.draftVersion} · 未交付` }
  }
  return {
    tone: 'neutral',
    text: coach.savedAt ? `草稿已保存 · ${formatDateTime(coach.savedAt)}` : '草稿尚未保存',
  }
})
export const coachPublishDisabled = computed(() => {
  const latest = coachLatestDelivery.value
  return Boolean(latest && latest.version === state.coach.draftVersion && !coachDirty.value)
})

export function setCoachMode(mode: CoachMode): void {
  ui.coachMode = mode
  ui.publishBlocked = false
  if (mode === 'review') {
    ui.flashDiff = true
    if (flashTimer !== null) window.clearTimeout(flashTimer)
    flashTimer = window.setTimeout(() => {
      ui.flashDiff = false
      flashTimer = null
    }, 340)
  }
}

export function selectCoachDay(dayId: string): void {
  ui.coachDayId = dayId
}

export function setActiveCoachDayId(dayId: string): void {
  ui.coachDayId = dayId
}

function noteSaved(message: string | null, tone: 'neutral' | 'error' | 'positive' = 'neutral'): void {
  ui.saveNote = message
  ui.saveNoteTone = message ? tone : null
  if (saveNoteTimer !== null) window.clearTimeout(saveNoteTimer)
  if (message) {
    saveNoteTimer = window.setTimeout(() => {
      ui.saveNote = null
      saveNoteTimer = null
    }, 5200)
  }
}

function findExercise(dayId: string, exerciseId: string): PlanExercise | null {
  const day = state.coach.draft.days.find((item) => item.id === dayId)
  return day?.exercises.find((item) => item.id === exerciseId) ?? null
}

export function updatePlanField(
  dayId: string,
  exerciseId: string,
  field: 'name' | 'sets' | 'target' | 'restSeconds' | 'note',
  value: string,
): void {
  const exercise = findExercise(dayId, exerciseId)
  if (!exercise) return
  exercise[field] = value
  noteSaved(null)
  ui.publishBlocked = false
  syncDraftWorkAfterMutation()
  if (state.preview.coachPlan === 'safety' || state.preview.coachPlan === 'tech') {
    state.preview.coachPlan = 'normal'
  }
}

export function updateDayTitle(dayId: string, value: string): void {
  const day = state.coach.draft.days.find((item) => item.id === dayId)
  if (!day) return
  day.title = value
  noteSaved(null)
  syncDraftWorkAfterMutation()
  ui.publishBlocked = false
}

function syncDraftWorkAfterMutation(): void {
  const latest = coachLatestDelivery.value
  if (
    latest &&
    state.coach.draftVersion === latest.version &&
    serializePlan(state.coach.draft) === state.coach.savedContent
  ) {
    syncPlanDraftWork(`已交付版本 ${latest.version}，等待采用`, true)
    return
  }
  syncPlanDraftWork('计划草稿有未保存改动', false)
}

export function addExercise(dayId: string): void {
  const day = state.coach.draft.days.find((item) => item.id === dayId)
  if (!day) return
  const exercise: PlanExercise = {
    id: `new-${Date.now().toString(36)}`,
    name: '',
    sets: '3',
    target: '',
    restSeconds: '60',
    note: '',
  }
  day.exercises.push(exercise)
  ui.coachMode = 'edit'
  ui.focusRequest = { dayId, exerciseId: exercise.id, field: 'name' }
  noteSaved(null)
  ui.publishBlocked = false
  syncDraftWorkAfterMutation()
}

export function removeExercise(dayId: string, exerciseId: string): void {
  const day = state.coach.draft.days.find((item) => item.id === dayId)
  if (!day) return
  const index = day.exercises.findIndex((item) => item.id === exerciseId)
  if (index < 0) return
  const [exercise] = day.exercises.splice(index, 1)
  const undo: RemoveUndo = { dayId, index, exercise: exercise!, name: exercise!.name || '未命名动作' }
  ui.removeUndo = undo
  if (removeUndoTimer !== null) window.clearTimeout(removeUndoTimer)
  removeUndoTimer = window.setTimeout(() => {
    ui.removeUndo = null
    removeUndoTimer = null
  }, 6000)
  noteSaved(null)
  ui.publishBlocked = false
  syncDraftWorkAfterMutation()
}

export function undoRemoveExercise(): void {
  const undo = ui.removeUndo
  if (!undo) return
  const day = state.coach.draft.days.find((item) => item.id === undo.dayId)
  if (day && !day.exercises.some((item) => item.id === undo.exercise.id)) {
    day.exercises.splice(Math.min(undo.index, day.exercises.length), 0, undo.exercise)
  }
  ui.removeUndo = null
  if (removeUndoTimer !== null) window.clearTimeout(removeUndoTimer)
  removeUndoTimer = null
  syncDraftWorkAfterMutation()
}

function activeCoachStudentRecord(): CoachStudent | null {
  return state.students.find((student) => student.id === state.activeCoachStudentId) ?? null
}

export function planWorkFor(student: CoachStudent): string {
  if (student.pendingWork?.includes('回复')) return student.pendingWork
  return student.planDraftStatus ?? student.pendingWork ?? ''
}

function syncPlanDraftWork(status: string | null, done?: boolean): void {
  const student = activeCoachStudentRecord()
  if (student) student.planDraftStatus = status
  if (done !== undefined && student) {
    const todo = state.todoItems.find(
      (item) => item.studentId === student.id && item.reason === 'draft',
    )
    if (todo) todo.done = done
  }
  schedulePersist()
}

export function saveDraft(): boolean {
  ui.publishBlocked = false
  const sanitized = sanitizePlan(state.coach.draft)
  const serialized = serializePlan(sanitized)
  if (serialized === state.coach.savedContent) {
    noteSaved('没有需要保存的改动')
    return false
  }
  const previousVersion = state.coach.draftVersion
  const previousSavedContent = state.coach.savedContent
  const previousSavedAt = state.coach.savedAt
  state.coach.draft = sanitized
  state.coach.draftVersion = previousVersion + 1
  state.coach.savedContent = serialized
  state.coach.savedAt = nowIso()
  if (!persistNow()) {
    state.coach.draftVersion = previousVersion
    state.coach.savedContent = previousSavedContent
    state.coach.savedAt = previousSavedAt
    noteSaved('未能保存到本机。请检查浏览器存储后重试。', 'error')
    return false
  }
  const latest = coachLatestDelivery.value
  if (latest && state.coach.draftVersion <= latest.version) {
    syncPlanDraftWork(`已交付版本 ${latest.version}，等待采用`, true)
  } else if (latest) {
    syncPlanDraftWork('计划草稿待发布', false)
  } else {
    syncPlanDraftWork('计划草稿已保存，尚未交付', false)
  }
  noteSaved(`草稿已保存 · ${formatTime(state.coach.savedAt)}`)
  return true
}

function focusIssue(issue: PlanValidationIssue): void {
  if (issue.dayId !== ui.coachDayId) ui.coachDayId = issue.dayId
  if (issue.field === 'instructions') {
    ui.coachMode = 'edit'
    ui.focusRequest = { dayId: issue.dayId, exerciseId: '', field: 'name' }
    return
  }
  ui.coachMode = 'edit'
  ui.focusRequest = {
    dayId: issue.dayId,
    exerciseId: issue.exerciseId ?? '',
    field: issue.field === 'dayTitle' ? 'dayTitle' : issue.field,
  }
}

export function focusValidationIssue(issue: PlanValidationIssue): void {
  focusIssue(issue)
}

export function requestPublish(): void {
  noteSaved(null)
  if (coachPublishDisabled.value) {
    noteSaved('这一版本已经交付，没有重复发送。', 'neutral')
    return
  }
  if (coachSafetyBlock.value) {
    ui.publishBlocked = true
    noteSaved(coachSafetyBlock.value, 'error')
    return
  }
  if (coachTechError.value) {
    ui.publishBlocked = true
    noteSaved('当前没有完成保存，未发布。', 'error')
    return
  }
  if (coachConflictPreview.value) {
    ui.publishBlocked = true
    noteSaved('内容存在版本冲突，重新核对后才能发布。', 'error')
    return
  }
  const issues = coachIssues.value
  if (issues.length) {
    saveDraft()
    ui.publishBlocked = true
    focusIssue(issues[0]!)
    return
  }
  if (coachDirty.value && !saveDraft()) return
  ui.publishBlocked = false
  ui.sheet = 'publish'
}

export function confirmPublish(): boolean {
  if (coachIssues.value.length) {
    ui.sheet = null
    ui.publishBlocked = true
    return false
  }
  if (coachSafetyBlock.value || coachTechError.value || coachConflictPreview.value) {
    ui.sheet = null
    requestPublish()
    return false
  }
  if (coachDirty.value && !saveDraft()) return false
  const version = state.coach.draftVersion
  if (coachLatestDelivery.value?.version === version) {
    ui.sheet = null
    noteSaved('这一版本已经交付，没有重复发送。')
    return false
  }
  const snapshot = structuredClone(sanitizePlan(state.coach.draft))
  const deliveredAt = nowIso()
  const studentId = state.activeCoachStudentId ?? 'lin-che'
  const student = state.students.find((item) => item.id === studentId)
  const previousPlanDraftStatus = student?.planDraftStatus
  const previousDraftTodo = state.todoItems.find(
    (item) => item.studentId === studentId && item.reason === 'draft',
  )?.done
  const previousDeliveries = state.coach.deliveries
  const previousIncoming = state.student.incoming
  state.coach.deliveries = [
    ...state.coach.deliveries.filter((item) => item.version !== version),
    { version, content: snapshot, deliveredAt, coachName: COACH_NAME },
  ]
  if (student) student.deliveredVersion = version
  const isLin = studentId === 'lin-che'
  if (isLin) {
    state.student.incoming = {
      id: `delivery-${version}`,
      version,
      baseVersion: state.student.planVersion,
      content: snapshot,
      hash: planHash(snapshot),
      deliveredAt,
      messageId: `m-plan-${version}`,
      status: 'new',
      available: true,
    }
  }
  const conversation =
    state.conversations.find(
      (item) => item.coachId === 'gu-yan' && item.studentId === studentId,
    ) ?? null
  if (conversation && !conversation.messages.some((item) => item.id === `m-plan-${version}`)) {
    const changeCount = coachChanges.value.length
    conversation.messages.push({
      id: `m-plan-${version}`,
      author: 'coach',
      authorName: COACH_NAME,
      text: `版本 ${version} 已经发给你：共 ${changeCount} 处调整。你看完整内容后再决定是否采用。`,
      sentAt: deliveredAt,
      status: 'sent',
      refs: [
        {
          kind: 'plan',
          id: `delivery-${version}`,
          label: `版本 ${version} · ${snapshot.title}`,
          meta: `${changeCount} 处调整 · 尚未采用`,
        },
      ],
    })
    conversation.readBy.coach = `m-plan-${version}`
    conversation.updatedAt = deliveredAt
  }
  if (!persistNow()) {
    state.coach.deliveries = previousDeliveries
    state.student.incoming = previousIncoming
    if (student) student.planDraftStatus = previousPlanDraftStatus
    const draftTodo = state.todoItems.find(
      (item) => item.studentId === studentId && item.reason === 'draft',
    )
    if (draftTodo && previousDraftTodo !== undefined) draftTodo.done = previousDraftTodo
    noteSaved('未能保存发布状态，请重试。', 'error')
    return false
  }
  ui.sheet = null
  syncPlanDraftWork(`已交付版本 ${version}，等待学员采用`, true)
  noteSaved(
    `已交付版本 ${version} 给 ${currentCoachStudentName()}。${currentCoachStudentName()}的现用版本 ${state.coach.activeStudentVersion} 仍未改变。`,
    'positive',
  )
  return true
}

export function consumeFocusRequest(): FocusRequest | null {
  const request = ui.focusRequest
  ui.focusRequest = null
  return request
}

export const recordsForSheet = computed(() =>
  state.session.exercises.map((exercise) => {
    const completed = exercise.sets.filter((set) => set.status === 'done')
    return {
      id: exercise.id,
      name: exercise.name,
      kind: exercise.kind,
      sets: completed,
      total: exercise.sets.length,
      progress: exerciseProgress(exercise),
    }
  }),
)

/* ------------------------------------------------------------------ */
/* 教练待办与学员                                                      */
/* ------------------------------------------------------------------ */

export const openTodo = computed(() =>
  state.todoItems
    .filter((item) => !item.done)
    .sort((a, b) => a.priority - b.priority || a.studentName.localeCompare(b.studentName)),
)
export const doneTodo = computed(() => state.todoItems.filter((item) => item.done))
export const todoCounts = computed(() => ({
  reply: openTodo.value.filter((item) => item.reason === 'reply').length,
  delivery: openTodo.value.filter((item) => item.reason === 'delivery').length,
  authorization: openTodo.value.filter((item) => item.reason === 'authorization').length,
  relation: openTodo.value.filter((item) => item.reason === 'relation').length,
  draft: openTodo.value.filter((item) => item.reason === 'draft').length,
}))

export function completeTodo(id: string): void {
  const item = state.todoItems.find((entry) => entry.id === id)
  if (!item) return
  item.done = true
  noteSaved(`已处理：${item.title}`)
}

export function restoreTodo(id: string): void {
  const item = state.todoItems.find((entry) => entry.id === id)
  if (!item) return
  item.done = false
}

export function openTodoItem(id: string): void {
  const item = state.todoItems.find((entry) => entry.id === id)
  if (!item) return
  state.actor = 'coach'
  ui.selectedStudentId = item.studentId
  if (item.targetScreen === 'messages') {
    if (item.conversationId) openConversation(item.conversationId)
    else navigate('messages')
    state.actor = 'coach'
    return
  }
  if (item.targetScreen === 'coachStudentDetail') {
    goToCoachStudentDetail(item.studentId)
    return
  }
  if (item.targetScreen === 'incomingPlan' || item.reason === 'delivery') {
    goToCoachStudentDetail(item.studentId)
    return
  }
  if (item.targetScreen === 'coachPlan') {
    goToCoachPlanForStudent(item.studentId)
    return
  }
  goToCoachStudents()
}

export const visibleStudents = computed(() => {
  switch (state.preview.coachStudents) {
    case 'empty':
      return []
    case 'unauthorized':
      return state.students.filter((student) => student.id === 'zhou-lan')
    case 'ended':
      return state.students.filter((student) => student.id === 'chen-mo')
    case 'stale':
      return state.students.filter((student) => student.id === 'zhou-lan')
    default:
      return state.students
  }
})

export const selectedStudent = computed(
  () => state.students.find((student) => student.id === ui.selectedStudentId) ?? state.students[0] ?? null,
)

export function relationForStudent(studentId: string) {
  const student = state.students.find((item) => item.id === studentId)
  return (
    state.relations.find((item) => item.id === student?.relationId) ??
    state.relations.find((item) => item.id === STUDENT_RELATIONS[studentId]) ??
    null
  )
}

export function healthAuthorizedFor(student: CoachStudent): boolean {
  const relation = relationForStudent(student.id)
  return Boolean(
    relation &&
      relation.healthAuthorized &&
      relation.status === 'active' &&
      !relation.readOnly &&
      !student.serviceEnded,
  )
}

export const selectedStudentHealthAuthorized = computed(() => {
  const student = selectedStudent.value
  return student ? healthAuthorizedFor(student) : false
})

export const selectedStudentEvidence = computed(() => {
  const student = selectedStudent.value
  if (!student) return []
  const authorized = healthAuthorizedFor(student)
  if (!authorized) {
    return student.records.filter((record) => record.kind === 'plan' && !record.private)
  }
  return student.records.filter((record) => !record.private)
})

export const selectedStudentRestriction = computed(() => {
  const student = selectedStudent.value
  if (!student) return null
  if (student.serviceEnded || student.relationStatus === 'ended') {
    return '服务关系已结束。历史计划与记录只读，不能再发起新的计划或查看健康内容。'
  }
  if (!selectedStudentHealthAuthorized.value) {
    return '健康内容未授权：只能查看计划与服务记录，健康记录和饮食观察不可见。'
  }
  if (state.preview.coachStudents === 'stale') {
    return '这里展示的是最近一次可见记录；健康内容与后续更新尚未同步。'
  }
  return null
})

export function openStudent(studentId: string): void {
  goToCoachStudentDetail(studentId)
}

export function replyToStudent(studentId: string): void {
  const student = state.students.find((item) => item.id === studentId)
  if (!student) return
  state.actor = 'coach'
  let conversation = state.conversations.find(
    (item) => item.coachId === 'gu-yan' && item.studentId === studentId,
  )
  if (!conversation) {
    const relation = relationForStudent(studentId)
    if (!relation || relation.status !== 'active') {
      noteSaved('这位学员还没有可用的站内会话；先确认服务关系。')
      return
    }
    conversation = {
      id: `conv-${studentId}`,
      coachId: 'gu-yan',
      coachName: COACH_NAME,
      studentId,
      serviceLabel: student.packageName,
      packageId: relation.packageId || null,
      relationId: relation.id,
      messages: [],
      drafts: { student: '', coach: '' },
      readBy: { student: null, coach: null },
      updatedAt: nowIso(),
      kind: 'service',
    }
    state.conversations.push(conversation)
  }
  openConversation(conversation.id)
  navigate('messages')
  state.actor = 'coach'
}

export function continueStudentPlan(studentId: string): void {
  goToCoachPlanForStudent(studentId)
}

/* ------------------------------------------------------------------ */
/* 教练服务内容与定价                                                  */
/* ------------------------------------------------------------------ */

export const serviceDraftList = computed(() => {
  const order =
    state.coaches
      .find((coach) => coach.id === 'gu-yan')
      ?.packages.map((pack) => pack.id) ?? []
  return Object.values(state.services.drafts).sort(
    (a, b) => order.indexOf(a.packageId) - order.indexOf(b.packageId),
  )
})

export const selectedServiceDraft = computed(
  () =>
    state.services.drafts[state.services.previewPackageId ?? 'pkg-strength'] ??
    serviceDraftList.value[0] ??
    null,
)

export const serviceDirty = computed(() =>
  Object.keys(state.services.drafts).some(
    (key) =>
      JSON.stringify(state.services.drafts[key]) !== JSON.stringify(state.services.saved[key]),
  ),
)

export const serviceAgreement = computed(() => state.relations.find((item) => item.id === 'rel-gu-yan') ?? null)

export const servicePriceIssues = computed<Record<string, string | null>>(() => {
  const issues: Record<string, string | null> = {}
  for (const [key, draft] of Object.entries(state.services.drafts)) {
    const raw = String(draft.priceInput ?? draft.priceYuan ?? '').trim()
    if (!raw) {
      issues[key] = '填写价格；0 表示免费'
    } else if (!/^\d+$/.test(raw)) {
      issues[key] = '价格需为 0–9999 的整数，不能是负数或小数'
    } else {
      const value = Number(raw)
      issues[key] = value >= 0 && value <= 9999 ? null : '价格需在 0–9999 之间'
    }
  }
  return issues
})

export const serviceValid = computed(() =>
  Object.values(servicePriceIssues.value).every((issue) => issue === null),
)

export function updateServiceField(
  packageId: string,
  field: 'description' | 'scope' | 'goalsText' | 'response' | 'priceInput' | 'priceUnit' | 'cycle',
  value: string,
): void {
  const draft = state.services.drafts[packageId]
  if (!draft) return
  if (field === 'priceInput') {
    draft.priceInput = String(value)
    const parsed = Number(String(value).trim())
    if (String(value).trim() && Number.isInteger(parsed) && parsed >= 0 && parsed <= 9999) {
      draft.priceYuan = parsed
    }
  } else if (field === 'priceUnit') {
    draft.priceUnit = String(value)
  } else if (field === 'cycle') {
    draft.cycle = String(value)
  } else if (field === 'description') {
    draft.description = String(value)
  } else if (field === 'scope') {
    draft.scope = String(value)
  } else if (field === 'goalsText') {
    draft.goalsText = String(value)
  } else {
    draft.response = String(value)
  }
  noteSaved(null)
}

export function saveServices(): boolean {
  noteSaved(null)
  if (state.preview.coachServices === 'failed') {
    noteSaved('未能保存服务内容，修改仍保留在页面中。请重试。', 'error')
    return false
  }
  if (!serviceValid.value) {
    noteSaved('先修正价格：需要 0–9999 的整数；0 表示免费。', 'error')
    return false
  }
  if (!serviceDirty.value) {
    noteSaved('没有需要保存的改动')
    return false
  }
  const previous = clonePlain(state.services)
  const changedKeys = Object.keys(state.services.drafts).filter(
    (key) => JSON.stringify(state.services.drafts[key]) !== JSON.stringify(state.services.saved[key]),
  )
  for (const key of changedKeys) {
    const draft = state.services.drafts[key]!
    const nextVersion = (state.services.saved[key]?.version ?? 1) + 1
    draft.version = nextVersion
    draft.priceYuan = Number(draft.priceInput.trim())
    state.services.saved[key] = clonePlain(draft)
    const coach = state.coaches.find((item) => item.id === 'gu-yan')
    const pack = coach?.packages.find((item) => item.id === draft.packageId)
    if (pack) {
      pack.summary = draft.description
      pack.priceYuan = draft.priceYuan
      pack.priceUnit = draft.priceUnit
      pack.cycle = draft.cycle
      pack.response = draft.response
      pack.includes = draft.scope
        .split(/[；;]/)
        .map((item) => item.trim())
        .filter(Boolean)
      pack.goals = draft.goalsText
        .split(/[·、,，]/)
        .map((item) => item.trim())
        .filter(Boolean)
      pack.visible = draft.visible
      pack.version = draft.version
    }
  }
  state.services.savedAt = nowIso()
  if (changedKeys.length) state.services.version += 1
  if (!persistNow()) {
    Object.assign(state.services, previous)
    noteSaved('未能保存服务内容，请重试。', 'error')
    return false
  }
  noteSaved('服务说明与价格已保存；已有服务约定的价格不变。', 'positive')
  return true
}

export function selectServicePackage(packageId: string): void {
  state.services.previewPackageId = packageId
}

export function toggleServiceVisible(packageId: string): void {
  const draft = state.services.drafts[packageId]
  if (!draft) return
  draft.visible = !draft.visible
  noteSaved(null)
}

export function previewServiceAsStudent(packageId: string): void {
  state.services.previewPackageId = packageId
  state.preview.coachServices = 'preview'
  ui.sheet = 'studentPreview'
}

/* ------------------------------------------------------------------ */
/* AI 整理与已有模板                                                   */
/* ------------------------------------------------------------------ */

export const activeAiTemplate = computed(
  () =>
    AI_TEMPLATES.find((item) => item.id === state.ai.templateId) ??
    AI_TEMPLATES.find((item) => item.context === state.ai.context) ??
    AI_TEMPLATES[0]!,
)

export const aiApplied = computed(() =>
  state.ai.appliedIds.includes(activeAiTemplate.value.id),
)

export const aiExpired = computed(
  () => (state.ai.baseVersion ?? activeAiTemplate.value.basePlanVersion) !== state.ownPlan.version,
)

export const aiStatus = computed<AiStatus>(() => {
  if (aiApplied.value) return 'duplicate'
  if (state.ai.context === 'coach') return state.preview.ai
  if (aiExpired.value && state.preview.ai !== 'tech_error' && state.preview.ai !== 'quota' && state.preview.ai !== 'safety_block' && state.preview.ai !== 'needs_input') return 'expired'
  return state.preview.ai
})

export const aiStatusCopy = computed(() => {
  switch (aiStatus.value) {
    case 'ready':
      return {
        tone: 'positive' as const,
        title: '候选已准备好',
        text: '候选来自已有模板，内容固定，可先核对改动。',
      }
    case 'needs_input':
      return {
        tone: 'warning' as const,
        title: '还缺一项信息',
        text: '需要先补充可用器械与不适部位，才能给出候选。',
      }
    case 'safety_block':
      return {
        tone: 'error' as const,
        title: '这项调整不能自助完成',
        text:
          state.ai.context === 'coach'
            ? '这是教练交付的计划，学员不能直接改写；可以把具体想法发给教练。'
            : '模板涉及需要专业判断的情况，不提供自助调整。原计划不会被修改。',
      }
    case 'tech_error':
      return {
        tone: 'error' as const,
        title: '候选没有生成完成',
        text: '这是技术问题，不是安全结论。原计划没有被修改，可以重试或手动调整。',
      }
    case 'quota':
      return {
        tone: 'warning' as const,
        title: '本周额度已用完',
        text: '额度不足不等于内容不安全；手动编辑与教练沟通仍可用。',
      }
    case 'expired':
      return {
        tone: 'warning' as const,
        title: '候选已过期',
        text: `候选来自版本 ${activeAiTemplate.value.basePlanVersion}，当前草稿已变化。请重新查看后再应用。`,
      }
    case 'duplicate':
      return {
        tone: 'neutral' as const,
        title: '这条建议已应用',
        text: '同一条固定候选只应用一次，不会重复修改。',
      }
    default:
      return { tone: 'neutral' as const, title: '', text: '' }
  }
})

export const aiCanApply = computed(
  () => aiStatus.value === 'ready' && !aiApplied.value && state.ai.context === 'own',
)

export function selectAiTemplate(id: string): void {
  state.ai.templateId = id
  ui.aiTemplateId = id
  state.ai.baseVersion = null
  const template = AI_TEMPLATES.find((item) => item.id === id)
  if (template) {
    state.ai.context = template.context
    state.ai.intent = template.intent
    state.preview.ai = template.status
  }
}

export function refreshAiCandidate(): void {
  state.ai.baseVersion = state.ownPlan.version
  state.preview.ai = 'ready'
  noteSaved('已按当前草稿版本重新查看候选。')
}

export function acceptAiInput(): void {
  state.ai.baseVersion = state.ownPlan.version
  state.preview.ai = 'ready'
  noteSaved('已补充必要信息，候选可以继续核对。')
}

export function setAiIntent(value: string): void {
  state.ai.intent = value
}

export function requestAiApply(): void {
  noteSaved(null)
  if (!aiCanApply.value) {
    if (aiStatus.value === 'duplicate') noteSaved('这条建议已经应用过，不会重复修改。')
    return
  }
  state.preview.ai = 'ready'
  ui.sheet = 'applyAi'
}

export function applyAiCandidate(): boolean {
  noteSaved(null)
  if (state.ai.context !== 'own') {
    noteSaved('教练计划只能请教练调整，不能由学员自助修改。', 'error')
    return false
  }
  if (aiApplied.value) {
    ui.sheet = null
    noteSaved('这条建议已经应用过，不会重复修改。')
    return false
  }
  if (aiStatus.value !== 'ready') {
    ui.sheet = null
    noteSaved(aiStatusCopy.value.text, 'error')
    return false
  }
  const changes = activeAiTemplate.value.changes
  for (const change of changes) {
    const day = state.ownPlan.days.find((item) => item.id === change.dayId)
    const exercise = day?.exercises.find((item) => item.id === change.exerciseId)
    if (!exercise) continue
    exercise[change.field] = change.after
  }
  invalidateOwnReview()
  state.ai.appliedIds = [...state.ai.appliedIds, activeAiTemplate.value.id]
  state.ai.appliedHash = planHash(ownPlanContent.value)
  state.ai.lastAppliedAt = nowIso()
  state.ai.confirmedVersion = state.ownPlan.version
  ui.sheet = null
  noteSaved('候选已改到我的计划草稿；现用安排没有改变。保存后可在我的计划中继续安排；公开审阅是另一个选择。', 'positive')
  return true
}

export function requestCoachAdjustment(): void {
  if (!activeConversation.value) {
    noteSaved('还没有可用的教练会话。', 'error')
    return
  }
  const conversation = activeConversation.value
  setMessageDraft(
    conversation.id,
    `你好，教练计划里的「${activeAiTemplate.value.title}」我想调整一下：${state.ai.intent}。可以帮我看一下吗？`,
  )
  ui.activeConversationId = conversation.id
  state.actor = 'student'
  navigate('messages')
}

/* ------------------------------------------------------------------ */
/* 饮食内容与当天自评                                                  */
/* ------------------------------------------------------------------ */

export const dietItem = computed(
  () =>
    EXPLORE_ITEMS.find((item) => item.id === state.diet.selectedItemId && item.type === 'diet') ??
    EXPLORE_ITEMS.find((item) => item.type === 'diet') ??
    null,
)

export const dietReport = computed(() => state.diet.report)

export const dietInsufficient = computed(
  () =>
    '没有记录实际食物、份量与确认摄入，无法给出热量或营养估算。自评只表示是否按计划。',
)

export const dietCoachVisibility = computed(() =>
  state.relations.find((item) => item.id === 'rel-gu-yan')?.healthAuthorized
    ? '教练可以看到这项自评与补充观察（已授权健康内容）。'
    : '教练看不到这项自评与补充观察（健康授权未开启或已撤回）；你自己的记录仍保留。',
)

export function setDietStatus(status: 'on_plan' | 'partial' | 'off_plan'): void {
  state.diet.report.status = status
  state.diet.saveState = 'idle'
  state.preview.diet = status === 'on_plan' ? 'recorded' : 'observation'
  noteSaved(null)
}

export function setDietObservation(value: string): void {
  state.diet.report.observation = value
  state.diet.saveState = 'idle'
  noteSaved(null)
}

export function saveDietReport(): boolean {
  noteSaved(null)
  if (!state.diet.report.status) {
    noteSaved('先选择今天的实际情况：按计划、部分按计划或未按计划。', 'error')
    return false
  }
  if (state.preview.diet === 'save-failed') {
    state.diet.saveState = 'failed'
    noteSaved('未能保存饮食自评，内容仍保留；可以重试。', 'error')
    return false
  }
  const previous = state.diet.report.savedAt
  state.diet.report.savedAt = nowIso()
  state.diet.report.coachVisible = Boolean(
    state.relations.find((item) => item.id === 'rel-gu-yan')?.healthAuthorized,
  )
  state.diet.saveState = 'saved'
  if (!persistNow()) {
    state.diet.report.savedAt = previous
    state.diet.saveState = 'failed'
    noteSaved('未能保存到本机，内容仍保留；可以重试。', 'error')
    return false
  }
  state.preview.diet = state.diet.report.status === 'on_plan' ? 'recorded' : 'observation'
  noteSaved('饮食自评已保存在本机。')
  return true
}

export function reopenDietReport(): void {
  if (state.diet.saveState === 'saved') state.preview.diet = 'recorded'
}

/* ------------------------------------------------------------------ */
/* 预览状态应用                                                        */
/* ------------------------------------------------------------------ */

function applyTodayMode(mode: TodayMode): void {
  state.preview.today = mode
  state.todayMode = mode
  if (mode === 'complete') {
    completeAllForPreview()
    return
  }
  const session = createSeedSession()
  if (mode === 'unscheduled' || mode === 'cycle_end') {
    state.student.schedule = []
  }
  if (mode === 'unscheduled' || mode === 'cycle_end' || mode === 'rest_day') {
    resetSessionToPending(session)
    session.status = 'in_progress'
  }
  if (mode === 'scheduled') {
    resetSessionToPending(session)
    session.status = 'in_progress'
    session.completedAt = null
    state.session = session
    state.inputs = {}
    const first = session.exercises[0]?.sets[0]
    if (first) state.inputs[first.id] = emptyInput()
  } else {
    state.session = session
    state.inputs = {}
    const first = session.exercises[0]?.sets.find((set) => set.status === 'pending')
    if (first) state.inputs[first.id] = emptyInput()
  }
}

function ensureTrainingConflict(): void {
  const entry = currentEntry.value
  if (!entry) return
  const input = state.inputs[entry.set.id] ?? suggestedInput(entry.exercise)
  const side = isPerSideKind(entry.exercise.kind) ? '/侧' : ''
  const local = isDurationKind(entry.exercise.kind)
    ? `${input.seconds || '—'} 秒${side}`
    : entry.exercise.kind === 'breath'
      ? `${input.reps || '—'} 次呼吸`
      : isBodyweightKind(entry.exercise.kind)
        ? `自重 × ${input.reps || '—'} 次${side}`
        : `${input.weightKg || '—'} kg × ${input.reps || '—'} 次${side}`
  const remoteText = isDurationKind(entry.exercise.kind)
    ? `42 秒${side}`
    : entry.exercise.kind === 'breath'
      ? '8 次呼吸'
      : isBodyweightKind(entry.exercise.kind)
        ? `自重 × 8 次${side}`
        : '12.5 kg × 8 次'
  state.session.conflicts = [
    {
      setId: entry.set.id,
      exerciseName: entry.exercise.name,
      setIndex: entry.set.index,
      localText: local,
      remoteText,
      remoteAt: '10月24日 19:13 · 另一台设备',
    },
  ]
}

function ensurePreviewMessage(status: 'sending' | 'failed'): void {
  const conversation = activeConversation.value
  if (!conversation) return
  conversation.messages = conversation.messages.filter((message) => message.id !== 'preview-pending')
  if (conversation.messages.some((message) => message.status === status)) return
  conversation.messages.push({
    id: 'preview-pending',
    author: 'student',
    authorName: STUDENT_NAME,
    text: '这周的第三次训练想挪到周六上午，可以吗？',
    sentAt: nowIso(),
    status,
  })
}

export function applyPreviewState(screen: Screen, key: string): void {
  const set = <K extends keyof PreviewFlags>(field: K, value: PreviewFlags[K]) => {
    state.preview[field] = value
  }
  switch (screen) {
    case 'today': {
      applyTodayMode(key as TodayMode)
      break
    }
    case 'explore': {
      set('explore', key as PreviewFlags['explore'])
      const published = key !== 'removed'
      for (const item of EXPLORE_ITEMS) item.status = 'published'
      if (!published) {
        const removed = EXPLORE_ITEMS.find((item) => item.id === 'plan-machine')
        if (removed) removed.status = 'removed'
      }
      ui.expandedId = null
      break
    }
    case 'planDetail': {
      set('planDetail', key as PreviewFlags['planDetail'])
      const item = selectedPlanItem.value
      if (item) item.status = key === 'removed' ? 'removed' : 'published'
      if (key === 'normal') state.auth = { signedIn: true, age: 'adult' }
      if (key === 'anonymous') state.auth = { signedIn: false, age: 'unknown' }
      if (key === 'unknown-age') state.auth = { signedIn: true, age: 'unknown' }
      if (key === 'minor') state.auth = { signedIn: true, age: 'minor' }
      break
    }
    case 'training': {
      set('training', key as PreviewFlags['training'])
      state.session.conflicts = []
      if (key === 'normal' || key === 'no-history' || key === 'sync-pending' || key === 'sync-failed') {
        applyTodayMode('in_progress')
      }
      if (key === 'conflict') ensureTrainingConflict()
      if (key === 'complete') {
        completeAllForPreview()
        set('training', 'complete')
      }
      break
    }
    case 'records': {
      set('records', key as PreviewFlags['records'])
      ui.recordsFocusExplicit = false
      ui.recordsFocusId = null
      if (key === 'single') {
        ui.recordsRange = '4w'
        ui.recordsExerciseId = '哑铃卧推'
      }
      break
    }
    case 'messages': {
      set('messages', key as PreviewFlags['messages'])
      if (key === 'normal' || key === 'revoked') ui.activeConversationId = 'conv-gu-yan'
      if (key !== 'sending' && key !== 'failed') {
        for (const conversation of state.conversations) {
          conversation.messages = conversation.messages.filter(
            (message) => message.id !== 'preview-pending',
          )
        }
      }
      const relation = state.relations.find((item) => item.id === 'rel-gu-yan')
      if (key === 'normal') {
        restoreHealthForPreview()
        if (relation) {
          relation.status = 'active'
          relation.readOnly = false
        }
      }
      if (key === 'readonly' && relation) {
        relation.readOnly = true
        relation.status = 'ended'
      }
      if (key === 'pending' && relation) {
        relation.status = 'pending'
        relation.readOnly = false
      }
      if (key === 'revoked') revokeHealthAuthorization('rel-gu-yan')
      if (key === 'sending' || key === 'failed') ensurePreviewMessage(key)
      break
    }
    case 'incomingPlan': {
      set('incomingPlan', key as PreviewFlags['incomingPlan'])
      ui.incomingHistoryVersion = null
      ui.incomingHistoryStudentId = 'lin-che'
      ui.incomingReadOnly = false
      ui.incomingRefMissing = false
      const incoming = state.student.incoming
      if (key === 'new') {
        if (!incoming) {
          const latest = coachLatestDelivery.value
          if (latest) {
            state.student.incoming = {
              id: `delivery-${latest.version}`,
              version: latest.version,
              baseVersion: state.student.planVersion,
              content: clonePlain(latest.content),
              hash: planHash(latest.content),
              deliveredAt: latest.deliveredAt,
              messageId: `m-plan-${latest.version}`,
              status: 'new',
              available: true,
            }
          }
        } else {
          incoming.status = 'new'
          incoming.available = true
          incoming.baseVersion = state.student.planVersion
        }
      }
      if (incoming) {
        if (key === 'expired') {
          incoming.status = 'expired'
          incoming.available = false
        }
        if (key === 'no-permission') incoming.available = false
        if (key === 'conflict') {
          incoming.status = 'new'
          incoming.available = true
          incoming.baseVersion = Math.max(0, state.student.planVersion - 1)
        }
        if (key === 'adopted') {
          incoming.available = true
          incoming.status = 'new'
          ui.confirmHash = incoming.hash
          ui.conflictResolved = true
          confirmAdoption()
        }
      }
      break
    }
    case 'studentPlan': {
      break
    }
    case 'myPlan': {
      set('myPlan', key as PreviewFlags['myPlan'])
      if (key === 'normal') {
        ui.conflictResolved = false
        state.ownPlan.remoteVersion = null
        state.ownPlan.remoteContent = null
        state.ownPlan.remoteAt = null
        if (!state.ownPlan.days.length) restoreOwnPlanSeed()
        if (state.ownPlan.status === 'taken_down' || state.ownPlan.status === 'pending_review') {
          state.ownPlan.status = 'draft'
          state.ownPlan.confirmedReviewHash = null
        }
      }
      if (key === 'empty') {
        state.ownPlan.days = []
        state.ownPlan.status = 'draft'
        state.ownPlan.savedContent = ''
      }
      if (key === 'conflict') {
        const remote = sanitizePlan(ownPlanContent.value)
        const first = remote.days[0]?.exercises[0]
        if (first) first.sets = first.sets === '4' ? '5' : '4'
        state.ownPlan.remoteVersion = state.ownPlan.version + 1
        state.ownPlan.remoteContent = remote
        state.ownPlan.remoteAt = nowIso()
        ui.conflictResolved = false
      }
      if (key === 'missing') {
        if (!state.ownPlan.days.length) restoreOwnPlanSeed()
        const exercise = state.ownPlan.days[0]?.exercises[1]
        if (exercise) exercise.name = ''
      }
      if (key === 'pending') {
        state.ownPlan.status = 'pending_review'
        state.ownPlan.confirmedReviewHash = ownPlanHash.value
      }
      if (key === 'taken_down') {
        state.ownPlan.status = 'taken_down'
        state.ownPlan.takenDownReason = '内容来源需要补充，暂时下架。'
      }
      break
    }
    case 'aiAssist': {
      set('ai', key as PreviewFlags['ai'])
      break
    }
    case 'diet': {
      set('diet', key as PreviewFlags['diet'])
      if (key === 'unrecorded' || key === 'insufficient') {
        state.diet.report.status = null
        state.diet.report.observation = ''
        state.diet.report.savedAt = null
        state.diet.saveState = 'idle'
      }
      if (key === 'recorded') {
        state.diet.report.status = 'on_plan'
        state.diet.report.savedAt = '2026-10-24T20:10:00+08:00'
        state.diet.saveState = 'saved'
      }
      if (key === 'observation') {
        state.diet.report.status = 'partial'
        state.diet.report.observation = '晚餐蔬菜偏少，其余按计划。'
        state.diet.report.savedAt = '2026-10-24T20:12:00+08:00'
        state.diet.saveState = 'saved'
      }
      if (key === 'save-failed') {
        state.diet.report.status = 'partial'
        state.diet.saveState = 'failed'
      }
      if (key === 'risk') state.diet.contentRisk = true
      else state.diet.contentRisk = false
      if (key === 'revoked') {
        revokeHealthAuthorization()
        state.diet.report.coachVisible = false
      }
      break
    }
    case 'coachDetail': {
      set('coachDetail', key as PreviewFlags['coachDetail'])
      break
    }
    case 'coachTodo': {
      set('coachTodo', key as PreviewFlags['coachTodo'])
      break
    }
    case 'coachStudents': {
      set('coachStudents', key as PreviewFlags['coachStudents'])
      if (key === 'unauthorized') ui.selectedStudentId = 'zhou-lan'
      if (key === 'ended') ui.selectedStudentId = 'chen-mo'
      if (key === 'stale') ui.selectedStudentId = 'zhou-lan'
      break
    }
    case 'coachPlan': {
      set('coachPlan', key as PreviewFlags['coachPlan'])
      if (key === 'normal' || key === 'safety' || key === 'tech' || key === 'conflict') {
        ui.publishBlocked = false
      }
      break
    }
    case 'coachServices': {
      set('coachServices', key as PreviewFlags['coachServices'])
      if (key === 'preview') {
        state.services.previewPackageId = 'pkg-strength'
        ui.sheet = 'studentPreview'
      }
      break
    }
  }
  schedulePersist()
}

/* ------------------------------------------------------------------ */
/* 对外上下文                                                          */
/* ------------------------------------------------------------------ */

export const coacheeContext = {
  student: STUDENT_NAME,
  coach: COACH_NAME,
  activeVersion: computed(() => state.student.planVersion),
  draftVersion: computed(() => state.coach.draftVersion),
  latestDeliveryVersion: computed(() => coachLatestDelivery.value?.version ?? null),
}

export const previewDate = PREVIEW_DATE_LABEL
export const previewWeek = PREVIEW_WEEK_LABEL
export const storageKey = STORAGE_KEY
export const currentRoleLabel = computed(() =>
  state.actor === 'coach' ? `${COACH_NAME} · 教练` : `${STUDENT_NAME} · 学员`,
)
export const coachConversation = computed(
  () => state.conversations.find((item) => item.id === 'conv-gu-yan') ?? null,
)

/* 仅开发预览：供本地截图与检查脚本切换状态，不渲染、不进入产品文案。 */
if (typeof window !== 'undefined' && import.meta.env.DEV) {
  ;(window as unknown as Record<string, unknown>).__nerae = {
    state,
    ui,
    jumpToScenario,
    applyPreviewState,
    openPlanFromExplore,
    goToPlanDetail,
    goToCoachDetail,
    goToCoachStudentDetail,
    goToConversation,
    replyToStudent,
    currentConversation,
    conversationUnread,
    conversationHealthVisible,
    openConversation,
    sendMessage,
    setMessageDraft,
    goToCoachPlanForStudent,
    prefillConsultation,
    requestStartPlan,
    confirmReplaceAndStart,
    completeLocalSignIn,
    confirmAdultAccess,
    declareMinor,
    cancelAccess,
    openShareReview,
    updateServiceField,
    saveServices,
    previewServiceAsStudent,
    setTextScale,
    setReduceMotion,
    goToMyPlan,
    goToStudentPlan,
    goToAiAssist,
    goToDiet,
    goToIncomingPlan,
    incomingDelivery,
    incomingAvailable,
    incomingStatusLabel,
    filteredCoaches,
    recordViewRows,
    recordChart,
    recordSummary,
    storageState,
    currentEntry,
    leaveTraining,
    goToRecords,
    setRecordsExercise,
    setInputValue,
    completeCurrentSet,
    setRecordingMode,
    recordLogSet,
    saveTrainingLog,
    addLogExercise,
    addLogSet,
    recordsFeedback,
    planWorkFor,
    openMessageRef,
    activateCoachStudentPlan,
    updatePlanField,
    updateDayTitle,
    saveDraft,
    requestPublish,
    confirmPublish,
    openAdoptSheet,
    closeSheet,
    openSheet,
    resetPreview,
  }
}
