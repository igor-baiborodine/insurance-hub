package service

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	scaffoldv1 "github.com/igor-baiborodine/insurance-hub/templates/go-service/gen/scaffold/v1"
	"github.com/igor-baiborodine/insurance-hub/templates/go-service/internal/config"
	transport "github.com/igor-baiborodine/insurance-hub/templates/go-service/internal/grpc"
	"github.com/igor-baiborodine/insurance-hub/templates/go-service/internal/health"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
	grpcgo "google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func TestRunWithdrawsReadinessWhileGracefullyDraining(t *testing.T) {
	// given
	settings := testConfig(time.Second)
	deps := productionDependencies()
	addresses := make(chan string, 2)
	deps.listen = func(network, _ string) (net.Listener, error) {
		listener, err := net.Listen(network, "127.0.0.1:0")
		if err == nil {
			addresses <- listener.Addr().String()
		}
		return listener, err
	}
	handlerStarted := make(chan struct{})
	releaseHandler := make(chan struct{})
	deps.echo = func(_ context.Context, message string) (string, error) {
		close(handlerStarted)
		<-releaseHandler
		return message, nil
	}

	// when
	ctx, cancel := context.WithCancel(context.Background())
	runResult := make(chan error, 1)
	go func() {
		runResult <- run(ctx, settings, discardLogger(), deps)
	}()
	grpcAddress := receiveString(t, addresses)
	healthAddress := receiveString(t, addresses)
	healthURL := "http://" + healthAddress
	httpClient := &http.Client{Timeout: time.Second}

	// then
	waitForHTTPStatus(t, httpClient, healthURL+"/readyz", http.StatusOK)
	assertHTTPStatus(t, httpClient, healthURL+"/livez", http.StatusOK)

	// when
	connection, err := grpcgo.NewClient(
		grpcAddress,
		grpcgo.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		cancel()
		t.Fatalf("create gRPC client: %v", err)
	}
	defer func() {
		if err := connection.Close(); err != nil {
			t.Errorf("close gRPC client: %v", err)
		}
	}()
	rpcResult := make(chan error, 1)
	go func() {
		response, err := scaffoldv1.NewExampleServiceClient(connection).Echo(
			context.Background(),
			&scaffoldv1.EchoRequest{Message: "drain"},
		)
		if err == nil && response.GetMessage() != "drain" {
			err = errors.New("Echo response changed during drain")
		}
		rpcResult <- err
	}()

	// then
	receiveSignal(t, handlerStarted, "Echo handler did not start")

	// when
	cancel()

	// then
	waitForHTTPStatus(t, httpClient, healthURL+"/readyz", http.StatusServiceUnavailable)
	assertHTTPStatus(t, httpClient, healthURL+"/livez", http.StatusOK)
	select {
	case err := <-rpcResult:
		t.Fatalf("in-flight RPC returned before release: %v", err)
	default:
	}

	// when
	close(releaseHandler)

	// then
	if err := receiveError(t, rpcResult, "in-flight RPC did not drain"); err != nil {
		t.Fatalf("drained RPC: %v", err)
	}
	if err := receiveError(t, runResult, "service did not stop"); err != nil {
		t.Fatalf("run service: %v", err)
	}
}

func TestRunCleansUpAfterLaterStartupFailureAndPreservesErrors(t *testing.T) {
	// given
	listenErr := errors.New("health listen failed")
	closeErr := errors.New("gRPC listener close failed")
	telemetryErr := errors.New("telemetry cleanup failed")
	listener := newTrackingListener(closeErr)
	provider := &fakeTelemetryProvider{shutdownErr: telemetryErr}
	server := newFakeGRPCServer(newOrderRecorder())
	listenCalls := 0
	deps := productionDependencies()
	deps.newTelemetry = func(
		context.Context,
		string,
		config.Telemetry,
	) (telemetryProvider, error) {
		return provider, nil
	}
	deps.newGRPCServer = func(
		*slog.Logger,
		trace.TracerProvider,
		propagation.TextMapPropagator,
		transport.Echo,
	) (grpcServer, error) {
		return server, nil
	}
	deps.listen = func(string, string) (net.Listener, error) {
		listenCalls++
		if listenCalls == 1 {
			return listener, nil
		}
		return nil, listenErr
	}

	// when
	err := run(context.Background(), testConfig(time.Second), discardLogger(), deps)

	// then
	for _, want := range []error{listenErr, closeErr, telemetryErr} {
		if !errors.Is(err, want) {
			t.Errorf("run error %v does not preserve %v", err, want)
		}
	}
	receiveSignal(t, listener.closed, "gRPC listener was not closed")
	receiveSignal(t, provider.shutdownCalled, "telemetry was not shut down")
	if provider.shutdownContextErr != nil {
		t.Errorf("telemetry cleanup context started canceled: %v", provider.shutdownContextErr)
	}
}

