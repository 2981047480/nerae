package approval

import (
	"context"
	"crypto/hmac"
	"database/sql"
	"encoding/base64"
	"errors"
	"log/slog"
	"strconv"
	"time"

	"nerae/api/internal/modules/auth/approvalreceipt"
	"nerae/api/internal/modules/auth/domain"
)

type PollStatus string

const (
	PollPending       PollStatus = "pending"
	PollDenied        PollStatus = "denied"
	PollExpired       PollStatus = "expired"
	PollAuthenticated PollStatus = "authenticated"
)

// PollOutcome cannot be serialized or logged as a credential. Only an
// authenticated result permits a transport to obtain its committed token.
type PollOutcome struct {
	status PollStatus
	token  string
}

func (PollOutcome) String() string               { return "ApprovalPollOutcome{redacted}" }
func (PollOutcome) GoString() string             { return "ApprovalPollOutcome{redacted}" }
func (PollOutcome) MarshalJSON() ([]byte, error) { return nil, ErrUnavailable }
func (PollOutcome) LogValue() slog.Value         { return slog.StringValue("ApprovalPollOutcome{redacted}") }
func (o PollOutcome) Status() PollStatus         { return o.status }
func (o PollOutcome) TokenForResponse() (string, bool) {
	if o.status != PollAuthenticated || o.token == "" {
		return "", false
	}
	return o.token, true
}

// PollExchanger is internal and has no ingress or per-approval rate limiting.
// A new poll uses a new key and proof; an exact retry returns the original
// non-secret status snapshot. Only approved/consumed requests are delegated to
// ReceiptExchanger, which atomically issues or recovers a committed session.
type PollExchanger struct {
	exchanger *Exchanger
	approved  *ReceiptExchanger
	crypto    *approvalreceipt.Crypto
	keyID     string
	retention time.Duration
}

func (PollExchanger) String() string               { return "ApprovalPollExchanger{redacted}" }
func (PollExchanger) GoString() string             { return "ApprovalPollExchanger{redacted}" }
func (PollExchanger) MarshalJSON() ([]byte, error) { return nil, ErrUnavailable }
func (PollExchanger) LogValue() slog.Value {
	return slog.StringValue("ApprovalPollExchanger{redacted}")
}

func NewPollExchanger(approved *ReceiptExchanger, keyID string, retention time.Duration) (*PollExchanger, error) {
	if approved == nil || !approved.valid() || approved.keyID != keyID || retention < time.Second || retention > 24*time.Hour || retention%time.Microsecond != 0 {
		return nil, ErrUnavailable
	}
	return &PollExchanger{exchanger: approved.exchanger, approved: approved, crypto: approved.crypto, keyID: keyID, retention: retention}, nil
}

