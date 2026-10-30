package application

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"time"

	"nerae/api/internal/modules/auth/challenge"
	"nerae/api/internal/modules/auth/loginledger"
	"nerae/api/internal/modules/auth/loginreceipt"
	"nerae/api/internal/modules/auth/session"
)

var (
	ErrReceiptConflict = errors.New("email login receipt conflicts")
	// Existing receipts are deliberately not opened until the replay path has
	// independently rechecked proof, authorization, expiry and commit state.
	ErrReceiptReplayUnavailable = errors.New("email login receipt replay unavailable")
)

// ReceiptLogin coordinates an attempt and a trusted-device receipt in one
// caller-owned transaction. It is not an HTTP endpoint or rate limiter.
type ReceiptLogin struct {
	login     *EmailLogin
	ledger    *loginledger.Store
	crypto    *loginreceipt.Crypto
	sessions  *session.ReceiptRecoverer
	retention time.Duration
	now       func() time.Time
}

func (ReceiptLogin) String() string               { return "ReceiptLogin{redacted}" }
func (ReceiptLogin) GoString() string             { return "ReceiptLogin{redacted}" }
func (ReceiptLogin) MarshalJSON() ([]byte, error) { return nil, ErrUnavailable }
func (ReceiptLogin) LogValue() slog.Value         { return slog.StringValue("ReceiptLogin{redacted}") }

func NewReceiptLogin(login *EmailLogin, ledger *loginledger.Store, crypto *loginreceipt.Crypto, retention time.Duration) (*ReceiptLogin, error) {
	if login == nil || login.challenges == nil || login.sessions == nil || login.approvals == nil || ledger == nil || crypto == nil ||
		retention < time.Second || retention > 24*time.Hour || retention%time.Microsecond != 0 {
		return nil, ErrUnavailable
	}
	recoverer, err := session.NewReceiptRecoverer(login.sessions, ledger, login.challenges, crypto)
	if err != nil {
		return nil, ErrUnavailable
	}
	return &ReceiptLogin{login: login, ledger: ledger, crypto: crypto, sessions: recoverer, retention: retention, now: time.Now}, nil
}

// ReceiptTransactionResult reports whether the caller must commit. It never
// exposes credentials in the live transaction. On any error or when
// CommitRequired is false, the caller must roll back the tx.
type ReceiptTransactionResult struct {
	kind           VerificationKind
	commitRequired bool
	session        *session.ReceiptCandidate
}

func (ReceiptTransactionResult) String() string               { return "ReceiptLoginResult{redacted}" }
func (ReceiptTransactionResult) GoString() string             { return "ReceiptLoginResult{redacted}" }
func (ReceiptTransactionResult) MarshalJSON() ([]byte, error) { return nil, ErrUnavailable }
func (ReceiptTransactionResult) LogValue() slog.Value {
	return slog.StringValue("ReceiptLoginResult{redacted}")
}
func (r ReceiptTransactionResult) Kind() VerificationKind { return r.kind }
func (r ReceiptTransactionResult) CommitRequired() bool   { return r.commitRequired }

// AfterCommit returns a trusted-device token only after the session candidate
// re-reads the committed receipt and revalidates proof and authorization in a
// fresh transaction. Approval and rejection replay remain unavailable.
func (r ReceiptTransactionResult) AfterCommit(ctx context.Context, db *sql.DB) (Outcome, error) {
	if r.kind != VerificationAuthenticated || !r.commitRequired || r.session == nil {
		return Outcome{}, ErrUnavailable
	}
	token, err := r.session.AfterCommit(ctx, db)
	if errors.Is(err, session.ErrInvalidSession) {
		return Outcome{}, ErrRejected
	}
	if err != nil || token == "" {
		return Outcome{}, ErrUnavailable
	}
	return Outcome{kind: Authenticated, token: token}, nil
}

