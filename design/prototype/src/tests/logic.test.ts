import { describe, expect, it } from 'vitest'
import {
  EXPLORE_ITEMS,
  createSeedCoachState,
  createSeedDraft,
  createSeedSession,
  filterExploreItems,
  SEED_PUBLISHED_PLAN,
} from '../data/sample'
import {
  appliedValues,
  doneSetCount,
  firstPendingEntry,
  referenceText,
  nextEventTime,
  suggestedInput,
  totalSetCount,
  validateSetInput,
} from '../utils/training'
import {
  diffPlans,
  planTotals,
  sanitizePlan,
  validatePlan,
} from '../utils/plan'
import { formatClock, formatTime, formatWeight, setValueLabel } from '../utils/format'

describe('今天与训练状态', () => {
  const session = createSeedSession()

  it('四个动作的组数为 4 / 3 / 3 / 2，共 12 组', () => {
    expect(session.exercises.map((exercise) => exercise.sets.length)).toEqual([4, 3, 3, 2])
    expect(totalSetCount(session)).toBe(12)
  })

  it('初始已记录 3 组，当前是哑铃卧推第 4 组', () => {
    expect(doneSetCount(session)).toBe(3)
    const entry = firstPendingEntry(session)
    expect(entry?.exercise.id).toBe('a1')
    expect(entry?.set.index).toBe(4)
  })

  it('当前组从最近一组预填重量与次数，并保留上次参考日期', () => {
    const entry = firstPendingEntry(session)
    expect(entry).not.toBeNull()
    const input = suggestedInput(entry!.exercise)
    expect(input.weightKg).toBe('12.5')
    expect(input.reps).toBe('9')
    expect(referenceText(entry!.exercise)).toContain('10月22日')
    expect(referenceText(entry!.exercise)).toContain('12.5 kg × 10 次')
  })

  it('自重/时长动作使用秒数，不混用重量', () => {
    const plank = session.exercises.find((exercise) => exercise.id === 'a4')!
    expect(suggestedInput(plank)).toEqual({ weightKg: '', reps: '', seconds: '45' })
    expect(
      validateSetInput('duration', { weightKg: '', reps: '', seconds: '45' }),
    ).toEqual({})
    expect(
      validateSetInput('duration', { weightKg: '', reps: '', seconds: '0' }),
    ).toHaveProperty('seconds')
  })

  it('接受合法的一位/两位小数，拒绝负数、非数与三位小数', () => {
    expect(validateSetInput('weight', { weightKg: '1.1', reps: '10', seconds: '' })).toEqual({})
    expect(validateSetInput('weight', { weightKg: '2.3', reps: '10', seconds: '' })).toEqual({})
    expect(validateSetInput('weight', { weightKg: '8.2', reps: '10', seconds: '' })).toEqual({})
    expect(validateSetInput('weight', { weightKg: '8.25', reps: '10', seconds: '' })).toEqual({})
    expect(
      validateSetInput('weight', { weightKg: '12.345', reps: '10', seconds: '' }),
    ).toHaveProperty('weightKg')
    expect(validateSetInput('weight', { weightKg: '-1.5', reps: '10', seconds: '' })).toHaveProperty(
      'weightKg',
    )
    expect(validateSetInput('weight', { weightKg: 'abc', reps: '10', seconds: '' })).toHaveProperty(
      'weightKg',
    )
  })

  it('新增记录时间在样例会话时间线之后，顺序可按时间读取', () => {
    const session = createSeedSession()
    const event = nextEventTime(session, 90)
    expect(Date.parse(event)).toBeGreaterThan(Date.parse('2026-10-24T19:12:00+08:00'))
    expect(formatTime(event)).toBe('19:14')
  })

  it('拒绝空、零、负数、小数次数与超界值，错误靠近字段', () => {
    expect(validateSetInput('weight', { weightKg: '', reps: '10', seconds: '' })).toHaveProperty(
      'weightKg',
    )
    expect(validateSetInput('weight', { weightKg: '-5', reps: '10', seconds: '' })).toHaveProperty(
      'weightKg',
    )
    expect(validateSetInput('weight', { weightKg: '12.5', reps: '9.5', seconds: '' })).toHaveProperty(
      'reps',
    )
    expect(validateSetInput('weight', { weightKg: '12.5', reps: '0', seconds: '' })).toHaveProperty(
      'reps',
    )
    expect(validateSetInput('weight', { weightKg: '12.5', reps: '101', seconds: '' })).toHaveProperty(
      'reps',
    )
  })

  it('有效输入转换为组记录值', () => {
    expect(appliedValues('weight', { weightKg: '12.5', reps: '10', seconds: '' })).toEqual({
      weightKg: 12.5,
      reps: 10,
      seconds: null,
    })
    expect(appliedValues('perSide', { weightKg: '10', reps: '12', seconds: '' })).toEqual({
      weightKg: 10,
      reps: 12,
      seconds: null,
    })
    expect(appliedValues('duration', { weightKg: '', reps: '', seconds: '50' })).toEqual({
      weightKg: null,
      reps: null,
      seconds: 50,
    })
  })

  it('组记录文案保留单位与每侧信息', () => {
    const perSide = session.exercises.find((exercise) => exercise.id === 'a3')!
    const done = { ...perSide.sets[0]!, status: 'done' as const, weightKg: 10, reps: 12 }
    expect(setValueLabel('perSide', done)).toBe('10 kg × 12 次/侧')
    expect(formatClock(75_000)).toBe('01:15')
    expect(formatWeight(12.5)).toBe('12.5')
  })
})

