package telemetry

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/propagation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"

	"github.com/igor-baiborodine/insurance-hub/libs/servicekit/config"
	"github.com/igor-baiborodine/insurance-hub/libs/servicekit/identity"
)

func TestNew_DisabledDoesNotCreateExporters(t *testing.T) {
	settings := config.Telemetry{Enabled: false, ExporterTimeout: time.Second}
	options := testOptions()
	httpPropagator := testPropagator{header: "x-http-trace"}
	grpcPropagator := testPropagator{header: "x-grpc-trace"}
	options.HTTPPropagator = httpPropagator
	options.GRPCPropagator = grpcPropagator

	provider, err := newProvider(
		context.Background(),
		settings,
		options,
		func(context.Context, config.Telemetry) (sdktrace.SpanExporter, error) {
			t.Fatal("disabled telemetry created a trace exporter")
			return nil, nil
		},
		func(context.Context, config.Telemetry) (sdkmetric.Reader, error) {
			t.Fatal("disabled telemetry created a metric reader")
			return nil, nil
		},
		func(metric.MeterProvider, string) (metric.Registration, error) {
			t.Fatal("disabled telemetry registered runtime instruments")
			return nil, nil
		},
	)
	if err != nil {
		t.Fatalf("new disabled provider: %v", err)
	}

	_, span := provider.TracerProvider().Tracer("test").Start(context.Background(), "disabled")
	span.End()
	if span.IsRecording() {
		t.Error("disabled span is recording")
	}
	for name, propagator := range map[string]propagation.TextMapPropagator{
		"http": provider.HTTPPropagator(),
		"grpc": provider.GRPCPropagator(),
	} {
		carrier := propagation.MapCarrier{}
		propagator.Inject(context.Background(), carrier)
		if carrier.Get("x-"+name+"-trace") != "injected" {
			t.Errorf("%s propagator was not retained: %#v", name, carrier)
		}
	}
	if err := provider.Shutdown(context.Background()); err != nil {
		t.Errorf("shutdown disabled provider: %v", err)
	}
}

func TestProvider_ExportsIdentifiedSpansAndScopedRuntimeMetrics(t *testing.T) {
	exporter := newRecordingExporter()
	reader := sdkmetric.NewManualReader()
	provider, err := newProvider(
		context.Background(),
		testTelemetrySettings(time.Second),
		Options{
			RuntimeScope: "insurance-hub/test/runtime",
			MetricViews: []sdkmetric.View{
				HistogramView("test.request.duration", []float64{0.01, 0.1, 1}),
			},
		},
		func(context.Context, config.Telemetry) (sdktrace.SpanExporter, error) {
			return exporter, nil
		},
		func(context.Context, config.Telemetry) (sdkmetric.Reader, error) {
			return reader, nil
		},
		registerRuntime,
	)
	if err != nil {
		t.Fatalf("new provider: %v", err)
	}
	t.Cleanup(func() { _ = provider.Shutdown(context.Background()) })

	ctx, span := provider.TracerProvider().Tracer("test.scope").Start(
		context.Background(),
		"exported",
	)
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
	assertResourceIdentity(t, exported[0].Resource().Attributes())

	histogram, err := provider.MeterProvider().Meter("test.metrics").Float64Histogram(
		"test.request.duration",
	)
	if err != nil {
		t.Fatalf("create histogram: %v", err)
	}
	histogram.Record(context.Background(), 0.05)
	var metrics metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &metrics); err != nil {
		t.Fatalf("collect metrics: %v", err)
	}
	assertResourceIdentity(t, metrics.Resource.Attributes())
	gotScopes := map[string]bool{}
	gotRuntime := false
	gotHistogram := false
	for _, scoped := range metrics.ScopeMetrics {
		gotScopes[scoped.Scope.Name] = true
		for _, measurement := range scoped.Metrics {
			if measurement.Name == "go.memory.allocations" {
				gotRuntime = true
			}
			if measurement.Name == "test.request.duration" {
				gotHistogram = true
				histogramData, ok := measurement.Data.(metricdata.Histogram[float64])
				if !ok || len(histogramData.DataPoints) != 1 ||
					len(histogramData.DataPoints[0].Bounds) != 3 {
					t.Errorf("unexpected histogram data: %#v", measurement.Data)
				}
			}
		}
	}
	if !gotScopes["insurance-hub/test/runtime"] || !gotScopes["test.metrics"] ||
		!gotRuntime || !gotHistogram {
		t.Errorf("scopes=%v runtime=%v histogram=%v", gotScopes, gotRuntime, gotHistogram)
	}
}

