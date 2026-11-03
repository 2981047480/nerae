package session

import (
	"context"
	"database/sql"
	"strconv"
	"time"
)

// VerifyCurrentSession implements the transaction-bound verification needed
// by encrypted approval-exchange receipts. It authenticates the signed token,
// locks and rechecks the identity, user, device and session in the caller's
// transaction, and returns both signed and stored expiries. Its result does not
// authorize an approval exchange or release a token by itself.
func (s *Store) VerifyCurrentSession(ctx context.Context, tx *sql.Tx, token string) (string, string, time.Time, time.Time, error) {
	if s == nil || ctx == nil || tx == nil || token == "" {
		return "", "", time.Time{}, time.Time{}, ErrSessionUnavailable
	}
	principal, err := s.Authenticate(ctx, tx, token)
	if err != nil {
		return "", "", time.Time{}, time.Time{}, err
	}
	details, err := principal.ForTransaction(ctx, tx)
	if err != nil {
		return "", "", time.Time{}, time.Time{}, ErrSessionUnavailable
	}
	claims, err := s.verifySessionToken(token)
	if err != nil {
		return "", "", time.Time{}, time.Time{}, err
	}
	subject := strconv.FormatUint(details.SubjectID, 10)
	now := s.now().UTC()
	if details.SubjectID == 0 || details.SessionID == "" || subject != claims.SubjectID || details.SessionID != claims.SessionID ||
		principal.expiresAt.IsZero() || !principal.expiresAt.Equal(claims.ExpiresAt) || now.IsZero() || !principal.expiresAt.After(now) {
		return "", "", time.Time{}, time.Time{}, ErrSessionUnavailable
	}
	return subject, details.SessionID, claims.ExpiresAt, principal.expiresAt, nil
}
