package domain

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func recordingV2Fixture() RecordingV2 {
	exercise := Exercise{Source: Custom, Name: "深蹲"}
	sets := map[string]SetV2{}
	for index, key := range []string{"record-001", "record-002", "record-003"} {
		set := testV2Set(key, MeasurementV2{Method: MeasurementTotal, WeightKg: stringValue("45"), Reps: intValue(8)})
		set.Index = index + 1
		set.Confirmed = index == 1
		sets[key] = set
	}
	return RecordingV2{Status: Completed, Mode: RecordingAfterWorkoutV2, Entries: map[string]Exercise{"entry-0001": exercise},
		Order: []string{"entry-0001"}, Sets: sets, Removed: map[string]RemovedSetV2{}, RemovedEntries: map[string]int{}}
}

func TestRecordingV2RemoveRestorePreservesSnapshotAndPosition(t *testing.T) {
	original := recordingV2Fixture()
	next, changed, err := ApplyRecordingBatchV2(original, RecordingBatchV2{RemoveKeys: []string{"record-002"}, Order: []string{"entry-0001"}})
	if err != nil || !changed || len(next.Sets) != 2 || next.Removed["record-002"].RemovedPosition != 2 {
		t.Fatalf("remove: %#v changed=%v err=%v", next, changed, err)
	}
	if len(original.Sets) != 3 || len(original.Removed) != 0 {
		t.Fatal("source was mutated")
	}
	newSet := testV2Set("record-004", MeasurementV2{Method: MeasurementTotal, WeightKg: stringValue("50"), Reps: intValue(6)})
	newSet.Index = 4
	next, _, err = ApplyRecordingBatchV2(next, RecordingBatchV2{Upserts: []SetV2{newSet}, Order: []string{"entry-0001"}})
	if err != nil {
		t.Fatal(err)
	}
	restored, changed, err := ApplyRecordingBatchV2(next, RecordingBatchV2{RestoreKeys: []string{"record-002"}, Order: []string{"entry-0001"}})
	if err != nil || !changed || len(restored.Removed) != 0 || restored.Sets["record-002"].Index != 2 ||
		*restored.Sets["record-002"].Measurement.WeightKg != "45" || *restored.Sets["record-002"].Measurement.Reps != 8 || !restored.Sets["record-002"].Confirmed {
		t.Fatalf("restore: %#v changed=%v err=%v", restored, changed, err)
	}
	*restored.Sets["record-002"].Measurement.WeightKg = "99"
	if *next.Removed["record-002"].Set.Measurement.WeightKg != "45" {
		t.Fatal("restore exposed mutable source data")
	}
}

func TestRecordingV2KeepsEmptyActionAndLastGroupReturnsToDraft(t *testing.T) {
	original := recordingV2Fixture()
	second := Exercise{Source: Custom, Name: "卧推"}
	next, _, err := ApplyRecordingBatchV2(original, RecordingBatchV2{AddEntries: []EntryV2{{Key: "entry-0002", Exercise: second}},
		RemoveKeys: []string{"record-001", "record-002", "record-003"}, Order: []string{"entry-0001", "entry-0002"}})
	if err != nil || next.Status != InProgress || len(next.Sets) != 0 || len(next.Removed) != 3 ||
		!reflect.DeepEqual(next.Order, []string{"entry-0001", "entry-0002"}) {
		t.Fatalf("last set removal: %#v, %v", next, err)
	}
	blank := SetV2{RecordKey: "record-004", ExerciseEntryKey: "entry-0002", Exercise: second, Index: 1, Measurement: MeasurementV2{Method: MeasurementTotal}}
	next, _, err = ApplyRecordingBatchV2(next, RecordingBatchV2{Upserts: []SetV2{blank}, Order: next.Order})
	if err != nil {
		t.Fatal(err)
	}
	completed, err := CompleteRecordingV2(next)
	if err != nil || completed.Status != Completed || len(completed.Sets) != 1 {
		t.Fatalf("blank group must count: %#v, %v", completed, err)
	}
}

func TestRecordingV2RejectsCollisionsWithoutPartialChanges(t *testing.T) {
	original := recordingV2Fixture()
	removed, _, err := ApplyRecordingBatchV2(original, RecordingBatchV2{RemoveKeys: []string{"record-002"}, Order: original.Order})
	if err != nil {
		t.Fatal(err)
	}
	collision := testV2Set("record-004", MeasurementV2{Method: MeasurementTotal})
	collision.Index = 2
	for _, batch := range []RecordingBatchV2{
		{Upserts: []SetV2{collision}, Order: removed.Order},
		{Upserts: []SetV2{removed.Removed["record-002"].Set}, Order: removed.Order},
		{RestoreKeys: []string{"record-003"}, Order: removed.Order},
		{RemoveKeys: []string{"record-001", "record-001"}, Order: removed.Order},
		{RemoveKeys: []string{"record-001"}, Order: []string{}},
	} {
		before := cloneRecordingV2(removed)
		if _, _, err := ApplyRecordingBatchV2(removed, batch); err == nil {
			t.Fatalf("accepted unsafe batch: %#v", batch)
		}
		if !reflect.DeepEqual(removed, before) {
			t.Fatal("failed batch changed stored state")
		}
	}
}

