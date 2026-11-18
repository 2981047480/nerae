package domain

import (
	"log/slog"
	"reflect"
	"sort"
	"unicode/utf8"
)

// RecordingV2 is an internal candidate for one person's recording. It does not
// authenticate callers, persist data or authorize exposing a draft to a coach.
type RecordingV2 struct {
	Status  Status
	Mode    RecordingModeV2
	Entries map[string]Exercise
	Order   []string
	Sets    map[string]SetV2
	Removed map[string]RemovedSetV2
	// RemovedEntries stores the visible position at removal. Order keeps all
	// entries, including removed ones, so restoration preserves their position.
	RemovedEntries map[string]int
}

type RecordingModeV2 string

const (
	RecordingAfterWorkoutV2 RecordingModeV2 = "after_workout"
	RecordingPerSetV2       RecordingModeV2 = "per_set"
)

type EntryV2 struct {
	Key      string
	Exercise Exercise
}

type RemovedSetV2 struct {
	Set             SetV2
	RemovedPosition int
}

type RecordingBatchV2 struct {
	// Date is session metadata, validated against its frozen timezone by the repository.
	Date       *string
	Mode       *RecordingModeV2
	AddEntries []EntryV2
	Upserts    []SetV2
	RemoveKeys []string
	// RemovalPositions preserves the visible ordinal at each offline removal.
	RemovalPositions map[string]int
	RestoreKeys      []string
	RemoveEntryKeys  []string
	RestoreEntryKeys []string
	// Order includes every action entry, including entries with zero active sets.
	Order []string
}

func (RecordingV2) String() string               { return "RecordingV2{redacted}" }
func (RecordingV2) GoString() string             { return "RecordingV2{redacted}" }
func (RecordingV2) MarshalJSON() ([]byte, error) { return nil, ErrInvalid }
func (RecordingV2) LogValue() slog.Value         { return slog.StringValue("RecordingV2{redacted}") }

func (EntryV2) String() string               { return "EntryV2{redacted}" }
func (EntryV2) GoString() string             { return "EntryV2{redacted}" }
func (EntryV2) MarshalJSON() ([]byte, error) { return nil, ErrInvalid }
func (EntryV2) LogValue() slog.Value         { return slog.StringValue("EntryV2{redacted}") }

func (RemovedSetV2) String() string               { return "RemovedSetV2{redacted}" }
func (RemovedSetV2) GoString() string             { return "RemovedSetV2{redacted}" }
func (RemovedSetV2) MarshalJSON() ([]byte, error) { return nil, ErrInvalid }
func (RemovedSetV2) LogValue() slog.Value         { return slog.StringValue("RemovedSetV2{redacted}") }

func (RecordingBatchV2) String() string               { return "RecordingBatchV2{redacted}" }
func (RecordingBatchV2) GoString() string             { return "RecordingBatchV2{redacted}" }
func (RecordingBatchV2) MarshalJSON() ([]byte, error) { return nil, ErrInvalid }
func (RecordingBatchV2) LogValue() slog.Value         { return slog.StringValue("RecordingBatchV2{redacted}") }

func cloneOptionalString(value *string) *string {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}
func cloneOptionalInt(value *int) *int {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}
func cloneSideV2(side *SideMeasurementV2) *SideMeasurementV2 {
	if side == nil {
		return nil
	}
	copy := *side
	copy.WeightKg = cloneOptionalString(side.WeightKg)
	copy.Reps = cloneOptionalInt(side.Reps)
	return &copy
}
func cloneSetV2(set SetV2) SetV2 {
	copy := set
	copy.RPE = cloneOptionalString(set.RPE)
	copy.Measurement.WeightKg = cloneOptionalString(set.Measurement.WeightKg)
	copy.Measurement.Reps = cloneOptionalInt(set.Measurement.Reps)
	copy.Measurement.BodyweightKg = cloneOptionalString(set.Measurement.BodyweightKg)
	copy.Measurement.AdjustmentKg = cloneOptionalString(set.Measurement.AdjustmentKg)
	copy.Measurement.Left = cloneSideV2(set.Measurement.Left)
	copy.Measurement.Right = cloneSideV2(set.Measurement.Right)
	return copy
}
func cloneRecordingV2(value RecordingV2) RecordingV2 {
	copy := RecordingV2{Status: value.Status, Mode: value.Mode, Entries: make(map[string]Exercise, len(value.Entries)),
		Order: append([]string{}, value.Order...), Sets: make(map[string]SetV2, len(value.Sets)),
		Removed: make(map[string]RemovedSetV2, len(value.Removed)), RemovedEntries: make(map[string]int, len(value.RemovedEntries))}
	for key, entry := range value.Entries {
		copy.Entries[key] = entry
	}
	for key, set := range value.Sets {
		copy.Sets[key] = cloneSetV2(set)
	}
	for key, removed := range value.Removed {
		removed.Set = cloneSetV2(removed.Set)
		copy.Removed[key] = removed
	}
	for key, position := range value.RemovedEntries {
		copy.RemovedEntries[key] = position
	}
	return copy
}

