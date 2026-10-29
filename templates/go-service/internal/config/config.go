package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	kitconfig "github.com/igor-baiborodine/insurance-hub/libs/servicekit/config"
	kitidentity "github.com/igor-baiborodine/insurance-hub/libs/servicekit/identity"
)

// DefaultServiceName identifies startup diagnostics until configuration has been validated.
const DefaultServiceName = "go-service"

const (
	requestTimeoutSetting      = "REQUEST_TIMEOUT"
	probeTimeoutSetting        = "PROBE_TIMEOUT"
	grpcMaxReceiveBytesSetting = "GRPC_MAX_RECV_BYTES"
	grpcMaxSendBytesSetting    = "GRPC_MAX_SEND_BYTES"
	environmentSetting         = "DEPLOYMENT_ENVIRONMENT"
	serviceVersionSetting      = "SERVICE_VERSION"
	serviceInstanceSetting     = "SERVICE_INSTANCE_ID"
	serviceNameSetting         = "SERVICE_NAME"
	grpcAddressSetting         = "GRPC_ADDR"
	healthAddressSetting       = "HEALTH_ADDR"
	logLevelSetting            = "LOG_LEVEL"
	shutdownTimeoutSetting     = "SHUTDOWN_TIMEOUT"
	otelEnabledSetting         = "OTEL_ENABLED"
	otelEndpointSetting        = "OTEL_EXPORTER_OTLP_ENDPOINT"
	otelTimeoutSetting         = "OTEL_EXPORTER_OTLP_TIMEOUT"
)

// Config contains settings parsed and validated once during process startup.
type Config struct {
	kitconfig.Runtime
	kitconfig.GRPC
	kitconfig.Telemetry
	ServiceName   string
	GRPCAddress   Address
	HealthAddress Address
}

// Address uses the shared TCP address parser and preserves ephemeral ports.
type Address = kitconfig.Address

// GRPC is the shared message-size group; the scaffold owns its permitted ranges.
type GRPC = kitconfig.GRPC

// Identity adds scaffold validation policy to the shared resource and log fields.
type Identity struct{ kitidentity.Identity }

// Telemetry is the shared exporter settings group. Identity policy stays in this package.
type Telemetry = kitconfig.Telemetry

// DevelopmentIdentity labels local startup diagnostics before configuration is validated.
func DevelopmentIdentity(serviceName string) Identity {
	return Identity{
		Identity: kitidentity.Identity{
			ServiceNamespace: "insurance-hub",
			ServiceName:      serviceName,
			ServiceVersion:   "development",
			ServiceInstance:  fmt.Sprintf("local-%d", os.Getpid()),
			Environment:      "development",
			Implementation:   "go",
		},
	}
}

// Load reads configuration from the process environment.
func Load() (Config, error) {
	return load(os.LookupEnv)
}

type lookupEnv = kitconfig.Lookup

