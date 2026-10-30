// Package application coordinates authentication transactions. It does not
// expose HTTP endpoints or supply mail delivery, rate limits, or idempotency.
package application

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"

	"nerae/api/internal/modules/auth/approval"
	"nerae/api/internal/modules/auth/challenge"
	"nerae/api/internal/modules/auth/session"
)

var (
	// ErrRejected intentionally does not distinguish expired, invalid, reused,
	// or superseded challenges from an incorrect code.
	ErrRejected    = errors.New("email login rejected")
	ErrUnavailable = errors.New("email login unavailable")
)

type EmailLogin struct {
	challenges *challenge.Store
	sessions   *session.Store
	approvals  *approval.Store
}

func (EmailLogin) String() string               { return "EmailLogin{redacted}" }
func (EmailLogin) GoString() string             { return "EmailLogin{redacted}" }
func (EmailLogin) MarshalJSON() ([]byte, error) { return nil, ErrUnavailable }
func (EmailLogin) LogValue() slog.Value         { return slog.StringValue("EmailLogin{redacted}") }

func NewEmailLogin(challenges *challenge.Store, sessions *session.Store, approvals *approval.Store) (*EmailLogin, error) {
	if challenges == nil || sessions == nil || approvals == nil {
		return nil, ErrUnavailable
	}
	return &EmailLogin{challenges: challenges, sessions: sessions, approvals: approvals}, nil
}

type OutcomeKind string

const (
	Authenticated    OutcomeKind = "authenticated"
	ApprovalRequired OutcomeKind = "approval_required"
)

// Outcome deliberately cannot be directly serialized or logged. Only a trusted
// presenter may explicitly retrieve the one post-commit result matching Kind.
type Outcome struct {
	kind     OutcomeKind
	token    string
	approval approval.Data
}

func (Outcome) String() string               { return "EmailLoginOutcome{redacted}" }
func (Outcome) GoString() string             { return "EmailLoginOutcome{redacted}" }
func (Outcome) MarshalJSON() ([]byte, error) { return nil, ErrUnavailable }
func (Outcome) LogValue() slog.Value         { return slog.StringValue("EmailLoginOutcome{redacted}") }
func (o Outcome) Kind() OutcomeKind          { return o.kind }

// SessionTokenForResponse exposes a credential to the eventual authenticated
// response presenter; it must never be logged, cached unprotected, or returned
// before its originating transaction is committed and revalidated.
func (o Outcome) SessionTokenForResponse() (string, bool) {
	if o.kind != Authenticated || o.token == "" {
		return "", false
	}
	return o.token, true
}

// PendingApprovalForResponse exposes only the new-device request to the
// eventual response presenter; it does not authorize a session or business data.
func (o Outcome) PendingApprovalForResponse() (approval.Data, bool) {
	if o.kind != ApprovalRequired || o.approval.ID == "" || o.approval.Secret == "" {
		return approval.Data{}, false
	}
	return o.approval, true
}

// VerifyEmail coordinates a previously issued challenge in one transaction.
// Incorrect-code counters are committed even though authentication is rejected.
// A new device receives a pending approval, never a session. Commit failures and
// post-commit validation failures are unavailable: this method does not claim
// to recover unknown commit results. It must not be exposed publicly until
// durable idempotency, rate limits, SMTP delivery, and HTTP integration exist.
func (s *EmailLogin) VerifyEmail(ctx context.Context, db *sql.DB, input challenge.AttemptInput) (Outcome, error) {
	if s == nil || s.challenges == nil || s.sessions == nil || s.approvals == nil || ctx == nil || db == nil {
		return Outcome{}, ErrUnavailable
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return Outcome{}, ErrUnavailable
	}
	defer tx.Rollback()

	attempt, err := s.challenges.Attempt(ctx, tx, input)
	if err != nil {
		return Outcome{}, ErrUnavailable
	}
	if !attempt.Verified {
		if attempt.Proof != nil {
			return Outcome{}, ErrUnavailable
		}
		if attempt.Changed && tx.Commit() != nil {
			return Outcome{}, ErrUnavailable
		}
		return Outcome{}, ErrRejected
	}
	if !attempt.Changed || attempt.Proof == nil {
		return Outcome{}, ErrUnavailable
	}

	issued, err := s.sessions.IssueEmail(ctx, tx, attempt.Proof)
	if errors.Is(err, session.ErrApprovalRequired) {
		pending, err := s.approvals.BeginEmail(ctx, tx, attempt.Proof)
		if err != nil || tx.Commit() != nil {
			return Outcome{}, ErrUnavailable
		}
		data, err := pending.AfterCommit(ctx, db)
		if err != nil {
			return Outcome{}, ErrUnavailable
		}
		return Outcome{kind: ApprovalRequired, approval: data}, nil
	}
	if err != nil || tx.Commit() != nil {
		return Outcome{}, ErrUnavailable
	}
	token, err := issued.AfterCommit(ctx, db)
	if err != nil || token == "" {
		return Outcome{}, ErrUnavailable
	}
	return Outcome{kind: Authenticated, token: token}, nil
}
