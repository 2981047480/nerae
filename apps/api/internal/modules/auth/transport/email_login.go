package transport

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/netip"
	"time"

	api "nerae/api/internal/gen"
	"nerae/api/internal/middleware"
	"nerae/api/internal/modules/auth/application"
	"nerae/api/internal/modules/auth/challenge"
	"nerae/api/internal/modules/auth/ratelimit"
	"nerae/api/internal/modules/auth/session"
)

const emailLoginPath = "/auth/email-login"

// EmailLoginVerifier must apply ingress and identity/device limits, commit the
// login transaction, and revalidate any receipt before returning an outcome.
// The handler does not accept a verifier that delegates those duties to HTTP.
type EmailLoginVerifier interface {
	VerifyEmail(context.Context, *sql.DB, string, challenge.AttemptInput, netip.Addr) (application.Outcome, error)
}

// EmailSessionProfiler rechecks the committed session against current storage.
type EmailSessionProfiler interface {
	ProfileForResponse(context.Context, *sql.DB, string) (session.ResponseProfile, error)
}

type emailLoginHandler struct {
	database  *sql.DB
	verifier  EmailLoginVerifier
	profiles  EmailSessionProfiler
	validator *middleware.SchemaValidator
	ip        TrustedSourceIPResolver
	now       func() time.Time
}

// NewEmailLoginHandler creates an unmounted handler. Production assembly must
// provide the real rate-limited verifier, trusted IP boundary and live stores.
func NewEmailLoginHandler(db *sql.DB, verifier EmailLoginVerifier, profiles EmailSessionProfiler, validator *middleware.SchemaValidator, ip TrustedSourceIPResolver) (http.Handler, error) {
	if db == nil || nilAuthDependency(verifier) || nilAuthDependency(profiles) || validator == nil || nilAuthDependency(ip) {
		return nil, middleware.ErrContract
	}
	return &emailLoginHandler{database: db, verifier: verifier, profiles: profiles, validator: validator, ip: ip, now: time.Now}, nil
}

