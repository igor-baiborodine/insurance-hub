//go:build integration

package integrationtest

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"

	grpcgo "google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	productv1 "github.com/igor-baiborodine/insurance-hub/services/product-service/gen/product/v1"
)

const (
	lifecycleShutdownTimeout = 2500 * time.Millisecond
	lifecycleGraceBudget     = 500 * time.Millisecond
	lifecycleWaitTimeout     = 4 * time.Second
)

func TestProductLifecycle(t *testing.T) {
	image := os.Getenv("PRODUCT_TEST_POSTGRES_IMAGE")
	if image != DefaultPostgresImage {
		t.Fatalf("PRODUCT_TEST_POSTGRES_IMAGE does not contain the pinned image")
	}
	if selection := os.Getenv(
		"PRODUCT_INTEGRATION_FIXTURE_SET",
	); selection != string(
		FixtureQA,
	) {
		t.Fatalf("fixture selection = %q, want qa", selection)
	}
	binary := os.Getenv("PRODUCT_TEST_SERVER_BINARY")
	if binary == "" {
		t.Fatal("PRODUCT_TEST_SERVER_BINARY must identify the race-built server executable")
	}
	if info, err := os.Stat(binary); err != nil || info.IsDir() {
		t.Fatalf("inspect server executable %q: %v", binary, err)
	}
	workingDirectory, err := os.Getwd()
	if err != nil {
		t.Fatalf("get working directory: %v", err)
	}
	baselineRoot, err := ResolveBaselineRoot(workingDirectory)
	if err != nil {
		t.Fatalf("resolve baseline root: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	harness, err := Start(ctx, Options{FixtureRoot: baselineRoot, Image: image})
	if err != nil {
		t.Fatalf("start harness: %v", err)
	}
	t.Cleanup(func() { _ = harness.Close() })
	if _, err := harness.LoadCatalog(ctx, FixtureQA); err != nil {
		t.Fatalf("load QA catalog: %v", err)
	}

	t.Run("stops an idle executable within the shared budget", func(t *testing.T) {
		process := startLifecycleProcess(t, binary, harness.RuntimeDatabaseURL())
		if got := httpStatus(t, process.addresses.health, "/livez"); got != http.StatusOK {
			t.Fatalf("liveness status = %d, want %d", got, http.StatusOK)
		}

		started := process.signal(t)
		waitForShutdownIntake(t, process.addresses)
		process.assertCleanExit(t, started, harness)
	})

	t.Run("drains accepted HTTP and gRPC work before exit", func(t *testing.T) {
		process := startLifecycleProcess(t, binary, harness.RuntimeDatabaseURL())
		release := lockProductTable(t, harness)
		defer release()
		httpResult := process.listProducts()
		grpcResult := process.getProduct("CAR")
		waitForRuntimeQuery(t, harness, listQueryMarker, true)
		waitForRuntimeQuery(t, harness, getQueryMarker, true)

		started := process.signal(t)
		waitForShutdownIntake(t, process.addresses)
		release()
		assertGracefulHTTPResult(t, httpResult)
		assertGracefulGRPCResult(t, grpcResult, "CAR")
		process.assertCleanExit(t, started, harness)
	})

	t.Run(
		"force stops blocked HTTP and gRPC work within the shared budget",
		func(t *testing.T) {
			process := startLifecycleProcess(t, binary, harness.RuntimeDatabaseURL())
			release := lockProductTable(t, harness)
			defer release()
			httpResult := process.listProducts()
			grpcResult := process.getProduct("CAR")
			waitForRuntimeQuery(t, harness, listQueryMarker, true)
			waitForRuntimeQuery(t, harness, getQueryMarker, true)

			started := process.signal(t)
			waitForShutdownIntake(t, process.addresses)
			assertForcedHTTPResult(t, httpResult)
			assertForcedGRPCResult(t, grpcResult)
			waitForRuntimeQuery(t, harness, listQueryMarker, false)
			waitForRuntimeQuery(t, harness, getQueryMarker, false)
			elapsed := process.assertCleanExit(t, started, harness)
			if elapsed < lifecycleGraceBudget-150*time.Millisecond {
				t.Fatalf(
					"forced shutdown elapsed = %s, want drain period near %s",
					elapsed,
					lifecycleGraceBudget,
				)
			}
			release()
		},
	)
}

type lifecycleProcess struct {
	addresses  serviceAddresses
	command    *exec.Cmd
	wait       chan error
	logs       *bytes.Buffer
	http       *http.Client
	connection *grpcgo.ClientConn
	grpc       productv1.ProductServiceClient
	stopped    bool
}

func startLifecycleProcess(t *testing.T, binary, databaseURL string) *lifecycleProcess {
	t.Helper()
	addresses := serviceAddresses{
		http: freeAddress(t), grpc: freeAddress(t), health: freeAddress(t),
	}
	logs := new(bytes.Buffer)
	command := exec.Command(binary)
	command.Env = lifecycleEnvironment(os.Environ(), []string{
		"SERVICE_NAME=product-service-lifecycle-test",
		"LOG_LEVEL=info",
		"HTTP_ADDR=" + addresses.http,
		"GRPC_ADDR=" + addresses.grpc,
		"HEALTH_ADDR=" + addresses.health,
		"PRODUCT_DATABASE_URL=" + databaseURL,
		"DB_MAX_CONNS=4",
		"DB_CONNECT_TIMEOUT=500ms",
		"DB_ACQUIRE_TIMEOUT=1s",
		"DB_QUERY_TIMEOUT=6s",
		"STARTUP_TIMEOUT=2s",
		"REQUEST_TIMEOUT=6s",
		"PROBE_TIMEOUT=200ms",
		"HTTP_READ_HEADER_TIMEOUT=500ms",
		"HTTP_READ_TIMEOUT=6s",
		"HTTP_WRITE_TIMEOUT=6s",
		"HTTP_IDLE_TIMEOUT=1s",
		"SHUTDOWN_TIMEOUT=2500ms",
		"OTEL_ENABLED=false",
		"OTEL_EXPORTER_OTLP_TIMEOUT=500ms",
	})
	command.Stdout = logs
	command.Stderr = logs
	if err := command.Start(); err != nil {
		t.Fatalf("start Product executable: %v", err)
	}
	process := &lifecycleProcess{
		addresses: addresses,
		command:   command,
		wait:      make(chan error, 1),
		logs:      logs,
		http:      &http.Client{Timeout: 8 * time.Second},
	}
	go func() { process.wait <- command.Wait() }()
	t.Cleanup(func() { process.cleanup(t) })
	waitForHTTPStatus(t, addresses.health, "/readyz", http.StatusOK)
	connection, err := grpcgo.NewClient(
		addresses.grpc,
		grpcgo.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("create lifecycle gRPC client: %v", err)
	}
	process.connection = connection
	process.grpc = productv1.NewProductServiceClient(connection)
	return process
}

func lifecycleEnvironment(base, overrides []string) []string {
	keys := make(map[string]struct{}, len(overrides))
	for _, override := range overrides {
		key, _, _ := strings.Cut(override, "=")
		keys[key] = struct{}{}
	}
	environment := make([]string, 0, len(base)+len(overrides))
	for _, value := range base {
		key, _, _ := strings.Cut(value, "=")
		if _, replaced := keys[key]; !replaced {
			environment = append(environment, value)
		}
	}
	return append(environment, overrides...)
}

func (process *lifecycleProcess) signal(t *testing.T) time.Time {
	t.Helper()
	started := time.Now()
	if err := process.command.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatalf("signal Product executable: %v", err)
	}
	return started
}

func (process *lifecycleProcess) listProducts() <-chan httpCallResult {
	result := make(chan httpCallResult, 1)
	go func() {
		response, err := process.http.Get("http://" + process.addresses.http + "/products")
		if err != nil {
			result <- httpCallResult{err: err}
			return
		}
		defer func() { _ = response.Body.Close() }()
		body, readErr := io.ReadAll(response.Body)
		result <- httpCallResult{status: response.StatusCode, body: body, err: readErr}
	}()
	return result
}

type lifecycleGRPCResult struct {
	response *productv1.GetProductResponse
	err      error
}

func (process *lifecycleProcess) getProduct(code string) <-chan lifecycleGRPCResult {
	result := make(chan lifecycleGRPCResult, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		response, err := process.grpc.GetProduct(
			ctx,
			&productv1.GetProductRequest{Code: code},
		)
		result <- lifecycleGRPCResult{response: response, err: err}
	}()
	return result
}

func (process *lifecycleProcess) assertCleanExit(
	t *testing.T,
	started time.Time,
	harness *Harness,
) time.Duration {
	t.Helper()
	select {
	case err := <-process.wait:
		process.stopped = true
		if err != nil {
			t.Fatalf(
				"Product executable exit: %v\nlogs:\n%s",
				err,
				process.logs.String(),
			)
		}
	case <-time.After(lifecycleWaitTimeout):
		t.Fatalf(
			"Product executable exceeded shutdown bound\nlogs:\n%s",
			process.logs.String(),
		)
	}
	elapsed := time.Since(started)
	if elapsed > lifecycleShutdownTimeout+1500*time.Millisecond {
		t.Fatalf("shutdown elapsed = %s, budget = %s", elapsed, lifecycleShutdownTimeout)
	}
	if process.connection != nil {
		if err := process.connection.Close(); err != nil {
			t.Errorf("close lifecycle gRPC client: %v", err)
		}
		process.connection = nil
	}
	waitForTCPRejection(t, process.addresses.http)
	waitForTCPRejection(t, process.addresses.grpc)
	waitForTCPRejection(t, process.addresses.health)
	waitForReaderConnections(t, harness, 0)
	assertLifecycleLogsSafe(t, process.logs.String(), harness.RuntimeDatabaseURL())
	return elapsed
}

func (process *lifecycleProcess) cleanup(t *testing.T) {
	t.Helper()
	if process.connection != nil {
		_ = process.connection.Close()
		process.connection = nil
	}
	if process.stopped {
		return
	}
	_ = process.command.Process.Kill()
	select {
	case <-process.wait:
		process.stopped = true
	case <-time.After(time.Second):
		t.Errorf("forced lifecycle process cleanup did not finish")
	}
}

func waitForShutdownIntake(t *testing.T, addresses serviceAddresses) {
	t.Helper()
	waitForHTTPNotReady(t, addresses.health)
	waitForTCPRejection(t, addresses.http)
	waitForTCPRejection(t, addresses.grpc)
}

func waitForHTTPNotReady(t *testing.T, address string) {
	t.Helper()
	deadline := time.NewTimer(lifecycleWaitTimeout)
	defer deadline.Stop()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		statusCode, err := tryHTTPStatus(address, "/readyz")
		if err != nil || statusCode != http.StatusOK {
			return
		}
		select {
		case <-deadline.C:
			t.Fatal("readiness continued accepting traffic during shutdown")
		case <-ticker.C:
		}
	}
}

