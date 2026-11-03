package ratelimit

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"log/slog"
	"net/netip"
)

// ApprovalExchangePolicy has its own budgets: all anonymous attempts spend
// IP/global capacity, and well-shaped approval capabilities spend a separate
// capability budget before any database lookup.
type ApprovalExchangePolicy struct {
	Capability DimensionPolicy
	IP         DimensionPolicy
	Global     DimensionPolicy
}

type ApprovalExchangeLimiter struct {
	base   *Limiter
	policy ApprovalExchangePolicy
}

func (ApprovalExchangeLimiter) String() string               { return "ApprovalExchangeLimiter{redacted}" }
func (ApprovalExchangeLimiter) GoString() string             { return "ApprovalExchangeLimiter{redacted}" }
func (ApprovalExchangeLimiter) MarshalJSON() ([]byte, error) { return []byte(`"[REDACTED]"`), nil }
func (ApprovalExchangeLimiter) LogValue() slog.Value {
	return slog.StringValue("ApprovalExchangeLimiter{redacted}")
}

func NewApprovalExchangeLimiter(base *Limiter, policy ApprovalExchangePolicy) (*ApprovalExchangeLimiter, error) {
	if base == nil || nilInterface(base.client) || !validDimension(policy.Capability) || !validDimension(policy.IP) || !validDimension(policy.Global) {
		return nil, ErrInvalid
	}
	return &ApprovalExchangeLimiter{base: base, policy: policy}, nil
}

// AllowEntry charges trusted IP and global budgets before any approval or
// database lookup. It must be called for every request, including malformed
// capabilities and exact retries.
func (l *ApprovalExchangeLimiter) AllowEntry(ctx context.Context, sourceIP netip.Addr) error {
	if l == nil || l.base == nil || nilInterface(l.base.client) || ctx == nil {
		return ErrUnavailable
	}
	keys, err := l.base.ipGlobalRedisKeys(ApprovalExchange, sourceIP)
	if err != nil {
		return ErrInvalid
	}
	return l.base.allow(ctx, keys, []DimensionPolicy{l.policy.IP, l.policy.Global})
}

// AllowCapability charges a keyed digest of the approval ID and secret. An
// observer knowing only an approval ID cannot exhaust its legitimate budget.
// This is not authorization and must follow AllowEntry for every request.
func (l *ApprovalExchangeLimiter) AllowCapability(ctx context.Context, approvalID, approvalSecret string) error {
	if l == nil || l.base == nil || nilInterface(l.base.client) || ctx == nil {
		return ErrUnavailable
	}
	key, err := l.base.approvalExchangeCapabilityKey(approvalID, approvalSecret)
	if err != nil {
		return ErrInvalid
	}
	return l.base.allow(ctx, []string{key}, []DimensionPolicy{l.policy.Capability})
}

func (l *Limiter) approvalExchangeCapabilityKey(approvalID, approvalSecret string) (string, error) {
	if l == nil || len(approvalID) < 22 || len(approvalID) > 128 || len(approvalSecret) != 43 {
		return "", ErrInvalid
	}
	for _, value := range approvalID {
		if !(value >= 'A' && value <= 'Z' || value >= 'a' && value <= 'z' || value >= '0' && value <= '9' || value == '_' || value == '-') {
			return "", ErrInvalid
		}
	}
	secret, err := base64.RawURLEncoding.Strict().DecodeString(approvalSecret)
	if err != nil || len(secret) != 32 || base64.RawURLEncoding.EncodeToString(secret) != approvalSecret {
		return "", ErrInvalid
	}
	// Length-prefix the ID so concatenation cannot identify two capabilities
	// as the same value. Redis receives only the domain-separated HMAC.
	value := make([]byte, 4+len(approvalID)+len(secret))
	binary.BigEndian.PutUint32(value[:4], uint32(len(approvalID)))
	copy(value[4:], approvalID)
	copy(value[4+len(approvalID):], secret)
	return l.redisKey(l.keys.device[:], ApprovalExchange, "capability", value), nil
}
