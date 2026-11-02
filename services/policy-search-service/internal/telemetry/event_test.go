package telemetry

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	metricnoop "go.opentelemetry.io/otel/metric/noop"
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
	); err == nil {
		t.Fatal("missing logger accepted")
	}
}
