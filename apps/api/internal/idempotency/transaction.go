package idempotency

import (
	"context"
	"database/sql"
	"log/slog"
	"strings"

	"nerae/api/internal/dbtx"
	"nerae/api/internal/modules/auth/session"
)

// Actor is produced from the authenticated session within this transaction.
// It cannot be supplied by a request body or constructed with an arbitrary ID.
type Actor struct {
	subjectID   uint64
	sessionID   string
	ageVerified bool
}

func (a Actor) SubjectID() uint64          { return a.subjectID }
func (a Actor) SessionID() string          { return a.sessionID }
func (a Actor) AgeVerified() bool          { return a.ageVerified }
func (Actor) String() string               { return "IdempotencyActor{redacted}" }
func (Actor) GoString() string             { return "IdempotencyActor{redacted}" }
func (Actor) MarshalJSON() ([]byte, error) { return nil, ErrUnavailable }
func (Actor) LogValue() slog.Value         { return slog.StringValue("IdempotencyActor{redacted}") }

// actorFromPrincipalDetails is called only with details returned by
// Principal.ForTransaction inside the executor-owned transaction. SessionID
// is an opaque, non-secret identifier; no request or callback can set it.
func actorFromPrincipalDetails(details session.PrincipalDetails) (Actor, error) {
	if details.SubjectID == 0 || details.SessionID == "" {
		return Actor{}, ErrUnavailable
	}
	return Actor{subjectID: details.SubjectID, sessionID: details.SessionID, ageVerified: details.AgeVerified}, nil
}

func samePrincipalSession(before, current session.PrincipalDetails) bool {
	return before.SubjectID != 0 && before.SessionID != "" && before.SubjectID == current.SubjectID &&
		before.SessionID == current.SessionID
}

// These shared capability types let repositories use either a normal SQL
// transaction view or this package's policy-restricted view. Authorizers must
// lock every mutable authorization row until transaction commit. This is not
// a SQL security sandbox: policies must be fixed, audited server code, never
// user input, and the database account must have only the privileges it needs.
type Row = dbtx.Row
type ReadTransaction = dbtx.ReadTransaction
type Transaction = dbtx.Transaction

// ResultValidator is trusted server code which can reject a reply before it is
// committed or replayed. It must not log the body or retain the supplied slice.
type ResultValidator func(status int, ref string, body []byte) error

// StatementPolicy is a fixed set of reviewed, parameterized statements for
// trusted callbacks. The store copies it at construction; arbitrary strings or
// SQL assembled from request input are not accepted.
type StatementPolicy struct {
	Reads          []string
	Writes         []string
	ValidateResult ResultValidator
}

type accessView struct {
	tx    *sql.Tx
	reads map[string]struct{}
}
type transactionView struct {
	accessView
	writes map[string]struct{}
}
type invalidRow struct{}

func (invalidRow) Scan(...any) error { return ErrInvalid }

func allowedStatement(query string, expected ...string) bool {
	if len(query) == 0 || len(query) > 16384 || strings.ContainsAny(query, ";\x00") || strings.Contains(query, "--") ||
		strings.Contains(query, "/*") || strings.Contains(query, "*/") || strings.Contains(query, "#") {
		return false
	}
	fields := strings.Fields(query)
	if len(fields) == 0 {
		return false
	}
	first := strings.ToUpper(fields[0])
	for _, keyword := range expected {
		if first == keyword {
			return true
		}
	}
	return false
}

func compilePolicy(policy StatementPolicy) (map[string]struct{}, map[string]struct{}, error) {
	if len(policy.Reads)+len(policy.Writes) > 256 {
		return nil, nil, ErrInvalid
	}
	reads, writes := make(map[string]struct{}, len(policy.Reads)), make(map[string]struct{}, len(policy.Writes))
	for _, query := range policy.Reads {
		if !allowedStatement(query, "SELECT") {
			return nil, nil, ErrInvalid
		}
		reads[query] = struct{}{}
	}
	for _, query := range policy.Writes {
		if !allowedStatement(query, "INSERT", "UPDATE", "DELETE") {
			return nil, nil, ErrInvalid
		}
		writes[query] = struct{}{}
	}
	return reads, writes, nil
}

func (a accessView) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	if _, ok := a.reads[query]; a.tx == nil || !ok {
		return nil, ErrInvalid
	}
	return a.tx.QueryContext(ctx, query, args...)
}
func (a accessView) QueryRowContext(ctx context.Context, query string, args ...any) Row {
	if _, ok := a.reads[query]; a.tx == nil || !ok {
		return invalidRow{}
	}
	return a.tx.QueryRowContext(ctx, query, args...)
}
func (t transactionView) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	if _, ok := t.writes[query]; t.tx == nil || !ok {
		return nil, ErrInvalid
	}
	return t.tx.ExecContext(ctx, query, args...)
}
