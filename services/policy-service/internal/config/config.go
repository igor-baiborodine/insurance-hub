package config

import (
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
const DefaultServiceName = "policy-service"

const canonicalVersionReason = "must use git- followed by 12 lowercase hexadecimal " +
	"characters with at least one nonzero character outside development"

const (
	databaseURLSetting      = "POLICY_DATABASE_URL"
	databaseMaxConnsSetting = "DB_MAX_CONNS"
	databaseConnectSetting  = "DB_CONNECT_TIMEOUT"
	databaseQuerySetting    = "DB_QUERY_TIMEOUT"
	productTargetSetting    = "PRODUCT_GRPC_TARGET"
	pricingTargetSetting    = "PRICING_GRPC_TARGET"
	providerTimeoutSetting  = "PROVIDER_CALL_TIMEOUT"
)

// Database holds the optional Policy writer connection and pool limits. An absent URL leaves the
// service without storage; environments that have provisioned Policy PostgreSQL supply it.
type Database struct {
	URL            DatabaseURL
	MaxConnections int32
	ConnectTimeout time.Duration
	QueryTimeout   time.Duration
}

// DatabaseURL redacts the writer credential from ordinary formatting.
type DatabaseURL struct{ value string }

// Value returns the writer connection URL to the PostgreSQL adapter.
func (databaseURL DatabaseURL) Value() string { return databaseURL.value }

func (DatabaseURL) String() string   { return "[REDACTED]" }
func (DatabaseURL) GoString() string { return "[REDACTED]" }

// Configured reports whether a database connection was supplied.
func (database Database) Configured() bool { return database.URL.value != "" }

// Providers holds the private Product and Pricing gRPC targets used to create offers. Both are
// supplied together or neither is; without them the service cannot create offers.
type Providers struct {
	ProductTarget string
	PricingTarget string
	CallTimeout   time.Duration
}

// Configured reports whether both provider targets were supplied.
func (providers Providers) Configured() bool { return providers.ProductTarget != "" }

// Config contains settings parsed and validated once during process startup.
type Config struct {
	Database  Database
	Providers Providers
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

// GRPC is the shared message-size group; Policy owns its permitted ranges.
type GRPC = kitconfig.GRPC

// Identity adds Policy validation policy to the shared resource and log fields.
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

	database, err := loadDatabase(lookup, runtime.ProbeTimeout)
	if err != nil {
		return Config{}, err
	}
	providers, err := loadProviders(lookup, runtime.RequestTimeout)
	if err != nil {
		return Config{}, err
	}

	return Config{
		Database:           database,
		Providers:          providers,
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

func loadDatabase(lookup lookupEnv, probeTimeout time.Duration) (Database, error) {
	databaseURL, err := loadDatabaseURL(lookup)
	if err != nil {
		return Database{}, err
	}
	maxConnections, err := kitconfig.LoadBoundedInt(lookup, databaseMaxConnsSetting, "4", 1, 16)
	if err != nil {
		return Database{}, err
	}
	connectTimeout, err := kitconfig.LoadPositiveDuration(lookup, databaseConnectSetting, "3s")
	if err != nil {
		return Database{}, err
	}
	queryTimeout, err := kitconfig.LoadPositiveDuration(lookup, databaseQuerySetting, "1s")
	if err != nil {
		return Database{}, err
	}
	// Readiness runs the database check inside the probe budget.
	if databaseURL.value != "" && queryTimeout > probeTimeout {
		return Database{}, kitconfig.Invalid(
			databaseQuerySetting,
			"must not exceed PROBE_TIMEOUT",
		)
	}
	return Database{
		URL:            databaseURL,
		MaxConnections: int32(maxConnections),
		ConnectTimeout: connectTimeout,
		QueryTimeout:   queryTimeout,
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
		parsed.User == nil || parsed.Path != "/policy" ||
		!kitconfig.ValidHost(parsed.Hostname()) || parsed.Fragment != "" {
		return DatabaseURL{}, kitconfig.Invalid(
			databaseURLSetting,
			"must identify the Policy database",
		)
	}
	if port := parsed.Port(); port != "" {
		if number, err := strconv.ParseUint(port, 10, 16); err != nil || number == 0 {
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

func loadProviders(lookup lookupEnv, requestTimeout time.Duration) (Providers, error) {
	productTarget, err := loadTarget(lookup, productTargetSetting)
	if err != nil {
		return Providers{}, err
	}
	pricingTarget, err := loadTarget(lookup, pricingTargetSetting)
	if err != nil {
		return Providers{}, err
	}
	if (productTarget == "") != (pricingTarget == "") {
		return Providers{}, kitconfig.Invalid(
			productTargetSetting,
			"must be supplied together with "+pricingTargetSetting,
		)
	}
	callTimeout, err := kitconfig.LoadPositiveDuration(lookup, providerTimeoutSetting, "3s")
	if err != nil {
		return Providers{}, err
	}
	// Product and Pricing run in sequence inside one request.
	if productTarget != "" && 2*callTimeout >= requestTimeout {
		return Providers{}, kitconfig.Invalid(
			providerTimeoutSetting,
			"must leave both provider calls within REQUEST_TIMEOUT",
		)
	}
	return Providers{
		ProductTarget: productTarget,
		PricingTarget: pricingTarget,
		CallTimeout:   callTimeout,
	}, nil
}

// loadTarget accepts an optional host:port gRPC target with an explicit nonzero port.
func loadTarget(lookup lookupEnv, setting string) (string, error) {
	value, ok := lookup(setting)
	if !ok {
		return "", nil
	}
	host, port, err := net.SplitHostPort(value)
	if err != nil || !kitconfig.ValidHost(host) {
		return "", kitconfig.Invalid(setting, "must be a host:port gRPC target")
	}
	if number, err := strconv.ParseUint(port, 10, 16); err != nil || number == 0 {
		return "", kitconfig.Invalid(setting, "must use a valid port")
	}
	return value, nil
}
