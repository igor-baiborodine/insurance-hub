package service

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	kitconfig "github.com/igor-baiborodine/insurance-hub/libs/servicekit/config"
	"go.opentelemetry.io/otel/metric"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
	"go.opentelemetry.io/otel/propagation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
	grpcgo "google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	dashboardv1 "github.com/igor-baiborodine/insurance-hub/services/dashboard-service/gen/dashboard/v1"
	"github.com/igor-baiborodine/insurance-hub/services/dashboard-service/internal/application"
	"github.com/igor-baiborodine/insurance-hub/services/dashboard-service/internal/config"
	"github.com/igor-baiborodine/insurance-hub/services/dashboard-service/internal/domain"
	dashboardkafka "github.com/igor-baiborodine/insurance-hub/services/dashboard-service/internal/kafka"
	"github.com/igor-baiborodine/insurance-hub/services/dashboard-service/internal/telemetry"
)

func TestRun_TraversesRealGatewayRPCAndReadiness(t *testing.T) {
	settings := testSettings(t)
	search := &fakeSearch{}
	deps := productionDependencies()
	deps.newTelemetry = func(context.Context, string, config.Telemetry) (telemetryProvider, error) {
		return fakeTelemetry{}, nil
	}
	deps.newSearch = func(config.Elasticsearch, *time.Location) (salesReader, searchDependency, error) {
		return search, search, nil
	}

	var listenerMu sync.Mutex
	addresses := make([]string, 0, 3)
	deps.listen = func(network, _ string) (net.Listener, error) {
		listener, err := net.Listen(network, "127.0.0.1:0")
		if err == nil {
			listenerMu.Lock()
			addresses = append(addresses, listener.Addr().String())
			listenerMu.Unlock()
		}
		return listener, err
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	var finished atomic.Bool
	go func() {
		done <- run(ctx, settings, slog.New(slog.NewJSONHandler(io.Discard, nil)), deps)
	}()
	t.Cleanup(func() {
		cancel()
		if finished.Load() {
			return
		}
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Error("service did not stop")
		}
	})

	grpcAddress, httpAddress, healthAddress := waitForAddresses(t, &listenerMu, &addresses)
	waitForReady(t, healthAddress)

	response, err := http.Post(
		"http://"+httpAddress+"/dashboard/totalsales?ignored=value",
		"application/json",
		strings.NewReader(
			`{"productCode":"SAFE","saleDateFrom":"2019-01-01","saleDateTo":"2020-01-01"}`,
		),
	)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK || string(body) !=
		`{"total":{"policiesCount":5,"premiumAmount":5000.00},`+
			`"perProductTotal":{"SAFE":{"policiesCount":5,"premiumAmount":5000.00}}}` {
		t.Fatalf("HTTP response = (%d, %s)", response.StatusCode, body)
	}

	connection, err := grpcgo.NewClient(
		grpcAddress,
		grpcgo.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = connection.Close() }()
	rpcResponse, err := dashboardv1.NewDashboardServiceClient(connection).GetTotalSales(
		context.Background(),
		&dashboardv1.GetTotalSalesRequest{
			SaleDateFrom: "2019-01-01", SaleDateTo: "2020-01-01",
		},
	)
	if err != nil || rpcResponse.GetTotal().GetPoliciesCount() != 5 {
		t.Fatalf("RPC response = (%v, %v)", rpcResponse, err)
	}
	agentHTTPResponse, err := http.Post(
		"http://"+httpAddress+"/dashboard/agentssales",
		"application/json",
		strings.NewReader(
			`{"agentLogin":"safe-agent","productCode":"SAFE",`+
				`"saleDateFrom":"2019-01-01","saleDateTo":"2020-01-01"}`,
		),
	)
	if err != nil {
		t.Fatal(err)
	}
	agentHTTPBody, _ := io.ReadAll(agentHTTPResponse.Body)
	_ = agentHTTPResponse.Body.Close()
	if agentHTTPResponse.StatusCode != http.StatusOK || string(agentHTTPBody) !=
		`{"perAgentTotal":{"safe-agent":{"policiesCount":5,"premiumAmount":5000.0}}}` {
		t.Fatalf(
			"agent HTTP response = (%d, %s)",
			agentHTTPResponse.StatusCode,
			agentHTTPBody,
		)
	}
	agentRPCResponse, err := dashboardv1.NewDashboardServiceClient(connection).GetAgentSales(
		context.Background(),
		&dashboardv1.GetAgentSalesRequest{
			SaleDateFrom: "2019-01-01", SaleDateTo: "2020-01-01",
		},
	)
	if err != nil || agentRPCResponse.GetPerAgentTotal()["safe-agent"].GetPremiumAmount() !=
		"5000.0" {
		t.Fatalf("agent RPC response = (%v, %v)", agentRPCResponse, err)
	}
	trendHTTPResponse, err := http.Post(
		"http://"+httpAddress+"/dashboard/trends",
		"application/json",
		strings.NewReader(
			`{"productCode":"SAFE","saleDateFrom":"2019-01-01",`+
				`"saleDateTo":"2020-01-01","aggregationUnitCode":"MONTH"}`,
		),
	)
	if err != nil {
		t.Fatal(err)
	}
	trendHTTPBody, _ := io.ReadAll(trendHTTPResponse.Body)
	_ = trendHTTPResponse.Body.Close()
	if trendHTTPResponse.StatusCode != http.StatusOK || string(trendHTTPBody) !=
		`{"periodSales":[{"periodDate":[2020,1,1],`+
			`"period":"2020-01-01T00:00:00.000Z",`+
			`"policiesCount":5,"premiumAmount":5000.00}]}` {
		t.Fatalf(
			"trend HTTP response = (%d, %s)",
			trendHTTPResponse.StatusCode,
			trendHTTPBody,
		)
	}
	trendRPCResponse, err := dashboardv1.NewDashboardServiceClient(connection).GetSalesTrends(
		context.Background(),
		&dashboardv1.GetSalesTrendsRequest{
			SaleDateFrom: "2019-01-01", SaleDateTo: "2020-01-01",
			AggregationUnitCode: "MONTH",
		},
	)
	if err != nil || len(trendRPCResponse.GetPeriodSales()) != 1 ||
		trendRPCResponse.GetPeriodSales()[0].GetPremiumAmount() != "5000.00" {
		t.Fatalf("trend RPC response = (%v, %v)", trendRPCResponse, err)
	}
	for _, unit := range []string{"", "month", "QUARTER", " MONTH "} {
		invalidResponse, requestErr := http.Post(
			"http://"+httpAddress+"/dashboard/trends",
			"application/json",
			strings.NewReader(
				`{"saleDateFrom":"2019-01-01","saleDateTo":"2020-01-01",`+
					`"aggregationUnitCode":"`+unit+`"}`,
			),
		)
		if requestErr != nil {
			t.Fatal(requestErr)
		}
		invalidBody, _ := io.ReadAll(invalidResponse.Body)
		_ = invalidResponse.Body.Close()
		if invalidResponse.StatusCode != http.StatusBadRequest || string(invalidBody) !=
			`{"code":3,"message":"invalid request","details":[]}` {
			t.Fatalf(
				"invalid trend unit %q response = (%d, %s)",
				unit,
				invalidResponse.StatusCode,
				invalidBody,
			)
		}
	}
	if search.reads.Load() != 6 || search.checks.Load() < 2 {
		t.Errorf(
			"search activity = reads %d, checks %d",
			search.reads.Load(),
			search.checks.Load(),
		)
	}

	cancel()
	err = <-done
	finished.Store(true)
	if err != nil {
		t.Fatalf("run = %v", err)
	}
	if !search.closed.Load() {
		t.Error("Elasticsearch pool was not closed")
	}
}

