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
	wantWebSocket := WebSocketSettings{
		MaxConnections:    128,
		MaxMessageBytes:   32 * 1024,
		OutboundQueueSize: 8,
		WriteTimeout:      5 * time.Second,
		PingInterval:      30 * time.Second,
		PongTimeout:       60 * time.Second,
		CloseTimeout:      2 * time.Second,
	}
	if settings.WebSocket != wantWebSocket {
		t.Errorf("WebSocket defaults = %+v, want %+v", settings.WebSocket, wantWebSocket)
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
		{
			"unsafe WebSocket write timeout",
			map[string]string{envWebSocketWriteTimeout: private},
			envWebSocketWriteTimeout,
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

func TestLoad_ExplicitWebSocketSettings(t *testing.T) {
	// given
	values := map[string]string{
		envWebSocketMaxConnections:    "64",
		envWebSocketMaxMessageBytes:   "16384",
		envWebSocketOutboundQueueSize: "4",
		envWebSocketWriteTimeout:      "4s",
		envWebSocketPingInterval:      "20s",
		envWebSocketPongTimeout:       "45s",
		envWebSocketCloseTimeout:      "1s",
	}

	// when
	settings, err := load(mapLookup(values))
	// then
	if err != nil {
		t.Fatal(err)
	}
	want := WebSocketSettings{
		MaxConnections:    64,
		MaxMessageBytes:   16384,
		OutboundQueueSize: 4,
		WriteTimeout:      4 * time.Second,
		PingInterval:      20 * time.Second,
		PongTimeout:       45 * time.Second,
		CloseTimeout:      time.Second,
	}
	if settings.WebSocket != want {
		t.Errorf("WebSocket settings = %+v, want %+v", settings.WebSocket, want)
	}
}

func TestLoad_InvalidWebSocketSettings(t *testing.T) {
	// given
	tests := []struct {
		name    string
		values  map[string]string
		setting string
	}{
		{
			"zero connections",
			map[string]string{envWebSocketMaxConnections: "0"},
			envWebSocketMaxConnections,
		},
		{
			"connections above process limit",
			map[string]string{envWebSocketMaxConnections: "129"},
			envWebSocketMaxConnections,
		},
		{
			"negative message bytes",
			map[string]string{envWebSocketMaxMessageBytes: "-1"},
			envWebSocketMaxMessageBytes,
		},
		{
			"message bytes above process limit",
			map[string]string{envWebSocketMaxMessageBytes: "32769"},
			envWebSocketMaxMessageBytes,
		},
		{
			"zero outbound queue",
			map[string]string{envWebSocketOutboundQueueSize: "0"},
			envWebSocketOutboundQueueSize,
		},
		{
			"outbound queue above process limit",
			map[string]string{envWebSocketOutboundQueueSize: "9"},
			envWebSocketOutboundQueueSize,
		},
		{
			"zero write timeout",
			map[string]string{envWebSocketWriteTimeout: "0s"},
			envWebSocketWriteTimeout,
		},
		{
			"negative close timeout",
			map[string]string{envWebSocketCloseTimeout: "-1s"},
			envWebSocketCloseTimeout,
		},
		{
			"ping equals pong timeout",
			map[string]string{
				envWebSocketPingInterval: "60s",
				envWebSocketPongTimeout:  "60s",
			},
			envWebSocketPingInterval,
		},
		{
			"ping exceeds pong timeout",
			map[string]string{
				envWebSocketPingInterval: "61s",
				envWebSocketPongTimeout:  "60s",
			},
			envWebSocketPingInterval,
		},
		{
			"WebSocket deadlines exhaust service shutdown budget",
			map[string]string{
				envWebSocketWriteTimeout: "7s",
				envWebSocketCloseTimeout: "2s",
			},
			kitconfig.EnvShutdownTimeout,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// when
			_, err := load(mapLookup(test.values))

			// then
			if err == nil {
				t.Fatal("invalid WebSocket settings accepted")
			}
			setting, _ := kitconfig.ErrorDetails(err)
			if setting != test.setting {
				t.Errorf("setting = %q, want %q", setting, test.setting)
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
