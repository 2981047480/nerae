import type {
  AiState,
  AiTemplate,
  CoachProfile,
  CoachServicesState,
  CoachState,
  CoachStudent,
  Conversation,
  DietState,
  IncomingDelivery,
  OwnPlanState,
  PersistedState,
  PreviewFlags,
  ScheduleSlot,
  ServicePackage,
  StudentState,
  TodoItem,
  TrainingRecord,
  PlanContent,
} from '../types'
import {
  SEED_PUBLISHED_PLAN,
  createSeedCoachState,
  STUDENT_NAME,
  COACH_NAME,
} from './sample'
import { planHash, sanitizePlan, serializePlan } from '../utils/plan'

type Relation = PersistedState['relations'][number]

export const PREVIEW_TODAY = '2026-10-24'
export const NEXT_TRAINING = '10月27日 周二'

const GUYAN_PACKAGES: ServicePackage[] = [
  {
    id: 'pkg-strength',
    coachId: 'gu-yan',
    name: '力量基础 · 月度计划',
    summary: '每周 3 次完整训练安排，围绕基础推拉与核心稳定；每次训练后给出文字反馈。',
    cycle: '4 周',
    priceYuan: 699,
    priceUnit: '/ 月',
    goals: ['力量基础', '动作稳定'],
    includes: ['每周 3 次训练安排', '每次训练后的文字反馈', '每 4 周 1 次计划调整'],
    sampleNote: '可先看一份完整训练日样例，再决定是否咨询。',
    samplePlanId: 'plan-upper-pull',
    channel: 'message',
    visible: true,
    version: 2,
  },
  {
    id: 'pkg-check',
    coachId: 'gu-yan',
    name: '动作调整 · 单次',
    summary: '围绕一个动作核对动作路径与训练量，给出一份可自行执行的调整说明。',
    cycle: '单次',
    priceYuan: 199,
    priceUnit: '/ 次',
    goals: ['动作稳定'],
    includes: ['一个动作的完整核对', '一份文字调整说明', '一次追问回复'],
    sampleNote: '适合已有训练习惯、只需要局部调整的人。',
    samplePlanId: 'plan-dumbbell',
    channel: 'message',
    visible: true,
    version: 1,
  },
  {
    id: 'pkg-consult',
    coachId: 'gu-yan',
    name: '服务咨询 · 20 分钟',
    summary: '先说明目标、可用时间与当前安排，再判断是否需要长期服务；不承诺训练效果。',
    cycle: '20 分钟',
    priceYuan: 0,
    priceUnit: '',
    goals: ['目标确认'],
    includes: ['一次站内文字咨询', '服务范围与时间说明', '没有自动续费'],
    sampleNote: '咨询不会自动建立服务关系，也不包含健康授权。',
    channel: 'message',
    visible: true,
    version: 1,
  },
]

const JIANG_PACKAGES: ServicePackage[] = [
  {
    id: 'pkg-endurance',
    coachId: 'jiang-yao',
    name: '耐力基础 · 月度计划',
    summary: '每周 3 次走跑与体能安排，按可投入时间调整；以能持续完成为先。',
    cycle: '4 周',
    priceYuan: 560,
    priceUnit: '/ 月',
    goals: ['耐力', '体能基础'],
    includes: ['每周 3 次安排', '每两周一次回顾', '训练后的文字反馈'],
    sampleNote: '线上交付；计划以文字与训练日说明为主。',
    samplePlanId: 'plan-machine',
    channel: 'message',
    visible: true,
    version: 1,
  },
  {
    id: 'pkg-endurance-check',
    coachId: 'jiang-yao',
    name: '跑量回顾 · 单次',
    summary: '看最近两周的训练与感受记录，给出下一阶段的安排建议。',
    cycle: '单次',
    priceYuan: 159,
    priceUnit: '/ 次',
    goals: ['耐力'],
    includes: ['最近两周记录回顾', '一份下一阶段建议', '一次追问回复'],
    sampleNote: '需要已有训练记录；没有记录时先说明情况，不代填。',
    samplePlanId: 'plan-core',
    channel: 'message',
    visible: true,
    version: 1,
  },
]

const HEAN_PACKAGES: ServicePackage[] = [
  {
    id: 'pkg-mobility',
    coachId: 'he-an',
    name: '活动度与恢复 · 单次',
    summary: '针对训练后的活动度安排，给出可以在家完成的内容；不提供康复诊断。',
    cycle: '单次',
    priceYuan: 480,
    priceUnit: '/ 次',
    goals: ['活动度', '训练后恢复'],
    includes: ['一次线下动作核对', '一份居家活动度安排', '一次追问回复'],
    sampleNote: '仅线下；地点与时间在咨询中确认。',
    samplePlanId: 'plan-mobility',
    channel: 'unavailable',
    visible: true,
    version: 1,
  },
]

