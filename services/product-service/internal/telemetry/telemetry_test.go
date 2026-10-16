package telemetry

import (
	"context"
	"errors"
	"net/url"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/propagation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"

	"github.com/igor-baiborodine/insurance-hub/services/product-service/internal/config"
)

func TestNew_DisabledDoesNotCreateExporter(t *testing.T) {
	// given
	settings := config.Telemetry{Enabled: false, ExporterTimeout: time.Second}

	// when
	provider, err := New(
		context.Background(),
		"product-service",
		settings,
	)
	// then
	if err != nil {
		t.Fatalf("new disabled provider: %v", err)
	}

	// when
	_, span := provider.TracerProvider().Tracer("test").Start(context.Background(), "disabled")

	// then
	if span.IsRecording() {
		t.Error("disabled span is recording")
	}
	if provider.MeterProvider() == nil {
		t.Error("disabled meter provider is nil")
	}

	// when
	span.End()
	shutdownErr := provider.Shutdown(context.Background())

	// then
	if shutdownErr != nil {
		t.Errorf("shutdown disabled provider: %v", shutdownErr)
	}
}

func TestProvider_ExportsSpansWithServiceIdentity(t *testing.T) {
	// given
	exporter := newRecordingExporter()
	provider := enabledProvider(t, exporter, time.Second)

	// when
	ctx, span := provider.TracerProvider().
		Tracer("test").
		Start(context.Background(), "exported")

	// then
	if !span.IsRecording() {
		t.Fatal("enabled span is not recording")
	}

	// when
	span.End()
	flushErr := provider.ForceFlush(ctx)
	exported := receiveSpans(t, exporter.exported)

	// then
	if flushErr != nil {
		t.Fatalf("force flush: %v", flushErr)
	}
	if len(exported) != 1 || exported[0].Name() != "exported" {
		t.Fatalf("unexpected exported spans: %#v", exported)
	}
	attributes := map[string]string{}
	for _, attribute := range exported[0].Resource().Attributes() {
		attributes[string(attribute.Key)] = attribute.Value.AsString()
	}
	wantAttributes := map[string]string{
		"service.namespace":                    "insurance-hub",
		"service.name":                         "product-service",
		"service.version":                      "test-telemetry",
		"service.instance.id":                  "123e4567-e89b-42d3-a456-426614174000",
		"deployment.environment.name":          "test",
		"insurance_hub.service.implementation": "go",
	}
	if !reflect.DeepEqual(attributes, wantAttributes) {
		t.Errorf("trace resource = %#v, want %#v", attributes, wantAttributes)
	}

	// when
	shutdownErr := provider.Shutdown(context.Background())

	// then
	if shutdownErr != nil {
		t.Fatalf("shutdown: %v", shutdownErr)
	}
	select {
	case <-exporter.shutdown:
	default:
		t.Error("exporter shutdown was not called")
	}
}

