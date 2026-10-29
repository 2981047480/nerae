package contractnullable

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"
)

func TestNullablePreservesOptionalStates(t *testing.T) {
	type payload struct {
		Note Nullable[string] `json:"note,omitempty"`
	}
	for _, tc := range []struct {
		name      string
		input     string
		specified bool
		null      bool
		value     string
	}{
		{"omitted", `{}`, false, false, ""},
		{"null", `{"note":null}`, true, true, ""},
		{"value", `{"note":"done"}`, true, false, "done"},
		{"empty value", `{"note":""}`, true, false, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var got payload
			if err := json.Unmarshal([]byte(tc.input), &got); err != nil {
				t.Fatal(err)
			}
			if got.Note.IsSpecified() != tc.specified || got.Note.IsNull() != tc.null {
				t.Fatalf("unexpected state: %#v", got.Note)
			}
			if tc.specified && !tc.null {
				if value, err := got.Note.Get(); err != nil || value != tc.value {
					t.Fatalf("Get = %q, %v", value, err)
				}
			}
			out, err := json.Marshal(got)
			if err != nil || string(out) != tc.input {
				t.Fatalf("Marshal = %s, %v; want %s", out, err, tc.input)
			}
		})
	}
}

func TestRequiredNullableMustBeExplicit(t *testing.T) {
	type response struct {
		NextCursor Nullable[string] `json:"nextCursor"`
	}
	var got response
	if _, err := json.Marshal(got); !errors.Is(err, ErrUnspecified) {
		t.Fatalf("unset required field must fail, got %v", err)
	}
	got.NextCursor.SetNull()
	if out, err := json.Marshal(got); err != nil || string(out) != `{"nextCursor":null}` {
		t.Fatalf("explicit null: %s, %v", out, err)
	}
	got.NextCursor.Set("cursor-1")
	if out, err := json.Marshal(got); err != nil || string(out) != `{"nextCursor":"cursor-1"}` {
		t.Fatalf("explicit value: %s, %v", out, err)
	}
	got.NextCursor.SetUnspecified()
	if _, err := json.Marshal(got); !errors.Is(err, ErrUnspecified) {
		t.Fatalf("reset required field must fail, got %v", err)
	}
}

func TestGetNullUnspecifiedAndInvalidState(t *testing.T) {
	var value Nullable[int]
	if _, err := value.Get(); !errors.Is(err, ErrUnspecified) {
		t.Fatalf("unset Get: %v", err)
	}
	value.SetNull()
	if _, err := value.Get(); !errors.Is(err, ErrNull) {
		t.Fatalf("null Get: %v", err)
	}
	value = Nullable[int]{false: 0, true: 5}
	if _, err := value.Get(); !errors.Is(err, ErrInvalid) {
		t.Fatalf("conflicting Get: %v", err)
	}
	if _, err := json.Marshal(value); !errors.Is(err, ErrInvalid) {
		t.Fatalf("conflicting marshal: %v", err)
	}
}

func TestFailedDecodeDoesNotMutatePreviousValue(t *testing.T) {
	value := NewNullableWithValue(7)
	before := Nullable[int]{true: 7}
	if err := json.Unmarshal([]byte(`"bad"`), &value); err == nil {
		t.Fatal("invalid integer must fail")
	}
	if !reflect.DeepEqual(value, before) {
		t.Fatalf("value changed after failure: %#v", value)
	}
}
