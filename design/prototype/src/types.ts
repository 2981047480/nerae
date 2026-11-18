export type Actor = 'student' | 'coach'

export type Screen =
  | 'today'
  | 'explore'
  | 'planDetail'
  | 'training'
  | 'records'
  | 'messages'
  | 'incomingPlan'
  | 'myPlan'
  | 'studentPlan'
  | 'aiAssist'
  | 'diet'
  | 'coachDetail'
  | 'coachTodo'
  | 'coachStudents'
  | 'coachStudentDetail'
  | 'coachServices'
  | 'coachPlan'

export type StudentRoot = 'today' | 'explore' | 'records' | 'messages'
export type CoachRoot = 'coachTodo' | 'coachStudents' | 'messages' | 'coachServices'

export type LoadKind =
  | 'weight'
  | 'perSide'
  | 'duration'
  | 'durationPerSide'
  | 'bodyweight'
  | 'bodyweightPerSide'
  | 'breath'

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

export interface SetConflict {
  setId: string
  exerciseName: string
  setIndex: number
  localText: string
  remoteText: string
  remoteAt: string
}

export interface SessionState {
  id: string
  planId: string
  dayId: string
  planVersion: number
  planTitle: string
  dayTitle: string
  dateLabel: string
  startedAt: string
  durationMin: number
  status: 'in_progress' | 'complete'
  completedAt: string | null
  exercises: SessionExercise[]
  selectedExerciseId?: string
  trainingDate?: string
  conflicts: SetConflict[]
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
  equipment?: string
}

export interface PlanDay {
  id: string
  title: string
  exercises: PlanExercise[]
  minutes?: number
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
  coachName?: string
}

export interface CoachState {
  published: PlanContent
  publishedVersion: number
  activeStudentVersion: number
  draft: PlanContent
  draftVersion: number
  savedContent: string
  savedAt: string | null
  deliveries: PlanDelivery[]
}

export interface ExploreFilters {
  goal: string
  equipment: string
  duration: string
}

export interface ExploreState {
  mode: 'training' | 'diet' | 'coach'
  trainingFilters: ExploreFilters
  dietFilters: ExploreFilters
  coachFilters: { goal: string; mode: string; price: string }
}

export type TodayMode =
  | 'scheduled'
  | 'in_progress'
  | 'complete'
  | 'rest_day'
  | 'paused'
  | 'cycle_end'
  | 'unscheduled'
  | 'offline'

export type AiStatus =
  | 'ready'
  | 'needs_input'
  | 'safety_block'
  | 'tech_error'
  | 'quota'
  | 'expired'
  | 'duplicate'

export type MessageStatus = 'sent' | 'sending' | 'failed'

export type RelationStatus = 'active' | 'pending' | 'ended'

export type OwnPlanStatus = 'draft' | 'pending_review' | 'public' | 'taken_down'

export interface IncomingDelivery {
  id: string
  version: number
  baseVersion: number
  content: PlanContent
  hash: string
  deliveredAt: string
  messageId: string
  status: 'new' | 'adopted' | 'expired' | 'superseded'
  available: boolean
}

export interface ScheduleSlot {
  date: string
  dateLabel: string
  dayId: string
  dayTitle: string
  planVersion: number
}

export interface StudentState {
  plan: PlanContent
  planVersion: number
  adoptedVersion: number | null
  adoptedAt: string | null
  incoming: IncomingDelivery | null
  schedule: ScheduleSlot[]
}

export interface RecordedSet {
  id: string
  exerciseId: string
  exerciseName: string
  kind: LoadKind
  weightKg: number | null
  reps: number | null
  seconds: number | null
  completedAt: string
}

export interface TrainingFeedback {
  author: string
  dateLabel: string
  text: string
  sourceLabel: string
}

export interface TrainingRecord {
  id: string
  date: string
  dateLabel: string
  dayTitle: string
  planVersion: number
  completedAt: string
  lateRecorded: boolean
  lateNote?: string
  partial?: boolean
  partialNote?: string
  sets: RecordedSet[]
  feedback?: TrainingFeedback
}

export interface ServicePackage {
  id: string
  coachId: string
  name: string
  summary: string
  cycle: string
  priceYuan: number
  priceUnit: string
  goals: string[]
  includes: string[]
  response?: string
  sampleNote: string
  samplePlanId?: string
  channel: 'message' | 'unavailable'
  visible: boolean
  version: number
}

export interface CoachProfile {
  id: string
  name: string
  title: string
  focus: string[]
  modes: string[]
  response: string
  intro: string
  city: string | null
  supportsOnline: boolean
  packages: ServicePackage[]
  samples: Array<{ label: string; planId: string; note: string }>
  verification: string
}

