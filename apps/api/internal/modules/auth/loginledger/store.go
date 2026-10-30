// Package loginledger persists encrypted anonymous-login retry receipts inside
// a caller-owned transaction. It never verifies a device proof, authorizes a
// session, issues credentials, or exposes HTTP.
package loginledger

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"errors"
	"log/slog"
	"strings"
	"time"
	"unicode/utf8"

	"nerae/api/internal/modules/auth/domain"
	"nerae/api/internal/modules/auth/loginreceipt"
)

var (
	ErrLedger   = errors.New("email login receipt ledger unavailable")
	ErrConflict = errors.New("email login receipt conflicts")
	ErrExpired  = errors.New("email login receipt expired")
	ErrExisting = errors.New("email login receipt already exists")
)

const maxRetention = 24 * time.Hour

type Store struct {
	crypto     *loginreceipt.Crypto
	identities *domain.EmailIdentityProtector
	keyID      string
}

func (Store) String() string               { return "EmailLoginReceiptStore{redacted}" }
func (Store) GoString() string             { return "EmailLoginReceiptStore{redacted}" }
func (Store) MarshalJSON() ([]byte, error) { return nil, ErrLedger }
func (Store) LogValue() slog.Value         { return slog.StringValue("EmailLoginReceiptStore{redacted}") }

func NewStore(crypto *loginreceipt.Crypto, identities *domain.EmailIdentityProtector, keyID string) (*Store, error) {
	if crypto == nil || identities == nil {
		return nil, ErrLedger
	}
	if _, err := crypto.EpochTag(keyID); err != nil {
		return nil, ErrLedger
	}
	if _, _, err := identities.KeyEpoch(); err != nil {
		return nil, ErrLedger
	}
	return &Store{crypto: crypto, identities: identities, keyID: keyID}, nil
}

// Handle is an opaque reservation bound to one Store, transaction, scope and
// request binding. It cannot authorize access outside that transaction.
type Handle struct {
	store        *Store
	tx           *sql.Tx
	scope        [sha256.Size]byte
	binding      loginreceipt.Binding
	existing     *Record
	putAttempted bool
}

func (Handle) String() string               { return "EmailLoginReceiptHandle{redacted}" }
func (Handle) GoString() string             { return "EmailLoginReceiptHandle{redacted}" }
func (Handle) MarshalJSON() ([]byte, error) { return nil, ErrLedger }
func (Handle) LogValue() slog.Value         { return slog.StringValue("EmailLoginReceiptHandle{redacted}") }

func (h *Handle) ExistingRecord() (Record, bool) {
	if h == nil || h.existing == nil {
		return Record{}, false
	}
	return *h.existing, true
}

// RecheckExisting repeats the locked receipt lookup in the original live
// transaction and never deletes an expired row. The returned protected scope,
// binding and ciphertext do not authorize decryption or credential release;
// callers must still reverify the exact signed request and current authority.
// On any error, the caller must roll back the transaction.
func (h *Handle) RecheckExisting(ctx context.Context, tx *sql.Tx) (Record, [sha256.Size]byte, loginreceipt.Binding, error) {
	if h == nil || h.store == nil || h.tx == nil || h.tx != tx || ctx == nil || h.existing == nil {
		return Record{}, [sha256.Size]byte{}, loginreceipt.Binding{}, ErrLedger
	}
	refreshed, err := h.store.lock(ctx, tx, h.scope, h.binding, false)
	if err != nil {
		return Record{}, [sha256.Size]byte{}, loginreceipt.Binding{}, err
	}
	record, ok := refreshed.ExistingRecord()
	if !ok {
		return Record{}, [sha256.Size]byte{}, loginreceipt.Binding{}, ErrLedger
	}
	return record, h.scope, h.binding, nil
}

