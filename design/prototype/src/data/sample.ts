import type {
  CoachState,
  ExploreFilters,
  ExploreItem,
  PlanContent,
  SessionExercise,
  SessionSet,
  SessionState,
} from '../types'
import { sanitizePlan, serializePlan } from '../utils/plan'

export const PREVIEW_DATE_LABEL = '10月24日 周六'
export const PREVIEW_WEEK_LABEL = '10月19日–25日'
export const NEXT_SESSION_LABEL = '10月27日 周二'
export const STORAGE_KEY = 'nerae.round3.prototype.v1'

export const STUDENT_NAME = '林澈'
export const COACH_NAME = '顾言'

export const DONE_DATES = ['2026-10-20', '2026-10-22']
export const TODAY_DATE = '2026-10-24'

export const WEEK_DAYS = [
  { key: 'mon', label: '周一', date: '19', training: false, past: false },
  { key: 'tue', label: '周二', date: '20', training: true, past: true },
  { key: 'wed', label: '周三', date: '21', training: false, past: true },
  { key: 'thu', label: '周四', date: '22', training: true, past: true },
  { key: 'fri', label: '周五', date: '23', training: false, past: true },
  { key: 'sat', label: '周六', date: '24', training: true, past: false },
  { key: 'sun', label: '周日', date: '25', training: false, past: false },
]

const STARTED_AT = '2026-10-24T19:04:00+08:00'

function createSeededSets(
  exerciseId: string,
  count: number,
  completed: Array<{
    weightKg?: number
    reps?: number
    seconds?: number
    at: string
  }>,
): SessionSet[] {
  return Array.from({ length: count }, (_, index) => {
    const record = completed[index]
    if (!record) {
      return {
        id: `s-${exerciseId}-${index + 1}`,
        index: index + 1,
        status: 'pending',
        weightKg: null,
        reps: null,
        seconds: null,
        completedAt: null,
      }
    }
    return {
      id: `s-${exerciseId}-${index + 1}`,
      index: index + 1,
      status: 'done',
      weightKg: record.weightKg ?? null,
      reps: record.reps ?? null,
      seconds: record.seconds ?? null,
      completedAt: record.at,
    }
  })
}

function createExercises(): SessionExercise[] {
  return [
    {
      id: 'a1',
      name: '哑铃卧推',
      kind: 'weight',
      equipment: '哑铃 · 平凳',
      cue: '肩胛后收，肘部略低于肩线；下放时控制两秒，推起时不要锁死肘关节。',
      restSeconds: 90,
      targetText: '10 次',
      lastRef: { dateLabel: '10月22日', weightKg: 12.5, reps: 10 },
      sets: createSeededSets('a1', 4, [
        { weightKg: 12.5, reps: 10, at: '2026-10-24T19:06:00+08:00' },
        { weightKg: 12.5, reps: 10, at: '2026-10-24T19:09:00+08:00' },
        { weightKg: 12.5, reps: 9, at: '2026-10-24T19:12:00+08:00' },
      ]),
    },
    {
      id: 'a2',
      name: '高位下拉',
      kind: 'weight',
      equipment: '高位下拉器',
      cue: '先沉肩，再把横杆拉向锁骨上方；不要后仰借力，回放保持控制。',
      restSeconds: 75,
      targetText: '12 次',
      lastRef: { dateLabel: '10月22日', weightKg: 40, reps: 12 },
      sets: createSeededSets('a2', 3, []),
    },
    {
      id: 'a3',
      name: '单臂哑铃划船',
      kind: 'perSide',
      equipment: '哑铃 · 平凳',
      cue: '髋部保持稳定，肘部贴近身体；顶端停一秒，下放不要松掉核心。',
      restSeconds: 60,
      targetText: '12 次/侧',
      lastRef: { dateLabel: '10月22日', weightKg: 10, reps: 12, perSide: true },
      sets: createSeededSets('a3', 3, []),
    },
    {
      id: 'a4',
      name: '平板支撑',
      kind: 'duration',
      equipment: '瑜伽垫',
      cue: '保持骨盆中立，正常呼吸；一旦塌腰就停下，不必硬撑到目标时间。',
      restSeconds: 60,
      targetText: '45 秒',
      lastRef: { dateLabel: '10月22日', seconds: 45 },
      sets: createSeededSets('a4', 2, []),
    },
  ]
}

export function createSeedSession(): SessionState {
  return {
    id: 'session-20261024-strength',
    planVersion: 6,
    planTitle: '力量基础',
    dayTitle: '上肢力量',
    dateLabel: PREVIEW_DATE_LABEL,
    startedAt: STARTED_AT,
    status: 'in_progress',
    completedAt: null,
    exercises: createExercises(),
    rest: null,
  }
}

