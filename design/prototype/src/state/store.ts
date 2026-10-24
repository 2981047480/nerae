import { computed, reactive, watch } from 'vue'
import type {
  CoachMode,
  ExploreFilters,
  FocusRequest,
  PersistedState,
  PlanChange,
  PlanExercise,
  PlanValidationIssue,
  RemoveUndo,
  Screen,
  SetInput,
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
  appliedValues,
  doneSetCount,
  exerciseProgress,
  firstPendingEntry,
  hasErrors,
  inputFromValues,
  suggestedInput,
  totalSetCount,
  nextEventTime,
  validateSetInput,
  type SetEntry,
} from '../utils/training'
import {
  dayTotals,
  diffPlans,
  planTotals,
  sanitizePlan,
  serializePlan,
  validatePlan,
} from '../utils/plan'
import { formatClock, formatDateTime, formatTime } from '../utils/format'

function nowIso(): string {
  return new Date().toISOString()
}

function createSeedState(): PersistedState {
  const session = createSeedSession()
  const inputs: Record<string, SetInput> = {}
  const first = session.exercises[0]?.sets.find((set) => set.status === 'pending')
  const firstExercise = session.exercises[0]
  if (first && firstExercise) {
    inputs[first.id] = suggestedInput(firstExercise)
  }
  return {
    schema: 1,
    actor: 'student',
    screen: 'today',
    session,
    inputs,
    favorites: [],
    explore: {
      mode: 'training',
      trainingFilters: { ...DEFAULT_FILTERS },
      dietFilters: { ...DEFAULT_FILTERS },
    },
    coach: createSeedCoachState(),
    reduceMotion: false,
    textScale: 1,
    updatedAt: nowIso(),
  }
}

function isScreen(value: unknown): value is Screen {
  return value === 'today' || value === 'explore' || value === 'training' || value === 'coachPlan'
}

function loadState(): PersistedState {
  if (typeof window === 'undefined') return createSeedState()
  try {
    const raw = window.localStorage.getItem(STORAGE_KEY)
    if (!raw) return createSeedState()
    const parsed = JSON.parse(raw) as Partial<PersistedState>
    if (
      !parsed ||
      parsed.schema !== 1 ||
      !parsed.session ||
      !Array.isArray(parsed.session.exercises) ||
      !parsed.coach
    ) {
      return createSeedState()
    }
    const base = createSeedState()
    const merged: PersistedState = {
      ...base,
      ...parsed,
      actor: parsed.actor === 'coach' ? 'coach' : 'student',
      screen: isScreen(parsed.screen) ? parsed.screen : 'today',
      inputs: parsed.inputs ?? base.inputs,
      favorites: Array.isArray(parsed.favorites) ? parsed.favorites : [],
      explore: {
        mode: parsed.explore?.mode === 'diet' ? 'diet' : 'training',
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
      },
      coach: {
        ...base.coach,
        ...parsed.coach,
        deliveries: Array.isArray(parsed.coach.deliveries) ? parsed.coach.deliveries : [],
      },
      textScale: parsed.textScale === 2 ? 2 : 1,
      updatedAt: nowIso(),
    }
    if (merged.session.rest && !merged.session.rest.paused) {
      if (merged.session.rest.endsAt !== null && Date.now() >= merged.session.rest.endsAt) {
        merged.session.rest = null
      }
    }
    return merged
  } catch {
    return createSeedState()
  }
}

function createInitialUi(): UiState {
  return {
    sheet: null,
    resetConfirm: false,
    trainingErrors: {},
    justCompletedSetId: null,
    coachMode: 'review',
    coachDayId: 'day-a',
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
  }
}

export const state = reactive<PersistedState>(loadState())
export const ui = reactive<UiState>(createInitialUi())

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

