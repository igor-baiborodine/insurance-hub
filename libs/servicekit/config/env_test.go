package config_test

import (
	"testing"

	"github.com/igor-baiborodine/insurance-hub/libs/servicekit/config"
)

func TestEnvironmentKeys_MatchExistingContract(t *testing.T) {
	// These literal names are the external contract retained from both consumers.
	// Do not derive expected values from other servicekit constants.
	for _, test := range []struct{ name, got, want string }{
		{"request timeout", config.EnvRequestTimeout, "REQUEST_TIMEOUT"},
		{"probe timeout", config.EnvProbeTimeout, "PROBE_TIMEOUT"},
		{"shutdown timeout", config.EnvShutdownTimeout, "SHUTDOWN_TIMEOUT"},
		{"log level", config.EnvLogLevel, "LOG_LEVEL"},
		{"gRPC address", config.EnvGRPCAddress, "GRPC_ADDR"},
		{"health address", config.EnvHealthAddress, "HEALTH_ADDR"},
		{"gRPC receive limit", config.EnvGRPCMaxReceiveBytes, "GRPC_MAX_RECV_BYTES"},
		{"gRPC send limit", config.EnvGRPCMaxSendBytes, "GRPC_MAX_SEND_BYTES"},
		{"service name", config.EnvServiceName, "SERVICE_NAME"},
		{"deployment environment", config.EnvDeploymentEnvironment, "DEPLOYMENT_ENVIRONMENT"},
		{"service version", config.EnvServiceVersion, "SERVICE_VERSION"},
		{"service instance", config.EnvServiceInstanceID, "SERVICE_INSTANCE_ID"},
		{"telemetry enabled", config.EnvOTELEnabled, "OTEL_ENABLED"},
		{"OTLP endpoint", config.EnvOTELExporterOTLPEndpoint, "OTEL_EXPORTER_OTLP_ENDPOINT"},
		{"OTLP timeout", config.EnvOTELExporterOTLPTimeout, "OTEL_EXPORTER_OTLP_TIMEOUT"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if test.got != test.want {
				t.Errorf("setting name = %q, want %q", test.got, test.want)
			}
		})
	}
}