const PUBLISHED_PLAN: PlanContent = {
  title: '力量基础',
  cycle: '第 4 周',
  days: [
    {
      id: 'day-a',
      title: '上肢力量',
      exercises: [
        {
          id: 'a1',
          name: '哑铃卧推',
          sets: '4',
          target: '10 次',
          restSeconds: '90',
          note: '重量以能完成 10 次、保留 2 次余力为准',
        },
        {
          id: 'a2',
          name: '高位下拉',
          sets: '3',
          target: '12 次',
          restSeconds: '75',
          note: '先沉肩，再把横杆拉向锁骨',
        },
        {
          id: 'a3',
          name: '单臂哑铃划船',
          sets: '3',
          target: '12 次/侧',
          restSeconds: '60',
          note: '髋部保持稳定，肘部贴近身体',
        },
        {
          id: 'a4',
          name: '平板支撑',
          sets: '2',
          target: '45 秒',
          restSeconds: '60',
          note: '保持骨盆中立，正常呼吸',
        },
      ],
    },
    {
      id: 'day-b',
      title: '下肢力量',
      exercises: [
        {
          id: 'b1',
          name: '高脚杯深蹲',
          sets: '4',
          target: '10 次',
          restSeconds: '90',
          note: '膝盖朝脚尖方向，起身时呼气',
        },
        {
          id: 'b2',
          name: '罗马尼亚硬拉',
          sets: '3',
          target: '10 次',
          restSeconds: '90',
          note: '髋部后移，背部保持中立',
        },
        {
          id: 'b3',
          name: '箭步蹲',
          sets: '3',
          target: '12 次/侧',
          restSeconds: '75',
          note: '前脚踩稳，躯干保持直立',
        },
        {
          id: 'b4',
          name: '死虫式',
          sets: '2',
          target: '40 秒',
          restSeconds: '45',
          note: '腰部贴地，动作放慢',
        },
      ],
    },
  ],
}

export const SEED_PUBLISHED_PLAN: PlanContent = structuredClone(PUBLISHED_PLAN)

export function createSeedDraft(): PlanContent {
  const draft = structuredClone(PUBLISHED_PLAN)
  const dayA = draft.days[0]
  const pulldown = dayA.exercises[1]
  const row = dayA.exercises[2]
  const plank = dayA.exercises[3]
  pulldown.target = '10 次'
  pulldown.note = '上一周期末两组完成吃紧，本周保持 10 次，动作更稳'
  row.sets = '4'
  plank.target = '50 秒'
  return sanitizePlan(draft)
}

export function createSeedCoachState(): CoachState {
  const draft = createSeedDraft()
  return {
    published: SEED_PUBLISHED_PLAN,
    publishedVersion: 6,
    activeStudentVersion: 6,
    draft,
    draftVersion: 7,
    savedContent: serializePlan(draft),
    savedAt: '2026-10-23T21:40:00+08:00',
    deliveredVersion: null,
    deliveredAt: null,
    deliveries: [],
  }
}

export const DEFAULT_FILTERS: ExploreFilters = {
  goal: 'all',
  equipment: 'all',
  duration: 'all',
}

export const TRAINING_GOALS = [
  { value: 'all', label: '全部' },
  { value: 'strength', label: '力量' },
  { value: 'muscle', label: '增肌' },
  { value: 'mobility', label: '舒展' },
]

export const TRAINING_EQUIPMENT = [
  { value: 'all', label: '全部' },
  { value: 'dumbbell', label: '哑铃' },
  { value: 'machine', label: '器械' },
  { value: 'none', label: '自重' },
]

export const TRAINING_DURATION = [
  { value: 'all', label: '全部' },
  { value: 'short', label: '20 分钟内' },
  { value: 'medium', label: '30 分钟内' },
  { value: 'long', label: '45 分钟内' },
]

export const DIET_GOALS = [
  { value: 'all', label: '全部' },
  { value: 'training', label: '训练日' },
  { value: 'rest', label: '休息日' },
]