func TestRunForcesGRPCStopAndReservesTelemetryCleanupTime(t *testing.T) {
	// given
	const shutdownTimeout = 8 * time.Second
	order := newOrderRecorder()
	provider := &fakeTelemetryProvider{order: order}
	server := newFakeGRPCServer(order)
	management := newFakeManagementServer(order)
	forced := make(chan time.Time, 1)
	var gracePeriod time.Duration
	var state *health.State
	deps := productionDependencies()
	deps.newTelemetry = func(
		context.Context,
		string,
		config.Telemetry,
	) (telemetryProvider, error) {
		return provider, nil
	}
	deps.newGRPCServer = func(
		*slog.Logger,
		trace.TracerProvider,
		propagation.TextMapPropagator,
		transport.Echo,
	) (grpcServer, error) {
		return server, nil
	}
	deps.newManagement = func(selected *health.State) managementServer {
		state = selected
		return management
	}
	deps.listen = func(string, string) (net.Listener, error) {
		return newTrackingListener(nil), nil
	}
	deps.after = func(duration time.Duration) <-chan time.Time {
		gracePeriod = duration
		return forced
	}

	// when
	ctx, cancel := context.WithCancel(context.Background())
	runResult := make(chan error, 1)
	go func() {
		runResult <- run(ctx, testConfig(shutdownTimeout), discardLogger(), deps)
	}()

	// then
	receiveSignal(t, server.serveStarted, "gRPC server did not start")
	receiveSignal(t, management.serveStarted, "management server did not start")
	waitForReadyState(t, state, true)

	// when
	cancel()
	receiveSignal(t, server.gracefulStarted, "graceful stop did not start")
	forced <- time.Now()

	// then
	if err := receiveError(t, runResult, "forced shutdown did not complete"); err != nil {
		t.Fatalf("run service: %v", err)
	}

	if gracePeriod != shutdownTimeout/2 {
		t.Errorf("grace period = %v, want %v", gracePeriod, shutdownTimeout/2)
	}
	if state.Ready() {
		t.Error("service remained ready during shutdown")
	}
	wantOrder := []string{"gRPC stop", "management shutdown", "telemetry shutdown"}
	if got := order.values(); !equalStrings(got, wantOrder) {
		t.Errorf("shutdown order = %v, want %v", got, wantOrder)
	}
	if management.shutdownContextErr != nil || provider.shutdownContextErr != nil {
		t.Errorf(
			"cleanup contexts started canceled: management=%v telemetry=%v",
			management.shutdownContextErr,
			provider.shutdownContextErr,
		)
	}
	if management.shutdownDeadline.IsZero() || provider.shutdownDeadline.IsZero() ||
		!management.shutdownDeadline.Before(provider.shutdownDeadline) {
		t.Errorf(
			"cleanup deadlines do not reserve telemetry time: management=%v telemetry=%v",
			management.shutdownDeadline,
			provider.shutdownDeadline,
		)
	}
}

