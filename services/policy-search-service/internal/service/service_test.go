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
	"testing"
	"time"

	kitconfig "github.com/igor-baiborodine/insurance-hub/libs/servicekit/config"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	policysearchv1 "github.com/igor-baiborodine/insurance-hub/services/policy-search-service/gen/policysearch/v1"
	"github.com/igor-baiborodine/insurance-hub/services/policy-search-service/internal/config"
	"github.com/igor-baiborodine/insurance-hub/services/policy-search-service/internal/domain"
	"github.com/igor-baiborodine/insurance-hub/services/policy-search-service/internal/telemetry"
)

func TestRun_ProductionCompositionUsesActualRPCListenerAndGeneratedGateway(t *testing.T) {
	dateFrom, _ := domain.ParseDate("2026-01-02")
	query := &fakeQuery{policies: []domain.Policy{{
		Number: "POL-1", DateFrom: &dateFrom, PolicyHolder: "Synthetic Person",
	}}}
	settings := testConfig(t)
	deps, addresses := testDependencies(query)
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() { result <- run(ctx, settings, discardLogger(), deps) }()
	grpcAddress := receiveString(t, addresses)
	httpAddress := receiveString(t, addresses)
	healthAddress := receiveString(t, addresses)
	waitForHTTPStatus(t, "http://"+healthAddress+"/readyz", http.StatusOK)

	request, err := http.NewRequest(
		http.MethodGet,
		"http://"+httpAddress+`/policies?q=holder%3A%22Synthetic+Person%22&q=ignored`,
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer private")
	request.Header.Set("Grpc-Metadata-Principal", "forged")
	response, err := (&http.Client{Timeout: time.Second}).Do(request)
	if err != nil {
		t.Fatal(err)
	}
	assertResponse(
		t,
		response,
		http.StatusOK,
		`{"policies":[{"number":"POL-1","dateFrom":"2026-01-02","policyHolder":"Synthetic Person"}]}`,
	)
	if got := query.lastQuery(); got != `holder:"Synthetic Person"` {
		t.Fatalf("query = %q", got)
	}
	for _, key := range []string{"authorization", "principal", "forwarded", "x-forwarded-for"} {
		if values := query.lastMetadata().Get(key); len(values) != 0 {
			t.Errorf("metadata %s escaped: %v", key, values)
		}
	}

	connection, err := grpc.NewClient(
		grpcAddress,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatal(err)
	}
	client := policysearchv1.NewPolicySearchServiceClient(connection)
	direct, err := client.SearchPolicies(
		context.Background(),
		&policysearchv1.SearchPoliciesRequest{Q: proto.String("direct")},
	)
	if err != nil || len(direct.GetPolicies()) != 1 {
		t.Fatalf("direct RPC = (%v, %v)", direct, err)
	}
	_, err = client.SearchPolicies(
		context.Background(),
		&policysearchv1.SearchPoliciesRequest{},
	)
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("absent q status = %v", status.Code(err))
	}
	_ = connection.Close()

	missing, err := http.Get("http://" + httpAddress + "/policies")
	if err != nil {
		t.Fatal(err)
	}
	assertResponse(
		t,
		missing,
		http.StatusBadRequest,
		`{"message":"Required QueryValue [q] not specified","path":"/queryText","_links":{"self":{"href":"/policies","templated":false}}}`,
	)

	cancel()
	if err := receiveError(t, result); err != nil {
		t.Fatalf("run = %v", err)
	}
	if !query.closed {
		t.Error("Elasticsearch idle connections were not closed")
	}
}

