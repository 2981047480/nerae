// Package ratelimit provides authentication rate-limit primitives. It does
// not resolve forwarding headers or make authentication decisions.
package ratelimit

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"hash"
	"log/slog"
	"net/netip"
	"reflect"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

var (
	ErrInvalid     = errors.New("invalid rate limit input")
	ErrLimited     = errors.New("rate limit reached")
	ErrUnavailable = errors.New("rate limiter unavailable")
)

const (
	EmailChallenge   Purpose = "email_challenge"
	EmailLogin       Purpose = "email_login"
	ApprovalDecision Purpose = "approval_decision"

	maxLimit        = uint64(1_000_000)
	minWindow       = 100 * time.Millisecond
	maxWindow       = 24 * time.Hour
	maxStoredCount  = uint64(1_000_000)
	redisClusterTag = "{auth-rate-v1}"
)

type Purpose string

type DimensionPolicy struct {
	Limit  uint64
	Window time.Duration
}

type Policy struct {
	Email  DimensionPolicy
	Device DimensionPolicy
	IP     DimensionPolicy
	Global DimensionPolicy
}

// ApprovalDecisionPolicy is configured independently of email login. Its
// limits count authenticated subjects, individual approvals, trusted IPs and
// all decision requests respectively; they do not count emails or devices.
type ApprovalDecisionPolicy struct {
	Subject  DimensionPolicy
	Approval DimensionPolicy
	IP       DimensionPolicy
	Global   DimensionPolicy
}

type ApprovalDecisionLimiter struct {
	base   *Limiter
	policy ApprovalDecisionPolicy
}

// NewApprovalDecisionLimiter requires an explicit server-owned policy. In
// particular, login limits must not silently become short-code limits.
func NewApprovalDecisionLimiter(base *Limiter, policy ApprovalDecisionPolicy) (*ApprovalDecisionLimiter, error) {
	if base == nil || nilInterface(base.client) || !validDimension(policy.Subject) ||
		!validDimension(policy.Approval) || !validDimension(policy.IP) || !validDimension(policy.Global) {
		return nil, ErrInvalid
	}
	return &ApprovalDecisionLimiter{base: base, policy: policy}, nil
}

// HMACKeys must contain independently generated 32-byte secrets. New copies
// every key, so later mutation of the caller's slices cannot change Redis keys.
type HMACKeys struct {
	EmailIndex        []byte
	DeviceFingerprint []byte
	TrustedIP         []byte
	Global            []byte
}

// Config contains only server-owned limits, trust-boundary prefix settings,
// and HMAC secrets. It must never be populated from request data.
type Config struct {
	Keys           HMACKeys
	IPv6PrefixBits int
	EmailChallenge Policy
	EmailLogin     Policy
}

// Input contains values derived by trusted server code. SourceIP must already
// have been resolved using the deployment's trusted network boundary. This
// package never reads or parses X-Forwarded-For or any other request header.
type Input struct {
	EmailIndex        [32]byte
	DeviceFingerprint [32]byte
	SourceIP          netip.Addr
}

type secretKeys struct {
	email  [32]byte
	device [32]byte
	ip     [32]byte
	global [32]byte
}

type evaler interface {
	Eval(ctx context.Context, script string, keys []string, args ...interface{}) *redis.Cmd
}

type Limiter struct {
	client       evaler
	keys         secretKeys
	v6PrefixBits int
	challenge    Policy
	login        Policy
}

func (HMACKeys) String() string               { return "RateLimitHMACKeys{redacted}" }
func (HMACKeys) GoString() string             { return "RateLimitHMACKeys{redacted}" }
func (HMACKeys) MarshalJSON() ([]byte, error) { return []byte(`"[REDACTED]"`), nil }
func (HMACKeys) LogValue() slog.Value         { return slog.StringValue("RateLimitHMACKeys{redacted}") }
func (Config) String() string                 { return "RateLimitConfig{redacted}" }
func (Config) GoString() string               { return "RateLimitConfig{redacted}" }
func (Config) MarshalJSON() ([]byte, error)   { return []byte(`"[REDACTED]"`), nil }
func (Config) LogValue() slog.Value           { return slog.StringValue("RateLimitConfig{redacted}") }
func (Input) String() string                  { return "RateLimitInput{redacted}" }
func (Input) GoString() string                { return "RateLimitInput{redacted}" }
func (Input) MarshalJSON() ([]byte, error)    { return []byte(`"[REDACTED]"`), nil }
func (Input) LogValue() slog.Value            { return slog.StringValue("RateLimitInput{redacted}") }
func (*Limiter) String() string               { return "EmailAuthRateLimiter{redacted}" }
func (*Limiter) GoString() string             { return "EmailAuthRateLimiter{redacted}" }
func (*Limiter) MarshalJSON() ([]byte, error) { return []byte(`"[REDACTED]"`), nil }
func (*Limiter) LogValue() slog.Value         { return slog.StringValue("EmailAuthRateLimiter{redacted}") }

