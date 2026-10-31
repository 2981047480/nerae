package issuance

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/subtle"
	"database/sql"
	"errors"
	"io"
	"log/slog"
	"time"

	"nerae/api/internal/modules/auth/domain"
	"nerae/api/internal/modules/auth/mail"
)

var (
	ErrOutbox            = errors.New("email outbox unavailable")
	ErrNoPending         = errors.New("no pending email delivery")
	ErrLeaseLost         = errors.New("email delivery lease unavailable")
	errChallengeExpired  = errors.New("email challenge expired")
	errChallengeConsumed = errors.New("email challenge consumed")
)

const emailLoginPurpose = "email_login"

type CodeSender interface {
	SendVerificationCode(context.Context, string, string) error
}

type Outbox struct {
	deriver    *Deriver
	codeKeyID  string
	hasher     *domain.EmailCodeHasher
	identities *domain.EmailIdentityProtector
	sender     CodeSender
	lease      time.Duration
	now        func() time.Time
}

func NewOutbox(deriver *Deriver, codeKeyID string, hasher *domain.EmailCodeHasher, identities *domain.EmailIdentityProtector, sender CodeSender, lease time.Duration) (*Outbox, error) {
	if deriver == nil || hasher == nil || identities == nil || sender == nil || !validOutboxKeyID(codeKeyID) ||
		lease < time.Second || lease > 10*time.Minute || lease%time.Microsecond != 0 {
		return nil, ErrOutbox
	}
	if _, err := deriver.KeyEpochTag(); err != nil {
		return nil, ErrOutbox
	}
	return &Outbox{deriver: deriver, codeKeyID: codeKeyID, hasher: hasher, identities: identities,
		sender: sender, lease: lease, now: time.Now}, nil
}

// NewEnqueueOutbox creates the producer side of the queue without granting it
// a mail sender. It may initialize/lock the epoch and enqueue challenges, but
// cannot claim, prepare, recover or send deliveries. Only a worker configured
// with NewOutbox may perform those operations.
func NewEnqueueOutbox(deriver *Deriver, codeKeyID string, hasher *domain.EmailCodeHasher, identities *domain.EmailIdentityProtector) (*Outbox, error) {
	if deriver == nil || hasher == nil || identities == nil || !validOutboxKeyID(codeKeyID) {
		return nil, ErrOutbox
	}
	if _, err := deriver.KeyEpochTag(); err != nil {
		return nil, ErrOutbox
	}
	return &Outbox{deriver: deriver, codeKeyID: codeKeyID, hasher: hasher, identities: identities, now: time.Now}, nil
}

func (Outbox) String() string               { return "EmailOutbox{redacted}" }
func (Outbox) GoString() string             { return "EmailOutbox{redacted}" }
func (Outbox) MarshalJSON() ([]byte, error) { return nil, ErrOutbox }
func (Outbox) LogValue() slog.Value         { return slog.StringValue("EmailOutbox{redacted}") }

type Claim struct {
	challengeID string
	token       [32]byte
}

// EpochLock proves both the identity and code-key epochs were checked and held
// FOR SHARE in tx before the caller advances the email slot/challenge locks.
type EpochLock struct {
	outbox *Outbox
	tx     *sql.Tx
}

func (EpochLock) String() string               { return "EmailOutboxEpochLock{redacted}" }
func (EpochLock) GoString() string             { return "EmailOutboxEpochLock{redacted}" }
func (EpochLock) MarshalJSON() ([]byte, error) { return nil, ErrOutbox }
func (EpochLock) LogValue() slog.Value         { return slog.StringValue("EmailOutboxEpochLock{redacted}") }

func (Claim) String() string               { return "EmailOutboxClaim{redacted}" }
func (Claim) GoString() string             { return "EmailOutboxClaim{redacted}" }
func (Claim) MarshalJSON() ([]byte, error) { return nil, ErrOutbox }
func (Claim) LogValue() slog.Value         { return slog.StringValue("EmailOutboxClaim{redacted}") }

