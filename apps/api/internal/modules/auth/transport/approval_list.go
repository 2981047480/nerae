package transport

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"net/http"
	"time"

	"nerae/api/internal/middleware"
	"nerae/api/internal/modules/auth/approval"
	"nerae/api/internal/modules/auth/session"
)

const approvalListPath = "/me/login-approvals"

// PendingApprovalLister authenticates the bearer and releases its list only
// after its own transaction commits. Callers must not supply a subject ID.
type PendingApprovalLister interface {
	ListPending(context.Context, *sql.DB, *session.Store, string) (approval.PendingList, error)
}

type approvalListHandler struct {
	db        *sql.DB
	lister    PendingApprovalLister
	sessions  *session.Store
	profiles  EmailSessionProfiler
	validator *middleware.SchemaValidator
	now       func() time.Time
}

// NewApprovalListHandler constructs an unmounted endpoint. It does not perform
// an approval decision or authorize any new device.
func NewApprovalListHandler(db *sql.DB, lister PendingApprovalLister, sessions *session.Store, profiles EmailSessionProfiler, validator *middleware.SchemaValidator) (http.Handler, error) {
	if db == nil || nilAuthDependency(lister) || sessions == nil || nilAuthDependency(profiles) || validator == nil {
		return nil, middleware.ErrContract
	}
	return &approvalListHandler{db: db, lister: lister, sessions: sessions, profiles: profiles, validator: validator, now: time.Now}, nil
}

func (h *approvalListHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if w == nil {
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	if h == nil || r == nil {
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
	if r.Method != http.MethodGet || r.URL == nil || r.URL.Path != approvalListPath || r.URL.RawPath != "" || r.URL.RawQuery != "" || r.URL.ForceQuery {
		fail(middleware.BadRequest)
		return
	}
	if r.Body != nil {
		body, readErr := io.ReadAll(io.LimitReader(r.Body, 1))
		if readErr != nil || len(body) != 0 {
			fail(middleware.BadRequest)
			return
		}
	}
	token, err := middleware.BearerToken(r.Header)
	if err != nil {
		fail(middleware.Unauthenticated)
		return
	}
	list, err := h.lister.ListPending(r.Context(), h.db, h.sessions, token)
	if err != nil {
		if errors.Is(err, session.ErrInvalidSession) {
			fail(middleware.Unauthenticated)
		} else {
			fail(middleware.Unavailable)
		}
		return
	}
	encoded, err := h.validator.EncodeResponse("LoginApprovalList", list)
	if err != nil || len(encoded) == 0 {
		fail(middleware.Unavailable)
		return
	}
	// A list is informational, not an authorization to decide. Recheck the
	// same bearer in a fresh transaction before releasing even this result.
	profile, err := h.profiles.ProfileForResponse(r.Context(), h.db, token)
	if err != nil {
		if errors.Is(err, session.ErrInvalidSession) {
			fail(middleware.Unauthenticated)
		} else {
			fail(middleware.Unavailable)
		}
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
	// ListPending rechecks expiry before committing. The profile recheck can
	// take additional time, so do not release an item that expired meanwhile.
	for _, item := range list.Items {
		if item.ExpiresAt.IsZero() || !item.ExpiresAt.After(now) {
			fail(middleware.Unavailable)
			return
		}
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(encoded)
}
