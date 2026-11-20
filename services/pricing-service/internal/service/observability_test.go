package service

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/propagation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	"github.com/igor-baiborodine/insurance-hub/services/pricing-service/internal/config"
)

const (
	callerTraceID = "4bf92f3577b34da6a3ce929d0e0e4736"
	callerSpanID  = "00f067aa0ba902b7"
	privateMarker = "PRIVATE-MARKER-415"
)

type recordingTelemetry struct {
	metrics *sdkmetric.ManualReader
	spans   *tracetest.SpanRecorder
	meter   *sdkmetric.MeterProvider
	tracer  *sdktrace.TracerProvider
}

func newRecordingTelemetry(t *testing.T) *recordingTelemetry {
	t.Helper()
	provider := &recordingTelemetry{
		metrics: sdkmetric.NewManualReader(),
		spans:   tracetest.NewSpanRecorder(),
	}
	provider.meter = sdkmetric.NewMeterProvider(sdkmetric.WithReader(provider.metrics))
	provider.tracer = sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(provider.spans))
	t.Cleanup(func() {
		_ = provider.meter.Shutdown(context.Background())
		_ = provider.tracer.Shutdown(context.Background())
	})
	return provider
}

func (provider *recordingTelemetry) MeterProvider() metric.MeterProvider { return provider.meter }

func (provider *recordingTelemetry) TracerProvider() trace.TracerProvider {
	return provider.tracer
}

func (*recordingTelemetry) HTTPPropagator() propagation.TextMapPropagator {
	return propagation.TraceContext{}
}

func (*recordingTelemetry) GRPCPropagator() propagation.TextMapPropagator {
	return propagation.TraceContext{}
}

// Shutdown leaves the recorders readable after the service stops.
func (*recordingTelemetry) Shutdown(context.Context) error { return nil }

func (provider *recordingTelemetry) collect(t *testing.T) map[string][]attribute.Set {
	t.Helper()
	var collected metricdata.ResourceMetrics
	if err := provider.metrics.Collect(context.Background(), &collected); err != nil {
		t.Fatal(err)
	}
	points := map[string][]attribute.Set{}
	for _, scope := range collected.ScopeMetrics {
		for _, measurement := range scope.Metrics {
			switch data := measurement.Data.(type) {
			case metricdata.Sum[int64]:
				for _, point := range data.DataPoints {
					points[measurement.Name] = append(
						points[measurement.Name],
						point.Attributes,
					)
				}
			case metricdata.Histogram[float64]:
				for _, point := range data.DataPoints {
					points[measurement.Name] = append(
						points[measurement.Name],
						point.Attributes,
					)
				}
			}
		}
	}
	return points
}

