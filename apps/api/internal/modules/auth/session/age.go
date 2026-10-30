package session

import (
	"context"
	"database/sql"
	"errors"
)

// ErrAdultDeclarationRequired indicates that no affirmative declaration was
// submitted. A declaration is not a verified identity or exact age.
var ErrAdultDeclarationRequired = errors.New("adult self-declaration required")

// ErrAgeDeclarationConflict indicates that the caller's declaration version is
// stale. The current state is unchanged.
var ErrAgeDeclarationConflict = errors.New("adult declaration version conflict")

// ErrAgeDeclarationVersionInvalid indicates that an input version cannot be
// represented safely by JSON clients or is outside the positive version range.
var ErrAgeDeclarationVersionInvalid = errors.New("adult declaration version invalid")

// ErrAgeDeclarationVersionExhausted indicates the declaration version cannot
// be advanced without exceeding the JSON safe-integer range.
var ErrAgeDeclarationVersionExhausted = errors.New("adult declaration version exhausted")

const maxAgeDeclarationVersion uint64 = 1<<53 - 1

// DeclareAdultAtVersion records an explicit declaration only when expectedVersion
// matches the current per-user declaration version. Every accepted action
// advances that version, including affirming an already active declaration.
// It is an internal transaction primitive, not a public HTTP handler.
func (s *Store) DeclareAdultAtVersion(ctx context.Context, db *sql.DB, token string, expectedVersion uint64, isAdult bool) (uint64, error) {
	if s == nil || s.now == nil || ctx == nil || db == nil {
		return 0, ErrSessionUnavailable
	}
	if !isAdult {
		return 0, ErrAdultDeclarationRequired
	}
	if !validAgeDeclarationVersion(expectedVersion) {
		return 0, ErrAgeDeclarationVersionInvalid
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, ErrSessionUnavailable
	}
	defer tx.Rollback()
	principal, err := s.Authenticate(ctx, tx, token)
	if err != nil {
		return 0, err
	}
	nextVersion, err := s.declareAdultAtVersionInTransaction(ctx, tx, principal, expectedVersion, isAdult)
	if err != nil {
		return 0, err
	}
	details, err := principal.ForTransaction(ctx, tx)
	if err != nil || !details.AgeVerified {
		return 0, ErrSessionUnavailable
	}
	if err := tx.Commit(); err != nil {
		return 0, ErrSessionUnavailable
	}
	return nextVersion, nil
}

// ClearAdultDeclarationAtVersion explicitly withdraws the declaration only
// when expectedVersion matches. Even withdrawing an already empty state is an
// accepted action and advances the version, invalidating delayed older writes.
// It is an internal transaction primitive, not a public HTTP handler.
func (s *Store) ClearAdultDeclarationAtVersion(ctx context.Context, db *sql.DB, token string, expectedVersion uint64) (uint64, error) {
	if s == nil || s.now == nil || ctx == nil || db == nil {
		return 0, ErrSessionUnavailable
	}
	if !validAgeDeclarationVersion(expectedVersion) {
		return 0, ErrAgeDeclarationVersionInvalid
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, ErrSessionUnavailable
	}
	defer tx.Rollback()
	principal, err := s.Authenticate(ctx, tx, token)
	if err != nil {
		return 0, err
	}
	nextVersion, err := s.clearAdultDeclarationAtVersionInTransaction(ctx, tx, principal, expectedVersion)
	if err != nil {
		return 0, err
	}
	details, err := principal.ForTransaction(ctx, tx)
	if err != nil || details.AgeVerified {
		return 0, ErrSessionUnavailable
	}
	if err := tx.Commit(); err != nil {
		return 0, ErrSessionUnavailable
	}
	return nextVersion, nil
}

func validAgeDeclarationVersion(version uint64) bool {
	return version > 0 && version <= maxAgeDeclarationVersion
}

