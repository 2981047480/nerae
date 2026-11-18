package repository

import (
	"context"
	"crypto/hmac"
	"database/sql"
	"errors"
	"log/slog"
	"math"
	"reflect"
	"time"

	"nerae/api/internal/dbtx"
	"nerae/api/internal/modules/workout/domain"
)

// OrderedSnapshot contains protected exercise information. It is an internal
// transaction result; an authorized presentation layer must project it.
type OrderedSnapshot struct {
	Snapshot
	ExerciseEntryKeys []string
}

func (OrderedSnapshot) String() string               { return "OrderedWorkoutSnapshot{redacted}" }
func (OrderedSnapshot) GoString() string             { return "OrderedWorkoutSnapshot{redacted}" }
func (OrderedSnapshot) MarshalJSON() ([]byte, error) { return nil, ErrUnavailable }
func (OrderedSnapshot) LogValue() slog.Value {
	return slog.StringValue("OrderedWorkoutSnapshot{redacted}")
}

const (
	orderLockSubjectSQL           = `SELECT status FROM user WHERE id = ? FOR UPDATE`
	orderLockSessionSQL           = `SELECT source, status, version, recorded_at IS NOT NULL FROM workout_session WHERE id = ? AND user_id = ? AND record_format = 1 FOR UPDATE`
	orderLockKeyEpochSQL          = `SELECT key_id, key_tag FROM workout_crypto_key_epoch WHERE slot = 1 FOR SHARE`
	workoutReadExerciseOrderSQL   = `SELECT exercise_entry_key, position FROM workout_exercise_order WHERE session_id = ? ORDER BY position`
	workoutDeleteExerciseOrderSQL = `DELETE FROM workout_exercise_order WHERE session_id = ?`
	workoutInsertExerciseOrderSQL = `INSERT INTO workout_exercise_order (session_id, exercise_entry_key, position) VALUES (?, ?, ?)`
	workoutUpdateOrderSessionSQL  = `UPDATE workout_session SET version = version + 1, updated_at = ? WHERE id = ? AND user_id = ? AND record_format = 1`
)

// OrderStatements returns copies of SQL issued directly by this file. The
// lists exclude SQL delegated to ReadFreeTx and record helpers; callers must
// also merge FreeDetailStatements and RecordStatements for full coverage.
func OrderStatements() (reads, writes []string) {
	return []string{orderLockSubjectSQL, orderLockSessionSQL, orderLockKeyEpochSQL, workoutReadExerciseOrderSQL},
		[]string{workoutDeleteExerciseOrderSQL, workoutInsertExerciseOrderSQL, workoutUpdateOrderSessionSQL}
}

// ReadOrderedFree checks ownership and returns only a self-consistent ordered
// snapshot while holding the session lock. The caller must enforce identity,
// age and resource authorization in this same transaction.
func (s *RecordStore) ReadOrderedFree(ctx context.Context, tx *sql.Tx, subjectID, sessionID uint64) (OrderedSnapshot, error) {
	return s.ReadOrderedFreeTx(ctx, dbtx.Wrap(tx), subjectID, sessionID)
}

// ReadOrderedFreeTx reads an ordered snapshot inside the caller-owned transaction.
// It performs no identity, age, or resource authorization and never commits.
func (s *RecordStore) ReadOrderedFreeTx(ctx context.Context, tx dbtx.ReadTransaction, subjectID, sessionID uint64) (OrderedSnapshot, error) {
	if s == nil || s.protector == nil || s.now == nil || ctx == nil || tx == nil || subjectID == 0 || sessionID == 0 {
		return OrderedSnapshot{}, ErrInvalid
	}
	snapshot, err := s.ReadFreeTx(ctx, tx, subjectID, sessionID)
	if err != nil {
		return OrderedSnapshot{}, err
	}
	order, err := readExerciseOrderTx(ctx, tx, sessionID)
	if err != nil {
		return OrderedSnapshot{}, ErrUnavailable
	}
	current, err := s.loadTx(ctx, tx, subjectID, sessionID, snapshot.Status)
	if err != nil {
		return OrderedSnapshot{}, ErrUnavailable
	}
	if _, _, err := domain.ApplyOrderedBatch(current, order, domain.Batch{}, order); err != nil {
		return OrderedSnapshot{}, ErrUnavailable
	}
	return OrderedSnapshot{Snapshot: snapshot, ExerciseEntryKeys: order}, nil
}

// ApplyOrderedBatch locks the subject and session, verifies the version, and
// commits group changes and complete entry order together in the caller's tx.
// The caller MUST roll back on any error. This is not an HTTP idempotency,
// authorization, age-verification, or migration/recovery boundary.
// The group-only helper is private so application callers cannot bypass order.
func (s *RecordStore) ApplyOrderedBatch(ctx context.Context, tx *sql.Tx, subjectID, sessionID, expectedVersion uint64, batch domain.Batch, targetOrder []string) (uint64, bool, error) {
	return s.ApplyOrderedBatchTx(ctx, dbtx.Wrap(tx), subjectID, sessionID, expectedVersion, batch, targetOrder)
}

