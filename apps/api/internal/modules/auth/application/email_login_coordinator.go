package application

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"net/netip"

	"nerae/api/internal/modules/auth/challenge"
	"nerae/api/internal/modules/auth/ratelimit"
)

// EmailLoginCoordinator sequences pre-database entry limiting, challenge-key
// limiting, and the existing receipt transaction. It is not an HTTP handler.
type EmailLoginCoordinator struct {
	challenges *challenge.Store
	limiter    *ratelimit.Limiter
	receipt    *ReceiptLogin
}

func (EmailLoginCoordinator) String() string               { return "EmailLoginCoordinator{redacted}" }
func (EmailLoginCoordinator) GoString() string             { return "EmailLoginCoordinator{redacted}" }
func (EmailLoginCoordinator) MarshalJSON() ([]byte, error) { return nil, ErrUnavailable }
func (EmailLoginCoordinator) LogValue() slog.Value {
	return slog.StringValue("EmailLoginCoordinator{redacted}")
}

// NewEmailLoginCoordinator requires the challenge store used by ReceiptLogin
// itself, so preflight metadata and the authoritative login transaction cannot
// accidentally be assembled from different challenge stores.
func NewEmailLoginCoordinator(challenges *challenge.Store, limiter *ratelimit.Limiter, receipt *ReceiptLogin) (*EmailLoginCoordinator, error) {
	if challenges == nil || limiter == nil || receipt == nil || receipt.login == nil || receipt.login.challenges != challenges {
		return nil, ErrUnavailable
	}
	return &EmailLoginCoordinator{challenges: challenges, limiter: limiter, receipt: receipt}, nil
}

// VerifyEmail first spends the IP/global budget before touching MySQL. It then
// reads opaque challenge limiter material in a short transaction, ends that
// transaction before Redis access, and spends email/device budget only for a
// known challenge. Finally it delegates the authoritative operation to
// ReceiptLogin in a separate transaction. Credentials are returned only after
// Commit succeeds and ReceiptLogin performs its post-commit revalidation.
func (c *EmailLoginCoordinator) VerifyEmail(ctx context.Context, db *sql.DB, idempotencyKey string, input challenge.AttemptInput, trustedSourceIP netip.Addr) (Outcome, error) {
	if c == nil || c.challenges == nil || c.limiter == nil || c.receipt == nil || ctx == nil {
		return Outcome{}, ErrUnavailable
	}
	if err := c.limiter.AllowLoginEntry(ctx, trustedSourceIP); err != nil {
		if errors.Is(err, ratelimit.ErrLimited) {
			return Outcome{}, ratelimit.ErrLimited
		}
		if errors.Is(err, ratelimit.ErrUnavailable) {
			return Outcome{}, errors.Join(ErrUnavailable, ratelimit.ErrUnavailable)
		}
		return Outcome{}, ErrUnavailable
	}
	if db == nil {
		return Outcome{}, ErrUnavailable
	}

	readTx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return Outcome{}, ErrUnavailable
	}
	metadata, found, err := c.challenges.RateLimitDetails(ctx, readTx, input.ChallengeID)
	if err != nil {
		_ = readTx.Rollback()
		return Outcome{}, ErrUnavailable
	}
	if err := readTx.Commit(); err != nil {
		return Outcome{}, ErrUnavailable
	}
	if !found {
		return Outcome{}, ErrRejected
	}
	if err := metadata.ApplyLoginDimensionLimit(ctx, c.limiter); err != nil {
		if errors.Is(err, ratelimit.ErrLimited) {
			return Outcome{}, ratelimit.ErrLimited
		}
		if errors.Is(err, ratelimit.ErrUnavailable) {
			return Outcome{}, errors.Join(ErrUnavailable, ratelimit.ErrUnavailable)
		}
		return Outcome{}, ErrUnavailable
	}

	loginTx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return Outcome{}, ErrUnavailable
	}
	result, err := c.receipt.VerifyInTransaction(ctx, loginTx, idempotencyKey, input)
	if err != nil {
		_ = loginTx.Rollback()
		if errors.Is(err, ErrReceiptConflict) || errors.Is(err, ErrRejected) {
			return Outcome{}, err
		}
		return Outcome{}, ErrUnavailable
	}
	if !result.CommitRequired() {
		_ = loginTx.Rollback()
		if result.Kind() == VerificationRejected {
			return Outcome{}, ErrRejected
		}
		return Outcome{}, ErrUnavailable
	}
	if err := loginTx.Commit(); err != nil {
		// Commit may have an unknown outcome. Never call AfterCommit or release
		// a token/approval capability unless Commit returned nil.
		return Outcome{}, ErrUnavailable
	}
	outcome, err := result.AfterCommit(ctx, db)
	if errors.Is(err, ErrRejected) || errors.Is(err, ErrUnavailable) {
		return Outcome{}, err
	}
	if err != nil {
		return Outcome{}, ErrUnavailable
	}
	return outcome, nil
}
