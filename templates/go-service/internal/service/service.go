// Package service owns construction, startup, serving, and bounded shutdown.
package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"

	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
	grpcgo "google.golang.org/grpc"

	"github.com/igor-baiborodine/insurance-hub/templates/go-service/internal/config"
	transport "github.com/igor-baiborodine/insurance-hub/templates/go-service/internal/grpc"
	"github.com/igor-baiborodine/insurance-hub/templates/go-service/internal/health"
	"github.com/igor-baiborodine/insurance-hub/templates/go-service/internal/telemetry"
)

const (
	managementReadHeaderTimeout = 5 * time.Second
	managementIdleTimeout       = 30 * time.Second
)

// Run starts the configured service and blocks until cancellation or an unexpected serving error.
func Run(ctx context.Context, settings config.Config, logger *slog.Logger) error {
	return run(ctx, settings, logger, productionDependencies())
}

type telemetryProvider interface {
	TracerProvider() trace.TracerProvider
	Propagator() propagation.TextMapPropagator
	Shutdown(context.Context) error
}

type grpcServer interface {
	Serve(net.Listener) error
	GracefulStop()
	Stop()
}

type managementServer interface {
	Serve(net.Listener) error
	Shutdown(context.Context) error
	Close() error
}

type dependencies struct {
	listen        func(string, string) (net.Listener, error)
	newTelemetry  func(context.Context, string, config.Telemetry) (telemetryProvider, error)
	newGRPCServer func(
		*slog.Logger,
		trace.TracerProvider,
		propagation.TextMapPropagator,
		transport.Echo,
	) (grpcServer, error)
	newManagement func(*health.State) managementServer
	echo          transport.Echo
	after         func(time.Duration) <-chan time.Time
}

type runningService struct {
	state              *health.State
	grpcServer         grpcServer
	grpcListener       net.Listener
	managementServer   managementServer
	managementListener net.Listener
	telemetry          telemetryProvider
	after              func(time.Duration) <-chan time.Time
}

func productionDependencies() dependencies {
	return dependencies{
		listen: net.Listen,
		newTelemetry: func(
			ctx context.Context,
			serviceName string,
			settings config.Telemetry,
		) (telemetryProvider, error) {
			return telemetry.New(ctx, serviceName, settings)
		},
		newGRPCServer: func(
			logger *slog.Logger,
			tracerProvider trace.TracerProvider,
			propagator propagation.TextMapPropagator,
			echo transport.Echo,
		) (grpcServer, error) {
			return transport.NewServer(logger, tracerProvider, propagator, echo)
		},
		newManagement: func(state *health.State) managementServer {
			return &http.Server{
				Handler:           health.NewHandler(state),
				ReadHeaderTimeout: managementReadHeaderTimeout,
				IdleTimeout:       managementIdleTimeout,
			}
		},
		echo:  transport.IdentityEcho,
		after: time.After,
	}
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

	server, err := deps.newGRPCServer(
		logger,
		provider.TracerProvider(),
		provider.Propagator(),
		deps.echo,
	)
	if err != nil {
		return errors.Join(
			fmt.Errorf("run service: initialize gRPC transport: %w", err),
			shutdownTelemetry(provider, settings.ShutdownTimeout),
		)
	}

	grpcListener, err := deps.listen("tcp", settings.GRPCAddress.String())
	if err != nil {
		return errors.Join(
			fmt.Errorf("run service: listen for gRPC: %w", err),
			shutdownTelemetry(provider, settings.ShutdownTimeout),
		)
	}
	managementListener, err := deps.listen("tcp", settings.HealthAddress.String())
	if err != nil {
		return errors.Join(
			fmt.Errorf("run service: listen for management HTTP: %w", err),
			closeListener("close gRPC listener", grpcListener),
			shutdownTelemetry(provider, settings.ShutdownTimeout),
		)
	}

	state := new(health.State)
	runtime := &runningService{
		state:              state,
		grpcServer:         server,
		grpcListener:       grpcListener,
		managementServer:   deps.newManagement(state),
		managementListener: managementListener,
		telemetry:          provider,
		after:              deps.after,
	}

	grpcResult := make(chan error, 1)
	managementResult := make(chan error, 1)
	go func() {
		grpcResult <- runtime.grpcServer.Serve(runtime.grpcListener)
	}()
	go func() {
		managementResult <- runtime.managementServer.Serve(runtime.managementListener)
	}()
	runtime.state.SetReady(true)
	logger.InfoContext(ctx, "service started",
		slog.String("grpc_address", grpcListener.Addr().String()),
		slog.String("health_address", managementListener.Addr().String()),
	)

	var serveErr error
	select {
	case <-ctx.Done():
	case err := <-grpcResult:
		serveErr = unexpectedServeError("gRPC", err)
	case err := <-managementResult:
		serveErr = unexpectedServeError("management HTTP", err)
	}

	shutdownErr := runtime.shutdown(settings.ShutdownTimeout)
	return errors.Join(serveErr, shutdownErr)
}

