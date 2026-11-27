package telemetry

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestRegistrationObserver_RecordsOnlyBoundedOutcomes(t *testing.T) {
	// given
	reader := sdkmetric.NewManualReader()
	meterProvider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	t.Cleanup(func() { _ = meterProvider.Shutdown(context.Background()) })
	spans := tracetest.NewSpanRecorder()
	tracerProvider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(spans))
	t.Cleanup(func() { _ = tracerProvider.Shutdown(context.Background()) })
	var logs bytes.Buffer
	observer, err := NewRegistrationObserver(
		slog.New(slog.NewTextHandler(&logs, nil)),
		meterProvider,
		tracerProvider,
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
		"insurance_hub.payment.registrations":                false,
		"insurance_hub.payment.registration.duration":        false,
		"insurance_hub.payment.registration.commit.failures": false,
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

func TestNewRegistrationObserver_RequiresDependencies(t *testing.T) {
	if _, err := NewRegistrationObserver(nil, nil, nil); err == nil {
		t.Fatal("missing registration observer dependencies accepted")
	}
}
