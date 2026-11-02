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

func TestDependencyObserver_RecordsBoundedSignalsWithoutQueryOrError(t *testing.T) {
	const privateMarker = "private-query-credential"
	var logs bytes.Buffer
	recorder := tracetest.NewSpanRecorder()
	tracerProvider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	t.Cleanup(func() { _ = tracerProvider.Shutdown(context.Background()) })
	observer, err := NewDependencyObserver(
		slog.New(slog.NewJSONHandler(&logs, nil)),
		metricnoop.NewMeterProvider(),
		tracerProvider,
	)
	if err != nil {
		t.Fatal(err)
	}
	ctx, request := observer.StartElasticsearch(context.Background(), ElasticsearchSearch)
	request.Finish(ctx, errors.New(privateMarker))
	combined := logs.String()
	for _, span := range recorder.Ended() {
		combined += span.Name()
		for _, attribute := range span.Attributes() {
			combined += string(attribute.Key) + attribute.Value.AsString()
		}
	}
	if strings.Contains(combined, privateMarker) {
		t.Fatalf("private marker escaped: %s", combined)
	}
	for _, expected := range []string{"elasticsearch", "search", "error"} {
		if !strings.Contains(combined, expected) {
			t.Errorf("missing bounded signal %q: %s", expected, combined)
		}
	}
}

func TestNewDependencyObserver_RequiresDependencies(t *testing.T) {
	if _, err := NewDependencyObserver(
		nil,
		metricnoop.NewMeterProvider(),
		sdktrace.NewTracerProvider(),
	); err == nil {
		t.Fatal("missing logger accepted")
	}
}