func TestRun_FailsClosedWhenElasticsearchReadinessFails(t *testing.T) {
	settings := testSettings(t)
	search := &fakeSearch{checkErr: errors.New("private dependency failure")}
	deps := productionDependencies()
	deps.newTelemetry = func(context.Context, string, config.Telemetry) (telemetryProvider, error) {
		return fakeTelemetry{}, nil
	}
	deps.newSearch = func(config.Elasticsearch, *time.Location) (salesReader, searchDependency, error) {
		return search, search, nil
	}
	deps.listen = func(network, _ string) (net.Listener, error) {
		return net.Listen(network, "127.0.0.1:0")
	}

	err := run(context.Background(), settings, slog.Default(), deps)
	if err == nil || !strings.Contains(err.Error(), "dependency not ready") {
		t.Fatalf("run error = %v", err)
	}
	if !search.closed.Load() {
		t.Error("Elasticsearch pool was not closed after startup failure")
	}
}

func TestRun_DisabledWorkerAcquiresNoEventResources(t *testing.T) {
	settings := testSettings(t)
	search := &fakeSearch{checkErr: errors.New("stop after disabled composition")}
	deps := productionDependencies()
	deps.newTelemetry = func(context.Context, string, config.Telemetry) (telemetryProvider, error) {
		return fakeTelemetry{}, nil
	}
	deps.newSearch = func(config.Elasticsearch, *time.Location) (salesReader, searchDependency, error) {
		return search, search, nil
	}
	deps.newProjection = func(
		config.Elasticsearch,
		*telemetry.DependencyObserver,
	) (projectionRuntime, error) {
		t.Fatal("disabled mode constructed the writer client")
		return nil, nil
	}
	deps.newWorker = func(dashboardkafka.Settings, projectionRuntime, *slog.Logger) (eventWorker, error) {
		t.Fatal("disabled mode constructed the Kafka client")
		return nil, nil
	}
	deps.listen = func(network, _ string) (net.Listener, error) {
		return net.Listen(network, "127.0.0.1:0")
	}

	_ = run(context.Background(), settings, slog.Default(), deps)
}

