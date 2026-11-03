package transport

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"reflect"
	"strings"
	"testing"
	"time"

	"nerae/api/internal/idempotency"
	"nerae/api/internal/middleware"
	"nerae/api/internal/modules/auth/approval"
	"nerae/api/internal/modules/auth/ratelimit"
	"nerae/api/internal/modules/auth/session"
)

type decisionTestPorts struct {
	steps                  []string
	entryErr, identityErr  error
	profileErr, executeErr error
	replayErr              error
	profile                session.ResponseProfile
	result, replay         idempotency.Result
	input                  approval.DecisionInput
	identitySubject        uint64
	identityApproval       string
}

func (p *decisionTestPorts) ResolveTrustedSourceIP(*http.Request) (netip.Addr, error) {
	p.steps = append(p.steps, "ip")
	return netip.MustParseAddr("192.0.2.41"), nil
}
func (p *decisionTestPorts) AllowApprovalDecisionEntry(_ context.Context, ip netip.Addr) error {
	p.steps = append(p.steps, "entry")
	if ip != netip.MustParseAddr("192.0.2.41") {
		return ratelimit.ErrInvalid
	}
	return p.entryErr
}
func (p *decisionTestPorts) ProfileForResponse(context.Context, *sql.DB, string) (session.ResponseProfile, error) {
	p.steps = append(p.steps, "profile")
	return p.profile, p.profileErr
}
func (p *decisionTestPorts) AllowApprovalDecisionIdentity(_ context.Context, subject uint64, id string) error {
	p.steps = append(p.steps, "identity")
	p.identitySubject, p.identityApproval = subject, id
	return p.identityErr
}
func (p *decisionTestPorts) Execute(_ context.Context, _ *sql.DB, _ string, input approval.DecisionInput) (idempotency.Result, error) {
	p.steps = append(p.steps, "execute")
	p.input = input
	return p.result, p.executeErr
}
func (p *decisionTestPorts) Replay(_ context.Context, _ *sql.DB, _ string, input approval.DecisionInput) (idempotency.Result, error) {
	p.steps = append(p.steps, "replay")
	if input != p.input {
		return idempotency.Result{}, idempotency.ErrUnavailable
	}
	return p.replay, p.replayErr
}

const decisionTestID = "approval-0000000000001"
const decisionTestKey = "request-key-000000000001"
const decisionTestToken = "synthetic-token-not-a-real-session"

func newDecisionTestHandler(t *testing.T) (http.Handler, *decisionTestPorts) {
	t.Helper()
	validator, err := middleware.NewSchemaValidator(16<<10, 2048)
	if err != nil {
		t.Fatal(err)
	}
	expires := time.Now().UTC().Add(time.Minute)
	p := &decisionTestPorts{profile: session.ResponseProfile{SubjectID: "101", ExpiresAt: expires},
		result: idempotency.Result{Status: 200, Body: []byte(`{"status":"approved"}`), ResultRef: "approval-decision:approved:" + decisionTestID, ExpiresAt: expires}}
	p.replay = p.result
	p.replay.Replayed = true
	handler, err := NewApprovalDecisionHandler(new(sql.DB), p, p, p, validator, p)
	if err != nil {
		t.Fatal(err)
	}
	return handler, p
}

func decisionTestRequest(body string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "http://local/me/login-approvals/"+decisionTestID+"/decision", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", "Bearer "+decisionTestToken)
	r.Header.Set("Idempotency-Key", decisionTestKey)
	return r
}

func TestApprovalDecisionHandlerUsesAuthenticatedSubjectAndRechecks(t *testing.T) {
	handler, p := newDecisionTestHandler(t)
	r := decisionTestRequest(`{"decision":"approve","comparisonCode":"123456","confirmed":true}`)
	r.Header.Set("X-User-ID", "999")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	want := []string{"ip", "entry", "profile", "identity", "execute", "replay"}
	if w.Code != http.StatusOK || w.Body.String() != `{"status":"approved"}` || w.Header().Get("Cache-Control") != "no-store" ||
		!reflect.DeepEqual(p.steps, want) || p.identitySubject != 101 || p.identityApproval != decisionTestID ||
		p.input.ComparisonCode != "123456" || !p.input.Confirmed || p.input.IdempotencyKey != decisionTestKey {
		t.Fatalf("approval decision did not follow the authenticated, rate-limited path: status=%d steps=%v", w.Code, p.steps)
	}
	if strings.Contains(w.Body.String(), decisionTestToken) || strings.Contains(w.Body.String(), "123456") || strings.Contains(w.Body.String(), "999") {
		t.Fatal("approval response disclosed request data")
	}
	p.steps = nil
	p.result.Replayed = true
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, decisionTestRequest(`{"decision":"approve","comparisonCode":"123456","confirmed":true}`))
	if w.Code != http.StatusOK || w.Header().Get("Idempotency-Replayed") != "true" {
		t.Fatal("authorized same-key replay was not identified")
	}
	p.result = idempotency.Result{Status: 200, Body: []byte(`{"status":"denied"}`), ResultRef: "approval-decision:denied:" + decisionTestID, ExpiresAt: p.profile.ExpiresAt}
	p.replay = p.result
	p.replay.Replayed = true
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, decisionTestRequest(`{"decision":"deny"}`))
	if w.Code != http.StatusOK || w.Body.String() != `{"status":"denied"}` || p.input.Confirmed || p.input.ComparisonCode != "" {
		t.Fatal("deny decision did not use its contract")
	}
}

