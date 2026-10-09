// Package service owns Product process construction and lifecycle.
package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	grpcgo "google.golang.org/grpc"

	"github.com/igor-baiborodine/insurance-hub/services/product-service/internal/application"
	"github.com/igor-baiborodine/insurance-hub/services/product-service/internal/config"
	productgrpc "github.com/igor-baiborodine/insurance-hub/services/product-service/internal/grpc"
	"github.com/igor-baiborodine/insurance-hub/services/product-service/internal/health"
	producthttp "github.com/igor-baiborodine/insurance-hub/services/product-service/internal/http"
	"github.com/igor-baiborodine/insurance-hub/services/product-service/internal/postgres"
	"github.com/igor-baiborodine/insurance-hub/services/product-service/internal/telemetry"
)

const (
	managementReadHeaderTimeout = 5 * time.Second
	managementIdleTimeout       = 30 * time.Second
	forcedCleanupReserve        = 2 * time.Second
)

// Run constructs the Product service, verifies its dependencies, serves all listeners, and owns
// their bounded cleanup together with the singleton PostgreSQL pool and telemetry provider.
func Run(ctx context.Context, settings config.Config, logger *slog.Logger) (runErr error) {
	if logger == nil {
		return errors.New("run service: logger is required")
	}
	if settings.StartupTimeout <= 0 {
		return errors.New("run service: startup timeout must be positive")
	}
	if settings.ShutdownTimeout <= 0 {
		return errors.New("run service: shutdown timeout must be positive")
	}

	startupCtx, cancelStartup := context.WithTimeout(ctx, settings.StartupTimeout)
	defer cancelStartup()

	provider, err := telemetry.New(startupCtx, settings.ServiceName, settings.Telemetry)
	if err != nil {
		return fmt.Errorf("run service: %w", err)
	}
	resources := runtimeResources{provider: provider}
	started := false
	defer func() {
		if !started {
			runErr = errors.Join(
				runErr,
				resources.closeAfterStartupFailure(settings.ShutdownTimeout),
			)
		}
	}()

	pool, err := postgres.OpenPool(startupCtx, settings.Database)
	if err != nil {
		return fmt.Errorf("run service: open Product database: %w", err)
	}
	resources.pool = pool
	if err := postgres.CheckReadAccess(startupCtx, pool); err != nil {
		return fmt.Errorf("run service: verify Product database: %w", err)
	}

	reader, err := postgres.NewReader(
		pool,
		settings.Database.AcquireTimeout,
		settings.Database.QueryTimeout,
	)
	if err != nil {
		return fmt.Errorf("run service: %w", err)
	}
	listProducts, err := application.NewListProducts(reader)
	if err != nil {
		return fmt.Errorf("run service: %w", err)
	}
	getProduct, err := application.NewGetProduct(reader)
	if err != nil {
		return fmt.Errorf("run service: %w", err)
	}

	httpHandler, err := producthttp.NewHandler(
		logger,
		producthttp.Settings{RequestTimeout: settings.RequestTimeout},
		producthttp.ListProducts(listProducts.Execute),
		producthttp.GetProduct(getProduct.Execute),
	)
	if err != nil {
		return fmt.Errorf("run service: %w", err)
	}
	grpcServer, err := productgrpc.NewServer(
		logger,
		provider.TracerProvider(),
		provider.Propagator(),
		productgrpc.Settings{
			RequestTimeout:  settings.RequestTimeout,
			MaxReceiveBytes: settings.GRPC.MaxReceiveBytes,
			MaxSendBytes:    settings.GRPC.MaxSendBytes,
		},
		productgrpc.ListProducts(listProducts.Execute),
		productgrpc.GetProduct(getProduct.Execute),
	)
	if err != nil {
		return fmt.Errorf("run service: %w", err)
	}
	resources.grpcServer = grpcServer
	healthState, err := health.NewState(
		settings.ProbeTimeout,
		func(probeCtx context.Context) error {
			return postgres.CheckReadAccess(probeCtx, pool)
		},
	)
	if err != nil {
		return fmt.Errorf("run service: %w", err)
	}
	resources.healthState = healthState

	resources.httpServer = &http.Server{
		Handler:           httpHandler,
		ReadHeaderTimeout: settings.HTTPServer.ReadHeaderTimeout,
		ReadTimeout:       settings.HTTPServer.ReadTimeout,
		WriteTimeout:      settings.HTTPServer.WriteTimeout,
		IdleTimeout:       settings.HTTPServer.IdleTimeout,
	}
	resources.managementServer = &http.Server{
		Handler:           health.NewHandler(healthState),
		ReadHeaderTimeout: managementReadHeaderTimeout,
		IdleTimeout:       managementIdleTimeout,
	}

	resources.httpListener, err = net.Listen("tcp", settings.HTTPAddress.String())
	if err != nil {
		return fmt.Errorf("run service: listen for Product HTTP: %w", err)
	}
	resources.grpcListener, err = net.Listen("tcp", settings.GRPCAddress.String())
	if err != nil {
		return fmt.Errorf("run service: listen for Product gRPC: %w", err)
	}
	resources.managementListener, err = net.Listen("tcp", settings.HealthAddress.String())
	if err != nil {
		return fmt.Errorf("run service: listen for management HTTP: %w", err)
	}
	if err := startupCtx.Err(); err != nil {
		if ctx.Err() != nil {
			return nil
		}
		return fmt.Errorf("run service: startup: %w", err)
	}

	serveResults := make(chan serveResult, 3)
	go func() {
		serveResults <- serveResult{
			name: "Product HTTP",
			err:  producthttp.Serve(resources.httpServer, resources.httpListener),
		}
	}()
	go func() {
		serveResults <- serveResult{
			name: "Product gRPC",
			err:  resources.grpcServer.Serve(resources.grpcListener),
		}
	}()
	go func() {
		serveResults <- serveResult{
			name: "management HTTP",
			err:  resources.managementServer.Serve(resources.managementListener),
		}
	}()
	healthState.SetReady(true)
	started = true
	logger.InfoContext(ctx, "product service started",
		slog.String("http_address", resources.httpListener.Addr().String()),
		slog.String("grpc_address", resources.grpcListener.Addr().String()),
		slog.String("health_address", resources.managementListener.Addr().String()),
	)

	var serveErr error
	select {
	case <-ctx.Done():
	case result := <-serveResults:
		if !expectedServeError(result.err) {
			serveErr = fmt.Errorf("serve %s: %w", result.name, result.err)
		}
	}
	shutdownErr := resources.shutdown(settings.ShutdownTimeout)
	return errors.Join(serveErr, shutdownErr)
}