func TestRun_EventWorkerGaugeUsesConfiguredMode(t *testing.T) {
	for _, test := range []struct {
		name    string
		enabled bool
		want    int64
	}{
		{name: "disabled", enabled: false, want: 0},
		{name: "enabled", enabled: true, want: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			reader := sdkmetric.NewManualReader()
			meterProvider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
			t.Cleanup(func() { _ = meterProvider.Shutdown(context.Background()) })
			provider := &workerMetricTelemetryProvider{meterProvider: meterProvider}
			settings := testSettings(t)
			settings.Events.Enabled = test.enabled
			search := &fakeSearch{}
			deps := productionDependencies()
			deps.newTelemetry = func(
				context.Context,
				string,
				config.Telemetry,
			) (telemetryProvider, error) {
				return provider, nil
			}
			deps.newSearch = func(
				config.Elasticsearch,
				*time.Location,
			) (salesReader, searchDependency, error) {
				return search, search, errors.New("stop after event observer")
			}
			if test.enabled {
				settings.Events.CAFile = writeTestCA(t)
				deps.newProjection = func(
					config.Elasticsearch,
					*telemetry.DependencyObserver,
				) (projectionRuntime, error) {
					return &fakeProjectionRuntime{}, nil
				}
				deps.newWorker = func(
					dashboardkafka.Settings,
					projectionRuntime,
					*slog.Logger,
				) (eventWorker, error) {
					return &fakeEventWorker{}, nil
				}
			}

			err := run(context.Background(), settings, slog.Default(), deps)
			if err == nil ||
				!strings.Contains(err.Error(), "stop after event observer") {
				t.Fatalf("run error = %v", err)
			}
			assertWorkerEnabledMetric(t, reader, test.want)
		})
	}
}

