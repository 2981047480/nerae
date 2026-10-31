package mailworker

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
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"
	"nerae/api/internal/modules/auth/bootstrap"
	"nerae/api/internal/modules/auth/challenge"
	"nerae/api/internal/modules/auth/codeepoch"
	"nerae/api/internal/modules/auth/domain"
	"nerae/api/internal/modules/auth/issuance"
	"nerae/api/internal/modules/auth/repository"
)

const runtimeHashEpochKeyID = "runtime-hash-epoch-test-v1"

// TestRuntimeHashEpochTamperAndLossFailClosed exercises the verifier epoch at
// the challenge write, proof attempt, and real worker send boundaries. The
// DSN is supplied only by the dedicated socket-only evidence harness.
func TestRuntimeHashEpochTamperAndLossFailClosed(t *testing.T) {
	dsn := os.Getenv("NERAE_EMAIL_HASH_EPOCH_TEST_DSN")
	if dsn == "" {
		t.Skip("dedicated hash-epoch MySQL DSN is not configured")
	}
	parsed, err := mysql.ParseDSN(dsn)
	if err != nil || parsed.Net != "unix" || parsed.User != "root" || parsed.Passwd != "" ||
		parsed.DBName != "nerae_email_hash_epoch_test" || !parsed.ParseTime || parsed.Loc == nil || parsed.Loc.String() != "UTC" {
		t.Fatal("hash-epoch test requires its dedicated UTC Unix socket DSN")
	}
	base := filepath.Dir(parsed.Addr)
	baseInfo, err := os.Lstat(base)
	resolvedBase, resolveErr := filepath.EvalSymlinks(base)
	expectedBase, absErr := filepath.Abs(base)
	socketInfo, socketErr := os.Lstat(parsed.Addr)
	if err != nil || resolveErr != nil || absErr != nil || socketErr != nil || !baseInfo.IsDir() ||
		baseInfo.Mode()&os.ModeSymlink != 0 || baseInfo.Mode().Perm()&0o077 != 0 || resolvedBase != expectedBase ||
		filepath.Dir(resolvedBase) != "/private/tmp" || !strings.HasPrefix(filepath.Base(resolvedBase), "nerae-a01-19-mysql-") ||
		filepath.Base(parsed.Addr) != "mysql.sock" || socketInfo.Mode()&os.ModeSocket == 0 || socketInfo.Mode()&os.ModeSymlink != 0 {
		t.Fatal("refusing non-private hash-epoch MySQL target")
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatal("private database open failed")
	}
	db.SetMaxOpenConns(12)
	db.SetMaxIdleConns(12)
	t.Cleanup(func() { _ = db.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		t.Fatal("private database unavailable")
	}

	hasher, err := domain.NewEmailCodeHasher(bytes.Repeat([]byte{0x61}, 32))
	if err != nil {
		t.Fatal("hasher setup failed")
	}
	replay, err := repository.NewProofReplayStore(bytes.Repeat([]byte{0x62}, 32))
	if err != nil {
		t.Fatal("replay setup failed")
	}
	identities, err := domain.NewEmailIdentityProtector(bytes.Repeat([]byte{0x63}, 32), bytes.Repeat([]byte{0x64}, 32), "runtime-hash-epoch-email-v1")
	if err != nil {
		t.Fatal("identity protector setup failed")
	}
	deriver, err := issuance.NewDeriver(bytes.Repeat([]byte{0x65}, 32))
	if err != nil {
		t.Fatal("code deriver setup failed")
	}
	policy := challenge.Policy{ChallengeTTL: 5 * time.Minute, MaxAttempts: 3, DeviceProof: domain.ProofPolicy{MaxLifetime: 5 * time.Minute, ClockSkew: 30 * time.Second}}
	challenges, err := challenge.NewStore(hasher, replay, identities, runtimeHashEpochKeyID, policy)
	if err != nil {
		t.Fatal("challenge store setup failed")
	}
	mailer, results, listener, connections, sinkDone := workerLocalSMTP(t)
	t.Cleanup(func() { _ = listener.Close() })
	outbox, err := issuance.NewOutbox(deriver, "runtime-code-test-v1", hasher, runtimeHashEpochKeyID, identities, mailer, 20*time.Second)
	if err != nil {
		t.Fatal("outbox setup failed")
	}
	worker, err := New(Config{Outbox: outbox, DB: db, PollInterval: 100 * time.Millisecond, RecoveryBatch: 10})
	if err != nil {
		t.Fatal("worker setup failed")
	}
	initializer, err := bootstrap.NewIdentityEpochInitializer(identities)
	if err != nil {
		t.Fatal("identity initializer setup failed")
	}
	hashEpoch, err := codeepoch.New(hasher, runtimeHashEpochKeyID)
	if err != nil {
		t.Fatal("hash epoch setup failed")
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal("epoch transaction failed")
	}
	if err := initializer.Initialize(ctx, tx); err != nil {
		_ = tx.Rollback()
		t.Fatal("identity epoch initialization failed")
	}
	if err := outbox.InitializeCodeKey(ctx, tx); err != nil {
		_ = tx.Rollback()
		t.Fatal("code epoch initialization failed")
	}
	if err := hashEpoch.Initialize(ctx, tx); err != nil {
		_ = tx.Rollback()
		t.Fatal("hash epoch initialization failed")
	}
	if err := tx.Commit(); err != nil {
		t.Fatal("epoch initialization commit failed")
	}
	var originalHashTag []byte
	if err := db.QueryRowContext(ctx, `SELECT key_tag FROM auth_email_code_hash_key_epoch WHERE slot=1`).Scan(&originalHashTag); err != nil || len(originalHashTag) != 32 {
		t.Fatal("could not capture configured hash epoch for restoration")
	}

	// A corrupted epoch blocks challenge issuance without partial slot,
	// challenge, or outbox writes.
	corrupt := bytes.Repeat([]byte{0x7a}, 32)
	if _, err := db.ExecContext(ctx, `UPDATE auth_email_code_hash_key_epoch SET key_tag=? WHERE slot=1`, corrupt); err != nil {
		t.Fatal("could not prepare epoch tamper case")
	}
	if _, err := issueRuntimeOnly(t, ctx, db, challenges, deriver, "tamper@worker.test"); !errors.Is(err, challenge.ErrChallengeStore) {
		t.Fatal("challenge issuance did not fail closed for tampered hash epoch")
	}
	assertNoRuntimeChallengeRows(t, ctx, db)

	// Missing epoch after history exists also blocks issuance. Restore the
	// original row only after checking absence of partial writes.
	if _, err := db.ExecContext(ctx, `DELETE FROM auth_email_code_hash_key_epoch WHERE slot=1`); err != nil {
		t.Fatal("could not prepare missing epoch case")
	}
	if _, err := issueRuntimeOnly(t, ctx, db, challenges, deriver, "missing@worker.test"); !errors.Is(err, challenge.ErrChallengeStore) {
		t.Fatal("challenge issuance did not fail closed for missing hash epoch")
	}
	assertNoRuntimeChallengeRows(t, ctx, db)
	restoreHashEpoch(t, ctx, db, originalHashTag)

	challengeID, code, proof, fingerprint, err := enqueueRuntimeChallenge(t, ctx, db, outbox, challenges, deriver, "valid@worker.test")
	if err != nil {
		t.Fatal("challenge setup failed after epoch restoration")
	}
	var beforeAttempts int
	if err := db.QueryRowContext(ctx, `SELECT attempts FROM auth_email_login_challenge WHERE challenge_id=?`, []byte(challengeID)).Scan(&beforeAttempts); err != nil || beforeAttempts != 0 {
		t.Fatal("challenge was not cleanly issued")
	}

	for _, mode := range []string{"tampered", "missing"} {
		if mode == "tampered" {
			if _, err := db.ExecContext(ctx, `UPDATE auth_email_code_hash_key_epoch SET key_tag=? WHERE slot=1`, corrupt); err != nil {
				t.Fatal("could not retamper hash epoch")
			}
		} else if _, err := db.ExecContext(ctx, `DELETE FROM auth_email_code_hash_key_epoch WHERE slot=1`); err != nil {
			t.Fatal("could not remove hash epoch")
		}

		attemptTx, err := db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal("attempt transaction failed")
		}
		result, attemptErr := challenges.Attempt(ctx, attemptTx, challenge.AttemptInput{ChallengeID: challengeID, Code: code, DeviceProof: proof})
		if !errors.Is(attemptErr, challenge.ErrChallengeStore) || result.Changed || result.Verified {
			_ = attemptTx.Rollback()
			t.Fatalf("attempt did not fail closed for %s epoch: changed=%v verified=%v", mode, result.Changed, result.Verified)
		}
		// Commit the transaction after the expected failure. This makes the
		// following assertions prove the failed operation did not stage writes
		// that would only be hidden by a caller rollback.
		if err := attemptTx.Commit(); err != nil {
			t.Fatalf("read-only failed %s attempt transaction could not commit", mode)
		}
		var attempts int
		if err := db.QueryRowContext(ctx, `SELECT attempts FROM auth_email_login_challenge WHERE challenge_id=?`, []byte(challengeID)).Scan(&attempts); err != nil || attempts != beforeAttempts {
			t.Fatalf("failed %s attempt changed attempt count", mode)
		}
		var replayRows int
		if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM auth_device_proof_replay WHERE device_fingerprint=?`, fingerprint[:]).Scan(&replayRows); err != nil || replayRows != 0 {
			t.Fatalf("failed %s attempt consumed device proof", mode)
		}
		if err := worker.RunOnce(ctx); !errors.Is(err, ErrDatabase) {
			t.Fatalf("worker did not fail closed for %s hash epoch", mode)
		}
		if got := connections.Load(); got != 0 {
			t.Fatalf("worker connected to SMTP while hash epoch was %s: %d", mode, got)
		}
		if got := workerState(t, ctx, db, challengeID); got != "pending" {
			t.Fatalf("worker changed queued delivery under %s epoch: %q", mode, got)
		}
		restoreHashEpoch(t, ctx, db, originalHashTag)
	}

	// After restoring the exact configured epoch, the queued message reaches
	// only the local TLS sink. Sending precedes attempt consumption because
	// the outbox deliberately refuses to send a consumed challenge.
	if err := worker.RunOnce(ctx); err != nil {
		t.Fatal("worker send failed after epoch restoration")
	}
	if got := workerState(t, ctx, db, challengeID); got != string(issuance.DeliveryAccepted) {
		t.Fatalf("restored delivery state = %q", got)
	}
	assertWorkerSinkMessage(t, <-results, "valid@worker.test", code)
	if got := connections.Load(); got != 1 {
		t.Fatalf("restored normal path SMTP connections = %d, want 1", got)
	}
	attemptTx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal("restored attempt transaction failed")
	}
	result, err := challenges.Attempt(ctx, attemptTx, challenge.AttemptInput{ChallengeID: challengeID, Code: code, DeviceProof: proof})
	if err != nil || !result.Verified || !result.Changed {
		_ = attemptTx.Rollback()
		t.Fatal("valid proof did not succeed after epoch restoration")
	}
	if err := attemptTx.Commit(); err != nil {
		t.Fatal("restored valid attempt commit failed")
	}
	if err := listener.Close(); err != nil {
		t.Fatal("local SMTP sink close failed")
	}
	select {
	case <-sinkDone:
	case <-time.After(time.Second):
		t.Fatal("local SMTP sink did not stop")
	}
}

func enqueueRuntimeChallenge(t *testing.T, ctx context.Context, db *sql.DB, outbox *issuance.Outbox, challenges *challenge.Store, deriver *issuance.Deriver, email string) (string, string, string, [32]byte, error) {
	t.Helper()
	id, err := domain.GenerateChallengeID()
	if err != nil {
		t.Fatal("challenge ID generation failed")
	}
	nonce, err := domain.GenerateChallengeNonce()
	if err != nil {
		t.Fatal("challenge nonce generation failed")
	}
	private, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal("device key generation failed")
	}
	encode := base64.RawURLEncoding.EncodeToString
	public := domain.DevicePublicJWK{Kty: "EC", Crv: "P-256", X: encode(private.X.FillBytes(make([]byte, 32))), Y: encode(private.Y.FillBytes(make([]byte, 32)))}
	code, err := deriver.Derive(id, "email_login", 6)
	if err != nil {
		t.Fatal("test code derivation failed")
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal("challenge transaction failed")
	}
	epoch, err := outbox.LockEpochs(ctx, tx)
	if err == nil {
		err = challenges.Issue(ctx, tx, challenge.IssueInput{Email: email, ChallengeID: id, Nonce: nonce, Code: code, PublicKey: public})
	}
	if err == nil {
		err = outbox.Enqueue(ctx, tx, epoch, id, 6)
	}
	if err != nil {
		_ = tx.Rollback()
		return "", "", "", [32]byte{}, err
	}
	if err := tx.Commit(); err != nil {
		t.Fatal("challenge commit failed")
	}
	proof := runtimeDeviceProof(t, private, id, nonce, "runtime-jti-"+id, time.Now().UTC().Truncate(time.Second))
	fingerprint, err := domain.DeviceKeyFingerprint(public)
	if err != nil {
		t.Fatal("device fingerprint derivation failed")
	}
	return id, code, proof, fingerprint, nil
}

func issueRuntimeOnly(t *testing.T, ctx context.Context, db *sql.DB, challenges *challenge.Store, deriver *issuance.Deriver, email string) (string, error) {
	t.Helper()
	id, err := domain.GenerateChallengeID()
	if err != nil {
		t.Fatal("challenge ID generation failed")
	}
	nonce, err := domain.GenerateChallengeNonce()
	if err != nil {
		t.Fatal("challenge nonce generation failed")
	}
	private, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal("device key generation failed")
	}
	encode := base64.RawURLEncoding.EncodeToString
	public := domain.DevicePublicJWK{Kty: "EC", Crv: "P-256", X: encode(private.X.FillBytes(make([]byte, 32))), Y: encode(private.Y.FillBytes(make([]byte, 32)))}
	code, err := deriver.Derive(id, "email_login", 6)
	if err != nil {
		t.Fatal("test code derivation failed")
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal("challenge transaction failed")
	}
	defer tx.Rollback()
	err = challenges.Issue(ctx, tx, challenge.IssueInput{Email: email, ChallengeID: id, Nonce: nonce, Code: code, PublicKey: public})
	// The test deliberately commits the expected epoch error so the caller's
	// rollback cannot conceal writes staged by Issue.
	if commitErr := tx.Commit(); commitErr != nil {
		return id, commitErr
	}
	return id, err
}

func runtimeDeviceProof(t *testing.T, private *ecdsa.PrivateKey, challengeID, nonce, jti string, now time.Time) string {
	t.Helper()
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"ES256","typ":"JWT"}`))
	claims, err := json.Marshal(map[string]any{"aud": "nerae-auth-device", "purpose": "email_login", "resourceId": challengeID,
		"nonce": nonce, "iat": now.Unix(), "exp": now.Add(time.Minute).Unix(), "jti": jti})
	if err != nil {
		t.Fatal("proof claims encoding failed")
	}
	input := header + "." + base64.RawURLEncoding.EncodeToString(claims)
	digest := sha256.Sum256([]byte(input))
	r, s, err := ecdsa.Sign(rand.Reader, private, digest[:])
	if err != nil {
		t.Fatal("proof signing failed")
	}
	signature := append(r.FillBytes(make([]byte, 32)), s.FillBytes(make([]byte, 32))...)
	return input + "." + base64.RawURLEncoding.EncodeToString(signature)
}

func assertNoRuntimeChallengeRows(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()
	for _, table := range []string{"auth_email_login_slot", "auth_email_login_challenge", "auth_email_outbox"} {
		var count int
		if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+table).Scan(&count); err != nil || count != 0 {
			t.Fatalf("failed issuance left rows in %s", table)
		}
	}
}

func restoreHashEpoch(t *testing.T, ctx context.Context, db *sql.DB, tag []byte) {
	t.Helper()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal("hash epoch restore transaction failed")
	}
	defer tx.Rollback()
	// Restore is test administration only: insert the exact configured ID/tag
	// after the preceding failed operation has rolled back.
	if _, err := tx.ExecContext(ctx, `INSERT INTO auth_email_code_hash_key_epoch (slot,key_id,key_tag) VALUES (1,?,?) ON DUPLICATE KEY UPDATE key_id=VALUES(key_id),key_tag=VALUES(key_tag)`, []byte(runtimeHashEpochKeyID), tag[:]); err != nil {
		t.Fatal("hash epoch restore failed")
	}
	if err := tx.Commit(); err != nil {
		t.Fatal("hash epoch restore commit failed")
	}
}
