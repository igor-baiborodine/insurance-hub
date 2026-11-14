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

func TestEventObserver_RecordsOnlyBoundedRegistrationAndCommitSignals(t *testing.T) {
	// given
	const privateMarker = "SYN-private-customer"
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

	// when
	ctx, observation := observer.Start(context.Background(), registrationEventType)
	observation.Finish(ctx, "error", errors.New(privateMarker))
	observer.RecordCommitFailure(context.Background(), privateMarker)

	// then
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
	for _, expected := range []string{
		"registered", "error", "policy registration", "offset commit failed", "unknown",
	} {
		if !strings.Contains(combined, expected) {
			t.Errorf("missing bounded signal %q: %s", expected, combined)
		}
	}
}

func TestEventObserver_DistinguishesDisabledIdleInFlightFailedAndRecovered(t *testing.T) {
	// given
	now := time.Date(2026, time.November, 14, 1, 0, 0, 0, time.UTC)
	_, disabledReader, disabledCleanup := newEventWorkerMetricFixture(
		t,
		false,
		func() time.Time { return now },
	)
	defer disabledCleanup()
	assertEventWorkerMetrics(t, disabledReader, 0, 0, 0)

	enabled, reader, cleanup := newEventWorkerMetricFixture(
		t,
		true,
		func() time.Time { return now },
	)
	defer cleanup()
	assertEventWorkerMetrics(t, reader, 1, 0, 0)

	// when
	ctx, failed := enabled.Start(context.Background(), registrationEventType)
	now = now.Add(5 * time.Second)

	// then
	assertEventWorkerMetrics(t, reader, 1, 5, 0)
	failed.Finish(ctx, "error", errors.New("private"))
	assertEventWorkerMetrics(t, reader, 1, 0, 1)

	// when
	ctx, recovered := enabled.Start(context.Background(), registrationEventType)
	recovered.Finish(ctx, "success", nil)

	// then
	assertEventWorkerMetrics(t, reader, 1, 0, 0)
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
	wantFailed int64,
) {
	t.Helper()
	var metrics metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &metrics); err != nil {
		t.Fatal(err)
	}
	gotEnabled, gotFailed := int64(-1), int64(-1)
	gotAge := -1.0
	for _, scope := range metrics.ScopeMetrics {
		for _, measurement := range scope.Metrics {
			switch data := measurement.Data.(type) {
			case metricdata.Gauge[int64]:
				assertRegistrationAttributes(t, data.DataPoints[0].Attributes)
				switch measurement.Name {
				case "insurance_hub.document.event.worker.enabled":
					gotEnabled = data.DataPoints[0].Value
				case "insurance_hub.document.event.worker.failed":
					gotFailed = data.DataPoints[0].Value
				}
			case metricdata.Gauge[float64]:
				if measurement.Name == "insurance_hub.document.event.worker.in_flight_age" {
					assertRegistrationAttributes(
						t,
						data.DataPoints[0].Attributes,
					)
					gotAge = data.DataPoints[0].Value
				}
			}
		}
	}
	if gotEnabled != wantEnabled || gotAge != wantAge || gotFailed != wantFailed {
		t.Fatalf(
			"worker metrics = enabled:%d age:%g failed:%d, want %d/%g/%d",
			gotEnabled,
			gotAge,
			gotFailed,
			wantEnabled,
			wantAge,
			wantFailed,
		)
	}
}

func assertRegistrationAttributes(t *testing.T, attributes attribute.Set) {
	t.Helper()
	values := attributes.ToSlice()
	if len(values) != 1 || values[0].Key != "insurance_hub.document.event.type" ||
		values[0].Value.AsString() != registrationEventType {
		t.Fatalf("worker metric attributes = %v", values)
	}
}
