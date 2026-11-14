package service

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	kitconfig "github.com/igor-baiborodine/insurance-hub/libs/servicekit/config"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
	"go.opentelemetry.io/otel/propagation"
	tracenoop "go.opentelemetry.io/otel/trace/noop"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	documentv1 "github.com/igor-baiborodine/insurance-hub/services/document-service/gen/document/v1"
	"github.com/igor-baiborodine/insurance-hub/services/document-service/internal/application"
	"github.com/igor-baiborodine/insurance-hub/services/document-service/internal/config"
	"github.com/igor-baiborodine/insurance-hub/services/document-service/internal/domain"
	documentkafka "github.com/igor-baiborodine/insurance-hub/services/document-service/internal/kafka"
	"github.com/igor-baiborodine/insurance-hub/services/document-service/internal/telemetry"
)

func TestRun_ComposesHTTPToActualRPCAndTracksDependencyReadiness(t *testing.T) {
	// given
	settings := testConfig()
	deps := productionDependencies()
	deps.newGeneration = func(context.Context, config.Events, int) (generationRuntime, error) {
		t.Fatal("disabled mode constructed generation resources")
		return nil, nil
	}
	deps.newWorker = func(documentkafka.Settings, generationRuntime, *slog.Logger) (eventWorker, error) {
		t.Fatal("disabled mode constructed a Kafka consumer")
		return nil, nil
	}
	addresses := make(chan string, 3)
	deps.listen = func(network, address string) (net.Listener, error) {
		listener, err := net.Listen(network, address)
		if err == nil {
			addresses <- listener.Addr().String()
		}
		return listener, err
	}
	var databaseReady atomic.Bool
	var objectStoreReady atomic.Bool
	databaseReady.Store(true)
	objectStoreReady.Store(true)
	database := &fakeManagedDependency{
		ready:   &databaseReady,
		failure: errors.New("database unavailable"),
	}
	objectStore := &fakeManagedDependency{
		ready:   &objectStoreReady,
		failure: errors.New("object storage unavailable"),
	}
	deps.newDatabase = func(
		context.Context,
		config.Database,
		int,
		int,
	) (application.MetadataReader, managedDependency, error) {
		return metadataReaderFunc(func(
			_ context.Context,
			policyNumber string,
		) ([]domain.StoredDocument, error) {
			return []domain.StoredDocument{{
				PolicyNumber: policyNumber,
				Bytes:        []byte("%PDF-362"),
			}}, nil
		}), database, nil
	}
	deps.newObjectStore = func(
		config.ObjectStore,
		int,
	) (application.ContentReader, managedDependency, error) {
		return contentReaderFunc(func(
			_ context.Context,
			content []byte,
		) ([]byte, bool, error) {
			return content, true, nil
		}), objectStore, nil
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
	response, err := client.Get("http://" + httpAddress + "/documents/SYN-362")
	if err != nil {
		t.Fatal(err)
	}

	// then
	assertResponse(
		t,
		response,
		http.StatusOK,
		`{"documents":[{"policyNumber":"SYN-362","content":"JVBERi0zNjI="}]}`,
	)
	connection, err := grpc.NewClient(
		grpcAddress,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = connection.Close() }()
	direct, err := documentv1.NewDocumentServiceClient(connection).FindDocuments(
		context.Background(),
		&documentv1.FindDocumentsRequest{PolicyNumber: "SYN-362"},
	)
	if err != nil || len(direct.GetDocuments()) != 1 {
		t.Fatalf("direct lookup = (%v, %v)", direct, err)
	}

	// when
	databaseReady.Store(false)

	// then
	waitForHTTPStatus(
		t,
		client,
		"http://"+healthAddress+"/readyz",
		http.StatusServiceUnavailable,
	)
	waitForHTTPStatus(t, client, "http://"+healthAddress+"/livez", http.StatusOK)
	cancel()
	select {
	case err := <-runResult:
		if err != nil {
			t.Fatalf("run service: %v", err)
		}
	case <-time.After(12 * time.Second):
		t.Fatal("service did not stop")
	}
}

func TestRunningService_ConfiguresExactWorkerCoordinatesAndClosesLifecycle(t *testing.T) {
	// given
	caFile := t.TempDir() + "/ca.crt"
	if err := os.WriteFile(caFile, []byte("test CA"), 0o600); err != nil {
		t.Fatal(err)
	}
	settings := config.Events{
		Enabled: true, Brokers: []string{"kafka.example:9093"},
		Group: "document-service", RegisteredTopic: "policy-registered", CAFile: caFile,
	}
	generation := &fakeGenerationRuntime{}
	worker := &fakeEventWorker{}
	deps := productionDependencies()
	deps.newGeneration = func(
		_ context.Context,
		got config.Events,
		maximumBytes int,
	) (generationRuntime, error) {
		if got.Group != "document-service" || maximumBytes != 8*1024*1024 {
			t.Fatalf("generation settings = (%+v, %d)", got, maximumBytes)
		}
		return generation, nil
	}
	deps.newWorker = func(
		got documentkafka.Settings,
		owned generationRuntime,
		_ *slog.Logger,
	) (eventWorker, error) {
		if got.ClientID != "policy-registered-document-listener" ||
			got.Group != "document-service" || got.Topic != "policy-registered" ||
			got.EventType != "registered" || got.Propagator == nil || got.Observe == nil ||
			got.CommitFailure == nil || string(got.CACert) != "test CA" || owned != generation {
			t.Fatalf("worker settings = %+v", got)
		}
		return worker, nil
	}
	eventObserver, err := telemetry.NewEventObserver(
		discardLogger(), metricnoop.NewMeterProvider(), tracenoop.NewTracerProvider(), true,
	)
	if err != nil {
		t.Fatal(err)
	}
	runtime := &runningService{}

	// when
	err = runtime.configureWorker(
		context.Background(), settings, 8*1024*1024, discardLogger(), deps,
		eventObserver, propagation.TraceContext{},
	)
	if err == nil {
		err = runtime.worker.Preflight(context.Background())
	}
	if err == nil {
		runtime.startWorker(context.Background())
		err = runtime.checkWorker(context.Background())
	}
	shutdownErr := runtime.shutdown(time.Second, 100*time.Millisecond)

	// then
	if err != nil || shutdownErr != nil || !worker.started.Load() ||
		!worker.preflighted.Load() || !worker.shutdown.Load() || generation.checks.Load() == 0 ||
		!generation.closed.Load() {
		t.Fatalf(
			"lifecycle = configure:%v shutdown:%v started:%t preflight:%t worker-shutdown:%t generation-checks:%d generation-closed:%t",
			err,
			shutdownErr,
			worker.started.Load(),
			worker.preflighted.Load(),
			worker.shutdown.Load(),
			generation.checks.Load(),
			generation.closed.Load(),
		)
	}
}

func TestRunningService_WorkerConstructionFailureReleasesGeneration(t *testing.T) {
	// given
	caFile := t.TempDir() + "/ca.crt"
	if err := os.WriteFile(caFile, []byte("test CA"), 0o600); err != nil {
		t.Fatal(err)
	}
	generation := &fakeGenerationRuntime{}
	failure := errors.New("construct registration worker")
	deps := productionDependencies()
	deps.newGeneration = func(context.Context, config.Events, int) (generationRuntime, error) {
		return generation, nil
	}
	deps.newWorker = func(documentkafka.Settings, generationRuntime, *slog.Logger) (eventWorker, error) {
		return nil, failure
	}
	eventObserver, err := telemetry.NewEventObserver(
		discardLogger(), metricnoop.NewMeterProvider(), tracenoop.NewTracerProvider(), true,
	)
	if err != nil {
		t.Fatal(err)
	}
	runtime := &runningService{}

	// when
	err = runtime.configureWorker(
		context.Background(),
		config.Events{Enabled: true, CAFile: caFile},
		8*1024*1024,
		discardLogger(),
		deps,
		eventObserver,
		propagation.TraceContext{},
	)
	shutdownErr := runtime.shutdown(time.Second, 100*time.Millisecond)

	// then
	if !errors.Is(err, failure) || shutdownErr != nil || !generation.closed.Load() {
		t.Fatalf(
			"failure = %v, shutdown = %v, generation closed = %t",
			err,
			shutdownErr,
			generation.closed.Load(),
		)
	}
}

func TestHandlerTracker_UsesUnavailableHandlerAfterDrain(t *testing.T) {
	// given
	tracker := newHandlerTracker(
		http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			writer.WriteHeader(http.StatusOK)
		}),
		http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			writer.WriteHeader(http.StatusServiceUnavailable)
		}),
	)
	tracker.beginDrain()
	recorder := &responseRecorder{}

	// when
	tracker.ServeHTTP(recorder, &http.Request{})

	// then
	if recorder.status != http.StatusServiceUnavailable {
		t.Fatalf("status = %d", recorder.status)
	}
}

