package config

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	kitconfig "github.com/igor-baiborodine/insurance-hub/libs/servicekit/config"
)

func mapLookup(values map[string]string) lookupEnv {
	return func(key string) (string, bool) { value, ok := values[key]; return value, ok }
}

func TestLoad_DefaultsForHTTPOnlyService(t *testing.T) {
	got, err := load(mapLookup(nil))
	if err != nil {
		t.Fatal(err)
	}
	if got.ServiceName != DefaultServiceName ||
		got.HTTPAddress.String() != kitconfig.DefaultHTTPAddress ||
		got.HealthAddress.String() != kitconfig.DefaultHealthAddress {
		t.Errorf("unexpected service identity or addresses: %+v", got)
	}
	if got.LogLevel != slog.LevelInfo || got.RequestTimeout != 8*time.Second ||
		got.ProbeTimeout != 2*time.Second || got.ShutdownTimeout != 10*time.Second {
		t.Errorf("unexpected runtime defaults: %+v", got.Runtime)
	}
	if got.Enabled || got.ExporterEndpoint != nil {
		t.Errorf("telemetry unexpectedly enabled: %+v", got.Telemetry)
	}
	if got.HTTPServer != kitconfig.DefaultHTTPServer() {
		t.Errorf("unexpected HTTP timeouts: %+v", got.HTTPServer)
	}
	if got.Elasticsearch.URL != "http://127.0.0.1:9200" ||
		got.Elasticsearch.Timeout != 5*time.Second || got.StartupTimeout != 10*time.Second {
		t.Errorf("unexpected Elasticsearch/startup defaults: %+v", got)
	}
}

