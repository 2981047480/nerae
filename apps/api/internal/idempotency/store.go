// Package idempotency implements replay for authenticated database-only
// mutations. It does not protect anonymous login, SMTP, or other external work.
package idempotency

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"errors"
	"log/slog"
	"strconv"
	"time"

	"nerae/api/internal/modules/auth/session"
)

var (
	ErrInvalid        = errors.New("invalid idempotency request")
	ErrAuthentication = errors.New("authentication required")
	ErrForbidden      = errors.New("operation forbidden")
	ErrConflict       = errors.New("idempotency conflict")
	ErrInProgress     = errors.New("request in progress")
	ErrUnavailable    = errors.New("idempotency unavailable")
	ErrOutcomeUnknown = errors.New("transaction outcome unknown")
)

// Request must be derived from the actual validated operation and canonical
// concrete path, never from a client-supplied actor or role. Body contains raw
// request JSON and is not stored; duplicate keys are rejected independently.
type Request struct {
	Method, Path, RawQuery, Key string
	Body                        []byte
}

func (Request) String() string               { return "IdempotencyRequest{redacted}" }
func (Request) GoString() string             { return "IdempotencyRequest{redacted}" }
func (Request) MarshalJSON() ([]byte, error) { return nil, ErrUnavailable }
func (Request) LogValue() slog.Value         { return slog.StringValue("IdempotencyRequest{redacted}") }

// Reply is constructed by a trusted mutation callback within the same tx. Its
// ResultRef must be an opaque, non-secret resource reference used to recheck
// permission on replay; response JSON may contain sensitive business data.
type Reply struct {
	Status    int
	Body      []byte
	ResultRef string
}

func (Reply) String() string               { return "IdempotencyReply{redacted}" }
func (Reply) GoString() string             { return "IdempotencyReply{redacted}" }
func (Reply) MarshalJSON() ([]byte, error) { return nil, ErrUnavailable }
func (Reply) LogValue() slog.Value         { return slog.StringValue("IdempotencyReply{redacted}") }

type Result struct {
	Status    int
	Body      []byte
	ResultRef string
	ExpiresAt time.Time
	Replayed  bool
}

func (Result) String() string               { return "IdempotencyResult{redacted}" }
func (Result) GoString() string             { return "IdempotencyResult{redacted}" }
func (Result) MarshalJSON() ([]byte, error) { return nil, ErrUnavailable }
func (Result) LogValue() slog.Value         { return slog.StringValue("IdempotencyResult{redacted}") }

// Authorize must check current permission, age and resource access. Its first
// check receives an empty resultRef and status 0; after mutation or before
// replay it receives the actual non-secret reference and response status. A
// callback must bind successful responses to their resource, and may accept an
// unscoped reference only for an explicitly non-sensitive failure. Lock every mutable permission or ownership
// row (FOR UPDATE/SHARE) until commit, or enforce equivalent atomic conditions.
// Return ErrForbidden for denied access and ErrUnavailable for uncertain
// failures. Never disclose cached content here.
type Authorize func(context.Context, ReadTransaction, Actor, string, int) error

// Mutate uses only the restricted transaction and performs database-only work.
// It must validate the operation schema and response and never perform external
// side effects. Return a JSON Reply with 409, 412, or 422 for a deterministic
// business rejection; its database changes are rolled back to a savepoint and
// the failure response is cached. A transient error rolls back the whole tx.
// Domain uniqueness and optimistic versions remain separate responsibilities.
type Mutate func(context.Context, Transaction, Actor) (Reply, error)

type Store struct {
	sessions       *session.Store
	hashKey        []byte
	cipherKey      []byte
	keyID          string
	epoch          [32]byte
	reads          map[string]struct{}
	writes         map[string]struct{}
	validateResult ResultValidator
	ttl            time.Duration
	now            func() time.Time
}

func (Store) String() string               { return "IdempotencyStore{redacted}" }
func (Store) GoString() string             { return "IdempotencyStore{redacted}" }
func (Store) MarshalJSON() ([]byte, error) { return nil, ErrUnavailable }
func (Store) LogValue() slog.Value         { return slog.StringValue("IdempotencyStore{redacted}") }

