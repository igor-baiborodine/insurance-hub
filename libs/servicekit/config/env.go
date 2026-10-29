package config

// Shared environment variable names are constants so service loaders can
// reference the same external contract while retaining their own policy.
const (
	// EnvRequestTimeout names the request timeout setting.
	EnvRequestTimeout = "REQUEST_TIMEOUT"
	// EnvProbeTimeout names the probe timeout setting.
	EnvProbeTimeout = "PROBE_TIMEOUT"
	// EnvShutdownTimeout names the shutdown timeout setting.
	EnvShutdownTimeout = "SHUTDOWN_TIMEOUT"
	// EnvLogLevel names the diagnostic log level setting.
	EnvLogLevel = "LOG_LEVEL"

	// EnvGRPCAddress names the gRPC listener setting.
	EnvGRPCAddress = "GRPC_ADDR"
	// EnvHealthAddress names the management HTTP listener setting.
	EnvHealthAddress = "HEALTH_ADDR"
	// EnvGRPCMaxReceiveBytes names the gRPC receive limit setting.
	EnvGRPCMaxReceiveBytes = "GRPC_MAX_RECV_BYTES"
	// EnvGRPCMaxSendBytes names the gRPC send limit setting.
	EnvGRPCMaxSendBytes = "GRPC_MAX_SEND_BYTES"

	// EnvServiceName names the service identity setting.
	EnvServiceName = "SERVICE_NAME"
	// EnvDeploymentEnvironment names the deployment environment setting.
	EnvDeploymentEnvironment = "DEPLOYMENT_ENVIRONMENT"
	// EnvServiceVersion names the service version setting.
	EnvServiceVersion = "SERVICE_VERSION"
	// EnvServiceInstanceID names the service instance setting.
	EnvServiceInstanceID = "SERVICE_INSTANCE_ID"

	// EnvOTELEnabled names the telemetry enablement setting.
	EnvOTELEnabled = "OTEL_ENABLED"
	// EnvOTELExporterOTLPEndpoint names the OTLP exporter endpoint setting.
	EnvOTELExporterOTLPEndpoint = "OTEL_EXPORTER_OTLP_ENDPOINT"
	// EnvOTELExporterOTLPTimeout names the OTLP exporter timeout setting.
	EnvOTELExporterOTLPTimeout = "OTEL_EXPORTER_OTLP_TIMEOUT"
)
