package transport

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"nerae/api/internal/middleware"
	"nerae/api/internal/modules/auth/approval"
	"nerae/api/internal/modules/auth/session"
)

type listPendingFunc func(context.Context, *sql.DB, *session.Store, string) (approval.PendingList, error)

func (f listPendingFunc) ListPending(ctx context.Context, db *sql.DB, sessions *session.Store, token string) (approval.PendingList, error) {
	return f(ctx, db, sessions, token)
}

func TestApprovalListHandlerAuthenticatedAndSchemaChecked(t *testing.T) {
	validator, err := middleware.NewSchemaValidator(16<<10, 2048)
	if err != nil {
		t.Fatal(err)
	}
	token := strings.Repeat("a", 32)
	db, sessions := new(sql.DB), new(session.Store)
	calls, profiles := 0, 0
	list := approval.PendingList{Items: []approval.PendingListItem{{ID: strings.Repeat("A", 24), ComparisonCode: "123456", CreatedAt: time.Now().Add(-time.Minute).UTC(), ExpiresAt: time.Now().Add(time.Minute).UTC()}}}
	lister := listPendingFunc(func(_ context.Context, gotDB *sql.DB, gotSessions *session.Store, got string) (approval.PendingList, error) {
		calls++
		if gotDB != db || gotSessions != sessions || got != token {
			t.Fatal("handler did not use server dependencies and the bearer")
		}
		return list, nil
	})
	profiler := loginProfileFunc(func(_ context.Context, gotDB *sql.DB, got string) (session.ResponseProfile, error) {
		profiles++
		if gotDB != db || got != token {
			t.Fatal("response recheck did not use the same bearer")
		}
		return session.ResponseProfile{SubjectID: "101", ExpiresAt: time.Now().Add(time.Minute)}, nil
	})
	handler, err := NewApprovalListHandler(db, lister, sessions, profiler, validator)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "http://local/me/login-approvals", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("X-User-ID", "999")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, request)
	var body struct {
		Items []map[string]any `json:"items"`
	}
	if w.Code != http.StatusOK || w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("X-Request-ID") == "" ||
		json.Unmarshal(w.Body.Bytes(), &body) != nil || len(body.Items) != 1 || body.Items[0]["comparisonCode"] != "123456" ||
		len(body.Items[0]) != 4 || calls != 1 || profiles != 1 {
		t.Fatal("authenticated list was not released under the response contract")
	}
	if strings.Contains(w.Body.String(), token) || strings.Contains(w.Body.String(), "999") {
		t.Fatal("response leaked bearer or trusted caller-supplied subject")
	}
	list.Items = []approval.PendingListItem{}
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, request)
	if w.Code != http.StatusOK || strings.TrimSpace(w.Body.String()) != `{"items":[]}` {
		t.Fatal("empty list did not produce a contract-valid array")
	}
	for _, tc := range []struct {
		name, method, path, body string
		authorizations           []string
	}{
		{"missing bearer", http.MethodGet, approvalListPath, "", nil},
		{"duplicate bearer", http.MethodGet, approvalListPath, "", []string{"Bearer " + token, "Bearer " + token}},
		{"bad bearer", http.MethodGet, approvalListPath, "", []string{"Bearer invalid"}},
		{"wrong method", http.MethodPost, approvalListPath, "", []string{"Bearer " + token}},
		{"query", http.MethodGet, approvalListPath + "?user=999", "", []string{"Bearer " + token}},
		{"body", http.MethodGet, approvalListPath, "{}", []string{"Bearer " + token}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(tc.method, "http://local"+tc.path, strings.NewReader(tc.body))
			for _, value := range tc.authorizations {
				r.Header.Add("Authorization", value)
			}
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			if w.Code < 400 || w.Code >= 500 || w.Header().Get("Cache-Control") != "no-store" || calls != 2 || profiles != 2 {
				t.Fatal("malformed request reached the lister or response verifier")
			}
		})
	}
}

