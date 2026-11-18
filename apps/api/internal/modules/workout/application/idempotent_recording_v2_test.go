package application

import (
	"testing"

	"nerae/api/internal/idempotency"
)

func TestRecordingV2ReceiptValidatesExactTargetAndShape(t *testing.T) {
	valid := []byte(`{"id":"42","version":2}`)
	if err := validateRecordingV2Result(200, WorkoutResultRef(42), valid); err != nil {
		t.Fatal("valid receipt rejected")
	}
	cases := []struct {
		status int
		ref    string
		body   string
	}{
		{200, WorkoutResultRef(43), `{"id":"42","version":2}`},
		{200, WorkoutResultRef(42), `{"id":"42","version":0}`},
		{200, WorkoutResultRef(42), `{"id":"42","version":9007199254740992}`},
		{200, WorkoutResultRef(42), `{"id":"42","version":2,"bodyweight":70}`},
		{200, WorkoutResultRef(42), `{"id":"42","version":2,"id":"42"}`},
		{202, WorkoutResultRef(42), `{"id":"42","version":2}`},
		{200, NoWorkoutResultRef, `{"id":"42","version":2}`},
	}
	for i, c := range cases {
		if validateRecordingV2Result(c.status, c.ref, []byte(c.body)) == nil {
			t.Fatalf("invalid receipt %d accepted", i)
		}
	}
}
func TestRecordingV2ExistingRoutesAreBound(t *testing.T) {
	valid := idempotency.Request{Method: "POST", Path: "/me/recordings/42/sets"}
	if err := validateRecordingV2Route(42, "sets", valid); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		target    uint64
		operation string
		req       idempotency.Request
	}{
		{43, "sets", valid}, {42, "finish", valid}, {0, "sets", valid}, {42, "sets", idempotency.Request{Method: "GET", Path: valid.Path}},
		{42, "sets", idempotency.Request{Method: "POST", Path: valid.Path, RawQuery: "x=1"}},
	} {
		if validateRecordingV2Route(test.target, test.operation, test.req) == nil {
			t.Fatal("invalid route accepted")
		}
	}
}
