package config

import (
	"log/slog"
	"strings"
	"testing"
	"time"
)

func TestLoad_Defaults(t *testing.T) {
	// given
	lookup := mapLookup(nil)

	// when
	got, err := load(lookup)
	// then
	if err != nil {
		t.Fatalf("load defaults: %v", err)
	}

	if got.ServiceName != "go-service" {
		t.Errorf("ServiceName = %q, want go-service", got.ServiceName)
	}
	if got.GRPCAddress.String() != "127.0.0.1:9090" {
		t.Errorf("GRPCAddress = %q, want 127.0.0.1:9090", got.GRPCAddress)
	}
	if got.HealthAddress.String() != "127.0.0.1:8080" {
		t.Errorf("HealthAddress = %q, want 127.0.0.1:8080", got.HealthAddress)
	}
	if got.LogLevel != slog.LevelInfo {
		t.Errorf("LogLevel = %v, want INFO", got.LogLevel)
	}
	if got.ShutdownTimeout != 10*time.Second {
		t.Errorf("ShutdownTimeout = %v, want 10s", got.ShutdownTimeout)
	}
	if got.Telemetry.Enabled {
		t.Error("Telemetry.Enabled = true, want false")
	}
	if got.Telemetry.ExporterEndpoint != nil {
		t.Errorf(
			"Telemetry.ExporterEndpoint = %v, want nil",
			got.Telemetry.ExporterEndpoint,
		)
	}
	if got.Telemetry.ExporterTimeout != 5*time.Second {
		t.Errorf("Telemetry.ExporterTimeout = %v, want 5s", got.Telemetry.ExporterTimeout)
	}
}

func TestLoad_ValidOverrides(t *testing.T) {
	// given
	values := map[string]string{
		serviceNameSetting:     "copy-service",
		grpcAddressSetting:     "[::1]:0",
		healthAddressSetting:   "localhost:0",
		logLevelSetting:        "DEBUG+2",
		shutdownTimeoutSetting: "15s",
		otelEnabledSetting:     "true",
		otelEndpointSetting:    "https://alloy.monitoring.svc:4317/",
		otelTimeoutSetting:     "750ms",
	}

	// when
	got, err := load(mapLookup(values))
	// then
	if err != nil {
		t.Fatalf("load overrides: %v", err)
	}

	if got.ServiceName != "copy-service" || got.GRPCAddress.String() != "[::1]:0" ||
		got.HealthAddress.String() != "localhost:0" {
		t.Errorf("unexpected identity or addresses: %+v", got)
	}
	if got.LogLevel != slog.LevelDebug+2 {
		t.Errorf("LogLevel = %v, want DEBUG+2", got.LogLevel)
	}
	if got.ShutdownTimeout != 15*time.Second ||
		got.Telemetry.ExporterTimeout != 750*time.Millisecond {
		t.Errorf("unexpected durations: %+v", got)
	}
	if !got.Telemetry.Enabled || got.Telemetry.ExporterEndpoint.String() !=
		"https://alloy.monitoring.svc:4317" {
		t.Errorf("unexpected telemetry: %+v", got.Telemetry)
	}
}

