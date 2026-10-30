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

	api "nerae/api/internal/gen"
	"nerae/api/internal/middleware"
	"nerae/api/internal/modules/auth/session"
)

type ageDeclarationWriterFunc func(context.Context, *sql.DB, string, uint64, bool) (uint64, error)

func (f ageDeclarationWriterFunc) DeclareAdultAtVersion(ctx context.Context, db *sql.DB, token string, expected uint64, isAdult bool) (uint64, error) {
	return f(ctx, db, token, expected, isAdult)
}

type ageProfileFunc func(context.Context, *sql.DB, string) (session.ResponseProfile, error)

func (f ageProfileFunc) ProfileForResponse(ctx context.Context, db *sql.DB, token string) (session.ResponseProfile, error) {
	return f(ctx, db, token)
}

func newAgeVerifyTestHandler(t *testing.T, writer AgeDeclarationWriter, profiles EmailSessionProfiler) http.Handler {
	t.Helper()
	validator, err := middleware.NewSchemaValidator(16<<10, 2048)
	if err != nil {
		t.Fatal("could not initialize schema validator")
	}
	handler, err := NewAgeVerifyHandler(new(sql.DB), writer, profiles, validator)
	if err != nil {
		t.Fatal("could not initialize age declaration handler")
	}
	return handler
}

func ageRequest(handler http.Handler, method, target, body string, authorization ...string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "http://local"+target, strings.NewReader(body))
	if body != "" {
		r.Header.Set("Content-Type", "application/json")
	}
	for _, value := range authorization {
		r.Header.Add("Authorization", value)
	}
	r.Header.Set("X-User-ID", "999")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	return w
}

func TestAgeVerifyHandlerUsesBearerAndReturnsCurrentVersionedUser(t *testing.T) {
	const token = "abcdefghijklmnopqrstuvwxyz0123456789"
	serviceCalls, profileCalls := 0, 0
	var eventOrder []string
	writer := ageDeclarationWriterFunc(func(_ context.Context, db *sql.DB, got string, expected uint64, isAdult bool) (uint64, error) {
		serviceCalls++
		if db == nil || got != token || expected != 7 || !isAdult {
			t.Fatal("handler did not pass only the bearer-derived affirmative versioned declaration")
		}
		eventOrder = append(eventOrder, "declare")
		return 8, nil
	})
	profiles := ageProfileFunc(func(_ context.Context, db *sql.DB, got string) (session.ResponseProfile, error) {
		profileCalls++
		if db == nil || got != token || len(eventOrder) != 1 || eventOrder[0] != "declare" {
			t.Fatal("handler did not reauthorize after the declaration committed")
		}
		eventOrder = append(eventOrder, "profile")
		return session.ResponseProfile{SubjectID: "101", Nickname: "", Roles: []string{}, AgeVerified: true,
			AgeDeclarationVersion: 8, ExpiresAt: time.Now().Add(time.Hour)}, nil
	})
	handler := newAgeVerifyTestHandler(t, writer, profiles)
	w := ageRequest(handler, http.MethodPost, ageVerifyPath, `{"isAdult":true,"expectedVersion":7}`, "Bearer "+token)
	var user api.User
	if w.Code != http.StatusOK || w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("Content-Type") != "application/json; charset=utf-8" ||
		len(w.Header().Get("X-Request-ID")) != 32 || json.Unmarshal(w.Body.Bytes(), &user) != nil || user.Id != "101" || user.AgeVerified == nil || !*user.AgeVerified ||
		user.AgeDeclarationVersion == nil || *user.AgeDeclarationVersion != 8 || strings.Contains(w.Body.String(), token) || strings.Contains(w.Body.String(), "999") ||
		serviceCalls != 1 || profileCalls != 1 || strings.Join(eventOrder, ",") != "declare,profile" {
		t.Fatal("handler did not return the current committed User without caching or trusting a forged subject")
	}
}

