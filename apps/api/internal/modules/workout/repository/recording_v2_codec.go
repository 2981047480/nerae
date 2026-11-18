package repository

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"sort"

	"nerae/api/internal/modules/workout/domain"
)

var errRecordingV2Snapshot = errors.New("recording v2 snapshot invalid")

const maxRecordingV2SnapshotBytes = 4 << 20

type recordingV2Document struct {
	Schema  int                    `json:"schema"`
	Status  domain.Status          `json:"status"`
	Mode    domain.RecordingModeV2 `json:"mode"`
	Entries []recordingV2Entry     `json:"entries"`
	Sets    []recordingV2Set       `json:"sets"`
	Removed []recordingV2Removed   `json:"removed"`
}
type recordingV2Entry struct {
	Key             string        `json:"key"`
	Source          domain.Source `json:"source"`
	ID              string        `json:"id,omitempty"`
	Name            string        `json:"name,omitempty"`
	Version         uint64        `json:"version,omitempty"`
	RemovedPosition *int          `json:"removedPosition,omitempty"`
}
type recordingV2Side struct {
	State    domain.SideStateV2 `json:"state"`
	WeightKg *string            `json:"weightKg,omitempty"`
	Reps     *int               `json:"reps,omitempty"`
}
type recordingV2Measurement struct {
	Method       domain.MeasurementMethodV2 `json:"method"`
	WeightKg     *string                    `json:"weightKg,omitempty"`
	Reps         *int                       `json:"reps,omitempty"`
	BodyweightKg *string                    `json:"bodyweightKg,omitempty"`
	AdjustmentKg *string                    `json:"adjustmentKg,omitempty"`
	Left         *recordingV2Side           `json:"left,omitempty"`
	Right        *recordingV2Side           `json:"right,omitempty"`
}
type recordingV2Set struct {
	Key         string                 `json:"key"`
	EntryKey    string                 `json:"entryKey"`
	Index       int                    `json:"index"`
	Confirmed   bool                   `json:"confirmed"`
	RPE         *string                `json:"rpe,omitempty"`
	Measurement recordingV2Measurement `json:"measurement"`
}
type recordingV2Removed struct {
	Set      recordingV2Set `json:"set"`
	Position int            `json:"position"`
}

func encodeSideV2(value *domain.SideMeasurementV2) *recordingV2Side {
	if value == nil {
		return nil
	}
	return &recordingV2Side{State: value.State, WeightKg: value.WeightKg, Reps: value.Reps}
}
func decodeSideV2(value *recordingV2Side) *domain.SideMeasurementV2 {
	if value == nil {
		return nil
	}
	return &domain.SideMeasurementV2{State: value.State, WeightKg: value.WeightKg, Reps: value.Reps}
}
func encodeSetV2(value domain.SetV2) recordingV2Set {
	m := value.Measurement
	return recordingV2Set{Key: value.RecordKey, EntryKey: value.ExerciseEntryKey, Index: value.Index, Confirmed: value.Confirmed, RPE: value.RPE,
		Measurement: recordingV2Measurement{Method: m.Method, WeightKg: m.WeightKg, Reps: m.Reps, BodyweightKg: m.BodyweightKg,
			AdjustmentKg: m.AdjustmentKg, Left: encodeSideV2(m.Left), Right: encodeSideV2(m.Right)}}
}
func decodeSetV2(value recordingV2Set, entries map[string]domain.Exercise) (domain.SetV2, error) {
	exercise, exists := entries[value.EntryKey]
	if !exists {
		return domain.SetV2{}, errRecordingV2Snapshot
	}
	m := value.Measurement
	return domain.SetV2{RecordKey: value.Key, ExerciseEntryKey: value.EntryKey, Exercise: exercise, Index: value.Index, Confirmed: value.Confirmed, RPE: value.RPE,
		Measurement: domain.MeasurementV2{Method: m.Method, WeightKg: m.WeightKg, Reps: m.Reps, BodyweightKg: m.BodyweightKg,
			AdjustmentKg: m.AdjustmentKg, Left: decodeSideV2(m.Left), Right: decodeSideV2(m.Right)}}, nil
}

