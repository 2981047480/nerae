package main

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"errors"
	"log/slog"
	"net/http"
	"net/netip"
	"time"

	"github.com/redis/go-redis/v9"
	"nerae/api/internal/middleware"
	authapp "nerae/api/internal/modules/auth/application"
	"nerae/api/internal/modules/auth/approval"
	"nerae/api/internal/modules/auth/bootstrap"
	"nerae/api/internal/modules/auth/challenge"
	"nerae/api/internal/modules/auth/codeepoch"
	"nerae/api/internal/modules/auth/domain"
	"nerae/api/internal/modules/auth/identity"
	"nerae/api/internal/modules/auth/issuance"
	"nerae/api/internal/modules/auth/loginledger"
	"nerae/api/internal/modules/auth/loginreceipt"
	"nerae/api/internal/modules/auth/ratelimit"
	"nerae/api/internal/modules/auth/repository"
	"nerae/api/internal/modules/auth/session"
	authtransport "nerae/api/internal/modules/auth/transport"
	"nerae/api/internal/modules/auth/trustedproxy"
	workoutapp "nerae/api/internal/modules/workout/application"
	workoutrepo "nerae/api/internal/modules/workout/repository"
	workouttransport "nerae/api/internal/modules/workout/transport"
)

var errBusinessStartup = errors.New("business startup unavailable")

// businessKeys are supplied by protected server configuration. Values are
// never accepted from HTTP requests, included in logs, or given defaults.
type businessKeys struct {
	identityIndex, identityCipher, codeHash, codeDerive                                        []byte
	proofReplay, sessionToken, approval                                                        []byte
	challengeIdempotency, challengeRequest, challengeCipher                                    []byte
	loginIdempotency, loginRequest, loginCipher                                                []byte
	rateEmail, rateDevice, rateIP, rateGlobal                                                  []byte
	workoutName, workoutHash, workoutCipher                                                    []byte
	identityID, codeID, codeHashID, approvalID, challengeID, loginID, workoutNameID, workoutID string
}

func (businessKeys) String() string               { return "BusinessKeys{redacted}" }
func (businessKeys) GoString() string             { return "BusinessKeys{redacted}" }
func (businessKeys) MarshalJSON() ([]byte, error) { return nil, errBusinessStartup }
func (businessKeys) LogValue() slog.Value         { return slog.StringValue("BusinessKeys{redacted}") }

// distinctKeys does not attest to entropy. Provisioning must generate each key
// with a CSPRNG; rejecting missing, all-zero, or reused bytes prevents common
// configuration mistakes and accidental cross-purpose key sharing.
func (k businessKeys) distinctKeys() bool {
	keys := [][]byte{k.identityIndex, k.identityCipher, k.codeHash, k.codeDerive,
		k.proofReplay, k.sessionToken, k.approval, k.challengeIdempotency,
		k.challengeRequest, k.challengeCipher, k.loginIdempotency, k.loginRequest,
		k.loginCipher, k.rateEmail, k.rateDevice, k.rateIP, k.rateGlobal,
		k.workoutName, k.workoutHash, k.workoutCipher}
	zero := make([]byte, 32)
	for i, key := range keys {
		if len(key) != 32 || subtle.ConstantTimeCompare(key, zero) == 1 {
			return false
		}
		for _, prior := range keys[:i] {
			if subtle.ConstantTimeCompare(key, prior) == 1 {
				return false
			}
		}
	}
	return true
}

type businessPolicy struct {
	challenge                                                             challenge.Policy
	rate                                                                  ratelimit.Config
	approvalRate                                                          ratelimit.ApprovalDecisionPolicy
	challengeRetention, loginRetention, sessionLifetime, workoutRetention time.Duration
	approval                                                              approval.Policy
	codeDigits                                                            int
}