func activeSetCountV2(value RecordingV2) int {
	count := 0
	for _, set := range value.Sets {
		if _, removed := value.RemovedEntries[set.ExerciseEntryKey]; !removed {
			count++
		}
	}
	return count
}

// Personal identity snapshots must have been resolved for the authenticated
// owner by the repository. Validation alone does not establish ownership.
func validateExerciseV2(exercise Exercise) error {
	if exercise.Source != Personal {
		return validateExercise(exercise)
	}
	if exercise.Version == 0 || exercise.Version > 9007199254740991 {
		return ErrInvalid
	}
	value, err := NormalizePersonalExercise(PersonalExercise{ID: exercise.ID, Name: exercise.Name})
	if err != nil || value.Name != exercise.Name {
		return ErrInvalid
	}
	return nil
}

func validateRecordingV2(value RecordingV2) error {
	if (value.Status != InProgress && value.Status != Completed) || (value.Mode != RecordingAfterWorkoutV2 && value.Mode != RecordingPerSetV2) || value.Entries == nil || value.Order == nil ||
		value.Sets == nil || value.Removed == nil || value.RemovedEntries == nil || len(value.Entries) > 2000 ||
		(value.Status == Completed && activeSetCountV2(value) == 0) || len(value.Order) != len(value.Entries) {
		return ErrInvalid
	}
	seenEntries := make(map[string]bool, len(value.Order))
	for _, entry := range value.Order {
		exercise, found := value.Entries[entry]
		if !found || seenEntries[entry] || !validKey(entry) || validateExerciseV2(exercise) != nil || !utf8.ValidString(exercise.Name) {
			return ErrInvalid
		}
		seenEntries[entry] = true
	}
	for key, position := range value.RemovedEntries {
		if _, exists := value.Entries[key]; !exists || position < 1 {
			return ErrInvalid
		}
	}
	indexes := make(map[string]map[int]bool)
	check := func(key string, set SetV2) bool {
		if key != set.RecordKey || ValidateSetV2(set) != nil || value.Entries[set.ExerciseEntryKey] != set.Exercise {
			return false
		}
		if indexes[set.ExerciseEntryKey] == nil {
			indexes[set.ExerciseEntryKey] = make(map[int]bool)
		}
		if indexes[set.ExerciseEntryKey][set.Index] {
			return false
		}
		indexes[set.ExerciseEntryKey][set.Index] = true
		return true
	}
	for key, set := range value.Sets {
		if _, deleted := value.Removed[key]; deleted || !check(key, set) {
			return ErrInvalid
		}
	}
	for key, removed := range value.Removed {
		if removed.RemovedPosition < 1 || !check(key, removed.Set) {
			return ErrInvalid
		}
	}
	return nil
}

// ValidateRecordingV2 checks internal invariants without authorizing access.
func ValidateRecordingV2(value RecordingV2) error { return validateRecordingV2(value) }

