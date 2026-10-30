package application

import (
	"context"
	"database/sql"

	"nerae/api/internal/modules/workout/repository"
)

// ListFree derives the subject from the live authenticated session, checks the
// adult declaration, and performs the list read in the same transaction.
// It is not an HTTP endpoint and currently lists free workouts only.
func (s *Service) ListFree(ctx context.Context, db *sql.DB, token string, filter repository.ListFilter) (repository.SessionPage, error) {
	var result repository.SessionPage
	err := s.inTransaction(ctx, db, token, func(tx *sql.Tx, subjectID uint64) error {
		var err error
		result, err = s.free.ListFree(ctx, tx, subjectID, filter)
		return err
	})
	if err != nil {
		return repository.SessionPage{}, err
	}
	return result, nil
}
