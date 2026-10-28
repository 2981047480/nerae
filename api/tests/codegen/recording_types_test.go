// Copy this file into the package containing the real Go generator output.
// These checks exercise generated models; they are not HTTP or database tests.
package api

import (
	"encoding/json"
	"testing"
)

func TestWorkoutFinishPreservesOmittedNullAndValue(t *testing.T) {
	cases := []struct {
		name      string
		input     string
		specified bool
		null      bool
		value     int
	}{
		{"omitted", `{"expectedVersion":1}`, false, false, 0},
		{"null", `{"expectedVersion":1,"durationMin":null}`, true, true, 0},
		{"value", `{"expectedVersion":1,"durationMin":30}`, true, false, 30},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var request WorkoutFinish
			if err := json.Unmarshal([]byte(tc.input), &request); err != nil {
				t.Fatal(err)
			}
			if request.DurationMin.IsSpecified() != tc.specified || request.DurationMin.IsNull() != tc.null {
				t.Fatalf("duration state: specified=%v null=%v", request.DurationMin.IsSpecified(), request.DurationMin.IsNull())
			}
			if tc.specified && !tc.null {
				if value, err := request.DurationMin.Get(); err != nil || value != tc.value {
					t.Fatalf("duration value: %d, %v", value, err)
				}
			}
			output, err := json.Marshal(request)
			if err != nil {
				t.Fatal(err)
			}
			var decoded map[string]json.RawMessage
			if err := json.Unmarshal(output, &decoded); err != nil {
				t.Fatal(err)
			}
			got, found := decoded["durationMin"]
			if found != tc.specified {
				t.Fatalf("duration presence: %v; JSON=%s", found, output)
			}
			if tc.null && string(got) != "null" {
				t.Fatalf("expected explicit null; JSON=%s", output)
			}
		})
	}
}

func TestGeneratedSetBatchCarriesTypedActualGroup(t *testing.T) {
	id, weight, reps := "101", float32(12.5), 8
	sets := []SetRecordWrite{{
		ExerciseId: &id, ExerciseEntryKey: "exercise-001", SetIndex: 1,
		RecordKey: "set-0001", Kind: SetRecordWriteKind("weight"),
		WeightKg: &weight, Reps: &reps,
	}}
	batch := SetBatchUpdate{ExpectedVersion: 1, Sets: &sets}
	payload, err := json.Marshal(batch)
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		ExpectedVersion int                          `json:"expectedVersion"`
		Sets            []map[string]json.RawMessage `json:"sets"`
	}
	if err := json.Unmarshal(payload, &got); err != nil {
		t.Fatal(err)
	}
	if got.ExpectedVersion != 1 || len(got.Sets) != 1 || string(got.Sets[0]["recordKey"]) != `"set-0001"` {
		t.Fatalf("unexpected batch: %s", payload)
	}
	if _, sent := got.Sets[0]["resolvedExerciseName"]; sent {
		t.Fatalf("server-owned name must not be sent: %s", payload)
	}
}
