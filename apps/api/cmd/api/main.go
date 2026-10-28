package main

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"nerae/api/internal/config"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	cfg, err := config.Load(config.API)
	if err != nil {
		// Config errors contain setting names only, never their values.
		logger.Error("configuration invalid", "reason", err.Error())
		os.Exit(1)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, cfg, logger); err != nil && !errors.Is(err, context.Canceled) {
		logger.Error("api stopped", "reason", err.Error())
		os.Exit(1)
	}
}
