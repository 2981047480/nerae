package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, logger); err != nil {
		// The error is deliberately not logged: startup and transport errors can
		// contain DSN, SMTP, database, or challenge details.
		logger.Error("email worker stopped")
		os.Exit(1)
	}
}
