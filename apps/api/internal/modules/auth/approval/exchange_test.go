package approval

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	issuancetestsupport "nerae/api/internal/modules/auth/issuance/testsupport"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"nerae/api/internal/modules/auth/approvalledger"
	"nerae/api/internal/modules/auth/approvalreceipt"
	"nerae/api/internal/modules/auth/challenge"
	"nerae/api/internal/modules/auth/domain"
	"nerae/api/internal/modules/auth/identity"
	"nerae/api/internal/modules/auth/repository"
	"nerae/api/internal/modules/auth/session"
	"nerae/api/internal/modules/workout/application"
	workoutdomain "nerae/api/internal/modules/workout/domain"
	workoutrepo "nerae/api/internal/modules/workout/repository"
)

func exchangeProof(t *testing.T, key *ecdsa.PrivateKey, id, nonce string) string {
	t.Helper()
	jti, err := domain.GenerateChallengeID()
	if err != nil {
		t.Fatal("could not generate exchange proof ID")
	}
	return exchangeProofWithJTI(t, key, id, nonce, jti)
}

func exchangeProofWithJTI(t *testing.T, key *ecdsa.PrivateKey, id, nonce, jti string) string {
	t.Helper()
	now := time.Now().UTC()
	payload, err := json.Marshal(map[string]any{"aud": "nerae-auth-device", "purpose": "approval_exchange", "resourceId": id,
		"nonce": nonce, "iat": now.Unix(), "exp": now.Add(time.Minute).Unix(), "jti": jti})
	if err != nil {
		t.Fatal("could not encode exchange proof")
	}
	encode := base64.RawURLEncoding.EncodeToString
	input := encode([]byte(`{"alg":"ES256","typ":"JWT"}`)) + "." + encode(payload)
	digest := sha256.Sum256([]byte(input))
	r, signatureS, err := ecdsa.Sign(rand.Reader, key, digest[:])
	if err != nil {
		t.Fatal("could not sign exchange proof")
	}
	return input + "." + encode(append(r.FillBytes(make([]byte, 32)), signatureS.FillBytes(make([]byte, 32))...))
}