func (runtime *runningService) shutdown(timeout time.Duration) error {
	runtime.state.SetReady(false)
	shutdownStarted := time.Now()
	overallContext, cancelOverall := context.WithTimeout(context.Background(), timeout)
	defer cancelOverall()

	gracefulResult := make(chan struct{})
	go func() {
		runtime.grpcServer.GracefulStop()
		close(gracefulResult)
	}()

	var shutdownErrors []error
	gracePeriod := timeout / 2
	select {
	case <-gracefulResult:
	case <-runtime.after(gracePeriod):
		runtime.grpcServer.Stop()
		select {
		case <-gracefulResult:
		case <-overallContext.Done():
			shutdownErrors = append(shutdownErrors,
				fmt.Errorf("stop gRPC server: %w", overallContext.Err()))
		}
	}
	if err := closeListener("close gRPC listener", runtime.grpcListener); err != nil {
		shutdownErrors = append(shutdownErrors, err)
	}

	healthDeadline := shutdownStarted.Add(timeout - timeout/4)
	if overallDeadline, ok := overallContext.Deadline(); ok &&
		healthDeadline.After(overallDeadline) {
		healthDeadline = overallDeadline
	}
	healthContext, cancelHealth := context.WithDeadline(overallContext, healthDeadline)
	if err := runtime.managementServer.Shutdown(healthContext); err != nil &&
		!errors.Is(err, http.ErrServerClosed) {
		shutdownErrors = append(
			shutdownErrors,
			fmt.Errorf("shutdown management HTTP: %w", err),
		)
		if closeErr := runtime.managementServer.Close(); closeErr != nil &&
			!errors.Is(closeErr, http.ErrServerClosed) {
			shutdownErrors = append(shutdownErrors,
				fmt.Errorf("force close management HTTP: %w", closeErr))
		}
	}
	cancelHealth()
	if err := closeListener(
		"close management listener",
		runtime.managementListener,
	); err != nil {
		shutdownErrors = append(shutdownErrors, err)
	}

	if err := runtime.telemetry.Shutdown(overallContext); err != nil {
		shutdownErrors = append(shutdownErrors, fmt.Errorf("shutdown telemetry: %w", err))
	}
	return errors.Join(shutdownErrors...)
}

func unexpectedServeError(name string, err error) error {
	if err == nil {
		return fmt.Errorf("serve %s: stopped unexpectedly", name)
	}
	return fmt.Errorf("serve %s: %w", name, err)
}

func shutdownTelemetry(provider telemetryProvider, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	if err := provider.Shutdown(ctx); err != nil {
		return fmt.Errorf("shutdown telemetry after startup failure: %w", err)
	}
	return nil
}

func closeListener(operation string, listener net.Listener) error {
	if err := listener.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
		return fmt.Errorf("%s: %w", operation, err)
	}
	return nil
}

var _ grpcServer = (*grpcgo.Server)(nil)
