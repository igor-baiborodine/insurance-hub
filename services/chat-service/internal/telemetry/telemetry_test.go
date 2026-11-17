package telemetry

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/igor-baiborodine/insurance-hub/libs/servicekit/identity"
	kitlogger "github.com/igor-baiborodine/insurance-hub/libs/servicekit/logger"
	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	"github.com/igor-baiborodine/insurance-hub/services/chat-service/internal/chat"
	"github.com/igor-baiborodine/insurance-hub/services/chat-service/internal/config"
	chatwebsocket "github.com/igor-baiborodine/insurance-hub/services/chat-service/internal/websocket"
)

func TestNew_DisabledProvider(t *testing.T) {
	// given
	settings := config.Telemetry{
		Identity:        config.DevelopmentIdentity(config.DefaultServiceName).Identity,
		ExporterTimeout: time.Second,
	}

	// when
	provider, err := New(
		context.Background(),
		config.DefaultServiceName,
		settings,
	)
	// then
	if err != nil {
		t.Fatal(err)
	}
	if err := provider.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestNew_EnabledProviderUsesSharedServicekitProviders(t *testing.T) {
	// given
	settings := config.Telemetry{
		Enabled: true,
		Identity: identity.Identity{
			ServiceNamespace: "insurance-hub",
			ServiceName:      config.DefaultServiceName,
			ServiceVersion:   "git-123456789abc",
			ServiceInstance:  "test-instance",
			Environment:      "test",
			Implementation:   "go",
		},
		ExporterEndpoint: mustURL("http://127.0.0.1:4317"),
		ExporterTimeout:  100 * time.Millisecond,
	}

	// when
	provider, err := New(context.Background(), config.DefaultServiceName, settings)
	// then
	if err != nil {
		t.Fatal(err)
	}
	if provider.MeterProvider() == nil || provider.TracerProvider() == nil {
		t.Fatal("enabled provider omitted shared providers")
	}
	_ = provider.Shutdown(context.Background())
}

func TestNew_InvalidIdentityDoesNotDiscloseValues(t *testing.T) {
	// given
	const private = "private-marker"
	settings := config.Telemetry{
		Enabled: true,
		Identity: identity.Identity{
			ServiceNamespace: "insurance-hub",
			ServiceName:      private,
			ServiceVersion:   "git-123456789abc",
			ServiceInstance:  "qa-chat-1",
			Environment:      "qa",
			Implementation:   "go",
		},
		ExporterEndpoint: mustURL("http://127.0.0.1:4317"),
		ExporterTimeout:  time.Second,
	}

	// when
	_, err := New(
		context.Background(),
		config.DefaultServiceName,
		settings,
	)

	// then
	if err == nil {
		t.Fatal("mismatched identity accepted")
	}
	if strings.Contains(err.Error(), private) {
		t.Fatalf("error disclosed identity: %v", err)
	}
}

func TestObserver_ExportsBoundedSignalsAndHealthyIdleZero(t *testing.T) {
	// given
	reader := sdkmetric.NewManualReader()
	meterProvider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	spanRecorder := tracetest.NewSpanRecorder()
	tracerProvider := sdktrace.NewTracerProvider(
		sdktrace.WithSpanProcessor(spanRecorder),
	)
	var logs bytes.Buffer
	observer, err := NewObserver(testLogger(&logs), meterProvider, tracerProvider)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := meterProvider.Shutdown(context.Background()); err != nil {
			t.Fatal(err)
		}
		if err := observer.Close(); err != nil {
			t.Fatal(err)
		}
		if err := tracerProvider.Shutdown(context.Background()); err != nil {
			t.Fatal(err)
		}
	})

	// when
	idle := collectMetrics(t, reader)
	observer.ConnectionOpened()
	active := collectMetrics(t, reader)
	const sensitive = "sensitive-user-topic-avatar-payload-origin-credential-error"
	ctx := context.WithValue(context.Background(), privateContextKey{}, sensitive)
	traceID := trace.TraceID{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16}
	ctx = trace.ContextWithRemoteSpanContext(ctx, trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    traceID,
		SpanID:     trace.SpanID{1, 2, 3, 4, 5, 6, 7, 8},
		TraceFlags: trace.FlagsSampled,
		Remote:     true,
	}))
	observer.ObserveHandshake(
		ctx,
		time.Now().Add(-time.Millisecond),
		chatwebsocket.HandshakeAccepted,
	)
	observer.ObserveBroadcast(chat.BroadcastEventMessage, chat.DeliveryOutcomeDelivered)
	observer.ObserveBroadcast(chat.BroadcastEvent(255), chat.DeliveryOutcome(255))
	observer.ObserveBroadcast(chat.BroadcastEvent(255), chat.DeliveryOutcomeQueueFull)
	observer.ConnectionClosed(chatwebsocket.CloseReasonNormal)
	observer.RecordShutdownFailure(context.Background())
	final := collectMetrics(t, reader)

	// then
	assertGaugeValue(t, idle, "insurance_hub.chat.connections.active", 0)
	assertGaugeValue(t, active, "insurance_hub.chat.connections.active", 1)
	assertGaugeValue(t, final, "insurance_hub.chat.connections.active", 0)
	assertCounterPoint(t, final, "insurance_hub.chat.handshakes", map[string]string{
		"insurance_hub.chat.handshake.outcome": "accepted",
	}, 1)
	assertHistogramPoint(t, final, "insurance_hub.chat.handshake.duration", map[string]string{
		"insurance_hub.chat.handshake.outcome": "accepted",
	}, 1)
	assertCounterPoint(t, final, "insurance_hub.chat.broadcast.deliveries", map[string]string{
		"insurance_hub.chat.broadcast.event":   "message",
		"insurance_hub.chat.broadcast.outcome": "delivered",
	}, 1)
	assertCounterPoint(t, final, "insurance_hub.chat.broadcast.deliveries", map[string]string{
		"insurance_hub.chat.broadcast.event":   "unknown",
		"insurance_hub.chat.broadcast.outcome": "unknown",
	}, 1)
	assertCounterPoint(t, final, "insurance_hub.chat.connection.closes", map[string]string{
		"insurance_hub.chat.connection.close.reason": "normal",
	}, 1)
	assertCounterPoint(t, final, "insurance_hub.chat.shutdown.failures", nil, 1)
	assertSignalSchemas(t, final)

	spans := spanRecorder.Ended()
	if len(spans) != 1 || spans[0].Name() != "Chat WebSocket handshake" {
		t.Fatalf("ended spans = %v", spans)
	}
	if spans[0].InstrumentationScope().Name != "insurance-hub/service" {
		t.Fatalf("span scope = %q", spans[0].InstrumentationScope().Name)
	}
	if got := attributeValue(
		spans[0].Attributes(),
		"insurance_hub.chat.handshake.outcome",
	); got != "accepted" {
		t.Fatalf("handshake span outcome = %q", got)
	}
	if len(spans[0].Attributes()) != 1 {
		t.Fatalf("handshake span attributes = %v", spans[0].Attributes())
	}
	metrics := fmt.Sprint(final)
	if strings.Contains(logs.String(), sensitive) || strings.Contains(metrics, sensitive) {
		t.Fatalf("telemetry disclosed sensitive marker %q", sensitive)
	}
	if strings.Contains(metrics, traceID.String()) {
		t.Fatalf("metrics included trace ID %q", traceID)
	}
	if !strings.Contains(logs.String(), traceID.String()) ||
		!strings.Contains(logs.String(), `"span_id":"`) {
		t.Fatalf("handshake log omitted trace correlation: %s", logs.String())
	}
	for _, expected := range []string{
		"chat WebSocket handshake completed",
		`"outcome":"accepted"`,
		`"duration_ms":`,
		`"reason":"normal"`,
		`"event":"unknown"`,
		`"outcome":"queue_full"`,
	} {
		if !strings.Contains(logs.String(), expected) {
			t.Errorf("safe structured logs missing %q: %s", expected, logs.String())
		}
	}
}