// This is an internal, canonical plaintext representation for encryption.
// It is never an HTTP response and may contain private body measurements.
func encodeRecordingV2(value domain.RecordingV2) ([]byte, error) {
	if domain.ValidateRecordingV2(value) != nil {
		return nil, errRecordingV2Snapshot
	}
	doc := recordingV2Document{Schema: 2, Status: value.Status, Mode: value.Mode,
		Entries: make([]recordingV2Entry, 0, len(value.Order)), Sets: make([]recordingV2Set, 0, len(value.Sets)),
		Removed: make([]recordingV2Removed, 0, len(value.Removed))}
	positions := make(map[string]int, len(value.Order))
	for index, key := range value.Order {
		exercise := value.Entries[key]
		// Schema 3 is emitted only when a personal identity exists. Older readers
		// reject it rather than discarding its ID or frozen version.
		if exercise.Source != domain.Custom && exercise.Source != domain.Personal {
			return nil, errRecordingV2Snapshot
		}
		if exercise.Source == domain.Personal {
			doc.Schema = 3
		}
		positions[key] = index
		entry := recordingV2Entry{Key: key, Source: exercise.Source, ID: exercise.ID, Name: exercise.Name, Version: exercise.Version}
		if position, removed := value.RemovedEntries[key]; removed {
			entry.RemovedPosition = &position
		}
		doc.Entries = append(doc.Entries, entry)
	}
	for _, set := range value.Sets {
		doc.Sets = append(doc.Sets, encodeSetV2(set))
	}
	sort.Slice(doc.Sets, func(i, j int) bool {
		a, b := doc.Sets[i], doc.Sets[j]
		if a.EntryKey != b.EntryKey {
			return positions[a.EntryKey] < positions[b.EntryKey]
		}
		return a.Index < b.Index
	})
	for _, removed := range value.Removed {
		doc.Removed = append(doc.Removed, recordingV2Removed{Set: encodeSetV2(removed.Set), Position: removed.RemovedPosition})
	}
	sort.Slice(doc.Removed, func(i, j int) bool {
		a, b := doc.Removed[i].Set, doc.Removed[j].Set
		if a.EntryKey != b.EntryKey {
			return positions[a.EntryKey] < positions[b.EntryKey]
		}
		return a.Index < b.Index
	})
	encoded, err := json.Marshal(doc)
	if err != nil || len(encoded) > maxRecordingV2SnapshotBytes {
		return nil, errRecordingV2Snapshot
	}
	return encoded, nil
}

func decodeRecordingV2(raw []byte) (domain.RecordingV2, error) {
	if len(raw) == 0 || len(raw) > maxRecordingV2SnapshotBytes {
		return domain.RecordingV2{}, errRecordingV2Snapshot
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var doc recordingV2Document
	if err := decoder.Decode(&doc); err != nil {
		return domain.RecordingV2{}, errRecordingV2Snapshot
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return domain.RecordingV2{}, errRecordingV2Snapshot
	}
	canonical, err := json.Marshal(doc)
	if err != nil || !bytes.Equal(canonical, raw) || (doc.Schema != 2 && doc.Schema != 3) || doc.Entries == nil || doc.Sets == nil || doc.Removed == nil {
		return domain.RecordingV2{}, errRecordingV2Snapshot
	}
	value := domain.RecordingV2{Status: doc.Status, Mode: doc.Mode, Entries: make(map[string]domain.Exercise, len(doc.Entries)),
		Order: make([]string, 0, len(doc.Entries)), Sets: make(map[string]domain.SetV2, len(doc.Sets)),
		Removed: make(map[string]domain.RemovedSetV2, len(doc.Removed)), RemovedEntries: make(map[string]int)}
	for _, entry := range doc.Entries {
		if _, exists := value.Entries[entry.Key]; exists || (entry.Source != domain.Custom && (doc.Schema != 3 || entry.Source != domain.Personal)) {
			return domain.RecordingV2{}, errRecordingV2Snapshot
		}
		value.Order = append(value.Order, entry.Key)
		value.Entries[entry.Key] = domain.Exercise{Source: entry.Source, ID: entry.ID, Name: entry.Name, Version: entry.Version}
		if entry.RemovedPosition != nil {
			value.RemovedEntries[entry.Key] = *entry.RemovedPosition
		}
	}
	for _, rawSet := range doc.Sets {
		if _, exists := value.Sets[rawSet.Key]; exists {
			return domain.RecordingV2{}, errRecordingV2Snapshot
		}
		set, err := decodeSetV2(rawSet, value.Entries)
		if err != nil {
			return domain.RecordingV2{}, errRecordingV2Snapshot
		}
		value.Sets[rawSet.Key] = set
	}
	for _, item := range doc.Removed {
		if _, exists := value.Removed[item.Set.Key]; exists {
			return domain.RecordingV2{}, errRecordingV2Snapshot
		}
		set, err := decodeSetV2(item.Set, value.Entries)
		if err != nil {
			return domain.RecordingV2{}, errRecordingV2Snapshot
		}
		value.Removed[item.Set.Key] = domain.RemovedSetV2{Set: set, RemovedPosition: item.Position}
	}
	if domain.ValidateRecordingV2(value) != nil {
		return domain.RecordingV2{}, errRecordingV2Snapshot
	}
	// Require the stored sort order as well as field order to be canonical.
	canonical, err = encodeRecordingV2(value)
	if err != nil || !bytes.Equal(canonical, raw) {
		return domain.RecordingV2{}, errRecordingV2Snapshot
	}
	return value, nil
}
