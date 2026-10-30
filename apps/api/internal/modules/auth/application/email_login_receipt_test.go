package application

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"nerae/api/internal/modules/auth/approval"
	authbootstraptest "nerae/api/internal/modules/auth/bootstrap/testsupport"
	"nerae/api/internal/modules/auth/challenge"
	"nerae/api/internal/modules/auth/domain"
	"nerae/api/internal/modules/auth/identity"
	"nerae/api/internal/modules/auth/loginledger"
	"nerae/api/internal/modules/auth/loginreceipt"
	"nerae/api/internal/modules/auth/repository"
	"nerae/api/internal/modules/auth/session"
)

func TestReceiptLoginAtomicity(t *testing.T) {
	db := emailFlowDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	hasher, _ := domain.NewEmailCodeHasher(bytes.Repeat([]byte{1}, 32))
	replay, _ := repository.NewProofReplayStore(bytes.Repeat([]byte{2}, 32))
	protector, _ := domain.NewEmailIdentityProtector(bytes.Repeat([]byte{3}, 32), bytes.Repeat([]byte{4}, 32), "email-test-key")
	challenges, err := challenge.NewStore(hasher, replay, protector, challenge.Policy{ChallengeTTL: 5 * time.Minute,
		MaxAttempts: 3, DeviceProof: domain.ProofPolicy{MaxLifetime: 5 * time.Minute, ClockSkew: 30 * time.Second}})
	if err != nil {
		t.Fatal(err)
	}
	authbootstraptest.InitializeIdentityEpoch(t, db, protector)
	resolver, _ := identity.NewResolver(protector)
	codec, _ := session.NewTokenCodec(bytes.Repeat([]byte{5}, 32), session.TokenPolicy{MaxLifetime: time.Hour})
	sessions, _ := session.NewStore(resolver, codec)
	approvals, _ := approval.NewStore(resolver, bytes.Repeat([]byte{6}, 32), "approval-test-key", approval.Policy{Lifetime: 5 * time.Minute, ReauthWindow: time.Minute})
	crypto, err := loginreceipt.New(loginreceipt.KeyConfig{IdempotencyHMACKey: bytes.Repeat([]byte{7}, 32),
		RequestHMACKey: bytes.Repeat([]byte{8}, 32), AES256GCMKey: bytes.Repeat([]byte{9}, 32)})
	if err != nil {
		t.Fatal(err)
	}
	ledger, err := loginledger.NewStore(crypto, protector, "receipt-test-key")
	if err != nil {
		t.Fatal(err)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil || approvals.InitializeKey(ctx, tx) != nil || ledger.Initialize(ctx, tx) != nil || tx.Commit() != nil {
		t.Fatal("isolated receipt/approval initialization failed")
	}
	login, _ := NewEmailLogin(challenges, sessions, approvals)
	service, err := NewReceiptLogin(login, ledger, crypto, 2*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	issue := func(email string, public domain.DevicePublicJWK) (string, string) {
		t.Helper()
		id, _ := domain.GenerateChallengeID()
		nonce, _ := domain.GenerateChallengeNonce()
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		if err := challenges.Issue(ctx, tx, challenge.IssueInput{Email: email, ChallengeID: id, Nonce: nonce, Code: "012345", PublicKey: public}); err != nil {
			_ = tx.Rollback()
			t.Fatal("isolated challenge issue failed")
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
		return id, nonce
	}
	perform := func(key string, input challenge.AttemptInput, commit bool) (ReceiptTransactionResult, error) {
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return ReceiptTransactionResult{}, err
		}
		defer tx.Rollback()
		result, err := service.VerifyInTransaction(ctx, tx, key, input)
		if err != nil {
			return result, err
		}
		if commit && result.CommitRequired() {
			err = tx.Commit()
		}
		return result, err
	}
	count := func(query string, arg any) int {
		t.Helper()
		var n int
		if err := db.QueryRowContext(ctx, query, arg).Scan(&n); err != nil {
			t.Fatal("could not inspect synthetic transaction")
		}
		return n
	}
	firstKey, firstPublic := flowDevice(t)
	newKey, newPublic := flowDevice(t)
	id, nonce := issue("receipt-login@example.test", firstPublic)
	wrong := challenge.AttemptInput{ChallengeID: id, Code: "999999", DeviceProof: flowProof(t, firstKey, id, nonce)}
	const wrongKey = "AQEBAQEBAQEBAQEBAQEBAQ"
	result, err := perform(wrongKey, wrong, true)
	if err != nil || result.Kind() != VerificationRejected || !result.CommitRequired() {
		t.Fatal("wrong attempt and rejection receipt did not commit together")
	}
	if count(`SELECT attempts FROM auth_email_login_challenge WHERE challenge_id=?`, id) != 1 ||
		count(`SELECT COUNT(*) FROM auth_email_login_receipt WHERE challenge_id=? AND result_kind='rejected'`, id) != 1 ||
		count(`SELECT COUNT(*) FROM auth_session WHERE login_challenge_id=?`, id) != 0 {
		t.Fatal("wrong attempt left an incorrect database state")
	}
	if _, err := perform(wrongKey, wrong, true); !errors.Is(err, ErrReceiptReplayUnavailable) {
		t.Fatal("existing rejection was not held for the future replay path")
	}
	result, err = perform("BQUFBQUFBQUFBQUFBQUFBQ", wrong, true)
	if err != nil || result.Kind() != VerificationRejected || result.CommitRequired() ||
		count(`SELECT attempts FROM auth_email_login_challenge WHERE challenge_id=?`, id) != 1 ||
		count(`SELECT COUNT(*) FROM auth_email_login_receipt WHERE challenge_id=? AND result_kind='rejected'`, id) != 1 {
		t.Fatal("same consumed proof with a different key changed attempts or created a receipt")
	}
	changed := wrong
	changed.Code = "012345"
	if _, err := perform(wrongKey, changed, true); !errors.Is(err, ErrReceiptConflict) {
		t.Fatal("unexpired same-key different request did not conflict")
	}
	if count(`SELECT attempts FROM auth_email_login_challenge WHERE challenge_id=?`, id) != 1 {
		t.Fatal("repeated wrong request changed attempts")
	}
	correct := challenge.AttemptInput{ChallengeID: id, Code: "012345", DeviceProof: flowProof(t, firstKey, id, nonce)}
	const correctKey = "AgICAgICAgICAgICAgICAg"
	result, err = perform(correctKey, correct, true)
	if err != nil || result.Kind() != VerificationAuthenticated || !result.CommitRequired() {
		t.Fatal("first device and encrypted session receipt did not commit together")
	}
	if count(`SELECT COUNT(*) FROM auth_session WHERE login_challenge_id=?`, id) != 1 ||
		count(`SELECT COUNT(*) FROM auth_email_login_receipt WHERE challenge_id=? AND result_kind='authenticated'`, id) != 1 {
		t.Fatal("authenticated session or receipt missing")
	}
	var ciphertext []byte
	if err := db.QueryRowContext(ctx, `SELECT response_enc FROM auth_email_login_receipt WHERE challenge_id=? AND result_kind='authenticated'`, id).Scan(&ciphertext); err != nil ||
		bytes.Contains(ciphertext, []byte(correct.Code)) || bytes.Contains(ciphertext, []byte(correct.DeviceProof)) || bytes.Contains(ciphertext, []byte(correctKey)) {
		t.Fatal("receipt exposed request material")
	}
	if _, err := perform(correctKey, correct, true); !errors.Is(err, ErrReceiptReplayUnavailable) {
		t.Fatal("existing authenticated receipt did not fail closed")
	}
	if _, err := json.Marshal(result); err == nil || strings.Contains(fmt.Sprintf("%+v", result), correct.DeviceProof) {
		t.Fatal("receipt result exposed the signed proof")
	}

	newID, newNonce := issue("receipt-login@example.test", newPublic)
	newInput := challenge.AttemptInput{ChallengeID: newID, Code: "012345", DeviceProof: flowProof(t, newKey, newID, newNonce)}
	const newKeyID = "AwMDAwMDAwMDAwMDAwMDAw"
	result, err = perform(newKeyID, newInput, true)
	if err != nil || result.Kind() != VerificationApprovalRequired || !result.CommitRequired() ||
		count(`SELECT COUNT(*) FROM auth_login_approval WHERE challenge_id=?`, newID) != 1 ||
		count(`SELECT COUNT(*) FROM auth_session WHERE login_challenge_id=?`, newID) != 0 ||
		count(`SELECT COUNT(*) FROM auth_email_login_receipt WHERE challenge_id=? AND result_kind='approval_required'`, newID) != 1 {
		t.Fatal("new device did not atomically create a protected approval")
	}
	if _, err := perform(newKeyID, newInput, true); !errors.Is(err, ErrReceiptReplayUnavailable) ||
		count(`SELECT COUNT(*) FROM auth_login_approval WHERE challenge_id=?`, newID) != 1 {
		t.Fatal("repeated new-device request created another approval")
	}

	rollbackKey, rollbackPublic := flowDevice(t)
	rollbackID, rollbackNonce := issue("receipt-rollback@example.test", rollbackPublic)
	rollbackInput := challenge.AttemptInput{ChallengeID: rollbackID, Code: "012345", DeviceProof: flowProof(t, rollbackKey, rollbackID, rollbackNonce)}
	const rollbackRequestKey = "BAQEBAQEBAQEBAQEBAQEBA"
	result, err = perform(rollbackRequestKey, rollbackInput, false)
	if err != nil || result.Kind() != VerificationAuthenticated || !result.CommitRequired() ||
		count(`SELECT COUNT(*) FROM auth_session WHERE login_challenge_id=?`, rollbackID) != 0 ||
		count(`SELECT COUNT(*) FROM auth_email_login_receipt WHERE challenge_id=?`, rollbackID) != 0 {
		t.Fatal("rollback left a session or receipt")
	}
	result, err = perform(rollbackRequestKey, rollbackInput, true)
	if err != nil || result.Kind() != VerificationAuthenticated || count(`SELECT COUNT(*) FROM auth_session WHERE login_challenge_id=?`, rollbackID) != 1 {
		t.Fatal("rolled-back proof could not be retried")
	}

	failureKey, failurePublic := flowDevice(t)
	failureID, failureNonce := issue("receipt-failure@example.test", failurePublic)
	failureInput := challenge.AttemptInput{ChallengeID: failureID, Code: "012345", DeviceProof: flowProof(t, failureKey, failureID, failureNonce)}
	service.now = func() time.Time { return time.Time{} }
	if _, err := perform("BQUFBQUFBQUFBQUFBQUFBQ", failureInput, true); !errors.Is(err, ErrUnavailable) {
		t.Fatal("clock failure was not reported unavailable")
	}
	service.now = time.Now
	if count(`SELECT COUNT(*) FROM auth_session WHERE login_challenge_id=?`, failureID) != 0 ||
		count(`SELECT COUNT(*) FROM auth_email_login_receipt WHERE challenge_id=?`, failureID) != 0 ||
		count(`SELECT consumed_at IS NOT NULL FROM auth_email_login_challenge WHERE challenge_id=?`, failureID) != 0 {
		t.Fatal("failed transaction left a partial login")
	}

	invalidKey, invalidPublic := flowDevice(t)
	otherKey, _ := flowDevice(t)
	invalidID, invalidNonce := issue("receipt-invalid@example.test", invalidPublic)
	invalidInput := challenge.AttemptInput{ChallengeID: invalidID, Code: "012345", DeviceProof: flowProof(t, otherKey, invalidID, invalidNonce)}
	result, err = perform("BwcHBwcHBwcHBwcHBwcHBw", invalidInput, true)
	if err != nil || result.Kind() != VerificationRejected || result.CommitRequired() ||
		count(`SELECT attempts FROM auth_email_login_challenge WHERE challenge_id=?`, invalidID) != 0 ||
		count(`SELECT COUNT(*) FROM auth_email_login_receipt WHERE challenge_id=?`, invalidID) != 0 {
		t.Fatal("invalid device signature changed the login or created a receipt")
	}
	// A coordinator accidentally configured with different receipt keys must
	// roll back the challenge and session when the ledger rejects its seal.
	wrongCrypto, err := loginreceipt.New(loginreceipt.KeyConfig{IdempotencyHMACKey: bytes.Repeat([]byte{10}, 32),
		RequestHMACKey: bytes.Repeat([]byte{11}, 32), AES256GCMKey: bytes.Repeat([]byte{12}, 32)})
	if err != nil {
		t.Fatal(err)
	}
	mismatched, err := NewReceiptLogin(login, ledger, wrongCrypto, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	correctService := service
	service = mismatched
	validInput := challenge.AttemptInput{ChallengeID: invalidID, Code: "012345", DeviceProof: flowProof(t, invalidKey, invalidID, invalidNonce)}
	if _, err := perform("CAgICAgICAgICAgICAgICA", validInput, true); !errors.Is(err, ErrUnavailable) {
		t.Fatal("mismatched receipt keys were accepted")
	}
	service = correctService
	if count(`SELECT consumed_at IS NOT NULL FROM auth_email_login_challenge WHERE challenge_id=?`, invalidID) != 0 ||
		count(`SELECT COUNT(*) FROM auth_session WHERE login_challenge_id=?`, invalidID) != 0 ||
		count(`SELECT COUNT(*) FROM auth_email_login_receipt WHERE challenge_id=?`, invalidID) != 0 {
		t.Fatal("mismatched receipt keys left a partial login")
	}
	// Simulate the receipt becoming stale after its insert. The caller must
	// roll back the already-created session and challenge consumption.
	var clockReads atomic.Int32
	clockStart := time.Now().UTC()
	service.now = func() time.Time {
		if clockReads.Add(1) == 1 {
			return clockStart
		}
		return clockStart.Add(3 * time.Minute)
	}
	if _, err := perform("CQkJCQkJCQkJCQkJCQkJCQ", validInput, true); !errors.Is(err, ErrUnavailable) {
		t.Fatal("receipt expiry after insertion was accepted")
	}
	service.now = time.Now
	if count(`SELECT consumed_at IS NOT NULL FROM auth_email_login_challenge WHERE challenge_id=?`, invalidID) != 0 ||
		count(`SELECT COUNT(*) FROM auth_session WHERE login_challenge_id=?`, invalidID) != 0 ||
		count(`SELECT COUNT(*) FROM auth_email_login_receipt WHERE challenge_id=?`, invalidID) != 0 {
		t.Fatal("expired receipt left a partial login")
	}

	raceKey, racePublic := flowDevice(t)
	raceID, raceNonce := issue("receipt-race@example.test", racePublic)
	raceInput := challenge.AttemptInput{ChallengeID: raceID, Code: "012345", DeviceProof: flowProof(t, raceKey, raceID, raceNonce)}
	type raceResult struct {
		result ReceiptTransactionResult
		err    error
	}
	results := make(chan raceResult, 2)
	start := make(chan struct{})
	var workers sync.WaitGroup
	for i := 0; i < 2; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			result, err := perform("BgYGBgYGBgYGBgYGBgYGBg", raceInput, true)
			results <- raceResult{result, err}
		}()
	}
	close(start)
	workers.Wait()
	close(results)
	successes, deferredReplays := 0, 0
	for item := range results {
		if item.err == nil && item.result.Kind() == VerificationAuthenticated && item.result.CommitRequired() {
			successes++
		} else if errors.Is(item.err, ErrReceiptReplayUnavailable) {
			deferredReplays++
		} else {
			t.Fatalf("unexpected concurrent transaction outcome: %v", item.err)
		}
	}
	if successes != 1 || deferredReplays != 1 || count(`SELECT COUNT(*) FROM auth_session WHERE login_challenge_id=?`, raceID) != 1 ||
		count(`SELECT COUNT(*) FROM auth_email_login_receipt WHERE challenge_id=?`, raceID) != 1 {
		t.Fatal("same-key concurrency created more than one result")
	}
}

func TestReceiptLoginRejectsMissingDependencies(t *testing.T) {
	if _, err := NewReceiptLogin(nil, nil, nil, time.Minute); !errors.Is(err, ErrUnavailable) {
		t.Fatal("missing dependencies accepted")
	}
	if _, err := (&ReceiptLogin{}).VerifyInTransaction(context.Background(), (*sql.Tx)(nil), "", challenge.AttemptInput{}); !errors.Is(err, ErrUnavailable) {
		t.Fatal("zero-value receipt coordinator was accepted")
	}
}
