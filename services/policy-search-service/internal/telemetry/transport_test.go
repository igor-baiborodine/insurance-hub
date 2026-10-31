package telemetry

import (
	"context"
	"net"
	"testing"
	"time"

	collectormetric "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	collectortrace "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	common "go.opentelemetry.io/proto/otlp/common/v1"
	metricpb "go.opentelemetry.io/proto/otlp/metrics/v1"
	"google.golang.org/grpc"
)

type traceReceiver struct {
	collectortrace.UnimplementedTraceServiceServer
	requests chan *collectortrace.ExportTraceServiceRequest
}

func (receiver *traceReceiver) Export(
	ctx context.Context,
	request *collectortrace.ExportTraceServiceRequest,
) (*collectortrace.ExportTraceServiceResponse, error) {
	select {
	case receiver.requests <- request:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return &collectortrace.ExportTraceServiceResponse{}, nil
}

type metricReceiver struct {
	collectormetric.UnimplementedMetricsServiceServer
	requests chan *collectormetric.ExportMetricsServiceRequest
}

func (receiver *metricReceiver) Export(
	ctx context.Context,
	request *collectormetric.ExportMetricsServiceRequest,
) (*collectormetric.ExportMetricsServiceResponse, error) {
	select {
	case receiver.requests <- request:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return &collectormetric.ExportMetricsServiceResponse{}, nil
}

func TestProvider_ExportsIdentifiedTracesAndCumulativeRuntimeMetrics(t *testing.T) {
	// given
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	traces := &traceReceiver{requests: make(chan *collectortrace.ExportTraceServiceRequest, 8)}
	metrics := &metricReceiver{
		requests: make(chan *collectormetric.ExportMetricsServiceRequest, 8),
	}
	server := grpc.NewServer()
	collectortrace.RegisterTraceServiceServer(server, traces)
	collectormetric.RegisterMetricsServiceServer(server, metrics)
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	t.Cleanup(func() {
		server.Stop()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("collector did not stop")
		}
	})
	settings := testTelemetrySettings(time.Second)
	settings.ExporterEndpoint = mustURL("http://" + listener.Addr().String())
	provider, err := New(context.Background(), "policy-search-service", settings)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = provider.Shutdown(context.Background()) })
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	// when
	_, span := provider.TracerProvider().Tracer("test").Start(ctx, "policy-search-startup")
	span.End()
	err = provider.ForceFlush(ctx)
	// then
	if err != nil {
		t.Fatal(err)
	}
	select {
	case request := <-traces.requests:
		if len(request.ResourceSpans) != 1 {
			t.Fatalf("trace resources = %d", len(request.ResourceSpans))
		}
		assertResourceIdentity(t, request.ResourceSpans[0].Resource.Attributes)
		if request.ResourceSpans[0].ScopeSpans[0].Spans[0].Name != "policy-search-startup" {
			t.Error("missing example span")
		}
	case <-ctx.Done():
		t.Fatal("trace receiver timed out")
	}
	select {
	case request := <-metrics.requests:
		if len(request.ResourceMetrics) != 1 {
			t.Fatalf("metric resources = %d", len(request.ResourceMetrics))
		}
		assertResourceIdentity(t, request.ResourceMetrics[0].Resource.Attributes)
		found := false
		for _, scope := range request.ResourceMetrics[0].ScopeMetrics {
			for _, measurement := range scope.Metrics {
				if measurement.Name == "go.memory.allocations" {
					found = true
					if measurement.GetSum().
						GetAggregationTemporality() !=
						metricpb.AggregationTemporality_AGGREGATION_TEMPORALITY_CUMULATIVE {
						t.Error("runtime counter is not cumulative")
					}
				}
			}
		}
		if !found {
			t.Error("missing runtime allocations counter")
		}
	case <-ctx.Done():
		t.Fatal("metric receiver timed out")
	}
}

func assertResourceIdentity(t *testing.T, attributes []*common.KeyValue) {
	t.Helper()
	got := map[string]string{}
	for _, attr := range attributes {
		got[attr.Key] = attr.Value.GetStringValue()
	}
	for key, want := range map[string]string{
		"service.namespace": "insurance-hub", "service.name": "policy-search-service", "service.version": "test-revision",
		"service.instance.id": "123e4567-e89b-42d3-a456-426614174000", "deployment.environment.name": "test", "insurance_hub.service.implementation": "go",
	} {
		if got[key] != want {
			t.Errorf("resource %s = %s, want %s", key, got[key], want)
		}
	}
}
