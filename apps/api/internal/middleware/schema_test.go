package middleware

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	gen "nerae/api/internal/gen"
)

const validBatch = `{"expectedVersion":1,"exerciseEntryKeys":["entry-0001"],"sets":[{"exerciseEntryKey":"entry-0001","recordKey":"record-001","exerciseName":"深蹲","setIndex":1,"kind":"weight","weightKg":50.5,"reps":8}]}`
const validDetail = `{"id":"101","date":"2026-10-20","timeZone":"Asia/Shanghai","status":"in_progress","source":"free","version":1,"createdAt":"2026-10-20T10:00:00Z","updatedAt":"2026-10-20T10:00:00Z","recordedAt":null,"sets":[],"exerciseEntryKeys":[]}`

func validator(t *testing.T, limit int64) *SchemaValidator {
	t.Helper()
	v, err := NewSchemaValidator(limit, 10_000)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func request(body string) *http.Request {
	r := httptest.NewRequest("POST", "/me/sessions/101/sets", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json; charset=UTF-8")
	return r
}

func TestRequestSchemaBoundary(t *testing.T) {
	v := validator(t, 4096)
	got, err := v.ReadRequest(request(validBatch), "SetBatchUpdate")
	if err != nil || string(got) != validBatch {
		t.Fatalf("valid request changed or rejected: %v", err)
	}
	cases := map[string]string{
		"missing order":         `{"expectedVersion":1}`,
		"duplicate order":       `{"expectedVersion":1,"exerciseEntryKeys":["entry-0001","entry-0001"]}`,
		"unknown property":      `{"expectedVersion":1,"exerciseEntryKeys":[],"private":"secret-token"}`,
		"forged read-only name": `{"expectedVersion":1,"exerciseEntryKeys":["entry-0001"],"sets":[{"exerciseEntryKey":"entry-0001","recordKey":"record-001","exerciseId":"101","setIndex":1,"kind":"bodyweight","reps":8,"resolvedExerciseName":"secret-token"}]}`,
		"illegal measurements":  `{"expectedVersion":1,"exerciseEntryKeys":["entry-0001"],"sets":[{"exerciseEntryKey":"entry-0001","recordKey":"record-001","exerciseId":"101","setIndex":1,"kind":"weight","weightKg":5,"reps":8,"seconds":30}]}`,
		"nested duplicate":      `{"expectedVersion":1,"exerciseEntryKeys":[],"sets":[{"recordKey":"record-001","recordKey":"record-002"}]}`,
		"escaped duplicate":     `{"expectedVersion":1,"\u0065xpectedVersion":2,"exerciseEntryKeys":[]}`,
		"multiple values":       `{"expectedVersion":1,"exerciseEntryKeys":[]} {}`,
		"malformed":             `{"expectedVersion":`,
		"invalid utf8":          string([]byte{'{', '"', 0xff, '"', ':', '1', '}'}),
		"too deep":              strings.Repeat("[", maxJSONDepth+1) + strings.Repeat("]", maxJSONDepth+1),
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := v.ReadRequest(request(body), "SetBatchUpdate")
			if !errors.Is(err, ErrInvalidRequest) || strings.Contains(fmt.Sprint(err), "secret-token") {
				t.Fatalf("expected sanitized invalid request, got %v", err)
			}
		})
	}
}

func TestRequestContentTypeAndSize(t *testing.T) {
	v := validator(t, int64(len(validBatch)))
	if _, err := v.ReadRequest(request(validBatch), "SetBatchUpdate"); err != nil {
		t.Fatalf("exact limit rejected: %v", err)
	}
	long := request(validBatch + " ")
	long.ContentLength = -1 // chunked/unknown length must also be bounded.
	if _, err := v.ReadRequest(long, "SetBatchUpdate"); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("oversize request accepted: %v", err)
	}
	for _, contentType := range []string{"", "text/plain", "application/json; charset=latin1"} {
		r := request(validBatch)
		r.Header.Set("Content-Type", contentType)
		if _, err := v.ReadRequest(r, "SetBatchUpdate"); !errors.Is(err, ErrInvalidRequest) {
			t.Fatalf("invalid content type accepted: %q", contentType)
		}
	}
	r := request(validBatch)
	r.Header.Add("Content-Type", "application/json")
	if _, err := v.ReadRequest(r, "SetBatchUpdate"); !errors.Is(err, ErrInvalidRequest) {
		t.Fatal("multiple content types accepted")
	}
	r = request(validBatch)
	r.Body = io.NopCloser(&failingReader{})
	if _, err := v.ReadRequest(r, "SetBatchUpdate"); !errors.Is(err, ErrInvalidRequest) {
		t.Fatal("body read error accepted")
	}
}

