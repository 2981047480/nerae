import type { DraftExercise, DraftSet } from './recording'

/** Empty fields are allowed; values that are present must match the v2 contract. */
export function validRecordingV2Set(exercise: DraftExercise, set: DraftSet): boolean {
  if (exercise.kind !== 'weight' || typeof set.weightKg !== 'string' || typeof set.reps !== 'string' ||
      typeof set.seconds !== 'string' || typeof set.rpe !== 'string' || set.seconds.trim()) return false
  const weight = set.weightKg.trim(); const repetitions = set.reps.trim(); const rpe = set.rpe.trim()
  return (!weight || /^\d{1,6}(?:\.\d{1,2})?$/.test(weight)) &&
    (!repetitions || (/^\d+$/.test(repetitions) && Number(repetitions) >= 1 && Number(repetitions) <= 100)) &&
    (!rpe || /^(?:[1-9](?:\.\d)?|10(?:\.0)?)$/.test(rpe))
}