func TestObserver_CloseUnregistersObservableCallbackOnce(t *testing.T) {
	// given
	calls := make([]string, 0, 1)
	observer := &Observer{
		registration: &trackingRegistration{unregister: func() error {
			calls = append(calls, "registration")
			return nil
		}},
	}

	// when
	first := observer.Close()
	second := observer.Close()

	// then
	if first != nil || second != nil {
		t.Fatalf("shutdown errors = %v, %v", first, second)
	}
	if !slices.Equal(calls, []string{"registration"}) {
		t.Fatalf("shutdown calls = %v", calls)
	}
}

func TestNewObserver_RequiresCommonDependencies(t *testing.T) {
	if _, err := NewObserver(
		nil,
		sdkmetric.NewMeterProvider(),
		sdktrace.NewTracerProvider(),
	); err == nil {
		t.Fatal("missing logger accepted")
	}
}

func testLogger(writer io.Writer) *slog.Logger {
	return kitlogger.New(writer, "chat-service", slog.LevelDebug)
}

type privateContextKey struct{}

type trackingRegistration struct{ unregister func() error }

func (registration *trackingRegistration) Unregister() error {
	return registration.unregister()
}

func collectMetrics(t *testing.T, reader *sdkmetric.ManualReader) metricdata.ResourceMetrics {
	t.Helper()
	var metrics metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &metrics); err != nil {
		t.Fatal(err)
	}
	return metrics
}