func TestRunBoundsSlowCleanupToOverallShutdownBudget(t *testing.T) {
	// given
	const shutdownTimeout = 200 * time.Millisecond
	order := newOrderRecorder()
	provider := &fakeTelemetryProvider{order: order}
	provider.shutdownCalled = make(chan struct{})
	provider.waitForContext = true
	server := newFakeGRPCServer(order)
	management := newFakeManagementServer(order)
	management.waitForContext = true
	forced := make(chan time.Time, 1)
	deps := productionDependencies()
	deps.newTelemetry = func(
		context.Context,
		string,
		config.Telemetry,
	) (telemetryProvider, error) {
		return provider, nil
	}
	deps.newGRPCServer = func(
		*slog.Logger,
		trace.TracerProvider,
		propagation.TextMapPropagator,
		transport.Echo,
	) (grpcServer, error) {
		return server, nil
	}
	deps.newManagement = func(*health.State) managementServer {
		return management
	}
	deps.listen = func(string, string) (net.Listener, error) {
		return newTrackingListener(nil), nil
	}
	deps.after = func(time.Duration) <-chan time.Time {
		return forced
	}
	ctx, cancel := context.WithCancel(context.Background())
	runResult := make(chan error, 1)
	go func() {
		runResult <- run(ctx, testConfig(shutdownTimeout), discardLogger(), deps)
	}()
	receiveSignal(t, server.serveStarted, "gRPC server did not start")
	receiveSignal(t, management.serveStarted, "management server did not start")

	// when
	shutdownStarted := time.Now()
	cancel()
	receiveSignal(t, server.gracefulStarted, "graceful stop did not start")
	forced <- time.Now()
	receiveSignal(t, management.shutdownCalled, "management shutdown did not start")
	receiveSignal(t, provider.shutdownCalled, "telemetry shutdown did not start")
	err := receiveErrorWithin(
		t,
		runResult,
		shutdownTimeout+time.Second,
		"shutdown exceeded the overall budget",
	)
	shutdownCompleted := time.Now()

	// then
	if elapsed := shutdownCompleted.Sub(shutdownStarted); elapsed < shutdownTimeout {
		t.Errorf("shutdown completed in %v, before the %v overall deadline", elapsed, shutdownTimeout)
	}
	if delay := shutdownCompleted.Sub(provider.shutdownDeadline); delay > 250*time.Millisecond {
		t.Errorf("shutdown completed %v after the overall deadline", delay)
	}
	for _, want := range []string{
		"shutdown management HTTP: context deadline exceeded",
		"shutdown telemetry: context deadline exceeded",
	} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("run error %q does not contain %q", err, want)
		}
	}
	wantOrder := []string{"gRPC stop", "management shutdown", "telemetry shutdown"}
	if got := order.values(); !equalStrings(got, wantOrder) {
		t.Errorf("shutdown order = %v, want %v", got, wantOrder)
	}
	if !errors.Is(management.shutdownResult, context.DeadlineExceeded) {
		t.Errorf("management shutdown result = %v, want deadline exceeded", management.shutdownResult)
	}
	if !errors.Is(provider.shutdownResult, context.DeadlineExceeded) {
		t.Errorf("telemetry shutdown result = %v, want deadline exceeded", provider.shutdownResult)
	}
}

func testConfig(timeout time.Duration) config.Config {
	return config.Config{
		ServiceName:     "go-service",
		ShutdownTimeout: timeout,
		Telemetry: config.Telemetry{
			ExporterTimeout: time.Second,
		},
	}
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
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
	return receiveErrorWithin(t, result, time.Second, failure)
}

func receiveErrorWithin(
	t *testing.T,
	result <-chan error,
	timeout time.Duration,
	failure string,
) error {
	t.Helper()
	select {
	case err := <-result:
		return err
	case <-time.After(timeout):
		t.Fatal(failure)
		return nil
	}
}

func waitForHTTPStatus(t *testing.T, client *http.Client, url string, want int) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		response, err := client.Get(url)
		if err == nil {
			_, _ = io.Copy(io.Discard, response.Body)
			_ = response.Body.Close()
			if response.StatusCode == want {
				return
			}
		}
		runtime.Gosched()
	}
	t.Fatalf("%s did not return status %d", url, want)
}

func assertHTTPStatus(t *testing.T, client *http.Client, url string, want int) {
	t.Helper()
	response, err := client.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != want {
		t.Errorf("GET %s status = %d, want %d", url, response.StatusCode, want)
	}
}

func waitForReadyState(t *testing.T, state *health.State, want bool) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if state != nil && state.Ready() == want {
			return
		}
		runtime.Gosched()
	}
	t.Fatalf("readiness did not become %t", want)
}

type fakeTelemetryProvider struct {
	order                *orderRecorder
	shutdownCalled       chan struct{}
	shutdownErr          error
	shutdownContextErr   error
	shutdownDeadline     time.Time
	shutdownResult       error
	waitForContext       bool
	shutdownNotification sync.Once
}

func (provider *fakeTelemetryProvider) TracerProvider() trace.TracerProvider {
	return noop.NewTracerProvider()
}

func (provider *fakeTelemetryProvider) Propagator() propagation.TextMapPropagator {
	return propagation.TraceContext{}
}

