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
	issuancetestsupport "nerae/api/internal/modules/auth/issuance/testsupport"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"

	"nerae/api/internal/modules/auth/challenge"
	"nerae/api/internal/modules/auth/domain"
	"nerae/api/internal/modules/auth/identity"
	"nerae/api/internal/modules/auth/loginledger"
	"nerae/api/internal/modules/auth/loginreceipt"
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
	challenges, err := challenge.NewStore(hasher, replay, protector, "fixture-email-code-hash-v1", challenge.Policy{ChallengeTTL: 5 * time.Minute,
		MaxAttempts: 3, DeviceProof: domain.ProofPolicy{MaxLifetime: 5 * time.Minute, ClockSkew: 30 * time.Second}})
	if err != nil {
		t.Fatal("could not initialize challenge store")
	}
	issuancetestsupport.InitializeEmailCodeHashEpoch(t, db, protector, hasher)
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
	crypto, err := loginreceipt.New(loginreceipt.KeyConfig{IdempotencyHMACKey: bytes.Repeat([]byte{6}, 32),
		RequestHMACKey: bytes.Repeat([]byte{7}, 32), AES256GCMKey: bytes.Repeat([]byte{8}, 32)})
	if err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	request := loginreceipt.Request{IdempotencyKey: "AQEBAQEBAQEBAQEBAQEBAQ", ChallengeID: id, Code: "012345", DeviceProof: signed}
	binding, err := crypto.Summarize(request)
	if err != nil {
		_ = tx.Rollback()
		t.Fatal("could not bind synthetic receipt request")
	}
	expires := time.Now().UTC().Add(2 * time.Minute).Truncate(time.Microsecond)
	otherTx, err := db.BeginTx(ctx, nil)
	if err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if _, err := issued.SealForReceipt(ctx, otherTx, proof, crypto, binding, expires); !errors.Is(err, ErrSessionUnavailable) {
		_ = otherTx.Rollback()
		_ = tx.Rollback()
		t.Fatal("issued session sealed in a different transaction")
	}
	_ = otherTx.Rollback()
	wrongRequest := request
	wrongRequest.ChallengeID = strings.Repeat("z", 32)
	wrongBinding, err := crypto.Summarize(wrongRequest)
	if err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if _, err := issued.SealForReceipt(ctx, tx, proof, crypto, wrongBinding, expires); !errors.Is(err, ErrSessionUnavailable) {
		_ = tx.Rollback()
		t.Fatal("receipt accepted a different challenge")
	}
	var deviceID, identityID []byte
	issuedClaims, err := codec.Verify(issued.token.ForResponse())
	if err != nil || tx.QueryRowContext(ctx, `SELECT device_id, identity_id FROM auth_session WHERE id = ?`, []byte(issuedClaims.SessionID)).Scan(&deviceID, &identityID) != nil {
		_ = tx.Rollback()
		t.Fatal("could not inspect synthetic session device")
	}
	if _, err := tx.ExecContext(ctx, `UPDATE trusted_device SET state = 'revoked' WHERE id = ?`, deviceID); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if _, err := issued.SealForReceipt(ctx, tx, proof, crypto, binding, expires); !errors.Is(err, ErrSessionUnavailable) {
		_ = tx.Rollback()
		t.Fatal("receipt was sealed after same-transaction device revocation")
	}
	if _, err := tx.ExecContext(ctx, `UPDATE trusted_device SET state = 'active' WHERE id = ?`, deviceID); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	scope, err := protector.Index("person@example.test")
	if err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE login_identity SET subject_lookup_hmac = ? WHERE id = ?`, bytes.Repeat([]byte{9}, 32), identityID); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if _, err := issued.SealForReceipt(ctx, tx, proof, crypto, binding, expires); !errors.Is(err, ErrSessionUnavailable) {
		_ = tx.Rollback()
		t.Fatal("receipt was sealed after same-transaction identity replacement")
	}
	if _, err := tx.ExecContext(ctx, `UPDATE login_identity SET subject_lookup_hmac = ? WHERE id = ?`, scope[:], identityID); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	capped, err := issued.SealForReceipt(ctx, tx, proof, crypto, binding, time.Now().UTC().Add(2*time.Hour).Truncate(time.Microsecond))
	if err != nil || !capped.ExpiresAt().Equal(issuedClaims.ExpiresAt) {
		_ = tx.Rollback()
		t.Fatal("session receipt lifetime exceeded the session lifetime")
	}
	sealed, err := issued.SealForReceipt(ctx, tx, proof, crypto, binding, expires)
	if err != nil || sealed.Reference() != issuedClaims.SessionID || !sealed.ExpiresAt().Equal(expires) || bytes.Contains(sealed.Ciphertext().Bytes(), []byte(issued.token.ForResponse())) {
		_ = tx.Rollback()
		t.Fatal("session receipt was not encrypted or bound to its session")
	}
	if _, err := json.Marshal(sealed); err == nil || strings.Contains(fmt.Sprintf("%+v", sealed), issued.token.ForResponse()) {
		_ = tx.Rollback()
		t.Fatal("session receipt formatting exposed sensitive material")
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
	verifyTx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := challenges.VerifyReceiptProof(ctx, verifyTx, scope, id, signed); err != nil {
		_ = verifyTx.Rollback()
		t.Fatal("could not revalidate the original device proof")
	}
	replayPrincipal, err := store.Authenticate(ctx, verifyTx, token)
	if err != nil {
		_ = verifyTx.Rollback()
		t.Fatal("could not recheck active session before decrypting test receipt")
	}
	if _, err := replayPrincipal.ForTransaction(ctx, verifyTx); err != nil {
		_ = verifyTx.Rollback()
		t.Fatal("session authorization recheck failed")
	}
	aad, err := crypto.NewContext(scope[:], binding, loginreceipt.ResultAuthenticated, sealed.Reference(), expires)
	if err != nil {
		_ = verifyTx.Rollback()
		t.Fatal(err)
	}
	decrypted, err := crypto.Open(aad, sealed.Ciphertext())
	if err != nil || string(decrypted.Bytes()) != token {
		_ = verifyTx.Rollback()
		t.Fatal("encrypted receipt did not preserve the committed session token")
	}
	_ = verifyTx.Rollback()
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
	otherTx, err = db.BeginTx(ctx, nil)
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
	revokeCheckTime := time.Now().UTC()
	codec.now = func() time.Time { return revokeCheckTime }
	store.now = func() time.Time { return revokeCheckTime }
	expiringTx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal("could not begin principal expiry check")
	}
	expiringPrincipal, err := store.Authenticate(ctx, expiringTx, token)
	if err != nil {
		_ = expiringTx.Rollback()
		t.Fatal("could not authenticate expiry fixture")
	}
	revokeCheckTime = claims.ExpiresAt
	if _, err := expiringPrincipal.ForTransaction(ctx, expiringTx); !errors.Is(err, ErrSessionUnavailable) {
		_ = expiringTx.Rollback()
		t.Fatal("principal recheck did not fail closed at expiry")
	} else if classified := classifyPrincipalRecheckError(expiringPrincipal, err); !errors.Is(classified, ErrInvalidSession) {
		_ = expiringTx.Rollback()
		t.Fatal("revoke did not distinguish expiry from recheck unavailability")
	}
	revokeCheckTime = time.Now().UTC()
	codec.now = time.Now
	store.now = time.Now
	if classified := classifyPrincipalRecheckError(expiringPrincipal, ErrSessionUnavailable); !errors.Is(classified, ErrSessionUnavailable) {
		_ = expiringTx.Rollback()
		t.Fatal("non-expiry principal recheck failure was misclassified")
	}
	if _, err := expiringTx.ExecContext(ctx, `UPDATE auth_session SET authenticated_at=DATE_SUB(UTC_TIMESTAMP(6), INTERVAL 1 MINUTE), expires_at=DATE_SUB(UTC_TIMESTAMP(6), INTERVAL 1 SECOND) WHERE id=?`, []byte(claims.SessionID)); err != nil {
		_ = expiringTx.Rollback()
		t.Fatal("could not prepare deterministic expired-row classification")
	}
	if err := classifyUnchangedSession(ctx, expiringTx, claims.SessionID); !errors.Is(err, ErrInvalidSession) {
		_ = expiringTx.Rollback()
		t.Fatal("expired current row was not classified as invalid")
	}
	if err := classifyUnchangedSession(ctx, expiringTx, strings.Repeat("A", 22)); !errors.Is(err, ErrInvalidSession) {
		_ = expiringTx.Rollback()
		t.Fatal("missing current row was not classified as invalid")
	}
	canceledLookup, cancelLookup := context.WithCancel(ctx)
	cancelLookup()
	if err := classifyUnchangedSession(canceledLookup, expiringTx, claims.SessionID); !errors.Is(err, ErrSessionUnavailable) {
		_ = expiringTx.Rollback()
		t.Fatal("database lookup failure was not classified as unavailable")
	}
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
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM auth_session WHERE user_id = ?`, details.SubjectID).Scan(&count); err != nil || count != 1 {
		t.Fatal("unapproved device acquired a session")
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM trusted_device WHERE user_id = ?`, details.SubjectID).Scan(&count); err != nil || count != 1 {
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
	rolledBinding, err := crypto.Summarize(loginreceipt.Request{IdempotencyKey: "AgICAgICAgICAgICAgICAg", ChallengeID: id, Code: "012345", DeviceProof: signed})
	if err != nil {
		_ = rolledTx.Rollback()
		t.Fatal(err)
	}
	if _, err := rolled.SealForReceipt(ctx, rolledTx, rolledProof, crypto, rolledBinding, expires); err != nil {
		_ = rolledTx.Rollback()
		t.Fatal("could not encrypt rollback fixture")
	}
	_ = rolledTx.Rollback()
	if _, err := rolled.SealForReceipt(ctx, rolledTx, rolledProof, crypto, rolledBinding, expires); !errors.Is(err, ErrSessionUnavailable) {
		t.Fatal("ended transaction could still seal a receipt")
	}
	if value, err := rolled.AfterCommit(ctx, db); err == nil || value != "" {
		t.Fatal("rolled-back session released a token")
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM auth_session WHERE user_id = ?`, details.SubjectID).Scan(&count); err != nil || count != 1 {
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
	checkDenied := func(want error) {
		t.Helper()
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal("could not begin negative authentication")
		}
		defer tx.Rollback()
		if _, err := store.Authenticate(ctx, tx, token); !errors.Is(err, want) {
			t.Fatalf("revoked or altered session returned wrong classification: %v", err)
		}
	}
	changes := []struct {
		change, restore string
		args            []any
		want            error
	}{
		{`UPDATE user SET status = 'disabled' WHERE id = ?`, `UPDATE user SET status = 'active' WHERE id = ?`, []any{details.SubjectID}, ErrInvalidSession},
		{`UPDATE user SET token_version = token_version + 1 WHERE id = ?`, `UPDATE user SET token_version = token_version - 1 WHERE id = ?`, []any{details.SubjectID}, ErrInvalidSession},
		{`UPDATE login_identity SET state = 'revoked' WHERE user_id = ?`, `UPDATE login_identity SET state = 'active' WHERE user_id = ?`, []any{details.SubjectID}, ErrInvalidSession},
		{`UPDATE trusted_device SET state = 'revoked' WHERE id = ?`, `UPDATE trusted_device SET state = 'active' WHERE id = ?`, []any{[]byte(details.DeviceID)}, ErrInvalidSession},
		{`UPDATE auth_session SET state = 'revoked', revoked_at = UTC_TIMESTAMP(6) WHERE id = ?`, `UPDATE auth_session SET state = 'active', revoked_at = NULL WHERE id = ?`, []any{[]byte(details.SessionID)}, ErrInvalidSession},
		{`UPDATE auth_session SET expires_at = DATE_SUB(expires_at, INTERVAL 1 SECOND) WHERE id = ?`, `UPDATE auth_session SET expires_at = DATE_ADD(expires_at, INTERVAL 1 SECOND) WHERE id = ?`, []any{[]byte(details.SessionID)}, ErrSessionUnavailable},
	}
	for _, item := range changes {
		if _, err := db.ExecContext(ctx, item.change, item.args...); err != nil {
			t.Fatal("could not modify isolated session state")
		}
		checkDenied(item.want)
		if _, err := db.ExecContext(ctx, item.restore, item.args...); err != nil {
			t.Fatal("could not restore isolated session state")
		}
	}
	codec.now = func() time.Time { return claims.ExpiresAt }
	store.now = codec.now
	checkDenied(ErrInvalidSession)
	codec.now = time.Now
	store.now = time.Now

	// Invalid credentials and missing sessions are distinct from a broken DB
	// transaction or cancelled query; neither result leaks the supplied token.
	negativeTx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal("could not begin classification check")
	}
	if _, err := store.Authenticate(ctx, negativeTx, token+"tampered"); !errors.Is(err, ErrInvalidSession) {
		t.Fatal("tampered credential was not classified invalid")
	}
	missing, err := codec.Issue(claims.SubjectID, "AAAAAAAAAAAAAAAAAAAAAA", claims.TokenVersion)
	if err != nil {
		t.Fatal("could not create missing-session token")
	}
	if _, err := store.Authenticate(ctx, negativeTx, missing.ForResponse()); !errors.Is(err, ErrInvalidSession) {
		t.Fatal("missing session was not classified invalid")
	}
	cancelled, cancelQuery := context.WithCancel(ctx)
	cancelQuery()
	if _, err := store.Authenticate(cancelled, negativeTx, token); !errors.Is(err, ErrSessionUnavailable) {
		t.Fatal("cancelled DB lookup was not classified unavailable")
	}
	_ = negativeTx.Rollback()
	if _, err := store.Authenticate(ctx, negativeTx, token); !errors.Is(err, ErrSessionUnavailable) {
		t.Fatal("ended transaction was not classified unavailable")
	}

	if err := store.RevokeCurrent(ctx, db, secondToken); err != nil {
		t.Fatal("current-session revoke failed for an active session")
	}
	otherSessionTx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal("could not begin other-session preservation check")
	}
	if _, err := store.Authenticate(ctx, otherSessionTx, token); err != nil {
		_ = otherSessionTx.Rollback()
		t.Fatal("revoking one session invalidated another session")
	}
	_ = otherSessionTx.Rollback()
	if err := store.RevokeCurrent(ctx, db, secondToken); !errors.Is(err, ErrInvalidSession) {
		t.Fatal("repeating revoke did not classify the already-revoked session as invalid")
	}

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
	checkDenied(ErrInvalidSession)
}

