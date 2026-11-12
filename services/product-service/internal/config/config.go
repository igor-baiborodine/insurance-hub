package config

import (
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	kitconfig "github.com/igor-baiborodine/insurance-hub/libs/servicekit/config"
	kitidentity "github.com/igor-baiborodine/insurance-hub/libs/servicekit/identity"
)

// DefaultServiceName identifies startup diagnostics until configuration has been validated.
const DefaultServiceName = "product-service"

const (
	databaseURLSetting      = "PRODUCT_DATABASE_URL"
	databaseMaxConnsSetting = "DB_MAX_CONNS"
	databaseConnectSetting  = "DB_CONNECT_TIMEOUT"
	databaseAcquireSetting  = "DB_ACQUIRE_TIMEOUT"
	databaseQuerySetting    = "DB_QUERY_TIMEOUT"
	startupTimeoutSetting   = "STARTUP_TIMEOUT"
)

// Config contains the immutable settings parsed and validated during process startup.
type Config struct {
	kitconfig.Runtime
	ServiceName        string
	HTTPAddress        Address
	GRPCAddress        Address
	HealthAddress      Address
	HTTPMaxHeaderBytes int
	HTTPMaxBodyBytes   int
	Database           Database
	StartupTimeout     time.Duration
	kitconfig.HTTPServer
	kitconfig.GRPC
	kitconfig.Telemetry
}

// Identity adds Product validation policy to the shared resource and log fields.
type Identity struct{ kitidentity.Identity }

// DevelopmentIdentity labels startup diagnostics before configuration is validated.
func DevelopmentIdentity(serviceName string) Identity {
	return Identity{Identity: kitidentity.DevelopmentIdentity(serviceName, os.Getpid())}
}

// Address is a validated TCP listen address with a host and numeric port.
type Address struct {
	host string
	port uint16
}

func (address Address) String() string {
	return net.JoinHostPort(address.host, strconv.FormatUint(uint64(address.port), 10))
}

// Database contains the restricted Product reader connection and pool limits.
type Database struct {
	URL            DatabaseURL
	MaxConnections int32
	ConnectTimeout time.Duration
	AcquireTimeout time.Duration
	QueryTimeout   time.Duration
}

// DatabaseURL prevents the reader credential from being exposed through ordinary formatting.
type DatabaseURL struct {
	value string
}

// Value returns the PostgreSQL URL for the database adapter.
func (databaseURL DatabaseURL) Value() string {
	return databaseURL.value
}

func (DatabaseURL) String() string {
	return "[REDACTED]"
}

func (DatabaseURL) GoString() string {
	return "[REDACTED]"
}

// HTTPServer contains business HTTP server time bounds.
type HTTPServer = kitconfig.HTTPServer

// GRPC contains business gRPC message-size bounds.
type GRPC = kitconfig.GRPC

// Telemetry contains the optional OTLP trace and metric export configuration.
type Telemetry = kitconfig.Telemetry

// Load reads configuration from the process environment.
func Load() (Config, error) {
	return load(os.LookupEnv)
}

type lookupEnv = kitconfig.Lookup

