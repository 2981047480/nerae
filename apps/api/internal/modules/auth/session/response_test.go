package session

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	authbootstraptest "nerae/api/internal/modules/auth/bootstrap/testsupport"
	"nerae/api/internal/modules/auth/challenge"
	"nerae/api/internal/modules/auth/domain"
	"nerae/api/internal/modules/auth/identity"
	"nerae/api/internal/modules/auth/repository"
)

func TestResponseProfileRemainingSeconds(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	profile := ResponseProfile{ExpiresAt: now.Add(61*time.Second + 900*time.Millisecond)}
	if remaining, err := profile.RemainingSeconds(now); err != nil || remaining != 61 {
		t.Fatal("remaining lifetime was not rounded down")
	}
	for _, at := range []time.Time{{}, profile.ExpiresAt, profile.ExpiresAt.Add(-999 * time.Millisecond)} {
		if remaining, err := profile.RemainingSeconds(at); !errors.Is(err, ErrInvalidSession) || remaining != 0 {
			t.Fatal("invalid or almost-expired lifetime was accepted")
		}
	}
	if _, err := (ResponseProfile{ExpiresAt: now.Add(31 * 24 * time.Hour)}).RemainingSeconds(now); !errors.Is(err, ErrInvalidSession) {
		t.Fatal("lifetime beyond session policy was accepted")
	}
}

