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
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.opentelemetry.io/otel/metric"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
	"go.opentelemetry.io/otel/propagation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
	"google.golang.org/grpc/metadata"

	"github.com/igor-baiborodine/insurance-hub/services/dashboard-service/internal/application"
	"github.com/igor-baiborodine/insurance-hub/services/dashboard-service/internal/config"
	"github.com/igor-baiborodine/insurance-hub/services/dashboard-service/internal/domain"
)

func TestRun_PropagatesDeadlineCancellationAndStripsMetadata(t *testing.T) {
	// given
	settings := testSettings(t)
	settings.RequestTimeout = 70 * time.Millisecond
	started := make(chan string, 2)
	finished := make(chan error, 2)
	metadataSeen := make(chan metadata.MD, 2)
	search := &controlledSearch{}
	search.read = func(
		ctx context.Context,
		query application.TotalSalesQuery,
	) ([]domain.ProductSales, error) {
		incoming, _ := metadata.FromIncomingContext(ctx)
		metadataSeen <- incoming.Copy()
		product := query.Product.Value()
		started <- product
		<-ctx.Done()
		finished <- ctx.Err()
		return nil, ctx.Err()
	}
	_, httpAddress, healthAddress, cancelService, runResult := startRegressionService(
		t,
		settings,
		search,
		slog.New(slog.NewJSONHandler(io.Discard, nil)),
	)
	client := &http.Client{Timeout: time.Second}
	waitForStatus(t, client, "http://"+healthAddress+"/readyz", http.StatusOK)

	request := totalSalesRequest(t, context.Background(), httpAddress, "BUDGET")
	request.Header.Set("Authorization", "Bearer private")
	request.Header.Set("Grpc-Metadata-Principal", "forged")
	request.Header.Set("Forwarded", "for=203.0.113.44")

	// when
	response, err := client.Do(request)
	// then
	if err != nil {
		t.Fatal(err)
	}
	assertHTTPResponse(
		t,
		response,
		http.StatusGatewayTimeout,
		`{"code":4,"message":"deadline exceeded","details":[]}`,
	)
	if product := receiveValue(
		t,
		started,
		"deadline request did not reach reader",
	); product != "BUDGET" {
		t.Fatalf("reader product = %q", product)
	}
	assertNoPrivateMetadata(t, receiveValue(t, metadataSeen, "metadata was not captured"))
	if err := receiveValue(
		t,
		finished,
		"deadline did not cancel reader",
	); !errors.Is(err, context.DeadlineExceeded) &&
		!errors.Is(err, context.Canceled) {
		t.Fatalf("deadline reader error = %v", err)
	}

	callerCtx, cancelCaller := context.WithCancel(context.Background())
	request = totalSalesRequest(t, callerCtx, httpAddress, "CALLER")
	requestResult := make(chan error, 1)
	go func() {
		result, requestErr := client.Do(request)
		if result != nil {
			_ = result.Body.Close()
		}
		requestResult <- requestErr
	}()
	if product := receiveValue(
		t,
		started,
		"caller request did not reach reader",
	); product != "CALLER" {
		t.Fatalf("reader product = %q", product)
	}
	_ = receiveValue(t, metadataSeen, "caller metadata was not captured")
	cancelCaller()
	if err := receiveValue(
		t,
		finished,
		"caller cancellation did not reach reader",
	); !errors.Is(
		err,
		context.Canceled,
	) {
		t.Fatalf("caller reader error = %v", err)
	}
	if err := receiveValue(
		t,
		requestResult,
		"caller request did not finish",
	); !errors.Is(
		err,
		context.Canceled,
	) {
		t.Fatalf("caller HTTP error = %v", err)
	}

	cancelService()
	if err := receiveValue(t, runResult, "service did not stop"); err != nil {
		t.Fatalf("run = %v", err)
	}
}

