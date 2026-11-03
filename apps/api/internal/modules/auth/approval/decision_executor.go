package approval

import (
	"bytes"
	"context"
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"time"
	"unicode/utf8"

	"nerae/api/internal/idempotency"
	"nerae/api/internal/modules/auth/session"
)

// The same rejection is used for an absent, expired, inaccessible or
// mismatched approval. It must not disclose which condition was encountered.
var errDecisionRejected = errors.New("approval decision rejected")

const (
	decisionSessionSQL = `SELECT identity_id, DATE_FORMAT(authenticated_at, '%Y-%m-%d %H:%i:%s.%f')
		FROM auth_session WHERE id = ? AND user_id = ? FOR UPDATE`
	decisionApprovalSQL = `SELECT user_id, identity_id, token_version, identity_version, comparison_code, state,
		DATE_FORMAT(expires_at, '%Y-%m-%d %H:%i:%s.%f'), COALESCE(decision_session_id, '')
		FROM auth_login_approval WHERE id = ? FOR UPDATE`
	decisionUserSQL   = `SELECT status, token_version, identity_version FROM user WHERE id = ? FOR UPDATE`
	decisionUpdateSQL = `UPDATE auth_login_approval SET state = ?, decision_session_id = ?, decided_at = ?
		WHERE id = ? AND state = 'pending'`
)

// DecisionStatementPolicy is the fixed SQL surface needed by the decision
// callbacks. It may only be composed with other reviewed server policies.
func DecisionStatementPolicy() idempotency.StatementPolicy {
	return idempotency.StatementPolicy{
		Reads:  []string{decisionSessionSQL, decisionApprovalSQL, decisionUserSQL, `SELECT key_id, key_tag FROM auth_approval_key_epoch WHERE slot = 1 FOR SHARE`},
		Writes: []string{decisionUpdateSQL}, ValidateResult: validateDecisionReply,
	}
}

type DecisionExecutor struct {
	approvals *Store
	store     *idempotency.Store
}

func (DecisionExecutor) String() string               { return "ApprovalDecisionExecutor{redacted}" }
func (DecisionExecutor) GoString() string             { return "ApprovalDecisionExecutor{redacted}" }
func (DecisionExecutor) MarshalJSON() ([]byte, error) { return nil, ErrUnavailable }
func (DecisionExecutor) LogValue() slog.Value {
	return slog.StringValue("ApprovalDecisionExecutor{redacted}")
}

// DecisionInput is an internal validated request. Its comparison code is an
// authentication factor, so formatting and logging are redacted.
type DecisionInput struct {
	ApprovalID     string
	IdempotencyKey string
	Decision       Decision
	ComparisonCode string
	Confirmed      bool
}

func (DecisionInput) String() string               { return "ApprovalDecisionInput{redacted}" }
func (DecisionInput) GoString() string             { return "ApprovalDecisionInput{redacted}" }
func (DecisionInput) MarshalJSON() ([]byte, error) { return nil, ErrUnavailable }
func (DecisionInput) LogValue() slog.Value {
	return slog.StringValue("ApprovalDecisionInput{redacted}")
}

type decisionBody struct {
	Decision       Decision `json:"decision"`
	ComparisonCode string   `json:"comparisonCode,omitempty"`
	Confirmed      *bool    `json:"confirmed,omitempty"`
}

type decisionPrincipal struct {
	subjectID uint64
	sessionID string
}

type decisionSnapshot struct {
	sessionIdentity  []byte
	authenticated    time.Time
	approvalUser     uint64
	approvalIdentity []byte
	tokenVersion     uint64
	identityVersion  uint64
	comparisonCode   string
	state            string
	expiresAt        time.Time
	decisionSession  []byte
	userStatus       string
	currentToken     uint64
	currentIdentity  uint64
}