export interface MessageRef {
  kind: 'plan' | 'training' | 'service'
  id: string
  label: string
  meta: string
}

export interface ChatMessage {
  id: string
  author: 'student' | 'coach' | 'system'
  authorName: string
  text: string
  sentAt: string
  status: MessageStatus
  redacted?: boolean
  health?: boolean
  refs?: MessageRef[]
}

export interface Conversation {
  id: string
  coachId: string
  coachName: string
  studentId: string
  serviceLabel: string
  packageId: string | null
  relationId: string
  messages: ChatMessage[]
  drafts: { student: string; coach: string }
  unassignedLegacyDraft?: string
  readBy: { student: string | null; coach: string | null }
  updatedAt: string
  kind: 'service' | 'consult'
}

export type TodoReason = 'reply' | 'delivery' | 'authorization' | 'relation' | 'draft'

export interface TodoItem {
  id: string
  studentId: string
  studentName: string
  reason: TodoReason
  title: string
  detail: string
  dueLabel: string
  priority: number
  done: boolean
  targetScreen: Screen
  conversationId?: string
  healthRelated?: boolean
}

export interface StudentEvidence {
  id: string
  dateLabel: string
  title: string
  summary: string
  kind: 'training' | 'feedback' | 'plan' | 'self-report'
  hidden?: boolean
  private?: boolean
}

export interface CoachStudent {
  id: string
  name: string
  relationStatus: RelationStatus
  relationId: string
  packageName: string
  planVersion: number
  deliveredVersion: number | null
  lastFact: string
  lastFactAt: string
  lastFactHealth: boolean
  records: StudentEvidence[]
  pendingWork: string | null
  planDraftStatus?: string | null
  pendingWorkHealth?: boolean
  serviceEnded: boolean
  stale: boolean
}

export interface OwnPlanState {
  title: string
  cycle: string
  days: PlanDay[]
  version: number
  savedContent: string
  savedAt: string | null
  visibility: 'private' | 'public'
  status: OwnPlanStatus
  healthNote: string
  reviewHash: string | null
  confirmedReviewHash: string | null
  publishedContent: string | null
  takenDownReason: string | null
  remoteVersion: number | null
  remoteContent: PlanContent | null
  remoteAt: string | null
}

export interface AiChange {
  dayId: string
  dayTitle: string
  exerciseId: string
  exerciseName: string
  field: 'sets' | 'target' | 'restSeconds' | 'note'
  label: string
  before: string
  after: string
}

export interface AiTemplate {
  id: string
  title: string
  intent: string
  context: 'own' | 'coach'
  status: AiStatus
  explanation: string
  missing: string | null
  safety: string | null
  changes: AiChange[]
  note: string
  sourceLabel: string
  templateVersion: number
  basePlanVersion: number
  requiresInput: boolean
}

export interface AiState {
  context: 'own' | 'coach'
  templateId: string
  intent: string
  appliedIds: string[]
  appliedHash: string | null
  lastAppliedAt: string | null
  confirmedVersion: number | null
  baseVersion: number | null
}

export interface DietSelfReport {
  date: string
  status: 'on_plan' | 'partial' | 'off_plan' | null
  observation: string
  savedAt: string | null
  coachVisible: boolean
}

export interface DietState {
  selectedItemId: string
  report: DietSelfReport
  saveState: 'idle' | 'saved' | 'failed'
  contentRisk: boolean
}

export interface ServiceDraft {
  packageId: string
  description: string
  scope: string
  goalsText: string
  response: string
  priceInput: string
  priceYuan: number
  priceUnit: string
  cycle: string
  visible: boolean
  version: number
}

export interface CoachServicesState {
  drafts: Record<string, ServiceDraft>
  saved: Record<string, ServiceDraft>
  savedAt: string | null
  version: number
  previewPackageId: string | null
}

export type AuthState = {
  signedIn: boolean
  age: 'adult' | 'minor' | 'unknown'
}

export type PendingAccess =
  | { kind: 'startPlan'; planId: string }
  | { kind: 'enterTraining' }
  | { kind: 'startScheduled' }
  | { kind: 'resume' }
  | null