// New validates immutable configuration without contacting Redis.
func New(client redis.Scripter, config Config) (*Limiter, error) {
	if nilInterface(client) || config.IPv6PrefixBits < 16 || config.IPv6PrefixBits > 128 ||
		!validPolicy(config.EmailChallenge) || !validPolicy(config.EmailLogin) {
		return nil, ErrInvalid
	}
	keys, ok := copyKeys(config.Keys)
	if !ok {
		return nil, ErrInvalid
	}
	return &Limiter{client: client, keys: keys,
		v6PrefixBits: config.IPv6PrefixBits, challenge: config.EmailChallenge, login: config.EmailLogin}, nil
}

// Allow is the compatible one-stage API: it atomically checks and charges all
// four dimensions. A login coordinator using AllowLoginEntry must not call
// Allow for the same request, because that would charge IP/global twice.
func (l *Limiter) Allow(ctx context.Context, purpose Purpose, input Input) error {
	if l == nil || nilInterface(l.client) || ctx == nil {
		return ErrUnavailable
	}
	keys, policy, err := l.redisKeys(purpose, input)
	if err != nil {
		return ErrInvalid
	}
	return l.allow(ctx, keys, []DimensionPolicy{policy.Email, policy.Device, policy.IP, policy.Global})
}

// AllowLoginEntry charges the shared IP and global login budgets before a
// coordinator reads a challenge from MySQL. Every login request, known or
// unknown, must pass this stage first.
func (l *Limiter) AllowLoginEntry(ctx context.Context, sourceIP netip.Addr) error {
	if l == nil || nilInterface(l.client) || ctx == nil {
		return ErrUnavailable
	}
	if !sourceIP.IsValid() || sourceIP.IsUnspecified() || sourceIP.IsMulticast() || sourceIP.Zone() != "" {
		return ErrInvalid
	}
	keys, err := l.ipGlobalRedisKeys(EmailLogin, sourceIP)
	if err != nil {
		return ErrInvalid
	}
	return l.allow(ctx, keys, []DimensionPolicy{l.login.IP, l.login.Global})
}

// AllowUnknownChallenge is retained for source compatibility. New callers
// should invoke AllowLoginEntry before any challenge lookup for every request.
func (l *Limiter) AllowUnknownChallenge(ctx context.Context, sourceIP netip.Addr) error {
	return l.AllowLoginEntry(ctx, sourceIP)
}

// AllowLoginIdentityDimensions atomically charges only email and device for a
// known challenge after the entry budget and challenge lookup. It must not be
// followed by Allow for that request, which would double-charge IP/global.
func (l *Limiter) AllowLoginIdentityDimensions(ctx context.Context, emailIndex, deviceFingerprint [32]byte) error {
	if l == nil || nilInterface(l.client) || ctx == nil {
		return ErrUnavailable
	}
	keys, err := l.identityRedisKeys(EmailLogin, emailIndex, deviceFingerprint)
	if err != nil {
		return ErrInvalid
	}
	return l.allow(ctx, keys, []DimensionPolicy{l.login.Email, l.login.Device})
}

// AllowApprovalDecisionEntry charges independent IP/global counters before a
// decision request performs a database lookup. SourceIP must already come
// from the trusted proxy boundary. The purpose keeps approval counters
// separate from login counters.
func (l *ApprovalDecisionLimiter) AllowApprovalDecisionEntry(ctx context.Context, sourceIP netip.Addr) error {
	if l == nil || l.base == nil || nilInterface(l.base.client) || ctx == nil {
		return ErrUnavailable
	}
	keys, err := l.base.ipGlobalRedisKeys(ApprovalDecision, sourceIP)
	if err != nil {
		return ErrInvalid
	}
	return l.base.allow(ctx, keys, []DimensionPolicy{l.policy.IP, l.policy.Global})
}

