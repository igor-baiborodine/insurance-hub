package telemetry

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/metric"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
	"go.opentelemetry.io/otel/propagation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"

	"github.com/igor-baiborodine/insurance-hub/libs/servicekit/config"
	"github.com/igor-baiborodine/insurance-hub/libs/servicekit/identity"
)

const (
	traceQueueSize       = 2048
	traceBatchSize       = 256
	traceBatchDelay      = time.Second
	metricExportInterval = 10 * time.Second
)

// Options supplies caller-owned instrumentation scopes, propagation policy and metric views.
// RuntimeScope is required. Nil propagators select W3C trace context and a nil sampler selects
// parent-based always-sample behavior.
type Options struct {
	RuntimeScope   string
	HTTPPropagator propagation.TextMapPropagator
	GRPCPropagator propagation.TextMapPropagator
	MetricViews    []sdkmetric.View
	Sampler        sdktrace.Sampler
}

// HistogramView constructs an explicit-bucket histogram view while retaining the metric name and
// boundaries supplied by the caller's signal contract.
func HistogramView(name string, buckets []float64) sdkmetric.View {
	return sdkmetric.NewView(
		sdkmetric.Instrument{Name: name, Kind: sdkmetric.InstrumentKindHistogram},
		sdkmetric.Stream{Aggregation: sdkmetric.AggregationExplicitBucketHistogram{
			Boundaries: append([]float64(nil), buckets...),
		}},
	)
}

// Provider owns trace and metric providers, propagation and bounded cleanup.
type Provider struct {
	tracerProvider      trace.TracerProvider
	meterProvider       metric.MeterProvider
	httpPropagator      propagation.TextMapPropagator
	grpcPropagator      propagation.TextMapPropagator
	sdkProvider         *sdktrace.TracerProvider
	sdkMeterProvider    *sdkmetric.MeterProvider
	runtimeRegistration metric.Registration
	exporter            *ownedExporter
	exporterTimeout     time.Duration
	shutdownOnce        sync.Once
	shutdownErr         error
}

// New constructs disabled no-op telemetry or OTLP/gRPC-backed trace and metric providers.
func New(ctx context.Context, settings config.Telemetry, options Options) (*Provider, error) {
	return newProvider(
		ctx,
		settings,
		options,
		newOTLPExporter,
		newMetricReader,
		registerRuntime,
	)
}

// TracerProvider returns the explicitly owned provider for transport instrumentation.
func (provider *Provider) TracerProvider() trace.TracerProvider { return provider.tracerProvider }

// MeterProvider returns the explicitly owned provider for caller-defined instruments.
func (provider *Provider) MeterProvider() metric.MeterProvider { return provider.meterProvider }

// HTTPPropagator returns the caller-selected HTTP propagation policy.
func (provider *Provider) HTTPPropagator() propagation.TextMapPropagator {
	return provider.httpPropagator
}

// GRPCPropagator returns the caller-selected gRPC propagation policy.
func (provider *Provider) GRPCPropagator() propagation.TextMapPropagator {
	return provider.grpcPropagator
}

// ForceFlush exports ended spans and current metrics within the shorter of the caller's deadline
// and the configured exporter timeout. Disabled telemetry returns immediately.
func (provider *Provider) ForceFlush(ctx context.Context) error {
	if provider.sdkProvider == nil {
		return nil
	}
	flushContext, cancel := context.WithTimeout(ctx, provider.exporterTimeout)
	defer cancel()
	return errors.Join(
		provider.sdkProvider.ForceFlush(flushContext),
		provider.sdkMeterProvider.ForceFlush(flushContext),
	)
}

// Shutdown unregisters runtime callbacks, flushes and closes providers and closes the owned trace
// exporter within the shorter of the caller's deadline and the configured exporter timeout.
func (provider *Provider) Shutdown(ctx context.Context) error {
	if provider.sdkProvider == nil {
		return nil
	}
	provider.shutdownOnce.Do(func() {
		shutdownContext, cancel := context.WithTimeout(ctx, provider.exporterTimeout)
		defer cancel()
		if provider.runtimeRegistration != nil {
			provider.shutdownErr = errors.Join(
				provider.shutdownErr,
				provider.runtimeRegistration.Unregister(),
			)
		}
		provider.shutdownErr = errors.Join(
			provider.shutdownErr,
			provider.sdkProvider.Shutdown(shutdownContext),
			provider.sdkMeterProvider.Shutdown(shutdownContext),
			provider.exporter.Shutdown(shutdownContext),
		)
	})
	return provider.shutdownErr
}

type (
	exporterFactory func(context.Context, config.Telemetry) (sdktrace.SpanExporter, error)
	readerFactory   func(context.Context, config.Telemetry) (sdkmetric.Reader, error)
	runtimeFactory  func(metric.MeterProvider, string) (metric.Registration, error)
)

