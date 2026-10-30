// Exercise the same OpenAPI schema against the Go validator used by codegen.
// This is not a test of a mounted HTTP middleware or authorization.
package api

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
)

func TestRecordingSchemaWithGoValidator(t *testing.T) {
	contract := os.Getenv("NERAE_OPENAPI")
	if contract == "" {
		t.Fatal("NERAE_OPENAPI must point to the OpenAPI file under test")
	}
	doc, err := openapi3.NewLoader().LoadFromFile(contract)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name, schema, value string
		valid               bool
	}{
		{"valid weight", "SetRecordWrite", `{"exerciseId":"101","exerciseEntryKey":"exercise-001","setIndex":1,"recordKey":"set-0001","kind":"weight","weightKg":12.5,"reps":8}`, true},
		{"incompatible seconds", "SetRecordWrite", `{"exerciseId":"101","exerciseEntryKey":"exercise-001","setIndex":1,"recordKey":"set-0001","kind":"weight","weightKg":12.5,"reps":8,"seconds":30}`, false},
		{"two exercise sources", "SetRecordWrite", `{"exerciseId":"101","exerciseName":"custom","exerciseEntryKey":"exercise-001","setIndex":1,"recordKey":"set-0001","kind":"bodyweight","reps":8}`, false},
		{"forged server name", "SetRecordWrite", `{"exerciseId":"101","exerciseEntryKey":"exercise-001","setIndex":1,"recordKey":"set-0001","kind":"bodyweight","reps":8,"resolvedExerciseName":"fake"}`, false},
		{"empty batch", "SetBatchUpdate", `{"expectedVersion":1}`, false},
		{"valid deletion", "SetBatchUpdate", `{"expectedVersion":1,"exerciseEntryKeys":[],"deleteRecordKeys":["set-0001"]}`, true},
		{"valid ordering only", "SetBatchUpdate", `{"expectedVersion":1,"exerciseEntryKeys":["exercise-001"]}`, true},
		{"duplicate entry", "SetBatchUpdate", `{"expectedVersion":1,"exerciseEntryKeys":["exercise-001","exercise-001"]}`, false},
		{"null entry order", "SetBatchUpdate", `{"expectedVersion":1,"exerciseEntryKeys":null}`, false},
		{"valid empty detail", "WorkoutSessionDetail", `{"id":"101","date":"2026-10-20","timeZone":"Asia/Shanghai","status":"in_progress","source":"free","version":1,"createdAt":"2026-10-20T10:00:00Z","updatedAt":"2026-10-20T10:00:00Z","recordedAt":null,"clientEventId":"event-test-001","sets":[],"exerciseEntryKeys":[]}`, true},
		{"missing event identity", "WorkoutSessionDetail", `{"id":"101","date":"2026-10-20","timeZone":"Asia/Shanghai","status":"in_progress","source":"free","version":1,"createdAt":"2026-10-20T10:00:00Z","updatedAt":"2026-10-20T10:00:00Z","recordedAt":null,"sets":[],"exerciseEntryKeys":[]}`, false},
		{"completed without groups", "WorkoutSessionDetail", `{"id":"101","date":"2026-10-20","timeZone":"Asia/Shanghai","status":"completed","source":"free","version":1,"createdAt":"2026-10-20T10:00:00Z","updatedAt":"2026-10-20T10:00:00Z","recordedAt":"2026-10-20T10:00:00Z","clientEventId":"event-test-001","sets":[],"exerciseEntryKeys":[]}`, false},
		{"skipped with group", "WorkoutSessionDetail", `{"id":"101","date":"2026-10-20","timeZone":"Asia/Shanghai","status":"skipped","source":"free","version":1,"createdAt":"2026-10-20T10:00:00Z","updatedAt":"2026-10-20T10:00:00Z","recordedAt":null,"clientEventId":"event-test-001","exerciseEntryKeys":["exercise-001"],"sets":[{"exerciseId":"101","exerciseEntryKey":"exercise-001","setIndex":1,"recordKey":"set-0001","kind":"bodyweight","reps":8,"resolvedExerciseName":"俯卧撑"}]}`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var value any
			if err := json.Unmarshal([]byte(tc.value), &value); err != nil {
				t.Fatal(err)
			}
			schema := doc.Components.Schemas[tc.schema]
			if schema == nil || schema.Value == nil {
				t.Fatalf("missing schema %s", tc.schema)
			}
			err := schema.Value.VisitJSON(value)
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v, error=%v", tc.valid, err)
			}
		})
	}
}