// AllowApprovalDecisionIdentity charges subject/approval counters after the
// subject has been resolved from an authenticated session by trusted code.
// It is only a rate limit, never proof of ownership of the approval.
func (l *ApprovalDecisionLimiter) AllowApprovalDecisionIdentity(ctx context.Context, subjectID uint64, approvalID string) error {
	if l == nil || l.base == nil || nilInterface(l.base.client) || ctx == nil {
		return ErrUnavailable
	}
	keys, err := l.base.approvalDecisionIdentityKeys(subjectID, approvalID)
	if err != nil {
		return ErrInvalid
	}
	return l.base.allow(ctx, keys, []DimensionPolicy{l.policy.Subject, l.policy.Approval})
}

func (l *Limiter) approvalDecisionIdentityKeys(subjectID uint64, approvalID string) ([]string, error) {
	if l == nil || subjectID == 0 || len(approvalID) < 22 || len(approvalID) > 128 {
		return nil, ErrInvalid
	}
	for _, c := range approvalID {
		if !(c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '_' || c == '-') {
			return nil, ErrInvalid
		}
	}
	subject := strconv.FormatUint(subjectID, 10)
	// Include the authenticated subject in the approval dimension so another
	// subject cannot consume this subject's budget even if it learns the ID.
	approval := subject + "\x00" + approvalID
	return []string{
		l.redisKey(l.keys.email[:], ApprovalDecision, "subject", []byte(subject)),
		l.redisKey(l.keys.device[:], ApprovalDecision, "approval", []byte(approval)),
	}, nil
}

func (l *Limiter) allow(ctx context.Context, keys []string, dimensions []DimensionPolicy) error {
	args := make([]interface{}, 0, len(dimensions)*2)
	for _, dimension := range dimensions {
		args = append(args, strconv.FormatUint(dimension.Limit, 10), strconv.FormatInt(dimension.Window.Milliseconds(), 10))
	}
	result, err := l.client.Eval(ctx, atomicScript, keys, args...).Int64()
	if err != nil {
		return ErrUnavailable
	}
	switch result {
	case 1:
		return nil
	case 0:
		return ErrLimited
	default:
		return ErrUnavailable
	}
}

func (l *Limiter) redisKeys(purpose Purpose, input Input) ([]string, Policy, error) {
	if l == nil || purpose != EmailChallenge && purpose != EmailLogin ||
		input.EmailIndex == [32]byte{} || input.DeviceFingerprint == [32]byte{} {
		return nil, Policy{}, ErrInvalid
	}
	if !input.SourceIP.IsValid() || input.SourceIP.IsUnspecified() || input.SourceIP.IsMulticast() || input.SourceIP.Zone() != "" {
		return nil, Policy{}, ErrInvalid
	}
	policy := l.challenge
	if purpose == EmailLogin {
		policy = l.login
	}
	ipGlobal, err := l.ipGlobalRedisKeys(purpose, input.SourceIP)
	if err != nil {
		return nil, Policy{}, ErrInvalid
	}
	keys := []string{
		l.redisKey(l.keys.email[:], purpose, "email", input.EmailIndex[:]),
		l.redisKey(l.keys.device[:], purpose, "device", input.DeviceFingerprint[:]),
		ipGlobal[0], ipGlobal[1],
	}
	return keys, policy, nil
}

func (l *Limiter) ipGlobalRedisKeys(purpose Purpose, sourceIP netip.Addr) ([]string, error) {
	if l == nil || purpose != EmailChallenge && purpose != EmailLogin && purpose != ApprovalDecision ||
		!sourceIP.IsValid() || sourceIP.IsUnspecified() || sourceIP.IsMulticast() || sourceIP.Zone() != "" {
		return nil, ErrInvalid
	}
	ipValue, err := l.ipPayload(sourceIP)
	if err != nil {
		return nil, ErrInvalid
	}
	return []string{
		l.redisKey(l.keys.ip[:], purpose, "ip", ipValue),
		l.redisKey(l.keys.global[:], purpose, "global", []byte("all")),
	}, nil
}

