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

type VerificationKind string

const (
	VerificationRejected         VerificationKind = "rejected"
	VerificationAuthenticated    VerificationKind = "authenticated"
	VerificationApprovalRequired VerificationKind = "approval_required"
)

// TransactionResult is a non-serializable transaction-bound decision. A
// rejected result may still require commit when the challenge attempt counter
// changed. Successful results retain only the existing opaque session or
// approval capability; credentials are available only through AfterCommit.
type TransactionResult struct {
	kind           VerificationKind
	commitRequired bool
	sourceTx       *sql.Tx
	issued         session.Issued
	pending        approval.Pending
	proof          *challenge.VerifiedEmail
}

func (TransactionResult) String() string               { return "EmailLoginTransactionResult{redacted}" }
func (TransactionResult) GoString() string             { return "EmailLoginTransactionResult{redacted}" }
func (TransactionResult) MarshalJSON() ([]byte, error) { return nil, ErrUnavailable }
func (TransactionResult) LogValue() slog.Value {
	return slog.StringValue("EmailLoginTransactionResult{redacted}")
}
func (r TransactionResult) Kind() VerificationKind { return r.kind }
func (r TransactionResult) CommitRequired() bool   { return r.commitRequired }

// AfterCommit finalizes a transaction result. Rejected results with changed
// attempt state are returned only after the source transaction has ended.
// sql.Tx does not expose whether it ended by commit or rollback, so callers
// must invoke this method for such a result only after Commit returned nil.
// Successful results delegate to opaque handles that re-authorize in a fresh
// transaction before releasing credentials.
func (r TransactionResult) AfterCommit(ctx context.Context, db *sql.DB) (Outcome, error) {
	switch r.kind {
	case VerificationAuthenticated:
		token, err := r.issued.AfterCommit(ctx, db)
		if err != nil || token == "" {
			return Outcome{}, ErrUnavailable
		}
		return Outcome{kind: Authenticated, token: token}, nil
	case VerificationApprovalRequired:
		data, err := r.pending.AfterCommit(ctx, db)
		if err != nil {
			return Outcome{}, ErrUnavailable
		}
		return Outcome{kind: ApprovalRequired, approval: data}, nil
	case VerificationRejected:
		if r.commitRequired {
			if ctx == nil || r.sourceTx == nil {
				return Outcome{}, ErrUnavailable
			}
			var live int
			if err := r.sourceTx.QueryRowContext(ctx, `SELECT 1`).Scan(&live); !errors.Is(err, sql.ErrTxDone) {
				return Outcome{}, ErrUnavailable
			}
		}
		return Outcome{}, ErrRejected
	default:
		return Outcome{}, ErrUnavailable
	}
}

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

	result, err := s.VerifyEmailInTransaction(ctx, tx, input)
	if err != nil {
		return Outcome{}, ErrUnavailable
	}
	if result.kind == VerificationRejected {
		if result.CommitRequired() && tx.Commit() != nil {
			return Outcome{}, ErrUnavailable
		}
		return Outcome{}, ErrRejected
	}
	if !result.CommitRequired() {
		return Outcome{}, ErrUnavailable
	}
	if tx.Commit() != nil {
		return Outcome{}, ErrUnavailable
	}
	return result.AfterCommit(ctx, db)
}

// VerifyEmailInTransaction coordinates a challenge within a caller-owned
// transaction. It never commits or rolls back. The caller must commit a result
// with CommitRequired()==true even when Kind()==VerificationRejected, so an
// incorrect-code attempt is persisted. Any returned error is infrastructure or
// consistency failure; the caller must roll back. A successful result releases
// no credential until AfterCommit is called after the caller commits.
func (s *EmailLogin) VerifyEmailInTransaction(ctx context.Context, tx *sql.Tx, input challenge.AttemptInput) (TransactionResult, error) {
	if s == nil || s.challenges == nil || s.sessions == nil || s.approvals == nil || ctx == nil || tx == nil {
		return TransactionResult{}, ErrUnavailable
	}
	attempt, err := s.challenges.Attempt(ctx, tx, input)
	if err != nil {
		return TransactionResult{}, ErrUnavailable
	}
	if !attempt.Verified {
		if attempt.Proof != nil {
			return TransactionResult{}, ErrUnavailable
		}
		return TransactionResult{kind: VerificationRejected, commitRequired: attempt.Changed, sourceTx: tx}, nil
	}
	if !attempt.Changed || attempt.Proof == nil {
		return TransactionResult{}, ErrUnavailable
	}
	issued, err := s.sessions.IssueEmail(ctx, tx, attempt.Proof)
	if errors.Is(err, session.ErrApprovalRequired) {
		pending, err := s.approvals.BeginEmail(ctx, tx, attempt.Proof)
		if err != nil {
			return TransactionResult{}, ErrUnavailable
		}
		return TransactionResult{kind: VerificationApprovalRequired, commitRequired: true, sourceTx: tx, pending: pending, proof: attempt.Proof}, nil
	}
	if err != nil {
		return TransactionResult{}, ErrUnavailable
	}
	return TransactionResult{kind: VerificationAuthenticated, commitRequired: true, sourceTx: tx, issued: issued, proof: attempt.Proof}, nil
}