func findMetric(t *testing.T, metrics metricdata.ResourceMetrics, name string) metricdata.Metrics {
	t.Helper()
	for _, scope := range metrics.ScopeMetrics {
		for _, candidate := range scope.Metrics {
			if candidate.Name == name {
				return candidate
			}
		}
	}
	t.Fatalf("metric %q was not collected", name)
	return metricdata.Metrics{}
}

func assertGaugeValue(
	t *testing.T,
	metrics metricdata.ResourceMetrics,
	name string,
	want int64,
) {
	t.Helper()
	gauge, ok := findMetric(t, metrics, name).Data.(metricdata.Gauge[int64])
	if !ok || len(gauge.DataPoints) != 1 || gauge.DataPoints[0].Value != want {
		t.Fatalf("gauge %q = %#v, want %d", name, gauge, want)
	}
}

func assertCounterPoint(
	t *testing.T,
	metrics metricdata.ResourceMetrics,
	name string,
	wantAttributes map[string]string,
	wantValue int64,
) {
	t.Helper()
	sum, ok := findMetric(t, metrics, name).Data.(metricdata.Sum[int64])
	if !ok {
		t.Fatalf("metric %q is not an int64 sum", name)
	}
	for _, point := range sum.DataPoints {
		if attributesEqual(point.Attributes, wantAttributes) {
			if point.Value != wantValue {
				t.Fatalf(
					"counter %q value = %d, want %d",
					name,
					point.Value,
					wantValue,
				)
			}
			return
		}
	}
	t.Fatalf("counter %q missing attributes %v: %#v", name, wantAttributes, sum.DataPoints)
}

func assertHistogramPoint(
	t *testing.T,
	metrics metricdata.ResourceMetrics,
	name string,
	wantAttributes map[string]string,
	wantCount uint64,
) {
	t.Helper()
	histogram, ok := findMetric(t, metrics, name).Data.(metricdata.Histogram[float64])
	if !ok {
		t.Fatalf("metric %q is not a float64 histogram", name)
	}
	for _, point := range histogram.DataPoints {
		if attributesEqual(point.Attributes, wantAttributes) {
			if point.Count != wantCount || point.Sum <= 0 {
				t.Fatalf("histogram %q point = %#v", name, point)
			}
			return
		}
	}
	t.Fatalf("histogram %q missing attributes %v", name, wantAttributes)
}