func (provider *fakeTelemetryProvider) Shutdown(ctx context.Context) error {
	if provider.order != nil {
		provider.order.add("telemetry shutdown")
	}
	provider.shutdownContextErr = ctx.Err()
	provider.shutdownDeadline, _ = ctx.Deadline()
	if provider.shutdownCalled == nil {
		provider.shutdownCalled = make(chan struct{})
	}
	provider.shutdownNotification.Do(func() { close(provider.shutdownCalled) })
	if provider.waitForContext {
		<-ctx.Done()
		provider.shutdownResult = ctx.Err()
		return provider.shutdownResult
	}
	provider.shutdownResult = provider.shutdownErr
	return provider.shutdownErr
}

type fakeGRPCServer struct {
	order             *orderRecorder
	serveStarted      chan struct{}
	gracefulStarted   chan struct{}
	stopped           chan struct{}
	serveNotification sync.Once
	graceNotification sync.Once
	stopNotification  sync.Once
}

func newFakeGRPCServer(order *orderRecorder) *fakeGRPCServer {
	return &fakeGRPCServer{
		order:           order,
		serveStarted:    make(chan struct{}),
		gracefulStarted: make(chan struct{}),
		stopped:         make(chan struct{}),
	}
}

func (server *fakeGRPCServer) Serve(net.Listener) error {
	server.serveNotification.Do(func() { close(server.serveStarted) })
	<-server.stopped
	return grpcgo.ErrServerStopped
}

func (server *fakeGRPCServer) GracefulStop() {
	server.graceNotification.Do(func() { close(server.gracefulStarted) })
	<-server.stopped
}

func (server *fakeGRPCServer) Stop() {
	server.order.add("gRPC stop")
	server.stopNotification.Do(func() { close(server.stopped) })
}

type fakeManagementServer struct {
	order              *orderRecorder
	serveStarted       chan struct{}
	stopped            chan struct{}
	serveNotification  sync.Once
	stopNotification   sync.Once
	shutdownContextErr error
	shutdownDeadline   time.Time
	shutdownCalled     chan struct{}
	shutdownResult     error
	waitForContext     bool
}

func newFakeManagementServer(order *orderRecorder) *fakeManagementServer {
	server := &fakeManagementServer{
		order:        order,
		serveStarted: make(chan struct{}),
		stopped:      make(chan struct{}),
	}
	server.shutdownCalled = make(chan struct{})
	return server
}

func (server *fakeManagementServer) Serve(net.Listener) error {
	server.serveNotification.Do(func() { close(server.serveStarted) })
	<-server.stopped
	return http.ErrServerClosed
}

func (server *fakeManagementServer) Shutdown(ctx context.Context) error {
	server.order.add("management shutdown")
	server.shutdownContextErr = ctx.Err()
	server.shutdownDeadline, _ = ctx.Deadline()
	close(server.shutdownCalled)
	if server.waitForContext {
		<-ctx.Done()
		server.shutdownResult = ctx.Err()
		return server.shutdownResult
	}
	server.stopNotification.Do(func() { close(server.stopped) })
	return nil
}

func (server *fakeManagementServer) Close() error {
	server.stopNotification.Do(func() { close(server.stopped) })
	return nil
}

type trackingListener struct {
	closed    chan struct{}
	closeErr  error
	closeOnce sync.Once
}

func newTrackingListener(closeErr error) *trackingListener {
	return &trackingListener{closed: make(chan struct{}), closeErr: closeErr}
}

func (listener *trackingListener) Accept() (net.Conn, error) {
	<-listener.closed
	return nil, net.ErrClosed
}

func (listener *trackingListener) Close() error {
	listener.closeOnce.Do(func() { close(listener.closed) })
	return listener.closeErr
}

func (listener *trackingListener) Addr() net.Addr {
	return testAddress("127.0.0.1:0")
}

type testAddress string

func (testAddress) Network() string { return "tcp" }

func (address testAddress) String() string { return string(address) }

type orderRecorder struct {
	mu    sync.Mutex
	items []string
}

func newOrderRecorder() *orderRecorder {
	return new(orderRecorder)
}

func (recorder *orderRecorder) add(item string) {
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	recorder.items = append(recorder.items, item)
}

func (recorder *orderRecorder) values() []string {
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	return append([]string(nil), recorder.items...)
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