func TestApprovedDeviceExchange(t *testing.T) {
	db := isolatedDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	hasher, _ := domain.NewEmailCodeHasher(bytes.Repeat([]byte{1}, 32))
	replay, _ := repository.NewProofReplayStore(bytes.Repeat([]byte{2}, 32))
	emailProtector, _ := domain.NewEmailIdentityProtector(bytes.Repeat([]byte{3}, 32), bytes.Repeat([]byte{4}, 32), "email-test-key")
	challenges, err := challenge.NewStore(hasher, replay, emailProtector, "fixture-email-code-hash-v1", challenge.Policy{ChallengeTTL: 5 * time.Minute,
		MaxAttempts: 3, DeviceProof: domain.ProofPolicy{MaxLifetime: 5 * time.Minute, ClockSkew: 30 * time.Second}})
	if err != nil {
		t.Fatal("could not initialize challenges")
	}
	issuancetestsupport.InitializeEmailCodeHashEpoch(t, db, emailProtector, hasher)
	resolver, _ := identity.NewResolver(emailProtector)
	codec, _ := session.NewTokenCodec(bytes.Repeat([]byte{5}, 32), session.TokenPolicy{MaxLifetime: time.Hour})
	auth, _ := session.NewStore(resolver, codec)
	approvals, _ := NewStore(resolver, bytes.Repeat([]byte{6}, 32), "approval-test-key", Policy{Lifetime: 5 * time.Minute, ReauthWindow: time.Minute})
	initTx, err := db.BeginTx(ctx, nil)
	if err != nil || approvals.InitializeKey(ctx, initTx) != nil || initTx.Commit() != nil {
		t.Fatal("could not initialize approval key")
	}
	exchanger, err := NewExchanger(approvals, codec, replay, domain.ProofPolicy{MaxLifetime: 5 * time.Minute, ClockSkew: 30 * time.Second})
	if err != nil {
		t.Fatal("could not initialize exchanger")
	}
	oldKey, oldPublic := testDevice(t)
	newKey, newPublic := testDevice(t)
	issueChallenge := func(email string, key *ecdsa.PrivateKey, public domain.DevicePublicJWK) (*sql.Tx, *challenge.VerifiedEmail) {
		t.Helper()
		id, _ := domain.GenerateChallengeID()
		nonce, _ := domain.GenerateChallengeNonce()
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal("could not start isolated challenge")
		}
		if err := challenges.Issue(ctx, tx, challenge.IssueInput{Email: email, ChallengeID: id, Nonce: nonce, Code: "012345", PublicKey: public}); err != nil || tx.Commit() != nil {
			t.Fatal("could not issue isolated challenge")
		}
		tx, err = db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal("could not begin isolated proof verification")
		}
		attempt, err := challenges.Attempt(ctx, tx, challenge.AttemptInput{ChallengeID: id, Code: "012345", DeviceProof: signedProof(t, key, id, nonce)})
		if err != nil || !attempt.Verified || attempt.Proof == nil {
			_ = tx.Rollback()
			t.Fatal("could not verify isolated challenge")
		}
		return tx, attempt.Proof
	}
	initialTx, initialProof := issueChallenge("owner@example.test", oldKey, oldPublic)
	initialIssued, err := auth.IssueEmail(ctx, initialTx, initialProof)
	if err != nil || initialTx.Commit() != nil {
		t.Fatal("could not commit initial session")
	}
	oldToken, err := initialIssued.AfterCommit(ctx, db)
	if err != nil {
		t.Fatal("could not release initial session")
	}
	oldClaims, err := codec.Verify(oldToken)
	if err != nil {
		t.Fatal("could not validate initial session")
	}
	beginApproved := func(key *ecdsa.PrivateKey, public domain.DevicePublicJWK) Data {
		t.Helper()
		tx, proof := issueChallenge("owner@example.test", key, public)
		pending, err := approvals.BeginEmail(ctx, tx, proof)
		if err != nil || tx.Commit() != nil {
			t.Fatal("could not commit isolated approval")
		}
		data, err := pending.AfterCommit(ctx, db)
		if err != nil {
			t.Fatal("could not release isolated approval")
		}
		tx, err = db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal("could not begin approval decision")
		}
		principal, err := auth.Authenticate(ctx, tx, oldToken)
		if err != nil || approvals.Decide(ctx, tx, principal, data.ID, Approve, data.ComparisonCode, true) != nil || tx.Commit() != nil {
			_ = tx.Rollback()
			t.Fatal("could not approve isolated device")
		}
		return data
	}
	blockedKey, blockedPublic := testDevice(t)
	blockedTx, blockedProof := issueChallenge("owner@example.test", blockedKey, blockedPublic)
	blockedPending, err := approvals.BeginEmail(ctx, blockedTx, blockedProof)
	if err != nil || blockedTx.Commit() != nil {
		t.Fatal("could not create unapproved device fixture")
	}
	blockedData, err := blockedPending.AfterCommit(ctx, db)
	if err != nil {
		t.Fatal("could not release unapproved device fixture")
	}
	blockedInput := ExchangeInput{ApprovalID: blockedData.ID, Secret: blockedData.Secret,
		Proof: exchangeProof(t, blockedKey, blockedData.ID, blockedData.Nonce)}
	if _, err := exchanger.Exchange(ctx, db, blockedInput); !errors.Is(err, ErrUnavailable) {
		t.Fatal("pending approval issued a device session")
	}
	denyTx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal("could not begin denial fixture")
	}
	denyPrincipal, err := auth.Authenticate(ctx, denyTx, oldToken)
	if err != nil || approvals.Decide(ctx, denyTx, denyPrincipal, blockedData.ID, Deny, "", false) != nil || denyTx.Commit() != nil {
		_ = denyTx.Rollback()
		t.Fatal("could not deny isolated approval")
	}
	if _, err := exchanger.Exchange(ctx, db, blockedInput); !errors.Is(err, ErrUnavailable) {
		t.Fatal("denied approval issued a device session")
	}
	data := beginApproved(newKey, newPublic)
	proof := exchangeProof(t, newKey, data.ID, data.Nonce)
	input := ExchangeInput{ApprovalID: data.ID, Secret: data.Secret, Proof: proof}
	wrongSecret, _ := domain.GenerateChallengeNonce()
	if _, err := exchanger.Exchange(ctx, db, ExchangeInput{ApprovalID: data.ID, Secret: wrongSecret, Proof: proof}); !errors.Is(err, ErrUnavailable) {
		t.Fatal("wrong polling secret was accepted")
	}
	if _, err := exchanger.Exchange(ctx, db, ExchangeInput{ApprovalID: data.ID, Secret: data.Secret, Proof: exchangeProof(t, oldKey, data.ID, data.Nonce)}); !errors.Is(err, ErrUnavailable) {
		t.Fatal("wrong device key was accepted")
	}
	if _, err := exchanger.Exchange(ctx, db, ExchangeInput{ApprovalID: data.ID, Secret: data.Secret, Proof: exchangeProof(t, newKey, data.ID, "different-nonce")}); !errors.Is(err, ErrUnavailable) {
		t.Fatal("wrong approval nonce was accepted")
	}
	exchanger.now = func() time.Time { return data.ExpiresAt }
	if _, err := exchanger.Exchange(ctx, db, input); !errors.Is(err, ErrUnavailable) {
		t.Fatal("expired approval was exchanged")
	}
	exchanger.now = time.Now
	// Keep the approval and target proof valid while expiring only the session
	// that approved it. A source session past expiry cannot authorize exchange.
	if _, err := db.ExecContext(ctx, `UPDATE auth_session SET expires_at = DATE_ADD(authenticated_at, INTERVAL 1 SECOND) WHERE id = ?`, []byte(oldClaims.SessionID)); err != nil {
		t.Fatal("could not set isolated source-session expiry")
	}
	fakeSourceTime := oldClaims.IssuedAt.Add(2 * time.Second)
	if !data.ExpiresAt.After(fakeSourceTime) {
		t.Fatal("approval fixture expired before source-session check")
	}
	exchanger.now = func() time.Time { return fakeSourceTime }
	if _, err := exchanger.Exchange(ctx, db, input); !errors.Is(err, ErrUnavailable) {
		t.Fatal("expired source session allowed an otherwise valid exchange")
	}
	exchanger.now = time.Now
	if _, err := db.ExecContext(ctx, `UPDATE auth_session SET expires_at = ? WHERE id = ?`, timestamp(oldClaims.ExpiresAt), []byte(oldClaims.SessionID)); err != nil {
		t.Fatal("could not restore isolated source-session expiry")
	}
	newFingerprint, _ := domain.DeviceKeyFingerprint(newPublic)
	revokedID, _ := domain.GenerateChallengeID()
	if _, err := db.ExecContext(ctx, `INSERT INTO trusted_device (id, user_id, key_fingerprint, public_x, public_y, state, created_at)
        VALUES (?, ?, ?, ?, ?, 'revoked', UTC_TIMESTAMP(6))`, []byte(revokedID), oldClaims.SubjectID, newFingerprint[:], newPublic.X, newPublic.Y); err != nil {
		t.Fatal("could not create isolated revoked-device fixture")
	}
	if _, err := exchanger.Exchange(ctx, db, input); !errors.Is(err, ErrUnavailable) {
		t.Fatal("revoked target device was silently re-trusted")
	}
	if _, err := db.ExecContext(ctx, `DELETE FROM trusted_device WHERE id = ?`, []byte(revokedID)); err != nil {
		t.Fatal("could not remove isolated revoked-device fixture")
	}
	changes := []struct {
		change, restore string
		args            []any
	}{
		{`UPDATE user SET status = 'disabled' WHERE id = ?`, `UPDATE user SET status = 'active' WHERE id = ?`, []any{oldClaims.SubjectID}},
		{`UPDATE login_identity SET state = 'revoked' WHERE user_id = ?`, `UPDATE login_identity SET state = 'active' WHERE user_id = ?`, []any{oldClaims.SubjectID}},
		{`UPDATE trusted_device SET state = 'revoked' WHERE user_id = ?`, `UPDATE trusted_device SET state = 'active' WHERE user_id = ?`, []any{oldClaims.SubjectID}},
		{`UPDATE auth_session SET state = 'revoked', revoked_at = UTC_TIMESTAMP(6) WHERE id = ?`, `UPDATE auth_session SET state = 'active', revoked_at = NULL WHERE id = ?`, []any{[]byte(oldClaims.SessionID)}},
		{`UPDATE user SET token_version = token_version + 1 WHERE id = ?`, `UPDATE user SET token_version = token_version - 1 WHERE id = ?`, []any{oldClaims.SubjectID}},
		{`UPDATE user SET identity_version = identity_version + 1 WHERE id = ?`, `UPDATE user SET identity_version = identity_version - 1 WHERE id = ?`, []any{oldClaims.SubjectID}},
	}
	for _, item := range changes {
		if _, err := db.ExecContext(ctx, item.change, item.args...); err != nil {
			t.Fatal("could not alter isolated authority")
		}
		if _, err := exchanger.Exchange(ctx, db, input); !errors.Is(err, ErrUnavailable) {
			t.Fatal("revoked original authority allowed exchange")
		}
		if _, err := db.ExecContext(ctx, item.restore, item.args...); err != nil {
			t.Fatal("could not restore isolated authority")
		}
	}
	// Force expiry while exchange waits on a real identity lock. No proof is
	// consumed and the same request may proceed after the fixture is released.
	expiryGate, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal("could not begin expiry lock test")
	}
	var lockedIdentity []byte
	if err := expiryGate.QueryRowContext(ctx, `SELECT id FROM login_identity WHERE user_id = ? AND state = 'active' FOR UPDATE`, oldClaims.SubjectID).Scan(&lockedIdentity); err != nil {
		_ = expiryGate.Rollback()
		t.Fatal("could not lock expiry fixture")
	}
	var fakeNow atomic.Int64
	fakeNow.Store(time.Now().UnixNano())
	exchanger.now = func() time.Time { return time.Unix(0, fakeNow.Load()) }
	expiredResult := make(chan error, 1)
	go func() {
		_, err := exchanger.Exchange(ctx, db, input)
		expiredResult <- err
	}()
	deadline := time.Now().Add(5 * time.Second)
	blocked := 0
	for time.Now().Before(deadline) {
		if err := db.QueryRowContext(ctx, `SELECT COUNT(DISTINCT w.REQUESTING_ENGINE_TRANSACTION_ID)
            FROM performance_schema.data_lock_waits w JOIN performance_schema.data_locks l ON l.ENGINE_LOCK_ID = w.REQUESTING_ENGINE_LOCK_ID
            WHERE l.OBJECT_SCHEMA = DATABASE() AND l.OBJECT_NAME = 'login_identity'`).Scan(&blocked); err != nil {
			_ = expiryGate.Rollback()
			t.Fatal("could not observe expiry lock wait")
		}
		if blocked >= 1 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if blocked < 1 {
		_ = expiryGate.Rollback()
		t.Fatal("exchange did not reach expiry lock")
	}
	fakeNow.Store(data.ExpiresAt.UnixNano())
	if err := expiryGate.Rollback(); err != nil {
		t.Fatal("could not release expiry lock")
	}
	if err := <-expiredResult; !errors.Is(err, ErrUnavailable) {
		t.Fatal("lock wait extended approval expiry")
	}
	exchanger.now = time.Now
	// Expire only the proof after it was consumed while the target-device row
	// is locked. The approval and source session remain valid; the transaction
	// must roll back the replay marker and session.
	proofParts := strings.Split(proof, ".")
	if len(proofParts) != 3 {
		t.Fatal("invalid synthetic proof fixture")
	}
	proofPayload, err := base64.RawURLEncoding.DecodeString(proofParts[1])
	if err != nil {
		t.Fatal("could not decode synthetic proof fixture")
	}
	var proofTimes struct {
		Exp int64 `json:"exp"`
	}
	if err := json.Unmarshal(proofPayload, &proofTimes); err != nil || proofTimes.Exp <= 0 {
		t.Fatal("could not read synthetic proof expiry")
	}
	proofExpiredAt := time.Unix(proofTimes.Exp+31, 0)
	if !data.ExpiresAt.After(proofExpiredAt) || !oldClaims.ExpiresAt.After(proofExpiredAt) {
		t.Fatal("approval or source session expired before proof-only test")
	}
	targetFixtureID, _ := domain.GenerateChallengeID()
	if _, err := db.ExecContext(ctx, `INSERT INTO trusted_device (id, user_id, key_fingerprint, public_x, public_y, state, created_at)
        VALUES (?, ?, ?, ?, ?, 'active', UTC_TIMESTAMP(6))`, []byte(targetFixtureID), oldClaims.SubjectID, newFingerprint[:], newPublic.X, newPublic.Y); err != nil {
		t.Fatal("could not create target lock fixture")
	}
	targetGate, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal("could not begin target expiry lock")
	}
	var lockedDevice []byte
	if err := targetGate.QueryRowContext(ctx, `SELECT id FROM trusted_device WHERE id = ? FOR UPDATE`, []byte(targetFixtureID)).Scan(&lockedDevice); err != nil {
		_ = targetGate.Rollback()
		t.Fatal("could not hold target lock")
	}
	fakeNow.Store(time.Now().UnixNano())
	exchanger.now = func() time.Time { return time.Unix(0, fakeNow.Load()) }
	targetResult := make(chan error, 1)
	go func() {
		_, err := exchanger.Exchange(ctx, db, input)
		targetResult <- err
	}()
	deadline = time.Now().Add(5 * time.Second)
	blocked = 0
	for time.Now().Before(deadline) {
		if err := db.QueryRowContext(ctx, `SELECT COUNT(DISTINCT w.REQUESTING_ENGINE_TRANSACTION_ID)
            FROM performance_schema.data_lock_waits w JOIN performance_schema.data_locks l ON l.ENGINE_LOCK_ID = w.REQUESTING_ENGINE_LOCK_ID
            WHERE l.OBJECT_SCHEMA = DATABASE() AND l.OBJECT_NAME = 'trusted_device'`).Scan(&blocked); err != nil {
			_ = targetGate.Rollback()
			t.Fatal("could not observe target-device lock")
		}
		if blocked >= 1 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if blocked < 1 {
		_ = targetGate.Rollback()
		t.Fatal("exchange did not reach target-device lock")
	}
	fakeNow.Store(proofExpiredAt.UnixNano())
	if err := targetGate.Rollback(); err != nil {
		t.Fatal("could not release target lock")
	}
	if err := <-targetResult; !errors.Is(err, ErrUnavailable) {
		t.Fatal("proof expiry during target lock wait committed the exchange")
	}
	exchanger.now = time.Now
	if _, err := db.ExecContext(ctx, `DELETE FROM trusted_device WHERE id = ?`, []byte(targetFixtureID)); err != nil {
		t.Fatal("could not remove target lock fixture")
	}
	t.Run("caller-owned exchange rolls back all effects", func(t *testing.T) {
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal("could not start caller-owned exchange")
		}
		candidate, err := exchanger.exchangeInTransaction(ctx, tx, input)
		if err != nil || candidate.verifyForTransaction(ctx, tx) != nil {
			_ = tx.Rollback()
			t.Fatal("could not stage exchange in caller-owned transaction")
		}
		if encoded, err := json.Marshal(candidate); err == nil || len(encoded) != 0 ||
			fmt.Sprint(candidate) != "ExchangeCandidate{redacted}" {
			_ = tx.Rollback()
			t.Fatal("staged candidate was serializable or unredacted")
		}
		var candidateSession []byte
		if err := tx.QueryRowContext(ctx, `SELECT exchange_session_id FROM auth_login_approval WHERE id=?`, []byte(data.ID)).Scan(&candidateSession); err != nil || len(candidateSession) == 0 {
			_ = tx.Rollback()
			t.Fatal("staged exchange did not bind a session")
		}
		var stagedState, committedState string
		var stagedSessions, visibleSessions int
		if err := tx.QueryRowContext(ctx, `SELECT state FROM auth_login_approval WHERE id=?`, []byte(data.ID)).Scan(&stagedState); err != nil || stagedState != "consumed" ||
			tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM auth_session WHERE id=? AND user_id=?`, candidateSession, oldClaims.SubjectID).Scan(&stagedSessions) != nil || stagedSessions != 1 ||
			db.QueryRowContext(ctx, `SELECT state FROM auth_login_approval WHERE id=?`, []byte(data.ID)).Scan(&committedState) != nil || committedState != "approved" ||
			db.QueryRowContext(ctx, `SELECT COUNT(*) FROM auth_session WHERE id=?`, candidateSession).Scan(&visibleSessions) != nil || visibleSessions != 0 {
			_ = tx.Rollback()
			t.Fatal("uncommitted exchange was externally visible or incomplete")
		}
		if err := tx.Rollback(); err != nil {
			t.Fatal("caller-owned exchange rollback failed")
		}
		var count int
		if err := db.QueryRowContext(ctx, `SELECT state FROM auth_login_approval WHERE id=?`, []byte(data.ID)).Scan(&committedState); err != nil || committedState != "approved" ||
			db.QueryRowContext(ctx, `SELECT COUNT(*) FROM auth_session WHERE id=?`, candidateSession).Scan(&count) != nil || count != 0 ||
			db.QueryRowContext(ctx, `SELECT COUNT(*) FROM trusted_device WHERE key_fingerprint=?`, newFingerprint[:]).Scan(&count) != nil || count != 0 {
			t.Fatal("rolled-back exchange left a session, device or consumed approval")
		}
	})
	newToken, err := exchanger.Exchange(ctx, db, input)
	if err != nil || newToken.ForResponse() == "" {
		t.Fatal("approved device could not obtain a session")
	}
	newClaims, err := codec.Verify(newToken.ForResponse())
	if err != nil || newClaims.SubjectID != oldClaims.SubjectID || newClaims.SessionID == oldClaims.SessionID {
		t.Fatal("new device did not obtain a distinct session for the same subject")
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal("could not authenticate new device")
	}
	principal, err := auth.Authenticate(ctx, tx, newToken.ForResponse())
	if err != nil {
		_ = tx.Rollback()
		t.Fatal("new device session was not valid")
	}
	newDetails, err := principal.ForTransaction(ctx, tx)
	if err != nil || newDetails.SubjectID == 0 {
		_ = tx.Rollback()
		t.Fatal("new device principal was invalid")
	}
	_ = tx.Rollback()
	// Exercise the authenticated storage seam with two distinct synthetic
	// device keys; this does not stand in for real devices, SMTP or HTTP.
	workoutKey, err := workoutrepo.NewExerciseNameProtector(bytes.Repeat([]byte{9}, 32), "workout-test-key")
	if err != nil {
		t.Fatal("could not create isolated workout key")
	}
	workoutInit, err := db.BeginTx(ctx, nil)
	if err != nil || workoutKey.InitializeExerciseKey(ctx, workoutInit) != nil || workoutInit.Commit() != nil {
		t.Fatal("could not initialize isolated workout key")
	}
	workoutRecords, _ := workoutrepo.NewRecordStore(workoutKey)
	workout, err := application.New(auth, workoutrepo.NewSessionStore(), workoutRecords)
	if err != nil {
		t.Fatal("could not initialize isolated workout service")
	}
	// This is an explicit self-declaration through the authenticated session
	// seam, not evidence of identity or actual age verification.
	declareAdultForTest(t, ctx, db, auth, oldToken)
	zone := "UTC"
	workoutInput := workoutrepo.FreeSessionInput{Date: time.Now().UTC().AddDate(0, 0, -1).Format("2006-01-02"), TimeZone: &zone, ClientEventID: "device-sync-event"}
	workoutRef, err := workout.CreateFree(ctx, db, oldToken, workoutInput)
	if err != nil {
		t.Fatal("old device session could not create isolated workout")
	}
	groupA := workoutdomain.Set{RecordKey: "device-group-001", ExerciseEntryKey: "device-entry-001", Exercise: workoutdomain.Exercise{Source: workoutdomain.Custom, Name: "自定义动作"},
		Index: 1, Kind: workoutdomain.Weight, WeightKg: "42.5", Reps: 8}
	groupB := workoutdomain.Set{RecordKey: "device-group-002", ExerciseEntryKey: "device-entry-002", Exercise: workoutdomain.Exercise{Source: workoutdomain.Custom, Name: "自定义卧推"},
		Index: 1, Kind: workoutdomain.Weight, WeightKg: "25", Reps: 10}
	initialOrder := []string{"device-entry-002", "device-entry-001"}
	if v, changed, err := workout.SaveSets(ctx, db, oldToken, workoutRef.ID, 1, workoutdomain.Batch{Sets: []workoutdomain.Set{groupA, groupB}}, initialOrder); err != nil || !changed || v != 2 {
		t.Fatal("old device session could not save isolated workout and complete order")
	}
	snapshot, err := workout.ReadOrderedFree(ctx, db, newToken.ForResponse(), workoutRef.ID)
	if err != nil || snapshot.Version != 2 || !reflect.DeepEqual(snapshot.ExerciseEntryKeys, initialOrder) ||
		snapshot.Sets[groupA.RecordKey].WeightKg != "42.50" || snapshot.Sets[groupA.RecordKey].Exercise.Name != groupA.Exercise.Name ||
		snapshot.Sets[groupB.RecordKey].WeightKg != "25.00" {
		t.Fatal("approved new device session could not read the same ordered workout")
	}
	groupB.WeightKg = "30"
	updatedOrder := []string{"device-entry-001", "device-entry-002"}
	if v, changed, err := workout.SaveSets(ctx, db, newToken.ForResponse(), workoutRef.ID, 2, workoutdomain.Batch{Sets: []workoutdomain.Set{groupB}}, updatedOrder); err != nil || !changed || v != 3 {
		t.Fatal("approved new device session could not update and reorder the same workout")
	}
	snapshot, err = workout.ReadOrderedFree(ctx, db, oldToken, workoutRef.ID)
	if err != nil || snapshot.Version != 3 || !reflect.DeepEqual(snapshot.ExerciseEntryKeys, updatedOrder) ||
		snapshot.Sets[groupB.RecordKey].WeightKg != "30.00" {
		t.Fatal("old device session could not read updated group and order")
	}
	clearAdultForTest(t, ctx, db, auth, oldToken)
	if _, err := workout.ReadOrderedFree(ctx, db, oldToken, workoutRef.ID); !errors.Is(err, application.ErrAgeRequired) {
		t.Fatal("withdrawn declaration did not block original device read")
	}
	if _, err := workout.ReadOrderedFree(ctx, db, newToken.ForResponse(), workoutRef.ID); !errors.Is(err, application.ErrAgeRequired) {
		t.Fatal("withdrawn declaration did not block approved device read")
	}
	declareAdultForTest(t, ctx, db, auth, oldToken)
	if snapshot, err = workout.ReadOrderedFree(ctx, db, newToken.ForResponse(), workoutRef.ID); err != nil ||
		snapshot.Version != 3 || !reflect.DeepEqual(snapshot.ExerciseEntryKeys, updatedOrder) {
		t.Fatal("explicit re-declaration did not restore approved device access")
	}
	if _, err := exchanger.Exchange(ctx, db, input); !errors.Is(err, ErrUnavailable) {
		t.Fatal("consumed approval was exchanged again")
	}
	if _, err := exchanger.Exchange(ctx, db, ExchangeInput{ApprovalID: data.ID, Secret: data.Secret, Proof: exchangeProof(t, newKey, data.ID, data.Nonce)}); !errors.Is(err, ErrUnavailable) {
		t.Fatal("consumed approval accepted a new proof")
	}
	var count int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM trusted_device WHERE user_id = ?`, oldClaims.SubjectID).Scan(&count); err != nil || count != 2 {
		t.Fatal("new device registration count was incorrect")
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM auth_session WHERE user_id = ?`, oldClaims.SubjectID).Scan(&count); err != nil || count != 2 {
		t.Fatal("new device session count was incorrect")
	}
	for _, value := range []any{input, &input, exchanger, *exchanger} {
		if _, err := json.Marshal(value); err == nil {
			t.Fatal("exchange input was JSON-serializable")
		}
		var log bytes.Buffer
		slog.New(slog.NewJSONHandler(&log, nil)).Info("test", "value", value)
		var entry map[string]any
		if json.Unmarshal(log.Bytes(), &entry) != nil {
			t.Fatal("could not parse test log")
		}
		output := fmt.Sprintf("%+v %#v %v", value, value, entry["value"])
		for _, sensitive := range []string{input.Secret, input.Proof, input.ApprovalID} {
			if strings.Contains(output, sensitive) {
				t.Fatal("exchange secret leaked into output")
			}
		}
	}
	// Inject a database error after proof consumption and device insertion.
	// Rollback must restore the approval and allow the same signed proof retry.
	rollbackKey, rollbackPublic := testDevice(t)
	rollbackApproval := beginApproved(rollbackKey, rollbackPublic)
	rollbackProof := exchangeProof(t, rollbackKey, rollbackApproval.ID, rollbackApproval.Nonce)
	rollbackInput := ExchangeInput{ApprovalID: rollbackApproval.ID, Secret: rollbackApproval.Secret, Proof: rollbackProof}
	if _, err := db.ExecContext(ctx, `CREATE TRIGGER fail_exchange_session BEFORE INSERT ON auth_session FOR EACH ROW SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'isolated test failure'`); err != nil {
		t.Fatal("could not install isolated failure trigger")
	}
	if _, err := exchanger.Exchange(ctx, db, rollbackInput); !errors.Is(err, ErrUnavailable) {
		t.Fatal("injected exchange failure was not returned")
	}
	if _, err := db.ExecContext(ctx, `DROP TRIGGER fail_exchange_session`); err != nil {
		t.Fatal("could not remove isolated failure trigger")
	}
	var state string
	if err := db.QueryRowContext(ctx, `SELECT state FROM auth_login_approval WHERE id = ?`, []byte(rollbackApproval.ID)).Scan(&state); err != nil || state != "approved" {
		t.Fatal("failed exchange did not preserve approval")
	}
	rollbackFingerprint, _ := domain.DeviceKeyFingerprint(rollbackPublic)
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM trusted_device WHERE key_fingerprint = ?`, rollbackFingerprint[:]).Scan(&count); err != nil || count != 0 {
		t.Fatal("failed exchange left a trusted device")
	}
	if retry, err := exchanger.Exchange(ctx, db, rollbackInput); err != nil || retry.ForResponse() == "" {
		t.Fatal("rolled-back proof could not be retried")
	}
	// A key claimed by a different subject after approval must never be merged.
	collisionKey, collisionPublic := testDevice(t)
	collisionApproval := beginApproved(collisionKey, collisionPublic)
	collisionTx, collisionProof := issueChallenge("other-owner@example.test", collisionKey, collisionPublic)
	collisionIssued, err := auth.IssueEmail(ctx, collisionTx, collisionProof)
	if err != nil || collisionTx.Commit() != nil {
		t.Fatal("could not establish isolated other-owner fixture")
	}
	if _, err := collisionIssued.AfterCommit(ctx, db); err != nil {
		t.Fatal("could not confirm isolated other-owner fixture")
	}
	collisionInput := ExchangeInput{ApprovalID: collisionApproval.ID, Secret: collisionApproval.Secret,
		Proof: exchangeProof(t, collisionKey, collisionApproval.ID, collisionApproval.Nonce)}
	if _, err := exchanger.Exchange(ctx, db, collisionInput); !errors.Is(err, ErrUnavailable) {
		t.Fatal("device key belonging to another subject was merged")
	}
	// Distinct approval challenges cannot reuse the same device-key/JTI pair.
	replayKey, replayPublic := testDevice(t)
	replayApprovalA := beginApproved(replayKey, replayPublic)
	replayApprovalB := beginApproved(replayKey, replayPublic)
	sharedJTI, _ := domain.GenerateChallengeID()
	replayInputA := ExchangeInput{ApprovalID: replayApprovalA.ID, Secret: replayApprovalA.Secret,
		Proof: exchangeProofWithJTI(t, replayKey, replayApprovalA.ID, replayApprovalA.Nonce, sharedJTI)}
	replayInputB := ExchangeInput{ApprovalID: replayApprovalB.ID, Secret: replayApprovalB.Secret,
		Proof: exchangeProofWithJTI(t, replayKey, replayApprovalB.ID, replayApprovalB.Nonce, sharedJTI)}
	if token, err := exchanger.Exchange(ctx, db, replayInputA); err != nil || token.ForResponse() == "" {
		t.Fatal("first distinct approval could not be exchanged")
	}
	if _, err := exchanger.Exchange(ctx, db, replayInputB); !errors.Is(err, ErrUnavailable) {
		t.Fatal("same device-key and proof ID were reused across approvals")
	}
	if err := db.QueryRowContext(ctx, `SELECT state FROM auth_login_approval WHERE id = ?`, []byte(replayApprovalB.ID)).Scan(&state); err != nil || state != "approved" {
		t.Fatal("replayed proof altered the second approval")
	}
	// Two valid proofs for one approved device compete under a controlled lock.
	raceKey, racePublic := testDevice(t)
	raceApproval := beginApproved(raceKey, racePublic)
	gate, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal("could not begin exchange race")
	}
	var identityID []byte
	if err := gate.QueryRowContext(ctx, `SELECT id FROM login_identity WHERE user_id = ? AND state = 'active' FOR UPDATE`, oldClaims.SubjectID).Scan(&identityID); err != nil {
		_ = gate.Rollback()
		t.Fatal("could not lock isolated identity")
	}
	proofs := []string{exchangeProof(t, raceKey, raceApproval.ID, raceApproval.Nonce), exchangeProof(t, raceKey, raceApproval.ID, raceApproval.Nonce)}
	type outcome struct {
		token session.AccessToken
		err   error
	}
	results := make(chan outcome, 2)
	for _, signed := range proofs {
		go func(signed string) {
			token, err := exchanger.Exchange(ctx, db, ExchangeInput{ApprovalID: raceApproval.ID, Secret: raceApproval.Secret, Proof: signed})
			results <- outcome{token, err}
		}(signed)
	}
	deadline = time.Now().Add(5 * time.Second)
	blocked = 0
	for time.Now().Before(deadline) {
		if err := db.QueryRowContext(ctx, `SELECT COUNT(DISTINCT w.REQUESTING_ENGINE_TRANSACTION_ID)
            FROM performance_schema.data_lock_waits w JOIN performance_schema.data_locks l ON l.ENGINE_LOCK_ID = w.REQUESTING_ENGINE_LOCK_ID
            WHERE l.OBJECT_SCHEMA = DATABASE() AND l.OBJECT_NAME = 'login_identity'`).Scan(&blocked); err != nil {
			_ = gate.Rollback()
			t.Fatal("could not observe exchange lock waits")
		}
		if blocked >= 2 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if blocked < 2 {
		_ = gate.Rollback()
		t.Fatal("both exchanges did not reach the lock")
	}
	if err := gate.Rollback(); err != nil {
		t.Fatal("could not release exchange gate")
	}
	a, b := <-results, <-results
	if !((a.err == nil && a.token.ForResponse() != "" && errors.Is(b.err, ErrUnavailable)) ||
		(b.err == nil && b.token.ForResponse() != "" && errors.Is(a.err, ErrUnavailable))) {
		t.Fatal("concurrent exchange did not produce one session")
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM auth_session WHERE login_challenge_id = (SELECT challenge_id FROM auth_login_approval WHERE id = ?)`, []byte(raceApproval.ID)).Scan(&count); err != nil || count != 1 {
		t.Fatal("concurrent exchange left multiple sessions")
	}
	t.Run("caller-owned exchange and encrypted receipt commit or roll back together", func(t *testing.T) {
		crypto, err := approvalreceipt.New(approvalreceipt.KeyConfig{IdempotencyHMACKey: bytes.Repeat([]byte{0x71}, 32),
			RequestHMACKey: bytes.Repeat([]byte{0x72}, 32), AES256GCMKey: bytes.Repeat([]byte{0x73}, 32)})
		if err != nil {
			t.Fatal("could not set up exchange receipt crypto")
		}
		ledger, err := approvalledger.New(crypto, "exchange-seam-test-v1")
		if err != nil {
			t.Fatal("could not set up exchange receipt ledger")
		}
		init, err := db.BeginTx(ctx, nil)
		if err != nil || ledger.Initialize(ctx, init) != nil || init.Commit() != nil {
			if init != nil {
				_ = init.Rollback()
			}
			t.Fatal("could not initialize exchange receipt epoch")
		}
		device, public := testDevice(t)
		item := beginApproved(device, public)
		exchangeInput := ExchangeInput{ApprovalID: item.ID, Secret: item.Secret, Proof: exchangeProof(t, device, item.ID, item.Nonce)}
		binding, err := crypto.Summarize(approvalreceipt.Request{IdempotencyKey: "AQEBAQEBAQEBAQEBAQEBAQ",
			ApprovalID: item.ID, ApprovalSecret: item.Secret, DeviceProof: exchangeInput.Proof})
		if err != nil {
			t.Fatal("could not bind exchange request")
		}
		approvalScope, err := crypto.ApprovalScope(item.ID)
		if err != nil {
			t.Fatal("could not derive approval scope")
		}
		approvalDigest := approvalScope.Digest()
		expires := time.Now().UTC().Add(30 * time.Second).Truncate(time.Microsecond)
		stage := func(tx *sql.Tx) (exchangeCandidate, string, *approvalledger.Handle, approvalreceipt.Sealed) {
			t.Helper()
			candidate, err := exchanger.exchangeInTransaction(ctx, tx, exchangeInput)
			if err != nil {
				t.Fatal("could not stage exchange")
			}
			var sessionID []byte
			if err := tx.QueryRowContext(ctx, `SELECT exchange_session_id FROM auth_login_approval WHERE id=?`, []byte(item.ID)).Scan(&sessionID); err != nil || len(sessionID) == 0 {
				t.Fatal("staged exchange session missing")
			}
			sealed, err := candidate.sealForReceipt(ctx, tx, crypto, "exchange-seam-test-v1", "AQEBAQEBAQEBAQEBAQEBAQ", expires, auth)
			if err != nil || sealed.subjectID != oldClaims.SubjectID || sealed.reference != string(sessionID) ||
				sealed.binding != binding || !sealed.expiresAt.Equal(expires) {
				t.Fatal("could not seal the staged exchange with its original binding")
			}
			handle, err := ledger.Lock(ctx, tx, sealed.subjectID, sealed.binding)
			if err != nil || handle.Exists() {
				t.Fatal("could not reserve new exchange receipt")
			}
			return candidate, string(sessionID), handle, sealed.sealed
		}
		assertNoExchange := func(sessionID string) {
			t.Helper()
			var state string
			var rows int
			if err := db.QueryRowContext(ctx, `SELECT state FROM auth_login_approval WHERE id=?`, []byte(item.ID)).Scan(&state); err != nil || state != "approved" ||
				db.QueryRowContext(ctx, `SELECT COUNT(*) FROM auth_session WHERE id=?`, []byte(sessionID)).Scan(&rows) != nil || rows != 0 ||
				db.QueryRowContext(ctx, `SELECT COUNT(*) FROM auth_approval_exchange_receipt WHERE approval_scope=?`, approvalDigest[:]).Scan(&rows) != nil || rows != 0 {
				t.Fatal("rolled-back exchange left a consumed approval, session or receipt")
			}
		}
		rollback, err := db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal("could not begin exchange rollback")
		}
		rolledCandidate, rolledSession, rolledHandle, rolledSealed := stage(rollback)
		if err := rolledHandle.Put(ctx, rollback, rolledSession, expires, rolledSealed, auth); err != nil || rolledCandidate.verifyForTransaction(ctx, rollback) != nil {
			_ = rollback.Rollback()
			t.Fatal("could not stage exchange receipt")
		}
		if err := rollback.Rollback(); err != nil {
			t.Fatal("could not roll back exchange and receipt")
		}
		assertNoExchange(rolledSession)

		mismatched, err := db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal("could not begin mismatched binding test")
		}
		_, mismatchedSession, _, correctCiphertext := stage(mismatched)
		wrongBinding, err := crypto.Summarize(approvalreceipt.Request{IdempotencyKey: "AQEBAQEBAQEBAQEBAQEBAQ",
			ApprovalID: item.ID, ApprovalSecret: base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{0x45}, 32)), DeviceProof: exchangeInput.Proof})
		if err != nil {
			_ = mismatched.Rollback()
			t.Fatal("could not construct mismatched request binding")
		}
		wrongHandle, err := ledger.Lock(ctx, mismatched, oldClaims.SubjectID, wrongBinding)
		if err != nil || wrongHandle.Put(ctx, mismatched, mismatchedSession, expires, correctCiphertext, auth) == nil {
			_ = mismatched.Rollback()
			t.Fatal("receipt accepted a binding from a different request")
		}
		if err := mismatched.Rollback(); err != nil {
			t.Fatal("could not roll back mismatched binding")
		}
		assertNoExchange(mismatchedSession)

		expiring, err := db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal("could not begin post-receipt expiry test")
		}
		approvalDeadline := time.Now().UTC().Add(5 * time.Second).Truncate(time.Microsecond)
		if _, err := expiring.ExecContext(ctx, `UPDATE auth_login_approval SET expires_at=? WHERE id=?`, timestamp(approvalDeadline), []byte(item.ID)); err != nil {
			_ = expiring.Rollback()
			t.Fatal("could not set isolated approval deadline")
		}
		expiringCandidate, expiringSession, expiringHandle, expiringSealed := stage(expiring)
		if err := expiringHandle.Put(ctx, expiring, expiringSession, expires, expiringSealed, auth); err != nil {
			_ = expiring.Rollback()
			t.Fatal("could not stage receipt before expiry")
		}
		if _, err := domain.VerifyDeviceProof(exchangeInput.Proof, public,
			domain.ProofExpectation{Purpose: "approval_exchange", ResourceID: item.ID, Nonce: item.Nonce}, approvalDeadline, exchanger.policy); err != nil ||
			!expiringCandidate.sourceExpiry.After(approvalDeadline) || !expiringCandidate.approvalExpiry.Equal(approvalDeadline) {
			_ = expiring.Rollback()
			t.Fatal("approval expiry fixture does not isolate approval lifetime")
		}
		exchanger.now = func() time.Time { return approvalDeadline }
		err = expiringCandidate.verifyForTransaction(ctx, expiring)
		exchanger.now = time.Now
		if !errors.Is(err, ErrUnavailable) {
			_ = expiring.Rollback()
			t.Fatal("candidate remained valid after approval expiry")
		}
		if err := expiring.Rollback(); err != nil {
			t.Fatal("could not roll back expired exchange")
		}
		assertNoExchange(expiringSession)

		proofExpiring, err := db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal("could not begin post-receipt proof expiry test")
		}
		proofCandidate, proofSession, proofHandle, proofSealed := stage(proofExpiring)
		if err := proofHandle.Put(ctx, proofExpiring, proofSession, expires, proofSealed, auth); err != nil {
			_ = proofExpiring.Rollback()
			t.Fatal("could not stage receipt before proof expiry")
		}
		verified, err := domain.VerifyDeviceProof(exchangeInput.Proof, public,
			domain.ProofExpectation{Purpose: "approval_exchange", ResourceID: item.ID, Nonce: item.Nonce}, time.Now().UTC(), exchanger.policy)
		proofDeadline := verified.ExpiresAt.Add(exchanger.policy.ClockSkew)
		if err != nil || !item.ExpiresAt.After(proofDeadline) || !proofCandidate.sourceExpiry.After(proofDeadline) {
			_ = proofExpiring.Rollback()
			t.Fatal("proof expiry fixture does not isolate proof lifetime")
		}
		exchanger.now = func() time.Time { return proofDeadline }
		err = proofCandidate.verifyForTransaction(ctx, proofExpiring)
		exchanger.now = time.Now
		if !errors.Is(err, ErrUnavailable) {
			_ = proofExpiring.Rollback()
			t.Fatal("candidate remained valid after proof expiry")
		}
		if err := proofExpiring.Rollback(); err != nil {
			t.Fatal("could not roll back expired proof")
		}
		assertNoExchange(proofSession)

		if _, err := db.ExecContext(ctx, `CREATE TRIGGER synthetic_exchange_receipt_failure BEFORE INSERT ON auth_approval_exchange_receipt FOR EACH ROW SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='synthetic receipt failure'`); err != nil {
			t.Fatal("could not install synthetic receipt failure")
		}
		defer func() {
			_, _ = db.ExecContext(context.Background(), `DROP TRIGGER IF EXISTS synthetic_exchange_receipt_failure`)
		}()
		failed, err := db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal("could not begin failed receipt transaction")
		}
		_, failedSession, failedHandle, failedSealed := stage(failed)
		if err := failedHandle.Put(ctx, failed, failedSession, expires, failedSealed, auth); err == nil {
			_ = failed.Rollback()
			t.Fatal("synthetic receipt failure returned success")
		}
		if err := failed.Rollback(); err != nil {
			t.Fatal("could not roll back failed receipt transaction")
		}
		assertNoExchange(failedSession)
		if _, err := db.ExecContext(ctx, `DROP TRIGGER synthetic_exchange_receipt_failure`); err != nil {
			t.Fatal("could not remove synthetic receipt failure")
		}

		committing, err := db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal("could not begin successful exchange")
		}
		committedCandidate, committedSession, committedHandle, committedSealed := stage(committing)
		if err := committedHandle.Put(ctx, committing, committedSession, expires, committedSealed, auth); err != nil || committedCandidate.verifyForTransaction(ctx, committing) != nil {
			_ = committing.Rollback()
			t.Fatal("could not store exchange receipt")
		}
		if err := committing.Commit(); err != nil {
			t.Fatal("could not commit exchange and receipt")
		}
		var state string
		var rows int
		if err := db.QueryRowContext(ctx, `SELECT state FROM auth_login_approval WHERE id=?`, []byte(item.ID)).Scan(&state); err != nil || state != "consumed" ||
			db.QueryRowContext(ctx, `SELECT COUNT(*) FROM auth_session WHERE id=?`, []byte(committedSession)).Scan(&rows) != nil || rows != 1 ||
			db.QueryRowContext(ctx, `SELECT COUNT(*) FROM auth_approval_exchange_receipt WHERE approval_scope=?`, approvalDigest[:]).Scan(&rows) != nil || rows != 1 {
			t.Fatal("committed exchange did not retain one session and receipt")
		}
		replayTx, err := db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal("could not begin receipt inspection")
		}
		defer replayTx.Rollback()
		replayHandle, err := ledger.Lock(ctx, replayTx, oldClaims.SubjectID, binding)
		if err != nil || !replayHandle.Exists() {
			t.Fatal("committed receipt was not found")
		}
		opened, err := replayHandle.Open(ctx, replayTx, auth)
		if err != nil || string(opened.Bytes()) != committedCandidate.token.ForResponse() {
			t.Fatal("committed receipt did not match session")
		}
		// This low-level test does not prove the future coordinator revalidates
		// the original proof/JTI or releases plaintext only after commit.
	})
	t.Run("receipt coordinator authenticates first exchange and exact retry", func(t *testing.T) {
		crypto, err := approvalreceipt.New(approvalreceipt.KeyConfig{IdempotencyHMACKey: bytes.Repeat([]byte{0x71}, 32),
			RequestHMACKey: bytes.Repeat([]byte{0x72}, 32), AES256GCMKey: bytes.Repeat([]byte{0x73}, 32)})
		if err != nil {
			t.Fatal("could not initialize receipt cryptography")
		}
		ledger, err := approvalledger.New(crypto, "exchange-seam-test-v1")
		if err != nil {
			t.Fatal("could not initialize receipt ledger")
		}
		coordinator, err := NewReceiptExchanger(exchanger, ledger, crypto, auth, "exchange-seam-test-v1", 3*time.Minute)
		if err != nil {
			t.Fatal("could not initialize receipt exchanger")
		}
		init, err := db.BeginTx(ctx, nil)
		if err != nil || coordinator.Initialize(ctx, init) != nil || init.Commit() != nil {
			if init != nil {
				_ = init.Rollback()
			}
			t.Fatal("could not verify receipt epoch")
		}
		const idem = "AQEBAQEBAQEBAQEBAQEBAQ"
		const otherIdem = "AgICAgICAgICAgICAgICAg"
		device, public := testDevice(t)
		item := beginApproved(device, public)
		input := ExchangeInput{ApprovalID: item.ID, Secret: item.Secret, Proof: exchangeProof(t, device, item.ID, item.Nonce)}
		first, err := coordinator.Exchange(ctx, db, idem, input)
		if err != nil || first == "" {
			t.Fatal("first authorized exchange did not return a committed session")
		}
		replayed, err := coordinator.Exchange(ctx, db, idem, input)
		if err != nil || replayed != first {
			t.Fatal("exact retry did not recover the same session")
		}
		claims, err := codec.Verify(first)
		if err != nil || claims.SubjectID != oldClaims.SubjectID || claims.SessionID == oldClaims.SessionID {
			t.Fatal("issued session was not bound to the original subject and new device")
		}
		var count int
		if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM auth_session WHERE login_challenge_id=(SELECT challenge_id FROM auth_login_approval WHERE id=?)`, []byte(item.ID)).Scan(&count); err != nil || count != 1 {
			t.Fatal("exact retry created more than one session")
		}
		if token, err := coordinator.Exchange(ctx, db, otherIdem, input); token != "" || !errors.Is(err, ErrExchangeReceiptConflict) {
			t.Fatal("different idempotency key did not conflict")
		}
		changedProof := input
		changedProof.Proof = exchangeProof(t, device, item.ID, item.Nonce)
		if token, err := coordinator.Exchange(ctx, db, idem, changedProof); token != "" || !errors.Is(err, ErrExchangeReceiptConflict) {
			t.Fatal("different valid device proof did not conflict")
		}
		wrongSecret := input
		wrongSecret.Secret = base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{0x45}, 32))
		if token, err := coordinator.Exchange(ctx, db, idem, wrongSecret); token != "" || !errors.Is(err, ErrUnavailable) {
			t.Fatal("wrong approval secret released a session")
		}
		wrongDevice := input
		wrongDevice.Proof = exchangeProof(t, oldKey, item.ID, item.Nonce)
		if token, err := coordinator.Exchange(ctx, db, idem, wrongDevice); token != "" || !errors.Is(err, ErrUnavailable) {
			t.Fatal("wrong device released a session")
		}
		if _, err := db.ExecContext(ctx, `UPDATE auth_session SET state='revoked',revoked_at=UTC_TIMESTAMP(6) WHERE id=?`, []byte(oldClaims.SessionID)); err != nil {
			t.Fatal("could not revoke source session")
		}
		if token, err := coordinator.Exchange(ctx, db, idem, input); token != "" || !errors.Is(err, ErrUnavailable) {
			t.Fatal("revoked source session released a receipt")
		}
		firstBinding, err := crypto.Summarize(approvalreceipt.Request{IdempotencyKey: idem, ApprovalID: input.ApprovalID, ApprovalSecret: input.Secret, DeviceProof: input.Proof})
		if err != nil {
			t.Fatal("could not derive original recovery binding")
		}
		if token, err := coordinator.recoverCommitted(ctx, db, idem, input, firstBinding); token != "" || !errors.Is(err, ErrUnavailable) {
			t.Fatal("post-commit recovery released a session after source revocation")
		}
		var targetState string
		if err := db.QueryRowContext(ctx, `SELECT state FROM auth_session WHERE id=?`, []byte(claims.SessionID)).Scan(&targetState); err != nil || targetState != "active" {
			t.Fatal("failed recovery revoked a session that may already have been delivered")
		}
		if _, err := db.ExecContext(ctx, `UPDATE auth_session SET state='active',revoked_at=NULL WHERE id=?`, []byte(oldClaims.SessionID)); err != nil {
			t.Fatal("could not restore synthetic source session")
		}
		if _, err := db.ExecContext(ctx, `UPDATE auth_session SET state='revoked',revoked_at=UTC_TIMESTAMP(6) WHERE id=?`, []byte(claims.SessionID)); err != nil {
			t.Fatal("could not revoke target session")
		}
		if token, err := coordinator.Exchange(ctx, db, idem, input); token != "" || !errors.Is(err, ErrUnavailable) {
			t.Fatal("revoked target session released a receipt")
		}
		if _, err := db.ExecContext(ctx, `UPDATE auth_session SET state='active',revoked_at=NULL WHERE id=?`, []byte(claims.SessionID)); err != nil {
			t.Fatal("could not restore synthetic target session")
		}
		fingerprint, err := domain.DeviceKeyFingerprint(public)
		if err != nil {
			t.Fatal("could not derive synthetic fingerprint")
		}
		if _, err := db.ExecContext(ctx, `DELETE FROM auth_device_proof_replay WHERE device_fingerprint=?`, fingerprint[:]); err != nil {
			t.Fatal("could not delete synthetic replay marker")
		}
		if token, err := coordinator.Exchange(ctx, db, idem, input); token != "" || !errors.Is(err, ErrUnavailable) {
			t.Fatal("missing original proof marker released a receipt")
		}

		raceDevice, racePublic := testDevice(t)
		raceItem := beginApproved(raceDevice, racePublic)
		raceInput := ExchangeInput{ApprovalID: raceItem.ID, Secret: raceItem.Secret, Proof: exchangeProof(t, raceDevice, raceItem.ID, raceItem.Nonce)}
		type result struct {
			token string
			err   error
		}
		results := make(chan result, 2)
		for range 2 {
			go func() { token, err := coordinator.Exchange(ctx, db, idem, raceInput); results <- result{token, err} }()
		}
		left, right := <-results, <-results
		if left.err != nil || right.err != nil || left.token == "" || left.token != right.token {
			t.Fatalf("concurrent exact requests did not converge: left error=%v, right error=%v, left present=%t, right present=%t, equal=%t",
				left.err, right.err, left.token != "", right.token != "", left.token == right.token)
		}
		if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM auth_session WHERE login_challenge_id=(SELECT challenge_id FROM auth_login_approval WHERE id=?)`, []byte(raceItem.ID)).Scan(&count); err != nil || count != 1 {
			t.Fatal("concurrent exact requests created multiple sessions")
		}
		raceScope, err := crypto.ApprovalScope(raceItem.ID)
		if err != nil {
			t.Fatal("could not derive race approval scope")
		}
		raceDigest := raceScope.Digest()
		gate, err := db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal("could not begin receipt lock-wait test")
		}
		var lockedScope []byte
		var expiryRaw string
		if err := gate.QueryRowContext(ctx, `SELECT approval_scope,DATE_FORMAT(expires_at,'%Y-%m-%d %H:%i:%s.%f') FROM auth_approval_exchange_receipt WHERE approval_scope=? FOR UPDATE`, raceDigest[:]).
			Scan(&lockedScope, &expiryRaw); err != nil {
			_ = gate.Rollback()
			t.Fatal("could not lock synthetic receipt")
		}
		receiptExpiry, expiryErr := time.Parse("2006-01-02 15:04:05.000000", expiryRaw)
		verified, proofErr := domain.VerifyDeviceProof(raceInput.Proof, racePublic,
			domain.ProofExpectation{Purpose: "approval_exchange", ResourceID: raceItem.ID, Nonce: raceItem.Nonce}, time.Now().UTC(), exchanger.policy)
		proofDeadline := verified.ExpiresAt.Add(exchanger.policy.ClockSkew)
		if expiryErr != nil || proofErr != nil || !receiptExpiry.After(proofDeadline) || !raceItem.ExpiresAt.After(proofDeadline) || !oldClaims.ExpiresAt.After(proofDeadline) {
			_ = gate.Rollback()
			t.Fatal("receipt lock fixture does not isolate proof expiry")
		}
		var fakeClock atomic.Int64
		fakeClock.Store(time.Now().UnixNano())
		exchanger.now = func() time.Time { return time.Unix(0, fakeClock.Load()) }
		waited := make(chan result, 1)
		go func() { token, err := coordinator.Exchange(ctx, db, idem, raceInput); waited <- result{token, err} }()
		deadline := time.Now().Add(5 * time.Second)
		blocked := 0
		for time.Now().Before(deadline) {
			if err := db.QueryRowContext(ctx, `SELECT COUNT(DISTINCT w.REQUESTING_ENGINE_TRANSACTION_ID)
				FROM performance_schema.data_lock_waits w JOIN performance_schema.data_locks l ON l.ENGINE_LOCK_ID=w.REQUESTING_ENGINE_LOCK_ID
				WHERE l.OBJECT_SCHEMA=DATABASE() AND l.OBJECT_NAME='auth_approval_exchange_receipt'`).Scan(&blocked); err != nil {
				_ = gate.Rollback()
				t.Fatal("could not observe receipt lock wait")
			}
			if blocked > 0 {
				break
			}
			time.Sleep(20 * time.Millisecond)
		}
		if blocked == 0 {
			_ = gate.Rollback()
			t.Fatal("replay did not reach receipt lock")
		}
		fakeClock.Store(proofDeadline.UnixNano())
		if err := gate.Rollback(); err != nil {
			t.Fatal("could not release receipt lock")
		}
		waitedResult := <-waited
		if waitedResult.token != "" || !errors.Is(waitedResult.err, ErrUnavailable) {
			exchanger.now = time.Now
			t.Fatal("proof expiration during receipt lock wait released a session")
		}
		raceBinding, err := crypto.Summarize(approvalreceipt.Request{IdempotencyKey: idem, ApprovalID: raceInput.ApprovalID, ApprovalSecret: raceInput.Secret, DeviceProof: raceInput.Proof})
		if err != nil {
			exchanger.now = time.Now
			t.Fatal("could not derive race recovery binding")
		}
		if token, err := coordinator.recoverCommitted(ctx, db, idem, raceInput, raceBinding); token != "" || !errors.Is(err, ErrUnavailable) {
			exchanger.now = time.Now
			t.Fatal("post-commit recovery released a session after proof expiry")
		}
		raceClaims, err := codec.Verify(left.token)
		if err != nil || db.QueryRowContext(ctx, `SELECT state FROM auth_session WHERE id=?`, []byte(raceClaims.SessionID)).Scan(&targetState) != nil || targetState != "active" {
			exchanger.now = time.Now
			t.Fatal("failed proof recovery revoked a session that may already have been delivered")
		}
		exchanger.now = time.Now
		if token, err := coordinator.Exchange(ctx, db, idem, raceInput); err != nil || token != left.token {
			t.Fatal("failed lock-wait replay changed the committed result")
		}

		uncertainDevice, uncertainPublic := testDevice(t)
		uncertainItem := beginApproved(uncertainDevice, uncertainPublic)
		uncertainInput := ExchangeInput{ApprovalID: uncertainItem.ID, Secret: uncertainItem.Secret, Proof: exchangeProof(t, uncertainDevice, uncertainItem.ID, uncertainItem.Nonce)}
		uncertainDB, failAcknowledgement := isolatedDBWithCommitAckFailure(t)
		if token, err := coordinator.Exchange(ctx, uncertainDB, idem, uncertainInput); token != "" || !errors.Is(err, ErrUnavailable) || failAcknowledgement.Load() {
			t.Fatal("lost commit acknowledgement released a credential or was not injected")
		}
		var uncertainState string
		if err := db.QueryRowContext(ctx, `SELECT state FROM auth_login_approval WHERE id=?`, []byte(uncertainItem.ID)).Scan(&uncertainState); err != nil || uncertainState != "consumed" ||
			db.QueryRowContext(ctx, `SELECT COUNT(*) FROM auth_session WHERE login_challenge_id=(SELECT challenge_id FROM auth_login_approval WHERE id=?)`, []byte(uncertainItem.ID)).Scan(&count) != nil || count != 1 {
			t.Fatal("synthetic lost acknowledgement did not follow a real successful commit")
		}
		recovered, err := coordinator.Exchange(ctx, db, idem, uncertainInput)
		if err != nil || recovered == "" {
			t.Fatal("exact retry did not recover a committed result after lost acknowledgement")
		}
		if again, err := coordinator.Exchange(ctx, db, idem, uncertainInput); err != nil || again != recovered ||
			db.QueryRowContext(ctx, `SELECT COUNT(*) FROM auth_session WHERE login_challenge_id=(SELECT challenge_id FROM auth_login_approval WHERE id=?)`, []byte(uncertainItem.ID)).Scan(&count) != nil || count != 1 {
			t.Fatal("lost-ack recovery changed or duplicated the committed session")
		}

		failedDevice, failedPublic := testDevice(t)
		failedItem := beginApproved(failedDevice, failedPublic)
		failedInput := ExchangeInput{ApprovalID: failedItem.ID, Secret: failedItem.Secret, Proof: exchangeProof(t, failedDevice, failedItem.ID, failedItem.Nonce)}
		if _, err := db.ExecContext(ctx, `CREATE TRIGGER synthetic_coordinator_receipt_failure BEFORE INSERT ON auth_approval_exchange_receipt FOR EACH ROW SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='synthetic receipt failure'`); err != nil {
			t.Fatal("could not install synthetic receipt failure")
		}
		defer func() {
			_, _ = db.ExecContext(context.Background(), `DROP TRIGGER IF EXISTS synthetic_coordinator_receipt_failure`)
		}()
		if token, err := coordinator.Exchange(ctx, db, idem, failedInput); token != "" || !errors.Is(err, ErrUnavailable) {
			t.Fatal("failed receipt write returned a session")
		}
		var state string
		if err := db.QueryRowContext(ctx, `SELECT state FROM auth_login_approval WHERE id=?`, []byte(failedItem.ID)).Scan(&state); err != nil || state != "approved" ||
			db.QueryRowContext(ctx, `SELECT COUNT(*) FROM auth_session WHERE login_challenge_id=(SELECT challenge_id FROM auth_login_approval WHERE id=?)`, []byte(failedItem.ID)).Scan(&count) != nil || count != 0 {
			t.Fatal("failed receipt write retained an exchange")
		}
		if _, err := db.ExecContext(ctx, `DROP TRIGGER synthetic_coordinator_receipt_failure`); err != nil {
			t.Fatal("could not remove synthetic receipt failure")
		}
		if token, err := coordinator.Exchange(ctx, db, idem, failedInput); err != nil || token == "" {
			t.Fatal("rolled-back exchange could not retry its original proof")
		}

		missingDevice, missingPublic := testDevice(t)
		missingItem := beginApproved(missingDevice, missingPublic)
		missingInput := ExchangeInput{ApprovalID: missingItem.ID, Secret: missingItem.Secret, Proof: exchangeProof(t, missingDevice, missingItem.ID, missingItem.Nonce)}
		if token, err := coordinator.Exchange(ctx, db, idem, missingInput); err != nil || token == "" {
			t.Fatal("could not create receipt deletion fixture")
		}
		scope, err := crypto.ApprovalScope(missingItem.ID)
		if err != nil {
			t.Fatal("could not derive receipt deletion fixture")
		}
		digest := scope.Digest()
		if _, err := db.ExecContext(ctx, `DELETE FROM auth_approval_exchange_receipt WHERE approval_scope=?`, digest[:]); err != nil {
			t.Fatal("could not delete synthetic receipt")
		}
		if token, err := coordinator.Exchange(ctx, db, idem, missingInput); token != "" || !errors.Is(err, ErrUnavailable) {
			t.Fatal("missing receipt caused a second exchange")
		}
		if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM auth_session WHERE login_challenge_id=(SELECT challenge_id FROM auth_login_approval WHERE id=?)`, []byte(missingItem.ID)).Scan(&count); err != nil || count != 1 {
			t.Fatal("missing receipt recreated a session")
		}

		tamperDevice, tamperPublic := testDevice(t)
		tamperItem := beginApproved(tamperDevice, tamperPublic)
		tamperInput := ExchangeInput{ApprovalID: tamperItem.ID, Secret: tamperItem.Secret, Proof: exchangeProof(t, tamperDevice, tamperItem.ID, tamperItem.Nonce)}
		if token, err := coordinator.Exchange(ctx, db, idem, tamperInput); err != nil || token == "" {
			t.Fatal("could not create receipt tampering fixture")
		}
		tamperScope, err := crypto.ApprovalScope(tamperItem.ID)
		if err != nil {
			t.Fatal("could not derive tampering fixture scope")
		}
		tamperDigest := tamperScope.Digest()
		var ciphertext []byte
		if err := db.QueryRowContext(ctx, `SELECT response_enc FROM auth_approval_exchange_receipt WHERE approval_scope=?`, tamperDigest[:]).Scan(&ciphertext); err != nil || len(ciphertext) < 2 {
			t.Fatal("could not read synthetic ciphertext")
		}
		ciphertext[len(ciphertext)-1] ^= 1
		if _, err := db.ExecContext(ctx, `UPDATE auth_approval_exchange_receipt SET response_enc=? WHERE approval_scope=?`, ciphertext, tamperDigest[:]); err != nil {
			t.Fatal("could not tamper synthetic receipt")
		}
		if token, err := coordinator.Exchange(ctx, db, idem, tamperInput); token != "" || !errors.Is(err, ErrUnavailable) {
			t.Fatal("tampered receipt released a credential")
		}
		if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM auth_session WHERE login_challenge_id=(SELECT challenge_id FROM auth_login_approval WHERE id=?)`, []byte(tamperItem.ID)).Scan(&count); err != nil || count != 1 {
			t.Fatal("tampered receipt recreated a session")
		}

		longRetention, err := NewReceiptExchanger(exchanger, ledger, crypto, auth, "exchange-seam-test-v1", 2*time.Hour)
		if err != nil {
			t.Fatal("could not configure long receipt retention")
		}
		shortDevice, shortPublic := testDevice(t)
		shortItem := beginApproved(shortDevice, shortPublic)
		shortInput := ExchangeInput{ApprovalID: shortItem.ID, Secret: shortItem.Secret, Proof: exchangeProof(t, shortDevice, shortItem.ID, shortItem.Nonce)}
		const contractKey = "0123456789abcdef0123456789abcdef="
		shortToken, err := longRetention.Exchange(ctx, db, contractKey, shortInput)
		if err != nil || shortToken == "" {
			t.Fatal("retention longer than the session prevented exchange")
		}
		if retry, err := longRetention.Exchange(ctx, db, contractKey, shortInput); err != nil || retry != shortToken {
			t.Fatal("contract-valid non-base64url key could not be retried")
		}
		shortClaims, err := codec.Verify(shortToken)
		shortScope, scopeErr := crypto.ApprovalScope(shortItem.ID)
		shortDigest := shortScope.Digest()
		var shortExpiryRaw string
		if err != nil || scopeErr != nil || db.QueryRowContext(ctx, `SELECT DATE_FORMAT(expires_at,'%Y-%m-%d %H:%i:%s.%f') FROM auth_approval_exchange_receipt WHERE approval_scope=?`, shortDigest[:]).Scan(&shortExpiryRaw) != nil {
			t.Fatal("could not inspect capped receipt lifetime")
		}
		shortExpiry, err := time.Parse("2006-01-02 15:04:05.000000", shortExpiryRaw)
		if err != nil || !shortExpiry.Equal(shortClaims.ExpiresAt) {
			t.Fatal("receipt outlived its issued session")
		}
	})
}