func TestInternalDialTarget_NormalizesWildcardFamilies(t *testing.T) {
	// given
	tests := []struct {
		address string
		want    string
	}{
		{"0.0.0.0:9090", "127.0.0.1:9090"},
		{"[::]:9090", "[::1]:9090"},
		{"127.0.0.1:9090", "127.0.0.1:9090"},
	}
	for _, test := range tests {
		// when
		got, err := internalDialTarget(fakeAddress(test.address))

		// then
		if err != nil || got != test.want {
			t.Errorf(
				"target(%s) = (%q, %v), want %q",
				test.address,
				got,
				err,
				test.want,
			)
		}
	}
}

type metadataReaderFunc func(context.Context, string) ([]domain.StoredDocument, error)

func (function metadataReaderFunc) FindByPolicyNumber(
	ctx context.Context,
	policyNumber string,
) ([]domain.StoredDocument, error) {
	return function(ctx, policyNumber)
}

type contentReaderFunc func(context.Context, []byte) ([]byte, bool, error)

func (function contentReaderFunc) Read(
	ctx context.Context,
	stored []byte,
) ([]byte, bool, error) {
	return function(ctx, stored)
}

func TestObservedContentReader_RecordsOnlyObjectFetches(t *testing.T) {
	// given
	var output bytes.Buffer
	observer, err := telemetry.NewDependencyObserver(
		slog.New(slog.NewJSONHandler(&output, nil)),
		metricnoop.NewMeterProvider(),
		tracenoop.NewTracerProvider(),
	)
	if err != nil {
		t.Fatal(err)
	}
	reader := newObservedContentReader(
		contentReaderFunc(func(_ context.Context, stored []byte) ([]byte, bool, error) {
			if string(stored) == "inline" {
				return stored, true, nil
			}
			return nil, false, nil
		}),
		observer,
	)

	// when
	if _, present, readErr := reader.Read(
		context.Background(),
		[]byte("inline"),
	); readErr != nil ||
		!present {
		t.Fatalf("inline read = (%t, %v)", present, readErr)
	}

	// then
	if output.Len() != 0 {
		t.Fatalf("inline dependency signal = %s", output.Bytes())
	}

	// when
	if _, present, readErr := reader.Read(
		context.Background(),
		[]byte("2026/02/missing.pdf"),
	); readErr != nil || present {
		t.Fatalf("object read = (%t, %v)", present, readErr)
	}

	// then
	if !bytes.Contains(output.Bytes(), []byte(`"dependency":"object_store"`)) ||
		!bytes.Contains(output.Bytes(), []byte(`"outcome":"fetch_failed"`)) ||
		bytes.Contains(output.Bytes(), []byte("missing.pdf")) {
		t.Fatalf("object dependency signal = %s", output.Bytes())
	}
}