func (l *Limiter) identityRedisKeys(purpose Purpose, emailIndex, deviceFingerprint [32]byte) ([]string, error) {
	if l == nil || purpose != EmailLogin && purpose != EmailChallenge || emailIndex == ([32]byte{}) || deviceFingerprint == ([32]byte{}) {
		return nil, ErrInvalid
	}
	return []string{
		l.redisKey(l.keys.email[:], purpose, "email", emailIndex[:]),
		l.redisKey(l.keys.device[:], purpose, "device", deviceFingerprint[:]),
	}, nil
}

func (l *Limiter) ipPayload(address netip.Addr) ([]byte, error) {
	address = address.Unmap()
	if !address.IsValid() || address.IsUnspecified() || address.IsMulticast() || address.Zone() != "" {
		return nil, ErrInvalid
	}
	prefixBits := 32
	family := byte(4)
	if !address.Is4() {
		prefixBits = l.v6PrefixBits
		family = 6
	}
	prefix := netip.PrefixFrom(address, prefixBits).Masked()
	value := []byte{family, byte(prefixBits)}
	return append(value, prefix.Addr().AsSlice()...), nil
}

func (l *Limiter) redisKey(key []byte, purpose Purpose, dimension string, value []byte) string {
	mac := hmac.New(sha256.New, key)
	writePart(mac, []byte("nerae.auth.email-rate-limit.v1"))
	writePart(mac, []byte(purpose))
	writePart(mac, []byte(dimension))
	writePart(mac, value)
	return "nerae:auth:rate:" + redisClusterTag + ":" + string(purpose) + ":" + dimension + ":" + hex.EncodeToString(mac.Sum(nil))
}

func writePart(mac hash.Hash, value []byte) {
	var size [4]byte
	binary.BigEndian.PutUint32(size[:], uint32(len(value)))
	_, _ = mac.Write(size[:])
	_, _ = mac.Write(value)
}

func validPolicy(policy Policy) bool {
	return validDimension(policy.Email) && validDimension(policy.Device) && validDimension(policy.IP) && validDimension(policy.Global)
}

func validDimension(value DimensionPolicy) bool {
	return value.Limit >= 1 && value.Limit <= maxLimit && value.Window >= minWindow && value.Window <= maxWindow && value.Window%time.Millisecond == 0
}

func copyKeys(keys HMACKeys) (secretKeys, bool) {
	var result secretKeys
	values := [][]byte{keys.EmailIndex, keys.DeviceFingerprint, keys.TrustedIP, keys.Global}
	outputs := []*[32]byte{&result.email, &result.device, &result.ip, &result.global}
	for i, value := range values {
		if len(value) != 32 {
			return secretKeys{}, false
		}
		var nonzero byte
		for _, item := range value {
			nonzero |= item
		}
		if nonzero == 0 {
			return secretKeys{}, false
		}
		copy(outputs[i][:], value)
		for j := 0; j < i; j++ {
			if hmac.Equal(outputs[i][:], outputs[j][:]) {
				return secretKeys{}, false
			}
		}
	}
	return result, true
}

func nilInterface(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}

// atomicScript validates every supplied counter and TTL before mutating keys.
// Counts remain small integers to keep Lua's numeric representation exact.
const atomicScript = `
local overLimit = false
for i = 1, #KEYS do
  local limit = tonumber(ARGV[(i - 1) * 2 + 1])
  local window = tonumber(ARGV[(i - 1) * 2 + 2])
  local kind = redis.call('TYPE', KEYS[i]).ok
  if kind == 'none' then
    if redis.call('PTTL', KEYS[i]) ~= -2 then return -1 end
  else
    if kind ~= 'string' then return -1 end
    local raw = redis.call('GET', KEYS[i])
    if not raw or #raw > 7 or string.match(raw, '^[0-9]+$') == nil or (#raw > 1 and string.sub(raw, 1, 1) == '0') then return -1 end
    local count = tonumber(raw)
    local ttl = redis.call('PTTL', KEYS[i])
    if not count or count < 1 or count > 1000000 or count % 1 ~= 0 or ttl <= 0 or ttl > window then return -1 end
    if count >= limit then overLimit = true end
  end
end
if overLimit then return 0 end
for i = 1, #KEYS do
  local kind = redis.call('TYPE', KEYS[i]).ok
  if kind == 'none' then
    redis.call('SET', KEYS[i], '1', 'PX', ARGV[(i - 1) * 2 + 2])
  else
    redis.call('INCR', KEYS[i])
  end
end
return 1
`
