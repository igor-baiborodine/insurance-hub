package config

import (
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	kitconfig "github.com/igor-baiborodine/insurance-hub/libs/servicekit/config"
	kitidentity "github.com/igor-baiborodine/insurance-hub/libs/servicekit/identity"
)

// DefaultServiceName identifies Chat startup diagnostics before configuration is validated.
const DefaultServiceName = "chat-service"

const canonicalVersionReason = "must use git- followed by 12 lowercase hexadecimal " +
	"characters with at least one nonzero character outside development"

// Config contains the settings parsed and validated once during process startup.
type Config struct {
	kitconfig.HTTPServer
	kitconfig.Telemetry
	ServiceName        string
	HTTPAddress        Address
	HealthAddress      Address
	HTTPMaxHeaderBytes int
	ProbeTimeout       time.Duration
	ShutdownTimeout    time.Duration
	LogLevel           slog.Level
}

// Address preserves the shared TCP parser, including ephemeral test ports.
type Address = kitconfig.Address

// Identity adds Chat policy to the shared telemetry and logging fields.
type Identity struct{ kitidentity.Identity }

// Telemetry is the shared exporter settings group with Chat-owned identity policy.
type Telemetry = kitconfig.Telemetry

// DevelopmentIdentity labels safe startup diagnostics before configuration is validated.
func DevelopmentIdentity(serviceName string) Identity {
	return Identity{Identity: kitidentity.DevelopmentIdentity(serviceName, os.Getpid())}
}

// Load reads configuration from the process environment.
func Load() (Config, error) { return load(os.LookupEnv) }

type lookupEnv = kitconfig.Lookup

