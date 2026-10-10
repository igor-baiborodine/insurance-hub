package service

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	grpcgo "google.golang.org/grpc"

	"github.com/igor-baiborodine/insurance-hub/services/product-service/internal/config"
	"github.com/igor-baiborodine/insurance-hub/services/product-service/internal/health"
	"github.com/igor-baiborodine/insurance-hub/services/product-service/internal/telemetry"
)

func TestRun_RejectsInvalidProcessDependencies(t *testing.T) {
	// given
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	tests := []struct {
		name     string
		settings config.Config
		logger   *slog.Logger
	}{
		{name: "missing logger", settings: config.Config{StartupTimeout: time.Second}},
		{
			name:     "missing startup timeout",
			settings: config.Config{ShutdownTimeout: time.Second},
			logger:   logger,
		},
		{
			name:     "missing shutdown timeout",
			settings: config.Config{StartupTimeout: time.Second},
			logger:   logger,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// when
			err := Run(context.Background(), test.settings, test.logger)

			// then
			if err == nil {
				t.Fatal("Run() error = nil")
			}
		})
	}
}

func TestRun_ReportsTelemetryInitializationFailure(t *testing.T) {
	// given
	settings := config.Config{
		ServiceName:     "product-service",
		StartupTimeout:  time.Second,
		ShutdownTimeout: time.Second,
		Telemetry: config.Telemetry{
			Enabled:         true,
			ExporterTimeout: time.Second,
		},
	}

	// when
	err := Run(
		context.Background(),
		settings,
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)

	// then
	if err == nil || !strings.Contains(err.Error(), "exporter endpoint is required") {
		t.Fatalf("Run() error = %v, want telemetry endpoint failure", err)
	}
}

func TestRun_CleansUpAfterDatabaseConnectionFailure(t *testing.T) {
	// given
	settings := loadUnavailableDatabaseSettings(t)

	// when
	err := Run(
		context.Background(),
		settings,
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)

	// then
	if err == nil || !strings.Contains(err.Error(), "open Product database") {
		t.Fatalf("Run() error = %v, want database connection failure", err)
	}
	if strings.Contains(err.Error(), "reader-password") {
		t.Fatalf("Run() error exposes database credentials: %v", err)
	}
}

func TestExpectedServeError_RecognizesOwnedServerShutdown(t *testing.T) {
	// given
	tests := []struct {
		err  error
		want bool
	}{
		{want: true},
		{err: http.ErrServerClosed, want: true},
		{err: grpcgo.ErrServerStopped, want: true},
		{err: context.Canceled},
	}

	for _, test := range tests {
		// when
		got := expectedServeError(test.err)

		// then
		if got != test.want {
			t.Errorf("expectedServeError(%v) = %t, want %t", test.err, got, test.want)
		}
	}
}

func TestWaitForRunStop_PreservesFatalServingError(t *testing.T) {
	// given
	cause := errors.New("fatal listener failure")
	results := make(chan serveResult, 1)
	results <- serveResult{name: "Product HTTP", err: cause}

	// when
	err := waitForRunStop(context.Background(), results)

	// then
	if !errors.Is(err, cause) || err.Error() != "serve Product HTTP: fatal listener failure" {
		t.Fatalf("waitForRunStop() error = %v", err)
	}
}

func TestWaitForRunStop_AcceptsSignalAndOwnedServerStops(t *testing.T) {
	// given
	canceledCtx, cancel := context.WithCancel(context.Background())
	cancel()

	// when
	signalErr := waitForRunStop(canceledCtx, make(chan serveResult))
	ownedResults := make(chan serveResult, 1)
	ownedResults <- serveResult{name: "Product gRPC", err: grpcgo.ErrServerStopped}
	ownedErr := waitForRunStop(context.Background(), ownedResults)

	// then
	if signalErr != nil || ownedErr != nil {
		t.Fatalf("signal/owned stop errors = %v/%v", signalErr, ownedErr)
	}
}

