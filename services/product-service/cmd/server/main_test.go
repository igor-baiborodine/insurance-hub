package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestRunLogsServiceIdentityWithoutInvalidConfigurationValue(t *testing.T) {
	// given
	const privateMarker = "private-credential-marker"
	t.Setenv("SERVICE_NAME", "configured-service")
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
	if entry["service"] != "product-service" || entry["level"] != "ERROR" ||
		entry["msg"] != "configuration failed" {
		t.Errorf("unexpected startup log: %#v", entry)
	}
}

func TestShutdownContextHandlesSIGTERM(t *testing.T) {
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