func NewDecisionExecutor(approvals *Store, sessions *session.Store, hashKey, cipherKey []byte, keyID string, ttl time.Duration) (*DecisionExecutor, error) {
	if approvals == nil || sessions == nil {
		return nil, ErrUnavailable
	}
	store, err := idempotency.NewStore(sessions, hashKey, cipherKey, keyID, ttl, DecisionStatementPolicy())
	if err != nil {
		return nil, ErrUnavailable
	}
	return &DecisionExecutor{approvals: approvals, store: store}, nil
}

// InitializeKey initializes only the transport idempotency epoch. Prefer
// Initialize when preparing this executor so both required epochs are checked.
func (e *DecisionExecutor) InitializeKey(ctx context.Context, tx *sql.Tx) error {
	if e == nil || e.store == nil {
		return idempotency.ErrUnavailable
	}
	return e.store.InitializeKey(ctx, tx)
}

// Initialize verifies both fixed key epochs in the caller-owned setup
// transaction. A missing or mismatched epoch fails closed; no key is rotated.
func (e *DecisionExecutor) Initialize(ctx context.Context, tx *sql.Tx) error {
	if e == nil || e.approvals == nil || e.store == nil || ctx == nil || tx == nil {
		return ErrUnavailable
	}
	if err := e.approvals.InitializeKey(ctx, tx); err != nil {
		return ErrUnavailable
	}
	if err := e.store.InitializeKey(ctx, tx); err != nil {
		return idempotency.ErrUnavailable
	}
	return nil
}

// Execute commits the decision and encrypted receipt in one transaction. The
// request body is constructed from typed input so the key cannot be reused for
// a different decision, confirmation state or comparison code.
func (e *DecisionExecutor) Execute(ctx context.Context, db *sql.DB, token string, input DecisionInput) (idempotency.Result, error) {
	if e == nil || e.approvals == nil || e.store == nil || db == nil || ctx == nil || token == "" || !validDecisionInput(input) {
		return idempotency.Result{}, idempotency.ErrInvalid
	}
	req, err := decisionRequest(input)
	if err != nil {
		return idempotency.Result{}, idempotency.ErrInvalid
	}
	authorize := e.authorize(input)
	return e.store.Execute(ctx, db, token, req, authorize, func(ctx context.Context, tx idempotency.Transaction, actor idempotency.Actor) (idempotency.Reply, error) {
		if err := e.approvals.decideForActor(ctx, tx, decisionPrincipal{subjectID: actor.SubjectID(), sessionID: actor.SessionID()},
			input.ApprovalID, input.Decision, input.ComparisonCode, input.Confirmed); err != nil {
			if errors.Is(err, errDecisionRejected) {
				return idempotency.Reply{}, idempotency.ErrForbidden
			}
			return idempotency.Reply{}, idempotency.ErrUnavailable
		}
		state := "denied"
		if input.Decision == Approve {
			state = "approved"
		}
		body, _ := json.Marshal(struct {
			Status string `json:"status"`
		}{Status: state})
		return idempotency.Reply{Status: 200, Body: body, ResultRef: approvalDecisionRef(input.ApprovalID, state)}, nil
	})
}

// Replay returns only a committed receipt after current session and approval
// authorization are checked again. It cannot execute a new decision.
func (e *DecisionExecutor) Replay(ctx context.Context, db *sql.DB, token string, input DecisionInput) (idempotency.Result, error) {
	if e == nil || e.approvals == nil || e.store == nil || db == nil || ctx == nil || token == "" || !validDecisionInput(input) {
		return idempotency.Result{}, idempotency.ErrInvalid
	}
	req, err := decisionRequest(input)
	if err != nil {
		return idempotency.Result{}, idempotency.ErrInvalid
	}
	return e.store.ReplayExisting(ctx, db, token, req, e.authorize(input))
}