type fakeManagedDependency struct {
	ready   *atomic.Bool
	failure error
	closed  atomic.Bool
}

type fakeGenerationRuntime struct {
	checks atomic.Int64
	closed atomic.Bool
}

func (*fakeGenerationRuntime) Execute(
	context.Context,
	domain.PolicyRegistration,
) (application.GenerationResult, error) {
	return application.GenerationResult{}, nil
}

func (generation *fakeGenerationRuntime) Check(context.Context) error {
	generation.checks.Add(1)
	return nil
}

func (generation *fakeGenerationRuntime) Close() { generation.closed.Store(true) }

type fakeEventWorker struct {
	preflighted atomic.Bool
	started     atomic.Bool
	shutdown    atomic.Bool
}

func (worker *fakeEventWorker) Preflight(context.Context) error {
	worker.preflighted.Store(true)
	return nil
}

func (*fakeEventWorker) Check(context.Context) error { return nil }

func (worker *fakeEventWorker) Run(ctx context.Context) error {
	worker.started.Store(true)
	<-ctx.Done()
	return nil
}

func (worker *fakeEventWorker) Shutdown(context.Context) error {
	worker.shutdown.Store(true)
	return nil
}

func (dependency *fakeManagedDependency) Check(context.Context) error {
	if !dependency.ready.Load() {
		return dependency.failure
	}
	return nil
}

