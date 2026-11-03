package transport

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"

	api "nerae/api/internal/gen"
	"nerae/api/internal/middleware"
	"nerae/api/internal/modules/auth/approval"
	"nerae/api/internal/modules/auth/ratelimit"
	"nerae/api/internal/modules/auth/session"
)

type pollBodyReader struct{ reads int }

func (b *pollBodyReader) Read(_ []byte) (int, error) { b.reads++; return 0, io.EOF }
func (*pollBodyReader) Close() error                 { return nil }

type pollEntryFixture struct{ calls int }

func (f *pollEntryFixture) Exchange(context.Context, *sql.DB, string, approval.ExchangeInput) (approval.PollOutcome, error) {
	f.calls++
	return approval.PollOutcome{}, approval.ErrUnavailable
}

func TestApprovalExchangeRechecksSessionLifetime(t *testing.T) {
	validator, err := middleware.NewSchemaValidator(16<<10, 2048)
	if err != nil {
		t.Fatal(err)
	}
	base := time.Unix(1_700_000_000, 0).UTC()
	profile := session.ResponseProfile{ExpiresAt: base.Add(10 * time.Second)}
	response := api.PhoneAuthSuccess{Status: api.Authenticated, Token: "signed-token", User: api.User{
		Id: "101", Roles: []api.UserRoles{},
	}}
	times := []time.Time{base.Add(100 * time.Millisecond), base.Add(1100 * time.Millisecond),
		base.Add(1200 * time.Millisecond), base.Add(1300 * time.Millisecond)}
	index := 0
	handler := &approvalExchangeHandler{validator: validator, now: func() time.Time {
		if index >= len(times) {
			t.Fatal("unexpected clock read")
		}
		value := times[index]
		index++
		return value
	}}
	encoded, err := handler.encodeAuthenticated(response, profile)
	var wire api.PhoneAuthSuccess
	if err != nil || json.Unmarshal(encoded, &wire) != nil || wire.ExpiresIn != 8 || index != 4 {
		t.Fatal("response did not recompute the shorter remaining lifetime")
	}
	handler.now = func() time.Time { return base.Add(9200 * time.Millisecond) }
	if _, err := handler.encodeAuthenticated(response, profile); !errors.Is(err, session.ErrInvalidSession) {
		t.Fatal("almost-expired session was returned as authenticated")
	}
	handler.now = func() time.Time { return time.Time{} }
	if _, err := handler.encodeAuthenticated(response, profile); !errors.Is(err, session.ErrSessionUnavailable) {
		t.Fatal("broken clock was treated as an expired account")
	}
}

func TestApprovalExchangeEntryLimitPrecedesBodyRead(t *testing.T) {
	validator, err := middleware.NewSchemaValidator(1<<20, 100000)
	if err != nil {
		t.Fatal("could not create schema validator")
	}
	entry := &pollEntryFixture{}
	beginCalls := 0
	handler := &approvalExchangeHandler{database: new(sql.DB), validator: validator, now: time.Now,
		profiles: ageProfileFunc(func(context.Context, *sql.DB, string) (session.ResponseProfile, error) {
			return session.ResponseProfile{}, session.ErrSessionUnavailable
		}),
		ip: TrustedSourceIPFunc(func(*http.Request) (netip.Addr, error) { return netip.MustParseAddr("198.51.100.20"), nil }),
		begin: func(context.Context, netip.Addr) (approvalPollEntry, error) {
			beginCalls++
			return nil, ratelimit.ErrLimited
		}}
	body := &pollBodyReader{}
	request := httptest.NewRequest(http.MethodPost, "/auth/login-approvals/approval-0000000000001/exchange", nil)
	request.Body = body
	request.Header.Set("Idempotency-Key", strings.Repeat("a", 22))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusTooManyRequests || beginCalls != 1 || body.reads != 0 || entry.calls != 0 ||
		response.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("rate-limited request read the body or reached the exchanger")
	}
	if strings.Contains(response.Body.String(), "approval-0000000000001") {
		t.Fatal("error exposed an approval identifier")
	}
	handler.begin = func(context.Context, netip.Addr) (approvalPollEntry, error) { beginCalls++; return entry, nil }
	body = &pollBodyReader{}
	request = httptest.NewRequest(http.MethodPost, "/auth/login-approvals/short/exchange", nil)
	request.Body = body
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || beginCalls != 2 || body.reads != 0 || entry.calls != 0 {
		t.Fatal("invalid route bypassed the entry budget or read the body")
	}
	request = httptest.NewRequest(http.MethodPost, "/auth/login-approvals/approval-0000000000001/exchange", strings.NewReader(`{"bad":"input"}`))
	request.Header.Set("Idempotency-Key", strings.Repeat("a", 22))
	request.Header.Set("Content-Type", "application/json")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if beginCalls != 3 || entry.calls != 0 || (response.Code != http.StatusBadRequest && response.Code != http.StatusUnprocessableEntity) {
		t.Fatal("malformed body reached the exchanger or skipped the entry budget")
	}
}

func TestApprovalExchangePathAndErrorPrivacy(t *testing.T) {
	valid := "/auth/login-approvals/approval-0000000000001/exchange"
	for _, test := range []struct {
		method string
		path   string
		ok     bool
	}{
		{http.MethodPost, valid, true},
		{http.MethodGet, valid, false},
		{http.MethodPost, "/auth/login-approvals/short/exchange", false},
		{http.MethodPost, valid + "?subject=101", false},
		{http.MethodPost, "/auth/login-approvals/approval-0000000000001/extra/exchange", false},
	} {
		request := httptest.NewRequest(test.method, test.path, nil)
		if _, ok := approvalExchangePath(request); ok != test.ok {
			t.Fatal("approval exchange route accepted an invalid request")
		}
	}
	if approvalExchangeError(ratelimit.ErrLimited) != middleware.RateLimited ||
		approvalExchangeError(approval.ErrExchangeReceiptConflict) != middleware.Conflict ||
		approvalExchangeError(errors.New("sensitive detail")) != middleware.Unavailable {
		t.Fatal("exchange errors were not mapped to bounded response classes")
	}
}
