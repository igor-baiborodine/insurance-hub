package service

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	kitconfig "github.com/igor-baiborodine/insurance-hub/libs/servicekit/config"
	"github.com/igor-baiborodine/insurance-hub/libs/servicekit/health"
	"github.com/jackc/pgx/v5"
	"go.opentelemetry.io/otel/metric"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
	"go.opentelemetry.io/otel/propagation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"

	paymentv1 "github.com/igor-baiborodine/insurance-hub/services/payment-service/gen/payment/v1"
	"github.com/igor-baiborodine/insurance-hub/services/payment-service/internal/application"
	"github.com/igor-baiborodine/insurance-hub/services/payment-service/internal/config"
	"github.com/igor-baiborodine/insurance-hub/services/payment-service/internal/gateway"
	transport "github.com/igor-baiborodine/insurance-hub/services/payment-service/internal/grpc"
	paymentkafka "github.com/igor-baiborodine/insurance-hub/services/payment-service/internal/kafka"
	"github.com/igor-baiborodine/insurance-hub/services/payment-service/internal/postgres"
)

func TestRun_ProductionCompositionServesBothTransportsAndDrainsHTTPFirst(t *testing.T) {
	// given
	settings := testConfig(t)
	deps := testDependencies()
	addresses := make(chan string, 3)
	deps.listen = func(network, address string) (net.Listener, error) {
		listener, err := net.Listen(network, address)
		if err == nil {
			addresses <- listener.Addr().String()
		}
		return listener, err
	}
	handlerStarted := make(chan struct{})
	releaseHandler := make(chan struct{})
	metadataSeen := make(chan metadata.MD, 4)
	reads := &fakeAccountReads{
		accounts: []application.Account{},
		balance: application.AccountBalance{
			Account: application.Account{
				PolicyAccountNumber: "drain-account",
				PolicyNumber:        "policy-1",
				Created:             time.UnixMilli(1704067200000),
				Updated:             time.UnixMilli(1704153600000),
			},
			Balance: "12.34",
		},
		metadataSeen:   metadataSeen,
		handlerStarted: handlerStarted,
		releaseHandler: releaseHandler,
	}
	deps.newAccountReads = func(databasePool) (transport.AccountReads, error) { return reads, nil }
	ctx, cancel := context.WithCancel(context.Background())
	runResult := make(chan error, 1)
	go func() { runResult <- run(ctx, settings, discardLogger(), deps) }()
	grpcAddress := receiveString(t, addresses)
	httpAddress := receiveString(t, addresses)
	healthAddress := receiveString(t, addresses)
	client := &http.Client{Timeout: time.Second}
	waitForHTTPStatus(t, client, "http://"+healthAddress+"/readyz", http.StatusOK)

	// when
	request, err := http.NewRequest(
		http.MethodGet,
		"http://"+httpAddress+"/payment/accounts",
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer private-token")
	request.Header.Set("Grpc-Metadata-Principal", "forged-principal")
	request.Header.Set("Forwarded", "for=203.0.113.44")
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	// then
	assertResponse(t, response, http.StatusOK, `[]`)
	assertNoIdentityMetadata(t, <-metadataSeen)
	assertOversizedHeaderRejected(t, httpAddress, settings.HTTPMaxHeaderBytes+5000)

	// when
	connection, err := grpc.NewClient(
		grpcAddress,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = connection.Close() }()
	direct, err := paymentv1.NewPaymentServiceClient(connection).ListAccounts(
		context.Background(),
		&paymentv1.ListAccountsRequest{},
	)
	// then
	if err != nil || len(direct.GetAccounts()) != 0 {
		t.Fatalf("direct account list = (%v, %v)", direct, err)
	}
	assertNoIdentityMetadata(t, <-metadataSeen)

	// when
	drainResult := make(chan *http.Response, 1)
	drainError := make(chan error, 1)
	go func() {
		req, requestErr := http.NewRequest(
			http.MethodGet,
			"http://"+httpAddress+"/payment/accounts/drain-account",
			nil,
		)
		if requestErr != nil {
			drainError <- requestErr
			return
		}
		result, requestErr := client.Do(req)
		if requestErr != nil {
			drainError <- requestErr
			return
		}
		drainResult <- result
	}()
	receiveSignal(t, handlerStarted, "gateway request did not reach RPC")
	// then
	cancel()
	waitForHTTPStatus(
		t,
		client,
		"http://"+healthAddress+"/readyz",
		http.StatusServiceUnavailable,
	)
	select {
	case response := <-drainResult:
		_ = response.Body.Close()
		t.Fatal("accepted HTTP request completed before release")
	case err := <-drainError:
		t.Fatalf("accepted HTTP request failed during drain: %v", err)
	default:
	}
	close(releaseHandler)
	select {
	case response := <-drainResult:
		assertResponse(
			t,
			response,
			http.StatusOK,
			`{"policyAccountNumber":"drain-account","policyNumber":"policy-1","balance":12.34,"created":1704067200000,"updated":1704153600000}`,
		)
	case err := <-drainError:
		t.Fatalf("drained HTTP request: %v", err)
	case <-time.After(time.Second):
		t.Fatal("accepted HTTP request did not drain")
	}
	if err := receiveError(t, runResult, "service did not stop"); err != nil {
		t.Fatalf("run service: %v", err)
	}
}

func TestRun_ProductionCompositionJoinsDependenciesToReadinessButNotLiveness(t *testing.T) {
	// given
	settings := testConfig(t)
	pool := &fakePool{}
	var databaseAvailable, objectStoreAvailable atomic.Bool
	databaseAvailable.Store(true)
	objectStoreAvailable.Store(true)
	objectStore := &fakeManagedDependency{
		ready:   &objectStoreAvailable,
		failure: errors.New("object storage unavailable"),
	}
	worker := &fakeEventWorker{}
	deps := testDependencies()
	deps.openPool = func(context.Context, config.Database) (databasePool, error) {
		return pool, nil
	}
	deps.checkDatabase = func(_ context.Context, database postgres.QueryRower, timeout time.Duration) error {
		if database != pool || timeout != settings.Database.QueryTimeout ||
			!databaseAvailable.Load() {
			return errors.New("database unavailable")
		}
		return nil
	}
	deps.newObjectStore = func(config.ObjectStore) (managedDependency, error) {
		return objectStore, nil
	}
	var workerSettings paymentkafka.Settings
	deps.newWorker = func(settings paymentkafka.Settings) (eventWorker, error) {
		workerSettings = settings
		return worker, nil
	}
	addresses := make(chan string, 3)
	deps.listen = func(network, address string) (net.Listener, error) {
		listener, err := net.Listen(network, address)
		if err == nil {
			addresses <- listener.Addr().String()
		}
		return listener, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	runResult := make(chan error, 1)
	go func() { runResult <- run(ctx, settings, discardLogger(), deps) }()
	_, _ = receiveString(t, addresses), receiveString(t, addresses)
	health := "http://" + receiveString(t, addresses)
	client := &http.Client{Timeout: time.Second}
	waitForHTTPStatus(t, client, health+"/readyz", http.StatusOK)
	if !worker.preflighted.Load() || workerSettings.ClientID != "payment-service" ||
		workerSettings.Topic != "policy-registered" ||
		strings.Join(workerSettings.Brokers, ",") != "kafka.example:9093" ||
		string(workerSettings.CACert) != "synthetic CA" {
		t.Fatalf("Kafka worker composed with unexpected settings or no preflight")
	}

	for _, dependency := range []*atomic.Bool{&databaseAvailable, &objectStoreAvailable} {
		// when
		dependency.Store(false)

		// then
		waitForHTTPStatus(t, client, health+"/readyz", http.StatusServiceUnavailable)
		waitForHTTPStatus(t, client, health+"/livez", http.StatusOK)
		dependency.Store(true)
		waitForHTTPStatus(t, client, health+"/readyz", http.StatusOK)
	}
	cancel()
	if err := receiveError(t, runResult, "service did not stop"); err != nil {
		t.Fatalf("run service: %v", err)
	}
	if !pool.closed.Load() || !objectStore.closed.Load() {
		t.Fatal("database pool or object storage was not closed during shutdown")
	}
}

func TestRun_ProductionCompositionRefusesUnverifiedDependenciesAndReleasesEarlierOnes(
	t *testing.T,
) {
	failure := errors.New("synthetic dependency failure")
	for _, test := range []struct {
		name             string
		openErr          error
		checkErr         error
		objectStoreErr   error
		objectCheckErr   error
		missingKafkaCA   bool
		preflightErr     error
		want             string
		wantListened     bool
		wantPoolClosed   bool
		wantObjectClosed bool
	}{
		{name: "PostgreSQL unreachable", openErr: failure, want: "open PostgreSQL"},
		{
			name: "PostgreSQL check fails", checkErr: failure,
			want: "verify PostgreSQL", wantPoolClosed: true,
		},
		{
			name: "MinIO client fails", objectStoreErr: failure,
			want: "initialize MinIO", wantPoolClosed: true,
		},
		{
			name: "MinIO check fails", objectCheckErr: failure,
			want: "verify MinIO", wantPoolClosed: true, wantObjectClosed: true,
		},
		{
			name: "Kafka CA unreadable", missingKafkaCA: true,
			want: "configure Kafka", wantPoolClosed: true, wantObjectClosed: true,
		},
		{
			name: "Kafka metadata fails", preflightErr: failure, want: "verify Kafka",
			wantListened: true, wantPoolClosed: true, wantObjectClosed: true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			// given
			settings := testConfig(t)
			if test.missingKafkaCA {
				settings.Kafka.CAFile = filepath.Join(t.TempDir(), "missing-ca.crt")
			}
			pool := &fakePool{}
			var objectStoreReady atomic.Bool
			objectStoreReady.Store(test.objectCheckErr == nil)
			objectStore := &fakeManagedDependency{
				ready:   &objectStoreReady,
				failure: test.objectCheckErr,
			}
			deps := testDependencies()
			deps.openPool = func(context.Context, config.Database) (databasePool, error) {
				if test.openErr != nil {
					return nil, test.openErr
				}
				return pool, nil
			}
			deps.checkDatabase = func(context.Context, postgres.QueryRower, time.Duration) error {
				return test.checkErr
			}
			deps.newObjectStore = func(config.ObjectStore) (managedDependency, error) {
				if test.objectStoreErr != nil {
					return nil, test.objectStoreErr
				}
				return objectStore, nil
			}
			deps.newWorker = func(paymentkafka.Settings) (eventWorker, error) {
				return &fakeEventWorker{failure: test.preflightErr}, nil
			}
			listened := false
			deps.listen = func(network, address string) (net.Listener, error) {
				listened = true
				return net.Listen(network, address)
			}
			managementCreated := false
			originalManagement := deps.newManagement
			deps.newManagement = func(state *health.State) httpServer {
				managementCreated = true
				return originalManagement(state)
			}

			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			// when
			err := run(ctx, settings, discardLogger(), deps)

			// then
			if err == nil || !strings.Contains(err.Error(), "run service: "+test.want) {
				t.Fatalf("run error = %v, want %q", err, test.want)
			}
			if listened != test.wantListened || managementCreated {
				t.Errorf(
					"listened = %v, management created = %v",
					listened,
					managementCreated,
				)
			}
			if pool.closed.Load() != test.wantPoolClosed ||
				objectStore.closed.Load() != test.wantObjectClosed {
				t.Errorf(
					"pool closed = %v, object storage closed = %v",
					pool.closed.Load(),
					objectStore.closed.Load(),
				)
			}
		})
	}
}

func TestRun_UnavailableGatewayNeverBecomesReadyAndCleansUp(t *testing.T) {
	// given
	settings := testConfig(t)
	settings.ProbeTimeout = 30 * time.Millisecond
	closed, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	unavailable := closed.Addr().String()
	_ = closed.Close()
	deps := testDependencies()
	deps.newConnection = func(_ string, provider telemetryProvider, settings config.Config) (gatewayConnection, error) {
		return newGatewayConnection(unavailable, provider, settings)
	}
	managementCreated := false
	originalManagement := deps.newManagement
	deps.newManagement = func(state *health.State) httpServer {
		managementCreated = true
		return originalManagement(state)
	}

	// when
	err = run(context.Background(), settings, discardLogger(), deps)
	// then
	if err == nil || !strings.Contains(err.Error(), "gateway connection not ready") {
		t.Fatalf("run error = %v", err)
	}
	if managementCreated {
		t.Error("management server was created before gateway readiness")
	}
}

func TestRun_ReportsDynamicGatewayReadinessLossAndRecovery(t *testing.T) {
	// given
	settings := testConfig(t)
	deps := testDependencies()
	connection := newStatefulGatewayConnection(connectivity.Ready)
	deps.newConnection = func(string, telemetryProvider, config.Config) (gatewayConnection, error) {
		return connection, nil
	}
	deps.newGateway = func(paymentv1.PaymentServiceClient, gateway.Settings) (http.Handler, error) {
		return http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			writer.WriteHeader(http.StatusNoContent)
		}), nil
	}
	addresses := make(chan string, 3)
	deps.listen = func(network, address string) (net.Listener, error) {
		listener, err := net.Listen(network, address)
		if err == nil {
			addresses <- listener.Addr().String()
		}
		return listener, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	runResult := make(chan error, 1)
	go func() { runResult <- run(ctx, settings, discardLogger(), deps) }()
	_ = receiveString(t, addresses)
	_ = receiveString(t, addresses)
	healthAddress := receiveString(t, addresses)
	client := &http.Client{Timeout: time.Second}
	waitForHTTPStatus(t, client, "http://"+healthAddress+"/readyz", http.StatusOK)

	// when
	connection.setState(connectivity.TransientFailure)

	// then
	waitForHTTPStatus(
		t,
		client,
		"http://"+healthAddress+"/readyz",
		http.StatusServiceUnavailable,
	)
	waitForHTTPStatus(t, client, "http://"+healthAddress+"/livez", http.StatusOK)

	// when
	connection.setState(connectivity.Ready)

	// then
	waitForHTTPStatus(t, client, "http://"+healthAddress+"/readyz", http.StatusOK)
	cancel()
	if err := receiveError(t, runResult, "service did not stop"); err != nil {
		t.Fatalf("run service: %v", err)
	}
}

