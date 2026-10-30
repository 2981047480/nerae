package ratelimit

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

func TestNewRejectsUnsafeConfiguration(t *testing.T) {
	client := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"})
	defer client.Close()
	config := validConfig(t)
	if _, err := New(nil, config); !errors.Is(err, ErrInvalid) {
		t.Fatal("nil Redis client was accepted")
	}
	invalid := config
	invalid.Keys.EmailIndex = []byte("short")
	if _, err := New(client, invalid); !errors.Is(err, ErrInvalid) {
		t.Fatal("short HMAC key was accepted")
	}
	invalid = config
	invalid.Keys.DeviceFingerprint = append([]byte(nil), invalid.Keys.EmailIndex...)
	if _, err := New(client, invalid); !errors.Is(err, ErrInvalid) {
		t.Fatal("reused HMAC key was accepted")
	}
	invalid = config
	invalid.IPv6PrefixBits = 0
	if _, err := New(client, invalid); !errors.Is(err, ErrInvalid) {
		t.Fatal("unbounded IPv6 prefix was accepted")
	}
	invalid = config
	invalid.EmailChallenge.Global.Window = 1500 * time.Microsecond
	if _, err := New(client, invalid); !errors.Is(err, ErrInvalid) {
		t.Fatal("sub-millisecond window was accepted")
	}
	invalid = config
	invalid.EmailLogin.IP.Limit = maxLimit + 1
	if _, err := New(client, invalid); !errors.Is(err, ErrInvalid) {
		t.Fatal("unbounded rate limit was accepted")
	}
}

func TestInputValidationKeySeparationAndRedaction(t *testing.T) {
	config := validConfig(t)
	client := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"})
	defer client.Close()
	limiter, err := New(client, config)
	if err != nil {
		t.Fatal("limiter construction failed")
	}
	input := testInput("sentinel-email", "198.51.100.9")
	keys, _, err := limiter.redisKeys(EmailChallenge, input)
	if err != nil || len(keys) != 4 {
		t.Fatal("valid input did not produce four keys")
	}
	originalKeys := append([]string(nil), keys...)
	config.Keys.EmailIndex[0] ^= 0xff
	keysAfterConfigMutation, _, err := limiter.redisKeys(EmailChallenge, input)
	if err != nil || fmt.Sprint(keysAfterConfigMutation) != fmt.Sprint(originalKeys) {
		t.Fatal("limiter retained caller-owned mutable HMAC key memory")
	}
	for _, key := range keys {
		if !contains(key, redisClusterTag) || contains(key, "198.51.100") || contains(key, "sentinel-email") {
			t.Fatal("Redis key omitted fixed cluster tag or exposed raw input")
		}
	}
	loginKeys, _, err := limiter.redisKeys(EmailLogin, input)
	if err != nil {
		t.Fatal("second purpose key derivation failed")
	}
	for i := range keys {
		if keys[i] == loginKeys[i] {
			t.Fatal("authentication purposes shared a counter key")
		}
	}
	withMappedIP := input
	withMappedIP.SourceIP = netip.MustParseAddr("::ffff:198.51.100.9")
	mappedKeys, _, err := limiter.redisKeys(EmailChallenge, withMappedIP)
	if err != nil || keys[2] != mappedKeys[2] {
		t.Fatal("IPv4-mapped address did not normalize to the same IPv4 host key")
	}
	differentIPv4Host := input
	differentIPv4Host.SourceIP = netip.MustParseAddr("198.51.100.10")
	differentHostKeys, _, err := limiter.redisKeys(EmailChallenge, differentIPv4Host)
	if err != nil || keys[2] == differentHostKeys[2] {
		t.Fatal("different IPv4 hosts shared a counter")
	}
	v6a := input
	v6a.SourceIP = netip.MustParseAddr("2001:db8:1:2::1")
	v6b := input
	v6b.SourceIP = netip.MustParseAddr("2001:db8:1:2:ffff::99")
	v6aKeys, _, err := limiter.redisKeys(EmailChallenge, v6a)
	if err != nil {
		t.Fatal("IPv6 key derivation failed")
	}
	v6bKeys, _, err := limiter.redisKeys(EmailChallenge, v6b)
	if err != nil || v6aKeys[2] != v6bKeys[2] {
		t.Fatal("addresses in the same configured IPv6 prefix did not share a counter")
	}

	for _, bad := range []Input{
		{},
		{EmailIndex: input.EmailIndex, DeviceFingerprint: input.DeviceFingerprint, SourceIP: netip.Addr{}},
		{EmailIndex: input.EmailIndex, DeviceFingerprint: input.DeviceFingerprint, SourceIP: netip.IPv4Unspecified()},
	} {
		if !errors.Is(limiter.Allow(context.Background(), EmailChallenge, bad), ErrInvalid) {
			t.Fatal("missing or unspecified trusted IP was accepted")
		}
	}
	if !errors.Is(limiter.Allow(context.Background(), Purpose("unknown"), input), ErrInvalid) {
		t.Fatal("unknown purpose was accepted")
	}
	encodedInput, err := json.Marshal(input)
	if err != nil || contains(string(encodedInput), "198.51.100") || contains(fmt.Sprintf("%#v", input), "198.51.100") {
		t.Fatal("input formatting exposed source IP")
	}
	encodedConfig, err := json.Marshal(config)
	if err != nil || contains(string(encodedConfig), string(config.Keys.EmailIndex)) || contains(fmt.Sprintf("%#v", config), string(config.Keys.EmailIndex)) {
		t.Fatal("config formatting exposed HMAC key")
	}
	if contains(fmt.Sprint(ErrUnavailable), "redis") || contains(fmt.Sprint(ErrLimited), "dimension") {
		t.Fatal("public errors reveal dependency or exceeded dimension")
	}
}