export const EXPLORE_ITEMS: ExploreItem[] = [
  {
    id: 'plan-upper-pull',
    type: 'training',
    title: '上肢拉力与划船',
    image: '/assets/explore-featured.webp',
    imageAlt: '训练者在健身房用弹力带完成上肢拉力动作',
    goal: 'muscle',
    equipment: 'machine',
    durationMin: 30,
    perWeek: '每周 2 次',
    countLabel: '5 个动作',
    source: 'Nerae 训练内容 · 免费',
    summary: '以高位下拉与坐姿划船为主，补足背部和上臂拉力，适合已有基础动作经验的人。',
    fit: ['适合：想加强背部与上臂拉力', '需要：高位下拉器、坐姿划船器、哑铃', '建议两次训练间隔 48 小时'],
    contents: [
      { name: '高位下拉', detail: '3 组 × 12 次 · 组间 75 秒' },
      { name: '坐姿划船', detail: '3 组 × 12 次 · 组间 75 秒' },
      { name: '面拉', detail: '2 组 × 15 次 · 组间 60 秒' },
      { name: '哑铃弯举', detail: '2 组 × 12 次 · 组间 60 秒' },
      { name: '平板支撑', detail: '2 组 × 40 秒 · 组间 60 秒' },
    ],
  },
  {
    id: 'plan-core',
    type: 'training',
    title: '核心稳定 · 20 分钟',
    image: '/assets/explore-plank.webp',
    imageAlt: '训练者在家中的瑜伽垫上完成平板支撑',
    goal: 'strength',
    equipment: 'none',
    durationMin: 20,
    perWeek: '每周 2–3 次',
    countLabel: '4 个动作',
    source: 'Nerae 训练内容 · 免费',
    summary: '不追求个数，以抗伸展和抗旋转为主，适合放在力量训练后或单独安排。',
    fit: ['适合：想建立稳定的躯干控制', '需要：瑜伽垫，约 2 平方米空间', '可与上肢或下肢训练同日进行'],
    contents: [
      { name: '死虫式', detail: '2 组 × 40 秒 · 组间 45 秒' },
      { name: '鸟狗式', detail: '2 组 × 10 次/侧 · 组间 45 秒' },
      { name: '侧桥', detail: '2 组 × 30 秒/侧 · 组间 45 秒' },
      { name: '平板支撑', detail: '2 组 × 45 秒 · 组间 60 秒' },
    ],
  },
  {
    id: 'plan-mobility',
    type: 'training',
    title: '训练后舒展 · 髋与肩',
    image: '/assets/explore-stretch.webp',
    imageAlt: '训练者在健身房地面完成腿后侧伸展',
    goal: 'mobility',
    equipment: 'none',
    durationMin: 15,
    perWeek: '每次训练后',
    countLabel: '6 个动作',
    source: 'Nerae 训练内容 · 免费',
    summary: '以缓慢、可控的幅度放松髋部与肩部，不追求疼痛感，也不替代康复治疗。',
    fit: ['适合：训练后身体发热时进行', '需要：瑜伽垫，可选弹力带', '出现刺痛或关节不适时停止'],
    contents: [
      { name: '猫牛式', detail: '8 次呼吸 · 缓慢往返' },
      { name: '90/90 髋部转换', detail: '6 次/侧 · 保持骨盆稳定' },
      { name: '跪姿肩屈', detail: '8 次/侧 · 不塌腰' },
      { name: '腿后侧伸展', detail: '30 秒/侧 · 保持自然呼吸' },
      { name: '髋屈肌伸展', detail: '30 秒/侧 · 骨盆中立' },
      { name: '胸椎旋转', detail: '8 次/侧 · 幅度由小到大' },
    ],
  },
  {
    id: 'plan-dumbbell',
    type: 'training',
    title: '哑铃上肢 · 推与拉',
    image: '/assets/explore-dumbbell.webp',
    imageAlt: '训练者在哑铃架前完成哑铃弯举',
    goal: 'muscle',
    equipment: 'dumbbell',
    durationMin: 28,
    perWeek: '每周 2 次',
    countLabel: '4 个动作',
    source: 'Nerae 训练内容 · 免费',
    summary: '只用一对哑铃完成上肢推拉，动作数量少，适合时间有限但想保持训练节奏的人。',
    fit: ['适合：已掌握哑铃卧推与划船', '需要：一对可调哑铃、平凳', '重量以最后两次仍能保持动作为准'],
    contents: [
      { name: '哑铃卧推', detail: '4 组 × 10 次 · 组间 90 秒' },
      { name: '单臂哑铃划船', detail: '3 组 × 12 次/侧 · 组间 60 秒' },
      { name: '哑铃推举', detail: '3 组 × 10 次 · 组间 75 秒' },
      { name: '哑铃弯举', detail: '2 组 × 12 次 · 组间 60 秒' },
    ],
  },
  {
    id: 'plan-machine',
    type: 'training',
    title: '器械全身 · 入门',
    image: '/assets/gym-interior.webp',
    imageAlt: '器械齐全的健身房训练区域',
    goal: 'strength',
    equipment: 'machine',
    durationMin: 35,
    perWeek: '每周 3 次',
    countLabel: '6 个动作',
    source: 'Nerae 训练内容 · 免费',
    summary: '固定器械路径清楚，适合刚开始使用健身房的人；每个动作先确认座位与配重位置。',
    fit: ['适合：第一次使用固定器械', '需要：腿举、胸推、划船与下拉器械', '不确定器械调节时先询问现场工作人员'],
    contents: [
      { name: '腿举', detail: '3 组 × 12 次 · 组间 90 秒' },
      { name: '坐姿划船', detail: '3 组 × 12 次 · 组间 75 秒' },
      { name: '高位下拉', detail: '3 组 × 12 次 · 组间 75 秒' },
      { name: '坐姿胸推', detail: '3 组 × 10 次 · 组间 75 秒' },
      { name: '腿弯举', detail: '2 组 × 12 次 · 组间 60 秒' },
      { name: '腹肌板', detail: '2 组 × 15 次 · 组间 45 秒' },
    ],
  },
  {
    id: 'diet-training-day',
    type: 'diet',
    title: '训练日三餐 · 分配原则',
    image: '/assets/diet-featured.webp',
    imageAlt: '桌面上的蔬菜沙拉与水果',
    goal: 'training',
    equipment: 'none',
    durationMin: 8,
    perWeek: '训练日参考',
    countLabel: '3 条原则',
    source: 'Nerae 饮食内容 · 免费',
    summary: '把主食、蛋白质和蔬菜分配到三餐，不为某一餐增加额外负担。内容不计算个人热量。',
    fit: ['适合：训练日正常吃三餐的人', '不替代个人饮食处方或疾病管理', '按自己的食量调整份量'],
    contents: [
      { name: '训练前 1–2 小时', detail: '主食搭配少量蛋白质，避免空腹做高强度训练' },
      { name: '训练后一餐', detail: '蛋白质 + 主食，蔬菜占餐盘约一半' },
      { name: '全天饮水', detail: '分散补水，不把含糖饮料当作主要水分来源' },
    ],
  },
  {
    id: 'diet-breakfast',
    type: 'diet',
    title: '训练日早餐 · 简单搭配',
    image: '/assets/diet-breakfast.webp',
    imageAlt: '一份燕麦水果早餐与吐司',
    goal: 'training',
    equipment: 'none',
    durationMin: 10,
    perWeek: '训练日早晨',
    countLabel: '3 条搭配',
    source: 'Nerae 饮食内容 · 免费',
    summary: '时间紧时优先保证主食和蛋白质，不要求复杂备餐。',
    fit: ['适合：早上训练或上午训练的人', '按可获得的食材替换，不追求固定菜单', '乳糖不耐受时换用酸奶或豆制品'],
    contents: [
      { name: '主食', detail: '燕麦、全麦面包或米饭任选其一' },
      { name: '蛋白质', detail: '鸡蛋、牛奶、酸奶或豆制品' },
      { name: '水果', detail: '按当季水果搭配一拳大小，不必榨汁' },
    ],
  },
  {
    id: 'diet-mealprep',
    type: 'diet',
    title: '一次准备两餐 · 备餐原则',
    image: '/assets/diet-mealprep.webp',
    imageAlt: '备餐桌上的沙拉、水果与备餐计划',
    goal: 'rest',
    equipment: 'none',
    durationMin: 20,
    perWeek: '休息日准备',
    countLabel: '3 条原则',
    source: 'Nerae 饮食内容 · 免费',
    summary: '利用休息日准备接下来两餐的基础食材，减少训练日临时决定。',
    fit: ['适合：工作日时间紧张的人', '优先考虑保存条件与再加热方式', '不把备餐当成必须完成的计划'],
    contents: [
      { name: '先定蛋白质', detail: '一次准备两餐的蛋白质主菜，分装冷藏' },
      { name: '主食分开保存', detail: '米饭或薯类单独存放，吃前再加热' },
      { name: '蔬菜现做', detail: '叶菜类尽量当天处理，避免久放影响口感' },
    ],
  },
]