func TestProvider_CollectsCumulativeMetricsWithExactResourceAndBuckets(t *testing.T) {
	// given
	reader := sdkmetric.NewManualReader()
	provider, err := newProvider(
		context.Background(),
		"product-service",
		testTelemetrySettings(time.Second),
		func(context.Context, config.Telemetry) (sdktrace.SpanExporter, error) {
			return newRecordingExporter(), nil
		},
		func(context.Context, config.Telemetry) (sdkmetric.Reader, error) { return reader, nil },
	)
	if err != nil {
		t.Fatalf("new provider: %v", err)
	}
	t.Cleanup(func() { _ = provider.Shutdown(context.Background()) })
	meter := provider.MeterProvider().Meter("product-test")
	counter, err := meter.Int64Counter("insurance_hub.product.requests")
	if err != nil {
		t.Fatalf("create counter: %v", err)
	}
	histograms := []struct {
		name    string
		buckets []float64
	}{
		{name: "insurance_hub.product.request.duration", buckets: requestBuckets},
		{name: "db.client.operation.duration", buckets: databaseBuckets},
		{name: "db.client.connection.wait_time", buckets: databaseBuckets},
	}
	for _, item := range histograms {
		histogram, createErr := meter.Float64Histogram(item.name, metric.WithUnit("s"))
		if createErr != nil {
			t.Fatalf("create %s: %v", item.name, createErr)
		}
		histogram.Record(context.Background(), 0.05)
	}

	// when
	counter.Add(context.Background(), 1)
	var collected metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &collected); err != nil {
		t.Fatalf("collect metrics: %v", err)
	}

	// then
	resourceAttributes := map[string]string{}
	for _, attribute := range collected.Resource.Attributes() {
		resourceAttributes[string(attribute.Key)] = attribute.Value.AsString()
	}
	if len(resourceAttributes) != 6 ||
		resourceAttributes["service.name"] != "product-service" ||
		resourceAttributes["service.version"] != "test-telemetry" ||
		resourceAttributes["service.instance.id"] != "123e4567-e89b-42d3-a456-426614174000" ||
		resourceAttributes["deployment.environment.name"] != "test" ||
		resourceAttributes["insurance_hub.service.implementation"] != "go" ||
		resourceAttributes["service.namespace"] != "insurance-hub" {
		t.Errorf("unexpected metric resource: %#v", resourceAttributes)
	}
	metrics := map[string]metricdata.Metrics{}
	for _, scope := range collected.ScopeMetrics {
		for _, item := range scope.Metrics {
			metrics[item.Name] = item
		}
	}
	sum, ok := metrics["insurance_hub.product.requests"].Data.(metricdata.Sum[int64])
	if !ok || sum.Temporality != metricdata.CumulativeTemporality || !sum.IsMonotonic ||
		len(sum.DataPoints) != 1 || sum.DataPoints[0].Value != 1 {
		t.Errorf(
			"unexpected request counter: %#v",
			metrics["insurance_hub.product.requests"],
		)
	}
	for _, item := range histograms {
		got, ok := metrics[item.name].Data.(metricdata.Histogram[float64])
		if !ok || got.Temporality != metricdata.CumulativeTemporality ||
			len(
				got.DataPoints,
			) != 1 || !reflect.DeepEqual(got.DataPoints[0].Bounds, item.buckets) {
			t.Errorf("unexpected %s histogram: %#v", item.name, metrics[item.name])
		}
	}
}

func TestProvider_HonorsRemoteSamplingAndSamplesRoots(t *testing.T) {
	// given
	provider := enabledProvider(t, newRecordingExporter(), time.Second)
	t.Cleanup(func() { _ = provider.Shutdown(context.Background()) })
	remote := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: trace.TraceID{1}, SpanID: trace.SpanID{2}, Remote: true,
	})

	// when
	_, root := provider.TracerProvider().Tracer("test").Start(context.Background(), "root")
	_, unsampled := provider.TracerProvider().Tracer("test").Start(
		trace.ContextWithRemoteSpanContext(context.Background(), remote),
		"remote-unsampled",
	)
	_, sampled := provider.TracerProvider().Tracer("test").Start(
		trace.ContextWithRemoteSpanContext(context.Background(), remote.WithTraceFlags(trace.FlagsSampled)),
		"remote-sampled",
	)

	// then
	if !root.IsRecording() || unsampled.IsRecording() || !sampled.IsRecording() {
		t.Errorf("sampling decisions: root=%v unsampled=%v sampled=%v",
			root.IsRecording(), unsampled.IsRecording(), sampled.IsRecording())
	}
	root.End()
	unsampled.End()
	sampled.End()
}

func TestProvider_BatchesSpansAtContractSize(t *testing.T) {
	// given
	const wantBatchSize = 256
	exporter := newRecordingExporter()
	provider := enabledProvider(t, exporter, time.Second)
	t.Cleanup(func() { _ = provider.Shutdown(context.Background()) })
	tracer := provider.TracerProvider().Tracer("test")

	// when
	for range 300 {
		_, span := tracer.Start(context.Background(), "batched")
		span.End()
	}
	if err := provider.ForceFlush(context.Background()); err != nil {
		t.Fatalf("flush spans: %v", err)
	}

	// then
	total := 0
	fullBatch := false
	for len(exporter.exported) > 0 {
		batch := <-exporter.exported
		if len(batch) > wantBatchSize {
			t.Errorf(
				"export batch size = %d, want at most %d",
				len(batch),
				wantBatchSize,
			)
		}
		fullBatch = fullBatch || len(batch) == wantBatchSize
		total += len(batch)
	}
	if total != 300 || !fullBatch {
		t.Errorf(
			"exported %d spans with full contract batch=%v, want 300 and true",
			total,
			fullBatch,
		)
	}
}