func TestLimiterAgainstPrivateRedis(t *testing.T) {
	socket := os.Getenv("NERAE_RATELIMIT_REDIS_SOCKET")
	if socket == "" {
		t.Skip("set NERAE_RATELIMIT_REDIS_SOCKET using the private Redis harness")
	}
	assertPrivateRedisSocket(t, socket)
	client := redis.NewClient(&redis.Options{Network: "unix", Addr: socket, DialTimeout: time.Second, ReadTimeout: 2 * time.Second, WriteTimeout: 2 * time.Second})
	if err := client.Ping(context.Background()).Err(); err != nil {
		t.Fatal("private Redis ping failed")
	}
	t.Cleanup(func() { _ = client.Close() })

	t.Run("purpose and four independent thresholds", func(t *testing.T) {
		policy := policy(1, 1, 1, 1, 3*time.Second)
		limiter := newTestLimiter(t, client, policy, policy)
		input := testInput("purpose", "198.51.100.1")
		if err := limiter.Allow(context.Background(), EmailChallenge, input); err != nil {
			t.Fatal("first challenge was denied")
		}
		if !errors.Is(limiter.Allow(context.Background(), EmailChallenge, input), ErrLimited) {
			t.Fatal("repeated challenge was not limited")
		}
		if err := limiter.Allow(context.Background(), EmailLogin, input); err != nil {
			t.Fatal("email login shared the challenge counter")
		}
		if !errors.Is(limiter.Allow(context.Background(), EmailLogin, input), ErrLimited) {
			t.Fatal("repeated login was not limited")
		}
	})

	t.Run("email dimension", func(t *testing.T) {
		p := policy(2, 20, 20, 20, 3*time.Second)
		limiter := newTestLimiter(t, client, p, p)
		base := testInput("email-fixed", "198.51.100.1")
		for i := 0; i < 2; i++ {
			input := testInput(fmt.Sprintf("other-%d", i), fmt.Sprintf("198.51.100.%d", 10+i))
			input.EmailIndex = base.EmailIndex
			if err := limiter.Allow(context.Background(), EmailChallenge, input); err != nil {
				t.Fatal("request under email limit was denied")
			}
		}
		input := testInput("other-3", "198.51.100.13")
		input.EmailIndex = base.EmailIndex
		if !errors.Is(limiter.Allow(context.Background(), EmailChallenge, input), ErrLimited) {
			t.Fatal("email threshold was not enforced")
		}
	})

	t.Run("device dimension", func(t *testing.T) {
		p := policy(20, 2, 20, 20, 3*time.Second)
		limiter := newTestLimiter(t, client, p, p)
		base := testInput("device-base", "198.51.100.1")
		for i := 0; i < 2; i++ {
			input := testInput(fmt.Sprintf("device-email-%d", i), fmt.Sprintf("198.51.100.%d", 20+i))
			input.DeviceFingerprint = base.DeviceFingerprint
			if err := limiter.Allow(context.Background(), EmailLogin, input); err != nil {
				t.Fatal("request under device limit was denied")
			}
		}
		input := testInput("device-email-3", "198.51.100.23")
		input.DeviceFingerprint = base.DeviceFingerprint
		if !errors.Is(limiter.Allow(context.Background(), EmailLogin, input), ErrLimited) {
			t.Fatal("device threshold was not enforced")
		}
	})

	t.Run("trusted IPv4 host dimension", func(t *testing.T) {
		p := policy(20, 20, 2, 20, 3*time.Second)
		limiter := newTestLimiter(t, client, p, p)
		for i := 1; i <= 2; i++ {
			input := testInput(fmt.Sprintf("ip-email-%d", i), "198.51.100.1")
			if err := limiter.Allow(context.Background(), EmailChallenge, input); err != nil {
				t.Fatal("request under IP limit was denied")
			}
		}
		if !errors.Is(limiter.Allow(context.Background(), EmailChallenge, testInput("ip-email-3", "198.51.100.1")), ErrLimited) {
			t.Fatal("IPv4 host threshold was not enforced")
		}
	})

	t.Run("global threshold", func(t *testing.T) {
		p := policy(20, 20, 20, 3, 3*time.Second)
		limiter := newTestLimiter(t, client, p, p)
		for i := 0; i < 3; i++ {
			if err := limiter.Allow(context.Background(), EmailChallenge, testInput(fmt.Sprintf("global-%d", i), "203.0.113.9")); err != nil {
				t.Fatal("request under global limit was denied")
			}
		}
		if !errors.Is(limiter.Allow(context.Background(), EmailChallenge, testInput("global-3", "203.0.113.9")), ErrLimited) {
			t.Fatal("global threshold was not enforced")
		}
	})

	t.Run("denial does not partially consume other dimensions", func(t *testing.T) {
		config := validConfig(t)
		config.EmailChallenge = policy(20, 20, 20, 1, 3*time.Second)
		config.EmailLogin = config.EmailChallenge
		blockedLimiter, err := New(client, config)
		if err != nil {
			t.Fatal("limiter construction failed")
		}
		first := testInput("no-partial-1", "203.0.113.1")
		second := testInput("no-partial-2", "203.0.114.2")
		if err := blockedLimiter.Allow(context.Background(), EmailChallenge, first); err != nil {
			t.Fatal("first request was denied")
		}
		if !errors.Is(blockedLimiter.Allow(context.Background(), EmailChallenge, second), ErrLimited) {
			t.Fatal("global rejection did not occur")
		}
		config.EmailChallenge = policy(1, 1, 1, 20, 3*time.Second)
		config.EmailLogin = config.EmailChallenge
		probeLimiter, err := New(client, config)
		if err != nil {
			t.Fatal("probe limiter construction failed")
		}
		if err := probeLimiter.Allow(context.Background(), EmailChallenge, second); err != nil {
			t.Fatal("denied request partially consumed email, device or IP counters")
		}
	})

	t.Run("window expiry", func(t *testing.T) {
		p := policy(1, 1, 1, 1, 200*time.Millisecond)
		limiter := newTestLimiter(t, client, p, p)
		input := testInput("ttl-expiry", "192.0.2.7")
		if err := limiter.Allow(context.Background(), EmailChallenge, input); err != nil {
			t.Fatal("first request was denied")
		}
		if !errors.Is(limiter.Allow(context.Background(), EmailChallenge, input), ErrLimited) {
			t.Fatal("window did not enforce threshold")
		}
		time.Sleep(250 * time.Millisecond)
		if err := limiter.Allow(context.Background(), EmailChallenge, input); err != nil {
			t.Fatal("expired window did not permit new request")
		}
	})

	t.Run("concurrent global bound", func(t *testing.T) {
		p := policy(1000, 1000, 1000, 17, 5*time.Second)
		limiter := newTestLimiter(t, client, p, p)
		const attempts = 100
		var wg sync.WaitGroup
		var mu sync.Mutex
		allowed, limited, failed := 0, 0, 0
		for i := 0; i < attempts; i++ {
			wg.Add(1)
			go func(index int) {
				defer wg.Done()
				err := limiter.Allow(context.Background(), EmailLogin, testInput(fmt.Sprintf("parallel-%d", index), "192.0.2.99"))
				mu.Lock()
				defer mu.Unlock()
				switch {
				case err == nil:
					allowed++
				case errors.Is(err, ErrLimited):
					limited++
				default:
					failed++
				}
			}(i)
		}
		wg.Wait()
		if allowed != 17 || limited != attempts-17 || failed != 0 {
			t.Fatalf("unexpected concurrency outcome: allowed=%d limited=%d failed=%d", allowed, limited, failed)
		}
	})

	t.Run("malformed and persistent counters fail closed without partial writes", func(t *testing.T) {
		p := policy(20, 20, 20, 20, time.Second)
		limiter := newTestLimiter(t, client, p, p)
		for _, setup := range []struct {
			name  string
			value string
			ttl   time.Duration
			kind  string
		}{
			{"persistent", "1", 0, "string"},
			{"bad counter", "not-a-counter", time.Second, "string"},
			{"noncanonical counter", "01", time.Second, "string"},
			{"wrong type", "list-value", time.Second, "list"},
			{"ttl exceeds configured window", "1", 10 * time.Second, "string"},
		} {
			t.Run(setup.name, func(t *testing.T) {
				input := testInput("corrupt-"+setup.name, "203.0.113.88")
				keys, _, err := limiter.redisKeys(EmailChallenge, input)
				if err != nil {
					t.Fatal("test input was invalid")
				}
				switch setup.kind {
				case "list":
					if err := client.RPush(context.Background(), keys[2], setup.value).Err(); err != nil {
						t.Fatal("failed to seed isolated wrong-type key")
					}
					if setup.ttl > 0 {
						if err := client.PExpire(context.Background(), keys[2], setup.ttl).Err(); err != nil {
							t.Fatal("failed to expire isolated wrong-type key")
						}
					}
				default:
					if err := client.Set(context.Background(), keys[2], setup.value, setup.ttl).Err(); err != nil {
						t.Fatal("failed to seed isolated counter")
					}
				}
				if !errors.Is(limiter.Allow(context.Background(), EmailChallenge, input), ErrUnavailable) {
					t.Fatal("invalid Redis counter was not rejected as unavailable")
				}
				for _, key := range []string{keys[0], keys[1], keys[3]} {
					if client.Exists(context.Background(), key).Val() != 0 {
						t.Fatal("invalid counter caused partial writes")
					}
				}
				_ = client.Del(context.Background(), keys...).Err()
			})
		}
	})

	t.Run("Redis unavailable fails closed without exposing cause", func(t *testing.T) {
		closed := redis.NewClient(&redis.Options{Network: "unix", Addr: socket})
		if err := closed.Close(); err != nil {
			t.Fatal("could not close isolated test client")
		}
		limiter := newTestLimiter(t, closed, policy(2, 2, 2, 2, time.Second), policy(2, 2, 2, 2, time.Second))
		err := limiter.Allow(context.Background(), EmailChallenge, testInput("unavailable", "192.0.2.88"))
		if !errors.Is(err, ErrUnavailable) || contains(err.Error(), "closed") || contains(err.Error(), socket) {
			t.Fatal("Redis failure was not mapped to a redacted unavailable error")
		}
	})
}

