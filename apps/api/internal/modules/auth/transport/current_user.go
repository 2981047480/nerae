package transport

import (
	"database/sql"
	"errors"
	"io"
	"net/http"
	"time"

	"nerae/api/internal/middleware"
	"nerae/api/internal/modules/auth/session"
)

type currentUserHandler struct {
	database  *sql.DB
	profiles  EmailSessionProfiler
	validator *middleware.SchemaValidator
	now       func() time.Time
}

// NewCurrentUserHandler builds an unmounted session verification endpoint.
// It derives the subject exclusively from the signed bearer and live store.
func NewCurrentUserHandler(db *sql.DB, profiles EmailSessionProfiler, validator *middleware.SchemaValidator) (http.Handler, error) {
	if db == nil || nilAuthDependency(profiles) || validator == nil {
		return nil, middleware.ErrContract
	}
	return &currentUserHandler{database: db, profiles: profiles, validator: validator, now: time.Now}, nil
}

func (h *currentUserHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if w == nil {
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	if r == nil {
		return
	}
	id, err := middleware.NewRequestID()
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	w.Header().Set("X-Request-ID", id.String())
	fail := func(kind middleware.ErrorKind) {
		if err := middleware.WriteError(w, h.validator, id, kind); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
		}
	}
	if r.Method != http.MethodGet || r.URL == nil || r.URL.Path != "/me" || r.URL.RawPath != "" || r.URL.RawQuery != "" || r.URL.ForceQuery {
		fail(middleware.BadRequest)
		return
	}
	if r.Body != nil {
		body, err := io.ReadAll(io.LimitReader(r.Body, 1))
		if err != nil || len(body) != 0 {
			fail(middleware.BadRequest)
			return
		}
	}
	token, err := middleware.BearerToken(r.Header)
	if err != nil {
		fail(middleware.Unauthenticated)
		return
	}
	profile, err := h.profiles.ProfileForResponse(r.Context(), h.database, token)
	if err != nil {
		if errors.Is(err, session.ErrInvalidSession) {
			fail(middleware.Unauthenticated)
		} else {
			fail(middleware.Unavailable)
		}
		return
	}
	user, err := responseUser(profile)
	if err != nil {
		fail(middleware.Unavailable)
		return
	}
	encoded, err := h.validator.EncodeResponse("User", user)
	if err != nil || len(encoded) == 0 {
		fail(middleware.Unavailable)
		return
	}
	now := h.now().UTC()
	if now.IsZero() || profile.ExpiresAt.IsZero() {
		fail(middleware.Unavailable)
		return
	}
	if !profile.ExpiresAt.After(now) {
		fail(middleware.Unauthenticated)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(encoded)
}