func TestProvider_ClosesTraceExporterWhenMetricStartupFails(t *testing.T) {
	// given
	exporter := newRecordingExporter()
	cause := errors.New("private metric startup detail")

	// when
	_, err := newProvider(
		context.Background(),
		"product-service",
		testTelemetrySettings(time.Second),
		func(context.Context, config.Telemetry) (sdktrace.SpanExporter, error) { return exporter, nil },
		func(context.Context, config.Telemetry) (sdkmetric.Reader, error) { return nil, cause },
	)

	// then
	if !errors.Is(err, cause) || strings.Contains(err.Error(), cause.Error()) {
		t.Errorf("unsafe or unwrapped startup error: %v", err)
	}
	select {
	case <-exporter.shutdown:
	default:
		t.Error("trace exporter was not closed after metric startup failure")
	}
}

func TestProvider_PropagatesW3CTraceContext(t *testing.T) {
	// given
	provider, err := newProvider(
		context.Background(),
		"product-service",
		config.Telemetry{Enabled: false, ExporterTimeout: time.Second},
		func(context.Context, config.Telemetry) (sdktrace.SpanExporter, error) {
			return nil, errors.New("unexpected exporter call")
		},
		testMetricReader,
	)
	if err != nil {
		t.Fatalf("new provider: %v", err)
	}
	traceID := trace.TraceID{
		0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08,
		0x09, 0x0a, 0x0b, 0x0c, 0x0d, 0x0e, 0x0f, 0x10,
	}
	spanID := trace.SpanID{0x11, 0x12, 0x13, 0x14, 0x15, 0x16, 0x17, 0x18}
	spanContext := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    traceID,
		SpanID:     spanID,
		TraceFlags: trace.FlagsSampled,
	})
	ctx := trace.ContextWithSpanContext(context.Background(), spanContext)
	carrier := propagation.MapCarrier{}
	httpCarrier := propagation.MapCarrier{}

	// when
	provider.GRPCPropagator().Inject(ctx, carrier)
	provider.HTTPPropagator().Inject(ctx, httpCarrier)
	extracted := trace.SpanContextFromContext(
		provider.GRPCPropagator().Extract(context.Background(), carrier),
	)

	// then
	wantHeader := "00-" + traceID.String() + "-" + spanID.String() + "-01"
	if carrier.Get("traceparent") != wantHeader {
		t.Fatalf("traceparent = %q, want %q", carrier.Get("traceparent"), wantHeader)
	}
	if httpCarrier.Get("traceparent") != wantHeader {
		t.Errorf(
			"HTTP traceparent = %q, want %q",
			httpCarrier.Get("traceparent"),
			wantHeader,
		)
	}
	if extracted.TraceID() != traceID || extracted.SpanID() != spanID || !extracted.IsRemote() {
		t.Errorf("unexpected extracted span context: %v", extracted)
	}
}

func TestProvider_BoundsExporterInitializationAndHidesCauseDetails(t *testing.T) {
	// given
	const privateMarker = "private-marker"
	cause := errors.New(privateMarker)
	settings := testTelemetrySettings(25 * time.Millisecond)
	started := make(chan struct{})

	// when
	start := time.Now()
	_, err := newProvider(
		context.Background(),
		"product-service",
		settings,
		func(ctx context.Context, _ config.Telemetry) (sdktrace.SpanExporter, error) {
			close(started)
			<-ctx.Done()
			return nil, cause
		},
		testMetricReader,
	)
	elapsed := time.Since(start)

	// then
	if err == nil {
		t.Fatal("new provider returned nil error")
	}
	select {
	case <-started:
	default:
		t.Error("exporter initialization did not start")
	}
	if !errors.Is(err, cause) {
		t.Errorf("error does not preserve cause: %v", err)
	}
	if strings.Contains(err.Error(), privateMarker) {
		t.Errorf("error exposes private marker: %v", err)
	}
	if elapsed < settings.ExporterTimeout || elapsed > time.Second {
		t.Errorf(
			"initialization elapsed %v, want between %v and 1s",
			elapsed,
			settings.ExporterTimeout,
		)
	}
}

