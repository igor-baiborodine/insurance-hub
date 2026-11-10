package service

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.opentelemetry.io/otel/metric"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	productv1 "github.com/igor-baiborodine/insurance-hub/services/product-service/gen/product/v1"
	"github.com/igor-baiborodine/insurance-hub/services/product-service/internal/application"
	"github.com/igor-baiborodine/insurance-hub/services/product-service/internal/config"
	"github.com/igor-baiborodine/insurance-hub/services/product-service/internal/domain"
	"github.com/igor-baiborodine/insurance-hub/services/product-service/internal/postgres"
)

func TestRun_TraversesRealGatewayRPCAndReadiness(t *testing.T) {
	settings := loadTestSettings(t)
	deps := productionDependencies()
	deps.newTelemetry = func(context.Context, string, config.Telemetry) (telemetryProvider, error) {
		return fakeTelemetry{}, nil
	}
	deps.openPool = func(context.Context, config.Database) (*pgxpool.Pool, error) { return nil, nil }
	deps.checkDatabase = func(context.Context, *pgxpool.Pool) error { return nil }
	deps.newDatabaseObserver = func(*pgxpool.Pool, trace.TracerProvider, metric.MeterProvider) (*postgres.Observer, error) {
		return nil, nil
	}
	deps.newReader = func(*pgxpool.Pool, time.Duration, time.Duration, *postgres.Observer) (application.ProductReader, error) {
		return fakeProductReader{}, nil
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
	go func() {
		done <- run(ctx, settings, slog.New(slog.NewTextHandler(io.Discard, nil)), deps)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("run service: %v", err)
			}
		case <-time.After(3 * time.Second):
			t.Error("service did not stop")
		}
	})

	grpcAddress, httpAddress, healthAddress := waitForAddresses(t, &listenerMu, &addresses)
	waitForStatus(t, "http://"+healthAddress+"/readyz", http.StatusOK)
	response, err := http.Get("http://" + httpAddress + "/products/ONLY")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK || string(body) !=
		`{"code":"ONLY","name":"Only product","maxNumberOfInsured":0}` {
		t.Fatalf("gateway response = (%d, %s)", response.StatusCode, body)
	}
	connection, err := grpc.NewClient(
		grpcAddress,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = connection.Close() }()
	rpcResponse, err := productv1.NewProductServiceClient(connection).ListProducts(
		context.Background(), &productv1.ListProductsRequest{},
	)
	if err != nil || len(rpcResponse.GetProducts()) != 1 {
		t.Fatalf("RPC response = (%v, %v)", rpcResponse, err)
	}
}

func TestRun_RejectsInvalidProcessDependencies(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	if err := Run(context.Background(), config.Config{}, nil); err == nil {
		t.Error("Run accepted a nil logger")
	}
	if err := Run(context.Background(), config.Config{}, logger); err == nil {
		t.Error("Run accepted missing startup and shutdown budgets")
	}
}

func TestInternalDialTarget_UsesLoopbackForWildcardListeners(t *testing.T) {
	target, err := internalDialTarget(fakeAddress("0.0.0.0:9090"))
	if err != nil || target != "127.0.0.1:9090" {
		t.Fatalf("target = %q, error = %v", target, err)
	}
}

func TestHandlerTracker_WithdrawsAdmissionAndDrainsAcceptedRequests(t *testing.T) {
	release := make(chan struct{})
	started := make(chan struct{})
	tracker := newHandlerTracker(
		http.HandlerFunc(
			func(http.ResponseWriter, *http.Request) { close(started); <-release },
		),
		http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			writer.WriteHeader(http.StatusServiceUnavailable)
		}),
	)
	done := make(chan struct{})
	go func() {
		tracker.ServeHTTP(
			httptest.NewRecorder(),
			httptest.NewRequest(http.MethodGet, "/products", nil),
		)
		close(done)
	}()
	<-started
	tracker.beginDrain()
	unavailable := httptest.NewRecorder()
	tracker.ServeHTTP(unavailable, httptest.NewRequest(http.MethodGet, "/products", nil))
	if unavailable.Code != http.StatusServiceUnavailable {
		t.Fatalf("draining status = %d", unavailable.Code)
	}
	close(release)
	waitCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if !tracker.wait(waitCtx) {
		t.Fatal("accepted request did not drain")
	}
	<-done
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

type fakeProductReader struct{}

func (fakeProductReader) ListProducts(context.Context) ([]domain.Product, error) {
	return []domain.Product{{Code: "ONLY", Name: "Only product"}}, nil
}

func (fakeProductReader) GetProduct(_ context.Context, code string) (domain.Product, bool, error) {
	if code != "ONLY" {
		return domain.Product{}, false, nil
	}
	return domain.Product{Code: "ONLY", Name: "Only product"}, true, nil
}

type fakeAddress string

func (address fakeAddress) Network() string { return "tcp" }
func (address fakeAddress) String() string  { return string(address) }

func waitForAddresses(t *testing.T, mu *sync.Mutex, addresses *[]string) (string, string, string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		if len(*addresses) == 3 {
			values := append([]string(nil), (*addresses)...)
			mu.Unlock()
			return values[0], values[1], values[2]
		}
		mu.Unlock()
		time.Sleep(time.Millisecond)
	}
	t.Fatal("service listeners were not created")
	return "", "", ""
}

func waitForStatus(t *testing.T, endpoint string, want int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		response, err := http.Get(endpoint)
		if err == nil {
			_ = response.Body.Close()
			if response.StatusCode == want {
				return
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("%s did not return %d", endpoint, want)
}

func loadTestSettings(t *testing.T) config.Config {
	t.Helper()
	t.Setenv("SERVICE_NAME", "product-service")
	t.Setenv(
		"PRODUCT_DATABASE_URL",
		"postgresql://unused:unused@127.0.0.1:1/product?sslmode=disable",
	)
	t.Setenv("DB_MAX_CONNS", "1")
	t.Setenv("DB_CONNECT_TIMEOUT", "500ms")
	t.Setenv("DB_ACQUIRE_TIMEOUT", "500ms")
	t.Setenv("DB_QUERY_TIMEOUT", "500ms")
	t.Setenv("HTTP_ADDR", "127.0.0.1:18081")
	t.Setenv("GRPC_ADDR", "127.0.0.1:19090")
	t.Setenv("HEALTH_ADDR", "127.0.0.1:18080")
	t.Setenv("STARTUP_TIMEOUT", "2s")
	t.Setenv("PROBE_TIMEOUT", "500ms")
	t.Setenv("SHUTDOWN_TIMEOUT", "2s")
	t.Setenv("OTEL_ENABLED", "false")
	t.Setenv("OTEL_EXPORTER_OTLP_TIMEOUT", "500ms")
	settings, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	return settings
}