func newProvider(
	ctx context.Context,
	settings config.Telemetry,
	options Options,
	createExporter exporterFactory,
	createMetricReader readerFactory,
	createRuntime runtimeFactory,
) (*Provider, error) {
	normalized, err := normalizeOptions(options)
	if err != nil {
		return nil, err
	}
	if err := config.ValidateTelemetry(settings); err != nil {
		return nil, errors.New("initialize telemetry: invalid exporter settings")
	}
	if !settings.Enabled {
		return &Provider{
			tracerProvider:  noop.NewTracerProvider(),
			meterProvider:   metricnoop.NewMeterProvider(),
			httpPropagator:  normalized.HTTPPropagator,
			grpcPropagator:  normalized.GRPCPropagator,
			exporterTimeout: settings.ExporterTimeout,
		}, nil
	}
	if err := validateIdentity(settings.Identity); err != nil {
		return nil, err
	}

	exporterContext, cancel := context.WithTimeout(ctx, settings.ExporterTimeout)
	defer cancel()
	exporter, err := createExporter(exporterContext, settings)
	if err != nil {
		return nil, &initializationError{cause: err, component: "trace"}
	}
	owned := &ownedExporter{SpanExporter: exporter}
	metricReader, err := createMetricReader(exporterContext, settings)
	if err != nil {
		cleanupOwnedExporter(owned, settings.ExporterTimeout)
		return nil, &initializationError{cause: err, component: "metric"}
	}

	resourceIdentity := newResource(settings.Identity)
	batchProcessor := sdktrace.NewBatchSpanProcessor(
		owned,
		sdktrace.WithMaxQueueSize(traceQueueSize),
		sdktrace.WithMaxExportBatchSize(traceBatchSize),
		sdktrace.WithBatchTimeout(traceBatchDelay),
		sdktrace.WithExportTimeout(settings.ExporterTimeout),
	)
	queueObserver := &spanQueueObserver{
		SpanProcessor: batchProcessor,
		exporter:      owned,
		errorHandler:  otel.GetErrorHandler(),
		warningDone:   make(chan struct{}),
	}
	tracerProvider := sdktrace.NewTracerProvider(
		sdktrace.WithResource(resourceIdentity),
		sdktrace.WithSampler(normalized.Sampler),
		sdktrace.WithSpanProcessor(queueObserver),
	)
	meterOptions := []sdkmetric.Option{
		sdkmetric.WithResource(resourceIdentity),
		sdkmetric.WithReader(metricReader),
	}
	if len(normalized.MetricViews) > 0 {
		meterOptions = append(meterOptions, sdkmetric.WithView(normalized.MetricViews...))
	}
	meterProvider := sdkmetric.NewMeterProvider(meterOptions...)
	runtimeRegistration, err := createRuntime(meterProvider, normalized.RuntimeScope)
	if err != nil {
		cleanupProviders(tracerProvider, meterProvider, owned, settings.ExporterTimeout)
		return nil, errors.New("initialize telemetry: runtime instruments failed")
	}

	return &Provider{
		tracerProvider:      tracerProvider,
		meterProvider:       meterProvider,
		httpPropagator:      normalized.HTTPPropagator,
		grpcPropagator:      normalized.GRPCPropagator,
		sdkProvider:         tracerProvider,
		sdkMeterProvider:    meterProvider,
		runtimeRegistration: runtimeRegistration,
		exporter:            owned,
		exporterTimeout:     settings.ExporterTimeout,
	}, nil
}

func normalizeOptions(options Options) (Options, error) {
	if options.RuntimeScope == "" {
		return Options{}, errors.New(
			"initialize telemetry: runtime scope must not be empty",
		)
	}
	if options.HTTPPropagator == nil {
		options.HTTPPropagator = propagation.TraceContext{}
	}
	if options.GRPCPropagator == nil {
		options.GRPCPropagator = propagation.TraceContext{}
	}
	if options.Sampler == nil {
		options.Sampler = sdktrace.ParentBased(sdktrace.AlwaysSample())
	}
	options.MetricViews = append([]sdkmetric.View(nil), options.MetricViews...)
	return options, nil
}

func validateIdentity(serviceIdentity identity.Identity) error {
	fields := []struct {
		name  string
		value string
	}{
		{"service namespace", serviceIdentity.ServiceNamespace},
		{"service name", serviceIdentity.ServiceName},
		{"service version", serviceIdentity.ServiceVersion},
		{"service instance", serviceIdentity.ServiceInstance},
		{"deployment environment", serviceIdentity.Environment},
		{"service implementation", serviceIdentity.Implementation},
	}
	for _, field := range fields {
		if field.value == "" {
			return fmt.Errorf("initialize telemetry: %s must not be empty", field.name)
		}
	}
	return nil
}

