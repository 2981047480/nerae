package middleware

import (
	"database/sql"
	"errors"
	"net/http"
	"strings"

	"nerae/api/internal/modules/auth/session"
)

var (
	ErrInvalidBearer   = errors.New("invalid bearer authorization")
	ErrSessionRejected = errors.New("session could not be authenticated")
	ErrAuthUnavailable = errors.New("authentication boundary unavailable")
)

// AuthenticateRequest accepts only a signed server session from Authorization
// and authenticates it against current database state in the caller's live tx.
// This is not resource, role or age authorization. The caller must use the same
// tx and recheck Principal.ForTransaction before committing any business write.
// Distinguishes an invalid session from inability to verify it; this boundary
// does not itself write an HTTP response or decide resource authorization.
func AuthenticateRequest(r *http.Request, tx *sql.Tx, sessions *session.Store) (*session.Principal, error) {
	if r == nil || tx == nil || sessions == nil {
		return nil, ErrAuthUnavailable
	}
	token, err := bearer(r.Header)
	if err != nil {
		return nil, err
	}
	principal, err := sessions.Authenticate(r.Context(), tx, token)
	if errors.Is(err, session.ErrInvalidSession) {
		return nil, ErrSessionRejected
	}
	if err != nil || principal == nil {
		return nil, ErrAuthUnavailable
	}
	return principal, nil
}

// AuthFailureKind maps only known boundary failures; unknown errors stay 500.
// A caller must still abort a cancelled request and must not expose its error.
func AuthFailureKind(err error) ErrorKind {
	switch {
	case errors.Is(err, ErrInvalidBearer), errors.Is(err, ErrSessionRejected):
		return Unauthenticated
	case errors.Is(err, ErrAuthUnavailable):
		return Unavailable
	default:
		return InternalError
	}
}

func bearer(header http.Header) (string, error) {
	values := header.Values("Authorization")
	if len(values) != 1 || len(values[0]) > 8200 {
		return "", ErrInvalidBearer
	}
	scheme, token, found := strings.Cut(values[0], " ")
	if !found || !strings.EqualFold(scheme, "Bearer") {
		return "", ErrInvalidBearer
	}
	token = strings.TrimLeft(token, " ")
	if len(token) < 32 || len(token) > 8192 {
		return "", ErrInvalidBearer
	}
	for _, c := range token {
		if !(c >= 'a' && c <= 'z') && !(c >= 'A' && c <= 'Z') &&
			!(c >= '0' && c <= '9') && c != '-' && c != '_' && c != '.' {
			return "", ErrInvalidBearer
		}
	}
	return token, nil
}
