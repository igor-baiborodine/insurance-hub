package service

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	grpcgo "google.golang.org/grpc"

	"github.com/igor-baiborodine/insurance-hub/services/product-service/internal/config"
	"github.com/igor-baiborodine/insurance-hub/services/product-service/internal/health"
	"github.com/igor-baiborodine/insurance-hub/services/product-service/internal/telemetry"
)

func TestRunRejectsInvalidProcessDependencies(t *testing.T) {
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

func TestExpectedServeErrorRecognizesOwnedServerShutdown(t *testing.T) {
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

func TestWaitForRunStopPreservesFatalServingError(t *testing.T) {
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

func TestWaitForRunStopAcceptsSignalAndOwnedServerStops(t *testing.T) {
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

func TestRuntimeResourcesShutdownIsRepeatableAndMarksUnready(t *testing.T) {
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