type Delivery struct {
	claim      Claim
	recipient  string
	code       string
	leaseUntil time.Time
}

func (Delivery) String() string               { return "EmailDelivery{redacted}" }
func (Delivery) GoString() string             { return "EmailDelivery{redacted}" }
func (Delivery) MarshalJSON() ([]byte, error) { return nil, ErrOutbox }
func (Delivery) LogValue() slog.Value         { return slog.StringValue("EmailDelivery{redacted}") }

type DeliveryState string

const (
	DeliveryAccepted   DeliveryState = "accepted"
	DeliveryFailed     DeliveryState = "failed"
	DeliveryUnknown    DeliveryState = "unknown"
	DeliverySuperseded DeliveryState = "superseded"
	DeliveryExpired    DeliveryState = "expired"
)

func (s *Outbox) InitializeCodeKey(ctx context.Context, tx *sql.Tx) error {
	if s == nil || tx == nil || ctx == nil || s.deriver == nil || !validOutboxKeyID(s.codeKeyID) {
		return ErrOutbox
	}
	tag, err := s.deriver.KeyEpochTag()
	if err != nil {
		return ErrOutbox
	}
	var storedID, storedTag []byte
	err = tx.QueryRowContext(ctx, `SELECT key_id,key_tag FROM auth_email_code_key_epoch WHERE slot=1 FOR UPDATE`).Scan(&storedID, &storedTag)
	if err == nil {
		if string(storedID) != s.codeKeyID || !hmac.Equal(storedTag, tag[:]) {
			return ErrOutbox
		}
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return ErrOutbox
	}
	// Use a locking/current read after locking the epoch first. A prior
	// consistent read in this transaction must not hide newly committed queue
	// history if the epoch row was lost. Concurrent first-time initialization
	// can deadlock on empty gap locks; callers must roll back and retry in a new
	// transaction rather than treating that failure as success.
	var queuedChallenge []byte
	err = tx.QueryRowContext(ctx, `SELECT challenge_id FROM auth_email_outbox LIMIT 1 FOR UPDATE`).Scan(&queuedChallenge)
	if err == nil || !errors.Is(err, sql.ErrNoRows) {
		return ErrOutbox
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO auth_email_code_key_epoch (slot,key_id,key_tag) VALUES (1,?,?)`, []byte(s.codeKeyID), tag[:]); err != nil {
		return ErrOutbox
	}
	return s.verifyCodeKeyEpoch(ctx, tx, s.codeKeyID, tag[:])
}

func (s *Outbox) verifyCodeKeyEpoch(ctx context.Context, tx *sql.Tx, keyID string, tag []byte) error {
	var storedID, storedTag []byte
	err := tx.QueryRowContext(ctx, `SELECT key_id,key_tag FROM auth_email_code_key_epoch WHERE slot=1 FOR UPDATE`).Scan(&storedID, &storedTag)
	if err != nil || string(storedID) != keyID || !hmac.Equal(storedTag, tag) {
		return ErrOutbox
	}
	return nil
}

// LockEpochs must run before challenge.Issue in the caller's transaction. It
// establishes the global epoch→slot→challenge→outbox lock order and prevents
// an epoch change between challenge issuance and queue insertion.
func (s *Outbox) LockEpochs(ctx context.Context, tx *sql.Tx) (*EpochLock, error) {
	if s == nil || tx == nil || ctx == nil {
		return nil, ErrOutbox
	}
	if err := s.lockEpochs(ctx, tx); err != nil {
		return nil, ErrOutbox
	}
	return &EpochLock{outbox: s, tx: tx}, nil
}

// Enqueue inserts only a challenge reference and fixed code length. It must be
// called in the same transaction as challenge issuance; its locking reads make
// an earlier slot advancement invalidate stale enqueue attempts.
func (s *Outbox) Enqueue(ctx context.Context, tx *sql.Tx, epoch *EpochLock, challengeID string, digits int) error {
	if s == nil || tx == nil || epoch == nil || epoch.outbox != s || epoch.tx != tx || ctx == nil || !validChallengeID(challengeID) || digits < 6 || digits > 10 || s.now == nil {
		return ErrOutbox
	}
	index, err := s.challengeIndex(ctx, tx, challengeID)
	if err != nil {
		return ErrOutbox
	}
	if err := s.lockSlot(ctx, tx, index, challengeID); err != nil {
		return ErrOutbox
	}
	meta, err := s.lockChallenge(ctx, tx, challengeID)
	if err != nil || s.validateChallenge(meta, challengeID, digits) != nil {
		return ErrOutbox
	}
	var oldDigits int
	err = tx.QueryRowContext(ctx, `SELECT code_digits FROM auth_email_outbox WHERE challenge_id=? FOR UPDATE`, challengeID).Scan(&oldDigits)
	if err == nil {
		if oldDigits != digits {
			return ErrOutbox
		}
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return ErrOutbox
	}
	now := s.utcNow()
	if now.IsZero() {
		return ErrOutbox
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO auth_email_outbox
		(challenge_id,code_digits,state,claim_token,lease_until,created_at,updated_at,terminal_at)
		VALUES (?,?,'pending',NULL,NULL,?,?,NULL)`, []byte(challengeID), digits, sqlUTC(now), sqlUTC(now))
	if err != nil {
		return ErrOutbox
	}
	return nil
}

