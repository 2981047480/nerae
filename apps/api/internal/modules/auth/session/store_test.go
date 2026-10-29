package session

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"

	"nerae/api/internal/modules/auth/challenge"
	"nerae/api/internal/modules/auth/domain"
	"nerae/api/internal/modules/auth/identity"
	"nerae/api/internal/modules/auth/repository"
)

func sessionTestDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("NERAE_SESSION_TEST_DSN")
	if dsn == "" {
		t.Skip("set NERAE_SESSION_TEST_DSN using the isolated MySQL test harness")
	}
	cfg, err := mysql.ParseDSN(dsn)
	if err != nil {
		t.Fatal("invalid isolated session DSN")
	}
	socket := filepath.Clean(cfg.Addr)
	parent := filepath.Dir(socket)
	resolved, err := filepath.EvalSymlinks(parent)
	info, statErr := os.Lstat(parent)
	socketInfo, socketErr := os.Lstat(socket)
	if err != nil || statErr != nil || socketErr != nil || cfg.Net != "unix" || cfg.DBName != "nerae_auth_session_test" ||
		cfg.User != "root" || cfg.Passwd != "" || !cfg.ParseTime || cfg.Loc != time.UTC ||
		filepath.Base(socket) != "mysql.sock" || filepath.Dir(resolved) != "/private/tmp" ||
		!strings.HasPrefix(filepath.Base(resolved), "nerae-auth-session-mysql-") ||
		!info.IsDir() || info.Mode().Perm()&0077 != 0 || info.Mode()&os.ModeSymlink != 0 || socketInfo.Mode()&os.ModeSocket == 0 {
		t.Fatal("refusing non-isolated MySQL test target")
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatal("cannot open isolated session database")
	}
	db.SetMaxOpenConns(4)
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Ping(); err != nil {
		t.Fatal("cannot connect to isolated session database")
	}
	return db
}

func testDevice(t *testing.T) (*ecdsa.PrivateKey, domain.DevicePublicJWK) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal("could not create synthetic device")
	}
	encode := base64.RawURLEncoding.EncodeToString
	return key, domain.DevicePublicJWK{Kty: "EC", Crv: "P-256", X: encode(key.X.FillBytes(make([]byte, 32))), Y: encode(key.Y.FillBytes(make([]byte, 32)))}
}

func testProof(t *testing.T, key *ecdsa.PrivateKey, challengeID, nonce string) string {
	t.Helper()
	jti, err := domain.GenerateChallengeID()
	if err != nil {
		t.Fatal("could not generate test proof ID")
	}
	now := time.Now().UTC()
	payload, err := json.Marshal(map[string]any{"aud": "nerae-auth-device", "purpose": "email_login", "resourceId": challengeID,
		"nonce": nonce, "iat": now.Unix(), "exp": now.Add(time.Minute).Unix(), "jti": jti})
	if err != nil {
		t.Fatal("could not encode test proof")
	}
	encode := base64.RawURLEncoding.EncodeToString
	unsigned := encode([]byte(`{"alg":"ES256","typ":"JWT"}`)) + "." + encode(payload)
	digest := sha256.Sum256([]byte(unsigned))
	r, signatureS, err := ecdsa.Sign(rand.Reader, key, digest[:])
	if err != nil {
		t.Fatal("could not sign test proof")
	}
	return unsigned + "." + encode(append(r.FillBytes(make([]byte, 32)), signatureS.FillBytes(make([]byte, 32))...))
}