export const COACH_PROFILES: CoachProfile[] = [
  {
    id: 'gu-yan',
    name: COACH_NAME,
    title: '力量基础与动作调整',
    focus: ['力量基础', '动作稳定', '训练节奏'],
    modes: ['线上计划', '文字反馈'],
    response: '工作日 24 小时内回复',
    intro:
      '带过从零开始和重新恢复训练的人。先确认可用器械、时间与动作经验，再安排每周训练；不把一次训练的结果当成身体结论。',
    city: null,
    supportsOnline: true,
    packages: GUYAN_PACKAGES,
    samples: [
      { label: '上肢拉力训练日', planId: 'plan-upper-pull', note: '完整动作、组次与组间休息' },
      { label: '哑铃上肢训练日', planId: 'plan-dumbbell', note: '仅用一对哑铃的推拉安排' },
    ],
    verification: '资质与认证尚未核验。',
  },
  {
    id: 'jiang-yao',
    name: '江遥',
    title: '耐力与体能基础',
    focus: ['耐力', '体能基础', '训练节奏'],
    modes: ['线上计划', '文字反馈', '上海线下'],
    response: '48 小时内回复',
    intro:
      '面向想重新开始规律活动的人，安排以能持续完成为先。需要线下见面时，先确认地点、时间与可用的场地条件。',
    city: '上海',
    supportsOnline: true,
    packages: JIANG_PACKAGES,
    samples: [
      { label: '器械入门训练日', planId: 'plan-machine', note: '固定器械路径与注意事项' },
      { label: '核心稳定训练日', planId: 'plan-core', note: '不追求个数的躯干控制安排' },
    ],
    verification: '资质与认证尚未核验。',
  },
  {
    id: 'he-an',
    name: '何岸',
    title: '活动度与训练后恢复',
    focus: ['活动度', '训练后恢复'],
    modes: ['杭州线下'],
    response: '工作日 24 小时内回复',
    intro:
      '只做线下活动度与训练后安排，不提供康复诊断或疾病饮食建议。预约前需要确认城市、场地与时间。',
    city: '杭州',
    supportsOnline: false,
    packages: HEAN_PACKAGES,
    samples: [{ label: '髋与肩活动度安排', planId: 'plan-mobility', note: '训练后可以完成的伸展序列' }],
    verification: '资质与认证尚未核验。',
  },
]

function seedSet(
  recordId: string,
  exerciseId: string,
  exerciseName: string,
  kind: 'weight' | 'perSide' | 'duration',
  index: number,
  values: { weightKg?: number; reps?: number; seconds?: number },
  completedAt: string,
) {
  return {
    id: `${recordId}-${exerciseId}-${index}`,
    exerciseId,
    exerciseName,
    kind,
    weightKg: values.weightKg ?? null,
    reps: values.reps ?? null,
    seconds: values.seconds ?? null,
    completedAt,
  }
}

interface RecordExercise {
  exerciseId: string
  exerciseName: string
  kind: 'weight' | 'perSide' | 'duration'
  values: Array<{ weightKg?: number; reps?: number; seconds?: number }>
}

function buildRecord(
  id: string,
  date: string,
  dateLabel: string,
  dayTitle: string,
  planVersion: number,
  exercises: RecordExercise[],
  extra: Partial<TrainingRecord> = {},
): TrainingRecord {
  const start = `${date}T19:05:00+08:00`
  const base = Date.parse(start)
  const sets = exercises.flatMap((exercise, exerciseIndex) =>
    exercise.values.map((values, index) =>
      seedSet(
        id,
        exercise.exerciseId,
        exercise.exerciseName,
        exercise.kind,
        index + 1,
        values,
        new Date(base + (exerciseIndex * 9 + index * 3) * 60_000).toISOString(),
      ),
    ),
  )
  return {
    id,
    date,
    dateLabel,
    dayTitle,
    planVersion,
    completedAt: sets[sets.length - 1]?.completedAt ?? start,
    lateRecorded: false,
    sets,
    ...extra,
  }
}