func TestSessionReceiptRecovery(t *testing.T) {
	db := sessionTestDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	hasher, _ := domain.NewEmailCodeHasher(bytes.Repeat([]byte{1}, 32))
	replay, _ := repository.NewProofReplayStore(bytes.Repeat([]byte{2}, 32))
	protector, _ := domain.NewEmailIdentityProtector(bytes.Repeat([]byte{3}, 32), bytes.Repeat([]byte{4}, 32), "test-key")
	challenges, err := challenge.NewStore(hasher, replay, protector, "fixture-email-code-hash-v1", challenge.Policy{ChallengeTTL: 5 * time.Minute,
		MaxAttempts: 3, DeviceProof: domain.ProofPolicy{MaxLifetime: 5 * time.Minute, ClockSkew: 30 * time.Second}})
	if err != nil {
		t.Fatal(err)
	}
	issuancetestsupport.InitializeEmailCodeHashEpoch(t, db, protector, hasher)
	resolver, _ := identity.NewResolver(protector)
	codec, _ := NewTokenCodec(bytes.Repeat([]byte{5}, 32), TokenPolicy{MaxLifetime: time.Hour})
	store, _ := NewStore(resolver, codec)
	crypto, err := loginreceipt.New(loginreceipt.KeyConfig{IdempotencyHMACKey: bytes.Repeat([]byte{6}, 32),
		RequestHMACKey: bytes.Repeat([]byte{7}, 32), AES256GCMKey: bytes.Repeat([]byte{8}, 32)})
	if err != nil {
		t.Fatal(err)
	}
	ledger, err := loginledger.NewStore(crypto, protector, "session-receipt-test")
	if err != nil {
		t.Fatal(err)
	}
	initTx, err := db.BeginTx(ctx, nil)
	if err != nil || ledger.Initialize(ctx, initTx) != nil || initTx.Commit() != nil {
		t.Fatal("isolated receipt initialization failed")
	}
	recoverer, err := NewReceiptRecoverer(store, ledger, challenges, crypto)
	if err != nil {
		t.Fatal(err)
	}
	prepare := func(email string, requestKey string) (*sql.Tx, ReceiptCandidate, Issued, loginreceipt.Request, [32]byte, loginreceipt.Binding) {
		t.Helper()
		private, public := testDevice(t)
		id, _ := domain.GenerateChallengeID()
		nonce, _ := domain.GenerateChallengeNonce()
		issueTx, err := db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		if err := challenges.Issue(ctx, issueTx, challenge.IssueInput{Email: email, ChallengeID: id, Nonce: nonce, Code: "012345", PublicKey: public}); err != nil {
			_ = issueTx.Rollback()
			t.Fatal("challenge issue failed")
		}
		if err := issueTx.Commit(); err != nil {
			t.Fatal(err)
		}
		signed := testProof(t, private, id, nonce)
		request := loginreceipt.Request{IdempotencyKey: requestKey, ChallengeID: id, Code: "012345", DeviceProof: signed}
		binding, err := crypto.Summarize(request)
		if err != nil {
			t.Fatal(err)
		}
		scope, err := protector.Index(email)
		if err != nil {
			t.Fatal(err)
		}
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		handle, err := ledger.Lock(ctx, tx, scope, binding)
		if err != nil {
			_ = tx.Rollback()
			t.Fatal("receipt reservation failed")
		}
		attempt, err := challenges.Attempt(ctx, tx, challenge.AttemptInput{ChallengeID: id, Code: request.Code, DeviceProof: signed})
		if err != nil || !attempt.Verified || attempt.Proof == nil {
			_ = tx.Rollback()
			t.Fatal("synthetic challenge verification failed")
		}
		issued, err := store.IssueEmail(ctx, tx, attempt.Proof)
		if err != nil {
			_ = tx.Rollback()
			t.Fatal("synthetic session issue failed")
		}
		expiry := time.Now().UTC().Add(2 * time.Minute).Truncate(time.Microsecond)
		sealed, err := issued.SealForReceipt(ctx, tx, attempt.Proof, crypto, binding, expiry)
		if err != nil || ledger.Put(ctx, tx, handle, loginreceipt.ResultAuthenticated, sealed.Reference(), sealed.ExpiresAt(), sealed.Ciphertext()) != nil {
			_ = tx.Rollback()
			t.Fatal("synthetic receipt write failed")
		}
		handle, err = ledger.Lock(ctx, tx, scope, binding)
		if err != nil {
			_ = tx.Rollback()
			t.Fatal("stored receipt lookup failed")
		}
		proof, err := challenges.VerifyReceiptProof(ctx, tx, scope, id, signed)
		if err != nil {
			_ = tx.Rollback()
			t.Fatal("proof recheck failed")
		}
		candidate, err := recoverer.Recover(ctx, tx, handle, proof, request)
		if err != nil {
			_ = tx.Rollback()
			t.Fatalf("session receipt recovery failed: %v", err)
		}
		return tx, candidate, issued, request, scope, binding
	}
	// Establish a repeatable-read snapshot before the session is created, as a
	// concurrent retry can do while it waits on the same login receipt.
	snapshotTx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	var priorSessions int
	if err := snapshotTx.QueryRowContext(ctx, `SELECT COUNT(*) FROM auth_session`).Scan(&priorSessions); err != nil {
		_ = snapshotTx.Rollback()
		t.Fatal(err)
	}
	tx, candidate, issued, request, scope, binding := prepare("receipt-session@example.test", "AQEBAQEBAQEBAQEBAQEBAQ")
	if token, err := candidate.AfterCommit(ctx, db); err == nil || token != "" {
		_ = tx.Rollback()
		t.Fatal("uncommitted receipt released a token")
	}
	if _, err := json.Marshal(candidate); err == nil || strings.Contains(fmt.Sprintf("%+v", candidate), request.DeviceProof) {
		_ = tx.Rollback()
		t.Fatal("receipt candidate exposed sensitive material")
	}
	if err := tx.Commit(); err != nil {
		_ = snapshotTx.Rollback()
		t.Fatal(err)
	}
	snapshotHandle, err := ledger.Lock(ctx, snapshotTx, scope, binding)
	if err != nil {
		_ = snapshotTx.Rollback()
		t.Fatal("stale snapshot could not lock the committed receipt")
	}
	snapshotProof, err := challenges.VerifyReceiptProof(ctx, snapshotTx, scope, request.ChallengeID, request.DeviceProof)
	if err != nil {
		_ = snapshotTx.Rollback()
		t.Fatal("stale snapshot could not recheck the committed proof")
	}
	snapshotCandidate, err := recoverer.Recover(ctx, snapshotTx, snapshotHandle, snapshotProof, request)
	if err != nil {
		_ = snapshotTx.Rollback()
		t.Fatalf("stale snapshot could not read the committed session: %v", err)
	}
	if err := snapshotTx.Commit(); err != nil {
		t.Fatal(err)
	}
	token, err := candidate.AfterCommit(ctx, db)
	if err != nil || token == "" {
		t.Fatal("committed valid receipt did not recover a token")
	}
	if value, err := snapshotCandidate.AfterCommit(ctx, db); err != nil || value != token {
		t.Fatal("stale snapshot did not recover the same committed token")
	}
	original, err := issued.AfterCommit(ctx, db)
	if err != nil || token != original {
		t.Fatal("receipt did not recover the originally issued token")
	}
	claims, err := codec.Verify(token)
	if err != nil {
		t.Fatal(err)
	}
	var deviceID, identityID []byte
	if err := db.QueryRowContext(ctx, `SELECT device_id, identity_id FROM auth_session WHERE id=?`, []byte(claims.SessionID)).Scan(&deviceID, &identityID); err != nil {
		t.Fatal(err)
	}
	changes := []struct {
		change, restore string
		args            []any
	}{
		{`UPDATE user SET status='disabled' WHERE id=?`, `UPDATE user SET status='active' WHERE id=?`, []any{claims.SubjectID}},
		{`UPDATE user SET token_version=token_version+1 WHERE id=?`, `UPDATE user SET token_version=token_version-1 WHERE id=?`, []any{claims.SubjectID}},
		{`UPDATE login_identity SET state='revoked' WHERE id=?`, `UPDATE login_identity SET state='active' WHERE id=?`, []any{identityID}},
		{`UPDATE trusted_device SET state='revoked' WHERE id=?`, `UPDATE trusted_device SET state='active' WHERE id=?`, []any{deviceID}},
		{`UPDATE auth_session SET state='revoked', revoked_at=UTC_TIMESTAMP(6) WHERE id=?`, `UPDATE auth_session SET state='active', revoked_at=NULL WHERE id=?`, []any{[]byte(claims.SessionID)}},
	}
	for _, item := range changes {
		if _, err := db.ExecContext(ctx, item.change, item.args...); err != nil {
			t.Fatal(err)
		}
		if value, err := candidate.AfterCommit(ctx, db); !errors.Is(err, ErrInvalidSession) || value != "" {
			t.Fatal("revoked receipt released a token")
		}
		if _, err := db.ExecContext(ctx, item.restore, item.args...); err != nil {
			t.Fatal(err)
		}
	}
	// Hold a real user-row lock, revoke inside that transaction and ensure a
	// concurrent receipt recovery observes the revocation after waiting.
	gate, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := gate.ExecContext(ctx, `UPDATE user SET status='disabled' WHERE id=?`, claims.SubjectID); err != nil {
		_ = gate.Rollback()
		t.Fatal(err)
	}
	type replayOutcome struct {
		token string
		err   error
	}
	waiting := make(chan replayOutcome, 1)
	go func() {
		value, err := candidate.AfterCommit(ctx, db)
		waiting <- replayOutcome{value, err}
	}()
	deadline := time.Now().Add(5 * time.Second)
	blocked := 0
	for time.Now().Before(deadline) {
		if err := db.QueryRowContext(ctx, `SELECT COUNT(DISTINCT w.REQUESTING_ENGINE_TRANSACTION_ID)
			FROM performance_schema.data_lock_waits w JOIN performance_schema.data_locks l ON l.ENGINE_LOCK_ID=w.REQUESTING_ENGINE_LOCK_ID
			WHERE l.OBJECT_SCHEMA=DATABASE() AND l.OBJECT_NAME='user'`).Scan(&blocked); err != nil {
			_ = gate.Rollback()
			t.Fatal("could not observe the receipt recovery lock wait")
		}
		if blocked > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if blocked == 0 {
		_ = gate.Rollback()
		t.Fatal("receipt recovery did not wait for the account lock")
	}
	if err := gate.Commit(); err != nil {
		t.Fatal(err)
	}
	select {
	case result := <-waiting:
		if !errors.Is(result.err, ErrInvalidSession) || result.token != "" {
			t.Fatal("receipt recovery ignored the account revocation after lock wait")
		}
	case <-ctx.Done():
		t.Fatal("receipt recovery did not finish after the lock was released")
	}
	if _, err := db.ExecContext(ctx, `UPDATE user SET status='active' WHERE id=?`, claims.SubjectID); err != nil {
		t.Fatal(err)
	}
	// Move the injectable session clock past expiration at different stages of
	// replay, including immediately after commit; any observed expiry must fail
	// closed rather than return an already-expired credential.
	for advanceAt := int32(1); advanceAt <= 15; advanceAt++ {
		var calls atomic.Int32
		var advanced atomic.Bool
		store.now = func() time.Time {
			if calls.Add(1) >= advanceAt {
				advanced.Store(true)
				return claims.ExpiresAt
			}
			return time.Now().UTC()
		}
		value, err := candidate.AfterCommit(ctx, db)
		if advanced.Load() && (err == nil || value != "") {
			store.now = time.Now
			t.Fatal("session expired during receipt recovery but a token was released")
		}
	}
	store.now = time.Now
	// The proof has a shorter lifetime than the receipt and session. Advance
	// only that far: even a commit that completes after proof expiry must not
	// release the otherwise-live token.
	proofExpiredAt := time.Now().UTC().Add(95 * time.Second)
	observedAdvance := false
	for advanceAt := int32(1); advanceAt <= 15; advanceAt++ {
		var calls atomic.Int32
		var advanced atomic.Bool
		store.now = func() time.Time {
			if calls.Add(1) >= advanceAt {
				advanced.Store(true)
				return proofExpiredAt
			}
			return time.Now().UTC()
		}
		value, err := candidate.AfterCommit(ctx, db)
		if advanced.Load() {
			observedAdvance = true
			if err == nil || value != "" {
				store.now = time.Now
				t.Fatal("expired device proof released a still-live session receipt")
			}
		}
	}
	store.now = time.Now
	if !observedAdvance {
		t.Fatal("proof-expiry clock was never exercised")
	}

	verifyTx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	handle, err := ledger.Lock(ctx, verifyTx, scope, binding)
	if err != nil {
		_ = verifyTx.Rollback()
		t.Fatal(err)
	}
	proof, err := challenges.VerifyReceiptProof(ctx, verifyTx, scope, request.ChallengeID, request.DeviceProof)
	if err != nil {
		_ = verifyTx.Rollback()
		t.Fatal(err)
	}
	changedRequest := request
	changedRequest.Code = "999999"
	if _, err := recoverer.Recover(ctx, verifyTx, handle, proof, changedRequest); !errors.Is(err, ErrInvalidSession) {
		_ = verifyTx.Rollback()
		t.Fatal("changed request recovered a receipt")
	}
	otherTx, err := db.BeginTx(ctx, nil)
	if err != nil {
		_ = verifyTx.Rollback()
		t.Fatal(err)
	}
	if _, err := recoverer.Recover(ctx, otherTx, handle, proof, request); !errors.Is(err, ErrSessionUnavailable) {
		_ = otherTx.Rollback()
		_ = verifyTx.Rollback()
		t.Fatal("cross-transaction receipt or proof was accepted")
	}
	_ = otherTx.Rollback()
	keyDigest := binding.IdempotencyDigest()
	if _, err := verifyTx.ExecContext(ctx, `UPDATE auth_email_login_receipt SET created_at=DATE_SUB(UTC_TIMESTAMP(6), INTERVAL 2 MINUTE), expires_at=DATE_SUB(UTC_TIMESTAMP(6), INTERVAL 1 MINUTE) WHERE email_scope=? AND request_key_hmac=?`, scope[:], keyDigest[:]); err != nil {
		_ = verifyTx.Rollback()
		t.Fatal(err)
	}
	if _, err := recoverer.Recover(ctx, verifyTx, handle, proof, request); !errors.Is(err, ErrInvalidSession) {
		_ = verifyTx.Rollback()
		t.Fatal("expired receipt recovered a token")
	}
	_ = verifyTx.Rollback()

	cancelTx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	cancelHandle, err := ledger.Lock(ctx, cancelTx, scope, binding)
	if err != nil {
		_ = cancelTx.Rollback()
		t.Fatal(err)
	}
	cancelProof, err := challenges.VerifyReceiptProof(ctx, cancelTx, scope, request.ChallengeID, request.DeviceProof)
	if err != nil {
		_ = cancelTx.Rollback()
		t.Fatal(err)
	}
	canceled, stop := context.WithCancel(ctx)
	stop()
	if _, err := recoverer.Recover(canceled, cancelTx, cancelHandle, cancelProof, request); !errors.Is(err, ErrSessionUnavailable) {
		_ = cancelTx.Rollback()
		t.Fatal("storage/context failure was classified as an invalid session")
	}
	_ = cancelTx.Rollback()

	var ciphertext []byte
	if err := db.QueryRowContext(ctx, `SELECT response_enc FROM auth_email_login_receipt WHERE email_scope=? AND request_key_hmac=?`, scope[:], keyDigest[:]).Scan(&ciphertext); err != nil {
		t.Fatal(err)
	}
	corrupt := append([]byte(nil), ciphertext...)
	corrupt[len(corrupt)-1] ^= 1
	if _, err := db.ExecContext(ctx, `UPDATE auth_email_login_receipt SET response_enc=? WHERE email_scope=? AND request_key_hmac=?`, corrupt, scope[:], keyDigest[:]); err != nil {
		t.Fatal(err)
	}
	if value, err := candidate.AfterCommit(ctx, db); err == nil || value != "" {
		t.Fatal("tampered ciphertext released a token")
	}
	if _, err := db.ExecContext(ctx, `UPDATE auth_email_login_receipt SET response_enc=? WHERE email_scope=? AND request_key_hmac=?`, ciphertext, scope[:], keyDigest[:]); err != nil {
		t.Fatal(err)
	}
	rolledTx, rolledCandidate, _, _, _, _ := prepare("receipt-session-rollback@example.test", "AgICAgICAgICAgICAgICAg")
	if err := rolledTx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if value, err := rolledCandidate.AfterCommit(ctx, db); err == nil || value != "" {
		t.Fatal("rolled-back receipt released a token")
	}
}

func TestAuthenticationClassifiesBrokenCodecAsUnavailable(t *testing.T) {
	codec, err := NewTokenCodec(bytes.Repeat([]byte{5}, 32), TokenPolicy{MaxLifetime: time.Hour})
	if err != nil {
		t.Fatal("codec setup failed")
	}
	store := &Store{codec: codec, now: time.Now}
	cases := []struct {
		name       string
		breakCodec func(*TokenCodec)
	}{
		{"missing key", func(c *TokenCodec) { c.key = nil }},
		{"missing clock", func(c *TokenCodec) { c.now = nil }},
		{"zero clock", func(c *TokenCodec) { c.now = func() time.Time { return time.Time{} } }},
		{"epoch clock", func(c *TokenCodec) { c.now = func() time.Time { return time.Unix(0, 0) } }},
		{"invalid policy", func(c *TokenCodec) { c.policy.MaxLifetime = 0 }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			copy := *codec
			tc.breakCodec(&copy)
			store.codec = &copy
			_, err := store.Authenticate(context.Background(), &sql.Tx{}, "invalid-credential")
			if !errors.Is(err, ErrSessionUnavailable) || errors.Is(err, ErrInvalidSession) {
				t.Fatalf("broken codec was not classified unavailable: %v", err)
			}
		})
	}
}