func newTestLimiter(t *testing.T, client redis.Scripter, challenge, login Policy) *Limiter {
	t.Helper()
	config := validConfig(t)
	config.EmailChallenge = challenge
	config.EmailLogin = login
	limiter, err := New(client, config)
	if err != nil {
		t.Fatal("limiter construction failed")
	}
	return limiter
}

func validConfig(t *testing.T) Config {
	t.Helper()
	secret := func() []byte {
		value := make([]byte, 32)
		if _, err := rand.Read(value); err != nil {
			t.Fatal("test HMAC secret generation failed")
		}
		return value
	}
	p := policy(5, 5, 5, 10, time.Second)
	return Config{Keys: HMACKeys{EmailIndex: secret(), DeviceFingerprint: secret(), TrustedIP: secret(), Global: secret()},
		IPv6PrefixBits: 64, EmailChallenge: p, EmailLogin: p}
}

func policy(email, device, ip, global uint64, window time.Duration) Policy {
	return Policy{Email: DimensionPolicy{email, window}, Device: DimensionPolicy{device, window},
		IP: DimensionPolicy{ip, window}, Global: DimensionPolicy{global, window}}
}

func testInput(seed, address string) Input {
	email := sha256.Sum256([]byte("email:" + seed))
	device := sha256.Sum256([]byte("device:" + seed))
	return Input{EmailIndex: email, DeviceFingerprint: device, SourceIP: netip.MustParseAddr(address)}
}

