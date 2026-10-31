// Package service owns policy search process construction and lifecycle.
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

	"github.com/igor-baiborodine/insurance-hub/libs/servicekit/health"

	"github.com/igor-baiborodine/insurance-hub/services/policy-search-service/internal/application"
	"github.com/igor-baiborodine/insurance-hub/services/policy-search-service/internal/config"
	"github.com/igor-baiborodine/insurance-hub/services/policy-search-service/internal/domain"
	"github.com/igor-baiborodine/insurance-hub/services/policy-search-service/internal/elasticsearch"
	policyhttp "github.com/igor-baiborodine/insurance-hub/services/policy-search-service/internal/http"
	"github.com/igor-baiborodine/insurance-hub/services/policy-search-service/internal/telemetry"
)

const forcedCleanupReserve = 2 * time.Second

// Run constructs policy search, serves its listeners, and owns bounded dependency cleanup.
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
		return fmt.Errorf("run service: initialize telemetry: %w", err)
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
	elasticsearchClient, err := elasticsearch.New(elasticsearch.Config{
		Endpoint: settings.Elasticsearch.URL,
		Username: settings.Elasticsearch.Username,
		Password: settings.Elasticsearch.Password.Value(),
		CACert:   settings.Elasticsearch.CACert,
		Timeout:  settings.Elasticsearch.Timeout,
	})
	if err != nil {
		return fmt.Errorf("run service: initialize elasticsearch: %w", err)
	}
	resources.elasticsearchClient = elasticsearchClient
	searcher, err := elasticsearch.NewSearcher(elasticsearchClient)
	if err != nil {
		return fmt.Errorf("run service: initialize policy search adapter: %w", err)
	}
	searchPolicies, err := application.NewSearchPolicies(searcher)
	if err != nil {
		return fmt.Errorf("run service: initialize policy search use case: %w", err)
	}
	observer, err := telemetry.NewRequestObserver(
		logger,
		provider.TracerProvider(),
		provider.MeterProvider(),
		provider.HTTPPropagator(),
	)
	if err != nil {
		return fmt.Errorf("run service: initialize request telemetry: %w", err)
	}
	search := func(ctx context.Context, query string) (policies []domain.Policy, err error) {
		err = observer.Application(ctx, func(ctx context.Context) error {
			policies, err = searchPolicies.Execute(ctx, query)
			return err
		})
		return policies, err
	}
	httpHandler, err := policyhttp.NewHandler(
		logger,
		policyhttp.Settings{
			RequestTimeout: settings.RequestTimeout,
			Observer:       observer,
		},
		search,
	)
	if err != nil {
		return fmt.Errorf("run service: initialize policy search HTTP: %w", err)
	}

	resources.healthState, err = health.NewState(
		settings.ProbeTimeout,
		func(probeCtx context.Context) error {
			return elasticsearchClient.Check(probeCtx)
		},
	)
	if err != nil {
		return fmt.Errorf("run service: initialize health: %w", err)
	}
	resources.httpServer = newBusinessServer(settings.HTTPServer, httpHandler)
	resources.managementServer = health.NewManagementServer(resources.healthState)

	resources.httpListener, err = net.Listen("tcp", settings.HTTPAddress.String())
	if err != nil {
		return fmt.Errorf("run service: listen for HTTP: %w", err)
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

	serveResults := make(chan serveResult, 2)
	go func() {
		serveResults <- serveResult{
			name: "HTTP",
			err:  resources.httpServer.Serve(resources.httpListener),
		}
	}()
	go func() {
		serveResults <- serveResult{
			name: "management HTTP",
			err:  resources.managementServer.Serve(resources.managementListener),
		}
	}()
	resources.healthState.SetReady(true)
	started = true
	logger.InfoContext(ctx, "policy search service started",
		slog.String("http_address", resources.httpListener.Addr().String()),
		slog.String("health_address", resources.managementListener.Addr().String()),
	)

	serveErr := waitForRunStop(ctx, serveResults)
	shutdownErr, telemetryErr := resources.shutdown(settings.ShutdownTimeout)
	return finishRun(logger, serveErr, shutdownErr, telemetryErr)
}

func newBusinessServer(bounds config.HTTPServer, handler http.Handler) *http.Server {
	return &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: bounds.ReadHeaderTimeout,
		ReadTimeout:       bounds.ReadTimeout,
		WriteTimeout:      bounds.WriteTimeout,
		IdleTimeout:       bounds.IdleTimeout,
	}
}

type serveResult struct {
	name string
	err  error
}

func waitForRunStop(ctx context.Context, results <-chan serveResult) error {
	select {
	case <-ctx.Done():
		return nil
	case result := <-results:
		if expectedServeError(result.err) {
			return nil
		}
		return fmt.Errorf("serve %s: %w", result.name, result.err)
	}
}

type runtimeResources struct {
	provider            *telemetry.Provider
	elasticsearchClient *elasticsearch.Client
	healthState         *health.State
	httpServer          *http.Server
	managementServer    *http.Server
	httpListener        net.Listener
	managementListener  net.Listener
}

func (resources *runtimeResources) closeAfterStartupFailure(timeout time.Duration) error {
	cleanupCtx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	closeListener(resources.managementListener)
	closeListener(resources.httpListener)
	if resources.elasticsearchClient != nil {
		resources.elasticsearchClient.CloseIdleConnections()
	}
	if resources.provider == nil {
		return nil
	}
	if err := resources.provider.Shutdown(cleanupCtx); err != nil {
		return fmt.Errorf("shutdown telemetry after startup failure: %w", err)
	}
	return nil
}

func (resources *runtimeResources) shutdown(timeout time.Duration) (error, error) {
	resources.healthState.SetReady(false)
	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), timeout)
	defer cancelShutdown()

	graceBudget := timeout - min(timeout, forcedCleanupReserve)
	graceCtx, cancelGrace := context.WithTimeout(shutdownCtx, graceBudget)
	defer cancelGrace()

	var wait sync.WaitGroup
	wait.Add(2)
	httpResults := make(chan error, 2)
	go func() {
		defer wait.Done()
		httpResults <- resources.httpServer.Shutdown(graceCtx)
	}()
	go func() {
		defer wait.Done()
		httpResults <- resources.managementServer.Shutdown(graceCtx)
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
		<-graceful
	}

	close(httpResults)
	var shutdownErr error
	for err := range httpResults {
		if err != nil && !errors.Is(err, context.DeadlineExceeded) {
			shutdownErr = errors.Join(shutdownErr, err)
		}
	}
	resources.elasticsearchClient.CloseIdleConnections()
	telemetryErr := resources.provider.Shutdown(shutdownCtx)
	return shutdownErr, telemetryErr
}

func finishRun(logger *slog.Logger, serveErr, shutdownErr, telemetryErr error) error {
	if telemetryErr != nil {
		logger.WarnContext(context.Background(), "telemetry shutdown incomplete")
	}
	return errors.Join(serveErr, shutdownErr)
}

func expectedServeError(err error) bool {
	return err == nil || errors.Is(err, http.ErrServerClosed)
}

func closeListener(listener net.Listener) {
	if listener != nil {
		_ = listener.Close()
	}
}
