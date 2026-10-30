// Package mailworker provides an explicitly assembled, bounded email outbox
// execution loop. It does not create keys or start itself as a process.
package mailworker

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"time"

	"nerae/api/internal/modules/auth/issuance"
)

var (
	ErrConfiguration = errors.New("email worker configuration unavailable")
	ErrDatabase      = errors.New("email worker database operation failed")
)

type Outbox interface {
	RecoverExpiredSending(context.Context, *sql.DB, int) (int64, error)
	SendOne(context.Context, *sql.DB) (issuance.DeliveryState, error)
}

type Config struct {
	Outbox        Outbox
	DB            *sql.DB
	PollInterval  time.Duration
	RecoveryBatch int
	Logger        *slog.Logger
}

type Worker struct {
	outbox   Outbox
	db       *sql.DB
	interval time.Duration
	batch    int
	logger   *slog.Logger
}

func New(cfg Config) (*Worker, error) {
	if cfg.Outbox == nil || cfg.DB == nil || cfg.PollInterval < 100*time.Millisecond || cfg.PollInterval > time.Minute || cfg.RecoveryBatch < 1 || cfg.RecoveryBatch > 500 {
		return nil, ErrConfiguration
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(discardWriter{}, nil))
	}
	return &Worker{outbox: cfg.Outbox, db: cfg.DB, interval: cfg.PollInterval, batch: cfg.RecoveryBatch, logger: logger}, nil
}

// Run executes until cancellation. It never logs error text or delivery data.
func (w *Worker) Run(ctx context.Context) error {
	if !w.valid() || ctx == nil {
		return ErrConfiguration
	}
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return nil
		}
		if err := w.RunOnce(ctx); err != nil {
			if ctx.Err() != nil {
				return nil
			}
			w.logger.Error("email worker iteration failed", "class", "database_or_outbox")
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

// RunOnce recovers expired in-flight sends before claiming at most one new row.
func (w *Worker) RunOnce(ctx context.Context) error {
	if !w.valid() || ctx == nil {
		return ErrConfiguration
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if _, err := w.outbox.RecoverExpiredSending(ctx, w.db, w.batch); err != nil {
		return ErrDatabase
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	state, err := w.outbox.SendOne(ctx, w.db)
	if errors.Is(err, issuance.ErrNoPending) {
		return nil
	}
	if err != nil {
		return ErrDatabase
	}
	switch state {
	case issuance.DeliveryAccepted, issuance.DeliveryFailed, issuance.DeliveryUnknown, issuance.DeliverySuperseded, issuance.DeliveryExpired:
		return nil
	default:
		return ErrDatabase
	}
}

func (w *Worker) valid() bool {
	return w != nil && w.outbox != nil && w.db != nil && w.logger != nil &&
		w.interval >= 100*time.Millisecond && w.interval <= time.Minute && w.batch >= 1 && w.batch <= 500
}

type discardWriter struct{}

func (discardWriter) Write(p []byte) (int, error) { return len(p), nil }
