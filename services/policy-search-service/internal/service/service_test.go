package service

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/igor-baiborodine/insurance-hub/libs/servicekit/health"

	"github.com/igor-baiborodine/insurance-hub/services/policy-search-service/internal/config"
	"github.com/igor-baiborodine/insurance-hub/services/policy-search-service/internal/telemetry"
)

type startSignalHandler struct {
	started chan struct{}
	once    sync.Once
}

func (handler *startSignalHandler) Enabled(context.Context, slog.Level) bool { return true }

func (handler *startSignalHandler) Handle(_ context.Context, record slog.Record) error {
	if record.Message == "policy search service started" {
		handler.once.Do(func() { close(handler.started) })
	}
	return nil
}

func (handler *startSignalHandler) WithAttrs([]slog.Attr) slog.Handler { return handler }

func (handler *startSignalHandler) WithGroup(string) slog.Handler { return handler }

func foundationSettings(t *testing.T) config.Config {
	t.Helper()
	return loadFoundationSettings(t, reserveAddress(t), reserveAddress(t))
}

func loadFoundationSettings(t *testing.T, httpAddress, healthAddress string) config.Config {
	t.Helper()
	elasticsearchServer := httptest.NewServer(http.HandlerFunc(
		func(writer http.ResponseWriter, request *http.Request) {
			writer.Header().Set("X-Elastic-Product", "Elasticsearch")
			writer.Header().Set("Content-Type", "application/json")
			switch request.URL.Path {
			case "/":
				_, _ = writer.Write([]byte(`{"version":{"number":"8.19.6"}}`))
			case "/policy-views/_search":
				_, _ = writer.Write([]byte(`{"hits":{"hits":[]}}`))
			default:
				http.NotFound(writer, request)
			}
		},
	))
	t.Cleanup(elasticsearchServer.Close)
	return loadSettingsForElasticsearch(t, httpAddress, healthAddress, elasticsearchServer.URL)
}

func loadSettingsForElasticsearch(
	t *testing.T,
	httpAddress string,
	healthAddress string,
	elasticsearchURL string,
) config.Config {
	t.Helper()
	t.Setenv("HTTP_ADDR", httpAddress)
	t.Setenv("HEALTH_ADDR", healthAddress)
	t.Setenv("ELASTICSEARCH_URL", elasticsearchURL)
	t.Setenv("OTEL_ENABLED", "false")
	settings, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	settings.ShutdownTimeout = time.Second
	return settings
}

