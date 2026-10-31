package transport

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"nerae/api/internal/middleware"
	"nerae/api/internal/modules/auth/session"
)

type sessionRevokerFunc func(context.Context, *sql.DB, string) error

func (f sessionRevokerFunc) RevokeCurrent(ctx context.Context, db *sql.DB, token string) error {
	return f(ctx, db, token)
}

func TestCurrentSessionDeleteHandler(t *testing.T) {
	validator, err := middleware.NewSchemaValidator(16<<10, 2048)
	if err != nil {
		t.Fatal(err)
	}
	const token = "synthetic-bearer-never-in-response"
	called := 0
	handler, err := NewCurrentSessionDeleteHandler(new(sql.DB), sessionRevokerFunc(func(_ context.Context, _ *sql.DB, got string) error {
		called++
		if got != token {
			t.Fatal("revoker did not receive the bearer")
		}
		return nil
	}), validator)
	if err != nil {
		t.Fatal("handler construction failed")
	}
	request := httptest.NewRequest(http.MethodDelete, "http://local/auth/sessions/current", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent || response.Body.Len() != 0 || response.Header().Get("Cache-Control") != "no-store" || called != 1 {
		t.Fatal("committed current-session revocation did not return empty no-store 204")
	}
	if strings.Contains(response.Body.String(), token) {
		t.Fatal("response exposed bearer")
	}

	for _, tc := range []struct {
		name, method, target, body string
		authorizations             []string
		result                     error
		want                       int
	}{
		{name: "missing bearer", method: http.MethodDelete, target: "/auth/sessions/current", want: http.StatusUnauthorized},
		{name: "duplicate bearer", method: http.MethodDelete, target: "/auth/sessions/current", authorizations: []string{"Bearer " + token, "Bearer " + token}, want: http.StatusUnauthorized},
		{name: "wrong method", method: http.MethodPost, target: "/auth/sessions/current", authorizations: []string{"Bearer " + token}, want: http.StatusBadRequest},
		{name: "query", method: http.MethodDelete, target: "/auth/sessions/current?x=1", authorizations: []string{"Bearer " + token}, want: http.StatusBadRequest},
		{name: "body", method: http.MethodDelete, target: "/auth/sessions/current", body: "{}", authorizations: []string{"Bearer " + token}, want: http.StatusBadRequest},
		{name: "expired or revoked", method: http.MethodDelete, target: "/auth/sessions/current", authorizations: []string{"Bearer " + token}, result: session.ErrInvalidSession, want: http.StatusUnauthorized},
		{name: "uncertain storage", method: http.MethodDelete, target: "/auth/sessions/current", authorizations: []string{"Bearer " + token}, result: errors.New("sensitive database detail"), want: http.StatusServiceUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := called
			local, err := NewCurrentSessionDeleteHandler(new(sql.DB), sessionRevokerFunc(func(_ context.Context, _ *sql.DB, got string) error {
				called++
				if got != token {
					t.Fatal("unexpected bearer")
				}
				return tc.result
			}), validator)
			if err != nil {
				t.Fatal("handler construction failed")
			}
			r := httptest.NewRequest(tc.method, "http://local"+tc.target, strings.NewReader(tc.body))
			for _, value := range tc.authorizations {
				r.Header.Add("Authorization", value)
			}
			w := httptest.NewRecorder()
			local.ServeHTTP(w, r)
			if w.Code != tc.want || w.Header().Get("Cache-Control") != "no-store" || strings.Contains(w.Body.String(), token) || strings.Contains(w.Body.String(), "sensitive database detail") {
				t.Fatal("request produced an unsafe status or response")
			}
			if len(tc.authorizations) != 1 || tc.method != http.MethodDelete || tc.target != "/auth/sessions/current" || tc.body != "" {
				if called != before {
					t.Fatal("invalid request reached revoker")
				}
			} else if called != before+1 {
				t.Fatal("validly-shaped request did not reach revoker")
			}
		})
	}
	if _, err := NewCurrentSessionDeleteHandler(nil, nil, nil); !errors.Is(err, middleware.ErrContract) {
		t.Fatal("missing dependencies accepted")
	}
}