type failingReader struct{}

func (*failingReader) Read([]byte) (int, error) { return 0, errors.New("secret-token") }

type brokenJSON struct{}

func (brokenJSON) MarshalJSON() ([]byte, error) { return nil, errors.New("secret-token") }

func TestResponseValidationAfterEncoding(t *testing.T) {
	v := validator(t, 4096)
	var valid map[string]any
	if err := json.Unmarshal([]byte(validDetail), &valid); err != nil {
		t.Fatal(err)
	}
	if _, err := v.EncodeResponse("WorkoutSessionDetail", valid); err != nil {
		t.Fatalf("valid response rejected: %v", err)
	}
	for _, field := range []string{"createdAt", "updatedAt", "recordedAt", "exerciseEntryKeys"} {
		copy := make(map[string]any)
		for key, value := range valid {
			copy[key] = value
		}
		delete(copy, field)
		if _, err := v.EncodeResponse("WorkoutSessionDetail", copy); !errors.Is(err, ErrInvalidResponse) {
			t.Fatalf("missing %s accepted: %v", field, err)
		}
	}
	if _, err := v.EncodeResponse("WorkoutSessionDetail", gen.WorkoutSessionDetail{}); !errors.Is(err, ErrInvalidResponse) {
		t.Fatalf("generated zero value accepted: %v", err)
	}
	if _, err := v.EncodeResponse("WorkoutSessionDetail", brokenJSON{}); !errors.Is(err, ErrInvalidResponse) || strings.Contains(fmt.Sprint(err), "secret-token") {
		t.Fatalf("marshal error leaked: %v", err)
	}
	if _, err := v.EncodeResponse("no-such-schema", valid); !errors.Is(err, ErrContract) {
		t.Fatalf("missing schema not treated as configuration error: %v", err)
	}
	if _, err := v.ReadRequest(request(validBatch), "no-such-schema"); !errors.Is(err, ErrContract) {
		t.Fatalf("missing request schema not treated as configuration error: %v", err)
	}
}

func TestConcurrentValidation(t *testing.T) {
	v := validator(t, 4096)
	var wg sync.WaitGroup
	errs := make(chan error, 24)
	for i := 0; i < cap(errs); i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			body, err := v.ReadRequest(request(validBatch), "SetBatchUpdate")
			if err == nil && !bytes.Equal(body, []byte(validBatch)) {
				err = errors.New("body changed")
			}
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestNodeBudgetRejectsDenseJSONBeforeSchemaValidation(t *testing.T) {
	v, err := NewSchemaValidator(4096, 12)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := v.ReadRequest(request(validBatch), "SetBatchUpdate"); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("node budget did not reject dense input: %v", err)
	}
	if _, err := strictJSON([]byte(`{"a":[1,2]}`), 4); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("node and key budget was not enforced: %v", err)
	}
	if _, err := strictJSON([]byte(`{"a":[1,2]}`), 5); err != nil {
		t.Fatalf("exact node budget rejected: %v", err)
	}
	for _, n := range []int{0, maxNodeLimit + 1} {
		if _, err := NewSchemaValidator(4096, n); !errors.Is(err, ErrContract) {
			t.Fatal("invalid budget accepted")
		}
	}
}

func TestSchemaValidationRetainsRawDecimalsForRequiredDomainCheck(t *testing.T) {
	// Schema bounds alone do not enforce the contract's decimal precision:
	// kin-openapi converts json.Number to float64. A real handler must check
	// raw measurements before conversion and must not equate this with approval.
	v := validator(t, 4096)
	body := strings.Replace(validBatch, "50.5", "50.0000000000000001", 1)
	got, err := v.ReadRequest(request(body), "SetBatchUpdate")
	if err != nil || string(got) != body {
		t.Fatalf("raw body was lost: %v", err)
	}
}
