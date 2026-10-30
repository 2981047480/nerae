package session

import (
	"context"
	"database/sql"
	"log/slog"
	"strconv"
	"time"
	"unicode/utf8"
)

// ResponseProfile is a point-in-time projection for an authenticated response,
// not an authorization grant. Subsequent requests must authenticate again.
// It deliberately omits the token and fields for which no source exists.
type ResponseProfile struct {
	SubjectID             string
	Nickname              string
	Roles                 []string
	AgeVerified           bool
	AgeDeclarationVersion uint64
	ExpiresAt             time.Time
}

func (ResponseProfile) String() string               { return "ResponseProfile{redacted}" }
func (ResponseProfile) GoString() string             { return "ResponseProfile{redacted}" }
func (ResponseProfile) MarshalJSON() ([]byte, error) { return nil, ErrSessionUnavailable }
func (ResponseProfile) LogValue() slog.Value         { return slog.StringValue("ResponseProfile{redacted}") }

// RemainingSeconds is for immediate response presentation. Rounding down
// avoids extending the signed session lifetime; an almost-expired session is
// not represented as a valid response with a zero or negative lifetime.
func (p ResponseProfile) RemainingSeconds(now time.Time) (int, error) {
	if now.IsZero() || p.ExpiresAt.IsZero() || !p.ExpiresAt.After(now) {
		return 0, ErrInvalidSession
	}
	seconds := int64(p.ExpiresAt.Sub(now) / time.Second)
	if seconds < 1 || seconds > int64((30*24*time.Hour)/time.Second) {
		return 0, ErrInvalidSession
	}
	return int(seconds), nil
}

// ProfileForResponse authenticates the committed session in a fresh database
// transaction, then reads the current profile and roles under row locks. It
// does not accept a caller-supplied subject or fabricate an onboarding state.
// Disabled roles are omitted; an absent nickname is represented by "".
func (s *Store) ProfileForResponse(ctx context.Context, db *sql.DB, token string) (ResponseProfile, error) {
	if s == nil || s.now == nil || ctx == nil || db == nil || token == "" {
		return ResponseProfile{}, ErrSessionUnavailable
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return ResponseProfile{}, ErrSessionUnavailable
	}
	defer tx.Rollback()
	principal, err := s.Authenticate(ctx, tx, token)
	if err != nil {
		return ResponseProfile{}, err
	}
	var nickname sql.NullString
	if err := tx.QueryRowContext(ctx, `SELECT nickname FROM user WHERE id = ? FOR UPDATE`, principal.subjectID).Scan(&nickname); err != nil {
		return ResponseProfile{}, ErrSessionUnavailable
	}
	if nickname.Valid && (!utf8.ValidString(nickname.String) || utf8.RuneCountInString(nickname.String) > 64) {
		return ResponseProfile{}, ErrSessionUnavailable
	}
	rows, err := tx.QueryContext(ctx, `SELECT role, status FROM user_role WHERE user_id = ? ORDER BY role FOR UPDATE`, principal.subjectID)
	if err != nil {
		return ResponseProfile{}, ErrSessionUnavailable
	}
	roles := make([]string, 0, 2)
	for rows.Next() {
		var role, status string
		if err := rows.Scan(&role, &status); err != nil || (role != "student" && role != "coach") || (status != "active" && status != "disabled") {
			_ = rows.Close()
			return ResponseProfile{}, ErrSessionUnavailable
		}
		if status == "active" {
			roles = append(roles, role)
		}
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return ResponseProfile{}, ErrSessionUnavailable
	}
	if err := rows.Close(); err != nil {
		return ResponseProfile{}, ErrSessionUnavailable
	}
	details, err := principal.ForTransaction(ctx, tx)
	if err != nil {
		// ForTransaction intentionally treats several in-transaction failures
		// as unavailable. Expiry is independently observable and should not
		// be mistaken for a transient storage failure.
		now := s.now().UTC()
		if !now.IsZero() && !principal.expiresAt.After(now) {
			return ResponseProfile{}, ErrInvalidSession
		}
		return ResponseProfile{}, ErrSessionUnavailable
	}
	if details.SubjectID != principal.subjectID {
		return ResponseProfile{}, ErrSessionUnavailable
	}
	ageVersion, declared, err := readAgeDeclarationVersion(ctx, tx, details.SubjectID)
	if err != nil || declared != details.AgeVerified {
		return ResponseProfile{}, ErrSessionUnavailable
	}
	if err := tx.Commit(); err != nil {
		return ResponseProfile{}, ErrSessionUnavailable
	}
	claims, err := s.verifySessionToken(token)
	if err != nil {
		return ResponseProfile{}, err
	}
	if claims.SubjectID != strconv.FormatUint(details.SubjectID, 10) || claims.SessionID != details.SessionID || !claims.ExpiresAt.Equal(principal.expiresAt) {
		return ResponseProfile{}, ErrSessionUnavailable
	}
	profile := ResponseProfile{SubjectID: claims.SubjectID, Roles: roles, AgeVerified: declared, AgeDeclarationVersion: ageVersion, ExpiresAt: claims.ExpiresAt}
	if nickname.Valid {
		profile.Nickname = nickname.String
	}
	if _, err := profile.RemainingSeconds(s.now().UTC()); err != nil {
		return ResponseProfile{}, err
	}
	return profile, nil
}
