package issuance

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hmac"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"
	"nerae/api/internal/modules/auth/challenge"
	"nerae/api/internal/modules/auth/domain"
	"nerae/api/internal/modules/auth/mail"
	"nerae/api/internal/modules/auth/repository"
)

type recordingSender struct {
	mu        sync.Mutex
	err       error
	calls     int
	recipient string
	code      string
}

func (s *recordingSender) SendVerificationCode(_ context.Context, recipient, code string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	s.recipient, s.code = recipient, code
	return s.err
}

func (s *recordingSender) setError(err error) { s.mu.Lock(); s.err = err; s.mu.Unlock() }
func (s *recordingSender) snapshot() (int, string, string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls, s.recipient, s.code
}

type outboxFixture struct {
	t          *testing.T
	db         *sql.DB
	ctx        context.Context
	hasher     *domain.EmailCodeHasher
	identities *domain.EmailIdentityProtector
	challenges *challenge.Store
	outbox     *Outbox
	sender     *recordingSender
}

func openOutboxFixture(t *testing.T) *outboxFixture {
	return openOutboxFixtureWithCodeEpoch(t, true)
}

func openOutboxFixtureWithCodeEpoch(t *testing.T, initializeCodeEpoch bool) *outboxFixture {
	t.Helper()
	dsn := os.Getenv("NERAE_EMAIL_OUTBOX_TEST_DSN")
	if dsn == "" {
		t.Skip("isolated socket-only MySQL DSN is not configured")
	}
	cfg, err := mysql.ParseDSN(dsn)
	if err != nil || cfg.Net != "unix" || cfg.Addr == "" {
		t.Fatal("test DSN must use a private Unix socket")
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatal("test database open failed")
	}
	db.SetMaxOpenConns(12)
	db.SetMaxIdleConns(12)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	if err := db.PingContext(ctx); err != nil {
		cancel()
		db.Close()
		t.Fatal("private test database unavailable")
	}
	t.Cleanup(func() { cancel(); db.Close() })
	columns, err := db.QueryContext(ctx, `SELECT column_name FROM information_schema.columns WHERE table_schema=DATABASE() AND table_name='auth_email_outbox'`)
	if err != nil {
		t.Fatal("outbox schema inspection failed")
	}
	forbidden := map[string]bool{"email": true, "email_address": true, "recipient": true, "code": true,
		"verification_code": true, "code_plaintext": true, "code_digest": true, "code_ciphertext": true, "email_ciphertext": true}
	for columns.Next() {
		var name string
		if err := columns.Scan(&name); err != nil {
			columns.Close()
			t.Fatal("outbox schema scan failed")
		}
		if forbidden[name] {
			columns.Close()
			t.Fatalf("outbox persists a prohibited sensitive column: %s", name)
		}
	}
	if err := columns.Err(); err != nil {
		columns.Close()
		t.Fatal("outbox schema enumeration failed")
	}
	columns.Close()
	hasher, err := domain.NewEmailCodeHasher(bytes.Repeat([]byte{0x11}, 32))
	if err != nil {
		t.Fatal("hasher setup failed")
	}
	replay, err := repository.NewProofReplayStore(bytes.Repeat([]byte{0x22}, 32))
	if err != nil {
		t.Fatal("replay store setup failed")
	}
	identities, err := domain.NewEmailIdentityProtector(bytes.Repeat([]byte{0x33}, 32), bytes.Repeat([]byte{0x44}, 32), "outbox-email-v1")
	if err != nil {
		t.Fatal("identity protector setup failed")
	}
	deriver, err := NewDeriver(bytes.Repeat([]byte{0x55}, 32))
	if err != nil {
		t.Fatal("code deriver setup failed")
	}
	sender := &recordingSender{}
	outbox, err := NewOutbox(deriver, "outbox-code-v1", hasher, identities, sender, 2*time.Second)
	if err != nil {
		t.Fatal("outbox setup failed")
	}
	challengeStore, err := challenge.NewStore(hasher, replay, identities, challenge.Policy{ChallengeTTL: 5 * time.Minute, MaxAttempts: 3, DeviceProof: domain.ProofPolicy{MaxLifetime: 5 * time.Minute, ClockSkew: 30 * time.Second}})
	if err != nil {
		t.Fatal("challenge store setup failed")
	}
	fixture := &outboxFixture{t: t, db: db, ctx: ctx, hasher: hasher, identities: identities, challenges: challengeStore, outbox: outbox, sender: sender}
	_, identityTag, err := identities.KeyEpoch()
	if err != nil {
		t.Fatal("identity epoch failed")
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal("epoch transaction failed")
	}
	var storedIdentityTag []byte
	err = tx.QueryRowContext(ctx, `SELECT key_tag FROM auth_email_identity_key_epoch WHERE slot=1 FOR SHARE`).Scan(&storedIdentityTag)
	if errors.Is(err, sql.ErrNoRows) {
		if _, err := tx.ExecContext(ctx, `INSERT INTO auth_email_identity_key_epoch(slot,key_tag) VALUES (1,?)`, identityTag[:]); err != nil {
			_ = tx.Rollback()
			t.Fatal("identity epoch initialization failed")
		}
	} else if err != nil || !hmac.Equal(storedIdentityTag, identityTag[:]) {
		_ = tx.Rollback()
		t.Fatal("identity epoch fixture mismatched")
	}
	if initializeCodeEpoch {
		if err := outbox.InitializeCodeKey(ctx, tx); err != nil {
			_ = tx.Rollback()
			t.Fatal("code epoch initialization failed")
		}
	} else {
		var count int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM auth_email_code_key_epoch`).Scan(&count); err != nil || count != 0 {
			_ = tx.Rollback()
			t.Fatal("code epoch should be absent in bootstrap fixture")
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal("epoch commit failed")
	}
	return fixture
}

func (f *outboxFixture) issue(email string, enqueue bool, suppliedCode string) string {
	f.t.Helper()
	id, err := domain.GenerateChallengeID()
	if err != nil {
		f.t.Fatal("challenge identifier generation failed")
	}
	nonce, err := domain.GenerateChallengeNonce()
	if err != nil {
		f.t.Fatal("challenge nonce generation failed")
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		f.t.Fatal("synthetic device generation failed")
	}
	encode := base64.RawURLEncoding.EncodeToString
	public := domain.DevicePublicJWK{Kty: "EC", Crv: "P-256", X: encode(key.X.FillBytes(make([]byte, 32))), Y: encode(key.Y.FillBytes(make([]byte, 32)))}
	if suppliedCode == "derive" {
		suppliedCode, err = f.outbox.deriver.Derive(id, emailLoginPurpose, 6)
		if err != nil {
			f.t.Fatal("challenge code derivation failed")
		}
	}
	tx, err := f.db.BeginTx(f.ctx, nil)
	if err != nil {
		f.t.Fatal("challenge transaction failed")
	}
	if enqueue {
		epoch, err := f.outbox.LockEpochs(f.ctx, tx)
		if err != nil {
			_ = tx.Rollback()
			f.t.Fatal("epoch lock failed")
		}
		if err := f.challenges.Issue(f.ctx, tx, challenge.IssueInput{Email: email, ChallengeID: id, Nonce: nonce, Code: suppliedCode, PublicKey: public}); err != nil {
			_ = tx.Rollback()
			f.t.Fatal("challenge issue failed")
		}
		if err := f.outbox.Enqueue(f.ctx, tx, epoch, id, 6); err != nil {
			_ = tx.Rollback()
			f.t.Fatalf("outbox enqueue failed: %v", err)
		}
	} else {
		if err := f.challenges.Issue(f.ctx, tx, challenge.IssueInput{Email: email, ChallengeID: id, Nonce: nonce, Code: suppliedCode, PublicKey: public}); err != nil {
			_ = tx.Rollback()
			f.t.Fatal("challenge issue failed")
		}
	}
	if err := tx.Commit(); err != nil {
		f.t.Fatal("challenge transaction commit failed")
	}
	return id
}

func (f *outboxFixture) enqueueExisting(id string, digits int) error {
	tx, err := f.db.BeginTx(f.ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	epoch, err := f.outbox.LockEpochs(f.ctx, tx)
	if err != nil {
		return err
	}
	if err := f.outbox.Enqueue(f.ctx, tx, epoch, id, digits); err != nil {
		return err
	}
	return tx.Commit()
}

func (f *outboxFixture) state(id string) string {
	f.t.Helper()
	var state string
	if err := f.db.QueryRowContext(f.ctx, `SELECT state FROM auth_email_outbox WHERE challenge_id=?`, []byte(id)).Scan(&state); err != nil {
		f.t.Fatalf("outbox state read failed: %v", err)
	}
	return state
}

func TestEmailOutboxCodeKeyBootstrapIsIdempotentAndFailClosed(t *testing.T) {
	f := openOutboxFixtureWithCodeEpoch(t, false)
	var wg sync.WaitGroup
	start := make(chan struct{})
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			tx, err := f.db.BeginTx(f.ctx, nil)
			if err != nil {
				errs <- err
				return
			}
			<-start
			if err := f.outbox.InitializeCodeKey(f.ctx, tx); err != nil {
				_ = tx.Rollback()
				errs <- err
				return
			}
			errs <- tx.Commit()
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	failures := 0
	for err := range errs {
		if err != nil {
			if !errors.Is(err, ErrOutbox) {
				t.Fatalf("concurrent same-key initialization returned unexpected error: %v", err)
			}
			failures++
		}
	}
	if failures > 1 {
		t.Fatalf("too many concurrent initializers failed: %d", failures)
	}
	if failures == 1 {
		// A lock wait/deadlock victim must start a new transaction and retry;
		// the failed transaction itself is never reported as initialized.
		tx, err := f.db.BeginTx(f.ctx, nil)
		if err != nil {
			t.Fatal("retry transaction failed")
		}
		if err := f.outbox.InitializeCodeKey(f.ctx, tx); err != nil {
			_ = tx.Rollback()
			t.Fatalf("new-transaction retry failed: %v", err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatal("retry transaction commit failed")
		}
	}

	// Restarting with the same material and key ID verifies the fixed epoch;
	// it must not attempt to insert or change it.
	tx, err := f.db.BeginTx(f.ctx, nil)
	if err != nil {
		t.Fatal("restart transaction failed")
	}
	if err := f.outbox.InitializeCodeKey(f.ctx, tx); err != nil {
		_ = tx.Rollback()
		t.Fatal("same-key restart was not idempotent")
	}
	if err := tx.Commit(); err != nil {
		t.Fatal("same-key restart commit failed")
	}

	otherDeriver, err := NewDeriver(bytes.Repeat([]byte{0x56}, 32))
	if err != nil {
		t.Fatal("alternate derivation key setup failed")
	}
	otherOutbox, err := NewOutbox(otherDeriver, "outbox-code-v1", f.hasher, f.identities, f.sender, 2*time.Second)
	if err != nil {
		t.Fatal("alternate outbox setup failed")
	}
	tx, err = f.db.BeginTx(f.ctx, nil)
	if err != nil {
		t.Fatal("alternate key transaction failed")
	}
	if err := otherOutbox.InitializeCodeKey(f.ctx, tx); !errors.Is(err, ErrOutbox) {
		_ = tx.Rollback()
		t.Fatal("different derivation key was accepted")
	}
	_ = tx.Rollback()
	otherIDOutbox, err := NewOutbox(f.outbox.deriver, "outbox-code-v2", f.hasher, f.identities, f.sender, 2*time.Second)
	if err != nil {
		t.Fatal("alternate key ID outbox setup failed")
	}
	tx, err = f.db.BeginTx(f.ctx, nil)
	if err != nil {
		t.Fatal("alternate key ID transaction failed")
	}
	if err := otherIDOutbox.InitializeCodeKey(f.ctx, tx); !errors.Is(err, ErrOutbox) {
		_ = tx.Rollback()
		t.Fatal("different key ID was accepted")
	}
	_ = tx.Rollback()

	// Start a REPEATABLE READ snapshot while the queue is empty. Another
	// transaction then records history and deletes the epoch. Initialization
	// must use current locking reads after the epoch lock and must not restore
	// the lost epoch from the stale snapshot.
	queuedID := f.issue("epoch-loss@example.test", false, "derive")
	staleTx, err := f.db.BeginTx(f.ctx, nil)
	if err != nil {
		t.Fatal("stale snapshot transaction failed")
	}
	var priorCount int
	if err := staleTx.QueryRowContext(f.ctx, `SELECT COUNT(*) FROM auth_email_outbox`).Scan(&priorCount); err != nil || priorCount != 0 {
		_ = staleTx.Rollback()
		t.Fatal("could not establish empty queue snapshot")
	}
	historyTx, err := f.db.BeginTx(f.ctx, nil)
	if err != nil {
		_ = staleTx.Rollback()
		t.Fatal("history transaction failed")
	}
	if _, err := historyTx.ExecContext(f.ctx, `DELETE FROM auth_email_code_key_epoch WHERE slot=1`); err != nil {
		_ = historyTx.Rollback()
		_ = staleTx.Rollback()
		t.Fatal("could not simulate lost epoch")
	}
	now := sqlUTC(time.Now().UTC())
	if _, err := historyTx.ExecContext(f.ctx, `INSERT INTO auth_email_outbox
		(challenge_id,code_digits,state,claim_token,lease_until,created_at,updated_at,terminal_at)
		VALUES (?,6,'pending',NULL,NULL,?,?,NULL)`, []byte(queuedID), now, now); err != nil {
		_ = historyTx.Rollback()
		_ = staleTx.Rollback()
		t.Fatal("could not create committed queue history")
	}
	if err := historyTx.Commit(); err != nil {
		_ = staleTx.Rollback()
		t.Fatal("history transaction commit failed")
	}
	if err := f.outbox.InitializeCodeKey(f.ctx, staleTx); !errors.Is(err, ErrOutbox) {
		_ = staleTx.Rollback()
		t.Fatal("old snapshot hid committed outbox history")
	}
	_ = staleTx.Rollback()
	for name, candidate := range map[string]*Outbox{"same key": f.outbox, "different key": otherOutbox} {
		tx, err = f.db.BeginTx(f.ctx, nil)
		if err != nil {
			t.Fatalf("%s historical-row transaction failed", name)
		}
		if err := candidate.InitializeCodeKey(f.ctx, tx); !errors.Is(err, ErrOutbox) {
			_ = tx.Rollback()
			t.Fatalf("missing epoch with history accepted %s", name)
		}
		_ = tx.Rollback()
	}
	var epochCount int
	if err := f.db.QueryRowContext(f.ctx, `SELECT COUNT(*) FROM auth_email_code_key_epoch`).Scan(&epochCount); err != nil || epochCount != 0 {
		t.Fatal("failed bootstrap recreated a missing historical epoch")
	}
	codeTag, err := f.outbox.deriver.KeyEpochTag()
	if err != nil {
		t.Fatal("fixture code epoch tag failed")
	}
	if _, err := f.db.ExecContext(f.ctx, `INSERT INTO auth_email_code_key_epoch(slot,key_id,key_tag) VALUES (1,?,?)`, []byte("outbox-code-v1"), codeTag[:]); err != nil {
		t.Fatal("could not restore isolated fixture epoch")
	}
	if f.state(queuedID) != "pending" {
		t.Fatal("epoch simulation changed queued state")
	}
	if _, err := f.db.ExecContext(f.ctx, `DELETE FROM auth_email_outbox WHERE challenge_id=?`, []byte(queuedID)); err != nil {
		t.Fatal("could not remove bootstrap test outbox row")
	}
	if _, err := f.db.ExecContext(f.ctx, `DELETE FROM auth_email_login_challenge WHERE challenge_id=?`, []byte(queuedID)); err != nil {
		t.Fatal("could not remove bootstrap test challenge")
	}
}

func TestEmailOutboxTransactionalStateMachine(t *testing.T) {
	f := openOutboxFixture(t)

	// Challenge and queue insertion are one transaction; a rollback leaves no
	// challenge, active slot, or outbox row behind.
	rolledBackID, err := domain.GenerateChallengeID()
	if err != nil {
		t.Fatal("challenge ID generation failed")
	}
	nonce, _ := domain.GenerateChallengeNonce()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	encode := base64.RawURLEncoding.EncodeToString
	public := domain.DevicePublicJWK{Kty: "EC", Crv: "P-256", X: encode(key.X.FillBytes(make([]byte, 32))), Y: encode(key.Y.FillBytes(make([]byte, 32)))}
	code, _ := f.outbox.deriver.Derive(rolledBackID, emailLoginPurpose, 6)
	tx, err := f.db.BeginTx(f.ctx, nil)
	if err != nil {
		t.Fatal("rollback fixture transaction failed")
	}
	epoch, err := f.outbox.LockEpochs(f.ctx, tx)
	if err != nil {
		t.Fatal("epoch lock failed")
	}
	if err := f.challenges.Issue(f.ctx, tx, challenge.IssueInput{Email: "rollback@example.test", ChallengeID: rolledBackID, Nonce: nonce, Code: code, PublicKey: public}); err != nil {
		t.Fatal("rollback challenge issue failed")
	}
	if err := f.outbox.Enqueue(f.ctx, tx, epoch, rolledBackID, 6); err != nil {
		t.Fatal("rollback enqueue failed")
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal("rollback failed")
	}
	var count int
	if err := f.db.QueryRowContext(f.ctx, `SELECT COUNT(*) FROM auth_email_login_challenge WHERE challenge_id=?`, []byte(rolledBackID)).Scan(&count); err != nil || count != 0 {
		t.Fatal("challenge survived rollback")
	}
	if err := f.db.QueryRowContext(f.ctx, `SELECT COUNT(*) FROM auth_email_outbox WHERE challenge_id=?`, []byte(rolledBackID)).Scan(&count); err != nil || count != 0 {
		t.Fatal("outbox survived rollback")
	}

	// A challenge whose saved digest does not match the derived code cannot be
	// queued; the caller can roll back issuance and expose no delivery result.
	badID, err := domain.GenerateChallengeID()
	if err != nil {
		t.Fatal("bad challenge ID generation failed")
	}
	badNonce, _ := domain.GenerateChallengeNonce()
	badKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	badPublic := domain.DevicePublicJWK{Kty: "EC", Crv: "P-256", X: encode(badKey.X.FillBytes(make([]byte, 32))), Y: encode(badKey.Y.FillBytes(make([]byte, 32)))}
	badTx, err := f.db.BeginTx(f.ctx, nil)
	if err != nil {
		t.Fatal("digest fixture transaction failed")
	}
	badEpoch, err := f.outbox.LockEpochs(f.ctx, badTx)
	if err != nil {
		t.Fatal("digest epoch lock failed")
	}
	if err := f.challenges.Issue(f.ctx, badTx, challenge.IssueInput{Email: "bad-digest@example.test", ChallengeID: badID, Nonce: badNonce, Code: "123456", PublicKey: badPublic}); err != nil {
		t.Fatal("digest challenge issue failed")
	}
	if err := f.outbox.Enqueue(f.ctx, badTx, badEpoch, badID, 6); err == nil {
		t.Fatal("mismatched stored digest was queued")
	}
	_ = badTx.Rollback()

	// Active-slot replacement wins over an already claimed old challenge. The
	// old claim becomes terminal and only the current challenge reaches sender.
	oldID := f.issue("slot@example.test", true, "derive")
	oldClaim, err := f.outbox.ClaimNext(f.ctx, f.db)
	if err != nil || oldClaim.challengeID != oldID {
		t.Fatal("could not claim first challenge")
	}
	newID := f.issue("slot@example.test", true, "derive")
	if _, err := f.outbox.Prepare(f.ctx, f.db, *oldClaim); err == nil {
		t.Fatal("superseded challenge was prepared for SMTP")
	}
	if got := f.state(oldID); got != "superseded" {
		t.Fatalf("old challenge state %q, want superseded", got)
	}
	state, err := f.outbox.SendOne(f.ctx, f.db)
	if err != nil || state != DeliveryAccepted {
		t.Fatalf("current challenge delivery failed: %s %v", state, err)
	}
	if got := f.state(newID); got != "accepted" {
		t.Fatalf("current challenge state %q, want accepted", got)
	}
	if calls, recipient, code := f.sender.snapshot(); calls != 1 || recipient != "slot@example.test" || len(code) != 6 {
		t.Fatal("sender did not receive only current synthetic challenge data")
	}

	// Key epoch mismatch fails closed before enqueue and before sending.
	epochID := f.issue("epoch@example.test", false, "derive")
	if _, err := f.db.ExecContext(f.ctx, `UPDATE auth_email_code_key_epoch SET key_tag=? WHERE slot=1`, make([]byte, 32)); err != nil {
		t.Fatal("epoch mutation fixture failed")
	}
	if err := f.enqueueExisting(epochID, 6); err == nil {
		t.Fatal("mismatched code-key epoch accepted")
	}
	tag, err := f.outbox.deriver.KeyEpochTag()
	if err != nil {
		t.Fatal("code epoch tag failed")
	}
	if _, err := f.db.ExecContext(f.ctx, `UPDATE auth_email_code_key_epoch SET key_tag=? WHERE slot=1`, tag[:]); err != nil {
		t.Fatal("epoch restoration failed")
	}
	_, identityTag, err := f.identities.KeyEpoch()
	if err != nil {
		t.Fatal("identity key epoch failed")
	}
	identityID := f.issue("identity-epoch@example.test", false, "derive")
	if _, err := f.db.ExecContext(f.ctx, `UPDATE auth_email_identity_key_epoch SET key_tag=? WHERE slot=1`, make([]byte, 32)); err != nil {
		t.Fatal("identity epoch mutation failed")
	}
	if err := f.enqueueExisting(identityID, 6); err == nil {
		t.Fatal("mismatched email identity epoch accepted")
	}
	if _, err := f.db.ExecContext(f.ctx, `UPDATE auth_email_identity_key_epoch SET key_tag=? WHERE slot=1`, identityTag[:]); err != nil {
		t.Fatal("identity epoch restoration failed")
	}

	// Corrupt authenticated ciphertext is rejected before SMTP preparation.
	tamperedID := f.issue("tampered@example.test", false, "derive")
	if _, err := f.db.ExecContext(f.ctx, `UPDATE auth_email_login_challenge SET email_ciphertext=? WHERE challenge_id=?`, []byte("tampered"), []byte(tamperedID)); err != nil {
		t.Fatal("ciphertext mutation fixture failed")
	}
	if err := f.enqueueExisting(tamperedID, 6); err == nil {
		t.Fatal("tampered encrypted email accepted")
	}

	// The digest is rechecked after claim as well as at enqueue; a corrupted
	// challenge is terminally failed and never reaches the sender.
	digestID := f.issue("tampered-digest@example.test", true, "derive")
	if _, err := f.db.ExecContext(f.ctx, `UPDATE auth_email_login_challenge SET code_digest=? WHERE challenge_id=?`, make([]byte, 32), []byte(digestID)); err != nil {
		t.Fatal("digest mutation fixture failed")
	}
	digestClaim, err := f.outbox.ClaimNext(f.ctx, f.db)
	if err != nil || digestClaim.challengeID != digestID {
		t.Fatal("corrupt digest claim missing")
	}
	if _, err := f.outbox.Prepare(f.ctx, f.db, *digestClaim); err == nil {
		t.Fatal("corrupted challenge digest prepared for send")
	}
	if got := f.state(digestID); got != "failed" {
		t.Fatalf("corrupted digest state %q, want failed", got)
	}

	// Expired or consumed challenges transition to terminal expiry without send.
	for _, mode := range []string{"expired", "consumed"} {
		id := f.issue(mode+"@example.test", true, "derive")
		if mode == "expired" {
			_, err = f.db.ExecContext(f.ctx, `UPDATE auth_email_login_challenge SET expires_at=? WHERE challenge_id=?`, sqlUTC(time.Now().UTC().Add(-time.Minute)), []byte(id))
		} else {
			_, err = f.db.ExecContext(f.ctx, `UPDATE auth_email_login_challenge SET consumed_at=? WHERE challenge_id=?`, sqlUTC(time.Now().UTC()), []byte(id))
		}
		if err != nil {
			t.Fatalf("could not prepare %s challenge", mode)
		}
		claim, err := f.outbox.ClaimNext(f.ctx, f.db)
		if err != nil || claim.challengeID != id {
			t.Fatalf("could not claim %s challenge: %v", mode, err)
		}
		if _, err := f.outbox.Prepare(f.ctx, f.db, *claim); err == nil {
			t.Fatalf("%s challenge prepared for send", mode)
		}
		if got := f.state(id); got != "expired" {
			t.Fatalf("%s challenge state %q, want expired", mode, got)
		}
	}

	// Claimed lease expiry is safe to reclaim; old fencing tokens cannot send.
	claimableID := f.issue("claim-recovery@example.test", true, "derive")
	firstClaim, err := f.outbox.ClaimNext(f.ctx, f.db)
	if err != nil || firstClaim.challengeID != claimableID {
		t.Fatal("initial claim missing")
	}
	f.outbox.now = func() time.Time { return time.Now().Add(3 * time.Second) }
	secondClaim, err := f.outbox.ClaimNext(f.ctx, f.db)
	if err != nil || secondClaim.challengeID != claimableID {
		t.Fatal("expired claimed lease was not recovered")
	}
	if _, err := f.outbox.Prepare(f.ctx, f.db, *firstClaim); !errors.Is(err, ErrLeaseLost) {
		t.Fatal("stale claimed token was not rejected")
	}
	delivery, err := f.outbox.Prepare(f.ctx, f.db, *secondClaim)
	if err != nil || delivery == nil {
		t.Fatal("reclaimed challenge could not be prepared")
	}

	// Once sending has begun, lease expiry becomes unknown and never pending;
	// a late completion is fenced out.
	unknownID := f.issue("unknown@example.test", true, "derive")
	// Finish the previous prepared delivery as accepted before continuing.
	f.outbox.now = time.Now
	if state, err := f.outbox.complete(f.ctx, f.db, *secondClaim, DeliveryAccepted); err != nil || state != DeliveryAccepted {
		t.Fatal("reclaimed send completion failed")
	}
	unknownClaim, err := f.outbox.ClaimNext(f.ctx, f.db)
	if err != nil || unknownClaim.challengeID != unknownID {
		t.Fatal("unknown scenario claim missing")
	}
	if _, err := f.outbox.Prepare(f.ctx, f.db, *unknownClaim); err != nil {
		t.Fatal("unknown scenario prepare failed")
	}
	f.outbox.now = func() time.Time { return time.Now().Add(3 * time.Second) }
	if n, err := f.outbox.RecoverExpiredSending(f.ctx, f.db, 10); err != nil || n != 1 {
		t.Fatalf("expired sending recovery returned %d, %v", n, err)
	}
	if got := f.state(unknownID); got != "unknown" {
		t.Fatalf("expired send state %q, want unknown", got)
	}
	if _, err := f.outbox.complete(f.ctx, f.db, *unknownClaim, DeliveryAccepted); !errors.Is(err, ErrLeaseLost) {
		t.Fatal("late SMTP response overwrote unknown state")
	}
	if got := f.state(unknownID); got != "unknown" {
		t.Fatal("late SMTP response changed terminal state")
	}
	if _, err := f.outbox.ClaimNext(f.ctx, f.db); !errors.Is(err, ErrNoPending) {
		t.Fatal("unknown delivery became eligible for automatic retry")
	}

	// Explicit SMTP rejection and indeterminate submission map to terminal
	// failed/unknown states; error strings never escape this package.
	for _, tc := range []struct {
		name    string
		sendErr error
		want    DeliveryState
	}{
		{"accepted", nil, DeliveryAccepted}, {"rejected", mail.ErrDeliveryFailed, DeliveryFailed}, {"uncertain", mail.ErrDeliveryUncertain, DeliveryUnknown},
	} {
		f.outbox.now = time.Now
		id := f.issue(tc.name+"@example.test", true, "derive")
		f.sender.setError(tc.sendErr)
		state, err := f.outbox.SendOne(f.ctx, f.db)
		if err != nil || state != tc.want {
			t.Fatalf("%s delivery outcome %q, %v; want %q", tc.name, state, err, tc.want)
		}
		if got := f.state(id); got != string(tc.want) {
			t.Fatalf("%s state %q, want %q", tc.name, got, tc.want)
		}
	}
}

type gatedSender struct {
	started chan context.Context
	release chan error
}

func (s *gatedSender) SendVerificationCode(ctx context.Context, _, _ string) error {
	s.started <- ctx
	return <-s.release
}

func TestEmailOutboxPausedWorkerDoesNotSendAfterLeaseRecovery(t *testing.T) {
	f := openOutboxFixture(t)
	id := f.issue("paused@example.test", true, "derive")
	claim, err := f.outbox.ClaimNext(f.ctx, f.db)
	if err != nil || claim.challengeID != id {
		t.Fatal("paused scenario claim missing")
	}
	delivery, err := f.outbox.Prepare(f.ctx, f.db, *claim)
	if err != nil || delivery == nil {
		t.Fatal("paused scenario prepare failed")
	}
	if !delivery.leaseUntil.After(time.Now()) {
		t.Fatal("prepared delivery has no future lease")
	}
	f.outbox.now = func() time.Time { return delivery.leaseUntil.Add(time.Second) }
	if n, err := f.outbox.RecoverExpiredSending(f.ctx, f.db, 10); err != nil || n != 1 {
		t.Fatalf("recovery returned %d, %v", n, err)
	}
	before, _, _ := f.sender.snapshot()
	state, err := f.outbox.sendPrepared(f.ctx, f.db, delivery)
	if err != nil || state != DeliveryUnknown {
		t.Fatalf("paused worker result %q, %v; want unknown", state, err)
	}
	after, _, _ := f.sender.snapshot()
	if after != before {
		t.Fatal("paused worker sent after its lease was recovered")
	}
	if got := f.state(id); got != string(DeliveryUnknown) {
		t.Fatalf("recovered delivery state %q, want unknown", got)
	}
}

func TestEmailOutboxExpiredCompletionReturnsPersistedUnknown(t *testing.T) {
	f := openOutboxFixture(t)
	id := f.issue("completion-expired@example.test", true, "derive")
	claim, err := f.outbox.ClaimNext(f.ctx, f.db)
	if err != nil || claim.challengeID != id {
		t.Fatal("completion scenario claim missing")
	}
	delivery, err := f.outbox.Prepare(f.ctx, f.db, *claim)
	if err != nil || delivery == nil {
		t.Fatal("completion scenario prepare failed")
	}
	gate := &gatedSender{started: make(chan context.Context, 1), release: make(chan error, 1)}
	f.outbox.sender = gate
	result := make(chan struct {
		state DeliveryState
		err   error
	}, 1)
	go func() {
		state, err := f.outbox.sendPrepared(f.ctx, f.db, delivery)
		result <- struct {
			state DeliveryState
			err   error
		}{state: state, err: err}
	}()
	select {
	case sendCtx := <-gate.started:
		deadline, ok := sendCtx.Deadline()
		if !ok || !deadline.Equal(delivery.leaseUntil) {
			t.Fatal("sender context is not bounded by persisted lease")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("sender was not called")
	}
	f.outbox.now = func() time.Time { return delivery.leaseUntil.Add(time.Second) }
	gate.release <- nil // SMTP reports accepted after the database lease elapsed.
	got := <-result
	if got.err != nil || got.state != DeliveryUnknown {
		t.Fatalf("expired completion returned %q, %v; want persisted unknown", got.state, got.err)
	}
	if state := f.state(id); state != string(DeliveryUnknown) {
		t.Fatalf("database state %q, want unknown", state)
	}
}

func TestEmailOutboxConcurrentClaimsDoNotDuplicate(t *testing.T) {
	f := openOutboxFixture(t)
	idA := f.issue("claim-a@example.test", true, "derive")
	idB := f.issue("claim-b@example.test", true, "derive")
	for _, id := range []string{idA, idB} {
		if got := f.state(id); got != "pending" {
			t.Fatalf("new concurrent-claim fixture %s has state %s, want pending", id, got)
		}
	}
	start := make(chan struct{})
	type claimResult struct {
		claim *Claim
		err   error
	}
	results := make(chan claimResult, 2)
	for i := 0; i < 2; i++ {
		go func() {
			<-start
			c, err := f.outbox.ClaimNext(f.ctx, f.db)
			results <- claimResult{claim: c, err: err}
		}()
	}
	close(start)
	claimed := map[string]bool{}
	pendingRetries := 0
	for i := 0; i < 2; i++ {
		result := <-results
		c, err := result.claim, result.err
		if errors.Is(err, ErrNoPending) {
			// SKIP LOCKED may observe the first worker's transient scan/lock
			// window as empty. A worker poll on a fresh transaction must then
			// claim the remaining row; this is not a duplicate success.
			pendingRetries++
			continue
		}
		if err != nil || c == nil {
			t.Fatalf("concurrent claim failed: %v (claim=%v, issue IDs=%s,%s)", err, c, idA, idB)
		}
		if claimed[c.challengeID] {
			t.Fatal("same pending challenge was claimed twice")
		}
		claimed[c.challengeID] = true
	}
	for claimedCount := len(claimed); claimedCount < 2 && pendingRetries > 0; claimedCount++ {
		c, err := f.outbox.ClaimNext(f.ctx, f.db)
		if err != nil || c == nil {
			t.Fatalf("fresh-transaction retry after transient no-pending failed: %v", err)
		}
		if claimed[c.challengeID] {
			t.Fatal("retry claimed a challenge twice")
		}
		claimed[c.challengeID] = true
		pendingRetries--
	}
	if !claimed[idA] || !claimed[idB] {
		states, _ := f.db.QueryContext(f.ctx, `SELECT challenge_id,state FROM auth_email_outbox ORDER BY created_at,challenge_id`)
		if states != nil {
			defer states.Close()
			for states.Next() {
				var id []byte
				var state string
				if states.Scan(&id, &state) == nil {
					t.Logf("outbox state after concurrent claims: id=%s state=%s", id, state)
				}
			}
		}
		t.Fatalf("concurrent claim set incomplete: want %s and %s, got %v", idA, idB, claimed)
	}
}

func TestEmailOutboxValuesAreRedacted(t *testing.T) {
	claim := Claim{challengeID: "Q1w2E3r4T5y6U7i8O9p0Aa"}
	claim.token[0] = 1
	delivery := Delivery{claim: claim, recipient: "private@example.test", code: "012345"}
	for _, value := range []any{Outbox{}, claim, EpochLock{}, delivery} {
		formatted := fmt.Sprintf("%+v %#v", value, value)
		if strings.Contains(formatted, "private@example.test") || strings.Contains(formatted, "012345") {
			t.Fatal("sensitive outbox material was formatted")
		}
		if _, err := json.Marshal(value); err == nil {
			t.Fatal("sensitive outbox value serialized")
		}
		var log strings.Builder
		slog.New(slog.NewJSONHandler(&log, nil)).Info("outbox", "value", value)
		if strings.Contains(log.String(), "private@example.test") || strings.Contains(log.String(), "012345") {
			t.Fatal("sensitive outbox material was logged")
		}
	}
}
