// These tests compile against the models generated from the current OpenAPI.
// They check encoding and schema behavior, not mounted HTTP endpoints.
package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"nerae/api/internal/contractnullable"
)

func TestGeneratedWorkoutFinishOptionalNullableStates(t *testing.T) {
	fields := []struct{ name, value string }{
		{"durationMin", "30"}, {"rpe", "7.5"}, {"note", `"finished"`},
	}
	for _, field := range fields {
		for _, state := range []string{"omitted", "null", "value"} {
			t.Run(field.name+"/"+state, func(t *testing.T) {
				input := `{"expectedVersion":1}`
				if state == "null" {
					input = fmt.Sprintf(`{"expectedVersion":1,%q:null}`, field.name)
				} else if state == "value" {
					input = fmt.Sprintf(`{"expectedVersion":1,%q:%s}`, field.name, field.value)
				}
				var value WorkoutFinish
				if err := json.Unmarshal([]byte(input), &value); err != nil {
					t.Fatal(err)
				}
				var specified, isNull bool
				switch field.name {
				case "durationMin":
					specified, isNull = value.DurationMin.IsSpecified(), value.DurationMin.IsNull()
				case "rpe":
					specified, isNull = value.Rpe.IsSpecified(), value.Rpe.IsNull()
				case "note":
					specified, isNull = value.Note.IsSpecified(), value.Note.IsNull()
				}
				if specified != (state != "omitted") || isNull != (state == "null") {
					t.Fatalf("unexpected state specified=%v null=%v", specified, isNull)
				}
				output, err := json.Marshal(value)
				if err != nil {
					t.Fatal(err)
				}
				var decoded map[string]json.RawMessage
				if err := json.Unmarshal(output, &decoded); err != nil {
					t.Fatal(err)
				}
				raw, exists := decoded[field.name]
				if exists != (state != "omitted") {
					t.Fatalf("unexpected presence in %s", output)
				}
				if state == "null" && string(raw) != "null" || state == "value" && string(raw) != field.value {
					t.Fatalf("unexpected encoded value %s", output)
				}
			})
		}
	}
}

func TestAllRequiredNonReadOnlyNullableFieldsRejectUnspecified(t *testing.T) {
	// The names are cross-checked against the current nullable inventory.
	fields := []struct {
		model any
		field string
	}{
		{ExerciseList{}, "NextCursor"}, {CoachPlanList{}, "NextCursor"},
		{TrainingOccurrenceList{}, "NextCursor"}, {RelationMessageList{}, "NextCursor"},
		{PlanDetail{}, "PublishedVersion"}, {Relation{}, "StudentConfirmedAt"},
		{DataGrant{}, "GrantedAt"}, {DataGrant{}, "RevokedAt"},
		{PlanList{}, "NextCursor"}, {CommentList{}, "NextCursor"},
		{MessageList{}, "NextCursor"}, {ShareLink{}, "ExpiresAt"}, {AnomalyList{}, "NextCursor"},
	}
	for _, tc := range fields {
		t.Run(reflect.TypeOf(tc.model).Name()+"/"+tc.field, func(t *testing.T) {
			field, ok := reflect.TypeOf(tc.model).FieldByName(tc.field)
			if !ok || strings.Contains(field.Tag.Get("json"), "omitempty") {
				t.Fatalf("required nullable field missing or marked omitempty: %s", tc.field)
			}
			typ := reflect.StructOf([]reflect.StructField{{Name: "Value", Type: field.Type, Tag: field.Tag}})
			value := reflect.New(typ)
			if _, err := json.Marshal(value.Interface()); !errors.Is(err, contractnullable.ErrUnspecified) {
				t.Fatalf("unset required field should fail; got %v", err)
			}
			value.Elem().Field(0).Addr().MethodByName("SetNull").Call(nil)
			output, err := json.Marshal(value.Interface())
			if err != nil {
				t.Fatal(err)
			}
			var object map[string]json.RawMessage
			if err := json.Unmarshal(output, &object); err != nil {
				t.Fatal(err)
			}
			key := strings.Split(field.Tag.Get("json"), ",")[0]
			if string(object[key]) != "null" {
				t.Fatalf("explicit null was not preserved: %s", output)
			}
		})
	}
}

func TestActualCommentListCannotInventEmptyCursor(t *testing.T) {
	response := CommentList{Items: []RelationComment{}}
	if _, err := json.Marshal(response); !errors.Is(err, contractnullable.ErrUnspecified) {
		t.Fatalf("unset nextCursor must fail, got %v", err)
	}
	response.NextCursor.SetNull()
	output, err := json.Marshal(response)
	if err != nil || string(output) != `{"items":[],"nextCursor":null}` {
		t.Fatalf("explicit null cursor: %s, %v", output, err)
	}
	if err := validateSchema(t, "CommentList", output); err != nil {
		t.Fatalf("valid null cursor rejected: %v", err)
	}
}

func TestRequiredReadOnlyNullableNeedsResponseSchemaValidation(t *testing.T) {
	input := `{"id":"101","date":"2026-10-20","timeZone":"Asia/Shanghai","status":"skipped","source":"free","version":1,"createdAt":"2026-10-20T10:00:00Z","updatedAt":"2026-10-20T10:00:00Z","sets":[]}`
	var response WorkoutSessionDetail
	if err := json.Unmarshal([]byte(input), &response); err != nil {
		t.Fatal(err)
	}
	output, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(output), `"recordedAt"`) {
		t.Fatalf("expected generated omitempty behavior: %s", output)
	}
	if err := validateSchema(t, "WorkoutSessionDetail", output); err == nil {
		t.Fatalf("missing required read-only recordedAt must fail schema: %s", output)
	}
	response.RecordedAt.SetNull()
	output, err = json.Marshal(response)
	if err != nil || !strings.Contains(string(output), `"recordedAt":null`) {
		t.Fatalf("explicit null not preserved: %s, %v", output, err)
	}
	if err := validateSchema(t, "WorkoutSessionDetail", output); err != nil {
		t.Fatalf("valid skipped response rejected: %v; %s", err, output)
	}
}

func TestRequiredReadOnlyExerciseNameNeedsResponseSchemaValidation(t *testing.T) {
	input := `{"exerciseId":"101","exerciseEntryKey":"exercise-001","setIndex":1,"recordKey":"set-0001","kind":"bodyweight","reps":8}`
	var response WorkoutSetRecord
	if err := json.Unmarshal([]byte(input), &response); err != nil {
		t.Fatal(err)
	}
	output, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateSchema(t, "WorkoutSetRecord", output); err == nil {
		t.Fatalf("missing required resolvedExerciseName must fail schema: %s", output)
	}
	name := "俯卧撑"
	response.ResolvedExerciseName = &name
	output, err = json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateSchema(t, "WorkoutSetRecord", output); err != nil {
		t.Fatalf("valid read-only name rejected: %v; %s", err, output)
	}
}

func validateSchema(t *testing.T, schemaName string, input []byte) error {
	t.Helper()
	contract := os.Getenv("NERAE_OPENAPI")
	if contract == "" {
		t.Fatal("NERAE_OPENAPI must point to the contract under test")
	}
	doc, err := openapi3.NewLoader().LoadFromFile(contract)
	if err != nil {
		t.Fatal(err)
	}
	schema := doc.Components.Schemas[schemaName]
	if schema == nil || schema.Value == nil {
		t.Fatalf("missing schema %s", schemaName)
	}
	var value any
	if err := json.Unmarshal(input, &value); err != nil {
		t.Fatal(err)
	}
	return schema.Value.VisitJSON(value)
}