func load(lookup lookupEnv) (Config, error) {
	serviceName, err := valueOrDefault(lookup, kitconfig.EnvServiceName, DefaultServiceName)
	if err != nil {
		return Config{}, err
	}
	if strings.TrimSpace(serviceName) == "" {
		return Config{}, invalid(kitconfig.EnvServiceName, "must not be blank")
	}

	httpAddress, err := loadAddress(
		lookup,
		kitconfig.EnvHTTPAddress,
		kitconfig.DefaultHTTPAddress,
	)
	if err != nil {
		return Config{}, err
	}
	grpcAddress, err := loadAddress(
		lookup,
		kitconfig.EnvGRPCAddress,
		kitconfig.DefaultGRPCAddress,
	)
	if err != nil {
		return Config{}, err
	}
	healthAddress, err := loadAddress(
		lookup,
		kitconfig.EnvHealthAddress,
		kitconfig.DefaultHealthAddress,
	)
	if err != nil {
		return Config{}, err
	}
	if err := validateDistinctAddresses(httpAddress, grpcAddress, healthAddress); err != nil {
		return Config{}, err
	}

	databaseURL, err := loadDatabaseURL(lookup)
	if err != nil {
		return Config{}, err
	}
	databaseMaxConnections, err := loadBoundedInt(
		lookup,
		databaseMaxConnsSetting,
		"8",
		1,
		32,
	)
	if err != nil {
		return Config{}, err
	}
	databaseConnectTimeout, err := loadPositiveDuration(
		lookup,
		databaseConnectSetting,
		"3s",
	)
	if err != nil {
		return Config{}, err
	}
	databaseAcquireTimeout, err := loadPositiveDuration(
		lookup,
		databaseAcquireSetting,
		"2s",
	)
	if err != nil {
		return Config{}, err
	}
	databaseQueryTimeout, err := loadPositiveDuration(
		lookup,
		databaseQuerySetting,
		"5s",
	)
	if err != nil {
		return Config{}, err
	}
	startupTimeout, err := loadPositiveDuration(lookup, startupTimeoutSetting, "10s")
	if err != nil {
		return Config{}, err
	}
	requestTimeout, err := loadPositiveDuration(lookup, kitconfig.EnvRequestTimeout, "8s")
	if err != nil {
		return Config{}, err
	}
	probeTimeout, err := loadPositiveDuration(lookup, kitconfig.EnvProbeTimeout, "2s")
	if err != nil {
		return Config{}, err
	}

	httpServer, err := kitconfig.LoadHTTPServer(lookup)
	if err != nil {
		return Config{}, err
	}
	httpMaxHeaderBytes, err := kitconfig.LoadBoundedInt(
		lookup,
		kitconfig.EnvHTTPMaxHeaderBytes,
		strconv.Itoa(kitconfig.DefaultHTTPMaxHeaderBytes),
		1024,
		1048576,
	)
	if err != nil {
		return Config{}, err
	}
	httpMaxBodyBytes, err := kitconfig.LoadBoundedInt(
		lookup,
		kitconfig.EnvHTTPMaxBodyBytes,
		strconv.Itoa(kitconfig.DefaultHTTPMaxBodyBytes),
		1024,
		1048576,
	)
	if err != nil {
		return Config{}, err
	}

	grpc, err := kitconfig.LoadGRPC(lookup)
	if err != nil {
		return Config{}, err
	}

	shutdownTimeout, err := loadPositiveDuration(lookup, kitconfig.EnvShutdownTimeout, "10s")
	if err != nil {
		return Config{}, err
	}
	logLevel, err := loadLogLevel(lookup)
	if err != nil {
		return Config{}, err
	}
	telemetrySettings, err := loadTelemetry(lookup, serviceName)
	if err != nil {
		return Config{}, err
	}

	if err := validateTimeoutRelationships(timeoutRelationships{
		databaseConnect: databaseConnectTimeout,
		databaseAcquire: databaseAcquireTimeout,
		databaseQuery:   databaseQueryTimeout,
		startup:         startupTimeout,
		request:         requestTimeout,
		probe:           probeTimeout,
		httpReadHeader:  httpServer.ReadHeaderTimeout,
		httpRead:        httpServer.ReadTimeout,
		httpWrite:       httpServer.WriteTimeout,
		shutdown:        shutdownTimeout,
		otelExporter:    telemetrySettings.ExporterTimeout,
	}); err != nil {
		return Config{}, err
	}
	if err := kitconfig.ValidateRuntime(kitconfig.Runtime{
		RequestTimeout: requestTimeout, ProbeTimeout: probeTimeout,
		ShutdownTimeout: shutdownTimeout, LogLevel: logLevel,
	}); err != nil {
		return Config{}, err
	}

	return Config{
		ServiceName:        serviceName,
		HTTPAddress:        httpAddress,
		GRPCAddress:        grpcAddress,
		HealthAddress:      healthAddress,
		HTTPMaxHeaderBytes: httpMaxHeaderBytes,
		HTTPMaxBodyBytes:   httpMaxBodyBytes,
		StartupTimeout:     startupTimeout,
		Runtime: kitconfig.Runtime{
			RequestTimeout:  requestTimeout,
			ProbeTimeout:    probeTimeout,
			ShutdownTimeout: shutdownTimeout,
			LogLevel:        logLevel,
		},
		Database: Database{
			URL:            databaseURL,
			MaxConnections: int32(databaseMaxConnections),
			ConnectTimeout: databaseConnectTimeout,
			AcquireTimeout: databaseAcquireTimeout,
			QueryTimeout:   databaseQueryTimeout,
		},
		HTTPServer: httpServer,
		GRPC:       grpc,
		Telemetry:  telemetrySettings,
	}, nil
}

