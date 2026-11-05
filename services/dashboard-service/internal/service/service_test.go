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
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	kitconfig "github.com/igor-baiborodine/insurance-hub/libs/servicekit/config"
	"github.com/igor-baiborodine/insurance-hub/libs/servicekit/health"
	"go.opentelemetry.io/otel/metric"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
	"go.opentelemetry.io/otel/propagation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	dashboardv1 "github.com/igor-baiborodine/insurance-hub/services/dashboard-service/gen/dashboard/v1"
	"github.com/igor-baiborodine/insurance-hub/services/dashboard-service/internal/config"
	"github.com/igor-baiborodine/insurance-hub/services/dashboard-service/internal/gateway"
)

func TestDeployedDirectRPC(t *testing.T) {
	address := os.Getenv("DASHBOARD_DEPLOYMENT_GRPC_ADDR")
	if address == "" {
		t.Skip("DASHBOARD_DEPLOYMENT_GRPC_ADDR is not set")
	}
	message := os.Getenv("DASHBOARD_DEPLOYMENT_GRPC_MESSAGE")
	if message == "" {
		t.Fatal("DASHBOARD_DEPLOYMENT_GRPC_MESSAGE is not set")
	}
	connection, err := grpc.NewClient(
		address,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = connection.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	response, err := dashboardv1.NewExampleServiceClient(connection).Echo(
		ctx,
		&dashboardv1.EchoRequest{Message: message},
	)
	if err != nil || response.GetMessage() != message {
		t.Fatalf("deployed direct Echo = (%v, %v)", response, err)
	}
}

func TestRun_ProductionCompositionServesBothTransportsAndDrainsHTTPFirst(t *testing.T) {
	// given
	settings := testConfig(t)
	deps := productionDependencies()
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
	metadataSeen := make(chan metadata.MD, 8)
	var startOnce sync.Once
	deps.echo = func(ctx context.Context, message string) (string, error) {
		md, _ := metadata.FromIncomingContext(ctx)
		metadataSeen <- md.Copy()
		if message == "failure" {
			return "", errors.New("private-upstream-marker")
		}
		if message == "drain" {
			startOnce.Do(func() { close(handlerStarted) })
			select {
			case <-releaseHandler:
			case <-ctx.Done():
				return "", ctx.Err()
			}
		}
		return message, nil
	}
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
		http.MethodPost,
		"http://"+httpAddress+"/v1/example/echo",
		strings.NewReader(`{"message":"hello"}`),
	)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer private-token")
	request.Header.Set("Grpc-Metadata-Principal", "forged-principal")
	request.Header.Set("Forwarded", "for=203.0.113.44")
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	// then
	assertResponse(t, response, http.StatusOK, `{"message":"hello"}`)
	assertNoIdentityMetadata(t, <-metadataSeen)
	assertOversizedHeaderRejected(t, httpAddress, settings.HTTPMaxHeaderBytes+5000)
	maximum := strings.Repeat("a", 128)
	assertResponse(
		t,
		postEcho(t, client, httpAddress, maximum),
		http.StatusOK,
		`{"message":"`+maximum+`"}`,
	)
	assertNoIdentityMetadata(t, <-metadataSeen)
	assertResponse(
		t,
		postEcho(t, client, httpAddress, maximum+"a"),
		http.StatusBadRequest,
		`{"code":3,"message":"invalid request","details":[]}`,
	)
	failureResponse := postEcho(t, client, httpAddress, "failure")
	assertResponse(
		t,
		failureResponse,
		http.StatusInternalServerError,
		`{"code":13,"message":"internal error","details":[]}`,
	)
	assertNoIdentityMetadata(t, <-metadataSeen)

	// when
	connection, err := grpc.NewClient(
		grpcAddress,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = connection.Close() }()
	direct, err := dashboardv1.NewExampleServiceClient(connection).Echo(
		context.Background(),
		&dashboardv1.EchoRequest{Message: "direct"},
	)
	// then
	if err != nil || direct.GetMessage() != "direct" {
		t.Fatalf("direct Echo = (%v, %v)", direct, err)
	}
	directMaximum, err := dashboardv1.NewExampleServiceClient(connection).Echo(
		context.Background(),
		&dashboardv1.EchoRequest{Message: maximum},
	)
	if err != nil || directMaximum.GetMessage() != maximum {
		t.Fatalf("direct maximum Echo = (%v, %v)", directMaximum, err)
	}
	_, err = dashboardv1.NewExampleServiceClient(connection).Echo(
		context.Background(),
		&dashboardv1.EchoRequest{Message: maximum + "a"},
	)
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("direct oversized status = %v, want invalid argument", status.Code(err))
	}

	// when
	drainResult := make(chan *http.Response, 1)
	drainError := make(chan error, 1)
	go func() {
		req, requestErr := http.NewRequest(
			http.MethodPost,
			"http://"+httpAddress+"/v1/example/echo",
			strings.NewReader(`{"message":"drain"}`),
		)
		if requestErr != nil {
			drainError <- requestErr
			return
		}
		req.Header.Set("Content-Type", "application/json")
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
		assertResponse(t, response, http.StatusOK, `{"message":"drain"}`)
	case err := <-drainError:
		t.Fatalf("drained HTTP request: %v", err)
	case <-time.After(time.Second):
		t.Fatal("accepted HTTP request did not drain")
	}
	if err := receiveError(t, runResult, "service did not stop"); err != nil {
		t.Fatalf("run service: %v", err)
	}
}

