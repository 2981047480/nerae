package approval

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hmac"
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
	"strconv"
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
	"nerae/api/internal/modules/auth/session"
)

func isolatedDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("NERAE_APPROVAL_TEST_DSN")
	if dsn == "" {
		t.Skip("set NERAE_APPROVAL_TEST_DSN using the isolated MySQL harness")
	}
	cfg, err := mysql.ParseDSN(dsn)
	if err != nil {
		t.Fatal("invalid isolated approval DSN")
	}
	socket := filepath.Clean(cfg.Addr)
	parent := filepath.Dir(socket)
	resolved, err := filepath.EvalSymlinks(parent)
	info, statErr := os.Lstat(parent)
	socketInfo, socketErr := os.Lstat(socket)
	if err != nil || statErr != nil || socketErr != nil || cfg.Net != "unix" || cfg.DBName != "nerae_approval_test" ||
		cfg.User != "root" || cfg.Passwd != "" || !cfg.ParseTime || cfg.Loc != time.UTC ||
		filepath.Base(socket) != "mysql.sock" || filepath.Dir(resolved) != "/private/tmp" ||
		!strings.HasPrefix(filepath.Base(resolved), "nerae-approval-mysql-") ||
		!info.IsDir() || info.Mode().Perm()&0077 != 0 || info.Mode()&os.ModeSymlink != 0 || socketInfo.Mode()&os.ModeSocket == 0 {
		t.Fatal("refusing non-isolated MySQL target")
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatal("could not open isolated database")
	}
	db.SetMaxOpenConns(4)
	t.Cleanup(func() { _ = db.Close() })
	if db.Ping() != nil {
		t.Fatal("could not connect to isolated database")
	}
	return db
}

func testDevice(t *testing.T) (*ecdsa.PrivateKey, domain.DevicePublicJWK) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal("could not generate synthetic device")
	}
	encode := base64.RawURLEncoding.EncodeToString
	return key, domain.DevicePublicJWK{Kty: "EC", Crv: "P-256", X: encode(key.X.FillBytes(make([]byte, 32))), Y: encode(key.Y.FillBytes(make([]byte, 32)))}
}

func signedProof(t *testing.T, key *ecdsa.PrivateKey, id, nonce string) string {
	t.Helper()
	jti, err := domain.GenerateChallengeID()
	if err != nil {
		t.Fatal("could not generate proof ID")
	}
	now := time.Now().UTC()
	payload, err := json.Marshal(map[string]any{"aud": "nerae-auth-device", "purpose": "email_login", "resourceId": id,
		"nonce": nonce, "iat": now.Unix(), "exp": now.Add(time.Minute).Unix(), "jti": jti})
	if err != nil {
		t.Fatal("could not encode proof")
	}
	encode := base64.RawURLEncoding.EncodeToString
	input := encode([]byte(`{"alg":"ES256","typ":"JWT"}`)) + "." + encode(payload)
	digest := sha256.Sum256([]byte(input))
	r, signatureS, err := ecdsa.Sign(rand.Reader, key, digest[:])
	if err != nil {
		t.Fatal("could not sign proof")
	}
	return input + "." + encode(append(r.FillBytes(make([]byte, 32)), signatureS.FillBytes(make([]byte, 32))...))
}