func valueOrDefault(lookup lookupEnv, name, fallback string) (string, error) {
	value, exists := lookup(name)
	if !exists {
		return fallback, nil
	}
	if value == "" {
		return "", invalid(name, "must not be empty")
	}
	return value, nil
}

func loadAddress(lookup lookupEnv, name, fallback string) (Address, error) {
	value, err := valueOrDefault(lookup, name, fallback)
	if err != nil {
		return Address{}, err
	}
	return parseAddress(name, value)
}

func parseAddress(name, value string) (Address, error) {
	host, port, err := net.SplitHostPort(value)
	if err != nil || !kitconfig.ValidHost(host) || port == "" {
		return Address{}, invalid(name, "must include a valid host and numeric port")
	}
	portNumber, err := strconv.ParseUint(port, 10, 16)
	if err != nil {
		return Address{}, invalid(name, "must include a valid host and numeric port")
	}
	return Address{host: host, port: uint16(portNumber)}, nil
}

func validateDistinctAddresses(httpAddress, grpcAddress, healthAddress Address) error {
	addresses := map[string]string{}
	for _, candidate := range []struct {
		name    string
		address Address
	}{
		{name: kitconfig.EnvHTTPAddress, address: httpAddress},
		{name: kitconfig.EnvGRPCAddress, address: grpcAddress},
		{name: kitconfig.EnvHealthAddress, address: healthAddress},
	} {
		address := candidate.address.String()
		if other, exists := addresses[address]; exists {
			return invalid(candidate.name, "must be distinct from "+other)
		}
		addresses[address] = candidate.name
	}
	return nil
}

func loadDatabaseURL(lookup lookupEnv) (DatabaseURL, error) {
	value, exists := lookup(databaseURLSetting)
	if !exists || strings.TrimSpace(value) == "" {
		return DatabaseURL{}, invalid(databaseURLSetting, "is required")
	}
	parsed, err := url.Parse(value)
	if err != nil || (parsed.Scheme != "postgres" && parsed.Scheme != "postgresql") ||
		!kitconfig.ValidHost(parsed.Hostname()) || parsed.User == nil ||
		strings.TrimSpace(parsed.User.Username()) == "" || parsed.Fragment != "" ||
		parsed.Path == "" || parsed.Path == "/" {
		return DatabaseURL{}, invalid(databaseURLSetting, "must be a PostgreSQL URL")
	}
	if _, overridesUser := parsed.Query()["user"]; overridesUser {
		return DatabaseURL{}, invalid(databaseURLSetting, "must be a PostgreSQL URL")
	}
	if port := parsed.Port(); port != "" {
		portNumber, portErr := strconv.ParseUint(port, 10, 16)
		if portErr != nil || portNumber == 0 {
			return DatabaseURL{}, invalid(
				databaseURLSetting,
				"must be a PostgreSQL URL",
			)
		}
	}
	return DatabaseURL{value: value}, nil
}