func validDecisionInput(input DecisionInput) bool {
	if !validApprovalOpaqueID(input.ApprovalID) || !utf8.ValidString(input.IdempotencyKey) ||
		utf8.RuneCountInString(input.IdempotencyKey) < 22 || utf8.RuneCountInString(input.IdempotencyKey) > 128 ||
		strings.ContainsAny(input.IdempotencyKey, "\x00\r\n") {
		return false
	}
	if input.Decision == Approve {
		return input.Confirmed && validComparisonInput(input.ComparisonCode)
	}
	return input.Decision == Deny && !input.Confirmed && input.ComparisonCode == ""
}

func decisionRequest(input DecisionInput) (idempotency.Request, error) {
	body := decisionBody{Decision: input.Decision}
	if input.Decision == Approve {
		confirmed := true
		body.ComparisonCode, body.Confirmed = input.ComparisonCode, &confirmed
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		return idempotency.Request{}, ErrUnavailable
	}
	return idempotency.Request{Method: "POST", Path: "/me/login-approvals/" + input.ApprovalID + "/decision",
		Key: input.IdempotencyKey, Body: encoded}, nil
}

func approvalDecisionRef(id, state string) string { return "approval-decision:" + state + ":" + id }

func validateDecisionReply(status int, ref string, body []byte) error {
	const approvedPrefix = "approval-decision:approved:"
	const deniedPrefix = "approval-decision:denied:"
	if status == 200 && len(ref) > len(approvedPrefix) && ref[:len(approvedPrefix)] == approvedPrefix &&
		validApprovalOpaqueID(ref[len(approvedPrefix):]) && bytes.Equal(body, []byte(`{"status":"approved"}`)) {
		return nil
	}
	if status == 200 && len(ref) > len(deniedPrefix) && ref[:len(deniedPrefix)] == deniedPrefix &&
		validApprovalOpaqueID(ref[len(deniedPrefix):]) && bytes.Equal(body, []byte(`{"status":"denied"}`)) {
		return nil
	}
	return ErrUnavailable
}

func (e *DecisionExecutor) authorize(input DecisionInput) idempotency.Authorize {
	return func(ctx context.Context, tx idempotency.ReadTransaction, actor idempotency.Actor, ref string, status int) error {
		if e == nil || e.approvals == nil || actor.SubjectID() == 0 || actor.SessionID() == "" {
			return idempotency.ErrForbidden
		}
		principal := decisionPrincipal{subjectID: actor.SubjectID(), sessionID: actor.SessionID()}
		snapshot, err := e.approvals.readDecisionSnapshot(ctx, tx, principal, input.ApprovalID)
		if err != nil {
			if errors.Is(err, errDecisionRejected) {
				return idempotency.ErrForbidden
			}
			return idempotency.ErrUnavailable
		}
		now := e.approvals.now().UTC().Truncate(time.Microsecond)
		if !snapshot.validFor(principal, now) {
			return idempotency.ErrForbidden
		}
		if ref == "" && status == 0 {
			return nil
		}
		wantState := "denied"
		if input.Decision == Approve {
			wantState = "approved"
		}
		if ref != approvalDecisionRef(input.ApprovalID, wantState) || status != 200 || snapshot.state != wantState || string(snapshot.decisionSession) != principal.sessionID {
			return idempotency.ErrForbidden
		}
		return nil
	}
}

