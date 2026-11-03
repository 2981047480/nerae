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
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"nerae/api/internal/idempotency"
	"nerae/api/internal/middleware"
	"nerae/api/internal/modules/auth/approval"
	"nerae/api/internal/modules/auth/ratelimit"
	"nerae/api/internal/modules/auth/session"
)

type ApprovalDecisionExecutor interface {
	Execute(context.Context, *sql.DB, string, approval.DecisionInput) (idempotency.Result, error)
	Replay(context.Context, *sql.DB, string, approval.DecisionInput) (idempotency.Result, error)
}

// ApprovalDecisionRateLimiter requires both the pre-authentication entry
// budget and the post-authentication subject/approval budgets.
type ApprovalDecisionRateLimiter interface {
	AllowApprovalDecisionEntry(context.Context, netip.Addr) error
	AllowApprovalDecisionIdentity(context.Context, uint64, string) error
}

type approvalDecisionHandler struct {
	db        *sql.DB
	executor  ApprovalDecisionExecutor
	profiles  EmailSessionProfiler
	limiter   ApprovalDecisionRateLimiter
	validator *middleware.SchemaValidator
	ip        TrustedSourceIPResolver
	now       func() time.Time
}

// NewApprovalDecisionHandler constructs an unmounted handler. Its caller
// must supply explicit approval-specific limits and a trusted proxy boundary.
func NewApprovalDecisionHandler(db *sql.DB, executor ApprovalDecisionExecutor, profiles EmailSessionProfiler, limiter ApprovalDecisionRateLimiter, validator *middleware.SchemaValidator, ip TrustedSourceIPResolver) (http.Handler, error) {
	if db == nil || nilAuthDependency(executor) || nilAuthDependency(profiles) || nilAuthDependency(limiter) || validator == nil || nilAuthDependency(ip) {
		return nil, middleware.ErrContract
	}
	return &approvalDecisionHandler{db: db, executor: executor, profiles: profiles, limiter: limiter, validator: validator, ip: ip, now: time.Now}, nil
}

func (h *approvalDecisionHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
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
	approvalID, ok := decisionPath(r)
	if !ok {
		fail(middleware.BadRequest)
		return
	}
	key, err := decisionIdempotencyKey(r.Header)
	if err != nil {
		fail(middleware.BadRequest)
		return
	}
	token, err := middleware.BearerToken(r.Header)
	if err != nil {
		fail(middleware.Unauthenticated)
		return
	}
	address, err := h.ip.ResolveTrustedSourceIP(r)
	if err != nil || !validSourceIP(address) {
		fail(middleware.Unavailable)
		return
	}
	if err := h.limiter.AllowApprovalDecisionEntry(r.Context(), address); err != nil {
		fail(decisionLimitError(err))
		return
	}
	body, err := h.validator.ReadRequest(r, "LoginApprovalDecision")
	if err != nil {
		fail(requestErrorKind(err))
		return
	}
	var payload struct {
		Decision       approval.Decision `json:"decision"`
		ComparisonCode string            `json:"comparisonCode"`
		Confirmed      *bool             `json:"confirmed"`
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&payload); err != nil || !errors.Is(decoder.Decode(new(any)), io.EOF) {
		fail(middleware.ValidationError)
		return
	}
	input := approval.DecisionInput{ApprovalID: approvalID, IdempotencyKey: key, Decision: payload.Decision, ComparisonCode: payload.ComparisonCode}
	if payload.Confirmed != nil {
		input.Confirmed = *payload.Confirmed
	}
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
	subject, err := strconv.ParseUint(profile.SubjectID, 10, 64)
	if err != nil || subject == 0 || strconv.FormatUint(subject, 10) != profile.SubjectID || now.IsZero() || profile.ExpiresAt.IsZero() {
		fail(middleware.Unavailable)
		return
	}
	if !profile.ExpiresAt.After(now) {
		fail(middleware.Unauthenticated)
		return
	}
	if err := h.limiter.AllowApprovalDecisionIdentity(r.Context(), subject, approvalID); err != nil {
		fail(decisionLimitError(err))
		return
	}
	result, err := h.executor.Execute(r.Context(), h.db, token, input)
	if err != nil {
		fail(decisionExecutorError(err))
		return
	}
	// Execute may have committed before a concurrent revocation or expiry.
	// Replay uses a new transaction and rechecks authorization before release.
	checked, err := h.executor.Replay(r.Context(), h.db, token, input)
	if err != nil {
		fail(decisionExecutorError(err))
		return
	}
	state := "denied"
	if payload.Decision == approval.Approve {
		state = "approved"
	}
	expected, encodeErr := h.validator.EncodeResponse("LoginApprovalDecisionResult", map[string]string{"status": state})
	if encodeErr != nil || result.Status != http.StatusOK || checked.Status != result.Status ||
		result.ResultRef != "approval-decision:"+state+":"+approvalID || checked.ResultRef != result.ResultRef ||
		!bytes.Equal(expected, result.Body) || !bytes.Equal(checked.Body, result.Body) ||
		result.ExpiresAt.IsZero() || !checked.ExpiresAt.Equal(result.ExpiresAt) || !checked.Replayed {
		fail(middleware.Unavailable)
		return
	}
	finished := h.now().UTC()
	if finished.IsZero() || !result.ExpiresAt.After(finished) || !profile.ExpiresAt.After(finished) {
		fail(middleware.Unavailable)
		return
	}
	if result.Replayed {
		w.Header().Set("Idempotency-Replayed", "true")
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(expected)
}

func decisionPath(r *http.Request) (string, bool) {
	const prefix, suffix = "/me/login-approvals/", "/decision"
	if r == nil || r.Method != http.MethodPost || r.URL == nil || r.URL.RawPath != "" || r.URL.RawQuery != "" || r.URL.ForceQuery ||
		!strings.HasPrefix(r.URL.Path, prefix) || !strings.HasSuffix(r.URL.Path, suffix) {
		return "", false
	}
	id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, prefix), suffix)
	if len(id) < 22 || len(id) > 128 {
		return "", false
	}
	for _, c := range id {
		if !(c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			return "", false
		}
	}
	return id, true
}

func decisionIdempotencyKey(header http.Header) (string, error) {
	values := header.Values("Idempotency-Key")
	if len(values) != 1 || !utf8.ValidString(values[0]) || utf8.RuneCountInString(values[0]) < 22 ||
		utf8.RuneCountInString(values[0]) > 128 || strings.ContainsAny(values[0], "\x00\r\n") {
		return "", middleware.ErrInvalidRequest
	}
	return values[0], nil
}

func decisionLimitError(err error) middleware.ErrorKind {
	if errors.Is(err, ratelimit.ErrLimited) {
		return middleware.RateLimited
	}
	return middleware.Unavailable
}

func decisionExecutorError(err error) middleware.ErrorKind {
	switch {
	case errors.Is(err, idempotency.ErrAuthentication):
		return middleware.Unauthenticated
	case errors.Is(err, idempotency.ErrForbidden):
		return middleware.Forbidden
	case errors.Is(err, idempotency.ErrConflict), errors.Is(err, idempotency.ErrInProgress):
		return middleware.Conflict
	case errors.Is(err, idempotency.ErrInvalid):
		return middleware.ValidationError
	default:
		return middleware.Unavailable
	}
}