export interface PreviewFlags {
  today: TodayMode
  explore: 'normal' | 'loading' | 'error' | 'empty' | 'removed'
  planDetail: 'normal' | 'removed' | 'anonymous' | 'unknown-age' | 'minor'
  training: 'normal' | 'no-history' | 'sync-pending' | 'sync-failed' | 'conflict' | 'complete'
  records: 'normal' | 'empty' | 'single' | 'insufficient' | 'late'
  messages: 'normal' | 'empty' | 'sending' | 'failed' | 'readonly' | 'revoked' | 'pending'
  incomingPlan: 'new' | 'adopted' | 'expired' | 'conflict' | 'no-permission'
  myPlan: 'normal' | 'empty' | 'conflict' | 'missing' | 'pending' | 'taken_down' | 'saving' | 'save-failed'
  ai: AiStatus
  diet: 'unrecorded' | 'recorded' | 'observation' | 'insufficient' | 'save-failed' | 'risk' | 'revoked'
  coachList: 'normal' | 'empty'
  coachDetail: 'normal' | 'unavailable'
  coachTodo: 'normal' | 'empty'
  coachStudents: 'normal' | 'empty' | 'unauthorized' | 'ended' | 'stale'
  coachPlan: 'normal' | 'safety' | 'tech' | 'conflict' | 'publishing' | 'delivered' | 'ai-draft'
  coachServices: 'normal' | 'saving' | 'failed' | 'preview'
}

export interface SelectionState {
  selectedPlanId: string
  planDayId: string
  selectedCoachId: string
  selectedPackageId: string | null
  selectedStudentId: string
  activeConversationId: string | null
  actor: 'student' | 'coach'
  incomingHistoryVersion: number | null
  incomingHistoryStudentId: string
  incomingReadOnly: boolean
  incomingRefMissing: boolean
  recordsFocusId: string | null
  recordsFocusExplicit: boolean
  recordsRange: '4w' | '8w' | '12w'
  recordsExerciseId: string
  ownDayId: string
  aiTemplateId: string
  coachDayId: string
  coachMode: CoachMode
}

export interface PersistedState {
  schema: 2
  actor: Actor
  screen: Screen
  stack: Screen[]
  selection: SelectionState
  sessionSequence: number
  recordingMode: 'after' | 'set'
  coachSnapshots: Record<string, CoachState>
  activeCoachStudentId: string
  auth: AuthState
  session: SessionState
  inputs: Record<string, SetInput>
  favorites: string[]
  explore: ExploreState
  coach: CoachState
  todayMode: TodayMode
  student: StudentState
  records: TrainingRecord[]
  coaches: CoachProfile[]
  relations: Array<{
    id: string
    coachId: string
    packageId: string
    status: RelationStatus
    agreedPriceYuan: number
    agreedPriceUnit: string
    agreedCycle: string
    agreedName: string
    agreedNeedsReview?: boolean
    agreedPriceKnown?: boolean
    agreedAtKnown?: boolean
    agreedAt: string
    serviceVersion: number
    healthAuthorized: boolean
    healthRevokedAt: string | null
    planVersion: number
    readOnly: boolean
  }>
  conversations: Conversation[]
  todoItems: TodoItem[]
  students: CoachStudent[]
  ownPlan: OwnPlanState
  ai: AiState
  diet: DietState
  services: CoachServicesState
  preview: PreviewFlags
  reduceMotion: boolean
  textScale: TextScale
  updatedAt: string
}

export type TextScale = 1 | 2

export type SheetKind =
  | 'preview'
  | 'filter'
  | 'publish'
  | 'records'
  | 'adult'
  | 'replace'
  | 'adopt'
  | 'share'
  | 'applyAi'
  | 'sync'
  | 'studentPreview'
  | 'access'
  | 'revoke'
  | null

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
  image?: string
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
  saveNoteTone: 'neutral' | 'error' | 'positive' | null
  removeUndo: RemoveUndo | null
  focusRequest: FocusRequest | null
  activeConversationId: string | null
  incomingHistoryVersion: number | null
  incomingHistoryStudentId: string
  incomingReadOnly: boolean
  incomingRefMissing: boolean
  recordsFocusId: string | null
  recordsFocusExplicit: boolean
  selectedPlanId: string | null
  selectedCoachId: string | null
  selectedPackageId: string | null
  selectedStudentId: string | null
  recordsRange: '4w' | '8w' | '12w'
  recordsExerciseId: string
  ownDayId: string
  planDayId: string
  aiTemplateId: string
  dietOpen: boolean
  confirmHash: string | null
  conflictResolved: boolean
  serviceSaving: boolean
  lastSentMessageId: string | null
  accessStep: 'signin' | 'age' | 'minor'
  pendingAccess: PendingAccess
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
  status: 'published' | 'removed'
  adultRequired: boolean
  author: string
  days?: PlanDay[]
}