func (s *Store) readDecisionSnapshot(ctx context.Context, tx idempotency.ReadTransaction, actor decisionPrincipal, approvalID string) (decisionSnapshot, error) {
	var snapshot decisionSnapshot
	if s == nil || s.now == nil || ctx == nil || tx == nil || actor.subjectID == 0 || actor.sessionID == "" || !validApprovalOpaqueID(approvalID) {
		return snapshot, ErrUnavailable
	}
	if err := s.checkEpochReader(ctx, tx); err != nil {
		return snapshot, ErrUnavailable
	}
	var authRaw string
	if err := tx.QueryRowContext(ctx, decisionSessionSQL, []byte(actor.sessionID), actor.subjectID).
		Scan(&snapshot.sessionIdentity, &authRaw); errors.Is(err, sql.ErrNoRows) {
		return decisionSnapshot{}, errDecisionRejected
	} else if err != nil {
		return decisionSnapshot{}, ErrUnavailable
	}
	var err error
	snapshot.authenticated, err = time.Parse("2006-01-02 15:04:05.000000", authRaw)
	if err != nil {
		return decisionSnapshot{}, ErrUnavailable
	}
	var expiresRaw string
	if err := tx.QueryRowContext(ctx, decisionApprovalSQL, []byte(approvalID)).Scan(&snapshot.approvalUser, &snapshot.approvalIdentity,
		&snapshot.tokenVersion, &snapshot.identityVersion, &snapshot.comparisonCode, &snapshot.state, &expiresRaw, &snapshot.decisionSession); errors.Is(err, sql.ErrNoRows) {
		return decisionSnapshot{}, errDecisionRejected
	} else if err != nil {
		return decisionSnapshot{}, ErrUnavailable
	}
	snapshot.expiresAt, err = time.Parse("2006-01-02 15:04:05.000000", expiresRaw)
	if err != nil {
		return decisionSnapshot{}, ErrUnavailable
	}
	if err := tx.QueryRowContext(ctx, decisionUserSQL, snapshot.approvalUser).
		Scan(&snapshot.userStatus, &snapshot.currentToken, &snapshot.currentIdentity); err != nil {
		return decisionSnapshot{}, ErrUnavailable
	}
	return snapshot, nil
}

func (snapshot decisionSnapshot) validFor(actor decisionPrincipal, now time.Time) bool {
	if actor.subjectID == 0 || actor.sessionID == "" || now.IsZero() || snapshot.approvalUser != actor.subjectID ||
		!bytes.Equal(snapshot.sessionIdentity, snapshot.approvalIdentity) || snapshot.userStatus != "active" ||
		snapshot.tokenVersion == 0 || snapshot.identityVersion == 0 || snapshot.tokenVersion != snapshot.currentToken ||
		snapshot.identityVersion != snapshot.currentIdentity || !validComparisonCode(snapshot.comparisonCode) ||
		!snapshot.expiresAt.After(now) || snapshot.authenticated.After(now) {
		return false
	}
	if snapshot.state == "pending" {
		return len(snapshot.decisionSession) == 0
	}
	return (snapshot.state == "approved" || snapshot.state == "denied") && string(snapshot.decisionSession) == actor.sessionID
}

func (s *Store) decideForActor(ctx context.Context, tx idempotency.Transaction, actor decisionPrincipal, approvalID string, decision Decision, code string, confirmed bool) error {
	if s == nil || s.now == nil || ctx == nil || tx == nil || actor.subjectID == 0 || actor.sessionID == "" ||
		!validApprovalOpaqueID(approvalID) || (decision != Approve && decision != Deny) ||
		(decision == Approve && (!confirmed || !validComparisonInput(code))) || (decision == Deny && (confirmed || code != "")) {
		return ErrUnavailable
	}
	snapshot, err := s.readDecisionSnapshot(ctx, tx, actor, approvalID)
	if err != nil {
		if errors.Is(err, errDecisionRejected) {
			return errDecisionRejected
		}
		return ErrUnavailable
	}
	now := s.now().UTC().Truncate(time.Microsecond)
	if !snapshot.validFor(actor, now) || snapshot.state != "pending" {
		return errDecisionRejected
	}
	state := "denied"
	if decision == Approve {
		if !snapshot.authenticated.Add(s.policy.ReauthWindow).After(now) || subtle.ConstantTimeCompare([]byte(snapshot.comparisonCode), []byte(code)) != 1 {
			return errDecisionRejected
		}
		state = "approved"
	}
	updated, err := tx.ExecContext(ctx, decisionUpdateSQL, state, []byte(actor.sessionID), timestamp(now), []byte(approvalID))
	if err != nil {
		return ErrUnavailable
	}
	if count, err := updated.RowsAffected(); err != nil || count != 1 {
		return ErrUnavailable
	}
	return nil
}