// ClaimNext commits a short claim transaction. Expired claimed leases may be
// reclaimed because no SMTP call begins before the row is changed to sending.
func (s *Outbox) ClaimNext(ctx context.Context, db *sql.DB) (*Claim, error) {
	if s == nil || s.sender == nil || db == nil || ctx == nil || s.now == nil {
		return nil, ErrOutbox
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, ErrOutbox
	}
	defer tx.Rollback()
	if err := s.lockEpochs(ctx, tx); err != nil {
		return nil, ErrOutbox
	}
	now := s.utcNow()
	if now.IsZero() {
		return nil, ErrOutbox
	}
	var rawID []byte
	err = tx.QueryRowContext(ctx, `SELECT challenge_id FROM auth_email_outbox
		WHERE state='pending' OR (state='claimed' AND lease_until<=?)
		ORDER BY created_at,challenge_id LIMIT 1 FOR UPDATE SKIP LOCKED`, sqlUTC(now)).Scan(&rawID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNoPending
	}
	if err != nil || len(rawID) == 0 || len(rawID) > 128 {
		return nil, ErrOutbox
	}
	claim, err := newClaim(string(rawID))
	if err != nil {
		return nil, ErrOutbox
	}
	result, err := tx.ExecContext(ctx, `UPDATE auth_email_outbox SET state='claimed',claim_token=?,lease_until=?,updated_at=?,terminal_at=NULL
		WHERE challenge_id=? AND (state='pending' OR (state='claimed' AND lease_until<=?))`, claim.token[:], sqlUTC(now.Add(s.lease)), sqlUTC(now), rawID, sqlUTC(now))
	if err != nil {
		return nil, ErrOutbox
	}
	if n, err := result.RowsAffected(); err != nil || n != 1 {
		return nil, ErrLeaseLost
	}
	if err := tx.Commit(); err != nil {
		return nil, ErrOutbox
	}
	return &claim, nil
}

