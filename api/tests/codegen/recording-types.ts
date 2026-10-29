// Compile against openapi-typescript output copied alongside this file.
// JSON Schema and service-side validation still enforce conditional rules.
import type { components } from './generated-types';

type Write = components['schemas']['SetRecordWrite'];
type Read = components['schemas']['WorkoutSetRecord'];
type Batch = components['schemas']['SetBatchUpdate'];
type Detail = components['schemas']['WorkoutSessionDetail'];
type Finish = components['schemas']['WorkoutFinish'];

const weight: Write = {
  exerciseId: '101', exerciseEntryKey: 'exercise-001', setIndex: 1,
  recordKey: 'set-0001', kind: 'weight', weightKg: 12.5, reps: 8,
};
const seconds: Write = {
  exerciseName: '自定义动作', exerciseEntryKey: 'exercise-002', setIndex: 1,
  recordKey: 'set-0002', kind: 'durationPerSide', seconds: 40,
};
const read: Read = { ...weight, resolvedExerciseName: '卧推' };
const batch: Batch = { expectedVersion: 1, exerciseEntryKeys: ['exercise-001', 'exercise-002'], sets: [weight, seconds] };
const deletion: Batch = { expectedVersion: 2, exerciseEntryKeys: ['exercise-001'], deleteRecordKeys: ['set-0002'] };
const detail: Detail = {
  id: '101', date: '2026-10-20', timeZone: 'Asia/Shanghai',
  status: 'completed', source: 'free', version: 1,
  createdAt: '2026-10-20T10:00:00Z', updatedAt: '2026-10-20T10:01:00Z',
  recordedAt: '2026-10-20T10:01:00Z', exerciseEntryKeys: ['exercise-001'], sets: [read],
};
const names: string[] = detail.sets.map((set) => set.resolvedExerciseName);
const version: number = batch.expectedVersion;
const unchanged: Finish = { expectedVersion: 1 };
const cleared: Finish = { expectedVersion: 1, durationMin: null };

// @ts-expect-error A stable exercise entry key is required.
const missingEntry: Write = { exerciseId: '101', setIndex: 1, recordKey: 'set-0001', kind: 'weight', weightKg: 10, reps: 8 };
// @ts-expect-error A batch version is a number, not a string.
const wrongVersion: Batch = { expectedVersion: '1', exerciseEntryKeys: ['exercise-001'], sets: [weight] };
// @ts-expect-error All batches must carry the complete projected order.
const missingOrder: Batch = { expectedVersion: 1, sets: [weight] };
// @ts-expect-error Detail must contain the current actual sets.
const missingSets: Detail = { id: '101', date: '2026-10-20', timeZone: 'Asia/Shanghai', status: 'completed', source: 'free', version: 1, createdAt: '2026-10-20T10:00:00Z', updatedAt: '2026-10-20T10:01:00Z', recordedAt: '2026-10-20T10:01:00Z', exerciseEntryKeys: ['exercise-001'] };

void [deletion, names, version, unchanged, cleared, missingEntry, wrongVersion, missingOrder, missingSets];