func TestRun_UnavailableTariffReadKeepsCallerTraceAndBoundedSignals(t *testing.T) {
	// given
	t.Setenv("PRICING_DATABASE_URL", "postgresql://go_pricing_reader:synthetic@127.0.0.1:1/"+
		"pricing?sslmode=verify-full&sslrootcert=%2Fnonexistent%2Fca.pem")
	loaded, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	settings := testConfig(t)
	settings.Database = loaded.Database
	poolConfig, err := pgxpool.ParseConfig(
		"postgres://go_pricing_reader@127.0.0.1:1/pricing?sslmode=disable",
	)
	if err != nil {
		t.Fatal(err)
	}
	poolConfig.MinConns = 0
	provider := newRecordingTelemetry(t)
	deps := productionDependencies()
	deps.newTelemetry = func(context.Context, string, config.Telemetry) (telemetryProvider, error) {
		return provider, nil
	}
	deps.openPool = func(ctx context.Context, _ config.Database) (*pgxpool.Pool, error) {
		return pgxpool.NewWithConfig(ctx, poolConfig)
	}
	deps.checkDatabase = func(context.Context, *pgxpool.Pool, time.Duration, time.Duration) error {
		return nil
	}
	addresses := make(chan string, 3)
	deps.listen = recordingListen(addresses)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runResult := make(chan error, 1)
	go func() { runResult <- run(ctx, settings, discardLogger(), deps) }()
	_ = receiveString(t, addresses)
	httpAddress := receiveString(t, addresses)
	healthAddress := receiveString(t, addresses)
	client := &http.Client{Timeout: 2 * time.Second}
	waitForHTTPStatus(t, client, "http://"+healthAddress+"/readyz", http.StatusOK)
	body := strings.Replace(triHTTPBody, `"EUR"`, `"`+privateMarker+`"`, 1)
	request, err := http.NewRequest(
		http.MethodPost,
		"http://"+httpAddress+"/pricing/calculate",
		strings.NewReader(body),
	)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("traceparent", "00-"+callerTraceID+"-"+callerSpanID+"-01")

	// when
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	assertResponse(
		t,
		response,
		http.StatusServiceUnavailable,
		errorBody(14, "service unavailable"),
	)
	cancel()
	runErr := receiveError(t, runResult, "service did not stop")

	// then
	if runErr != nil {
		t.Fatalf("run returned %v", runErr)
	}
	spans := provider.spans.Ended()
	var httpServer, rpcClient, rpcServer, acquire sdktrace.ReadOnlySpan
	for _, span := range spans {
		if span.SpanContext().TraceID().String() != callerTraceID {
			t.Errorf("span %s left the caller trace", span.Name())
		}
		for _, attr := range span.Attributes() {
			if strings.Contains(attr.Value.String(), privateMarker) {
				t.Errorf("unsafe attribute on %s: %v", span.Name(), attr)
			}
		}
		switch {
		case span.Name() == "pricing.db.acquire":
			acquire = span
		case span.SpanKind() == trace.SpanKindClient:
			rpcClient = span
		case span.SpanKind() == trace.SpanKindServer &&
			span.Parent().SpanID().String() == callerSpanID:
			httpServer = span
		case span.SpanKind() == trace.SpanKindServer:
			rpcServer = span
		}
	}
	if len(spans) != 4 || httpServer == nil || rpcClient == nil || rpcServer == nil ||
		acquire == nil {
		t.Fatalf("request spans = %v", spans)
	}
	if rpcClient.Parent().SpanID() != httpServer.SpanContext().SpanID() ||
		rpcServer.Parent().SpanID() != rpcClient.SpanContext().SpanID() ||
		acquire.Parent().SpanID() != rpcServer.SpanContext().SpanID() {
		t.Fatal("broken span chain: caller -> HTTP -> RPC client -> RPC server -> acquire")
	}
	acquireAttrs := attribute.NewSet(acquire.Attributes()...)
	stage, _ := acquireAttrs.Value("insurance_hub.pricing.database.stage")
	if acquire.Status().Code != codes.Error || stage.AsString() != "acquire" {
		t.Errorf("acquire span = %v %v", acquire.Status(), acquire.Attributes())
	}
	points := provider.collect(t)
	outcomes := map[string]bool{}
	for _, set := range points["insurance_hub.pricing.requests"] {
		transport, _ := set.Value("insurance_hub.pricing.transport")
		outcome, _ := set.Value("insurance_hub.pricing.outcome")
		outcomes[transport.AsString()+"/"+outcome.AsString()] = true
	}
	if len(outcomes) != 2 || !outcomes["http/internal_error"] ||
		!outcomes["grpc/internal_error"] {
		t.Errorf("request outcomes = %v", outcomes)
	}
	operations := points["insurance_hub.pricing.database.operations"]
	if len(operations) != 1 {
		t.Fatalf("database operations = %v", operations)
	}
	outcome, _ := operations[0].Value("insurance_hub.pricing.database.outcome")
	if outcome.AsString() != "storage_unavailable" {
		t.Errorf("database outcome = %s", outcome.AsString())
	}
	if _, ok := points["db.client.connection.count"]; ok {
		t.Error("pool metrics continued after shutdown")
	}
	for name, sets := range points {
		for _, set := range sets {
			for _, attr := range set.ToSlice() {
				if strings.Contains(attr.Value.String(), privateMarker) {
					t.Errorf("unsafe %s label: %v", name, attr)
				}
			}
		}
	}
}
