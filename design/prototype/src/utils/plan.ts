import type {
  PlanChange,
  PlanContent,
  PlanDay,
  PlanValidationIssue,
} from '../types'

const FIELD_LABELS: Record<string, string> = {
  dayTitle: '训练日名称',
  name: '动作名称',
  sets: '组数',
  target: '目标次数/时长',
  restSeconds: '组间休息',
  note: '备注',
}

const FIELD_LIMITS: Record<string, number> = {
  name: 24,
  target: 20,
  note: 80,
  dayTitle: 24,
}

function parseSets(raw: string): number | null {
  const value = Number(raw.trim())
  return Number.isInteger(value) && value > 0 ? value : null
}

function parseRest(raw: string): number | null {
  const value = Number(raw.trim())
  return Number.isInteger(value) && value > 0 ? value : null
}

export function sanitizePlan(content: PlanContent): PlanContent {
  return {
    title: content.title.trim(),
    cycle: content.cycle.trim(),
    days: content.days.map((day) => ({
      id: day.id,
      title: day.title.trim(),
      exercises: day.exercises.map((exercise) => ({
        id: exercise.id,
        name: exercise.name.trim(),
        sets: exercise.sets.trim(),
        target: exercise.target.trim(),
        restSeconds: exercise.restSeconds.trim(),
        note: exercise.note.trim(),
      })),
    })),
  }
}

export function serializePlan(content: PlanContent): string {
  return JSON.stringify(sanitizePlan(content))
}

export function validatePlan(content: PlanContent): PlanValidationIssue[] {
  const issues: PlanValidationIssue[] = []
  for (const day of content.days) {
    if (!day.title.trim()) {
      issues.push({
        dayId: day.id,
        dayTitle: day.title || '未命名训练日',
        exerciseId: null,
        exerciseName: null,
        field: 'dayTitle',
        message: '填写训练日名称',
      })
    } else if (day.title.trim().length > FIELD_LIMITS.dayTitle) {
      issues.push({
        dayId: day.id,
        dayTitle: day.title,
        exerciseId: null,
        exerciseName: null,
        field: 'dayTitle',
        message: `训练日名称最多 ${FIELD_LIMITS.dayTitle} 个字`,
      })
    }

    if (day.exercises.length === 0) {
      issues.push({
        dayId: day.id,
        dayTitle: day.title || '未命名训练日',
        exerciseId: null,
        exerciseName: null,
        field: 'instructions',
        message: '至少保留一个动作',
      })
      continue
    }

    for (const exercise of day.exercises) {
      const name = exercise.name.trim()
      if (!name) {
        issues.push({
          dayId: day.id,
          dayTitle: day.title,
          exerciseId: exercise.id,
          exerciseName: null,
          field: 'name',
          message: '填写动作名称',
        })
      } else if (name.length > FIELD_LIMITS.name) {
        issues.push({
          dayId: day.id,
          dayTitle: day.title,
          exerciseId: exercise.id,
          exerciseName: name,
          field: 'name',
          message: `动作名称最多 ${FIELD_LIMITS.name} 个字`,
        })
      }

      const sets = parseSets(exercise.sets)
      if (sets === null || sets < 1 || sets > 10) {
        issues.push({
          dayId: day.id,
          dayTitle: day.title,
          exerciseId: exercise.id,
          exerciseName: name || '未命名动作',
          field: 'sets',
          message: '组数需要是 1–10 的整数',
        })
      }

      if (!exercise.target.trim()) {
        issues.push({
          dayId: day.id,
          dayTitle: day.title,
          exerciseId: exercise.id,
          exerciseName: name || '未命名动作',
          field: 'target',
          message: '填写次数或时长',
        })
      } else if (exercise.target.trim().length > FIELD_LIMITS.target) {
        issues.push({
          dayId: day.id,
          dayTitle: day.title,
          exerciseId: exercise.id,
          exerciseName: name || '未命名动作',
          field: 'target',
          message: `目标最多 ${FIELD_LIMITS.target} 个字`,
        })
      }

      const rest = parseRest(exercise.restSeconds)
      if (rest === null || rest < 20 || rest > 300) {
        issues.push({
          dayId: day.id,
          dayTitle: day.title,
          exerciseId: exercise.id,
          exerciseName: name || '未命名动作',
          field: 'restSeconds',
          message: '休息时长需要是 20–300 秒',
        })
      }

      if (exercise.note.trim().length > FIELD_LIMITS.note) {
        issues.push({
          dayId: day.id,
          dayTitle: day.title,
          exerciseId: exercise.id,
          exerciseName: name || '未命名动作',
          field: 'note',
          message: `备注最多 ${FIELD_LIMITS.note} 个字`,
        })
      }
    }
  }
  return issues
}