function persistNow(): boolean {
  if (typeof window === 'undefined') return true
  try {
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
  return persistNow()
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

export const clock = reactive({ now: Date.now() })
let ticker: number | null = null

function ensureTicker(): void {
  if (typeof window === 'undefined' || ticker !== null) return
  ticker = window.setInterval(() => {
    clock.now = Date.now()
    const rest = state.session.rest
    if (rest && !rest.paused && rest.endsAt !== null && clock.now >= rest.endsAt) {
      state.session.rest = null
      stopTicker()
      schedulePersist()
    }
  }, 250)
}

function stopTicker(): void {
  if (ticker !== null && typeof window !== 'undefined') {
    window.clearInterval(ticker)
  }
  ticker = null
}

if (state.session.rest && !state.session.rest.paused) ensureTicker()

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

const history: Screen[] = []
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

function navigate(
  screen: Screen,
  options: { resetHistory?: boolean; top?: boolean } = {},
): void {
  if (state.screen === screen && !options.top) return
  rememberScroll()
  if (options.resetHistory) history.length = 0
  else if (state.screen !== screen) history.push(state.screen)
  state.screen = screen
  if (options.top) scrollPositions.delete(screen)
}

export function goToExplore(): void {
  navigate('explore')
}

export function goToToday(): void {
  navigate('today', { resetHistory: true, top: true })
}

export function goToCoachPlan(): void {
  state.actor = 'coach'
  navigate('coachPlan', { resetHistory: true, top: true })
}

export function goHome(): void {
  if (state.actor === 'coach') goToCoachPlan()
  else goToToday()
}

export function enterTraining(): void {
  navigate('training', { top: true })
}

export function leaveTraining(): void {
  navigate('today', { resetHistory: false })
}


export type PreviewScenario = 'today' | 'explore' | 'training' | 'coach-edit' | 'coach-review'

export function jumpToScenario(scenario: PreviewScenario): void {
  if (scenario === 'coach-edit' || scenario === 'coach-review') {
    state.actor = 'coach'
    ui.coachMode = scenario === 'coach-edit' ? 'edit' : 'review'
    state.screen = 'coachPlan'
  } else {
    state.actor = 'student'
    state.screen = scenario
  }
  history.length = 0
  scrollPositions.clear()
  ui.sheet = null
  if (typeof window !== 'undefined') window.scrollTo(0, 0)
}

export function openSheet(sheet: UiState['sheet']): void {
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
  stopTicker()
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
  Object.assign(ui, createInitialUi())
  history.length = 0
  scrollPositions.clear()
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
    state.inputs[entry.set.id] = suggestedInput(entry.exercise)
  }
  return state.inputs[entry.set.id]
})
export const isComplete = computed(() => state.session.status === 'complete')
export const restRemainingMs = computed(() => {
  const rest = state.session.rest
  if (!rest) return 0
  if (rest.paused) return rest.remainingMs ?? 0
  if (rest.endsAt === null) return 0
  return Math.max(0, rest.endsAt - clock.now)
})
export const restClock = computed(() => formatClock(restRemainingMs.value))
export const restNextLabel = computed(() => {
  const rest = state.session.rest
  if (!rest) return ''
  for (const exercise of state.session.exercises) {
    const set = exercise.sets.find((item) => item.id === rest.nextSetId)
    if (set) return `${exercise.name} · 第 ${set.index} 组`
  }
  return ''
})
export const currentExerciseProgress = computed(() => {
  const entry = currentEntry.value
  return entry ? exerciseProgress(entry.exercise) : null
})

function ensureInputFor(entry: SetEntry): void {
  if (!state.inputs[entry.set.id]) {
    state.inputs[entry.set.id] = suggestedInput(entry.exercise)
  }
}

export function setInputValue(setId: string, field: keyof SetInput, value: string): void {
  const input = state.inputs[setId] ?? inputFromValues('weight', null)
  input[field] = value
  state.inputs[setId] = input
  if (Object.keys(ui.trainingErrors).length) {
    const entry = currentEntry.value
    if (entry && entry.set.id === setId) {
      ui.trainingErrors = validateSetInput(entry.exercise.kind, input)
    }
  }
}

