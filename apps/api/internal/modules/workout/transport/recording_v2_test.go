package transport

import (
	"encoding/json"
	"testing"
	"time"

	"nerae/api/internal/modules/workout/domain"
	"nerae/api/internal/modules/workout/repository"
)

func TestRecordingV2ProjectionAndBatchKeepBlankValues(t *testing.T) {
	exercise := domain.Exercise{Source: domain.Custom, Name: "深蹲"}
	current := domain.RecordingV2{Status: domain.InProgress, Mode: domain.RecordingAfterWorkoutV2, Entries: map[string]domain.Exercise{"entry-0001": exercise}, Order: []string{"entry-0001"}, Sets: map[string]domain.SetV2{}, Removed: map[string]domain.RemovedSetV2{}, RemovedEntries: map[string]int{}}
	request := recordingV2BatchInput{ExpectedVersion: 1, ExerciseEntryKeys: []string{"entry-0001"}, Sets: []recordingV2SetJSON{{RecordKey: "record-001", ExerciseEntryKey: "entry-0001", SetIndex: 1, Measurement: recordingV2MeasurementJSON{Method: domain.MeasurementTotal}}}}
	batch, err := buildRecordingV2Batch(current, request)
	if err != nil || len(batch.Upserts) != 1 || batch.Upserts[0].Measurement.WeightKg != nil || batch.Upserts[0].Exercise != exercise {
		t.Fatal("blank group did not map safely")
	}
	next, _, err := domain.ApplyRecordingBatchV2(current, batch)
	if err != nil {
		t.Fatal("valid batch rejected")
	}
	snapshot := repository.RecordingV2Snapshot{ID: 1, Version: 2, Date: "2026-11-15", TimeZone: "UTC", ClientEventID: "event-0001", CreatedAt: time.Date(2026, 11, 15, 0, 0, 0, 0, time.UTC), UpdatedAt: time.Date(2026, 11, 15, 0, 0, 1, 0, time.UTC), Recording: next}
	value, err := recordingV2Detail(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if json.Unmarshal(raw, &decoded) != nil {
		t.Fatal("invalid projection")
	}
	sets := decoded["sets"].([]any)
	measurement := sets[0].(map[string]any)["measurement"].(map[string]any)
	if _, exists := measurement["weightKg"]; exists {
		t.Fatal("missing weight became zero")
	}
	if _, exists := measurement["reps"]; exists {
		t.Fatal("missing repetitions became zero")
	}
}
func TestRecordingV2BatchRejectsUnknownExercise(t *testing.T) {
	current := domain.RecordingV2{Status: domain.InProgress, Mode: domain.RecordingAfterWorkoutV2, Entries: map[string]domain.Exercise{}, Order: []string{}, Sets: map[string]domain.SetV2{}, Removed: map[string]domain.RemovedSetV2{}, RemovedEntries: map[string]int{}}
	request := recordingV2BatchInput{ExpectedVersion: 1, ExerciseEntryKeys: []string{"entry-0001"}, Sets: []recordingV2SetJSON{{RecordKey: "record-001", ExerciseEntryKey: "entry-0001", SetIndex: 1, Measurement: recordingV2MeasurementJSON{Method: domain.MeasurementTotal}}}}
	if _, err := buildRecordingV2Batch(current, request); err == nil {
		t.Fatal("accepted group without a real action")
	}
}

func TestRecordingV2RejectsUnpairedUnicodeEscapes(t *testing.T) {
	for _, value := range []string{`{"name":"\ud83d\udcaa"}`, `{"name":"中文"}`, `{"name":"\\ud800"}`} {
		if !validV2UnicodeEscapes([]byte(value)) {
			t.Fatal("valid escaped string rejected")
		}
	}
	for _, value := range []string{`{"name":"\ud800"}`, `{"name":"\udc00"}`, `{"name":"\ud800\u0041"}`} {
		if validV2UnicodeEscapes([]byte(value)) {
			t.Fatal("unpaired unicode escape accepted")
		}
	}
}

func TestRecordingV2BatchCarriesOfflineRemovalPosition(t *testing.T) {
	exercise := domain.Exercise{Source: domain.Custom, Name: "深蹲"}
	current := domain.RecordingV2{Status: domain.InProgress, Mode: domain.RecordingAfterWorkoutV2, Entries: map[string]domain.Exercise{"entry-0001": exercise}, Order: []string{"entry-0001"}, Sets: map[string]domain.SetV2{}, Removed: map[string]domain.RemovedSetV2{}, RemovedEntries: map[string]int{}}
	input := recordingV2BatchInput{ExpectedVersion: 1, ExerciseEntryKeys: []string{"entry-0001"}, RemoveRecords: []recordingV2RemoveInput{{RecordKey: "record-001", RemovedPosition: 2}}}
	batch, err := buildRecordingV2Batch(current, input)
	if err != nil || len(batch.RemoveKeys) != 1 || batch.RemovalPositions["record-001"] != 2 {
		t.Fatal("offline removal metadata was not preserved")
	}
}

func TestRecordingV2DetailCalculatesVolumePerActiveEntry(t *testing.T) {
	exercise := domain.Exercise{Source: domain.Custom, Name: "深蹲"}
	weight12, weight14 := "12", "14"
	reps12, reps10 := 12, 10
	mk := func(key string, index int, weight *string, reps *int) domain.SetV2 {
		return domain.SetV2{RecordKey: key, ExerciseEntryKey: "entry-0001", Exercise: exercise, Index: index,
			Measurement: domain.MeasurementV2{Method: domain.MeasurementTotal, WeightKg: weight, Reps: reps}}
	}
	recording := domain.RecordingV2{Status: domain.InProgress, Mode: domain.RecordingAfterWorkoutV2,
		Entries: map[string]domain.Exercise{"entry-0001": exercise}, Order: []string{"entry-0001"},
		Sets:    map[string]domain.SetV2{"record-001": mk("record-001", 1, &weight12, &reps12), "record-002": mk("record-002", 2, &weight14, &reps10), "record-003": mk("record-003", 3, &weight14, nil)},
		Removed: map[string]domain.RemovedSetV2{"record-004": {Set: mk("record-004", 4, &weight14, &reps10), RemovedPosition: 4}}, RemovedEntries: map[string]int{}}
	snapshot := repository.RecordingV2Snapshot{ID: 1, Version: 2, Date: "2026-11-15", TimeZone: "UTC", ClientEventID: "event-0001", CreatedAt: time.Date(2026, 11, 15, 0, 0, 0, 0, time.UTC), UpdatedAt: time.Date(2026, 11, 15, 0, 0, 1, 0, time.UTC), Recording: recording}
	result, err := recordingV2Detail(snapshot)
	if err != nil || len(result.Entries) != 1 || result.Entries[0].Volume == nil || result.Entries[0].Volume.CalculatedKg == nil || *result.Entries[0].Volume.CalculatedKg != "284.00" || result.Entries[0].Volume.ExcludedSets != 1 {
		t.Fatal("volume should include 284 kg, exclude the incomplete group and ignore removed groups")
	}
	recording.RemovedEntries["entry-0001"] = 1
	snapshot.Recording = recording
	result, err = recordingV2Detail(snapshot)
	if err != nil || result.Entries[0].Volume != nil {
		t.Fatal("removed action must not expose an active volume")
	}
}

func TestRecordingV2DetailCombinesRepeatedActionSegments(t *testing.T) {
	squat := domain.Exercise{Source: domain.Custom, Name: "深蹲"}
	bench := domain.Exercise{Source: domain.Custom, Name: "卧推"}
	w40, w50, w30 := "40", "50", "30"
	r8, r6, r10 := 8, 6, 10
	mk := func(record, entry string, exercise domain.Exercise, index int, weight *string, reps *int) domain.SetV2 {
		return domain.SetV2{RecordKey: record, ExerciseEntryKey: entry, Exercise: exercise, Index: index,
			Measurement: domain.MeasurementV2{Method: domain.MeasurementTotal, WeightKg: weight, Reps: reps}}
	}
	recording := domain.RecordingV2{Status: domain.InProgress, Mode: domain.RecordingAfterWorkoutV2,
		Entries: map[string]domain.Exercise{"entry-0001": squat, "entry-0002": bench, "entry-0003": squat, "entry-0004": squat},
		Order:   []string{"entry-0001", "entry-0002", "entry-0003", "entry-0004"},
		Sets: map[string]domain.SetV2{
			"record-001": mk("record-001", "entry-0001", squat, 1, &w40, &r8),
			"record-002": mk("record-002", "entry-0002", bench, 1, &w30, &r10),
			"record-003": mk("record-003", "entry-0003", squat, 1, &w50, &r6),
			"record-004": mk("record-004", "entry-0003", squat, 2, &w50, nil),
			"record-005": mk("record-005", "entry-0004", squat, 1, &w50, &r10),
		},
		Removed:        map[string]domain.RemovedSetV2{"record-006": {Set: mk("record-006", "entry-0001", squat, 2, &w50, &r10), RemovedPosition: 2}},
		RemovedEntries: map[string]int{"entry-0004": 4}}
	snapshot := repository.RecordingV2Snapshot{ID: 1, Version: 2, Date: "2026-11-15", TimeZone: "UTC", ClientEventID: "event-0001",
		CreatedAt: time.Date(2026, 11, 15, 0, 0, 0, 0, time.UTC), UpdatedAt: time.Date(2026, 11, 15, 0, 0, 1, 0, time.UTC), Recording: recording}
	detail, err := recordingV2Detail(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if len(detail.ActionVolumes) != 2 {
		t.Fatalf("expected squat and bench totals, got %d", len(detail.ActionVolumes))
	}
	s := detail.ActionVolumes[0]
	if len(s.ExerciseEntryKeys) != 2 || s.ExerciseEntryKeys[0] != "entry-0001" || s.ExerciseEntryKeys[1] != "entry-0003" ||
		s.Volume.CalculatedKg == nil || *s.Volume.CalculatedKg != "620.00" || s.Volume.FullSets != 2 || s.Volume.ExcludedSets != 1 {
		t.Fatal("squat total must combine active segments, exclude incomplete sets and ignore removed sets and actions")
	}
	b := detail.ActionVolumes[1]
	if len(b.ExerciseEntryKeys) != 1 || b.ExerciseEntryKeys[0] != "entry-0002" || b.Volume.CalculatedKg == nil || *b.Volume.CalculatedKg != "300.00" {
		t.Fatal("bench must retain its separate total")
	}
}
