package config

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	kitconfig "github.com/igor-baiborodine/insurance-hub/libs/servicekit/config"
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
	if got.HealthAddress.String() != "127.0.0.1:8081" {
		t.Errorf("HealthAddress = %q, want 127.0.0.1:8081", got.HealthAddress)
	}
	if got.LogLevel != slog.LevelInfo {
		t.Errorf("LogLevel = %v, want INFO", got.LogLevel)
	}
	if got.ShutdownTimeout != 10*time.Second {
		t.Errorf("ShutdownTimeout = %v, want 10s", got.ShutdownTimeout)
	}
	if got.RequestTimeout != 8*time.Second || got.ProbeTimeout != 2*time.Second {
		t.Errorf(
			"runtime timeouts = (%v, %v), want (8s, 2s)",
			got.RequestTimeout,
			got.ProbeTimeout,
		)
	}
	if got.MaxReceiveBytes != 1048576 || got.MaxSendBytes != 8388608 {
		t.Errorf("gRPC limits = %+v, want 1048576 receive and 8388608 send", got.GRPC)
	}
	if got.Enabled {
		t.Error("Telemetry.Enabled = true, want false")
	}
	if got.ExporterEndpoint != nil {
		t.Errorf(
			"Telemetry.ExporterEndpoint = %v, want nil",
			got.ExporterEndpoint,
		)
	}
	if got.ExporterTimeout != 2*time.Second {
		t.Errorf("Telemetry.ExporterTimeout = %v, want 2s", got.ExporterTimeout)
	}
}

func TestDevelopmentIdentity_PreservesScaffoldDefaults(t *testing.T) {
	// given
	serviceName := "copy-service"

	// when
	got := DevelopmentIdentity(serviceName)

	// then
	if got.ServiceNamespace != "insurance-hub" || got.ServiceName != serviceName ||
		got.ServiceVersion != "development" || got.Environment != "development" ||
		got.Implementation != "go" || got.ServiceInstance != fmt.Sprintf("local-%d", os.Getpid()) {
		t.Errorf("development identity = %+v", got.Identity)
	}
}

func TestLoad_GRPCFailurePreservesFirstErrorOrder(t *testing.T) {
	// given
	values := map[string]string{
		kitconfig.EnvGRPCMaxReceiveBytes: "16777217",
		kitconfig.EnvGRPCMaxSendBytes:    "0",
	}

	// when
	_, err := load(mapLookup(values))

	// then
	if err == nil {
		t.Fatal("invalid receive limit accepted")
	}
	setting, _ := kitconfig.ErrorDetails(err)
	if setting != "GRPC_MAX_RECV_BYTES" {
		t.Errorf("first error setting = %q, want GRPC_MAX_RECV_BYTES", setting)
	}
}

