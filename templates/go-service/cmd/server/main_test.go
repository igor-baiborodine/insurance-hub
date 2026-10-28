package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/igor-baiborodine/insurance-hub/templates/go-service/internal/config"
)

func TestRun_LogsServiceIdentityWithoutInvalidConfigurationValue(t *testing.T) {
	// given
	const privateMarker = "private-credential-marker"
	t.Setenv("SERVICE_NAME", "configured-service")
	t.Setenv("GRPC_ADDR", "127.0.0.1:0")
	t.Setenv("HEALTH_ADDR", "127.0.0.1:0")
	t.Setenv("LOG_LEVEL", "info")
	t.Setenv("SHUTDOWN_TIMEOUT", "1s")
	t.Setenv("OTEL_ENABLED", "true")
	t.Setenv(
		"OTEL_EXPORTER_OTLP_ENDPOINT",
		"http://user:"+privateMarker+"@127.0.0.1:4317",
	)
	t.Setenv("OTEL_EXPORTER_OTLP_TIMEOUT", "1s")
	var output bytes.Buffer

	// when
	err := run(context.Background(), &output)

	// then
	if err == nil {
		t.Fatal("run error = nil, want invalid configuration error")
	}
	if strings.Contains(err.Error(), privateMarker) {
		t.Fatalf("run error exposes private marker: %v", err)
	}
	if strings.Contains(output.String(), privateMarker) {
		t.Fatalf("startup log exposes private marker: %s", output.String())
	}

	var entry map[string]any
	if decodeErr := json.Unmarshal(bytes.TrimSpace(output.Bytes()), &entry); decodeErr != nil {
		t.Fatalf("decode startup log: %v", decodeErr)
	}
	if entry["service_name"] != "go-service" || entry["level"] != "ERROR" ||
		entry["msg"] != "configuration failed" {
		t.Errorf("unexpected startup log: %#v", entry)
	}
	if entry["setting"] != "OTEL_EXPORTER_OTLP_ENDPOINT" ||
		entry["reason"] != "must be an HTTP or HTTPS OTLP/gRPC URL" || entry["error"] != "[REDACTED]" {
		t.Errorf("missing safe configuration diagnostic: %#v", entry)
	}
}

func TestShutdownContext_HandlesSIGTERM(t *testing.T) {
	// given
	ctx, stop := shutdownContext(context.Background())
	defer stop()

	// when
	if err := syscall.Kill(os.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatalf("send SIGTERM: %v", err)
	}

	// then
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("SIGTERM did not cancel the shutdown context")
	}
}

func TestRun_LogsUnknownConfigurationFailureSafely(t *testing.T) {
	// given
	var output bytes.Buffer
	cause := errors.New("private-marker")
	load := func() (config.Config, error) { return config.Config{}, cause }
	runService := func(context.Context, config.Config, *slog.Logger) error {
		t.Fatal("service started despite configuration failure")
		return nil
	}
	// when
	err := runWith(context.Background(), &output, load, runService)
	// then
	if !errors.Is(err, cause) {
		t.Fatalf("original error lost: %v", err)
	}
	if strings.Contains(output.String(), "private-marker") {
		t.Fatalf("unsafe log: %s", output.String())
	}
	var entry map[string]any
	if err := json.Unmarshal(output.Bytes(), &entry); err != nil {
		t.Fatal(err)
	}
	if entry["setting"] != "unknown" || entry["reason"] != "could not load configuration" ||
		entry["error"] != "[REDACTED]" {
		t.Errorf("unsafe or incomplete diagnostic: %#v", entry)
	}
}
