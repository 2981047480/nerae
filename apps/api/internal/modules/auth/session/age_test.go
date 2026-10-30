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
	if err := store.DeclareAdult(ctx, db, first, false); !errors.Is(err, ErrAdultDeclarationRequired) {
		t.Fatal("negative declaration accepted")
	}
	if err := store.DeclareAdult(ctx, db, first+"tampered", true); !errors.Is(err, ErrSessionUnavailable) {
		t.Fatal("invalid session declared adult")
	}
	cancelled, stop := context.WithCancel(ctx)
	stop()
	if err := store.DeclareAdult(cancelled, db, first, true); !errors.Is(err, ErrSessionUnavailable) {
		t.Fatal("cancelled request declared adult")
	}
	if value, _, _ := age(firstClaims.SubjectID); value.Valid {
		t.Fatal("invalid request changed declaration")
	}
	if err := store.DeclareAdult(ctx, db, first, true); err != nil {
		t.Fatal("valid self-declaration failed")
	}
	declared, updated, birthYear := age(firstClaims.SubjectID)
	if !declared.Valid || birthYear.Valid {
		t.Fatal("declaration missing or birth year invented")
	}
	if value, _, _ := age(secondClaims.SubjectID); value.Valid {
		t.Fatal("declaration affected a different subject")
	}
	if err := store.DeclareAdult(ctx, db, first, true); err != nil {
		t.Fatal("repeat declaration failed")
	}
	repeated, repeatedUpdated, _ := age(firstClaims.SubjectID)
	if !repeated.Time.Equal(declared.Time) || !repeatedUpdated.Equal(updated) {
		t.Fatal("repeat changed declaration timestamp")
	}
	if err := store.ClearAdultDeclaration(ctx, db, first+"tampered"); !errors.Is(err, ErrSessionUnavailable) {
		t.Fatal("invalid session cleared declaration")
	}
	if err := store.ClearAdultDeclaration(ctx, db, first); err != nil {
		t.Fatal("explicit declaration withdrawal failed")
	}
	cleared, clearedUpdated, _ := age(firstClaims.SubjectID)
	if cleared.Valid {
		t.Fatal("withdrawn declaration remained active")
	}
	if err := store.ClearAdultDeclaration(ctx, db, first); err != nil {
		t.Fatal("repeat withdrawal failed")
	}
	_, repeatedClearUpdated, _ := age(firstClaims.SubjectID)
	if !repeatedClearUpdated.Equal(clearedUpdated) {
		t.Fatal("repeat withdrawal changed update time")
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
	if err := store.DeclareAdult(ctx, db, first, true); err != nil {
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
	if err := store.clearAdultInTransaction(ctx, withdrawTx, withdrawPrincipal); err != nil {
		_ = withdrawTx.Rollback()
		t.Fatal("stage withdrawal")
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
	gate, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal("begin declaration concurrency gate")
	}
	var lockedID uint64
	if err := gate.QueryRowContext(ctx, `SELECT id FROM user WHERE id = ? FOR UPDATE`, secondClaims.SubjectID).Scan(&lockedID); err != nil {
		_ = gate.Rollback()
		t.Fatal("lock declaration subject")
	}
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() { results <- store.DeclareAdult(ctx, db, second, true) }()
	}
	deadline, blocked := time.Now().Add(5*time.Second), 0
	for time.Now().Before(deadline) {
		if err := db.QueryRowContext(ctx, `SELECT COUNT(DISTINCT w.REQUESTING_ENGINE_TRANSACTION_ID)
			FROM performance_schema.data_lock_waits w JOIN performance_schema.data_locks l ON l.ENGINE_LOCK_ID = w.REQUESTING_ENGINE_LOCK_ID
			WHERE l.OBJECT_SCHEMA = DATABASE() AND l.OBJECT_NAME IN ('user', 'login_identity')`).Scan(&blocked); err != nil {
			_ = gate.Rollback()
			t.Fatal("observe declaration lock waits")
		}
		if blocked >= 2 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if blocked < 2 {
		_ = gate.Rollback()
		t.Fatal("declaration requests did not both reach a lock")
	}
	if err := gate.Rollback(); err != nil {
		t.Fatal("release declaration gate")
	}
	if err := <-results; err != nil {
		t.Fatal("first concurrent declaration failed")
	}
	if err := <-results; err != nil {
		t.Fatal("second concurrent declaration failed")
	}
	if value, _, _ := age(secondClaims.SubjectID); !value.Valid {
		t.Fatal("concurrent declaration was not committed")
	}

	// A session revoked after the write but within the transaction must prevent
	// the caller's final principal check and roll back the declaration.
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal("begin rollback fixture")
	}
	principal, err := store.Authenticate(ctx, tx, third)
	if err != nil {
		_ = tx.Rollback()
		t.Fatal("authenticate rollback fixture")
	}
	if err := store.declareAdultInTransaction(ctx, tx, principal, true); err != nil {
		_ = tx.Rollback()
		t.Fatal("stage declaration")
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
	if err := store.DeclareAdult(ctx, db, third, true); !errors.Is(err, ErrSessionUnavailable) {
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
	if err := store.DeclareAdult(ctx, db, third, true); !errors.Is(err, ErrSessionUnavailable) {
		t.Fatal("disabled subject declared adult")
	}
	if _, err := db.ExecContext(ctx, `UPDATE user SET status = 'active' WHERE id = ?`, thirdClaims.SubjectID); err != nil {
		t.Fatal("restore isolated subject")
	}
	if _, err := db.ExecContext(ctx, `UPDATE trusted_device SET state = 'revoked' WHERE user_id = ?`, thirdClaims.SubjectID); err != nil {
		t.Fatal("revoke isolated device")
	}
	if err := store.DeclareAdult(ctx, db, third, true); !errors.Is(err, ErrSessionUnavailable) {
		t.Fatal("revoked device declared adult")
	}
	if _, err := db.ExecContext(ctx, `UPDATE trusted_device SET state = 'active' WHERE user_id = ?`, thirdClaims.SubjectID); err != nil {
		t.Fatal("restore isolated device")
	}
	if _, err := db.ExecContext(ctx, `UPDATE login_identity SET state = 'revoked' WHERE user_id = ?`, thirdClaims.SubjectID); err != nil {
		t.Fatal("revoke isolated identity")
	}
	if err := store.DeclareAdult(ctx, db, third, true); !errors.Is(err, ErrSessionUnavailable) {
		t.Fatal("revoked identity declared adult")
	}
	if value, _, _ := age(thirdClaims.SubjectID); value.Valid {
		t.Fatal("invalid subject state changed declaration")
	}
}