func (dependency *fakeManagedDependency) Close() { dependency.closed.Store(true) }

func testConfig() config.Config {
	identity := config.DevelopmentIdentity(config.DefaultServiceName)
	return config.Config{
		Runtime: kitconfig.Runtime{
			RequestTimeout:  100 * time.Millisecond,
			ProbeTimeout:    100 * time.Millisecond,
			ShutdownTimeout: 10 * time.Second,
			LogLevel:        slog.LevelInfo,
		},
		GRPC: kitconfig.GRPC{MaxReceiveBytes: 1024 * 1024, MaxSendBytes: 1024 * 1024},
		HTTPServer: kitconfig.HTTPServer{
			ReadHeaderTimeout: 100 * time.Millisecond,
			ReadTimeout:       200 * time.Millisecond,
			WriteTimeout:      200 * time.Millisecond,
			IdleTimeout:       time.Second,
		},
		Telemetry: kitconfig.Telemetry{
			Identity: identity.Identity, ExporterTimeout: 50 * time.Millisecond,
		},
		ServiceName: config.DefaultServiceName,
		GRPCAddress: mustAddress("127.0.0.1:0"), HTTPAddress: mustAddress("127.0.0.1:0"),
		HealthAddress: mustAddress("127.0.0.1:0"), HTTPMaxHeaderBytes: 16 * 1024,
		HTTPMaxBodyBytes: 4 * 1024, HTTPMaxResponseBytes: 24 * 1024 * 1024,
		Database: config.Database{
			MaxConnections: 1, ConnectTimeout: time.Second,
			AcquireTimeout: time.Second, QueryTimeout: time.Second,
		},
		StartupTimeout: time.Second, MaximumDocumentBytes: 8 * 1024 * 1024,
		MaximumResponseBytes: 16 * 1024 * 1024, MaximumConcurrentLookups: 2,
	}
}

func mustAddress(value string) config.Address {
	address, err := kitconfig.ParseAddress("test", value)
	if err != nil {
		panic(err)
	}
	return address
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewJSONHandler(io.Discard, nil))
}

func receiveString(t *testing.T, values <-chan string) string {
	t.Helper()
	select {
	case value := <-values:
		return value
	case <-time.After(time.Second):
		t.Fatal("listener address not received")
		return ""
	}
}

func waitForHTTPStatus(
	t *testing.T,
	client *http.Client,
	endpoint string,
	want int,
) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		response, err := client.Get(endpoint)
		if err == nil {
			_ = response.Body.Close()
			if response.StatusCode == want {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("%s did not return %d", endpoint, want)
}

func assertResponse(t *testing.T, response *http.Response, status int, body string) {
	t.Helper()
	defer func() { _ = response.Body.Close() }()
	actual, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != status || strings.TrimSpace(string(actual)) != body {
		t.Fatalf(
			"response = (%d, %q), want (%d, %q)",
			response.StatusCode,
			actual,
			status,
			body,
		)
	}
}

type responseRecorder struct{ status int }

func (recorder *responseRecorder) Header() http.Header            { return make(http.Header) }
func (recorder *responseRecorder) WriteHeader(status int)         { recorder.status = status }
func (recorder *responseRecorder) Write(body []byte) (int, error) { return len(body), nil }

type fakeAddress string

func (address fakeAddress) Network() string { return "tcp" }
func (address fakeAddress) String() string  { return string(address) }
