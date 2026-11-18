package repository

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"time"

	"nerae/api/internal/dbtx"
	"nerae/api/internal/modules/workout/domain"
)

// RecordingV2Store is a persistence primitive, not an authentication or HTTP
// service. The caller must authorize and commit/roll back the same transaction.
type RecordingV2Store struct {
	protector *recordingV2Protector
	personal  *PersonalExerciseStore
	now       func() time.Time
}
type RecordingV2Snapshot struct {
	ID, Version                   uint64
	Date, TimeZone, ClientEventID string
	CreatedAt, UpdatedAt          time.Time
	RecordedAt                    *time.Time
	Recording                     domain.RecordingV2
}

func (RecordingV2Snapshot) String() string               { return "RecordingV2Snapshot{redacted}" }
func (RecordingV2Snapshot) GoString() string             { return "RecordingV2Snapshot{redacted}" }
func (RecordingV2Snapshot) MarshalJSON() ([]byte, error) { return nil, ErrUnavailable }
func (RecordingV2Snapshot) LogValue() slog.Value {
	return slog.StringValue("RecordingV2Snapshot{redacted}")
}

const (
	recordingV2FindEventSQL = `SELECT id, version, creation_request_hash, record_format FROM workout_session WHERE user_id = ? AND client_event_id = ? FOR UPDATE`
	recordingV2CreateSQL    = `INSERT INTO workout_session
        (user_id, source, date, time_zone, status, version, client_event_id, creation_request_hash, created_at, updated_at, record_format)
        VALUES (?, 'free', ?, ?, 'in_progress', 1, ?, ?, ?, ?, 2)`
	recordingV2LockSessionSQL = `SELECT source, status, version, record_format, CAST(date AS CHAR), time_zone, client_event_id,
        DATE_FORMAT(created_at, '%Y-%m-%d %H:%i:%s.%f'), DATE_FORMAT(updated_at, '%Y-%m-%d %H:%i:%s.%f'), DATE_FORMAT(recorded_at, '%Y-%m-%d %H:%i:%s.%f')
        FROM workout_session WHERE id = ? AND user_id = ? FOR UPDATE`
	recordingV2ReadSnapshotSQL   = `SELECT version, key_id, snapshot_enc FROM workout_recording_v2 WHERE session_id = ? FOR UPDATE`
	recordingV2InsertSnapshotSQL = `INSERT INTO workout_recording_v2 (session_id, version, key_id, snapshot_enc) VALUES (?, ?, ?, ?)`
	recordingV2UpdateSnapshotSQL = `UPDATE workout_recording_v2 SET version = ?, key_id = ?, snapshot_enc = ? WHERE session_id = ? AND version = ?`
	recordingV2UpdateSessionSQL  = `UPDATE workout_session SET recorded_at = CASE WHEN ? = 'in_progress' THEN NULL WHEN status <> 'completed' THEN ? ELSE recorded_at END,
        status = ?, date = ?, updated_at = ?, version = ? WHERE id = ? AND user_id = ? AND version = ? AND record_format = 2`
)

// RecordingV2Statements returns the complete, fixed SQL set for a future
// idempotency policy. It does not grant access or expose these methods via HTTP.
func RecordingV2Statements() (reads, writes []string) {
	return []string{freeLockSubjectSQL, recordingV2FindEventSQL, recordingV2LockSessionSQL, workoutLockKeyEpochSQL, recordingV2ReadSnapshotSQL},
		[]string{recordingV2CreateSQL, recordingV2InsertSnapshotSQL, recordingV2UpdateSnapshotSQL, recordingV2UpdateSessionSQL}
}
func NewRecordingV2Store(root []byte, keyID string) (*RecordingV2Store, error) {
	protector, err := newRecordingV2Protector(root, keyID)
	if err != nil {
		return nil, ErrUnavailable
	}
	personal, err := NewPersonalExerciseStore(root, keyID)
	if err != nil {
		return nil, ErrUnavailable
	}
	return &RecordingV2Store{protector: protector, personal: personal, now: time.Now}, nil
}
func (s *RecordingV2Store) ready(ctx context.Context, tx dbtx.ReadTransaction, subject, session uint64) bool {
	return s != nil && s.protector != nil && s.now != nil && ctx != nil && tx != nil && subject != 0 && session != 0
}
func lockV2Subject(ctx context.Context, tx dbtx.ReadTransaction, subject uint64) error {
	var status string
	if err := tx.QueryRowContext(ctx, freeLockSubjectSQL, subject).Scan(&status); err != nil || status != "active" {
		return ErrUnavailable
	}
	return nil
}
func (s *RecordingV2Store) checkEpoch(ctx context.Context, tx dbtx.ReadTransaction) error {
	var id, tag []byte
	if err := tx.QueryRowContext(ctx, workoutLockKeyEpochSQL).Scan(&id, &tag); err != nil || string(id) != s.protector.keyID || !hmac.Equal(tag, s.protector.epochTag[:]) {
		return ErrUnavailable
	}
	return nil
}
func v2Timestamp(now time.Time) (string, error) {
	if now.IsZero() {
		return "", ErrUnavailable
	}
	return now.UTC().Truncate(time.Microsecond).Format("2006-01-02 15:04:05.000000"), nil
}

