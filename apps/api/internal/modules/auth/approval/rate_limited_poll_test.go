package approval

import (
	"context"
	"database/sql"
	"errors"
	"net/netip"
	"reflect"
	"testing"

	"nerae/api/internal/modules/auth/ratelimit"
)

type pollGateFixture struct {
	steps       []string
	entryErr    error
	capErr      error
	result      PollOutcome
	exchangeErr error
}

func (f *pollGateFixture) AllowEntry(_ context.Context, _ netip.Addr) error {
	f.steps = append(f.steps, "entry")
	return f.entryErr
}
func (f *pollGateFixture) AllowCapability(_ context.Context, _, _ string) error {
	f.steps = append(f.steps, "capability")
	return f.capErr
}
func (f *pollGateFixture) exchange(_ context.Context, _ *sql.DB, _ string, _ ExchangeInput) (PollOutcome, error) {
	f.steps = append(f.steps, "database")
	return f.result, f.exchangeErr
}

func TestRateLimitedPollOrdersEveryRequestBeforeDatabase(t *testing.T) {
	ip := netip.MustParseAddr("198.51.100.20")
	input := ExchangeInput{ApprovalID: "synthetic-approval-id-0001", Secret: "synthetic-secret", Proof: "synthetic-proof"}
	for _, test := range []struct {
		name      string
		entry     error
		cap       error
		exchange  error
		want      error
		wantSteps []string
	}{
		{"normal", nil, nil, nil, nil, []string{"entry", "capability", "database"}},
		{"entry limited", ratelimit.ErrLimited, nil, nil, ratelimit.ErrLimited, []string{"entry"}},
		{"entry unavailable", ratelimit.ErrUnavailable, nil, nil, ErrUnavailable, []string{"entry"}},
		{"capability limited", nil, ratelimit.ErrLimited, nil, ratelimit.ErrLimited, []string{"entry", "capability"}},
		{"invalid capability", nil, ratelimit.ErrInvalid, nil, ErrUnavailable, []string{"entry", "capability"}},
		{"database unavailable", nil, nil, ErrUnavailable, ErrUnavailable, []string{"entry", "capability", "database"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := &pollGateFixture{entryErr: test.entry, capErr: test.cap, exchangeErr: test.exchange,
				result: PollOutcome{status: PollPending}}
			gateway := &RateLimitedPoll{poll: fixture, limiter: fixture}
			entry, err := gateway.Begin(context.Background(), ip)
			var result PollOutcome
			if err == nil {
				result, err = entry.Exchange(context.Background(), new(sql.DB), "synthetic-key", input)
			}
			if !errors.Is(err, test.want) || !reflect.DeepEqual(fixture.steps, test.wantSteps) {
				t.Fatal("gateway did not preserve rate-limit ordering or error class")
			}
			if test.want != nil && result.Status() != "" {
				t.Fatal("failed gateway released a poll result")
			}
			if test.want == nil && result.Status() != PollPending {
				t.Fatal("successful gateway did not preserve result")
			}
		})
	}
}

func TestPollEntryCannotBeReusedOrCopied(t *testing.T) {
	fixture := &pollGateFixture{result: PollOutcome{status: PollPending}}
	gateway := &RateLimitedPoll{poll: fixture, limiter: fixture}
	entry, err := gateway.Begin(context.Background(), netip.MustParseAddr("198.51.100.20"))
	if err != nil || entry == nil {
		t.Fatal("could not obtain one-use entry")
	}
	copyOfInterface := entry
	results := make(chan error, 16)
	for index := range 16 {
		go func() {
			selected := entry
			if index%2 == 0 {
				selected = copyOfInterface
			}
			_, err := selected.Exchange(context.Background(), new(sql.DB), "synthetic-key", ExchangeInput{})
			results <- err
		}()
	}
	success, rejected := 0, 0
	for range 16 {
		err := <-results
		if err == nil {
			success++
		} else if errors.Is(err, ErrUnavailable) {
			rejected++
		} else {
			t.Fatal("unexpected entry error")
		}
	}
	if success != 1 || rejected != 15 || !reflect.DeepEqual(fixture.steps, []string{"entry", "capability", "database"}) {
		t.Fatal("a copied or concurrent permit skipped the IP/global budget")
	}
}

func TestOnlyRateLimitedPollExportsExchangeEntry(t *testing.T) {
	for _, value := range []any{new(Exchanger), new(ReceiptExchanger), new(PollExchanger)} {
		if _, exists := reflect.TypeOf(value).MethodByName("Exchange"); exists {
			t.Fatal("a non-rate-limited exchange entry remains exported")
		}
	}
	if _, exists := reflect.TypeOf(new(RateLimitedPoll)).MethodByName("Begin"); !exists {
		t.Fatal("rate-limited request entry is missing")
	}
}
