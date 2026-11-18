package transport

import (
	"sort"
	"strconv"
	"time"

	"nerae/api/internal/modules/workout/domain"
	"nerae/api/internal/modules/workout/repository"
)

type recordingV2SideJSON struct {
	State    domain.SideStateV2 `json:"state"`
	WeightKg *string            `json:"weightKg,omitempty"`
	Reps     *int               `json:"reps,omitempty"`
}
type recordingV2MeasurementJSON struct {
	Method       domain.MeasurementMethodV2 `json:"method"`
	WeightKg     *string                    `json:"weightKg,omitempty"`
	Reps         *int                       `json:"reps,omitempty"`
	BodyweightKg *string                    `json:"bodyweightKg,omitempty"`
	AdjustmentKg *string                    `json:"adjustmentKg,omitempty"`
	Left         *recordingV2SideJSON       `json:"left,omitempty"`
	Right        *recordingV2SideJSON       `json:"right,omitempty"`
}
type recordingV2SetJSON struct {
	RecordKey        string                     `json:"recordKey"`
	ExerciseEntryKey string                     `json:"exerciseEntryKey"`
	SetIndex         int                        `json:"setIndex"`
	Confirmed        bool                       `json:"confirmed"`
	RPE              *string                    `json:"rpe,omitempty"`
	Measurement      recordingV2MeasurementJSON `json:"measurement"`
}
type recordingV2EntryInput struct {
	ExerciseEntryKey string `json:"exerciseEntryKey"`
	ExerciseName     string `json:"exerciseName"`
}
type recordingV2RemoveInput struct {
	RecordKey       string `json:"recordKey"`
	RemovedPosition int    `json:"removedPosition"`
}
type recordingV2BatchInput struct {
	ExpectedVersion   uint64                   `json:"expectedVersion"`
	Date              *string                  `json:"date,omitempty"`
	Mode              *domain.RecordingModeV2  `json:"mode,omitempty"`
	AddEntries        []recordingV2EntryInput  `json:"addEntries,omitempty"`
	Sets              []recordingV2SetJSON     `json:"sets,omitempty"`
	RemoveRecordKeys  []string                 `json:"removeRecordKeys,omitempty"`
	RemoveRecords     []recordingV2RemoveInput `json:"removeRecords,omitempty"`
	RestoreRecordKeys []string                 `json:"restoreRecordKeys,omitempty"`
	RemoveEntryKeys   []string                 `json:"removeEntryKeys,omitempty"`
	RestoreEntryKeys  []string                 `json:"restoreEntryKeys,omitempty"`
	ExerciseEntryKeys []string                 `json:"exerciseEntryKeys"`
}
type recordingV2VolumeJSON struct {
	CalculatedKg *string `json:"calculatedKg,omitempty"`
	FullSets     int     `json:"fullSets"`
	PartialSets  int     `json:"partialSets"`
	ExcludedSets int     `json:"excludedSets"`
}
type recordingV2ActionVolumeJSON struct {
	ExerciseEntryKeys []string              `json:"exerciseEntryKeys"`
	Volume            recordingV2VolumeJSON `json:"volume"`
}
type recordingV2EntryJSON struct {
	ExerciseEntryKey string                 `json:"exerciseEntryKey"`
	Source           string                 `json:"source"`
	ExerciseName     string                 `json:"exerciseName"`
	RemovedPosition  *int                   `json:"removedPosition,omitempty"`
	Volume           *recordingV2VolumeJSON `json:"volume,omitempty"`
}
type recordingV2RemovedJSON struct {
	Set             recordingV2SetJSON `json:"set"`
	RemovedPosition int                `json:"removedPosition"`
}
type recordingV2DetailJSON struct {
	ID            string                        `json:"id"`
	Version       uint64                        `json:"version"`
	Source        string                        `json:"source"`
	Date          string                        `json:"date"`
	TimeZone      string                        `json:"timeZone"`
	ClientEventID string                        `json:"clientEventId"`
	Status        domain.Status                 `json:"status"`
	Mode          domain.RecordingModeV2        `json:"mode"`
	CreatedAt     string                        `json:"createdAt"`
	UpdatedAt     string                        `json:"updatedAt"`
	RecordedAt    *string                       `json:"recordedAt,omitempty"`
	Entries       []recordingV2EntryJSON        `json:"entries"`
	Sets          []recordingV2SetJSON          `json:"sets"`
	RemovedSets   []recordingV2RemovedJSON      `json:"removedSets"`
	ActionVolumes []recordingV2ActionVolumeJSON `json:"actionVolumes,omitempty"`
}