// NewStore requires independently generated secret keys and an explicit
// retention period. No production retention window is assumed by this package.
func NewStore(sessions *session.Store, hashKey, cipherKey []byte, keyID string, ttl time.Duration, policy StatementPolicy) (*Store, error) {
	if sessions == nil || len(hashKey) != 32 || len(cipherKey) != 32 || hmac.Equal(hashKey, cipherKey) ||
		!validKeyID(keyID) || ttl < time.Second || ttl%time.Microsecond != 0 {
		return nil, ErrInvalid
	}
	reads, writes, err := compilePolicy(policy)
	if err != nil {
		return nil, ErrInvalid
	}
	s := &Store{sessions: sessions, hashKey: append([]byte(nil), hashKey...), cipherKey: append([]byte(nil), cipherKey...),
		keyID: keyID, reads: reads, writes: writes, validateResult: policy.ValidateResult, ttl: ttl, now: time.Now}
	mac := hmac.New(sha256.New, s.hashKey)
	_, _ = mac.Write(framed([]byte("nerae/idempotency/epoch/v1"), []byte(keyID), s.cipherKey))
	copy(s.epoch[:], mac.Sum(nil))
	return s, nil
}

func validKeyID(value string) bool {
	if len(value) < 1 || len(value) > 64 {
		return false
	}
	for _, r := range value {
		if !(r >= 'a' && r <= 'z') && !(r >= 'A' && r <= 'Z') && !(r >= '0' && r <= '9') && r != '-' && r != '_' && r != '.' {
			return false
		}
	}
	return true
}

func validRef(value string) bool {
	if len(value) < 1 || len(value) > 256 {
		return false
	}
	for _, r := range value {
		if !(r >= 'a' && r <= 'z') && !(r >= 'A' && r <= 'Z') && !(r >= '0' && r <= '9') &&
			r != '-' && r != '_' && r != '.' && r != ':' && r != '/' {
			return false
		}
	}
	return true
}

// InitializeKey is a deployment-only transaction. A different existing key is
// rejected; key rotation and old-key retention require a separate procedure.
func (s *Store) InitializeKey(ctx context.Context, tx *sql.Tx) error {
	if s == nil || ctx == nil || tx == nil {
		return ErrUnavailable
	}
	var keyID, tag []byte
	if err := tx.QueryRowContext(ctx, `SELECT key_id, key_tag FROM transport_idempotency_key_epoch WHERE slot = 1 FOR UPDATE`).Scan(&keyID, &tag); err != nil {
		return ErrUnavailable
	}
	if keyID == nil && tag == nil {
		if _, err := tx.ExecContext(ctx, `UPDATE transport_idempotency_key_epoch SET key_id = ?, key_tag = ? WHERE slot = 1`, []byte(s.keyID), s.epoch[:]); err != nil {
			return ErrUnavailable
		}
		return nil
	}
	if string(keyID) != s.keyID || !hmac.Equal(tag, s.epoch[:]) {
		return ErrUnavailable
	}
	return nil
}

func (s *Store) checkEpoch(ctx context.Context, tx *sql.Tx) error {
	var keyID, tag []byte
	if err := tx.QueryRowContext(ctx, `SELECT key_id, key_tag FROM transport_idempotency_key_epoch WHERE slot = 1 FOR SHARE`).Scan(&keyID, &tag); err != nil ||
		string(keyID) != s.keyID || !hmac.Equal(tag, s.epoch[:]) {
		return ErrUnavailable
	}
	return nil
}

func framed(parts ...[]byte) []byte {
	var result []byte
	var length [8]byte
	for _, part := range parts {
		binary.BigEndian.PutUint64(length[:], uint64(len(part)))
		result = append(result, length[:]...)
		result = append(result, part...)
	}
	return result
}

func (s *Store) digest(actor uint64, request Request) []byte {
	mac := hmac.New(sha256.New, s.hashKey)
	_, _ = mac.Write(framed([]byte("nerae/idempotency/request/v1"), []byte(strconv.FormatUint(actor, 10)), []byte(request.Method),
		[]byte(request.Path), []byte(request.RawQuery), []byte(request.Key), request.Body))
	return mac.Sum(nil)
}

func associated(actor uint64, request Request, digest []byte, status int, ref string, expires time.Time) []byte {
	return framed([]byte("nerae/idempotency/response/v1"), []byte(strconv.FormatUint(actor, 10)), []byte(request.Method),
		[]byte(request.Path), []byte(request.Key), digest, []byte(strconv.Itoa(status)), []byte(ref), []byte(sqlTime(expires)))
}

