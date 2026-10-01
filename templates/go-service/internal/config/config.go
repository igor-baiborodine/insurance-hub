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

const (
	serviceNameSetting     = "SERVICE_NAME"
	grpcAddressSetting     = "GRPC_ADDR"
	healthAddressSetting   = "HEALTH_ADDR"
	logLevelSetting        = "LOG_LEVEL"
	shutdownTimeoutSetting = "SHUTDOWN_TIMEOUT"
	otelEnabledSetting     = "OTEL_ENABLED"
	otelEndpointSetting    = "OTEL_EXPORTER_OTLP_ENDPOINT"
	otelTimeoutSetting     = "OTEL_EXPORTER_OTLP_TIMEOUT"
)

// Config contains settings parsed and validated once during process startup.
type Config struct {
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

// Telemetry contains the optional OTLP trace-export configuration.
type Telemetry struct {
	Enabled          bool
	ExporterEndpoint *url.URL
	ExporterTimeout time.Duration
}

// Load reads configuration from the process environment.
func Load() (Config, error) {
	return load(os.LookupEnv)
}

type lookupEnv func(string) (string, bool)

func load(lookup lookupEnv) (Config, error) {
	serviceName, err := valueOrDefault(lookup, serviceNameSetting, "go-service")
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

	otelTimeoutValue, err := valueOrDefault(lookup, otelTimeoutSetting, "5s")
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

	return Config{
		ServiceName:     serviceName,
		GRPCAddress:     grpcAddress,
		HealthAddress:   healthAddress,
		LogLevel:        logLevel,
		ShutdownTimeout: shutdownTimeout,
		Telemetry: Telemetry{
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
			return nil, invalid(otelEndpointSetting, "is required when telemetry is enabled")
		}
		return nil, nil
	}
	if value == "" {
		if enabled {
			return nil, invalid(otelEndpointSetting, "is required when telemetry is enabled")
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
		return nil, invalid(otelEndpointSetting, "must include a valid host and numeric port")
	}
	portNumber, err := strconv.ParseUint(port, 10, 16)
	if err != nil || portNumber == 0 {
		return nil, invalid(otelEndpointSetting, "must include a valid host and numeric port")
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
			if (character < 'a' || character > 'z') && (character < 'A' || character > 'Z') &&
				(character < '0' || character > '9') && character != '-' {
				return false
			}
		}
	}
	return true
}

func invalid(name, problem string) error {
	return fmt.Errorf("invalid %s: %s", name, problem)
}