export function adjustInput(field: 'weightKg' | 'reps' | 'seconds', delta: number): void {
  const entry = currentEntry.value
  if (!entry) return
  const input = state.inputs[entry.set.id] ?? suggestedInput(entry.exercise)
  if (field === 'weightKg') {
    const current = Number(input.weightKg.replace(',', '.')) || 0
    const next = Math.min(300, Math.max(1.25, Math.round((current + delta) * 100) / 100))
    input.weightKg = String(next)
  } else if (field === 'reps') {
    const current = Number(input.reps) || 0
    input.reps = String(Math.min(100, Math.max(1, Math.round(current + delta))))
  } else {
    const current = Number(input.seconds) || 0
    input.seconds = String(Math.min(600, Math.max(5, Math.round(current + delta))))
  }
  state.inputs[entry.set.id] = input
  ui.trainingErrors = validateSetInput(entry.exercise.kind, input)
}

function startRest(seconds: number, nextSetId: string): void {
  const now = Date.now()
  const durationMs = seconds * 1000
  state.session.rest = {
    id: `rest-${nextSetId}-${now}`,
    durationMs,
    endsAt: now + durationMs,
    remainingMs: null,
    paused: false,
    nextSetId,
    startedAt: now,
  }
  clock.now = now
  ensureTicker()
  schedulePersist()
}

export function completeCurrentSet(): boolean {
  const entry = currentEntry.value
  if (!entry || isComplete.value || state.session.rest) return false
  const input = state.inputs[entry.set.id] ?? suggestedInput(entry.exercise)
  const errors = validateSetInput(entry.exercise.kind, input)
  ui.trainingErrors = errors
  if (hasErrors(errors)) return false

  const values = appliedValues(entry.exercise.kind, input)
  const eventTime = nextEventTime(state.session, entry.exercise.restSeconds)
  entry.set.status = 'done'
  entry.set.weightKg = values.weightKg
  entry.set.reps = values.reps
  entry.set.seconds = values.seconds
  entry.set.completedAt = eventTime
  ui.justCompletedSetId = entry.set.id
  if (completedTimer !== null) window.clearTimeout(completedTimer)
  completedTimer = window.setTimeout(() => {
    ui.justCompletedSetId = null
    completedTimer = null
  }, 900)

  const next = firstPendingEntry(state.session)
  if (next) {
    ensureInputFor(next)
    startRest(entry.exercise.restSeconds, next.set.id)
  } else {
    state.session.status = 'complete'
    state.session.completedAt = eventTime
    state.session.rest = null
    stopTicker()
  }
  schedulePersist()
  return true
}

export function pauseRest(): void {
  const rest = state.session.rest
  if (!rest || rest.paused) return
  rest.remainingMs = Math.max(0, (rest.endsAt ?? Date.now()) - Date.now())
  rest.endsAt = null
  rest.paused = true
  stopTicker()
  schedulePersist()
}

export function resumeRest(): void {
  const rest = state.session.rest
  if (!rest || !rest.paused) return
  rest.endsAt = Date.now() + (rest.remainingMs ?? 0)
  rest.remainingMs = null
  rest.paused = false
  ensureTicker()
  schedulePersist()
}

export function extendRest(seconds = 15): void {
  const rest = state.session.rest
  if (!rest) return
  const extra = seconds * 1000
  rest.durationMs += extra
  if (rest.paused) {
    rest.remainingMs = (rest.remainingMs ?? 0) + extra
  } else if (rest.endsAt !== null) {
    rest.endsAt += extra
  }
  schedulePersist()
}

export function skipRest(): void {
  if (!state.session.rest) return
  state.session.rest = null
  stopTicker()
  schedulePersist()
}

export const sessionGroups = computed(() => {
  return state.session.exercises.map((exercise) => {
    const progress = exerciseProgress(exercise)
    return {
      exercise,
      progress,
      sets: exercise.sets,
    }
  })
})