func (s *Store) seal(body, aad []byte) ([]byte, error) {
	block, err := aes.NewCipher(s.cipherKey)
	if err != nil {
		return nil, ErrUnavailable
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, ErrUnavailable
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, ErrUnavailable
	}
	return aead.Seal(nonce, nonce, body, aad), nil
}

func (s *Store) open(encrypted, aad []byte) ([]byte, error) {
	block, err := aes.NewCipher(s.cipherKey)
	if err != nil {
		return nil, ErrUnavailable
	}
	aead, err := cipher.NewGCM(block)
	if err != nil || len(encrypted) < aead.NonceSize()+aead.Overhead() {
		return nil, ErrUnavailable
	}
	size := aead.NonceSize()
	body, err := aead.Open(nil, encrypted[:size], encrypted[size:], aad)
	if err != nil {
		return nil, ErrUnavailable
	}
	if _, err := canonicalJSON(body); err != nil {
		return nil, ErrUnavailable
	}
	return body, nil
}

func sqlTime(value time.Time) string {
	return value.UTC().Truncate(time.Microsecond).Format("2006-01-02 15:04:05.000000")
}

func statusState(status int) string {
	if status >= 200 && status <= 299 {
		return "succeeded"
	}
	if status == 409 || status == 412 || status == 422 {
		return "failed"
	}
	return ""
}

// PurgeExpired is for trusted maintenance, never an HTTP endpoint. It deletes
// only expired terminal results in a bounded batch, leaving processing rows and
// domain uniqueness constraints untouched. The caller controls scheduling.
func (s *Store) PurgeExpired(ctx context.Context, db *sql.DB, maxRows int) (int64, error) {
	if s == nil || s.now == nil || ctx == nil || db == nil {
		return 0, ErrUnavailable
	}
	if maxRows < 1 || maxRows > 1000 {
		return 0, ErrInvalid
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, ErrUnavailable
	}
	defer tx.Rollback()
	if err := s.checkEpoch(ctx, tx); err != nil {
		return 0, err
	}
	now := s.now().UTC().Truncate(time.Microsecond)
	if now.IsZero() {
		return 0, ErrUnavailable
	}
	deleted, err := tx.ExecContext(ctx, `DELETE FROM transport_idempotency_record
		WHERE state IN ('succeeded', 'failed') AND expires_at <= ? ORDER BY expires_at LIMIT ?`, sqlTime(now), maxRows)
	if err != nil {
		return 0, ErrUnavailable
	}
	count, err := deleted.RowsAffected()
	if err != nil || count < 0 || count > int64(maxRows) {
		return 0, ErrUnavailable
	}
	if err := tx.Commit(); err != nil {
		return 0, ErrOutcomeUnknown
	}
	return count, nil
}

func authorizeError(err error) error {
	if errors.Is(err, ErrForbidden) {
		return ErrForbidden
	}
	return ErrUnavailable
}
func mutationError(err error) error {
	switch {
	case errors.Is(err, ErrInvalid):
		return ErrInvalid
	case errors.Is(err, ErrForbidden):
		return ErrForbidden
	case errors.Is(err, ErrConflict):
		return ErrConflict
	case errors.Is(err, ErrInProgress):
		return ErrInProgress
	default:
		return ErrUnavailable
	}
}

