package config

import (
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
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
	RequestTimeout  time.Duration
	ProbeTimeout    time.Duration
	GRPC            GRPC
	ServiceName     string
	GRPCAddress     Address
	HealthAddress   Address
	LogLevel        slog.Level
	ShutdownTimeout time.Duration
	Telemetry       Telemetry
}

// Address is a validated TCP listen address with a host and numeric port.
type Address struct {
	host string
	port uint16
}

func (address Address) String() string {
	return net.JoinHostPort(address.host, strconv.FormatUint(uint64(address.port), 10))
}

// GRPC contains business gRPC message-size bounds.
type GRPC struct {
	MaxReceiveBytes int
	MaxSendBytes    int
}

// Identity is the application-owned resource and log identity. Collectors add infrastructure fields.
type Identity struct {
	ServiceNamespace string
	ServiceName      string
	ServiceVersion   string
	ServiceInstance  string
	Environment      string
	Implementation   string
}

// DevelopmentIdentity labels local startup diagnostics before configuration is validated.
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

// Telemetry contains optional OTLP trace and metric export configuration.
type Telemetry struct {
	Identity         Identity
	Enabled          bool
	ExporterEndpoint *url.URL
	ExporterTimeout  time.Duration
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

	grpcAddressValue, err := valueOrDefault(lookup, grpcAddressSetting, "127.0.0.1:9090")
	if err != nil {
		return Config{}, err
	}
	grpcAddress, err := parseAddress(grpcAddressSetting, grpcAddressValue)
	if err != nil {
		return Config{}, err
	}

	healthAddressValue, err := valueOrDefault(lookup, healthAddressSetting, "127.0.0.1:8080")
	if err != nil {
		return Config{}, err
	}
	healthAddress, err := parseAddress(healthAddressSetting, healthAddressValue)
	if err != nil {
		return Config{}, err
	}

	logLevelValue, err := valueOrDefault(lookup, logLevelSetting, "info")
	if err != nil {
		return Config{}, err
	}
	logLevel, err := parseLogLevel(logLevelValue)
	if err != nil {
		return Config{}, err
	}

	shutdownTimeoutValue, err := valueOrDefault(lookup, shutdownTimeoutSetting, "10s")
	if err != nil {
		return Config{}, err
	}
	shutdownTimeout, err := parsePositiveDuration(shutdownTimeoutSetting, shutdownTimeoutValue)
	if err != nil {
		return Config{}, err
	}

	otelEnabledValue, err := valueOrDefault(lookup, otelEnabledSetting, "false")
	if err != nil {
		return Config{}, err
	}
	otelEnabled, err := strconv.ParseBool(otelEnabledValue)
	if err != nil {
		return Config{}, invalid(otelEnabledSetting, "must be a boolean")
	}

	otelTimeoutValue, err := valueOrDefault(lookup, otelTimeoutSetting, "2s")
	if err != nil {
		return Config{}, err
	}
	otelTimeout, err := parsePositiveDuration(otelTimeoutSetting, otelTimeoutValue)
	if err != nil {
		return Config{}, err
	}

	otelEndpoint, err := parseOptionalEndpoint(lookup, otelEnabled)
	if err != nil {
		return Config{}, err
	}

	identity, err := loadIdentity(lookup, serviceName, otelEnabled)
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
	receiveBytes, err := loadBoundedInt(
		lookup,
		grpcMaxReceiveBytesSetting,
		"1048576",
		1,
		16777216,
	)
	if err != nil {
		return Config{}, err
	}
	sendBytes, err := loadBoundedInt(lookup, grpcMaxSendBytesSetting, "8388608", 1, 67108864)
	if err != nil {
		return Config{}, err
	}
	if otelTimeout > shutdownTimeout {
		return Config{}, invalid(otelTimeoutSetting, "must not exceed SHUTDOWN_TIMEOUT")
	}
	if grpcAddress.port != 0 && grpcAddress.String() == healthAddress.String() {
		return Config{}, invalid(healthAddressSetting, "must be distinct from GRPC_ADDR")
	}

	return Config{
		RequestTimeout: requestTimeout, ProbeTimeout: probeTimeout,
		GRPC:            GRPC{MaxReceiveBytes: receiveBytes, MaxSendBytes: sendBytes},
		ServiceName:     serviceName,
		GRPCAddress:     grpcAddress,
		HealthAddress:   healthAddress,
		LogLevel:        logLevel,
		ShutdownTimeout: shutdownTimeout,
		Telemetry: Telemetry{
			Identity:         identity,
			Enabled:          otelEnabled,
			ExporterEndpoint: otelEndpoint,
			ExporterTimeout:  otelTimeout,
		},
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

func parseLogLevel(value string) (slog.Level, error) {
	var level slog.Level
	if err := level.UnmarshalText([]byte(value)); err != nil {
		return 0, invalid(logLevelSetting, "must be a supported slog level")
	}
	return level, nil
}

func parsePositiveDuration(name, value string) (time.Duration, error) {
	duration, err := time.ParseDuration(value)
	if err != nil || duration <= 0 {
		return 0, invalid(name, "must be a positive duration")
	}
	return duration, nil
}

func parseOptionalEndpoint(lookup lookupEnv, enabled bool) (*url.URL, error) {
	value, exists := lookup(otelEndpointSetting)
	if !exists {
		if enabled {
			return nil, invalid(
				otelEndpointSetting,
				"is required when telemetry is enabled",
			)
		}
		return nil, nil
	}
	if value == "" {
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

// Validate rejects incomplete identity without echoing supplied values.
func (identity Identity) Validate(enabled bool) error {
	if identity.ServiceNamespace != "insurance-hub" || identity.Implementation != "go" {
		return invalid(
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
			return invalid(
				field.name,
				"must contain 1 through 128 ASCII letters, digits, dots, underscores or hyphens",
			)
		}
	}
	if enabled &&
		(identity.Environment == "development" || identity.ServiceVersion == "development" || strings.HasPrefix(identity.ServiceInstance, "local-")) {
		return invalid(
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
