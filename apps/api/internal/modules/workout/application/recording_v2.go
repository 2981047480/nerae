package application

import (
	"context"
	"database/sql"
	"errors"

	"nerae/api/internal/dbtx"
	"nerae/api/internal/modules/auth/session"
	"nerae/api/internal/modules/workout/repository"
)

// RecordingV2Service checks live sessions and ownership inside one transaction.
// It does not authorize coaches to view in-progress or completed snapshots.
type RecordingV2Service struct {
	sessions *session.Store
	records  *repository.RecordingV2Store
	list     *repository.SessionStore
}

func NewRecordingV2Service(sessions *session.Store, records *repository.RecordingV2Store) (*RecordingV2Service, error) {
	if sessions == nil || records == nil {
		return nil, ErrUnavailable
	}
	return &RecordingV2Service{sessions: sessions, records: records, list: repository.NewSessionStore()}, nil
}
func (s *RecordingV2Service) inTransaction(ctx context.Context, db *sql.DB, token string, work func(*sql.Tx, uint64) error) error {
	if s == nil || s.sessions == nil || s.records == nil || s.list == nil || ctx == nil || db == nil || work == nil {
		return ErrUnavailable
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return ErrUnavailable
	}
	defer tx.Rollback()
	principal, err := s.sessions.Authenticate(ctx, tx, token)
	if errors.Is(err, session.ErrInvalidSession) {
		return ErrAuthentication
	}
	if err != nil || principal == nil {
		return ErrUnavailable
	}
	before, err := principal.ForTransaction(ctx, tx)
	if err != nil {
		return ErrUnavailable
	}
	if !before.AgeVerified {
		return ErrAgeRequired
	}
	if err := work(tx, before.SubjectID); err != nil {
		return err
	}
	after, err := principal.ForTransaction(ctx, tx)
	if err != nil || after.SubjectID != before.SubjectID || after.SessionID != before.SessionID {
		return ErrUnavailable
	}
	if !after.AgeVerified {
		return ErrAgeRequired
	}
	if tx.Commit() != nil {
		return ErrUnavailable
	}
	return nil
}
func (s *RecordingV2Service) ReadOwn(ctx context.Context, db *sql.DB, token string, id uint64) (repository.RecordingV2Snapshot, error) {
	if id == 0 {
		return repository.RecordingV2Snapshot{}, repository.ErrInvalid
	}
	var snapshot repository.RecordingV2Snapshot
	err := s.inTransaction(ctx, db, token, func(tx *sql.Tx, subject uint64) error {
		var found uint64
		err := tx.QueryRowContext(ctx, `SELECT id FROM workout_session WHERE id = ? AND user_id = ? AND source = 'free' AND record_format = 2 FOR UPDATE`, id, subject).Scan(&found)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrWorkoutNotFound
		}
		if err != nil || found != id {
			return ErrUnavailable
		}
		snapshot, err = s.records.ReadFreeV2Tx(ctx, dbtx.Wrap(tx), subject, id)
		return err
	})
	if err != nil {
		return repository.RecordingV2Snapshot{}, err
	}
	return snapshot, nil
}
func (s *RecordingV2Service) ListOwn(ctx context.Context, db *sql.DB, token string, filter repository.ListFilter) (repository.RecordingV2ListPage, error) {
	var page repository.RecordingV2ListPage
	err := s.inTransaction(ctx, db, token, func(tx *sql.Tx, subject uint64) error {
		var err error
		page, err = s.list.ListRecordingFormats(ctx, tx, subject, filter)
		return err
	})
	if err != nil {
		return repository.RecordingV2ListPage{}, err
	}
	return page, nil
}