export const TRAINING_HISTORY: TrainingRecord[] = [
  buildRecord('record-20261013', '2026-10-13', '10月13日 周二', '上肢力量', 6, [
    {
      exerciseId: 'a1',
      exerciseName: '哑铃卧推',
      kind: 'weight',
      values: [
        { weightKg: 10, reps: 10 },
        { weightKg: 10, reps: 10 },
        { weightKg: 10, reps: 9 },
        { weightKg: 10, reps: 8 },
      ],
    },
    {
      exerciseId: 'a2',
      exerciseName: '高位下拉',
      kind: 'weight',
      values: [
        { weightKg: 35, reps: 12 },
        { weightKg: 35, reps: 12 },
        { weightKg: 35, reps: 11 },
      ],
    },
    {
      exerciseId: 'a3',
      exerciseName: '单臂哑铃划船',
      kind: 'perSide',
      values: [
        { weightKg: 8, reps: 12 },
        { weightKg: 8, reps: 12 },
        { weightKg: 8, reps: 12 },
      ],
    },
    {
      exerciseId: 'a4',
      exerciseName: '平板支撑',
      kind: 'duration',
      values: [{ seconds: 40 }, { seconds: 38 }],
    },
  ]),
  buildRecord('record-20261016', '2026-10-16', '10月16日 周五', '上肢力量', 6, [
    {
      exerciseId: 'a1',
      exerciseName: '哑铃卧推',
      kind: 'weight',
      values: [
        { weightKg: 11.25, reps: 10 },
        { weightKg: 11.25, reps: 10 },
        { weightKg: 11.25, reps: 10 },
        { weightKg: 11.25, reps: 9 },
      ],
    },
    {
      exerciseId: 'a2',
      exerciseName: '高位下拉',
      kind: 'weight',
      values: [
        { weightKg: 37.5, reps: 12 },
        { weightKg: 37.5, reps: 12 },
        { weightKg: 37.5, reps: 12 },
      ],
    },
    {
      exerciseId: 'a3',
      exerciseName: '单臂哑铃划船',
      kind: 'perSide',
      values: [
        { weightKg: 9, reps: 12 },
        { weightKg: 9, reps: 12 },
        { weightKg: 9, reps: 12 },
      ],
    },
    {
      exerciseId: 'a4',
      exerciseName: '平板支撑',
      kind: 'duration',
      values: [{ seconds: 42 }, { seconds: 40 }],
    },
  ]),
  buildRecord('record-20261020', '2026-10-20', '10月20日 周二', '下肢力量', 6, [
    {
      exerciseId: 'b1',
      exerciseName: '高脚杯深蹲',
      kind: 'weight',
      values: [
        { weightKg: 16, reps: 10 },
        { weightKg: 16, reps: 10 },
        { weightKg: 16, reps: 10 },
        { weightKg: 16, reps: 9 },
      ],
    },
    {
      exerciseId: 'b2',
      exerciseName: '罗马尼亚硬拉',
      kind: 'weight',
      values: [
        { weightKg: 30, reps: 10 },
        { weightKg: 30, reps: 10 },
        { weightKg: 30, reps: 10 },
      ],
    },
    {
      exerciseId: 'b3',
      exerciseName: '箭步蹲',
      kind: 'perSide',
      values: [
        { weightKg: 8, reps: 12 },
        { weightKg: 8, reps: 12 },
        { weightKg: 8, reps: 12 },
      ],
    },
    {
      exerciseId: 'b4',
      exerciseName: '死虫式',
      kind: 'duration',
      values: [{ seconds: 40 }, { seconds: 40 }],
    },
  ]),
  buildRecord(
    'record-20261021',
    '2026-10-21',
    '10月21日 周三',
    '核心稳定',
    6,
    [
      {
        exerciseId: 'c1',
        exerciseName: '死虫式',
        kind: 'duration',
        values: [{ seconds: 40 }, { seconds: 40 }],
      },
      {
        exerciseId: 'c2',
        exerciseName: '鸟狗式',
        kind: 'perSide',
        values: [
          { weightKg: 0, reps: 10 },
          { weightKg: 0, reps: 10 },
        ],
      },
      {
        exerciseId: 'c3',
        exerciseName: '侧桥',
        kind: 'duration',
        values: [{ seconds: 30 }, { seconds: 30 }],
      },
      {
        exerciseId: 'c4',
        exerciseName: '平板支撑',
        kind: 'duration',
        values: [{ seconds: 40 }, { seconds: 35 }],
      },
    ],
    {
      lateRecorded: true,
      lateNote: '10月23日补记 · 原安排10月21日',
    },
  ),
  buildRecord(
    'record-20261022',
    '2026-10-22',
    '10月22日 周四',
    '上肢力量',
    6,
    [
      {
        exerciseId: 'a1',
        exerciseName: '哑铃卧推',
        kind: 'weight',
        values: [
          { weightKg: 12.5, reps: 10 },
          { weightKg: 12.5, reps: 10 },
          { weightKg: 12.5, reps: 9 },
          { weightKg: 12.5, reps: 8 },
        ],
      },
      {
        exerciseId: 'a2',
        exerciseName: '高位下拉',
        kind: 'weight',
        values: [
          { weightKg: 40, reps: 12 },
          { weightKg: 40, reps: 12 },
          { weightKg: 40, reps: 12 },
        ],
      },
      {
        exerciseId: 'a3',
        exerciseName: '单臂哑铃划船',
        kind: 'perSide',
        values: [
          { weightKg: 10, reps: 12 },
          { weightKg: 10, reps: 12 },
          { weightKg: 10, reps: 12 },
        ],
      },
      {
        exerciseId: 'a4',
        exerciseName: '平板支撑',
        kind: 'duration',
        values: [{ seconds: 45 }, { seconds: 45 }],
      },
    ],
    {
      feedback: {
        author: COACH_NAME,
        dateLabel: '10月22日 21:10',
        text: '卧推最后一组比上次稳，落杠速度控制住了。下次保持 12.5 kg，先把次数补齐，不用急着加重量。',
        sourceLabel: '力量基础 · 版本 6',
      },
    },
  ),
]

