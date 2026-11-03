package telemetry

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"go.opentelemetry.io/otel/attribute"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestEventObserver_RecordsOnlyBoundedEventSignals(t *testing.T) {
	const privateMarker = "SYN-230-private-customer"
	var logs bytes.Buffer
	recorder := tracetest.NewSpanRecorder()
	tracerProvider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	t.Cleanup(func() { _ = tracerProvider.Shutdown(context.Background()) })
	observer, err := NewEventObserver(
		slog.New(slog.NewJSONHandler(&logs, nil)),
		metricnoop.NewMeterProvider(),
		tracerProvider,
		true,
	)
	if err != nil {
		t.Fatal(err)
	}
	ctx, observation := observer.Start(context.Background(), "registered")
	observation.Finish(ctx, "error", errors.New(privateMarker))
	combined := logs.String()
	for _, span := range recorder.Ended() {
		combined += span.Name()
		for _, item := range span.Attributes() {
			combined += string(item.Key) + item.Value.AsString()
		}
	}
	if strings.Contains(combined, privateMarker) {
		t.Fatalf("private marker escaped: %s", combined)
	}
	for _, expected := range []string{"registered", "error", "policy event"} {
		if !strings.Contains(combined, expected) {
			t.Errorf("missing bounded signal %q: %s", expected, combined)
		}
	}
}

func TestNewEventObserver_RequiresDependencies(t *testing.T) {
	if _, err := NewEventObserver(
		nil,
		metricnoop.NewMeterProvider(),
		sdktrace.NewTracerProvider(),
		true,
	); err == nil {
		t.Fatal("missing logger accepted")
	}
}

func TestEventObserver_ReportsBoundedWorkerLifecycle(t *testing.T) {
	// given
	now := time.Date(2026, time.November, 3, 19, 0, 0, 0, time.UTC)
	observer, reader, cleanup := newEventWorkerMetricFixture(t, true, func() time.Time {
		return now
	})
	defer cleanup()

	// then: enabled workers begin idle with exactly two fixed event-type series.
	assertEventWorkerMetrics(t, reader, map[string]workerMetricValue{
		"registered": {enabled: 1, age: 0},
		"terminated": {enabled: 1, age: 0},
	})

	// when: one synchronous record remains in flight.
	ctx, observation := observer.Start(context.Background(), "registered")
	now = now.Add(75 * time.Second)

	// then: age grows only for that fixed event type.
	assertEventWorkerMetrics(t, reader, map[string]workerMetricValue{
		"registered": {enabled: 1, age: 75},
		"terminated": {enabled: 1, age: 0},
	})

	// when: the record completes.
	observation.Finish(ctx, "success", nil)

	// then: recovery resets age without changing enabled state.
	assertEventWorkerMetrics(t, reader, map[string]workerMetricValue{
		"registered": {enabled: 1, age: 0},
		"terminated": {enabled: 1, age: 0},
	})
}

func TestEventObserver_DisabledAndCancelledWorkerSemantics(t *testing.T) {
	// given: disabled mode still reports explicit zero state without constructing workers.
	disabled, disabledReader, disabledCleanup := newEventWorkerMetricFixture(
		t,
		false,
		time.Now,
	)
	defer disabledCleanup()
	ctx, disabledObservation := disabled.Start(context.Background(), "registered")
	assertEventWorkerMetrics(t, disabledReader, map[string]workerMetricValue{
		"registered": {enabled: 0, age: 0},
		"terminated": {enabled: 0, age: 0},
	})
	disabledObservation.Finish(ctx, "success", nil)

	// given: an enabled cancelled record recovers to idle.
	now := time.Date(2026, time.November, 3, 19, 0, 0, 0, time.UTC)
	enabled, reader, cleanup := newEventWorkerMetricFixture(t, true, func() time.Time {
		return now
	})
	defer cleanup()
	ctx, observation := enabled.Start(context.Background(), "terminated")
	now = now.Add(30 * time.Second)
	assertEventWorkerMetrics(t, reader, map[string]workerMetricValue{
		"registered": {enabled: 1, age: 0},
		"terminated": {enabled: 1, age: 30},
	})
	observation.Finish(ctx, "cancelled", context.Canceled)
	assertEventWorkerMetrics(t, reader, map[string]workerMetricValue{
		"registered": {enabled: 1, age: 0},
		"terminated": {enabled: 1, age: 0},
	})
}

type workerMetricValue struct {
	enabled int64
	age     float64
}

func newEventWorkerMetricFixture(
	t *testing.T,
	enabled bool,
	now func() time.Time,
) (*EventObserver, *sdkmetric.ManualReader, func()) {
	t.Helper()
	reader := sdkmetric.NewManualReader()
	meterProvider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	tracerProvider := sdktrace.NewTracerProvider()
	observer, err := newEventObserver(
		slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)),
		meterProvider,
		tracerProvider,
		enabled,
		now,
	)
	if err != nil {
		t.Fatal(err)
	}
	if observer.registration == nil {
		t.Fatal("event worker metric registration was not retained")
	}
	return observer, reader, func() {
		_ = tracerProvider.Shutdown(context.Background())
		_ = meterProvider.Shutdown(context.Background())
	}
}

func assertEventWorkerMetrics(
	t *testing.T,
	reader *sdkmetric.ManualReader,
	want map[string]workerMetricValue,
) {
	t.Helper()
	var metrics metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &metrics); err != nil {
		t.Fatal(err)
	}
	got := make(map[string]workerMetricValue, len(want))
	seen := make(map[string]map[string]bool)
	for _, scope := range metrics.ScopeMetrics {
		for _, measurement := range scope.Metrics {
			switch data := measurement.Data.(type) {
			case metricdata.Gauge[int64]:
				if measurement.Name != "insurance_hub.policy_search.event.worker.enabled" {
					continue
				}
				for _, point := range data.DataPoints {
					eventType := assertWorkerAttributes(t, point.Attributes)
					value := got[eventType]
					value.enabled = point.Value
					got[eventType] = value
					markWorkerMetric(seen, eventType, "enabled")
				}
			case metricdata.Gauge[float64]:
				if measurement.Name != "insurance_hub.policy_search.event.worker.in_flight_age" {
					continue
				}
				for _, point := range data.DataPoints {
					eventType := assertWorkerAttributes(t, point.Attributes)
					value := got[eventType]
					value.age = point.Value
					got[eventType] = value
					markWorkerMetric(seen, eventType, "age")
				}
			}
		}
	}
	if len(got) != len(want) {
		t.Fatalf("worker metric event types = %v, want %v", got, want)
	}
	for eventType, expected := range want {
		if got[eventType] != expected || !seen[eventType]["enabled"] ||
			!seen[eventType]["age"] {
			t.Errorf(
				"worker metric %s = %+v (%v), want %+v",
				eventType,
				got[eventType],
				seen[eventType],
				expected,
			)
		}
	}
}

func assertWorkerAttributes(t *testing.T, attributes attribute.Set) string {
	t.Helper()
	values := attributes.ToSlice()
	if len(values) != 1 || values[0].Key != "insurance_hub.policy_search.event.type" {
		t.Fatalf("worker metric attributes = %v", values)
	}
	eventType := values[0].Value.AsString()
	if eventType != "registered" && eventType != "terminated" {
		t.Fatalf("unbounded event type = %q", eventType)
	}
	return eventType
}

func markWorkerMetric(seen map[string]map[string]bool, eventType, metricName string) {
	if seen[eventType] == nil {
		seen[eventType] = make(map[string]bool)
	}
	seen[eventType][metricName] = true
}