// CreateFreeV2Tx atomically creates a v2 session and its empty encrypted draft.
// A caller retry using the same creation key gets the existing owned session.
func (s *RecordingV2Store) CreateFreeV2Tx(ctx context.Context, tx dbtx.Transaction, subject uint64, input FreeSessionInput, mode domain.RecordingModeV2) (SessionRef, error) {
	if s == nil || s.protector == nil || s.now == nil || ctx == nil || tx == nil || subject == 0 || (mode != domain.RecordingAfterWorkoutV2 && mode != domain.RecordingPerSetV2) {
		return SessionRef{}, ErrInvalid
	}
	now := s.now()
	zone, err := validateFreeInput(input, now)
	if err != nil {
		return SessionRef{}, ErrInvalid
	}
	stamp, err := v2Timestamp(now)
	if err != nil {
		return SessionRef{}, err
	}
	request, err := json.Marshal(struct {
		Version                int `json:"version"`
		Source, Date, TimeZone string
		Mode                   domain.RecordingModeV2 `json:"mode"`
	}{2, "free", input.Date, zone, mode})
	if err != nil {
		return SessionRef{}, ErrUnavailable
	}
	digest := sha256.Sum256(request)
	if err := lockV2Subject(ctx, tx, subject); err != nil {
		return SessionRef{}, err
	}
	var ref SessionRef
	var existingHash []byte
	var format uint8
	err = tx.QueryRowContext(ctx, recordingV2FindEventSQL, subject, []byte(input.ClientEventID)).Scan(&ref.ID, &ref.Version, &existingHash, &format)
	if err == nil {
		if format != 2 || !hmac.Equal(existingHash, digest[:]) {
			return SessionRef{}, ErrConflict
		}
		snapshot, err := s.readLocked(ctx, tx, subject, ref.ID)
		if err != nil || snapshot.Version != ref.Version {
			return SessionRef{}, ErrUnavailable
		}
		return ref, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return SessionRef{}, ErrUnavailable
	}
	if err := s.checkEpoch(ctx, tx); err != nil {
		return SessionRef{}, err
	}
	result, err := tx.ExecContext(ctx, recordingV2CreateSQL, subject, input.Date, zone, []byte(input.ClientEventID), digest[:], stamp, stamp)
	if err != nil {
		return SessionRef{}, ErrUnavailable
	}
	id, err := result.LastInsertId()
	if err != nil || id <= 0 {
		return SessionRef{}, ErrUnavailable
	}
	ref = SessionRef{ID: uint64(id), Version: 1}
	initial := domain.RecordingV2{Status: domain.InProgress, Mode: mode, Entries: map[string]domain.Exercise{}, Order: []string{}, Sets: map[string]domain.SetV2{}, Removed: map[string]domain.RemovedSetV2{}, RemovedEntries: map[string]int{}}
	plain, err := encodeRecordingV2(initial)
	if err != nil {
		return SessionRef{}, ErrUnavailable
	}
	sealed, err := s.protector.seal(subject, ref.ID, ref.Version, plain)
	clear(plain)
	if err != nil {
		return SessionRef{}, ErrUnavailable
	}
	if _, err := tx.ExecContext(ctx, recordingV2InsertSnapshotSQL, ref.ID, ref.Version, []byte(s.protector.keyID), sealed); err != nil {
		return SessionRef{}, ErrUnavailable
	}
	return ref, nil
}

