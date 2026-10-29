package application

import (
	"context"
	"database/sql"
)

// CompleteFree confirms an existing free record. HTTP idempotency and optional
// finish fields must be integrated separately before exposing the API route.
func (s *Service) CompleteFree(ctx context.Context, db *sql.DB, token string, sessionID, expectedVersion uint64) (uint64, error) {
	return s.CompleteOrderedFree(ctx, db, token, sessionID, expectedVersion)
}
