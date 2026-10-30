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
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"

	authbootstraptest "nerae/api/internal/modules/auth/bootstrap/testsupport"
	"nerae/api/internal/modules/auth/challenge"
	"nerae/api/internal/modules/auth/domain"
	"nerae/api/internal/modules/auth/identity"
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
	challenges, err := challenge.NewStore(hasher, replay, emailProtector, challenge.Policy{ChallengeTTL: 5 * time.Minute,
		MaxAttempts: 3, DeviceProof: domain.ProofPolicy{MaxLifetime: 5 * time.Minute, ClockSkew: 30 * time.Second}})
	if err != nil {
		t.Fatal("could not initialize challenges")
	}
	authbootstraptest.InitializeIdentityEpoch(t, db, emailProtector)
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
			sealed, err = pending.SealForReceipt(ctx, tx, proof, receiptCrypto, binding, receiptExpiry)
			if err != nil || sealed.Reference() != pending.data.ID || bytes.Contains(sealed.Ciphertext().Bytes(), []byte(pending.data.Secret)) {
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
	pending, data := begin()
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
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM auth_session WHERE user_id = ?`, claims.SubjectID).Scan(&count); err != nil || count != 1 {
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
