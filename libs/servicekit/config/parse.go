package config

import (
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Lookup is an explicit environment source. Services choose when and what to load.
type Lookup func(string) (string, bool)

// Address is a parsed TCP listen address. Its zero value is not a configured listener.
type Address struct {
	host string
	port uint16
}

func (address Address) String() string {
	return net.JoinHostPort(address.host, strconv.FormatUint(uint64(address.port), 10))
}

// Port exposes the numeric port so callers can apply their own collision policy.
func (address Address) Port() uint16 { return address.port }

// ParseLogLevel accepts slog levels, including numeric offsets.
func ParseLogLevel(name, value string) (slog.Level, error) {
	var level slog.Level
	if err := level.UnmarshalText([]byte(value)); err != nil {
		return 0, Invalid(name, "must be a supported slog level")
	}
	return level, nil
}

// ParseBool accepts the standard Go boolean representations.
func ParseBool(name, value string) (bool, error) {
	result, err := strconv.ParseBool(value)
	if err != nil {
		return false, Invalid(name, "must be a boolean")
	}
	return result, nil
}

// ValueOrDefault returns the fallback only for absent values; explicit empty values fail.
func ValueOrDefault(lookup Lookup, name, fallback string) (string, error) {
	value, exists := lookup(name)
	if !exists {
		return fallback, nil
	}
	if value == "" {
		return "", Invalid(name, "must not be empty")
	}
	return value, nil
}

// LoadAddress looks up and parses a TCP address using an explicit fallback.
func LoadAddress(lookup Lookup, name, fallback string) (Address, error) {
	value, err := ValueOrDefault(lookup, name, fallback)
	if err != nil {
		return Address{}, err
	}
	return ParseAddress(name, value)
}

// ParseAddress accepts a host and numeric port, including ephemeral port zero.
func ParseAddress(name, value string) (Address, error) {
	host, port, err := net.SplitHostPort(value)
	if err != nil || !ValidHost(host) || port == "" {
		return Address{}, Invalid(name, "must include a valid host and numeric port")
	}
	portNumber, err := strconv.ParseUint(port, 10, 16)
	if err != nil {
		return Address{}, Invalid(name, "must include a valid host and numeric port")
	}
	return Address{host: host, port: uint16(portNumber)}, nil
}

// LoadBoundedInt looks up an integer with inclusive caller-owned bounds.
func LoadBoundedInt(
	lookup Lookup,
	name string,
	fallback string,
	minimum int,
	maximum int,
) (int, error) {
	value, err := ValueOrDefault(lookup, name, fallback)
	if err != nil {
		return 0, err
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed < minimum || parsed > maximum {
		return 0, Invalid(
			name,
			fmt.Sprintf("must be an integer from %d through %d", minimum, maximum),
		)
	}
	return parsed, nil
}

// LoadPositiveDuration looks up and parses a positive duration.
func LoadPositiveDuration(lookup Lookup, name, fallback string) (time.Duration, error) {
	value, err := ValueOrDefault(lookup, name, fallback)
	if err != nil {
		return 0, err
	}
	return ParsePositiveDuration(name, value)
}

// ParsePositiveDuration parses a duration greater than zero.
func ParsePositiveDuration(name, value string) (time.Duration, error) {
	duration, err := time.ParseDuration(value)
	if err != nil || duration <= 0 {
		return 0, Invalid(name, "must be a positive duration")
	}
	return duration, nil
}

// OptionalEndpoint allows absent or empty endpoints only when export is disabled.
func OptionalEndpoint(lookup Lookup, name string, enabled bool) (*url.URL, error) {
	value, exists := lookup(name)
	if !exists || value == "" {
		if enabled {
			return nil, Invalid(
				name,
				"is required when telemetry is enabled",
			)
		}
		return nil, nil
	}

	return ParseEndpoint(name, value)
}

// ParseEndpoint parses an OTLP/gRPC URL without retaining input in errors.
func ParseEndpoint(name, value string) (*url.URL, error) {
	endpoint, err := url.Parse(value)
	if err != nil || endpoint.Opaque != "" || endpoint.User != nil || endpoint.RawQuery != "" ||
		endpoint.Fragment != "" {
		return nil, Invalid(name, "must be an HTTP or HTTPS OTLP/gRPC URL")
	}
	if endpoint.Scheme != "http" && endpoint.Scheme != "https" {
		return nil, Invalid(name, "must use the http or https scheme")
	}
	if endpoint.Path != "" && endpoint.Path != "/" {
		return nil, Invalid(name, "must not include an OTLP/HTTP path")
	}

	host := endpoint.Hostname()
	port := endpoint.Port()
	if !ValidHost(host) || port == "" {
		return nil, Invalid(
			name,
			"must include a valid host and numeric port",
		)
	}
	portNumber, err := strconv.ParseUint(port, 10, 16)
	if err != nil || portNumber == 0 {
		return nil, Invalid(
			name,
			"must include a valid host and numeric port",
		)
	}

	endpoint.Path = ""
	return endpoint, nil
}

// ValidHost reports whether an unbracketed host is an IP literal or an ASCII DNS-style name.
// It rejects empty hosts, whitespace, ports and URL text without resolving the name.
func ValidHost(host string) bool {
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
