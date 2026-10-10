package config

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

const testDatabaseURL = "postgresql://product_reader:reader-password@127.0.0.1:5432/product"

func TestLoad_Defaults(t *testing.T) {
	// given
	values := map[string]string{databaseURLSetting: testDatabaseURL}

	// when
	settings, err := load(mapLookup(values))
	// then
	if err != nil {
		t.Fatalf("load defaults: %v", err)
	}
	if settings.ServiceName != "product-service" ||
		settings.HTTPAddress.String() != "127.0.0.1:8081" ||
		settings.GRPCAddress.String() != "127.0.0.1:9090" ||
		settings.HealthAddress.String() != "127.0.0.1:8080" {
		t.Errorf("unexpected identity or listener defaults: %#v", settings)
	}
	if settings.Database.URL.Value() != testDatabaseURL ||
		settings.Database.MaxConnections != 8 ||
		settings.Database.ConnectTimeout != 3*time.Second ||
		settings.Database.AcquireTimeout != 2*time.Second ||
		settings.Database.QueryTimeout != 5*time.Second {
		t.Errorf("unexpected database defaults: %#v", settings.Database)
	}
	if settings.StartupTimeout != 10*time.Second ||
		settings.RequestTimeout != 8*time.Second ||
		settings.ProbeTimeout != 2*time.Second ||
		settings.ShutdownTimeout != 10*time.Second {
		t.Errorf("unexpected operation defaults: %#v", settings)
	}
	if settings.HTTPServer != (HTTPServer{
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       30 * time.Second,
	}) {
		t.Errorf("unexpected HTTP defaults: %#v", settings.HTTPServer)
	}
	if settings.GRPC != (GRPC{MaxReceiveBytes: 1048576, MaxSendBytes: 8388608}) {
		t.Errorf("unexpected gRPC defaults: %#v", settings.GRPC)
	}
	if settings.Telemetry.Enabled || settings.Telemetry.ExporterEndpoint != nil ||
		settings.Telemetry.ExporterTimeout != 2*time.Second {
		t.Errorf("unexpected telemetry defaults: %#v", settings.Telemetry)
	}
}

func TestLoad_AcceptsTypedOverrides(t *testing.T) {
	// given
	values := map[string]string{
		serviceNameSetting:           "product-reader",
		httpAddressSetting:           "[::1]:8081",
		grpcAddressSetting:           "[::1]:9090",
		healthAddressSetting:         "[::1]:8080",
		databaseURLSetting:           "postgres://reader:secret@db.internal:5433/catalog?sslmode=require",
		databaseMaxConnsSetting:      "16",
		databaseConnectSetting:       "4s",
		databaseAcquireSetting:       "3s",
		databaseQuerySetting:         "6s",
		startupTimeoutSetting:        "12s",
		requestTimeoutSetting:        "9s",
		probeTimeoutSetting:          "3s",
		httpReadHeaderTimeoutSetting: "6s",
		httpReadTimeoutSetting:       "12s",
		httpWriteTimeoutSetting:      "12s",
		httpIdleTimeoutSetting:       "45s",
		grpcMaxReceiveBytesSetting:   "2097152",
		grpcMaxSendBytesSetting:      "16777216",
		shutdownTimeoutSetting:       "12s",
		logLevelSetting:              "debug",
		otelEnabledSetting:           "true",
		otelEndpointSetting:          "https://collector.internal:4317",
		otelTimeoutSetting:           "3s",
	}

	// when
	settings, err := load(mapLookup(values))
	// then
	if err != nil {
		t.Fatalf("load overrides: %v", err)
	}
	if settings.ServiceName != "product-reader" ||
		settings.HTTPAddress.String() != "[::1]:8081" ||
		settings.GRPCAddress.String() != "[::1]:9090" ||
		settings.HealthAddress.String() != "[::1]:8080" {
		t.Errorf("unexpected identity or listener overrides: %#v", settings)
	}
	if settings.Database.MaxConnections != 16 ||
		settings.Database.ConnectTimeout != 4*time.Second ||
		settings.Database.AcquireTimeout != 3*time.Second ||
		settings.Database.QueryTimeout != 6*time.Second {
		t.Errorf("unexpected database overrides: %#v", settings.Database)
	}
	if settings.StartupTimeout != 12*time.Second ||
		settings.RequestTimeout != 9*time.Second ||
		settings.ProbeTimeout != 3*time.Second ||
		settings.ShutdownTimeout != 12*time.Second {
		t.Errorf("unexpected operation overrides: %#v", settings)
	}
	if settings.HTTPServer.ReadHeaderTimeout != 6*time.Second ||
		settings.HTTPServer.ReadTimeout != 12*time.Second ||
		settings.HTTPServer.WriteTimeout != 12*time.Second ||
		settings.HTTPServer.IdleTimeout != 45*time.Second {
		t.Errorf("unexpected HTTP overrides: %#v", settings.HTTPServer)
	}
	if settings.GRPC.MaxReceiveBytes != 2097152 || settings.GRPC.MaxSendBytes != 16777216 {
		t.Errorf("unexpected gRPC overrides: %#v", settings.GRPC)
	}
	if !settings.Telemetry.Enabled ||
		settings.Telemetry.ExporterEndpoint.String() != "https://collector.internal:4317" ||
		settings.Telemetry.ExporterTimeout != 3*time.Second {
		t.Errorf("unexpected telemetry overrides: %#v", settings.Telemetry)
	}
}