func TestRun_EnabledWorkerUsesExactSettingsAndBoundedLifecycle(t *testing.T) {
	settings := testSettings(t)
	caFile := t.TempDir() + "/ca.crt"
	if err := os.WriteFile(caFile, []byte("test CA"), 0o600); err != nil {
		t.Fatal(err)
	}
	settings.Events = config.Events{
		Enabled:         true,
		Brokers:         []string{"kafka.example:9093"},
		Group:           "dashboard-service-issue-290",
		RegisteredTopic: "policy-registered-issue-290",
		CAFile:          caFile,
		Writer: config.Elasticsearch{
			URL: "http://127.0.0.1:9200", Index: "policy_stats_issue_290",
			Username: "writer", Password: "private", Timeout: time.Second,
		},
	}
	search := &fakeSearch{}
	projection := &fakeProjectionRuntime{}
	worker := &fakeEventWorker{}
	deps := productionDependencies()
	deps.newTelemetry = func(context.Context, string, config.Telemetry) (telemetryProvider, error) {
		return fakeTelemetry{}, nil
	}
	deps.newSearch = func(config.Elasticsearch, *time.Location) (salesReader, searchDependency, error) {
		return search, search, nil
	}
	deps.newProjection = func(
		got config.Elasticsearch,
		observer *telemetry.DependencyObserver,
	) (projectionRuntime, error) {
		if got.Index != "policy_stats_issue_290" || got.Username != "writer" {
			t.Fatalf("projection settings = %+v", got)
		}
		if observer == nil {
			t.Fatal("projection dependency observer is nil")
		}
		return projection, nil
	}
	deps.newWorker = func(got dashboardkafka.Settings, _ projectionRuntime, _ *slog.Logger) (eventWorker, error) {
		if got.ClientID != "policy-registered-dashboard-listener" ||
			got.Group != "dashboard-service-issue-290" ||
			got.Topic != "policy-registered-issue-290" || got.EventType != "registered" ||
			got.Propagator == nil || got.Observe == nil || got.CommitFailure == nil ||
			string(got.CACert) != "test CA" {
			t.Fatalf("worker settings = %+v", got)
		}
		return worker, nil
	}
	var listenerMu sync.Mutex
	addresses := make([]string, 0, 3)
	deps.listen = func(network, _ string) (net.Listener, error) {
		listener, err := net.Listen(network, "127.0.0.1:0")
		if err == nil {
			listenerMu.Lock()
			addresses = append(addresses, listener.Addr().String())
			listenerMu.Unlock()
		}
		return listener, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- run(ctx, settings, slog.Default(), deps) }()
	_, _, healthAddress := waitForAddresses(t, &listenerMu, &addresses)
	waitForReady(t, healthAddress)
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if !worker.started.Load() || !worker.shutdown.Load() || !projection.closed.Load() ||
		projection.checks.Load() == 0 {
		t.Fatalf(
			"lifecycle = worker started:%t shutdown:%t projection checks:%d closed:%t",
			worker.started.Load(), worker.shutdown.Load(), projection.checks.Load(),
			projection.closed.Load(),
		)
	}
}

func TestRun_EnabledWorkerConstructionFailureCleansProjection(t *testing.T) {
	settings := testSettings(t)
	caFile := t.TempDir() + "/ca.crt"
	if err := os.WriteFile(caFile, []byte("test CA"), 0o600); err != nil {
		t.Fatal(err)
	}
	settings.Events = config.Events{
		Enabled:         true,
		Brokers:         []string{"kafka.example:9093"},
		Group:           "dashboard-service-issue-290",
		RegisteredTopic: "policy-registered-issue-290",
		CAFile:          caFile,
		Writer:          config.Elasticsearch{Index: "policy_stats_issue_290"},
	}
	projection := &fakeProjectionRuntime{}
	failure := errors.New("construct registration worker")
	deps := productionDependencies()
	deps.newTelemetry = func(context.Context, string, config.Telemetry) (telemetryProvider, error) {
		return fakeTelemetry{}, nil
	}
	deps.newProjection = func(
		config.Elasticsearch,
		*telemetry.DependencyObserver,
	) (projectionRuntime, error) {
		return projection, nil
	}
	deps.newWorker = func(dashboardkafka.Settings, projectionRuntime, *slog.Logger) (eventWorker, error) {
		return nil, failure
	}

	err := run(context.Background(), settings, slog.Default(), deps)
	if !errors.Is(err, failure) || !projection.closed.Load() {
		t.Fatalf("run error = %v, projection closed = %t", err, projection.closed.Load())
	}
}

