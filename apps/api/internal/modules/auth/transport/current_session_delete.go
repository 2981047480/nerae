package transport

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"net/http"

	"nerae/api/internal/middleware"
	"nerae/api/internal/modules/auth/session"
)

type currentSessionRevoker interface {
	RevokeCurrent(ctx context.Context, db *sql.DB, token string) error
}

type currentSessionDeleteHandler struct {
	database  *sql.DB
	revoker   currentSessionRevoker
	validator *middleware.SchemaValidator
}

// NewCurrentSessionDeleteHandler builds an unmounted DELETE /auth/sessions/current
// handler. It never derives a subject or session ID from request fields.
func NewCurrentSessionDeleteHandler(db *sql.DB, revoker currentSessionRevoker, validator *middleware.SchemaValidator) (http.Handler, error) {
	if db == nil || nilAuthDependency(revoker) || validator == nil {
		return nil, middleware.ErrContract
	}
	return &currentSessionDeleteHandler{database: db, revoker: revoker, validator: validator}, nil
}

func (h *currentSessionDeleteHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
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
	if r.Method != http.MethodDelete || r.URL == nil || r.URL.Path != "/auth/sessions/current" || r.URL.RawPath != "" || r.URL.RawQuery != "" || r.URL.ForceQuery {
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
	if err := h.revoker.RevokeCurrent(r.Context(), h.database, token); err != nil {
		if errors.Is(err, session.ErrInvalidSession) {
			fail(middleware.Unauthenticated)
		} else {
			fail(middleware.Unavailable)
		}
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