func TestLoad_RejectsAbsentEmptyOrBlankDatabaseURL(t *testing.T) {
	// given
	testCases := []struct {
		name   string
		values map[string]string
	}{
		{name: "absent"},
		{name: "empty", values: map[string]string{databaseURLSetting: ""}},
		{name: "blank", values: map[string]string{databaseURLSetting: " 	 "}},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			// when
			_, err := load(mapLookup(testCase.values))

			// then
			assertSettingError(t, err, databaseURLSetting)
		})
	}
}

func TestLoad_RejectsDatabaseURLWithoutExplicitUser(t *testing.T) {
	// given
	testCases := []struct {
		name string
		url  string
	}{
		{name: "missing user", url: "postgresql://127.0.0.1:5432/product"},
		{name: "empty user", url: "postgresql://@127.0.0.1:5432/product"},
		{name: "blank user", url: "postgresql://%20@127.0.0.1:5432/product"},
		{
			name: "query user overrides explicit user",
			url:  "postgresql://product_reader@127.0.0.1:5432/product?user=",
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			// when
			_, err := load(
				mapLookup(map[string]string{databaseURLSetting: testCase.url}),
			)

			// then
			assertSettingError(t, err, databaseURLSetting)
		})
	}
}

func TestLoad_RejectsInvalidSettings(t *testing.T) {
	// given
	testCases := []struct {
		name    string
		setting string
		value   string
	}{
		{name: "empty service name", setting: serviceNameSetting, value: ""},
		{name: "blank service name", setting: serviceNameSetting, value: "   "},
		{name: "malformed HTTP address", setting: httpAddressSetting, value: "localhost"},
		{
			name:    "malformed gRPC address",
			setting: grpcAddressSetting,
			value:   "localhost:port",
		},
		{
			name:    "malformed management address",
			setting: healthAddressSetting,
			value:   ":8080",
		},
		{
			name:    "invalid database URL",
			setting: databaseURLSetting,
			value:   "mysql://db/catalog",
		},
		{name: "zero pool maximum", setting: databaseMaxConnsSetting, value: "0"},
		{name: "excessive pool maximum", setting: databaseMaxConnsSetting, value: "33"},
		{name: "noninteger pool maximum", setting: databaseMaxConnsSetting, value: "8.5"},
		{name: "zero connect timeout", setting: databaseConnectSetting, value: "0s"},
		{name: "negative acquire timeout", setting: databaseAcquireSetting, value: "-1s"},
		{name: "malformed query timeout", setting: databaseQuerySetting, value: "soon"},
		{name: "zero startup timeout", setting: startupTimeoutSetting, value: "0"},
		{name: "zero request timeout", setting: requestTimeoutSetting, value: "0s"},
		{name: "zero probe timeout", setting: probeTimeoutSetting, value: "-1ns"},
		{name: "zero header timeout", setting: httpReadHeaderTimeoutSetting, value: "0s"},
		{name: "zero read timeout", setting: httpReadTimeoutSetting, value: "0s"},
		{name: "zero write timeout", setting: httpWriteTimeoutSetting, value: "0s"},
		{name: "zero idle timeout", setting: httpIdleTimeoutSetting, value: "0s"},
		{name: "zero receive maximum", setting: grpcMaxReceiveBytesSetting, value: "0"},
		{
			name:    "excessive receive maximum",
			setting: grpcMaxReceiveBytesSetting,
			value:   "16777217",
		},
		{name: "zero send maximum", setting: grpcMaxSendBytesSetting, value: "0"},
		{
			name:    "excessive send maximum",
			setting: grpcMaxSendBytesSetting,
			value:   "67108865",
		},
		{name: "zero shutdown timeout", setting: shutdownTimeoutSetting, value: "0s"},
		{name: "unsupported log level", setting: logLevelSetting, value: "verbose"},
		{
			name:    "malformed telemetry switch",
			setting: otelEnabledSetting,
			value:   "sometimes",
		},
		{name: "zero exporter timeout", setting: otelTimeoutSetting, value: "0s"},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			values := map[string]string{
				databaseURLSetting: testDatabaseURL,
				testCase.setting:   testCase.value,
			}

			// when
			_, err := load(mapLookup(values))

			// then
			assertSettingError(t, err, testCase.setting)
		})
	}
}

func TestLoad_RejectsDuplicateListenerAddresses(t *testing.T) {
	// given
	testCases := []struct {
		name    string
		setting string
		value   string
	}{
		{
			name:    "gRPC duplicates HTTP",
			setting: grpcAddressSetting,
			value:   "127.0.0.1:8081",
		},
		{
			name:    "management duplicates HTTP",
			setting: healthAddressSetting,
			value:   "127.0.0.1:8081",
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			values := map[string]string{
				databaseURLSetting: testDatabaseURL,
				testCase.setting:   testCase.value,
			}

			// when
			_, err := load(mapLookup(values))

			// then
			assertSettingError(t, err, testCase.setting)
		})
	}
}