func TestLoad_RejectsInvalidSettingsWithoutEchoingValues(t *testing.T) {
	// given
	const privateMarker = "private-marker"
	tests := []struct {
		name    string
		values  map[string]string
		setting string
	}{
		{
			name:    "empty service",
			values:  map[string]string{serviceNameSetting: ""},
			setting: serviceNameSetting,
		},
		{
			name:    "empty grpc address",
			values:  map[string]string{grpcAddressSetting: ""},
			setting: grpcAddressSetting,
		},
		{
			name:    "malformed grpc address",
			values:  map[string]string{grpcAddressSetting: privateMarker},
			setting: grpcAddressSetting,
		},
		{
			name:    "malformed health address",
			values:  map[string]string{healthAddressSetting: "localhost"},
			setting: healthAddressSetting,
		},
		{
			name:    "empty log level",
			values:  map[string]string{logLevelSetting: ""},
			setting: logLevelSetting,
		},
		{
			name:    "unsupported log level",
			values:  map[string]string{logLevelSetting: privateMarker},
			setting: logLevelSetting,
		},
		{
			name:    "empty shutdown timeout",
			values:  map[string]string{shutdownTimeoutSetting: ""},
			setting: shutdownTimeoutSetting,
		},
		{
			name:    "invalid shutdown timeout",
			values:  map[string]string{shutdownTimeoutSetting: "later"},
			setting: shutdownTimeoutSetting,
		},
		{
			name:    "zero shutdown timeout",
			values:  map[string]string{shutdownTimeoutSetting: "0s"},
			setting: shutdownTimeoutSetting,
		},
		{
			name:    "empty telemetry flag",
			values:  map[string]string{otelEnabledSetting: ""},
			setting: otelEnabledSetting,
		},
		{
			name:    "invalid telemetry flag",
			values:  map[string]string{otelEnabledSetting: privateMarker},
			setting: otelEnabledSetting,
		},
		{
			name:    "empty exporter timeout",
			values:  map[string]string{otelTimeoutSetting: ""},
			setting: otelTimeoutSetting,
		},
		{
			name:    "negative exporter timeout",
			values:  map[string]string{otelTimeoutSetting: "-1s"},
			setting: otelTimeoutSetting,
		},
		{
			name:    "missing enabled endpoint",
			values:  map[string]string{otelEnabledSetting: "true"},
			setting: otelEndpointSetting,
		},
		{
			name: "empty enabled endpoint",
			values: map[string]string{
				otelEnabledSetting:  "true",
				otelEndpointSetting: "",
			},
			setting: otelEndpointSetting,
		},
		{
			name: "endpoint credentials",
			values: map[string]string{
				otelEnabledSetting:  "true",
				otelEndpointSetting: "http://user:" + privateMarker + "@127.0.0.1:4317",
			},
			setting: otelEndpointSetting,
		},
		{
			name: "endpoint HTTP trace path",
			values: map[string]string{
				otelEnabledSetting:  "true",
				otelEndpointSetting: "http://127.0.0.1:4317/v1/traces?token=" + privateMarker,
			},
			setting: otelEndpointSetting,
		},
		{
			name: "endpoint unsupported scheme",
			values: map[string]string{
				otelEnabledSetting:  "true",
				otelEndpointSetting: "ftp://" + privateMarker + ":4317",
			},
			setting: otelEndpointSetting,
		},
		{
			name: "endpoint missing port",
			values: map[string]string{
				otelEnabledSetting:  "true",
				otelEndpointSetting: "http://" + privateMarker,
			},
			setting: otelEndpointSetting,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// given
			lookup := mapLookup(test.values)

			// when
			_, err := load(lookup)

			// then
			if err == nil {
				t.Fatal("load returned nil error")
			}
			if !strings.Contains(err.Error(), test.setting) {
				t.Errorf("error %q does not name %s", err, test.setting)
			}
			if strings.Contains(err.Error(), privateMarker) {
				t.Errorf("error exposes private marker: %q", err)
			}
		})
	}
}

func TestLoad_AllowsEmptyEndpointWhenTelemetryIsDisabled(t *testing.T) {
	// given
	lookup := mapLookup(map[string]string{otelEndpointSetting: ""})

	// when
	got, err := load(lookup)
	// then
	if err != nil {
		t.Fatalf("load disabled telemetry: %v", err)
	}
	if got.Telemetry.ExporterEndpoint != nil {
		t.Errorf(
			"Telemetry.ExporterEndpoint = %v, want nil",
			got.Telemetry.ExporterEndpoint,
		)
	}
}

func mapLookup(values map[string]string) lookupEnv {
	return func(name string) (string, bool) {
		value, exists := values[name]
		return value, exists
	}
}