func TestGatewayConnectionIdleTimeout_Disabled(t *testing.T) {
	// given
	want := time.Duration(0)

	// when
	got := gatewayConnectionIdleTimeout

	// then
	if got != want {
		t.Fatalf("gateway connection idle timeout = %v, want disabled", got)
	}
}

func TestHandlerTracker_UsesUnavailableHandlerAfterDrain(t *testing.T) {
	// given
	nextCalled := false
	unavailableCalled := false
	tracker := newHandlerTracker(
		http.HandlerFunc(func(http.ResponseWriter, *http.Request) { nextCalled = true }),
		http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			unavailableCalled = true
			writer.WriteHeader(http.StatusServiceUnavailable)
		}),
	)
	tracker.beginDrain()
	recorder := httptest.NewRecorder()

	// when
	tracker.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/", nil))

	// then
	if nextCalled || !unavailableCalled || recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf(
			"drain routing = (next %t, unavailable %t, status %d)",
			nextCalled,
			unavailableCalled,
			recorder.Code,
		)
	}
}

func TestRun_CorrelatesHTTPClientAndServerSpans(t *testing.T) {
	// given
	settings := testConfig(t)
	deps := testDependencies()
	recorder := tracetest.NewSpanRecorder()
	tracerProvider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	provider := &recordingProvider{
		tracerProvider: tracerProvider,
		meterProvider:  metricnoop.NewMeterProvider(),
	}
	deps.newTelemetry = func(context.Context, string, config.Telemetry) (telemetryProvider, error) {
		return provider, nil
	}
	addresses := make(chan string, 3)
	deps.listen = func(network, address string) (net.Listener, error) {
		listener, err := net.Listen(network, address)
		if err == nil {
			addresses <- listener.Addr().String()
		}
		return listener, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	runResult := make(chan error, 1)
	go func() { runResult <- run(ctx, settings, discardLogger(), deps) }()
	_ = receiveString(t, addresses)
	httpAddress := receiveString(t, addresses)
	healthAddress := receiveString(t, addresses)
	client := &http.Client{Timeout: time.Second}
	waitForHTTPStatus(t, client, "http://"+healthAddress+"/readyz", http.StatusOK)
	// when
	request, err := http.NewRequest(
		http.MethodGet,
		"http://"+httpAddress+"/payment/accounts",
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Traceparent", "00-11111111111111111111111111111111-2222222222222222-01")
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	assertResponse(t, response, http.StatusOK, `[]`)
	cancel()
	if err := receiveError(t, runResult, "traced service did not stop"); err != nil {
		t.Fatal(err)
	}

	// then
	var httpSpan, clientSpan, serverSpan sdktrace.ReadOnlySpan
	for _, span := range recorder.Ended() {
		if span.SpanContext().TraceID().String() != "11111111111111111111111111111111" {
			continue
		}
		switch {
		case span.Name() == "GET /payment/accounts":
			httpSpan = span
		case span.SpanKind() == trace.SpanKindClient:
			clientSpan = span
		case span.SpanKind() == trace.SpanKindServer && strings.Contains(span.Name(), "PaymentService"):
			serverSpan = span
		}
	}
	if httpSpan == nil || clientSpan == nil || serverSpan == nil {
		t.Fatalf(
			"missing trace chain: HTTP=%v client=%v server=%v",
			httpSpan != nil,
			clientSpan != nil,
			serverSpan != nil,
		)
	}
	if clientSpan.Parent().SpanID() != httpSpan.SpanContext().SpanID() ||
		serverSpan.Parent().SpanID() != clientSpan.SpanContext().SpanID() {
		t.Errorf("trace ancestry is not HTTP -> client -> server")
	}
}

func TestRun_MapsGatewayClientResourceExhaustionToInternalError(t *testing.T) {
	// given
	settings := testConfig(t)
	settings.MaxReceiveBytes = 1
	deps := testDependencies()
	addresses := make(chan string, 3)
	deps.listen = func(network, address string) (net.Listener, error) {
		listener, err := net.Listen(network, address)
		if err == nil {
			addresses <- listener.Addr().String()
		}
		return listener, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	runResult := make(chan error, 1)
	go func() { runResult <- run(ctx, settings, discardLogger(), deps) }()
	_ = receiveString(t, addresses)
	httpAddress := receiveString(t, addresses)
	healthAddress := receiveString(t, addresses)
	client := &http.Client{Timeout: time.Second}
	waitForHTTPStatus(t, client, "http://"+healthAddress+"/readyz", http.StatusOK)

	// when
	response := getPayment(t, client, httpAddress, "/payment/accounts/a")

	// then
	assertResponse(
		t,
		response,
		http.StatusInternalServerError,
		`{"code":13,"message":"internal error","details":[]}`,
	)
	cancel()
	if err := receiveError(t, runResult, "service did not stop"); err != nil {
		t.Fatalf("run service: %v", err)
	}
}

func TestRun_PropagatesServiceDeadlineAndCallerCancellationThroughRPC(t *testing.T) {
	// given
	settings := testConfig(t)
	settings.RequestTimeout = 80 * time.Millisecond
	deps := testDependencies()
	addresses := make(chan string, 3)
	deps.listen = func(network, address string) (net.Listener, error) {
		listener, err := net.Listen(network, address)
		if err == nil {
			addresses <- listener.Addr().String()
		}
		return listener, err
	}
	started := make(chan string, 2)
	finished := make(chan error, 2)
	deps.newAccountReads = func(databasePool) (transport.AccountReads, error) {
		return &fakeAccountReads{balanceFunc: func(
			ctx context.Context,
			accountNumber string,
		) (application.AccountBalance, error) {
			started <- accountNumber
			<-ctx.Done()
			finished <- ctx.Err()
			return application.AccountBalance{}, ctx.Err()
		}}, nil
	}
	ctx, cancelService := context.WithCancel(context.Background())
	runResult := make(chan error, 1)
	go func() { runResult <- run(ctx, settings, discardLogger(), deps) }()
	_ = receiveString(t, addresses)
	httpAddress := receiveString(t, addresses)
	healthAddress := receiveString(t, addresses)
	client := &http.Client{Timeout: time.Second}
	waitForHTTPStatus(t, client, "http://"+healthAddress+"/readyz", http.StatusOK)

	// when
	response := getPayment(t, client, httpAddress, "/payment/accounts/budget")

	// then
	if receiveString(t, started) != "budget" {
		t.Fatal("service-budget request did not reach the RPC handler")
	}
	assertResponse(
		t,
		response,
		http.StatusGatewayTimeout,
		`{"code":4,"message":"deadline exceeded","details":[]}`,
	)
	if observed := receiveError(
		t,
		finished,
		"service budget cancellation did not reach RPC",
	); !errors.Is(observed, context.Canceled) &&
		!errors.Is(observed, context.DeadlineExceeded) {
		t.Fatalf("service budget RPC cancellation = %v", observed)
	}

	// when
	requestCtx, cancelRequest := context.WithCancel(context.Background())
	request, err := http.NewRequestWithContext(
		requestCtx,
		http.MethodGet,
		"http://"+httpAddress+"/payment/accounts/caller",
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	requestResult := make(chan error, 1)
	go func() {
		response, requestErr := client.Do(request)
		if response != nil {
			_ = response.Body.Close()
		}
		requestResult <- requestErr
	}()
	if receiveString(t, started) != "caller" {
		t.Fatal("caller-cancel request did not reach the RPC handler")
	}
	cancelRequest()

	// then
	if observed := receiveError(
		t,
		finished,
		"caller cancellation did not reach RPC",
	); !errors.Is(
		observed,
		context.Canceled,
	) {
		t.Fatalf("caller cancellation RPC error = %v", observed)
	}
	if err := receiveError(
		t,
		requestResult,
		"canceled HTTP request did not finish",
	); !errors.Is(
		err,
		context.Canceled,
	) {
		t.Fatalf("canceled HTTP request error = %v", err)
	}
	cancelService()
	if err := receiveError(t, runResult, "service did not stop"); err != nil {
		t.Fatalf("run service: %v", err)
	}
}

func TestRun_EnforcesGatewayClientReceiveLimit(t *testing.T) {
	// given
	settings := testConfig(t)
	settings.MaxSendBytes = 1
	deps := testDependencies()
	deps.newAccountReads = func(databasePool) (transport.AccountReads, error) {
		return &fakeAccountReads{accounts: []application.Account{{
			PolicyAccountNumber: "payment-1",
			PolicyNumber:        "policy-1",
		}}}, nil
	}
	addresses := make(chan string, 3)
	deps.listen = func(network, address string) (net.Listener, error) {
		listener, err := net.Listen(network, address)
		if err == nil {
			addresses <- listener.Addr().String()
		}
		return listener, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	runResult := make(chan error, 1)
	go func() { runResult <- run(ctx, settings, discardLogger(), deps) }()
	_ = receiveString(t, addresses)
	httpAddress := receiveString(t, addresses)
	healthAddress := receiveString(t, addresses)
	client := &http.Client{Timeout: time.Second}
	waitForHTTPStatus(t, client, "http://"+healthAddress+"/readyz", http.StatusOK)

	// when
	response := getPayment(t, client, httpAddress, "/payment/accounts")

	// then
	assertResponse(
		t,
		response,
		http.StatusInternalServerError,
		`{"code":13,"message":"internal error","details":[]}`,
	)
	cancel()
	if err := receiveError(t, runResult, "service did not stop"); err != nil {
		t.Fatalf("run service: %v", err)
	}
}

func TestRun_HTTPToRPCFailureTelemetryExcludesPrivateMarker(t *testing.T) {
	// given
	const marker = "private-http-rpc-marker"
	settings := testConfig(t)
	deps := testDependencies()
	reader := sdkmetric.NewManualReader()
	meterProvider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	spanRecorder := tracetest.NewSpanRecorder()
	tracerProvider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(spanRecorder))
	provider := &recordingProvider{tracerProvider: tracerProvider, meterProvider: meterProvider}
	deps.newTelemetry = func(context.Context, string, config.Telemetry) (telemetryProvider, error) {
		return provider, nil
	}
	deps.newAccountReads = func(databasePool) (transport.AccountReads, error) {
		return &fakeAccountReads{err: errors.New(marker)}, nil
	}
	addresses := make(chan string, 3)
	deps.listen = func(network, address string) (net.Listener, error) {
		listener, err := net.Listen(network, address)
		if err == nil {
			addresses <- listener.Addr().String()
		}
		return listener, err
	}
	var logs bytes.Buffer
	ctx, cancel := context.WithCancel(context.Background())
	runResult := make(chan error, 1)
	go func() { runResult <- run(ctx, settings, slog.New(slog.NewJSONHandler(&logs, nil)), deps) }()
	_ = receiveString(t, addresses)
	httpAddress := receiveString(t, addresses)
	healthAddress := receiveString(t, addresses)
	client := &http.Client{Timeout: time.Second}
	waitForHTTPStatus(t, client, "http://"+healthAddress+"/readyz", http.StatusOK)

	// when
	response := getPayment(t, client, httpAddress, "/payment/accounts")
	cancel()
	if err := receiveError(t, runResult, "service did not stop"); err != nil {
		t.Fatalf("run service: %v", err)
	}

	// then
	assertResponse(
		t,
		response,
		http.StatusInternalServerError,
		`{"code":13,"message":"internal error","details":[]}`,
	)
	var metrics metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &metrics); err != nil {
		t.Fatal(err)
	}
	combined := logs.String() + fmt.Sprint(metrics)
	for _, span := range spanRecorder.Ended() {
		combined += span.Name() + fmt.Sprint(span.Attributes(), span.Events())
	}
	if strings.Contains(combined, marker) {
		t.Fatalf("private marker escaped into telemetry: %s", combined)
	}
	if !strings.Contains(combined, "GET /payment/accounts") ||
		!strings.Contains(combined, "insurance_hub.payment.requests") {
		t.Errorf("missing HTTP-to-RPC completion signals: %s", combined)
	}
}

func TestRun_ForcesStalledRPCWithinShutdownBudget(t *testing.T) {
	// given
	settings := testConfig(t)
	settings.RequestTimeout = 50 * time.Millisecond
	settings.ShutdownTimeout = 200 * time.Millisecond
	settings.ExporterTimeout = 50 * time.Millisecond
	deps := testDependencies()
	addresses := make(chan string, 3)
	deps.listen = func(network, address string) (net.Listener, error) {
		listener, err := net.Listen(network, address)
		if err == nil {
			addresses <- listener.Addr().String()
		}
		return listener, err
	}
	started := make(chan struct{})
	release := make(chan struct{})
	deps.newAccountReads = func(databasePool) (transport.AccountReads, error) {
		return &fakeAccountReads{balanceFunc: func(
			context.Context,
			string,
		) (application.AccountBalance, error) {
			close(started)
			<-release
			return application.AccountBalance{}, nil
		}}, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	runResult := make(chan error, 1)
	go func() { runResult <- run(ctx, settings, discardLogger(), deps) }()
	_ = receiveString(t, addresses)
	httpAddress := receiveString(t, addresses)
	healthAddress := receiveString(t, addresses)
	client := &http.Client{Timeout: time.Second}
	waitForHTTPStatus(t, client, "http://"+healthAddress+"/readyz", http.StatusOK)
	// when
	requestDone := make(chan struct{})
	go func() {
		defer close(requestDone)
		request, _ := http.NewRequest(
			http.MethodGet,
			"http://"+httpAddress+"/payment/accounts/stall",
			nil,
		)
		response, err := client.Do(request)
		if err == nil {
			_ = response.Body.Close()
		}
	}()
	receiveSignal(t, started, "stalled RPC did not start")
	// then
	shutdownStarted := time.Now()
	cancel()
	err := receiveError(t, runResult, "forced shutdown did not complete")
	elapsed := time.Since(shutdownStarted)
	close(release)
	if elapsed > settings.ShutdownTimeout+150*time.Millisecond {
		t.Errorf("shutdown took %v, budget %v", elapsed, settings.ShutdownTimeout)
	}
	if err == nil || !strings.Contains(err.Error(), "deadline exceeded") {
		t.Errorf("forced shutdown error = %v", err)
	}
	select {
	case <-requestDone:
	case <-time.After(time.Second):
		t.Error("HTTP request remained blocked after forced shutdown")
	}
}

func TestRun_PartialBindFailureClosesEarlierListeners(t *testing.T) {
	// given
	settings := testConfig(t)
	deps := testDependencies()
	bindFailure := errors.New("management bind failed")
	var listeners []net.Listener
	deps.listen = func(network, address string) (net.Listener, error) {
		if len(listeners) == 2 {
			return nil, bindFailure
		}
		listener, err := net.Listen(network, address)
		if err == nil {
			listeners = append(listeners, listener)
		}
		return listener, err
	}
	// when
	err := run(context.Background(), settings, discardLogger(), deps)
	// then
	if !errors.Is(err, bindFailure) {
		t.Fatalf("run error = %v", err)
	}
	for _, listener := range listeners {
		if _, err := listener.Accept(); !errors.Is(err, net.ErrClosed) {
			t.Errorf("listener remained open: %v", err)
		}
	}
}

func TestInternalDialTarget_NormalizesWildcardFamilies(t *testing.T) {
	// given
	for _, test := range []struct{ input, want string }{
		{"0.0.0.0:1234", "127.0.0.1:1234"},
		{"[::]:1234", "[::1]:1234"},
		{"127.0.0.2:1234", "127.0.0.2:1234"},
	} {
		// when
		got, err := internalDialTarget(testAddress(test.input))
		// then
		if err != nil || got != test.want {
			t.Errorf(
				"internalDialTarget(%q) = (%q, %v), want %q",
				test.input,
				got,
				err,
				test.want,
			)
		}
	}
}

func testConfig(t *testing.T) config.Config {
	t.Helper()
	caPath := filepath.Join(t.TempDir(), "kafka-ca.crt")
	if err := os.WriteFile(caPath, []byte("synthetic CA"), 0o600); err != nil {
		t.Fatal(err)
	}
	parse := func(name string) config.Address {
		address, err := kitconfig.ParseAddress(name, "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		return address
	}
	return config.Config{
		Runtime: kitconfig.Runtime{
			RequestTimeout:  500 * time.Millisecond,
			ProbeTimeout:    500 * time.Millisecond,
			ShutdownTimeout: 2 * time.Second,
		},
		GRPC: kitconfig.GRPC{MaxReceiveBytes: 1048576, MaxSendBytes: 8388608},
		HTTPServer: kitconfig.HTTPServer{
			ReadHeaderTimeout: time.Second, ReadTimeout: time.Second,
			WriteTimeout: time.Second, IdleTimeout: time.Second,
		},
		Telemetry:          config.Telemetry{ExporterTimeout: 200 * time.Millisecond},
		ServiceName:        "payment-service",
		HTTPAddress:        parse(kitconfig.EnvHTTPAddress),
		GRPCAddress:        parse(kitconfig.EnvGRPCAddress),
		HealthAddress:      parse(kitconfig.EnvHealthAddress),
		HTTPMaxHeaderBytes: 1024,
		HTTPMaxBodyBytes:   4096,
		Database: config.Database{
			MaxConnections: 1,
			ConnectTimeout: time.Second,
			QueryTimeout:   100 * time.Millisecond,
		},
		ObjectStore: config.ObjectStore{
			Bucket:       "payments-import",
			FetchTimeout: time.Second,
		},
		Kafka: config.Kafka{
			Brokers: []string{
				"kafka.example:9093",
			},
			RegisteredTopic: "policy-registered",
			CAFile:          caPath,
		},
		StartupTimeout: 5 * time.Second,
	}
}

// testDependencies replaces PostgreSQL, MinIO and Kafka with healthy fakes.
func testDependencies() dependencies {
	deps := productionDependencies()
	deps.openPool = func(context.Context, config.Database) (databasePool, error) {
		return &fakePool{}, nil
	}
	deps.checkDatabase = func(context.Context, postgres.QueryRower, time.Duration) error {
		return nil
	}
	deps.newObjectStore = func(config.ObjectStore) (managedDependency, error) {
		ready := &atomic.Bool{}
		ready.Store(true)
		return &fakeManagedDependency{ready: ready}, nil
	}
	deps.newWorker = func(paymentkafka.Settings) (eventWorker, error) {
		return &fakeEventWorker{}, nil
	}
	deps.newAccountReads = func(databasePool) (transport.AccountReads, error) {
		return &fakeAccountReads{accounts: []application.Account{}}, nil
	}
	return deps
}

type fakeAccountReads struct {
	accounts       []application.Account
	balance        application.AccountBalance
	err            error
	listFunc       func(context.Context) ([]application.Account, error)
	balanceFunc    func(context.Context, string) (application.AccountBalance, error)
	metadataSeen   chan metadata.MD
	handlerStarted chan struct{}
	releaseHandler chan struct{}
	startOnce      sync.Once
}

func (reads *fakeAccountReads) List(ctx context.Context) ([]application.Account, error) {
	if reads.metadataSeen != nil {
		md, _ := metadata.FromIncomingContext(ctx)
		reads.metadataSeen <- md.Copy()
	}
	if reads.listFunc != nil {
		return reads.listFunc(ctx)
	}
	return reads.accounts, reads.err
}

func (reads *fakeAccountReads) Balance(
	ctx context.Context,
	accountNumber string,
) (application.AccountBalance, error) {
	if reads.metadataSeen != nil {
		md, _ := metadata.FromIncomingContext(ctx)
		reads.metadataSeen <- md.Copy()
	}
	if reads.balanceFunc != nil {
		return reads.balanceFunc(ctx, accountNumber)
	}
	if reads.handlerStarted != nil {
		reads.startOnce.Do(func() { close(reads.handlerStarted) })
		select {
		case <-reads.releaseHandler:
		case <-ctx.Done():
			return application.AccountBalance{}, ctx.Err()
		}
	}
	return reads.balance, reads.err
}

type fakePool struct{ closed atomic.Bool }

func (*fakePool) QueryRow(context.Context, string, ...any) pgx.Row { return nil }
func (*fakePool) BeginTx(context.Context, pgx.TxOptions) (pgx.Tx, error) {
	return nil, errors.New("fake pool does not serve account reads")
}
func (pool *fakePool) Close() { pool.closed.Store(true) }

type fakeManagedDependency struct {
	ready   *atomic.Bool
	failure error
	closed  atomic.Bool
}

func (dependency *fakeManagedDependency) Check(context.Context) error {
	if !dependency.ready.Load() {
		return dependency.failure
	}
	return nil
}

func (dependency *fakeManagedDependency) Close() { dependency.closed.Store(true) }

type fakeEventWorker struct {
	preflighted atomic.Bool
	failure     error
}

func (worker *fakeEventWorker) Preflight(context.Context) error {
	worker.preflighted.Store(true)
	return worker.failure
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func assertResponse(t *testing.T, response *http.Response, status int, body string) {
	t.Helper()
	defer func() { _ = response.Body.Close() }()
	data, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != status || string(data) != body {
		t.Errorf(
			"response = (%d, %q), want (%d, %q)",
			response.StatusCode,
			data,
			status,
			body,
		)
	}
}

func assertNoIdentityMetadata(t *testing.T, md metadata.MD) {
	t.Helper()
	for _, key := range []string{"authorization", "principal", "forwarded", "x-forwarded-for", "x-forwarded-host", "x-real-ip"} {
		if values := md.Get(key); len(values) != 0 {
			t.Errorf("metadata %s escaped: %v", key, values)
		}
	}
}

func assertOversizedHeaderRejected(t *testing.T, address string, size int) {
	t.Helper()
	connection, err := net.DialTimeout("tcp", address, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = connection.Close() }()
	_ = connection.SetDeadline(time.Now().Add(time.Second))
	_, err = io.WriteString(
		connection,
		"GET /payment/accounts HTTP/1.1\r\nHost: example\r\nX-Large: "+
			strings.Repeat("x", size)+"\r\nContent-Length: 0\r\n\r\n",
	)
	if err != nil {
		t.Fatal(err)
	}
	response, err := http.ReadResponse(
		bufio.NewReader(connection),
		&http.Request{Method: http.MethodGet},
	)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusRequestHeaderFieldsTooLarge {
		t.Errorf("oversized header status = %d, want 431", response.StatusCode)
	}
}

func getPayment(t *testing.T, client *http.Client, address, path string) *http.Response {
	t.Helper()
	request, err := http.NewRequest(
		http.MethodGet,
		"http://"+address+path,
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	return response
}

func waitForHTTPStatus(t *testing.T, client *http.Client, target string, want int) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		response, err := client.Get(target)
		if err == nil {
			_, _ = io.Copy(io.Discard, response.Body)
			_ = response.Body.Close()
			if response.StatusCode == want {
				return
			}
		}
	}
	t.Fatalf("%s did not return %d", target, want)
}

func receiveString(t *testing.T, values <-chan string) string {
	t.Helper()
	select {
	case value := <-values:
		return value
	case <-time.After(time.Second):
		t.Fatal("listener address was not reported")
		return ""
	}
}

func receiveSignal(t *testing.T, signal <-chan struct{}, failure string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(time.Second):
		t.Fatal(failure)
	}
}

func receiveError(t *testing.T, result <-chan error, failure string) error {
	t.Helper()
	select {
	case err := <-result:
		return err
	case <-time.After(3 * time.Second):
		t.Fatal(failure)
		return nil
	}
}

type testAddress string

func (testAddress) Network() string        { return "tcp" }
func (address testAddress) String() string { return string(address) }

type recordingProvider struct {
	tracerProvider *sdktrace.TracerProvider
	meterProvider  metric.MeterProvider
}

type statefulGatewayConnection struct {
	mu      sync.Mutex
	state   connectivity.State
	changed chan struct{}
}

func newStatefulGatewayConnection(state connectivity.State) *statefulGatewayConnection {
	return &statefulGatewayConnection{state: state, changed: make(chan struct{})}
}

func (*statefulGatewayConnection) Invoke(
	context.Context,
	string,
	any,
	any,
	...grpc.CallOption,
) error {
	return errors.New("unexpected RPC invocation")
}

func (*statefulGatewayConnection) NewStream(
	context.Context,
	*grpc.StreamDesc,
	string,
	...grpc.CallOption,
) (grpc.ClientStream, error) {
	return nil, errors.New("unexpected RPC stream")
}

func (*statefulGatewayConnection) Connect() {}

func (connection *statefulGatewayConnection) GetState() connectivity.State {
	connection.mu.Lock()
	defer connection.mu.Unlock()
	return connection.state
}

func (connection *statefulGatewayConnection) WaitForStateChange(
	ctx context.Context,
	source connectivity.State,
) bool {
	connection.mu.Lock()
	if connection.state != source {
		connection.mu.Unlock()
		return true
	}
	changed := connection.changed
	connection.mu.Unlock()
	select {
	case <-changed:
		return true
	case <-ctx.Done():
		return false
	}
}

func (connection *statefulGatewayConnection) Close() error { return nil }

func (connection *statefulGatewayConnection) setState(state connectivity.State) {
	connection.mu.Lock()
	connection.state = state
	close(connection.changed)
	connection.changed = make(chan struct{})
	connection.mu.Unlock()
}

func (provider *recordingProvider) MeterProvider() metric.MeterProvider {
	return provider.meterProvider
}

func (provider *recordingProvider) TracerProvider() trace.TracerProvider {
	return provider.tracerProvider
}

func (*recordingProvider) HTTPPropagator() propagation.TextMapPropagator {
	return propagation.TraceContext{}
}

func (*recordingProvider) GRPCPropagator() propagation.TextMapPropagator {
	return propagation.TraceContext{}
}

func (provider *recordingProvider) Shutdown(ctx context.Context) error {
	return provider.tracerProvider.Shutdown(ctx)
}