// ApplyRecordingBatchV2 is atomic at the value level. A repository must also
// perform ownership, session-version, idempotency and persistence checks inside
// the same database transaction before exposing this behavior.
func ApplyRecordingBatchV2(current RecordingV2, batch RecordingBatchV2) (RecordingV2, bool, error) {
	if validateRecordingV2(current) != nil || batch.Order == nil || len(batch.AddEntries) > 2000 ||
		len(batch.Upserts)+len(batch.RemoveKeys)+len(batch.RestoreKeys)+len(batch.RemoveEntryKeys)+len(batch.RestoreEntryKeys) > MaxBatchItems {
		return RecordingV2{}, false, ErrInvalid
	}
	original := cloneRecordingV2(current)
	next := cloneRecordingV2(current)
	if batch.Mode != nil {
		if *batch.Mode != RecordingAfterWorkoutV2 && *batch.Mode != RecordingPerSetV2 {
			return RecordingV2{}, false, ErrInvalid
		}
		next.Mode = *batch.Mode
	}
	seenEntries := make(map[string]bool)
	for _, entry := range batch.AddEntries {
		if !validKey(entry.Key) || validateExerciseV2(entry.Exercise) != nil || seenEntries[entry.Key] {
			return RecordingV2{}, false, ErrInvalid
		}
		seenEntries[entry.Key] = true
		if previous, exists := next.Entries[entry.Key]; exists && previous != entry.Exercise {
			return RecordingV2{}, false, ErrConflict
		}
		next.Entries[entry.Key] = entry.Exercise
	}
	seenEntryChanges := make(map[string]bool)
	for _, key := range batch.RestoreEntryKeys {
		if !validKey(key) || seenEntryChanges[key] {
			return RecordingV2{}, false, ErrInvalid
		}
		seenEntryChanges[key] = true
		if _, exists := next.RemovedEntries[key]; !exists {
			return RecordingV2{}, false, ErrConflict
		}
		delete(next.RemovedEntries, key)
	}
	// Restore first, then apply the latest edit, then remove. This allows one
	// offline batch to carry edits made immediately before removal/restoration.
	seenStates := make(map[string]bool)
	for _, key := range batch.RestoreKeys {
		if !validKey(key) || seenStates[key] {
			return RecordingV2{}, false, ErrInvalid
		}
		seenStates[key] = true
		removed, exists := next.Removed[key]
		if !exists {
			return RecordingV2{}, false, ErrConflict
		}
		if _, entryRemoved := next.RemovedEntries[removed.Set.ExerciseEntryKey]; entryRemoved {
			return RecordingV2{}, false, ErrConflict
		}
		next.Sets[key] = cloneSetV2(removed.Set)
		delete(next.Removed, key)
	}
	seenUpserts := make(map[string]bool)
	for _, set := range batch.Upserts {
		if ValidateSetV2(set) != nil || seenUpserts[set.RecordKey] {
			return RecordingV2{}, false, ErrInvalid
		}
		seenUpserts[set.RecordKey] = true
		entry, exists := next.Entries[set.ExerciseEntryKey]
		_, entryRemoved := next.RemovedEntries[set.ExerciseEntryKey]
		if !exists || entryRemoved || entry != set.Exercise {
			return RecordingV2{}, false, ErrInvalid
		}
		if _, removed := next.Removed[set.RecordKey]; removed {
			return RecordingV2{}, false, ErrConflict
		}
		if previous, exists := next.Sets[set.RecordKey]; exists && previous.ExerciseEntryKey != set.ExerciseEntryKey {
			return RecordingV2{}, false, ErrConflict
		}
		next.Sets[set.RecordKey] = cloneSetV2(set)
	}
	positions := make(map[string]int, len(next.Sets))
	byEntry := make(map[string][]SetV2)
	for _, set := range next.Sets {
		byEntry[set.ExerciseEntryKey] = append(byEntry[set.ExerciseEntryKey], set)
	}
	for _, sets := range byEntry {
		sort.Slice(sets, func(i, j int) bool { return sets[i].Index < sets[j].Index })
		for i, set := range sets {
			positions[set.RecordKey] = i + 1
		}
	}
	removalSet := make(map[string]bool, len(batch.RemoveKeys))
	for _, key := range batch.RemoveKeys {
		removalSet[key] = true
	}
	for key, position := range batch.RemovalPositions {
		if !removalSet[key] || position < 1 {
			return RecordingV2{}, false, ErrInvalid
		}
	}
	seenRemovals := make(map[string]bool)
	for _, key := range batch.RemoveKeys {
		// Restoring then removing is meaningful only with an explicit intervening
		// update; otherwise it is an ambiguous no-op.
		if !validKey(key) || seenRemovals[key] || (seenStates[key] && !seenUpserts[key]) {
			return RecordingV2{}, false, ErrInvalid
		}
		seenRemovals[key] = true
		if set, exists := next.Sets[key]; exists {
			if _, entryRemoved := next.RemovedEntries[set.ExerciseEntryKey]; entryRemoved {
				return RecordingV2{}, false, ErrConflict
			}
			position := positions[key]
			if supplied, ok := batch.RemovalPositions[key]; ok {
				position = supplied
			}
			next.Removed[key] = RemovedSetV2{Set: cloneSetV2(set), RemovedPosition: position}
			delete(next.Sets, key)
		}
	}
	next.Order = append([]string{}, batch.Order...)
	for _, key := range batch.RemoveEntryKeys {
		if !validKey(key) || seenEntryChanges[key] {
			return RecordingV2{}, false, ErrInvalid
		}
		seenEntryChanges[key] = true
		if _, exists := next.Entries[key]; !exists {
			return RecordingV2{}, false, ErrConflict
		}
		if _, alreadyRemoved := next.RemovedEntries[key]; alreadyRemoved {
			continue
		}
		position := 0
		found := false
		for _, orderedKey := range next.Order {
			if _, removed := next.RemovedEntries[orderedKey]; !removed {
				position++
			}
			if orderedKey == key {
				found = true
				break
			}
		}
		if !found {
			return RecordingV2{}, false, ErrInvalid
		}
		next.RemovedEntries[key] = position
	}
	if next.Status == Completed && activeSetCountV2(next) == 0 {
		next.Status = InProgress
	}
	if validateRecordingV2(next) != nil {
		return RecordingV2{}, false, ErrInvalid
	}
	return next, !reflect.DeepEqual(original, next), nil
}

// CompleteRecordingV2 includes every remaining group, including missing values;
// per-group marking is not a prerequisite. No training time is inferred here.
func CompleteRecordingV2(current RecordingV2) (RecordingV2, error) {
	if validateRecordingV2(current) != nil {
		return RecordingV2{}, ErrInvalid
	}
	if activeSetCountV2(current) == 0 {
		return RecordingV2{}, ErrConflict
	}
	result := cloneRecordingV2(current)
	result.Status = Completed
	return result, nil
}