func TestNewDeviceApprovalTransactions(t *testing.T) {
	db := isolatedDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
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
	store, err := NewStore(resolver, bytes.Repeat([]byte{6}, 32), "approval-test-key", Policy{Lifetime: 5 * time.Minute, ReauthWindow: time.Minute})
	if err != nil {
		t.Fatal("could not initialize approval store")
	}
	initTx, err := db.BeginTx(ctx, nil)
	if err != nil || store.InitializeKey(ctx, initTx) != nil || initTx.Commit() != nil {
		t.Fatal("could not initialize isolated approval key")
	}
	wrongStore, _ := NewStore(resolver, bytes.Repeat([]byte{7}, 32), "approval-test-key", Policy{Lifetime: 5 * time.Minute, ReauthWindow: time.Minute})
	wrongTx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal("could not begin wrong-key test")
	}
	if err := wrongStore.InitializeKey(ctx, wrongTx); !errors.Is(err, ErrUnavailable) {
		_ = wrongTx.Rollback()
		t.Fatal("mismatched approval key was accepted")
	}
	_ = wrongTx.Rollback()
	firstKey, firstPublic := testDevice(t)
	newKey, newPublic := testDevice(t)
	otherKey, otherPublic := testDevice(t)
	issueChallenge := func(email string, key *ecdsa.PrivateKey, public domain.DevicePublicJWK) (string, string) {
		t.Helper()
		id, _ := domain.GenerateChallengeID()
		nonce, _ := domain.GenerateChallengeNonce()
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal("could not begin isolated challenge")
		}
		defer tx.Rollback()
		if err := challenges.Issue(ctx, tx, challenge.IssueInput{Email: email, ChallengeID: id, Nonce: nonce, Code: "012345", PublicKey: public}); err != nil || tx.Commit() != nil {
			t.Fatal("could not issue isolated challenge")
		}
		return id, signedProof(t, key, id, nonce)
	}
	beginVerified := func(id, signed string) (*sql.Tx, *challenge.VerifiedEmail) {
		t.Helper()
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal("could not begin verified challenge")
		}
		attempt, err := challenges.Attempt(ctx, tx, challenge.AttemptInput{ChallengeID: id, Code: "012345", DeviceProof: signed})
		if err != nil || !attempt.Verified || attempt.Proof == nil {
			_ = tx.Rollback()
			t.Fatal("could not verify isolated challenge")
		}
		return tx, attempt.Proof
	}
	login := func(email string, key *ecdsa.PrivateKey, public domain.DevicePublicJWK) (string, session.TokenClaims) {
		t.Helper()
		id, proof := issueChallenge(email, key, public)
		tx, verified := beginVerified(id, proof)
		issued, err := auth.IssueEmail(ctx, tx, verified)
		if err != nil || tx.Commit() != nil {
			t.Fatal("could not establish isolated session")
		}
		token, err := issued.AfterCommit(ctx, db)
		if err != nil {
			t.Fatal("could not release isolated session")
		}
		claims, err := codec.Verify(token)
		if err != nil {
			t.Fatal("could not validate isolated token")
		}
		return token, claims
	}
	token, claims := login("first@example.test", firstKey, firstPublic)
	otherToken, otherClaims := login("other@example.test", otherKey, otherPublic)
	if claims.SubjectID == otherClaims.SubjectID {
		t.Fatal("isolated subjects were not distinct")
	}
	receiptCrypto, err := loginreceipt.New(loginreceipt.KeyConfig{IdempotencyHMACKey: bytes.Repeat([]byte{8}, 32),
		RequestHMACKey: bytes.Repeat([]byte{9}, 32), AES256GCMKey: bytes.Repeat([]byte{10}, 32)})
	if err != nil {
		t.Fatal(err)
	}
	testReceipt := true
	begin := func() (Pending, Data) {
		t.Helper()
		id, signed := issueChallenge("first@example.test", newKey, newPublic)
		tx, proof := beginVerified(id, signed)
		pending, err := store.BeginEmail(ctx, tx, proof)
		if err != nil {
			_ = tx.Rollback()
			t.Fatal("could not begin approval")
		}
		var sealed SealedReceipt
		var binding loginreceipt.Binding
		var receiptExpiry time.Time
		if testReceipt {
			binding, err = receiptCrypto.Summarize(loginreceipt.Request{IdempotencyKey: "AQEBAQEBAQEBAQEBAQEBAQ", ChallengeID: id, Code: "012345", DeviceProof: signed})
			if err != nil {
				_ = tx.Rollback()
				t.Fatal(err)
			}
			receiptExpiry = time.Now().UTC().Add(2 * time.Minute).Truncate(time.Microsecond)
			wrongBinding, err := receiptCrypto.Summarize(loginreceipt.Request{IdempotencyKey: "AQEBAQEBAQEBAQEBAQEBAQ", ChallengeID: strings.Repeat("z", 32), Code: "012345", DeviceProof: signed})
			if err != nil {
				_ = tx.Rollback()
				t.Fatal(err)
			}
			if _, err := pending.SealForReceipt(ctx, tx, proof, receiptCrypto, wrongBinding, receiptExpiry); !errors.Is(err, ErrUnavailable) {
				_ = tx.Rollback()
				t.Fatal("approval accepted a different challenge binding")
			}
			otherTx, err := db.BeginTx(ctx, nil)
			if err != nil {
				_ = tx.Rollback()
				t.Fatal(err)
			}
			if _, err := pending.SealForReceipt(ctx, otherTx, proof, receiptCrypto, binding, receiptExpiry); !errors.Is(err, ErrUnavailable) {
				_ = otherTx.Rollback()
				_ = tx.Rollback()
				t.Fatal("approval sealed in a different transaction")
			}
			_ = otherTx.Rollback()
			if _, err := tx.ExecContext(ctx, `UPDATE login_identity SET state = 'revoked' WHERE id = ?`, []byte(pending.identityID)); err != nil {
				_ = tx.Rollback()
				t.Fatal(err)
			}
			if _, err := pending.SealForReceipt(ctx, tx, proof, receiptCrypto, binding, receiptExpiry); !errors.Is(err, ErrUnavailable) {
				_ = tx.Rollback()
				t.Fatal("approval sealed after identity revocation")
			}
			if _, err := tx.ExecContext(ctx, `UPDATE login_identity SET state = 'active' WHERE id = ?`, []byte(pending.identityID)); err != nil {
				_ = tx.Rollback()
				t.Fatal(err)
			}
			if _, err := tx.ExecContext(ctx, `UPDATE auth_login_approval SET state = 'approved', decision_session_id = ?, decided_at = UTC_TIMESTAMP(6) WHERE id = ?`, []byte(claims.SessionID), []byte(pending.data.ID)); err != nil {
				_ = tx.Rollback()
				t.Fatal(err)
			}
			if _, err := pending.SealForReceipt(ctx, tx, proof, receiptCrypto, binding, receiptExpiry); !errors.Is(err, ErrUnavailable) {
				_ = tx.Rollback()
				t.Fatal("already-decided approval was sealed as a newly pending result")
			}
			if _, err := tx.ExecContext(ctx, `UPDATE auth_login_approval SET state = 'pending', decision_session_id = NULL, decided_at = NULL WHERE id = ?`, []byte(pending.data.ID)); err != nil {
				_ = tx.Rollback()
				t.Fatal(err)
			}
			capped, err := pending.SealForReceipt(ctx, tx, proof, receiptCrypto, binding, time.Now().UTC().Add(2*time.Hour).Truncate(time.Microsecond))
			if err != nil || !capped.ExpiresAt().Equal(pending.data.ExpiresAt) {
				_ = tx.Rollback()
				t.Fatal("approval receipt lifetime exceeded the approval lifetime")
			}
			sealed, err = pending.SealForReceipt(ctx, tx, proof, receiptCrypto, binding, receiptExpiry)
			if err != nil || sealed.Reference() != pending.data.ID || !sealed.ExpiresAt().Equal(receiptExpiry) || bytes.Contains(sealed.Ciphertext().Bytes(), []byte(pending.data.Secret)) {
				_ = tx.Rollback()
				t.Fatal("approval receipt was not encrypted or bound to its approval")
			}
			if _, err := json.Marshal(sealed); err == nil || strings.Contains(fmt.Sprintf("%+v", sealed), pending.data.Secret) {
				_ = tx.Rollback()
				t.Fatal("approval receipt formatting exposed the secret")
			}
		}
		if _, err := pending.AfterCommit(ctx, db); err == nil {
			_ = tx.Rollback()
			t.Fatal("uncommitted approval released polling secret")
		}
		if err := tx.Commit(); err != nil {
			t.Fatal("could not commit approval")
		}
		data, err := pending.AfterCommit(ctx, db)
		if err != nil || data.ID == "" || len(data.Secret) < 40 || len(data.Nonce) < 40 || len(data.ComparisonCode) != 6 {
			t.Fatal("committed approval did not release expected data")
		}
		if testReceipt {
			verifyTx, err := db.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := challenges.VerifyReceiptProof(ctx, verifyTx, pending.scope, id, signed); err != nil || pending.validatePending(ctx, verifyTx, false) != nil {
				_ = verifyTx.Rollback()
				t.Fatal("could not revalidate proof and approval before test decryption")
			}
			aad, err := receiptCrypto.NewContext(pending.scope[:], binding, loginreceipt.ResultApprovalRequired, sealed.Reference(), receiptExpiry)
			if err != nil {
				_ = verifyTx.Rollback()
				t.Fatal(err)
			}
			plain, err := receiptCrypto.Open(aad, sealed.Ciphertext())
			var payload receiptPayload
			if err != nil || json.Unmarshal(plain.Bytes(), &payload) != nil || payload.Version != 1 || payload.Secret != data.Secret ||
				payload.Nonce != data.Nonce || payload.ComparisonCode != data.ComparisonCode || !payload.ExpiresAt.Equal(data.ExpiresAt) {
				_ = verifyTx.Rollback()
				t.Fatal("encrypted approval receipt did not preserve committed data")
			}
			_ = verifyTx.Rollback()
			testReceipt = false
		}
		return pending, data
	}
	// A synthetic linked identity/session exercises subject-wide listing without
	// relying on a registered external WeChat account or provider integration.
	var ownerID uint64
	var deviceID []byte
	expectedOwnerID, ownerErr := strconv.ParseUint(claims.SubjectID, 10, 64)
	if err := db.QueryRowContext(ctx, `SELECT user_id, device_id FROM auth_session WHERE id=?`, []byte(claims.SessionID)).
		Scan(&ownerID, &deviceID); err != nil || ownerErr != nil || ownerID != expectedOwnerID {
		t.Fatal("could not load isolated session owner")
	}
	var emailIdentityID []byte
	if err := db.QueryRowContext(ctx, `SELECT id FROM login_identity WHERE user_id=? AND kind='email' AND state='active'`, ownerID).Scan(&emailIdentityID); err != nil {
		t.Fatal("could not load isolated email identity")
	}
	wechatIdentityID, _ := domain.GenerateChallengeID()
	wechatSessionID, _ := domain.GenerateChallengeID()
	wechatChallengeID, _ := domain.GenerateChallengeID()
	linkTx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal("could not begin synthetic linked identity fixture")
	}
	defer linkTx.Rollback()
	linkNow := time.Now().UTC().Truncate(time.Microsecond)
	if _, err := linkTx.ExecContext(ctx, `INSERT INTO login_identity
		(id,user_id,kind,provider_scope,subject_enc,subject_lookup_hmac,key_id,state,verified_at,created_at,updated_at)
		VALUES (?,?, 'wechat_mini','synthetic-test',?,?,?,'active',?,?,?)`, []byte(wechatIdentityID), ownerID,
		[]byte{1, 2, 3}, bytes.Repeat([]byte{18}, 32), []byte("synthetic-key"), timestamp(linkNow), timestamp(linkNow), timestamp(linkNow)); err != nil {
		_ = linkTx.Rollback()
		t.Fatal("could not insert synthetic linked identity")
	}
	if _, err := linkTx.ExecContext(ctx, `UPDATE user SET identity_version=identity_version+1 WHERE id=?`, ownerID); err != nil {
		_ = linkTx.Rollback()
		t.Fatal("could not advance identity version for synthetic link")
	}
	wechatToken, err := codec.Issue(claims.SubjectID, wechatSessionID, claims.TokenVersion)
	if err != nil {
		_ = linkTx.Rollback()
		t.Fatal("could not issue synthetic linked session token")
	}
	wechatClaims, err := codec.Verify(wechatToken.ForResponse())
	if err != nil {
		_ = linkTx.Rollback()
		t.Fatal("could not verify synthetic linked session token")
	}
	if _, err := linkTx.ExecContext(ctx, `INSERT INTO auth_session
		(id,user_id,identity_id,device_id,login_challenge_id,token_version,state,authenticated_at,expires_at,created_at)
		VALUES (?,?,?,?,?,?,'active',?,?,?)`, []byte(wechatSessionID), ownerID, []byte(wechatIdentityID), deviceID,
		[]byte(wechatChallengeID), wechatClaims.TokenVersion, timestamp(wechatClaims.IssuedAt), timestamp(wechatClaims.ExpiresAt), timestamp(linkNow)); err != nil {
		_ = linkTx.Rollback()
		t.Fatal("could not insert synthetic linked session")
	}
	if err := linkTx.Commit(); err != nil {
		t.Fatal("could not commit synthetic linked identity fixture")
	}
	pending, data := begin()
	listFor := func(credential string) (PendingList, error) {
		t.Helper()
		return store.ListPending(ctx, db, auth, credential)
	}
	listed, err := listFor(token)
	if err != nil || len(listed.Items) != 1 || listed.Items[0].ID != data.ID || listed.Items[0].ComparisonCode != data.ComparisonCode ||
		!listed.Items[0].ExpiresAt.Equal(data.ExpiresAt) || listed.Items[0].CreatedAt.IsZero() {
		t.Fatalf("authenticated subject could not read pending summary: err=%v count=%d idMatch=%t codeMatch=%t expiryMatch=%t created=%t",
			err, len(listed.Items), len(listed.Items) == 1 && listed.Items[0].ID == data.ID,
			len(listed.Items) == 1 && listed.Items[0].ComparisonCode == data.ComparisonCode,
			len(listed.Items) == 1 && listed.Items[0].ExpiresAt.Equal(data.ExpiresAt),
			len(listed.Items) == 1 && !listed.Items[0].CreatedAt.IsZero())
	}
	if !validApprovalOpaqueID(listed.Items[0].ID) || validApprovalOpaqueID(strings.Repeat("a", 21)+"/") {
		t.Fatal("approval list ID validation did not match AuthOpaqueId constraints")
	}
	wechatList, err := listFor(wechatToken.ForResponse())
	if err != nil || len(wechatList.Items) != 1 || wechatList.Items[0].ID != data.ID {
		t.Fatal("synthetic same-subject session with a different identity could not list pending item")
	}
	if _, err := db.ExecContext(ctx, `UPDATE user SET identity_version=identity_version+1 WHERE id=?`, ownerID); err != nil {
		t.Fatal("could not stage identity-version invalidation")
	}
	if invalidated, err := listFor(wechatToken.ForResponse()); err != nil || len(invalidated.Items) != 0 {
		t.Fatal("pending approval survived an identity-version change")
	}
	if _, err := db.ExecContext(ctx, `UPDATE user SET identity_version=identity_version-1 WHERE id=?`, ownerID); err != nil {
		t.Fatal("could not restore identity version")
	}
	// Model ResolveEmail's identity -> user lock order while the list principal
	// already holds the other identity -> user. The list must not lock the email
	// identity after acquiring the shared user row.
	crossIdentityListTx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal("could not begin cross-identity lock-order check")
	}
	crossIdentityPrincipal, err := auth.Authenticate(ctx, crossIdentityListTx, wechatToken.ForResponse())
	if err != nil {
		_ = crossIdentityListTx.Rollback()
		t.Fatal("could not authenticate synthetic linked session")
	}
	emailIdentityTx, err := db.BeginTx(ctx, nil)
	if err != nil {
		_ = crossIdentityListTx.Rollback()
		t.Fatal("could not begin email identity lock fixture")
	}
	var lockedEmailIdentity []byte
	if err := emailIdentityTx.QueryRowContext(ctx, `SELECT id FROM login_identity WHERE id=? FOR UPDATE`, emailIdentityID).Scan(&lockedEmailIdentity); err != nil {
		_ = emailIdentityTx.Rollback()
		_ = crossIdentityListTx.Rollback()
		t.Fatal("could not acquire ResolveEmail-order identity lock")
	}
	userLockStarted := make(chan struct{})
	userLockDone := make(chan error, 1)
	go func() {
		close(userLockStarted)
		var lockedUser uint64
		userLockDone <- emailIdentityTx.QueryRowContext(ctx, `SELECT id FROM user WHERE id=? FOR UPDATE`, ownerID).Scan(&lockedUser)
	}()
	<-userLockStarted
	select {
	case err := <-userLockDone:
		_ = emailIdentityTx.Rollback()
		_ = crossIdentityListTx.Rollback()
		if err != nil {
			t.Fatal("email identity transaction failed before list lock check")
		}
		t.Fatal("email identity fixture did not wait for the shared user lock")
	case <-time.After(100 * time.Millisecond):
	}
	crossListDone := make(chan error, 1)
	go func() {
		_, err := store.readPendingInTx(ctx, crossIdentityListTx, crossIdentityPrincipal)
		crossListDone <- err
	}()
	select {
	case err := <-crossListDone:
		if err != nil {
			_ = crossIdentityListTx.Rollback()
			_ = emailIdentityTx.Rollback()
			t.Fatal("cross-identity list deadlocked or failed while email identity waited on user")
		}
	case <-time.After(3 * time.Second):
		_ = crossIdentityListTx.Rollback()
		_ = emailIdentityTx.Rollback()
		t.Fatal("cross-identity list waited on the other identity lock")
	}
	if err := crossIdentityListTx.Commit(); err != nil {
		_ = emailIdentityTx.Rollback()
		t.Fatal("could not commit cross-identity list lock check")
	}
	select {
	case err := <-userLockDone:
		if err != nil {
			_ = emailIdentityTx.Rollback()
			t.Fatal("email identity transaction did not continue after list commit")
		}
	case <-time.After(3 * time.Second):
		_ = emailIdentityTx.Rollback()
		t.Fatal("email identity transaction stayed blocked after list commit")
	}
	if err := emailIdentityTx.Commit(); err != nil {
		t.Fatal("could not commit email identity lock fixture")
	}
	otherList, err := listFor(otherToken)
	if err != nil || len(otherList.Items) != 0 {
		t.Fatal("pending approval list crossed subject boundaries")
	}
	encodedList, err := json.Marshal(listed)
	if err != nil || !bytes.Contains(encodedList, []byte(`"comparisonCode"`)) || bytes.Contains(encodedList, []byte(data.Secret)) ||
		bytes.Contains(encodedList, []byte(data.Nonce)) || bytes.Contains(encodedList, []byte(`"secret"`)) || bytes.Contains(encodedList, []byte(`"nonce"`)) {
		t.Fatal("pending approval list exposed fields outside its bounded contract")
	}
	lockedListTx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal("could not begin concurrent list check")
	}
	lockedPrincipal, err := auth.Authenticate(ctx, lockedListTx, token)
	if err != nil {
		_ = lockedListTx.Rollback()
		t.Fatal("could not authenticate concurrent list check")
	}
	if _, err := store.readPendingInTx(ctx, lockedListTx, lockedPrincipal); err != nil {
		_ = lockedListTx.Rollback()
		t.Fatal("could not lock pending row for list snapshot")
	}
	updateStarted := make(chan struct{})
	updateDone := make(chan error, 1)
	go func() {
		close(updateStarted)
		_, err := db.ExecContext(ctx, `UPDATE auth_login_approval SET state='denied', decision_session_id=?, decided_at=UTC_TIMESTAMP(6) WHERE id=?`,
			[]byte(claims.SessionID), []byte(data.ID))
		updateDone <- err
	}()
	<-updateStarted
	select {
	case <-updateDone:
		_ = lockedListTx.Rollback()
		t.Fatal("approval mutation did not wait for the list transaction lock")
	case <-time.After(100 * time.Millisecond):
	}
	if err := lockedListTx.Commit(); err != nil {
		t.Fatal("could not commit list snapshot")
	}
	select {
	case err := <-updateDone:
		if err != nil {
			t.Fatal("concurrent approval mutation failed after list commit")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("concurrent approval mutation remained blocked after list commit")
	}
	if afterDecision, err := listFor(token); err != nil || len(afterDecision.Items) != 0 {
		t.Fatal("decided approval remained visible in a fresh list")
	}
	if _, err := db.ExecContext(ctx, `UPDATE auth_login_approval SET state='pending', decision_session_id=NULL, decided_at=NULL, expires_at=? WHERE id=?`,
		timestamp(data.ExpiresAt), []byte(data.ID)); err != nil {
		t.Fatal("could not restore synthetic pending approval")
	}
	if _, err := db.ExecContext(ctx, `UPDATE auth_login_approval SET created_at=UTC_TIMESTAMP(6)-INTERVAL 1 HOUR,
		expires_at=UTC_TIMESTAMP(6)-INTERVAL 1 SECOND WHERE id=?`, []byte(data.ID)); err != nil {
		t.Fatal("could not stage expired list item")
	}
	if expiredList, err := listFor(token); err != nil || len(expiredList.Items) != 0 {
		t.Fatal("expired approval was returned by pending list")
	}
	if _, err := db.ExecContext(ctx, `UPDATE auth_login_approval SET created_at=?, expires_at=? WHERE id=?`,
		timestamp(listed.Items[0].CreatedAt), timestamp(data.ExpiresAt), []byte(data.ID)); err != nil {
		t.Fatal("could not restore pending approval expiry")
	}
	if _, err := db.ExecContext(ctx, `UPDATE auth_session SET state='revoked', revoked_at=UTC_TIMESTAMP(6) WHERE id=?`, []byte(claims.SessionID)); err != nil {
		t.Fatal("could not stage revoked list session")
	}
	if _, err := listFor(token); err == nil {
		t.Fatal("revoked session read a pending approval list")
	}
	if _, err := db.ExecContext(ctx, `UPDATE auth_session SET state='active', revoked_at=NULL WHERE id=?`, []byte(claims.SessionID)); err != nil {
		t.Fatal("could not restore isolated list session")
	}
	store.now = func() time.Time { return time.Now().Add(6 * time.Minute) }
	if ahead, err := listFor(token); err != nil || len(ahead.Items) != 0 {
		store.now = time.Now
		t.Fatal("list did not use the approval application clock when it was ahead of database time")
	}
	if _, err := db.ExecContext(ctx, `UPDATE auth_login_approval SET created_at=UTC_TIMESTAMP(6)-INTERVAL 1 HOUR,
		expires_at=UTC_TIMESTAMP(6)-INTERVAL 30 SECOND WHERE id=?`, []byte(data.ID)); err != nil {
		store.now = time.Now
		t.Fatal("could not stage database-expired item for clock-skew check")
	}
	store.now = func() time.Time { return time.Now().Add(-time.Minute) }
	behind, err := listFor(token)
	if err != nil || len(behind.Items) != 1 || behind.Items[0].ID != data.ID {
		store.now = time.Now
		t.Fatal("list did not match BeginEmail/Decide clock semantics when application time lagged database time")
	}
	store.now = time.Now
	if _, err := db.ExecContext(ctx, `UPDATE auth_login_approval SET created_at=?, expires_at=? WHERE id=?`,
		timestamp(listed.Items[0].CreatedAt), timestamp(data.ExpiresAt), []byte(data.ID)); err != nil {
		t.Fatal("could not restore pending approval after clock-skew check")
	}
	// The internal cap is deliberately fail-closed: cap+1 is detected without
	// returning any prefix as if it were the complete pending list.
	baseTime := time.Now().UTC().Truncate(time.Microsecond)
	for i := 0; i < maxPendingListItems; i++ {
		id, idErr := domain.GenerateChallengeID()
		challengeID, challengeErr := domain.GenerateChallengeID()
		if idErr != nil || challengeErr != nil {
			t.Fatal("could not generate isolated list-cap identifiers")
		}
		created := timestamp(baseTime.Add(time.Duration(i+1) * time.Microsecond))
		expires := timestamp(baseTime.Add(5 * time.Minute))
		if _, err := db.ExecContext(ctx, `INSERT INTO auth_login_approval
			(id,user_id,identity_id,challenge_id,token_version,identity_version,target_fingerprint,public_x,public_y,
			 secret_digest,key_id,nonce,comparison_code,state,created_at,expires_at)
			VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,'pending',?,?)`, []byte(id), claims.SubjectID, []byte(pending.identityID), []byte(challengeID),
			pending.tokenVersion, pending.identityVersion, bytes.Repeat([]byte{13}, 32),
			strings.Repeat("A", 43), strings.Repeat("B", 43), bytes.Repeat([]byte{14}, 32), []byte(store.keyID),
			[]byte(strings.Repeat("C", 43)), fmt.Sprintf("%06d", i), created, expires); err != nil {
			t.Fatal("could not seed bounded approval list")
		}
	}
	if overfull, err := listFor(token); !errors.Is(err, ErrUnavailable) || len(overfull.Items) != 0 {
		t.Fatal("overfull pending list returned partial data")
	}
	if _, err := db.ExecContext(ctx, `UPDATE login_identity SET subject_lookup_hmac = ? WHERE id = ?`, bytes.Repeat([]byte{11}, 32), []byte(pending.identityID)); err != nil {
		t.Fatal(err)
	}
	if _, err := pending.AfterCommit(ctx, db); !errors.Is(err, ErrUnavailable) {
		t.Fatal("approval secret released after the email identity changed")
	}
	if _, err := db.ExecContext(ctx, `UPDATE login_identity SET subject_lookup_hmac = ? WHERE id = ?`, pending.scope[:], []byte(pending.identityID)); err != nil {
		t.Fatal(err)
	}
	var secretDigest []byte
	if err := db.QueryRowContext(ctx, `SELECT secret_digest FROM auth_login_approval WHERE id = ?`, []byte(data.ID)).Scan(&secretDigest); err != nil ||
		len(secretDigest) != sha256.Size || !hmac.Equal(secretDigest, store.digest(data.ID, data.Secret)) || bytes.Equal(secretDigest, []byte(data.Secret)) {
		t.Fatal("polling secret was stored directly")
	}
	for _, value := range []any{store, *store, pending, &pending, data, &data} {
		if _, err := json.Marshal(value); err == nil {
			t.Fatal("approval value was JSON-serializable")
		}
		var log bytes.Buffer
		slog.New(slog.NewJSONHandler(&log, nil)).Info("test", "value", value)
		var entry map[string]any
		if json.Unmarshal(log.Bytes(), &entry) != nil {
			t.Fatal("could not parse test log")
		}
		output := fmt.Sprintf("%+v %#v %v", value, value, entry["value"])
		for _, sensitive := range []string{data.Secret, data.Nonce, data.ComparisonCode, data.ID} {
			if strings.Contains(output, sensitive) {
				t.Fatal("approval data leaked into output")
			}
		}
	}
	decide := func(credential string, item Data, decision Decision, code string, confirmed bool) error {
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer tx.Rollback()
		principal, err := auth.Authenticate(ctx, tx, credential)
		if err != nil {
			return err
		}
		if err := store.Decide(ctx, tx, principal, item.ID, decision, code, confirmed); err != nil {
			return err
		}
		return tx.Commit()
	}
	if err := decide(otherToken, data, Approve, data.ComparisonCode, true); !errors.Is(err, ErrUnavailable) {
		t.Fatal("other subject approved an unrelated device")
	}
	wrongCode := "000000"
	if wrongCode == data.ComparisonCode {
		wrongCode = "999999"
	}
	if err := decide(token, data, Approve, wrongCode, true); !errors.Is(err, ErrUnavailable) {
		t.Fatal("wrong comparison code was accepted")
	}
	if err := decide(token, data, Approve, data.ComparisonCode, false); !errors.Is(err, ErrUnavailable) {
		t.Fatal("approval without explicit confirmation succeeded")
	}
	if err := decide(token, data, Approve, data.ComparisonCode, true); err != nil {
		t.Fatal("owner could not approve new device")
	}
	var state string
	if err := db.QueryRowContext(ctx, `SELECT state FROM auth_login_approval WHERE id = ?`, []byte(data.ID)).Scan(&state); err != nil || state != "approved" {
		t.Fatal("approval decision was not persisted")
	}
	var count int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM trusted_device WHERE user_id = ?`, claims.SubjectID).Scan(&count); err != nil || count != 1 {
		t.Fatal("approval alone registered the new device")
	}
	// One original email session plus the synthetic linked-identity session
	// above; deciding an approval must not issue an additional session.
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM auth_session WHERE user_id = ?`, claims.SubjectID).Scan(&count); err != nil || count != 2 {
		t.Fatal("approval alone issued another session")
	}
	if err := decide(token, data, Deny, "", false); !errors.Is(err, ErrUnavailable) {
		t.Fatal("terminal approval was changed")
	}
	_, denied := begin()
	if err := decide(token, denied, Deny, "", false); err != nil {
		t.Fatal("owner could not deny an approval without a code")
	}
	if err := db.QueryRowContext(ctx, `SELECT state FROM auth_login_approval WHERE id = ?`, []byte(denied.ID)).Scan(&state); err != nil || state != "denied" {
		t.Fatal("denial did not persist")
	}
	_, rollbackDecision := begin()
	decisionTx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal("could not begin decision rollback check")
	}
	decisionPrincipal, err := auth.Authenticate(ctx, decisionTx, token)
	if err != nil || store.Decide(ctx, decisionTx, decisionPrincipal, rollbackDecision.ID, Approve, rollbackDecision.ComparisonCode, true) != nil {
		_ = decisionTx.Rollback()
		t.Fatal("could not stage approval decision")
	}
	if err := decisionTx.Rollback(); err != nil {
		t.Fatal("could not roll back decision")
	}
	if err := db.QueryRowContext(ctx, `SELECT state FROM auth_login_approval WHERE id = ?`, []byte(rollbackDecision.ID)).Scan(&state); err != nil || state != "pending" {
		t.Fatal("rolled-back decision remained approved")
	}
	_, stale := begin()
	store.now = func() time.Time { return time.Now().Add(2 * time.Minute) }
	if err := decide(token, stale, Approve, stale.ComparisonCode, true); !errors.Is(err, ErrUnavailable) {
		t.Fatal("stale authentication approved a device")
	}
	store.now = time.Now
	if err := decide(token, stale, Approve, stale.ComparisonCode, true); err != nil {
		t.Fatal("recent authentication could not approve")
	}
	expiringPending, expiring := begin()
	store.now = func() time.Time { return expiring.ExpiresAt }
	if err := decide(token, expiring, Approve, expiring.ComparisonCode, true); !errors.Is(err, ErrUnavailable) {
		t.Fatal("expired approval was accepted")
	}
	if _, err := expiringPending.AfterCommit(ctx, db); err == nil {
		t.Fatal("expired approval released a polling secret")
	}
	store.now = time.Now
	versionPending, _ := begin()
	if _, err := db.ExecContext(ctx, `UPDATE user SET token_version = token_version + 1 WHERE id = ?`, claims.SubjectID); err != nil {
		t.Fatal("could not change isolated token version")
	}
	if _, err := versionPending.AfterCommit(ctx, db); err == nil {
		t.Fatal("stale account version released an approval secret")
	}
	if _, err := db.ExecContext(ctx, `UPDATE user SET token_version = token_version - 1 WHERE id = ?`, claims.SubjectID); err != nil {
		t.Fatal("could not restore isolated token version")
	}
	_, revoked := begin()
	changes := []struct {
		change  string
		restore string
		args    []any
	}{
		{`UPDATE user SET status = 'disabled' WHERE id = ?`, `UPDATE user SET status = 'active' WHERE id = ?`, []any{claims.SubjectID}},
		{`UPDATE login_identity SET state = 'revoked' WHERE user_id = ?`, `UPDATE login_identity SET state = 'active' WHERE user_id = ?`, []any{claims.SubjectID}},
		{`UPDATE trusted_device SET state = 'revoked' WHERE user_id = ?`, `UPDATE trusted_device SET state = 'active' WHERE user_id = ?`, []any{claims.SubjectID}},
		{`UPDATE auth_session SET state = 'revoked', revoked_at = UTC_TIMESTAMP(6) WHERE id = ?`, `UPDATE auth_session SET state = 'active', revoked_at = NULL WHERE id = ?`, []any{[]byte(claims.SessionID)}},
	}
	for _, item := range changes {
		if _, err := db.ExecContext(ctx, item.change, item.args...); err != nil {
			t.Fatal("could not change isolated approval authority")
		}
		if err := decide(token, revoked, Approve, revoked.ComparisonCode, true); err == nil {
			t.Fatal("revoked authority approved a device")
		}
		if _, err := db.ExecContext(ctx, item.restore, item.args...); err != nil {
			t.Fatal("could not restore isolated approval authority")
		}
	}
	if err := decide(token, revoked, Approve, revoked.ComparisonCode, true); err != nil {
		t.Fatal("restored authority could not approve")
	}
	knownID, knownSigned := issueChallenge("first@example.test", firstKey, firstPublic)
	knownTx, knownProof := beginVerified(knownID, knownSigned)
	if _, err := store.BeginEmail(ctx, knownTx, knownProof); !errors.Is(err, ErrUnavailable) {
		_ = knownTx.Rollback()
		t.Fatal("trusted device was treated as a new-device approval")
	}
	_ = knownTx.Rollback()

	// The second creation from one verified challenge fails; both writes are
	// rolled back and the first uncommitted result cannot release its secret.
	id, signed := issueChallenge("first@example.test", newKey, newPublic)
	reuseTx, proof := beginVerified(id, signed)
	first, err := store.BeginEmail(ctx, reuseTx, proof)
	if err != nil {
		_ = reuseTx.Rollback()
		t.Fatal("could not prepare repeated approval")
	}
	rollbackBinding, err := receiptCrypto.Summarize(loginreceipt.Request{IdempotencyKey: "AgICAgICAgICAgICAgICAg", ChallengeID: id, Code: "012345", DeviceProof: signed})
	if err != nil {
		_ = reuseTx.Rollback()
		t.Fatal(err)
	}
	rollbackExpiry := time.Now().UTC().Add(2 * time.Minute).Truncate(time.Microsecond)
	if _, err := first.SealForReceipt(ctx, reuseTx, proof, receiptCrypto, rollbackBinding, rollbackExpiry); err != nil {
		_ = reuseTx.Rollback()
		t.Fatal("could not seal rollback approval fixture")
	}
	if _, err := store.BeginEmail(ctx, reuseTx, proof); !errors.Is(err, ErrUnavailable) {
		_ = reuseTx.Rollback()
		t.Fatal("one challenge created multiple approvals")
	}
	_ = reuseTx.Rollback()
	if _, err := first.SealForReceipt(ctx, reuseTx, proof, receiptCrypto, rollbackBinding, rollbackExpiry); !errors.Is(err, ErrUnavailable) {
		t.Fatal("ended transaction could still seal an approval")
	}
	if _, err := first.AfterCommit(ctx, db); err == nil {
		t.Fatal("rolled-back approval released its secret")
	}

	_, raced := begin()
	gate, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal("could not start approval race")
	}
	var identityID []byte
	if err := gate.QueryRowContext(ctx, `SELECT id FROM login_identity WHERE user_id = ? AND state = 'active' FOR UPDATE`, claims.SubjectID).Scan(&identityID); err != nil {
		_ = gate.Rollback()
		t.Fatal("could not lock isolated identity")
	}
	results := make(chan error, 2)
	go func() { results <- decide(token, raced, Approve, raced.ComparisonCode, true) }()
	go func() { results <- decide(token, raced, Deny, "", false) }()
	deadline := time.Now().Add(5 * time.Second)
	blocked := 0
	for time.Now().Before(deadline) {
		if err := db.QueryRowContext(ctx, `SELECT COUNT(DISTINCT w.REQUESTING_ENGINE_TRANSACTION_ID)
            FROM performance_schema.data_lock_waits w JOIN performance_schema.data_locks l ON l.ENGINE_LOCK_ID = w.REQUESTING_ENGINE_LOCK_ID
            WHERE l.OBJECT_SCHEMA = DATABASE() AND l.OBJECT_NAME = 'login_identity'`).Scan(&blocked); err != nil {
			_ = gate.Rollback()
			t.Fatal("could not observe approval race")
		}
		if blocked >= 2 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if blocked < 2 {
		_ = gate.Rollback()
		t.Fatal("both approval decisions did not reach the lock")
	}
	if err := gate.Rollback(); err != nil {
		t.Fatal("could not release approval gate")
	}
	firstErr, secondErr := <-results, <-results
	if !((firstErr == nil && errors.Is(secondErr, ErrUnavailable)) || (secondErr == nil && errors.Is(firstErr, ErrUnavailable))) {
		t.Fatal("approval race did not reach one terminal decision")
	}
}