func TestAgeVerifyHandlerRejectsMalformedRequestsBeforeService(t *testing.T) {
	calls := 0
	writer := ageDeclarationWriterFunc(func(context.Context, *sql.DB, string, uint64, bool) (uint64, error) {
		calls++
		return 2, nil
	})
	profiles := ageProfileFunc(func(context.Context, *sql.DB, string) (session.ResponseProfile, error) {
		t.Fatal("invalid request reached profile lookup")
		return session.ResponseProfile{}, session.ErrSessionUnavailable
	})
	handler := newAgeVerifyTestHandler(t, writer, profiles)
	valid := `{"isAdult":true,"expectedVersion":1}`
	for _, tc := range []struct {
		name, method, target, body string
		headers                    []string
	}{
		{"method", http.MethodGet, ageVerifyPath, valid, []string{"Bearer abcdefghijklmnopqrstuvwxyz0123456789"}},
		{"query", http.MethodPost, ageVerifyPath + "?version=1", valid, []string{"Bearer abcdefghijklmnopqrstuvwxyz0123456789"}},
		{"missing bearer", http.MethodPost, ageVerifyPath, valid, nil},
		{"duplicate bearer", http.MethodPost, ageVerifyPath, valid, []string{"Bearer abcdefghijklmnopqrstuvwxyz0123456789", "Bearer abcdefghijklmnopqrstuvwxyz0123456789"}},
		{"invalid JSON", http.MethodPost, ageVerifyPath, `{"isAdult":`, []string{"Bearer abcdefghijklmnopqrstuvwxyz0123456789"}},
		{"duplicate key", http.MethodPost, ageVerifyPath, `{"isAdult":true,"expectedVersion":1,"expectedVersion":1}`, []string{"Bearer abcdefghijklmnopqrstuvwxyz0123456789"}},
		{"unknown field", http.MethodPost, ageVerifyPath, `{"isAdult":true,"expectedVersion":1,"userId":"999"}`, []string{"Bearer abcdefghijklmnopqrstuvwxyz0123456789"}},
		{"trailing JSON", http.MethodPost, ageVerifyPath, valid + ` {}`, []string{"Bearer abcdefghijklmnopqrstuvwxyz0123456789"}},
		{"false declaration", http.MethodPost, ageVerifyPath, `{"isAdult":false,"expectedVersion":1}`, []string{"Bearer abcdefghijklmnopqrstuvwxyz0123456789"}},
		{"zero version", http.MethodPost, ageVerifyPath, `{"isAdult":true,"expectedVersion":0}`, []string{"Bearer abcdefghijklmnopqrstuvwxyz0123456789"}},
		{"fractional version", http.MethodPost, ageVerifyPath, `{"isAdult":true,"expectedVersion":1.5}`, []string{"Bearer abcdefghijklmnopqrstuvwxyz0123456789"}},
		{"unsafe version", http.MethodPost, ageVerifyPath, `{"isAdult":true,"expectedVersion":9007199254740992}`, []string{"Bearer abcdefghijklmnopqrstuvwxyz0123456789"}},
		{"overflow version", http.MethodPost, ageVerifyPath, `{"isAdult":true,"expectedVersion":9223372036854775808}`, []string{"Bearer abcdefghijklmnopqrstuvwxyz0123456789"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(tc.method, "http://local"+tc.target, strings.NewReader(tc.body))
			r.Header.Set("Content-Type", "application/json")
			for _, header := range tc.headers {
				r.Header.Add("Authorization", header)
			}
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			if w.Code < 400 || w.Code >= 500 || w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("X-Request-ID") == "" || calls != 0 {
				t.Fatalf("malformed request was not rejected before mutation: status=%d calls=%d", w.Code, calls)
			}
		})
	}
	missingType := httptest.NewRequest(http.MethodPost, "http://local"+ageVerifyPath, strings.NewReader(valid))
	missingType.Header.Set("Authorization", "Bearer abcdefghijklmnopqrstuvwxyz0123456789")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, missingType)
	if w.Code < 400 || w.Code >= 500 || calls != 0 {
		t.Fatal("missing content type was not rejected")
	}
}

func TestAgeVerifyHandlerMapsCASFailuresWithoutRetryOrLeak(t *testing.T) {
	const token = "abcdefghijklmnopqrstuvwxyz0123456789"
	for _, tc := range []struct {
		name string
		err  error
		want int
	}{
		{"invalid bearer session", session.ErrInvalidSession, http.StatusUnauthorized},
		{"stale expected version", session.ErrAgeDeclarationConflict, http.StatusPreconditionFailed},
		{"exhausted version", session.ErrAgeDeclarationVersionExhausted, http.StatusConflict},
		{"declined declaration", session.ErrAdultDeclarationRequired, http.StatusUnprocessableEntity},
		{"invalid version", session.ErrAgeDeclarationVersionInvalid, http.StatusUnprocessableEntity},
		{"ambiguous commit unavailable", fmt.Errorf("private database detail: %w", session.ErrSessionUnavailable), http.StatusServiceUnavailable},
		{"unknown storage error", errors.New("private connection string"), http.StatusServiceUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls, profileCalls := 0, 0
			handler := newAgeVerifyTestHandler(t,
				ageDeclarationWriterFunc(func(context.Context, *sql.DB, string, uint64, bool) (uint64, error) {
					calls++
					return 0, tc.err
				}),
				ageProfileFunc(func(context.Context, *sql.DB, string) (session.ResponseProfile, error) {
					profileCalls++
					return session.ResponseProfile{}, nil
				}))
			w := ageRequest(handler, http.MethodPost, ageVerifyPath, `{"isAdult":true,"expectedVersion":4}`, "Bearer "+token)
			if w.Code != tc.want || w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("X-Request-ID") == "" || calls != 1 || profileCalls != 0 ||
				strings.Contains(w.Body.String(), "private database detail") || strings.Contains(w.Body.String(), "connection string") || strings.Contains(w.Body.String(), token) {
				t.Fatal("declaration failure was retried, leaked or misclassified")
			}
		})
	}
	for _, tc := range []struct {
		name    string
		profile session.ResponseProfile
		err     error
		want    int
	}{
		{"concurrent version change", session.ResponseProfile{SubjectID: "101", AgeVerified: true, AgeDeclarationVersion: 9}, nil, http.StatusPreconditionFailed},
		{"withdrawal before reauthorization", session.ResponseProfile{SubjectID: "101", AgeVerified: false, AgeDeclarationVersion: 8}, nil, http.StatusPreconditionFailed},
		{"revoked session before reauthorization", session.ResponseProfile{}, session.ErrInvalidSession, http.StatusUnauthorized},
		{"profile storage unavailable", session.ResponseProfile{}, session.ErrSessionUnavailable, http.StatusServiceUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			profileCalls := 0
			handler := newAgeVerifyTestHandler(t,
				ageDeclarationWriterFunc(func(context.Context, *sql.DB, string, uint64, bool) (uint64, error) { return 8, nil }),
				ageProfileFunc(func(context.Context, *sql.DB, string) (session.ResponseProfile, error) {
					profileCalls++
					return tc.profile, tc.err
				}))
			w := ageRequest(handler, http.MethodPost, ageVerifyPath, `{"isAdult":true,"expectedVersion":7}`, "Bearer "+token)
			if w.Code != tc.want || w.Header().Get("Cache-Control") != "no-store" || profileCalls != 1 || strings.Contains(w.Body.String(), token) {
				t.Fatal("post-commit reauthorization or current declaration check failed")
			}
		})
	}
}
