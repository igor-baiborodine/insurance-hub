package main

import (
	"context"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/igor-baiborodine/insurance-hub/templates/go-service/internal/config"
	"github.com/igor-baiborodine/insurance-hub/templates/go-service/internal/logger"
	"github.com/igor-baiborodine/insurance-hub/templates/go-service/internal/service"
)

func main() {
	ctx, stop := shutdownContext(context.Background())
	defer stop()

	if err := run(ctx, os.Stderr); err != nil {
		os.Exit(1)
	}
}

func shutdownContext(parent context.Context) (context.Context, context.CancelFunc) {
	return signal.NotifyContext(parent, os.Interrupt, syscall.SIGTERM)
}

func run(ctx context.Context, output io.Writer) error {
	settings, err := config.Load()
	if err != nil {
		slog.New(slog.NewJSONHandler(output, nil)).Error(
			"configuration failed",
			slog.Any("error", err),
		)
		return err
	}
	serviceLogger := logger.New(output, settings.ServiceName, settings.LogLevel)
	if err := service.Run(ctx, settings, serviceLogger); err != nil {
		serviceLogger.ErrorContext(context.Background(), "service failed", slog.Any("error", err))
		return err
	}
	return nil
}
