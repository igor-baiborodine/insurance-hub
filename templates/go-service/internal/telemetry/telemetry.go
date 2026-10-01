package telemetry

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/igor-baiborodine/insurance-hub/templates/go-service/internal/config"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
)

// Provider owns the trace provider, propagation policy, and bounded exporter cleanup.
type Provider struct {
	tracerProvider  trace.TracerProvider
	propagator      propagation.TextMapPropagator
	sdkProvider     *sdktrace.TracerProvider
	exporter        *ownedExporter
	exporterTimeout time.Duration
}

// New constructs disabled no-op telemetry or an OTLP/gRPC-backed trace provider.
func New(ctx context.Context, serviceName string, settings config.Telemetry) (*Provider, error) {
	return newProvider(ctx, serviceName, settings, newOTLPExporter)
}

// TracerProvider returns the explicitly owned provider for transport instrumentation.
func (provider *Provider) TracerProvider() trace.TracerProvider {
	return provider.tracerProvider
}

// Propagator returns W3C trace-context propagation for inbound and outbound transports.
func (provider *Provider) Propagator() propagation.TextMapPropagator {
	return provider.propagator
}

// ForceFlush exports ended spans within both the caller's deadline and the configured exporter
// timeout. Disabled telemetry returns immediately.
func (provider *Provider) ForceFlush(ctx context.Context) error {
	if provider.sdkProvider == nil {
		return nil
	}
	flushContext, cancel := context.WithTimeout(ctx, provider.exporterTimeout)
	defer cancel()
	return provider.sdkProvider.ForceFlush(flushContext)
}

// Shutdown flushes and closes telemetry within both the caller's deadline and the configured
// exporter timeout. The service lifecycle supplies the remaining overall shutdown budget.
func (provider *Provider) Shutdown(ctx context.Context) error {
	if provider.sdkProvider == nil {
		return nil
	}
	shutdownContext, cancel := context.WithTimeout(ctx, provider.exporterTimeout)
	defer cancel()
	shutdownErr := provider.sdkProvider.Shutdown(shutdownContext)
	exporterErr := provider.exporter.Shutdown(shutdownContext)
	if shutdownErr != nil {
		return shutdownErr
	}
	return exporterErr
}

type exporterFactory func(context.Context, config.Telemetry) (sdktrace.SpanExporter, error)

func newProvider(
	ctx context.Context,
	serviceName string,
	settings config.Telemetry,
	createExporter exporterFactory,
) (*Provider, error) {
	propagator := propagation.TraceContext{}
	if !settings.Enabled {
		return &Provider{
			tracerProvider:  noop.NewTracerProvider(),
			propagator:      propagator,
			exporterTimeout: settings.ExporterTimeout,
		}, nil
	}
	if serviceName == "" {
		return nil, errors.New("initialize telemetry: service name must not be empty")
	}
	if settings.ExporterEndpoint == nil {
		return nil, errors.New("initialize telemetry: exporter endpoint is required")
	}
	if settings.ExporterTimeout <= 0 {
		return nil, errors.New("initialize telemetry: exporter timeout must be positive")
	}

	exporterContext, cancel := context.WithTimeout(ctx, settings.ExporterTimeout)
	defer cancel()
	exporter, err := createExporter(exporterContext, settings)
	if err != nil {
		return nil, &initializationError{cause: err}
	}
	owned := &ownedExporter{SpanExporter: exporter}

	tracerProvider := sdktrace.NewTracerProvider(
		sdktrace.WithResource(resource.NewSchemaless(
			attribute.String("service.name", serviceName),
		)),
		sdktrace.WithBatcher(
			owned,
			sdktrace.WithExportTimeout(settings.ExporterTimeout),
		),
	)
	return &Provider{
		tracerProvider:  tracerProvider,
		propagator:      propagator,
		sdkProvider:     tracerProvider,
		exporter:        owned,
		exporterTimeout: settings.ExporterTimeout,
	}, nil
}

func newOTLPExporter(
	ctx context.Context,
	settings config.Telemetry,
) (sdktrace.SpanExporter, error) {
	return otlptracegrpc.New(
		ctx,
		otlptracegrpc.WithEndpointURL(settings.ExporterEndpoint.String()),
		otlptracegrpc.WithTimeout(settings.ExporterTimeout),
	)
}

type initializationError struct {
	cause error
}

type ownedExporter struct {
	sdktrace.SpanExporter
	shutdownOnce sync.Once
	shutdownErr error
}

func (exporter *ownedExporter) Shutdown(ctx context.Context) error {
	exporter.shutdownOnce.Do(func() {
		exporter.shutdownErr = exporter.SpanExporter.Shutdown(ctx)
	})
	return exporter.shutdownErr
}

func (err *initializationError) Error() string {
	return "initialize telemetry: OTLP trace exporter failed"
}

func (err *initializationError) Unwrap() error {
	return err.cause
}
