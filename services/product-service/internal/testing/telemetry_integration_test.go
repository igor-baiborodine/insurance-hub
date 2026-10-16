//go:build integration

package integrationtest

import (
	"context"
	"net"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	collectormetric "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	collectortrace "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type telemetryTraceCollector struct {
	collectortrace.UnimplementedTraceServiceServer
	reject   atomic.Bool
	blocked  <-chan struct{}
	started  chan struct{}
	accepted chan struct{}
}

func (collector *telemetryTraceCollector) Export(
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
		return nil, status.Error(codes.Unavailable, "private-collector-marker")
	}
	if len(request.ResourceSpans) != 0 {
		select {
		case collector.accepted <- struct{}{}:
		default:
		}
	}
	return &collectortrace.ExportTraceServiceResponse{}, nil
}

type telemetryMetricCollector struct {
	collectormetric.UnimplementedMetricsServiceServer
}

func (*telemetryMetricCollector) Export(
	context.Context,
	*collectormetric.ExportMetricsServiceRequest,
) (*collectormetric.ExportMetricsServiceResponse, error) {
	return &collectormetric.ExportMetricsServiceResponse{}, nil
}

func startTelemetryCollector(t *testing.T, collector *telemetryTraceCollector) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen for telemetry collector: %v", err)
	}
	server := grpc.NewServer()
	collectortrace.RegisterTraceServiceServer(server, collector)
	collectormetric.RegisterMetricsServiceServer(server, &telemetryMetricCollector{})
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)
	return "http://" + listener.Addr().String()
}

func TestProductTelemetry(t *testing.T) {
	if os.Getenv("PRODUCT_TEST_POSTGRES_IMAGE") != DefaultPostgresImage ||
		os.Getenv("PRODUCT_INTEGRATION_FIXTURE_SET") != string(FixtureQA) {
		t.Fatal("telemetry integration requires the pinned PostgreSQL image and QA fixture")
	}
	binary := os.Getenv("PRODUCT_TEST_SERVER_BINARY")
	if binary == "" {
		t.Fatal("telemetry integration requires the race-built Product executable")
	}
	workingDirectory, err := os.Getwd()
	if err != nil {
		t.Fatalf("get working directory: %v", err)
	}
	baselineRoot, err := ResolveBaselineRoot(workingDirectory)
	if err != nil {
		t.Fatalf("resolve baseline root: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	harness, err := Start(ctx, Options{FixtureRoot: baselineRoot, Image: DefaultPostgresImage})
	if err != nil {
		t.Fatalf("start harness: %v", err)
	}
	t.Cleanup(func() { _ = harness.Close() })
	if _, err := harness.LoadCatalog(ctx, FixtureQA); err != nil {
		t.Fatalf("load QA catalog: %v", err)
	}

	t.Run("disabled mode makes no exporter connection", func(t *testing.T) {
		listener, listenErr := net.Listen("tcp", "127.0.0.1:0")
		if listenErr != nil {
			t.Fatalf("listen for connection probe: %v", listenErr)
		}
		defer func() { _ = listener.Close() }()
		accepted := make(chan struct{}, 1)
		go func() {
			connection, acceptErr := listener.Accept()
			if acceptErr == nil {
				_ = connection.Close()
				accepted <- struct{}{}
			}
		}()
		process := startLifecycleProcess(t, binary, harness.RuntimeDatabaseURL(),
			"OTEL_ENABLED=false",
			"OTEL_EXPORTER_OTLP_ENDPOINT=http://"+listener.Addr().String(),
		)
		assertGracefulGRPCResult(t, process.getProduct("CAR"), "CAR")
		started := process.signal(t)
		process.assertCleanExit(t, started, harness)
		select {
		case <-accepted:
			t.Fatal("disabled Product connected to telemetry endpoint")
		default:
		}
	})

	t.Run("refused collector does not affect reads or probes", func(t *testing.T) {
		process := startLifecycleProcess(t, binary, harness.RuntimeDatabaseURL(),
			telemetryEnvironment("http://"+freeAddress(t))...,
		)
		assertGracefulGRPCResult(t, process.getProduct("CAR"), "CAR")
		if got := httpStatus(t, process.addresses.health, "/readyz"); got != 200 {
			t.Fatalf("readiness under collector refusal = %d", got)
		}
		if got := httpStatus(t, process.addresses.health, "/livez"); got != 200 {
			t.Fatalf("liveness under collector refusal = %d", got)
		}
		started := process.signal(t)
		process.assertCleanExit(t, started, harness)
	})

	t.Run("collector recovery exports without a Product restart", func(t *testing.T) {
		collector := &telemetryTraceCollector{
			started: make(chan struct{}, 1), accepted: make(chan struct{}, 1),
		}
		collector.reject.Store(true)
		endpoint := startTelemetryCollector(t, collector)
		process := startLifecycleProcess(t, binary, harness.RuntimeDatabaseURL(),
			telemetryEnvironment(endpoint)...,
		)
		assertGracefulGRPCResult(t, process.getProduct("CAR"), "CAR")
		select {
		case <-collector.started:
		case <-time.After(3 * time.Second):
			t.Fatal("collector received no initial export")
		}
		time.Sleep(2500 * time.Millisecond)
		collector.reject.Store(false)
		assertGracefulGRPCResult(t, process.getProduct("CAR"), "CAR")
		select {
		case <-collector.accepted:
		case <-time.After(4 * time.Second):
			t.Fatal("collector received no recovered export")
		}
		started := process.signal(t)
		process.assertCleanExit(t, started, harness)
		if strings.Contains(process.logs.String(), "private-collector-marker") {
			t.Fatal("exporter diagnostics exposed collector response text")
		}
		if !strings.Contains(process.logs.String(), "telemetry export failed") {
			t.Fatal("export failure was not reported safely")
		}
	})

	t.Run("SIGTERM bounds an in-flight export", func(t *testing.T) {
		release := make(chan struct{})
		defer close(release)
		collector := &telemetryTraceCollector{
			blocked:  release,
			started:  make(chan struct{}, 1),
			accepted: make(chan struct{}, 1),
		}
		endpoint := startTelemetryCollector(t, collector)
		process := startLifecycleProcess(t, binary, harness.RuntimeDatabaseURL(),
			telemetryEnvironment(endpoint)...,
		)
		assertGracefulGRPCResult(t, process.getProduct("CAR"), "CAR")
		select {
		case <-collector.started:
		case <-time.After(3 * time.Second):
			t.Fatal("collector received no in-flight export")
		}
		assertGracefulGRPCResult(t, process.getProduct("CAR"), "CAR")
		started := process.signal(t)
		process.assertCleanExit(t, started, harness)
		if !strings.Contains(process.logs.String(), "telemetry export failed") {
			t.Fatalf(
				"in-flight export loss was not reported safely: %s",
				process.logs.String(),
			)
		}
	})
}

func telemetryEnvironment(endpoint string) []string {
	return []string{
		"SERVICE_NAME=product-service",
		"OTEL_ENABLED=true",
		"OTEL_EXPORTER_OTLP_ENDPOINT=" + endpoint,
		"OTEL_EXPORTER_OTLP_TIMEOUT=2s",
		"DEPLOYMENT_ENVIRONMENT=test",
		"SERVICE_VERSION=test-telemetry",
	}
}
