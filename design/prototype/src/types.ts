export type Actor = 'student' | 'coach'

export type Screen = 'today' | 'explore' | 'training' | 'coachPlan'

export type LoadKind = 'weight' | 'perSide' | 'duration'

export type CoachMode = 'edit' | 'review'

export interface SessionSet {
  id: string
  index: number
  status: 'pending' | 'done'
  weightKg: number | null
  reps: number | null
  seconds: number | null
  completedAt: string | null
}

export interface LastReference {
  dateLabel: string
  weightKg?: number
  reps?: number
  seconds?: number
  perSide?: boolean
}

export interface SessionExercise {
  id: string
  name: string
  kind: LoadKind
  equipment: string
  cue: string
  restSeconds: number
  targetText: string
  lastRef: LastReference
  sets: SessionSet[]
}

export interface RestState {
  id: string
  durationMs: number
  endsAt: number | null
  remainingMs: number | null
  paused: boolean
  nextSetId: string
  startedAt: number
}

export interface SessionState {
  id: string
  planVersion: number
  planTitle: string
  dayTitle: string
  dateLabel: string
  startedAt: string
  status: 'in_progress' | 'complete'
  completedAt: string | null
  exercises: SessionExercise[]
  rest: RestState | null
}

export interface SetInput {
  weightKg: string
  reps: string
  seconds: string
}

export interface SetInputErrors {
  weightKg?: string
  reps?: string
  seconds?: string
}

export interface PlanExercise {
  id: string
  name: string
  sets: string
  target: string
  restSeconds: string
  note: string
}

export interface PlanDay {
  id: string
  title: string
  exercises: PlanExercise[]
}

export interface PlanContent {
  title: string
  cycle: string
  days: PlanDay[]
}

export interface PlanDelivery {
  version: number
  content: PlanContent
  deliveredAt: string
}

export interface CoachState {
  published: PlanContent
  publishedVersion: number
  activeStudentVersion: number
  draft: PlanContent
  draftVersion: number
  savedContent: string
  savedAt: string | null
  deliveredVersion: number | null
  deliveredAt: string | null
  deliveries: PlanDelivery[]
}

export interface ExploreFilters {
  goal: string
  equipment: string
  duration: string
}

export interface ExploreState {
  mode: 'training' | 'diet'
  trainingFilters: ExploreFilters
  dietFilters: ExploreFilters
}

export interface PersistedState {
  schema: 1
  actor: Actor
  screen: Screen
  session: SessionState
  inputs: Record<string, SetInput>
  favorites: string[]
  explore: ExploreState
  coach: CoachState
  reduceMotion: boolean
  textScale: TextScale
  updatedAt: string
}

export type TextScale = 1 | 2

export type SheetKind = 'preview' | 'filter' | 'publish' | 'records' | null

export interface FocusRequest {
  dayId: string
  exerciseId: string
  field: 'name' | 'sets' | 'target' | 'restSeconds' | 'note' | 'dayTitle'
}

export interface RemoveUndo {
  dayId: string
  index: number
  exercise: PlanExercise
  name: string
}

export interface FocusOrigin {
  x: number
  y: number
  width: number
  height: number
  text: string
}

export interface UiState {
  focusOrigin: FocusOrigin | null
  sheet: SheetKind
  resetConfirm: boolean
  trainingErrors: SetInputErrors
  justCompletedSetId: string | null
  coachMode: CoachMode
  coachDayId: string
  searchOpen: boolean
  exploreQuery: string
  expandedId: string | null
  filterDraft: ExploreFilters
  publishBlocked: boolean
  flashDiff: boolean
  saveNote: string | null
  saveNoteTone: 'neutral' | 'error' | null
  removeUndo: RemoveUndo | null
  focusRequest: FocusRequest | null
}

export interface PlanFieldChange {
  kind: 'field'
  dayId: string
  dayTitle: string
  exerciseId: string
  exerciseName: string
  field: 'dayTitle' | 'name' | 'sets' | 'target' | 'restSeconds' | 'note'
  label: string
  before: string
  after: string
}

export interface PlanRowChange {
  kind: 'added' | 'removed'
  dayId: string
  dayTitle: string
  exerciseId: string
  exerciseName: string
}

export type PlanChange = PlanFieldChange | PlanRowChange

export interface PlanValidationIssue {
  dayId: string
  dayTitle: string
  exerciseId: string | null
  exerciseName: string | null
  field: 'dayTitle' | 'name' | 'sets' | 'target' | 'restSeconds' | 'note' | 'instructions'
  message: string
}

export interface ExploreContentLine {
  name: string
  detail: string
}

export interface ExploreItem {
  id: string
  type: 'training' | 'diet'
  title: string
  image: string
  imageAlt: string
  goal: string
  equipment: string
  durationMin: number
  perWeek: string
  countLabel: string
  source: string
  summary: string
  fit: string[]
  contents: ExploreContentLine[]
}
