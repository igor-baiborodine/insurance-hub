package telemetry

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	kitlogger "github.com/igor-baiborodine/insurance-hub/libs/servicekit/logger"
)

func TestNewRequestObserver_RequiresDependencies(t *testing.T) {
	// given
	provider := sdkmetric.NewMeterProvider()
	t.Cleanup(func() { _ = provider.Shutdown(context.Background()) })
	log := slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))

	for _, test := range []struct {
		name     string
		logger   *slog.Logger
		provider metric.MeterProvider
	}{
		{name: "missing logger", provider: provider},
		{name: "missing meter provider", logger: log},
	} {
		t.Run(test.name, func(t *testing.T) {
			// when
			_, err := NewRequestObserver(test.logger, test.provider)

			// then
			if err == nil {
				t.Fatal("missing dependency accepted")
			}
		})
	}
}

func TestRequestObserver_RecordsBoundedGRPCSignals(t *testing.T) {
	// given
	testCases := []struct {
		status, wantStatus, outcome, level string
	}{
		{"OK", "OK", "success", "INFO"},
		{"INVALID_ARGUMENT", "INVALID_ARGUMENT", "invalid_request", "INFO"},
		{"CANCELLED", "CANCELLED", "cancelled", "WARN"},
		{"DEADLINE_EXCEEDED", "DEADLINE_EXCEEDED", "deadline_exceeded", "WARN"},
		{"INTERNAL", "INTERNAL", "internal_error", "ERROR"},
		{"unexpected-private-status", "UNKNOWN", "internal_error", "ERROR"},
	}
	observer, logs, reader, spans, cleanup := newRequestObserverTestFixture(t)
	defer cleanup()
	tracer := spans.provider.Tracer("request-test")

	// when
	for _, test := range testCases {
		ctx, span := tracer.Start(context.Background(), "grpc-request")
		observer.StartGRPC(ctx).Finish(ctx, test.status)
		span.End()
	}

	// then
	entries := decodeCompletionLogs(t, logs.String())
	if len(entries) != len(testCases) {
		t.Fatalf("completion logs = %d, want %d", len(entries), len(testCases))
	}
	for index, test := range testCases {
		entry := entries[index]
		assertCompletionFields(t, entry, "grpc", "search", test.outcome, test.level)
		if entry["rpc_status_code"] != test.wantStatus {
			t.Errorf(
				"log %d status = %v, want %s",
				index,
				entry["rpc_status_code"],
				test.wantStatus,
			)
		}
	}
	if strings.Contains(logs.String(), "unexpected-private-status") {
		t.Fatalf("unbounded status escaped into logs: %s", logs.String())
	}
	assertRequestMetrics(t, reader, len(testCases), "rpc.response.status_code")
	assertRequestSpans(t, spans.recorder.Ended(), len(testCases))
}

func TestRequestObserver_RecordsBoundedHTTPSignals(t *testing.T) {
	// given
	testCases := []struct {
		status           int
		route, operation string
		outcome, level   string
	}{
		{200, "GET /policies", "search", "success", "INFO"},
		{400, "unmatched", "unknown", "invalid_request", "INFO"},
		{499, "GET /policies", "search", "cancelled", "WARN"},
		{504, "GET /policies", "search", "deadline_exceeded", "WARN"},
		{500, "GET /policies", "search", "internal_error", "ERROR"},
	}
	observer, logs, reader, spans, cleanup := newRequestObserverTestFixture(t)
	defer cleanup()
	tracer := spans.provider.Tracer("request-test")

	// when
	for _, test := range testCases {
		ctx, span := tracer.Start(context.Background(), "http-request")
		observer.StartHTTP(ctx, test.route).FinishHTTP(ctx, test.status)
		span.End()
	}

	// then
	entries := decodeCompletionLogs(t, logs.String())
	if len(entries) != len(testCases) {
		t.Fatalf("completion logs = %d, want %d", len(entries), len(testCases))
	}
	for index, test := range testCases {
		entry := entries[index]
		assertCompletionFields(t, entry, "http", test.operation, test.outcome, test.level)
		if entry["route"] != test.route ||
			entry["http_status_code"] != float64(test.status) {
			t.Errorf(
				"log %d route/status = (%v, %v)",
				index,
				entry["route"],
				entry["http_status_code"],
			)
		}
	}
	assertRequestMetrics(t, reader, len(testCases), "http.response.status_code")
	assertRequestSpans(t, spans.recorder.Ended(), len(testCases))
}

type requestObserverSpans struct {
	provider *sdktrace.TracerProvider
	recorder *tracetest.SpanRecorder
}

