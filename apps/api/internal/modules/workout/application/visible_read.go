package application

import (
	"context"
	"database/sql"
	"errors"

	"nerae/api/internal/modules/workout/repository"
)

// ErrWorkoutNotFound intentionally covers both missing and non-owned records.
// It does not indicate whether another subject owns the requested identifier.
var ErrWorkoutNotFound = errors.New("workout not found")

// ReadVisibleOrderedFree distinguishes an inaccessible record from a storage
// failure without revealing ownership. Authentication, age, visibility and the
// actual ordered read are checked in the same transaction. Current persistence
// only supports free workouts; planned records need their own authorization.
func (s *Service) ReadVisibleOrderedFree(ctx context.Context, db *sql.DB, token string, sessionID uint64) (repository.OrderedSnapshot, error) {
	if sessionID == 0 {
		return repository.OrderedSnapshot{}, repository.ErrInvalid
	}
	var result repository.OrderedSnapshot
	err := s.inTransaction(ctx, db, token, func(tx *sql.Tx, subjectID uint64) error {
		var found uint64
		var format uint8
		err := tx.QueryRowContext(ctx, `SELECT id, record_format FROM workout_session WHERE id = ? AND user_id = ? AND source = 'free' FOR UPDATE`, sessionID, subjectID).Scan(&found, &format)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrWorkoutNotFound
		}
		if err != nil || found != sessionID {
			return ErrUnavailable
		}
		if format == 2 {
			return repository.ErrClientUpgradeRequired
		}
		if format != 1 {
			return ErrUnavailable
		}
		result, err = s.records.ReadOrderedFree(ctx, tx, subjectID, sessionID)
		return err
	})
	if err != nil {
		return repository.OrderedSnapshot{}, err
	}
	return result, nil
}
