package telemetry

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"go.opentelemetry.io/contrib/propagators/b3"
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

	"github.com/igor-baiborodine/insurance-hub/services/product-service/internal/config"
)

const (
	traceQueueSize       = 2048
	traceBatchSize       = 256
	traceBatchDelay      = time.Second
	metricExportInterval = 10 * time.Second
	exportTimeout        = 2 * time.Second
)

var (
	requestBuckets = []float64{
		0.005,
		0.01,
		0.025,
		0.05,
		0.075,
		0.1,
		0.25,
		0.5,
		0.75,
		1,
		2.5,
		5,
		7.5,
		10,
	}
	databaseBuckets = []float64{0.001, 0.005, 0.01, 0.05, 0.1, 0.5, 1, 5, 10}
)

// Provider owns the trace and metric providers, propagation, and bounded cleanup.
type Provider struct {
	tracerProvider      trace.TracerProvider
	meterProvider       metric.MeterProvider
	httpPropagator      propagation.TextMapPropagator
	grpcPropagator      propagation.TextMapPropagator
	sdkProvider         *sdktrace.TracerProvider
	sdkMeterProvider    *sdkmetric.MeterProvider
	runtimeRegistration metric.Registration
	exporter            *ownedExporter
	queueObserver       *spanQueueObserver
	exporterTimeout     time.Duration
}

// New constructs disabled no-op telemetry or OTLP/gRPC-backed trace and metric providers.
func New(ctx context.Context, serviceName string, settings config.Telemetry) (*Provider, error) {
	return newProvider(ctx, serviceName, settings, newOTLPExporter, newMetricReader)
}

// TracerProvider returns the explicitly owned provider for transport instrumentation.
func (provider *Provider) TracerProvider() trace.TracerProvider {
	return provider.tracerProvider
}

// MeterProvider returns the explicitly owned provider for Product instruments.
func (provider *Provider) MeterProvider() metric.MeterProvider {
	return provider.meterProvider
}

// HTTPPropagator returns the HTTP boundary's trace-context policy.
func (provider *Provider) HTTPPropagator() propagation.TextMapPropagator {
	return provider.httpPropagator
}

// GRPCPropagator returns the gRPC boundary's trace-context policy.
func (provider *Provider) GRPCPropagator() propagation.TextMapPropagator {
	return provider.grpcPropagator
}

// ForceFlush exports ended spans within both the caller's deadline and the configured exporter
// timeout. Disabled telemetry returns immediately.
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

// Shutdown flushes and closes telemetry within both the caller's deadline and the configured
// exporter timeout. The service lifecycle supplies the remaining overall shutdown budget.
func (provider *Provider) Shutdown(ctx context.Context) error {
	if provider.sdkProvider == nil {
		return nil
	}
	shutdownContext, cancel := context.WithTimeout(ctx, provider.exporterTimeout)
	defer cancel()
	err := errors.Join(
		provider.sdkProvider.Shutdown(shutdownContext),
		provider.sdkMeterProvider.Shutdown(shutdownContext),
		provider.exporter.Shutdown(shutdownContext),
	)
	if provider.runtimeRegistration != nil {
		err = errors.Join(err, provider.runtimeRegistration.Unregister())
	}
	return err
}

type (
	exporterFactory     func(context.Context, config.Telemetry) (sdktrace.SpanExporter, error)
	metricReaderFactory func(context.Context, config.Telemetry) (sdkmetric.Reader, error)
)