func TestProvider_DefaultPropagationAndParentSampling(t *testing.T) {
	provider, err := newProvider(
		context.Background(),
		config.Telemetry{Enabled: false, ExporterTimeout: time.Second},
		testOptions(),
		unexpectedExporter(t),
		unexpectedReader(t),
		unexpectedRuntime(t),
	)
	if err != nil {
		t.Fatalf("new disabled provider: %v", err)
	}
	traceID := trace.TraceID{
		0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08,
		0x09, 0x0a, 0x0b, 0x0c, 0x0d, 0x0e, 0x0f, 0x10,
	}
	spanID := trace.SpanID{0x11, 0x12, 0x13, 0x14, 0x15, 0x16, 0x17, 0x18}
	spanContext := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: traceID, SpanID: spanID, TraceFlags: trace.FlagsSampled,
	})
	carrier := propagation.MapCarrier{}
	provider.GRPCPropagator().Inject(
		trace.ContextWithSpanContext(context.Background(), spanContext),
		carrier,
	)
	extracted := trace.SpanContextFromContext(
		provider.HTTPPropagator().Extract(context.Background(), carrier),
	)
	if carrier.Get("traceparent") == "" || extracted.TraceID() != traceID ||
		extracted.SpanID() != spanID || !extracted.IsRemote() {
		t.Errorf("unexpected W3C propagation: carrier=%v context=%v", carrier, extracted)
	}

	exporter := newRecordingExporter()
	enabled := enabledProvider(t, exporter, time.Second)
	t.Cleanup(func() { _ = enabled.Shutdown(context.Background()) })
	unsampledParent := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: traceID, SpanID: spanID, Remote: true,
	})
	_, child := enabled.TracerProvider().Tracer("test").Start(
		trace.ContextWithRemoteSpanContext(context.Background(), unsampledParent),
		"child",
	)
	if child.IsRecording() {
		t.Error("remote unsampled parent produced a recording child")
	}
	child.End()
	_, root := enabled.TracerProvider().Tracer("test").Start(context.Background(), "root")
	if !root.IsRecording() {
		t.Error("root span was not sampled")
	}
	root.End()
}

func TestProvider_BoundsInitializationAndHidesCauseDetails(t *testing.T) {
	const privateMarker = "private-collector-marker"
	cause := errors.New(privateMarker)
	settings := testTelemetrySettings(25 * time.Millisecond)
	started := make(chan struct{})

	start := time.Now()
	_, err := newProvider(
		context.Background(),
		settings,
		testOptions(),
		func(ctx context.Context, _ config.Telemetry) (sdktrace.SpanExporter, error) {
			close(started)
			<-ctx.Done()
			return nil, cause
		},
		unexpectedReader(t),
		unexpectedRuntime(t),
	)
	elapsed := time.Since(start)

	if !errors.Is(err, cause) || strings.Contains(err.Error(), privateMarker) {
		t.Fatalf("unsafe or incomplete initialization error: %v", err)
	}
	select {
	case <-started:
	default:
		t.Error("exporter initialization did not start")
	}
	if elapsed < settings.ExporterTimeout || elapsed > time.Second {
		t.Errorf(
			"initialization elapsed %v, want between %v and 1s",
			elapsed,
			settings.ExporterTimeout,
		)
	}
}

