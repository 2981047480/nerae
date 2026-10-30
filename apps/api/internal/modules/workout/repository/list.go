package repository

import (
	"context"
	"database/sql"
	"log/slog"
	"time"

	"nerae/api/internal/modules/workout/domain"
)

// ListFilter is constructed by a trusted caller after parsing the contract's
// query parameters. Empty dates mean omitted; Page and PageSize are explicit.
type ListFilter struct {
	From, To       string
	Page, PageSize uint64
}

// SessionSummary and SessionPage are internal results, not HTTP responses.
// A presenter must map only fields allowed to the currently authenticated owner.
type SessionSummary struct {
	ID, Version    uint64
	Date, TimeZone string
	Status         domain.Status
	CreatedAt      time.Time
	UpdatedAt      time.Time
	RecordedAt     *time.Time
}

type SessionPage struct {
	Items                 []SessionSummary
	Page, PageSize, Total uint64
}

func (SessionSummary) String() string               { return "WorkoutSummary{redacted}" }
func (SessionSummary) GoString() string             { return "WorkoutSummary{redacted}" }
func (SessionSummary) MarshalJSON() ([]byte, error) { return nil, ErrUnavailable }
func (SessionSummary) LogValue() slog.Value         { return slog.StringValue("WorkoutSummary{redacted}") }
func (SessionPage) String() string                  { return "WorkoutPage{redacted}" }
func (SessionPage) GoString() string                { return "WorkoutPage{redacted}" }
func (SessionPage) MarshalJSON() ([]byte, error)    { return nil, ErrUnavailable }
func (SessionPage) LogValue() slog.Value            { return slog.StringValue("WorkoutPage{redacted}") }

func validListDate(value string) bool {
	if value == "" {
		return true
	}
	parsed, err := time.Parse("2006-01-02", value)
	return err == nil && parsed.Format("2006-01-02") == value
}

// ListFree returns only one authenticated subject's persisted free records.
// The caller MUST authenticate and check age in the same live transaction.
// Records are ordered by training date descending, then session ID descending.
// This does not include planned records, HTTP, or pagination snapshot tokens.
func (s *SessionStore) ListFree(ctx context.Context, tx *sql.Tx, subjectID uint64, filter ListFilter) (SessionPage, error) {
	if s == nil || s.now == nil || ctx == nil || tx == nil || subjectID == 0 ||
		filter.Page == 0 || filter.PageSize == 0 || filter.PageSize > 50 ||
		!validListDate(filter.From) || !validListDate(filter.To) ||
		(filter.From != "" && filter.To != "" && filter.From > filter.To) {
		return SessionPage{}, ErrInvalid
	}
	var status string
	if err := tx.QueryRowContext(ctx, `SELECT status FROM user WHERE id = ? FOR UPDATE`, subjectID).Scan(&status); err != nil || status != "active" {
		return SessionPage{}, ErrUnavailable
	}
	clause := ` FROM workout_session WHERE user_id = ? AND source = 'free'`
	args := []any{subjectID}
	if filter.From != "" {
		clause += ` AND date >= ?`
		args = append(args, filter.From)
	}
	if filter.To != "" {
		clause += ` AND date <= ?`
		args = append(args, filter.To)
	}
	result := SessionPage{Items: make([]SessionSummary, 0), Page: filter.Page, PageSize: filter.PageSize}
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*)`+clause, args...).Scan(&result.Total); err != nil {
		return SessionPage{}, ErrUnavailable
	}
	// Compare before multiplying to avoid overflow, even for enormous pages.
	if result.Total == 0 || filter.Page-1 > result.Total/filter.PageSize {
		return result, nil
	}
	offset := (filter.Page - 1) * filter.PageSize
	if offset >= result.Total {
		return result, nil
	}
	args = append(args, filter.PageSize, offset)
	rows, err := tx.QueryContext(ctx, `SELECT id, version, CAST(date AS CHAR), time_zone, status, client_event_id,
		DATE_FORMAT(created_at, '%Y-%m-%d %H:%i:%s.%f'), DATE_FORMAT(updated_at, '%Y-%m-%d %H:%i:%s.%f'),
		DATE_FORMAT(recorded_at, '%Y-%m-%d %H:%i:%s.%f')`+clause+` ORDER BY date DESC, id DESC LIMIT ? OFFSET ?`, args...)
	if err != nil {
		return SessionPage{}, ErrUnavailable
	}
	defer rows.Close()
	const layout = "2006-01-02 15:04:05.000000"
	now := s.now()
	if now.IsZero() {
		return SessionPage{}, ErrUnavailable
	}
	for rows.Next() {
		var row SessionSummary
		var eventID []byte
		var rowStatus, createdRaw, updatedRaw string
		var recordedRaw sql.NullString
		if err := rows.Scan(&row.ID, &row.Version, &row.Date, &row.TimeZone, &rowStatus, &eventID, &createdRaw, &updatedRaw, &recordedRaw); err != nil || row.ID == 0 || row.Version == 0 {
			return SessionPage{}, ErrUnavailable
		}
		if _, err := validateFreeInput(FreeSessionInput{Date: row.Date, TimeZone: &row.TimeZone, ClientEventID: string(eventID)}, now); err != nil {
			return SessionPage{}, ErrUnavailable
		}
		row.Status = domain.Status(rowStatus)
		if row.Status != domain.InProgress && row.Status != domain.Completed && row.Status != domain.Skipped {
			return SessionPage{}, ErrUnavailable
		}
		row.CreatedAt, err = time.Parse(layout, createdRaw)
		if err != nil {
			return SessionPage{}, ErrUnavailable
		}
		row.UpdatedAt, err = time.Parse(layout, updatedRaw)
		if err != nil {
			return SessionPage{}, ErrUnavailable
		}
		if recordedRaw.Valid {
			value, err := time.Parse(layout, recordedRaw.String)
			if err != nil {
				return SessionPage{}, ErrUnavailable
			}
			row.RecordedAt = &value
		}
		if row.Status == domain.Completed && row.RecordedAt == nil {
			return SessionPage{}, ErrUnavailable
		}
		result.Items = append(result.Items, row)
	}
	if rows.Err() != nil {
		return SessionPage{}, ErrUnavailable
	}
	return result, nil
}