func newProvider(
	ctx context.Context,
	serviceName string,
	settings config.Telemetry,
	createExporter exporterFactory,
	createMetricReader metricReaderFactory,
) (*Provider, error) {
	httpPropagator := propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		b3.New(b3.WithInjectEncoding(b3.B3MultipleHeader)),
	)
	grpcPropagator := propagation.TraceContext{}
	if !settings.Enabled {
		return &Provider{
			tracerProvider:  noop.NewTracerProvider(),
			meterProvider:   metricnoop.NewMeterProvider(),
			httpPropagator:  httpPropagator,
			grpcPropagator:  grpcPropagator,
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
	if settings.Identity.ServiceName != serviceName {
		return nil, errors.New("initialize telemetry: service identity mismatch")
	}
	if err := settings.Identity.Validate(true); err != nil {
		return nil, errors.New("initialize telemetry: invalid resource identity")
	}

	exporterContext, cancel := context.WithTimeout(ctx, settings.ExporterTimeout)
	defer cancel()
	exporter, err := createExporter(exporterContext, settings)
	if err != nil {
		return nil, &initializationError{cause: err}
	}
	owned := &ownedExporter{SpanExporter: exporter}
	metricReader, err := createMetricReader(exporterContext, settings)
	if err != nil {
		cleanupContext, cancelCleanup := context.WithTimeout(
			context.Background(),
			settings.ExporterTimeout,
		)
		defer cancelCleanup()
		_ = owned.Shutdown(cleanupContext)
		return nil, &initializationError{cause: err, component: "metric"}
	}
	identity := settings.Identity
	resourceIdentity := resource.NewSchemaless(
		attribute.String("service.namespace", identity.ServiceNamespace),
		attribute.String("service.name", identity.ServiceName),
		attribute.String("service.version", identity.ServiceVersion),
		attribute.String("service.instance.id", identity.ServiceInstance),
		attribute.String("deployment.environment.name", identity.Environment),
		attribute.String("insurance_hub.service.implementation", identity.Implementation),
	)

	batchProcessor := sdktrace.NewBatchSpanProcessor(
		owned,
		sdktrace.WithMaxQueueSize(traceQueueSize),
		sdktrace.WithMaxExportBatchSize(traceBatchSize),
		sdktrace.WithBatchTimeout(traceBatchDelay),
		sdktrace.WithExportTimeout(exportTimeout),
	)
	queueObserver := &spanQueueObserver{
		SpanProcessor: batchProcessor,
		exporter:      owned,
		errorHandler:  otel.GetErrorHandler(),
		warningDone:   make(chan struct{}),
	}
	tracerProvider := sdktrace.NewTracerProvider(
		sdktrace.WithResource(resourceIdentity),
		sdktrace.WithSampler(sdktrace.ParentBased(sdktrace.AlwaysSample())),
		sdktrace.WithSpanProcessor(queueObserver),
	)
	meterProvider := sdkmetric.NewMeterProvider(
		sdkmetric.WithResource(resourceIdentity),
		sdkmetric.WithReader(metricReader),
		sdkmetric.WithView(
			histogramView("insurance_hub.product.request.duration", requestBuckets),
			histogramView("db.client.operation.duration", databaseBuckets),
			histogramView("db.client.connection.wait_time", databaseBuckets),
		),
	)
	runtimeRegistration, err := registerRuntime(meterProvider)
	if err != nil {
		cleanupContext, cancelCleanup := context.WithTimeout(
			context.Background(),
			settings.ExporterTimeout,
		)
		defer cancelCleanup()
		_ = meterProvider.Shutdown(cleanupContext)
		_ = tracerProvider.Shutdown(cleanupContext)
		_ = owned.Shutdown(cleanupContext)
		return nil, errors.New("initialize telemetry: runtime instruments failed")
	}
	return &Provider{
		tracerProvider:      tracerProvider,
		meterProvider:       meterProvider,
		httpPropagator:      httpPropagator,
		grpcPropagator:      grpcPropagator,
		sdkProvider:         tracerProvider,
		sdkMeterProvider:    meterProvider,
		runtimeRegistration: runtimeRegistration,
		exporter:            owned,
		queueObserver:       queueObserver,
		exporterTimeout:     settings.ExporterTimeout,
	}, nil
}

func histogramView(name string, buckets []float64) sdkmetric.View {
	return sdkmetric.NewView(
		sdkmetric.Instrument{Name: name, Kind: sdkmetric.InstrumentKindHistogram},
		sdkmetric.Stream{
			Aggregation: sdkmetric.AggregationExplicitBucketHistogram{
				Boundaries: buckets,
			},
		},
	)
}

func newOTLPExporter(
	ctx context.Context,
	settings config.Telemetry,
) (sdktrace.SpanExporter, error) {
	return otlptracegrpc.New(
		ctx,
		otlptracegrpc.WithEndpointURL(settings.ExporterEndpoint.String()),
		otlptracegrpc.WithTimeout(exportTimeout),
		otlptracegrpc.WithRetry(otlptracegrpc.RetryConfig{
			Enabled:         true,
			InitialInterval: 250 * time.Millisecond,
			MaxInterval:     time.Second,
			MaxElapsedTime:  2 * time.Second,
		}),
	)
}

func newMetricReader(ctx context.Context, settings config.Telemetry) (sdkmetric.Reader, error) {
	exporter, err := otlpmetricgrpc.New(
		ctx,
		otlpmetricgrpc.WithEndpointURL(settings.ExporterEndpoint.String()),
		otlpmetricgrpc.WithTimeout(exportTimeout),
		otlpmetricgrpc.WithTemporalitySelector(sdkmetric.CumulativeTemporalitySelector),
	)
	if err != nil {
		return nil, err
	}
	return sdkmetric.NewPeriodicReader(exporter,
		sdkmetric.WithInterval(metricExportInterval),
		sdkmetric.WithTimeout(exportTimeout),
	), nil
}

type initializationError struct {
	cause     error
	component string
}

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

func (exporter *ownedExporter) Shutdown(ctx context.Context) error {
	exporter.shutdownOnce.Do(func() {
		exporter.shutdownErr = exporter.SpanExporter.Shutdown(ctx)
	})
	return exporter.shutdownErr
}

func (err *initializationError) Error() string {
	if err.component == "metric" {
		return "initialize telemetry: OTLP metric exporter failed"
	}
	return "initialize telemetry: OTLP trace exporter failed"
}

func (err *initializationError) Unwrap() error {
	return err.cause
}
