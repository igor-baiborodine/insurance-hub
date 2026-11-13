package service

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	kitconfig "github.com/igor-baiborodine/insurance-hub/libs/servicekit/config"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	documentv1 "github.com/igor-baiborodine/insurance-hub/services/document-service/gen/document/v1"
	"github.com/igor-baiborodine/insurance-hub/services/document-service/internal/application"
	"github.com/igor-baiborodine/insurance-hub/services/document-service/internal/config"
	"github.com/igor-baiborodine/insurance-hub/services/document-service/internal/domain"
)

func TestRun_ComposesHTTPToActualRPCAndTracksDependencyReadiness(t *testing.T) {
	// given
	settings := testConfig()
	deps := productionDependencies()
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