// buildBusinessHandler assembles the existing real handlers and initializes
// their epochs before returning a handler. It deliberately does not open a
// listener or mount itself in the production router.
func buildBusinessHandler(ctx context.Context, db *sql.DB, rdb *redis.Client, keys businessKeys, policy businessPolicy, proxyPrefixes []netip.Prefix) (http.Handler, error) {
	if ctx == nil || db == nil || rdb == nil || !keys.distinctKeys() {
		return nil, errBusinessStartup
	}
	ip, err := trustedproxy.NewResolver(proxyPrefixes)
	if err != nil || rdb.Ping(ctx).Err() != nil {
		return nil, errBusinessStartup
	}
	identities, err := domain.NewEmailIdentityProtector(keys.identityIndex, keys.identityCipher, keys.identityID)
	if err != nil {
		return nil, errBusinessStartup
	}
	hasher, err := domain.NewEmailCodeHasher(keys.codeHash)
	if err != nil {
		return nil, errBusinessStartup
	}
	proofReplay, err := repository.NewProofReplayStore(keys.proofReplay)
	if err != nil {
		return nil, errBusinessStartup
	}
	hashEpoch, err := codeepoch.New(hasher, keys.codeHashID)
	if err != nil {
		return nil, errBusinessStartup
	}
	challenges, err := challenge.NewStore(hasher, proofReplay, identities, keys.codeHashID, policy.challenge)
	if err != nil {
		return nil, errBusinessStartup
	}
	deriver, err := issuance.NewDeriver(keys.codeDerive)
	if err != nil {
		return nil, errBusinessStartup
	}
	producer, err := issuance.NewEnqueueOutbox(deriver, keys.codeID, hasher, keys.codeHashID, identities)
	if err != nil {
		return nil, errBusinessStartup
	}
	policy.rate.Keys = ratelimit.HMACKeys{EmailIndex: keys.rateEmail, DeviceFingerprint: keys.rateDevice, TrustedIP: keys.rateIP, Global: keys.rateGlobal}
	limiter, err := ratelimit.New(rdb, policy.rate)
	if err != nil {
		return nil, errBusinessStartup
	}
	issuer, err := issuance.NewChallengeRequestIssuer(challenges, producer, identities, limiter, issuance.RequestKeyConfig{
		IdempotencyHMACKey: keys.challengeIdempotency, RequestHMACKey: keys.challengeRequest,
		ResponseAESKey: keys.challengeCipher, KeyID: keys.challengeID, Retention: policy.challengeRetention, CodeDigits: policy.codeDigits,
	})
	if err != nil {
		return nil, errBusinessStartup
	}
	resolver, err := identity.NewResolver(identities)
	if err != nil {
		return nil, errBusinessStartup
	}
	codec, err := session.NewTokenCodec(keys.sessionToken, session.TokenPolicy{MaxLifetime: policy.sessionLifetime})
	if err != nil {
		return nil, errBusinessStartup
	}
	sessions, err := session.NewStore(resolver, codec)
	if err != nil {
		return nil, errBusinessStartup
	}
	approvals, err := approval.NewStore(resolver, keys.approval, keys.approvalID, policy.approval)
	if err != nil {
		return nil, errBusinessStartup
	}
	receiptCrypto, err := loginreceipt.New(loginreceipt.KeyConfig{IdempotencyHMACKey: keys.loginIdempotency,
		RequestHMACKey: keys.loginRequest, AES256GCMKey: keys.loginCipher})
	if err != nil {
		return nil, errBusinessStartup
	}
	ledger, err := loginledger.NewStore(receiptCrypto, identities, keys.loginID)
	if err != nil {
		return nil, errBusinessStartup
	}
	login, err := authapp.NewEmailLogin(challenges, sessions, approvals)
	if err != nil {
		return nil, errBusinessStartup
	}
	receipt, err := authapp.NewReceiptLogin(login, ledger, receiptCrypto, policy.loginRetention)
	if err != nil {
		return nil, errBusinessStartup
	}
	coordinator, err := authapp.NewEmailLoginCoordinator(challenges, limiter, receipt)
	if err != nil {
		return nil, errBusinessStartup
	}
	validator, err := middleware.NewSchemaValidator(4<<20, 100000)
	if err != nil {
		return nil, errBusinessStartup
	}
	challengeHandler, err := authtransport.NewEmailChallengeHandler(db, issuer, validator, ip)
	if err != nil {
		return nil, errBusinessStartup
	}
	loginHandler, err := authtransport.NewEmailLoginHandler(db, coordinator, sessions, validator, ip)
	if err != nil {
		return nil, errBusinessStartup
	}
	meHandler, err := authtransport.NewCurrentUserHandler(db, sessions, validator)
	if err != nil {
		return nil, errBusinessStartup
	}
	logoutHandler, err := authtransport.NewCurrentSessionDeleteHandler(db, sessions, validator)
	if err != nil {
		return nil, errBusinessStartup
	}
	ageHandler, err := authtransport.NewAgeVerifyHandler(db, sessions, sessions, validator)
	if err != nil {
		return nil, errBusinessStartup
	}
	approvalListHandler, err := authtransport.NewApprovalListHandler(db, approvals, sessions, sessions, validator)
	if err != nil {
		return nil, errBusinessStartup
	}
	// A new window must not reset one approval's short-code attempt budget
	// while that approval could still be valid.
	if policy.approvalRate.Approval.Limit > 10 || policy.approvalRate.Approval.Window < policy.approval.Lifetime {
		return nil, errBusinessStartup
	}
	decisionLimiter, err := ratelimit.NewApprovalDecisionLimiter(limiter, policy.approvalRate)
	if err != nil {
		return nil, errBusinessStartup
	}
	// The transport idempotency epoch is shared with workout requests. Both
	// executors must use identical epoch keys while retaining separate SQL policies.
	decisionExecutor, err := approval.NewDecisionExecutor(approvals, sessions, keys.workoutHash, keys.workoutCipher, keys.workoutID, policy.workoutRetention)
	if err != nil {
		return nil, errBusinessStartup
	}
	decisionHandler, err := authtransport.NewApprovalDecisionHandler(db, decisionExecutor, sessions, decisionLimiter, validator, ip)
	if err != nil {
		return nil, errBusinessStartup
	}
	nameProtector, err := workoutrepo.NewExerciseNameProtector(keys.workoutName, keys.workoutNameID)
	if err != nil {
		return nil, errBusinessStartup
	}
	records, err := workoutrepo.NewRecordStore(nameProtector)
	if err != nil {
		return nil, errBusinessStartup
	}
	workouts, err := workoutapp.New(sessions, workoutrepo.NewSessionStore(), records)
	if err != nil {
		return nil, errBusinessStartup
	}
	executor, err := workoutapp.NewWorkoutExecutor(sessions, keys.workoutHash, keys.workoutCipher, keys.workoutID, policy.workoutRetention)
	if err != nil {
		return nil, errBusinessStartup
	}
	writeHandler, err := workouttransport.NewWriteHandler(db, workouts, validator, executor)
	if err != nil {
		return nil, errBusinessStartup
	}
	readHandler, err := workouttransport.NewReadHandler(db, workouts, validator)
	if err != nil {
		return nil, errBusinessStartup
	}

	identityEpoch, err := bootstrap.NewIdentityEpochInitializer(identities)
	if err != nil {
		return nil, errBusinessStartup
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, errBusinessStartup
	}
	defer tx.Rollback()
	for _, initialize := range []func(context.Context, *sql.Tx) error{
		identityEpoch.Initialize, ensureEmptyCodeEpochHistory, producer.InitializeCodeKey, hashEpoch.Initialize,
		issuer.InitializeKey, ledger.Initialize, approvals.InitializeKey,
		executor.InitializeKey, decisionExecutor.Initialize, nameProtector.InitializeExerciseKey,
	} {
		if err := initialize(ctx, tx); err != nil {
			return nil, errBusinessStartup
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, errBusinessStartup
	}

	workoutHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			readHandler.ServeHTTP(w, r)
			return
		}
		writeHandler.ServeHTTP(w, r)
	})
	mux := http.NewServeMux()
	mux.Handle("/auth/email/challenges", challengeHandler)
	mux.Handle("/auth/email-login", loginHandler)
	mux.Handle("/auth/sessions/current", logoutHandler)
	mux.Handle("/me", meHandler)
	mux.Handle("/me/age-verify", ageHandler)
	mux.Handle("/me/login-approvals", approvalListHandler)
	mux.Handle("/me/login-approvals/", decisionHandler)
	mux.Handle("/me/sessions", workoutHandler)
	mux.Handle("/me/sessions/", workoutHandler)
	return mux, nil
}

// A missing derivation epoch may only be initialized on a genuinely fresh
// email store. The identity epoch lock is already held by the caller. Locking
// reads prevent an old repeatable-read snapshot from hiding existing history.
func ensureEmptyCodeEpochHistory(ctx context.Context, tx *sql.Tx) error {
	var epoch []byte
	err := tx.QueryRowContext(ctx, `SELECT key_id FROM auth_email_code_key_epoch WHERE slot=1 FOR UPDATE`).Scan(&epoch)
	if err == nil {
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return errBusinessStartup
	}
	for _, query := range []string{
		`SELECT challenge_id FROM auth_email_login_slot LIMIT 1 FOR UPDATE`,
		`SELECT challenge_id FROM auth_email_login_challenge LIMIT 1 FOR UPDATE`,
		`SELECT challenge_id FROM auth_email_outbox LIMIT 1 FOR UPDATE`,
	} {
		var history []byte
		if err := tx.QueryRowContext(ctx, query).Scan(&history); !errors.Is(err, sql.ErrNoRows) {
			return errBusinessStartup
		}
	}
	return nil
}