func TestRecordingV2AllowsRepeatedExerciseAsDistinctEntries(t *testing.T) {
	original := recordingV2Fixture()
	next, changed, err := ApplyRecordingBatchV2(original, RecordingBatchV2{AddEntries: []EntryV2{{Key: "entry-0002", Exercise: original.Entries["entry-0001"]}},
		Order: []string{"entry-0001", "entry-0002"}})
	if err != nil || !changed || len(next.Entries) != 2 || len(next.Order) != 2 {
		t.Fatalf("repeated action was lost: %#v, %v", next, err)
	}
}

func TestRecordingV2PersonalExerciseRequiresStableIdentityAndVersion(t *testing.T) {
	valid := Exercise{Source: Personal, ID: "97ab0210c56f48ae930de1629b761f09", Name: "上斜推举", Version: 1}
	base := recordingV2Fixture()
	for name, exercise := range map[string]Exercise{
		"missing ID":          {Source: Personal, Name: valid.Name, Version: 1},
		"missing version":     {Source: Personal, ID: valid.ID, Name: valid.Name},
		"zero ID":             {Source: Personal, ID: "00000000000000000000000000000000", Name: valid.Name, Version: 1},
		"noncanonical name":   {Source: Personal, ID: valid.ID, Name: " 上斜推举 ", Version: 1},
		"legacy with version": {Source: Custom, Name: valid.Name, Version: 1},
	} {
		t.Run(name, func(t *testing.T) {
			_, _, err := ApplyRecordingBatchV2(base, RecordingBatchV2{AddEntries: []EntryV2{{Key: "entry-0002", Exercise: exercise}}, Order: []string{"entry-0001", "entry-0002"}})
			if err == nil {
				t.Fatal("invalid identity accepted")
			}
		})
	}
	next, _, err := ApplyRecordingBatchV2(base, RecordingBatchV2{AddEntries: []EntryV2{{Key: "entry-0002", Exercise: valid}}, Order: []string{"entry-0001", "entry-0002"}})
	if err != nil || next.Entries["entry-0002"] != valid {
		t.Fatal("valid versioned action was not retained", err)
	}
	legacy := Set{RecordKey: "record-010", ExerciseEntryKey: "entry-0002", Exercise: valid, Index: 1, Kind: Weight, WeightKg: "12", Reps: 8}
	if ValidateSet(legacy) == nil {
		t.Fatal("legacy contract accepted a personal identity")
	}
}

func TestRecordingV2DoesNotAccidentallyExposeSensitiveDataInLogsOrJSON(t *testing.T) {
	value := recordingV2Fixture()
	for _, output := range []string{fmt.Sprintf("%v", value), fmt.Sprintf("%#v", value), fmt.Sprintf("%+v", value.Sets["record-001"])} {
		if strings.Contains(output, "深蹲") || strings.Contains(output, "45") {
			t.Fatalf("sensitive data exposed: %q", output)
		}
	}
	if _, err := json.Marshal(value); err == nil {
		t.Fatal("internal recording should require an explicit authorized projection")
	}
	if _, err := json.Marshal(value.Sets["record-001"]); err == nil {
		t.Fatal("internal set should require an explicit authorized projection")
	}
}

// Removing an entire action keeps its groups, including separately removed
// groups, so undo can restore the action at its original position.
func TestRecordingV2RemoveRestoreActionAndItsGroups(t *testing.T) {
	original := recordingV2Fixture()
	second := Exercise{Source: Custom, Name: "卧推"}
	initial, _, err := ApplyRecordingBatchV2(original, RecordingBatchV2{
		AddEntries: []EntryV2{{Key: "entry-0002", Exercise: second}},
		RemoveKeys: []string{"record-002"}, Order: []string{"entry-0001", "entry-0002"},
	})
	if err != nil {
		t.Fatal(err)
	}
	removed, changed, err := ApplyRecordingBatchV2(initial, RecordingBatchV2{
		RemoveEntryKeys: []string{"entry-0001"}, Order: initial.Order,
	})
	if err != nil || !changed || removed.Status != InProgress || removed.RemovedEntries["entry-0001"] != 1 {
		t.Fatalf("remove action: changed=%v err=%v", changed, err)
	}
	if _, err := CompleteRecordingV2(removed); err == nil {
		t.Fatal("only removed groups cannot complete the recording")
	}
	if _, _, err := ApplyRecordingBatchV2(removed, RecordingBatchV2{RestoreKeys: []string{"record-002"}, Order: removed.Order}); err == nil {
		t.Fatal("cannot restore a group while its action is removed")
	}
	restored, changed, err := ApplyRecordingBatchV2(removed, RecordingBatchV2{
		RestoreEntryKeys: []string{"entry-0001"}, Order: removed.Order,
	})
	if err != nil || !changed || len(restored.RemovedEntries) != 0 || len(restored.Sets) != 2 ||
		restored.Removed["record-002"].RemovedPosition != 2 || !reflect.DeepEqual(restored.Order, initial.Order) {
		t.Fatalf("restore action: changed=%v err=%v", changed, err)
	}
	if *restored.Sets["record-001"].Measurement.WeightKg != "45" {
		t.Fatal("restored action lost its groups")
	}
	if len(initial.RemovedEntries) != 0 || initial.Status != Completed {
		t.Fatal("source was mutated")
	}
}