// VerifyInTransaction derives both the binding and device attempt from the
// exact same input. It serializes on the protected email slot and writes any
// changed attempt, session/approval, and encrypted receipt together. A caller
// must commit or roll back all of them; this method neither commits nor returns
// a credential. Existing session receipts can be recovered after commit;
// approval and rejection receipts still fail closed.
func (s *ReceiptLogin) VerifyInTransaction(ctx context.Context, tx *sql.Tx, key string, input challenge.AttemptInput) (ReceiptTransactionResult, error) {
	if s == nil || s.login == nil || s.ledger == nil || s.crypto == nil || s.sessions == nil || s.now == nil || ctx == nil || tx == nil ||
		s.retention < time.Second || s.retention > 24*time.Hour || s.retention%time.Microsecond != 0 {
		return ReceiptTransactionResult{}, ErrUnavailable
	}
	request := loginreceipt.Request{IdempotencyKey: key, ChallengeID: input.ChallengeID, Code: input.Code, DeviceProof: input.DeviceProof}
	binding, err := s.crypto.Summarize(request)
	if err != nil {
		return ReceiptTransactionResult{}, ErrUnavailable
	}
	scope, found, err := s.login.challenges.ReceiptScope(ctx, tx, input.ChallengeID)
	if err != nil {
		return ReceiptTransactionResult{}, ErrUnavailable
	}
	if !found {
		return ReceiptTransactionResult{kind: VerificationRejected}, nil
	}
	handle, err := s.ledger.Lock(ctx, tx, scope, binding)
	if errors.Is(err, loginledger.ErrConflict) {
		return ReceiptTransactionResult{}, ErrReceiptConflict
	}
	if err != nil {
		return ReceiptTransactionResult{}, ErrUnavailable
	}
	if record, exists := handle.ExistingRecord(); exists {
		if record.ResultType() != loginreceipt.ResultAuthenticated {
			return ReceiptTransactionResult{}, ErrReceiptReplayUnavailable
		}
		proof, err := s.login.challenges.VerifyReceiptProof(ctx, tx, scope, input.ChallengeID, input.DeviceProof)
		if errors.Is(err, challenge.ErrReceiptProofRejected) {
			return ReceiptTransactionResult{}, ErrRejected
		}
		if err != nil {
			return ReceiptTransactionResult{}, ErrUnavailable
		}
		candidate, err := s.sessions.Recover(ctx, tx, handle, proof, request)
		if errors.Is(err, session.ErrInvalidSession) {
			return ReceiptTransactionResult{}, ErrRejected
		}
		if err != nil {
			return ReceiptTransactionResult{}, ErrUnavailable
		}
		return ReceiptTransactionResult{kind: VerificationAuthenticated, commitRequired: true, session: &candidate}, nil
	}
	result, err := s.login.VerifyEmailInTransaction(ctx, tx, input)
	if err != nil {
		return ReceiptTransactionResult{}, ErrUnavailable
	}
	if !result.CommitRequired() {
		if result.Kind() != VerificationRejected {
			return ReceiptTransactionResult{}, ErrUnavailable
		}
		return ReceiptTransactionResult{kind: result.Kind()}, nil
	}
	now := s.now().UTC()
	if now.IsZero() {
		return ReceiptTransactionResult{}, ErrUnavailable
	}
	expires := now.Add(s.retention).Truncate(time.Microsecond)
	var kind loginreceipt.ResultType
	var reference string
	var sealed loginreceipt.Sealed
	switch result.Kind() {
	case VerificationRejected:
		kind, reference = loginreceipt.ResultRejected, input.ChallengeID
		aad, contextErr := s.crypto.NewContext(scope[:], binding, kind, reference, expires)
		if contextErr != nil {
			return ReceiptTransactionResult{}, ErrUnavailable
		}
		sealed, err = s.crypto.Seal(aad, nil)
	case VerificationAuthenticated:
		if result.proof == nil {
			return ReceiptTransactionResult{}, ErrUnavailable
		}
		issuedReceipt, sealErr := result.issued.SealForReceipt(ctx, tx, result.proof, s.crypto, binding, expires)
		err = sealErr
		if err == nil {
			kind, reference, sealed, expires = loginreceipt.ResultAuthenticated, issuedReceipt.Reference(), issuedReceipt.Ciphertext(), issuedReceipt.ExpiresAt()
		}
	case VerificationApprovalRequired:
		if result.proof == nil {
			return ReceiptTransactionResult{}, ErrUnavailable
		}
		approvalReceipt, sealErr := result.pending.SealForReceipt(ctx, tx, result.proof, s.crypto, binding, expires)
		err = sealErr
		if err == nil {
			kind, reference, sealed, expires = loginreceipt.ResultApprovalRequired, approvalReceipt.Reference(), approvalReceipt.Ciphertext(), approvalReceipt.ExpiresAt()
		}
	default:
		return ReceiptTransactionResult{}, ErrUnavailable
	}
	if err != nil || s.ledger.Put(ctx, tx, handle, kind, reference, expires, sealed) != nil {
		return ReceiptTransactionResult{}, ErrUnavailable
	}
	// A marker write or receipt insertion can wait long enough for a signed
	// proof, challenge, or receipt to expire. Roll the entire transaction back
	// if any of them is no longer live after all writes and locks.
	checkedProof, err := s.login.challenges.VerifyReceiptProof(ctx, tx, scope, input.ChallengeID, input.DeviceProof)
	if err != nil {
		return ReceiptTransactionResult{}, ErrUnavailable
	}
	if _, err := checkedProof.ForTransaction(ctx, tx); err != nil {
		return ReceiptTransactionResult{}, ErrUnavailable
	}
	if result.proof != nil {
		if _, err := result.proof.ForTransaction(ctx, tx); err != nil {
			return ReceiptTransactionResult{}, ErrUnavailable
		}
	}
	checkedHandle, err := s.ledger.Lock(ctx, tx, scope, binding)
	if err != nil {
		return ReceiptTransactionResult{}, ErrUnavailable
	}
	record, exists := checkedHandle.ExistingRecord()
	if !exists || record.ResultType() != kind || record.ResultReference() != reference || !record.ExpiresAt().Equal(expires) {
		return ReceiptTransactionResult{}, ErrUnavailable
	}
	if _, err := checkedProof.ForTransaction(ctx, tx); err != nil {
		return ReceiptTransactionResult{}, ErrUnavailable
	}
	if result.proof != nil {
		if _, err := result.proof.ForTransaction(ctx, tx); err != nil {
			return ReceiptTransactionResult{}, ErrUnavailable
		}
	}
	if finished := s.now().UTC(); finished.IsZero() || !expires.After(finished) {
		return ReceiptTransactionResult{}, ErrUnavailable
	}
	if result.Kind() == VerificationAuthenticated {
		candidate, err := s.sessions.Recover(ctx, tx, checkedHandle, checkedProof, request)
		if err != nil {
			return ReceiptTransactionResult{}, ErrUnavailable
		}
		return ReceiptTransactionResult{kind: result.Kind(), commitRequired: true, session: &candidate}, nil
	}
	return ReceiptTransactionResult{kind: result.Kind(), commitRequired: true}, nil
}
