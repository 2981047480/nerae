package transport

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	api "nerae/api/internal/gen"
	"nerae/api/internal/middleware"
	"nerae/api/internal/modules/auth/session"
)

const ageVerifyPath = "/me/age-verify"

// AgeDeclarationWriter must authenticate the bearer against current session
// state and commit the caller's versioned self-declaration before returning.
type AgeDeclarationWriter interface {
	DeclareAdultAtVersion(ctx context.Context, db *sql.DB, token string, expectedVersion uint64, isAdult bool) (uint64, error)
}

type ageVerifyHandler struct {
	database     *sql.DB
	declarations AgeDeclarationWriter
	profiles     EmailSessionProfiler
	validator    *middleware.SchemaValidator
	now          func() time.Time
}

// NewAgeVerifyHandler builds an unmounted authenticated declaration handler.
// It never accepts a caller-supplied subject; the session store derives it
// exclusively from the live bearer session.
func NewAgeVerifyHandler(db *sql.DB, declarations AgeDeclarationWriter, profiles EmailSessionProfiler, validator *middleware.SchemaValidator) (http.Handler, error) {
	if db == nil || nilAuthDependency(declarations) || nilAuthDependency(profiles) || validator == nil {
		return nil, middleware.ErrContract
	}
	return &ageVerifyHandler{database: db, declarations: declarations, profiles: profiles, validator: validator, now: time.Now}, nil
}

func (h *ageVerifyHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
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
	if r.Method != http.MethodPost || r.URL == nil || r.URL.Path != ageVerifyPath || r.URL.RawPath != "" || r.URL.RawQuery != "" || r.URL.ForceQuery {
		h.fail(w, id, middleware.BadRequest)
		return
	}
	body, err := h.validator.ReadRequest(r, "AgeAttestation")
	if err != nil {
		h.fail(w, id, requestErrorKind(err))
		return
	}
	var request api.AgeAttestation
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil || !errors.Is(decoder.Decode(new(any)), io.EOF) {
		h.fail(w, id, middleware.ValidationError)
		return
	}
	if request.IsAdult != api.AgeAttestationIsAdult(true) || request.ExpectedVersion < 1 || request.ExpectedVersion > 1<<53-1 {
		h.fail(w, id, middleware.ValidationError)
		return
	}
	token, err := middleware.BearerToken(r.Header)
	if err != nil {
		h.fail(w, id, middleware.Unauthenticated)
		return
	}
	committedVersion, err := h.declarations.DeclareAdultAtVersion(r.Context(), h.database, token, uint64(request.ExpectedVersion), true)
	if err != nil {
		h.fail(w, id, ageDeclarationErrorKind(err))
		return
	}
	expectedVersion := uint64(request.ExpectedVersion)
	if expectedVersion >= 1<<53-1 || committedVersion != expectedVersion+1 {
		h.fail(w, id, middleware.Unavailable)
		return
	}
	// The mutation commits before returning. Re-authenticate in a fresh
	// transaction and confirm that no concurrent withdrawal/reaffirmation
	// changed the current declaration before presenting a User response.
	profile, err := h.profiles.ProfileForResponse(r.Context(), h.database, token)
	if err != nil {
		if errors.Is(err, session.ErrInvalidSession) {
			h.fail(w, id, middleware.Unauthenticated)
		} else {
			h.fail(w, id, middleware.Unavailable)
		}
		return
	}
	if profile.AgeDeclarationVersion < 1 || profile.AgeDeclarationVersion > 1<<53-1 {
		h.fail(w, id, middleware.Unavailable)
		return
	}
	if !profile.AgeVerified || profile.AgeDeclarationVersion != committedVersion {
		h.fail(w, id, middleware.VersionConflict)
		return
	}
	user, err := responseUser(profile)
	if err != nil {
		h.fail(w, id, middleware.Unavailable)
		return
	}
	encoded, err := h.validator.EncodeResponse("User", user)
	if err != nil || len(encoded) == 0 {
		h.fail(w, id, middleware.Unavailable)
		return
	}
	now := h.now().UTC()
	if now.IsZero() || profile.ExpiresAt.IsZero() {
		h.fail(w, id, middleware.Unavailable)
		return
	}
	if !profile.ExpiresAt.After(now) {
		h.fail(w, id, middleware.Unauthenticated)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(encoded)
}

func ageDeclarationErrorKind(err error) middleware.ErrorKind {
	switch {
	case errors.Is(err, session.ErrInvalidSession):
		return middleware.Unauthenticated
	case errors.Is(err, session.ErrAgeDeclarationConflict):
		return middleware.VersionConflict
	case errors.Is(err, session.ErrAgeDeclarationVersionExhausted):
		return middleware.Conflict
	case errors.Is(err, session.ErrAdultDeclarationRequired), errors.Is(err, session.ErrAgeDeclarationVersionInvalid):
		return middleware.ValidationError
	default:
		return middleware.Unavailable
	}
}

func (h *ageVerifyHandler) fail(w http.ResponseWriter, id middleware.RequestID, kind middleware.ErrorKind) {
	if err := middleware.WriteError(w, h.validator, id, kind); err != nil {
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusInternalServerError)
	}
}
