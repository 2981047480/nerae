// Package repository contains workout persistence primitives, not an auth API.
package repository

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"time"
	"unicode/utf8"
)

var (
	ErrInvalid     = errors.New("invalid workout session")
	ErrConflict    = errors.New("workout event conflict")
	ErrUnavailable = errors.New("workout session unavailable")
)

type FreeSessionInput struct {
	Date          string
	TimeZone      *string // nil means omitted; an explicit empty string is invalid.
	ClientEventID string
}

func (FreeSessionInput) String() string               { return "FreeSessionInput{redacted}" }
func (FreeSessionInput) GoString() string             { return "FreeSessionInput{redacted}" }
func (FreeSessionInput) MarshalJSON() ([]byte, error) { return nil, ErrUnavailable }
func (FreeSessionInput) LogValue() slog.Value         { return slog.StringValue("FreeSessionInput{redacted}") }

// SessionRef is an internal result. It is not an authorization credential or
// an HTTP response; a session may have been edited since its initial creation.
type SessionRef struct {
	ID      uint64
	Version uint64
}

func (SessionRef) String() string               { return "SessionRef{redacted}" }
func (SessionRef) GoString() string             { return "SessionRef{redacted}" }
func (SessionRef) MarshalJSON() ([]byte, error) { return nil, ErrUnavailable }
func (SessionRef) LogValue() slog.Value         { return slog.StringValue("SessionRef{redacted}") }

type SessionStore struct{ now func() time.Time }

func NewSessionStore() *SessionStore { return &SessionStore{now: time.Now} }

// CreateFree creates or locates a free workout for a subject-scoped event.
// The caller must derive subjectID from real server-side authentication,
// enforce age and resource access, and commit or roll back the same tx.
// It does not implement HTTP response idempotency, sessions, or set storage.
func (s *SessionStore) CreateFree(ctx context.Context, tx *sql.Tx, subjectID uint64, input FreeSessionInput) (SessionRef, error) {
	if s == nil || s.now == nil || ctx == nil || tx == nil || subjectID == 0 {
		return SessionRef{}, ErrInvalid
	}
	now := s.now()
	timeZone, err := validateFreeInput(input, now)
	if now.IsZero() || err != nil {
		return SessionRef{}, ErrInvalid
	}
	var status string
	// Lock the subject first: simultaneous first inserts for the same event can
	// otherwise deadlock on InnoDB gap locks after a competing insert rolls back.
	// All callers touching both user and workout rows must keep this order.
	if err := tx.QueryRowContext(ctx, `SELECT status FROM user WHERE id = ? FOR UPDATE`, subjectID).Scan(&status); err != nil || status != "active" {
		return SessionRef{}, ErrUnavailable
	}
	request, err := json.Marshal(struct {
		Version  int    `json:"version"`
		Source   string `json:"source"`
		Date     string `json:"date"`
		TimeZone string `json:"timeZone"`
	}{Version: 1, Source: "free", Date: input.Date, TimeZone: timeZone})
	if err != nil {
		return SessionRef{}, ErrUnavailable
	}
	digest := sha256.Sum256(request)
	timestamp := now.UTC().Truncate(time.Microsecond).Format("2006-01-02 15:04:05.000000")
	_, err = tx.ExecContext(ctx, `INSERT INTO workout_session
        (user_id, source, date, time_zone, status, version, client_event_id, creation_request_hash, created_at, updated_at)
		VALUES (?, 'free', ?, ?, 'in_progress', 1, ?, ?, ?, ?)
		ON DUPLICATE KEY UPDATE id = id`, subjectID, input.Date, timeZone, []byte(input.ClientEventID), digest[:], timestamp, timestamp)
	if err != nil {
		return SessionRef{}, ErrUnavailable
	}
	var result SessionRef
	var storedHash []byte
	err = tx.QueryRowContext(ctx, `SELECT id, version, creation_request_hash FROM workout_session
        WHERE user_id = ? AND client_event_id = ? FOR UPDATE`, subjectID, []byte(input.ClientEventID)).
		Scan(&result.ID, &result.Version, &storedHash)
	if err != nil || result.ID == 0 || result.Version == 0 {
		return SessionRef{}, ErrUnavailable
	}
	if !hmac.Equal(storedHash, digest[:]) {
		return SessionRef{}, ErrConflict
	}
	return result, nil
}

func validateFreeInput(input FreeSessionInput, now time.Time) (string, error) {
	timeZone := "Asia/Shanghai"
	if input.TimeZone != nil {
		timeZone = *input.TimeZone
	}
	if !utf8.ValidString(input.ClientEventID) || utf8.RuneCountInString(input.ClientEventID) < 8 ||
		utf8.RuneCountInString(input.ClientEventID) > 64 || len(timeZone) == 0 || len(timeZone) > 64 || timeZone == "Local" {
		return "", ErrInvalid
	}
	location, err := time.LoadLocation(timeZone)
	if err != nil {
		return "", ErrInvalid
	}
	date, err := time.Parse("2006-01-02", input.Date)
	if err != nil || date.Format("2006-01-02") != input.Date || date.Year() < 1000 || input.Date > now.In(location).Format("2006-01-02") {
		return "", ErrInvalid
	}
	// IANA names are ASCII. Avoid implicit database conversion or truncation.
	if strings.IndexFunc(timeZone, func(r rune) bool { return r > 127 || r < 33 }) != -1 {
		return "", ErrInvalid
	}
	return timeZone, nil
}
