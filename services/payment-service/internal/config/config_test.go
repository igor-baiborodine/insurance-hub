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

	if got.ServiceName != "payment-service" {
		t.Errorf("ServiceName = %q, want payment-service", got.ServiceName)
	}
	if got.GRPCAddress.String() != "127.0.0.1:9090" {
		t.Errorf("GRPCAddress = %q, want 127.0.0.1:9090", got.GRPCAddress)
	}
	if got.HTTPAddress.String() != "127.0.0.1:8080" {
		t.Errorf("HTTPAddress = %q, want 127.0.0.1:8080", got.HTTPAddress)
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
	if got.HTTPMaxHeaderBytes != 16384 || got.HTTPMaxBodyBytes != 4096 ||
		got.ReadHeaderTimeout != 5*time.Second || got.ReadTimeout != 10*time.Second ||
		got.WriteTimeout != 10*time.Second || got.IdleTimeout != 30*time.Second {
		t.Errorf("HTTP settings = %+v", got)
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

func TestLoad_ServiceNamePolicy(t *testing.T) {
	t.Run("explicit fixed name accepted", func(t *testing.T) {
		// given
		lookup := mapLookup(map[string]string{kitconfig.EnvServiceName: DefaultServiceName})

		// when
		got, err := load(lookup)
		// then
		if err != nil {
			t.Fatalf("load fixed service name: %v", err)
		}
		if got.ServiceName != DefaultServiceName {
			t.Errorf("ServiceName = %q, want %q", got.ServiceName, DefaultServiceName)
		}
	})

	t.Run("foreign name rejected safely", func(t *testing.T) {
		// given
		const foreignName = "foreign-service"
		lookup := mapLookup(map[string]string{kitconfig.EnvServiceName: foreignName})

		// when
		_, err := load(lookup)

		// then
		if err == nil {
			t.Fatal("foreign service name accepted")
		}
		setting, reason := kitconfig.ErrorDetails(err)
		if setting != kitconfig.EnvServiceName ||
			reason != "must match the service's fixed logical name" {
			t.Errorf("safe details = (%q, %q)", setting, reason)
		}
		if strings.Contains(err.Error(), foreignName) {
			t.Errorf("error exposes foreign service name: %q", err)
		}
	})
}

func TestLoad_RejectsHTTPBoundsRelationshipsAndListenerCollisions(t *testing.T) {
	// given
	tests := []struct {
		name    string
		values  map[string]string
		setting string
	}{
		{
			"header bound",
			map[string]string{kitconfig.EnvHTTPMaxHeaderBytes: "1023"},
			kitconfig.EnvHTTPMaxHeaderBytes,
		},
		{
			"body bound",
			map[string]string{kitconfig.EnvHTTPMaxBodyBytes: "1048577"},
			kitconfig.EnvHTTPMaxBodyBytes,
		},
		{
			"header after read",
			map[string]string{kitconfig.EnvHTTPReadHeaderTimeout: "11s"},
			kitconfig.EnvHTTPReadHeaderTimeout,
		},
		{
			"read after write",
			map[string]string{kitconfig.EnvHTTPReadTimeout: "11s"},
			kitconfig.EnvHTTPReadTimeout,
		},
		{
			"request reaches write",
			map[string]string{kitconfig.EnvRequestTimeout: "10s"},
			kitconfig.EnvRequestTimeout,
		},
		{
			"shutdown omits exporter reserve",
			map[string]string{kitconfig.EnvShutdownTimeout: "9s"},
			kitconfig.EnvShutdownTimeout,
		},
		{
			"HTTP and gRPC collide",
			map[string]string{kitconfig.EnvHTTPAddress: "127.0.0.1:9090"},
			kitconfig.EnvGRPCAddress,
		},
		{
			"HTTP and health collide",
			map[string]string{kitconfig.EnvHTTPAddress: "127.0.0.1:8081"},
			kitconfig.EnvHealthAddress,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// when
			_, err := load(mapLookup(test.values))
			// then
			if err == nil {
				t.Fatal("invalid settings accepted")
			}
			setting, _ := kitconfig.ErrorDetails(err)
			if setting != test.setting {
				t.Errorf("setting = %q, want %q: %v", setting, test.setting, err)
			}
		})
	}
	// given
	values := map[string]string{
		kitconfig.EnvHTTPAddress:   "127.0.0.1:0",
		kitconfig.EnvGRPCAddress:   "127.0.0.1:0",
		kitconfig.EnvHealthAddress: "127.0.0.1:0",
	}
	// when
	_, err := load(mapLookup(values))
	// then
	if err != nil {
		t.Fatalf("port-zero listeners rejected: %v", err)
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

func TestLoad_NonDevelopmentVersionPolicy(t *testing.T) {
	t.Run("canonical test identity accepted", func(t *testing.T) {
		// given
		lookup := mapLookup(map[string]string{
			kitconfig.EnvDeploymentEnvironment: "test",
			kitconfig.EnvServiceVersion:        "git-000000000001",
			kitconfig.EnvServiceInstanceID:     "test-instance",
		})

		// when
		got, err := load(lookup)
		// then
		if err != nil {
			t.Fatalf("load canonical test identity: %v", err)
		}
		if got.Identity.Environment != "test" ||
			got.Identity.ServiceVersion != "git-000000000001" ||
			got.Identity.ServiceInstance != "test-instance" {
			t.Errorf("unexpected test identity: %+v", got.Identity)
		}
	})

	t.Run("development environment is exempt", func(t *testing.T) {
		// given
		lookup := mapLookup(map[string]string{
			kitconfig.EnvDeploymentEnvironment: "development",
			kitconfig.EnvServiceVersion:        "local-revision",
			kitconfig.EnvServiceInstanceID:     "synthetic-instance",
		})

		// when
		_, err := load(lookup)
		// then
		if err != nil {
			t.Fatalf("development version override rejected: %v", err)
		}
	})

	for _, test := range []struct{ name, version, reason string }{
		{"old prefix", "sha-123456789abc", canonicalVersionReason},
		{"short suffix", "git-123456789ab", canonicalVersionReason},
		{"long suffix", "git-123456789abcd", canonicalVersionReason},
		{"uppercase hex", "git-ABCDEF123456", canonicalVersionReason},
		{"nonhex suffix", "git-123456789abg", canonicalVersionReason},
		{
			"padded version",
			" git-123456789abc",
			"must contain 1 through 128 ASCII letters, digits, dots, underscores or hyphens",
		},
		{"zero version", "git-000000000000", canonicalVersionReason},
	} {
		t.Run(test.name, func(t *testing.T) {
			// given
			lookup := mapLookup(map[string]string{
				kitconfig.EnvDeploymentEnvironment: "test",
				kitconfig.EnvServiceVersion:        test.version,
				kitconfig.EnvServiceInstanceID:     "test-instance",
			})

			// when
			_, err := load(lookup)

			// then
			if err == nil {
				t.Fatal("invalid non-development version accepted")
			}
			setting, reason := kitconfig.ErrorDetails(err)
			if setting != kitconfig.EnvServiceVersion || reason != test.reason {
				t.Errorf("safe details = (%q, %q)", setting, reason)
			}
			if strings.Contains(err.Error(), test.version) {
				t.Errorf("error exposes rejected version: %q", err)
			}
		})
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
		kitconfig.EnvServiceVersion:           "git-000000000001",
		kitconfig.EnvServiceInstanceID:        "test-instance",
		kitconfig.EnvServiceName:              DefaultServiceName,
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

	if got.ServiceName != DefaultServiceName || got.GRPCAddress.String() != "[::1]:0" ||
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

func TestLoad_RejectsInvalidSettingsWithoutExposingValues(t *testing.T) {
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

// mapLookup overlays test values on valid dependency settings so that each test varies only
// the settings it names.
func mapLookup(values map[string]string) lookupEnv {
	return func(name string) (string, bool) {
		if value, exists := values[name]; exists {
			return value, true
		}
		value, exists := validDependencySettings()[name]
		return value, exists
	}
}

func TestLoad_RuntimeBoundsAndIdentity(t *testing.T) {
	// given
	valid := map[string]string{
		kitconfig.EnvOTELEnabled:              "true",
		kitconfig.EnvOTELExporterOTLPEndpoint: "http://127.0.0.1:4317",
		kitconfig.EnvDeploymentEnvironment:    "staging",
		kitconfig.EnvServiceVersion:           "git-123456789abc",
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

const sensitiveMarker = "sensitive-marker-7f3a"

func validDependencySettings() map[string]string {
	return map[string]string{
		databaseURLSetting: "postgres://go_payment_writer:" + sensitiveMarker +
			"@postgres-rw.payment.svc:5432/payment?sslmode=verify-full" +
			"&sslrootcert=/etc/payment-service/postgres/ca.crt",
		objectEndpointSetting:  "https://minio.payment.svc:9000",
		objectAccessKeySetting: "payment-access-" + sensitiveMarker,
		objectSecretKeySetting: "payment-secret-" + sensitiveMarker,
		objectCAFileSetting:    "/etc/payment-service/minio/ca.crt",
		kafkaBrokersSetting:    "kafka-bootstrap.data.svc:9093, kafka-1.data.svc:9093",
		kafkaCAFileSetting:     "/etc/payment-service/kafka/ca.crt",
	}
}

func TestLoad_DependencySettings(t *testing.T) {
	// given
	lookup := mapLookup(nil)

	// when
	got, err := load(lookup)
	// then
	if err != nil {
		t.Fatalf("load dependency settings: %v", err)
	}
	if got.StartupTimeout != 10*time.Second {
		t.Errorf("StartupTimeout = %v, want 10s", got.StartupTimeout)
	}
	if got.Database.MaxConnections != 4 || got.Database.ConnectTimeout != 3*time.Second ||
		got.Database.QueryTimeout != time.Second {
		t.Errorf("database limits = %+v", got.Database)
	}
	if got.ObjectStore.Endpoint.String() != "https://minio.payment.svc:9000" ||
		got.ObjectStore.Bucket != "payments-import" ||
		got.ObjectStore.FetchTimeout != 3*time.Second ||
		got.ObjectStore.MaximumStatementBytes != 1024*1024 {
		t.Errorf(
			"object store = (%s, %q, %v, %d)",
			got.ObjectStore.Endpoint,
			got.ObjectStore.Bucket,
			got.ObjectStore.FetchTimeout,
			got.ObjectStore.MaximumStatementBytes,
		)
	}
	wantBrokers := []string{"kafka-bootstrap.data.svc:9093", "kafka-1.data.svc:9093"}
	if fmt.Sprint(got.Kafka.Brokers) != fmt.Sprint(wantBrokers) ||
		got.Kafka.RegisteredTopic != "policy-registered" || got.Kafka.Consume ||
		got.Kafka.Group != "" {
		t.Errorf("kafka = %+v", got.Kafka)
	}
	for _, format := range []string{"%v", "%+v", "%#v"} {
		if formatted := fmt.Sprintf(
			format,
			got,
		); strings.Contains(
			formatted,
			sensitiveMarker,
		) {
			t.Errorf("formatting with %s exposes a credential", format)
		}
	}
}

func TestLoad_EventWorkerMode(t *testing.T) {
	t.Run("explicit consumption uses serving identity", func(t *testing.T) {
		// given
		lookup := mapLookup(map[string]string{eventWorkersSetting: "true"})

		// when
		got, err := load(lookup)
		// then
		if err != nil {
			t.Fatal(err)
		}
		if !got.Kafka.Consume || got.Kafka.Group != "payment-service" {
			t.Fatalf("Kafka mode = %+v", got.Kafka)
		}
	})

	for _, test := range []struct {
		name   string
		values map[string]string
	}{
		{
			name: "foreign serving group",
			values: map[string]string{
				eventWorkersSetting: "true", kafkaGroupSetting: "qualification-group",
			},
		},
		{
			name:   "disabled mode group",
			values: map[string]string{kafkaGroupSetting: "payment-service"},
		},
		{
			name:   "invalid mode",
			values: map[string]string{eventWorkersSetting: "sometimes"},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			// when
			_, err := load(mapLookup(test.values))

			// then
			if err == nil {
				t.Fatal("invalid event worker mode accepted")
			}
		})
	}
}

func TestLoad_AllowsPlainHTTPObjectStoreWithoutCA(t *testing.T) {
	// given
	lookup := mapLookup(map[string]string{
		objectEndpointSetting: "http://minio.payment.svc:9000",
		objectCAFileSetting:   "",
	})

	// when
	got, err := load(lookup)
	// then
	if err != nil {
		t.Fatalf("load HTTP object store: %v", err)
	}
	if got.ObjectStore.Endpoint.Scheme != "http" {
		t.Errorf("scheme = %q, want http", got.ObjectStore.Endpoint.Scheme)
	}
}

func TestLoad_RejectsInvalidDependencySettingsWithoutExposingValues(t *testing.T) {
	// given
	validURL := validDependencySettings()[databaseURLSetting]
	tests := []struct {
		name    string
		values  map[string]string
		setting string
		reason  string
	}{
		{
			"missing database URL",
			map[string]string{databaseURLSetting: ""},
			databaseURLSetting, "is required",
		},
		{
			"unverified database TLS",
			map[string]string{
				databaseURLSetting: strings.Replace(
					validURL,
					"verify-full",
					"require",
					1,
				),
			},
			databaseURLSetting,
			"must require TLS hostname and CA verification",
		},
		{
			"foreign database",
			map[string]string{
				databaseURLSetting: strings.Replace(
					validURL,
					"/payment?",
					"/policy?",
					1,
				),
			},
			databaseURLSetting,
			"must identify the Payment database",
		},
		{
			"missing object secret key",
			map[string]string{objectSecretKeySetting: ""},
			objectSecretKeySetting, "is required",
		},
		{
			"object endpoint with credentials",
			map[string]string{
				objectEndpointSetting: "https://user:" + sensitiveMarker + "@minio:9000",
			},
			objectEndpointSetting,
			"must be an HTTP or HTTPS origin URL",
		},
		{
			"HTTPS object store without CA",
			map[string]string{objectCAFileSetting: ""},
			objectCAFileSetting, "is required for HTTPS object storage",
		},
		{
			"foreign bucket",
			map[string]string{objectBucketSetting: "policies"},
			objectBucketSetting, "must match the fixed payments-import bucket",
		},
		{
			"statement size above maximum",
			map[string]string{objectStatementMaxBytes: "8388609"},
			objectStatementMaxBytes, "must be an integer from 1 through 8388608",
		},
		{
			"missing brokers",
			map[string]string{kafkaBrokersSetting: ""},
			kafkaBrokersSetting, "is required",
		},
		{
			"broker without port",
			map[string]string{kafkaBrokersSetting: "kafka-bootstrap.data.svc"},
			kafkaBrokersSetting, "must contain comma-separated host:port brokers",
		},
		{
			"foreign topic",
			map[string]string{kafkaRegisteredSetting: "policy-terminated"},
			kafkaRegisteredSetting, "must select the Policy registration topic",
		},
		{
			"missing Kafka CA",
			map[string]string{kafkaCAFileSetting: ""},
			kafkaCAFileSetting, "is required",
		},
		{
			"zero startup timeout",
			map[string]string{startupTimeoutSetting: "0s"},
			startupTimeoutSetting, "must be a positive duration",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// when
			_, err := load(mapLookup(test.values))

			// then
			if err == nil {
				t.Fatal("invalid dependency setting accepted")
			}
			setting, reason := kitconfig.ErrorDetails(err)
			if setting != test.setting || reason != test.reason {
				t.Errorf(
					"safe details = (%q, %q), want (%q, %q)",
					setting, reason, test.setting, test.reason,
				)
			}
			if strings.Contains(fmt.Sprintf("%v %+v", err, err), sensitiveMarker) {
				t.Error("error exposes a sensitive value")
			}
		})
	}
}