func TestLoad_RejectsInvalidTimeoutRelationships(t *testing.T) {
	// given
	testCases := []struct {
		name    string
		setting string
		values  map[string]string
	}{
		{
			name:    "connect exceeds startup",
			setting: databaseConnectSetting,
			values:  map[string]string{databaseConnectSetting: "11s"},
		},
		{
			name:    "acquire exceeds query",
			setting: databaseAcquireSetting,
			values:  map[string]string{databaseAcquireSetting: "6s"},
		},
		{
			name:    "query exceeds request",
			setting: databaseQuerySetting,
			values:  map[string]string{databaseQuerySetting: "9s"},
		},
		{
			name:    "probe exceeds query",
			setting: probeTimeoutSetting,
			values:  map[string]string{probeTimeoutSetting: "6s"},
		},
		{
			name:    "header exceeds read",
			setting: httpReadHeaderTimeoutSetting,
			values:  map[string]string{httpReadHeaderTimeoutSetting: "11s"},
		},
		{
			name:    "request exceeds write",
			setting: requestTimeoutSetting,
			values:  map[string]string{requestTimeoutSetting: "11s"},
		},
		{
			name:    "exporter exceeds shutdown",
			setting: otelTimeoutSetting,
			values:  map[string]string{otelTimeoutSetting: "11s"},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			testCase.values[databaseURLSetting] = testDatabaseURL

			// when
			_, err := load(mapLookup(testCase.values))

			// then
			assertSettingError(t, err, testCase.setting)
		})
	}
}

func TestLoad_AllowsDisabledTelemetryWithoutEndpoint(t *testing.T) {
	// given
	values := map[string]string{
		databaseURLSetting: testDatabaseURL,
		otelEnabledSetting: "false",
	}

	// when
	settings, err := load(mapLookup(values))
	// then
	if err != nil {
		t.Fatalf("load with disabled telemetry: %v", err)
	}
	if settings.Telemetry.ExporterEndpoint != nil {
		t.Errorf(
			"disabled exporter endpoint = %v, want nil",
			settings.Telemetry.ExporterEndpoint,
		)
	}
}

func TestLoad_RequiresEndpointForEnabledTelemetry(t *testing.T) {
	// given
	values := map[string]string{
		databaseURLSetting: testDatabaseURL,
		otelEnabledSetting: "true",
	}

	// when
	_, err := load(mapLookup(values))

	// then
	assertSettingError(t, err, otelEndpointSetting)
}

func TestDatabaseURLAndErrors_RedactCredentialValues(t *testing.T) {
	// given
	const privateMarker = "private-password-marker"
	rawURL := "postgresql://product_reader:" + privateMarker + "@127.0.0.1:5432/product"
	settings, err := load(mapLookup(map[string]string{databaseURLSetting: rawURL}))
	if err != nil {
		t.Fatalf("load database URL: %v", err)
	}

	// when
	formatted := fmt.Sprintf(
		"%s %v %+v %#v",
		settings.Database.URL,
		settings,
		settings,
		settings,
	)
	_, invalidURLError := load(mapLookup(map[string]string{
		databaseURLSetting: "postgresql://product_reader:" + privateMarker + "@bad host/product",
	}))

	// then
	if settings.Database.URL.Value() != rawURL {
		t.Errorf("database URL value = %q, want exact input", settings.Database.URL.Value())
	}
	if strings.Contains(formatted, privateMarker) {
		t.Errorf("formatted settings expose database credential: %s", formatted)
	}
	assertSettingError(t, invalidURLError, databaseURLSetting)
	if strings.Contains(invalidURLError.Error(), privateMarker) {
		t.Errorf("configuration error exposed database credential: %v", invalidURLError)
	}
}

func TestLoad_RejectsInvalidTelemetryEndpointWithoutValue(t *testing.T) {
	// given
	const privateMarker = "private-token-marker"
	values := map[string]string{
		databaseURLSetting:  testDatabaseURL,
		otelEnabledSetting:  "true",
		otelEndpointSetting: "http://user:" + privateMarker + "@127.0.0.1:4317",
	}

	// when
	_, err := load(mapLookup(values))

	// then
	assertSettingError(t, err, otelEndpointSetting)
	if strings.Contains(err.Error(), privateMarker) {
		t.Errorf("configuration error exposed secret: %v", err)
	}
}

func mapLookup(values map[string]string) lookupEnv {
	return func(key string) (string, bool) {
		value, found := values[key]
		return value, found
	}
}

func assertSettingError(t *testing.T, err error, setting string) {
	t.Helper()
	if err == nil || !strings.Contains(err.Error(), setting) {
		t.Fatalf("error = %v, want safe diagnostic naming %s", err, setting)
	}
}