func TestRun_ReportsElasticsearchReadinessLossAndRecovery(t *testing.T) {
	// given
	settings := testSettings(t)
	search := &controlledSearch{}
	_, _, healthAddress, cancel, runResult := startRegressionService(
		t,
		settings,
		search,
		slog.New(slog.NewJSONHandler(io.Discard, nil)),
	)
	client := &http.Client{Timeout: time.Second}
	waitForStatus(t, client, "http://"+healthAddress+"/readyz", http.StatusOK)

	// when
	search.setCheckError(errors.New("private Elasticsearch loss"))

	// then
	waitForStatus(
		t,
		client,
		"http://"+healthAddress+"/readyz",
		http.StatusServiceUnavailable,
	)
	waitForStatus(t, client, "http://"+healthAddress+"/livez", http.StatusOK)
	search.setCheckError(nil)
	waitForStatus(t, client, "http://"+healthAddress+"/readyz", http.StatusOK)

	cancel()
	if err := receiveValue(t, runResult, "service did not stop"); err != nil {
		t.Fatalf("run = %v", err)
	}
}

func TestRun_DrainsAcceptedHTTPBeforeRPCShutdown(t *testing.T) {
	// given
	settings := testSettings(t)
	started := make(chan struct{})
	release := make(chan struct{})
	search := &controlledSearch{}
	search.read = func(
		ctx context.Context,
		_ application.TotalSalesQuery,
	) ([]domain.ProductSales, error) {
		close(started)
		select {
		case <-release:
			return successfulSales(), nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	_, httpAddress, healthAddress, cancel, runResult := startRegressionService(
		t,
		settings,
		search,
		slog.New(slog.NewJSONHandler(io.Discard, nil)),
	)
	client := &http.Client{Timeout: time.Second}
	waitForStatus(t, client, "http://"+healthAddress+"/readyz", http.StatusOK)
	responseResult := make(chan *http.Response, 1)
	errorResult := make(chan error, 1)
	request := totalSalesRequest(t, context.Background(), httpAddress, "DRAIN")

	// when
	go func() {
		response, err := client.Do(request)
		if err != nil {
			errorResult <- err
			return
		}
		responseResult <- response
	}()
	receiveSignalWithin(t, started, "accepted request did not reach reader")

	// then
	cancel()
	waitForStatus(
		t,
		client,
		"http://"+healthAddress+"/readyz",
		http.StatusServiceUnavailable,
	)
	select {
	case response := <-responseResult:
		_ = response.Body.Close()
		t.Fatal("accepted request completed before release")
	case err := <-errorResult:
		t.Fatalf("accepted request failed during drain: %v", err)
	default:
	}
	close(release)
	select {
	case response := <-responseResult:
		assertHTTPResponse(
			t,
			response,
			http.StatusOK,
			`{"total":{"policiesCount":5,"premiumAmount":5000.00},`+
				`"perProductTotal":{"SAFE":{"policiesCount":5,"premiumAmount":5000.00}}}`,
		)
	case err := <-errorResult:
		t.Fatalf("drained request failed: %v", err)
	case <-time.After(time.Second):
		t.Fatal("accepted request did not drain")
	}
	if err := receiveValue(t, runResult, "service did not stop"); err != nil {
		t.Fatalf("run = %v", err)
	}
}

func TestRun_ForcesStalledRPCWithinShutdownBudget(t *testing.T) {
	// given
	settings := testSettings(t)
	settings.RequestTimeout = 50 * time.Millisecond
	settings.ShutdownTimeout = 200 * time.Millisecond
	settings.ExporterTimeout = 50 * time.Millisecond
	started := make(chan struct{})
	release := make(chan struct{})
	search := &controlledSearch{}
	search.read = func(
		context.Context,
		application.TotalSalesQuery,
	) ([]domain.ProductSales, error) {
		close(started)
		<-release
		return successfulSales(), nil
	}
	_, httpAddress, healthAddress, cancel, runResult := startRegressionService(
		t,
		settings,
		search,
		slog.New(slog.NewJSONHandler(io.Discard, nil)),
	)
	client := &http.Client{Timeout: time.Second}
	waitForStatus(t, client, "http://"+healthAddress+"/readyz", http.StatusOK)
	requestDone := make(chan struct{})
	request := totalSalesRequest(t, context.Background(), httpAddress, "STALL")
	go func() {
		defer close(requestDone)
		response, err := client.Do(request)
		if err == nil {
			_ = response.Body.Close()
		}
	}()
	receiveSignalWithin(t, started, "stalled request did not start")

	// when
	shutdownStarted := time.Now()
	cancel()
	err := receiveValue(t, runResult, "forced shutdown did not complete")
	elapsed := time.Since(shutdownStarted)
	close(release)

	// then
	if elapsed > settings.ShutdownTimeout+200*time.Millisecond {
		t.Errorf("shutdown took %v, budget %v", elapsed, settings.ShutdownTimeout)
	}
	if err == nil || !strings.Contains(err.Error(), "deadline exceeded") {
		t.Errorf("forced shutdown error = %v", err)
	}
	receiveSignalWithin(t, requestDone, "HTTP request remained blocked")
}

func TestRun_PartialStartupClosesResourcesExactlyOnce(t *testing.T) {
	// given
	settings := testSettings(t)
	search := &controlledSearch{}
	provider := &countingTelemetry{}
	deps := productionDependencies()
	deps.newTelemetry = func(context.Context, string, config.Telemetry) (telemetryProvider, error) {
		return provider, nil
	}
	deps.newSearch = func(config.Elasticsearch) (application.TotalSalesReader, searchDependency, error) {
		return search, search, nil
	}
	bindFailure := errors.New("management bind failed")
	var listeners []net.Listener
	deps.listen = func(network, _ string) (net.Listener, error) {
		if len(listeners) == 2 {
			return nil, bindFailure
		}
		listener, err := net.Listen(network, "127.0.0.1:0")
		if err == nil {
			listeners = append(listeners, listener)
		}
		return listener, err
	}

	// when
	err := run(context.Background(), settings, slog.Default(), deps)

	// then
	if !errors.Is(err, bindFailure) {
		t.Fatalf("run error = %v", err)
	}
	for _, listener := range listeners {
		if _, err := listener.Accept(); !errors.Is(err, net.ErrClosed) {
			t.Errorf("listener remained open: %v", err)
		}
	}
	if !search.closed.Load() || provider.shutdowns.Load() != 1 {
		t.Errorf(
			"cleanup = (search %t, telemetry %d)",
			search.closed.Load(),
			provider.shutdowns.Load(),
		)
	}
}

func TestRun_EnforcesHeaderAndGatewayReceiveLimits(t *testing.T) {
	// given
	settings := testSettings(t)
	settings.MaxSendBytes = 1
	search := &controlledSearch{}
	_, httpAddress, healthAddress, cancel, runResult := startRegressionService(
		t,
		settings,
		search,
		slog.New(slog.NewJSONHandler(io.Discard, nil)),
	)
	client := &http.Client{Timeout: time.Second}
	waitForStatus(t, client, "http://"+healthAddress+"/readyz", http.StatusOK)
	assertOversizedHeader(t, httpAddress, settings.HTTPMaxHeaderBytes+5000)

	// when
	response, err := client.Do(
		totalSalesRequest(t, context.Background(), httpAddress, "SAFE"),
	)
	// then
	if err != nil {
		t.Fatal(err)
	}
	assertHTTPResponse(
		t,
		response,
		http.StatusInternalServerError,
		`{"code":13,"message":"internal error","details":[]}`,
	)
	cancel()
	if err := receiveValue(t, runResult, "service did not stop"); err != nil {
		t.Fatalf("run = %v", err)
	}
}

func TestRun_HTTPToRPCFailureTelemetryExcludesPrivateValues(t *testing.T) {
	// given
	const marker = "private-total-sales-marker"
	settings := testSettings(t)
	search := &controlledSearch{
		read: func(
			context.Context,
			application.TotalSalesQuery,
		) ([]domain.ProductSales, error) {
			return nil, errors.New(marker)
		},
	}
	metricReader := sdkmetric.NewManualReader()
	meterProvider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(metricReader))
	spanRecorder := tracetest.NewSpanRecorder()
	tracerProvider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(spanRecorder))
	provider := &recordingTelemetry{
		meterProvider: meterProvider, tracerProvider: tracerProvider,
	}
	deps := productionDependencies()
	deps.newTelemetry = func(context.Context, string, config.Telemetry) (telemetryProvider, error) {
		return provider, nil
	}
	deps.newSearch = func(config.Elasticsearch) (application.TotalSalesReader, searchDependency, error) {
		return search, search, nil
	}
	addresses := make(chan string, 3)
	deps.listen = func(network, _ string) (net.Listener, error) {
		listener, err := net.Listen(network, "127.0.0.1:0")
		if err == nil {
			addresses <- listener.Addr().String()
		}
		return listener, err
	}
	var logs bytes.Buffer
	ctx, cancel := context.WithCancel(context.Background())
	runResult := make(chan error, 1)
	go func() {
		runResult <- run(ctx, settings, slog.New(slog.NewJSONHandler(&logs, nil)), deps)
	}()
	_ = receiveValue(t, addresses, "gRPC address missing")
	httpAddress := receiveValue(t, addresses, "HTTP address missing")
	healthAddress := receiveValue(t, addresses, "health address missing")
	client := &http.Client{Timeout: time.Second}
	waitForStatus(t, client, "http://"+healthAddress+"/readyz", http.StatusOK)

	// when
	response, err := client.Do(
		totalSalesRequest(t, context.Background(), httpAddress, "FAIL"),
	)
	// then
	if err != nil {
		t.Fatal(err)
	}
	assertHTTPResponse(
		t,
		response,
		http.StatusInternalServerError,
		`{"code":13,"message":"internal error","details":[]}`,
	)
	cancel()
	if err := receiveValue(t, runResult, "service did not stop"); err != nil {
		t.Fatalf("run = %v", err)
	}

	var metrics metricdata.ResourceMetrics
	if err := metricReader.Collect(context.Background(), &metrics); err != nil {
		t.Fatal(err)
	}
	combined := logs.String() + fmt.Sprint(metrics)
	for _, span := range spanRecorder.Ended() {
		combined += span.Name() + fmt.Sprint(span.Attributes(), span.Events())
	}
	if strings.Contains(combined, marker) {
		t.Fatalf("private marker escaped into telemetry: %s", combined)
	}
	if !strings.Contains(combined, "POST /dashboard/totalsales") ||
		!strings.Contains(combined, "insurance_hub.dashboard.requests") {
		t.Errorf("missing HTTP-to-RPC completion signals: %s", combined)
	}
}

