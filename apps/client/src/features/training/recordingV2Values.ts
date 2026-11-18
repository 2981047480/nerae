import type { DraftExercise, DraftSet } from './recording'
import { validV2DraftSet } from './recordingV2Measure'

/** Empty fields are allowed; values that are present must match the v2 contract. */
export function validRecordingV2Set(exercise: DraftExercise, set: DraftSet): boolean {
  return validV2DraftSet(exercise, set)
}