func TestApprovalDecisionHandlerRejectsMalformedRequests(t *testing.T) {
	valid := `{"decision":"approve","comparisonCode":"123456","confirmed":true}`
	for _, tc := range []struct {
		name string
		edit func(*http.Request)
		body string
	}{
		{"method", func(r *http.Request) { r.Method = http.MethodGet }, valid},
		{"query", func(r *http.Request) { r.URL.RawQuery = "subject=101" }, valid},
		{"short id", func(r *http.Request) { r.URL.Path = "/me/login-approvals/x/decision" }, valid},
		{"raw path", func(r *http.Request) { r.URL.RawPath = "/encoded" }, valid},
		{"missing bearer", func(r *http.Request) { r.Header.Del("Authorization") }, valid},
		{"duplicate bearer", func(r *http.Request) { r.Header.Add("Authorization", "Bearer "+decisionTestToken) }, valid},
		{"missing key", func(r *http.Request) { r.Header.Del("Idempotency-Key") }, valid},
		{"duplicate key", func(r *http.Request) { r.Header.Add("Idempotency-Key", decisionTestKey) }, valid},
		{"short key", func(r *http.Request) { r.Header.Set("Idempotency-Key", "short") }, valid},
		{"duplicate JSON", nil, `{"decision":"approve","decision":"deny","comparisonCode":"123456","confirmed":true}`},
		{"missing confirmation", nil, `{"decision":"approve","comparisonCode":"123456"}`},
		{"extra field", nil, `{"decision":"deny","comparisonCode":"123456"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			handler, p := newDecisionTestHandler(t)
			r := decisionTestRequest(tc.body)
			if tc.edit != nil {
				tc.edit(r)
			}
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			if w.Code < 400 || w.Code >= 500 || strings.Contains(strings.Join(p.steps, ","), "profile") ||
				strings.Contains(w.Body.String(), decisionTestToken) || w.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("malformed request reached database or leaked details: status=%d steps=%v", w.Code, p.steps)
			}
		})
	}
}

func TestApprovalDecisionHandlerFailsClosed(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*decisionTestPorts)
		want int
		last string
	}{
		{"entry limit", func(p *decisionTestPorts) { p.entryErr = ratelimit.ErrLimited }, 429, "entry"},
		{"entry unavailable", func(p *decisionTestPorts) { p.entryErr = ratelimit.ErrUnavailable }, 503, "entry"},
		{"session revoked", func(p *decisionTestPorts) { p.profileErr = session.ErrInvalidSession }, 401, "profile"},
		{"profile unavailable", func(p *decisionTestPorts) { p.profileErr = session.ErrSessionUnavailable }, 503, "profile"},
		{"bad subject", func(p *decisionTestPorts) { p.profile.SubjectID = "00101" }, 503, "profile"},
		{"expired session", func(p *decisionTestPorts) { p.profile.ExpiresAt = time.Now().Add(-time.Second) }, 401, "profile"},
		{"identity limit", func(p *decisionTestPorts) { p.identityErr = ratelimit.ErrLimited }, 429, "identity"},
		{"identity unavailable", func(p *decisionTestPorts) { p.identityErr = ratelimit.ErrUnavailable }, 503, "identity"},
		{"executor authentication", func(p *decisionTestPorts) { p.executeErr = idempotency.ErrAuthentication }, 401, "execute"},
		{"executor denial", func(p *decisionTestPorts) { p.executeErr = idempotency.ErrForbidden }, 403, "execute"},
		{"executor conflict", func(p *decisionTestPorts) { p.executeErr = idempotency.ErrConflict }, 409, "execute"},
		{"outcome unknown", func(p *decisionTestPorts) { p.executeErr = idempotency.ErrOutcomeUnknown }, 503, "execute"},
		{"replay revoked", func(p *decisionTestPorts) { p.replayErr = idempotency.ErrAuthentication }, 401, "replay"},
		{"replay mismatch", func(p *decisionTestPorts) { p.replay.Body = []byte(`{"status":"denied"}`) }, 503, "replay"},
		{"unvalidated receipt", func(p *decisionTestPorts) { p.replay.Replayed = false }, 503, "replay"},
		{"wrong resource", func(p *decisionTestPorts) { p.result.ResultRef = "other" }, 503, "replay"},
		{"expired receipt", func(p *decisionTestPorts) {
			p.result.ExpiresAt = time.Now().Add(-time.Second)
			p.replay.ExpiresAt = p.result.ExpiresAt
		}, 503, "replay"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			handler, p := newDecisionTestHandler(t)
			tc.edit(p)
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, decisionTestRequest(`{"decision":"approve","comparisonCode":"123456","confirmed":true}`))
			if w.Code != tc.want || len(p.steps) == 0 || p.steps[len(p.steps)-1] != tc.last ||
				w.Header().Get("Cache-Control") != "no-store" || strings.Contains(w.Body.String(), decisionTestToken) || strings.Contains(w.Body.String(), "123456") {
				t.Fatalf("unexpected status or unsafe continuation: status=%d steps=%v", w.Code, p.steps)
			}
		})
	}
	if _, err := NewApprovalDecisionHandler(nil, nil, nil, nil, nil, nil); !errors.Is(err, middleware.ErrContract) {
		t.Fatal("missing decision dependencies were accepted")
	}
}
