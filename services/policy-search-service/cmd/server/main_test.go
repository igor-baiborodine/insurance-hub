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

	"github.com/igor-baiborodine/insurance-hub/services/policy-search-service/internal/config"
)

func TestRun_LogsSafeStartupConfigurationFailure(t *testing.T) {
	// given
	const privateMarker = "private-credential-marker"
	t.Setenv("HTTP_ADDR", privateMarker)
	var output bytes.Buffer

	// when
	err := run(context.Background(), &output)

	// then
	if err == nil || strings.Contains(err.Error(), privateMarker) ||
		strings.Contains(output.String(), privateMarker) {
		t.Fatalf(
			"unsafe configuration diagnostic: error=%v output=%s",
			err,
			output.String(),
		)
	}
	var entry map[string]any
	if err := json.Unmarshal(output.Bytes(), &entry); err != nil {
		t.Fatal(err)
	}
	if entry["service_name"] != config.DefaultServiceName || entry["setting"] != "HTTP_ADDR" ||
		entry["error"] != "[REDACTED]" {
		t.Errorf("unexpected startup log: %#v", entry)
	}
}

func TestRun_PreservesServiceFailure(t *testing.T) {
	// given
	cause := errors.New("private marker")
	settings := config.Config{ServiceName: config.DefaultServiceName}
	load := func() (config.Config, error) { return settings, nil }
	runService := func(context.Context, config.Config, *slog.Logger) error { return cause }
	var output bytes.Buffer

	// when
	err := runWith(context.Background(), &output, load, runService)

	// then
	if !errors.Is(err, cause) {
		t.Fatalf("service failure lost: %v", err)
	}
	if strings.Contains(output.String(), "private marker") {
		t.Errorf("unsafe service log: %s", output.String())
	}
}

func TestShutdownContext_HandlesSIGTERM(t *testing.T) {
	// given
	ctx, stop := shutdownContext(context.Background())
	defer stop()

	// when
	if err := syscall.Kill(os.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}

	// then
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("SIGTERM did not cancel shutdown context")
	}
}
