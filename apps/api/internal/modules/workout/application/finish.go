package application

import (
	"context"
	"database/sql"
)

// CompleteFree confirms an existing free record. HTTP idempotency and optional
// finish fields must be integrated separately before exposing the API route.
func (s *Service) CompleteFree(ctx context.Context, db *sql.DB, token string, sessionID, expectedVersion uint64) (uint64, error) {
	var version uint64
	err := s.inTransaction(ctx, db, token, func(tx *sql.Tx, subjectID uint64) error {
		var err error
		version, err = s.records.CompleteFree(ctx, tx, subjectID, sessionID, expectedVersion)
		return err
	})
	if err != nil {
		return 0, err
	}
	return version, nil
}
