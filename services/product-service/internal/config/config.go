package config

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// DefaultServiceName identifies startup diagnostics until configuration has been validated.
const DefaultServiceName = "product-service"

const (
	serviceNameSetting           = "SERVICE_NAME"
	httpAddressSetting           = "HTTP_ADDR"
	grpcAddressSetting           = "GRPC_ADDR"
	healthAddressSetting         = "HEALTH_ADDR"
	databaseURLSetting           = "PRODUCT_DATABASE_URL"
	databaseMaxConnsSetting      = "DB_MAX_CONNS"
	databaseConnectSetting       = "DB_CONNECT_TIMEOUT"
	databaseAcquireSetting       = "DB_ACQUIRE_TIMEOUT"
	databaseQuerySetting         = "DB_QUERY_TIMEOUT"
	startupTimeoutSetting        = "STARTUP_TIMEOUT"
	requestTimeoutSetting        = "REQUEST_TIMEOUT"
	probeTimeoutSetting          = "PROBE_TIMEOUT"
	httpReadHeaderTimeoutSetting = "HTTP_READ_HEADER_TIMEOUT"
	httpReadTimeoutSetting       = "HTTP_READ_TIMEOUT"
	httpWriteTimeoutSetting      = "HTTP_WRITE_TIMEOUT"
	httpIdleTimeoutSetting       = "HTTP_IDLE_TIMEOUT"
	grpcMaxReceiveBytesSetting   = "GRPC_MAX_RECV_BYTES"
	grpcMaxSendBytesSetting      = "GRPC_MAX_SEND_BYTES"
	shutdownTimeoutSetting       = "SHUTDOWN_TIMEOUT"
	logLevelSetting              = "LOG_LEVEL"
	otelEnabledSetting           = "OTEL_ENABLED"
	otelEndpointSetting          = "OTEL_EXPORTER_OTLP_ENDPOINT"
	otelTimeoutSetting           = "OTEL_EXPORTER_OTLP_TIMEOUT"
	environmentSetting           = "DEPLOYMENT_ENVIRONMENT"
	serviceVersionSetting        = "SERVICE_VERSION"
	serviceInstanceSetting       = "SERVICE_INSTANCE_ID"
)