func TestRecordingV2CanSwitchModesWithoutChangingGroups(t *testing.T) {
	original := recordingV2Fixture()
	mode := RecordingPerSetV2
	next, changed, err := ApplyRecordingBatchV2(original, RecordingBatchV2{Mode: &mode, Order: original.Order})
	if err != nil || !changed || next.Mode != mode || !reflect.DeepEqual(next.Sets, original.Sets) {
		t.Fatal("mode switch must retain the same groups")
	}
	invalid := RecordingModeV2("unknown")
	if _, _, err := ApplyRecordingBatchV2(next, RecordingBatchV2{Mode: &invalid, Order: next.Order}); err == nil {
		t.Fatal("unknown mode accepted")
	}
}

func TestRecordingV2OfflineEditRemoveAndRestoreAreAtomic(t *testing.T) {
	original := recordingV2Fixture()
	edited := cloneSetV2(original.Sets["record-002"])
	*edited.Measurement.WeightKg = "50"
	removed, changed, err := ApplyRecordingBatchV2(original, RecordingBatchV2{Upserts: []SetV2{edited}, RemoveKeys: []string{"record-002"}, Order: original.Order})
	if err != nil || !changed || *removed.Removed["record-002"].Set.Measurement.WeightKg != "50" {
		t.Fatal("remove did not retain the latest offline edit")
	}
	editedAgain := cloneSetV2(removed.Removed["record-002"].Set)
	*editedAgain.Measurement.WeightKg = "55"
	restored, changed, err := ApplyRecordingBatchV2(removed, RecordingBatchV2{RestoreKeys: []string{"record-002"}, Upserts: []SetV2{editedAgain}, Order: removed.Order})
	if err != nil || !changed || *restored.Sets["record-002"].Measurement.WeightKg != "55" {
		t.Fatal("restore did not retain the latest offline edit")
	}
	fresh := testV2Set("record-004", MeasurementV2{Method: MeasurementTotal, WeightKg: stringValue("60")})
	fresh.Index = 4
	hidden, _, err := ApplyRecordingBatchV2(restored, RecordingBatchV2{Upserts: []SetV2{fresh}, RemoveKeys: []string{"record-004"}, Order: restored.Order})
	if err != nil || *hidden.Removed["record-004"].Set.Measurement.WeightKg != "60" {
		t.Fatal("unsynced removed group was not retained")
	}
	if _, _, err := ApplyRecordingBatchV2(hidden, RecordingBatchV2{RestoreKeys: []string{"record-004"}, RemoveKeys: []string{"record-004"}, Order: hidden.Order}); err == nil {
		t.Fatal("ambiguous remove and restore accepted")
	}
	updatedHidden := cloneSetV2(hidden.Removed["record-004"].Set)
	*updatedHidden.Measurement.WeightKg = "65"
	hiddenAgain, _, err := ApplyRecordingBatchV2(hidden, RecordingBatchV2{RestoreKeys: []string{"record-004"}, Upserts: []SetV2{updatedHidden}, RemoveKeys: []string{"record-004"}, Order: hidden.Order})
	if err != nil || *hiddenAgain.Removed["record-004"].Set.Measurement.WeightKg != "65" {
		t.Fatal("edited removed group was not retained")
	}
}

func TestRecordingV2PreservesPositionsFromSequentialOfflineRemovals(t *testing.T) {
	original := recordingV2Fixture()
	removed, _, err := ApplyRecordingBatchV2(original, RecordingBatchV2{RemoveKeys: []string{"record-002", "record-003"},
		RemovalPositions: map[string]int{"record-002": 2, "record-003": 2}, Order: original.Order})
	if err != nil || removed.Removed["record-002"].RemovedPosition != 2 || removed.Removed["record-003"].RemovedPosition != 2 {
		t.Fatal("sequential removal position was lost")
	}
	if _, _, err := ApplyRecordingBatchV2(original, RecordingBatchV2{RemoveKeys: []string{"record-002"}, RemovalPositions: map[string]int{"record-003": 2}, Order: original.Order}); err == nil {
		t.Fatal("accepted removal position for an unrelated group")
	}
}
