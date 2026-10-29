package session

import (
	"context"
	"database/sql"
	"errors"
)

// ErrAdultDeclarationRequired indicates that no affirmative declaration was
// submitted. A declaration is not a verified identity or exact age.
var ErrAdultDeclarationRequired = errors.New("adult self-declaration required")

// DeclareAdult records only the authenticated subject's explicit 18+ self-
// declaration. It does not collect a birth date or prove actual age. Repeats
// preserve the original declaration time. Success is returned only after the
// transaction commits; an ambiguous commit can be retried safely.
func (s *Store) DeclareAdult(ctx context.Context, db *sql.DB, token string, isAdult bool) error {
	if s == nil || s.now == nil || ctx == nil || db == nil {
		return ErrSessionUnavailable
	}
	if !isAdult {
		return ErrAdultDeclarationRequired
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return ErrSessionUnavailable
	}
	defer tx.Rollback()
	principal, err := s.Authenticate(ctx, tx, token)
	if err != nil {
		return ErrSessionUnavailable
	}
	if err := s.declareAdultInTransaction(ctx, tx, principal, isAdult); err != nil {
		return err
	}
	// Recheck all locked authorization state and token expiry before commit.
	details, err := principal.ForTransaction(ctx, tx)
	if err != nil || !details.AgeVerified {
		return ErrSessionUnavailable
	}
	if err := tx.Commit(); err != nil {
		return ErrSessionUnavailable
	}
	return nil
}

// ClearAdultDeclaration lets the authenticated subject explicitly withdraw or
// correct the self-declaration. It does not delete their data or log them out;
// adult-only operations must reject the resulting un-declared state.
func (s *Store) ClearAdultDeclaration(ctx context.Context, db *sql.DB, token string) error {
	if s == nil || s.now == nil || ctx == nil || db == nil {
		return ErrSessionUnavailable
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return ErrSessionUnavailable
	}
	defer tx.Rollback()
	principal, err := s.Authenticate(ctx, tx, token)
	if err != nil {
		return ErrSessionUnavailable
	}
	if err := s.clearAdultInTransaction(ctx, tx, principal); err != nil {
		return err
	}
	details, err := principal.ForTransaction(ctx, tx)
	if err != nil || details.AgeVerified {
		return ErrSessionUnavailable
	}
	if err := tx.Commit(); err != nil {
		return ErrSessionUnavailable
	}
	return nil
}

func (s *Store) clearAdultInTransaction(ctx context.Context, tx *sql.Tx, principal *Principal) error {
	if s == nil || s.now == nil || ctx == nil || tx == nil || principal == nil || principal.store != s {
		return ErrSessionUnavailable
	}
	details, err := principal.ForTransaction(ctx, tx)
	if err != nil || details.SubjectID == 0 {
		return ErrSessionUnavailable
	}
	if details.AgeVerified {
		now := s.now()
		if now.IsZero() {
			return ErrSessionUnavailable
		}
		result, err := tx.ExecContext(ctx, `UPDATE user SET age_verified_at = NULL, updated_at = ? WHERE id = ? AND status = 'active' AND age_verified_at IS NOT NULL`, utcTimestamp(now), details.SubjectID)
		if err != nil {
			return ErrSessionUnavailable
		}
		rows, err := result.RowsAffected()
		if err != nil || rows != 1 {
			return ErrSessionUnavailable
		}
	}
	after, err := principal.ForTransaction(ctx, tx)
	if err != nil || after.SubjectID != details.SubjectID || after.AgeVerified {
		return ErrSessionUnavailable
	}
	return nil
}

func (s *Store) declareAdultInTransaction(ctx context.Context, tx *sql.Tx, principal *Principal, isAdult bool) error {
	if s == nil || s.now == nil || ctx == nil || tx == nil || principal == nil || principal.store != s {
		return ErrSessionUnavailable
	}
	if !isAdult {
		return ErrAdultDeclarationRequired
	}
	details, err := principal.ForTransaction(ctx, tx)
	if err != nil || details.SubjectID == 0 {
		return ErrSessionUnavailable
	}
	if !details.AgeVerified {
		now := s.now()
		if now.IsZero() {
			return ErrSessionUnavailable
		}
		timestamp := utcTimestamp(now)
		result, err := tx.ExecContext(ctx, `UPDATE user SET age_verified_at = ?, updated_at = ? WHERE id = ? AND status = 'active' AND age_verified_at IS NULL`, timestamp, timestamp, details.SubjectID)
		if err != nil {
			return ErrSessionUnavailable
		}
		rows, err := result.RowsAffected()
		if err != nil || rows != 1 {
			return ErrSessionUnavailable
		}
	}
	after, err := principal.ForTransaction(ctx, tx)
	if err != nil || after.SubjectID != details.SubjectID || !after.AgeVerified {
		return ErrSessionUnavailable
	}
	return nil
}