// ApplyOrderedBatchTx uses the supplied capability and leaves transaction
// lifecycle and authorization to the caller.
func (s *RecordStore) ApplyOrderedBatchTx(ctx context.Context, tx dbtx.Transaction, subjectID, sessionID, expectedVersion uint64, batch domain.Batch, targetOrder []string) (uint64, bool, error) {
	if s == nil || s.protector == nil || s.now == nil || ctx == nil || tx == nil || subjectID == 0 || sessionID == 0 || expectedVersion == 0 || targetOrder == nil {
		return 0, false, ErrInvalid
	}
	var userStatus string
	if err := tx.QueryRowContext(ctx, orderLockSubjectSQL, subjectID).Scan(&userStatus); err != nil || userStatus != "active" {
		return 0, false, ErrUnavailable
	}
	var version uint64
	var source, status string
	var recorded bool
	if err := tx.QueryRowContext(ctx, orderLockSessionSQL, sessionID, subjectID).
		Scan(&source, &status, &version, &recorded); err != nil || source != "free" || version == 0 || (status == string(domain.Completed) && !recorded) {
		return 0, false, ErrUnavailable
	}
	if version != expectedVersion {
		return 0, false, ErrVersionConflict
	}
	var epochID, epochTag []byte
	if err := tx.QueryRowContext(ctx, orderLockKeyEpochSQL).Scan(&epochID, &epochTag); err != nil ||
		string(epochID) != s.protector.keyID || !hmac.Equal(epochTag, s.protector.tag[:]) {
		return 0, false, ErrUnavailable
	}
	current, err := s.loadTx(ctx, tx, subjectID, sessionID, domain.Status(status))
	if err != nil {
		return 0, false, ErrUnavailable
	}
	oldOrder, err := readExerciseOrderTx(ctx, tx, sessionID)
	if err != nil {
		return 0, false, ErrUnavailable
	}
	_, projectedOrder, err := domain.ApplyOrderedBatch(current, oldOrder, batch, targetOrder)
	switch {
	case errors.Is(err, domain.ErrInvalidOrderedSnapshot):
		return 0, false, ErrUnavailable
	case errors.Is(err, domain.ErrConflict):
		return 0, false, ErrSetConflict
	case err != nil:
		return 0, false, ErrInvalid
	}
	orderChanged := !reflect.DeepEqual(oldOrder, projectedOrder)
	resultVersion, groupsChanged := version, false
	if len(batch.Sets) != 0 || len(batch.DeleteKeys) != 0 || len(batch.RestoreKeys) != 0 {
		resultVersion, groupsChanged, err = s.applyBatchWithoutOrderTx(ctx, tx, subjectID, sessionID, expectedVersion, batch)
		if err != nil {
			return 0, false, err
		}
	}
	if !orderChanged {
		return resultVersion, groupsChanged, nil
	}
	if !groupsChanged && version == math.MaxUint64 {
		return 0, false, ErrUnavailable
	}
	if _, err := tx.ExecContext(ctx, workoutDeleteExerciseOrderSQL, sessionID); err != nil {
		return 0, false, ErrUnavailable
	}
	for i, entryKey := range projectedOrder {
		if _, err := tx.ExecContext(ctx, workoutInsertExerciseOrderSQL, sessionID, []byte(entryKey), i+1); err != nil {
			return 0, false, ErrUnavailable
		}
	}
	if !groupsChanged {
		now := s.now()
		if now.IsZero() {
			return 0, false, ErrUnavailable
		}
		timestamp := now.UTC().Truncate(time.Microsecond).Format("2006-01-02 15:04:05.000000")
		if _, err := tx.ExecContext(ctx, workoutUpdateOrderSessionSQL, timestamp, sessionID, subjectID); err != nil {
			return 0, false, ErrUnavailable
		}
		resultVersion++
	}
	return resultVersion, true, nil
}

func readExerciseOrder(ctx context.Context, tx *sql.Tx, sessionID uint64) ([]string, error) {
	return readExerciseOrderTx(ctx, dbtx.Wrap(tx), sessionID)
}

func readExerciseOrderTx(ctx context.Context, tx dbtx.ReadTransaction, sessionID uint64) ([]string, error) {
	if ctx == nil || tx == nil || sessionID == 0 {
		return nil, ErrInvalid
	}
	rows, err := tx.QueryContext(ctx, workoutReadExerciseOrderSQL, sessionID)
	if err != nil {
		return nil, ErrUnavailable
	}
	defer rows.Close()
	order := make([]string, 0)
	for rows.Next() {
		var entry []byte
		var position uint64
		if rows.Scan(&entry, &position) != nil || position != uint64(len(order)+1) || position > 2000 {
			return nil, ErrUnavailable
		}
		order = append(order, string(entry))
	}
	if rows.Err() != nil {
		return nil, ErrUnavailable
	}
	return order, nil
}