// Prepare locks epoch→slot→challenge→outbox, validates all server-side state,
// persists sending, and commits before any external SMTP call.
func (s *Outbox) Prepare(ctx context.Context, db *sql.DB, claim Claim) (*Delivery, error) {
	if s == nil || s.sender == nil || db == nil || ctx == nil || !validChallengeID(claim.challengeID) || claim.token == [32]byte{} || s.now == nil {
		return nil, ErrOutbox
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, ErrOutbox
	}
	defer tx.Rollback()
	if err := s.lockEpochs(ctx, tx); err != nil {
		return nil, ErrOutbox
	}
	index, err := s.challengeIndex(ctx, tx, claim.challengeID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, s.terminalizeMissingChallenge(ctx, tx, claim)
	}
	if err != nil {
		return nil, ErrOutbox
	}
	active, slotErr := s.activeSlot(ctx, tx, index)
	if slotErr != nil && !errors.Is(slotErr, sql.ErrNoRows) {
		return nil, ErrOutbox
	}
	meta, challengeErr := s.lockChallenge(ctx, tx, claim.challengeID)
	if challengeErr != nil {
		return nil, ErrOutbox
	}
	var digits int
	var state string
	var token []byte
	var leaseRaw string
	err = tx.QueryRowContext(ctx, `SELECT code_digits,state,claim_token,CAST(lease_until AS CHAR)
		FROM auth_email_outbox WHERE challenge_id=? FOR UPDATE`, []byte(claim.challengeID)).Scan(&digits, &state, &token, &leaseRaw)
	if err != nil || state != "claimed" || !hmac.Equal(token, claim.token[:]) {
		return nil, ErrLeaseLost
	}
	leaseUntil, parseErr := parseOutboxTime(leaseRaw)
	now := s.utcNow()
	if parseErr != nil || now.IsZero() || !leaseUntil.After(now) {
		return nil, ErrLeaseLost
	}
	if errors.Is(slotErr, sql.ErrNoRows) || active != claim.challengeID {
		if err := s.finishLocked(ctx, tx, claim, "superseded", now); err != nil {
			return nil, err
		}
		if err := tx.Commit(); err != nil {
			return nil, ErrOutbox
		}
		return nil, ErrOutbox
	}
	if err := s.validateChallenge(meta, claim.challengeID, digits); err != nil {
		terminal := "failed"
		if errors.Is(err, errChallengeExpired) || errors.Is(err, errChallengeConsumed) {
			terminal = "expired"
		}
		if finishErr := s.finishLocked(ctx, tx, claim, terminal, now); finishErr != nil {
			return nil, finishErr
		}
		if commitErr := tx.Commit(); commitErr != nil {
			return nil, ErrOutbox
		}
		return nil, ErrOutbox
	}
	recipient, err := s.identities.Open(meta.index, meta.ciphertext)
	if err != nil {
		return nil, ErrOutbox
	}
	code, err := s.deriver.Derive(claim.challengeID, emailLoginPurpose, digits)
	if err != nil {
		return nil, ErrOutbox
	}
	leaseUntil = now.Add(s.lease).UTC().Truncate(time.Microsecond)
	result, err := tx.ExecContext(ctx, `UPDATE auth_email_outbox SET state='sending',lease_until=?,updated_at=?
		WHERE challenge_id=? AND state='claimed' AND claim_token=?`, sqlUTC(leaseUntil), sqlUTC(now), []byte(claim.challengeID), claim.token[:])
	if err != nil {
		return nil, ErrOutbox
	}
	if n, err := result.RowsAffected(); err != nil || n != 1 {
		return nil, ErrLeaseLost
	}
	if err := tx.Commit(); err != nil {
		return nil, ErrOutbox
	}
	return &Delivery{claim: claim, recipient: recipient, code: code, leaseUntil: leaseUntil}, nil
}

// SendOne performs no SMTP call until Prepare's sending state has committed.
// Every sender error is reduced to a state category; its text is never returned.
func (s *Outbox) SendOne(ctx context.Context, db *sql.DB) (DeliveryState, error) {
	claim, err := s.ClaimNext(ctx, db)
	if err != nil {
		return "", err
	}
	delivery, err := s.Prepare(ctx, db, *claim)
	if err != nil {
		return "", err
	}
	return s.sendPrepared(ctx, db, delivery)
}

