package application

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"database/sql"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"nerae/api/internal/dbtx"
	authbootstraptest "nerae/api/internal/modules/auth/bootstrap/testsupport"
	"nerae/api/internal/modules/auth/challenge"
	authdomain "nerae/api/internal/modules/auth/domain"
	"nerae/api/internal/modules/auth/identity"
	authrepo "nerae/api/internal/modules/auth/repository"
	"nerae/api/internal/modules/auth/session"
	"nerae/api/internal/modules/workout/domain"
	"nerae/api/internal/modules/workout/repository"
)

func TestWorkoutCompletionTransactions(t *testing.T) {
	db := integrationDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	hasher, _ := authdomain.NewEmailCodeHasher(bytes.Repeat([]byte{1}, 32))
	replay, _ := authrepo.NewProofReplayStore(bytes.Repeat([]byte{2}, 32))
	emailProtector, _ := authdomain.NewEmailIdentityProtector(bytes.Repeat([]byte{3}, 32), bytes.Repeat([]byte{4}, 32), "test-key")
	challenges, err := challenge.NewStore(hasher, replay, emailProtector, challenge.Policy{ChallengeTTL: 5 * time.Minute,
		MaxAttempts: 3, DeviceProof: authdomain.ProofPolicy{MaxLifetime: 5 * time.Minute, ClockSkew: 30 * time.Second}})
	if err != nil {
		t.Fatal("could not initialize challenge store")
	}
	authbootstraptest.InitializeIdentityEpoch(t, db, emailProtector)
	resolver, _ := identity.NewResolver(emailProtector)
	codec, _ := session.NewTokenCodec(bytes.Repeat([]byte{5}, 32), session.TokenPolicy{MaxLifetime: time.Hour})
	auth, _ := session.NewStore(resolver, codec)
	protector, _ := repository.NewExerciseNameProtector(bytes.Repeat([]byte{6}, 32), "workout-test-key")
	initTx, err := db.BeginTx(ctx, nil)
	if err != nil || protector.InitializeExerciseKey(ctx, initTx) != nil || initTx.Commit() != nil {
		t.Fatal("could not initialize isolated encryption key")
	}
	records, _ := repository.NewRecordStore(protector)
	service, _ := New(auth, repository.NewSessionStore(), records)
	login := func(email string, key *ecdsa.PrivateKey, public authdomain.DevicePublicJWK) (string, session.TokenClaims) {
		t.Helper()
		id, _ := authdomain.GenerateChallengeID()
		nonce, _ := authdomain.GenerateChallengeNonce()
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal("could not begin isolated challenge")
		}
		defer tx.Rollback()
		if err := challenges.Issue(ctx, tx, challenge.IssueInput{Email: email, ChallengeID: id, Nonce: nonce, Code: "012345", PublicKey: public}); err != nil || tx.Commit() != nil {
			t.Fatal("could not issue isolated challenge")
		}
		signed := integrationProof(t, key, id, nonce)
		tx, err = db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal("could not begin isolated authentication")
		}
		defer tx.Rollback()
		attempt, err := challenges.Attempt(ctx, tx, challenge.AttemptInput{ChallengeID: id, Code: "012345", DeviceProof: signed})
		if err != nil || !attempt.Verified {
			t.Fatal("could not verify isolated challenge")
		}
		issued, err := auth.IssueEmail(ctx, tx, attempt.Proof)
		if err != nil || tx.Commit() != nil {
			t.Fatal("could not commit isolated session")
		}
		token, err := issued.AfterCommit(ctx, db)
		if err != nil {
			t.Fatal("could not release isolated session")
		}
		claims, err := codec.Verify(token)
		if err != nil {
			t.Fatal("could not verify isolated session token")
		}
		return token, claims
	}
	key, public := integrationDevice(t)
	otherKey, otherPublic := integrationDevice(t)
	token, claims := login("finish@example.test", key, public)
	tokenAgain, claimsAgain := login("finish@example.test", key, public)
	other, otherClaims := login("finish-other@example.test", otherKey, otherPublic)
	subjectID, err := strconv.ParseUint(claims.SubjectID, 10, 64)
	if err != nil || subjectID == 0 {
		t.Fatal("invalid test subject id")
	}
	if _, err := records.CompleteFreeTx(ctx, nil, subjectID, 1, 1); !errors.Is(err, repository.ErrInvalid) {
		t.Fatal("nil completion capability was not rejected")
	}
	if _, err := records.CompleteFree(ctx, nil, subjectID, 1, 1); !errors.Is(err, repository.ErrInvalid) {
		t.Fatal("legacy nil completion transaction was not rejected")
	}
	statements := repository.CompletionStatements()
	if len(statements) != 1 || !strings.HasPrefix(strings.ToUpper(strings.TrimSpace(statements[0])), "UPDATE ") {
		t.Fatal("completion write statement list is invalid")
	}
	completionSQL := statements[0]
	statements[0] = "DELETE FROM workout_session"
	if repository.CompletionStatements()[0] != completionSQL {
		t.Fatal("completion SQL statement list was not copied")
	}
	if claims.SubjectID != claimsAgain.SubjectID || claims.SubjectID == otherClaims.SubjectID {
		t.Fatal("invalid test ownership")
	}
	// Synthetic fixture only: the real age-verification flow is not implemented.
	if _, err := db.ExecContext(ctx, `UPDATE user SET age_verified_at = UTC_TIMESTAMP(6) WHERE id IN (?, ?)`, claims.SubjectID, otherClaims.SubjectID); err != nil {
		t.Fatal("could not set isolated age fixture")
	}
	zone := "UTC"
	input := repository.FreeSessionInput{Date: time.Now().UTC().AddDate(0, 0, -1).Format("2006-01-02"), TimeZone: &zone, ClientEventID: "event-finish-test"}
	ref, err := service.CreateFree(ctx, db, token, input)
	if err != nil {
		t.Fatal("could not create isolated workout")
	}
	completeViaCapability := func(credential string, sessionID, expectedVersion uint64) (uint64, error) {
		var version uint64
		err := service.inTransaction(ctx, db, credential, func(tx *sql.Tx, userID uint64) error {
			var err error
			version, err = records.CompleteFreeTx(ctx, dbtx.Wrap(tx), userID, sessionID, expectedVersion)
			return err
		})
		return version, err
	}
	if _, err := service.CompleteFree(ctx, db, token, ref.ID, 1); !errors.Is(err, repository.ErrSetConflict) {
		t.Fatal("empty workout was marked complete")
	}
	before, err := service.ReadFree(ctx, db, token, ref.ID)
	if err != nil || before.Status != domain.InProgress || before.Version != 1 || before.RecordedAt != nil {
		t.Fatal("rejected completion changed an empty workout")
	}
	set := domain.Set{RecordKey: "record-finish-001", ExerciseEntryKey: "entry-finish-001", Exercise: domain.Exercise{Source: domain.Custom, Name: "自定义动作"},
		Index: 1, Kind: domain.Weight, WeightKg: "42.5", Reps: 8}
	if v, changed, err := service.SaveSets(ctx, db, token, ref.ID, 1, domain.Batch{Sets: []domain.Set{set}}, []string{set.ExerciseEntryKey}); err != nil || !changed || v != 2 {
		t.Fatal("could not save isolated group")
	}
	if _, err := service.CompleteFree(ctx, db, other, ref.ID, 2); !errors.Is(err, repository.ErrUnavailable) {
		t.Fatal("different subject completed a private workout")
	}
	if _, err := db.ExecContext(ctx, `DELETE FROM workout_exercise_order WHERE session_id = ?`, ref.ID); err != nil {
		t.Fatal("could not corrupt isolated order fixture")
	}
	if _, err := service.CompleteFree(ctx, db, token, ref.ID, 2); !errors.Is(err, repository.ErrUnavailable) {
		t.Fatal("completion accepted a broken exercise order")
	}
	var brokenVersion uint64
	var brokenStatus string
	var brokenRecorded sql.NullTime
	if err := db.QueryRowContext(ctx, `SELECT version, status, recorded_at FROM workout_session WHERE id = ?`, ref.ID).
		Scan(&brokenVersion, &brokenStatus, &brokenRecorded); err != nil || brokenVersion != 2 || brokenStatus != string(domain.InProgress) || brokenRecorded.Valid {
		t.Fatalf("rejected broken-order completion changed session state: err=%v version=%d status=%q recorded=%t", err, brokenVersion, brokenStatus, brokenRecorded.Valid)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO workout_exercise_order (session_id, exercise_entry_key, position) VALUES (?, ?, 1)`, ref.ID, []byte(set.ExerciseEntryKey)); err != nil {
		t.Fatal("could not repair isolated order fixture")
	}
	if _, err := completeViaCapability(token, ref.ID, 1); !errors.Is(err, repository.ErrVersionConflict) {
		t.Fatal("transaction capability accepted a stale completion version")
	}
	stale, err := service.ReadFree(ctx, db, token, ref.ID)
	if err != nil || stale.Version != 2 || stale.Status != domain.InProgress || stale.RecordedAt != nil {
		t.Fatal("stale transaction capability changed the workout")
	}
	// Exercise successful completion through the capability entry on an
	// independent synthetic record; the main record remains available for the
	// rollback and same-version race assertions below.
	capabilityInput := input
	capabilityInput.ClientEventID = "event-finish-capability"
	capabilityRef, err := service.CreateFree(ctx, db, token, capabilityInput)
	if err != nil {
		t.Fatal("could not create isolated capability workout")
	}
	capabilitySet := domain.Set{RecordKey: "record-finish-cap-001", ExerciseEntryKey: "entry-finish-cap-001", Exercise: domain.Exercise{Source: domain.Custom, Name: "能力测试动作"},
		Index: 1, Kind: domain.Weight, WeightKg: "30", Reps: 6}
	if v, changed, err := service.SaveSets(ctx, db, token, capabilityRef.ID, 1, domain.Batch{Sets: []domain.Set{capabilitySet}}, []string{capabilitySet.ExerciseEntryKey}); err != nil || !changed || v != 2 {
		t.Fatal("could not prepare capability completion workout")
	}
	if v, err := completeViaCapability(token, capabilityRef.ID, 2); err != nil || v != 3 {
		t.Fatal("DBTX completion did not commit successfully")
	}
	capabilityResult, err := service.ReadFree(ctx, db, token, capabilityRef.ID)
	if err != nil || capabilityResult.Status != domain.Completed || capabilityResult.Version != 3 || capabilityResult.RecordedAt == nil || len(capabilityResult.Sets) != 1 {
		t.Fatal("DBTX completion did not read back")
	}
	// Force authentication to change after the completion update but before the
	// service's final check; the status, timestamp and version must all roll back.
	err = service.inTransaction(ctx, db, token, func(tx *sql.Tx, userID uint64) error {
		if _, err := records.CompleteFree(ctx, tx, userID, ref.ID, 2); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `UPDATE user SET token_version = token_version + 1 WHERE id = ?`, userID)
		return err
	})
	if !errors.Is(err, ErrUnavailable) {
		t.Fatal("changed authentication did not conservatively reject completion")
	}
	rolledBack, err := service.ReadFree(ctx, db, token, ref.ID)
	if err != nil || rolledBack.Version != 2 || rolledBack.Status != domain.InProgress || rolledBack.RecordedAt != nil {
		t.Fatal("rejected completion was not fully rolled back")
	}
	// Two authenticated sessions race from the same version. Hold identity so
	// both must actually wait before the test lets them proceed.
	gate, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal("could not start completion race")
	}
	var identityID []byte
	if err := gate.QueryRowContext(ctx, `SELECT id FROM login_identity WHERE user_id = ? AND state = 'active' FOR UPDATE`, claims.SubjectID).Scan(&identityID); err != nil {
		_ = gate.Rollback()
		t.Fatal("could not lock isolated identity")
	}
	type outcome struct {
		version uint64
		err     error
	}
	results := make(chan outcome, 2)
	for _, credential := range []string{token, tokenAgain} {
		go func(credential string) {
			version, err := service.CompleteFree(ctx, db, credential, ref.ID, 2)
			results <- outcome{version, err}
		}(credential)
	}
	deadline := time.Now().Add(5 * time.Second)
	blocked := 0
	for time.Now().Before(deadline) {
		if err := db.QueryRowContext(ctx, `SELECT COUNT(DISTINCT w.REQUESTING_ENGINE_TRANSACTION_ID)
            FROM performance_schema.data_lock_waits w JOIN performance_schema.data_locks l ON l.ENGINE_LOCK_ID = w.REQUESTING_ENGINE_LOCK_ID
            WHERE l.OBJECT_SCHEMA = DATABASE() AND l.OBJECT_NAME = 'login_identity'`).Scan(&blocked); err != nil {
			_ = gate.Rollback()
			t.Fatal("could not observe isolated completion race")
		}
		if blocked >= 2 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if blocked < 2 {
		_ = gate.Rollback()
		t.Fatal("both completion attempts did not reach the lock")
	}
	if err := gate.Rollback(); err != nil {
		t.Fatal("could not release completion gate")
	}
	a, b := <-results, <-results
	if !((a.err == nil && a.version == 3 && errors.Is(b.err, repository.ErrVersionConflict)) ||
		(b.err == nil && b.version == 3 && errors.Is(a.err, repository.ErrVersionConflict))) {
		t.Fatal("same-version completion did not produce one success and one conflict")
	}
	completed, err := service.ReadFree(ctx, db, tokenAgain, ref.ID)
	if err != nil || completed.Status != domain.Completed || completed.Version != 3 || completed.RecordedAt == nil || len(completed.Sets) != 1 {
		t.Fatal("completed workout did not read back correctly")
	}
	var started sql.NullTime
	if err := db.QueryRowContext(ctx, `SELECT started_at FROM workout_session WHERE id = ?`, ref.ID).Scan(&started); err != nil || started.Valid {
		t.Fatal("completion invented a training start time")
	}
	if v, err := service.CompleteFree(ctx, db, token, ref.ID, 3); err != nil || v != 4 {
		t.Fatal("current-version repeat confirmation failed")
	}
	var count int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM workout_session WHERE id = ?`, ref.ID).Scan(&count); err != nil || count != 1 {
		t.Fatal("repeat confirmation created another workout")
	}
	if v, changed, err := service.SaveSets(ctx, db, token, ref.ID, 4, domain.Batch{DeleteKeys: []string{set.RecordKey}}, []string{}); err != nil || !changed || v != 5 {
		t.Fatal("could not delete final group")
	}
	draft, err := service.ReadFree(ctx, db, token, ref.ID)
	if err != nil || draft.Status != domain.InProgress || draft.Version != 5 || len(draft.Sets) != 0 {
		t.Fatal("removing final group did not return to draft")
	}
	if _, err := service.CompleteFree(ctx, db, token, ref.ID, 5); !errors.Is(err, repository.ErrSetConflict) {
		t.Fatal("empty draft was re-completed")
	}
}