func waitForTCPRejection(t *testing.T, address string) {
	t.Helper()
	deadline := time.NewTimer(lifecycleWaitTimeout)
	defer deadline.Stop()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		connection, err := net.DialTimeout("tcp", address, 100*time.Millisecond)
		if err != nil {
			return
		}
		_ = connection.Close()
		select {
		case <-deadline.C:
			t.Fatalf("listener %s continued accepting traffic during shutdown", address)
		case <-ticker.C:
		}
	}
}

func assertGracefulHTTPResult(t *testing.T, result <-chan httpCallResult) {
	t.Helper()
	select {
	case actual := <-result:
		if actual.err != nil || actual.status != http.StatusOK {
			t.Fatalf(
				"graceful HTTP result = (%d, %q, %v)",
				actual.status,
				actual.body,
				actual.err,
			)
		}
	case <-time.After(lifecycleWaitTimeout):
		t.Fatal("accepted HTTP call did not drain")
	}
}

func assertGracefulGRPCResult(
	t *testing.T,
	result <-chan lifecycleGRPCResult,
	wantCode string,
) {
	t.Helper()
	select {
	case actual := <-result:
		if actual.err != nil || actual.response.GetProduct().GetCode() != wantCode {
			t.Fatalf("graceful gRPC result = (%v, %v)", actual.response, actual.err)
		}
	case <-time.After(lifecycleWaitTimeout):
		t.Fatal("accepted gRPC call did not drain")
	}
}