type controlledSearch struct {
	mu       sync.RWMutex
	checkErr error
	read     func(context.Context, application.TotalSalesQuery) ([]domain.ProductSales, error)
	closed   atomic.Bool
}

func (search *controlledSearch) ReadTotalSales(
	ctx context.Context,
	query application.TotalSalesQuery,
) ([]domain.ProductSales, error) {
	if search.read != nil {
		return search.read(ctx, query)
	}
	return successfulSales(), nil
}

func (search *controlledSearch) Check(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	search.mu.RLock()
	defer search.mu.RUnlock()
	return search.checkErr
}

func (search *controlledSearch) setCheckError(err error) {
	search.mu.Lock()
	search.checkErr = err
	search.mu.Unlock()
}

func (search *controlledSearch) CloseIdleConnections() { search.closed.Store(true) }

func successfulSales() []domain.ProductSales {
	value, _ := domain.NewSalesValue(5, domain.NewPremiumAmount(500000))
	return []domain.ProductSales{{ProductCode: "SAFE", Value: value}}
}

type countingTelemetry struct{ shutdowns atomic.Int32 }

func (*countingTelemetry) MeterProvider() metric.MeterProvider { return metricnoop.NewMeterProvider() }

func (*countingTelemetry) TracerProvider() trace.TracerProvider {
	return noop.NewTracerProvider()
}