func newResource(serviceIdentity identity.Identity) *resource.Resource {
	return resource.NewSchemaless(
		attribute.String("service.namespace", serviceIdentity.ServiceNamespace),
		attribute.String("service.name", serviceIdentity.ServiceName),
		attribute.String("service.version", serviceIdentity.ServiceVersion),
		attribute.String("service.instance.id", serviceIdentity.ServiceInstance),
		attribute.String("deployment.environment.name", serviceIdentity.Environment),
		attribute.String(
			"insurance_hub.service.implementation",
			serviceIdentity.Implementation,
		),
	)
}

func newOTLPExporter(
	ctx context.Context,
	settings config.Telemetry,
) (sdktrace.SpanExporter, error) {
	return otlptracegrpc.New(
		ctx,
		otlptracegrpc.WithEndpointURL(settings.ExporterEndpoint.String()),
		otlptracegrpc.WithTimeout(settings.ExporterTimeout),
		otlptracegrpc.WithRetry(otlptracegrpc.RetryConfig{
			Enabled:         true,
			InitialInterval: 250 * time.Millisecond,
			MaxInterval:     time.Second,
			MaxElapsedTime:  settings.ExporterTimeout,
		}),
	)
}

func newMetricReader(ctx context.Context, settings config.Telemetry) (sdkmetric.Reader, error) {
	exporter, err := otlpmetricgrpc.New(
		ctx,
		otlpmetricgrpc.WithEndpointURL(settings.ExporterEndpoint.String()),
		otlpmetricgrpc.WithTimeout(settings.ExporterTimeout),
		otlpmetricgrpc.WithTemporalitySelector(sdkmetric.CumulativeTemporalitySelector),
	)
	if err != nil {
		return nil, err
	}
	return sdkmetric.NewPeriodicReader(
		exporter,
		sdkmetric.WithInterval(metricExportInterval),
		sdkmetric.WithTimeout(settings.ExporterTimeout),
	), nil
}

func cleanupOwnedExporter(exporter *ownedExporter, timeout time.Duration) {
	cleanupContext, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	_ = exporter.Shutdown(cleanupContext)
}

func cleanupProviders(
	tracerProvider *sdktrace.TracerProvider,
	meterProvider *sdkmetric.MeterProvider,
	exporter *ownedExporter,
	timeout time.Duration,
) {
	cleanupContext, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	_ = meterProvider.Shutdown(cleanupContext)
	_ = tracerProvider.Shutdown(cleanupContext)
	_ = exporter.Shutdown(cleanupContext)
}

type initializationError struct {
	cause     error
	component string
}

func (err *initializationError) Error() string {
	return fmt.Sprintf("initialize telemetry: OTLP %s exporter failed", err.component)
}

func (err *initializationError) Unwrap() error { return err.cause }

type ownedExporter struct {
	sdktrace.SpanExporter
	shutdownOnce sync.Once
	shutdownErr  error
	attempted    atomic.Uint64
}

func (exporter *ownedExporter) ExportSpans(
	ctx context.Context,
	spans []sdktrace.ReadOnlySpan,
) error {
	exporter.attempted.Add(uint64(len(spans)))
	return exporter.SpanExporter.ExportSpans(ctx, spans)
}

func (exporter *ownedExporter) Shutdown(ctx context.Context) error {
	exporter.shutdownOnce.Do(func() {
		exporter.shutdownErr = exporter.SpanExporter.Shutdown(ctx)
	})
	return exporter.shutdownErr
}

// spanQueueObserver detects a definite overflow without replacing the SDK's bounded queue.
// At most one batch and one queue of spans can be waiting before export begins.
type spanQueueObserver struct {
	sdktrace.SpanProcessor
	exporter     *ownedExporter
	errorHandler otel.ErrorHandler
	warningDone  chan struct{}
	ended        atomic.Uint64
	saturated    atomic.Bool
}

func (observer *spanQueueObserver) OnEnd(span sdktrace.ReadOnlySpan) {
	observer.SpanProcessor.OnEnd(span)
	ended := observer.ended.Add(1)
	if ended > observer.exporter.attempted.Load()+traceQueueSize+traceBatchSize &&
		observer.saturated.CompareAndSwap(false, true) {
		go func() {
			defer close(observer.warningDone)
			observer.errorHandler.Handle(errSpanQueueSaturated)
		}()
	}
}

func (observer *spanQueueObserver) Shutdown(ctx context.Context) error {
	err := observer.SpanProcessor.Shutdown(ctx)
	if observer.saturated.Load() {
		select {
		case <-observer.warningDone:
		case <-ctx.Done():
			err = errors.Join(err, ctx.Err())
		}
	}
	return err
}
