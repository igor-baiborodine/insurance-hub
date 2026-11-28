package telemetry

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	sdkmetricdata "go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/igor-baiborodine/insurance-hub/services/payment-service/internal/importer"
)

func TestStatementObserver_RecordsOnlyBoundedOutcomes(t *testing.T) {
	// given
	reader := sdkmetric.NewManualReader()
	meterProvider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	t.Cleanup(func() { _ = meterProvider.Shutdown(context.Background()) })
	spans := tracetest.NewSpanRecorder()
	tracerProvider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(spans))
	t.Cleanup(func() { _ = tracerProvider.Shutdown(context.Background()) })
	var logs bytes.Buffer
	observer, err := NewStatementObserver(
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
	finish(ctx, importer.Outcome("private-account-marker"), errors.New("private-row-marker"))
	var metrics sdkmetricdata.ResourceMetrics
	if err := reader.Collect(t.Context(), &metrics); err != nil {
		t.Fatal(err)
	}

	// then
	output := logs.String()
	if !strings.Contains(output, "outcome=dependency_failure") ||
		strings.Contains(output, "private-account-marker") ||
		strings.Contains(output, "private-row-marker") {
		t.Fatalf("unsafe or unbounded statement diagnostics: %q", output)
	}
	if len(spans.Ended()) != 1 {
		t.Fatalf("ended spans = %d, want 1", len(spans.Ended()))
	}
}