func TestLoad_ValidOverrides(t *testing.T) {
	// given
	values := map[string]string{
		kitconfig.EnvDeploymentEnvironment:    "test",
		kitconfig.EnvServiceVersion:           "test-revision",
		kitconfig.EnvServiceInstanceID:        "test-instance",
		kitconfig.EnvServiceName:              "copy-service",
		kitconfig.EnvGRPCAddress:              "[::1]:0",
		kitconfig.EnvHealthAddress:            "localhost:0",
		kitconfig.EnvLogLevel:                 "DEBUG+2",
		kitconfig.EnvShutdownTimeout:          "15s",
		kitconfig.EnvOTELEnabled:              "true",
		kitconfig.EnvOTELExporterOTLPEndpoint: "https://alloy.monitoring.svc:4317/",
		kitconfig.EnvOTELExporterOTLPTimeout:  "750ms",
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
		got.ExporterTimeout != 750*time.Millisecond {
		t.Errorf("unexpected durations: %+v", got)
	}
	if !got.Enabled || got.ExporterEndpoint.String() !=
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
			values:  map[string]string{kitconfig.EnvServiceName: ""},
			setting: kitconfig.EnvServiceName,
		},
		{
			name:    "empty grpc address",
			values:  map[string]string{kitconfig.EnvGRPCAddress: ""},
			setting: kitconfig.EnvGRPCAddress,
		},
		{
			name:    "malformed grpc address",
			values:  map[string]string{kitconfig.EnvGRPCAddress: privateMarker},
			setting: kitconfig.EnvGRPCAddress,
		},
		{
			name:    "malformed health address",
			values:  map[string]string{kitconfig.EnvHealthAddress: "localhost"},
			setting: kitconfig.EnvHealthAddress,
		},
		{
			name:    "empty log level",
			values:  map[string]string{kitconfig.EnvLogLevel: ""},
			setting: kitconfig.EnvLogLevel,
		},
		{
			name:    "unsupported log level",
			values:  map[string]string{kitconfig.EnvLogLevel: privateMarker},
			setting: kitconfig.EnvLogLevel,
		},
		{
			name:    "empty shutdown timeout",
			values:  map[string]string{kitconfig.EnvShutdownTimeout: ""},
			setting: kitconfig.EnvShutdownTimeout,
		},
		{
			name:    "invalid shutdown timeout",
			values:  map[string]string{kitconfig.EnvShutdownTimeout: "later"},
			setting: kitconfig.EnvShutdownTimeout,
		},
		{
			name:    "zero shutdown timeout",
			values:  map[string]string{kitconfig.EnvShutdownTimeout: "0s"},
			setting: kitconfig.EnvShutdownTimeout,
		},
		{
			name:    "empty telemetry flag",
			values:  map[string]string{kitconfig.EnvOTELEnabled: ""},
			setting: kitconfig.EnvOTELEnabled,
		},
		{
			name:    "invalid telemetry flag",
			values:  map[string]string{kitconfig.EnvOTELEnabled: privateMarker},
			setting: kitconfig.EnvOTELEnabled,
		},
		{
			name:    "empty exporter timeout",
			values:  map[string]string{kitconfig.EnvOTELExporterOTLPTimeout: ""},
			setting: kitconfig.EnvOTELExporterOTLPTimeout,
		},
		{
			name:    "negative exporter timeout",
			values:  map[string]string{kitconfig.EnvOTELExporterOTLPTimeout: "-1s"},
			setting: kitconfig.EnvOTELExporterOTLPTimeout,
		},
		{
			name:    "missing enabled endpoint",
			values:  map[string]string{kitconfig.EnvOTELEnabled: "true"},
			setting: kitconfig.EnvOTELExporterOTLPEndpoint,
		},
		{
			name: "empty enabled endpoint",
			values: map[string]string{
				kitconfig.EnvOTELEnabled:              "true",
				kitconfig.EnvOTELExporterOTLPEndpoint: "",
			},
			setting: kitconfig.EnvOTELExporterOTLPEndpoint,
		},
		{
			name: "endpoint credentials",
			values: map[string]string{
				kitconfig.EnvOTELEnabled:              "true",
				kitconfig.EnvOTELExporterOTLPEndpoint: "http://user:" + privateMarker + "@127.0.0.1:4317",
			},
			setting: kitconfig.EnvOTELExporterOTLPEndpoint,
		},
		{
			name: "endpoint HTTP trace path",
			values: map[string]string{
				kitconfig.EnvOTELEnabled:              "true",
				kitconfig.EnvOTELExporterOTLPEndpoint: "http://127.0.0.1:4317/v1/traces?token=" + privateMarker,
			},
			setting: kitconfig.EnvOTELExporterOTLPEndpoint,
		},
		{
			name: "endpoint unsupported scheme",
			values: map[string]string{
				kitconfig.EnvOTELEnabled:              "true",
				kitconfig.EnvOTELExporterOTLPEndpoint: "ftp://" + privateMarker + ":4317",
			},
			setting: kitconfig.EnvOTELExporterOTLPEndpoint,
		},
		{
			name: "endpoint missing port",
			values: map[string]string{
				kitconfig.EnvOTELEnabled:              "true",
				kitconfig.EnvOTELExporterOTLPEndpoint: "http://" + privateMarker,
			},
			setting: kitconfig.EnvOTELExporterOTLPEndpoint,
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
	lookup := mapLookup(map[string]string{kitconfig.EnvOTELExporterOTLPEndpoint: ""})

	// when
	got, err := load(lookup)
	// then
	if err != nil {
		t.Fatalf("load disabled telemetry: %v", err)
	}
	if got.ExporterEndpoint != nil {
		t.Errorf(
			"Telemetry.ExporterEndpoint = %v, want nil",
			got.ExporterEndpoint,
		)
	}
}

func mapLookup(values map[string]string) lookupEnv {
	return func(name string) (string, bool) {
		value, exists := values[name]
		return value, exists
	}
}

func TestLoad_RuntimeBoundsAndIdentity(t *testing.T) {
	// given
	valid := map[string]string{
		kitconfig.EnvOTELEnabled:              "true",
		kitconfig.EnvOTELExporterOTLPEndpoint: "http://127.0.0.1:4317",
		kitconfig.EnvDeploymentEnvironment:    "staging",
		kitconfig.EnvServiceVersion:           "sha-1234567",
		kitconfig.EnvServiceInstanceID:        "instance-123",
		kitconfig.EnvRequestTimeout:           "3s",
		kitconfig.EnvProbeTimeout:             "250ms",
		kitconfig.EnvGRPCMaxReceiveBytes:      "2048",
		kitconfig.EnvGRPCMaxSendBytes:         "4096",
	}
	cases := []struct{ name, setting, value string }{
		{"missing environment", kitconfig.EnvDeploymentEnvironment, ""},
		{"development environment", kitconfig.EnvDeploymentEnvironment, "development"},
		{"development version", kitconfig.EnvServiceVersion, "development"},
		{"local instance", kitconfig.EnvServiceInstanceID, "local-123"},
		{"padded service", kitconfig.EnvServiceName, " service "},
		{"control character", kitconfig.EnvServiceInstanceID, "private-marker\n"},
		{"zero request timeout", kitconfig.EnvRequestTimeout, "0s"},
		{"negative probe timeout", kitconfig.EnvProbeTimeout, "-1s"},
		{"oversized receive limit", kitconfig.EnvGRPCMaxReceiveBytes, "16777217"},
		{"zero send limit", kitconfig.EnvGRPCMaxSendBytes, "0"},
		{"export exceeds shutdown", kitconfig.EnvShutdownTimeout, "1s"},
	}
	// when
	got, err := load(mapLookup(valid))
	// then
	if err != nil {
		t.Fatal(err)
	}
	if got.Identity.Environment != "staging" || got.RequestTimeout != 3*time.Second ||
		got.ProbeTimeout != 250*time.Millisecond || got.MaxReceiveBytes != 2048 || got.MaxSendBytes != 4096 {
		t.Fatalf("unexpected runtime configuration: %+v", got)
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			// given
			values := make(map[string]string, len(valid)+1)
			for key, value := range valid {
				values[key] = value
			}
			values[test.setting] = test.value
			// when
			_, err := load(mapLookup(values))
			// then
			if err == nil || strings.Contains(err.Error(), "private-marker") {
				t.Fatalf("unsafe or absent rejection: %v", err)
			}
		})
	}
}

func TestErrorDetails_UsesOnlyValidatorOwnedFields(t *testing.T) {
	// given
	_, validation := load(mapLookup(map[string]string{kitconfig.EnvRequestTimeout: "0s"}))
	if validation == nil {
		t.Fatal("invalid duration accepted")
	}
	cases := []struct {
		name            string
		err             error
		setting, reason string
	}{
		{"invalid duration", validation, "REQUEST_TIMEOUT", "must be a positive duration"},
		{
			"wrapped validation",
			fmt.Errorf("private-wrapper: %w", validation),
			"REQUEST_TIMEOUT",
			"must be a positive duration",
		},
		{
			"unknown error",
			errors.New("private-marker"),
			"unknown",
			"could not load configuration",
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			// when
			setting, reason := kitconfig.ErrorDetails(test.err)
			// then
			if setting != test.setting || reason != test.reason {
				t.Errorf(
					"safe details = (%q, %q), want (%q, %q)",
					setting,
					reason,
					test.setting,
					test.reason,
				)
			}
		})
	}
}