func TestEmailSessionLifecycle(t *testing.T) {
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
	resolver, _ := identity.NewResolver(protector)
	codec, _ := NewTokenCodec(bytes.Repeat([]byte{5}, 32), TokenPolicy{MaxLifetime: time.Hour})
	store, _ := NewStore(resolver, codec)
	firstKey, firstPublic := testDevice(t)
	otherKey, otherPublic := testDevice(t)
	issueChallenge := func(key *ecdsa.PrivateKey, public domain.DevicePublicJWK) (string, string) {
		t.Helper()
		id, _ := domain.GenerateChallengeID()
		nonce, _ := domain.GenerateChallengeNonce()
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal("could not begin challenge")
		}
		defer tx.Rollback()
		if err := challenges.Issue(ctx, tx, challenge.IssueInput{Email: "person@example.test", ChallengeID: id, Nonce: nonce, Code: "012345", PublicKey: public}); err != nil || tx.Commit() != nil {
			t.Fatal("could not create isolated challenge")
		}
		return id, testProof(t, key, id, nonce)
	}
	beginVerified := func(id, signed string) (*sql.Tx, *challenge.VerifiedEmail) {
		t.Helper()
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal("could not begin authentication")
		}
		result, err := challenges.Attempt(ctx, tx, challenge.AttemptInput{ChallengeID: id, Code: "012345", DeviceProof: signed})
		if err != nil || !result.Verified || result.Proof == nil {
			_ = tx.Rollback()
			t.Fatal("could not verify isolated challenge")
		}
		return tx, result.Proof
	}
	id, signed := issueChallenge(firstKey, firstPublic)
	tx, proof := beginVerified(id, signed)
	issued, err := store.IssueEmail(ctx, tx, proof)
	if err != nil {
		_ = tx.Rollback()
		t.Fatal("could not issue first device session")
	}
	if token, err := issued.AfterCommit(ctx, db); err == nil || token != "" {
		_ = tx.Rollback()
		t.Fatal("uncommitted session released a token")
	}
	if err := tx.Commit(); err != nil {
		t.Fatal("could not commit first session")
	}
	token, err := issued.AfterCommit(ctx, db)
	if err != nil || token == "" {
		t.Fatal("committed session did not release a token")
	}
	claims, err := codec.Verify(token)
	if err != nil {
		t.Fatal("could not verify issued token")
	}
	authTx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal("could not begin session authentication")
	}
	principal, err := store.Authenticate(ctx, authTx, token)
	if err != nil {
		_ = authTx.Rollback()
		t.Fatal("committed session could not authenticate")
	}
	details, err := principal.ForTransaction(ctx, authTx)
	if err != nil || details.SubjectID == 0 || details.AgeVerified || details.SessionID != claims.SessionID || details.DeviceID == "" {
		_ = authTx.Rollback()
		t.Fatal("session principal has incorrect initial state")
	}
	otherTx, err := db.BeginTx(ctx, nil)
	if err != nil {
		_ = authTx.Rollback()
		t.Fatal("could not create a second transaction")
	}
	if _, err := principal.ForTransaction(ctx, otherTx); err == nil {
		_ = otherTx.Rollback()
		_ = authTx.Rollback()
		t.Fatal("principal was usable in a different transaction")
	}
	_ = otherTx.Rollback()
	if err := authTx.Commit(); err != nil {
		t.Fatal("could not finish authentication transaction")
	}
	if _, err := principal.ForTransaction(ctx, authTx); err == nil {
		t.Fatal("principal remained usable after commit")
	}
	if _, err := (&Principal{}).ForTransaction(ctx, authTx); err == nil {
		t.Fatal("zero principal was accepted")
	}
	selfTx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal("could not begin same-transaction revocation check")
	}
	selfPrincipal, err := store.Authenticate(ctx, selfTx, token)
	if err != nil {
		_ = selfTx.Rollback()
		t.Fatal("could not authenticate revocation fixture")
	}
	if _, err := selfTx.ExecContext(ctx, `UPDATE trusted_device SET state = 'revoked' WHERE id = ?`, []byte(details.DeviceID)); err != nil {
		_ = selfTx.Rollback()
		t.Fatal("could not revoke within active transaction")
	}
	if _, err := selfPrincipal.ForTransaction(ctx, selfTx); err == nil {
		_ = selfTx.Rollback()
		t.Fatal("principal survived revocation in its own transaction")
	}
	_ = selfTx.Rollback()
	if _, err := db.ExecContext(ctx, `UPDATE user SET age_verified_at = UTC_TIMESTAMP(6) WHERE id = ?`, details.SubjectID); err != nil {
		t.Fatal("could not set isolated age fixture")
	}
	ageTx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal("could not begin age projection check")
	}
	agePrincipal, err := store.Authenticate(ctx, ageTx, token)
	if err != nil {
		_ = ageTx.Rollback()
		t.Fatal("could not authenticate age fixture")
	}
	ageDetails, err := agePrincipal.ForTransaction(ctx, ageTx)
	if err != nil || !ageDetails.AgeVerified {
		_ = ageTx.Rollback()
		t.Fatal("current age state was not projected")
	}
	_ = ageTx.Rollback()
	if _, err := db.ExecContext(ctx, `UPDATE user SET age_verified_at = NULL WHERE id = ?`, details.SubjectID); err != nil {
		t.Fatal("could not restore isolated age fixture")
	}
	expiringTx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal("could not begin principal expiry check")
	}
	expiringPrincipal, err := store.Authenticate(ctx, expiringTx, token)
	if err != nil {
		_ = expiringTx.Rollback()
		t.Fatal("could not authenticate expiry fixture")
	}
	codec.now = func() time.Time { return claims.ExpiresAt }
	store.now = codec.now
	if _, err := expiringPrincipal.ForTransaction(ctx, expiringTx); err == nil {
		_ = expiringTx.Rollback()
		t.Fatal("principal remained usable at token expiry")
	}
	codec.now = time.Now
	store.now = time.Now
	_ = expiringTx.Rollback()
	for _, value := range []any{issued, &issued, principal, *principal, details, &details} {
		if _, err := json.Marshal(value); err == nil {
			t.Fatal("sensitive session result was JSON-serializable")
		}
		var log bytes.Buffer
		slog.New(slog.NewJSONHandler(&log, nil)).Info("test", "value", value)
		var entry map[string]any
		if json.Unmarshal(log.Bytes(), &entry) != nil {
			t.Fatal("could not parse session log")
		}
		output := fmt.Sprintf("%+v %#v %v", value, value, entry["value"])
		for _, secret := range []string{token, details.SessionID, details.DeviceID} {
			if strings.Contains(output, secret) {
				t.Fatal("session credential leaked into output")
			}
		}
	}

	// Existing identity on a different device must not get a session.
	id, signed = issueChallenge(otherKey, otherPublic)
	newTx, newProof := beginVerified(id, signed)
	if _, err := store.IssueEmail(ctx, newTx, newProof); !errors.Is(err, ErrApprovalRequired) {
		_ = newTx.Rollback()
		t.Fatal("untrusted device did not require approval")
	}
	_ = newTx.Rollback()
	var count int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM auth_session`).Scan(&count); err != nil || count != 1 {
		t.Fatal("unapproved device acquired a session")
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM trusted_device`).Scan(&count); err != nil || count != 1 {
		t.Fatal("unapproved device was trusted")
	}
	// A valid session created inside a rolled-back transaction stays unusable.
	id, signed = issueChallenge(firstKey, firstPublic)
	rolledTx, rolledProof := beginVerified(id, signed)
	rolled, err := store.IssueEmail(ctx, rolledTx, rolledProof)
	if err != nil {
		_ = rolledTx.Rollback()
		t.Fatal("could not issue rollback fixture")
	}
	_ = rolledTx.Rollback()
	if value, err := rolled.AfterCommit(ctx, db); err == nil || value != "" {
		t.Fatal("rolled-back session released a token")
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM auth_session`).Scan(&count); err != nil || count != 1 {
		t.Fatal("rolled-back session remained in database")
	}
	// Even in the same live transaction, one verified challenge can create
	// at most one session. The caller rolls back the whole transaction on error.
	id, signed = issueChallenge(firstKey, firstPublic)
	reuseTx, reuseProof := beginVerified(id, signed)
	once, err := store.IssueEmail(ctx, reuseTx, reuseProof)
	if err != nil {
		_ = reuseTx.Rollback()
		t.Fatal("could not prepare repeated-proof check")
	}
	if _, err := store.IssueEmail(ctx, reuseTx, reuseProof); !errors.Is(err, ErrSessionUnavailable) {
		_ = reuseTx.Rollback()
		t.Fatal("one challenge created multiple sessions")
	}
	_ = reuseTx.Rollback()
	if value, err := once.AfterCommit(ctx, db); err == nil || value != "" {
		t.Fatal("rolled-back repeated proof released a token")
	}
	// The same trusted device may authenticate again with a fresh challenge.
	id, signed = issueChallenge(firstKey, firstPublic)
	trustedTx, trustedProof := beginVerified(id, signed)
	trustedIssued, err := store.IssueEmail(ctx, trustedTx, trustedProof)
	if err != nil {
		_ = trustedTx.Rollback()
		t.Fatal("trusted device could not obtain a new session")
	}
	if err := trustedTx.Commit(); err != nil {
		t.Fatal("could not commit trusted-device session")
	}
	secondToken, err := trustedIssued.AfterCommit(ctx, db)
	if err != nil || secondToken == "" {
		t.Fatal("trusted-device session was not durable")
	}
	secondClaims, err := codec.Verify(secondToken)
	if err != nil || secondClaims.SubjectID != claims.SubjectID || secondClaims.SessionID == claims.SessionID {
		t.Fatal("fresh trusted-device session did not preserve the same subject")
	}
	checkDenied := func() {
		t.Helper()
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal("could not begin negative authentication")
		}
		defer tx.Rollback()
		if _, err := store.Authenticate(ctx, tx, token); !errors.Is(err, ErrSessionUnavailable) {
			t.Fatal("revoked or altered session was accepted")
		}
	}
	changes := []struct {
		change, restore string
		args            []any
	}{
		{`UPDATE user SET status = 'disabled' WHERE id = ?`, `UPDATE user SET status = 'active' WHERE id = ?`, []any{details.SubjectID}},
		{`UPDATE user SET token_version = token_version + 1 WHERE id = ?`, `UPDATE user SET token_version = token_version - 1 WHERE id = ?`, []any{details.SubjectID}},
		{`UPDATE login_identity SET state = 'revoked' WHERE user_id = ?`, `UPDATE login_identity SET state = 'active' WHERE user_id = ?`, []any{details.SubjectID}},
		{`UPDATE trusted_device SET state = 'revoked' WHERE id = ?`, `UPDATE trusted_device SET state = 'active' WHERE id = ?`, []any{[]byte(details.DeviceID)}},
		{`UPDATE auth_session SET state = 'revoked', revoked_at = UTC_TIMESTAMP(6) WHERE id = ?`, `UPDATE auth_session SET state = 'active', revoked_at = NULL WHERE id = ?`, []any{[]byte(details.SessionID)}},
		{`UPDATE auth_session SET expires_at = DATE_SUB(expires_at, INTERVAL 1 SECOND) WHERE id = ?`, `UPDATE auth_session SET expires_at = DATE_ADD(expires_at, INTERVAL 1 SECOND) WHERE id = ?`, []any{[]byte(details.SessionID)}},
	}
	for _, item := range changes {
		if _, err := db.ExecContext(ctx, item.change, item.args...); err != nil {
			t.Fatal("could not modify isolated session state")
		}
		checkDenied()
		if _, err := db.ExecContext(ctx, item.restore, item.args...); err != nil {
			t.Fatal("could not restore isolated session state")
		}
	}
	codec.now = func() time.Time { return claims.ExpiresAt }
	store.now = codec.now
	checkDenied()
	codec.now = time.Now
	store.now = time.Now

	// A device revocation waits for an in-flight authorized transaction; after
	// it commits, every later authentication observes the revoked device.
	lockedTx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal("could not begin revocation test")
	}
	if _, err := store.Authenticate(ctx, lockedTx, token); err != nil {
		_ = lockedTx.Rollback()
		t.Fatal("could not hold authenticated transaction")
	}
	revoked := make(chan error, 1)
	go func() {
		_, err := db.ExecContext(ctx, `UPDATE trusted_device SET state = 'revoked' WHERE id = ?`, []byte(details.DeviceID))
		revoked <- err
	}()
	deadline := time.Now().Add(5 * time.Second)
	blocked := 0
	for time.Now().Before(deadline) {
		if err := db.QueryRowContext(ctx, `SELECT COUNT(DISTINCT w.REQUESTING_ENGINE_TRANSACTION_ID)
            FROM performance_schema.data_lock_waits w JOIN performance_schema.data_locks l ON l.ENGINE_LOCK_ID = w.REQUESTING_ENGINE_LOCK_ID
            WHERE l.OBJECT_SCHEMA = DATABASE() AND l.OBJECT_NAME = 'trusted_device'`).Scan(&blocked); err != nil {
			_ = lockedTx.Rollback()
			t.Fatal("could not observe device revocation lock")
		}
		if blocked >= 1 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if blocked < 1 {
		_ = lockedTx.Rollback()
		t.Fatal("device revocation did not wait for the active transaction")
	}
	if err := lockedTx.Commit(); err != nil || <-revoked != nil {
		t.Fatal("could not complete the device revocation test")
	}
	checkDenied()
}