func assertForcedHTTPResult(t *testing.T, result <-chan httpCallResult) {
	t.Helper()
	select {
	case actual := <-result:
		if actual.err == nil && actual.status == http.StatusOK {
			t.Fatalf("blocked HTTP call completed successfully: %q", actual.body)
		}
	case <-time.After(lifecycleWaitTimeout):
		t.Fatal("blocked HTTP call survived forced shutdown")
	}
}

func assertForcedGRPCResult(t *testing.T, result <-chan lifecycleGRPCResult) {
	t.Helper()
	select {
	case actual := <-result:
		code := status.Code(actual.err)
		if code != codes.Canceled && code != codes.Unavailable {
			t.Fatalf(
				"forced gRPC status = %s, want Canceled or Unavailable: %v",
				code,
				actual.err,
			)
		}
	case <-time.After(lifecycleWaitTimeout):
		t.Fatal("blocked gRPC call survived forced shutdown")
	}
}

func assertLifecycleLogsSafe(t *testing.T, logs, databaseURL string) {
	t.Helper()
	parsed, err := url.Parse(databaseURL)
	if err != nil {
		t.Fatalf("parse runtime database URL for log check: %v", err)
	}
	password, _ := parsed.User.Password()
	for _, marker := range []string{
		password, "postgresql://", "rawDefinitionJson", "SELECT code", "public.product",
	} {
		if marker != "" && strings.Contains(logs, marker) {
			t.Fatalf("lifecycle logs contain unsafe marker %q", marker)
		}
	}
}
