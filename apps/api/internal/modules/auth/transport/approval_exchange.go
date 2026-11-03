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
	"strings"
	"time"

	api "nerae/api/internal/gen"
	"nerae/api/internal/middleware"
	"nerae/api/internal/modules/auth/approval"
	"nerae/api/internal/modules/auth/ratelimit"
	"nerae/api/internal/modules/auth/session"
)

type approvalPollEntry interface {
	Exchange(context.Context, *sql.DB, string, approval.ExchangeInput) (approval.PollOutcome, error)
}

type approvalPollBegin func(context.Context, netip.Addr) (approvalPollEntry, error)

type approvalExchangeHandler struct {
	database  *sql.DB
	begin     approvalPollBegin
	profiles  EmailSessionProfiler
	validator *middleware.SchemaValidator
	ip        TrustedSourceIPResolver
	now       func() time.Time
}

// NewApprovalExchangeHandler constructs an unmounted anonymous handler. The
// concrete gateway enforces IP/global and capability limits before MySQL.
func NewApprovalExchangeHandler(db *sql.DB, gateway *approval.RateLimitedPoll, profiles EmailSessionProfiler,
	validator *middleware.SchemaValidator, ip TrustedSourceIPResolver) (http.Handler, error) {
	if db == nil || gateway == nil || nilAuthDependency(profiles) || validator == nil || nilAuthDependency(ip) {
		return nil, middleware.ErrContract
	}
	return &approvalExchangeHandler{database: db, profiles: profiles, validator: validator, ip: ip, now: time.Now,
		begin: func(ctx context.Context, address netip.Addr) (approvalPollEntry, error) {
			return gateway.Begin(ctx, address)
		}}, nil
}

func (h *approvalExchangeHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if w == nil {
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	if h == nil || r == nil || h.database == nil || h.validator == nil || h.begin == nil || h.now == nil ||
		nilAuthDependency(h.ip) || nilAuthDependency(h.profiles) {
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
	address, err := h.ip.ResolveTrustedSourceIP(r)
	if err != nil || !validSourceIP(address) {
		fail(middleware.Unavailable)
		return
	}
	// Charge IP/global before parsing key or body, including invalid requests.
	entry, err := h.begin(r.Context(), address)
	if err != nil || nilAuthDependency(entry) {
		fail(decisionLimitError(err))
		return
	}
	approvalID, ok := approvalExchangePath(r)
	if !ok {
		fail(middleware.BadRequest)
		return
	}
	key, err := decisionIdempotencyKey(r.Header)
	if err != nil {
		fail(middleware.BadRequest)
		return
	}
	body, err := h.validator.ReadRequest(r, "LoginApprovalExchangeRequest")
	if err != nil {
		fail(requestErrorKind(err))
		return
	}
	var request api.LoginApprovalExchangeRequest
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil || !errors.Is(decoder.Decode(new(any)), io.EOF) {
		fail(middleware.ValidationError)
		return
	}
	result, err := entry.Exchange(r.Context(), h.database, key, approval.ExchangeInput{
		ApprovalID: approvalID, Secret: string(request.ApprovalSecret), Proof: string(request.DeviceProof),
	})
	if err != nil {
		fail(approvalExchangeError(err))
		return
	}
	var encoded []byte
	switch result.Status() {
	case approval.PollPending, approval.PollDenied, approval.PollExpired:
		if token, ok := result.TokenForResponse(); ok || token != "" {
			fail(middleware.Unavailable)
			return
		}
		encoded, err = h.validator.EncodeResponse("LoginApprovalOutcome", map[string]string{"status": string(result.Status())})
	case approval.PollAuthenticated:
		token, ok := result.TokenForResponse()
		if !ok || !validResponseToken(token) {
			fail(middleware.Unavailable)
			return
		}
		profile, profileErr := h.profiles.ProfileForResponse(r.Context(), h.database, token)
		if profileErr != nil {
			if errors.Is(profileErr, session.ErrInvalidSession) {
				fail(middleware.Unauthenticated)
			} else {
				fail(middleware.Unavailable)
			}
			return
		}
		user, profileErr := responseUser(profile)
		if profileErr != nil {
			fail(middleware.Unavailable)
			return
		}
		encoded, err = h.encodeAuthenticated(api.PhoneAuthSuccess{Status: api.Authenticated, Token: token, User: user}, profile)
		if errors.Is(err, session.ErrInvalidSession) {
			fail(middleware.Unauthenticated)
			return
		}
	default:
		fail(middleware.Unavailable)
		return
	}
	if err != nil || len(encoded) == 0 {
		fail(middleware.Unavailable)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(encoded)
}

func (h *approvalExchangeHandler) encodeAuthenticated(response api.PhoneAuthSuccess, profile session.ResponseProfile) ([]byte, error) {
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
		encoded, err := h.validator.EncodeResponse("LoginApprovalOutcome", response)
		if err != nil {
			return nil, session.ErrSessionUnavailable
		}
		after := h.now().UTC()
		current, err := profile.RemainingSeconds(after)
		if after.IsZero() || err != nil {
			return nil, session.ErrInvalidSession
		}
		if current == seconds {
			return encoded, nil
		}
	}
	return nil, session.ErrSessionUnavailable
}

func approvalExchangePath(r *http.Request) (string, bool) {
	const prefix, suffix = "/auth/login-approvals/", "/exchange"
	if r == nil || r.Method != http.MethodPost || r.URL == nil || r.URL.RawPath != "" || r.URL.RawQuery != "" || r.URL.ForceQuery ||
		!strings.HasPrefix(r.URL.Path, prefix) || !strings.HasSuffix(r.URL.Path, suffix) {
		return "", false
	}
	id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, prefix), suffix)
	if len(id) < 22 || len(id) > 128 {
		return "", false
	}
	for _, value := range id {
		if !(value >= 'A' && value <= 'Z' || value >= 'a' && value <= 'z' || value >= '0' && value <= '9' || value == '-' || value == '_') {
			return "", false
		}
	}
	return id, true
}

func approvalExchangeError(err error) middleware.ErrorKind {
	switch {
	case errors.Is(err, ratelimit.ErrLimited):
		return middleware.RateLimited
	case errors.Is(err, approval.ErrExchangeReceiptConflict):
		return middleware.Conflict
	default:
		return middleware.Unavailable
	}
}
