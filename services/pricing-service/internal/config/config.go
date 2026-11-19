package config

import (
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	kitconfig "github.com/igor-baiborodine/insurance-hub/libs/servicekit/config"
	kitidentity "github.com/igor-baiborodine/insurance-hub/libs/servicekit/identity"
)

// DefaultServiceName identifies startup diagnostics until configuration has been validated.
const DefaultServiceName = "pricing-service"

const canonicalVersionReason = "must use git- followed by 12 lowercase hexadecimal " +
	"characters with at least one nonzero character outside development"

const (
	databaseURLSetting      = "PRICING_DATABASE_URL"
	databaseMaxConnsSetting = "DB_MAX_CONNS"
	databaseConnectSetting  = "DB_CONNECT_TIMEOUT"
	databaseAcquireSetting  = "DB_ACQUIRE_TIMEOUT"
	databaseQuerySetting    = "DB_QUERY_TIMEOUT"
	startupTimeoutSetting   = "STARTUP_TIMEOUT"
)

// Database contains the restricted Pricing reader connection and pool limits.
type Database struct {
	URL            DatabaseURL
	MaxConnections int32
	ConnectTimeout time.Duration
	AcquireTimeout time.Duration
	QueryTimeout   time.Duration
}

// DatabaseURL redacts the reader credential from ordinary formatting.
type DatabaseURL struct{ value string }

// Value returns the reader connection URL to the PostgreSQL adapter.
func (databaseURL DatabaseURL) Value() string { return databaseURL.value }

func (DatabaseURL) String() string   { return "[REDACTED]" }
func (DatabaseURL) GoString() string { return "[REDACTED]" }

// Config contains settings parsed and validated once during process startup.
type Config struct {
	Database       Database
	StartupTimeout time.Duration
	kitconfig.Runtime
	kitconfig.GRPC
	kitconfig.HTTPServer
	kitconfig.Telemetry
	ServiceName        string
	HTTPAddress        Address
	GRPCAddress        Address
	HealthAddress      Address
	HTTPMaxHeaderBytes int
	HTTPMaxBodyBytes   int
}

// Address uses the shared TCP address parser and preserves ephemeral ports.
type Address = kitconfig.Address

// GRPC is the shared message-size group; Pricing owns its permitted ranges.
type GRPC = kitconfig.GRPC

// Identity adds Pricing validation policy to the shared resource and log fields.
type Identity struct{ kitidentity.Identity }

// Telemetry is the shared exporter settings group. Identity policy stays in this package.
type Telemetry = kitconfig.Telemetry

