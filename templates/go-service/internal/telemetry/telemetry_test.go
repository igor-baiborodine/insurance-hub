package telemetry

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/igor-baiborodine/insurance-hub/templates/go-service/internal/config"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

func TestNewDisabledDoesNotCreateExporter(t *testing.T) {
	provider, err := New(
		context.Background(),
		"go-service",
		config.Telemetry{Enabled: false, ExporterTimeout: time.Second},
	)
	if err != nil {
		t.Fatalf("new disabled provider: %v", err)
	}

	_, span := provider.TracerProvider().Tracer("test").Start(context.Background(), "disabled")
	if span.IsRecording() {
		t.Error("disabled span is recording")
	}
	span.End()
	if err := provider.Shutdown(context.Background()); err != nil {
		t.Errorf("shutdown disabled provider: %v", err)
	}
}

func TestProviderExportsSpansWithServiceIdentity(t *testing.T) {
	exporter := newRecordingExporter()
	provider := enabledProvider(t, exporter, time.Second)
	ctx, span := provider.TracerProvider().Tracer("test").Start(context.Background(), "exported")
	if !span.IsRecording() {
		t.Fatal("enabled span is not recording")
	}
	span.End()

	if err := provider.ForceFlush(ctx); err != nil {
		t.Fatalf("force flush: %v", err)
	}
	exported := receiveSpans(t, exporter.exported)
	if len(exported) != 1 || exported[0].Name() != "exported" {
		t.Fatalf("unexpected exported spans: %#v", exported)
	}
	serviceName := ""
	for _, attribute := range exported[0].Resource().Attributes() {
		if string(attribute.Key) == "service.name" {
			serviceName = attribute.Value.AsString()
		}
	}
	if serviceName != "go-service" {
		t.Errorf("service.name = %q, want go-service", serviceName)
	}

	if err := provider.Shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	select {
	case <-exporter.shutdown:
	default:
		t.Error("exporter shutdown was not called")
	}
}

func TestProviderPropagatesW3CTraceContext(t *testing.T) {
	provider, err := newProvider(
		context.Background(),
		"go-service",
		config.Telemetry{Enabled: false, ExporterTimeout: time.Second},
		func(context.Context, config.Telemetry) (sdktrace.SpanExporter, error) {
			return nil, errors.New("unexpected exporter call")
		},
	)
	if err != nil {
		t.Fatalf("new provider: %v", err)
	}
	traceID := trace.TraceID{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08,
		0x09, 0x0a, 0x0b, 0x0c, 0x0d, 0x0e, 0x0f, 0x10}
	spanID := trace.SpanID{0x11, 0x12, 0x13, 0x14, 0x15, 0x16, 0x17, 0x18}
	spanContext := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: traceID,
		SpanID: spanID,
		TraceFlags: trace.FlagsSampled,
	})
	ctx := trace.ContextWithSpanContext(context.Background(), spanContext)
	carrier := propagation.MapCarrier{}

	provider.Propagator().Inject(ctx, carrier)

	wantHeader := "00-" + traceID.String() + "-" + spanID.String() + "-01"
	if carrier.Get("traceparent") != wantHeader {
		t.Fatalf("traceparent = %q, want %q", carrier.Get("traceparent"), wantHeader)
	}
	extracted := trace.SpanContextFromContext(
		provider.Propagator().Extract(context.Background(), carrier),
	)
	if extracted.TraceID() != traceID || extracted.SpanID() != spanID || !extracted.IsRemote() {
		t.Errorf("unexpected extracted span context: %v", extracted)
	}
}

func TestProviderBoundsExporterInitializationAndHidesCauseDetails(t *testing.T) {
	const privateMarker = "private-marker"
	cause := errors.New(privateMarker)
	settings := testTelemetrySettings(25 * time.Millisecond)
	started := make(chan struct{})

	start := time.Now()
	_, err := newProvider(
		context.Background(),
		"go-service",
		settings,
		func(ctx context.Context, _ config.Telemetry) (sdktrace.SpanExporter, error) {
			close(started)
			<-ctx.Done()
			return nil, cause
		},
	)
	elapsed := time.Since(start)
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
		t.Errorf("initialization elapsed %v, want between %v and 1s", elapsed, settings.ExporterTimeout)
	}
}

func TestProviderShutdownIsBoundedWhenExporterIsUnavailable(t *testing.T) {
	exporter := &blockingExporter{
		started: make(chan struct{}),
		shutdown: make(chan struct{}),
	}
	provider := enabledProvider(t, exporter, time.Second)
	_, span := provider.TracerProvider().Tracer("test").Start(context.Background(), "blocked")
	span.End()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	result := make(chan error, 1)
	go func() {
		result <- provider.Shutdown(ctx)
	}()

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

func TestProviderDoesNotBlockSpanCompletionDuringExporterOutage(t *testing.T) {
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
	go func() {
		flushResult <- provider.ForceFlush(flushContext)
	}()
	select {
	case <-exporter.started:
	case <-time.After(time.Second):
		t.Fatal("export did not start")
	}

	spanEnded := make(chan struct{})
	go func() {
		_, secondSpan := tracer.Start(context.Background(), "second")
		secondSpan.End()
		close(spanEnded)
	}()
	select {
	case <-spanEnded:
	case <-time.After(time.Second):
		t.Fatal("span completion blocked on unavailable exporter")
	}

	cancelFlush()
	select {
	case <-flushResult:
	case <-time.After(time.Second):
		t.Fatal("force flush exceeded cancellation bound")
	}

	shutdownContext, cancelShutdown := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancelShutdown()
	if err := provider.Shutdown(shutdownContext); err == nil {
		t.Fatal("shutdown returned nil error")
	}
}

func TestNewRejectsInvalidEnabledSettings(t *testing.T) {
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
			serviceName: "go-service",
			settings:    config.Telemetry{Enabled: true, ExporterTimeout: time.Second},
		},
		{
			name:        "non-positive timeout",
			serviceName: "go-service",
			settings: config.Telemetry{
				Enabled: true,
				ExporterEndpoint: mustURL("http://127.0.0.1:4317"),
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := newProvider(
				context.Background(),
				test.serviceName,
				test.settings,
				func(context.Context, config.Telemetry) (sdktrace.SpanExporter, error) {
					t.Fatal("invalid settings created an exporter")
					return nil, nil
				},
			)
			if err == nil {
				t.Fatal("new provider returned nil error")
			}
		})
	}
}

type recordingExporter struct {
	exported chan []sdktrace.ReadOnlySpan
	shutdown chan struct{}
	once     sync.Once
}

func newRecordingExporter() *recordingExporter {
	return &recordingExporter{
		exported: make(chan []sdktrace.ReadOnlySpan, 1),
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
		"go-service",
		testTelemetrySettings(timeout),
		func(context.Context, config.Telemetry) (sdktrace.SpanExporter, error) {
			return exporter, nil
		},
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
	}
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