func TestProvider_PartialStartupFailuresCloseOwnedResources(t *testing.T) {
	t.Run("metric reader", func(t *testing.T) {
		exporter := newRecordingExporter()
		cause := errors.New("private-metric-marker")
		_, err := newProvider(
			context.Background(),
			testTelemetrySettings(time.Second),
			testOptions(),
			func(context.Context, config.Telemetry) (sdktrace.SpanExporter, error) {
				return exporter, nil
			},
			func(context.Context, config.Telemetry) (sdkmetric.Reader, error) {
				return nil, cause
			},
			unexpectedRuntime(t),
		)
		if !errors.Is(err, cause) ||
			err.Error() != "initialize telemetry: OTLP metric exporter failed" {
			t.Fatalf("unexpected metric error: %v", err)
		}
		assertClosed(t, exporter.shutdown, "trace exporter")
	})

	t.Run("runtime registration", func(t *testing.T) {
		exporter := newRecordingExporter()
		reader := sdkmetric.NewManualReader()
		_, err := newProvider(
			context.Background(),
			testTelemetrySettings(time.Second),
			testOptions(),
			func(context.Context, config.Telemetry) (sdktrace.SpanExporter, error) {
				return exporter, nil
			},
			func(context.Context, config.Telemetry) (sdkmetric.Reader, error) {
				return reader, nil
			},
			func(metric.MeterProvider, string) (metric.Registration, error) {
				return nil, errors.New("private-runtime-marker")
			},
		)
		if err == nil || err.Error() != "initialize telemetry: runtime instruments failed" {
			t.Fatalf("unexpected runtime error: %v", err)
		}
		assertClosed(t, exporter.shutdown, "trace exporter")
		var result metricdata.ResourceMetrics
		if collectErr := reader.Collect(context.Background(), &result); collectErr == nil {
			t.Error("metric reader remained active after runtime startup failure")
		}
	})
}

func TestProvider_FullQueueDoesNotBlockSpanCompletionAndShutdownIsBounded(t *testing.T) {
	exporter := &blockingExporter{
		started:  make(chan struct{}),
		shutdown: make(chan struct{}),
	}
	provider := enabledProvider(t, exporter, 100*time.Millisecond)
	tracer := provider.TracerProvider().Tracer("test")
	_, first := tracer.Start(context.Background(), "first")
	first.End()
	flushResult := make(chan error, 1)
	go func() { flushResult <- provider.ForceFlush(context.Background()) }()
	assertClosed(t, exporter.started, "export start")

	completed := make(chan struct{})
	go func() {
		defer close(completed)
		for index := 0; index < traceQueueSize+traceBatchSize+1024; index++ {
			_, span := tracer.Start(context.Background(), "queued")
			span.End()
		}
	}()
	select {
	case <-completed:
	case <-time.After(time.Second):
		t.Fatal("span completion blocked on a full export queue")
	}
	select {
	case <-flushResult:
	case <-time.After(time.Second):
		t.Fatal("force flush exceeded exporter timeout")
	}

	start := time.Now()
	err := provider.Shutdown(context.Background())
	if err == nil {
		t.Fatal("shutdown returned nil error for unavailable exporter")
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("shutdown exceeded bound: %v", elapsed)
	}
	assertClosed(t, exporter.shutdown, "exporter shutdown")
}

func TestNew_RejectsInvalidOptionsSettingsAndIdentityBeforeStartup(t *testing.T) {
	tests := []struct {
		name     string
		settings config.Telemetry
		options  Options
	}{
		{"missing runtime scope", testTelemetrySettings(time.Second), Options{}},
		{
			"missing endpoint",
			config.Telemetry{Enabled: true, ExporterTimeout: time.Second},
			testOptions(),
		},
		{
			"non-positive timeout",
			config.Telemetry{
				Enabled:          true,
				ExporterEndpoint: mustURL("http://127.0.0.1:4317"),
			},
			testOptions(),
		},
		{
			"incomplete identity",
			config.Telemetry{
				Enabled:          true,
				ExporterEndpoint: mustURL("http://127.0.0.1:4317"),
				ExporterTimeout:  time.Second,
				Identity:         identity.Identity{ServiceName: "go-service"},
			},
			testOptions(),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := newProvider(
				context.Background(),
				test.settings,
				test.options,
				unexpectedExporter(t),
				unexpectedReader(t),
				unexpectedRuntime(t),
			)
			if err == nil {
				t.Fatal("new provider returned nil error")
			}
		})
	}
}