func load(lookup lookupEnv) (Config, error) {
	runtimeDefaults := kitconfig.DefaultRuntime()
	telemetryDefaults := kitconfig.DefaultTelemetry()

	serviceName, err := kitconfig.ValueOrDefault(
		lookup,
		kitconfig.EnvServiceName,
		DefaultServiceName,
	)
	if err != nil {
		return Config{}, err
	}
	if serviceName != DefaultServiceName {
		return Config{}, kitconfig.Invalid(
			kitconfig.EnvServiceName,
			"must match the service's fixed logical name",
		)
	}

	httpAddress, err := kitconfig.LoadAddress(
		lookup,
		kitconfig.EnvHTTPAddress,
		kitconfig.DefaultHTTPAddress,
	)
	if err != nil {
		return Config{}, err
	}
	healthAddress, err := kitconfig.LoadAddress(
		lookup,
		kitconfig.EnvHealthAddress,
		kitconfig.DefaultHealthAddress,
	)
	if err != nil {
		return Config{}, err
	}
	if httpAddress.Port() != 0 && httpAddress.String() == healthAddress.String() {
		return Config{}, kitconfig.Invalid(
			kitconfig.EnvHealthAddress,
			"must be distinct from HTTP_ADDR",
		)
	}

	logLevelValue, err := kitconfig.ValueOrDefault(
		lookup,
		kitconfig.EnvLogLevel,
		runtimeDefaults.LogLevel.String(),
	)
	if err != nil {
		return Config{}, err
	}
	logLevel, err := kitconfig.ParseLogLevel(kitconfig.EnvLogLevel, logLevelValue)
	if err != nil {
		return Config{}, err
	}
	probeTimeout, err := kitconfig.LoadPositiveDuration(
		lookup,
		kitconfig.EnvProbeTimeout,
		runtimeDefaults.ProbeTimeout.String(),
	)
	if err != nil {
		return Config{}, err
	}
	shutdownTimeout, err := kitconfig.LoadPositiveDuration(
		lookup,
		kitconfig.EnvShutdownTimeout,
		runtimeDefaults.ShutdownTimeout.String(),
	)
	if err != nil {
		return Config{}, err
	}

	otelEnabledValue, err := kitconfig.ValueOrDefault(
		lookup,
		kitconfig.EnvOTELEnabled,
		strconv.FormatBool(telemetryDefaults.Enabled),
	)
	if err != nil {
		return Config{}, err
	}
	otelEnabled, err := kitconfig.ParseBool(kitconfig.EnvOTELEnabled, otelEnabledValue)
	if err != nil {
		return Config{}, err
	}
	otelTimeout, err := kitconfig.LoadPositiveDuration(
		lookup,
		kitconfig.EnvOTELExporterOTLPTimeout,
		telemetryDefaults.ExporterTimeout.String(),
	)
	if err != nil {
		return Config{}, err
	}
	otelEndpoint, err := kitconfig.OptionalEndpoint(
		lookup,
		kitconfig.EnvOTELExporterOTLPEndpoint,
		otelEnabled,
	)
	if err != nil {
		return Config{}, err
	}
	identity, err := loadIdentity(lookup, serviceName, otelEnabled)
	if err != nil {
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
	if shutdownTimeout <= otelTimeout {
		return Config{}, kitconfig.Invalid(
			kitconfig.EnvShutdownTimeout,
			"must exceed OTEL_EXPORTER_OTLP_TIMEOUT",
		)
	}

	httpServer, err := kitconfig.LoadHTTPServer(lookup)
	if err != nil {
		return Config{}, err
	}
	if httpServer.ReadHeaderTimeout > httpServer.ReadTimeout {
		return Config{}, kitconfig.Invalid(
			kitconfig.EnvHTTPReadHeaderTimeout,
			"must not exceed HTTP_READ_TIMEOUT",
		)
	}
	if httpServer.ReadTimeout > httpServer.WriteTimeout {
		return Config{}, kitconfig.Invalid(
			kitconfig.EnvHTTPReadTimeout,
			"must not exceed HTTP_WRITE_TIMEOUT",
		)
	}
	maxHeaderBytes, err := kitconfig.LoadBoundedInt(
		lookup,
		kitconfig.EnvHTTPMaxHeaderBytes,
		strconv.Itoa(kitconfig.DefaultHTTPMaxHeaderBytes),
		1024,
		1048576,
	)
	if err != nil {
		return Config{}, err
	}

	return Config{
		HTTPServer:         httpServer,
		Telemetry:          telemetry,
		ServiceName:        serviceName,
		HTTPAddress:        httpAddress,
		HealthAddress:      healthAddress,
		HTTPMaxHeaderBytes: maxHeaderBytes,
		ProbeTimeout:       probeTimeout,
		ShutdownTimeout:    shutdownTimeout,
		LogLevel:           logLevel,
	}, nil
}

func loadIdentity(lookup lookupEnv, serviceName string, enabled bool) (Identity, error) {
	identity := DevelopmentIdentity(serviceName)
	fields := []struct {
		name  string
		value *string
	}{
		{kitconfig.EnvDeploymentEnvironment, &identity.Environment},
		{kitconfig.EnvServiceVersion, &identity.ServiceVersion},
		{kitconfig.EnvServiceInstanceID, &identity.ServiceInstance},
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
	if identity.ServiceName != DefaultServiceName {
		return kitconfig.Invalid(
			kitconfig.EnvServiceName,
			"must match the service's fixed logical name",
		)
	}
	if identity.ServiceNamespace != "insurance-hub" || identity.Implementation != "go" {
		return kitconfig.Invalid(
			"telemetry identity",
			"must use the insurance-hub namespace and Go implementation",
		)
	}
	for _, field := range []struct{ name, value string }{
		{kitconfig.EnvServiceName, identity.ServiceName},
		{kitconfig.EnvDeploymentEnvironment, identity.Environment},
		{kitconfig.EnvServiceVersion, identity.ServiceVersion},
		{kitconfig.EnvServiceInstanceID, identity.ServiceInstance},
	} {
		if !validIdentityValue(field.value) {
			return kitconfig.Invalid(
				field.name,
				"must contain 1 through 128 ASCII letters, digits, dots, underscores or hyphens",
			)
		}
	}
	if identity.Environment != "development" && !validCanonicalVersion(identity.ServiceVersion) {
		return kitconfig.Invalid(kitconfig.EnvServiceVersion, canonicalVersionReason)
	}
	if enabled && (identity.Environment == "development" ||
		identity.ServiceVersion == "development" ||
		strings.HasPrefix(identity.ServiceInstance, "local-")) {
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
	for _, character := range value {
		if (character < 'a' || character > 'z') &&
			(character < 'A' || character > 'Z') &&
			(character < '0' || character > '9') &&
			character != '.' && character != '_' && character != '-' {
			return false
		}
	}
	return true
}

func validCanonicalVersion(value string) bool {
	if len(value) != len("git-")+12 || !strings.HasPrefix(value, "git-") {
		return false
	}
	nonzero := false
	for _, character := range value[len("git-"):] {
		if (character < '0' || character > '9') &&
			(character < 'a' || character > 'f') {
			return false
		}
		if character != '0' {
			nonzero = true
		}
	}
	return nonzero
}