func sideFromV2(value *recordingV2SideJSON) *domain.SideMeasurementV2 {
	if value == nil {
		return nil
	}
	return &domain.SideMeasurementV2{State: value.State, WeightKg: value.WeightKg, Reps: value.Reps}
}
func sideToV2(value *domain.SideMeasurementV2) *recordingV2SideJSON {
	if value == nil {
		return nil
	}
	return &recordingV2SideJSON{State: value.State, WeightKg: value.WeightKg, Reps: value.Reps}
}
func measurementFromV2(v recordingV2MeasurementJSON) domain.MeasurementV2 {
	return domain.MeasurementV2{Method: v.Method, WeightKg: v.WeightKg, Reps: v.Reps, BodyweightKg: v.BodyweightKg, AdjustmentKg: v.AdjustmentKg, Left: sideFromV2(v.Left), Right: sideFromV2(v.Right)}
}
func setToV2(v domain.SetV2) recordingV2SetJSON {
	m := v.Measurement
	return recordingV2SetJSON{RecordKey: v.RecordKey, ExerciseEntryKey: v.ExerciseEntryKey, SetIndex: v.Index, Confirmed: v.Confirmed, RPE: v.RPE,
		Measurement: recordingV2MeasurementJSON{Method: m.Method, WeightKg: m.WeightKg, Reps: m.Reps, BodyweightKg: m.BodyweightKg, AdjustmentKg: m.AdjustmentKg, Left: sideToV2(m.Left), Right: sideToV2(m.Right)}}
}

// Exercise identity is resolved from the locked server snapshot or from an
// explicitly added custom entry; it is never accepted on an individual set.
func buildRecordingV2Batch(current domain.RecordingV2, input recordingV2BatchInput) (domain.RecordingBatchV2, error) {
	if domain.ValidateRecordingV2(current) != nil || input.ExerciseEntryKeys == nil {
		return domain.RecordingBatchV2{}, repository.ErrInvalid
	}
	entries := make(map[string]domain.Exercise, len(current.Entries)+len(input.AddEntries))
	for key, item := range current.Entries {
		entries[key] = item
	}
	result := domain.RecordingBatchV2{Date: input.Date, Mode: input.Mode, Order: input.ExerciseEntryKeys, RemoveKeys: input.RemoveRecordKeys, RestoreKeys: input.RestoreRecordKeys, RemoveEntryKeys: input.RemoveEntryKeys, RestoreEntryKeys: input.RestoreEntryKeys}
	if len(input.RemoveRecords) > 0 {
		result.RemoveKeys = append([]string{}, result.RemoveKeys...)
		result.RemovalPositions = make(map[string]int, len(input.RemoveRecords))
		for _, item := range input.RemoveRecords {
			if _, exists := result.RemovalPositions[item.RecordKey]; exists {
				return domain.RecordingBatchV2{}, repository.ErrInvalid
			}
			result.RemoveKeys = append(result.RemoveKeys, item.RecordKey)
			result.RemovalPositions[item.RecordKey] = item.RemovedPosition
		}
	}
	for _, item := range input.AddEntries {
		exercise := domain.Exercise{Source: domain.Custom, Name: item.ExerciseName}
		result.AddEntries = append(result.AddEntries, domain.EntryV2{Key: item.ExerciseEntryKey, Exercise: exercise})
		if old, exists := entries[item.ExerciseEntryKey]; exists && old != exercise {
			return domain.RecordingBatchV2{}, repository.ErrSetConflict
		}
		entries[item.ExerciseEntryKey] = exercise
	}
	for _, item := range input.Sets {
		exercise, exists := entries[item.ExerciseEntryKey]
		if !exists {
			return domain.RecordingBatchV2{}, repository.ErrInvalid
		}
		result.Upserts = append(result.Upserts, domain.SetV2{RecordKey: item.RecordKey, ExerciseEntryKey: item.ExerciseEntryKey, Exercise: exercise, Index: item.SetIndex, Confirmed: item.Confirmed, RPE: item.RPE, Measurement: measurementFromV2(item.Measurement)})
	}
	return result, nil
}
func volumeToV2(summary domain.VolumeSummaryV2) recordingV2VolumeJSON {
	return recordingV2VolumeJSON{CalculatedKg: summary.Kilograms, FullSets: summary.FullSets,
		PartialSets: summary.PartialSets, ExcludedSets: summary.ExcludedSets}
}

