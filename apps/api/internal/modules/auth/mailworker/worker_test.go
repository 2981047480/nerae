package mailworker

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"nerae/api/internal/modules/auth/issuance"
)

type fakeOutbox struct {
	recovered, sent     int
	state               issuance.DeliveryState
	recoverErr, sendErr error
	order               []string
	sendStarted         chan struct{}
	sendOnce            sync.Once
}

func (f *fakeOutbox) RecoverExpiredSending(context.Context, *sql.DB, int) (int64, error) {
	f.order = append(f.order, "recover")
	f.recovered++
	return 0, f.recoverErr
}
func (f *fakeOutbox) SendOne(context.Context, *sql.DB) (issuance.DeliveryState, error) {
	f.order = append(f.order, "send")
	f.sent++
	if f.sendStarted != nil {
		f.sendOnce.Do(func() { close(f.sendStarted) })
	}
	return f.state, f.sendErr
}

func workerForTest(t *testing.T, outbox *fakeOutbox, logger *slog.Logger) *Worker {
	t.Helper()
	w, err := New(Config{Outbox: outbox, DB: &sql.DB{}, PollInterval: 100 * time.Millisecond, RecoveryBatch: 5, Logger: logger})
	if err != nil {
		t.Fatal("worker configuration rejected")
	}
	return w
}

func TestRunOnceRecoversThenSendsAtMostOne(t *testing.T) {
	f := &fakeOutbox{state: issuance.DeliveryAccepted}
	if err := workerForTest(t, f, nil).RunOnce(context.Background()); err != nil {
		t.Fatal("iteration failed")
	}
	if f.recovered != 1 || f.sent != 1 || strings.Join(f.order, ",") != "recover,send" {
		t.Fatalf("unexpected sequence: %#v", f.order)
	}
}

func TestRunOnceIdleAndUnknownAreNotRetried(t *testing.T) {
	for _, tc := range []struct {
		name  string
		state issuance.DeliveryState
		err   error
	}{
		{name: "idle", err: issuance.ErrNoPending},
		{name: "unknown", state: issuance.DeliveryUnknown},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &fakeOutbox{state: tc.state, sendErr: tc.err}
			if err := workerForTest(t, f, nil).RunOnce(context.Background()); err != nil {
				t.Fatal("iteration failed")
			}
			if f.sent != 1 {
				t.Fatal("expected one bounded SendOne call")
			}
		})
	}
}

func TestRunOnceFailsClosedAndRedactsErrors(t *testing.T) {
	secret := "smtp-password-and-dsn-secret"
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	t.Run("recovery failure prevents sending", func(t *testing.T) {
		f := &fakeOutbox{recoverErr: errors.New(secret)}
		err := workerForTest(t, f, logger).RunOnce(context.Background())
		if !errors.Is(err, ErrDatabase) || f.sent != 0 {
			t.Fatal("recovery failure did not fail closed")
		}
	})
	t.Run("send failure sanitized", func(t *testing.T) {
		f := &fakeOutbox{sendErr: errors.New(secret)}
		err := workerForTest(t, f, logger).RunOnce(context.Background())
		if !errors.Is(err, ErrDatabase) {
			t.Fatal("send failure classification lost")
		}
		if strings.Contains(err.Error(), secret) || strings.Contains(logs.String(), secret) {
			t.Fatal("underlying error leaked")
		}
	})
}

func TestRunStopsOnCancellationAndDoesNotReportDelivery(t *testing.T) {
	f := &fakeOutbox{state: issuance.DeliveryAccepted}
	w := workerForTest(t, f, nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := w.Run(ctx); err != nil {
		t.Fatal("cancelled worker returned an error")
	}
	if f.recovered != 0 || f.sent != 0 {
		t.Fatal("cancelled worker touched queue")
	}
}

func TestRunStopsAfterCancellationDuringIdleWait(t *testing.T) {
	f := &fakeOutbox{sendErr: issuance.ErrNoPending, sendStarted: make(chan struct{})}
	w := workerForTest(t, f, nil)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- w.Run(ctx) }()
	<-f.sendStarted
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal("cancelled worker returned an error")
		}
	case <-time.After(time.Second):
		t.Fatal("worker did not stop after cancellation")
	}
	if f.sent != 1 {
		t.Fatal("worker started another send after cancellation")
	}
}

func TestNewRequiresExplicitConfiguration(t *testing.T) {
	if _, err := New(Config{}); !errors.Is(err, ErrConfiguration) {
		t.Fatal("empty configuration enabled worker")
	}
}

func TestZeroValueWorkerFailsClosed(t *testing.T) {
	worker := &Worker{}
	if err := worker.Run(context.Background()); !errors.Is(err, ErrConfiguration) {
		t.Fatal("zero value Run did not return configuration error")
	}
	if err := worker.RunOnce(context.Background()); !errors.Is(err, ErrConfiguration) {
		t.Fatal("zero value RunOnce did not return configuration error")
	}
}