func TestApprovalListHandlerFailsClosed(t *testing.T) {
	validator, err := middleware.NewSchemaValidator(16<<10, 2048)
	if err != nil {
		t.Fatal(err)
	}
	token := strings.Repeat("b", 32)
	for _, tc := range []struct {
		name                string
		list                approval.PendingList
		listErr, profileErr error
		expiry              time.Time
		want                int
	}{
		{"revoked list", approval.PendingList{}, session.ErrInvalidSession, nil, time.Time{}, http.StatusUnauthorized},
		{"list unavailable", approval.PendingList{}, fmt.Errorf("sensitive-error: %w", approval.ErrUnavailable), nil, time.Time{}, http.StatusServiceUnavailable},
		{"malformed list", approval.PendingList{Items: nil}, nil, nil, time.Now().Add(time.Minute), http.StatusServiceUnavailable},
		{"revoked at release", approval.PendingList{Items: []approval.PendingListItem{}}, nil, session.ErrInvalidSession, time.Time{}, http.StatusUnauthorized},
		{"release unavailable", approval.PendingList{Items: []approval.PendingListItem{}}, nil, fmt.Errorf("sensitive-error: %w", session.ErrSessionUnavailable), time.Time{}, http.StatusServiceUnavailable},
		{"expired at release", approval.PendingList{Items: []approval.PendingListItem{}}, nil, nil, time.Now().Add(-time.Second), http.StatusUnauthorized},
	} {
		t.Run(tc.name, func(t *testing.T) {
			lister := listPendingFunc(func(context.Context, *sql.DB, *session.Store, string) (approval.PendingList, error) {
				return tc.list, tc.listErr
			})
			profiler := loginProfileFunc(func(context.Context, *sql.DB, string) (session.ResponseProfile, error) {
				return session.ResponseProfile{SubjectID: "101", ExpiresAt: tc.expiry}, tc.profileErr
			})
			handler, err := NewApprovalListHandler(new(sql.DB), lister, new(session.Store), profiler, validator)
			if err != nil {
				t.Fatal(err)
			}
			r := httptest.NewRequest(http.MethodGet, "http://local"+approvalListPath, nil)
			r.Header.Set("Authorization", "Bearer "+token)
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			if w.Code != tc.want || w.Header().Get("Cache-Control") != "no-store" || strings.Contains(w.Body.String(), token) || strings.Contains(w.Body.String(), "sensitive-error") {
				t.Fatalf("unexpected status or disclosure: %d", w.Code)
			}
		})
	}
	if _, err := NewApprovalListHandler(nil, nil, nil, nil, nil); !errors.Is(err, middleware.ErrContract) {
		t.Fatal("missing dependencies were accepted")
	}
}

func TestApprovalListHandlerDoesNotReleaseItemExpiredDuringRecheck(t *testing.T) {
	validator, err := middleware.NewSchemaValidator(16<<10, 2048)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 10, 31, 1, 0, 0, 0, time.UTC)
	list := approval.PendingList{Items: []approval.PendingListItem{{ID: strings.Repeat("C", 24), ComparisonCode: "123456", CreatedAt: start, ExpiresAt: start.Add(time.Second)}}}
	lister := listPendingFunc(func(context.Context, *sql.DB, *session.Store, string) (approval.PendingList, error) { return list, nil })
	profiler := loginProfileFunc(func(context.Context, *sql.DB, string) (session.ResponseProfile, error) {
		return session.ResponseProfile{SubjectID: "101", ExpiresAt: start.Add(time.Minute)}, nil
	})
	handler, err := NewApprovalListHandler(new(sql.DB), lister, new(session.Store), profiler, validator)
	if err != nil {
		t.Fatal(err)
	}
	handler.(*approvalListHandler).now = func() time.Time { return start.Add(2 * time.Second) }
	r := httptest.NewRequest(http.MethodGet, "http://local"+approvalListPath, nil)
	r.Header.Set("Authorization", "Bearer "+strings.Repeat("b", 32))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != http.StatusServiceUnavailable || strings.Contains(w.Body.String(), "123456") || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("expired approval was released after the profile recheck")
	}
}