func recordingV2Detail(snapshot repository.RecordingV2Snapshot) (recordingV2DetailJSON, error) {
	v := snapshot.Recording
	if snapshot.ID == 0 || snapshot.Version == 0 || snapshot.Version > 9007199254740991 || snapshot.CreatedAt.IsZero() || snapshot.UpdatedAt.IsZero() || domain.ValidateRecordingV2(v) != nil {
		return recordingV2DetailJSON{}, repository.ErrUnavailable
	}
	result := recordingV2DetailJSON{ID: strconv.FormatUint(snapshot.ID, 10), Version: snapshot.Version, Source: "free", Date: snapshot.Date, TimeZone: snapshot.TimeZone, ClientEventID: snapshot.ClientEventID,
		Status: v.Status, Mode: v.Mode, CreatedAt: snapshot.CreatedAt.UTC().Format(time.RFC3339Nano), UpdatedAt: snapshot.UpdatedAt.UTC().Format(time.RFC3339Nano),
		Entries: make([]recordingV2EntryJSON, 0, len(v.Order)), Sets: make([]recordingV2SetJSON, 0, len(v.Sets)), RemovedSets: make([]recordingV2RemovedJSON, 0, len(v.Removed))}
	if snapshot.RecordedAt != nil {
		value := snapshot.RecordedAt.UTC().Format(time.RFC3339Nano)
		result.RecordedAt = &value
	}
	positions := make(map[string]int, len(v.Order))
	setsByEntry := make(map[string][]domain.SetV2, len(v.Order))
	for _, set := range v.Sets {
		setsByEntry[set.ExerciseEntryKey] = append(setsByEntry[set.ExerciseEntryKey], set)
	}
	for index, key := range v.Order {
		exercise := v.Entries[key]
		if exercise.Source != domain.Custom {
			return recordingV2DetailJSON{}, repository.ErrUnsupportedSource
		}
		entry := recordingV2EntryJSON{ExerciseEntryKey: key, Source: string(exercise.Source), ExerciseName: exercise.Name}
		if position, removed := v.RemovedEntries[key]; removed {
			entry.RemovedPosition = &position
		} else {
			volume, err := domain.SummarizeV2(setsByEntry[key])
			if err != nil {
				return recordingV2DetailJSON{}, repository.ErrUnavailable
			}
			summary := volumeToV2(volume)
			entry.Volume = &summary
		}
		result.Entries = append(result.Entries, entry)
		positions[key] = index
	}
	// A repeated action can occur in separate segments. Aggregate only active
	// segments of the same identity; removed snapshots never contribute.
	type actionGroup struct {
		keys []string
		sets []domain.SetV2
	}
	groups := make(map[domain.Exercise]*actionGroup)
	var ordered []*actionGroup
	for _, key := range v.Order {
		if _, removed := v.RemovedEntries[key]; removed || len(setsByEntry[key]) == 0 {
			continue
		}
		exercise := v.Entries[key]
		group := groups[exercise]
		if group == nil {
			group = &actionGroup{}
			groups[exercise] = group
			ordered = append(ordered, group)
		}
		group.keys = append(group.keys, key)
		group.sets = append(group.sets, setsByEntry[key]...)
	}
	for _, group := range ordered {
		volume, err := domain.SummarizeV2(group.sets)
		if err != nil {
			return recordingV2DetailJSON{}, repository.ErrUnavailable
		}
		result.ActionVolumes = append(result.ActionVolumes, recordingV2ActionVolumeJSON{ExerciseEntryKeys: group.keys, Volume: volumeToV2(volume)})
	}
	for _, set := range v.Sets {
		result.Sets = append(result.Sets, setToV2(set))
	}
	sort.Slice(result.Sets, func(i, j int) bool {
		a, b := result.Sets[i], result.Sets[j]
		if a.ExerciseEntryKey != b.ExerciseEntryKey {
			return positions[a.ExerciseEntryKey] < positions[b.ExerciseEntryKey]
		}
		return a.SetIndex < b.SetIndex
	})
	for _, removed := range v.Removed {
		result.RemovedSets = append(result.RemovedSets, recordingV2RemovedJSON{Set: setToV2(removed.Set), RemovedPosition: removed.RemovedPosition})
	}
	sort.Slice(result.RemovedSets, func(i, j int) bool {
		a, b := result.RemovedSets[i].Set, result.RemovedSets[j].Set
		if a.ExerciseEntryKey != b.ExerciseEntryKey {
			return positions[a.ExerciseEntryKey] < positions[b.ExerciseEntryKey]
		}
		return a.SetIndex < b.SetIndex
	})
	return result, nil
}