func load(lookup lookupEnv) (Config, error) {
	runtimeDefaults := kitconfig.DefaultRuntime()
	grpcDefaults := kitconfig.DefaultGRPC()
	telemetryDefaults := kitconfig.DefaultTelemetry()

	serviceName, err := kitconfig.ValueOrDefault(lookup, serviceNameSetting, DefaultServiceName)
	if err != nil {
		return Config{}, err
	}

	grpcAddress, err := kitconfig.LoadAddress(
		lookup, grpcAddressSetting, kitconfig.DefaultGRPCAddress,
	)
	if err != nil {
		return Config{}, err
	}

	healthAddress, err := kitconfig.LoadAddress(
		lookup, healthAddressSetting, kitconfig.DefaultHealthAddress,
	)
	if err != nil {
		return Config{}, err
	}

	logLevelValue, err := kitconfig.ValueOrDefault(
		lookup,
		logLevelSetting,
		runtimeDefaults.LogLevel.String(),
	)
	if err != nil {
		return Config{}, err
	}
	logLevel, err := kitconfig.ParseLogLevel(logLevelSetting, logLevelValue)
	if err != nil {
		return Config{}, err
	}

	shutdownTimeout, err := kitconfig.LoadPositiveDuration(
		lookup, shutdownTimeoutSetting, runtimeDefaults.ShutdownTimeout.String(),
	)
	if err != nil {
		return Config{}, err
	}

	otelEnabledValue, err := kitconfig.ValueOrDefault(
		lookup, otelEnabledSetting, strconv.FormatBool(telemetryDefaults.Enabled),
	)
	if err != nil {
		return Config{}, err
	}
	otelEnabled, err := kitconfig.ParseBool(otelEnabledSetting, otelEnabledValue)
	if err != nil {
		return Config{}, err
	}

	otelTimeout, err := kitconfig.LoadPositiveDuration(
		lookup, otelTimeoutSetting, telemetryDefaults.ExporterTimeout.String(),
	)
	if err != nil {
		return Config{}, err
	}

	otelEndpoint, err := kitconfig.OptionalEndpoint(lookup, otelEndpointSetting, otelEnabled)
	if err != nil {
		return Config{}, err
	}

	identity, err := loadIdentity(lookup, serviceName, otelEnabled)
	if err != nil {
		return Config{}, err
	}
	requestTimeout, err := kitconfig.LoadPositiveDuration(
		lookup, requestTimeoutSetting, runtimeDefaults.RequestTimeout.String(),
	)
	if err != nil {
		return Config{}, err
	}
	probeTimeout, err := kitconfig.LoadPositiveDuration(
		lookup, probeTimeoutSetting, runtimeDefaults.ProbeTimeout.String(),
	)
	if err != nil {
		return Config{}, err
	}
	receiveBytes, err := kitconfig.LoadBoundedInt(
		lookup,
		grpcMaxReceiveBytesSetting,
		strconv.Itoa(grpcDefaults.MaxReceiveBytes),
		1,
		16777216,
	)
	if err != nil {
		return Config{}, err
	}
	sendBytes, err := kitconfig.LoadBoundedInt(
		lookup,
		grpcMaxSendBytesSetting,
		strconv.Itoa(grpcDefaults.MaxSendBytes),
		1,
		67108864,
	)
	if err != nil {
		return Config{}, err
	}
	runtime := kitconfig.Runtime{
		RequestTimeout:  requestTimeout,
		ProbeTimeout:    probeTimeout,
		ShutdownTimeout: shutdownTimeout,
		LogLevel:        logLevel,
	}
	if err := kitconfig.ValidateRuntime(runtime); err != nil {
		return Config{}, err
	}
	telemetry := kitconfig.Telemetry{
		Identity:         identity.Identity,
		Enabled:          otelEnabled,
		ExporterEndpoint: otelEndpoint,
		ExporterTimeout:  otelTimeout,
	}
	if err := kitconfig.ValidateTelemetry(telemetry); err != nil {
		return Config{}, err
	}
	if otelTimeout > runtime.ShutdownTimeout {
		return Config{}, kitconfig.Invalid(
			otelTimeoutSetting,
			"must not exceed SHUTDOWN_TIMEOUT",
		)
	}
	if grpcAddress.Port() != 0 && grpcAddress.String() == healthAddress.String() {
		return Config{}, kitconfig.Invalid(
			healthAddressSetting,
			"must be distinct from GRPC_ADDR",
		)
	}

	return Config{
		Runtime:       runtime,
		GRPC:          GRPC{MaxReceiveBytes: receiveBytes, MaxSendBytes: sendBytes},
		ServiceName:   serviceName,
		GRPCAddress:   grpcAddress,
		HealthAddress: healthAddress,
		Telemetry:     telemetry,
	}, nil
}

func loadIdentity(lookup lookupEnv, serviceName string, enabled bool) (Identity, error) {
	identity := DevelopmentIdentity(serviceName)
	fields := []struct {
		name  string
		value *string
	}{
		{environmentSetting, &identity.Environment},
		{serviceVersionSetting, &identity.ServiceVersion},
		{serviceInstanceSetting, &identity.ServiceInstance},
	}
	for _, field := range fields {
		fallback := *field.value
		if enabled {
			fallback = ""
		}
		value, err := kitconfig.ValueOrDefault(lookup, field.name, fallback)
		if err != nil {
			return Identity{}, err
		}
		*field.value = value
	}
	if err := identity.Validate(enabled); err != nil {
		return Identity{}, err
	}
	return identity, nil
}

// Validate rejects incomplete identity without echoing supplied values.
func (identity Identity) Validate(enabled bool) error {
	if identity.ServiceNamespace != "insurance-hub" || identity.Implementation != "go" {
		return kitconfig.Invalid(
			"telemetry identity",
			"must use the insurance-hub namespace and Go implementation",
		)
	}
	for _, field := range []struct{ name, value string }{
		{serviceNameSetting, identity.ServiceName},
		{environmentSetting, identity.Environment},
		{serviceVersionSetting, identity.ServiceVersion},
		{serviceInstanceSetting, identity.ServiceInstance},
	} {
		if !validIdentityValue(field.value) {
			return kitconfig.Invalid(
				field.name,
				"must contain 1 through 128 ASCII letters, digits, dots, underscores or hyphens",
			)
		}
	}
	if enabled &&
		(identity.Environment == "development" || identity.ServiceVersion == "development" || strings.HasPrefix(identity.ServiceInstance, "local-")) {
		return kitconfig.Invalid(
			"telemetry identity",
			"must use explicit non-development environment, version and instance when enabled",
		)
	}
	return nil
}

func validIdentityValue(value string) bool {
	if len(value) == 0 || len(value) > 128 {
		return false
	}
	for _, ch := range value {
		if (ch < 'a' || ch > 'z') && (ch < 'A' || ch > 'Z') && (ch < '0' || ch > '9') &&
			ch != '.' &&
			ch != '_' &&
			ch != '-' {
			return false
		}
	}
	return true
}