func assertPrivateRedisSocket(t *testing.T, socket string) {
	t.Helper()
	absolute, err := filepath.Abs(socket)
	if err != nil || absolute != socket {
		t.Fatal("Redis test socket path must be absolute")
	}
	parent := filepath.Dir(socket)
	resolved, err := filepath.EvalSymlinks(parent)
	info, statErr := os.Lstat(parent)
	socketInfo, socketErr := os.Lstat(socket)
	if err != nil || statErr != nil || socketErr != nil || filepath.Dir(resolved) != "/private/tmp" ||
		!stringsHasPrefix(filepath.Base(resolved), "nerae-email-ratelimit-") || !info.IsDir() || info.Mode().Perm()&0077 != 0 ||
		info.Mode()&os.ModeSymlink != 0 || filepath.Base(socket) != "redis.sock" || socketInfo.Mode()&os.ModeSocket == 0 {
		t.Fatal("refusing a non-isolated Redis target")
	}
}

func stringsHasPrefix(value, prefix string) bool {
	return len(value) >= len(prefix) && value[:len(prefix)] == prefix
}
func contains(value, part string) bool {
	return len(part) == 0 || len(value) >= len(part) && stringContains(value, part)
}

func stringContains(value, part string) bool {
	for i := 0; i+len(part) <= len(value); i++ {
		if value[i:i+len(part)] == part {
			return true
		}
	}
	return false
}
