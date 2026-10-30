package session

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	authbootstraptest "nerae/api/internal/modules/auth/bootstrap/testsupport"
	"nerae/api/internal/modules/auth/challenge"
	"nerae/api/internal/modules/auth/domain"
	"nerae/api/internal/modules/auth/identity"
	"nerae/api/internal/modules/auth/repository"
)

func TestAgeDeclarationVersionJSONSafeBounds(t *testing.T) {
	for _, test := range []struct {
		version uint64
		valid   bool
	}{{0, false}, {1, true}, {maxAgeDeclarationVersion, true}, {maxAgeDeclarationVersion + 1, false}, {^uint64(0), false}} {
		if got := validAgeDeclarationVersion(test.version); got != test.valid {
			t.Fatalf("validAgeDeclarationVersion(%d) = %v, want %v", test.version, got, test.valid)
		}
	}
}

func TestAdultSelfDeclarationTransactions(t *testing.T) {
	db := sessionTestDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	hasher, _ := domain.NewEmailCodeHasher(bytes.Repeat([]byte{1}, 32))
	replay, _ := repository.NewProofReplayStore(bytes.Repeat([]byte{2}, 32))
	protector, _ := domain.NewEmailIdentityProtector(bytes.Repeat([]byte{3}, 32), bytes.Repeat([]byte{4}, 32), "test-key")
	challenges, err := challenge.NewStore(hasher, replay, protector, challenge.Policy{ChallengeTTL: 5 * time.Minute,
		MaxAttempts: 3, DeviceProof: domain.ProofPolicy{MaxLifetime: 5 * time.Minute, ClockSkew: 30 * time.Second}})
	if err != nil {
		t.Fatal("initialize isolated challenges")
	}
	authbootstraptest.InitializeIdentityEpoch(t, db, protector)
	resolver, _ := identity.NewResolver(protector)
	codec, _ := NewTokenCodec(bytes.Repeat([]byte{5}, 32), TokenPolicy{MaxLifetime: time.Hour})
	store, _ := NewStore(resolver, codec)
	login := func(email string) (string, TokenClaims) {
		t.Helper()
		key, public := testDevice(t)
		id, _ := domain.GenerateChallengeID()
		nonce, _ := domain.GenerateChallengeNonce()
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal("begin challenge")
		}
		defer tx.Rollback()
		if err := challenges.Issue(ctx, tx, challenge.IssueInput{Email: email, ChallengeID: id, Nonce: nonce, Code: "012345", PublicKey: public}); err != nil || tx.Commit() != nil {
			t.Fatal("issue synthetic challenge")
		}
		tx, err = db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal("begin session")
		}
		defer tx.Rollback()
		attempt, err := challenges.Attempt(ctx, tx, challenge.AttemptInput{ChallengeID: id, Code: "012345", DeviceProof: testProof(t, key, id, nonce)})
		if err != nil || !attempt.Verified {
			t.Fatal("verify synthetic challenge")
		}
		issued, err := store.IssueEmail(ctx, tx, attempt.Proof)
		if err != nil || tx.Commit() != nil {
			t.Fatal("commit synthetic session")
		}
		token, err := issued.AfterCommit(ctx, db)
		if err != nil {
			t.Fatal("obtain session")
		}
		claims, err := codec.Verify(token)
		if err != nil {
			t.Fatal("verify session")
		}
		return token, claims
	}
	first, firstClaims := login("first@example.test")
	second, secondClaims := login("second@example.test")
	third, thirdClaims := login("third@example.test")
	if firstClaims.SubjectID == secondClaims.SubjectID {
		t.Fatal("fixture subjects collided")
	}
	age := func(subject string) (sql.NullTime, time.Time, sql.NullInt64) {
		t.Helper()
		var declared sql.NullTime
		var updated time.Time
		var birthYear sql.NullInt64
		if err := db.QueryRowContext(ctx, `SELECT age_verified_at, updated_at, birth_year FROM user WHERE id = ?`, subject).Scan(&declared, &updated, &birthYear); err != nil {
			t.Fatal("read isolated declaration")
		}
		return declared, updated, birthYear
	}
	ageVersion := func(subject string) uint64 {
		t.Helper()
		var version uint64
		if err := db.QueryRowContext(ctx, `SELECT age_declaration_version FROM user WHERE id = ?`, subject).Scan(&version); err != nil {
			t.Fatal("read isolated declaration version")
		}
		return version
	}
	firstVersion := authenticatedAgeDeclarationVersion(t, ctx, db, store, first)
	if _, err := store.DeclareAdultAtVersion(ctx, db, first, firstVersion, false); !errors.Is(err, ErrAdultDeclarationRequired) {
		t.Fatal("negative declaration accepted")
	}
	if _, err := store.DeclareAdultAtVersion(ctx, db, first+"tampered", firstVersion, true); !errors.Is(err, ErrInvalidSession) {
		t.Fatal("invalid session declared adult")
	}
	cancelled, stop := context.WithCancel(ctx)
	stop()
	if _, err := store.DeclareAdultAtVersion(cancelled, db, first, firstVersion, true); !errors.Is(err, ErrSessionUnavailable) {
		t.Fatal("cancelled request declared adult")
	}
	if value, _, _ := age(firstClaims.SubjectID); value.Valid {
		t.Fatal("invalid request changed declaration")
	}
	declaredVersion, err := store.DeclareAdultAtVersion(ctx, db, first, firstVersion, true)
	if err != nil || declaredVersion != firstVersion+1 {
		t.Fatal("valid self-declaration failed")
	}
	declared, updated, birthYear := age(firstClaims.SubjectID)
	if !declared.Valid || birthYear.Valid {
		t.Fatal("declaration missing or birth year invented")
	}
	if value, _, _ := age(secondClaims.SubjectID); value.Valid {
		t.Fatal("declaration affected a different subject")
	}
	reaffirmedVersion, err := store.DeclareAdultAtVersion(ctx, db, first, declaredVersion, true)
	if err != nil || reaffirmedVersion != declaredVersion+1 {
		t.Fatal("repeat declaration failed to advance version")
	}
	repeated, repeatedUpdated, _ := age(firstClaims.SubjectID)
	if !repeated.Time.Equal(declared.Time) || repeatedUpdated.Before(updated) {
		t.Fatal("repeat declaration changed the original timestamp or regressed update time")
	}
	if _, err := store.ClearAdultDeclarationAtVersion(ctx, db, first+"tampered", reaffirmedVersion); !errors.Is(err, ErrInvalidSession) {
		t.Fatal("invalid session cleared declaration")
	}
	clearedVersion, err := store.ClearAdultDeclarationAtVersion(ctx, db, first, reaffirmedVersion)
	if err != nil || clearedVersion != reaffirmedVersion+1 {
		t.Fatal("explicit declaration withdrawal failed")
	}
	cleared, clearedUpdated, _ := age(firstClaims.SubjectID)
	if cleared.Valid {
		t.Fatal("withdrawn declaration remained active")
	}
	emptyVersion, err := store.ClearAdultDeclarationAtVersion(ctx, db, first, clearedVersion)
	if err != nil || emptyVersion != clearedVersion+1 {
		t.Fatal("explicit repeated withdrawal did not advance version")
	}
	_, repeatedClearUpdated, _ := age(firstClaims.SubjectID)
	if repeatedClearUpdated.Before(clearedUpdated) {
		t.Fatal("repeated withdrawal regressed update time")
	}
	checkTx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal("begin withdrawn session check")
	}
	checkPrincipal, err := store.Authenticate(ctx, checkTx, first)
	if err != nil {
		_ = checkTx.Rollback()
		t.Fatal("authenticate withdrawn session")
	}
	checkDetails, err := checkPrincipal.ForTransaction(ctx, checkTx)
	if err != nil || checkDetails.AgeVerified {
		_ = checkTx.Rollback()
		t.Fatal("withdrawn declaration still passes age gate")
	}
	_ = checkTx.Rollback()
	redeclaredVersion, err := store.DeclareAdultAtVersion(ctx, db, first, emptyVersion, true)
	if err != nil || redeclaredVersion != emptyVersion+1 {
		t.Fatal("explicit re-declaration failed")
	}
	withdrawTx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal("begin withdrawal rollback")
	}
	withdrawPrincipal, err := store.Authenticate(ctx, withdrawTx, first)
	if err != nil {
		_ = withdrawTx.Rollback()
		t.Fatal("authenticate withdrawal rollback")
	}
	if _, err := store.clearAdultDeclarationAtVersionInTransaction(ctx, withdrawTx, withdrawPrincipal, redeclaredVersion); err != nil {
		_ = withdrawTx.Rollback()
		t.Fatal("stage versioned withdrawal")
	}
	if _, err := withdrawTx.ExecContext(ctx, `UPDATE auth_session SET state = 'revoked', revoked_at = UTC_TIMESTAMP(6) WHERE id = ?`, []byte(firstClaims.SessionID)); err != nil {
		_ = withdrawTx.Rollback()
		t.Fatal("stage withdrawal revocation")
	}
	if _, err := withdrawPrincipal.ForTransaction(ctx, withdrawTx); !errors.Is(err, ErrSessionUnavailable) {
		_ = withdrawTx.Rollback()
		t.Fatal("withdrawal retained revoked session")
	}
	if err := withdrawTx.Rollback(); err != nil {
		t.Fatal("roll back withdrawal")
	}
	if value, _, _ := age(firstClaims.SubjectID); !value.Valid {
		t.Fatal("rolled-back withdrawal persisted")
	}

	// Versioned actions invalidate stale retries, including repeated withdrawal
	// of an already empty state. Every expected version comes from an
	// authenticated current-state read or the preceding versioned result.
	secondVersion := authenticatedAgeDeclarationVersion(t, ctx, db, store, second)
	if secondVersion != 1 {
		t.Fatalf("unexpected initial declaration version: %d", secondVersion)
	}
	declaredVersion, err = store.DeclareAdultAtVersion(ctx, db, second, secondVersion, true)
	if err != nil || declaredVersion != secondVersion+1 {
		t.Fatal("versioned declaration failed")
	}
	secondDeclaredAfter, _, _ := age(secondClaims.SubjectID)
	if !secondDeclaredAfter.Valid {
		t.Fatal("versioned declaration did not set its timestamp")
	}
	if _, err := store.DeclareAdultAtVersion(ctx, db, second, secondVersion, true); !errors.Is(err, ErrAgeDeclarationConflict) {
		t.Fatal("stale declaration retry was accepted")
	}
	if ageVersion(secondClaims.SubjectID) != declaredVersion {
		t.Fatal("stale declaration retry changed version")
	}
	withdrawnVersion, err := store.ClearAdultDeclarationAtVersion(ctx, db, second, declaredVersion)
	if err != nil || withdrawnVersion != declaredVersion+1 {
		t.Fatal("versioned withdrawal failed")
	}
	if value, _, _ := age(secondClaims.SubjectID); value.Valid {
		t.Fatal("versioned withdrawal left declaration active")
	}
	if _, err := store.DeclareAdultAtVersion(ctx, db, second, declaredVersion, true); !errors.Is(err, ErrAgeDeclarationConflict) {
		t.Fatal("late old declaration reactivated withdrawn state")
	}
	if _, err := store.ClearAdultDeclarationAtVersion(ctx, db, second, declaredVersion); !errors.Is(err, ErrAgeDeclarationConflict) {
		t.Fatal("old withdrawal replay was accepted")
	}
	if ageVersion(secondClaims.SubjectID) != withdrawnVersion {
		t.Fatal("stale requests changed withdrawn version")
	}
	clearedAgainVersion, err := store.ClearAdultDeclarationAtVersion(ctx, db, second, withdrawnVersion)
	if err != nil || clearedAgainVersion != withdrawnVersion+1 {
		t.Fatal("explicit withdrawal of empty state did not advance version")
	}
	if ageVersion(secondClaims.SubjectID) != clearedAgainVersion {
		t.Fatal("empty withdrawal did not persist version")
	}
	if ageVersion(firstClaims.SubjectID) != redeclaredVersion {
		t.Fatal("versioned action changed a different subject")
	}
	if _, err := store.DeclareAdultAtVersion(ctx, db, second+"tampered", clearedAgainVersion, true); !errors.Is(err, ErrInvalidSession) {
		t.Fatal("invalid bearer was not classified as invalid session")
	}
	cancelledVersionContext, cancelVersionContext := context.WithCancel(ctx)
	cancelVersionContext()
	if _, err := store.DeclareAdultAtVersion(cancelledVersionContext, db, second, clearedAgainVersion, true); !errors.Is(err, ErrSessionUnavailable) {
		t.Fatal("cancelled request was not classified as unavailable")
	}
	if ageVersion(secondClaims.SubjectID) != clearedAgainVersion {
		t.Fatal("invalid bearer changed declaration version")
	}
	if _, err := store.DeclareAdultAtVersion(ctx, db, second, maxAgeDeclarationVersion+1, true); !errors.Is(err, ErrAgeDeclarationVersionInvalid) {
		t.Fatal("unsafe JSON integer version was accepted")
	}
	if _, err := store.DeclareAdultAtVersion(ctx, db, second, 0, true); !errors.Is(err, ErrAgeDeclarationVersionInvalid) {
		t.Fatal("zero declaration version was accepted")
	}
	if _, err := db.ExecContext(ctx, `UPDATE user SET age_declaration_version = ? WHERE id = ?`, maxAgeDeclarationVersion, secondClaims.SubjectID); err != nil {
		t.Fatal("set maximum safe version fixture")
	}
	maxVersion := authenticatedAgeDeclarationVersion(t, ctx, db, store, second)
	if maxVersion != maxAgeDeclarationVersion {
		t.Fatal("authenticated current version did not observe the maximum fixture")
	}
	if _, err := store.ClearAdultDeclarationAtVersion(ctx, db, second, maxVersion); !errors.Is(err, ErrAgeDeclarationVersionExhausted) {
		t.Fatal("maximum version overflow was not rejected")
	}
	if got := ageVersion(secondClaims.SubjectID); got != maxAgeDeclarationVersion {
		t.Fatal("version exhaustion changed state")
	}
	if _, err := db.ExecContext(ctx, `UPDATE user SET age_declaration_version = ? WHERE id = ?`, maxAgeDeclarationVersion+1, secondClaims.SubjectID); err == nil {
		t.Fatal("database accepted a version beyond JSON safe integer")
	}
	if _, err := db.ExecContext(ctx, `UPDATE user SET age_declaration_version = ? WHERE id = ?`, clearedAgainVersion, secondClaims.SubjectID); err != nil {
		t.Fatal("restore declaration version fixture")
	}

	// Exercise both lock orders with real competing transactions. The first
	// transaction stages one action while the opposite stale action waits; after
	// commit, the delayed request must conflict without changing the winner.
	thirdInitial := authenticatedAgeDeclarationVersion(t, ctx, db, store, third)
	thirdDeclared, err := store.DeclareAdultAtVersion(ctx, db, third, thirdInitial, true)
	if err != nil {
		t.Fatal("prepare declaration race")
	}
	stageAndRace := func(token string, expected uint64, stageWithdrawal bool) (uint64, error) {
		t.Helper()
		gate, err := db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal("begin versioned race gate")
		}
		principal, err := store.Authenticate(ctx, gate, token)
		if err != nil {
			_ = gate.Rollback()
			t.Fatal("authenticate versioned race gate")
		}
		var staged uint64
		if stageWithdrawal {
			staged, err = store.clearAdultDeclarationAtVersionInTransaction(ctx, gate, principal, expected)
		} else {
			staged, err = store.declareAdultAtVersionInTransaction(ctx, gate, principal, expected, true)
		}
		if err != nil {
			_ = gate.Rollback()
			t.Fatal("stage versioned race action")
		}
		result := make(chan error, 1)
		go func() {
			if stageWithdrawal {
				_, err := store.DeclareAdultAtVersion(ctx, db, token, expected, true)
				result <- err
				return
			}
			_, err := store.ClearAdultDeclarationAtVersion(ctx, db, token, expected)
			result <- err
		}()
		deadline := time.Now().Add(5 * time.Second)
		blocked := 0
		for time.Now().Before(deadline) {
			if err := db.QueryRowContext(ctx, `SELECT COUNT(DISTINCT w.REQUESTING_ENGINE_TRANSACTION_ID)
				FROM performance_schema.data_lock_waits w JOIN performance_schema.data_locks l ON l.ENGINE_LOCK_ID = w.REQUESTING_ENGINE_LOCK_ID
				WHERE l.OBJECT_SCHEMA = DATABASE() AND l.OBJECT_NAME IN ('user', 'login_identity')`).Scan(&blocked); err != nil {
				_ = gate.Rollback()
				t.Fatal("observe versioned lock wait")
			}
			if blocked > 0 {
				break
			}
			time.Sleep(20 * time.Millisecond)
		}
		if blocked == 0 {
			_ = gate.Rollback()
			t.Fatal("competing versioned request did not wait")
		}
		if err := gate.Commit(); err != nil {
			t.Fatal("commit versioned race gate")
		}
		return staged, <-result
	}
	withdrawnRaceVersion, delayedDeclareErr := stageAndRace(third, thirdDeclared, true)
	if withdrawnRaceVersion != thirdDeclared+1 || !errors.Is(delayedDeclareErr, ErrAgeDeclarationConflict) {
		t.Fatal("withdrawal-first race did not reject late declaration")
	}
	if value, _, _ := age(thirdClaims.SubjectID); value.Valid || ageVersion(thirdClaims.SubjectID) != withdrawnRaceVersion {
		t.Fatal("late declaration changed withdrawal-first result")
	}
	declaredRaceVersion, delayedWithdrawErr := stageAndRace(third, withdrawnRaceVersion, false)
	if declaredRaceVersion != withdrawnRaceVersion+1 || !errors.Is(delayedWithdrawErr, ErrAgeDeclarationConflict) {
		t.Fatal("declaration-first race did not reject stale withdrawal")
	}
	if value, _, _ := age(thirdClaims.SubjectID); !value.Valid || ageVersion(thirdClaims.SubjectID) != declaredRaceVersion {
		t.Fatal("stale withdrawal changed declaration-first result")
	}
	thirdEmptyVersion, err := store.ClearAdultDeclarationAtVersion(ctx, db, third, declaredRaceVersion)
	if err != nil || thirdEmptyVersion != declaredRaceVersion+1 {
		t.Fatal("restore empty state after race checks")
	}

	// A caller transaction can roll back a staged declaration and its version
	// together; neither state nor version may leak out.
	rollbackVersion := authenticatedAgeDeclarationVersion(t, ctx, db, store, second)
	rollbackTx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal("begin version rollback fixture")
	}
	rollbackPrincipal, err := store.Authenticate(ctx, rollbackTx, second)
	if err != nil {
		_ = rollbackTx.Rollback()
		t.Fatal("authenticate version rollback fixture")
	}
	if _, err := store.declareAdultAtVersionInTransaction(ctx, rollbackTx, rollbackPrincipal, rollbackVersion, true); err != nil {
		_ = rollbackTx.Rollback()
		t.Fatal("stage version rollback declaration")
	}
	if err := rollbackTx.Rollback(); err != nil {
		t.Fatal("rollback versioned declaration")
	}
	if ageVersion(secondClaims.SubjectID) != rollbackVersion {
		t.Fatal("rolled-back declaration version persisted")
	}

	// A session revoked after the write but within the transaction must prevent
	// the caller's final principal check and roll back the declaration.
	thirdVersion := authenticatedAgeDeclarationVersion(t, ctx, db, store, third)
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal("begin rollback fixture")
	}
	principal, err := store.Authenticate(ctx, tx, third)
	if err != nil {
		_ = tx.Rollback()
		t.Fatal("authenticate rollback fixture")
	}
	if _, err := store.declareAdultAtVersionInTransaction(ctx, tx, principal, thirdVersion, true); err != nil {
		_ = tx.Rollback()
		t.Fatal("stage versioned declaration")
	}
	if _, err := tx.ExecContext(ctx, `UPDATE auth_session SET state = 'revoked', revoked_at = UTC_TIMESTAMP(6) WHERE id = ?`, []byte(thirdClaims.SessionID)); err != nil {
		_ = tx.Rollback()
		t.Fatal("stage revocation")
	}
	if _, err := principal.ForTransaction(ctx, tx); !errors.Is(err, ErrSessionUnavailable) {
		_ = tx.Rollback()
		t.Fatal("revoked session remained valid")
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal("roll back declaration")
	}
	if value, _, _ := age(thirdClaims.SubjectID); value.Valid {
		t.Fatal("rolled-back declaration persisted")
	}
	if _, err := db.ExecContext(ctx, `UPDATE auth_session SET state = 'revoked', revoked_at = UTC_TIMESTAMP(6) WHERE id = ?`, []byte(thirdClaims.SessionID)); err != nil {
		t.Fatal("revoke fixture session")
	}
	if _, err := store.DeclareAdultAtVersion(ctx, db, third, thirdVersion, true); !errors.Is(err, ErrInvalidSession) {
		t.Fatal("revoked session declared adult")
	}
	if value, _, _ := age(thirdClaims.SubjectID); value.Valid {
		t.Fatal("revoked session changed declaration")
	}
	if _, err := db.ExecContext(ctx, `UPDATE auth_session SET state = 'active', revoked_at = NULL WHERE id = ?`, []byte(thirdClaims.SessionID)); err != nil {
		t.Fatal("restore isolated session")
	}
	if _, err := db.ExecContext(ctx, `UPDATE user SET status = 'disabled' WHERE id = ?`, thirdClaims.SubjectID); err != nil {
		t.Fatal("disable isolated subject")
	}
	if _, err := store.DeclareAdultAtVersion(ctx, db, third, thirdVersion, true); !errors.Is(err, ErrInvalidSession) {
		t.Fatal("disabled subject declared adult")
	}
	if _, err := db.ExecContext(ctx, `UPDATE user SET status = 'active' WHERE id = ?`, thirdClaims.SubjectID); err != nil {
		t.Fatal("restore isolated subject")
	}
	if _, err := db.ExecContext(ctx, `UPDATE trusted_device SET state = 'revoked' WHERE user_id = ?`, thirdClaims.SubjectID); err != nil {
		t.Fatal("revoke isolated device")
	}
	if _, err := store.DeclareAdultAtVersion(ctx, db, third, thirdVersion, true); !errors.Is(err, ErrInvalidSession) {
		t.Fatal("revoked device declared adult")
	}
	if _, err := db.ExecContext(ctx, `UPDATE trusted_device SET state = 'active' WHERE user_id = ?`, thirdClaims.SubjectID); err != nil {
		t.Fatal("restore isolated device")
	}
	if _, err := db.ExecContext(ctx, `UPDATE login_identity SET state = 'revoked' WHERE user_id = ?`, thirdClaims.SubjectID); err != nil {
		t.Fatal("revoke isolated identity")
	}
	if _, err := store.DeclareAdultAtVersion(ctx, db, third, thirdVersion, true); !errors.Is(err, ErrInvalidSession) {
		t.Fatal("revoked identity declared adult")
	}
	if value, _, _ := age(thirdClaims.SubjectID); value.Valid {
		t.Fatal("invalid subject state changed declaration")
	}
}

func authenticatedAgeDeclarationVersion(t *testing.T, ctx context.Context, db *sql.DB, store *Store, token string) uint64 {
	t.Helper()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal("begin authenticated age-version read")
	}
	defer tx.Rollback()
	principal, err := store.Authenticate(ctx, tx, token)
	if err != nil {
		t.Fatal("authenticate age-version read")
	}
	details, err := principal.ForTransaction(ctx, tx)
	if err != nil || details.SubjectID == 0 {
		t.Fatal("read authenticated principal for age-version read")
	}
	var version uint64
	if err := tx.QueryRowContext(ctx, `SELECT age_declaration_version FROM user WHERE id = ?`, details.SubjectID).Scan(&version); err != nil {
		t.Fatal("read authenticated current age declaration version")
	}
	if err := tx.Commit(); err != nil {
		t.Fatal("commit authenticated age-version read")
	}
	return version
}
