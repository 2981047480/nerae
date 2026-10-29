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
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

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
	challenges, err := challenge.NewStore(hasher, replay, emailProtector, challenge.Policy{ChallengeTTL: 5 * time.Minute,
		MaxAttempts: 3, DeviceProof: domain.ProofPolicy{MaxLifetime: 5 * time.Minute, ClockSkew: 30 * time.Second}})
	if err != nil {
		t.Fatal("could not initialize challenges")
	}
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
	// This fixture is not an implementation of age verification.
	if _, err := db.ExecContext(ctx, `UPDATE user SET age_verified_at = UTC_TIMESTAMP(6) WHERE id = ?`, oldClaims.SubjectID); err != nil {
		t.Fatal("could not set isolated age fixture")
	}
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
}