// Record contains ciphertext and non-secret lookup metadata only. Use its
// accessors to rebuild the AAD; never interpret it as signature or auth proof.
type Record struct {
	requestHMAC [sha256.Size]byte
	challengeID string
	kind        loginreceipt.ResultType
	reference   string
	keyID       string
	expiresAt   time.Time
	sealed      loginreceipt.Sealed
}

func (Record) String() string                        { return "EmailLoginReceiptRecord{redacted}" }
func (Record) GoString() string                      { return "EmailLoginReceiptRecord{redacted}" }
func (Record) MarshalJSON() ([]byte, error)          { return nil, ErrLedger }
func (Record) LogValue() slog.Value                  { return slog.StringValue("EmailLoginReceiptRecord{redacted}") }
func (r Record) RequestHMAC() [sha256.Size]byte      { return r.requestHMAC }
func (r Record) ChallengeID() string                 { return r.challengeID }
func (r Record) ResultType() loginreceipt.ResultType { return r.kind }
func (r Record) ResultReference() string             { return r.reference }
func (r Record) KeyID() string                       { return r.keyID }
func (r Record) ExpiresAt() time.Time                { return r.expiresAt }
func (r Record) Sealed() loginreceipt.Sealed         { return r.sealed }

// Initialize explicitly binds the receipt store to both configured key sets.
// It must be called in a bootstrap transaction before request traffic starts.
// A missing receipt epoch can be created only when the receipt table is empty.
func (s *Store) Initialize(ctx context.Context, tx *sql.Tx) error {
	if !s.valid() || ctx == nil || tx == nil {
		return ErrLedger
	}
	if err := s.lockIdentityEpoch(ctx, tx); err != nil {
		return ErrLedger
	}
	if err := s.validateSchema(ctx, tx); err != nil {
		return ErrLedger
	}
	expectedTag, err := s.crypto.EpochTag(s.keyID)
	if err != nil {
		return ErrLedger
	}
	var keyID, storedTag []byte
	err = tx.QueryRowContext(ctx, `SELECT key_id,key_tag FROM auth_email_login_receipt_key_epoch WHERE slot=1 FOR UPDATE`).Scan(&keyID, &storedTag)
	if err == nil {
		if string(keyID) != s.keyID || !hmac.Equal(storedTag, expectedTag[:]) {
			return ErrLedger
		}
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return ErrLedger
	}
	var history []byte
	err = tx.QueryRowContext(ctx, `SELECT request_key_hmac FROM auth_email_login_receipt LIMIT 1 FOR UPDATE`).Scan(&history)
	if err == nil || !errors.Is(err, sql.ErrNoRows) {
		return ErrLedger
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO auth_email_login_receipt_key_epoch(slot,key_id,key_tag) VALUES (1,?,?)`, []byte(s.keyID), expectedTag[:]); err != nil {
		return ErrLedger
	}
	return nil
}

// Lock enforces identity epoch → receipt epoch → email slot → challenge →
// receipt row order. scope must be a server-derived protected email index; it
// is a lookup namespace only and grants no identity or authorization. Existing
// ciphertext receives only framing and AAD-metadata checks here; a future
// coordinator may authenticate/decrypt it only after signature and current
// authorization have been independently revalidated in this transaction.
// If the slot has since advanced, only an exact, still-live existing receipt
// may be returned; a missing or expired receipt cannot be created for that stale challenge.
func (s *Store) Lock(ctx context.Context, tx *sql.Tx, scope [sha256.Size]byte, binding loginreceipt.Binding) (*Handle, error) {
	return s.lock(ctx, tx, scope, binding, true)
}

func (s *Store) lock(ctx context.Context, tx *sql.Tx, scope [sha256.Size]byte, binding loginreceipt.Binding, replaceExpired bool) (*Handle, error) {
	if !s.valid() || ctx == nil || tx == nil || allZero(scope[:]) || !validChallengeID(binding.ChallengeID()) ||
		binding.IdempotencyDigest() == ([sha256.Size]byte{}) || binding.RequestDigest() == ([sha256.Size]byte{}) {
		return nil, ErrLedger
	}
	if err := s.lockIdentityEpoch(ctx, tx); err != nil {
		return nil, ErrLedger
	}
	if err := s.lockReceiptEpoch(ctx, tx); err != nil {
		return nil, ErrLedger
	}
	var slotChallenge []byte
	err := tx.QueryRowContext(ctx, `SELECT challenge_id FROM auth_email_login_slot WHERE email_index=? FOR UPDATE`, scope[:]).Scan(&slotChallenge)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrConflict
	}
	if err != nil {
		return nil, ErrLedger
	}
	slotMatchesChallenge := hmac.Equal(slotChallenge, []byte(binding.ChallengeID()))
	var challengeScope []byte
	err = tx.QueryRowContext(ctx, `SELECT email_index FROM auth_email_login_challenge WHERE challenge_id=? FOR UPDATE`, []byte(binding.ChallengeID())).Scan(&challengeScope)
	if errors.Is(err, sql.ErrNoRows) || err == nil && (len(challengeScope) != sha256.Size || !hmac.Equal(challengeScope, scope[:])) {
		return nil, ErrConflict
	}
	if err != nil {
		return nil, ErrLedger
	}
	handle := &Handle{store: s, tx: tx, scope: scope, binding: binding}
	idempotencyDigest := binding.IdempotencyDigest()
	requestDigest := binding.RequestDigest()
	var storedRequest, storedChallenge, rawKind, reference, storedKeyID, ciphertext []byte
	var createdRaw, expiresRaw string
	err = tx.QueryRowContext(ctx, `SELECT request_hmac,challenge_id,result_kind,result_reference,key_id,response_enc,
		CAST(created_at AS CHAR),CAST(expires_at AS CHAR)
		FROM auth_email_login_receipt WHERE email_scope=? AND request_key_hmac=? FOR UPDATE`,
		scope[:], idempotencyDigest[:]).Scan(&storedRequest, &storedChallenge, &rawKind, &reference, &storedKeyID, &ciphertext, &createdRaw, &expiresRaw)
	if errors.Is(err, sql.ErrNoRows) {
		if !slotMatchesChallenge {
			return nil, ErrConflict
		}
		return handle, nil
	}
	if err != nil {
		return nil, ErrLedger
	}
	storedChallengeID := string(storedChallenge)
	if len(storedRequest) != sha256.Size || allZero(storedRequest) || !validChallengeID(storedChallengeID) ||
		string(storedKeyID) != s.keyID || len(reference) < 1 || len(reference) > 256 || !utf8.Valid(reference) || strings.IndexByte(string(reference), 0) >= 0 {
		return nil, ErrLedger
	}
	kind := loginreceipt.ResultType(rawKind)
	if (kind != loginreceipt.ResultAuthenticated && kind != loginreceipt.ResultApprovalRequired && kind != loginreceipt.ResultRejected) ||
		kind == loginreceipt.ResultRejected && string(reference) != storedChallengeID {
		return nil, ErrLedger
	}
	created, err := parseSQLTime(createdRaw)
	if err != nil {
		return nil, ErrLedger
	}
	expires, err := parseSQLTime(expiresRaw)
	if err != nil || !expires.After(created) || expires.Sub(created) > maxRetention {
		return nil, ErrLedger
	}
	sealed, err := loginreceipt.ParseSealed(ciphertext)
	if err != nil {
		return nil, ErrLedger
	}
	now, err := databaseUTCNow(ctx, tx)
	if err != nil {
		return nil, ErrLedger
	}
	if !expires.After(now) {
		if !replaceExpired {
			return nil, ErrExpired
		}
		if !slotMatchesChallenge {
			return nil, ErrConflict
		}
		result, err := tx.ExecContext(ctx, `DELETE FROM auth_email_login_receipt WHERE email_scope=? AND request_key_hmac=?`, scope[:], idempotencyDigest[:])
		if err != nil {
			return nil, ErrLedger
		}
		if affected, err := result.RowsAffected(); err != nil || affected != 1 {
			return nil, ErrLedger
		}
		return handle, nil
	}
	if !hmac.Equal(storedRequest, requestDigest[:]) || storedChallengeID != binding.ChallengeID() {
		return nil, ErrConflict
	}
	if _, err := s.crypto.NewContext(scope[:], binding, kind, string(reference), expires); err != nil {
		return nil, ErrLedger
	}
	var requestHMAC [sha256.Size]byte
	copy(requestHMAC[:], storedRequest)
	record := &Record{requestHMAC: requestHMAC, challengeID: string(storedChallenge), kind: kind,
		reference: string(reference), keyID: string(storedKeyID), expiresAt: expires, sealed: sealed}
	handle.existing = record
	return handle, nil
}

// Put accepts a server-created result only after its caller has completed
// transaction authorization. It authenticates sealed bytes against the exact
// locked context and inserts ciphertext; commit/rollback remains the caller's job.
func (s *Store) Put(ctx context.Context, tx *sql.Tx, handle *Handle, kind loginreceipt.ResultType, reference string, expires time.Time, sealed loginreceipt.Sealed) error {
	if !s.valid() || ctx == nil || tx == nil || handle == nil || handle.store != s || handle.tx != tx ||
		handle.existing != nil || handle.putAttempted {
		return ErrLedger
	}
	handle.putAttempted = true
	if !sameBinding(handle.binding, handle.scope, kind, reference, expires, s.crypto) {
		return ErrLedger
	}
	if err := s.validateLockedChallenge(ctx, tx, handle.scope, handle.binding.ChallengeID()); err != nil {
		return err
	}
	now, err := databaseUTCNow(ctx, tx)
	if err != nil || !expires.After(now) || expires.Sub(now) > maxRetention {
		return ErrLedger
	}
	contextAAD, err := s.crypto.NewContext(handle.scope[:], handle.binding, kind, reference, expires)
	if err != nil {
		return ErrLedger
	}
	blob := sealed.Bytes()
	parsed, err := loginreceipt.ParseSealed(blob)
	if err != nil {
		return ErrLedger
	}
	if _, err := s.crypto.Open(contextAAD, parsed); err != nil {
		return ErrLedger
	}
	idempotencyDigest := handle.binding.IdempotencyDigest()
	requestDigest := handle.binding.RequestDigest()
	created := now.UTC().Truncate(time.Microsecond)
	result, err := tx.ExecContext(ctx, `INSERT INTO auth_email_login_receipt
		(email_scope,request_key_hmac,request_hmac,challenge_id,result_kind,result_reference,key_id,response_enc,created_at,expires_at)
		VALUES (?,?,?,?,?,?,?,?,?,?)`, handle.scope[:], idempotencyDigest[:], requestDigest[:],
		[]byte(handle.binding.ChallengeID()), []byte(kind), []byte(reference), []byte(s.keyID), blob, sqlUTC(created), sqlUTC(expires))
	if err != nil {
		return ErrLedger
	}
	if affected, err := result.RowsAffected(); err != nil || affected != 1 {
		return ErrLedger
	}
	return nil
}

func (s *Store) validateLockedChallenge(ctx context.Context, tx *sql.Tx, scope [sha256.Size]byte, challengeID string) error {
	var activeID []byte
	err := tx.QueryRowContext(ctx, `SELECT challenge_id FROM auth_email_login_slot WHERE email_index=? FOR UPDATE`, scope[:]).Scan(&activeID)
	if errors.Is(err, sql.ErrNoRows) || err == nil && !hmac.Equal(activeID, []byte(challengeID)) {
		return ErrConflict
	}
	if err != nil {
		return ErrLedger
	}
	var challengeScope []byte
	err = tx.QueryRowContext(ctx, `SELECT email_index FROM auth_email_login_challenge WHERE challenge_id=? FOR UPDATE`, []byte(challengeID)).Scan(&challengeScope)
	if errors.Is(err, sql.ErrNoRows) || err == nil && (len(challengeScope) != sha256.Size || !hmac.Equal(challengeScope, scope[:])) {
		return ErrConflict
	}
	if err != nil {
		return ErrLedger
	}
	return nil
}

func (s *Store) lockIdentityEpoch(ctx context.Context, tx *sql.Tx) error {
	_, expected, err := s.identities.KeyEpoch()
	if err != nil {
		return ErrLedger
	}
	var stored []byte
	if err := tx.QueryRowContext(ctx, `SELECT key_tag FROM auth_email_identity_key_epoch WHERE slot=1 FOR SHARE`).Scan(&stored); err != nil ||
		len(stored) != sha256.Size || !hmac.Equal(stored, expected[:]) {
		return ErrLedger
	}
	return nil
}

func (s *Store) lockReceiptEpoch(ctx context.Context, tx *sql.Tx) error {
	expected, err := s.crypto.EpochTag(s.keyID)
	if err != nil {
		return ErrLedger
	}
	var keyID, stored []byte
	if err := tx.QueryRowContext(ctx, `SELECT key_id,key_tag FROM auth_email_login_receipt_key_epoch WHERE slot=1 FOR SHARE`).Scan(&keyID, &stored); err != nil ||
		string(keyID) != s.keyID || len(stored) != sha256.Size || !hmac.Equal(stored, expected[:]) {
		return ErrLedger
	}
	return nil
}

func (s *Store) validateSchema(ctx context.Context, tx *sql.Tx) error {
	for _, query := range [...]string{
		`SELECT slot,key_id,key_tag FROM auth_email_login_receipt_key_epoch LIMIT 0`,
		`SELECT email_scope,request_key_hmac,request_hmac,challenge_id,result_kind,result_reference,key_id,response_enc,created_at,expires_at FROM auth_email_login_receipt LIMIT 0`,
	} {
		rows, err := tx.QueryContext(ctx, query)
		if err != nil {
			return ErrLedger
		}
		if err := rows.Close(); err != nil || rows.Err() != nil {
			return ErrLedger
		}
	}
	return nil
}

func (s *Store) valid() bool {
	return s != nil && s.crypto != nil && s.identities != nil && s.keyID != ""
}

func sameBinding(binding loginreceipt.Binding, scope [sha256.Size]byte, kind loginreceipt.ResultType, reference string, expiry time.Time, crypto *loginreceipt.Crypto) bool {
	_, err := crypto.NewContext(scope[:], binding, kind, reference, expiry)
	return err == nil
}

func parseSQLTime(value string) (time.Time, error) {
	parsed, err := time.Parse("2006-01-02 15:04:05.999999", value)
	if err == nil {
		return parsed.UTC(), nil
	}
	parsed, err = time.Parse(time.RFC3339Nano, value)
	return parsed.UTC(), err
}

func databaseUTCNow(ctx context.Context, tx *sql.Tx) (time.Time, error) {
	var raw string
	if err := tx.QueryRowContext(ctx, `SELECT CAST(UTC_TIMESTAMP(6) AS CHAR)`).Scan(&raw); err != nil {
		return time.Time{}, ErrLedger
	}
	now, err := parseSQLTime(raw)
	if err != nil || now.IsZero() {
		return time.Time{}, ErrLedger
	}
	return now, nil
}

func sqlUTC(value time.Time) string {
	return value.UTC().Truncate(time.Microsecond).Format("2006-01-02 15:04:05.000000")
}

func allZero(value []byte) bool {
	var aggregate byte
	for _, b := range value {
		aggregate |= b
	}
	return aggregate == 0
}

func validChallengeID(value string) bool {
	if len(value) < 22 || len(value) > 128 {
		return false
	}
	for i := 0; i < len(value); i++ {
		c := value[i]
		if !((c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-' || c == '_') {
			return false
		}
	}
	return true
}
