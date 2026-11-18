package repository

import (
	"context"
	"database/sql"
	"log/slog"

	"nerae/api/internal/modules/workout/domain"
)

type RecordingV2ListItem struct {
	ID, Version    uint64
	RecordFormat   uint8
	Date, TimeZone string
	Status         domain.Status
}
type RecordingV2ListPage struct {
	Items                 []RecordingV2ListItem
	Page, PageSize, Total uint64
}

func (RecordingV2ListItem) String() string               { return "RecordingV2ListItem{redacted}" }
func (RecordingV2ListItem) GoString() string             { return "RecordingV2ListItem{redacted}" }
func (RecordingV2ListItem) MarshalJSON() ([]byte, error) { return nil, ErrUnavailable }
func (RecordingV2ListItem) LogValue() slog.Value {
	return slog.StringValue("RecordingV2ListItem{redacted}")
}
func (RecordingV2ListPage) String() string               { return "RecordingV2ListPage{redacted}" }
func (RecordingV2ListPage) GoString() string             { return "RecordingV2ListPage{redacted}" }
func (RecordingV2ListPage) MarshalJSON() ([]byte, error) { return nil, ErrUnavailable }
func (RecordingV2ListPage) LogValue() slog.Value {
	return slog.StringValue("RecordingV2ListPage{redacted}")
}

// ListRecordingFormats lists both legacy and new records for the authorized
// owner. It is a persistence primitive; caller enforces session and age.
func (s *SessionStore) ListRecordingFormats(ctx context.Context, tx *sql.Tx, subject uint64, filter ListFilter) (RecordingV2ListPage, error) {
	if s == nil || s.now == nil || ctx == nil || tx == nil || subject == 0 || filter.Page == 0 || filter.PageSize == 0 || filter.PageSize > 50 ||
		!validListDate(filter.From) || !validListDate(filter.To) || (filter.From != "" && filter.To != "" && filter.From > filter.To) {
		return RecordingV2ListPage{}, ErrInvalid
	}
	var userStatus string
	if err := tx.QueryRowContext(ctx, freeLockSubjectSQL, subject).Scan(&userStatus); err != nil || userStatus != "active" {
		return RecordingV2ListPage{}, ErrUnavailable
	}
	clause := ` FROM workout_session WHERE user_id = ? AND source = 'free'`
	args := []any{subject}
	if filter.From != "" {
		clause += ` AND date >= ?`
		args = append(args, filter.From)
	}
	if filter.To != "" {
		clause += ` AND date <= ?`
		args = append(args, filter.To)
	}
	page := RecordingV2ListPage{Items: make([]RecordingV2ListItem, 0), Page: filter.Page, PageSize: filter.PageSize}
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*)`+clause, args...).Scan(&page.Total); err != nil {
		return RecordingV2ListPage{}, ErrUnavailable
	}
	if page.Total == 0 || filter.Page-1 > page.Total/filter.PageSize {
		return page, nil
	}
	offset := (filter.Page - 1) * filter.PageSize
	if offset >= page.Total {
		return page, nil
	}
	args = append(args, filter.PageSize, offset)
	rows, err := tx.QueryContext(ctx, `SELECT id, version, record_format, CAST(date AS CHAR), time_zone, status, client_event_id, recorded_at IS NOT NULL`+clause+` ORDER BY date DESC, id DESC LIMIT ? OFFSET ?`, args...)
	if err != nil {
		return RecordingV2ListPage{}, ErrUnavailable
	}
	defer rows.Close()
	now := s.now()
	if now.IsZero() {
		return RecordingV2ListPage{}, ErrUnavailable
	}
	for rows.Next() {
		var item RecordingV2ListItem
		var event []byte
		var status string
		var hasRecorded bool
		if rows.Scan(&item.ID, &item.Version, &item.RecordFormat, &item.Date, &item.TimeZone, &status, &event, &hasRecorded) != nil || item.ID == 0 || item.Version == 0 || item.Version > 9007199254740991 || (item.RecordFormat != 1 && item.RecordFormat != 2) {
			return RecordingV2ListPage{}, ErrUnavailable
		}
		if _, err := validateFreeInput(FreeSessionInput{Date: item.Date, TimeZone: &item.TimeZone, ClientEventID: string(event)}, now); err != nil {
			return RecordingV2ListPage{}, ErrUnavailable
		}
		item.Status = domain.Status(status)
		if item.Status != domain.InProgress && item.Status != domain.Completed && item.Status != domain.Skipped || item.RecordFormat == 2 && item.Status == domain.Skipped || item.Status == domain.Completed && !hasRecorded {
			return RecordingV2ListPage{}, ErrUnavailable
		}
		page.Items = append(page.Items, item)
	}
	if rows.Err() != nil {
		return RecordingV2ListPage{}, ErrUnavailable
	}
	return page, nil
}
