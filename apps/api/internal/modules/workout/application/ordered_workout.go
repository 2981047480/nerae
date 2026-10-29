package application

import (
	"context"
	"database/sql"

	"nerae/api/internal/modules/workout/domain"
	"nerae/api/internal/modules/workout/repository"
)

// ReadOrderedFree preserves the same live-session, subject and age checks as
// other workout operations and returns an internal, redacted snapshot.
func (s *Service) ReadOrderedFree(ctx context.Context, db *sql.DB, token string, sessionID uint64) (repository.OrderedSnapshot, error) {
	var result repository.OrderedSnapshot
	err := s.inTransaction(ctx, db, token, func(tx *sql.Tx, subjectID uint64) error {
		var err error
		result, err = s.records.ReadOrderedFree(ctx, tx, subjectID, sessionID)
		return err
	})
	if err != nil {
		return repository.OrderedSnapshot{}, err
	}
	return result, nil
}

// SaveOrderedSets requires the complete projected entry order. The subject,
// age, session and resource are rechecked within the transaction that writes
// both groups and ordering. HTTP idempotency remains a separate requirement.
func (s *Service) SaveOrderedSets(ctx context.Context, db *sql.DB, token string, sessionID, expectedVersion uint64, batch domain.Batch, order []string) (uint64, bool, error) {
	var version uint64
	var changed bool
	err := s.inTransaction(ctx, db, token, func(tx *sql.Tx, subjectID uint64) error {
		var err error
		version, changed, err = s.records.ApplyOrderedBatch(ctx, tx, subjectID, sessionID, expectedVersion, batch, order)
		return err
	})
	if err != nil {
		return 0, false, err
	}
	return version, changed, nil
}

// CompleteOrderedFree validates the ordered snapshot before the existing
// completion transition, under the same session lock and authorization tx.
// This does not implement the HTTP finish request, optional fields, or replay.
func (s *Service) CompleteOrderedFree(ctx context.Context, db *sql.DB, token string, sessionID, expectedVersion uint64) (uint64, error) {
	var version uint64
	err := s.inTransaction(ctx, db, token, func(tx *sql.Tx, subjectID uint64) error {
		if _, err := s.records.ReadOrderedFree(ctx, tx, subjectID, sessionID); err != nil {
			return err
		}
		var err error
		version, err = s.records.CompleteFree(ctx, tx, subjectID, sessionID, expectedVersion)
		return err
	})
	if err != nil {
		return 0, err
	}
	return version, nil
}
