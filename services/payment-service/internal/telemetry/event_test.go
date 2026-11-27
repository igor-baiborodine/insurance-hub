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
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestEventObserver_RecordsOnlyBoundedOutcomes(t *testing.T) {
	// given
	reader := sdkmetric.NewManualReader()
	meterProvider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	t.Cleanup(func() { _ = meterProvider.Shutdown(context.Background()) })
	spans := tracetest.NewSpanRecorder()
	tracerProvider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(spans))
	t.Cleanup(func() { _ = tracerProvider.Shutdown(context.Background()) })
	var logs bytes.Buffer
	observer, err := NewEventObserver(
		slog.New(slog.NewTextHandler(&logs, nil)),
		meterProvider,
		tracerProvider,
		true,
	)
	if err != nil {
		t.Fatal(err)
	}

	// when
	ctx, finish := observer.Start(t.Context())
	finish(ctx, "unexpected-private-outcome", errors.New("private registration facts"))
	observer.RecordCommitFailure(t.Context())
	var metrics metricdata.ResourceMetrics
	if err := reader.Collect(t.Context(), &metrics); err != nil {
		t.Fatal(err)
	}

	// then
	want := map[string]bool{
		"insurance_hub.payment.events":                     false,
		"insurance_hub.payment.event.duration":             false,
		"insurance_hub.payment.event.commit.failures":      false,
		"insurance_hub.payment.event.worker.enabled":       false,
		"insurance_hub.payment.event.worker.in_flight_age": false,
	}
	for _, scope := range metrics.ScopeMetrics {
		for _, measurement := range scope.Metrics {
			if _, present := want[measurement.Name]; present {
				want[measurement.Name] = true
			}
		}
	}
	for name, found := range want {
		if !found {
			t.Errorf("metric %q was not recorded", name)
		}
	}
	if len(spans.Ended()) != 1 {
		t.Fatalf("ended spans = %d, want 1", len(spans.Ended()))
	}
	output := logs.String()
	if !strings.Contains(output, "outcome=held") ||
		strings.Contains(output, "private registration facts") ||
		strings.Contains(output, "unexpected-private-outcome") {
		t.Fatalf("unsafe or unbounded registration diagnostics: %q", output)
	}
}

func TestNewEventObserver_RequiresDependencies(t *testing.T) {
	if _, err := NewEventObserver(nil, nil, nil, true); err == nil {
		t.Fatal("missing event observer dependencies accepted")
	}
}

func TestEventObserver_ReportsDisabledIdleActiveAndRecoveredState(t *testing.T) {
	// given
	now := time.Date(2026, time.November, 27, 12, 0, 0, 0, time.UTC)
	_, disabledReader, disabledCleanup := newEventWorkerMetricFixture(
		t,
		false,
		func() time.Time { return now },
	)
	defer disabledCleanup()
	assertEventWorkerMetrics(t, disabledReader, 0, 0)

	enabled, reader, cleanup := newEventWorkerMetricFixture(
		t,
		true,
		func() time.Time { return now },
	)
	defer cleanup()
	assertEventWorkerMetrics(t, reader, 1, 0)

	// when
	ctx, finish := enabled.Start(t.Context())
	now = now.Add(45 * time.Second)

	// then
	assertEventWorkerMetrics(t, reader, 1, 45)
	finish(ctx, "success", nil)
	assertEventWorkerMetrics(t, reader, 1, 0)
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
	wantEnabled int64,
	wantAge float64,
) {
	t.Helper()
	var metrics metricdata.ResourceMetrics
	if err := reader.Collect(t.Context(), &metrics); err != nil {
		t.Fatal(err)
	}
	var gotEnabled int64
	var gotAge float64
	seenEnabled := false
	seenAge := false
	for _, scope := range metrics.ScopeMetrics {
		for _, measurement := range scope.Metrics {
			switch data := measurement.Data.(type) {
			case metricdata.Gauge[int64]:
				if measurement.Name == "insurance_hub.payment.event.worker.enabled" {
					assertEventAttributes(t, data.DataPoints[0].Attributes)
					gotEnabled = data.DataPoints[0].Value
					seenEnabled = true
				}
			case metricdata.Gauge[float64]:
				if measurement.Name == "insurance_hub.payment.event.worker.in_flight_age" {
					assertEventAttributes(t, data.DataPoints[0].Attributes)
					gotAge = data.DataPoints[0].Value
					seenAge = true
				}
			}
		}
	}
	if !seenEnabled || !seenAge || gotEnabled != wantEnabled || gotAge != wantAge {
		t.Fatalf(
			"worker metrics enabled=%d age=%g seen=(%t,%t), want enabled=%d age=%g",
			gotEnabled,
			gotAge,
			seenEnabled,
			seenAge,
			wantEnabled,
			wantAge,
		)
	}
}

func assertEventAttributes(t *testing.T, attributes attribute.Set) {
	t.Helper()
	values := attributes.ToSlice()
	if len(values) != 1 || values[0].Key != "insurance_hub.payment.event.type" ||
		values[0].Value.AsString() != registrationEventType {
		t.Fatalf("worker metric attributes = %v", values)
	}
}
