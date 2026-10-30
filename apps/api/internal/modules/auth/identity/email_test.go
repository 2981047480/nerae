package identity

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
	"sync"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"

	authbootstraptest "nerae/api/internal/modules/auth/bootstrap/testsupport"
	"nerae/api/internal/modules/auth/challenge"
	"nerae/api/internal/modules/auth/domain"
	"nerae/api/internal/modules/auth/repository"
)

func privateDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("NERAE_IDENTITY_TEST_DSN")
	if dsn == "" {
		t.Skip("set NERAE_IDENTITY_TEST_DSN using the isolated MySQL test harness")
	}
	cfg, err := mysql.ParseDSN(dsn)
	if err != nil {
		t.Fatal("invalid isolated test DSN")
	}
	socket := filepath.Clean(cfg.Addr)
	parent := filepath.Dir(socket)
	resolved, err := filepath.EvalSymlinks(parent)
	info, statErr := os.Lstat(parent)
	socketInfo, socketErr := os.Lstat(socket)
	if err != nil || statErr != nil || socketErr != nil || cfg.Net != "unix" || cfg.DBName != "nerae_identity_test" ||
		cfg.User != "root" || cfg.Passwd != "" || !cfg.ParseTime || cfg.Loc != time.UTC ||
		filepath.Base(socket) != "mysql.sock" || filepath.Dir(resolved) != "/private/tmp" ||
		!strings.HasPrefix(filepath.Base(resolved), "nerae-email-identity-mysql-") ||
		!info.IsDir() || info.Mode().Perm()&0077 != 0 || info.Mode()&os.ModeSymlink != 0 || socketInfo.Mode()&os.ModeSocket == 0 {
		t.Fatal("refusing non-isolated MySQL test target")
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatal("cannot open isolated MySQL")
	}
	db.SetMaxOpenConns(4)
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Ping(); err != nil {
		t.Fatal("cannot connect to isolated MySQL")
	}
	return db
}

func privateKey(t *testing.T) (*ecdsa.PrivateKey, domain.DevicePublicJWK) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	encode := base64.RawURLEncoding.EncodeToString
	return key, domain.DevicePublicJWK{Kty: "EC", Crv: "P-256", X: encode(key.X.FillBytes(make([]byte, 32))), Y: encode(key.Y.FillBytes(make([]byte, 32)))}
}

