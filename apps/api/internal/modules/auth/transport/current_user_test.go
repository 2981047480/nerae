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

func TestCurrentUserHandlerUsesOnlyAuthenticatedProfile(t *testing.T) {
	validator, err := middleware.NewSchemaValidator(16<<10, 2048)
	if err != nil {
		t.Fatal(err)
	}
	token := strings.Repeat("a", 32)
	called := 0
	profiler := loginProfileFunc(func(_ context.Context, _ *sql.DB, got string) (session.ResponseProfile, error) {
		called++
		if got != token {
			t.Fatal("handler did not use bearer token")
		}
		return session.ResponseProfile{SubjectID: "101", Nickname: "名称", Roles: []string{"student"}, AgeVerified: true, AgeDeclarationVersion: 3, ExpiresAt: time.Now().Add(time.Minute)}, nil
	})
	handler, err := NewCurrentUserHandler(new(sql.DB), profiler, validator)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodGet, "http://local/me", nil)
	r.Header.Set("Authorization", "Bearer "+token)
	r.Header.Set("X-User-ID", "999")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	var user api.User
	if w.Code != http.StatusOK || w.Header().Get("Cache-Control") != "no-store" || json.Unmarshal(w.Body.Bytes(), &user) != nil ||
		user.Id != "101" || user.Nickname != "名称" || len(user.Roles) != 1 || user.Roles[0] != api.UserRolesStudent || user.AgeVerified == nil || !*user.AgeVerified || user.AgeDeclarationVersion == nil || *user.AgeDeclarationVersion != 3 || called != 1 {
		t.Fatal("current-user response did not use authenticated profile")
	}
	if strings.Contains(w.Body.String(), token) || strings.Contains(w.Body.String(), "999") {
		t.Fatal("current-user response exposed bearer or trusted a client subject")
	}
	for _, tc := range []struct {
		name, method, path, body string
		authorizations           []string
	}{
		{"missing bearer", http.MethodGet, "/me", "", nil},
		{"duplicate bearer", http.MethodGet, "/me", "", []string{"Bearer " + token, "Bearer " + token}},
		{"bad bearer", http.MethodGet, "/me", "", []string{"Bearer not-a-valid-token"}},
		{"wrong method", http.MethodPost, "/me", "", []string{"Bearer " + token}},
		{"query", http.MethodGet, "/me?user=999", "", []string{"Bearer " + token}},
		{"body", http.MethodGet, "/me", "{}", []string{"Bearer " + token}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(tc.method, "http://local"+tc.path, strings.NewReader(tc.body))
			for _, value := range tc.authorizations {
				r.Header.Add("Authorization", value)
			}
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			if w.Code < 400 || w.Code >= 500 || w.Header().Get("Cache-Control") != "no-store" || called != 1 {
				t.Fatal("malformed request reached profile lookup")
			}
		})
	}
}

func TestResponseUserRejectsInvalidAgeVersions(t *testing.T) {
	for _, version := range []uint64{0, 1 << 53} {
		if _, err := responseUser(session.ResponseProfile{SubjectID: "101", AgeDeclarationVersion: version}); !errors.Is(err, session.ErrSessionUnavailable) {
			t.Fatal("invalid age declaration version must fail closed")
		}
	}
}

func TestCurrentUserHandlerKeepsUnavailableDistinctFromExpired(t *testing.T) {
	validator, err := middleware.NewSchemaValidator(16<<10, 2048)
	if err != nil {
		t.Fatal(err)
	}
	token := strings.Repeat("a", 32)
	for _, tc := range []struct {
		err  error
		want int
	}{
		{session.ErrInvalidSession, http.StatusUnauthorized},
		{fmt.Errorf("private-profile-data: %w", session.ErrSessionUnavailable), http.StatusServiceUnavailable},
	} {
		profiler := loginProfileFunc(func(context.Context, *sql.DB, string) (session.ResponseProfile, error) {
			return session.ResponseProfile{}, tc.err
		})
		handler, err := NewCurrentUserHandler(new(sql.DB), profiler, validator)
		if err != nil {
			t.Fatal(err)
		}
		r := httptest.NewRequest(http.MethodGet, "http://local/me", nil)
		r.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != tc.want || w.Header().Get("Cache-Control") != "no-store" || strings.Contains(w.Body.String(), "private-profile-data") || strings.Contains(w.Body.String(), token) {
			t.Fatal("invalid or unavailable session produced an unsafe response")
		}
	}
	if _, err := NewCurrentUserHandler(nil, nil, nil); !errors.Is(err, middleware.ErrContract) {
		t.Fatal("missing dependencies were accepted")
	}
}
