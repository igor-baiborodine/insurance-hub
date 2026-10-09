package config

import (
	"strings"
	"testing"
	"time"
)

func TestLoadShellDefaults(t *testing.T) {
	// when
	settings, err := load(func(string) (string, bool) { return "", false })
	// then
	if err != nil {
		t.Fatalf("load defaults: %v", err)
	}
	if settings.ServiceName != "product-service" ||
		settings.HealthAddress.String() != "127.0.0.1:8080" ||
		settings.ShutdownTimeout != 10*time.Second ||
		settings.Telemetry.Enabled || settings.Telemetry.ExporterTimeout != 2*time.Second {
		t.Errorf("unexpected shell defaults: %#v", settings)
	}
}

func TestLoadAcceptsManagementAndTelemetryOverrides(t *testing.T) {
	// given
	values := map[string]string{
		"SERVICE_NAME":                "product-reader",
		"HEALTH_ADDR":                 "[::1]:0",
		"SHUTDOWN_TIMEOUT":            "3s",
		"OTEL_ENABLED":                "true",
		"OTEL_EXPORTER_OTLP_ENDPOINT": "http://127.0.0.1:4317",
		"OTEL_EXPORTER_OTLP_TIMEOUT":  "1s",
	}

	// when
	settings, err := load(func(key string) (string, bool) {
		value, found := values[key]
		return value, found
	})
	// then
	if err != nil {
		t.Fatalf("load overrides: %v", err)
	}
	if settings.ServiceName != "product-reader" ||
		settings.HealthAddress.String() != "[::1]:0" ||
		settings.ShutdownTimeout != 3*time.Second ||
		!settings.Telemetry.Enabled ||
		settings.Telemetry.ExporterTimeout != time.Second {
		t.Errorf("unexpected overrides: %#v", settings)
	}
}

func TestLoadRejectsInvalidSettingWithoutValue(t *testing.T) {
	// given
	const marker = "private-password-marker"
	values := map[string]string{
		"OTEL_ENABLED":                "true",
		"OTEL_EXPORTER_OTLP_ENDPOINT": "http://user:" + marker + "@127.0.0.1:4317",
	}

	// when
	_, err := load(func(key string) (string, bool) {
		value, found := values[key]
		return value, found
	})

	// then
	if err == nil || !strings.Contains(err.Error(), "OTEL_EXPORTER_OTLP_ENDPOINT") {
		t.Fatalf("expected endpoint diagnostic, got %v", err)
	}
	if strings.Contains(err.Error(), marker) {
		t.Errorf("configuration error exposed secret: %v", err)
	}
}
