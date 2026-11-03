package approval

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"net/netip"
	"sync/atomic"

	"nerae/api/internal/modules/auth/ratelimit"
)

// RateLimitedPoll is an internal pre-database gate. The source IP must come
// from a separately verified proxy boundary, never a caller-supplied header.
type pollRateLimiter interface {
	AllowEntry(context.Context, netip.Addr) error
	AllowCapability(context.Context, string, string) error
}

type pollExecutor interface {
	exchange(context.Context, *sql.DB, string, ExchangeInput) (PollOutcome, error)
}

type RateLimitedPoll struct {
	poll    pollExecutor
	limiter pollRateLimiter
}

func (RateLimitedPoll) String() string               { return "RateLimitedApprovalPoll{redacted}" }
func (RateLimitedPoll) GoString() string             { return "RateLimitedApprovalPoll{redacted}" }
func (RateLimitedPoll) MarshalJSON() ([]byte, error) { return nil, ErrUnavailable }
func (RateLimitedPoll) LogValue() slog.Value {
	return slog.StringValue("RateLimitedApprovalPoll{redacted}")
}

func NewRateLimitedPoll(poll *PollExchanger, limiter *ratelimit.ApprovalExchangeLimiter) (*RateLimitedPoll, error) {
	if poll == nil || !poll.valid() || limiter == nil {
		return nil, ErrUnavailable
	}
	return &RateLimitedPoll{poll: poll, limiter: limiter}, nil
}

// PollEntry is a one-use, in-process permit for a request whose IP/global
// budget has already been charged. Its private implementation prevents a
// caller from copying the atomic one-use state into a second permit.
type PollEntry interface {
	Exchange(context.Context, *sql.DB, string, ExchangeInput) (PollOutcome, error)
	approvalPollEntry()
}

type pollEntry struct {
	gateway *RateLimitedPoll
	used    atomic.Bool
}

func (*pollEntry) approvalPollEntry()           {}
func (*pollEntry) String() string               { return "ApprovalPollEntry{redacted}" }
func (*pollEntry) GoString() string             { return "ApprovalPollEntry{redacted}" }
func (*pollEntry) MarshalJSON() ([]byte, error) { return nil, ErrUnavailable }
func (*pollEntry) LogValue() slog.Value         { return slog.StringValue("ApprovalPollEntry{redacted}") }

// Begin charges IP/global before an HTTP handler parses the bounded request
// body or touches MySQL. A permit may be consumed at most once.
func (g *RateLimitedPoll) Begin(ctx context.Context, trustedIP netip.Addr) (PollEntry, error) {
	if g == nil || g.poll == nil || g.limiter == nil || ctx == nil {
		return nil, ErrUnavailable
	}
	if err := g.limiter.AllowEntry(ctx, trustedIP); err != nil {
		return nil, pollRateError(err)
	}
	return &pollEntry{gateway: g}, nil
}

// Exchange consumes the entry permit, charges the protected capability and
// only then touches MySQL. Limiter failures release no status or session.
func (entry *pollEntry) Exchange(ctx context.Context, db *sql.DB, key string, input ExchangeInput) (PollOutcome, error) {
	if entry == nil || entry.gateway == nil || entry.gateway.poll == nil || entry.gateway.limiter == nil || ctx == nil || !entry.used.CompareAndSwap(false, true) {
		return PollOutcome{}, ErrUnavailable
	}
	if err := entry.gateway.limiter.AllowCapability(ctx, input.ApprovalID, input.Secret); err != nil {
		return PollOutcome{}, pollRateError(err)
	}
	result, err := entry.gateway.poll.exchange(ctx, db, key, input)
	if err != nil {
		return PollOutcome{}, err
	}
	return result, nil
}

func pollRateError(err error) error {
	if errors.Is(err, ratelimit.ErrLimited) {
		return ratelimit.ErrLimited
	}
	if errors.Is(err, ratelimit.ErrUnavailable) {
		return errors.Join(ErrUnavailable, ratelimit.ErrUnavailable)
	}
	return ErrUnavailable
}