func TestProvider_Shutdown_IsBoundedWhenExporterIsUnavailable(t *testing.T) {
	// given
	exporter := &blockingExporter{
		started:  make(chan struct{}),
		shutdown: make(chan struct{}),
	}
	provider := enabledProvider(t, exporter, time.Second)
	_, span := provider.TracerProvider().Tracer("test").Start(context.Background(), "blocked")
	span.End()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	result := make(chan error, 1)

	// when
	go func() {
		result <- provider.Shutdown(ctx)
	}()

	// then
	select {
	case <-exporter.started:
	case <-time.After(time.Second):
		t.Fatal("export did not start")
	}
	select {
	case err := <-result:
		if err == nil {
			t.Fatal("shutdown returned nil error")
		}
	case <-time.After(time.Second):
		t.Fatal("shutdown exceeded failure bound")
	}
	select {
	case <-exporter.shutdown:
	default:
		t.Error("exporter shutdown was not attempted")
	}
}

func TestProvider_DoesNotBlockSpanCompletionDuringExporterOutage(t *testing.T) {
	// given
	exporter := &blockingExporter{
		started:  make(chan struct{}),
		shutdown: make(chan struct{}),
	}
	provider := enabledProvider(t, exporter, time.Second)
	tracer := provider.TracerProvider().Tracer("test")
	_, firstSpan := tracer.Start(context.Background(), "first")
	firstSpan.End()

	flushContext, cancelFlush := context.WithCancel(context.Background())
	flushResult := make(chan error, 1)

	// when
	go func() {
		flushResult <- provider.ForceFlush(flushContext)
	}()

	// then
	select {
	case <-exporter.started:
	case <-time.After(time.Second):
		t.Fatal("export did not start")
	}

	// when
	spanEnded := make(chan struct{})
	go func() {
		_, secondSpan := tracer.Start(context.Background(), "second")
		secondSpan.End()
		close(spanEnded)
	}()

	// then
	select {
	case <-spanEnded:
	case <-time.After(time.Second):
		t.Fatal("span completion blocked on unavailable exporter")
	}

	// when
	cancelFlush()

	// then
	select {
	case <-flushResult:
	case <-time.After(time.Second):
		t.Fatal("force flush exceeded cancellation bound")
	}

	// when
	shutdownContext, cancelShutdown := context.WithTimeout(
		context.Background(),
		50*time.Millisecond,
	)
	defer cancelShutdown()
	shutdownErr := provider.Shutdown(shutdownContext)

	// then
	if shutdownErr == nil {
		t.Fatal("shutdown returned nil error")
	}
}

func TestNew_RejectsInvalidEnabledSettings(t *testing.T) {
	// given
	tests := []struct {
		name        string
		serviceName string
		settings    config.Telemetry
	}{
		{
			name:     "empty service name",
			settings: testTelemetrySettings(time.Second),
		},
		{
			name:        "missing endpoint",
			serviceName: "product-service",
			settings:    config.Telemetry{Enabled: true, ExporterTimeout: time.Second},
		},
		{
			name:        "non-positive timeout",
			serviceName: "product-service",
			settings: config.Telemetry{
				Enabled:          true,
				ExporterEndpoint: mustURL("http://127.0.0.1:4317"),
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// given
			newExporter := func(
				context.Context,
				config.Telemetry,
			) (sdktrace.SpanExporter, error) {
				t.Fatal("invalid settings created an exporter")
				return nil, nil
			}

			// when
			_, err := newProvider(
				context.Background(),
				test.serviceName,
				test.settings,
				newExporter,
				testMetricReader,
			)

			// then
			if err == nil {
				t.Fatal("new provider returned nil error")
			}
		})
	}
}