TRAINING_HISTORY.push(
  buildRecord('record-20260929', '2026-09-29', '9月29日 周二', '上肢力量', 5, [
    {
      exerciseId: 'a1',
      exerciseName: '哑铃卧推',
      kind: 'weight',
      values: [
        { weightKg: 9.5, reps: 10 },
        { weightKg: 9.5, reps: 10 },
        { weightKg: 9.5, reps: 9 },
        { weightKg: 9.5, reps: 8 },
      ],
    },
    {
      exerciseId: 'a2',
      exerciseName: '高位下拉',
      kind: 'weight',
      values: [
        { weightKg: 32.5, reps: 12 },
        { weightKg: 32.5, reps: 12 },
        { weightKg: 32.5, reps: 11 },
      ],
    },
    {
      exerciseId: 'a3',
      exerciseName: '单臂哑铃划船',
      kind: 'perSide',
      values: [
        { weightKg: 7.5, reps: 12 },
        { weightKg: 7.5, reps: 12 },
        { weightKg: 7.5, reps: 12 },
      ],
    },
    {
      exerciseId: 'a4',
      exerciseName: '平板支撑',
      kind: 'duration',
      values: [{ seconds: 35 }, { seconds: 35 }],
    },
  ]),
  buildRecord('record-20260922', '2026-09-22', '9月22日 周二', '上肢力量', 5, [
    {
      exerciseId: 'a1',
      exerciseName: '哑铃卧推',
      kind: 'weight',
      values: [
        { weightKg: 9, reps: 10 },
        { weightKg: 9, reps: 10 },
        { weightKg: 9, reps: 8 },
        { weightKg: 9, reps: 8 },
      ],
    },
    {
      exerciseId: 'a2',
      exerciseName: '高位下拉',
      kind: 'weight',
      values: [
        { weightKg: 30, reps: 12 },
        { weightKg: 30, reps: 12 },
        { weightKg: 30, reps: 10 },
      ],
    },
    {
      exerciseId: 'a3',
      exerciseName: '单臂哑铃划船',
      kind: 'perSide',
      values: [
        { weightKg: 7.5, reps: 12 },
        { weightKg: 7.5, reps: 12 },
        { weightKg: 7.5, reps: 11 },
      ],
    },
    {
      exerciseId: 'a4',
      exerciseName: '平板支撑',
      kind: 'duration',
      values: [{ seconds: 32 }, { seconds: 30 }],
    },
  ]),
  buildRecord('record-20260915', '2026-09-15', '9月15日 周二', '上肢力量', 5, [
    {
      exerciseId: 'a1',
      exerciseName: '哑铃卧推',
      kind: 'weight',
      values: [
        { weightKg: 8.75, reps: 10 },
        { weightKg: 8.75, reps: 9 },
        { weightKg: 8.75, reps: 8 },
        { weightKg: 8.75, reps: 8 },
      ],
    },
    {
      exerciseId: 'a2',
      exerciseName: '高位下拉',
      kind: 'weight',
      values: [
        { weightKg: 30, reps: 12 },
        { weightKg: 30, reps: 10 },
        { weightKg: 30, reps: 10 },
      ],
    },
    {
      exerciseId: 'a3',
      exerciseName: '单臂哑铃划船',
      kind: 'perSide',
      values: [
        { weightKg: 7.5, reps: 12 },
        { weightKg: 7.5, reps: 10 },
        { weightKg: 7.5, reps: 10 },
      ],
    },
    {
      exerciseId: 'a4',
      exerciseName: '平板支撑',
      kind: 'duration',
      values: [{ seconds: 30 }, { seconds: 30 }],
    },
  ]),
)

export function createFutureSchedule(plan: PlanContent, version: number): ScheduleSlot[] {
  const slots: Array<{ date: string; dateLabel: string; dayIndex: number }> = [
    { date: '2026-10-27', dateLabel: '10月27日 周二', dayIndex: 0 },
    { date: '2026-10-29', dateLabel: '10月29日 周四', dayIndex: 1 },
    { date: '2026-10-31', dateLabel: '10月31日 周六', dayIndex: 0 },
  ]
  return slots.map((slot) => {
    const day = plan.days[slot.dayIndex] ?? plan.days[0]
    return {
      date: slot.date,
      dateLabel: slot.dateLabel,
      dayId: day?.id ?? 'day-a',
      dayTitle: day?.title ?? '训练日',
      planVersion: version,
    }
  })
}

const seedCoach = createSeedCoachState()
const latestDelivery = seedCoach.deliveries[0]!
const incomingDelivery: IncomingDelivery = {
  id: `delivery-${latestDelivery.version}`,
  version: latestDelivery.version,
  baseVersion: 6,
  content: structuredClone(latestDelivery.content),
  hash: planHash(latestDelivery.content),
  deliveredAt: latestDelivery.deliveredAt,
  messageId: 'm-plan-7',
  status: 'new',
  available: true,
}

export function createSeedStudentState(): StudentState {
  return {
    plan: structuredClone(SEED_PUBLISHED_PLAN),
    planVersion: 6,
    adoptedVersion: null,
    adoptedAt: null,
    incoming: structuredClone(incomingDelivery),
    schedule: createFutureSchedule(SEED_PUBLISHED_PLAN, 6),
  }
}

export function createSeedRelations(): Relation[] {
  return [
    {
      id: 'rel-gu-yan',
      coachId: 'gu-yan',
      packageId: 'pkg-strength',
      status: 'active',
      agreedPriceYuan: 699,
      agreedPriceUnit: '/ 月',
      agreedCycle: '4 周',
      agreedName: '力量基础 · 月度计划',
      agreedAt: '2026-10-02T10:20:00+08:00',
      serviceVersion: 2,
      healthAuthorized: true,
      healthRevokedAt: null,
      planVersion: 6,
      readOnly: false,
    },
    {
      id: 'rel-jiang-yao',
      coachId: 'jiang-yao',
      packageId: 'pkg-endurance',
      status: 'pending',
      agreedPriceYuan: 560,
      agreedPriceUnit: '/ 月',
      agreedCycle: '4 周',
      agreedName: '耐力基础 · 月度计划',
      agreedAt: '2026-10-24T10:12:00+08:00',
      serviceVersion: 1,
      healthAuthorized: false,
      healthRevokedAt: null,
      planVersion: 0,
      readOnly: false,
    },
    {
      id: 'rel-zhou-lan',
      coachId: 'gu-yan',
      packageId: 'pkg-endurance',
      status: 'active',
      agreedPriceYuan: 560,
      agreedPriceUnit: '/ 月',
      agreedCycle: '4 周',
      agreedName: '耐力基础 · 月度计划',
      agreedAt: '2026-10-02T10:30:00+08:00',
      serviceVersion: 1,
      healthAuthorized: false,
      healthRevokedAt: null,
      planVersion: 3,
      readOnly: false,
    },
    {
      id: 'rel-he-an',
      coachId: 'he-an',
      packageId: 'pkg-mobility',
      status: 'ended',
      agreedPriceYuan: 480,
      agreedPriceUnit: '/ 次',
      agreedCycle: '单次',
      agreedName: '活动度与恢复 · 单次',
      agreedAt: '2026-09-01T09:00:00+08:00',
      serviceVersion: 1,
      healthAuthorized: false,
      healthRevokedAt: '2026-09-30T18:00:00+08:00',
      planVersion: 2,
      readOnly: true,
    },
  ]
}

