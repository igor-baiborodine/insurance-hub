package config

import (
	"log/slog"
	"net/url"
	"time"

	"github.com/igor-baiborodine/insurance-hub/libs/servicekit/identity"
)

const (
	// DefaultHTTPAddress reserves the business HTTP port; it does not create an API.
	DefaultHTTPAddress = "127.0.0.1:8080"
	// DefaultHealthAddress is the management HTTP listener default.
	DefaultHealthAddress = "127.0.0.1:8081"
	// DefaultGRPCAddress is the business gRPC listener default.
	DefaultGRPCAddress = "127.0.0.1:9090"
)

// Runtime is a small value-only group suitable for embedding or named composition.
// It has no loading or validation methods; callers must invoke validation explicitly.
type Runtime struct {
	RequestTimeout  time.Duration
	ProbeTimeout    time.Duration
	ShutdownTimeout time.Duration
	LogLevel        slog.Level
}

// DefaultRuntime returns shared defaults without accessing process state.
func DefaultRuntime() Runtime {
	return Runtime{
		RequestTimeout: 8 * time.Second, ProbeTimeout: 2 * time.Second,
		ShutdownTimeout: 10 * time.Second, LogLevel: slog.LevelInfo,
	}
}

// ValidateRuntime checks positive durations only. Service-specific relationships
// (database, HTTP write, exporter/drain budgets) remain in the local validator.
func ValidateRuntime(settings Runtime) error {
	for _, field := range []struct {
		name  string
		value time.Duration
	}{
		{EnvRequestTimeout, settings.RequestTimeout},
		{EnvProbeTimeout, settings.ProbeTimeout},
		{EnvShutdownTimeout, settings.ShutdownTimeout},
	} {
		if field.value <= 0 {
			return Invalid(field.name, "must be a positive duration")
		}
	}
	return nil
}

// GRPC contains message-size bounds. Service loaders choose permitted ranges.
type GRPC struct {
	MaxReceiveBytes int
	MaxSendBytes    int
}

// DefaultGRPC returns the existing consumer message-size defaults.
func DefaultGRPC() GRPC {
	return GRPC{MaxReceiveBytes: 1048576, MaxSendBytes: 8388608}
}

// HTTPServer contains business HTTP server time bounds.
type HTTPServer struct {
	ReadHeaderTimeout time.Duration
	ReadTimeout       time.Duration
	WriteTimeout      time.Duration
	IdleTimeout       time.Duration
}

// DefaultHTTPServer returns HTTP defaults without creating a listener.
func DefaultHTTPServer() HTTPServer {
	return HTTPServer{
		ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second,
		WriteTimeout: 10 * time.Second, IdleTimeout: 30 * time.Second,
	}
}

// Telemetry contains optional OTLP/gRPC export settings and caller-validated identity.
// Treat settings, including ExporterEndpoint, as immutable after startup validation.
type Telemetry struct {
	Enabled          bool
	ExporterEndpoint *url.URL
	ExporterTimeout  time.Duration
	Identity         identity.Identity
}

// DefaultTelemetry disables export and leaves identity for the local loader.
func DefaultTelemetry() Telemetry { return Telemetry{ExporterTimeout: 2 * time.Second} }

// ValidateTelemetry checks neutral exporter mechanics only. It does not validate
// identity or impose Product's enabled exactly-2s timeout or service drain budget.
func ValidateTelemetry(settings Telemetry) error {
	if settings.ExporterTimeout <= 0 {
		return Invalid(EnvOTELExporterOTLPTimeout, "must be a positive duration")
	}
	if settings.ExporterEndpoint == nil {
		if settings.Enabled {
			return Invalid(
				EnvOTELExporterOTLPEndpoint,
				"is required when telemetry is enabled",
			)
		}
		return nil
	}
	_, err := ParseEndpoint(EnvOTELExporterOTLPEndpoint, settings.ExporterEndpoint.String())
	return err
}
