//go:build integration

package integrationtest

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	collectormetric "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	collectortrace "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	commonv1 "go.opentelemetry.io/proto/otlp/common/v1"
	metricv1 "go.opentelemetry.io/proto/otlp/metrics/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	productv1 "github.com/igor-baiborodine/insurance-hub/services/product-service/gen/product/v1"
)

type telemetryTraceCollector struct {
	collectortrace.UnimplementedTraceServiceServer
	reject   atomic.Bool
	blocked  <-chan struct{}
	started  chan struct{}
	accepted chan struct{}
	spans    chan *collectortrace.ExportTraceServiceRequest
	metrics  chan *collectormetric.ExportMetricsServiceRequest
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
		if collector.spans != nil {
			collector.spans <- request
		}
		select {
		case collector.accepted <- struct{}{}:
		default:
		}
	}
	return &collectortrace.ExportTraceServiceResponse{}, nil
}

type telemetryMetricCollector struct {
	collectormetric.UnimplementedMetricsServiceServer
	metrics chan *collectormetric.ExportMetricsServiceRequest
}

func (collector *telemetryMetricCollector) Export(
	_ context.Context,
	request *collectormetric.ExportMetricsServiceRequest,
) (*collectormetric.ExportMetricsServiceResponse, error) {
	if collector.metrics != nil {
		collector.metrics <- request
	}
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
	collectormetric.RegisterMetricsServiceServer(
		server,
		&telemetryMetricCollector{metrics: collector.metrics},
	)
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

	t.Run("real HTTP and gRPC reads export one request observation each", func(t *testing.T) {
		collector := &telemetryTraceCollector{
			started: make(chan struct{}, 4), accepted: make(chan struct{}, 4),
			spans:   make(chan *collectortrace.ExportTraceServiceRequest, 4),
			metrics: make(chan *collectormetric.ExportMetricsServiceRequest, 16),
		}
		endpoint := startTelemetryCollector(t, collector)
		process := startLifecycleProcess(t, binary, harness.RuntimeDatabaseURL(),
			telemetryEnvironment(endpoint)...,
		)
		if got := <-process.listProducts(); got.err != nil || got.status != 200 {
			t.Fatalf("HTTP list: %+v", got)
		}
		if got := httpStatus(t, process.addresses.http, "/products/CAR"); got != 200 {
			t.Fatalf("HTTP get status = %d", got)
		}
		if _, err := process.grpc.ListProducts(
			ctx,
			&productv1.ListProductsRequest{},
		); err != nil {
			t.Fatalf("gRPC list: %v", err)
		}
		assertGracefulGRPCResult(t, process.getProduct("CAR"), "CAR")
		if got := httpStatus(t, process.addresses.health, "/readyz"); got != 200 {
			t.Fatalf("readiness = %d", got)
		}
		started := process.signal(t)
		process.assertCleanExit(t, started, harness)
		spanNames := map[string]int{}
		for len(collector.spans) != 0 {
			request := <-collector.spans
			for _, resource := range request.ResourceSpans {
				for _, scope := range resource.ScopeSpans {
					for _, span := range scope.Spans {
						spanNames[span.Name]++
					}
				}
			}
		}
		for _, name := range []string{
			"GET /products", "GET /products/{productCode}",
			"product.v1.ProductService/ListProducts", "product.v1.ProductService/GetProduct",
			"product.list", "product.get",
		} {
			want := 1
			if name == "product.list" || name == "product.get" {
				want = 2
			}
			if spanNames[name] != want {
				t.Fatalf(
					"span %q count = %d, want %d; all=%v",
					name,
					spanNames[name],
					want,
					spanNames,
				)
			}
		}
		var metricExports []*collectormetric.ExportMetricsServiceRequest
		for len(collector.metrics) != 0 {
			metricExports = append(metricExports, <-collector.metrics)
		}
		if len(metricExports) == 0 {
			t.Fatal("no request metric export received")
		}
		// Replay every export so the assertion detects cumulative double-counting.
		metricExports = append(metricExports, metricExports...)
		metricCounts := latestCumulativeRequestMetrics(t, metricExports)
		if len(metricCounts) != 8 {
			t.Fatalf(
				"request metric series = %d, want 8: %v",
				len(metricCounts),
				metricCounts,
			)
		}
		for _, name := range []string{
			"insurance_hub.product.requests", "insurance_hub.product.request.duration",
		} {
			for _, transport := range []string{"http", "grpc"} {
				for _, operation := range []string{"list", "get"} {
					key := requestMetricSeries{name, transport, operation}
					if metricCounts[key] != 1 {
						t.Fatalf("request metric %v = %d, want 1: %v",
							key, metricCounts[key], metricCounts)
					}
				}
			}
		}
		if strings.Count(process.logs.String(), "Product request completed") != 4 {
			t.Fatalf("completion log count != four: %s", process.logs.String())
		}
	})

	t.Run("disabled mode makes no exporter connection", func(t *testing.T) {
		for _, level := range []string{"info", "warn", "error"} {
			t.Run(level, func(t *testing.T) {
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
				process := startLifecycleProcess(
					t,
					binary,
					harness.RuntimeDatabaseURL(),
					"OTEL_ENABLED=false",
					"OTEL_EXPORTER_OTLP_ENDPOINT=http://"+listener.Addr().
						String(),
					"LOG_LEVEL="+level,
				)
				assertGracefulGRPCResult(t, process.getProduct("CAR"), "CAR")
				started := process.signal(t)
				process.assertCleanExit(t, started, harness)
				completionCount := 0
				for _, line := range strings.Split(strings.TrimSpace(process.logs.String()), "\n") {
					var record map[string]any
					if err := json.Unmarshal(
						[]byte(line),
						&record,
					); err != nil {
						t.Fatalf("decode Product log: %v", err)
					}
					if record["msg"] != "Product request completed" {
						continue
					}
					completionCount++
					if record["level"] != "INFO" ||
						record["outcome"] != "success" ||
						record["rpc_status_code"] != "OK" {
						t.Fatalf(
							"disabled mode completion record: %v",
							record,
						)
					}
				}
				if completionCount != 1 {
					t.Fatalf(
						"disabled mode completion log count = %d, want one: %s",
						completionCount,
						process.logs.String(),
					)
				}
				select {
				case <-accepted:
					t.Fatal("disabled Product connected to telemetry endpoint")
				default:
				}
			})
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

type requestMetricSeries struct {
	name      string
	transport string
	operation string
}

type cumulativeRequestPoint struct {
	time  uint64
	count uint64
}

func latestCumulativeRequestMetrics(
	t *testing.T,
	exports []*collectormetric.ExportMetricsServiceRequest,
) map[requestMetricSeries]uint64 {
	t.Helper()
	latest := map[requestMetricSeries]cumulativeRequestPoint{}
	for _, export := range exports {
		for _, resource := range export.ResourceMetrics {
			for _, scope := range resource.ScopeMetrics {
				for _, metric := range scope.Metrics {
					switch metric.Name {
					case "insurance_hub.product.requests":
						sum := metric.GetSum()
						if sum == nil || !sum.IsMonotonic ||
							sum.AggregationTemporality != metricv1.AggregationTemporality_AGGREGATION_TEMPORALITY_CUMULATIVE {
							t.Fatalf(
								"request counter is not cumulative and monotonic: %v",
								metric,
							)
						}
						for _, point := range sum.DataPoints {
							if point.GetAsInt() < 0 {
								t.Fatalf(
									"negative request counter: %v",
									point,
								)
							}
							recordLatestRequestPoint(
								t,
								latest,
								metric.Name,
								point.Attributes,
								point.TimeUnixNano,
								uint64(point.GetAsInt()),
							)
						}
					case "insurance_hub.product.request.duration":
						histogram := metric.GetHistogram()
						if histogram == nil ||
							histogram.AggregationTemporality != metricv1.AggregationTemporality_AGGREGATION_TEMPORALITY_CUMULATIVE {
							t.Fatalf(
								"request duration is not cumulative: %v",
								metric,
							)
						}
						for _, point := range histogram.DataPoints {
							recordLatestRequestPoint(
								t,
								latest,
								metric.Name,
								point.Attributes,
								point.TimeUnixNano,
								point.Count,
							)
						}
					}
				}
			}
		}
	}
	counts := make(map[requestMetricSeries]uint64, len(latest))
	for key, point := range latest {
		counts[key] = point.count
	}
	return counts
}

func recordLatestRequestPoint(
	t *testing.T,
	latest map[requestMetricSeries]cumulativeRequestPoint,
	name string,
	attrs []*commonv1.KeyValue,
	timestamp, count uint64,
) {
	t.Helper()
	if timestamp == 0 {
		t.Fatal("request metric point has no timestamp")
	}
	key := requestMetricSeriesFromAttributes(t, name, attrs)
	if previous, exists := latest[key]; exists {
		if timestamp < previous.time {
			return
		}
		if timestamp == previous.time && count != previous.count {
			t.Fatalf("conflicting cumulative points for %v at %d", key, timestamp)
		}
	}
	latest[key] = cumulativeRequestPoint{time: timestamp, count: count}
}

func requestMetricSeriesFromAttributes(
	t *testing.T,
	name string,
	attrs []*commonv1.KeyValue,
) requestMetricSeries {
	t.Helper()
	if len(attrs) != 4 {
		t.Fatalf("request metric %s has %d attributes, want 4: %v", name, len(attrs), attrs)
	}
	key := requestMetricSeries{name: name}
	var outcome, statusTransport string
	for _, attr := range attrs {
		switch attr.Key {
		case "insurance_hub.product.operation":
			key.operation = attr.Value.GetStringValue()
		case "insurance_hub.product.transport":
			key.transport = attr.Value.GetStringValue()
		case "insurance_hub.product.outcome":
			outcome = attr.Value.GetStringValue()
		case "http.response.status_code":
			if attr.Value.GetIntValue() != 200 {
				t.Fatalf("unexpected HTTP request metric status: %v", attr)
			}
			statusTransport = "http"
		case "rpc.response.status_code":
			if attr.Value.GetStringValue() != "OK" {
				t.Fatalf("unexpected gRPC request metric status: %v", attr)
			}
			statusTransport = "grpc"
		default:
			t.Fatalf("unapproved request metric attribute: %v", attr)
		}
	}
	if (key.operation != "list" && key.operation != "get") ||
		(key.transport != "http" && key.transport != "grpc") ||
		outcome != "success" || statusTransport != key.transport {
		t.Fatalf("unexpected request metric series %v outcome=%s status=%s", key,
			outcome, statusTransport)
	}
	return key
}