// DevelopmentIdentity labels local startup diagnostics before configuration is validated.
func DevelopmentIdentity(serviceName string) Identity {
	return Identity{Identity: kitidentity.DevelopmentIdentity(serviceName, os.Getpid())}
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
	if serviceName != DefaultServiceName {
		return Config{}, kitconfig.Invalid(
			kitconfig.EnvServiceName,
			"must match the service's fixed logical name",
		)
	}

	grpcAddress, err := kitconfig.LoadAddress(
		lookup, kitconfig.EnvGRPCAddress, kitconfig.DefaultGRPCAddress,
	)
	if err != nil {
		return Config{}, err
	}
	httpAddress, err := kitconfig.LoadAddress(
		lookup, kitconfig.EnvHTTPAddress, kitconfig.DefaultHTTPAddress,
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
	grpc, err := kitconfig.LoadGRPC(lookup)
	if err != nil {
		return Config{}, err
	}
	httpServer, err := kitconfig.LoadHTTPServer(lookup)
	if err != nil {
		return Config{}, err
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
	maxBodyBytes, err := kitconfig.LoadBoundedInt(
		lookup,
		kitconfig.EnvHTTPMaxBodyBytes,
		strconv.Itoa(kitconfig.DefaultHTTPMaxBodyBytes),
		1024,
		1048576,
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
	if runtime.RequestTimeout >= httpServer.WriteTimeout {
		return Config{}, kitconfig.Invalid(
			kitconfig.EnvRequestTimeout,
			"must be less than HTTP_WRITE_TIMEOUT",
		)
	}
	if runtime.ShutdownTimeout < runtime.RequestTimeout+otelTimeout {
		return Config{}, kitconfig.Invalid(
			kitconfig.EnvShutdownTimeout,
			"must cover REQUEST_TIMEOUT plus OTEL_EXPORTER_OTLP_TIMEOUT",
		)
	}
	listeners := []struct {
		name    string
		address Address
	}{
		{kitconfig.EnvHTTPAddress, httpAddress},
		{kitconfig.EnvGRPCAddress, grpcAddress},
		{kitconfig.EnvHealthAddress, healthAddress},
	}
	for left := range listeners {
		for right := left + 1; right < len(listeners); right++ {
			if listeners[left].address.Port() != 0 &&
				listeners[left].address.String() == listeners[right].address.String() {
				return Config{}, kitconfig.Invalid(
					listeners[right].name,
					"must be distinct from "+listeners[left].name,
				)
			}
		}
	}

	databaseURL, err := loadDatabaseURL(lookup)
	if err != nil {
		return Config{}, err
	}
	maxConnections, err := kitconfig.LoadBoundedInt(lookup, databaseMaxConnsSetting, "8", 1, 32)
	if err != nil {
		return Config{}, err
	}
	connectTimeout, err := kitconfig.LoadPositiveDuration(lookup, databaseConnectSetting, "3s")
	if err != nil {
		return Config{}, err
	}
	acquireTimeout, err := kitconfig.LoadPositiveDuration(lookup, databaseAcquireSetting, "2s")
	if err != nil {
		return Config{}, err
	}
	queryTimeout, err := kitconfig.LoadPositiveDuration(lookup, databaseQuerySetting, "5s")
	if err != nil {
		return Config{}, err
	}
	startupTimeout, err := kitconfig.LoadPositiveDuration(lookup, startupTimeoutSetting, "10s")
	if err != nil {
		return Config{}, err
	}
	if connectTimeout > startupTimeout {
		return Config{}, kitconfig.Invalid(
			databaseConnectSetting,
			"must not exceed STARTUP_TIMEOUT",
		)
	}
	if databaseURL.Value() != "" {
		for _, relationship := range []struct {
			setting string
			value   time.Duration
			bound   string
			maximum time.Duration
		}{
			{databaseAcquireSetting, acquireTimeout, databaseQuerySetting, queryTimeout},
			{databaseQuerySetting, queryTimeout, kitconfig.EnvRequestTimeout, runtime.RequestTimeout},
			{kitconfig.EnvProbeTimeout, runtime.ProbeTimeout, databaseQuerySetting, queryTimeout},
		} {
			if relationship.value > relationship.maximum {
				return Config{}, kitconfig.Invalid(
					relationship.setting, "must not exceed "+relationship.bound,
				)
			}
		}
	}
	return Config{
		Database: Database{
			URL:            databaseURL,
			MaxConnections: int32(maxConnections),
			ConnectTimeout: connectTimeout,
			AcquireTimeout: acquireTimeout,
			QueryTimeout:   queryTimeout,
		},
		StartupTimeout:     startupTimeout,
		Runtime:            runtime,
		GRPC:               grpc,
		HTTPServer:         httpServer,
		ServiceName:        serviceName,
		HTTPAddress:        httpAddress,
		GRPCAddress:        grpcAddress,
		HealthAddress:      healthAddress,
		HTTPMaxHeaderBytes: maxHeaderBytes,
		HTTPMaxBodyBytes:   maxBodyBytes,
		Telemetry:          telemetry,
	}, nil
}

func loadDatabaseURL(lookup lookupEnv) (DatabaseURL, error) {
	value, ok := lookup(databaseURLSetting)
	if !ok {
		return DatabaseURL{}, nil
	}
	if strings.TrimSpace(value) == "" {
		return DatabaseURL{}, kitconfig.Invalid(databaseURLSetting, "must not be empty")
	}
	parsed, err := url.Parse(value)
	if err != nil || (parsed.Scheme != "postgres" && parsed.Scheme != "postgresql") ||
		parsed.User == nil || parsed.User.Username() != "go_pricing_reader" ||
		parsed.Path != "/pricing" || !kitconfig.ValidHost(parsed.Hostname()) || parsed.Fragment != "" {
		return DatabaseURL{}, kitconfig.Invalid(
			databaseURLSetting,
			"must identify the Pricing reader",
		)
	}
	if port := parsed.Port(); port != "" {
		portNumber, err := strconv.ParseUint(port, 10, 16)
		if err != nil || portNumber == 0 {
			return DatabaseURL{}, kitconfig.Invalid(
				databaseURLSetting,
				"must use a valid port",
			)
		}
	}
	password, ok := parsed.User.Password()
	params, err := url.ParseQuery(parsed.RawQuery)
	if !ok || password == "" || err != nil || len(params) != 2 ||
		len(params["sslmode"]) != 1 || params.Get("sslmode") != "verify-full" ||
		len(params["sslrootcert"]) != 1 || params.Get("sslrootcert") == "" {
		return DatabaseURL{}, kitconfig.Invalid(
			databaseURLSetting,
			"must require TLS hostname and CA verification",
		)
	}
	return DatabaseURL{value: value}, nil
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
	if identity.Environment != "development" &&
		!validCanonicalVersion(identity.ServiceVersion) {
		return kitconfig.Invalid(
			kitconfig.EnvServiceVersion,
			canonicalVersionReason,
		)
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

func validCanonicalVersion(value string) bool {
	if len(value) != len("git-")+12 || !strings.HasPrefix(value, "git-") {
		return false
	}
	nonzero := false
	for _, ch := range value[len("git-"):] {
		if (ch < '0' || ch > '9') && (ch < 'a' || ch > 'f') {
			return false
		}
		if ch != '0' {
			nonzero = true
		}
	}
	return nonzero
}
