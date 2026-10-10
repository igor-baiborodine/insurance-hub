package main

import (
	"context"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/igor-baiborodine/insurance-hub/services/product-service/internal/config"
	"github.com/igor-baiborodine/insurance-hub/services/product-service/internal/logger"
	"github.com/igor-baiborodine/insurance-hub/services/product-service/internal/service"
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
	return runWith(ctx, output, config.Load, service.Run)
}

func runWith(
	ctx context.Context,
	output io.Writer,
	loadConfig func() (config.Config, error),
	runService func(context.Context, config.Config, *slog.Logger) error,
) error {
	bootstrapLogger := logger.New(output, config.DefaultServiceName, slog.LevelInfo)
	settings, err := loadConfig()
	if err != nil {
		bootstrapLogger.ErrorContext(
			context.Background(),
			"configuration failed",
			slog.Any("error", err),
		)
		return err
	}
	serviceLogger := logger.New(output, settings.ServiceName, settings.LogLevel)
	if err := runService(ctx, settings, serviceLogger); err != nil {
		serviceLogger.ErrorContext(
			context.Background(),
			"service failed",
			slog.Any("error", err),
		)
		return err
	}
	return nil
}
