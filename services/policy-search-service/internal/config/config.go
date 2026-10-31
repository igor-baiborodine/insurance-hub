package config

import (
	"crypto/rand"
	"encoding/hex"
	"os"
	"strconv"

	kitconfig "github.com/igor-baiborodine/insurance-hub/libs/servicekit/config"
	kitidentity "github.com/igor-baiborodine/insurance-hub/libs/servicekit/identity"
)

// DefaultServiceName identifies startup diagnostics until configuration has been validated.
const DefaultServiceName = "policy-search-service"

const httpAddressSetting = "HTTP_ADDR"

// Config contains the immutable settings parsed and validated during process startup.
type Config struct {
	kitconfig.Runtime
	ServiceName   string
	HTTPAddress   Address
	HealthAddress Address
	kitconfig.HTTPServer
	kitconfig.Telemetry
}

// Address is a validated TCP listen address with a host and numeric port.
type Address = kitconfig.Address

// Identity is the application-owned resource and log identity. Kubernetes fields are added by Alloy.
type Identity = kitidentity.Identity

// HTTPServer contains business HTTP server time bounds.
type HTTPServer = kitconfig.HTTPServer

// Telemetry contains the optional OTLP trace and metric export configuration.
type Telemetry = kitconfig.Telemetry

// DevelopmentIdentity labels startup diagnostics before configuration is validated.
func DevelopmentIdentity(serviceName string) Identity {
	return kitidentity.DevelopmentIdentity(serviceName, os.Getpid())
}

// Load reads configuration from the process environment.
func Load() (Config, error) {
	return load(os.LookupEnv)
}

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

	httpAddress, err := kitconfig.LoadAddress(
		lookup, httpAddressSetting, kitconfig.DefaultHTTPAddress,
	)
	if err != nil {
		return Config{}, err
	}

	healthAddress, err := kitconfig.LoadAddress(
		lookup, kitconfig.EnvHealthAddress, kitconfig.DefaultHealthAddress,
	)
	if err != nil {
		return Config{}, err
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

	shutdownTimeout, err := kitconfig.LoadPositiveDuration(
		lookup, kitconfig.EnvShutdownTimeout, runtimeDefaults.ShutdownTimeout.String(),
	)
	if err != nil {
		return Config{}, err
	}

	otelEnabledValue, err := kitconfig.ValueOrDefault(
		lookup, kitconfig.EnvOTELEnabled, strconv.FormatBool(telemetryDefaults.Enabled),
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
	if otelEnabled && otelTimeout != telemetryDefaults.ExporterTimeout {
		return Config{}, kitconfig.Invalid(
			kitconfig.EnvOTELExporterOTLPTimeout,
			"must be 2s when telemetry is enabled",
		)
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
	requestTimeout, err := kitconfig.LoadPositiveDuration(
		lookup, kitconfig.EnvRequestTimeout, runtimeDefaults.RequestTimeout.String(),
	)
	if err != nil {
		return Config{}, err
	}
	probeTimeout, err := kitconfig.LoadPositiveDuration(
		lookup, kitconfig.EnvProbeTimeout, runtimeDefaults.ProbeTimeout.String(),
	)
	if err != nil {
		return Config{}, err
	}
	httpServer, err := kitconfig.LoadHTTPServer(lookup)
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
		Identity:         identity,
		Enabled:          otelEnabled,
		ExporterEndpoint: otelEndpoint,
		ExporterTimeout:  otelTimeout,
	}
	if err := kitconfig.ValidateTelemetry(telemetry); err != nil {
		return Config{}, err
	}
	if httpServer.ReadHeaderTimeout > httpServer.ReadTimeout {
		return Config{}, kitconfig.Invalid(
			kitconfig.EnvHTTPReadHeaderTimeout,
			"must not exceed HTTP_READ_TIMEOUT",
		)
	}
	if runtime.RequestTimeout > httpServer.WriteTimeout {
		return Config{}, kitconfig.Invalid(
			kitconfig.EnvRequestTimeout,
			"must not exceed HTTP_WRITE_TIMEOUT",
		)
	}
	if otelTimeout > runtime.ShutdownTimeout {
		return Config{}, kitconfig.Invalid(
			kitconfig.EnvOTELExporterOTLPTimeout,
			"must not exceed SHUTDOWN_TIMEOUT",
		)
	}
	if httpAddress.String() == healthAddress.String() {
		return Config{}, kitconfig.Invalid(
			kitconfig.EnvHealthAddress,
			"must be distinct from HTTP_ADDR",
		)
	}

	return Config{
		Runtime:       runtime,
		HTTPServer:    httpServer,
		ServiceName:   serviceName,
		HTTPAddress:   httpAddress,
		HealthAddress: healthAddress,
		Telemetry:     telemetry,
	}, nil
}

func loadIdentity(lookup lookupEnv, serviceName string, enabled bool) (Identity, error) {
	defaultEnvironment := "development"
	if enabled {
		defaultEnvironment = ""
	}
	environment, err := kitconfig.ValueOrDefault(
		lookup,
		kitconfig.EnvDeploymentEnvironment,
		defaultEnvironment,
	)
	if err != nil {
		return Identity{}, err
	}
	version, err := kitconfig.ValueOrDefault(
		lookup,
		kitconfig.EnvServiceVersion,
		"development",
	)
	if err != nil {
		return Identity{}, err
	}
	instance, err := kitconfig.ValueOrDefault(lookup, kitconfig.EnvServiceInstanceID, "")
	if err != nil {
		return Identity{}, err
	}
	if environment == "test" && instance != "" {
		return Identity{}, kitconfig.Invalid(
			kitconfig.EnvServiceInstanceID,
			"must be generated in test mode",
		)
	}
	if environment == "test" {
		instance, err = randomInstanceID()
		if err != nil {
			return Identity{}, kitconfig.Invalid(
				kitconfig.EnvServiceInstanceID,
				"could not generate test instance ID",
			)
		}
	} else if environment == "development" && instance == "" {
		instance = DevelopmentIdentity(serviceName).ServiceInstance
	}
	identity := Identity{
		ServiceNamespace: "insurance-hub",
		ServiceName:      serviceName,
		ServiceVersion:   version,
		ServiceInstance:  instance,
		Environment:      environment,
		Implementation:   "go",
	}
	if err := kitidentity.ValidateDeployment(
		identity,
		DefaultServiceName,
		enabled,
	); err != nil {
		return Identity{}, err
	}
	return identity, nil
}

func randomInstanceID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	value[6] = value[6]&0x0f | 0x40
	value[8] = value[8]&0x3f | 0x80
	encoded := hex.EncodeToString(value[:])
	return encoded[:8] + "-" + encoded[8:12] + "-" + encoded[12:16] + "-" +
		encoded[16:20] + "-" + encoded[20:], nil
}
