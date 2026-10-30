package application

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"nerae/api/internal/modules/auth/approval"
	authbootstraptest "nerae/api/internal/modules/auth/bootstrap/testsupport"
	"nerae/api/internal/modules/auth/challenge"
	"nerae/api/internal/modules/auth/domain"
	"nerae/api/internal/modules/auth/identity"
	"nerae/api/internal/modules/auth/loginledger"
	"nerae/api/internal/modules/auth/loginreceipt"
	"nerae/api/internal/modules/auth/ratelimit"
	"nerae/api/internal/modules/auth/repository"
	"nerae/api/internal/modules/auth/session"
)

func TestEmailLoginCoordinatorWithPrivateMySQLAndRedis(t *testing.T) {
	db := emailFlowDB(t)
	redisSocket := os.Getenv("NERAE_EMAIL_COORDINATOR_REDIS_SOCKET")
	if redisSocket == "" {
		t.Skip("set NERAE_EMAIL_COORDINATOR_REDIS_SOCKET using the private Redis harness")
	}
	assertCoordinatorRedisSocket(t, redisSocket)
	rdb := redis.NewClient(&redis.Options{Network: "unix", Addr: redisSocket, DialTimeout: time.Second, ReadTimeout: 2 * time.Second, WriteTimeout: 2 * time.Second})
	if err := rdb.Ping(context.Background()).Err(); err != nil {
		t.Fatal("private Redis ping failed")
	}
	t.Cleanup(func() { _ = rdb.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	hasher, err := domain.NewEmailCodeHasher(bytes.Repeat([]byte{1}, 32))
	if err != nil {
		t.Fatal("code hasher initialization failed")
	}
	replay, err := repository.NewProofReplayStore(bytes.Repeat([]byte{2}, 32))
	if err != nil {
		t.Fatal("proof replay initialization failed")
	}
	protector, err := domain.NewEmailIdentityProtector(bytes.Repeat([]byte{3}, 32), bytes.Repeat([]byte{4}, 32), "email-test-key")
	if err != nil {
		t.Fatal("identity protector initialization failed")
	}
	challenges, err := challenge.NewStore(hasher, replay, protector, challenge.Policy{ChallengeTTL: 5 * time.Minute,
		MaxAttempts: 3, DeviceProof: domain.ProofPolicy{MaxLifetime: 5 * time.Minute, ClockSkew: 30 * time.Second}})
	if err != nil {
		t.Fatal("challenge store initialization failed")
	}
	authbootstraptest.InitializeIdentityEpoch(t, db, protector)
	resolver, err := identity.NewResolver(protector)
	if err != nil {
		t.Fatal("identity resolver initialization failed")
	}
	codec, err := session.NewTokenCodec(bytes.Repeat([]byte{5}, 32), session.TokenPolicy{MaxLifetime: time.Hour})
	if err != nil {
		t.Fatal("token codec initialization failed")
	}
	sessions, err := session.NewStore(resolver, codec)
	if err != nil {
		t.Fatal("session store initialization failed")
	}
	approvals, err := approval.NewStore(resolver, bytes.Repeat([]byte{6}, 32), "approval-test-key", approval.Policy{Lifetime: 5 * time.Minute, ReauthWindow: time.Minute})
	if err != nil {
		t.Fatal("approval store initialization failed")
	}
	crypto, err := loginreceipt.New(loginreceipt.KeyConfig{IdempotencyHMACKey: bytes.Repeat([]byte{7}, 32),
		RequestHMACKey: bytes.Repeat([]byte{8}, 32), AES256GCMKey: bytes.Repeat([]byte{9}, 32)})
	if err != nil {
		t.Fatal("receipt crypto initialization failed")
	}
	ledger, err := loginledger.NewStore(crypto, protector, "receipt-test-key")
	if err != nil {
		t.Fatal("receipt ledger initialization failed")
	}
	initTx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal("initialization transaction failed")
	}
	if err := approvals.InitializeKey(ctx, initTx); err != nil {
		_ = initTx.Rollback()
		t.Fatal("approval epoch initialization failed")
	}
	if err := ledger.Initialize(ctx, initTx); err != nil {
		_ = initTx.Rollback()
		t.Fatal("receipt epoch initialization failed")
	}
	if err := initTx.Commit(); err != nil {
		t.Fatal("initialization commit failed")
	}
	login, err := NewEmailLogin(challenges, sessions, approvals)
	if err != nil {
		t.Fatal("email login construction failed")
	}
	receipt, err := NewReceiptLogin(login, ledger, crypto, 2*time.Minute)
	if err != nil {
		t.Fatal("receipt login construction failed")
	}

	newLimiterWithWindow := func(email, device, ip, global uint64, window time.Duration) *ratelimit.Limiter {
		t.Helper()
		secret := func() []byte {
			value := make([]byte, 32)
			if _, err := rand.Read(value); err != nil {
				t.Fatal("limiter key generation failed")
			}
			return value
		}
		policy := ratelimit.Policy{Email: ratelimit.DimensionPolicy{Limit: email, Window: window},
			Device: ratelimit.DimensionPolicy{Limit: device, Window: window},
			IP:     ratelimit.DimensionPolicy{Limit: ip, Window: window},
			Global: ratelimit.DimensionPolicy{Limit: global, Window: window}}
		limiter, err := ratelimit.New(rdb, ratelimit.Config{Keys: ratelimit.HMACKeys{EmailIndex: secret(), DeviceFingerprint: secret(),
			TrustedIP: secret(), Global: secret()}, IPv6PrefixBits: 64, EmailLogin: policy, EmailChallenge: policy})
		if err != nil {
			t.Fatal("rate limiter construction failed")
		}
		return limiter
	}
	newLimiter := func(email, device, ip, global uint64) *ratelimit.Limiter {
		return newLimiterWithWindow(email, device, ip, global, time.Minute)
	}
	newCoordinator := func(limiter *ratelimit.Limiter) *EmailLoginCoordinator {
		t.Helper()
		coordinator, err := NewEmailLoginCoordinator(challenges, limiter, receipt)
		if err != nil {
			t.Fatal("coordinator construction failed")
		}
		return coordinator
	}
	issue := func(email string, public domain.DevicePublicJWK) (string, string) {
		t.Helper()
		id, err := domain.GenerateChallengeID()
		if err != nil {
			t.Fatal("challenge id generation failed")
		}
		nonce, err := domain.GenerateChallengeNonce()
		if err != nil {
			t.Fatal("challenge nonce generation failed")
		}
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal("challenge issue transaction failed")
		}
		if err := challenges.Issue(ctx, tx, challenge.IssueInput{Email: email, ChallengeID: id, Nonce: nonce, Code: "012345", PublicKey: public}); err != nil {
			_ = tx.Rollback()
			t.Fatal("challenge issue failed")
		}
		if err := tx.Commit(); err != nil {
			t.Fatal("challenge issue commit failed")
		}
		return id, nonce
	}
	invoke := func(coordinator *EmailLoginCoordinator, key string, input challenge.AttemptInput, ip netip.Addr) (Outcome, error) {
		t.Helper()
		return coordinator.VerifyEmail(ctx, db, key, input, ip)
	}
	count := func(query, arg string) int {
		t.Helper()
		var value int
		if err := db.QueryRowContext(ctx, query, arg).Scan(&value); err != nil {
			t.Fatal("database state inspection failed")
		}
		return value
	}

	t.Run("entry quota blocks before database access", func(t *testing.T) {
		limiter := newLimiter(10, 10, 1, 1)
		ip := netip.MustParseAddr("192.0.2.201")
		if err := limiter.AllowLoginEntry(ctx, ip); err != nil {
			t.Fatal("could not seed isolated entry budget")
		}
		coordinator := newCoordinator(limiter)
		_, err := coordinator.VerifyEmail(ctx, nil, "AQEBAQEBAQEBAQEBAQEBAQ", challenge.AttemptInput{
			ChallengeID: strings.Repeat("A", 32), Code: "012345", DeviceProof: "opaque"}, ip)
		if !errors.Is(err, ratelimit.ErrLimited) {
			t.Fatal("entry rejection did not happen before the nil database could be touched")
		}
	})

	t.Run("unknown challenge is generic rejection and Redis failure closes", func(t *testing.T) {
		coordinator := newCoordinator(newLimiter(100, 100, 100, 100))
		_, err := invoke(coordinator, "AgICAgICAgICAgICAgICAg", challenge.AttemptInput{
			ChallengeID: strings.Repeat("Z", 32), Code: "012345", DeviceProof: "opaque"}, netip.MustParseAddr("192.0.2.202"))
		if !errors.Is(err, ErrRejected) {
			t.Fatal("unknown challenge did not map to a generic rejection")
		}
		closed := redis.NewClient(&redis.Options{Network: "unix", Addr: redisSocket})
		if err := closed.Close(); err != nil {
			t.Fatal("could not close isolated Redis client")
		}
		failClosedLimiter, err := ratelimit.New(closed, ratelimit.Config{Keys: ratelimit.HMACKeys{
			EmailIndex: bytes.Repeat([]byte{11}, 32), DeviceFingerprint: bytes.Repeat([]byte{12}, 32),
			TrustedIP: bytes.Repeat([]byte{13}, 32), Global: bytes.Repeat([]byte{14}, 32)}, IPv6PrefixBits: 64,
			EmailLogin: ratelimit.Policy{Email: ratelimit.DimensionPolicy{Limit: 1, Window: time.Minute},
				Device: ratelimit.DimensionPolicy{Limit: 1, Window: time.Minute}, IP: ratelimit.DimensionPolicy{Limit: 1, Window: time.Minute},
				Global: ratelimit.DimensionPolicy{Limit: 1, Window: time.Minute}},
			EmailChallenge: ratelimit.Policy{Email: ratelimit.DimensionPolicy{Limit: 1, Window: time.Minute},
				Device: ratelimit.DimensionPolicy{Limit: 1, Window: time.Minute}, IP: ratelimit.DimensionPolicy{Limit: 1, Window: time.Minute},
				Global: ratelimit.DimensionPolicy{Limit: 1, Window: time.Minute}}})
		if err != nil {
			t.Fatal("closed limiter construction failed unexpectedly")
		}
		_, err = newCoordinator(failClosedLimiter).VerifyEmail(ctx, db, "AwMDAwMDAwMDAwMDAwMDAw", challenge.AttemptInput{}, netip.MustParseAddr("192.0.2.203"))
		if !errors.Is(err, ErrUnavailable) || !errors.Is(err, ratelimit.ErrUnavailable) {
			t.Fatal("Redis failure did not retain safe unavailable classifications")
		}
	})

	coordinator := newCoordinator(newLimiter(100, 100, 100, 500))
	t.Run("success same-key retry and changed request conflict", func(t *testing.T) {
		private, public := flowDevice(t)
		id, nonce := issue("coordinator-success@example.test", public)
		input := challenge.AttemptInput{ChallengeID: id, Code: "012345", DeviceProof: flowProof(t, private, id, nonce)}
		const key = "BAQEBAQEBAQEBAQEBAQEBA"
		first, err := invoke(coordinator, key, input, netip.MustParseAddr("192.0.2.204"))
		firstToken, ok := first.SessionTokenForResponse()
		if err != nil || first.Kind() != Authenticated || !ok || firstToken == "" {
			t.Fatal("coordinator did not release a committed authenticated result")
		}
		retry, err := invoke(coordinator, key, input, netip.MustParseAddr("192.0.2.204"))
		retryToken, retryOK := retry.SessionTokenForResponse()
		if err != nil || !retryOK || retryToken != firstToken {
			t.Fatal("same-key retry did not return the original authorized token")
		}
		changed := input
		changed.Code = "999999"
		if _, err := invoke(coordinator, key, changed, netip.MustParseAddr("192.0.2.204")); !errors.Is(err, ErrReceiptConflict) {
			t.Fatal("changed request with a live key did not preserve the receipt conflict classification")
		}
	})

	t.Run("rate-limited same-key retry recovers after fixed window", func(t *testing.T) {
		const window = 250 * time.Millisecond
		limiter := newLimiterWithWindow(1, 100, 100, 100, window)
		coordinator := newCoordinator(limiter)
		private, public := flowDevice(t)
		id, nonce := issue("coordinator-window-retry@example.test", public)
		input := challenge.AttemptInput{ChallengeID: id, Code: "012345", DeviceProof: flowProof(t, private, id, nonce)}
		const key = "Dw8PDw8PDw8PDw8PDw8PDw"
		ip := netip.MustParseAddr("192.0.2.212")
		first, err := invoke(coordinator, key, input, ip)
		firstToken, ok := first.SessionTokenForResponse()
		if err != nil || first.Kind() != Authenticated || !ok || firstToken == "" {
			t.Fatal("initial request did not create and release one committed session")
		}
		if _, err := invoke(coordinator, key, input, ip); !errors.Is(err, ratelimit.ErrLimited) {
			t.Fatal("immediate same-key replay did not respect the email rate limit")
		}
		if got := count(`SELECT COUNT(*) FROM auth_session WHERE login_challenge_id=?`, id); got != 1 {
			t.Fatal("rate-limited replay changed the existing session count")
		}
		time.Sleep(window + 150*time.Millisecond)
		recovered, err := invoke(coordinator, key, input, ip)
		recoveredToken, recoveredOK := recovered.SessionTokenForResponse()
		if err != nil || !recoveredOK || recoveredToken != firstToken {
			t.Fatal("same request did not recover its original token after the quota window")
		}
		if got := count(`SELECT COUNT(*) FROM auth_session WHERE login_challenge_id=?`, id); got != 1 {
			t.Fatal("post-window retry created a new session")
		}
	})

	t.Run("new device returns approval capability but never a session", func(t *testing.T) {
		firstPrivate, firstPublic := flowDevice(t)
		firstID, firstNonce := issue("coordinator-approval@example.test", firstPublic)
		firstInput := challenge.AttemptInput{ChallengeID: firstID, Code: "012345", DeviceProof: flowProof(t, firstPrivate, firstID, firstNonce)}
		if outcome, err := invoke(coordinator, "DQwMDAwMDAwMDAwMDAwMDAw", firstInput, netip.MustParseAddr("192.0.2.210")); err != nil || outcome.Kind() != Authenticated {
			t.Fatal("initial trusted device login failed")
		}
		newPrivate, newPublic := flowDevice(t)
		newID, newNonce := issue("coordinator-approval@example.test", newPublic)
		newInput := challenge.AttemptInput{ChallengeID: newID, Code: "012345", DeviceProof: flowProof(t, newPrivate, newID, newNonce)}
		const key = "Dg4ODg4ODg4ODg4ODg4ODg"
		pending, err := invoke(coordinator, key, newInput, netip.MustParseAddr("192.0.2.211"))
		data, ok := pending.PendingApprovalForResponse()
		if err != nil || pending.Kind() != ApprovalRequired || !ok || data.ID == "" || data.Secret == "" {
			t.Fatal("new device did not receive its committed approval capability")
		}
		if _, ok := pending.SessionTokenForResponse(); ok {
			t.Fatal("new device received a session before approval")
		}
		retried, err := invoke(coordinator, key, newInput, netip.MustParseAddr("192.0.2.211"))
		retryData, retryOK := retried.PendingApprovalForResponse()
		if err != nil || !retryOK || retryData.ID != data.ID || retryData.Secret != data.Secret {
			t.Fatal("same-key approval retry did not preserve the original capability")
		}
	})

	t.Run("wrong code commits once, retry stays rejected, invalid proof rolls back", func(t *testing.T) {
		private, public := flowDevice(t)
		id, nonce := issue("coordinator-wrong@example.test", public)
		wrong := challenge.AttemptInput{ChallengeID: id, Code: "999999", DeviceProof: flowProof(t, private, id, nonce)}
		const key = "BQUFBQUFBQUFBQUFBQUFBQ"
		for i := 0; i < 2; i++ {
			if _, err := invoke(coordinator, key, wrong, netip.MustParseAddr("192.0.2.205")); !errors.Is(err, ErrRejected) {
				t.Fatal("wrong code or exact retry was not rejected generically")
			}
		}
		if count(`SELECT attempts FROM auth_email_login_challenge WHERE challenge_id=?`, id) != 1 ||
			count(`SELECT COUNT(*) FROM auth_email_login_receipt WHERE challenge_id=? AND result_kind='rejected'`, id) != 1 {
			t.Fatal("wrong-code attempt or rejection receipt did not commit exactly once")
		}
		invalidID, invalidNonce := issue("coordinator-invalid-proof@example.test", public)
		wrongSigner, _ := flowDevice(t)
		invalid := challenge.AttemptInput{ChallengeID: invalidID, Code: "012345", DeviceProof: flowProof(t, wrongSigner, invalidID, invalidNonce)}
		if _, err := invoke(coordinator, "BgYGBgYGBgYGBgYGBgYGBg", invalid, netip.MustParseAddr("192.0.2.206")); !errors.Is(err, ErrRejected) {
			t.Fatalf("invalid proof did not map to a generic rejection: %v", err)
		}
		if count(`SELECT attempts FROM auth_email_login_challenge WHERE challenge_id=?`, invalidID) != 0 ||
			count(`SELECT COUNT(*) FROM auth_email_login_receipt WHERE challenge_id=?`, invalidID) != 0 {
			t.Fatal("invalid proof left an attempt or receipt write behind")
		}
	})

	t.Run("known email/device quota blocks before login transaction", func(t *testing.T) {
		limiter := newLimiter(1, 100, 100, 100)
		coordinator := newCoordinator(limiter)
		private, public := flowDevice(t)
		firstID, firstNonce := issue("coordinator-limited@example.test", public)
		firstInput := challenge.AttemptInput{ChallengeID: firstID, Code: "999999", DeviceProof: flowProof(t, private, firstID, firstNonce)}
		if _, err := invoke(coordinator, "CAgICAgICAgICAgICAgICAg", firstInput, netip.MustParseAddr("192.0.2.207")); !errors.Is(err, ErrRejected) {
			t.Fatal("first request did not proceed to the incorrect-code result")
		}
		secondID, secondNonce := issue("coordinator-limited@example.test", public)
		secondInput := challenge.AttemptInput{ChallengeID: secondID, Code: "012345", DeviceProof: flowProof(t, private, secondID, secondNonce)}
		if _, err := invoke(coordinator, "CQkJCQkJCQkJCQkJCQkJCQ", secondInput, netip.MustParseAddr("192.0.2.208")); !errors.Is(err, ratelimit.ErrLimited) {
			t.Fatal("email dimension limit did not stop the known challenge before login transaction")
		}
		if count(`SELECT attempts FROM auth_email_login_challenge WHERE challenge_id=?`, secondID) != 0 {
			t.Fatal("identity-dimension rejection still executed the login transaction")
		}
	})

	t.Run("same-key concurrent retries return one committed token", func(t *testing.T) {
		private, public := flowDevice(t)
		id, nonce := issue("coordinator-concurrent@example.test", public)
		input := challenge.AttemptInput{ChallengeID: id, Code: "012345", DeviceProof: flowProof(t, private, id, nonce)}
		const key = "DAwMDAwMDAwMDAwMDAwMDA"
		type result struct {
			outcome Outcome
			err     error
		}
		start := make(chan struct{})
		results := make(chan result, 2)
		var workers sync.WaitGroup
		for i := 0; i < 2; i++ {
			workers.Add(1)
			go func() {
				defer workers.Done()
				<-start
				outcome, err := invoke(coordinator, key, input, netip.MustParseAddr("192.0.2.209"))
				results <- result{outcome: outcome, err: err}
			}()
		}
		close(start)
		workers.Wait()
		close(results)
		tokens := make([]string, 0, 2)
		for item := range results {
			token, ok := item.outcome.SessionTokenForResponse()
			if item.err != nil || !ok || token == "" {
				t.Fatal("concurrent exact retry did not return a committed token")
			}
			tokens = append(tokens, token)
		}
		if len(tokens) != 2 || tokens[0] != tokens[1] ||
			count(`SELECT COUNT(*) FROM auth_session WHERE login_challenge_id=?`, id) != 1 {
			t.Fatal("concurrent retries created different credentials or sessions")
		}
	})
}

func assertCoordinatorRedisSocket(t *testing.T, socket string) {
	t.Helper()
	absolute, err := filepath.Abs(socket)
	parent := filepath.Dir(socket)
	resolved, resolveErr := filepath.EvalSymlinks(parent)
	info, dirErr := os.Lstat(parent)
	socketInfo, socketErr := os.Lstat(socket)
	if err != nil || absolute != socket || resolveErr != nil || dirErr != nil || socketErr != nil ||
		filepath.Dir(resolved) != "/private/tmp" || !strings.HasPrefix(filepath.Base(resolved), "nerae-email-flow-mysql-") ||
		!info.IsDir() || info.Mode().Perm()&0077 != 0 || info.Mode()&os.ModeSymlink != 0 ||
		filepath.Base(socket) != "redis.sock" || socketInfo.Mode()&os.ModeSocket == 0 {
		t.Fatal("refusing non-isolated coordinator Redis target")
	}
}