func (*countingTelemetry) HTTPPropagator() propagation.TextMapPropagator {
	return propagation.TraceContext{}
}

func (*countingTelemetry) GRPCPropagator() propagation.TextMapPropagator {
	return propagation.TraceContext{}
}

func (provider *countingTelemetry) Shutdown(context.Context) error {
	provider.shutdowns.Add(1)
	return nil
}

type recordingTelemetry struct {
	meterProvider  metric.MeterProvider
	tracerProvider *sdktrace.TracerProvider
}

func (provider *recordingTelemetry) MeterProvider() metric.MeterProvider {
	return provider.meterProvider
}

func (provider *recordingTelemetry) TracerProvider() trace.TracerProvider {
	return provider.tracerProvider
}

func (*recordingTelemetry) HTTPPropagator() propagation.TextMapPropagator {
	return propagation.TraceContext{}
}

func (*recordingTelemetry) GRPCPropagator() propagation.TextMapPropagator {
	return propagation.TraceContext{}
}

func (provider *recordingTelemetry) Shutdown(ctx context.Context) error {
	return provider.tracerProvider.Shutdown(ctx)
}

func startRegressionService(
	t *testing.T,
	settings config.Config,
	search *controlledSearch,
	logger *slog.Logger,
) (string, string, string, context.CancelFunc, <-chan error) {
	t.Helper()
	deps := productionDependencies()
	deps.newTelemetry = func(context.Context, string, config.Telemetry) (telemetryProvider, error) {
		return fakeTelemetry{}, nil
	}
	deps.newSearch = func(config.Elasticsearch) (application.TotalSalesReader, searchDependency, error) {
		return search, search, nil
	}
	addresses := make(chan string, 3)
	deps.listen = func(network, _ string) (net.Listener, error) {
		listener, err := net.Listen(network, "127.0.0.1:0")
		if err == nil {
			addresses <- listener.Addr().String()
		}
		return listener, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	done := make(chan struct{})
	go func() {
		result <- run(ctx, settings, logger, deps)
		close(done)
	}()
	grpcAddress := receiveValue(t, addresses, "gRPC address missing")
	httpAddress := receiveValue(t, addresses, "HTTP address missing")
	healthAddress := receiveValue(t, addresses, "health address missing")
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Error("service did not stop during cleanup")
		}
	})
	return grpcAddress, httpAddress, healthAddress, cancel, result
}

