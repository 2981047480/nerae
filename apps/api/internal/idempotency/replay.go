package idempotency

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"errors"
	"time"

	"nerae/api/internal/modules/auth/session"
)

// ErrReplayUnavailable covers both absent and expired receipts so callers
// cannot infer whether a result once existed.
var ErrReplayUnavailable = errors.New("stored idempotency result unavailable")

func validCompletedMetadata(state string, status sql.NullInt64, keyID, ref []byte, expectedKeyID string) bool {
	return status.Valid && statusState(int(status.Int64)) != "" && statusState(int(status.Int64)) == state &&
		string(keyID) == expectedKeyID && validRef(string(ref))
}

// decodeCompleted validates and decrypts a committed terminal result after the
// caller has authenticated the current actor, locked the idempotency row, and
// rechecked authorization for ref/status. It is shared by Execute's ordinary
// replay path and ReplayExisting so both enforce the same response policy.
func (s *Store) decodeCompleted(actor uint64, request Request, digest []byte, state string,
	status sql.NullInt64, encrypted, keyID, ref []byte, expires time.Time) (Result, error) {
	if !validCompletedMetadata(state, status, keyID, ref, s.keyID) {
		return Result{}, ErrUnavailable
	}
	body, err := s.open(encrypted, associated(actor, request, digest, int(status.Int64), string(ref), expires))
	if err != nil {
		return Result{}, ErrUnavailable
	}
	if s.validateResult != nil && s.validateResult(int(status.Int64), string(ref), append([]byte(nil), body...)) != nil {
		return Result{}, ErrUnavailable
	}
	return Result{Status: int(status.Int64), Body: body, ResultRef: string(ref), ExpiresAt: expires, Replayed: true}, nil
}

// ReplayExisting returns only an already-committed, live response for this
// authenticated actor and exact normalized request. It never claims a key,
// refreshes expiry, or invokes a mutation. Absence and expiry intentionally
// share ErrReplayUnavailable. A commit error never releases the cached body.
func (s *Store) ReplayExisting(ctx context.Context, db *sql.DB, token string, input Request, authorize Authorize) (Result, error) {
	if s == nil || s.sessions == nil || s.now == nil || ctx == nil || db == nil || authorize == nil {
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
	actor, err := actorFromPrincipalDetails(before)
	if err != nil {
		return Result{}, ErrUnavailable
	}
	readView := accessView{tx: tx, reads: s.reads}
	if err := authorize(ctx, readView, actor, "", 0); err != nil {
		return Result{}, authorizeError(err)
	}
	if err := s.checkEpoch(ctx, tx); err != nil {
		return Result{}, err
	}
	digest := s.digest(before.SubjectID, request)
	var savedHash, claim, encrypted, keyID, ref []byte
	var state, expiresRaw string
	var status sql.NullInt64
	err = tx.QueryRowContext(ctx, `SELECT request_hmac, claim_token, state, response_status, response_enc, key_id, result_ref,
		DATE_FORMAT(expires_at, '%Y-%m-%d %H:%i:%s.%f') FROM transport_idempotency_record
		WHERE actor_id = ? AND method = ? AND route = ? AND request_key = ? FOR UPDATE`,
		before.SubjectID, []byte(request.Method), []byte(request.Path), []byte(request.Key)).
		Scan(&savedHash, &claim, &state, &status, &encrypted, &keyID, &ref, &expiresRaw)
	if errors.Is(err, sql.ErrNoRows) {
		return Result{}, ErrReplayUnavailable
	}
	if err != nil || len(savedHash) != sha256.Size || len(claim) != 32 {
		return Result{}, ErrUnavailable
	}
	expires, err := time.Parse("2006-01-02 15:04:05.000000", expiresRaw)
	if err != nil {
		return Result{}, ErrUnavailable
	}
	lockedNow := s.now().UTC().Truncate(time.Microsecond)
	if lockedNow.IsZero() {
		return Result{}, ErrUnavailable
	}
	if !expires.After(lockedNow) {
		return Result{}, ErrReplayUnavailable
	}
	if !hmac.Equal(savedHash, digest) {
		return Result{}, ErrConflict
	}
	if state == "processing" {
		if status.Valid || len(encrypted) != 0 || len(keyID) != 0 || len(ref) != 0 {
			return Result{}, ErrUnavailable
		}
		return Result{}, ErrInProgress
	}
	if state != "succeeded" && state != "failed" {
		return Result{}, ErrUnavailable
	}
	if !validCompletedMetadata(state, status, keyID, ref, s.keyID) {
		return Result{}, ErrUnavailable
	}
	current, err := principal.ForTransaction(ctx, tx)
	if err != nil || !samePrincipalSession(before, current) {
		return Result{}, ErrUnavailable
	}
	actor, err = actorFromPrincipalDetails(current)
	if err != nil {
		return Result{}, ErrUnavailable
	}
	if err := authorize(ctx, readView, actor, string(ref), int(status.Int64)); err != nil {
		return Result{}, authorizeError(err)
	}
	result, err := s.decodeCompleted(before.SubjectID, request, digest, state, status, encrypted, keyID, ref, expires)
	if err != nil {
		return Result{}, err
	}
	after, err := principal.ForTransaction(ctx, tx)
	if err != nil || !samePrincipalSession(before, after) || after.AgeVerified != actor.ageVerified {
		return Result{}, ErrUnavailable
	}
	lastCheck := s.now().UTC().Truncate(time.Microsecond)
	if lastCheck.IsZero() || !result.ExpiresAt.After(lastCheck) {
		return Result{}, ErrReplayUnavailable
	}
	if err := tx.Commit(); err != nil {
		return Result{}, ErrOutcomeUnknown
	}
	return result, nil
}