// Execute authenticates a live session and owns one SQL transaction. The
// trusted callbacks must use that transaction only. On an uncertain commit no
// response is released: retrying the same request/key will recover a committed
// encrypted result, or run after a rollback. The same key with another request
// conflicts until expiry. This does not cover SMTP or other external effects.
func (s *Store) Execute(ctx context.Context, db *sql.DB, token string, input Request, authorize Authorize, mutate Mutate) (Result, error) {
	if s == nil || s.sessions == nil || s.now == nil || ctx == nil || db == nil || authorize == nil || mutate == nil {
		return Result{}, ErrUnavailable
	}
	request, err := normalizeRequest(input)
	if err != nil {
		return Result{}, ErrInvalid
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return Result{}, ErrUnavailable
	}
	defer tx.Rollback()
	principal, err := s.sessions.Authenticate(ctx, tx, token)
	if errors.Is(err, session.ErrInvalidSession) {
		return Result{}, ErrAuthentication
	}
	if err != nil || principal == nil {
		return Result{}, ErrUnavailable
	}
	before, err := principal.ForTransaction(ctx, tx)
	if err != nil || before.SubjectID == 0 {
		return Result{}, ErrUnavailable
	}
	readView := accessView{tx: tx, reads: s.reads}
	view := transactionView{accessView: readView, writes: s.writes}
	currentActor := Actor{subjectID: before.SubjectID, ageVerified: before.AgeVerified}
	if err := authorize(ctx, readView, currentActor, "", 0); err != nil {
		return Result{}, authorizeError(err)
	}
	if err := s.checkEpoch(ctx, tx); err != nil {
		return Result{}, err
	}
	var claim [32]byte
	if _, err := rand.Read(claim[:]); err != nil {
		return Result{}, ErrUnavailable
	}
	digest := s.digest(before.SubjectID, request)
	now := s.now().UTC().Truncate(time.Microsecond)
	expires := now.Add(s.ttl)
	if now.IsZero() || now.Year() < 1000 || expires.Year() > 9999 || !expires.After(now) {
		return Result{}, ErrUnavailable
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO transport_idempotency_record
		(actor_id, method, route, request_key, request_hmac, claim_token, state, created_at, expires_at)
		VALUES (?, ?, ?, ?, ?, ?, 'processing', ?, ?) ON DUPLICATE KEY UPDATE actor_id = actor_id`,
		before.SubjectID, []byte(request.Method), []byte(request.Path), []byte(request.Key), digest, claim[:], sqlTime(now), sqlTime(expires))
	if err != nil {
		return Result{}, ErrUnavailable
	}
	var savedHash, savedClaim, encrypted, keyID, ref []byte
	var state, expiresRaw string
	var savedStatus sql.NullInt64
	err = tx.QueryRowContext(ctx, `SELECT request_hmac, claim_token, state, response_status, response_enc, key_id, result_ref,
		DATE_FORMAT(expires_at, '%Y-%m-%d %H:%i:%s.%f') FROM transport_idempotency_record
		WHERE actor_id = ? AND method = ? AND route = ? AND request_key = ? FOR UPDATE`,
		before.SubjectID, []byte(request.Method), []byte(request.Path), []byte(request.Key)).
		Scan(&savedHash, &savedClaim, &state, &savedStatus, &encrypted, &keyID, &ref, &expiresRaw)
	if err != nil || len(savedHash) != 32 || len(savedClaim) != 32 {
		return Result{}, ErrUnavailable
	}
	savedExpiry, err := time.Parse("2006-01-02 15:04:05.000000", expiresRaw)
	if err != nil {
		return Result{}, ErrUnavailable
	}
	lockedNow := s.now().UTC().Truncate(time.Microsecond)
	if lockedNow.IsZero() {
		return Result{}, ErrUnavailable
	}
	owned := hmac.Equal(savedClaim, claim[:]) && state == "processing"
	if !owned && (state == "succeeded" || state == "failed") && !savedExpiry.After(lockedNow) {
		expires = lockedNow.Add(s.ttl)
		if expires.Year() > 9999 || !expires.After(lockedNow) {
			return Result{}, ErrUnavailable
		}
		_, err = tx.ExecContext(ctx, `UPDATE transport_idempotency_record SET request_hmac = ?, claim_token = ?, state = 'processing',
			response_status = NULL, response_enc = NULL, key_id = NULL, result_ref = NULL, created_at = ?, completed_at = NULL, expires_at = ?
			WHERE actor_id = ? AND method = ? AND route = ? AND request_key = ?`, digest, claim[:], sqlTime(lockedNow), sqlTime(expires),
			before.SubjectID, []byte(request.Method), []byte(request.Path), []byte(request.Key))
		if err != nil {
			return Result{}, ErrUnavailable
		}
		owned = true
		savedExpiry = expires
		savedHash = digest
	}
	if !hmac.Equal(savedHash, digest) {
		return Result{}, ErrConflict
	}
	var result Result
	if owned {
		if _, err := tx.ExecContext(ctx, `SAVEPOINT nerae_idempotency_mutation`); err != nil {
			return Result{}, ErrUnavailable
		}
		reply, err := mutate(ctx, view, currentActor)
		if err != nil {
			return Result{}, mutationError(err)
		}
		responseState := statusState(reply.Status)
		if responseState == "" || !validRef(reply.ResultRef) {
			return Result{}, ErrUnavailable
		}
		body := append([]byte(nil), reply.Body...)
		if _, err := canonicalJSON(body); err != nil {
			return Result{}, ErrUnavailable
		}
		if s.validateResult != nil && s.validateResult(reply.Status, reply.ResultRef, append([]byte(nil), body...)) != nil {
			return Result{}, ErrUnavailable
		}
		if responseState == "failed" {
			if _, err := tx.ExecContext(ctx, `ROLLBACK TO SAVEPOINT nerae_idempotency_mutation`); err != nil {
				return Result{}, ErrUnavailable
			}
		}
		if _, err := tx.ExecContext(ctx, `RELEASE SAVEPOINT nerae_idempotency_mutation`); err != nil {
			return Result{}, ErrUnavailable
		}
		live, err := principal.ForTransaction(ctx, tx)
		if err != nil || live.SubjectID != before.SubjectID {
			return Result{}, ErrUnavailable
		}
		currentActor = Actor{subjectID: live.SubjectID, ageVerified: live.AgeVerified}
		if err := authorize(ctx, readView, currentActor, reply.ResultRef, reply.Status); err != nil {
			return Result{}, authorizeError(err)
		}
		completed := s.now().UTC().Truncate(time.Microsecond)
		if completed.IsZero() || completed.Before(lockedNow) {
			return Result{}, ErrUnavailable
		}
		savedExpiry = completed.Add(s.ttl)
		if savedExpiry.Year() > 9999 || !savedExpiry.After(completed) {
			return Result{}, ErrUnavailable
		}
		sealed, err := s.seal(body, associated(before.SubjectID, request, digest, reply.Status, reply.ResultRef, savedExpiry))
		if err != nil {
			return Result{}, ErrUnavailable
		}
		updated, err := tx.ExecContext(ctx, `UPDATE transport_idempotency_record SET state = ?, response_status = ?,
			response_enc = ?, key_id = ?, result_ref = ?, completed_at = ?, expires_at = ? WHERE actor_id = ? AND method = ? AND route = ?
			AND request_key = ? AND state = 'processing' AND claim_token = ?`, responseState, reply.Status, sealed, []byte(s.keyID), []byte(reply.ResultRef),
			sqlTime(completed), sqlTime(savedExpiry), before.SubjectID, []byte(request.Method), []byte(request.Path), []byte(request.Key), claim[:])
		if err != nil {
			return Result{}, ErrUnavailable
		}
		if count, err := updated.RowsAffected(); err != nil || count != 1 {
			return Result{}, ErrUnavailable
		}
		result = Result{Status: reply.Status, Body: body, ResultRef: reply.ResultRef, ExpiresAt: savedExpiry}
	} else {
		if state == "processing" {
			return Result{}, ErrInProgress
		}
		if !validCompletedMetadata(state, savedStatus, keyID, ref, s.keyID) {
			return Result{}, ErrUnavailable
		}
		live, err := principal.ForTransaction(ctx, tx)
		if err != nil || live.SubjectID != before.SubjectID {
			return Result{}, ErrUnavailable
		}
		currentActor = Actor{subjectID: live.SubjectID, ageVerified: live.AgeVerified}
		if err := authorize(ctx, readView, currentActor, string(ref), int(savedStatus.Int64)); err != nil {
			return Result{}, authorizeError(err)
		}
		result, err = s.decodeCompleted(before.SubjectID, request, digest, state, savedStatus, encrypted, keyID, ref, savedExpiry)
		if err != nil {
			return Result{}, err
		}
	}
	after, err := principal.ForTransaction(ctx, tx)
	if err != nil || after.SubjectID != before.SubjectID || after.AgeVerified != currentActor.ageVerified {
		return Result{}, ErrUnavailable
	}
	lastCheck := s.now().UTC().Truncate(time.Microsecond)
	if lastCheck.IsZero() || !result.ExpiresAt.After(lastCheck) {
		return Result{}, ErrUnavailable
	}
	if err := tx.Commit(); err != nil {
		return Result{}, ErrOutcomeUnknown
	}
	return result, nil
}