function fieldChange(
  day: PlanDay,
  exerciseId: string,
  exerciseName: string,
  field: string,
  before: string,
  after: string,
): PlanChange {
  return {
    kind: 'field',
    dayId: day.id,
    dayTitle: day.title,
    exerciseId,
    exerciseName,
    field: field as 'name' | 'sets' | 'target' | 'restSeconds' | 'note',
    label: FIELD_LABELS[field] ?? field,
    before,
    after,
  }
}

export function diffPlans(before: PlanContent, after: PlanContent): PlanChange[] {
  const changes: PlanChange[] = []
  const beforeDays = new Map(before.days.map((day) => [day.id, day]))
  const afterDays = new Map(after.days.map((day) => [day.id, day]))

  for (const beforeDay of before.days) {
    const afterDay = afterDays.get(beforeDay.id)
    if (!afterDay) continue
    if (beforeDay.title.trim() !== afterDay.title.trim()) {
      changes.push({
        kind: 'field',
        dayId: beforeDay.id,
        dayTitle: afterDay.title,
        exerciseId: '',
        exerciseName: '',
        field: 'dayTitle',
        label: FIELD_LABELS.dayTitle,
        before: beforeDay.title,
        after: afterDay.title,
      })
    }

    const afterExercises = new Map(afterDay.exercises.map((item) => [item.id, item]))
    for (const beforeExercise of beforeDay.exercises) {
      const afterExercise = afterExercises.get(beforeExercise.id)
      if (!afterExercise) {
        changes.push({
          kind: 'removed',
          dayId: afterDay.id,
          dayTitle: afterDay.title,
          exerciseId: beforeExercise.id,
          exerciseName: beforeExercise.name,
        })
        continue
      }
      const fields: Array<'name' | 'sets' | 'target' | 'restSeconds' | 'note'> = [
        'name',
        'sets',
        'target',
        'restSeconds',
        'note',
      ]
      for (const field of fields) {
        const beforeValue = beforeExercise[field].trim()
        const afterValue = afterExercise[field].trim()
        if (beforeValue !== afterValue) {
          changes.push(
            fieldChange(afterDay, afterExercise.id, afterExercise.name, field, beforeValue, afterValue),
          )
        }
      }
    }

    const beforeExercises = new Map(beforeDay.exercises.map((item) => [item.id, item]))
    for (const afterExercise of afterDay.exercises) {
      if (!beforeExercises.has(afterExercise.id)) {
        changes.push({
          kind: 'added',
          dayId: afterDay.id,
          dayTitle: afterDay.title,
          exerciseId: afterExercise.id,
          exerciseName: afterExercise.name || '未命名动作',
        })
      }
    }
  }

  for (const afterDay of after.days) {
    if (!beforeDays.has(afterDay.id)) {
      for (const exercise of afterDay.exercises) {
        changes.push({
          kind: 'added',
          dayId: afterDay.id,
          dayTitle: afterDay.title,
          exerciseId: exercise.id,
          exerciseName: exercise.name || '未命名动作',
        })
      }
    }
  }

  return changes
}

export function changesForExercise(
  changes: PlanChange[],
  dayId: string,
  exerciseId: string,
): PlanChange[] {
  return changes.filter(
    (change) =>
      change.dayId === dayId &&
      (change.exerciseId === exerciseId ||
        (change.kind === 'field' && change.field === 'dayTitle' && exerciseId === '')),
  )
}

export function planTotals(content: PlanContent): { exercises: number; sets: number } {
  let exercises = 0
  let sets = 0
  for (const day of content.days) {
    exercises += day.exercises.length
    for (const exercise of day.exercises) {
      sets += parseSets(exercise.sets) ?? 0
    }
  }
  return { exercises, sets }
}

export function dayTotals(day: PlanDay): { exercises: number; sets: number } {
  return {
    exercises: day.exercises.length,
    sets: day.exercises.reduce((sum, exercise) => sum + (parseSets(exercise.sets) ?? 0), 0),
  }
}

export function changeDisplayValue(change: PlanChange): { before: string; after: string } {
  if (change.kind !== 'field') {
    return { before: change.kind === 'removed' ? '原有动作' : '无', after: change.kind === 'added' ? '新增动作' : '已移除' }
  }
  if (change.field === 'sets') return { before: `${change.before} 组`, after: `${change.after} 组` }
  if (change.field === 'restSeconds') return { before: `${change.before} 秒`, after: `${change.after} 秒` }
  return { before: change.before || '空', after: change.after || '空' }
}

export function planChangeCountLabel(count: number): string {
  return `${count} 处改动`
}