func TestInstallDiagnostics_RateLimitsRedactsAndRestoresPreviousHandler(t *testing.T) {
	original := otel.GetErrorHandler()
	previous := &recordingErrorHandler{}
	otel.SetErrorHandler(previous)
	t.Cleanup(func() { otel.SetErrorHandler(original) })
	var output bytes.Buffer
	restore := InstallDiagnostics(slog.New(slog.NewJSONHandler(&output, nil)))

	for range 100 {
		otel.Handle(errors.New("private-collector-marker"))
	}
	if strings.Contains(output.String(), "private-collector-marker") {
		t.Fatalf("diagnostic disclosed collector text: %s", output.String())
	}
	lines := bytes.Split(bytes.TrimSpace(output.Bytes()), []byte("\n"))
	if len(lines) != 1 {
		t.Fatalf("diagnostic lines = %d, want one", len(lines))
	}
	var record map[string]any
	if err := json.Unmarshal(lines[0], &record); err != nil {
		t.Fatalf("decode diagnostic: %v", err)
	}
	if record["msg"] != "telemetry export failed" || record["level"] != "WARN" {
		t.Errorf("unexpected diagnostic: %#v", record)
	}

	restore()
	restore()
	if otel.GetErrorHandler() != previous {
		t.Fatal("diagnostic cleanup did not restore the previous global handler")
	}
	otel.Handle(errors.New("after-restore"))
	if previous.calls != 1 {
		t.Errorf("previous handler calls = %d, want 1", previous.calls)
	}
}

func TestInstallDiagnostics_FirstInstallationDetachesLogger(t *testing.T) {
	const marker = "private-after-cleanup-marker"
	if os.Getenv("SERVICEKIT_FRESH_DIAGNOSTICS") == "" {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		command := exec.CommandContext(
			ctx, os.Args[0],
			"-test.run=^TestInstallDiagnostics_FirstInstallationDetachesLogger$",
		)
		command.Env = append(os.Environ(), "SERVICEKIT_FRESH_DIAGNOSTICS=1")
		output, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("fresh-process diagnostics failed: %v: %s", err, output)
		}
		if bytes.Contains(output, []byte(marker)) {
			t.Fatalf("post-cleanup diagnostic exposed private marker: %s", output)
		}
		return
	}

	initial := otel.GetErrorHandler()
	var firstOutput bytes.Buffer
	restore := InstallDiagnostics(slog.New(slog.NewJSONHandler(&firstOutput, nil)))
	otel.Handle(errors.New("private-during-installation-marker"))
	if !bytes.Contains(firstOutput.Bytes(), []byte("telemetry export failed")) ||
		bytes.Contains(firstOutput.Bytes(), []byte("private-during-installation-marker")) {
		t.Fatalf("installed diagnostic was not safe: %s", firstOutput.String())
	}
	restore()
	if otel.GetErrorHandler() != initial {
		t.Fatal("first cleanup did not restore the original handler identity")
	}
	otel.Handle(errors.New(marker))
	if bytes.Count(firstOutput.Bytes(), []byte("telemetry export failed")) != 1 {
		t.Fatalf("first logger received post-cleanup diagnostic: %s", firstOutput.String())
	}
	var secondOutput bytes.Buffer
	restoreSecond := InstallDiagnostics(slog.New(slog.NewJSONHandler(&secondOutput, nil)))
	otel.Handle(errors.New("private-second-marker"))
	restoreSecond()
	if !bytes.Contains(secondOutput.Bytes(), []byte("telemetry export failed")) ||
		bytes.Contains(secondOutput.Bytes(), []byte("private-second-marker")) {
		t.Fatalf("second installation did not route safely: %s", secondOutput.String())
	}
}

func TestInstallDiagnostics_NestedInstallationsRestoreInLIFOOrder(t *testing.T) {
	original := otel.GetErrorHandler()
	previous := &recordingErrorHandler{}
	otel.SetErrorHandler(previous)
	t.Cleanup(func() { otel.SetErrorHandler(original) })
	var outerOutput, innerOutput bytes.Buffer
	restoreOuter := InstallDiagnostics(slog.New(slog.NewJSONHandler(&outerOutput, nil)))
	restoreInner := InstallDiagnostics(slog.New(slog.NewJSONHandler(&innerOutput, nil)))
	otel.Handle(errors.New("private-inner-marker"))
	restoreInner()
	otel.Handle(errors.New("private-outer-marker"))
	restoreOuter()
	otel.Handle(errors.New("after-restore"))
	if !bytes.Contains(innerOutput.Bytes(), []byte("telemetry export failed")) ||
		bytes.Contains(innerOutput.Bytes(), []byte("private-inner-marker")) ||
		!bytes.Contains(outerOutput.Bytes(), []byte("telemetry export failed")) ||
		bytes.Contains(outerOutput.Bytes(), []byte("private-outer-marker")) {
		t.Fatalf(
			"nested diagnostics were not safely routed: outer=%s inner=%s",
			outerOutput.String(),
			innerOutput.String(),
		)
	}
	if previous.calls != 1 || otel.GetErrorHandler() != previous {
		t.Fatal("nested cleanup did not restore the custom handler")
	}
}