func TestApprovalReceiptRecovery(t *testing.T) {
	db := isolatedDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	hasher, _ := domain.NewEmailCodeHasher(bytes.Repeat([]byte{1}, 32))
	replay, _ := repository.NewProofReplayStore(bytes.Repeat([]byte{2}, 32))
	protector, _ := domain.NewEmailIdentityProtector(bytes.Repeat([]byte{3}, 32), bytes.Repeat([]byte{4}, 32), "email-test-key")
	challenges, err := challenge.NewStore(hasher, replay, protector, "fixture-email-code-hash-v1", challenge.Policy{ChallengeTTL: 5 * time.Minute,
		MaxAttempts: 3, DeviceProof: domain.ProofPolicy{MaxLifetime: 5 * time.Minute, ClockSkew: 30 * time.Second}})
	if err != nil {
		t.Fatal(err)
	}
	issuancetestsupport.InitializeEmailCodeHashEpoch(t, db, protector, hasher)
	resolver, _ := identity.NewResolver(protector)
	codec, _ := session.NewTokenCodec(bytes.Repeat([]byte{5}, 32), session.TokenPolicy{MaxLifetime: time.Hour})
	sessions, _ := session.NewStore(resolver, codec)
	store, _ := NewStore(resolver, bytes.Repeat([]byte{6}, 32), "approval-test-key", Policy{Lifetime: 5 * time.Minute, ReauthWindow: time.Minute})
	crypto, err := loginreceipt.New(loginreceipt.KeyConfig{IdempotencyHMACKey: bytes.Repeat([]byte{7}, 32),
		RequestHMACKey: bytes.Repeat([]byte{8}, 32), AES256GCMKey: bytes.Repeat([]byte{9}, 32)})
	if err != nil {
		t.Fatal(err)
	}
	ledger, err := loginledger.NewStore(crypto, protector, "approval-receipt-test")
	if err != nil {
		t.Fatal(err)
	}
	initTx, err := db.BeginTx(ctx, nil)
	if err != nil || store.InitializeKey(ctx, initTx) != nil || ledger.Initialize(ctx, initTx) != nil || initTx.Commit() != nil {
		t.Fatal("isolated approval/receipt initialization failed")
	}
	recoverer, err := NewReceiptRecoverer(store, ledger, challenges, crypto)
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
			t.Fatal("synthetic challenge issue failed")
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
		return id, nonce
	}
	firstKey, firstPublic := testDevice(t)
	const email = "approval-receipt@example.test"
	id, nonce := issue(email, firstPublic)
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	initial, err := challenges.Attempt(ctx, tx, challenge.AttemptInput{ChallengeID: id, Code: "012345", DeviceProof: signedProof(t, firstKey, id, nonce)})
	if err != nil || !initial.Verified || initial.Proof == nil {
		_ = tx.Rollback()
		t.Fatal("initial challenge failed")
	}
	issued, err := sessions.IssueEmail(ctx, tx, initial.Proof)
	if err != nil || tx.Commit() != nil {
		t.Fatal("initial trusted session failed")
	}
	firstToken, err := issued.AfterCommit(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	claims, err := codec.Verify(firstToken)
	if err != nil {
		t.Fatal(err)
	}
	prepare := func(public domain.DevicePublicJWK, private *ecdsa.PrivateKey, key string) (*sql.Tx, ReceiptCandidate, Pending, loginreceipt.Request, [32]byte, loginreceipt.Binding) {
		t.Helper()
		id, nonce := issue(email, public)
		signed := signedProof(t, private, id, nonce)
		request := loginreceipt.Request{IdempotencyKey: key, ChallengeID: id, Code: "012345", DeviceProof: signed}
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
			t.Fatal(err)
		}
		attempt, err := challenges.Attempt(ctx, tx, challenge.AttemptInput{ChallengeID: id, Code: request.Code, DeviceProof: signed})
		if err != nil || !attempt.Verified || attempt.Proof == nil {
			_ = tx.Rollback()
			t.Fatal("new device proof failed")
		}
		pending, err := store.BeginEmail(ctx, tx, attempt.Proof)
		if err != nil {
			_ = tx.Rollback()
			t.Fatal("new device approval setup failed")
		}
		expires := time.Now().UTC().Add(2 * time.Minute).Truncate(time.Microsecond)
		sealed, err := pending.SealForReceipt(ctx, tx, attempt.Proof, crypto, binding, expires)
		if err != nil || ledger.Put(ctx, tx, handle, loginreceipt.ResultApprovalRequired, sealed.Reference(), sealed.ExpiresAt(), sealed.Ciphertext()) != nil {
			_ = tx.Rollback()
			t.Fatal("approval receipt write failed")
		}
		handle, err = ledger.Lock(ctx, tx, scope, binding)
		if err != nil {
			_ = tx.Rollback()
			t.Fatal(err)
		}
		proof, err := challenges.VerifyReceiptProof(ctx, tx, scope, id, signed)
		if err != nil {
			_ = tx.Rollback()
			t.Fatal(err)
		}
		candidate, err := recoverer.Recover(ctx, tx, handle, proof, request)
		if err != nil {
			_ = tx.Rollback()
			t.Fatalf("approval receipt recovery failed: %v", err)
		}
		return tx, candidate, pending, request, scope, binding
	}
	snapshotTx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	var before int
	if err := snapshotTx.QueryRowContext(ctx, `SELECT COUNT(*) FROM auth_login_approval`).Scan(&before); err != nil {
		_ = snapshotTx.Rollback()
		t.Fatal(err)
	}
	newKey, newPublic := testDevice(t)
	tx, candidate, pending, request, scope, binding := prepare(newPublic, newKey, "AQEBAQEBAQEBAQEBAQEBAQ")
	if value, err := candidate.AfterCommit(ctx, db); err == nil || value.Secret != "" {
		_ = tx.Rollback()
		_ = snapshotTx.Rollback()
		t.Fatal("uncommitted approval receipt released a secret")
	}
	if _, err := json.Marshal(candidate); err == nil || strings.Contains(fmt.Sprintf("%+v", candidate), request.DeviceProof) {
		_ = tx.Rollback()
		_ = snapshotTx.Rollback()
		t.Fatal("approval receipt candidate exposed a proof")
	}
	if err := tx.Commit(); err != nil {
		_ = snapshotTx.Rollback()
		t.Fatal(err)
	}
	snapshotHandle, err := ledger.Lock(ctx, snapshotTx, scope, binding)
	if err != nil {
		_ = snapshotTx.Rollback()
		t.Fatal(err)
	}
	snapshotProof, err := challenges.VerifyReceiptProof(ctx, snapshotTx, scope, request.ChallengeID, request.DeviceProof)
	if err != nil {
		_ = snapshotTx.Rollback()
		t.Fatal(err)
	}
	snapshotCandidate, err := recoverer.Recover(ctx, snapshotTx, snapshotHandle, snapshotProof, request)
	if err != nil {
		_ = snapshotTx.Rollback()
		t.Fatal("old snapshot did not observe the committed approval")
	}
	if err := snapshotTx.Commit(); err != nil {
		t.Fatal(err)
	}
	data, err := candidate.AfterCommit(ctx, db)
	if err != nil || data.Secret == "" {
		t.Fatal("committed approval receipt did not recover")
	}
	original, err := pending.AfterCommit(ctx, db)
	if err != nil || original.ID != data.ID || original.Secret != data.Secret || original.Nonce != data.Nonce || original.ComparisonCode != data.ComparisonCode || !original.ExpiresAt.Equal(data.ExpiresAt) {
		t.Fatal("approval receipt did not recover original data")
	}
	if snapshotData, err := snapshotCandidate.AfterCommit(ctx, db); err != nil || snapshotData.Secret != data.Secret {
		t.Fatal("old snapshot did not recover the same approval")
	}
	changes := []struct {
		change, restore string
		args            []any
	}{
		{`UPDATE user SET status='disabled' WHERE id=?`, `UPDATE user SET status='active' WHERE id=?`, []any{claims.SubjectID}},
		{`UPDATE user SET token_version=token_version+1 WHERE id=?`, `UPDATE user SET token_version=token_version-1 WHERE id=?`, []any{claims.SubjectID}},
		{`UPDATE user SET identity_version=identity_version+1 WHERE id=?`, `UPDATE user SET identity_version=identity_version-1 WHERE id=?`, []any{claims.SubjectID}},
		{`UPDATE login_identity SET state='revoked' WHERE id=?`, `UPDATE login_identity SET state='active' WHERE id=?`, []any{[]byte(pending.identityID)}},
	}
	for _, item := range changes {
		if _, err := db.ExecContext(ctx, item.change, item.args...); err != nil {
			t.Fatal(err)
		}
		if value, err := candidate.AfterCommit(ctx, db); !errors.Is(err, ErrInvalidReceipt) || value.Secret != "" {
			t.Fatal("revoked approval receipt released its secret")
		}
		if _, err := db.ExecContext(ctx, item.restore, item.args...); err != nil {
			t.Fatal(err)
		}
	}
	gate, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := gate.ExecContext(ctx, `UPDATE user SET status='disabled' WHERE id=?`, claims.SubjectID); err != nil {
		_ = gate.Rollback()
		t.Fatal(err)
	}
	type approvalResult struct {
		data Data
		err  error
	}
	waiting := make(chan approvalResult, 1)
	go func() {
		value, err := candidate.AfterCommit(ctx, db)
		waiting <- approvalResult{value, err}
	}()
	deadline := time.Now().Add(5 * time.Second)
	blocked := 0
	for time.Now().Before(deadline) {
		if err := db.QueryRowContext(ctx, `SELECT COUNT(DISTINCT w.REQUESTING_ENGINE_TRANSACTION_ID)
			FROM performance_schema.data_lock_waits w JOIN performance_schema.data_locks l ON l.ENGINE_LOCK_ID=w.REQUESTING_ENGINE_LOCK_ID
			WHERE l.OBJECT_SCHEMA=DATABASE() AND l.OBJECT_NAME='user'`).Scan(&blocked); err != nil {
			_ = gate.Rollback()
			t.Fatal("could not observe approval recovery lock wait")
		}
		if blocked > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if blocked == 0 {
		_ = gate.Rollback()
		t.Fatal("approval recovery did not wait on the account lock")
	}
	if err := gate.Commit(); err != nil {
		t.Fatal(err)
	}
	select {
	case result := <-waiting:
		if !errors.Is(result.err, ErrInvalidReceipt) || result.data.Secret != "" {
			t.Fatal("approval recovery ignored account revocation after lock wait")
		}
	case <-ctx.Done():
		t.Fatal("approval recovery did not finish after lock release")
	}
	if _, err := db.ExecContext(ctx, `UPDATE user SET status='active' WHERE id=?`, claims.SubjectID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE auth_login_approval SET state='approved', decision_session_id=?, decided_at=UTC_TIMESTAMP(6) WHERE id=?`, []byte(claims.SessionID), []byte(data.ID)); err != nil {
		t.Fatal(err)
	}
	if value, err := candidate.AfterCommit(ctx, db); err != nil || value.Secret != data.Secret {
		t.Fatal("an approved, still-live request could not recover its original polling secret")
	}
	var firstDevice []byte
	if err := db.QueryRowContext(ctx, `SELECT device_id FROM auth_session WHERE id=?`, []byte(claims.SessionID)).Scan(&firstDevice); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE auth_login_approval SET state='consumed', exchange_device_id=?, exchange_session_id=?, consumed_at=UTC_TIMESTAMP(6) WHERE id=?`, firstDevice, []byte(claims.SessionID), []byte(data.ID)); err != nil {
		t.Fatal(err)
	}
	if value, err := candidate.AfterCommit(ctx, db); !errors.Is(err, ErrInvalidReceipt) || value.Secret != "" {
		t.Fatal("consumed approval recovered its old polling secret")
	}
	if _, err := db.ExecContext(ctx, `UPDATE auth_login_approval SET state='pending', decision_session_id=NULL, decided_at=NULL, exchange_device_id=NULL, exchange_session_id=NULL, consumed_at=NULL WHERE id=?`, []byte(data.ID)); err != nil {
		t.Fatal(err)
	}
	fingerprint, _ := domain.DeviceKeyFingerprint(newPublic)
	if _, err := db.ExecContext(ctx, `INSERT INTO trusted_device (id,user_id,key_fingerprint,public_x,public_y,state,created_at) VALUES (?,?,?,?,?,'revoked',UTC_TIMESTAMP(6))`,
		[]byte("revoked_target_0123456789"), claims.SubjectID, fingerprint[:], newPublic.X, newPublic.Y); err != nil {
		t.Fatal(err)
	}
	if value, err := candidate.AfterCommit(ctx, db); !errors.Is(err, ErrInvalidReceipt) || value.Secret != "" {
		t.Fatal("revoked target device recovered an approval secret")
	}
	if _, err := db.ExecContext(ctx, `DELETE FROM trusted_device WHERE key_fingerprint=?`, fingerprint[:]); err != nil {
		t.Fatal(err)
	}
	proofExpiredAt := time.Now().UTC().Add(95 * time.Second)
	observedAdvance := false
	for advanceAt := int32(1); advanceAt <= 12; advanceAt++ {
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
			if err == nil || value.Secret != "" {
				store.now = time.Now
				t.Fatal("expired device proof released an approval secret")
			}
		}
	}
	store.now = time.Now
	if !observedAdvance {
		t.Fatal("proof expiry clock was not exercised")
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
	changed := request
	changed.Code = "999999"
	if _, err := recoverer.Recover(ctx, verifyTx, handle, proof, changed); !errors.Is(err, ErrInvalidReceipt) {
		_ = verifyTx.Rollback()
		t.Fatal("changed request recovered an approval receipt")
	}
	otherTx, err := db.BeginTx(ctx, nil)
	if err != nil {
		_ = verifyTx.Rollback()
		t.Fatal(err)
	}
	if _, err := recoverer.Recover(ctx, otherTx, handle, proof, request); !errors.Is(err, ErrUnavailable) {
		_ = otherTx.Rollback()
		_ = verifyTx.Rollback()
		t.Fatal("cross-transaction approval recovery succeeded")
	}
	_ = otherTx.Rollback()
	keyDigest := binding.IdempotencyDigest()
	if _, err := verifyTx.ExecContext(ctx, `UPDATE auth_email_login_receipt SET created_at=DATE_SUB(UTC_TIMESTAMP(6), INTERVAL 2 MINUTE), expires_at=DATE_SUB(UTC_TIMESTAMP(6), INTERVAL 1 MINUTE) WHERE email_scope=? AND request_key_hmac=?`, scope[:], keyDigest[:]); err != nil {
		_ = verifyTx.Rollback()
		t.Fatal(err)
	}
	if _, err := recoverer.Recover(ctx, verifyTx, handle, proof, request); !errors.Is(err, ErrInvalidReceipt) {
		_ = verifyTx.Rollback()
		t.Fatal("expired approval receipt recovered a secret")
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
	if _, err := recoverer.Recover(canceled, cancelTx, cancelHandle, cancelProof, request); !errors.Is(err, ErrUnavailable) {
		_ = cancelTx.Rollback()
		t.Fatal("approval storage failure was classified as invalid authorization")
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
	if value, err := candidate.AfterCommit(ctx, db); err == nil || value.Secret != "" {
		t.Fatal("tampered approval ciphertext released its secret")
	}
	if _, err := db.ExecContext(ctx, `UPDATE auth_email_login_receipt SET response_enc=? WHERE email_scope=? AND request_key_hmac=?`, ciphertext, scope[:], keyDigest[:]); err != nil {
		t.Fatal(err)
	}
	rollbackKey, rollbackPublic := testDevice(t)
	rolledTx, rolledCandidate, _, _, _, _ := prepare(rollbackPublic, rollbackKey, "AgICAgICAgICAgICAgICAg")
	if err := rolledTx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if value, err := rolledCandidate.AfterCommit(ctx, db); err == nil || value.Secret != "" {
		t.Fatal("rolled-back approval receipt released a secret")
	}
}
