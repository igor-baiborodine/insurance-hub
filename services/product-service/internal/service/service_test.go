package service

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"testing"
	"time"

	grpcgo "google.golang.org/grpc"

	"github.com/igor-baiborodine/insurance-hub/services/product-service/internal/config"
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