func sign(t *testing.T, key *ecdsa.PrivateKey, id, nonce string) string {
	t.Helper()
	jti, err := domain.GenerateChallengeID()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	payload, err := json.Marshal(map[string]any{"aud": "nerae-auth-device", "purpose": "email_login", "resourceId": id,
		"nonce": nonce, "iat": now.Unix(), "exp": now.Add(time.Minute).Unix(), "jti": jti})
	if err != nil {
		t.Fatal(err)
	}
	encode := base64.RawURLEncoding.EncodeToString
	input := encode([]byte(`{"alg":"ES256","typ":"JWT"}`)) + "." + encode(payload)
	digest := sha256.Sum256([]byte(input))
	r, s, err := ecdsa.Sign(rand.Reader, key, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	return input + "." + encode(append(r.FillBytes(make([]byte, 32)), s.FillBytes(make([]byte, 32))...))
}

func TestEmailIdentityAndTrustedDeviceTransactions(t *testing.T) {
	db := privateDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	hasher, _ := domain.NewEmailCodeHasher(bytes.Repeat([]byte{1}, 32))
	replay, _ := repository.NewProofReplayStore(bytes.Repeat([]byte{2}, 32))
	protector, _ := domain.NewEmailIdentityProtector(bytes.Repeat([]byte{3}, 32), bytes.Repeat([]byte{4}, 32), "test-key")
	store, err := challenge.NewStore(hasher, replay, protector, challenge.Policy{ChallengeTTL: 5 * time.Minute,
		MaxAttempts: 3, DeviceProof: domain.ProofPolicy{MaxLifetime: 5 * time.Minute, ClockSkew: 30 * time.Second}})
	if err != nil {
		t.Fatal(err)
	}
	authbootstraptest.InitializeIdentityEpoch(t, db, protector)
	resolver, err := NewResolver(protector)
	if err != nil {
		t.Fatal(err)
	}
	firstKey, firstPublic := privateKey(t)
	secondKey, secondPublic := privateKey(t)
	issue := func(email string, key *ecdsa.PrivateKey, public domain.DevicePublicJWK) (string, string) {
		t.Helper()
		id, err := domain.GenerateChallengeID()
		if err != nil {
			t.Fatal(err)
		}
		nonce, err := domain.GenerateChallengeNonce()
		if err != nil {
			t.Fatal(err)
		}
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		if err := store.Issue(ctx, tx, challenge.IssueInput{Email: email, ChallengeID: id, Nonce: nonce, Code: "012345", PublicKey: public}); err != nil {
			t.Fatal("could not create test challenge")
		}
		if err := tx.Commit(); err != nil {
			t.Fatal("could not commit test challenge")
		}
		return id, sign(t, key, id, nonce)
	}
	resolve := func(id, signed string, commit bool) (Resolution, error) {
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return Resolution{}, err
		}
		defer tx.Rollback()
		result, err := store.Attempt(ctx, tx, challenge.AttemptInput{ChallengeID: id, Code: "012345", DeviceProof: signed})
		if err != nil || !result.Verified || result.Proof == nil {
			return Resolution{}, ErrIdentityUnavailable
		}
		resolved, err := resolver.ResolveEmail(ctx, tx, result.Proof)
		if err != nil {
			return resolved, err
		}
		if commit {
			err = tx.Commit()
		}
		return resolved, err
	}

	email := "person@example.test"
	id, signed := issue(email, firstKey, firstPublic)
	created, err := resolve(id, signed, true)
	if err != nil || created.State != Created || created.SubjectID == "" || created.DeviceID == "" {
		t.Fatalf("verified first identity not created: %v", err)
	}
	var age sql.NullTime
	var roles int
	if err := db.QueryRowContext(ctx, `SELECT age_verified_at FROM user WHERE id = ?`, created.SubjectID).Scan(&age); err != nil || age.Valid {
		t.Fatal("new identity must not pretend age is verified")
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM user_role WHERE user_id = ?`, created.SubjectID).Scan(&roles); err != nil || roles != 0 {
		t.Fatal("new identity must not silently acquire a role")
	}
	// Verify each database uniqueness constraint independently of the
	// challenge slot. The fixture user and attempted writes are rolled back.
	constraintTx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal("could not begin identity constraint check")
	}
	defer constraintTx.Rollback()
	fixture, err := constraintTx.ExecContext(ctx, `INSERT INTO user (created_at, updated_at) VALUES (UTC_TIMESTAMP(6), UTC_TIMESTAMP(6))`)
	if err != nil {
		t.Fatal("could not create test subject")
	}
	fixtureUser, err := fixture.LastInsertId()
	if err != nil {
		t.Fatal("could not read test subject")
	}
	duplicateID, _ := domain.GenerateChallengeID()
	_, err = constraintTx.ExecContext(ctx, `INSERT INTO login_identity
        (id, user_id, kind, provider_scope, subject_enc, subject_lookup_hmac, key_id, state, verified_at, created_at, updated_at)
        SELECT ?, ?, kind, provider_scope, subject_enc, subject_lookup_hmac, key_id, state, verified_at, created_at, updated_at
        FROM login_identity WHERE user_id = ?`, duplicateID, fixtureUser, created.SubjectID)
	var duplicate *mysql.MySQLError
	if !errors.As(err, &duplicate) || duplicate.Number != 1062 || !strings.Contains(duplicate.Message, "identity_active_subject") {
		t.Fatal("database allowed the same active email identity on two subjects")
	}
	otherIndex, otherCiphertext, err := protector.Protect("constraint-check@example.test")
	if err != nil {
		t.Fatal("could not protect test email")
	}
	duplicateID, _ = domain.GenerateChallengeID()
	_, err = constraintTx.ExecContext(ctx, `INSERT INTO login_identity
        (id, user_id, kind, provider_scope, subject_enc, subject_lookup_hmac, key_id, state, verified_at, created_at, updated_at)
        VALUES (?, ?, 'email', 'beta-email', ?, ?, 'test-key', 'active', UTC_TIMESTAMP(6), UTC_TIMESTAMP(6), UTC_TIMESTAMP(6))`,
		duplicateID, created.SubjectID, otherCiphertext, otherIndex[:])
	duplicate = nil
	if !errors.As(err, &duplicate) || duplicate.Number != 1062 || !strings.Contains(duplicate.Message, "identity_active_kind") {
		t.Fatal("database allowed two active email identities for one subject")
	}
	if err := constraintTx.Rollback(); err != nil {
		t.Fatal("could not roll back identity constraint check")
	}
	var ciphertext []byte
	if err := db.QueryRowContext(ctx, `SELECT subject_enc FROM login_identity WHERE user_id = ?`, created.SubjectID).Scan(&ciphertext); err != nil || bytes.Contains(ciphertext, []byte(email)) {
		t.Fatal("identity email was not protected")
	}
	tampered := append([]byte(nil), ciphertext...)
	tampered[len(tampered)-1] ^= 1
	if _, err := db.ExecContext(ctx, `UPDATE login_identity SET subject_enc = ? WHERE user_id = ?`, tampered, created.SubjectID); err != nil {
		t.Fatal("could not corrupt test identity ciphertext")
	}
	id, signed = issue(email, firstKey, firstPublic)
	if _, err := resolve(id, signed, true); !errors.Is(err, ErrIdentityUnavailable) {
		t.Fatal("tampered identity ciphertext was accepted")
	}
	if _, err := db.ExecContext(ctx, `UPDATE login_identity SET subject_enc = ? WHERE user_id = ?`, ciphertext, created.SubjectID); err != nil {
		t.Fatal("could not restore test identity ciphertext")
	}

	id, signed = issue("person@EXAMPLE.TEST", firstKey, firstPublic)
	trusted, err := resolve(id, signed, true)
	if err != nil || trusted.State != Trusted || trusted.SubjectID != created.SubjectID || trusted.DeviceID != created.DeviceID {
		t.Fatalf("existing trusted device was not recognized: %v", err)
	}
	id, signed = issue(email, firstKey, firstPublic)
	wrongProtector, _ := domain.NewEmailIdentityProtector(bytes.Repeat([]byte{5}, 32), bytes.Repeat([]byte{4}, 32), "test-key")
	wrongResolver, _ := NewResolver(wrongProtector)
	wrongTx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	verified, err := store.Attempt(ctx, wrongTx, challenge.AttemptInput{ChallengeID: id, Code: "012345", DeviceProof: signed})
	if err != nil || !verified.Verified {
		_ = wrongTx.Rollback()
		t.Fatal("could not verify test challenge for wrong-key check")
	}
	if _, err := wrongResolver.ResolveEmail(ctx, wrongTx, verified.Proof); !errors.Is(err, ErrIdentityUnavailable) {
		_ = wrongTx.Rollback()
		t.Fatal("identity resolver accepted the wrong key epoch")
	}
	_ = wrongTx.Rollback()
	id, signed = issue(email, secondKey, secondPublic)
	pending, err := resolve(id, signed, false)
	if err != nil || pending.State != ApprovalRequired || pending.SubjectID != created.SubjectID || pending.DeviceID != "" {
		t.Fatalf("new device did not require approval: %v", err)
	}
	var count int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM trusted_device WHERE user_id = ?`, created.SubjectID).Scan(&count); err != nil || count != 1 {
		t.Fatal("unapproved device became trusted")
	}

	// The same key cannot silently create another subject under a different email.
	id, signed = issue("other@example.test", firstKey, firstPublic)
	if _, err := resolve(id, signed, true); !errors.Is(err, ErrIdentityUnavailable) {
		t.Fatal("device key was reused for another subject")
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM user`).Scan(&count); err != nil || count != 1 {
		t.Fatal("failed identity creation left an extra subject")
	}

	type outcome struct {
		result Resolution
		err    error
	}
	runConcurrent := func(ids, proofs []string, gate *sql.Tx, table string, release func() error) []outcome {
		t.Helper()
		var start sync.WaitGroup
		start.Add(1)
		results := make(chan outcome, 2)
		for i := range ids {
			go func(i int) {
				start.Wait()
				result, err := resolve(ids[i], proofs[i], true)
				results <- outcome{result, err}
			}(i)
		}
		start.Done()
		deadline := time.Now().Add(5 * time.Second)
		blocked := 0
		for time.Now().Before(deadline) {
			err := db.QueryRowContext(ctx, `SELECT COUNT(DISTINCT w.REQUESTING_ENGINE_TRANSACTION_ID)
                FROM performance_schema.data_lock_waits w
                JOIN performance_schema.data_locks l ON l.ENGINE_LOCK_ID = w.REQUESTING_ENGINE_LOCK_ID
                WHERE l.OBJECT_SCHEMA = DATABASE() AND l.OBJECT_NAME = ?`, table).Scan(&blocked)
			if err != nil {
				_ = gate.Rollback()
				t.Fatal("could not observe isolated database lock waits")
			}
			if blocked >= 2 {
				break
			}
			time.Sleep(20 * time.Millisecond)
		}
		if blocked < 2 {
			_ = gate.Rollback()
			t.Fatal("both transactions did not reach the controlled lock")
		}
		if err := release(); err != nil {
			_ = gate.Rollback()
			t.Fatal("could not release the isolated test lock")
		}
		return []outcome{<-results, <-results}
	}
	thirdKey, thirdPublic := privateKey(t)
	firstID, firstSigned := issue("device-race-a@example.test", thirdKey, thirdPublic)
	secondID, secondSigned := issue("device-race-b@example.test", thirdKey, thirdPublic)
	thirdFingerprint, _ := domain.DeviceKeyFingerprint(thirdPublic)
	sentinelID, _ := domain.GenerateChallengeID()
	if _, err := db.ExecContext(ctx, `INSERT INTO trusted_device (id, user_id, key_fingerprint, public_x, public_y, state, created_at)
        VALUES (?, ?, ?, ?, ?, 'active', UTC_TIMESTAMP(6))`, sentinelID, created.SubjectID, thirdFingerprint[:], thirdPublic.X, thirdPublic.Y); err != nil {
		t.Fatal("could not create isolated device lock fixture")
	}
	deviceGate, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	var lockedID []byte
	if err := deviceGate.QueryRowContext(ctx, `SELECT id FROM trusted_device WHERE key_fingerprint = ? FOR UPDATE`, thirdFingerprint[:]).Scan(&lockedID); err != nil {
		_ = deviceGate.Rollback()
		t.Fatal("could not lock test device")
	}
	deviceRace := runConcurrent([]string{firstID, secondID}, []string{firstSigned, secondSigned}, deviceGate, "trusted_device", func() error {
		if _, err := deviceGate.ExecContext(ctx, `DELETE FROM trusted_device WHERE id = ?`, sentinelID); err != nil {
			return err
		}
		return deviceGate.Commit()
	})
	deviceSuccesses := 0
	for _, result := range deviceRace {
		if result.err == nil && result.result.State == Created {
			deviceSuccesses++
		} else if !errors.Is(result.err, ErrIdentityUnavailable) {
			t.Fatalf("unexpected device-race result: %v", result.err)
		}
	}
	if deviceSuccesses != 1 || db.QueryRowContext(ctx, `SELECT COUNT(*) FROM trusted_device WHERE key_fingerprint = ?`, thirdFingerprint[:]).Scan(&count) != nil || count != 1 {
		t.Fatal("same device key created multiple subjects")
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM user`).Scan(&count); err != nil || count != 2 {
		t.Fatal("device-race rollback left extra subjects")
	}

	fourthKey, fourthPublic := privateKey(t)
	fifthKey, fifthPublic := privateKey(t)
	firstID, firstSigned = issue("same@example.test", fourthKey, fourthPublic)
	secondID, secondSigned = issue("same@EXAMPLE.TEST", fifthKey, fifthPublic)
	index, _ := protector.Index("same@example.test")
	emailGate, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := emailGate.QueryRowContext(ctx, `SELECT challenge_id FROM auth_email_login_slot WHERE email_index = ? FOR UPDATE`, index[:]).Scan(&lockedID); err != nil {
		_ = emailGate.Rollback()
		t.Fatal("could not lock test email slot")
	}
	// Resending replaces the first challenge, so this validates challenge-slot
	// serialization and invalidation, not concurrent identity INSERTs.
	emailSlotRace := runConcurrent([]string{firstID, secondID}, []string{firstSigned, secondSigned}, emailGate, "auth_email_login_slot", emailGate.Commit)
	emailSuccesses := 0
	for _, result := range emailSlotRace {
		if result.err == nil && result.result.State == Created {
			emailSuccesses++
		} else if !errors.Is(result.err, ErrIdentityUnavailable) {
			t.Fatalf("unexpected email-race result: %v", result.err)
		}
	}
	if emailSuccesses != 1 || db.QueryRowContext(ctx, `SELECT COUNT(*) FROM login_identity WHERE subject_lookup_hmac = ? AND active_slot = 1`, index[:]).Scan(&count) != nil || count != 1 {
		t.Fatal("normalized email created multiple active identities")
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM user`).Scan(&count); err != nil || count != 3 {
		t.Fatal("email-race rollback left extra subjects")
	}

	// A caller cannot pass a verified challenge to a different transaction.
	id, signed = issue(email, firstKey, firstPublic)
	firstTx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	proof, err := store.Attempt(ctx, firstTx, challenge.AttemptInput{ChallengeID: id, Code: "012345", DeviceProof: signed})
	if err != nil || !proof.Verified {
		_ = firstTx.Rollback()
		t.Fatal("could not verify test challenge")
	}
	otherTx, err := db.BeginTx(ctx, nil)
	if err != nil {
		_ = firstTx.Rollback()
		t.Fatal(err)
	}
	if _, err := resolver.ResolveEmail(ctx, otherTx, proof.Proof); !errors.Is(err, ErrIdentityUnavailable) {
		_ = otherTx.Rollback()
		_ = firstTx.Rollback()
		t.Fatal("cross-transaction proof accepted")
	}
	_ = otherTx.Rollback()
	_ = firstTx.Rollback()

	if _, err := db.ExecContext(ctx, `UPDATE trusted_device SET state = 'revoked' WHERE id = ?`, created.DeviceID); err != nil {
		t.Fatal("could not revoke test device")
	}
	id, signed = issue(email, firstKey, firstPublic)
	if _, err := resolve(id, signed, true); !errors.Is(err, ErrIdentityUnavailable) {
		t.Fatal("revoked device was silently trusted")
	}
	if _, err := db.ExecContext(ctx, `UPDATE user SET status = 'disabled' WHERE id = ?`, created.SubjectID); err != nil {
		t.Fatal("could not disable test user")
	}
	id, signed = issue(email, secondKey, secondPublic)
	if _, err := resolve(id, signed, true); !errors.Is(err, ErrIdentityUnavailable) {
		t.Fatal("disabled subject was accepted")
	}
}

func TestResolutionDoesNotExposeIdentityInLogs(t *testing.T) {
	value := Resolution{State: Trusted, SubjectID: "123456", DeviceID: "sensitive-device"}
	if _, err := json.Marshal(value); err == nil {
		t.Fatal("identity resolution was JSON-serializable")
	}
	for _, result := range []string{fmt.Sprintf("%+v", value), fmt.Sprintf("%#v", value)} {
		if strings.Contains(result, value.SubjectID) || strings.Contains(result, value.DeviceID) {
			t.Fatal("identity resolution leaked identifiers")
		}
	}
	var log bytes.Buffer
	slog.New(slog.NewJSONHandler(&log, nil)).Info("test", "result", value)
	if strings.Contains(log.String(), value.SubjectID) || strings.Contains(log.String(), value.DeviceID) {
		t.Fatal("structured log exposed identity resolution")
	}
}