func TestReadBoundedFile_RejectsOversizedCertificate(t *testing.T) {
	path := t.TempDir() + "/ca.pem"
	if err := os.WriteFile(path, []byte("12345"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readBoundedFile(path, 4); err == nil {
		t.Fatal("oversized certificate accepted")
	}
	data, err := readBoundedFile(path, 5)
	if err != nil || string(data) != "12345" {
		t.Fatalf("bounded read = (%q, %v)", data, err)
	}
}

type fakeSearch struct {
	reads    atomic.Int64
	checks   atomic.Int64
	closed   atomic.Bool
	checkErr error
}

func (search *fakeSearch) ReadTotalSales(
	ctx context.Context,
	query application.TotalSalesQuery,
) ([]domain.ProductSales, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	search.reads.Add(1)
	product := "SAFE"
	if query.Product.Present() {
		product = query.Product.Value()
	}
	value, _ := domain.NewSalesValue(5, domain.NewPremiumAmount(500000))
	return []domain.ProductSales{{ProductCode: product, Value: value}}, nil
}

func (search *fakeSearch) ReadAgentSales(
	ctx context.Context,
	_ application.AgentSalesQuery,
) ([]domain.AgentSales, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	search.reads.Add(1)
	amount, _ := domain.NewAgentPremiumAmount("5000.0")
	value, _ := domain.NewAgentSalesValue(5, amount)
	return []domain.AgentSales{{AgentLogin: "safe-agent", Value: value}}, nil
}

func (search *fakeSearch) ReadSalesTrends(
	ctx context.Context,
	_ application.SalesTrendsQuery,
) ([]domain.PeriodSales, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	search.reads.Add(1)
	date, _ := domain.ParseDate("2020-01-01")
	amount, _ := domain.NewTrendPremiumAmount("5000.00")
	value, _ := domain.NewTrendSalesValue(5, amount)
	return []domain.PeriodSales{{
		PeriodDate: date, Period: "2020-01-01T00:00:00.000Z", Value: value,
	}}, nil
}

func (search *fakeSearch) Check(ctx context.Context) error {
	search.checks.Add(1)
	if err := ctx.Err(); err != nil {
		return err
	}
	return search.checkErr
}

func (search *fakeSearch) CloseIdleConnections() { search.closed.Store(true) }

type fakeProjectionRuntime struct {
	checks atomic.Int64
	closed atomic.Bool
}

func (*fakeProjectionRuntime) ProjectPolicy(context.Context, application.PolicyEvent) error {
	return nil
}

func (projection *fakeProjectionRuntime) Check(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	projection.checks.Add(1)
	return nil
}

func (projection *fakeProjectionRuntime) CloseIdleConnections() {
	projection.closed.Store(true)
}

type fakeEventWorker struct {
	started  atomic.Bool
	shutdown atomic.Bool
}

func (*fakeEventWorker) Check(ctx context.Context) error { return ctx.Err() }

func (worker *fakeEventWorker) Run(ctx context.Context) error {
	worker.started.Store(true)
	<-ctx.Done()
	return nil
}

func (worker *fakeEventWorker) Shutdown(ctx context.Context) error {
	worker.shutdown.Store(true)
	return ctx.Err()
}

type fakeTelemetry struct{}

func (fakeTelemetry) MeterProvider() metric.MeterProvider  { return metricnoop.NewMeterProvider() }
func (fakeTelemetry) TracerProvider() trace.TracerProvider { return noop.NewTracerProvider() }
func (fakeTelemetry) HTTPPropagator() propagation.TextMapPropagator {
	return propagation.TraceContext{}
}

func (fakeTelemetry) GRPCPropagator() propagation.TextMapPropagator {
	return propagation.TraceContext{}
}
func (fakeTelemetry) Shutdown(context.Context) error { return nil }

type workerMetricTelemetryProvider struct {
	meterProvider *sdkmetric.MeterProvider
}

func (provider *workerMetricTelemetryProvider) MeterProvider() metric.MeterProvider {
	return provider.meterProvider
}

func (*workerMetricTelemetryProvider) TracerProvider() trace.TracerProvider {
	return noop.NewTracerProvider()
}

func (*workerMetricTelemetryProvider) HTTPPropagator() propagation.TextMapPropagator {
	return propagation.TraceContext{}
}

func (*workerMetricTelemetryProvider) GRPCPropagator() propagation.TextMapPropagator {
	return propagation.TraceContext{}
}

func (*workerMetricTelemetryProvider) Shutdown(context.Context) error { return nil }

func assertWorkerEnabledMetric(t *testing.T, reader *sdkmetric.ManualReader, want int64) {
	t.Helper()
	var metrics metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &metrics); err != nil {
		t.Fatal(err)
	}
	for _, scope := range metrics.ScopeMetrics {
		for _, measurement := range scope.Metrics {
			gauge, ok := measurement.Data.(metricdata.Gauge[int64])
			if !ok ||
				measurement.Name != "insurance_hub.dashboard.event.worker.enabled" {
				continue
			}
			if len(gauge.DataPoints) != 1 || gauge.DataPoints[0].Value != want {
				t.Fatalf(
					"event-worker enabled metrics = %v, want %d",
					gauge.DataPoints,
					want,
				)
			}
			attributes := gauge.DataPoints[0].Attributes.ToSlice()
			if len(attributes) != 1 ||
				attributes[0].Key != "insurance_hub.dashboard.event.type" ||
				attributes[0].Value.AsString() != "registered" {
				t.Fatalf("event-worker metric attributes = %v", attributes)
			}
			return
		}
	}
	t.Fatal("event-worker enabled metric was not collected")
}

func writeTestCA(t *testing.T) string {
	t.Helper()
	path := t.TempDir() + "/ca.crt"
	if err := os.WriteFile(path, []byte("test CA"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func testSettings(t *testing.T) config.Config {
	t.Helper()
	address := func(name string) config.Address {
		parsed, err := kitconfig.ParseAddress(name, "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		return parsed
	}
	return config.Config{
		Runtime: kitconfig.Runtime{
			RequestTimeout: time.Second, ProbeTimeout: time.Second,
			ShutdownTimeout: 3 * time.Second,
		},
		GRPC: kitconfig.DefaultGRPC(),
		HTTPServer: kitconfig.HTTPServer{
			ReadHeaderTimeout: time.Second, ReadTimeout: 2 * time.Second,
			WriteTimeout: 2 * time.Second, IdleTimeout: 2 * time.Second,
		},
		Telemetry:   kitconfig.Telemetry{ExporterTimeout: time.Second},
		ServiceName: "dashboard-service",
		GRPCAddress: address("GRPC_ADDR"), HTTPAddress: address("HTTP_ADDR"),
		HealthAddress: address("HEALTH_ADDR"), HTTPMaxHeaderBytes: 16384,
		HTTPMaxBodyBytes: 4096,
		Elasticsearch: config.Elasticsearch{
			URL: "http://127.0.0.1:9200", Index: "policy_stats", Timeout: time.Second,
		},
	}
}

func waitForAddresses(
	t *testing.T,
	mu *sync.Mutex,
	addresses *[]string,
) (string, string, string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		if len(*addresses) == 3 {
			result := append([]string(nil), (*addresses)...)
			mu.Unlock()
			return result[0], result[1], result[2]
		}
		mu.Unlock()
		time.Sleep(time.Millisecond)
	}
	t.Fatal("listeners were not created")
	return "", "", ""
}

func waitForReady(t *testing.T, address string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		response, err := http.Get("http://" + address + "/readyz")
		if err == nil {
			_ = response.Body.Close()
			if response.StatusCode == http.StatusOK {
				return
			}
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("service never became ready")
}