type serveResult struct {
	name string
	err  error
}

type runtimeResources struct {
	provider           *telemetry.Provider
	pool               *pgxpool.Pool
	healthState        *health.State
	httpServer         *http.Server
	grpcServer         *grpcgo.Server
	managementServer   *http.Server
	httpListener       net.Listener
	grpcListener       net.Listener
	managementListener net.Listener
}

func (resources *runtimeResources) closeAfterStartupFailure(timeout time.Duration) error {
	cleanupCtx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	closeListener(resources.managementListener)
	closeListener(resources.grpcListener)
	closeListener(resources.httpListener)
	if resources.pool != nil {
		resources.pool.Close()
	}
	if resources.provider == nil {
		return nil
	}
	if err := resources.provider.Shutdown(cleanupCtx); err != nil {
		return fmt.Errorf("shutdown telemetry after startup failure: %w", err)
	}
	return nil
}

func (resources *runtimeResources) shutdown(timeout time.Duration) error {
	resources.healthState.SetReady(false)
	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), timeout)
	defer cancelShutdown()

	graceBudget := timeout - min(timeout, forcedCleanupReserve)
	graceCtx, cancelGrace := context.WithTimeout(shutdownCtx, graceBudget)
	defer cancelGrace()

	var wait sync.WaitGroup
	wait.Add(3)
	httpResult := make(chan error, 2)
	go func() {
		defer wait.Done()
		httpResult <- resources.httpServer.Shutdown(graceCtx)
	}()
	go func() {
		defer wait.Done()
		httpResult <- resources.managementServer.Shutdown(graceCtx)
	}()
	go func() {
		defer wait.Done()
		resources.grpcServer.GracefulStop()
	}()

	graceful := make(chan struct{})
	go func() {
		wait.Wait()
		close(graceful)
	}()
	select {
	case <-graceful:
	case <-graceCtx.Done():
		_ = resources.httpServer.Close()
		_ = resources.managementServer.Close()
		resources.grpcServer.Stop()
		<-graceful
	}

	close(httpResult)
	var shutdownErr error
	for err := range httpResult {
		if err != nil && !errors.Is(err, context.DeadlineExceeded) {
			shutdownErr = errors.Join(shutdownErr, err)
		}
	}
	resources.pool.Close()
	if err := resources.provider.Shutdown(shutdownCtx); err != nil {
		shutdownErr = errors.Join(shutdownErr, fmt.Errorf("shutdown telemetry: %w", err))
	}
	return shutdownErr
}

func closeListener(listener net.Listener) {
	if listener != nil {
		_ = listener.Close()
	}
}

func expectedServeError(err error) bool {
	return err == nil || errors.Is(err, http.ErrServerClosed) ||
		errors.Is(err, grpcgo.ErrServerStopped)
}