export function createSeedConversations(): Conversation[] {
  return [
    {
      id: 'conv-gu-yan',
      coachId: 'gu-yan',
      coachName: COACH_NAME,
      studentId: 'lin-che',
      serviceLabel: '力量基础 · 月度计划',
      packageId: 'pkg-strength',
      relationId: 'rel-gu-yan',
      readBy: { student: 'm-health-1', coach: 'm-plan-7' },
      updatedAt: '2026-10-23T21:40:00+08:00',
      kind: 'service',
      drafts: { student: '', coach: '' },
      messages: [
        {
          id: 'm-feedback-1',
          author: 'coach',
          authorName: COACH_NAME,
          text: '今天卧推最后一组比上次稳，落杠速度控制住了。核心那组如果腰酸，缩短到 30 秒也算完成。',
          sentAt: '2026-10-22T21:10:00+08:00',
          status: 'sent',
          refs: [
            {
              kind: 'training',
              id: 'record-20261022',
              label: '10月22日 · 上肢力量',
              meta: '12 组已记录',
            },
          ],
        },
        {
          id: 'm-health-1',
          author: 'student',
          authorName: STUDENT_NAME,
          text: '右肩这两天有点紧，推举时不太舒服，需要停一下吗？',
          sentAt: '2026-10-23T09:02:00+08:00',
          status: 'sent',
          health: true,
        },
        {
          id: 'm-health-2',
          author: 'coach',
          authorName: COACH_NAME,
          text: '这周先不做过头推举，卧推保持现在的重量；如果疼到影响日常活动，先找专业人员看一下。下周我再看你的反馈。',
          sentAt: '2026-10-23T12:10:00+08:00',
          status: 'sent',
          health: true,
        },
        {
          id: 'm-plan-7',
          author: 'coach',
          authorName: COACH_NAME,
          text: '新版计划发给你了：高位下拉从 12 次改成 10 次、备注写清了这周的调整，划船加到 4 组，平板支撑 50 秒。共 4 处调整，你看完整内容后再决定是否采用。',
          sentAt: '2026-10-23T21:40:00+08:00',
          status: 'sent',
          refs: [
            {
              kind: 'plan',
              id: 'delivery-7',
              label: '版本 7 · 力量基础',
              meta: '4 处调整 · 尚未采用',
            },
          ],
        },
      ],
    },
    {
      id: 'conv-jiang-yao',
      coachId: 'jiang-yao',
      coachName: '江遥',
      studentId: 'lin-che',
      serviceLabel: '耐力基础 · 咨询',
      packageId: 'pkg-endurance',
      relationId: 'rel-jiang-yao',
      readBy: { student: 'm-consult-1', coach: null },
      updatedAt: '2026-10-24T10:12:00+08:00',
      kind: 'consult',
      drafts: { student: '', coach: '' },
      messages: [
        {
          id: 'm-consult-1',
          author: 'student',
          authorName: STUDENT_NAME,
          text: '想了解一下耐力训练计划。我每周能练三次，主要想让跑步不那么容易累。',
          sentAt: '2026-10-24T10:12:00+08:00',
          status: 'sent',
          refs: [
            {
              kind: 'service',
              id: 'pkg-endurance',
              label: '耐力基础 · 月度计划',
              meta: '¥560 / 月 · 线上',
            },
          ],
        },
      ],
    },
    {
      id: 'conv-zhou-lan',
      coachId: 'gu-yan',
      coachName: COACH_NAME,
      studentId: 'zhou-lan',
      serviceLabel: '耐力基础 · 月度计划',
      packageId: 'pkg-endurance',
      relationId: 'rel-zhou-lan',
      readBy: { student: 'm-zhou-1', coach: 'm-zhou-1' },
      updatedAt: '2026-10-15T20:12:00+08:00',
      kind: 'service',
      drafts: { student: '', coach: '' },
      messages: [
        {
          id: 'm-zhou-1',
          author: 'coach',
          authorName: COACH_NAME,
          text: '这周先按周二、周四的耐力安排走，强度以能说完整句子为准。',
          sentAt: '2026-10-15T20:12:00+08:00',
          status: 'sent',
        },
      ],
    },
    {
      id: 'conv-he-an',
      coachId: 'he-an',
      coachName: '何岸',
      studentId: 'lin-che',
      serviceLabel: '活动度与恢复 · 已结束',
      packageId: 'pkg-mobility',
      relationId: 'rel-he-an',
      readBy: { student: 'm-ended-1', coach: 'm-ended-1' },
      updatedAt: '2026-09-28T17:00:00+08:00',
      kind: 'service',
      drafts: { student: '', coach: '' },
      messages: [
        {
          id: 'm-ended-1',
          author: 'coach',
          authorName: '何岸',
          text: '这周的活动度安排还是放在训练后，幅度以不引起刺痛为准。下次见面我们再核对一次。',
          sentAt: '2026-09-28T17:00:00+08:00',
          status: 'sent',
        },
      ],
    },
  ]
}