// sendPrepared binds the SMTP attempt to the persisted lease. The check and
// context deadline prevent a paused worker from starting after recovery has
// expired the claim; SMTP acceptance itself cannot be made atomic with MySQL.
func (s *Outbox) sendPrepared(ctx context.Context, db *sql.DB, delivery *Delivery) (DeliveryState, error) {
	if s == nil || s.sender == nil || db == nil || ctx == nil || delivery == nil || delivery.leaseUntil.IsZero() || s.now == nil {
		return "", ErrOutbox
	}
	if !s.utcNow().Before(delivery.leaseUntil) {
		state, err := s.complete(ctx, db, delivery.claim, DeliveryUnknown)
		if errors.Is(err, ErrLeaseLost) {
			return DeliveryUnknown, nil
		}
		return state, err
	}
	sendCtx, cancel := context.WithDeadline(ctx, delivery.leaseUntil)
	defer cancel()
	if err := sendCtx.Err(); err != nil {
		state, completeErr := s.complete(ctx, db, delivery.claim, DeliveryUnknown)
		if errors.Is(completeErr, ErrLeaseLost) {
			return DeliveryUnknown, nil
		}
		return state, completeErr
	}
	sendErr := s.sender.SendVerificationCode(sendCtx, delivery.recipient, delivery.code)
	return s.complete(ctx, db, delivery.claim, classifyDelivery(sendErr))
}