func TestRun_ProductionCompositionEnforcesByteAndPresenceContract(t *testing.T) {
	// given
	settings := testConfig(t)
	deps := productionDependencies()
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
	grpcAddress := receiveString(t, addresses)
	httpAddress := receiveString(t, addresses)
	healthAddress := receiveString(t, addresses)
	httpClient := &http.Client{Timeout: time.Second}
	waitForHTTPStatus(t, httpClient, "http://"+healthAddress+"/readyz", http.StatusOK)
	connection, err := grpc.NewClient(
		grpcAddress,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = connection.Close() }()
	directClient := dashboardv1.NewExampleServiceClient(connection)
	validMessages := make([]string, 0, 130)
	for size := 1; size <= 128; size++ {
		validMessages = append(validMessages, strings.Repeat("a", size))
	}
	validMessages = append(validMessages, "é", strings.Repeat("é", 64))

	// when
	for _, message := range validMessages {
		response, callErr := directClient.Echo(
			context.Background(),
			&dashboardv1.EchoRequest{Message: message},
		)
		// then
		if callErr != nil || response.GetMessage() != message {
			t.Fatalf(
				"direct differential Echo(%d bytes) = (%q, %v)",
				len(message),
				response.GetMessage(),
				callErr,
			)
		}
		assertResponse(
			t,
			postEcho(t, httpClient, httpAddress, message),
			http.StatusOK,
			`{"message":"`+message+`"}`,
		)
	}
	for _, message := range []string{strings.Repeat("a", 129), strings.Repeat("é", 64) + "a"} {
		_, callErr := directClient.Echo(
			context.Background(),
			&dashboardv1.EchoRequest{Message: message},
		)
		if status.Code(callErr) != codes.InvalidArgument {
			t.Fatalf(
				"direct oversized status = %v, want invalid argument",
				status.Code(callErr),
			)
		}
		assertResponse(
			t,
			postEcho(t, httpClient, httpAddress, message),
			http.StatusBadRequest,
			`{"code":3,"message":"invalid request","details":[]}`,
		)
	}
	for _, body := range []string{`{}`, `{"message":null}`, `{"message":""}`} {
		assertResponse(
			t,
			postEchoBody(t, httpClient, httpAddress, body),
			http.StatusBadRequest,
			`{"code":3,"message":"invalid request","details":[]}`,
		)
	}
	_, err = directClient.Echo(context.Background(), &dashboardv1.EchoRequest{})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("direct empty status = %v, want invalid argument", status.Code(err))
	}
	cancel()
	if err := receiveError(t, runResult, "service did not stop"); err != nil {
		t.Fatalf("run service: %v", err)
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
	deps := productionDependencies()
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
	deps := productionDependencies()
	connection := newStatefulGatewayConnection(connectivity.Ready)
	deps.newConnection = func(string, telemetryProvider, config.Config) (gatewayConnection, error) {
		return connection, nil
	}
	deps.newGateway = func(dashboardv1.ExampleServiceClient, gateway.Settings) (http.Handler, error) {
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
	deps := productionDependencies()
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
		http.MethodPost,
		"http://"+httpAddress+"/v1/example/echo",
		strings.NewReader(`{"message":"trace"}`),
	)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Traceparent", "00-11111111111111111111111111111111-2222222222222222-01")
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	assertResponse(t, response, http.StatusOK, `{"message":"trace"}`)
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
		case span.Name() == "POST /v1/example/echo":
			httpSpan = span
		case span.SpanKind() == trace.SpanKindClient:
			clientSpan = span
		case span.SpanKind() == trace.SpanKindServer && strings.Contains(span.Name(), "ExampleService"):
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
	deps := productionDependencies()
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
	response := postEcho(t, client, httpAddress, "a")

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
	deps := productionDependencies()
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
	deps.echo = func(ctx context.Context, message string) (string, error) {
		started <- message
		<-ctx.Done()
		finished <- ctx.Err()
		return "", ctx.Err()
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
	response := postEcho(t, client, httpAddress, "budget")

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
		http.MethodPost,
		"http://"+httpAddress+"/v1/example/echo",
		strings.NewReader(`{"message":"caller"}`),
	)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
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
	deps := productionDependencies()
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
	response := postEcho(t, client, httpAddress, "a")

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

func TestRun_BoundsSlowHTTPBodyWithReadTimeout(t *testing.T) {
	// given
	settings := testConfig(t)
	settings.ReadTimeout = 60 * time.Millisecond
	deps := productionDependencies()
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
	waitForHTTPStatus(
		t,
		&http.Client{Timeout: time.Second},
		"http://"+healthAddress+"/readyz",
		http.StatusOK,
	)
	connection, err := net.DialTimeout("tcp", httpAddress, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = connection.Close() }()
	_ = connection.SetDeadline(time.Now().Add(time.Second))
	started := time.Now()
	_, err = io.WriteString(
		connection,
		"POST /v1/example/echo HTTP/1.1\r\nHost: example\r\n"+
			"Content-Type: application/json\r\nContent-Length: 100\r\n\r\n{",
	)
	if err != nil {
		t.Fatal(err)
	}

	// when
	response, readErr := http.ReadResponse(
		bufio.NewReader(connection),
		&http.Request{Method: http.MethodPost},
	)

	// then
	if response != nil {
		_ = response.Body.Close()
	}
	if elapsed := time.Since(started); elapsed > 500*time.Millisecond {
		t.Fatalf("slow body completed in %v, want within read-timeout bound", elapsed)
	}
	if readErr == nil && response.StatusCode != http.StatusInternalServerError {
		t.Fatalf(
			"slow body status = %d, want bounded 500 or closed connection",
			response.StatusCode,
		)
	}
	cancel()
	if err := receiveError(t, runResult, "service did not stop"); err != nil {
		t.Fatalf("run service: %v", err)
	}
}

func TestRun_HTTPToRPCFailureTelemetryExcludesPrivateMarker(t *testing.T) {
	// given
	const marker = "private-http-rpc-marker"
	settings := testConfig(t)
	deps := productionDependencies()
	reader := sdkmetric.NewManualReader()
	meterProvider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	spanRecorder := tracetest.NewSpanRecorder()
	tracerProvider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(spanRecorder))
	provider := &recordingProvider{tracerProvider: tracerProvider, meterProvider: meterProvider}
	deps.newTelemetry = func(context.Context, string, config.Telemetry) (telemetryProvider, error) {
		return provider, nil
	}
	deps.echo = func(context.Context, string) (string, error) { return "", errors.New(marker) }
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
	response := postEcho(t, client, httpAddress, "failure")
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
	if !strings.Contains(combined, "POST /v1/example/echo") ||
		!strings.Contains(combined, "insurance_hub.dashboard.requests") {
		t.Errorf("missing HTTP-to-RPC completion signals: %s", combined)
	}
}