export function matchesTrainingFilters(item: ExploreItem, filters: ExploreFilters): boolean {
  if (item.type !== 'training') return false
  if (filters.goal !== 'all' && item.goal !== filters.goal) return false
  if (filters.equipment !== 'all' && item.equipment !== filters.equipment) return false
  if (filters.duration === 'short' && item.durationMin > 20) return false
  if (filters.duration === 'medium' && item.durationMin > 30) return false
  if (filters.duration === 'long' && item.durationMin > 45) return false
  return true
}

export function matchesDietFilters(item: ExploreItem, filters: ExploreFilters): boolean {
  if (item.type !== 'diet') return false
  if (filters.goal !== 'all' && item.goal !== filters.goal) return false
  return true
}

export function filterExploreItems(
  items: ExploreItem[],
  mode: 'training' | 'diet',
  filters: ExploreFilters,
  query: string,
): ExploreItem[] {
  const normalizedQuery = query.trim().toLowerCase()
  return items.filter((item) => {
    if (mode === 'training' ? !matchesTrainingFilters(item, filters) : !matchesDietFilters(item, filters)) {
      return false
    }
    if (!normalizedQuery) return true
    const haystack = [
      item.title,
      item.summary,
      ...item.fit,
      ...item.contents.map((line) => `${line.name} ${line.detail}`),
    ]
      .join(' ')
      .toLowerCase()
    return haystack.includes(normalizedQuery)
  })
}
