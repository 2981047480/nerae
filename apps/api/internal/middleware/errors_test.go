package middleware

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	gen "nerae/api/internal/gen"
)

func TestErrorResponsesUseSafeMessagesAndRequestID(t *testing.T) {
	v := validator(t, 4096)
	id, err := NewRequestID()
	if err != nil || !regexp.MustCompile(`^[0-9a-f]{32}$`).MatchString(id.String()) {
		t.Fatalf("invalid request ID: %v", err)
	}
	cases := []struct {
		kind   ErrorKind
		status int
		code   string
	}{
		{BadRequest, 400, "BAD_REQUEST"}, {Unauthenticated, 401, "UNAUTHENTICATED"},
		{Forbidden, 403, "FORBIDDEN"}, {NotFound, 404, "NOT_FOUND"},
		{Conflict, 409, "CONFLICT"}, {VersionConflict, 412, "VERSION_CONFLICT"},
		{ValidationError, 422, "VALIDATION_ERROR"}, {RateLimited, 429, "RATE_LIMITED"},
		{Unavailable, 503, "SERVICE_UNAVAILABLE"}, {InternalError, 500, "INTERNAL_ERROR"},
		{ErrorKind(255), 500, "INTERNAL_ERROR"},
	}
	for _, tc := range cases {
		t.Run(tc.code, func(t *testing.T) {
			w := httptest.NewRecorder()
			if err := WriteError(w, v, id, tc.kind); err != nil {
				t.Fatal(err)
			}
			if w.Code != tc.status || w.Header().Get("Cache-Control") != "no-store" ||
				w.Header().Get("X-Request-ID") != id.String() || w.Header().Get("Content-Type") != "application/json; charset=utf-8" {
				t.Fatalf("wrong status or headers: %d", w.Code)
			}
			var body gen.Error
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if body.Code != tc.code || body.RequestId != id.String() || body.Message == "" || body.Details != nil {
				t.Fatalf("wrong error body: %+v", body)
			}
		})
	}
}

type failingWriter struct {
	header http.Header
	status int
}

func (w *failingWriter) Header() http.Header     { return w.header }
func (w *failingWriter) WriteHeader(status int)  { w.status = status }
func (*failingWriter) Write([]byte) (int, error) { return 0, errors.New("secret-token") }

func TestErrorResponseFailsClosedWithoutLeakingWriterErrors(t *testing.T) {
	v := validator(t, 4096)
	id, err := NewRequestID()
	if err != nil {
		t.Fatal(err)
	}
	writer := &failingWriter{header: make(http.Header)}
	err = WriteError(writer, v, id, InternalError)
	if !errors.Is(err, ErrResponseWrite) || strings.Contains(err.Error(), "secret-token") {
		t.Fatalf("unsafe error: %v", err)
	}
	if writer.status != 500 {
		t.Fatal("expected status before writer failure")
	}
	for _, invalid := range []struct {
		w  http.ResponseWriter
		v  *SchemaValidator
		id RequestID
	}{
		{nil, v, id}, {httptest.NewRecorder(), nil, id}, {httptest.NewRecorder(), v, RequestID{}},
	} {
		if err := WriteError(invalid.w, invalid.v, invalid.id, BadRequest); !errors.Is(err, ErrResponseWrite) {
			t.Fatal("invalid state accepted")
		}
	}
	tooSmall := validator(t, 1)
	w := httptest.NewRecorder()
	if err := WriteError(w, tooSmall, id, BadRequest); !errors.Is(err, ErrResponseWrite) || w.Body.Len() != 0 {
		t.Fatal("unencodable response was sent")
	}
}

type shortWriter struct{ header http.Header }

func (w *shortWriter) Header() http.Header          { return w.header }
func (*shortWriter) WriteHeader(int)                {}
func (*shortWriter) Write(body []byte) (int, error) { return len(body) - 1, nil }

func TestShortErrorWriteIsNotReportedAsSuccess(t *testing.T) {
	id, err := NewRequestID()
	if err != nil {
		t.Fatal(err)
	}
	err = WriteError(&shortWriter{header: make(http.Header)}, validator(t, 4096), id, BadRequest)
	if !errors.Is(err, ErrResponseWrite) {
		t.Fatalf("short write accepted: %v", err)
	}
}
