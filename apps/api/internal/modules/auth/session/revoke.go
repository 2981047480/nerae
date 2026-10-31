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
		return classifyPrincipalRecheckError(principal, err)
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
	if err != nil {
		return ErrSessionUnavailable
	}
	if changed == 0 {
		return classifyUnchangedSession(ctx, tx, principal.sessionID)
	}
	if changed != 1 {
		return ErrSessionUnavailable
	}
	if err := tx.Commit(); err != nil {
		return ErrSessionUnavailable
	}
	return nil
}

func classifyPrincipalRecheckError(principal *Principal, err error) error {
	if errors.Is(err, ErrInvalidSession) {
		return ErrInvalidSession
	}
	if principal != nil && principal.now != nil {
		now := principal.now().UTC()
		if !now.IsZero() && !principal.expiresAt.After(now) {
			return ErrInvalidSession
		}
	}
	return ErrSessionUnavailable
}

// classifyUnchangedSession uses a locking current read after a conditional
// UPDATE affected no rows. Expiry can cross between the earlier liveness read
// and the UPDATE; classify that as an invalid credential while preserving
// genuine database failures as unavailable.
func classifyUnchangedSession(ctx context.Context, tx *sql.Tx, sessionID string) error {
	var state string
	var live bool
	err := tx.QueryRowContext(ctx, `SELECT state, expires_at > UTC_TIMESTAMP(6) FROM auth_session WHERE id = ? FOR UPDATE`, []byte(sessionID)).Scan(&state, &live)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrInvalidSession
	}
	if err != nil {
		return ErrSessionUnavailable
	}
	return classifyCurrentSessionRow(state, live)
}

func classifyCurrentSessionRow(state string, live bool) error {
	if state == "revoked" || state == "active" && !live {
		return ErrInvalidSession
	}
	// An active, unexpired locked row should have matched the conditional
	// update, so a zero row count in this state is an unexplained storage result.
	// Unknown or damaged states are storage failures, not proof of invalidity.
	return ErrSessionUnavailable
}
