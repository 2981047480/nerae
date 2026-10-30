package repository

import (
	"context"
	"crypto/hmac"
	"database/sql"
	"errors"
	"math"
	"math/big"
	"reflect"
	"strings"
	"time"
	"unicode/utf8"

	"nerae/api/internal/dbtx"
	"nerae/api/internal/modules/workout/domain"
)

var (
	ErrVersionConflict   = errors.New("workout version conflict")
	ErrSetConflict       = errors.New("workout set conflict")
	ErrUnsupportedSource = errors.New("exercise source unavailable")
)

type RecordStore struct {
	protector *ExerciseNameProtector
	now       func() time.Time
}

const (
	workoutLockSubjectSQL    = `SELECT status FROM user WHERE id = ? FOR UPDATE`
	workoutLockSessionSQL    = `SELECT source, status, version FROM workout_session WHERE id = ? AND user_id = ? FOR UPDATE`
	workoutLockKeyEpochSQL   = `SELECT key_id, key_tag FROM workout_crypto_key_epoch WHERE slot = 1 FOR SHARE`
	workoutInsertExerciseSQL = `INSERT INTO workout_exercise
            (session_id, exercise_entry_key, source, name_snapshot_enc, key_id, created_at, updated_at)
            VALUES (?, ?, 'custom', ?, ?, ?, ?)`
	workoutInsertSetKeySQL = `INSERT INTO set_record_key
                (session_id, record_key, exercise_entry_key, state, created_at, deleted_at) VALUES (?, ?, ?, ?, ?, ?)`
	workoutUpdateSetKeySQL = `UPDATE set_record_key SET state = ?, deleted_at = ? WHERE session_id = ? AND record_key = ?`
	workoutDeleteSetSQL    = `DELETE FROM set_record WHERE session_id = ? AND record_key = ?`
	workoutInsertSetSQL    = `INSERT INTO set_record
                (session_id, record_key, set_index, kind, weight_kg, reps, seconds, rpe, created_at, updated_at)
                VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	workoutUpdateSetSQL = `UPDATE set_record SET set_index = ?, kind = ?, weight_kg = ?, reps = ?, seconds = ?, rpe = ?, updated_at = ?
                WHERE session_id = ? AND record_key = ?`
	workoutUpdateSessionSQL = `UPDATE workout_session SET status = ?, version = version + 1, updated_at = ? WHERE id = ? AND user_id = ?`
	workoutLoadExercisesSQL = `SELECT exercise_entry_key, source, name_snapshot_enc, key_id FROM workout_exercise WHERE session_id = ?`
	workoutLoadSetKeysSQL   = `SELECT record_key, exercise_entry_key, state FROM set_record_key WHERE session_id = ?`
	workoutLoadSetsSQL      = `SELECT record_key, set_index, kind, weight_kg, reps, seconds, rpe FROM set_record WHERE session_id = ?`
)

var workoutRecordReadSQL = [...]string{
	workoutLockSubjectSQL,
	workoutLockSessionSQL,
	workoutLockKeyEpochSQL,
	workoutLoadExercisesSQL,
	workoutLoadSetKeysSQL,
	workoutLoadSetsSQL,
}

var workoutRecordWriteSQL = [...]string{
	workoutInsertExerciseSQL,
	workoutInsertSetKeySQL,
	workoutUpdateSetKeySQL,
	workoutDeleteSetSQL,
	workoutInsertSetSQL,
	workoutUpdateSetSQL,
	workoutUpdateSessionSQL,
}

// RecordStatements returns copies of the fixed read and write SQL used by this repository.
func RecordStatements() (reads, writes []string) {
	return append([]string(nil), workoutRecordReadSQL[:]...), append([]string(nil), workoutRecordWriteSQL[:]...)
}

func NewRecordStore(protector *ExerciseNameProtector) (*RecordStore, error) {
	if protector == nil || protector.aead == nil {
		return nil, ErrUnavailable
	}
	return &RecordStore{protector: protector, now: time.Now}, nil
}

// applyBatchWithoutOrder is used only inside the ordered transaction, not as
// an application entry point. It performs persistence, not authorization. The caller
// must derive subjectID from a real authenticated session, enforce age and
// resource access, and roll back on any error. No HTTP idempotency or catalog
// authorization is implemented here. All transactions lock user before workout.
func (s *RecordStore) applyBatchWithoutOrder(ctx context.Context, tx *sql.Tx, subjectID, sessionID, expectedVersion uint64, batch domain.Batch) (uint64, bool, error) {
	return s.applyBatchWithoutOrderTx(ctx, dbtx.Wrap(tx), subjectID, sessionID, expectedVersion, batch)
}

// applyBatchWithoutOrderTx performs the same fixed, parameterized mutations through a
// transaction capability. It does not authorize the caller or own transaction lifecycle.
func (s *RecordStore) applyBatchWithoutOrderTx(ctx context.Context, tx dbtx.Transaction, subjectID, sessionID, expectedVersion uint64, batch domain.Batch) (uint64, bool, error) {
	if s == nil || s.protector == nil || s.now == nil || ctx == nil || tx == nil || subjectID == 0 || sessionID == 0 || expectedVersion == 0 {
		return 0, false, ErrInvalid
	}
	if len(batch.Sets) > domain.MaxBatchItems || len(batch.DeleteKeys) > domain.MaxBatchItems ||
		len(batch.Sets)+len(batch.DeleteKeys) > domain.MaxBatchItems || len(batch.RestoreKeys) > len(batch.Sets) ||
		(len(batch.Sets) == 0 && len(batch.DeleteKeys) == 0) {
		return 0, false, ErrInvalid
	}
	var userStatus string
	if err := tx.QueryRowContext(ctx, workoutLockSubjectSQL, subjectID).Scan(&userStatus); err != nil || userStatus != "active" {
		return 0, false, ErrUnavailable
	}
	var version uint64
	var source, status string
	if err := tx.QueryRowContext(ctx, workoutLockSessionSQL, sessionID, subjectID).
		Scan(&source, &status, &version); err != nil || source != "free" || version == 0 {
		return 0, false, ErrUnavailable
	}
	if version != expectedVersion {
		return 0, false, ErrVersionConflict
	}
	var epochID, epochTag []byte
	if err := tx.QueryRowContext(ctx, workoutLockKeyEpochSQL).Scan(&epochID, &epochTag); err != nil ||
		string(epochID) != s.protector.keyID || !hmac.Equal(epochTag, s.protector.tag[:]) {
		return 0, false, ErrUnavailable
	}
	current, err := s.loadTx(ctx, tx, subjectID, sessionID, domain.Status(status))
	if err != nil {
		return 0, false, err
	}
	// Validate before normalizing decimals; big.Rat alone accepts syntax that
	// the wire contract forbids, including fractions and exponent notation.
	clean := domain.Batch{Sets: append([]domain.Set(nil), batch.Sets...), DeleteKeys: batch.DeleteKeys, RestoreKeys: batch.RestoreKeys}
	for i := range clean.Sets {
		item := &clean.Sets[i]
		if item.Exercise.Source != domain.Custom {
			return 0, false, ErrUnsupportedSource
		}
		if !utf8.ValidString(item.Exercise.Name) || domain.ValidateSet(*item) != nil {
			return 0, false, ErrInvalid
		}
		item.WeightKg = canonicalDecimal(item.WeightKg, 2)
		item.RPE = canonicalDecimal(item.RPE, 1)
	}
	next, err := domain.ApplyBatch(current, clean)
	if errors.Is(err, domain.ErrConflict) {
		return 0, false, ErrSetConflict
	}
	if err != nil {
		return 0, false, ErrInvalid
	}
	if reflect.DeepEqual(current, next) {
		return version, false, nil
	}
	if version == math.MaxUint64 {
		return 0, false, ErrUnavailable
	}
	now := s.now()
	if now.IsZero() {
		return 0, false, ErrUnavailable
	}
	timestamp := now.UTC().Truncate(time.Microsecond).Format("2006-01-02 15:04:05.000000")
	for key, exercise := range next.Entries {
		if _, exists := current.Entries[key]; exists {
			continue
		}
		ciphertext, err := s.protector.seal(subjectID, sessionID, key, exercise.Name)
		if err != nil {
			return 0, false, ErrUnavailable
		}
		if _, err := tx.ExecContext(ctx, workoutInsertExerciseSQL, sessionID, []byte(key), ciphertext, s.protector.keyID, timestamp, timestamp); err != nil {
			return 0, false, ErrUnavailable
		}
	}
	for key, value := range next.Keys {
		old, exists := current.Keys[key]
		if exists && old == value {
			continue
		}
		state := "active"
		var deletedAt any
		if value.Deleted {
			state, deletedAt = "deleted", timestamp
		}
		if !exists {
			_, err = tx.ExecContext(ctx, workoutInsertSetKeySQL,
				sessionID, []byte(key), []byte(value.ExerciseEntryKey), state, timestamp, deletedAt)
		} else {
			_, err = tx.ExecContext(ctx, workoutUpdateSetKeySQL,
				state, deletedAt, sessionID, []byte(key))
		}
		if err != nil {
			return 0, false, ErrUnavailable
		}
	}
	for key := range current.Sets {
		if _, exists := next.Sets[key]; !exists {
			if _, err := tx.ExecContext(ctx, workoutDeleteSetSQL, sessionID, []byte(key)); err != nil {
				return 0, false, ErrUnavailable
			}
		}
	}
	for key, value := range next.Sets {
		old, exists := current.Sets[key]
		if exists && old == value {
			continue
		}
		if !exists {
			_, err = tx.ExecContext(ctx, workoutInsertSetSQL, sessionID, []byte(key), value.Index, value.Kind,
				nullDecimal(value.WeightKg), nullInt(value.Reps), nullInt(value.Seconds), nullDecimal(value.RPE), timestamp, timestamp)
		} else {
			_, err = tx.ExecContext(ctx, workoutUpdateSetSQL, value.Index, value.Kind, nullDecimal(value.WeightKg), nullInt(value.Reps),
				nullInt(value.Seconds), nullDecimal(value.RPE), timestamp, sessionID, []byte(key))
		}
		if err != nil {
			return 0, false, ErrUnavailable
		}
	}
	if _, err := tx.ExecContext(ctx, workoutUpdateSessionSQL, next.Status, timestamp, sessionID, subjectID); err != nil {
		return 0, false, ErrUnavailable
	}
	return version + 1, true, nil
}

func (s *RecordStore) load(ctx context.Context, tx *sql.Tx, subjectID, sessionID uint64, status domain.Status) (domain.Session, error) {
	return s.loadTx(ctx, dbtx.Wrap(tx), subjectID, sessionID, status)
}

func (s *RecordStore) loadTx(ctx context.Context, tx dbtx.ReadTransaction, subjectID, sessionID uint64, status domain.Status) (domain.Session, error) {
	cur := domain.Session{Status: status, Sets: map[string]domain.Set{}, Keys: map[string]domain.Key{}, Entries: map[string]domain.Exercise{}}
	if s == nil || s.protector == nil || ctx == nil || tx == nil || subjectID == 0 || sessionID == 0 {
		return cur, ErrInvalid
	}
	rows, err := tx.QueryContext(ctx, workoutLoadExercisesSQL, sessionID)
	if err != nil {
		return cur, ErrUnavailable
	}
	for rows.Next() {
		var key, source, ciphertext, keyID []byte
		if rows.Scan(&key, &source, &ciphertext, &keyID) != nil || string(source) != "custom" || string(keyID) != s.protector.keyID {
			_ = rows.Close()
			return cur, ErrUnavailable
		}
		name, err := s.protector.open(subjectID, sessionID, string(key), ciphertext)
		if err != nil || !utf8.ValidString(name) || strings.TrimSpace(name) == "" || utf8.RuneCountInString(name) > 60 {
			_ = rows.Close()
			return cur, ErrUnavailable
		}
		cur.Entries[string(key)] = domain.Exercise{Source: domain.Custom, Name: name}
	}
	if rows.Err() != nil || rows.Close() != nil {
		return cur, ErrUnavailable
	}
	rows, err = tx.QueryContext(ctx, workoutLoadSetKeysSQL, sessionID)
	if err != nil {
		return cur, ErrUnavailable
	}
	for rows.Next() {
		var key, entry, state []byte
		if rows.Scan(&key, &entry, &state) != nil || (string(state) != "active" && string(state) != "deleted") {
			_ = rows.Close()
			return cur, ErrUnavailable
		}
		if _, ok := cur.Entries[string(entry)]; !ok {
			_ = rows.Close()
			return cur, ErrUnavailable
		}
		cur.Keys[string(key)] = domain.Key{ExerciseEntryKey: string(entry), Deleted: string(state) == "deleted"}
	}
	if rows.Err() != nil || rows.Close() != nil {
		return cur, ErrUnavailable
	}
	rows, err = tx.QueryContext(ctx, workoutLoadSetsSQL, sessionID)
	if err != nil {
		return cur, ErrUnavailable
	}
	for rows.Next() {
		var key, kind string
		var index int
		var weight, rpe sql.NullString
		var reps, seconds sql.NullInt64
		if rows.Scan(&key, &index, &kind, &weight, &reps, &seconds, &rpe) != nil {
			_ = rows.Close()
			return cur, ErrUnavailable
		}
		registered, ok := cur.Keys[key]
		if !ok || registered.Deleted || !validStoredMeasurements(domain.Kind(kind), weight.Valid, reps.Valid, seconds.Valid) {
			_ = rows.Close()
			return cur, ErrUnavailable
		}
		item := domain.Set{RecordKey: key, ExerciseEntryKey: registered.ExerciseEntryKey, Exercise: cur.Entries[registered.ExerciseEntryKey],
			Index: index, Kind: domain.Kind(kind), WeightKg: weight.String, Reps: int(reps.Int64), Seconds: int(seconds.Int64), RPE: rpe.String}
		if domain.ValidateSet(item) != nil {
			_ = rows.Close()
			return cur, ErrUnavailable
		}
		cur.Sets[key] = item
	}
	if rows.Err() != nil || rows.Close() != nil {
		return cur, ErrUnavailable
	}
	for key, registered := range cur.Keys {
		_, exists := cur.Sets[key]
		if exists == registered.Deleted {
			return cur, ErrUnavailable
		}
	}
	return cur, nil
}

func validStoredMeasurements(kind domain.Kind, weight, reps, seconds bool) bool {
	switch kind {
	case domain.Weight, domain.PerSide:
		return weight && reps && !seconds
	case domain.Bodyweight, domain.BodyweightPerSide, domain.Breath:
		return !weight && reps && !seconds
	case domain.Duration, domain.DurationPerSide:
		return !weight && !reps && seconds
	default:
		return false
	}
}

func canonicalDecimal(value string, places int) string {
	if value == "" {
		return ""
	}
	rat, ok := new(big.Rat).SetString(value)
	if !ok {
		return value
	}
	return rat.FloatString(places)
}

func nullDecimal(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func nullInt(value int) any {
	if value == 0 {
		return nil
	}
	return value
}
