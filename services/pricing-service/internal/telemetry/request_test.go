package telemetry

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"sort"
	"strings"
	"testing"

	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestRequestObserver_UsesPricingSignalsAndBoundedAttributes(t *testing.T) {
	// given
	metricReader := sdkmetric.NewManualReader()
	meterProvider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(metricReader))
	spanRecorder := tracetest.NewSpanRecorder()
	traceProvider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(spanRecorder))
	t.Cleanup(func() {
		_ = meterProvider.Shutdown(context.Background())
		_ = traceProvider.Shutdown(context.Background())
	})
	var logs bytes.Buffer
	observer, err := NewRequestObserver(
		slog.New(slog.NewJSONHandler(&logs, nil)),
		meterProvider,
	)
	if err != nil {
		t.Fatal(err)
	}
	tracer := traceProvider.Tracer("test")
	httpCtx, httpSpan := tracer.Start(context.Background(), "POST /pricing/calculate")
	unmatchedCtx, unmatchedSpan := tracer.Start(context.Background(), "unmatched")
	rpcCtx, rpcSpan := tracer.Start(context.Background(), "CalculatePrice")

	// when
	observer.StartHTTP(httpCtx, "POST /pricing/calculate").FinishHTTP(httpCtx, 503)
	observer.StartHTTP(unmatchedCtx, "unmatched").FinishHTTP(unmatchedCtx, 404)
	observer.StartGRPC(rpcCtx).Finish(rpcCtx, "UNAVAILABLE")
	httpSpan.End()
	unmatchedSpan.End()
	rpcSpan.End()
	var collected metricdata.ResourceMetrics
	collectErr := metricReader.Collect(context.Background(), &collected)

	// then
	if collectErr != nil {
		t.Fatal(collectErr)
	}
	wantKeys := map[string]bool{
		"http.response.status_code|insurance_hub.pricing.operation|" +
			"insurance_hub.pricing.outcome|insurance_hub.pricing.transport": true,
		"insurance_hub.pricing.operation|insurance_hub.pricing.outcome|" +
			"insurance_hub.pricing.transport|rpc.response.status_code": true,
	}
	names := map[string]int{}
	for _, scope := range collected.ScopeMetrics {
		for _, measurement := range scope.Metrics {
			names[measurement.Name]++
			var sets []attribute.Set
			switch data := measurement.Data.(type) {
			case metricdata.Sum[int64]:
				for _, point := range data.DataPoints {
					sets = append(sets, point.Attributes)
				}
			case metricdata.Histogram[float64]:
				for _, point := range data.DataPoints {
					sets = append(sets, point.Attributes)
				}
			}
			if len(sets) != 3 {
				t.Errorf("%s points = %d, want 3", measurement.Name, len(sets))
			}
			for _, set := range sets {
				if !wantKeys[attributeKeys(set)] {
					t.Errorf(
						"%s attributes = %v",
						measurement.Name,
						set.ToSlice(),
					)
				}
			}
		}
	}
	if len(names) != 2 || names["insurance_hub.pricing.requests"] != 1 ||
		names["insurance_hub.pricing.request.duration"] != 1 {
		t.Errorf("request instruments = %v", names)
	}
	routes := map[string]string{}
	for _, span := range spanRecorder.Ended() {
		set := attribute.NewSet(span.Attributes()...)
		route, _ := set.Value("http.route")
		operation, _ := set.Value("insurance_hub.pricing.operation")
		routes[span.Name()] = route.AsString() + "|" + operation.AsString()
	}
	if routes["POST /pricing/calculate"] != "/pricing/calculate|calculate_price" ||
		routes["unmatched"] != "|unknown" || routes["CalculatePrice"] != "|calculate_price" {
		t.Errorf("span route and operation = %v", routes)
	}
	lines := strings.Split(strings.TrimSpace(logs.String()), "\n")
	if len(lines) != 3 {
		t.Fatalf("completion logs = %q", logs.String())
	}
	var first map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &first); err != nil {
		t.Fatal(err)
	}
	if first["operation"] != "calculate_price" || first["transport"] != "http" ||
		first["outcome"] != "internal_error" || first["http_status_code"] != float64(503) {
		t.Errorf("HTTP completion = %v", first)
	}
}

func attributeKeys(set attribute.Set) string {
	keys := make([]string, 0, set.Len())
	for _, attr := range set.ToSlice() {
		keys = append(keys, string(attr.Key))
	}
	sort.Strings(keys)
	return strings.Join(keys, "|")
}