func (h *emailLoginHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
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
	if r.Method != http.MethodPost || r.URL == nil || r.URL.Path != emailLoginPath || r.URL.RawPath != "" || r.URL.RawQuery != "" || r.URL.ForceQuery {
		h.fail(w, id, middleware.BadRequest)
		return
	}
	key, err := challengeIdempotencyKey(r.Header)
	if err != nil {
		h.fail(w, id, middleware.BadRequest)
		return
	}
	body, err := h.validator.ReadRequest(r, "EmailLoginRequest")
	if err != nil {
		h.fail(w, id, requestErrorKind(err))
		return
	}
	var request api.EmailLoginRequest
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil || !errors.Is(decoder.Decode(new(any)), io.EOF) {
		h.fail(w, id, middleware.ValidationError)
		return
	}
	ip, err := h.ip.ResolveTrustedSourceIP(r)
	if err != nil || !validSourceIP(ip) {
		h.fail(w, id, middleware.Unavailable)
		return
	}
	result, err := h.verifier.VerifyEmail(r.Context(), h.database, key, challenge.AttemptInput{
		ChallengeID: string(request.ChallengeId), Code: string(request.Code), DeviceProof: string(request.DeviceProof),
	}, ip)
	if err != nil {
		h.fail(w, id, loginErrorKind(err))
		return
	}
	var encoded []byte
	var validUntil time.Time
	switch result.Kind() {
	case application.Authenticated:
		token, ok := result.SessionTokenForResponse()
		if !ok || !validResponseToken(token) {
			h.fail(w, id, middleware.Unavailable)
			return
		}
		profile, err := h.profiles.ProfileForResponse(r.Context(), h.database, token)
		if err != nil {
			if errors.Is(err, session.ErrInvalidSession) {
				h.fail(w, id, middleware.Unauthenticated)
			} else {
				h.fail(w, id, middleware.Unavailable)
			}
			return
		}
		user, err := responseUser(profile)
		if err != nil {
			h.fail(w, id, middleware.Unavailable)
			return
		}
		response := api.PhoneAuthSuccess{Status: api.Authenticated, Token: token, User: user}
		encoded, err = h.encodeAuthenticatedResponse(response, profile)
		if errors.Is(err, session.ErrInvalidSession) {
			h.fail(w, id, middleware.Unauthenticated)
			return
		}
	case application.ApprovalRequired:
		pending, ok := result.PendingApprovalForResponse()
		if !ok || pending.ExpiresAt.IsZero() {
			h.fail(w, id, middleware.Unavailable)
			return
		}
		now := h.now().UTC()
		if now.IsZero() {
			h.fail(w, id, middleware.Unavailable)
			return
		}
		if !pending.ExpiresAt.After(now) {
			h.fail(w, id, middleware.Unauthenticated)
			return
		}
		response := api.PhoneAuthApprovalRequired{Status: api.ApprovalRequired, ApprovalId: api.AuthOpaqueId(pending.ID),
			ApprovalSecret: api.AuthSecret(pending.Secret), Nonce: api.AuthSecret(pending.Nonce), ComparisonCode: pending.ComparisonCode, ExpiresAt: pending.ExpiresAt}
		validUntil = pending.ExpiresAt
		encoded, err = h.validator.EncodeResponse("EmailLoginOutcome", response)
	default:
		h.fail(w, id, middleware.Unavailable)
		return
	}
	if err != nil || len(encoded) == 0 {
		h.fail(w, id, middleware.Unavailable)
		return
	}
	if result.Kind() == application.ApprovalRequired {
		finished := h.now().UTC()
		if finished.IsZero() || validUntil.IsZero() {
			h.fail(w, id, middleware.Unavailable)
			return
		}
		if !validUntil.After(finished) {
			h.fail(w, id, middleware.Unauthenticated)
			return
		}
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(encoded)
}

// The schema validator may itself take time. Recompute after encoding and
// retry a bounded number of times if a second elapsed, so a stale initial
// lifetime is not sent when validation crosses a second boundary.
func (h *emailLoginHandler) encodeAuthenticatedResponse(response api.PhoneAuthSuccess, profile session.ResponseProfile) ([]byte, error) {
	for attempt := 0; attempt < 3; attempt++ {
		now := h.now().UTC()
		if now.IsZero() {
			return nil, session.ErrSessionUnavailable
		}
		seconds, err := profile.RemainingSeconds(now)
		if err != nil {
			return nil, err
		}
		response.ExpiresIn = seconds
		encoded, err := h.validator.EncodeResponse("EmailLoginOutcome", response)
		if err != nil {
			return nil, session.ErrSessionUnavailable
		}
		after := h.now().UTC()
		if after.IsZero() {
			return nil, session.ErrSessionUnavailable
		}
		current, err := profile.RemainingSeconds(after)
		if err != nil {
			return nil, err
		}
		if current == seconds {
			return encoded, nil
		}
	}
	return nil, session.ErrSessionUnavailable
}

func validResponseToken(token string) bool {
	if len(token) < 1 || len(token) > 8192 {
		return false
	}
	for _, b := range []byte(token) {
		if b < 0x21 || b > 0x7e {
			return false
		}
	}
	return true
}

func responseUser(profile session.ResponseProfile) (api.User, error) {
	if profile.AgeDeclarationVersion < 1 || profile.AgeDeclarationVersion > 1<<53-1 {
		return api.User{}, session.ErrSessionUnavailable
	}
	roles := make([]api.UserRoles, 0, len(profile.Roles))
	for _, role := range profile.Roles {
		switch role {
		case "student":
			roles = append(roles, api.UserRolesStudent)
		case "coach":
			roles = append(roles, api.UserRolesCoach)
		default:
			return api.User{}, session.ErrSessionUnavailable
		}
	}
	ageVerified := profile.AgeVerified
	ageVersion := int64(profile.AgeDeclarationVersion)
	status := api.UserStatusActive
	return api.User{Id: profile.SubjectID, Nickname: profile.Nickname, Roles: roles, AgeVerified: &ageVerified, AgeDeclarationVersion: &ageVersion, Status: &status}, nil
}

func loginErrorKind(err error) middleware.ErrorKind {
	switch {
	case errors.Is(err, application.ErrRejected):
		return middleware.Unauthenticated
	case errors.Is(err, application.ErrReceiptConflict):
		return middleware.Conflict
	case errors.Is(err, ratelimit.ErrLimited):
		return middleware.RateLimited
	default:
		return middleware.Unavailable
	}
}

func (h *emailLoginHandler) fail(w http.ResponseWriter, id middleware.RequestID, kind middleware.ErrorKind) {
	if err := middleware.WriteError(w, h.validator, id, kind); err != nil {
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusInternalServerError)
	}
}