func newRequestObserverTestFixture(
	t *testing.T,
) (*RequestObserver, *bytes.Buffer, *sdkmetric.ManualReader, requestObserverSpans, func()) {
	t.Helper()
	logs := new(bytes.Buffer)
	reader := sdkmetric.NewManualReader()
	meterProvider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	recorder := tracetest.NewSpanRecorder()
	tracerProvider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	observer, err := NewRequestObserver(
		kitlogger.New(logs, "policy-search-service", slog.LevelError),
		meterProvider,
	)
	if err != nil {
		t.Fatal(err)
	}
	cleanup := func() {
		_ = tracerProvider.Shutdown(context.Background())
		_ = meterProvider.Shutdown(context.Background())
	}
	return observer, logs, reader, requestObserverSpans{tracerProvider, recorder}, cleanup
}

func decodeCompletionLogs(t *testing.T, output string) []map[string]any {
	t.Helper()
	lines := strings.Split(strings.TrimSpace(output), "\n")
	entries := make([]map[string]any, 0, len(lines))
	for _, line := range lines {
		var entry map[string]any
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Fatalf("decode completion log: %v; line=%s", err, line)
		}
		entries = append(entries, entry)
	}
	return entries
}

func assertCompletionFields(
	t *testing.T,
	entry map[string]any,
	transport, operation, outcome, level string,
) {
	t.Helper()
	for key, want := range map[string]any{
		"msg": "request completed", "transport": transport, "operation": operation,
		"outcome": outcome, "level": level,
	} {
		if entry[key] != want {
			t.Errorf("completion %s = %v, want %v", key, entry[key], want)
		}
	}
	if duration, ok := entry["duration_ms"].(float64); !ok || duration < 0 {
		t.Errorf("completion duration = %v", entry["duration_ms"])
	}
	if entry["trace_id"] == nil || entry["span_id"] == nil {
		t.Errorf("completion log lacks trace correlation: %v", entry)
	}
}

func assertRequestMetrics(
	t *testing.T,
	reader *sdkmetric.ManualReader,
	want int,
	statusKey attribute.Key,
) {
	t.Helper()
	var metrics metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &metrics); err != nil {
		t.Fatal(err)
	}
	counts := map[string]int{}
	for _, scope := range metrics.ScopeMetrics {
		for _, measurement := range scope.Metrics {
			switch data := measurement.Data.(type) {
			case metricdata.Sum[int64]:
				if measurement.Name != "insurance_hub.policy_search.requests" ||
					measurement.Unit != "{request}" {
					t.Errorf("unexpected counter: %#v", measurement)
				}
				for _, point := range data.DataPoints {
					assertMetricDimensions(t, point.Attributes, statusKey)
					counts[measurement.Name] += int(point.Value)
				}
			case metricdata.Histogram[float64]:
				if measurement.Name != "insurance_hub.policy_search.request.duration" ||
					measurement.Unit != "s" {
					t.Errorf("unexpected histogram: %#v", measurement)
				}
				for _, point := range data.DataPoints {
					assertMetricDimensions(t, point.Attributes, statusKey)
					if point.Sum < 0 {
						t.Errorf("negative duration: %f", point.Sum)
					}
					counts[measurement.Name] += int(point.Count)
				}
			}
		}
	}
	for _, name := range []string{"insurance_hub.policy_search.requests", "insurance_hub.policy_search.request.duration"} {
		if counts[name] != want {
			t.Errorf("%s observations = %d, want %d", name, counts[name], want)
		}
	}
}

func assertMetricDimensions(t *testing.T, attributes attribute.Set, statusKey attribute.Key) {
	t.Helper()
	for _, key := range []attribute.Key{
		"insurance_hub.policy_search.operation",
		"insurance_hub.policy_search.transport",
		"insurance_hub.policy_search.outcome",
		statusKey,
	} {
		if _, ok := attributes.Value(key); !ok {
			t.Errorf("metric lacks %s: %v", key, attributes)
		}
	}
}

func assertRequestSpans(
	t *testing.T,
	spans []sdktrace.ReadOnlySpan,
	want int,
) {
	t.Helper()
	if len(spans) != want {
		t.Fatalf("ended spans = %d, want %d", len(spans), want)
	}
	for index, span := range spans {
		attrs := attribute.NewSet(span.Attributes()...)
		outcome, ok := attrs.Value("insurance_hub.policy_search.outcome")
		if !ok {
			t.Errorf("span %d lacks outcome: %v", index, attrs)
			continue
		}
		if outcome.AsString() == "success" {
			if span.Status().Code != 0 {
				t.Errorf("successful span %d status = %v", index, span.Status())
			}
			continue
		}
		errorType, ok := attrs.Value("error.type")
		if !ok || errorType.AsString() != outcome.AsString() || span.Status().Code == 0 {
			t.Errorf(
				"failed span %d status/attributes = (%v, %v)",
				index,
				span.Status(),
				attrs,
			)
		}
	}
}