func TestRun_ForcesStalledRPCWithinShutdownBudget(t *testing.T) {
	// given
	settings := testConfig(t)
	settings.RequestTimeout = 50 * time.Millisecond
	settings.ShutdownTimeout = 200 * time.Millisecond
	settings.ExporterTimeout = 50 * time.Millisecond
	deps := productionDependencies()
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
	deps.echo = func(context.Context, string) (string, error) {
		close(started)
		<-release
		return "stall", nil
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
			http.MethodPost,
			"http://"+httpAddress+"/v1/example/echo",
			strings.NewReader(`{"message":"stall"}`),
		)
		request.Header.Set("Content-Type", "application/json")
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
	deps := productionDependencies()
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

func TestRun_PartialStartupShutsDownTelemetryExactlyOnce(t *testing.T) {
	// given
	settings := testConfig(t)
	deps := productionDependencies()
	provider := &recordingProvider{
		tracerProvider: sdktrace.NewTracerProvider(),
		meterProvider:  metricnoop.NewMeterProvider(),
	}
	deps.newTelemetry = func(context.Context, string, config.Telemetry) (telemetryProvider, error) {
		return provider, nil
	}
	bindFailure := errors.New("gRPC bind failed")
	deps.listen = func(string, string) (net.Listener, error) { return nil, bindFailure }

	// when
	err := run(context.Background(), settings, discardLogger(), deps)

	// then
	if !errors.Is(err, bindFailure) {
		t.Fatalf("run error = %v, want bind failure", err)
	}
	if provider.shutdowns.Load() != 1 {
		t.Errorf("telemetry shutdowns = %d, want exactly one", provider.shutdowns.Load())
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
		ServiceName:        "dashboard-service",
		HTTPAddress:        parse(config.EnvHTTPAddress),
		GRPCAddress:        parse(kitconfig.EnvGRPCAddress),
		HealthAddress:      parse(kitconfig.EnvHealthAddress),
		HTTPMaxHeaderBytes: 1024,
		HTTPMaxBodyBytes:   4096,
	}
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
		"POST /v1/example/echo HTTP/1.1\r\nHost: example\r\nX-Large: "+
			strings.Repeat("x", size)+"\r\nContent-Length: 0\r\n\r\n",
	)
	if err != nil {
		t.Fatal(err)
	}
	response, err := http.ReadResponse(
		bufio.NewReader(connection),
		&http.Request{Method: http.MethodPost},
	)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusRequestHeaderFieldsTooLarge {
		t.Errorf("oversized header status = %d, want 431", response.StatusCode)
	}
}

func postEcho(t *testing.T, client *http.Client, address, message string) *http.Response {
	t.Helper()
	return postEchoBody(t, client, address, `{"message":"`+message+`"}`)
}

func postEchoBody(t *testing.T, client *http.Client, address, body string) *http.Response {
	t.Helper()
	request, err := http.NewRequest(
		http.MethodPost,
		"http://"+address+"/v1/example/echo",
		strings.NewReader(body),
	)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
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
	shutdowns      atomic.Int32
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
	provider.shutdowns.Add(1)
	return provider.tracerProvider.Shutdown(ctx)
}
