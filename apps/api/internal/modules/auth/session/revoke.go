package session

import (
	"context"
	"database/sql"
	"errors"
)

// RevokeCurrent authenticates and revokes exactly the session represented by
// token. It commits before returning nil. A commit error is deliberately
// reported as unavailable because the database may have committed despite the
// lost acknowledgement.
func (s *Store) RevokeCurrent(ctx context.Context, db *sql.DB, token string) error {
	if s == nil || ctx == nil || db == nil || token == "" {
		return ErrSessionUnavailable
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return ErrSessionUnavailable
	}
	defer func() { _ = tx.Rollback() }()

	principal, err := s.Authenticate(ctx, tx, token)
	if err != nil {
		return err
	}
	if _, err := principal.ForTransaction(ctx, tx); err != nil {
		if errors.Is(err, ErrInvalidSession) {
			return ErrInvalidSession
		}
		return ErrSessionUnavailable
	}

	// Authenticate checks the injected application clock after acquiring the
	// locks. Check the authoritative database clock as well, after lock waits,
	// so clock skew cannot revoke a session that is already expired in storage.
	var live int
	if err := tx.QueryRowContext(ctx, `SELECT 1 FROM auth_session WHERE id = ? AND state = 'active' AND expires_at > UTC_TIMESTAMP(6)`, []byte(principal.sessionID)).Scan(&live); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrInvalidSession
		}
		return ErrSessionUnavailable
	}
	result, err := tx.ExecContext(ctx, `UPDATE auth_session SET state = 'revoked', revoked_at = UTC_TIMESTAMP(6) WHERE id = ? AND state = 'active' AND expires_at > UTC_TIMESTAMP(6)`, []byte(principal.sessionID))
	if err != nil {
		return ErrSessionUnavailable
	}
	changed, err := result.RowsAffected()
	if err != nil || changed != 1 {
		return ErrSessionUnavailable
	}
	if err := tx.Commit(); err != nil {
		return ErrSessionUnavailable
	}
	return nil
}