func TestRun_ReadinessTracksElasticsearchAndRecovers(t *testing.T) {
	query := &fakeQuery{}
	settings := testConfig(t)
	deps, addresses := testDependencies(query)
	connection := newStatefulConnection(connectivity.Ready)
	deps.newConnection = func(string, telemetryProvider, config.Config) (gatewayConnection, error) {
		return connection, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() { result <- run(ctx, settings, discardLogger(), deps) }()
	_ = receiveString(t, addresses)
	_ = receiveString(t, addresses)
	healthAddress := receiveString(t, addresses)
	waitForHTTPStatus(t, "http://"+healthAddress+"/readyz", http.StatusOK)
	query.setCheckError(errors.New("dependency unavailable"))
	waitForHTTPStatus(t, "http://"+healthAddress+"/readyz", http.StatusServiceUnavailable)
	query.setCheckError(nil)
	waitForHTTPStatus(t, "http://"+healthAddress+"/readyz", http.StatusOK)
	connection.setState(connectivity.TransientFailure)
	waitForHTTPStatus(t, "http://"+healthAddress+"/readyz", http.StatusServiceUnavailable)
	connection.setState(connectivity.Ready)
	waitForHTTPStatus(t, "http://"+healthAddress+"/readyz", http.StatusOK)
	cancel()
	if err := receiveError(t, result); err != nil {
		t.Fatalf("run = %v", err)
	}
}

func TestRun_FailsStartupWhenElasticsearchIsUnavailableAndCleansUp(t *testing.T) {
	query := &fakeQuery{checkErr: errors.New("private dependency diagnostic")}
	settings := testConfig(t)
	deps, _ := testDependencies(query)
	err := run(context.Background(), settings, discardLogger(), deps)
	if err == nil || !strings.Contains(err.Error(), "Elasticsearch not ready") {
		t.Fatalf("run error = %v", err)
	}
	if !query.closed {
		t.Error("query runtime not closed after startup failure")
	}
}

func TestRun_PartialBindFailureClosesEarlierListeners(t *testing.T) {
	query := &fakeQuery{}
	settings := testConfig(t)
	deps, _ := testDependencies(query)
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
	err := run(context.Background(), settings, discardLogger(), deps)
	if !errors.Is(err, bindFailure) {
		t.Fatalf("run error = %v", err)
	}
	for _, listener := range listeners {
		if _, err := listener.Accept(); !errors.Is(err, net.ErrClosed) {
			t.Errorf("listener remained open: %v", err)
		}
	}
}

func TestRun_ForcesStalledSearchWithinShutdownBudget(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	query := &fakeQuery{search: func(context.Context, string) ([]domain.Policy, error) {
		close(started)
		<-release
		return nil, nil
	}}
	settings := testConfig(t)
	settings.RequestTimeout = 50 * time.Millisecond
	settings.ShutdownTimeout = 200 * time.Millisecond
	settings.ExporterTimeout = 50 * time.Millisecond
	deps, addresses := testDependencies(query)
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() { result <- run(ctx, settings, discardLogger(), deps) }()
	_ = receiveString(t, addresses)
	httpAddress := receiveString(t, addresses)
	healthAddress := receiveString(t, addresses)
	waitForHTTPStatus(t, "http://"+healthAddress+"/readyz", http.StatusOK)
	requestDone := make(chan struct{})
	go func() {
		defer close(requestDone)
		response, err := http.Get("http://" + httpAddress + "/policies?q=stall")
		if err == nil {
			_ = response.Body.Close()
		}
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("stalled search did not start")
	}
	shutdownStarted := time.Now()
	cancel()
	err := receiveError(t, result)
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

func TestHandlerTracker_RejectsNewRequestsDuringDrain(t *testing.T) {
	next := http.HandlerFunc(
		func(writer http.ResponseWriter, _ *http.Request) { writer.WriteHeader(204) },
	)
	unavailable := http.HandlerFunc(
		func(writer http.ResponseWriter, _ *http.Request) { writer.WriteHeader(503) },
	)
	tracker := newHandlerTracker(next, unavailable)
	tracker.beginDrain()
	response := &statusRecorder{header: make(http.Header)}
	tracker.ServeHTTP(response, &http.Request{})
	if response.status != 503 {
		t.Fatalf("status = %d", response.status)
	}
}

func TestInternalDialTarget_NormalizesWildcardFamilies(t *testing.T) {
	for _, test := range []struct{ input, want string }{
		{"0.0.0.0:1234", "127.0.0.1:1234"},
		{"[::]:1234", "[::1]:1234"},
		{"127.0.0.2:1234", "127.0.0.2:1234"},
	} {
		got, err := internalDialTarget(testAddress(test.input))
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

type fakeQuery struct {
	mu        sync.Mutex
	policies  []domain.Policy
	queries   []string
	metadata  metadata.MD
	search    func(context.Context, string) ([]domain.Policy, error)
	searchErr error
	checkErr  error
	closed    bool
}

func (query *fakeQuery) SearchPolicies(ctx context.Context, value string) ([]domain.Policy, error) {
	query.mu.Lock()
	query.queries = append(query.queries, value)
	query.metadata, _ = metadata.FromIncomingContext(ctx)
	search := query.search
	policies, err := query.policies, query.searchErr
	query.mu.Unlock()
	if search != nil {
		return search(ctx, value)
	}
	return policies, err
}

func (query *fakeQuery) Check(context.Context) error {
	query.mu.Lock()
	defer query.mu.Unlock()
	return query.checkErr
}

func (query *fakeQuery) CloseIdleConnections() {
	query.mu.Lock()
	query.closed = true
	query.mu.Unlock()
}

func (query *fakeQuery) setCheckError(err error) {
	query.mu.Lock()
	query.checkErr = err
	query.mu.Unlock()
}

func (query *fakeQuery) lastQuery() string {
	query.mu.Lock()
	defer query.mu.Unlock()
	return query.queries[len(query.queries)-1]
}

func (query *fakeQuery) lastMetadata() metadata.MD {
	query.mu.Lock()
	defer query.mu.Unlock()
	return query.metadata.Copy()
}

func testDependencies(query queryRuntime) (dependencies, <-chan string) {
	deps := productionDependencies()
	addresses := make(chan string, 3)
	deps.listen = func(network, address string) (net.Listener, error) {
		listener, err := net.Listen(network, address)
		if err == nil {
			addresses <- listener.Addr().String()
		}
		return listener, err
	}
	deps.newQuery = func(config.Elasticsearch, *telemetry.DependencyObserver) (queryRuntime, error) {
		return query, nil
	}
	return deps, addresses
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
			ProbeTimeout:    100 * time.Millisecond,
			ShutdownTimeout: 2 * time.Second,
		},
		GRPC: kitconfig.GRPC{MaxReceiveBytes: 1048576, MaxSendBytes: 8388608},
		HTTPServer: kitconfig.HTTPServer{
			ReadHeaderTimeout: time.Second,
			ReadTimeout:       time.Second,
			WriteTimeout:      time.Second,
			IdleTimeout:       time.Second,
		},
		Telemetry:          config.Telemetry{ExporterTimeout: 200 * time.Millisecond},
		ServiceName:        "policy-search-service",
		HTTPAddress:        parse(config.EnvHTTPAddress),
		GRPCAddress:        parse(kitconfig.EnvGRPCAddress),
		HealthAddress:      parse(kitconfig.EnvHealthAddress),
		HTTPMaxHeaderBytes: 16384,
		HTTPMaxBodyBytes:   4096,
		Elasticsearch: config.Elasticsearch{
			URL:     "http://127.0.0.1:9200",
			Timeout: 5 * time.Second,
		},
	}
}

func waitForHTTPStatus(t *testing.T, target string, want int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		response, err := (&http.Client{Timeout: 200 * time.Millisecond}).Get(target)
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

func assertResponse(t *testing.T, response *http.Response, wantStatus int, wantBody string) {
	t.Helper()
	defer func() { _ = response.Body.Close() }()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != wantStatus || strings.TrimSpace(string(body)) != wantBody {
		t.Fatalf(
			"response = (%d, %q), want (%d, %q)",
			response.StatusCode,
			body,
			wantStatus,
			wantBody,
		)
	}
}

func receiveString(t *testing.T, values <-chan string) string {
	t.Helper()
	select {
	case value := <-values:
		return value
	case <-time.After(time.Second):
		t.Fatal("listener address not reported")
		return ""
	}
}

func receiveError(t *testing.T, values <-chan error) error {
	t.Helper()
	select {
	case err := <-values:
		return err
	case <-time.After(3 * time.Second):
		t.Fatal("service did not stop")
		return nil
	}
}

func discardLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

type testAddress string

func (testAddress) Network() string        { return "tcp" }
func (address testAddress) String() string { return string(address) }

type statusRecorder struct {
	header http.Header
	status int
}

func (recorder *statusRecorder) Header() http.Header        { return recorder.header }
func (recorder *statusRecorder) Write([]byte) (int, error)  { return 0, nil }
func (recorder *statusRecorder) WriteHeader(statusCode int) { recorder.status = statusCode }

type statefulConnection struct {
	mu      sync.Mutex
	state   connectivity.State
	changed chan struct{}
}

func newStatefulConnection(state connectivity.State) *statefulConnection {
	return &statefulConnection{state: state, changed: make(chan struct{})}
}

func (*statefulConnection) Invoke(
	context.Context,
	string,
	any,
	any,
	...grpc.CallOption,
) error {
	return errors.New("unexpected RPC invocation")
}

func (*statefulConnection) NewStream(
	context.Context,
	*grpc.StreamDesc,
	string,
	...grpc.CallOption,
) (grpc.ClientStream, error) {
	return nil, errors.New("unexpected RPC stream")
}

func (*statefulConnection) Connect() {}

func (connection *statefulConnection) GetState() connectivity.State {
	connection.mu.Lock()
	defer connection.mu.Unlock()
	return connection.state
}

func (connection *statefulConnection) WaitForStateChange(
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

func (*statefulConnection) Close() error { return nil }

func (connection *statefulConnection) setState(state connectivity.State) {
	connection.mu.Lock()
	connection.state = state
	close(connection.changed)
	connection.changed = make(chan struct{})
	connection.mu.Unlock()
}
