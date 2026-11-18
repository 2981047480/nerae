package application

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"

	"nerae/api/internal/dbtx"
	"nerae/api/internal/modules/auth/session"
	"nerae/api/internal/modules/workout/domain"
	"nerae/api/internal/modules/workout/repository"
)

// PersonalExerciseService is an authenticated application boundary. Standard
// catalog collision resolution and an HTTP route are not provided here.
type PersonalExerciseService struct {
	sessions *session.Store
	store    *repository.PersonalExerciseStore
}

func (PersonalExerciseService) String() string               { return "PersonalExerciseService{redacted}" }
func (PersonalExerciseService) GoString() string             { return "PersonalExerciseService{redacted}" }
func (PersonalExerciseService) MarshalJSON() ([]byte, error) { return nil, ErrUnavailable }
func (PersonalExerciseService) LogValue() slog.Value {
	return slog.StringValue("PersonalExerciseService{redacted}")
}

func NewPersonalExerciseService(sessions *session.Store, store *repository.PersonalExerciseStore) (*PersonalExerciseService, error) {
	if sessions == nil || store == nil {
		return nil, ErrUnavailable
	}
	return &PersonalExerciseService{sessions: sessions, store: store}, nil
}

func (s *PersonalExerciseService) inTransaction(ctx context.Context, db *sql.DB, token string, work func(*sql.Tx, uint64) error) error {
	if s == nil || s.sessions == nil || s.store == nil || ctx == nil || db == nil || work == nil {
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

// PutOwn uses the stable caller-supplied ID for safe replay of one creation or
// one versioned update. The service never accepts a subject ID from the caller.
func (s *PersonalExerciseService) PutOwn(ctx context.Context, db *sql.DB, token string,
	value domain.PersonalExercise, expectedVersion uint64) (domain.PersonalExercise, bool, error) {
	var result domain.PersonalExercise
	var changed bool
	err := s.inTransaction(ctx, db, token, func(tx *sql.Tx, subject uint64) error {
		var err error
		result, changed, err = s.store.PutTx(ctx, dbtx.Wrap(tx), subject, value, expectedVersion)
		return err
	})
	if err != nil {
		return domain.PersonalExercise{}, false, err
	}
	return result, changed, nil
}

func (s *PersonalExerciseService) ReadOwn(ctx context.Context, db *sql.DB, token, id string) (domain.PersonalExercise, error) {
	var result domain.PersonalExercise
	err := s.inTransaction(ctx, db, token, func(tx *sql.Tx, subject uint64) error {
		var err error
		result, err = s.store.ReadTx(ctx, dbtx.Wrap(tx), subject, id)
		return err
	})
	if err != nil {
		return domain.PersonalExercise{}, err
	}
	return result, nil
}

// ReadOwnVersion returns only a historical snapshot owned by the authenticated subject.
func (s *PersonalExerciseService) ReadOwnVersion(ctx context.Context, db *sql.DB, token, id string, version uint64) (domain.PersonalExercise, error) {
	var result domain.PersonalExercise
	err := s.inTransaction(ctx, db, token, func(tx *sql.Tx, subject uint64) error {
		var err error
		result, err = s.store.ReadVersionTx(ctx, dbtx.Wrap(tx), subject, id, version)
		return err
	})
	if err != nil {
		return domain.PersonalExercise{}, err
	}
	return result, nil
}

func (s *PersonalExerciseService) ListOwn(ctx context.Context, db *sql.DB, token, query, cursor string, limit int) (repository.PersonalExercisePage, error) {
	var result repository.PersonalExercisePage
	err := s.inTransaction(ctx, db, token, func(tx *sql.Tx, subject uint64) error {
		var err error
		result, err = s.store.ListTx(ctx, dbtx.Wrap(tx), subject, query, cursor, limit)
		return err
	})
	if err != nil {
		return repository.PersonalExercisePage{}, err
	}
	return result, nil
}
