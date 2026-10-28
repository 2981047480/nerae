package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"nerae/api/internal/config"
)

// No jobs are registered yet: this process does not consume queues until a
// later task wires real handlers and delivery semantics.
func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	if _, err := config.Load(config.Worker); err != nil {
		logger.Error("configuration invalid", "reason", err.Error())
		os.Exit(1)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	logger.Info("worker process started without registered jobs")
	<-ctx.Done()
	logger.Info("worker process stopped")
}