export const todayFacts = computed(() => ({
  title: state.session.dayTitle,
  planTitle: state.session.planTitle,
  version: state.session.planVersion,
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
/* 发现                                                                */
/* ------------------------------------------------------------------ */

export const currentFilters = computed<ExploreFilters>(() =>
  state.explore.mode === 'training' ? state.explore.trainingFilters : state.explore.dietFilters,
)

export const filteredExplore = computed(() =>
  filterExploreItems(EXPLORE_ITEMS, state.explore.mode, currentFilters.value, ui.exploreQuery),
)
export const exploreFeatured = computed(() => filteredExplore.value[0] ?? null)
export const exploreRest = computed(() => filteredExplore.value.slice(1))
export const activeFilterCount = computed(() => {
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
  if (state.explore.mode === 'diet') {
    return [{ key: 'goal', label: '场景', options: DIET_GOALS }]
  }
  return [
    { key: 'goal', label: '目标', options: TRAINING_GOALS },
    { key: 'equipment', label: '器械', options: TRAINING_EQUIPMENT },
    { key: 'duration', label: '单次时长', options: TRAINING_DURATION },
  ]
})
export const filterDraftResultCount = computed(() =>
  filterExploreItems(EXPLORE_ITEMS, state.explore.mode, ui.filterDraft, ui.exploreQuery).length,
)
export const expandedItem = computed(() => {
  if (!ui.expandedId) return null
  return filteredExplore.value.find((item) => item.id === ui.expandedId) ?? null
})

export function setExploreMode(mode: 'training' | 'diet'): void {
  if (state.explore.mode === mode) return
  state.explore.mode = mode
  ui.expandedId = null
  ui.exploreQuery = ''
}

export function applyFilterDraft(): void {
  if (state.explore.mode === 'training') {
    state.explore.trainingFilters = { ...ui.filterDraft }
  } else {
    state.explore.dietFilters = { ...ui.filterDraft }
  }
  ui.expandedId = null
  ui.sheet = null
}

export function resetFilters(): void {
  if (state.explore.mode === 'training') {
    state.explore.trainingFilters = { ...DEFAULT_FILTERS }
  } else {
    state.explore.dietFilters = { ...DEFAULT_FILTERS }
  }
  ui.exploreQuery = ''
  ui.expandedId = null
}

export function relaxFilters(): void {
  const filters = currentFilters.value
  if (filters.goal !== 'all') {
    filters.goal = 'all'
  } else if (state.explore.mode === 'training' && filters.equipment !== 'all') {
    filters.equipment = 'all'
  } else if (state.explore.mode === 'training' && filters.duration !== 'all') {
    filters.duration = 'all'
  } else {
    ui.exploreQuery = ''
  }
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
export const coachStatus = computed(() => {
  const coach = state.coach
  if (coach.deliveredVersion !== null && coach.draftVersion === coach.deliveredVersion && !coachDirty.value) {
    return {
      tone: 'positive',
      text: `版本 ${coach.deliveredVersion} 已交付 ${STUDENT_NAME}，等待采用`,
    }
  }
  if (coachDirty.value) return { tone: 'warning', text: '有未保存的改动' }
  if (coach.deliveredVersion !== null && coach.draftVersion > coach.deliveredVersion) {
    return { tone: 'neutral', text: `草稿版本 ${coach.draftVersion} · 未交付` }
  }
  return {
    tone: 'neutral',
    text: coach.savedAt ? `草稿已保存 · ${formatDateTime(coach.savedAt)}` : '草稿尚未保存',
  }
})
export const coachPublishDisabled = computed(
  () =>
    state.coach.deliveredVersion !== null &&
    state.coach.draftVersion === state.coach.deliveredVersion &&
    !coachDirty.value,
)

export const coachDeliveries = computed(() =>
  [...state.coach.deliveries].sort((a, b) => b.version - a.version),
)

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

function noteSaved(message: string | null, tone: 'neutral' | 'error' = 'neutral'): void {
  ui.saveNote = message
  ui.saveNoteTone = message ? tone : null
  if (saveNoteTimer !== null) window.clearTimeout(saveNoteTimer)
  if (message) {
    saveNoteTimer = window.setTimeout(() => {
      ui.saveNote = null
      saveNoteTimer = null
    }, 4200)
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
}

export function updateDayTitle(dayId: string, value: string): void {
  const day = state.coach.draft.days.find((item) => item.id === dayId)
  if (!day) return
  day.title = value
  noteSaved(null)
  ui.publishBlocked = false
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
}

export function removeExercise(dayId: string, exerciseId: string): void {
  const day = state.coach.draft.days.find((item) => item.id === dayId)
  if (!day) return
  const index = day.exercises.findIndex((item) => item.id === exerciseId)
  if (index < 0) return
  const [exercise] = day.exercises.splice(index, 1)
  const undo: RemoveUndo = { dayId, index, exercise, name: exercise.name || '未命名动作' }
  ui.removeUndo = undo
  if (removeUndoTimer !== null) window.clearTimeout(removeUndoTimer)
  removeUndoTimer = window.setTimeout(() => {
    ui.removeUndo = null
    removeUndoTimer = null
  }, 6000)
  noteSaved(null)
  ui.publishBlocked = false
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
  if (coachPublishDisabled.value) return
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

export function confirmPublish(): void {
  if (coachIssues.value.length) {
    ui.sheet = null
    ui.publishBlocked = true
    return
  }
  if (coachDirty.value && !saveDraft()) return
  const version = state.coach.draftVersion
  const snapshot = structuredClone(sanitizePlan(state.coach.draft))
  const deliveredAt = nowIso()
  const previousVersion = state.coach.deliveredVersion
  const previousAt = state.coach.deliveredAt
  const previousDeliveries = state.coach.deliveries
  state.coach.deliveries = [
    ...state.coach.deliveries.filter((item) => item.version !== version),
    { version, content: snapshot, deliveredAt },
  ]
  state.coach.deliveredVersion = version
  state.coach.deliveredAt = deliveredAt
  if (!persistNow()) {
    state.coach.deliveries = previousDeliveries
    state.coach.deliveredVersion = previousVersion
    state.coach.deliveredAt = previousAt
    noteSaved('未能保存发布状态，请重试。', 'error')
    return
  }
  ui.sheet = null
  noteSaved(null)
}

export function consumeFocusRequest(): FocusRequest | null {
  const request = ui.focusRequest
  ui.focusRequest = null
  return request
}

export const previewScenarios = computed(() => {
  const student = [
    { key: 'today', label: '今天', meta: `${doneSets.value}/${totalSets.value} 组已记录` },
    { key: 'explore', label: '发现', meta: '训练与饮食内容' },
    {
      key: 'training',
      label: '训练',
      meta: isComplete.value
        ? '今天已完成'
        : currentEntry.value
          ? `第 ${currentEntry.value.set.index} 组 · ${currentEntry.value.exercise.name}`
          : '训练记录',
    },
  ] as const
  const coach = [
    { key: 'coach-edit', label: '计划编辑', meta: `草稿版本 ${state.coach.draftVersion}` },
    {
      key: 'coach-review',
      label: '计划审阅',
      meta: coachChanges.value.length ? `${coachChanges.value.length} 处改动` : '暂无改动',
    },
  ] as const
  return { student, coach }
})

export const recordsForSheet = computed(() => {
  return state.session.exercises.map((exercise) => {
    const completed = exercise.sets.filter((set) => set.status === 'done')
    return {
      id: exercise.id,
      name: exercise.name,
      kind: exercise.kind,
      sets: completed,
      total: exercise.sets.length,
      progress: exerciseProgress(exercise),
    }
  })
})

export const coacheeContext = {
  student: STUDENT_NAME,
  coach: COACH_NAME,
  activeVersion: computed(() => state.coach.activeStudentVersion),
  draftVersion: computed(() => state.coach.draftVersion),
  deliveredVersion: computed(() => state.coach.deliveredVersion),
}

export const previewDate = PREVIEW_DATE_LABEL
export const previewWeek = PREVIEW_WEEK_LABEL
export const storageKey = STORAGE_KEY