type testPropagator struct{ header string }

func (propagator testPropagator) Inject(_ context.Context, carrier propagation.TextMapCarrier) {
	carrier.Set(propagator.header, "injected")
}

func (propagator testPropagator) Extract(
	ctx context.Context,
	_ propagation.TextMapCarrier,
) context.Context {
	return ctx
}

func (propagator testPropagator) Fields() []string { return []string{propagator.header} }

type recordingErrorHandler struct {
	mu    sync.Mutex
	calls int
}

func (handler *recordingErrorHandler) Handle(error) {
	handler.mu.Lock()
	handler.calls++
	handler.mu.Unlock()
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
	started  chan struct{}
	shutdown chan struct{}
	start    sync.Once
	stop     sync.Once
}

func (exporter *blockingExporter) ExportSpans(
	ctx context.Context,
	_ []sdktrace.ReadOnlySpan,
) error {
	exporter.start.Do(func() { close(exporter.started) })
	<-ctx.Done()
	return ctx.Err()
}

func (exporter *blockingExporter) Shutdown(context.Context) error {
	exporter.stop.Do(func() { close(exporter.shutdown) })
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
		testTelemetrySettings(timeout),
		testOptions(),
		func(context.Context, config.Telemetry) (sdktrace.SpanExporter, error) {
			return exporter, nil
		},
		func(context.Context, config.Telemetry) (sdkmetric.Reader, error) {
			return sdkmetric.NewManualReader(), nil
		},
		registerRuntime,
	)
	if err != nil {
		t.Fatalf("new enabled provider: %v", err)
	}
	return provider
}

func testOptions() Options {
	return Options{RuntimeScope: "insurance-hub/test/runtime"}
}

func testTelemetrySettings(timeout time.Duration) config.Telemetry {
	return config.Telemetry{
		Enabled:          true,
		ExporterEndpoint: mustURL("http://127.0.0.1:4317"),
		ExporterTimeout:  timeout,
		Identity: identity.Identity{
			ServiceNamespace: "insurance-hub",
			ServiceName:      "go-service",
			ServiceVersion:   "test-revision",
			ServiceInstance:  "test-instance",
			Environment:      "test",
			Implementation:   "go",
		},
	}
}

func mustURL(value string) *url.URL {
	parsed, err := url.Parse(value)
	if err != nil {
		panic(err)
	}
	return parsed
}

func unexpectedExporter(t *testing.T) exporterFactory {
	t.Helper()
	return func(context.Context, config.Telemetry) (sdktrace.SpanExporter, error) {
		t.Fatal("unexpected trace exporter creation")
		return nil, nil
	}
}

func unexpectedReader(t *testing.T) readerFactory {
	t.Helper()
	return func(context.Context, config.Telemetry) (sdkmetric.Reader, error) {
		t.Fatal("unexpected metric reader creation")
		return nil, nil
	}
}

func unexpectedRuntime(t *testing.T) runtimeFactory {
	t.Helper()
	return func(metric.MeterProvider, string) (metric.Registration, error) {
		t.Fatal("unexpected runtime registration")
		return nil, nil
	}
}

func receiveSpans(t *testing.T, spans <-chan []sdktrace.ReadOnlySpan) []sdktrace.ReadOnlySpan {
	t.Helper()
	select {
	case exported := <-spans:
		return exported
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for exported spans")
		return nil
	}
}

func assertResourceIdentity(t *testing.T, attributes []attribute.KeyValue) {
	t.Helper()
	got := map[string]string{}
	for _, attr := range attributes {
		got[string(attr.Key)] = attr.Value.AsString()
	}
	for key, want := range map[string]string{
		"service.namespace":                    "insurance-hub",
		"service.name":                         "go-service",
		"service.version":                      "test-revision",
		"service.instance.id":                  "test-instance",
		"deployment.environment.name":          "test",
		"insurance_hub.service.implementation": "go",
	} {
		if got[key] != want {
			t.Errorf("resource %s = %q, want %q", key, got[key], want)
		}
	}
}

func assertClosed(t *testing.T, channel <-chan struct{}, name string) {
	t.Helper()
	select {
	case <-channel:
	case <-time.After(time.Second):
		t.Fatalf("%s did not complete", name)
	}
}