func TestLoad_AcceptsTypedElasticsearchSettingsAndRedactsPassword(t *testing.T) {
	certificatePath := filepath.Join(t.TempDir(), "ca.crt")
	if err := os.WriteFile(certificatePath, []byte("certificate"), 0o600); err != nil {
		t.Fatal(err)
	}
	const password = "private-password-marker"
	got, err := load(mapLookup(map[string]string{
		elasticsearchURLSetting:        "https://elastic.example.test:9200",
		elasticsearchUsernameSetting:   "policy-reader",
		elasticsearchPasswordSetting:   password,
		elasticsearchCACertFileSetting: certificatePath,
		elasticsearchTimeoutSetting:    "3s",
		startupTimeoutSetting:          "7s",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if got.Elasticsearch.Username != "policy-reader" ||
		got.Elasticsearch.Password.Value() != password ||
		string(got.Elasticsearch.CACert) != "certificate" ||
		got.Elasticsearch.Timeout != 3*time.Second || got.StartupTimeout != 7*time.Second {
		t.Fatalf("unexpected Elasticsearch settings: %+v", got.Elasticsearch)
	}
	if strings.Contains(fmt.Sprintf("%+v", got), password) ||
		strings.Contains(got.Elasticsearch.Password.String(), password) {
		t.Fatal("configuration formatting exposed the Elasticsearch password")
	}
}

func TestLoad_RejectsInvalidElasticsearchSettingsWithoutEchoingInput(t *testing.T) {
	const private = "private-marker"
	missingPath := filepath.Join(t.TempDir(), private)
	certificatePath := filepath.Join(t.TempDir(), "ca.crt")
	if err := os.WriteFile(certificatePath, []byte("certificate"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name    string
		values  map[string]string
		setting string
	}{
		{"credential in URL", map[string]string{
			elasticsearchURLSetting: "https://user:" + private + "@example.test:9200",
		}, elasticsearchURLSetting},
		{"URL path", map[string]string{
			elasticsearchURLSetting: "https://example.test:9200/" + private,
		}, elasticsearchURLSetting},
		{"username only", map[string]string{
			elasticsearchUsernameSetting: private,
		}, elasticsearchUsernameSetting},
		{"password only", map[string]string{
			elasticsearchPasswordSetting: private,
		}, elasticsearchUsernameSetting},
		{"missing CA file", map[string]string{
			elasticsearchURLSetting:        "https://example.test:9200",
			elasticsearchCACertFileSetting: missingPath,
		}, elasticsearchCACertFileSetting},
		{"CA with HTTP", map[string]string{
			elasticsearchCACertFileSetting: certificatePath,
		}, elasticsearchCACertFileSetting},
		{"zero timeout", map[string]string{
			elasticsearchTimeoutSetting: "0s",
		}, elasticsearchTimeoutSetting},
		{"zero startup timeout", map[string]string{
			startupTimeoutSetting: "0s",
		}, startupTimeoutSetting},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := load(mapLookup(test.values))
			if err == nil {
				t.Fatal("invalid Elasticsearch configuration accepted")
			}
			setting, _ := kitconfig.ErrorDetails(err)
			if setting != test.setting {
				t.Fatalf("setting = %q, want %q: %v", setting, test.setting, err)
			}
			if strings.Contains(err.Error(), private) {
				t.Fatalf("configuration error exposed private input: %v", err)
			}
		})
	}
}

func TestLoad_AcceptsEphemeralHTTPAndTelemetryIdentity(t *testing.T) {
	got, err := load(mapLookup(map[string]string{
		kitconfig.EnvServiceName:              DefaultServiceName,
		httpAddressSetting:                    "127.0.0.1:0",
		kitconfig.EnvHealthAddress:            "localhost:0",
		kitconfig.EnvOTELEnabled:              "true",
		kitconfig.EnvOTELExporterOTLPEndpoint: "https://alloy.example.test:4317",
		kitconfig.EnvDeploymentEnvironment:    "qa",
		kitconfig.EnvServiceVersion:           "sha-6c4669c81d3a",
		kitconfig.EnvServiceInstanceID:        "123e4567-e89b-42d3-a456-426614174000",
		kitconfig.EnvHTTPReadHeaderTimeout:    "750ms",
		kitconfig.EnvHTTPReadTimeout:          "11s",
		kitconfig.EnvHTTPWriteTimeout:         "12s",
		kitconfig.EnvHTTPIdleTimeout:          "31s",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if got.ServiceName != DefaultServiceName || got.HTTPAddress.Port() != 0 ||
		got.HealthAddress.Port() != 0 || !got.Enabled || got.Identity.ServiceName != DefaultServiceName {
		t.Errorf("unexpected override result: %+v", got)
	}
	if got.HTTPServer != (kitconfig.HTTPServer{
		ReadHeaderTimeout: 750 * time.Millisecond,
		ReadTimeout:       11 * time.Second,
		WriteTimeout:      12 * time.Second,
		IdleTimeout:       31 * time.Second,
	}) {
		t.Errorf("unexpected HTTP timeout overrides: %+v", got.HTTPServer)
	}
}

func TestLoad_RejectsInvalidHTTPSettingsWithoutEchoingInput(t *testing.T) {
	const privateMarker = "private-marker"
	for _, test := range []struct {
		name    string
		values  map[string]string
		setting string
	}{
		{"empty", map[string]string{httpAddressSetting: ""}, httpAddressSetting},
		{"malformed", map[string]string{httpAddressSetting: privateMarker}, httpAddressSetting},
		{"same address", map[string]string{httpAddressSetting: "127.0.0.1:8081"}, kitconfig.EnvHealthAddress},
		{"zero request timeout", map[string]string{kitconfig.EnvRequestTimeout: "0s"}, kitconfig.EnvRequestTimeout},
		{"zero HTTP read timeout", map[string]string{kitconfig.EnvHTTPReadTimeout: "0s"}, kitconfig.EnvHTTPReadTimeout},
		{"header exceeds read timeout", map[string]string{kitconfig.EnvHTTPReadHeaderTimeout: "11s"}, kitconfig.EnvHTTPReadHeaderTimeout},
		{"request exceeds write timeout", map[string]string{kitconfig.EnvRequestTimeout: "11s"}, kitconfig.EnvRequestTimeout},
		{"missing telemetry identity", map[string]string{
			kitconfig.EnvOTELEnabled:              "true",
			kitconfig.EnvOTELExporterOTLPEndpoint: "https://alloy.example.test:4317",
		}, kitconfig.EnvDeploymentEnvironment},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := load(mapLookup(test.values))
			if err == nil {
				t.Fatal("invalid configuration accepted")
			}
			setting, _ := kitconfig.ErrorDetails(err)
			if setting != test.setting {
				t.Errorf("setting = %q, want %q", setting, test.setting)
			}
			if strings.Contains(err.Error(), privateMarker) {
				t.Errorf("private value leaked: %v", err)
			}
		})
	}
}

func TestLoad_GeneratesTestInstanceUsingProductIdentityPolicy(t *testing.T) {
	values := map[string]string{
		kitconfig.EnvOTELEnabled:              "true",
		kitconfig.EnvOTELExporterOTLPEndpoint: "http://127.0.0.1:4317",
		kitconfig.EnvDeploymentEnvironment:    "test",
		kitconfig.EnvServiceVersion:           "test-local-run",
	}

	first, firstErr := load(mapLookup(values))
	second, secondErr := load(mapLookup(values))

	if firstErr != nil || secondErr != nil {
		t.Fatalf("load test identities: first=%v second=%v", firstErr, secondErr)
	}
	firstID := first.Identity.ServiceInstance
	secondID := second.Identity.ServiceInstance
	if len(firstID) != 36 || len(secondID) != 36 || firstID == secondID ||
		firstID[14] != '4' || secondID[14] != '4' {
		t.Errorf("test instances are not distinct UUIDv4 values: %q, %q", firstID, secondID)
	}
}

func TestLoad_RejectsSuppliedTestInstance(t *testing.T) {
	_, err := load(mapLookup(map[string]string{
		kitconfig.EnvOTELEnabled:              "true",
		kitconfig.EnvOTELExporterOTLPEndpoint: "http://127.0.0.1:4317",
		kitconfig.EnvDeploymentEnvironment:    "test",
		kitconfig.EnvServiceVersion:           "test-local-run",
		kitconfig.EnvServiceInstanceID:        "123e4567-e89b-42d3-a456-426614174000",
	}))

	setting, _ := kitconfig.ErrorDetails(err)
	if setting != kitconfig.EnvServiceInstanceID {
		t.Errorf("setting = %q, want %q", setting, kitconfig.EnvServiceInstanceID)
	}
}