func TestNew_RejectsInvalidResourceIdentityBeforeExporterStartup(t *testing.T) {
	// given
	settings := testTelemetrySettings(time.Second)
	settings.Identity.ServiceVersion = "latest"
	traceCalled := false
	metricCalled := false

	// when
	_, err := newProvider(
		context.Background(), "product-service", settings,
		func(context.Context, config.Telemetry) (sdktrace.SpanExporter, error) {
			traceCalled = true
			return nil, nil
		},
		func(context.Context, config.Telemetry) (sdkmetric.Reader, error) {
			metricCalled = true
			return nil, nil
		},
	)

	// then
	if err == nil || !strings.Contains(err.Error(), "invalid resource identity") {
		t.Errorf("invalid identity error = %v", err)
	}
	if traceCalled || metricCalled {
		t.Errorf(
			"invalid identity started exporters: trace=%v metric=%v",
			traceCalled,
			metricCalled,
		)
	}
}

type recordingExporter struct {
	exported chan []sdktrace.ReadOnlySpan
	shutdown chan struct{}
	once     sync.Once
}

func newRecordingExporter() *recordingExporter {
	return &recordingExporter{
		exported: make(chan []sdktrace.ReadOnlySpan, 4),
		shutdown: make(chan struct{}),
	}
}

func (exporter *recordingExporter) ExportSpans(
	ctx context.Context,
	spans []sdktrace.ReadOnlySpan,
) error {
	copied := append([]sdktrace.ReadOnlySpan(nil), spans...)
	select {
	case exporter.exported <- copied:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (exporter *recordingExporter) Shutdown(context.Context) error {
	exporter.once.Do(func() { close(exporter.shutdown) })
	return nil
}

type blockingExporter struct {
	started      chan struct{}
	shutdown     chan struct{}
	startOnce    sync.Once
	shutdownOnce sync.Once
}

func (exporter *blockingExporter) ExportSpans(
	ctx context.Context,
	_ []sdktrace.ReadOnlySpan,
) error {
	exporter.startOnce.Do(func() { close(exporter.started) })
	<-ctx.Done()
	return ctx.Err()
}

func (exporter *blockingExporter) Shutdown(context.Context) error {
	exporter.shutdownOnce.Do(func() { close(exporter.shutdown) })
	return nil
}

func enabledProvider(
	t *testing.T,
	exporter sdktrace.SpanExporter,
	timeout time.Duration,
) *Provider {
	t.Helper()
	provider, err := newProvider(
		context.Background(),
		"product-service",
		testTelemetrySettings(timeout),
		func(context.Context, config.Telemetry) (sdktrace.SpanExporter, error) {
			return exporter, nil
		},
		testMetricReader,
	)
	if err != nil {
		t.Fatalf("new provider: %v", err)
	}
	return provider
}

func testTelemetrySettings(timeout time.Duration) config.Telemetry {
	return config.Telemetry{
		Enabled:          true,
		ExporterEndpoint: mustURL("http://127.0.0.1:4317"),
		ExporterTimeout:  timeout,
		Identity: config.Identity{
			ServiceNamespace: "insurance-hub",
			ServiceName:      "product-service",
			ServiceVersion:   "test-telemetry",
			ServiceInstance:  "123e4567-e89b-42d3-a456-426614174000",
			Environment:      "test",
			Implementation:   "go",
		},
	}
}

func testMetricReader(context.Context, config.Telemetry) (sdkmetric.Reader, error) {
	return sdkmetric.NewManualReader(), nil
}

func mustURL(value string) *url.URL {
	parsed, err := url.Parse(value)
	if err != nil {
		panic(err)
	}
	return parsed
}

func receiveSpans(t *testing.T, spans <-chan []sdktrace.ReadOnlySpan) []sdktrace.ReadOnlySpan {
	t.Helper()
	select {
	case exported := <-spans:
		return exported
	case <-time.After(time.Second):
		t.Fatal("span export timed out")
		return nil
	}
}