func loadBoundedInt(
	lookup lookupEnv,
	name string,
	fallback string,
	minimum int,
	maximum int,
) (int, error) {
	value, err := valueOrDefault(lookup, name, fallback)
	if err != nil {
		return 0, err
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed < minimum || parsed > maximum {
		return 0, invalid(
			name,
			fmt.Sprintf("must be an integer from %d through %d", minimum, maximum),
		)
	}
	return parsed, nil
}

func loadPositiveDuration(lookup lookupEnv, name, fallback string) (time.Duration, error) {
	value, err := valueOrDefault(lookup, name, fallback)
	if err != nil {
		return 0, err
	}
	return parsePositiveDuration(name, value)
}

func parsePositiveDuration(name, value string) (time.Duration, error) {
	duration, err := time.ParseDuration(value)
	if err != nil || duration <= 0 {
		return 0, invalid(name, "must be a positive duration")
	}
	return duration, nil
}

func loadLogLevel(lookup lookupEnv) (slog.Level, error) {
	value, err := valueOrDefault(lookup, kitconfig.EnvLogLevel, "info")
	if err != nil {
		return 0, err
	}
	var level slog.Level
	if err := level.UnmarshalText([]byte(value)); err != nil {
		return 0, invalid(kitconfig.EnvLogLevel, "must be a supported slog level")
	}
	return level, nil
}

func loadTelemetry(lookup lookupEnv, serviceName string) (Telemetry, error) {
	enabledValue, err := valueOrDefault(lookup, kitconfig.EnvOTELEnabled, "false")
	if err != nil {
		return Telemetry{}, err
	}
	enabled, err := strconv.ParseBool(enabledValue)
	if err != nil {
		return Telemetry{}, invalid(kitconfig.EnvOTELEnabled, "must be a boolean")
	}
	timeout, err := loadPositiveDuration(lookup, kitconfig.EnvOTELExporterOTLPTimeout, "2s")
	if err != nil {
		return Telemetry{}, err
	}
	if enabled && timeout != 2*time.Second {
		return Telemetry{}, invalid(
			kitconfig.EnvOTELExporterOTLPTimeout,
			"must be 2s when telemetry is enabled",
		)
	}
	endpoint, err := parseOptionalEndpoint(lookup, enabled)
	if err != nil {
		return Telemetry{}, err
	}
	identity, err := loadIdentity(lookup, serviceName, enabled)
	if err != nil {
		return Telemetry{}, err
	}
	settings := Telemetry{
		Enabled:          enabled,
		ExporterEndpoint: endpoint,
		ExporterTimeout:  timeout,
		Identity:         identity.Identity,
	}
	if err := kitconfig.ValidateTelemetry(settings); err != nil {
		return Telemetry{}, err
	}
	return settings, nil
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
		value, err := valueOrDefault(lookup, field.name, fallback)
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

// Validate enforces Product's logical and deployed identity contract.
func (identity Identity) Validate(enabled bool) error {
	if identity.ServiceNamespace != "insurance-hub" || identity.Implementation != "go" {
		return kitconfig.Invalid(
			"telemetry identity",
			"must use the insurance-hub namespace and Go implementation",
		)
	}
	if identity.ServiceName != DefaultServiceName {
		return kitconfig.Invalid(
			kitconfig.EnvServiceName,
			"must identify the Product service",
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
	if enabled &&
		(identity.Environment == "development" || identity.ServiceVersion == "development" || strings.HasPrefix(identity.ServiceInstance, "local-")) {
		return kitconfig.Invalid(
			"telemetry identity",
			"must use explicit non-development environment, version and instance when enabled",
		)
	}
	if identity.Environment != "development" && !validSourceVersion(identity.ServiceVersion) {
		return kitconfig.Invalid(
			kitconfig.EnvServiceVersion,
			"must be git- followed by 12 nonzero lowercase source revision characters",
		)
	}
	return nil
}

func validSourceVersion(value string) bool {
	if len(value) != len("git-")+12 || !strings.HasPrefix(value, "git-") {
		return false
	}
	suffix := strings.TrimPrefix(value, "git-")
	allZero := true
	for _, ch := range suffix {
		if (ch < '0' || ch > '9') && (ch < 'a' || ch > 'f') {
			return false
		}
		if ch != '0' {
			allZero = false
		}
	}
	return !allZero
}

func validIdentityValue(value string) bool {
	if len(value) == 0 || len(value) > 128 {
		return false
	}
	for _, ch := range value {
		if (ch < 'a' || ch > 'z') && (ch < 'A' || ch > 'Z') && (ch < '0' || ch > '9') &&
			ch != '.' && ch != '_' && ch != '-' {
			return false
		}
	}
	return true
}

func parseOptionalEndpoint(lookup lookupEnv, enabled bool) (*url.URL, error) {
	value, exists := lookup(kitconfig.EnvOTELExporterOTLPEndpoint)
	if !exists || value == "" {
		if enabled {
			return nil, invalid(
				kitconfig.EnvOTELExporterOTLPEndpoint,
				"is required when telemetry is enabled",
			)
		}
		return nil, nil
	}

	endpoint, err := url.Parse(value)
	if err != nil || endpoint.Opaque != "" || endpoint.User != nil || endpoint.RawQuery != "" ||
		endpoint.Fragment != "" {
		return nil, invalid(
			kitconfig.EnvOTELExporterOTLPEndpoint,
			"must be an HTTP or HTTPS OTLP/gRPC URL",
		)
	}
	if endpoint.Scheme != "http" && endpoint.Scheme != "https" {
		return nil, invalid(
			kitconfig.EnvOTELExporterOTLPEndpoint,
			"must use the http or https scheme",
		)
	}
	if endpoint.Path != "" && endpoint.Path != "/" {
		return nil, invalid(
			kitconfig.EnvOTELExporterOTLPEndpoint,
			"must not include an OTLP/HTTP path",
		)
	}

	host := endpoint.Hostname()
	port := endpoint.Port()
	if !kitconfig.ValidHost(host) || port == "" {
		return nil, invalid(
			kitconfig.EnvOTELExporterOTLPEndpoint,
			"must include a valid host and numeric port",
		)
	}
	portNumber, err := strconv.ParseUint(port, 10, 16)
	if err != nil || portNumber == 0 {
		return nil, invalid(
			kitconfig.EnvOTELExporterOTLPEndpoint,
			"must include a valid host and numeric port",
		)
	}

	endpoint.Path = ""
	return endpoint, nil
}

type timeoutRelationships struct {
	databaseConnect time.Duration
	databaseAcquire time.Duration
	databaseQuery   time.Duration
	startup         time.Duration
	request         time.Duration
	probe           time.Duration
	httpReadHeader  time.Duration
	httpRead        time.Duration
	httpWrite       time.Duration
	shutdown        time.Duration
	otelExporter    time.Duration
}

func validateTimeoutRelationships(timeouts timeoutRelationships) error {
	for _, relationship := range []struct {
		setting string
		value   time.Duration
		bound   string
		maximum time.Duration
	}{
		{databaseConnectSetting, timeouts.databaseConnect, startupTimeoutSetting, timeouts.startup},
		{databaseAcquireSetting, timeouts.databaseAcquire, databaseQuerySetting, timeouts.databaseQuery},
		{databaseQuerySetting, timeouts.databaseQuery, kitconfig.EnvRequestTimeout, timeouts.request},
		{kitconfig.EnvProbeTimeout, timeouts.probe, databaseQuerySetting, timeouts.databaseQuery},
		{kitconfig.EnvHTTPReadHeaderTimeout, timeouts.httpReadHeader, kitconfig.EnvHTTPReadTimeout, timeouts.httpRead},
		{kitconfig.EnvRequestTimeout, timeouts.request, kitconfig.EnvHTTPWriteTimeout, timeouts.httpWrite},
		{kitconfig.EnvOTELExporterOTLPTimeout, timeouts.otelExporter, kitconfig.EnvShutdownTimeout, timeouts.shutdown},
	} {
		if relationship.value > relationship.maximum {
			return invalid(relationship.setting, "must not exceed "+relationship.bound)
		}
	}
	return nil
}

func invalid(name, problem string) error {
	return &validationError{setting: name, problem: problem}
}

// validationError contains only validator-owned setting names and reasons, never input values.
type validationError struct {
	setting string
	problem string
}

func (err *validationError) Error() string {
	return fmt.Sprintf("invalid %s: %s", err.setting, err.problem)
}

// ErrorDetails exposes safe configuration diagnostics without rendering arbitrary errors.
// Unknown errors retain a constant category; wrappers cannot contribute text to the log fields.
func ErrorDetails(err error) (setting, reason string) {
	if validation, ok := errors.AsType[*validationError](err); ok {
		return validation.setting, validation.problem
	}
	return kitconfig.ErrorDetails(err)
}