var (
	qaVersionPattern   = regexp.MustCompile(`^sha-[0-9a-f]{7,40}$`)
	testVersionPattern = regexp.MustCompile(`^test-[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
)

// Config contains the immutable settings parsed and validated during process startup.
type Config struct {
	ServiceName     string
	HTTPAddress     Address
	GRPCAddress     Address
	HealthAddress   Address
	Database        Database
	StartupTimeout  time.Duration
	RequestTimeout  time.Duration
	ProbeTimeout    time.Duration
	HTTPServer      HTTPServer
	GRPC            GRPC
	ShutdownTimeout time.Duration
	LogLevel        slog.Level
	Telemetry       Telemetry
}

// Identity is the application-owned resource and log identity. Kubernetes fields are added by Alloy.
type Identity struct {
	ServiceNamespace string
	ServiceName      string
	ServiceVersion   string
	ServiceInstance  string
	Environment      string
	Implementation   string
}

// DevelopmentIdentity labels startup diagnostics before configuration is validated.
func DevelopmentIdentity(serviceName string) Identity {
	return Identity{
		ServiceNamespace: "insurance-hub",
		ServiceName:      serviceName,
		ServiceVersion:   "development",
		ServiceInstance:  fmt.Sprintf("local-%d", os.Getpid()),
		Environment:      "development",
		Implementation:   "go",
	}
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
type HTTPServer struct {
	ReadHeaderTimeout time.Duration
	ReadTimeout       time.Duration
	WriteTimeout      time.Duration
	IdleTimeout       time.Duration
}

// GRPC contains business gRPC message-size bounds.
type GRPC struct {
	MaxReceiveBytes int
	MaxSendBytes    int
}

// Telemetry contains the optional OTLP trace and metric export configuration.
type Telemetry struct {
	Enabled          bool
	ExporterEndpoint *url.URL
	ExporterTimeout  time.Duration
	Identity         Identity
}

// Load reads configuration from the process environment.
func Load() (Config, error) {
	return load(os.LookupEnv)
}

type lookupEnv func(string) (string, bool)

func load(lookup lookupEnv) (Config, error) {
	serviceName, err := valueOrDefault(lookup, serviceNameSetting, DefaultServiceName)
	if err != nil {
		return Config{}, err
	}
	if strings.TrimSpace(serviceName) == "" {
		return Config{}, invalid(serviceNameSetting, "must not be blank")
	}

	httpAddress, err := loadAddress(lookup, httpAddressSetting, "127.0.0.1:8081")
	if err != nil {
		return Config{}, err
	}
	grpcAddress, err := loadAddress(lookup, grpcAddressSetting, "127.0.0.1:9090")
	if err != nil {
		return Config{}, err
	}
	healthAddress, err := loadAddress(lookup, healthAddressSetting, "127.0.0.1:8080")
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
	requestTimeout, err := loadPositiveDuration(lookup, requestTimeoutSetting, "8s")
	if err != nil {
		return Config{}, err
	}
	probeTimeout, err := loadPositiveDuration(lookup, probeTimeoutSetting, "2s")
	if err != nil {
		return Config{}, err
	}

	httpReadHeaderTimeout, err := loadPositiveDuration(
		lookup,
		httpReadHeaderTimeoutSetting,
		"5s",
	)
	if err != nil {
		return Config{}, err
	}
	httpReadTimeout, err := loadPositiveDuration(lookup, httpReadTimeoutSetting, "10s")
	if err != nil {
		return Config{}, err
	}
	httpWriteTimeout, err := loadPositiveDuration(lookup, httpWriteTimeoutSetting, "10s")
	if err != nil {
		return Config{}, err
	}
	httpIdleTimeout, err := loadPositiveDuration(lookup, httpIdleTimeoutSetting, "30s")
	if err != nil {
		return Config{}, err
	}

	grpcMaxReceiveBytes, err := loadBoundedInt(
		lookup,
		grpcMaxReceiveBytesSetting,
		"1048576",
		1,
		16777216,
	)
	if err != nil {
		return Config{}, err
	}
	grpcMaxSendBytes, err := loadBoundedInt(
		lookup,
		grpcMaxSendBytesSetting,
		"8388608",
		1,
		67108864,
	)
	if err != nil {
		return Config{}, err
	}

	shutdownTimeout, err := loadPositiveDuration(lookup, shutdownTimeoutSetting, "10s")
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
		httpReadHeader:  httpReadHeaderTimeout,
		httpRead:        httpReadTimeout,
		httpWrite:       httpWriteTimeout,
		shutdown:        shutdownTimeout,
		otelExporter:    telemetrySettings.ExporterTimeout,
	}); err != nil {
		return Config{}, err
	}

	return Config{
		ServiceName:    serviceName,
		HTTPAddress:    httpAddress,
		GRPCAddress:    grpcAddress,
		HealthAddress:  healthAddress,
		StartupTimeout: startupTimeout,
		RequestTimeout: requestTimeout,
		ProbeTimeout:   probeTimeout,
		Database: Database{
			URL:            databaseURL,
			MaxConnections: int32(databaseMaxConnections),
			ConnectTimeout: databaseConnectTimeout,
			AcquireTimeout: databaseAcquireTimeout,
			QueryTimeout:   databaseQueryTimeout,
		},
		HTTPServer: HTTPServer{
			ReadHeaderTimeout: httpReadHeaderTimeout,
			ReadTimeout:       httpReadTimeout,
			WriteTimeout:      httpWriteTimeout,
			IdleTimeout:       httpIdleTimeout,
		},
		GRPC: GRPC{
			MaxReceiveBytes: grpcMaxReceiveBytes,
			MaxSendBytes:    grpcMaxSendBytes,
		},
		ShutdownTimeout: shutdownTimeout,
		LogLevel:        logLevel,
		Telemetry:       telemetrySettings,
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
	if err != nil || !validHost(host) || port == "" {
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
		{name: httpAddressSetting, address: httpAddress},
		{name: grpcAddressSetting, address: grpcAddress},
		{name: healthAddressSetting, address: healthAddress},
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
		!validHost(parsed.Hostname()) || parsed.User == nil ||
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
	value, err := valueOrDefault(lookup, logLevelSetting, "info")
	if err != nil {
		return 0, err
	}
	var level slog.Level
	if err := level.UnmarshalText([]byte(value)); err != nil {
		return 0, invalid(logLevelSetting, "must be a supported slog level")
	}
	return level, nil
}

func loadTelemetry(lookup lookupEnv, serviceName string) (Telemetry, error) {
	enabledValue, err := valueOrDefault(lookup, otelEnabledSetting, "false")
	if err != nil {
		return Telemetry{}, err
	}
	enabled, err := strconv.ParseBool(enabledValue)
	if err != nil {
		return Telemetry{}, invalid(otelEnabledSetting, "must be a boolean")
	}
	timeout, err := loadPositiveDuration(lookup, otelTimeoutSetting, "2s")
	if err != nil {
		return Telemetry{}, err
	}
	if enabled && timeout != 2*time.Second {
		return Telemetry{}, invalid(
			otelTimeoutSetting,
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
	return Telemetry{
		Enabled:          enabled,
		ExporterEndpoint: endpoint,
		ExporterTimeout:  timeout,
		Identity:         identity,
	}, nil
}

func loadIdentity(lookup lookupEnv, serviceName string, enabled bool) (Identity, error) {
	defaultEnvironment := "development"
	if enabled {
		defaultEnvironment = ""
	}
	environment, err := valueOrDefault(lookup, environmentSetting, defaultEnvironment)
	if err != nil {
		return Identity{}, err
	}
	version, err := valueOrDefault(lookup, serviceVersionSetting, "development")
	if err != nil {
		return Identity{}, err
	}
	instance, err := valueOrDefault(lookup, serviceInstanceSetting, "")
	if err != nil {
		return Identity{}, err
	}
	if environment == "test" && instance != "" {
		return Identity{}, invalid(serviceInstanceSetting, "must be generated in test mode")
	}
	if environment == "test" {
		instance, err = randomInstanceID()
		if err != nil {
			return Identity{}, invalid(
				serviceInstanceSetting,
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
	if err := identity.Validate(enabled); err != nil {
		return Identity{}, err
	}
	return identity, nil
}

// Validate rejects an incomplete or conflicting telemetry identity without echoing its values.
func (identity Identity) Validate(enabled bool) error {
	if identity.ServiceNamespace != "insurance-hub" || identity.Implementation != "go" {
		return invalid(
			"telemetry identity",
			"must use the Product namespace and Go implementation",
		)
	}
	if identity.ServiceName == "" ||
		strings.TrimSpace(identity.ServiceName) != identity.ServiceName {
		return invalid(serviceNameSetting, "must not be blank or padded")
	}
	if enabled && identity.ServiceName != DefaultServiceName {
		return invalid(
			serviceNameSetting,
			"must be product-service when telemetry is enabled",
		)
	}
	switch identity.Environment {
	case "qa":
		if !qaVersionPattern.MatchString(identity.ServiceVersion) {
			return invalid(
				serviceVersionSetting,
				"must be an immutable sha version in QA",
			)
		}
		if !validUUID(identity.ServiceInstance) {
			return invalid(serviceInstanceSetting, "must be a Pod UID in QA")
		}
	case "test":
		if !testVersionPattern.MatchString(identity.ServiceVersion) {
			return invalid(serviceVersionSetting, "must be a test revision")
		}
		if !validUUID(identity.ServiceInstance) {
			return invalid(serviceInstanceSetting, "must be a UUID in test mode")
		}
	case "development":
		if enabled {
			return invalid(
				environmentSetting,
				"must be qa or test when telemetry is enabled",
			)
		}
		if identity.ServiceVersion != "development" ||
			!strings.HasPrefix(identity.ServiceInstance, "local-") {
			return invalid(
				"telemetry identity",
				"must use development identity when disabled locally",
			)
		}
	default:
		return invalid(environmentSetting, "must be qa, test, or disabled development")
	}
	return nil
}

func validUUID(value string) bool {
	if len(value) != 36 || value[8] != '-' || value[13] != '-' || value[18] != '-' ||
		value[23] != '-' {
		return false
	}
	_, err := hex.DecodeString(strings.ReplaceAll(value, "-", ""))
	return err == nil
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

func parseOptionalEndpoint(lookup lookupEnv, enabled bool) (*url.URL, error) {
	value, exists := lookup(otelEndpointSetting)
	if !exists || value == "" {
		if enabled {
			return nil, invalid(
				otelEndpointSetting,
				"is required when telemetry is enabled",
			)
		}
		return nil, nil
	}

	endpoint, err := url.Parse(value)
	if err != nil || endpoint.Opaque != "" || endpoint.User != nil || endpoint.RawQuery != "" ||
		endpoint.Fragment != "" {
		return nil, invalid(otelEndpointSetting, "must be an HTTP or HTTPS OTLP/gRPC URL")
	}
	if endpoint.Scheme != "http" && endpoint.Scheme != "https" {
		return nil, invalid(otelEndpointSetting, "must use the http or https scheme")
	}
	if endpoint.Path != "" && endpoint.Path != "/" {
		return nil, invalid(otelEndpointSetting, "must not include an OTLP/HTTP path")
	}

	host := endpoint.Hostname()
	port := endpoint.Port()
	if !validHost(host) || port == "" {
		return nil, invalid(
			otelEndpointSetting,
			"must include a valid host and numeric port",
		)
	}
	portNumber, err := strconv.ParseUint(port, 10, 16)
	if err != nil || portNumber == 0 {
		return nil, invalid(
			otelEndpointSetting,
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
		{databaseQuerySetting, timeouts.databaseQuery, requestTimeoutSetting, timeouts.request},
		{probeTimeoutSetting, timeouts.probe, databaseQuerySetting, timeouts.databaseQuery},
		{httpReadHeaderTimeoutSetting, timeouts.httpReadHeader, httpReadTimeoutSetting, timeouts.httpRead},
		{requestTimeoutSetting, timeouts.request, httpWriteTimeoutSetting, timeouts.httpWrite},
		{otelTimeoutSetting, timeouts.otelExporter, shutdownTimeoutSetting, timeouts.shutdown},
	} {
		if relationship.value > relationship.maximum {
			return invalid(relationship.setting, "must not exceed "+relationship.bound)
		}
	}
	return nil
}

func validHost(host string) bool {
	if host == "" || strings.ContainsAny(host, " \t\r\n") {
		return false
	}
	if net.ParseIP(host) != nil {
		return true
	}
	if len(host) > 253 {
		return false
	}
	for _, label := range strings.Split(host, ".") {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, character := range label {
			if (character < 'a' || character > 'z') &&
				(character < 'A' || character > 'Z') &&
				(character < '0' || character > '9') &&
				character != '-' {
				return false
			}
		}
	}
	return true
}

func invalid(name, problem string) error {
	return fmt.Errorf("invalid %s: %s", name, problem)
}