func TestAuthenticatedResponseProfile(t *testing.T) {
	db := sessionTestDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	hasher, _ := domain.NewEmailCodeHasher(bytes.Repeat([]byte{1}, 32))
	replay, _ := repository.NewProofReplayStore(bytes.Repeat([]byte{2}, 32))
	protector, _ := domain.NewEmailIdentityProtector(bytes.Repeat([]byte{3}, 32), bytes.Repeat([]byte{4}, 32), "test-key")
	challenges, err := challenge.NewStore(hasher, replay, protector, challenge.Policy{ChallengeTTL: 5 * time.Minute,
		MaxAttempts: 3, DeviceProof: domain.ProofPolicy{MaxLifetime: 5 * time.Minute, ClockSkew: 30 * time.Second}})
	if err != nil {
		t.Fatal("could not initialize challenge store")
	}
	authbootstraptest.InitializeIdentityEpoch(t, db, protector)
	resolver, _ := identity.NewResolver(protector)
	codec, _ := NewTokenCodec(bytes.Repeat([]byte{5}, 32), TokenPolicy{MaxLifetime: time.Hour})
	store, _ := NewStore(resolver, codec)
	key, public := testDevice(t)
	id, _ := domain.GenerateChallengeID()
	nonce, _ := domain.GenerateChallengeNonce()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := challenges.Issue(ctx, tx, challenge.IssueInput{Email: "profile@example.test", ChallengeID: id, Nonce: nonce, Code: "012345", PublicKey: public}); err != nil || tx.Commit() != nil {
		t.Fatal("could not issue challenge")
	}
	tx, err = db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	result, err := challenges.Attempt(ctx, tx, challenge.AttemptInput{ChallengeID: id, Code: "012345", DeviceProof: testProof(t, key, id, nonce)})
	if err != nil || !result.Verified || result.Proof == nil {
		_ = tx.Rollback()
		t.Fatal("could not verify challenge")
	}
	issued, err := store.IssueEmail(ctx, tx, result.Proof)
	if err != nil || tx.Commit() != nil {
		t.Fatal("could not issue session")
	}
	token, err := issued.AfterCommit(ctx, db)
	if err != nil {
		t.Fatal("could not obtain committed session")
	}
	claims, err := codec.Verify(token)
	if err != nil {
		t.Fatal(err)
	}
	view, err := store.ProfileForResponse(ctx, db, token)
	if err != nil || view.SubjectID != claims.SubjectID || view.Nickname != "" || view.Roles == nil || len(view.Roles) != 0 || view.AgeVerified || view.AgeDeclarationVersion != 1 || !view.ExpiresAt.Equal(claims.ExpiresAt) {
		t.Fatal("new account projection did not reflect stored values")
	}
	if _, err := json.Marshal(view); err == nil || strings.Contains(fmt.Sprintf("%+v", view), claims.SubjectID) {
		t.Fatal("profile allowed accidental serialization or logging")
	}
	if _, err := db.ExecContext(ctx, `UPDATE user SET nickname = ? WHERE id = ?`, "测试昵称", claims.SubjectID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO user_role (user_id, role, status, created_at, updated_at) VALUES
        (?, 'student', 'active', UTC_TIMESTAMP(6), UTC_TIMESTAMP(6)), (?, 'coach', 'disabled', UTC_TIMESTAMP(6), UTC_TIMESTAMP(6))`, claims.SubjectID, claims.SubjectID); err != nil {
		t.Fatal(err)
	}
	declaredVersion, err := store.DeclareAdultAtVersion(ctx, db, token, view.AgeDeclarationVersion, true)
	if err != nil || declaredVersion != 2 {
		t.Fatal("versioned declaration did not advance from the projected state")
	}
	fixedNow := claims.IssuedAt.Add(10 * time.Second)
	store.now = func() time.Time { return fixedNow }
	codec.now = func() time.Time { return fixedNow }
	view, err = store.ProfileForResponse(ctx, db, token)
	if err != nil || view.Nickname != "测试昵称" || !view.AgeVerified || view.AgeDeclarationVersion != declaredVersion || len(view.Roles) != 1 || view.Roles[0] != "student" {
		t.Fatal("profile did not reflect current nickname, declaration and active roles")
	}
	if remaining, err := view.RemainingSeconds(fixedNow); err != nil || remaining != 3590 {
		t.Fatal("response lifetime did not reflect the original token expiry")
	}
	fixedNow = fixedNow.Add(20 * time.Second)
	view, err = store.ProfileForResponse(ctx, db, token)
	if remaining, ttlErr := view.RemainingSeconds(fixedNow); err != nil || ttlErr != nil || remaining != 3570 {
		t.Fatal("replayed token incorrectly received a fresh lifetime")
	}
	locked, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer locked.Rollback()
	lockedVersion := declaredVersion + 1
	userUpdate, err := locked.ExecContext(ctx, `UPDATE user SET nickname = ?, age_verified_at = COALESCE(age_verified_at, UTC_TIMESTAMP(6)),
        age_declaration_version = ? WHERE id = ? AND age_declaration_version = ?`, "更新昵称", lockedVersion, claims.SubjectID, declaredVersion)
	if err != nil {
		t.Fatal(err)
	}
	if affected, err := userUpdate.RowsAffected(); err != nil || affected != 1 {
		t.Fatal("could not stage a valid newer age state under the user row lock")
	}
	if _, err := locked.ExecContext(ctx, `UPDATE user_role SET status = CASE role WHEN 'coach' THEN 'active' ELSE 'disabled' END WHERE user_id = ?`, claims.SubjectID); err != nil {
		t.Fatal(err)
	}
	type profileResult struct {
		profile ResponseProfile
		err     error
	}
	finished := make(chan profileResult, 1)
	go func() {
		profile, err := store.ProfileForResponse(ctx, db, token)
		finished <- profileResult{profile: profile, err: err}
	}()
	blocked := 0
	for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); {
		if err := db.QueryRowContext(ctx, `SELECT COUNT(DISTINCT w.REQUESTING_ENGINE_TRANSACTION_ID) FROM performance_schema.data_lock_waits w
            JOIN performance_schema.data_locks l ON l.ENGINE_LOCK_ID = w.REQUESTING_ENGINE_LOCK_ID
            WHERE l.OBJECT_SCHEMA = DATABASE() AND l.OBJECT_NAME = 'user'`).Scan(&blocked); err != nil {
			t.Fatal(err)
		}
		if blocked > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if blocked == 0 {
		select {
		case completed := <-finished:
			t.Fatalf("profile lookup completed before account lock was observed (error=%v, version=%d, age_verified=%t)", completed.err, completed.profile.AgeDeclarationVersion, completed.profile.AgeVerified)
		default:
			t.Fatal("profile lookup did not wait for the account lock")
		}
	}
	if err := locked.Commit(); err != nil {
		t.Fatal(err)
	}
	waited := <-finished
	if waited.err != nil || waited.profile.Nickname != "更新昵称" || len(waited.profile.Roles) != 1 || waited.profile.Roles[0] != "coach" || !waited.profile.AgeVerified || waited.profile.AgeDeclarationVersion != lockedVersion {
		t.Fatal("profile lookup returned a stale snapshot after waiting")
	}
	if _, err := db.ExecContext(ctx, `UPDATE user SET status = 'disabled' WHERE id = ?`, claims.SubjectID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ProfileForResponse(ctx, db, token); !errors.Is(err, ErrInvalidSession) {
		t.Fatal("disabled account produced an authenticated profile")
	}
	if _, err := db.ExecContext(ctx, `UPDATE user SET status = 'active' WHERE id = ?`, claims.SubjectID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE auth_session SET state = 'revoked', revoked_at = UTC_TIMESTAMP(6) WHERE id = ?`, []byte(claims.SessionID)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ProfileForResponse(ctx, db, token); !errors.Is(err, ErrInvalidSession) {
		t.Fatal("revoked session produced an authenticated profile")
	}
	if _, err := db.ExecContext(ctx, `UPDATE auth_session SET state = 'active', revoked_at = NULL WHERE id = ?`, []byte(claims.SessionID)); err != nil {
		t.Fatal(err)
	}
	clockCalls := 0
	store.now = func() time.Time {
		clockCalls++
		if clockCalls >= 2 {
			return claims.ExpiresAt
		}
		return fixedNow
	}
	if _, err := store.ProfileForResponse(ctx, db, token); !errors.Is(err, ErrInvalidSession) || clockCalls < 2 {
		t.Fatal("expiry during profile revalidation was not classified as an invalid session")
	}
	store.now = func() time.Time { return fixedNow }
	fixedNow = claims.ExpiresAt
	if _, err := store.ProfileForResponse(ctx, db, token); !errors.Is(err, ErrInvalidSession) {
		t.Fatal("expired token produced an authenticated profile")
	}
	if _, err := (*Store)(nil).ProfileForResponse(ctx, db, token); !errors.Is(err, ErrSessionUnavailable) {
		t.Fatal("missing store was not unavailable")
	}
}
