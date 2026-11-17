package service

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	kitconfig "github.com/igor-baiborodine/insurance-hub/libs/servicekit/config"
	"github.com/igor-baiborodine/insurance-hub/libs/servicekit/health"

	"github.com/igor-baiborodine/insurance-hub/services/chat-service/internal/config"
)

func TestRunServesManagementAndExactBusinessNotFound(t *testing.T) {
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
	go func() { result <- run(ctx, settings, discardLogger(), deps) }()
	businessAddress := receiveString(t, addresses)
	managementAddress := receiveString(t, addresses)
	client := &http.Client{Timeout: time.Second}
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

func TestRunCleansUpBusinessListenerWhenManagementAcquisitionFails(t *testing.T) {
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

	err := run(context.Background(), settings, discardLogger(), deps)
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

func TestRunPreservesServeAndCleanupErrorsAndWithdrawsReadiness(t *testing.T) {
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

	err := run(context.Background(), settings, discardLogger(), deps)
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

func TestBusinessHandlerRejectsNewWorkAfterDrain(t *testing.T) {
	handler := newBusinessHandler()
	handler.beginDrain()
	request, err := http.NewRequest(
		http.MethodGet,
		"http://chat.invalid/ws/chat/main/test",
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	recorder := &responseRecorder{header: make(http.Header)}
	handler.ServeHTTP(recorder, request)
	if recorder.status != http.StatusServiceUnavailable ||
		recorder.body.String() != "service unavailable\n" {
		t.Errorf("response = (%d, %q)", recorder.status, recorder.body.String())
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