// Initialize checks both the approved-exchange and poll epochs in one setup
// transaction. Missing epoch metadata with retained history fails closed.
func (p *PollExchanger) Initialize(ctx context.Context, tx *sql.Tx) error {
	if !p.valid() || ctx == nil || tx == nil || p.approved.Initialize(ctx, tx) != nil {
		return ErrUnavailable
	}
	for _, query := range []string{
		`SELECT slot,key_id,key_tag FROM auth_approval_poll_key_epoch LIMIT 0`,
		`SELECT approval_scope,request_key_hmac,subject_scope,request_hmac,status,key_id,created_at,expires_at FROM auth_approval_poll_receipt LIMIT 0`,
	} {
		rows, err := tx.QueryContext(ctx, query)
		if err != nil {
			return ErrUnavailable
		}
		if err := rows.Close(); err != nil || rows.Err() != nil {
			return ErrUnavailable
		}
	}
	tag, err := p.crypto.EpochTag(p.keyID)
	if err != nil {
		return ErrUnavailable
	}
	var storedID, storedTag []byte
	err = tx.QueryRowContext(ctx, `SELECT key_id,key_tag FROM auth_approval_poll_key_epoch WHERE slot=1 FOR UPDATE`).Scan(&storedID, &storedTag)
	if err == nil {
		if string(storedID) != p.keyID || !hmac.Equal(storedTag, tag[:]) {
			return ErrUnavailable
		}
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return ErrUnavailable
	}
	var history []byte
	if err := tx.QueryRowContext(ctx, `SELECT approval_scope FROM auth_approval_poll_receipt LIMIT 1 FOR UPDATE`).Scan(&history); !errors.Is(err, sql.ErrNoRows) {
		return ErrUnavailable
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO auth_approval_poll_key_epoch(slot,key_id,key_tag) VALUES(1,?,?)`, []byte(p.keyID), tag[:]); err != nil {
		return ErrUnavailable
	}
	return nil
}

// exchange commits status snapshots and consumes their device JTI in one
// transaction. A session is only returned through the existing receipt
// coordinator after its own commit and post-commit authorization checks.
func (p *PollExchanger) exchange(ctx context.Context, db *sql.DB, key string, input ExchangeInput) (PollOutcome, error) {
	if !p.valid() || ctx == nil || db == nil {
		return PollOutcome{}, ErrUnavailable
	}
	binding, err := p.crypto.Summarize(approvalreceipt.Request{IdempotencyKey: key, ApprovalID: input.ApprovalID,
		ApprovalSecret: input.Secret, DeviceProof: input.Proof})
	if err != nil {
		return PollOutcome{}, ErrUnavailable
	}
	tx, err := db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return PollOutcome{}, ErrUnavailable
	}
	defer tx.Rollback()
	authority, err := p.lockPollAuthority(ctx, tx, input)
	if err != nil || p.checkEpoch(ctx, tx) != nil {
		return PollOutcome{}, ErrUnavailable
	}
	subject, err := p.crypto.SubjectScope(strconv.FormatUint(authority.userID, 10))
	if err != nil {
		return PollOutcome{}, ErrUnavailable
	}
	approvalScope, err := p.crypto.ApprovalScope(input.ApprovalID)
	if err != nil {
		return PollOutcome{}, ErrUnavailable
	}
	approvalDigest, subjectDigest := approvalScope.Digest(), subject.Digest()
	keyDigest, requestDigest := binding.IdempotencyDigest(), binding.RequestDigest()
	var storedSubject, storedRequest, storedKeyID []byte
	var storedStatus, createdRaw, expiryRaw string
	err = tx.QueryRowContext(ctx, `SELECT subject_scope,request_hmac,status,key_id,
		DATE_FORMAT(created_at,'%Y-%m-%d %H:%i:%s.%f'),DATE_FORMAT(expires_at,'%Y-%m-%d %H:%i:%s.%f')
		FROM auth_approval_poll_receipt WHERE approval_scope=? AND request_key_hmac=? FOR UPDATE`, approvalDigest[:], keyDigest[:]).
		Scan(&storedSubject, &storedRequest, &storedStatus, &storedKeyID, &createdRaw, &expiryRaw)
	if err == nil {
		if !hmac.Equal(storedSubject, subjectDigest[:]) || string(storedKeyID) != p.keyID {
			return PollOutcome{}, ErrUnavailable
		}
		if !hmac.Equal(storedRequest, requestDigest[:]) {
			return PollOutcome{}, ErrExchangeReceiptConflict
		}
		created, createdErr := time.Parse(pollTimeLayout, createdRaw)
		expires, expiryErr := time.Parse(pollTimeLayout, expiryRaw)
		dbNow, nowErr := pollDatabaseNow(ctx, tx)
		status := PollStatus(storedStatus)
		if createdErr != nil || expiryErr != nil || nowErr != nil || created.After(dbNow) || !expires.After(dbNow) ||
			!expires.After(created) || expires.After(created.Add(24*time.Hour)) || !validNonSecretStatus(status) ||
			p.verifyConsumed(ctx, tx, input, authority) != nil {
			return PollOutcome{}, ErrUnavailable
		}
		if err := tx.Commit(); err != nil || p.checkAfterCommit(input, authority, expires) != nil {
			return PollOutcome{}, ErrUnavailable
		}
		return PollOutcome{status: status}, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return PollOutcome{}, ErrUnavailable
	}
	now := p.exchanger.now().UTC()
	if _, err := authority.verify(p.exchanger, input, now); err != nil {
		return PollOutcome{}, ErrUnavailable
	}
	if authority.state == "consumed" || (authority.state == "approved" && authority.expires.After(now)) {
		// A consumed approval may already have an encrypted success receipt.
		// Never create a competing status receipt for the same key after it
		// expires; the success coordinator owns its replay/failure semantics.
		if err := tx.Commit(); err != nil {
			return PollOutcome{}, ErrUnavailable
		}
		token, err := p.approved.exchange(ctx, db, key, input)
		if err != nil {
			return PollOutcome{}, err
		}
		return PollOutcome{status: PollAuthenticated, token: token}, nil
	}
	status := PollStatus("")
	switch {
	case !authority.expires.After(now):
		status = PollExpired
	case authority.state == "pending":
		status = PollPending
	case authority.state == "denied":
		status = PollDenied
	default:
		return PollOutcome{}, ErrUnavailable
	}
	proof, err := authority.verify(p.exchanger, input, now)
	if err != nil || p.exchanger.replay.Consume(ctx, tx, authority.fingerprint, proof, now) != nil {
		return PollOutcome{}, ErrUnavailable
	}
	dbNow, err := pollDatabaseNow(ctx, tx)
	if err != nil {
		return PollOutcome{}, ErrUnavailable
	}
	expires := dbNow.Add(p.retention).Truncate(time.Microsecond)
	result, err := tx.ExecContext(ctx, `INSERT INTO auth_approval_poll_receipt
		(approval_scope,request_key_hmac,subject_scope,request_hmac,status,key_id,created_at,expires_at)
		VALUES(?,?,?,?,?,?,?,?)`, approvalDigest[:], keyDigest[:], subjectDigest[:], requestDigest[:], string(status),
		[]byte(p.keyID), timestamp(dbNow), timestamp(expires))
	if err != nil {
		return PollOutcome{}, ErrUnavailable
	}
	if count, err := result.RowsAffected(); err != nil || count != 1 || p.verifyConsumed(ctx, tx, input, authority) != nil {
		return PollOutcome{}, ErrUnavailable
	}
	// Do not commit a new pending/denied snapshot after the approval crosses
	// its deadline while a write or proof-marker lock was waiting. A retry
	// can then record the expired result using the same proof and key.
	finished := p.exchanger.now().UTC()
	if finished.IsZero() || (status != PollExpired && !authority.expires.After(finished)) {
		return PollOutcome{}, ErrUnavailable
	}
	dbNow, err = pollDatabaseNow(ctx, tx)
	if err != nil || !expires.After(dbNow) || p.verifyConsumed(ctx, tx, input, authority) != nil {
		return PollOutcome{}, ErrUnavailable
	}
	finished = p.exchanger.now().UTC()
	if finished.IsZero() || (status != PollExpired && !authority.expires.After(finished)) ||
		tx.Commit() != nil || p.checkAfterCommit(input, authority, expires) != nil {
		return PollOutcome{}, ErrUnavailable
	}
	return PollOutcome{status: status}, nil
}

const pollTimeLayout = "2006-01-02 15:04:05.000000"

func validNonSecretStatus(status PollStatus) bool {
	return status == PollPending || status == PollDenied || status == PollExpired
}

func (p *PollExchanger) valid() bool {
	return p != nil && p.exchanger != nil && p.exchanger.approvals != nil && p.exchanger.replay != nil && p.exchanger.now != nil &&
		p.approved != nil && p.approved.valid() && p.approved.exchanger == p.exchanger && p.approved.crypto == p.crypto &&
		p.approved.keyID == p.keyID && p.retention >= time.Second && p.retention <= 24*time.Hour && p.retention%time.Microsecond == 0
}

func (p *PollExchanger) checkEpoch(ctx context.Context, tx *sql.Tx) error {
	tag, err := p.crypto.EpochTag(p.keyID)
	if err != nil {
		return ErrUnavailable
	}
	var storedID, storedTag []byte
	if err := tx.QueryRowContext(ctx, `SELECT key_id,key_tag FROM auth_approval_poll_key_epoch WHERE slot=1 FOR SHARE`).Scan(&storedID, &storedTag); err != nil ||
		string(storedID) != p.keyID || !hmac.Equal(storedTag, tag[:]) {
		return ErrUnavailable
	}
	return nil
}

func pollDatabaseNow(ctx context.Context, tx *sql.Tx) (time.Time, error) {
	var raw string
	if err := tx.QueryRowContext(ctx, `SELECT DATE_FORMAT(UTC_TIMESTAMP(6),'%Y-%m-%d %H:%i:%s.%f')`).Scan(&raw); err != nil {
		return time.Time{}, ErrUnavailable
	}
	now, err := time.Parse(pollTimeLayout, raw)
	if err != nil || now.Format(pollTimeLayout) != raw {
		return time.Time{}, ErrUnavailable
	}
	return now, nil
}

type pollAuthority struct {
	userID      uint64
	state       string
	expires     time.Time
	public      domain.DevicePublicJWK
	expectation domain.ProofExpectation
	fingerprint [32]byte
}

func (a pollAuthority) verify(exchanger *Exchanger, input ExchangeInput, now time.Time) (domain.VerifiedDeviceProof, error) {
	if exchanger == nil || now.IsZero() {
		return domain.VerifiedDeviceProof{}, ErrUnavailable
	}
	proof, err := domain.VerifyDeviceProof(input.Proof, a.public, a.expectation, now, exchanger.policy)
	if err != nil {
		return domain.VerifiedDeviceProof{}, ErrUnavailable
	}
	return proof, nil
}

func (p *PollExchanger) verifyConsumed(ctx context.Context, tx *sql.Tx, input ExchangeInput, authority pollAuthority) error {
	now := p.exchanger.now().UTC()
	proof, err := authority.verify(p.exchanger, input, now)
	if err != nil || p.exchanger.replay.EnsureConsumed(ctx, tx, authority.fingerprint, proof, now) != nil {
		return ErrUnavailable
	}
	if _, err := authority.verify(p.exchanger, input, p.exchanger.now().UTC()); err != nil {
		return ErrUnavailable
	}
	return nil
}

func (p *PollExchanger) checkAfterCommit(input ExchangeInput, authority pollAuthority, receiptExpiry time.Time) error {
	now := p.exchanger.now().UTC()
	if !receiptExpiry.After(now) {
		return ErrUnavailable
	}
	if _, err := authority.verify(p.exchanger, input, now); err != nil {
		return ErrUnavailable
	}
	return nil
}

// lockPollAuthority derives all user, public-key and nonce data from the
// approval record. The anonymous caller supplies no subject or public key.
func (p *PollExchanger) lockPollAuthority(ctx context.Context, tx *sql.Tx, input ExchangeInput) (pollAuthority, error) {
	invalid := func() (pollAuthority, error) { return pollAuthority{}, ErrUnavailable }
	if !p.valid() || ctx == nil || tx == nil || len(input.ApprovalID) < 22 || len(input.ApprovalID) > 128 || len(input.Secret) != 43 || len(input.Proof) < 32 || len(input.Proof) > 4096 {
		return invalid()
	}
	secret, err := base64.RawURLEncoding.Strict().DecodeString(input.Secret)
	if err != nil || len(secret) != 32 {
		return invalid()
	}
	var authority pollAuthority
	var identity []byte
	if err := tx.QueryRowContext(ctx, `SELECT user_id,identity_id FROM auth_login_approval WHERE id=?`, []byte(input.ApprovalID)).
		Scan(&authority.userID, &identity); err != nil || authority.userID == 0 || len(identity) == 0 {
		return invalid()
	}
	var identityUser uint64
	var identityState string
	if err := tx.QueryRowContext(ctx, `SELECT user_id,state FROM login_identity WHERE id=? FOR UPDATE`, identity).
		Scan(&identityUser, &identityState); err != nil || identityUser != authority.userID || identityState != "active" {
		return invalid()
	}
	var userState string
	var tokenVersion, identityVersion uint64
	if err := tx.QueryRowContext(ctx, `SELECT status,token_version,identity_version FROM user WHERE id=? FOR UPDATE`, authority.userID).
		Scan(&userState, &tokenVersion, &identityVersion); err != nil || userState != "active" || tokenVersion == 0 || identityVersion == 0 ||
		p.exchanger.approvals.checkEpoch(ctx, tx) != nil {
		return invalid()
	}
	var rowUser, rowToken, rowIdentityVersion uint64
	var rowIdentity, fingerprint, digest, keyID, nonce []byte
	var x, y, expiresRaw string
	if err := tx.QueryRowContext(ctx, `SELECT user_id,identity_id,token_version,identity_version,target_fingerprint,
		public_x,public_y,secret_digest,key_id,nonce,state,DATE_FORMAT(expires_at,'%Y-%m-%d %H:%i:%s.%f')
		FROM auth_login_approval WHERE id=? FOR UPDATE`, []byte(input.ApprovalID)).
		Scan(&rowUser, &rowIdentity, &rowToken, &rowIdentityVersion, &fingerprint, &x, &y, &digest, &keyID, &nonce, &authority.state, &expiresRaw); err != nil ||
		rowUser != authority.userID || !hmac.Equal(rowIdentity, identity) || rowToken != tokenVersion || rowIdentityVersion != identityVersion ||
		string(keyID) != p.exchanger.approvals.keyID || !hmac.Equal(digest, p.exchanger.approvals.digest(input.ApprovalID, input.Secret)) {
		return invalid()
	}
	if authority.state != "pending" && authority.state != "denied" && authority.state != "approved" && authority.state != "consumed" {
		return invalid()
	}
	authority.expires, err = time.Parse(pollTimeLayout, expiresRaw)
	if err != nil {
		return invalid()
	}
	authority.public = domain.DevicePublicJWK{Kty: "EC", Crv: "P-256", X: x, Y: y}
	authority.fingerprint, err = domain.DeviceKeyFingerprint(authority.public)
	if err != nil || !hmac.Equal(fingerprint, authority.fingerprint[:]) {
		return invalid()
	}
	authority.expectation = domain.ProofExpectation{Purpose: "approval_exchange", ResourceID: input.ApprovalID, Nonce: string(nonce)}
	if _, err := authority.verify(p.exchanger, input, p.exchanger.now().UTC()); err != nil {
		return invalid()
	}
	return authority, nil
}
