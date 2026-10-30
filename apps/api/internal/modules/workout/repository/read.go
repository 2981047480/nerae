package repository

import (
	"context"
	"crypto/hmac"
	"database/sql"
	"log/slog"
	"time"

	"nerae/api/internal/modules/workout/domain"
)

// Snapshot is an internal, sensitive result; presentation must be assembled
// only after authorization and must not serialize or log this value directly.
type Snapshot struct {
	ID            uint64
	Version       uint64
	ClientEventID string
	Date          string
	TimeZone      string
	Status        domain.Status
	CreatedAt     time.Time
	UpdatedAt     time.Time
	RecordedAt    *time.Time
	Sets          map[string]domain.Set
}

func (Snapshot) String() string               { return "WorkoutSnapshot{redacted}" }
func (Snapshot) GoString() string             { return "WorkoutSnapshot{redacted}" }
func (Snapshot) MarshalJSON() ([]byte, error) { return nil, ErrUnavailable }
func (Snapshot) LogValue() slog.Value         { return slog.StringValue("WorkoutSnapshot{redacted}") }

// ReadFree returns actual, non-deleted groups for one subject's free record.
// This is a persistence primitive, not authentication or resource permission.
// The caller must enforce both within the same live transaction.
func (s *RecordStore) ReadFree(ctx context.Context, tx *sql.Tx, subjectID, sessionID uint64) (Snapshot, error) {
	if s == nil || s.protector == nil || s.now == nil || ctx == nil || tx == nil || subjectID == 0 || sessionID == 0 {
		return Snapshot{}, ErrInvalid
	}
	var userStatus string
	if err := tx.QueryRowContext(ctx, `SELECT status FROM user WHERE id = ? FOR UPDATE`, subjectID).Scan(&userStatus); err != nil || userStatus != "active" {
		return Snapshot{}, ErrUnavailable
	}
	var result Snapshot
	var source, status, createdRaw, updatedRaw string
	var recordedRaw sql.NullString
	var eventID []byte
	err := tx.QueryRowContext(ctx, `SELECT id, version, CAST(date AS CHAR), time_zone, source, status, client_event_id,
        DATE_FORMAT(created_at, '%Y-%m-%d %H:%i:%s.%f'), DATE_FORMAT(updated_at, '%Y-%m-%d %H:%i:%s.%f'),
        DATE_FORMAT(recorded_at, '%Y-%m-%d %H:%i:%s.%f')
        FROM workout_session WHERE id = ? AND user_id = ? FOR UPDATE`, sessionID, subjectID).
		Scan(&result.ID, &result.Version, &result.Date, &result.TimeZone, &source, &status, &eventID, &createdRaw, &updatedRaw, &recordedRaw)
	if err != nil || result.ID != sessionID || result.Version == 0 || source != "free" {
		return Snapshot{}, ErrUnavailable
	}
	if _, err := validateFreeInput(FreeSessionInput{Date: result.Date, TimeZone: &result.TimeZone, ClientEventID: string(eventID)}, s.now()); err != nil {
		return Snapshot{}, ErrUnavailable
	}
	result.ClientEventID = string(eventID)
	result.Status = domain.Status(status)
	if result.Status != domain.InProgress && result.Status != domain.Completed && result.Status != domain.Skipped {
		return Snapshot{}, ErrUnavailable
	}
	var epochID, epochTag []byte
	if err := tx.QueryRowContext(ctx, `SELECT key_id, key_tag FROM workout_crypto_key_epoch WHERE slot = 1 FOR SHARE`).Scan(&epochID, &epochTag); err != nil ||
		string(epochID) != s.protector.keyID || !hmac.Equal(epochTag, s.protector.tag[:]) {
		return Snapshot{}, ErrUnavailable
	}
	current, err := s.load(ctx, tx, subjectID, sessionID, result.Status)
	if err != nil || (result.Status == domain.Completed && len(current.Sets) == 0) || (result.Status == domain.Skipped && len(current.Sets) != 0) {
		return Snapshot{}, ErrUnavailable
	}
	const layout = "2006-01-02 15:04:05.000000"
	result.CreatedAt, err = time.Parse(layout, createdRaw)
	if err != nil {
		return Snapshot{}, ErrUnavailable
	}
	result.UpdatedAt, err = time.Parse(layout, updatedRaw)
	if err != nil {
		return Snapshot{}, ErrUnavailable
	}
	if recordedRaw.Valid {
		value, err := time.Parse(layout, recordedRaw.String)
		if err != nil {
			return Snapshot{}, ErrUnavailable
		}
		result.RecordedAt = &value
	}
	if result.Status == domain.Completed && result.RecordedAt == nil {
		return Snapshot{}, ErrUnavailable
	}
	result.Sets = current.Sets
	return result, nil
}
