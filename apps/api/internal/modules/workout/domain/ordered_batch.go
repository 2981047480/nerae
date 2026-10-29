package domain

import "errors"

// ErrInvalidOrderedSnapshot marks persisted state that cannot be safely ordered.
var ErrInvalidOrderedSnapshot = errors.New("invalid persisted workout ordering snapshot")

// ApplyOrderedBatch validates and applies a batch together with the complete
// desired exercise-entry order. Ordering is supplied by the caller's persisted
// snapshot; this function never infers it from maps or timestamps.
func ApplyOrderedBatch(current Session, currentOrder []string, batch Batch, targetOrder []string) (Session, []string, error) {
	if !validSessionStatus(current.Status) || currentOrder == nil {
		return Session{}, nil, ErrInvalidOrderedSnapshot
	}
	if targetOrder == nil {
		return Session{}, nil, ErrInvalid
	}
	if err := validateOrderedSnapshot(current, currentOrder); err != nil {
		return Session{}, nil, ErrInvalidOrderedSnapshot
	}

	result := cloneSession(current)
	var err error
	if len(batch.Sets) != 0 || len(batch.DeleteKeys) != 0 || len(batch.RestoreKeys) != 0 {
		result, err = ApplyBatch(current, batch)
		if err != nil {
			return Session{}, nil, err
		}
	}
	if err := validateOrderedResult(result, targetOrder); err != nil {
		return Session{}, nil, err
	}
	return result, append([]string{}, targetOrder...), nil
}

func validSessionStatus(status Status) bool {
	return status == InProgress || status == Completed || status == Skipped
}

func validateOrderedSnapshot(session Session, order []string) error {
	if session.Sets == nil || session.Keys == nil || session.Entries == nil || (session.Status == Completed && len(session.Sets) == 0) {
		return ErrInvalidOrderedSnapshot
	}
	entryKeys := make(map[string]struct{})
	indexes := make(map[string]map[int]struct{})
	for mapKey, set := range session.Sets {
		if mapKey != set.RecordKey || ValidateSet(set) != nil {
			return ErrInvalidOrderedSnapshot
		}
		key, ok := session.Keys[mapKey]
		if !ok || key.Deleted || key.ExerciseEntryKey != set.ExerciseEntryKey {
			return ErrInvalidOrderedSnapshot
		}
		if identity, ok := session.Entries[set.ExerciseEntryKey]; !ok || identity != set.Exercise {
			return ErrInvalidOrderedSnapshot
		}
		entryKeys[set.ExerciseEntryKey] = struct{}{}
		if indexes[set.ExerciseEntryKey] == nil {
			indexes[set.ExerciseEntryKey] = make(map[int]struct{})
		}
		if _, exists := indexes[set.ExerciseEntryKey][set.Index]; exists {
			return ErrInvalidOrderedSnapshot
		}
		indexes[set.ExerciseEntryKey][set.Index] = struct{}{}
	}
	for mapKey, key := range session.Keys {
		if !validKey(mapKey) || !validKey(key.ExerciseEntryKey) {
			return ErrInvalidOrderedSnapshot
		}
		if key.Deleted {
			if _, active := session.Sets[mapKey]; active {
				return ErrInvalidOrderedSnapshot
			}
			if _, entryExists := session.Entries[key.ExerciseEntryKey]; !entryExists {
				return ErrInvalidOrderedSnapshot
			}
			continue
		}
		set, active := session.Sets[mapKey]
		if !active || set.ExerciseEntryKey != key.ExerciseEntryKey {
			return ErrInvalidOrderedSnapshot
		}
	}
	if session.Status == Skipped && len(session.Sets) != 0 {
		return ErrInvalidOrderedSnapshot
	}
	if len(entryKeys) > 2000 {
		return ErrInvalidOrderedSnapshot
	}
	if len(order) != len(entryKeys) {
		return ErrInvalidOrderedSnapshot
	}
	seen := make(map[string]struct{}, len(order))
	for _, entryKey := range order {
		if !validKey(entryKey) {
			return ErrInvalidOrderedSnapshot
		}
		if _, duplicate := seen[entryKey]; duplicate {
			return ErrInvalidOrderedSnapshot
		}
		if _, exists := entryKeys[entryKey]; !exists {
			return ErrInvalidOrderedSnapshot
		}
		seen[entryKey] = struct{}{}
	}
	return nil
}

func validateOrderedResult(session Session, order []string) error {
	if err := validateOrderedSnapshot(session, order); err != nil {
		return ErrInvalid
	}
	return nil
}