export function createSeedTodoItems(): TodoItem[] {
  return [
    {
      id: 'todo-reply',
      studentId: 'lin-che',
      studentName: STUDENT_NAME,
      reason: 'reply',
      title: '回复训练反馈',
      detail: '10月22日上肢力量 · 卧推最后一组 12.5 kg × 8',
      dueLabel: '今天',
      priority: 1,
      done: false,
      targetScreen: 'messages',
      conversationId: 'conv-gu-yan',
      healthRelated: true,
    },
    {
      id: 'todo-delivery',
      studentId: 'lin-che',
      studentName: STUDENT_NAME,
      reason: 'delivery',
      title: '跟进版本 7 的采用',
      detail: '10月23日已交付 · 学员尚未采用',
      dueLabel: '今天',
      priority: 2,
      done: false,
      targetScreen: 'incomingPlan',
    },
    {
      id: 'todo-auth',
      studentId: 'zhou-lan',
      studentName: '周岚',
      reason: 'authorization',
      title: '健康内容未授权',
      detail: '健康记录与饮食观察不可见；计划与服务记录仍可查看',
      dueLabel: '本周',
      priority: 2,
      done: false,
      targetScreen: 'coachStudentDetail',
    },
    {
      id: 'todo-relation',
      studentId: 'an-ran',
      studentName: '安然',
      reason: 'relation',
      title: '服务约定待确认',
      detail: '咨询已收到 · 尚未建立服务关系',
      dueLabel: '今天',
      priority: 3,
      done: false,
      targetScreen: 'coachStudents',
    },
    {
      id: 'todo-draft',
      studentId: 'zhou-lan',
      studentName: '周岚',
      reason: 'draft',
      title: '计划草稿未保存',
      detail: '周四训练日有 2 处未保存改动',
      dueLabel: '今天',
      priority: 3,
      done: false,
      targetScreen: 'coachPlan',
    },
  ]
}

export function createSeedStudents(): CoachStudent[] {
  return [
    {
      id: 'lin-che',
      name: STUDENT_NAME,
      relationStatus: 'active',
      relationId: 'rel-gu-yan',
      packageName: '力量基础 · 月度计划',
      planVersion: 6,
      deliveredVersion: 7,
      lastFact: '10月22日 · 上肢力量完成 12 组',
      lastFactAt: '10月22日 19:41',
      lastFactHealth: true,
      pendingWork: '回复 10月22日训练反馈',
      pendingWorkHealth: true,
      serviceEnded: false,
      stale: false,
      records: [
        {
          id: 'lin-1',
          dateLabel: '10月22日',
          title: '上肢力量 · 12 组',
          summary: '卧推 12.5 kg × 10/10/9/8；平板支撑 45 秒 × 2',
          kind: 'training',
        },
        {
          id: 'lin-2',
          dateLabel: '10月23日',
          title: '教练点评',
          summary: '保持 12.5 kg，先把次数补齐，不用急着加重量。',
          kind: 'feedback',
        },
        {
          id: 'lin-3',
          dateLabel: '10月24日',
          title: '饮食自评',
          summary: '按计划 · 晚餐蔬菜偏少',
          kind: 'self-report',
          private: true,
        },
        {
          id: 'lin-4',
          dateLabel: '10月23日',
          title: '计划版本 7',
          summary: '已交付 · 尚未采用；现用版本 6',
          kind: 'plan',
        },
      ],
    },
    {
      id: 'zhou-lan',
      name: '周岚',
      relationStatus: 'active',
      relationId: 'rel-zhou-lan',
      packageName: '耐力基础 · 月度计划',
      planVersion: 3,
      deliveredVersion: 3,
      lastFact: '10月21日 · 基础耐力完成 8 组',
      lastFactAt: '10月21日 20:05',
      lastFactHealth: true,
      pendingWork: '计划草稿待保存',
      planDraftStatus: '计划草稿待保存',
      serviceEnded: false,
      stale: false,
      records: [
        {
          id: 'zhou-1',
          dateLabel: '10月21日',
          title: '基础耐力 · 8 组',
          summary: '走跑交替完成；强度与感受未授权查看',
          kind: 'training',
          hidden: true,
        },
        {
          id: 'zhou-3',
          dateLabel: '10月22日',
          title: '计划版本 3',
          summary: '服务与计划安排可见；健康记录不可见',
          kind: 'plan',
        },
        {
          id: 'zhou-2',
          dateLabel: '10月22日',
          title: '饮食观察',
          summary: '未授权查看',
          kind: 'self-report',
          hidden: true,
        },
      ],
    },
    {
      id: 'an-ran',
      name: '安然',
      relationStatus: 'pending',
      relationId: 'rel-jiang-yao',
      packageName: '动作调整 · 单次（待确认）',
      planVersion: 0,
      deliveredVersion: null,
      lastFact: '10月24日 · 发起服务咨询',
      lastFactAt: '10月24日 09:40',
      lastFactHealth: false,
      pendingWork: '确认服务范围与时间',
      serviceEnded: false,
      stale: false,
      records: [],
    },
    {
      id: 'chen-mo',
      name: '陈墨',
      relationStatus: 'ended',
      relationId: 'rel-he-an',
      packageName: '活动度与恢复 · 单次',
      planVersion: 2,
      deliveredVersion: null,
      lastFact: '9月30日 · 服务结束',
      lastFactAt: '9月30日 18:00',
      lastFactHealth: false,
      pendingWork: '',
      serviceEnded: true,
      stale: true,
      records: [
        {
          id: 'chen-1',
          dateLabel: '9月28日',
          title: '活动度安排',
          summary: '服务结束后的历史记录；只读，不再更新',
          kind: 'plan',
        },
      ],
    },
  ]
}

