package service

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/igor-baiborodine/insurance-hub/services/product-service/internal/config"
	"github.com/igor-baiborodine/insurance-hub/services/product-service/internal/telemetry"
)

func TestShellServesLivenessButNeverClaimsReadiness(t *testing.T) {
	// given
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	settings := config.Config{
		ServiceName:     "product-service",
		ShutdownTimeout: time.Second,
		Telemetry:       config.Telemetry{ExporterTimeout: time.Second},
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	deps := dependencies{
		listen:       func(string, string) (net.Listener, error) { return listener, nil },
		newTelemetry: telemetry.New,
	}
	go func() { result <- run(ctx, settings, slog.New(slog.NewTextHandler(io.Discard, nil)), deps) }()
	client := &http.Client{Timeout: time.Second}

	// when
	live := getStatus(t, client, "http://"+listener.Addr().String()+"/livez")
	ready := getStatus(t, client, "http://"+listener.Addr().String()+"/readyz")

	// then
	if live != http.StatusOK || ready != http.StatusServiceUnavailable {
		t.Errorf("management status live=%d ready=%d", live, ready)
	}

	// when
	cancel()

	// then
	select {
	case err := <-result:
		if err != nil {
			t.Errorf("run shell: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("shell did not stop within its test bound")
	}
}

func TestShellPreservesListenerFailure(t *testing.T) {
	// given
	listenErr := errors.New("occupied listener")
	deps := dependencies{
		listen:       func(string, string) (net.Listener, error) { return nil, listenErr },
		newTelemetry: telemetry.New,
	}
	settings := config.Config{
		ServiceName:     "product-service",
		ShutdownTimeout: time.Second,
		Telemetry:       config.Telemetry{ExporterTimeout: time.Second},
	}

	// when
	err := run(context.Background(), settings, slog.Default(), deps)

	// then
	if !errors.Is(err, listenErr) {
		t.Errorf("listener failure not preserved: %v", err)
	}
}

func getStatus(t *testing.T, client *http.Client, url string) int {
	t.Helper()
	response, err := client.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer func() { _ = response.Body.Close() }()
	if _, err := io.Copy(io.Discard, response.Body); err != nil {
		t.Fatalf("read response from %s: %v", url, err)
	}
	return response.StatusCode
}
