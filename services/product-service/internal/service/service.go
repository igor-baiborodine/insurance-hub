// Package service owns Product process construction and lifecycle.
package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/igor-baiborodine/insurance-hub/services/product-service/internal/config"
	"github.com/igor-baiborodine/insurance-hub/services/product-service/internal/health"
	"github.com/igor-baiborodine/insurance-hub/services/product-service/internal/telemetry"
)

const (
	managementReadHeaderTimeout = 5 * time.Second
	managementIdleTimeout       = 30 * time.Second
)

// Run starts the management-only shell. Readiness stays false until Product readers and
// business listeners are implemented and wired in later delivery steps.
func Run(ctx context.Context, settings config.Config, logger *slog.Logger) error {
	return run(ctx, settings, logger, dependencies{
		listen:       net.Listen,
		newTelemetry: telemetry.New,
	})
}

type telemetryProvider interface {
	Shutdown(context.Context) error
}

type dependencies struct {
	listen       func(string, string) (net.Listener, error)
	newTelemetry func(context.Context, string, config.Telemetry) (*telemetry.Provider, error)
}

func run(
	ctx context.Context,
	settings config.Config,
	logger *slog.Logger,
	deps dependencies,
) error {
	if logger == nil {
		return errors.New("run service: logger is required")
	}
	if settings.ShutdownTimeout <= 0 {
		return errors.New("run service: shutdown timeout must be positive")
	}

	provider, err := deps.newTelemetry(ctx, settings.ServiceName, settings.Telemetry)
	if err != nil {
		return fmt.Errorf("run service: %w", err)
	}

	listener, err := deps.listen("tcp", settings.HealthAddress.String())
	if err != nil {
		return errors.Join(
			fmt.Errorf("run service: listen for management HTTP: %w", err),
			shutdownTelemetry(provider, settings.ShutdownTimeout),
		)
	}

	state := new(health.State)
	server := &http.Server{
		Handler:           health.NewHandler(state),
		ReadHeaderTimeout: managementReadHeaderTimeout,
		IdleTimeout:       managementIdleTimeout,
	}
	serveResult := make(chan error, 1)
	go func() { serveResult <- server.Serve(listener) }()
	logger.InfoContext(ctx, "product service shell started",
		slog.String("health_address", listener.Addr().String()),
	)

	var serveErr error
	select {
	case <-ctx.Done():
	case err := <-serveResult:
		serveErr = fmt.Errorf("serve management HTTP: %w", err)
	}

	state.SetReady(false)
	shutdownCtx, cancel := context.WithTimeout(context.Background(), settings.ShutdownTimeout)
	defer cancel()
	shutdownErr := server.Shutdown(shutdownCtx)
	if shutdownErr != nil {
		_ = server.Close()
		shutdownErr = fmt.Errorf("shutdown management HTTP: %w", shutdownErr)
	}
	return errors.Join(serveErr, shutdownErr, provider.Shutdown(shutdownCtx))
}

func shutdownTelemetry(provider telemetryProvider, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	if err := provider.Shutdown(ctx); err != nil {
		return fmt.Errorf("shutdown telemetry after startup failure: %w", err)
	}
	return nil
}
