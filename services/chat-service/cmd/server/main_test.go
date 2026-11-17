package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"

	kitconfig "github.com/igor-baiborodine/insurance-hub/libs/servicekit/config"

	"github.com/igor-baiborodine/insurance-hub/services/chat-service/internal/config"
)

func TestRunWithLogsConfigurationFailureSafely(t *testing.T) {
	const private = "private-marker"
	cause := kitconfig.Invalid(kitconfig.EnvServiceName, "must match fixed identity")
	load := func() (config.Config, error) { return config.Config{}, cause }
	var output bytes.Buffer

	err := runWith(
		context.Background(),
		&output,
		load,
		func(context.Context, config.Config, *slog.Logger) error {
			t.Fatal("service started after configuration failure")
			return nil
		},
	)
	if !errors.Is(err, cause) {
		t.Fatalf("cause lost: %v", err)
	}
	if strings.Contains(output.String(), private) {
		t.Fatalf("unsafe log: %s", output.String())
	}
	var entry map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(output.Bytes()), &entry); err != nil {
		t.Fatal(err)
	}
	if entry["service_name"] != config.DefaultServiceName ||
		entry["msg"] != "configuration failed" || entry["error"] != "[REDACTED]" {
		t.Errorf("startup log = %#v", entry)
	}
}

func TestRunWithPreservesServiceFailure(t *testing.T) {
	cause := errors.New("private-service-cause")
	settings := config.Config{
		ServiceName: config.DefaultServiceName,
		Telemetry: config.Telemetry{
			Identity: config.DevelopmentIdentity(
				config.DefaultServiceName,
			).Identity,
			ExporterTimeout: 1,
		},
	}
	var output bytes.Buffer
	err := runWith(
		context.Background(),
		&output,
		func() (config.Config, error) { return settings, nil },
		func(context.Context, config.Config, *slog.Logger) error { return cause },
	)
	if !errors.Is(err, cause) {
		t.Fatalf("cause lost: %v", err)
	}
	if strings.Contains(output.String(), cause.Error()) {
		t.Fatalf("service log disclosed raw error: %s", output.String())
	}
}