// readLocked still acquires the session row. Its caller must lock the owner
// first, matching every other workout transaction's lock order.
func (s *RecordingV2Store) readLocked(ctx context.Context, tx dbtx.ReadTransaction, subject, sessionID uint64) (RecordingV2Snapshot, error) {
	var source, status, date, zone, createdRaw, updatedRaw string
	var eventID []byte
	var recordedRaw sql.NullString
	var version uint64
	var format uint8
	if err := tx.QueryRowContext(ctx, recordingV2LockSessionSQL, sessionID, subject).Scan(&source, &status, &version, &format, &date, &zone, &eventID, &createdRaw, &updatedRaw, &recordedRaw); err != nil || source != "free" || format != 2 || version == 0 {
		return RecordingV2Snapshot{}, ErrUnavailable
	}
	if _, err := validateFreeInput(FreeSessionInput{Date: date, TimeZone: &zone, ClientEventID: string(eventID)}, s.now()); err != nil {
		return RecordingV2Snapshot{}, ErrUnavailable
	}
	const layout = "2006-01-02 15:04:05.000000"
	created, err := time.Parse(layout, createdRaw)
	if err != nil {
		return RecordingV2Snapshot{}, ErrUnavailable
	}
	updated, err := time.Parse(layout, updatedRaw)
	if err != nil || updated.Before(created) {
		return RecordingV2Snapshot{}, ErrUnavailable
	}
	var recorded *time.Time
	if recordedRaw.Valid {
		parsed, err := time.Parse(layout, recordedRaw.String)
		if err != nil {
			return RecordingV2Snapshot{}, ErrUnavailable
		}
		recorded = &parsed
	}
	if status == string(domain.Completed) && recorded == nil || status == string(domain.InProgress) && recorded != nil {
		return RecordingV2Snapshot{}, ErrUnavailable
	}
	if err := s.checkEpoch(ctx, tx); err != nil {
		return RecordingV2Snapshot{}, err
	}
	var storedVersion uint64
	var keyID, sealed []byte
	if err := tx.QueryRowContext(ctx, recordingV2ReadSnapshotSQL, sessionID).Scan(&storedVersion, &keyID, &sealed); err != nil || storedVersion != version || string(keyID) != s.protector.keyID {
		return RecordingV2Snapshot{}, ErrUnavailable
	}
	plain, err := s.protector.open(subject, sessionID, version, sealed)
	if err != nil {
		return RecordingV2Snapshot{}, ErrUnavailable
	}
	recording, err := decodeRecordingV2(plain)
	clear(plain)
	if err != nil || string(recording.Status) != status {
		return RecordingV2Snapshot{}, ErrUnavailable
	}
	return RecordingV2Snapshot{ID: sessionID, Version: version, Date: date, TimeZone: zone, ClientEventID: string(eventID), CreatedAt: created, UpdatedAt: updated, RecordedAt: recorded, Recording: recording}, nil
}
func (s *RecordingV2Store) ReadFreeV2Tx(ctx context.Context, tx dbtx.ReadTransaction, subject, sessionID uint64) (RecordingV2Snapshot, error) {
	if !s.ready(ctx, tx, subject, sessionID) {
		return RecordingV2Snapshot{}, ErrInvalid
	}
	if err := lockV2Subject(ctx, tx, subject); err != nil {
		return RecordingV2Snapshot{}, err
	}
	return s.readLocked(ctx, tx, subject, sessionID)
}
func nextRecordingV2Version(version uint64) (uint64, error) {
	if version == 0 || version >= 9007199254740991 {
		return 0, ErrUnavailable
	}
	return version + 1, nil
}