func attributesEqual(values attribute.Set, want map[string]string) bool {
	if values.Len() != len(want) {
		return false
	}
	for key, wantValue := range want {
		value, ok := values.Value(attribute.Key(key))
		if !ok || value.AsString() != wantValue {
			return false
		}
	}
	return true
}

func attributeValue(values []attribute.KeyValue, key string) string {
	for _, value := range values {
		if string(value.Key) == key {
			return value.Value.AsString()
		}
	}
	return ""
}

func assertSignalSchemas(t *testing.T, metrics metricdata.ResourceMetrics) {
	t.Helper()
	wantKeys := map[string][]string{
		"insurance_hub.chat.connections.active": {},
		"insurance_hub.chat.handshakes":         {"insurance_hub.chat.handshake.outcome"},
		"insurance_hub.chat.handshake.duration": {"insurance_hub.chat.handshake.outcome"},
		"insurance_hub.chat.broadcast.deliveries": {
			"insurance_hub.chat.broadcast.event",
			"insurance_hub.chat.broadcast.outcome",
		},
		"insurance_hub.chat.connection.closes": {
			"insurance_hub.chat.connection.close.reason",
		},
		"insurance_hub.chat.shutdown.failures": {},
	}
	wantUnits := map[string]string{
		"insurance_hub.chat.connections.active":   "{connection}",
		"insurance_hub.chat.handshakes":           "{handshake}",
		"insurance_hub.chat.handshake.duration":   "s",
		"insurance_hub.chat.broadcast.deliveries": "{delivery}",
		"insurance_hub.chat.connection.closes":    "{connection}",
		"insurance_hub.chat.shutdown.failures":    "{failure}",
	}
	for name, keys := range wantKeys {
		candidate := findMetric(t, metrics, name)
		if candidate.Unit != wantUnits[name] {
			t.Fatalf(
				"metric %q unit = %q, want %q",
				name,
				candidate.Unit,
				wantUnits[name],
			)
		}
		for _, attributes := range metricAttributeSets(candidate) {
			actual := make([]string, 0, attributes.Len())
			iterator := attributes.Iter()
			for iterator.Next() {
				actual = append(actual, string(iterator.Attribute().Key))
			}
			slices.Sort(actual)
			expected := append([]string(nil), keys...)
			slices.Sort(expected)
			if !slices.Equal(actual, expected) {
				t.Fatalf(
					"metric %q attribute keys = %v, want %v",
					name,
					actual,
					expected,
				)
			}
		}
	}
	for _, scope := range metrics.ScopeMetrics {
		if len(scope.Metrics) > 0 && scope.Scope.Name != "insurance-hub/service" {
			t.Fatalf("metric scope = %q, want insurance-hub/service", scope.Scope.Name)
		}
	}
}

func metricAttributeSets(candidate metricdata.Metrics) []attribute.Set {
	switch data := candidate.Data.(type) {
	case metricdata.Gauge[int64]:
		values := make([]attribute.Set, 0, len(data.DataPoints))
		for _, point := range data.DataPoints {
			values = append(values, point.Attributes)
		}
		return values
	case metricdata.Sum[int64]:
		values := make([]attribute.Set, 0, len(data.DataPoints))
		for _, point := range data.DataPoints {
			values = append(values, point.Attributes)
		}
		return values
	case metricdata.Histogram[float64]:
		values := make([]attribute.Set, 0, len(data.DataPoints))
		for _, point := range data.DataPoints {
			values = append(values, point.Attributes)
		}
		return values
	default:
		return nil
	}
}

func mustURL(value string) *url.URL {
	parsed, err := url.Parse(value)
	if err != nil {
		panic(err)
	}
	return parsed
}
