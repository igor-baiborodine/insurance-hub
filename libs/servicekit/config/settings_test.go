package config_test

import (
	"log/slog"
	"net/url"
	"testing"
	"time"

	"github.com/igor-baiborodine/insurance-hub/libs/servicekit/config"
	"github.com/igor-baiborodine/insurance-hub/libs/servicekit/identity"
)

func TestDefaults_RetainsIndependentConsumerBaseline(t *testing.T) {
	// given: values retained from the original consumer config tests.
	wantRuntime := config.Runtime{
		RequestTimeout:  8 * time.Second,
		ProbeTimeout:    2 * time.Second,
		ShutdownTimeout: 10 * time.Second,
		LogLevel:        slog.LevelInfo,
	}
	wantGRPC := config.GRPC{MaxReceiveBytes: 1048576, MaxSendBytes: 8388608}
	wantHTTP := config.HTTPServer{
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       30 * time.Second,
	}
	// when
	runtime, grpc, http, telemetry := config.DefaultRuntime(), config.DefaultGRPC(), config.DefaultHTTPServer(), config.DefaultTelemetry()
	// then
	if runtime != wantRuntime || grpc != wantGRPC || http != wantHTTP {
		t.Fatalf("unexpected defaults: %+v %+v %+v", runtime, grpc, http)
	}
	if telemetry.Enabled || telemetry.ExporterEndpoint != nil ||
		telemetry.ExporterTimeout != 2*time.Second ||
		telemetry.Identity != (identity.Identity{}) {
		t.Fatalf("unexpected telemetry defaults: %+v", telemetry)
	}
	if err := config.ValidateRuntime(runtime); err != nil {
		t.Fatal(err)
	}
	if err := config.ValidateTelemetry(telemetry); err != nil {
		t.Fatal(err)
	}
}

func TestValidateRuntime_RejectsNonpositiveDurations(t *testing.T) {
	// given
	for _, test := range []struct {
		name string
		edit func(*config.Runtime, time.Duration)
	}{
		{"REQUEST_TIMEOUT", func(s *config.Runtime, v time.Duration) { s.RequestTimeout = v }},
		{"PROBE_TIMEOUT", func(s *config.Runtime, v time.Duration) { s.ProbeTimeout = v }},
		{"SHUTDOWN_TIMEOUT", func(s *config.Runtime, v time.Duration) { s.ShutdownTimeout = v }},
	} {
		t.Run(test.name, func(t *testing.T) {
			for _, value := range []time.Duration{0, -time.Second} {
				// given
				settings := config.DefaultRuntime()
				test.edit(&settings, value)
				// when
				err := config.ValidateRuntime(settings)
				// then
				assertSafeError(t, err, test.name)
			}
		})
	}
}

func TestValidateTelemetry_LeavesIdentityAndTimeoutPolicyLocal(t *testing.T) {
	// given: scaffold permits an explicit non-development identity and 750ms export;
	// Product QA permits a SHA version and Pod UID. Neither policy becomes universal.
	for _, id := range []identity.Identity{
		{ServiceNamespace: "insurance-hub", ServiceName: "copy-service", Environment: "production", ServiceVersion: "v1.2.3", ServiceInstance: "copy-instance", Implementation: "go"},
		{ServiceNamespace: "insurance-hub", ServiceName: "product-service", Environment: "qa", ServiceVersion: "sha-abcdef0", ServiceInstance: "123e4567-e89b-12d3-a456-426614174000", Implementation: "go"},
	} {
		settings := config.Telemetry{
			Enabled:          true,
			ExporterEndpoint: &url.URL{Scheme: "http", Host: "collector:4317"},
			ExporterTimeout:  750 * time.Millisecond,
			Identity:         id,
		}
		// when
		err := config.ValidateTelemetry(settings)
		// then
		if err != nil {
			t.Fatalf("neutral validation imposed service policy: %v", err)
		}
		if settings.Identity != id {
			t.Fatal("identity was changed")
		}
	}
}

func TestValidateTelemetry_RejectsInvalidMechanics(t *testing.T) {
	// given
	for _, test := range []struct {
		name     string
		settings config.Telemetry
		setting  string
	}{
		{"zero timeout", config.Telemetry{}, "OTEL_EXPORTER_OTLP_TIMEOUT"},
		{"negative timeout", config.Telemetry{ExporterTimeout: -time.Second}, "OTEL_EXPORTER_OTLP_TIMEOUT"},
		{"enabled missing endpoint", config.Telemetry{Enabled: true, ExporterTimeout: time.Second}, "OTEL_EXPORTER_OTLP_ENDPOINT"},
		{"disabled unsafe endpoint", config.Telemetry{ExporterTimeout: time.Second, ExporterEndpoint: &url.URL{Scheme: "http", Host: "collector:4317", User: url.UserPassword("user", "secret-marker")}}, "OTEL_EXPORTER_OTLP_ENDPOINT"},
	} {
		t.Run(test.name, func(t *testing.T) {
			// when
			err := config.ValidateTelemetry(test.settings)
			// then
			assertSafeError(t, err, test.setting)
		})
	}
}

// An embedding caller supplies its own Validate method and explicitly invokes the
// common validator first. Runtime has no promoted Validate method to bypass policy.
type localSettings struct {
	config.Runtime
	requiredValue string
}

func (s localSettings) Validate() error {
	if err := config.ValidateRuntime(s.Runtime); err != nil {
		return err
	}
	if s.requiredValue == "" {
		return config.Invalid("LOCAL_REQUIRED", "is required")
	}
	return nil
}

func TestComposition_InvokesCommonAndLocalValidation(t *testing.T) {
	// given
	for _, test := range []struct {
		name     string
		settings localSettings
		setting  string
	}{
		{"missing local value", localSettings{Runtime: config.DefaultRuntime()}, "LOCAL_REQUIRED"},
		{"missing common values", localSettings{requiredValue: "present"}, "REQUEST_TIMEOUT"},
		{"valid", localSettings{Runtime: config.DefaultRuntime(), requiredValue: "present"}, ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			// when
			err := test.settings.Validate()
			// then
			if test.setting != "" {
				assertSafeError(t, err, test.setting)
			} else if err != nil {
				t.Fatal(err)
			}
		})
	}
}