func (s *RecordingV2Store) storeNext(ctx context.Context, tx dbtx.Transaction, subject, sessionID, version uint64, value domain.RecordingV2, date string) (uint64, error) {
	newVersion, err := nextRecordingV2Version(version)
	if err != nil {
		return 0, err
	}
	stamp, err := v2Timestamp(s.now())
	if err != nil {
		return 0, err
	}
	plain, err := encodeRecordingV2(value)
	if err != nil {
		return 0, ErrInvalid
	}
	sealed, err := s.protector.seal(subject, sessionID, newVersion, plain)
	clear(plain)
	if err != nil {
		return 0, ErrUnavailable
	}
	result, err := tx.ExecContext(ctx, recordingV2UpdateSnapshotSQL, newVersion, []byte(s.protector.keyID), sealed, sessionID, version)
	if err != nil {
		return 0, ErrUnavailable
	}
	affected, err := result.RowsAffected()
	if err != nil || affected != 1 {
		return 0, ErrUnavailable
	}
	result, err = tx.ExecContext(ctx, recordingV2UpdateSessionSQL, string(value.Status), stamp, string(value.Status), date, stamp, newVersion, sessionID, subject, version)
	if err != nil {
		return 0, ErrUnavailable
	}
	affected, err = result.RowsAffected()
	if err != nil || affected != 1 {
		return 0, ErrUnavailable
	}
	return newVersion, nil
}
func (s *RecordingV2Store) ApplyBatchV2Tx(ctx context.Context, tx dbtx.Transaction, subject, sessionID, expected uint64, batch domain.RecordingBatchV2) (uint64, bool, error) {
	if !s.ready(ctx, tx, subject, sessionID) || expected == 0 || len(batch.AddEntries) > 2000 {
		return 0, false, ErrInvalid
	}
	if err := lockV2Subject(ctx, tx, subject); err != nil {
		return 0, false, err
	}
	current, err := s.readLocked(ctx, tx, subject, sessionID)
	if err != nil {
		return 0, false, err
	}
	if current.Version != expected {
		return 0, false, ErrVersionConflict
	}
	// Identity and historical name are verified under this same owner-locked
	// transaction. Domain validation cannot authorize another user's action.
	type personalKey struct {
		id      string
		version uint64
	}
	resolved := make(map[personalKey]domain.PersonalExercise)
	for _, entry := range batch.AddEntries {
		if entry.Exercise.Source != domain.Personal {
			continue
		}
		if s.personal == nil {
			return 0, false, ErrUnavailable
		}
		key := personalKey{id: entry.Exercise.ID, version: entry.Exercise.Version}
		owned, found := resolved[key]
		if !found {
			var err error
			owned, err = s.personal.ReadVersionTx(ctx, tx, subject, key.id, key.version)
			if errors.Is(err, ErrPersonalExerciseNotFound) || errors.Is(err, ErrInvalid) {
				return 0, false, ErrInvalid
			}
			if err != nil {
				return 0, false, err
			}
			resolved[key] = owned
		}
		if owned.Name != entry.Exercise.Name {
			return 0, false, ErrInvalid
		}
	}
	next, changed, err := domain.ApplyRecordingBatchV2(current.Recording, batch)
	if errors.Is(err, domain.ErrConflict) {
		return 0, false, ErrSetConflict
	}
	if err != nil {
		return 0, false, ErrInvalid
	}
	date := current.Date
	if batch.Date != nil {
		if _, err := validateFreeInput(FreeSessionInput{Date: *batch.Date, TimeZone: &current.TimeZone, ClientEventID: current.ClientEventID}, s.now()); err != nil {
			return 0, false, ErrInvalid
		}
		date = *batch.Date
	}
	if !changed && date == current.Date {
		return current.Version, false, nil
	}
	version, err := s.storeNext(ctx, tx, subject, sessionID, current.Version, next, date)
	return version, err == nil, err
}
func (s *RecordingV2Store) CompleteV2Tx(ctx context.Context, tx dbtx.Transaction, subject, sessionID, expected uint64) (uint64, error) {
	if !s.ready(ctx, tx, subject, sessionID) || expected == 0 {
		return 0, ErrInvalid
	}
	if err := lockV2Subject(ctx, tx, subject); err != nil {
		return 0, err
	}
	current, err := s.readLocked(ctx, tx, subject, sessionID)
	if err != nil {
		return 0, err
	}
	if current.Version != expected {
		return 0, ErrVersionConflict
	}
	next, err := domain.CompleteRecordingV2(current.Recording)
	if errors.Is(err, domain.ErrConflict) {
		return 0, ErrSetConflict
	}
	if err != nil {
		return 0, ErrInvalid
	}
	if current.Recording.Status == domain.Completed {
		return current.Version, nil
	}
	return s.storeNext(ctx, tx, subject, sessionID, current.Version, next, current.Date)
}