// RecoverExpiredSending converts in-flight leases to unknown. It never makes
// them eligible for another SMTP attempt.
func (s *Outbox) RecoverExpiredSending(ctx context.Context, db *sql.DB, limit int) (int64, error) {
	if s == nil || s.sender == nil || db == nil || ctx == nil || limit < 1 || limit > 500 || s.now == nil {
		return 0, ErrOutbox
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, ErrOutbox
	}
	defer tx.Rollback()
	now := s.utcNow()
	if now.IsZero() {
		return 0, ErrOutbox
	}
	rows, err := tx.QueryContext(ctx, `SELECT challenge_id,claim_token FROM auth_email_outbox
		WHERE state='sending' AND lease_until<=? ORDER BY lease_until,challenge_id LIMIT ? FOR UPDATE SKIP LOCKED`, sqlUTC(now), limit)
	if err != nil {
		return 0, ErrOutbox
	}
	type expired struct{ id, token []byte }
	var list []expired
	for rows.Next() {
		var item expired
		if err := rows.Scan(&item.id, &item.token); err != nil {
			rows.Close()
			return 0, ErrOutbox
		}
		list = append(list, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, ErrOutbox
	}
	rows.Close()
	for _, item := range list {
		_, err := tx.ExecContext(ctx, `UPDATE auth_email_outbox SET state='unknown',claim_token=NULL,lease_until=NULL,updated_at=?,terminal_at=?
			WHERE challenge_id=? AND state='sending' AND claim_token=? AND lease_until<=?`, sqlUTC(now), sqlUTC(now), item.id, item.token, sqlUTC(now))
		if err != nil {
			return 0, ErrOutbox
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, ErrOutbox
	}
	return int64(len(list)), nil
}

func (s *Outbox) complete(ctx context.Context, db *sql.DB, claim Claim, state DeliveryState) (DeliveryState, error) {
	if state != DeliveryAccepted && state != DeliveryFailed && state != DeliveryUnknown {
		return "", ErrOutbox
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return "", ErrOutbox
	}
	defer tx.Rollback()
	now := s.utcNow()
	if now.IsZero() {
		return "", ErrOutbox
	}
	var leaseRaw string
	err = tx.QueryRowContext(ctx, `SELECT CAST(lease_until AS CHAR) FROM auth_email_outbox WHERE challenge_id=? AND state='sending' AND claim_token=? FOR UPDATE`, []byte(claim.challengeID), claim.token[:]).Scan(&leaseRaw)
	if err != nil {
		return "", ErrLeaseLost
	}
	leaseUntil, err := parseOutboxTime(leaseRaw)
	if err != nil {
		return "", ErrOutbox
	}
	if !leaseUntil.After(now) {
		state = DeliveryUnknown
	}
	_, err = tx.ExecContext(ctx, `UPDATE auth_email_outbox SET state=?,claim_token=NULL,lease_until=NULL,updated_at=?,terminal_at=?
		WHERE challenge_id=? AND state='sending' AND claim_token=?`, string(state), sqlUTC(now), sqlUTC(now), []byte(claim.challengeID), claim.token[:])
	if err != nil {
		return "", ErrOutbox
	}
	if err := tx.Commit(); err != nil {
		return "", ErrOutbox
	}
	return state, nil
}

func (s *Outbox) lockEpochs(ctx context.Context, tx *sql.Tx) error {
	_, identityTag, err := s.identities.KeyEpoch()
	if err != nil {
		return ErrOutbox
	}
	var storedIdentity []byte
	if err := tx.QueryRowContext(ctx, `SELECT key_tag FROM auth_email_identity_key_epoch WHERE slot=1 FOR SHARE`).Scan(&storedIdentity); err != nil || !hmac.Equal(storedIdentity, identityTag[:]) {
		return ErrOutbox
	}
	codeTag, err := s.deriver.KeyEpochTag()
	if err != nil {
		return ErrOutbox
	}
	var codeID, storedCode []byte
	if err := tx.QueryRowContext(ctx, `SELECT key_id,key_tag FROM auth_email_code_key_epoch WHERE slot=1 FOR SHARE`).Scan(&codeID, &storedCode); err != nil || string(codeID) != s.codeKeyID || subtle.ConstantTimeCompare(storedCode, codeTag[:]) != 1 {
		return ErrOutbox
	}
	return nil
}

type challengeMeta struct {
	index      [32]byte
	ciphertext []byte
	keyID      []byte
	digest     []byte
	expires    time.Time
	consumed   bool
}

func (s *Outbox) challengeIndex(ctx context.Context, tx *sql.Tx, id string) ([32]byte, error) {
	var result [32]byte
	var raw []byte
	err := tx.QueryRowContext(ctx, `SELECT email_index FROM auth_email_login_challenge WHERE challenge_id=?`, []byte(id)).Scan(&raw)
	if err != nil {
		return result, err
	}
	if len(raw) != 32 {
		return result, ErrOutbox
	}
	copy(result[:], raw)
	return result, nil
}

func (s *Outbox) activeSlot(ctx context.Context, tx *sql.Tx, index [32]byte) (string, error) {
	var raw []byte
	err := tx.QueryRowContext(ctx, `SELECT challenge_id FROM auth_email_login_slot WHERE email_index=? FOR UPDATE`, index[:]).Scan(&raw)
	return string(raw), err
}

func (s *Outbox) lockSlot(ctx context.Context, tx *sql.Tx, index [32]byte, id string) error {
	active, err := s.activeSlot(ctx, tx, index)
	if err != nil || active != id {
		return ErrOutbox
	}
	return nil
}

func (s *Outbox) lockChallenge(ctx context.Context, tx *sql.Tx, id string) (challengeMeta, error) {
	var meta challengeMeta
	var index, keyID []byte
	var expiryText string
	err := tx.QueryRowContext(ctx, `SELECT email_index,email_ciphertext,email_key_id,code_digest,CAST(expires_at AS CHAR),consumed_at IS NOT NULL
		FROM auth_email_login_challenge WHERE challenge_id=? FOR UPDATE`, []byte(id)).Scan(&index, &meta.ciphertext, &keyID, &meta.digest, &expiryText, &meta.consumed)
	if err != nil || len(index) != 32 || len(meta.digest) != 32 {
		return challengeMeta{}, ErrOutbox
	}
	copy(meta.index[:], index)
	meta.keyID = keyID
	meta.expires, err = parseOutboxTime(expiryText)
	if err != nil {
		return challengeMeta{}, ErrOutbox
	}
	return meta, nil
}

func (s *Outbox) validateChallenge(meta challengeMeta, id string, digits int) error {
	if meta.consumed {
		return errChallengeConsumed
	}
	if !meta.expires.After(s.utcNow()) {
		return errChallengeExpired
	}
	if meta.index == [32]byte{} || len(meta.ciphertext) == 0 || len(meta.digest) != 32 || string(meta.keyID) != identityKeyID(s.identities) {
		return ErrOutbox
	}
	if _, err := s.identities.Open(meta.index, meta.ciphertext); err != nil {
		return ErrOutbox
	}
	code, err := s.deriver.Derive(id, emailLoginPurpose, digits)
	if err != nil {
		return ErrOutbox
	}
	digest, err := s.hasher.Digest(id, emailLoginPurpose, code)
	if err != nil || !hmac.Equal(digest, meta.digest) {
		return ErrOutbox
	}
	return nil
}

func (s *Outbox) finishLocked(ctx context.Context, tx *sql.Tx, claim Claim, state string, now time.Time) error {
	if state != "superseded" && state != "expired" && state != "failed" {
		return ErrOutbox
	}
	result, err := tx.ExecContext(ctx, `UPDATE auth_email_outbox SET state=?,claim_token=NULL,lease_until=NULL,updated_at=?,terminal_at=? WHERE challenge_id=? AND state='claimed' AND claim_token=?`, state, sqlUTC(now), sqlUTC(now), []byte(claim.challengeID), claim.token[:])
	if err != nil {
		return ErrOutbox
	}
	if n, err := result.RowsAffected(); err != nil || n != 1 {
		return ErrLeaseLost
	}
	return nil
}

func (s *Outbox) terminalizeMissingChallenge(ctx context.Context, tx *sql.Tx, claim Claim) error {
	var state string
	var token []byte
	err := tx.QueryRowContext(ctx, `SELECT state,claim_token FROM auth_email_outbox WHERE challenge_id=? FOR UPDATE`, []byte(claim.challengeID)).Scan(&state, &token)
	if err != nil || state != "claimed" || !hmac.Equal(token, claim.token[:]) {
		return ErrLeaseLost
	}
	now := s.utcNow()
	if now.IsZero() {
		return ErrOutbox
	}
	_, err = tx.ExecContext(ctx, `UPDATE auth_email_outbox SET state='expired',claim_token=NULL,lease_until=NULL,updated_at=?,terminal_at=? WHERE challenge_id=? AND state='claimed' AND claim_token=?`, sqlUTC(now), sqlUTC(now), []byte(claim.challengeID), claim.token[:])
	if err != nil {
		return ErrOutbox
	}
	if err := tx.Commit(); err != nil {
		return ErrOutbox
	}
	return ErrOutbox
}

func newClaim(id string) (Claim, error) {
	var c Claim
	if !validChallengeID(id) {
		return c, ErrOutbox
	}
	c.challengeID = id
	if _, err := io.ReadFull(rand.Reader, c.token[:]); err != nil {
		return Claim{}, ErrOutbox
	}
	return c, nil
}

func classifyDelivery(err error) DeliveryState {
	if err == nil {
		return DeliveryAccepted
	}
	if errors.Is(err, mail.ErrDeliveryUncertain) {
		return DeliveryUnknown
	}
	if errors.Is(err, mail.ErrDeliveryFailed) || errors.Is(err, mail.ErrDeliveryCancelled) || errors.Is(err, mail.ErrInvalidMessage) || errors.Is(err, mail.ErrInvalidConfiguration) {
		return DeliveryFailed
	}
	return DeliveryUnknown
}

func (s *Outbox) utcNow() time.Time {
	if s == nil || s.now == nil {
		return time.Time{}
	}
	return s.now().UTC().Truncate(time.Microsecond)
}

func identityKeyID(p *domain.EmailIdentityProtector) string {
	id, _, err := p.KeyEpoch()
	if err != nil {
		return ""
	}
	return id
}

func parseOutboxTime(value string) (time.Time, error) {
	return time.ParseInLocation("2006-01-02 15:04:05.999999", value, time.UTC)
}

func validOutboxKeyID(value string) bool {
	if len(value) < 1 || len(value) > 64 {
		return false
	}
	for _, c := range value {
		if !((c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-' || c == '_' || c == '.') {
			return false
		}
	}
	return true
}

func sqlUTC(value time.Time) string {
	return value.UTC().Truncate(time.Microsecond).Format("2006-01-02 15:04:05.000000")
}
