// Package service owns Chat process composition and lifecycle.
package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/igor-baiborodine/insurance-hub/libs/servicekit/health"

	"github.com/igor-baiborodine/insurance-hub/services/chat-service/internal/config"
	"github.com/igor-baiborodine/insurance-hub/services/chat-service/internal/telemetry"
)

type telemetryProvider interface {
	Shutdown(context.Context) error
}

type ownedServer interface {
	Serve(net.Listener) error
	Shutdown(context.Context) error
	Close() error
}

type connectionLifecycle interface {
	BeginDrain()
	Drain(context.Context) error
}

type dependencies struct {
	newTelemetry  func(context.Context, string, config.Telemetry) (telemetryProvider, error)
	newHealth     func(time.Duration, health.Check) (*health.State, error)
	newRegistry   func() connectionLifecycle
	listen        func(string, string) (net.Listener, error)
	newBusiness   func(http.Handler, config.Config) ownedServer
	newManagement func(*health.State) ownedServer
}

func productionDependencies() dependencies {
	return dependencies{
		newTelemetry: func(
			ctx context.Context,
			serviceName string,
			settings config.Telemetry,
		) (telemetryProvider, error) {
			return telemetry.New(ctx, serviceName, settings)
		},
		newHealth:   health.NewState,
		newRegistry: func() connectionLifecycle { return NewConnectionRegistry() },
		listen:      net.Listen,
		newBusiness: func(handler http.Handler, settings config.Config) ownedServer {
			return &http.Server{
				Handler:           handler,
				ReadHeaderTimeout: settings.ReadHeaderTimeout,
				ReadTimeout:       settings.ReadTimeout,
				WriteTimeout:      settings.WriteTimeout,
				IdleTimeout:       settings.IdleTimeout,
				MaxHeaderBytes:    settings.HTTPMaxHeaderBytes,
			}
		},
		newManagement: func(state *health.State) ownedServer {
			return health.NewManagementServer(state)
		},
	}
}

// Run owns telemetry, readiness, both HTTP listeners and bounded shutdown.
func Run(ctx context.Context, settings config.Config, logger *slog.Logger) error {
	if logger == nil {
		return errors.New("run service: logger is required")
	}
	return run(ctx, settings, logger, productionDependencies())
}

func run(
	ctx context.Context,
	settings config.Config,
	logger *slog.Logger,
	deps dependencies,
) error {
	provider, err := deps.newTelemetry(ctx, settings.ServiceName, settings.Telemetry)
	if err != nil {
		return fmt.Errorf("run service: initialize telemetry: %w", err)
	}
	state, err := deps.newHealth(
		settings.ProbeTimeout,
		func(context.Context) error { return nil },
	)
	if err != nil {
		return errors.Join(
			fmt.Errorf("run service: initialize health: %w", err),
			shutdownTelemetry(provider, settings.ExporterTimeout),
		)
	}

	registry := deps.newRegistry()
	businessHandler := newBusinessHandler()
	businessListener, err := deps.listen("tcp", settings.HTTPAddress.String())
	if err != nil {
		return errors.Join(
			fmt.Errorf("run service: acquire business listener: %w", err),
			shutdownTelemetry(provider, settings.ExporterTimeout),
		)
	}
	managementListener, err := deps.listen("tcp", settings.HealthAddress.String())
	if err != nil {
		return errors.Join(
			fmt.Errorf("run service: acquire management listener: %w", err),
			businessListener.Close(),
			shutdownTelemetry(provider, settings.ExporterTimeout),
		)
	}

	businessServer := deps.newBusiness(businessHandler, settings)
	managementServer := deps.newManagement(state)
	cause := supervise(
		ctx,
		logger,
		state,
		businessServer,
		businessListener,
		managementServer,
		managementListener,
	)
	cleanup := shutdown(
		settings,
		state,
		businessHandler,
		registry,
		businessServer,
		managementServer,
		provider,
	)
	return errors.Join(cause, cleanup)
}

type serveResult struct {
	name string
	err  error
}

func supervise(
	ctx context.Context,
	logger *slog.Logger,
	state *health.State,
	businessServer ownedServer,
	businessListener net.Listener,
	managementServer ownedServer,
	managementListener net.Listener,
) error {
	results := make(chan serveResult, 2)
	businessStarted := make(chan struct{})
	managementStarted := make(chan struct{})
	go serve("business", businessServer, businessListener, businessStarted, results)
	go serve("management", managementServer, managementListener, managementStarted, results)

	for businessStarted != nil || managementStarted != nil {
		select {
		case <-ctx.Done():
			return nil
		case result := <-results:
			return unexpectedServeError(result)
		case <-businessStarted:
			businessStarted = nil
		case <-managementStarted:
			managementStarted = nil
		}
	}
	state.SetReady(true)
	logger.InfoContext(ctx, "service ready")

	select {
	case <-ctx.Done():
		return nil
	case result := <-results:
		return unexpectedServeError(result)
	}
}

func serve(
	name string,
	server ownedServer,
	listener net.Listener,
	started chan struct{},
	results chan<- serveResult,
) {
	err := server.Serve(&startListener{Listener: listener, started: started})
	results <- serveResult{name: name, err: err}
}

func unexpectedServeError(result serveResult) error {
	if result.err == nil || errors.Is(result.err, http.ErrServerClosed) {
		return fmt.Errorf("run service: %s server stopped unexpectedly", result.name)
	}
	return fmt.Errorf("run service: %s server failed: %w", result.name, result.err)
}

type startListener struct {
	net.Listener
	once    sync.Once
	started chan struct{}
}

func (listener *startListener) Accept() (net.Conn, error) {
	listener.once.Do(func() { close(listener.started) })
	return listener.Listener.Accept()
}

type businessHandler struct{ draining atomic.Bool }

func newBusinessHandler() *businessHandler { return &businessHandler{} }

func (handler *businessHandler) beginDrain() { handler.draining.Store(true) }

func (handler *businessHandler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	if handler.draining.Load() {
		http.Error(writer, "service unavailable", http.StatusServiceUnavailable)
		return
	}
	http.NotFound(writer, request)
}

func shutdown(
	settings config.Config,
	state *health.State,
	businessHandler *businessHandler,
	registry connectionLifecycle,
	businessServer ownedServer,
	managementServer ownedServer,
	provider telemetryProvider,
) error {
	state.SetReady(false)
	businessHandler.beginDrain()
	registry.BeginDrain()
	serviceBudget := settings.ShutdownTimeout - settings.ExporterTimeout
	serviceCtx, cancelService := context.WithTimeout(context.Background(), serviceBudget)
	defer cancelService()

	serviceErr := errors.Join(
		shutdownServer(serviceCtx, businessServer),
		registry.Drain(serviceCtx),
		shutdownServer(serviceCtx, managementServer),
	)
	return errors.Join(serviceErr, shutdownTelemetry(provider, settings.ExporterTimeout))
}

func shutdownServer(ctx context.Context, server ownedServer) error {
	if err := server.Shutdown(ctx); err != nil {
		return errors.Join(err, server.Close())
	}
	return nil
}

func shutdownTelemetry(provider telemetryProvider, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return provider.Shutdown(ctx)
}