func TestRuntimeResources_Shutdown_IsRepeatableAndMarksUnready(t *testing.T) {
	// given
	pool, err := pgxpool.New(
		context.Background(),
		"postgresql://unused:unused@127.0.0.1:1/unused?sslmode=disable",
	)
	if err != nil {
		t.Fatalf("create unopened test pool: %v", err)
	}
	provider, err := telemetry.New(
		context.Background(),
		"product-service",
		config.Telemetry{Enabled: false, ExporterTimeout: time.Second},
	)
	if err != nil {
		pool.Close()
		t.Fatalf("create disabled telemetry: %v", err)
	}
	healthState, err := health.NewState(time.Second, func(context.Context) error { return nil })
	if err != nil {
		pool.Close()
		t.Fatalf("create health state: %v", err)
	}
	healthState.SetReady(true)
	resources := runtimeResources{
		provider:         provider,
		pool:             pool,
		healthState:      healthState,
		httpServer:       &http.Server{},
		grpcServer:       grpcgo.NewServer(),
		managementServer: &http.Server{},
	}

	// when
	firstErr := resources.shutdown(20 * time.Millisecond)
	secondErr := resources.shutdown(20 * time.Millisecond)

	// then
	if firstErr != nil || secondErr != nil {
		t.Fatalf("first/second shutdown errors = %v/%v", firstErr, secondErr)
	}
	if healthState.Ready() {
		t.Error("health state remained ready after shutdown")
	}
}

func TestRuntimeResources_CloseAfterStartupFailure_ReleasesOwnedResources(t *testing.T) {
	// given
	pool, err := pgxpool.New(
		context.Background(),
		"postgresql://unused:unused@127.0.0.1:1/unused?sslmode=disable",
	)
	if err != nil {
		t.Fatalf("create unopened test pool: %v", err)
	}
	provider, err := telemetry.New(
		context.Background(),
		"product-service",
		config.Telemetry{Enabled: false, ExporterTimeout: time.Second},
	)
	if err != nil {
		pool.Close()
		t.Fatalf("create disabled telemetry: %v", err)
	}
	listeners := make([]net.Listener, 3)
	for index := range listeners {
		listeners[index], err = net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			for _, listener := range listeners {
				closeListener(listener)
			}
			pool.Close()
			t.Fatalf("create listener %d: %v", index, err)
		}
	}
	resources := runtimeResources{
		provider:           provider,
		pool:               pool,
		httpListener:       listeners[0],
		grpcListener:       listeners[1],
		managementListener: listeners[2],
	}

	// when
	cleanupErr := resources.closeAfterStartupFailure(time.Second)

	// then
	if cleanupErr != nil {
		t.Fatalf("closeAfterStartupFailure() error = %v", cleanupErr)
	}
	for _, listener := range listeners {
		connection, dialErr := net.DialTimeout(
			"tcp",
			listener.Addr().String(),
			20*time.Millisecond,
		)
		if dialErr == nil {
			_ = connection.Close()
			t.Errorf("listener %s still accepts connections", listener.Addr())
		}
	}
	if pingErr := pool.Ping(context.Background()); pingErr == nil {
		t.Error("pool remains usable after startup cleanup")
	}
	if err := (&runtimeResources{}).closeAfterStartupFailure(time.Second); err != nil {
		t.Fatalf("empty startup cleanup error = %v", err)
	}
}

func loadUnavailableDatabaseSettings(t *testing.T) config.Config {
	t.Helper()
	t.Setenv("SERVICE_NAME", "product-service")
	t.Setenv(
		"PRODUCT_DATABASE_URL",
		"postgresql://product_reader:reader-password@127.0.0.1:1/product?sslmode=disable",
	)
	t.Setenv("DB_MAX_CONNS", "1")
	t.Setenv("HTTP_ADDR", "127.0.0.1:18081")
	t.Setenv("GRPC_ADDR", "127.0.0.1:19090")
	t.Setenv("HEALTH_ADDR", "127.0.0.1:18080")
	t.Setenv("DB_CONNECT_TIMEOUT", "20ms")
	t.Setenv("DB_ACQUIRE_TIMEOUT", "20ms")
	t.Setenv("DB_QUERY_TIMEOUT", "50ms")
	t.Setenv("STARTUP_TIMEOUT", "100ms")
	t.Setenv("REQUEST_TIMEOUT", "100ms")
	t.Setenv("PROBE_TIMEOUT", "20ms")
	t.Setenv("HTTP_READ_HEADER_TIMEOUT", "20ms")
	t.Setenv("HTTP_READ_TIMEOUT", "100ms")
	t.Setenv("HTTP_WRITE_TIMEOUT", "100ms")
	t.Setenv("HTTP_IDLE_TIMEOUT", "100ms")
	t.Setenv("SHUTDOWN_TIMEOUT", "100ms")
	t.Setenv("LOG_LEVEL", "info")
	t.Setenv("OTEL_ENABLED", "false")
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://127.0.0.1:4317")
	t.Setenv("OTEL_EXPORTER_OTLP_TIMEOUT", "20ms")
	settings, err := config.Load()
	if err != nil {
		t.Fatalf("load unavailable database settings: %v", err)
	}
	return settings
}
