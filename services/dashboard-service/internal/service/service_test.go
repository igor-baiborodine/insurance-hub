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
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
	grpcgo "google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	dashboardv1 "github.com/igor-baiborodine/insurance-hub/services/dashboard-service/gen/dashboard/v1"
	"github.com/igor-baiborodine/insurance-hub/services/dashboard-service/internal/application"
	"github.com/igor-baiborodine/insurance-hub/services/dashboard-service/internal/config"
	"github.com/igor-baiborodine/insurance-hub/services/dashboard-service/internal/domain"
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
