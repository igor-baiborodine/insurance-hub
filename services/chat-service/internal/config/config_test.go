package config

import (
	"log/slog"
	"strings"
	"testing"
	"time"

	kitconfig "github.com/igor-baiborodine/insurance-hub/libs/servicekit/config"
)

func TestLoad_Defaults(t *testing.T) {
	// when
	settings, err := load(mapLookup(nil))
	// then
	if err != nil {
		t.Fatal(err)
	}
	if settings.ServiceName != DefaultServiceName {
		t.Errorf("ServiceName = %q", settings.ServiceName)
	}
	if settings.HTTPAddress.String() != "127.0.0.1:8080" ||
		settings.HealthAddress.String() != "127.0.0.1:8081" {
		t.Errorf("addresses = (%s, %s)", settings.HTTPAddress, settings.HealthAddress)
	}
	if settings.ProbeTimeout != 2*time.Second || settings.ShutdownTimeout != 10*time.Second {
		t.Errorf(
			"lifecycle budgets = (%s, %s)",
			settings.ProbeTimeout,
			settings.ShutdownTimeout,
		)
	}
	if settings.LogLevel != slog.LevelInfo || settings.Enabled ||
		settings.ExporterTimeout != 2*time.Second {
		t.Errorf(
			"logging/telemetry defaults = (%s, %t, %s)",
			settings.LogLevel,
			settings.Enabled,
			settings.ExporterTimeout,
		)
	}
	if settings.HTTPMaxHeaderBytes != 16384 || settings.ReadHeaderTimeout != 5*time.Second ||
		settings.ReadTimeout != 10*time.Second || settings.WriteTimeout != 10*time.Second ||
		settings.IdleTimeout != 30*time.Second {
		t.Errorf("HTTP defaults = %+v", settings)
	}
}

func TestLoad_InvalidSettingsDoNotDiscloseValues(t *testing.T) {
	// given
	const private = "private-marker"
	tests := []struct {
		name    string
		values  map[string]string
		setting string
	}{
		{
			"foreign service",
			map[string]string{kitconfig.EnvServiceName: private},
			kitconfig.EnvServiceName,
		},
		{
			"colliding listeners",
			map[string]string{kitconfig.EnvHTTPAddress: "127.0.0.1:8081"},
			kitconfig.EnvHealthAddress,
		},
		{
			"small header bound",
			map[string]string{kitconfig.EnvHTTPMaxHeaderBytes: "1023"},
			kitconfig.EnvHTTPMaxHeaderBytes,
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
			"shutdown has no exporter reserve",
			map[string]string{kitconfig.EnvShutdownTimeout: "2s"},
			kitconfig.EnvShutdownTimeout,
		},
		{"zero version outside development", map[string]string{
			kitconfig.EnvDeploymentEnvironment: "qa",
			kitconfig.EnvServiceVersion:        "git-000000000000",
			kitconfig.EnvServiceInstanceID:     "chat-test",
		}, kitconfig.EnvServiceVersion},
		{
			"unsafe endpoint",
			map[string]string{
				kitconfig.EnvOTELEnabled:              "true",
				kitconfig.EnvDeploymentEnvironment:    "qa",
				kitconfig.EnvServiceVersion:           "git-000000000001",
				kitconfig.EnvServiceInstanceID:        "chat-test",
				kitconfig.EnvOTELExporterOTLPEndpoint: "http://user:" + private + "@127.0.0.1:4317",
			},
			kitconfig.EnvOTELExporterOTLPEndpoint,
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
				t.Errorf("setting = %q, want %q", setting, test.setting)
			}
			if strings.Contains(err.Error(), private) {
				t.Fatalf("error disclosed rejected value: %v", err)
			}
		})
	}
}

func TestLoad_ExplicitProductionIdentity(t *testing.T) {
	// given
	values := map[string]string{
		kitconfig.EnvServiceName:           DefaultServiceName,
		kitconfig.EnvDeploymentEnvironment: "qa",
		kitconfig.EnvServiceVersion:        "git-123456789abc",
		kitconfig.EnvServiceInstanceID:     "qa-chat-1",
		kitconfig.EnvHTTPAddress:           "0.0.0.0:8080",
		kitconfig.EnvHealthAddress:         "0.0.0.0:8081",
	}

	// when
	settings, err := load(mapLookup(values))
	// then
	if err != nil {
		t.Fatal(err)
	}
	if settings.Identity.ServiceName != DefaultServiceName ||
		settings.Identity.ServiceVersion != "git-123456789abc" ||
		settings.Identity.Environment != "qa" {
		t.Errorf("identity = %+v", settings.Identity)
	}
}

func mapLookup(values map[string]string) lookupEnv {
	return func(name string) (string, bool) {
		value, ok := values[name]
		return value, ok
	}
}