func totalSalesRequest(
	t *testing.T,
	ctx context.Context,
	httpAddress string,
	product string,
) *http.Request {
	t.Helper()
	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		"http://"+httpAddress+"/dashboard/totalsales",
		strings.NewReader(
			`{"productCode":"`+product+`","saleDateFrom":"2019-01-01",`+
				`"saleDateTo":"2020-01-01"}`,
		),
	)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	return request
}

func waitForStatus(t *testing.T, client *http.Client, target string, want int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		response, err := client.Get(target)
		if err == nil {
			_, _ = io.Copy(io.Discard, response.Body)
			_ = response.Body.Close()
			if response.StatusCode == want {
				return
			}
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("%s did not return %d", target, want)
}

func assertHTTPResponse(t *testing.T, response *http.Response, status int, body string) {
	t.Helper()
	defer func() { _ = response.Body.Close() }()
	data, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != status || string(data) != body {
		t.Fatalf(
			"response = (%d, %q), want (%d, %q)",
			response.StatusCode,
			data,
			status,
			body,
		)
	}
}

func assertNoPrivateMetadata(t *testing.T, values metadata.MD) {
	t.Helper()
	for _, key := range []string{
		"authorization", "principal", "forwarded", "x-forwarded-for",
		"x-forwarded-host", "x-real-ip",
	} {
		if len(values.Get(key)) != 0 {
			t.Errorf("metadata %s escaped: %v", key, values.Get(key))
		}
	}
}

func assertOversizedHeader(t *testing.T, address string, size int) {
	t.Helper()
	connection, err := net.DialTimeout("tcp", address, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = connection.Close() }()
	_ = connection.SetDeadline(time.Now().Add(time.Second))
	_, err = io.WriteString(
		connection,
		"POST /dashboard/totalsales HTTP/1.1\r\nHost: example\r\nX-Large: "+
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
		t.Fatalf("oversized header status = %d", response.StatusCode)
	}
}

func receiveValue[T any](t *testing.T, values <-chan T, failure string) T {
	t.Helper()
	select {
	case value := <-values:
		return value
	case <-time.After(3 * time.Second):
		t.Fatal(failure)
		var zero T
		return zero
	}
}

func receiveSignalWithin(t *testing.T, signal <-chan struct{}, failure string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(time.Second):
		t.Fatal(failure)
	}
}
