package repository

import (
	"bytes"
	"reflect"
	"testing"

	"nerae/api/internal/modules/workout/domain"
)

func testString(value string) *string { return &value }
func testInt(value int) *int          { return &value }
func recordingV2CodecFixture() domain.RecordingV2 {
	exercise := domain.Exercise{Source: domain.Custom, Name: "深蹲"}
	set := domain.SetV2{RecordKey: "record-001", ExerciseEntryKey: "entry-0001", Exercise: exercise, Index: 1, Confirmed: true,
		Measurement: domain.MeasurementV2{Method: domain.MeasurementBodyweightExtra, BodyweightKg: testString("70"), AdjustmentKg: testString("10"), Reps: testInt(10)}}
	blank := domain.SetV2{RecordKey: "record-002", ExerciseEntryKey: "entry-0001", Exercise: exercise, Index: 2,
		Measurement: domain.MeasurementV2{Method: domain.MeasurementTotal}}
	removed := domain.SetV2{RecordKey: "record-003", ExerciseEntryKey: "entry-0001", Exercise: exercise, Index: 3,
		Measurement: domain.MeasurementV2{Method: domain.MeasurementSides,
			Left:  &domain.SideMeasurementV2{State: domain.SidePerformed, WeightKg: testString("12"), Reps: testInt(10)},
			Right: &domain.SideMeasurementV2{State: domain.SideNotPerformed}}}
	return domain.RecordingV2{Status: domain.InProgress, Mode: domain.RecordingPerSetV2,
		Entries: map[string]domain.Exercise{"entry-0001": exercise}, Order: []string{"entry-0001"},
		Sets:    map[string]domain.SetV2{"record-002": blank, "record-001": set},
		Removed: map[string]domain.RemovedSetV2{"record-003": {Set: removed, RemovedPosition: 3}}, RemovedEntries: map[string]int{}}
}

func TestRecordingV2SnapshotRoundTripAndDeterminism(t *testing.T) {
	value := recordingV2CodecFixture()
	first, err := encodeRecordingV2(value)
	if err != nil {
		t.Fatal("failed to encode valid recording")
	}
	second, err := encodeRecordingV2(value)
	if err != nil || !bytes.Equal(first, second) {
		t.Fatal("snapshot encoding is not stable")
	}
	restored, err := decodeRecordingV2(first)
	if err != nil || !reflect.DeepEqual(restored, value) {
		t.Fatalf("roundtrip lost recording data: %v", err)
	}
	*restored.Sets["record-001"].Measurement.BodyweightKg = "72"
	if *value.Sets["record-001"].Measurement.BodyweightKg != "70" {
		t.Fatal("restored data aliases the source")
	}
}

func TestRecordingV2SnapshotPreservesRemovedAction(t *testing.T) {
	value := recordingV2CodecFixture()
	value.RemovedEntries["entry-0001"] = 1
	raw, err := encodeRecordingV2(value)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := decodeRecordingV2(raw)
	if err != nil || !reflect.DeepEqual(value, restored) {
		t.Fatalf("removed action did not survive storage: %v", err)
	}
}

func TestRecordingV2SnapshotKeepsPersonalIdentityAndFrozenName(t *testing.T) {
	value := recordingV2CodecFixture()
	personal := domain.Exercise{Source: domain.Personal, ID: "97ab0210c56f48ae930de1629b761f09", Name: "旧名称", Version: 1}
	value.Entries["entry-0001"] = personal
	for key, set := range value.Sets {
		set.Exercise = personal
		value.Sets[key] = set
	}
	for key, removed := range value.Removed {
		removed.Set.Exercise = personal
		value.Removed[key] = removed
	}
	raw, err := encodeRecordingV2(value)
	if err != nil || !bytes.Contains(raw, []byte(`"schema":3`)) {
		t.Fatal("personal identity must use a protected new schema", err)
	}
	restored, err := decodeRecordingV2(raw)
	if err != nil || !reflect.DeepEqual(value, restored) {
		t.Fatal("personal identity or historical name was lost", err)
	}
	for _, corrupt := range [][]byte{
		bytes.Replace(raw, []byte(`"schema":3`), []byte(`"schema":2`), 1),
		bytes.Replace(raw, []byte(`"version":1`), []byte(`"version":0`), 1),
		bytes.Replace(raw, []byte(`"schema":3`), []byte(`"schema":4`), 1),
	} {
		if _, err := decodeRecordingV2(corrupt); err == nil {
			t.Fatal("accepted unsupported or incomplete identity snapshot")
		}
	}
}

func TestRecordingV2SnapshotRejectsMalformedOrFutureVersions(t *testing.T) {
	original, err := encodeRecordingV2(recordingV2CodecFixture())
	if err != nil {
		t.Fatal(err)
	}
	cases := [][]byte{
		bytes.Replace(original, []byte(`"schema":2`), []byte(`"schema":3`), 1),
		bytes.Replace(original, []byte(`"schema":2`), []byte(`"schema":2,"schema":2`), 1),
		bytes.Replace(original, []byte(`"schema":2`), []byte(`"schema":2,"unexpected":true`), 1),
		bytes.Replace(original, []byte(`"entryKey":"entry-0001"`), []byte(`"entryKey":"entry-9999"`), 1),
		append(append([]byte{}, original...), []byte(` {}`)...),
		[]byte(`{"schema":2}`),
	}
	for i, value := range cases {
		if _, err := decodeRecordingV2(value); err == nil {
			t.Fatalf("accepted malformed snapshot %d", i)
		}
	}
	bad := recordingV2CodecFixture()
	bad.Order = []string{}
	if _, err := encodeRecordingV2(bad); err == nil {
		t.Fatal("accepted invalid action order")
	}
}
