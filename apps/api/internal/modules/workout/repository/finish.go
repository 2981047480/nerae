package repository

import (
	"context"
	"database/sql"
	"math"
	"time"

	"nerae/api/internal/dbtx"
	"nerae/api/internal/modules/workout/domain"
)

const workoutCompleteSessionSQL = `UPDATE workout_session SET status = 'completed', recorded_at = ?, updated_at = ?, version = version + 1
        WHERE id = ? AND user_id = ?`

// CompletionStatements returns a fresh slice containing this file's fixed write SQL.
func CompletionStatements() []string { return []string{workoutCompleteSessionSQL} }

// CompleteFree records a save confirmation for a free workout containing at
// least one actual set. It does not set training start, end or duration. The
// caller must authorize the subject and commit or roll back this same tx.
// Date edits, optional finish fields, statistics and HTTP idempotency are not
// implemented here.
func (s *RecordStore) CompleteFree(ctx context.Context, tx *sql.Tx, subjectID, sessionID, expectedVersion uint64) (uint64, error) {
	return s.CompleteFreeTx(ctx, dbtx.Wrap(tx), subjectID, sessionID, expectedVersion)
}

// CompleteFreeTx confirms the record in the caller-owned transaction. It does
// not authorize, commit, or infer training start/end/duration.
func (s *RecordStore) CompleteFreeTx(ctx context.Context, tx dbtx.Transaction, subjectID, sessionID, expectedVersion uint64) (uint64, error) {
	if s == nil || s.now == nil || ctx == nil || tx == nil || subjectID == 0 || sessionID == 0 || expectedVersion == 0 {
		return 0, ErrInvalid
	}
	ordered, err := s.ReadOrderedFreeTx(ctx, tx, subjectID, sessionID)
	if err != nil {
		return 0, err
	}
	snapshot := ordered.Snapshot
	if snapshot.Version != expectedVersion {
		return 0, ErrVersionConflict
	}
	if _, err := domain.Complete(domain.Session{Status: snapshot.Status, Sets: snapshot.Sets}); err != nil {
		return 0, ErrSetConflict
	}
	if snapshot.Version == math.MaxUint64 {
		return 0, ErrUnavailable
	}
	now := s.now().UTC().Truncate(time.Microsecond)
	if now.IsZero() || now.Before(snapshot.CreatedAt) || now.Before(snapshot.UpdatedAt) {
		return 0, ErrUnavailable
	}
	timestamp := now.Format("2006-01-02 15:04:05.000000")
	if _, err := tx.ExecContext(ctx, workoutCompleteSessionSQL, timestamp, timestamp, sessionID, subjectID); err != nil {
		return 0, ErrUnavailable
	}
	return snapshot.Version + 1, nil
}
