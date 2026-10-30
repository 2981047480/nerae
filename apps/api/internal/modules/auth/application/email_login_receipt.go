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
)

var (
	ErrReceiptConflict = errors.New("email login receipt conflicts")
	// Existing receipts are deliberately not opened until the replay path has
	// independently rechecked proof, authorization, expiry and commit state.
	ErrReceiptReplayUnavailable = errors.New("email login receipt replay unavailable")
)

// ReceiptLogin coordinates the first attempt and its encrypted receipt in one
// caller-owned transaction. It is not an HTTP endpoint or a replay service.
type ReceiptLogin struct {
	login     *EmailLogin
	ledger    *loginledger.Store
	crypto    *loginreceipt.Crypto
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
	return &ReceiptLogin{login: login, ledger: ledger, crypto: crypto, retention: retention, now: time.Now}, nil
}

// ReceiptTransactionResult reports whether the caller must commit. It never
// exposes credentials: safe post-commit replay is not yet implemented. On any
// error or when CommitRequired is false, the caller must roll back the tx.
type ReceiptTransactionResult struct {
	kind           VerificationKind
	commitRequired bool
}

func (ReceiptTransactionResult) String() string               { return "ReceiptLoginResult{redacted}" }
func (ReceiptTransactionResult) GoString() string             { return "ReceiptLoginResult{redacted}" }
func (ReceiptTransactionResult) MarshalJSON() ([]byte, error) { return nil, ErrUnavailable }
func (ReceiptTransactionResult) LogValue() slog.Value {
	return slog.StringValue("ReceiptLoginResult{redacted}")
}
func (r ReceiptTransactionResult) Kind() VerificationKind { return r.kind }
func (r ReceiptTransactionResult) CommitRequired() bool   { return r.commitRequired }

// VerifyInTransaction derives both the binding and device attempt from the
// exact same input. It serializes on the protected email slot and writes any
// changed attempt, session/approval, and encrypted receipt together. A caller
// must commit or roll back all of them; this method neither commits nor returns
// a credential. Existing receipts fail closed until replay is implemented.
func (s *ReceiptLogin) VerifyInTransaction(ctx context.Context, tx *sql.Tx, key string, input challenge.AttemptInput) (ReceiptTransactionResult, error) {
	if s == nil || s.login == nil || s.ledger == nil || s.crypto == nil || s.now == nil || ctx == nil || tx == nil ||
		s.retention < time.Second || s.retention > 24*time.Hour || s.retention%time.Microsecond != 0 {
		return ReceiptTransactionResult{}, ErrUnavailable
	}
	binding, err := s.crypto.Summarize(loginreceipt.Request{IdempotencyKey: key, ChallengeID: input.ChallengeID,
		Code: input.Code, DeviceProof: input.DeviceProof})
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
	if _, exists := handle.ExistingRecord(); exists {
		return ReceiptTransactionResult{}, ErrReceiptReplayUnavailable
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
	return ReceiptTransactionResult{kind: result.Kind(), commitRequired: true}, nil
}
