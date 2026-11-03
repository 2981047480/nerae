// Package approvalledger stores encrypted approval-exchange receipts inside a
// caller-owned transaction. It never verifies device proofs or authorizes an
// exchange; a coordinator must do both before using any returned receipt.
package approvalledger

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"errors"
	"log/slog"
	"reflect"
	"time"

	"nerae/api/internal/modules/auth/approvalreceipt"
)

var (
	ErrUnavailable = errors.New("approval exchange receipt unavailable")
	ErrConflict    = errors.New("approval exchange receipt conflicts")
	ErrExpired     = errors.New("approval exchange receipt expired")
)

const maxRetention = 24 * time.Hour

type Store struct {
	crypto *approvalreceipt.Crypto
	keyID  string
}

func (Store) String() string               { return "ApprovalExchangeLedger{redacted}" }
func (Store) GoString() string             { return "ApprovalExchangeLedger{redacted}" }
func (Store) MarshalJSON() ([]byte, error) { return nil, ErrUnavailable }
func (Store) LogValue() slog.Value         { return slog.StringValue("ApprovalExchangeLedger{redacted}") }

func New(crypto *approvalreceipt.Crypto, keyID string) (*Store, error) {
	if crypto == nil {
		return nil, ErrUnavailable
	}
	if _, err := crypto.EpochTag(keyID); err != nil {
		return nil, ErrUnavailable
	}
	return &Store{crypto: crypto, keyID: keyID}, nil
}

