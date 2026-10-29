// Package application connects authenticated sessions to workout transactions.
// It does not implement HTTP, idempotency responses, or real age verification.
package application

import (
	"context"
	"database/sql"
	"errors"

	"nerae/api/internal/modules/auth/session"
	"nerae/api/internal/modules/workout/domain"
	"nerae/api/internal/modules/workout/repository"
)

var (
	ErrAuthentication = errors.New("authentication required")
	ErrAgeRequired    = errors.New("age verification required")
	ErrUnavailable    = errors.New("workout unavailable")
)

type Service struct {
	sessions *session.Store
	free     *repository.SessionStore
	records  *repository.RecordStore
}

func New(sessions *session.Store, free *repository.SessionStore, records *repository.RecordStore) (*Service, error) {
	if sessions == nil || free == nil || records == nil {
		return nil, ErrUnavailable
	}
	return &Service{sessions: sessions, free: free, records: records}, nil
}

// inTransaction owns commit/rollback, so no result can be returned after an
// authentication failure or a partially failed workout transaction.
func (s *Service) inTransaction(ctx context.Context, db *sql.DB, token string, work func(*sql.Tx, uint64) error) error {
	if s == nil || s.sessions == nil || s.free == nil || s.records == nil || ctx == nil || db == nil || work == nil {
		return ErrUnavailable
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return ErrUnavailable
	}
	defer tx.Rollback()
	principal, err := s.sessions.Authenticate(ctx, tx, token)
	if err != nil {
		return ErrAuthentication
	}
	before, err := principal.ForTransaction(ctx, tx)
	if err != nil {
		return ErrAuthentication
	}
	if !before.AgeVerified {
		return ErrAgeRequired
	}
	if err := work(tx, before.SubjectID); err != nil {
		return err
	}
	after, err := principal.ForTransaction(ctx, tx)
	if err != nil || after.SubjectID != before.SubjectID {
		return ErrAuthentication
	}
	if !after.AgeVerified {
		return ErrAgeRequired
	}
	if err := tx.Commit(); err != nil {
		return ErrUnavailable
	}
	return nil
}

func (s *Service) CreateFree(ctx context.Context, db *sql.DB, token string, input repository.FreeSessionInput) (repository.SessionRef, error) {
	var result repository.SessionRef
	err := s.inTransaction(ctx, db, token, func(tx *sql.Tx, subjectID uint64) error {
		var err error
		result, err = s.free.CreateFree(ctx, tx, subjectID, input)
		return err
	})
	if err != nil {
		return repository.SessionRef{}, err
	}
	return result, nil
}

func (s *Service) ReadFree(ctx context.Context, db *sql.DB, token string, sessionID uint64) (repository.Snapshot, error) {
	var result repository.Snapshot
	err := s.inTransaction(ctx, db, token, func(tx *sql.Tx, subjectID uint64) error {
		var err error
		result, err = s.records.ReadFree(ctx, tx, subjectID, sessionID)
		return err
	})
	if err != nil {
		return repository.Snapshot{}, err
	}
	return result, nil
}

func (s *Service) SaveSets(ctx context.Context, db *sql.DB, token string, sessionID, expectedVersion uint64, batch domain.Batch) (uint64, bool, error) {
	var version uint64
	var changed bool
	err := s.inTransaction(ctx, db, token, func(tx *sql.Tx, subjectID uint64) error {
		var err error
		version, changed, err = s.records.ApplyBatch(ctx, tx, subjectID, sessionID, expectedVersion, batch)
		return err
	})
	if err != nil {
		return 0, false, err
	}
	return version, changed, nil
}
