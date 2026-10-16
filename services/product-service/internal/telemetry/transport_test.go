package telemetry

import (
	"context"
	"net"
	"sync/atomic"
	"testing"
	"time"

	collectormetric "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	collectortrace "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/igor-baiborodine/insurance-hub/services/product-service/internal/config"
)

type disposableCollector struct {
	collectortrace.UnimplementedTraceServiceServer
	reject   atomic.Bool
	blocked  <-chan struct{}
	started  chan struct{}
	accepted chan struct{}
	metric   *metricCollector
}

func (collector *disposableCollector) Export(
	ctx context.Context,
	request *collectortrace.ExportTraceServiceRequest,
) (*collectortrace.ExportTraceServiceResponse, error) {
	select {
	case collector.started <- struct{}{}:
	default:
	}
	if collector.blocked != nil {
		select {
		case <-collector.blocked:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if collector.reject.Load() {
		return nil, status.Error(codes.Unavailable, "collector unavailable")
	}
	if len(request.ResourceSpans) != 0 {
		select {
		case collector.accepted <- struct{}{}:
		default:
		}
	}
	return &collectortrace.ExportTraceServiceResponse{}, nil
}

type metricCollector struct {
	collectormetric.UnimplementedMetricsServiceServer
	reject   atomic.Bool
	accepted chan struct{}
}

func (collector *metricCollector) Export(
	_ context.Context,
	request *collectormetric.ExportMetricsServiceRequest,
) (*collectormetric.ExportMetricsServiceResponse, error) {
	if collector.reject.Load() {
		return nil, status.Error(codes.Unavailable, "collector unavailable")
	}
	if len(request.ResourceMetrics) != 0 && collector.accepted != nil {
		select {
		case collector.accepted <- struct{}{}:
		default:
		}
	}
	return &collectormetric.ExportMetricsServiceResponse{}, nil
}

func startDisposableCollector(t *testing.T, collector *disposableCollector) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen for disposable collector: %v", err)
	}
	server := grpc.NewServer()
	collectortrace.RegisterTraceServiceServer(server, collector)
	metric := collector.metric
	if metric == nil {
		metric = &metricCollector{}
	}
	collectormetric.RegisterMetricsServiceServer(server, metric)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)
	return "http://" + listener.Addr().String()
}

func TestProvider_RealMetricCollectorRejectsThenRecovers(t *testing.T) {
	metric := &metricCollector{accepted: make(chan struct{}, 1)}
	metric.reject.Store(true)
	collector := &disposableCollector{
		started: make(chan struct{}, 1), accepted: make(chan struct{}, 1), metric: metric,
	}
	settings := testTelemetrySettings(2 * time.Second)
	settings.ExporterEndpoint = mustURL(startDisposableCollector(t, collector))
	provider, err := New(context.Background(), "product-service", settings)
	if err != nil {
		t.Fatalf("start provider with rejecting metric collector: %v", err)
	}
	t.Cleanup(func() { _ = provider.Shutdown(context.Background()) })
	counter, err := provider.MeterProvider().Meter("test").Int64Counter("test.requests")
	if err != nil {
		t.Fatalf("create test counter: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	counter.Add(ctx, 1)
	if err := provider.ForceFlush(ctx); err == nil {
		t.Fatal("rejected metric export unexpectedly succeeded")
	}
	metric.reject.Store(false)
	counter.Add(ctx, 1)
	if err := provider.ForceFlush(ctx); err != nil {
		t.Fatalf("metric export after collector recovery: %v", err)
	}
	select {
	case <-metric.accepted:
	case <-time.After(time.Second):
		t.Fatal("collector received no recovered metric")
	}
}

func TestProvider_RealCollectorRejectsThenRecoversWithoutRestart(t *testing.T) {
	collector := &disposableCollector{
		started:  make(chan struct{}, 1),
		accepted: make(chan struct{}, 1),
	}
	collector.reject.Store(true)
	settings := testTelemetrySettings(2 * time.Second)
	settings.ExporterEndpoint = mustURL(startDisposableCollector(t, collector))
	provider, err := New(context.Background(), "product-service", settings)
	if err != nil {
		t.Fatalf("start provider with rejecting collector: %v", err)
	}
	t.Cleanup(func() { _ = provider.Shutdown(context.Background()) })
	tracer := provider.TracerProvider().Tracer("test")

	_, rejected := tracer.Start(context.Background(), "rejected")
	rejected.End()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := provider.ForceFlush(ctx); err == nil {
		t.Fatal("rejected export unexpectedly succeeded")
	}
	select {
	case <-collector.started:
	default:
		t.Fatal("collector received no rejected request")
	}

	collector.reject.Store(false)
	_, recovered := tracer.Start(context.Background(), "recovered")
	recovered.End()
	if err := provider.ForceFlush(ctx); err != nil {
		t.Fatalf("export after collector recovery: %v", err)
	}
	select {
	case <-collector.accepted:
	case <-time.After(time.Second):
		t.Fatal("collector received no recovered span")
	}
}

func TestProvider_RealSlowCollectorHonorsCallerDeadline(t *testing.T) {
	release := make(chan struct{})
	collector := &disposableCollector{
		blocked:  release,
		started:  make(chan struct{}, 1),
		accepted: make(chan struct{}, 1),
	}
	settings := testTelemetrySettings(2 * time.Second)
	settings.ExporterEndpoint = mustURL(startDisposableCollector(t, collector))
	provider, err := New(context.Background(), "product-service", settings)
	if err != nil {
		t.Fatalf("start provider with slow collector: %v", err)
	}
	t.Cleanup(func() {
		close(release)
		_ = provider.Shutdown(context.Background())
	})
	_, span := provider.TracerProvider().Tracer("test").Start(context.Background(), "slow")
	span.End()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	started := time.Now()
	if err := provider.ForceFlush(ctx); err == nil {
		t.Fatal("slow export unexpectedly succeeded")
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("force flush exceeded caller deadline: %s", elapsed)
	}
	select {
	case <-collector.started:
	case <-time.After(time.Second):
		t.Fatal("slow collector received no export")
	}
}

func TestNew_DisabledWithEndpointDoesNotConnect(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen for connection probe: %v", err)
	}
	defer func() { _ = listener.Close() }()
	connectionAccepted := make(chan struct{}, 1)
	go func() {
		connection, acceptErr := listener.Accept()
		if acceptErr == nil {
			_ = connection.Close()
			connectionAccepted <- struct{}{}
		}
	}()
	provider, err := New(context.Background(), "product-service", config.Telemetry{
		ExporterEndpoint: mustURL("http://" + listener.Addr().String()),
		ExporterTimeout:  time.Second,
	})
	if err != nil {
		t.Fatalf("start disabled provider: %v", err)
	}
	_, span := provider.TracerProvider().Tracer("test").Start(context.Background(), "disabled")
	span.End()
	if err := provider.ForceFlush(context.Background()); err != nil {
		t.Fatalf("flush disabled provider: %v", err)
	}
	if err := provider.Shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown disabled provider: %v", err)
	}
	select {
	case <-connectionAccepted:
		t.Fatal("disabled provider connected to collector")
	case <-time.After(100 * time.Millisecond):
	}
}