const OWN_PLAN_CONTENT: PlanContent = {
  title: '我的一周安排',
  cycle: '本周',
  days: [
    {
      id: 'own-day-a',
      title: '上肢与核心',
      exercises: [
        {
          id: 'oa1',
          name: '哑铃卧推',
          sets: '4',
          target: '10 次',
          restSeconds: '90',
          note: '保留 2 次余力；肩部不适时先停',
        },
        {
          id: 'oa2',
          name: '单臂哑铃划船',
          sets: '3',
          target: '12 次/侧',
          restSeconds: '60',
          note: '髋部保持稳定',
        },
        {
          id: 'oa3',
          name: '平板支撑',
          sets: '2',
          target: '45 秒',
          restSeconds: '60',
          note: '',
        },
      ],
    },
    {
      id: 'own-day-b',
      title: '下肢与髋',
      exercises: [
        {
          id: 'ob1',
          name: '高脚杯深蹲',
          sets: '4',
          target: '10 次',
          restSeconds: '90',
          note: '膝盖朝脚尖方向',
        },
        {
          id: 'ob2',
          name: '罗马尼亚硬拉',
          sets: '3',
          target: '10 次',
          restSeconds: '90',
          note: '髋部后移，背部保持中立',
        },
      ],
    },
  ],
}

export function createSeedZhouCoachState(): CoachState {
  const published: PlanContent = {
    title: '耐力基础',
    cycle: '第 2 周',
    days: [
      {
        id: 'zhou-day-a',
        title: '周二 · 基础耐力',
        exercises: [
          {
            id: 'z-a1',
            name: '走跑交替',
            sets: '4',
            target: '3 分钟',
            restSeconds: '90',
            note: '快走与慢跑交替，保持能说话',
          },
          {
            id: 'z-a2',
            name: '徒手深蹲',
            sets: '3',
            target: '12 次',
            restSeconds: '60',
            note: '动作放慢，膝盖朝脚尖方向',
          },
          {
            id: 'z-a3',
            name: '平板支撑',
            sets: '2',
            target: '40 秒',
            restSeconds: '45',
            note: '保持骨盆中立',
          },
        ],
      },
      {
        id: 'zhou-day-b',
        title: '周四 · 走跑间歇',
        exercises: [
          {
            id: 'z-b1',
            name: '快走热身',
            sets: '3',
            target: '5 分钟',
            restSeconds: '60',
            note: '微微出汗即可',
          },
          {
            id: 'z-b2',
            name: '间歇跑',
            sets: '4',
            target: '1 分钟',
            restSeconds: '90',
            note: '每组之间用慢走恢复',
          },
        ],
      },
    ],
  }
  const draft = sanitizePlan(structuredClone(published))
  draft.days[0]!.exercises[0]!.target = '4 分钟'
  draft.days[1]!.exercises[1]!.sets = '5'
  const delivery = {
    version: 3,
    content: structuredClone(published),
    deliveredAt: '2026-10-15T20:10:00+08:00',
    coachName: COACH_NAME,
  }
  return {
    published,
    publishedVersion: 3,
    activeStudentVersion: 3,
    draft,
    draftVersion: 4,
    savedContent: serializePlan(published),
    savedAt: '2026-10-15T20:10:00+08:00',
    deliveries: [delivery],
  }
}

export function createSeedOwnPlan(): OwnPlanState {
  const draft = sanitizePlan(structuredClone(OWN_PLAN_CONTENT))
  return {
    title: draft.title,
    cycle: draft.cycle,
    days: draft.days,
    version: 3,
    savedContent: serializePlan(draft),
    savedAt: '2026-10-24T12:10:00+08:00',
    visibility: 'private',
    status: 'draft',
    healthNote: '右肩紧时不做推举',
    reviewHash: null,
    confirmedReviewHash: null,
    publishedContent: null,
    takenDownReason: null,
    remoteVersion: null,
    remoteContent: null,
    remoteAt: null,
  }
}