// Initialize binds the configured keys to a singleton epoch. If history is
// already present without an epoch, it refuses to adopt or replace that data.
func (s *Store) Initialize(ctx context.Context, tx *sql.Tx) error {
	if !s.valid() || ctx == nil || tx == nil {
		return ErrUnavailable
	}
	for _, query := range [...]string{
		`SELECT slot,key_id,key_tag FROM auth_approval_exchange_receipt_key_epoch LIMIT 0`,
		`SELECT approval_scope,subject_scope,request_key_hmac,request_hmac,session_reference,key_id,response_enc,created_at,expires_at FROM auth_approval_exchange_receipt LIMIT 0`,
	} {
		rows, err := tx.QueryContext(ctx, query)
		if err != nil {
			return ErrUnavailable
		}
		if err := rows.Close(); err != nil || rows.Err() != nil {
			return ErrUnavailable
		}
	}
	tag, err := s.crypto.EpochTag(s.keyID)
	if err != nil {
		return ErrUnavailable
	}
	var existingID, existingTag []byte
	err = tx.QueryRowContext(ctx, `SELECT key_id,key_tag FROM auth_approval_exchange_receipt_key_epoch WHERE slot=1 FOR UPDATE`).Scan(&existingID, &existingTag)
	if err == nil {
		if string(existingID) != s.keyID || !hmac.Equal(existingTag, tag[:]) {
			return ErrUnavailable
		}
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return ErrUnavailable
	}
	var history []byte
	if err := tx.QueryRowContext(ctx, `SELECT approval_scope FROM auth_approval_exchange_receipt LIMIT 1 FOR UPDATE`).Scan(&history); !errors.Is(err, sql.ErrNoRows) {
		return ErrUnavailable
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO auth_approval_exchange_receipt_key_epoch(slot,key_id,key_tag) VALUES (1,?,?)`, []byte(s.keyID), tag[:]); err != nil {
		return ErrUnavailable
	}
	return nil
}

type Handle struct {
	store      *Store
	tx         *sql.Tx
	binding    approvalreceipt.Binding
	subjectID  string
	subject    approvalreceipt.SubjectScope
	approval   approvalreceipt.ApprovalScope
	existing   *record
	putAttempt bool
}

type record struct {
	reference string
	expires   time.Time
	sealed    approvalreceipt.Sealed
}

func (Handle) String() string               { return "ApprovalExchangeReceiptHandle{redacted}" }
func (Handle) GoString() string             { return "ApprovalExchangeReceiptHandle{redacted}" }
func (Handle) MarshalJSON() ([]byte, error) { return nil, ErrUnavailable }
func (Handle) LogValue() slog.Value {
	return slog.StringValue("ApprovalExchangeReceiptHandle{redacted}")
}

func (h *Handle) Exists() bool { return h != nil && h.existing != nil }

// Lock must only be called after the coordinator has resolved and locked the
// approval's real subject and checked its current authority. subjectID must
// come from server-side approval data, never from an anonymous request. This
// method is a storage reservation, not proof of device or user authorization.
// A receipt is never automatically overwritten or deleted after expiry.
func (s *Store) Lock(ctx context.Context, tx *sql.Tx, subjectID string, binding approvalreceipt.Binding) (*Handle, error) {
	if !s.valid() || ctx == nil || tx == nil {
		return nil, ErrUnavailable
	}
	subject, err := s.crypto.SubjectScope(subjectID)
	if err != nil {
		return nil, ErrUnavailable
	}
	approval, err := s.crypto.ApprovalScope(binding.ApprovalID())
	if err != nil || binding.IdempotencyDigest() == ([sha256.Size]byte{}) || binding.RequestDigest() == ([sha256.Size]byte{}) {
		return nil, ErrUnavailable
	}
	if err := s.checkEpoch(ctx, tx); err != nil {
		return nil, ErrUnavailable
	}
	h := &Handle{store: s, tx: tx, binding: binding, subjectID: subjectID, subject: subject, approval: approval}
	scope := approval.Digest()
	var storedSubject, keyDigest, requestDigest, reference, keyID, cipher []byte
	var createdRaw, expiresRaw string
	err = tx.QueryRowContext(ctx, `SELECT subject_scope,request_key_hmac,request_hmac,session_reference,key_id,response_enc,
		DATE_FORMAT(created_at, '%Y-%m-%d %H:%i:%s.%f'),DATE_FORMAT(expires_at, '%Y-%m-%d %H:%i:%s.%f')
		FROM auth_approval_exchange_receipt WHERE approval_scope=? FOR UPDATE`, scope[:]).
		Scan(&storedSubject, &keyDigest, &requestDigest, &reference, &keyID, &cipher, &createdRaw, &expiresRaw)
	if errors.Is(err, sql.ErrNoRows) {
		return h, nil
	}
	if err != nil {
		return nil, ErrUnavailable
	}
	wantSubject, wantKey, wantRequest := subject.Digest(), binding.IdempotencyDigest(), binding.RequestDigest()
	if !hmac.Equal(storedSubject, wantSubject[:]) {
		return nil, ErrUnavailable
	}
	if !hmac.Equal(keyDigest, wantKey[:]) || !hmac.Equal(requestDigest, wantRequest[:]) {
		return nil, ErrConflict
	}
	created, errCreated := time.Parse("2006-01-02 15:04:05.000000", createdRaw)
	expires, errExpires := time.Parse("2006-01-02 15:04:05.000000", expiresRaw)
	now, errNow := databaseNow(ctx, tx)
	sealed, errSealed := approvalreceipt.ParseSealed(cipher)
	if errCreated != nil || errExpires != nil || errNow != nil || errSealed != nil || string(keyID) != s.keyID ||
		created.After(now) || !expires.After(created) || expires.After(created.Add(maxRetention)) {
		return nil, ErrUnavailable
	}
	if !expires.After(now) {
		return nil, ErrExpired
	}
	if _, err := s.crypto.NewContext(s.keyID, subject, approval, binding, "issued_session", string(reference), expires); err != nil {
		return nil, ErrUnavailable
	}
	h.existing = &record{reference: string(reference), expires: expires, sealed: sealed}
	return h, nil
}

// Put verifies ciphertext and current session binding before inserting once.
// The caller must have already verified the original device proof and JTI,
// and must atomically include the actual exchange in this same transaction.
// It does not release credentials and never commits the caller's transaction.
// The caller must roll back the owning transaction after any error.
func (h *Handle) Put(ctx context.Context, tx *sql.Tx, reference string, expires time.Time, sealed approvalreceipt.Sealed, verifier approvalreceipt.CurrentSessionVerifier) error {
	if h == nil || h.store == nil || h.tx == nil || h.tx != tx || ctx == nil || nilVerifier(verifier) || h.existing != nil || h.putAttempt {
		return ErrUnavailable
	}
	h.putAttempt = true
	now, err := databaseNow(ctx, tx)
	if err != nil || expires.Location() != time.UTC || expires.Nanosecond()%int(time.Microsecond) != 0 || !expires.After(now) || expires.After(now.Add(maxRetention)) {
		return ErrUnavailable
	}
	receiptContext, err := h.store.crypto.NewContext(h.store.keyID, h.subject, h.approval, h.binding, "issued_session", reference, expires)
	if err != nil {
		return ErrUnavailable
	}
	dbNow, err := approvalreceipt.ReadDatabaseUTC(ctx, tx)
	if err != nil {
		return ErrUnavailable
	}
	if _, err := h.store.crypto.Open(ctx, receiptContext, sealed, dbNow, tx, verifier); err != nil {
		return ErrUnavailable
	}
	approval, subject, keyDigest, requestDigest := h.approval.Digest(), h.subject.Digest(), h.binding.IdempotencyDigest(), h.binding.RequestDigest()
	result, err := tx.ExecContext(ctx, `INSERT INTO auth_approval_exchange_receipt
		(approval_scope,subject_scope,request_key_hmac,request_hmac,session_reference,key_id,response_enc,created_at,expires_at)
		VALUES (?,?,?,?,?,?,?,?,?)`, approval[:], subject[:], keyDigest[:], requestDigest[:], []byte(reference), []byte(h.store.keyID), sealed.Bytes(), sqlTime(now), sqlTime(expires))
	if err != nil {
		return ErrUnavailable
	}
	if count, err := result.RowsAffected(); err != nil || count != 1 {
		return ErrUnavailable
	}
	h.existing = &record{reference: reference, expires: expires, sealed: sealed}
	return nil
}

// Open is a low-level cryptographic operation, not authorization. The caller
// must first revalidate the exact original signed proof, consumed JTI and all
// approval/source authority in this transaction, then commit successfully
// before releasing plaintext to any client. An expired receipt is not renewed.
func (h *Handle) Open(ctx context.Context, tx *sql.Tx, verifier approvalreceipt.CurrentSessionVerifier) (approvalreceipt.Plaintext, error) {
	if h == nil || h.store == nil || h.tx == nil || h.tx != tx || ctx == nil || nilVerifier(verifier) || h.existing == nil {
		return approvalreceipt.Plaintext{}, ErrUnavailable
	}
	// Re-read under the same live transaction so a caller-side mutation of the
	// row cannot turn a stale in-memory handle into an authorization decision.
	refreshed, err := h.store.Lock(ctx, tx, h.subjectID, h.binding)
	if err != nil || refreshed == nil || refreshed.existing == nil {
		return approvalreceipt.Plaintext{}, ErrUnavailable
	}
	receiptContext, err := h.store.crypto.NewContext(h.store.keyID, refreshed.subject, refreshed.approval, refreshed.binding,
		"issued_session", refreshed.existing.reference, refreshed.existing.expires)
	if err != nil {
		return approvalreceipt.Plaintext{}, ErrUnavailable
	}
	dbNow, err := approvalreceipt.ReadDatabaseUTC(ctx, tx)
	if err != nil {
		return approvalreceipt.Plaintext{}, ErrUnavailable
	}
	plaintext, err := h.store.crypto.Open(ctx, receiptContext, refreshed.existing.sealed, dbNow, tx, verifier)
	if err != nil {
		return approvalreceipt.Plaintext{}, ErrUnavailable
	}
	return plaintext, nil
}

func (s *Store) valid() bool { return s != nil && s.crypto != nil && s.keyID != "" }

func nilVerifier(verifier approvalreceipt.CurrentSessionVerifier) bool {
	if verifier == nil {
		return true
	}
	value := reflect.ValueOf(verifier)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}

func (s *Store) checkEpoch(ctx context.Context, tx *sql.Tx) error {
	tag, err := s.crypto.EpochTag(s.keyID)
	if err != nil {
		return ErrUnavailable
	}
	var id, saved []byte
	if err := tx.QueryRowContext(ctx, `SELECT key_id,key_tag FROM auth_approval_exchange_receipt_key_epoch WHERE slot=1 FOR SHARE`).Scan(&id, &saved); err != nil ||
		string(id) != s.keyID || !hmac.Equal(saved, tag[:]) {
		return ErrUnavailable
	}
	return nil
}

func databaseNow(ctx context.Context, tx *sql.Tx) (time.Time, error) {
	var raw string
	if err := tx.QueryRowContext(ctx, `SELECT DATE_FORMAT(UTC_TIMESTAMP(6), '%Y-%m-%d %H:%i:%s.%f')`).Scan(&raw); err != nil {
		return time.Time{}, ErrUnavailable
	}
	now, err := time.Parse("2006-01-02 15:04:05.000000", raw)
	if err != nil || now.IsZero() {
		return time.Time{}, ErrUnavailable
	}
	return now, nil
}

func sqlTime(value time.Time) string { return value.UTC().Format("2006-01-02 15:04:05.000000") }
