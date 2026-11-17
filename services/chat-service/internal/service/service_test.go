package service

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	kitconfig "github.com/igor-baiborodine/insurance-hub/libs/servicekit/config"
	"github.com/igor-baiborodine/insurance-hub/libs/servicekit/health"

	"github.com/igor-baiborodine/insurance-hub/services/chat-service/internal/config"
)

func TestRun_ServesManagementAndExactBusinessNotFound(t *testing.T) {
	// given
	settings := testConfig(t)
	deps := productionDependencies()
	addresses := make(chan string, 2)
	originalListen := deps.listen
	deps.listen = func(network, address string) (net.Listener, error) {
		listener, err := originalListen(network, address)
		if err == nil {
			addresses <- listener.Addr().String()
		}
		return listener, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)

	// when
	go func() { result <- run(ctx, settings, discardLogger(), deps) }()
	businessAddress := receiveString(t, addresses)
	managementAddress := receiveString(t, addresses)
	client := &http.Client{Timeout: time.Second}

	// then
	waitForResponse(t, client, "http://"+managementAddress+"/readyz", http.StatusOK, "ready\n")
	waitForResponse(t, client, "http://"+managementAddress+"/livez", http.StatusOK, "live\n")
	waitForResponse(
		t,
		client,
		"http://"+businessAddress+"/ws/chat/main/test",
		http.StatusNotFound,
		"404 page not found\n",
	)
	request, err := http.NewRequest(
		http.MethodGet,
		"http://"+businessAddress+"/ws/chat/main/test",
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Connection", "Upgrade")
	request.Header.Set("Upgrade", "websocket")
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	assertResponse(t, response, http.StatusNotFound, "404 page not found\n")
	cancel()
	select {
	case err := <-result:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("service did not stop")
	}
}

func TestRun_ManagementAcquisitionFailureCleansUpBusinessListener(t *testing.T) {
	// given
	settings := testConfig(t)
	deps := productionDependencies()
	provider := &recordingProvider{}
	deps.newTelemetry = func(context.Context, string, config.Telemetry) (telemetryProvider, error) {
		return provider, nil
	}
	listener := &recordingListener{}
	calls := 0
	listenErr := errors.New("management listen failed")
	deps.listen = func(string, string) (net.Listener, error) {
		calls++
		if calls == 1 {
			return listener, nil
		}
		return nil, listenErr
	}

	// when
	err := run(context.Background(), settings, discardLogger(), deps)

	// then
	if !errors.Is(err, listenErr) {
		t.Fatalf("run error = %v", err)
	}
	if listener.closed.Load() != 1 || provider.shutdowns.Load() != 1 {
		t.Errorf(
			"cleanup = listener %d, telemetry %d",
			listener.closed.Load(),
			provider.shutdowns.Load(),
		)
	}
}

func TestRun_TelemetryAcquisitionFailureStopsComposition(t *testing.T) {
	// given
	settings := testConfig(t)
	deps := productionDependencies()
	telemetryErr := errors.New("telemetry initialization failed")
	deps.newTelemetry = func(context.Context, string, config.Telemetry) (telemetryProvider, error) {
		return nil, telemetryErr
	}
	deps.newHealth = func(time.Duration, health.Check) (*health.State, error) {
		t.Fatal("health acquired after telemetry failure")
		return nil, nil
	}

	// when
	err := run(context.Background(), settings, discardLogger(), deps)

	// then
	if !errors.Is(err, telemetryErr) {
		t.Fatalf("run error = %v", err)
	}
}

func TestRun_HealthAcquisitionFailureCleansUpTelemetry(t *testing.T) {
	// given
	settings := testConfig(t)
	deps := productionDependencies()
	provider := &recordingProvider{}
	deps.newTelemetry = func(context.Context, string, config.Telemetry) (telemetryProvider, error) {
		return provider, nil
	}
	healthErr := errors.New("health initialization failed")
	deps.newHealth = func(time.Duration, health.Check) (*health.State, error) {
		return nil, healthErr
	}
	deps.listen = func(string, string) (net.Listener, error) {
		t.Fatal("listener acquired after health failure")
		return nil, nil
	}

	// when
	err := run(context.Background(), settings, discardLogger(), deps)

	// then
	if !errors.Is(err, healthErr) || provider.shutdowns.Load() != 1 {
		t.Fatalf("run error = %v, telemetry shutdowns = %d", err, provider.shutdowns.Load())
	}
}

func TestRun_BusinessAcquisitionFailureCleansUpTelemetry(t *testing.T) {
	// given
	settings := testConfig(t)
	deps := productionDependencies()
	provider := &recordingProvider{}
	deps.newTelemetry = func(context.Context, string, config.Telemetry) (telemetryProvider, error) {
		return provider, nil
	}
	listenErr := errors.New("business listen failed")
	deps.listen = func(string, string) (net.Listener, error) { return nil, listenErr }

	// when
	err := run(context.Background(), settings, discardLogger(), deps)

	// then
	if !errors.Is(err, listenErr) || provider.shutdowns.Load() != 1 {
		t.Fatalf("run error = %v, telemetry shutdowns = %d", err, provider.shutdowns.Load())
	}
}

func TestRun_ServeAndCleanupErrorsArePreservedAndReadinessIsWithdrawn(t *testing.T) {
	// given
	settings := testConfig(t)
	deps := productionDependencies()
	serveErr := errors.New("serve failed")
	cleanupErr := errors.New("telemetry cleanup failed")
	provider := &recordingProvider{err: cleanupErr}
	deps.newTelemetry = func(context.Context, string, config.Telemetry) (telemetryProvider, error) {
		return provider, nil
	}
	var state *health.State
	deps.newHealth = func(timeout time.Duration, check health.Check) (*health.State, error) {
		created, err := health.NewState(timeout, check)
		state = created
		return created, err
	}
	deps.listen = func(string, string) (net.Listener, error) { return &acceptingListener{}, nil }
	business := newFakeServer(serveErr)
	management := newFakeServer(nil)
	deps.newBusiness = func(http.Handler, config.Config) ownedServer { return business }
	deps.newManagement = func(*health.State) ownedServer { return management }

	// when
	err := run(context.Background(), settings, discardLogger(), deps)

	// then
	if !errors.Is(err, serveErr) || !errors.Is(err, cleanupErr) {
		t.Fatalf("run error = %v", err)
	}
	if state == nil || state.Ready() {
		t.Errorf("readiness not withdrawn: %v", state)
	}
	if business.shutdowns.Load() != 1 || management.shutdowns.Load() != 1 ||
		provider.shutdowns.Load() != 1 {
		t.Errorf(
			"shutdown counts = (%d, %d, %d)",
			business.shutdowns.Load(),
			management.shutdowns.Load(),
			provider.shutdowns.Load(),
		)
	}
}

func TestBusinessHandler_DrainRejectsNewWork(t *testing.T) {
	// given
	handler := newBusinessHandler()
	request, err := http.NewRequest(
		http.MethodGet,
		"http://chat.invalid/ws/chat/main/test",
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	recorder := &responseRecorder{header: make(http.Header)}

	// when
	handler.beginDrain()
	handler.ServeHTTP(recorder, request)

	// then
	if recorder.status != http.StatusServiceUnavailable ||
		recorder.body.String() != "service unavailable\n" {
		t.Errorf("response = (%d, %q)", recorder.status, recorder.body.String())
	}
}

func TestSupervise_PublishesReadinessAfterBothServersStart(t *testing.T) {
	// given
	state, err := health.NewState(time.Second, func(context.Context) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	business := newControlledServer()
	management := newControlledServer()
	t.Cleanup(business.releaseServe)
	t.Cleanup(management.releaseServe)
	result := make(chan error, 1)
	go func() {
		result <- supervise(
			ctx,
			discardLogger(),
			state,
			business,
			&acceptingListener{},
			management,
			&acceptingListener{},
		)
	}()

	// when
	business.startServe()
	waitForSignal(t, business.accepted, "business server did not start")
	readyAfterBusiness := state.Ready()
	management.startServe()
	waitForSignal(t, management.accepted, "management server did not start")
	waitForReady(t, state)
	cancel()

	// then
	if readyAfterBusiness {
		t.Error("readiness published before management server started")
	}
	select {
	case superviseErr := <-result:
		if superviseErr != nil {
			t.Fatal(superviseErr)
		}
	case <-time.After(time.Second):
		t.Fatal("supervise did not stop after cancellation")
	}
}

func TestShutdown_OrdersDrainServersAndTelemetry(t *testing.T) {
	// given
	settings := testConfig(t)
	state, err := health.NewState(time.Second, func(context.Context) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	state.SetReady(true)
	handler := newBusinessHandler()
	calls := make([]string, 0, 5)
	registry := &orderedRegistry{calls: &calls}
	business := &orderedServer{name: "business", calls: &calls}
	management := &orderedServer{name: "management", calls: &calls}
	provider := &orderedProvider{calls: &calls}

	// when
	err = shutdown(settings, state, handler, registry, business, management, provider)
	// then
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"registry begin",
		"business shutdown",
		"registry drain",
		"management shutdown",
		"telemetry shutdown",
	}
	if !slices.Equal(calls, want) {
		t.Errorf("shutdown calls = %v, want %v", calls, want)
	}
	if state.Ready() {
		t.Error("readiness remained published during shutdown")
	}
}

func testConfig(t *testing.T) config.Config {
	t.Helper()
	httpAddress, err := kitconfig.ParseAddress(kitconfig.EnvHTTPAddress, "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	healthAddress, err := kitconfig.ParseAddress(kitconfig.EnvHealthAddress, "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	return config.Config{
		HTTPServer:         kitconfig.DefaultHTTPServer(),
		ServiceName:        config.DefaultServiceName,
		HTTPAddress:        httpAddress,
		HealthAddress:      healthAddress,
		HTTPMaxHeaderBytes: kitconfig.DefaultHTTPMaxHeaderBytes,
		ProbeTimeout:       100 * time.Millisecond,
		ShutdownTimeout:    time.Second,
		Telemetry: config.Telemetry{
			Identity: config.DevelopmentIdentity(
				config.DefaultServiceName,
			).Identity,
			ExporterTimeout: 100 * time.Millisecond,
		},
	}
}

func discardLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func receiveString(t *testing.T, values <-chan string) string {
	t.Helper()
	select {
	case value := <-values:
		return value
	case <-time.After(time.Second):
		t.Fatal("listener was not created")
		return ""
	}
}

func waitForResponse(t *testing.T, client *http.Client, url string, status int, body string) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		response, err := client.Get(url)
		if err == nil {
			actual, readErr := io.ReadAll(response.Body)
			_ = response.Body.Close()
			if readErr == nil && response.StatusCode == status &&
				string(actual) == body {
				return
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("%s did not return (%d, %q)", url, status, body)
}

func waitForSignal(t *testing.T, signal <-chan struct{}, failure string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(time.Second):
		t.Fatal(failure)
	}
}

func waitForReady(t *testing.T, state *health.State) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if state.Ready() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("readiness was not published")
}

func assertResponse(t *testing.T, response *http.Response, status int, body string) {
	t.Helper()
	defer func() { _ = response.Body.Close() }()
	actual, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != status || string(actual) != body {
		t.Errorf(
			"response = (%d, %q), want (%d, %q)",
			response.StatusCode,
			actual,
			status,
			body,
		)
	}
}

type recordingProvider struct {
	shutdowns atomic.Int32
	err       error
}

func (provider *recordingProvider) Shutdown(context.Context) error {
	provider.shutdowns.Add(1)
	return provider.err
}

type recordingListener struct{ closed atomic.Int32 }

func (*recordingListener) Accept() (net.Conn, error) { return nil, errors.New("not serving") }
func (*recordingListener) Addr() net.Addr            { return testAddress("recording") }
func (listener *recordingListener) Close() error {
	listener.closed.Add(1)
	return nil
}

type acceptingListener struct{}

func (*acceptingListener) Accept() (net.Conn, error) { return nil, nil }
func (*acceptingListener) Close() error              { return nil }
func (*acceptingListener) Addr() net.Addr            { return testAddress("accepting") }

type testAddress string

func (address testAddress) Network() string { return "test" }
func (address testAddress) String() string  { return string(address) }

type fakeServer struct {
	serveErr  error
	release   chan struct{}
	once      sync.Once
	shutdowns atomic.Int32
}

type controlledServer struct {
	start    chan struct{}
	accepted chan struct{}
	release  chan struct{}
	startOne sync.Once
	stopOne  sync.Once
}

func newControlledServer() *controlledServer {
	return &controlledServer{
		start:    make(chan struct{}),
		accepted: make(chan struct{}),
		release:  make(chan struct{}),
	}
}

func (server *controlledServer) startServe() { server.startOne.Do(func() { close(server.start) }) }
func (server *controlledServer) releaseServe() {
	server.stopOne.Do(func() { close(server.release) })
}

func (server *controlledServer) Serve(listener net.Listener) error {
	<-server.start
	_, _ = listener.Accept()
	close(server.accepted)
	<-server.release
	return http.ErrServerClosed
}
func (*controlledServer) Shutdown(context.Context) error { return nil }
func (*controlledServer) Close() error                   { return nil }

type orderedRegistry struct{ calls *[]string }

func (registry *orderedRegistry) BeginDrain() {
	*registry.calls = append(*registry.calls, "registry begin")
}

func (registry *orderedRegistry) Drain(context.Context) error {
	*registry.calls = append(*registry.calls, "registry drain")
	return nil
}

type orderedServer struct {
	name  string
	calls *[]string
}

func (*orderedServer) Serve(net.Listener) error { return nil }
func (server *orderedServer) Shutdown(context.Context) error {
	*server.calls = append(*server.calls, server.name+" shutdown")
	return nil
}
func (*orderedServer) Close() error { return nil }

type orderedProvider struct{ calls *[]string }

func (provider *orderedProvider) Shutdown(context.Context) error {
	*provider.calls = append(*provider.calls, "telemetry shutdown")
	return nil
}

func newFakeServer(serveErr error) *fakeServer {
	return &fakeServer{serveErr: serveErr, release: make(chan struct{})}
}

func (server *fakeServer) Serve(listener net.Listener) error {
	_, _ = listener.Accept()
	if server.serveErr != nil {
		return server.serveErr
	}
	<-server.release
	return http.ErrServerClosed
}

func (server *fakeServer) Shutdown(context.Context) error {
	server.shutdowns.Add(1)
	server.once.Do(func() { close(server.release) })
	return nil
}

func (server *fakeServer) Close() error {
	server.once.Do(func() { close(server.release) })
	return nil
}

type responseRecorder struct {
	header http.Header
	body   strings.Builder
	status int
}

func (recorder *responseRecorder) Header() http.Header { return recorder.header }
func (recorder *responseRecorder) Write(value []byte) (int, error) {
	return recorder.body.Write(value)
}
func (recorder *responseRecorder) WriteHeader(status int) { recorder.status = status }