export const AI_TEMPLATES: AiTemplate[] = [
  {
    id: 'ai-stable-reps',
    title: '把卧推目标改为 8 次',
    intent: '这周卧推想做得稳一点，不想加重量。',
    context: 'own',
    status: 'ready',
    explanation: '基于已有模板「保守次数调整」：只改目标次数与备注，不改变现用安排。',
    missing: null,
    safety: null,
    sourceLabel: '已有模板 · 固定候选',
    templateVersion: 1,
    basePlanVersion: 3,
    requiresInput: false,
    note: '先把每组做到 8 次且保留余力，再考虑加重量。',
    changes: [
      {
        dayId: 'own-day-a',
        dayTitle: '上肢与核心',
        exerciseId: 'oa1',
        exerciseName: '哑铃卧推',
        field: 'target',
        label: '目标次数/时长',
        before: '10 次',
        after: '8 次',
      },
      {
        dayId: 'own-day-a',
        dayTitle: '上肢与核心',
        exerciseId: 'oa1',
        exerciseName: '哑铃卧推',
        field: 'note',
        label: '备注',
        before: '保留 2 次余力；肩部不适时先停',
        after: '保留 2 次余力；肩部不适时先停。先把 8 次做稳。',
      },
    ],
  },
  {
    id: 'ai-volume',
    title: '本周减少一组卧推',
    intent: '这周时间紧，想把上肢训练缩短一点。',
    context: 'own',
    status: 'ready',
    explanation: '基于已有模板「训练量下调」：只减少一处组数，其余内容保持。',
    missing: null,
    safety: null,
    sourceLabel: '已有模板 · 固定候选',
    templateVersion: 1,
    basePlanVersion: 3,
    requiresInput: false,
    note: '减少一组后，先把动作完成质量保持住；下周可以再评估。',
    changes: [
      {
        dayId: 'own-day-a',
        dayTitle: '上肢与核心',
        exerciseId: 'oa1',
        exerciseName: '哑铃卧推',
        field: 'sets',
        label: '组数',
        before: '4',
        after: '3',
      },
    ],
  },
  {
    id: 'ai-coach-plan',
    title: '教练计划中的调整',
    intent: '把教练给的卧推安排改轻一点。',
    context: 'coach',
    status: 'safety_block',
    explanation: '这是教练交付的计划，学员不能自助改写；可以把具体想法发给教练。',
    missing: null,
    safety: '教练计划只能由教练确认调整，避免覆盖教练已经核对过的内容。',
    sourceLabel: '教练计划 · 不可自助修改',
    templateVersion: 1,
    basePlanVersion: 7,
    requiresInput: false,
    note: '',
    changes: [],
  },
]

export function createSeedAi(): AiState {
  return {
    context: 'own',
    templateId: 'ai-stable-reps',
    intent: '这周卧推想做得稳一点，不想加重量。',
    appliedIds: [],
    appliedHash: null,
    lastAppliedAt: null,
    confirmedVersion: null,
    baseVersion: null,
  }
}

export function createSeedDiet(): DietState {
  return {
    selectedItemId: 'diet-training-day',
    saveState: 'idle',
    contentRisk: false,
    report: {
      date: PREVIEW_TODAY,
      status: null,
      observation: '',
      savedAt: null,
      coachVisible: true,
    },
  }
}

export function createSeedServices(): CoachServicesState {
  const drafts: CoachServicesState['drafts'] = {}
  for (const pack of GUYAN_PACKAGES) {
    drafts[pack.id] = {
      packageId: pack.id,
      description: pack.summary,
      scope: pack.includes.join('；'),
      goalsText: pack.goals.join(' · '),
      response: pack.id === 'pkg-consult' ? '工作日 24 小时内回复' : '工作日 24 小时内回复',
      priceInput: String(pack.priceYuan),
      priceYuan: pack.priceYuan,
      priceUnit: pack.priceUnit,
      cycle: pack.cycle,
      visible: pack.visible,
      version: pack.version,
    }
  }
  return {
    drafts,
    saved: structuredClone(drafts),
    savedAt: '2026-10-23T20:15:00+08:00',
    version: 2,
    previewPackageId: 'pkg-strength',
  }
}

export function createDefaultPreview(): PreviewFlags {
  return {
    today: 'in_progress',
    explore: 'normal',
    planDetail: 'normal',
    training: 'normal',
    records: 'normal',
    messages: 'normal',
    incomingPlan: 'new',
    myPlan: 'normal',
    ai: 'ready',
    diet: 'unrecorded',
    coachList: 'normal',
    coachDetail: 'normal',
    coachTodo: 'normal',
    coachStudents: 'normal',
    coachPlan: 'normal',
    coachServices: 'normal',
  }
}

export interface ExtendedSeed {
  todayMode: PreviewFlags['today']
  student: StudentState
  records: TrainingRecord[]
  coaches: CoachProfile[]
  relations: Relation[]
  conversations: Conversation[]
  todoItems: TodoItem[]
  students: CoachStudent[]
  ownPlan: OwnPlanState
  ai: AiState
  diet: DietState
  services: CoachServicesState
  preview: PreviewFlags
}

export function createExtendedSeed(): ExtendedSeed {
  return {
    todayMode: 'in_progress',
    student: createSeedStudentState(),
    records: structuredClone(TRAINING_HISTORY),
    coaches: structuredClone(COACH_PROFILES),
    relations: createSeedRelations(),
    conversations: createSeedConversations(),
    todoItems: createSeedTodoItems(),
    students: createSeedStudents(),
    ownPlan: createSeedOwnPlan(),
    ai: createSeedAi(),
    diet: createSeedDiet(),
    services: createSeedServices(),
    preview: createDefaultPreview(),
  }
}

export const PREVIEW_COPY = {
  sample: '本地交互预览 · 人物与内容为样例，记录只保存在本机浏览器。',
  today: PREVIEW_TODAY,
}