func TestRun_ComposesElasticsearchUseCaseAndHTTPMapping(t *testing.T) {
	elasticsearchServer := httptest.NewServer(http.HandlerFunc(
		func(writer http.ResponseWriter, request *http.Request) {
			writer.Header().Set("X-Elastic-Product", "Elasticsearch")
			writer.Header().Set("Content-Type", "application/json")
			switch request.URL.Path {
			case "/":
				_, _ = writer.Write([]byte(`{"version":{"number":"8.19.6"}}`))
			case "/policy-views/_search":
				_, _ = writer.Write([]byte(`{"hits":{"hits":[{"_source":{` +
					`"number":"POL-229","dateFrom":"2024-02-29",` +
					`"policyHolder":"Synthetic Holder"}}]}}`))
			default:
				http.NotFound(writer, request)
			}
		},
	))
	t.Cleanup(elasticsearchServer.Close)
	settings := loadSettingsForElasticsearch(
		t, reserveAddress(t), reserveAddress(t), elasticsearchServer.URL,
	)
	handler := &startSignalHandler{started: make(chan struct{})}
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() { result <- Run(ctx, settings, slog.New(handler)) }()
	select {
	case <-handler.started:
	case <-time.After(2 * time.Second):
		t.Fatal("service did not start")
	}
	t.Cleanup(func() {
		cancel()
		select {
		case <-result:
		case <-time.After(2 * time.Second):
			t.Error("service did not stop")
		}
	})

	response, err := http.Get(
		"http://" + settings.HTTPAddress.String() + "/policies?q=number%3A%22POL-229%22",
	)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"policies":[{"number":"POL-229","dateFrom":"2024-02-29",` +
		`"policyHolder":"Synthetic Holder"}]}`
	if response.StatusCode != http.StatusOK || string(body) != want {
		t.Fatalf(
			"composed HTTP response = (%d, %q), want (200, %q)",
			response.StatusCode,
			body,
			want,
		)
	}
}

func TestRun_RemainsLiveAndUnreadyWhenElasticsearchIsUnavailable(t *testing.T) {
	elasticsearchAddress := reserveAddress(t)
	settings := loadSettingsForElasticsearch(
		t, reserveAddress(t), reserveAddress(t), "http://"+elasticsearchAddress,
	)
	settings.Elasticsearch.Timeout = 20 * time.Millisecond
	handler := &startSignalHandler{started: make(chan struct{})}
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() { result <- Run(ctx, settings, slog.New(handler)) }()
	select {
	case <-handler.started:
	case <-time.After(2 * time.Second):
		t.Fatal("service did not start")
	}

	liveStatus := getStatus(t, "http://"+settings.HealthAddress.String()+"/livez")
	readyStatus := getStatus(t, "http://"+settings.HealthAddress.String()+"/readyz")
	response, err := http.Get("http://" + settings.HTTPAddress.String() + "/policies?q=*")
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if liveStatus != http.StatusOK || readyStatus != http.StatusServiceUnavailable ||
		response.StatusCode != http.StatusInternalServerError ||
		strings.Contains(string(body), elasticsearchAddress) {
		t.Fatalf(
			"dependency-loss responses = live %d ready %d search (%d, %q)",
			liveStatus,
			readyStatus,
			response.StatusCode,
			body,
		)
	}
	cancel()
	select {
	case err := <-result:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("service did not stop")
	}
}

func reserveAddress(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	return address
}

func getStatus(t *testing.T, url string) int {
	t.Helper()
	client := &http.Client{Timeout: time.Second}
	response, err := client.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	if _, err := io.Copy(io.Discard, response.Body); err != nil {
		t.Fatal(err)
	}
	return response.StatusCode
}

func TestNewBusinessServer_UsesConfiguredBounds(t *testing.T) {
	// given
	bounds := config.HTTPServer{
		ReadHeaderTimeout: 750 * time.Millisecond,
		ReadTimeout:       11 * time.Second,
		WriteTimeout:      12 * time.Second,
		IdleTimeout:       31 * time.Second,
	}

	// when
	server := newBusinessServer(bounds, http.NotFoundHandler())

	// then
	if server.ReadHeaderTimeout != bounds.ReadHeaderTimeout ||
		server.ReadTimeout != bounds.ReadTimeout ||
		server.WriteTimeout != bounds.WriteTimeout ||
		server.IdleTimeout != bounds.IdleTimeout {
		t.Errorf("business HTTP bounds = %+v, want %+v", server, bounds)
	}
}

func TestRun_ServesPolicySearchAndWithdrawsOnCancel(t *testing.T) {
	// given
	settings := foundationSettings(t)
	handler := &startSignalHandler{started: make(chan struct{})}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	result := make(chan error, 1)
	go func() { result <- Run(ctx, settings, slog.New(handler)) }()
	select {
	case <-handler.started:
	case <-time.After(2 * time.Second):
		t.Fatal("service did not start")
	}

	// when
	businessStatus := getStatus(t, "http://"+settings.HTTPAddress.String()+"/policies?q=")
	liveStatus := getStatus(t, "http://"+settings.HealthAddress.String()+"/livez")
	readyStatus := getStatus(t, "http://"+settings.HealthAddress.String()+"/readyz")
	cancel()

	// then
	if businessStatus != http.StatusOK || liveStatus != http.StatusOK ||
		readyStatus != http.StatusOK {
		t.Errorf(
			"foundation statuses = (%d, %d, %d), want (%d, %d, %d)",
			businessStatus,
			liveStatus,
			readyStatus,
			http.StatusOK,
			http.StatusOK,
			http.StatusOK,
		)
	}
	select {
	case err := <-result:
		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("service did not stop")
	}
}

func TestRun_ClosesFirstListenerWhenSecondBindFails(t *testing.T) {
	// given
	businessAddress := reserveAddress(t)
	healthListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = healthListener.Close() })
	settings := loadFoundationSettings(t, businessAddress, healthListener.Addr().String())

	// when
	runErr := Run(
		context.Background(),
		settings,
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)

	// then
	if runErr == nil || !strings.Contains(runErr.Error(), "listen for management HTTP") {
		t.Fatalf("Run() error = %v, want management listener failure", runErr)
	}
	rebound, err := net.Listen("tcp", businessAddress)
	if err != nil {
		t.Fatalf("business listener remained owned after startup failure: %v", err)
	}
	if err := rebound.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestRun_RejectsMissingLogger(t *testing.T) {
	settings := foundationSettings(t)
	if err := Run(context.Background(), settings, nil); err == nil {
		t.Fatal("nil logger accepted")
	}
}

func TestExpectedServeError_RecognizesOwnedHTTPShutdown(t *testing.T) {
	// given
	tests := []struct {
		err  error
		want bool
	}{
		{want: true},
		{err: http.ErrServerClosed, want: true},
		{err: context.Canceled},
	}

	for _, test := range tests {
		// when
		got := expectedServeError(test.err)

		// then
		if got != test.want {
			t.Errorf("expectedServeError(%v) = %t, want %t", test.err, got, test.want)
		}
	}
}

func TestWaitForRunStop_PreservesFatalServingError(t *testing.T) {
	// given
	cause := errors.New("fatal listener failure")
	results := make(chan serveResult, 1)
	results <- serveResult{name: "HTTP", err: cause}

	// when
	err := waitForRunStop(context.Background(), results)

	// then
	if !errors.Is(err, cause) || err.Error() != "serve HTTP: fatal listener failure" {
		t.Fatalf("waitForRunStop() error = %v", err)
	}
}

func TestWaitForRunStop_AcceptsSignalAndOwnedServerStop(t *testing.T) {
	// given
	canceledCtx, cancel := context.WithCancel(context.Background())
	cancel()
	ownedResults := make(chan serveResult, 1)
	ownedResults <- serveResult{name: "HTTP", err: http.ErrServerClosed}

	// when
	signalErr := waitForRunStop(canceledCtx, make(chan serveResult))
	ownedErr := waitForRunStop(context.Background(), ownedResults)

	// then
	if signalErr != nil || ownedErr != nil {
		t.Fatalf("signal/owned stop errors = %v/%v", signalErr, ownedErr)
	}
}

func TestRuntimeResources_Shutdown_MarksUnreadyAndSeparatesTelemetry(t *testing.T) {
	// given
	provider, err := telemetry.New(
		context.Background(),
		config.DefaultServiceName,
		config.Telemetry{Enabled: false, ExporterTimeout: time.Second},
	)
	if err != nil {
		t.Fatal(err)
	}
	healthState, err := health.NewState(time.Second, func(context.Context) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	healthState.SetReady(true)
	resources := runtimeResources{
		provider:         provider,
		healthState:      healthState,
		httpServer:       &http.Server{},
		managementServer: &http.Server{},
	}

	// when
	shutdownErr, telemetryErr := resources.shutdown(3 * time.Second)

	// then
	if shutdownErr != nil || telemetryErr != nil {
		t.Fatalf("shutdown errors = %v; telemetry = %v", shutdownErr, telemetryErr)
	}
	if healthState.Ready() {
		t.Error("health state remained ready after shutdown")
	}
}

func TestFinishRun_ReportsTelemetryFailureSeparately(t *testing.T) {
	// given
	serveErr := errors.New("serve failure")
	shutdownErr := errors.New("shutdown failure")
	telemetryErr := errors.New("private telemetry detail")
	var output bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&output, nil))

	// when
	err := finishRun(logger, serveErr, shutdownErr, telemetryErr)

	// then
	if !errors.Is(err, serveErr) || !errors.Is(err, shutdownErr) ||
		errors.Is(err, telemetryErr) {
		t.Fatalf("finishRun() error = %v", err)
	}
	if !strings.Contains(output.String(), "telemetry shutdown incomplete") ||
		strings.Contains(output.String(), telemetryErr.Error()) {
		t.Fatalf("telemetry warning = %q", output.String())
	}
}

func TestRuntimeResources_CloseAfterStartupFailure_ReleasesListeners(t *testing.T) {
	// given
	provider, err := telemetry.New(
		context.Background(),
		config.DefaultServiceName,
		config.Telemetry{Enabled: false, ExporterTimeout: time.Second},
	)
	if err != nil {
		t.Fatal(err)
	}
	business, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	management, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		_ = business.Close()
		t.Fatal(err)
	}
	resources := runtimeResources{
		provider:           provider,
		httpListener:       business,
		managementListener: management,
	}

	// when
	cleanupErr := resources.closeAfterStartupFailure(time.Second)

	// then
	if cleanupErr != nil {
		t.Fatalf("closeAfterStartupFailure() error = %v", cleanupErr)
	}
	for _, listener := range []net.Listener{business, management} {
		connection, dialErr := net.DialTimeout(
			"tcp",
			listener.Addr().String(),
			20*time.Millisecond,
		)
		if dialErr == nil {
			_ = connection.Close()
			t.Errorf("listener %s still accepts connections", listener.Addr())
		}
	}
}