func (s *Store) declareAdultAtVersionInTransaction(ctx context.Context, tx *sql.Tx, principal *Principal, expectedVersion uint64, isAdult bool) (uint64, error) {
	if s == nil || s.now == nil || ctx == nil || tx == nil || principal == nil || principal.store != s {
		return 0, ErrSessionUnavailable
	}
	if !isAdult {
		return 0, ErrAdultDeclarationRequired
	}
	details, err := principal.ForTransaction(ctx, tx)
	if err != nil || details.SubjectID == 0 {
		return 0, ErrSessionUnavailable
	}
	current, declared, err := readAgeDeclarationVersion(ctx, tx, details.SubjectID)
	if err != nil {
		return 0, ErrSessionUnavailable
	}
	if current != expectedVersion {
		return 0, ErrAgeDeclarationConflict
	}
	if current == maxAgeDeclarationVersion {
		return 0, ErrAgeDeclarationVersionExhausted
	}
	now := s.now()
	if now.IsZero() {
		return 0, ErrSessionUnavailable
	}
	timestamp := utcTimestamp(now)
	var result sql.Result
	if declared {
		result, err = tx.ExecContext(ctx, `UPDATE user SET age_declaration_version = ?, updated_at = ? WHERE id = ? AND status = 'active' AND age_declaration_version = ?`, current+1, timestamp, details.SubjectID, current)
	} else {
		result, err = tx.ExecContext(ctx, `UPDATE user SET age_verified_at = ?, age_declaration_version = ?, updated_at = ? WHERE id = ? AND status = 'active' AND age_verified_at IS NULL AND age_declaration_version = ?`, timestamp, current+1, timestamp, details.SubjectID, current)
	}
	if err != nil {
		return 0, ErrSessionUnavailable
	}
	rows, err := result.RowsAffected()
	if err != nil || rows != 1 {
		return 0, ErrSessionUnavailable
	}
	after, err := principal.ForTransaction(ctx, tx)
	if err != nil || after.SubjectID != details.SubjectID || !after.AgeVerified {
		return 0, ErrSessionUnavailable
	}
	return current + 1, nil
}

func (s *Store) clearAdultDeclarationAtVersionInTransaction(ctx context.Context, tx *sql.Tx, principal *Principal, expectedVersion uint64) (uint64, error) {
	if s == nil || s.now == nil || ctx == nil || tx == nil || principal == nil || principal.store != s {
		return 0, ErrSessionUnavailable
	}
	details, err := principal.ForTransaction(ctx, tx)
	if err != nil || details.SubjectID == 0 {
		return 0, ErrSessionUnavailable
	}
	current, _, err := readAgeDeclarationVersion(ctx, tx, details.SubjectID)
	if err != nil {
		return 0, ErrSessionUnavailable
	}
	if current != expectedVersion {
		return 0, ErrAgeDeclarationConflict
	}
	if current == maxAgeDeclarationVersion {
		return 0, ErrAgeDeclarationVersionExhausted
	}
	now := s.now()
	if now.IsZero() {
		return 0, ErrSessionUnavailable
	}
	result, err := tx.ExecContext(ctx, `UPDATE user SET age_verified_at = NULL, age_declaration_version = ?, updated_at = ? WHERE id = ? AND status = 'active' AND age_declaration_version = ?`, current+1, utcTimestamp(now), details.SubjectID, current)
	if err != nil {
		return 0, ErrSessionUnavailable
	}
	rows, err := result.RowsAffected()
	if err != nil || rows != 1 {
		return 0, ErrSessionUnavailable
	}
	after, err := principal.ForTransaction(ctx, tx)
	if err != nil || after.SubjectID != details.SubjectID || after.AgeVerified {
		return 0, ErrSessionUnavailable
	}
	return current + 1, nil
}

func readAgeDeclarationVersion(ctx context.Context, tx *sql.Tx, subjectID uint64) (uint64, bool, error) {
	var version uint64
	var declared bool
	err := tx.QueryRowContext(ctx, `SELECT age_declaration_version, age_verified_at IS NOT NULL FROM user WHERE id = ? FOR UPDATE`, subjectID).Scan(&version, &declared)
	if err != nil || !validAgeDeclarationVersion(version) {
		return 0, false, ErrSessionUnavailable
	}
	return version, declared, nil
}