describe('教练计划差异与校验', () => {
  const draft = createSeedDraft()
  const coach = createSeedCoachState()

  it('草稿对照现用版本产生可读差异，且完整方案仍存在', () => {
    const changes = diffPlans(SEED_PUBLISHED_PLAN, sanitizePlan(draft))
    expect(changes).toHaveLength(4)
    const labels = changes.map((change) => change.kind === 'field' ? change.label : change.kind)
    expect(labels).toContain('组数')
    expect(labels).toContain('目标次数/时长')
    expect(labels).toContain('备注')
    expect(planTotals(draft).exercises).toBe(8)
    expect(planTotals(draft).sets).toBe(25)
    expect(draft.days[0]!.exercises).toHaveLength(4)
    expect(draft.days[1]!.exercises).toHaveLength(4)
  })

  it('种子草稿是完整可发布内容', () => {
    expect(validatePlan(draft)).toEqual([])
    expect(coach.draftVersion).toBe(7)
    expect(coach.publishedVersion).toBe(6)
    expect(coach.deliveredVersion).toBeNull()
  })

  it('缺项会按字段返回，并给出可定位的训练日与动作', () => {
    const broken = sanitizePlan(structuredClone(draft))
    broken.days[0]!.title = ''
    broken.days[0]!.exercises[0]!.name = ''
    broken.days[0]!.exercises[0]!.sets = '0'
    broken.days[0]!.exercises[1]!.restSeconds = ''
    const issues = validatePlan(broken)
    expect(issues.some((issue) => issue.field === 'dayTitle')).toBe(true)
    expect(
      issues.some(
        (issue) => issue.field === 'name' && issue.exerciseId === broken.days[0]!.exercises[0]!.id,
      ),
    ).toBe(true)
    expect(issues.some((issue) => issue.field === 'sets')).toBe(true)
    expect(issues.some((issue) => issue.field === 'restSeconds')).toBe(true)
  })

  it('没有动作的训练日阻断发布', () => {
    const empty = sanitizePlan(structuredClone(draft))
    empty.days[0]!.exercises = []
    const issues = validatePlan(empty)
    expect(issues.some((issue) => issue.field === 'instructions')).toBe(true)
  })
})

describe('发现筛选与搜索', () => {
  it('默认每个模式都有内容', () => {
    const emptyFilters = { goal: 'all', equipment: 'all', duration: 'all' }
    expect(filterExploreItems(EXPLORE_ITEMS, 'training', emptyFilters, '')).toHaveLength(5)
    expect(filterExploreItems(EXPLORE_ITEMS, 'diet', emptyFilters, '')).toHaveLength(3)
  })

  it('组合条件可以到达真实空状态，放宽后恢复', () => {
    const filters = { goal: 'mobility', equipment: 'machine', duration: 'all' }
    expect(filterExploreItems(EXPLORE_ITEMS, 'training', filters, '')).toHaveLength(0)
    const relaxed = { ...filters, equipment: 'all' }
    expect(filterExploreItems(EXPLORE_ITEMS, 'training', relaxed, '').length).toBeGreaterThan(0)
  })

  it('搜索覆盖动作与原则内容', () => {
    const filters = { goal: 'all', equipment: 'all', duration: 'all' }
    expect(
      filterExploreItems(EXPLORE_ITEMS, 'training', filters, '平板支撑').length,
    ).toBeGreaterThan(0)
    expect(
      filterExploreItems(EXPLORE_ITEMS, 'diet', filters, '燕麦').length,
    ).toBeGreaterThan(0)
  })
})
