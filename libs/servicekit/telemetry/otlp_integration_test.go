package telemetry

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	metriccollector "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	tracecollector "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	commonv1 "go.opentelemetry.io/proto/otlp/common/v1"
	"google.golang.org/grpc"

	"github.com/igor-baiborodine/insurance-hub/libs/servicekit/identity"
)

type otlpReceiver struct {
	traces  chan *tracecollector.ExportTraceServiceRequest
	metrics chan *metriccollector.ExportMetricsServiceRequest
}

// The two generated services both name their RPC Export, so separate adapters retain one receiver.
type traceReceiver struct {
	tracecollector.UnimplementedTraceServiceServer
	owner *otlpReceiver
}

func (receiver *traceReceiver) Export(
	_ context.Context,
	request *tracecollector.ExportTraceServiceRequest,
) (*tracecollector.ExportTraceServiceResponse, error) {
	receiver.owner.traces <- request
	return &tracecollector.ExportTraceServiceResponse{}, nil
}

type metricReceiver struct {
	metriccollector.UnimplementedMetricsServiceServer
	owner *otlpReceiver
}

func (receiver *metricReceiver) Export(
	_ context.Context,
	request *metriccollector.ExportMetricsServiceRequest,
) (*metriccollector.ExportMetricsServiceResponse, error) {
	receiver.owner.metrics <- request
	return &metriccollector.ExportMetricsServiceResponse{}, nil
}

func TestNew_PublicOTLPExportsTracesAndMetrics(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen for disposable OTLP receiver: %v", err)
	}
	receiver := &otlpReceiver{
		traces:  make(chan *tracecollector.ExportTraceServiceRequest, 4),
		metrics: make(chan *metriccollector.ExportMetricsServiceRequest, 4),
	}
	server := grpc.NewServer()
	tracecollector.RegisterTraceServiceServer(server, &traceReceiver{owner: receiver})
	metriccollector.RegisterMetricsServiceServer(server, &metricReceiver{owner: receiver})
	serveDone := make(chan error, 1)
	go func() { serveDone <- server.Serve(listener) }()
	t.Cleanup(func() {
		server.Stop()
		_ = listener.Close()
		select {
		case err := <-serveDone:
			if err != nil && !errors.Is(err, grpc.ErrServerStopped) {
				t.Errorf("disposable OTLP receiver failed: %v", err)
			}
		case <-time.After(time.Second):
			t.Error("disposable OTLP receiver did not stop")
		}
	})

	settings := testTelemetrySettings(2 * time.Second)
	settings.ExporterEndpoint = mustURL("http://" + listener.Addr().String())
	provider, err := New(context.Background(), settings, testOptions())
	if err != nil {
		t.Fatalf("construct public provider: %v", err)
	}
	t.Cleanup(func() { _ = provider.Shutdown(context.Background()) })
	_, span := provider.TracerProvider().Tracer("integration.traces").Start(
		context.Background(), "real-otlp-span",
	)
	span.End()
	counter, err := provider.MeterProvider().
		Meter("integration.metrics").
		Int64Counter("test.requests")
	if err != nil {
		t.Fatalf("create counter: %v", err)
	}
	counter.Add(context.Background(), 1)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := provider.ForceFlush(ctx); err != nil {
		t.Fatalf("force flush real OTLP exports: %v", err)
	}

	select {
	case request := <-receiver.traces:
		if len(request.ResourceSpans) != 1 ||
			request.ResourceSpans[0].ScopeSpans[0].Scope.Name != "integration.traces" ||
			request.ResourceSpans[0].ScopeSpans[0].Spans[0].Name != "real-otlp-span" {
			t.Fatalf("unexpected trace export: %#v", request.ResourceSpans)
		}
		assertOTLPIdentity(t, request.ResourceSpans[0].Resource.Attributes)
	case <-ctx.Done():
		t.Fatal("OTLP trace request not received")
	}
	select {
	case request := <-receiver.metrics:
		if len(request.ResourceMetrics) != 1 {
			t.Fatalf("unexpected metric resources: %#v", request.ResourceMetrics)
		}
		resource := request.ResourceMetrics[0]
		assertOTLPIdentity(t, resource.Resource.Attributes)
		found := false
		foundRuntimeScope := false
		for _, scope := range resource.ScopeMetrics {
			foundRuntimeScope = foundRuntimeScope ||
				scope.Scope.Name == "insurance-hub/test/runtime"
			if scope.Scope.Name == "integration.metrics" {
				for _, measurement := range scope.Metrics {
					found = found || measurement.Name == "test.requests"
				}
			}
		}
		if !found || !foundRuntimeScope {
			t.Fatalf(
				"caller or runtime metric scope absent from export: %#v",
				resource.ScopeMetrics,
			)
		}
	case <-ctx.Done():
		t.Fatal("OTLP metric request not received")
	}
	if err := provider.Shutdown(ctx); err != nil {
		t.Fatalf("shutdown real OTLP provider: %v", err)
	}
}

func TestNew_PublicOTLPDisabledAndUnavailableReceiver(t *testing.T) {
	disabledSettings := testTelemetrySettings(time.Second)
	disabledSettings.Enabled = false
	disabledSettings.ExporterEndpoint = nil
	disabledSettings.Identity = identity.Identity{}
	disabled, err := New(context.Background(), disabledSettings, testOptions())
	if err != nil {
		t.Fatalf("construct disabled provider: %v", err)
	}
	_, span := disabled.TracerProvider().Tracer("disabled").Start(context.Background(), "noop")
	if span.IsRecording() {
		t.Fatal("disabled public provider recorded a span")
	}
	span.End()
	if err := disabled.ForceFlush(context.Background()); err != nil {
		t.Fatalf("flush disabled provider: %v", err)
	}
	if err := disabled.Shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown disabled provider: %v", err)
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve unavailable port: %v", err)
	}
	address := listener.Addr().String()
	_ = listener.Close()
	settings := testTelemetrySettings(350 * time.Millisecond)
	settings.ExporterEndpoint = mustURL("http://" + address)
	started := time.Now()
	provider, err := New(context.Background(), settings, testOptions())
	if time.Since(started) > 2*time.Second {
		t.Fatal("unavailable receiver initialization exceeded bounded timeout")
	}
	if err != nil {
		// A transport implementation may fail eagerly; the failure must remain bounded and safe.
		return
	}
	_, span = provider.TracerProvider().
		Tracer("unavailable").
		Start(context.Background(), "failed")
	span.End()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := provider.ForceFlush(ctx); err == nil {
		t.Error("unavailable receiver unexpectedly accepted telemetry")
	}
	if err := provider.Shutdown(ctx); ctx.Err() != nil {
		t.Fatalf("shutdown exceeded caller deadline: %v", err)
	}
}

func assertOTLPIdentity(t *testing.T, attributes []*commonv1.KeyValue) {
	t.Helper()
	got := map[string]string{}
	for _, attribute := range attributes {
		got[attribute.Key] = attribute.Value.GetStringValue()
	}
	for key, want := range map[string]string{
		"service.namespace":                    "insurance-hub",
		"service.name":                         "go-service",
		"service.version":                      "test-revision",
		"service.instance.id":                  "test-instance",
		"deployment.environment.name":          "test",
		"insurance_hub.service.implementation": "go",
	} {
		if got[key] != want {
			t.Errorf("resource %s = %q, want %q", key, got[key], want)
		}
	}
}
