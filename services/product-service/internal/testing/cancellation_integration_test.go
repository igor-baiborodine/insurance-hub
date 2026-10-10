//go:build integration

package integrationtest

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	grpcgo "google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	productv1 "github.com/igor-baiborodine/insurance-hub/services/product-service/gen/product/v1"
	"github.com/igor-baiborodine/insurance-hub/services/product-service/internal/config"
	"github.com/igor-baiborodine/insurance-hub/services/product-service/internal/logger"
	"github.com/igor-baiborodine/insurance-hub/services/product-service/internal/service"
)

const (
	cancellationAcquireTimeout = 350 * time.Millisecond
	cancellationCallerTimeout  = 450 * time.Millisecond
	cancellationQueryTimeout   = 900 * time.Millisecond
	cancellationRequestTimeout = 1300 * time.Millisecond
	cancellationProbeTimeout   = 200 * time.Millisecond
	cancellationWaitTimeout    = 3 * time.Second

	listQueryMarker = "name: ListProducts"
	getQueryMarker  = "name: GetProduct"
)

func TestProductCancellation(t *testing.T) {
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
	runtime := startCancellationRuntime(t, harness)

	t.Run("cancels blocked gRPC SQL at the caller boundary", func(t *testing.T) {
		release := lockProductTable(t, harness)
		defer release()
		callCtx, cancelCall := context.WithCancel(context.Background())
		result := make(chan error, 1)
		go func() {
			_, callErr := runtime.grpc.ListProducts(
				callCtx,
				&productv1.ListProductsRequest{},
			)
			result <- callErr
		}()
		waitForRuntimeQuery(t, harness, listQueryMarker, true)

		cancelCall()
		assertAsyncGRPCStatus(t, result, codes.Canceled)
		waitForRuntimeQuery(t, harness, listQueryMarker, false)
		release()
		assertGRPCRecovery(t, runtime)
	})

	t.Run("uses the configured SQL deadline and returns a safe HTTP error", func(t *testing.T) {
		release := lockProductTable(t, harness)
		defer release()
		result := make(chan httpCallResult, 1)
		go func() {
			response, callErr := runtime.http.Get(
				"http://" + runtime.addresses.http + "/products",
			)
			if callErr != nil {
				result <- httpCallResult{err: callErr}
				return
			}
			defer func() { _ = response.Body.Close() }()
			body, readErr := io.ReadAll(response.Body)
			result <- httpCallResult{status: response.StatusCode, body: body, err: readErr}
		}()
		waitForRuntimeQuery(t, harness, listQueryMarker, true)

		assertSafeHTTPTimeout(t, result)
		waitForRuntimeQuery(t, harness, listQueryMarker, false)
		release()
		assertHTTPRecovery(t, runtime)
	})

	t.Run("lets a shorter gRPC caller deadline win", func(t *testing.T) {
		release := lockProductTable(t, harness)
		defer release()
		callCtx, cancelCall := context.WithTimeout(
			context.Background(),
			cancellationCallerTimeout,
		)
		defer cancelCall()
		result := make(chan error, 1)
		go func() {
			_, callErr := runtime.grpc.GetProduct(
				callCtx,
				&productv1.GetProductRequest{Code: "CAR"},
			)
			result <- callErr
		}()
		waitForRuntimeQuery(t, harness, getQueryMarker, true)

		assertAsyncGRPCStatus(t, result, codes.DeadlineExceeded)
		waitForRuntimeQuery(t, harness, getQueryMarker, false)
		release()
		assertGRPCRecovery(t, runtime)
	})

	t.Run("maps the configured SQL deadline to gRPC DeadlineExceeded", func(t *testing.T) {
		release := lockProductTable(t, harness)
		defer release()
		result := make(chan error, 1)
		go func() {
			_, callErr := runtime.grpc.GetProduct(
				context.Background(),
				&productv1.GetProductRequest{Code: "CAR"},
			)
			result <- callErr
		}()
		waitForRuntimeQuery(t, harness, getQueryMarker, true)

		assertAsyncGRPCStatus(t, result, codes.DeadlineExceeded)
		waitForRuntimeQuery(t, harness, getQueryMarker, false)
		release()
		assertGRPCRecovery(t, runtime)
	})

	t.Run("bounds pool acquisition and preserves probe policy", func(t *testing.T) {
		release := lockProductTable(t, harness)
		defer release()
		blockedCtx, cancelBlocked := context.WithCancel(context.Background())
		blockedResult := make(chan error, 1)
		go func() {
			_, callErr := runtime.grpc.ListProducts(
				blockedCtx,
				&productv1.ListProductsRequest{},
			)
			blockedResult <- callErr
		}()
		waitForRuntimeQuery(t, harness, listQueryMarker, true)

		if got := httpStatus(t, runtime.addresses.health, "/livez"); got != http.StatusOK {
			t.Fatalf("liveness status = %d, want %d", got, http.StatusOK)
		}
		if got := httpStatus(
			t,
			runtime.addresses.health,
			"/readyz",
		); got != http.StatusServiceUnavailable {
			t.Fatalf(
				"readiness status = %d, want %d",
				got,
				http.StatusServiceUnavailable,
			)
		}
		_, acquireErr := runtime.grpc.GetProduct(
			context.Background(),
			&productv1.GetProductRequest{Code: "CAR"},
		)
		assertCancellationStatus(t, acquireErr, codes.DeadlineExceeded)
		waitForRuntimeQuery(t, harness, listQueryMarker, true)

		cancelBlocked()
		assertAsyncGRPCStatus(t, blockedResult, codes.Canceled)
		waitForRuntimeQuery(t, harness, listQueryMarker, false)
		release()
		waitForHTTPStatus(t, runtime.addresses.health, "/readyz", http.StatusOK)
		assertGRPCRecovery(t, runtime)
	})

	t.Run("cancels SQL when the HTTP caller disconnects", func(t *testing.T) {
		release := lockProductTable(t, harness)
		defer release()
		connection, err := net.DialTimeout("tcp", runtime.addresses.http, time.Second)
		if err != nil {
			t.Fatalf("connect HTTP caller: %v", err)
		}
		if _, err := fmt.Fprintf(
			connection,
			"GET /products HTTP/1.1\r\nHost: %s\r\nConnection: close\r\n\r\n",
			runtime.addresses.http,
		); err != nil {
			_ = connection.Close()
			t.Fatalf("write HTTP request: %v", err)
		}
		waitForRuntimeQuery(t, harness, listQueryMarker, true)

		if err := connection.Close(); err != nil {
			t.Fatalf("disconnect HTTP caller: %v", err)
		}
		waitForRuntimeQuery(t, harness, listQueryMarker, false)
		release()
		assertHTTPRecovery(t, runtime)
	})

	t.Run("maps connection loss to unavailable and reconnects", func(t *testing.T) {
		release := lockProductTable(t, harness)
		defer release()
		result := make(chan error, 1)
		go func() {
			_, callErr := runtime.grpc.GetProduct(
				context.Background(),
				&productv1.GetProductRequest{Code: "CAR"},
			)
			result <- callErr
		}()
		waitForRuntimeQuery(t, harness, getQueryMarker, true)

		terminateRuntimeQueries(t, harness, getQueryMarker)
		assertAsyncGRPCStatus(t, result, codes.Unavailable)
		waitForRuntimeQuery(t, harness, getQueryMarker, false)
		release()
		waitForHTTPStatus(t, runtime.addresses.health, "/readyz", http.StatusOK)
		assertGRPCRecovery(t, runtime)
	})

	runtime.stop(t)
	waitForReaderConnections(t, harness, 0)
	for _, marker := range []string{
		"password", "secret", "rawDefinitionJson", "SELECT code", "public.product",
	} {
		if strings.Contains(runtime.logs.String(), marker) {
			t.Fatalf("runtime logs contain unsafe marker %q", marker)
		}
	}
}

func startCancellationRuntime(t *testing.T, harness *Harness) *productRuntime {
	t.Helper()
	addresses := serviceAddresses{
		http: freeAddress(t), grpc: freeAddress(t), health: freeAddress(t),
	}
	settings := loadServiceSettings(t, harness.RuntimeDatabaseURL(), addresses)
	settings.Database.MaxConnections = 1
	settings.Database.AcquireTimeout = cancellationAcquireTimeout
	settings.Database.QueryTimeout = cancellationQueryTimeout
	settings.RequestTimeout = cancellationRequestTimeout
	settings.ProbeTimeout = cancellationProbeTimeout
	settings.HTTPServer.ReadTimeout = 2 * time.Second
	settings.HTTPServer.WriteTimeout = 2 * time.Second
	serviceCtx, stopService := context.WithCancel(context.Background())
	result := make(chan error, 1)
	logs := new(bytes.Buffer)
	go func() {
		result <- service.Run(
			serviceCtx,
			settings,
			logger.New(logs, settings.ServiceName, settings.LogLevel),
		)
	}()
	waitForHTTPStatus(t, addresses.health, "/readyz", http.StatusOK)
	connection, err := newProductGRPCConnection(addresses.grpc, settings)
	if err != nil {
		stopService()
		t.Fatalf("create cancellation gRPC client: %v", err)
	}
	runtime := &productRuntime{
		addresses:   addresses,
		http:        &http.Client{Timeout: 3 * time.Second},
		grpc:        productv1.NewProductServiceClient(connection),
		connection:  connection,
		stopService: stopService,
		result:      result,
		logs:        logs,
	}
	t.Cleanup(func() {
		if !runtime.stopped {
			runtime.stop(t)
		}
	})
	return runtime
}

func newProductGRPCConnection(
	address string,
	settings config.Config,
) (*grpcgo.ClientConn, error) {
	return grpcgo.NewClient(
		address,
		grpcgo.WithTransportCredentials(insecure.NewCredentials()),
		grpcgo.WithDefaultCallOptions(
			grpcgo.MaxCallSendMsgSize(settings.GRPC.MaxReceiveBytes),
			grpcgo.MaxCallRecvMsgSize(settings.GRPC.MaxSendBytes),
		),
	)
}

func lockProductTable(t *testing.T, harness *Harness) func() {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), cancellationWaitTimeout)
	defer cancel()
	transaction, err := harness.adminPool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin Product lock transaction: %v", err)
	}
	if _, err := transaction.Exec(
		ctx,
		"LOCK TABLE public.product IN ACCESS EXCLUSIVE MODE",
	); err != nil {
		_ = transaction.Rollback(context.Background())
		t.Fatalf("lock Product table: %v", err)
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			if err := transaction.Rollback(
				context.Background(),
			); err != nil &&
				err != pgx.ErrTxClosed {
				t.Errorf("release Product table lock: %v", err)
			}
		})
	}
}

func waitForRuntimeQuery(t *testing.T, harness *Harness, marker string, present bool) {
	t.Helper()
	deadline := time.NewTimer(cancellationWaitTimeout)
	defer deadline.Stop()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		count, err := runtimeQueryCount(harness, marker, present)
		if err == nil && (present && count > 0 || !present && count == 0) {
			return
		}
		select {
		case <-deadline.C:
			t.Fatalf(
				"runtime query %q presence=%t not observed; last=(%d, %v)",
				marker,
				present,
				count,
				err,
			)
		case <-ticker.C:
		}
	}
}

func runtimeQueryCount(harness *Harness, marker string, blockedOnly bool) (int, error) {
	var count int
	err := harness.adminPool.QueryRow(
		context.Background(),
		"SELECT count(*)::integer FROM pg_stat_activity "+
			"WHERE datname = current_database() AND usename = $1 "+
			"AND state = 'active' AND position($2 in query) > 0 "+
			"AND (NOT $3 OR wait_event_type = 'Lock')",
		runtimeRole,
		marker,
		blockedOnly,
	).Scan(&count)
	return count, err
}

func terminateRuntimeQueries(t *testing.T, harness *Harness, marker string) {
	t.Helper()
	rows, err := harness.adminPool.Query(
		context.Background(),
		"SELECT pid FROM pg_stat_activity WHERE datname = current_database() "+
			"AND usename = $1 AND state = 'active' AND position($2 in query) > 0",
		runtimeRole,
		marker,
	)
	if err != nil {
		t.Fatalf("find runtime query to terminate: %v", err)
	}
	pids, err := pgx.CollectRows(rows, pgx.RowTo[int32])
	if err != nil {
		t.Fatalf("collect runtime query backends: %v", err)
	}
	if len(pids) != 1 {
		t.Fatalf("runtime query backends = %v, want exactly one", pids)
	}
	var terminated bool
	if err := harness.adminPool.QueryRow(
		context.Background(),
		"SELECT pg_terminate_backend($1)",
		pids[0],
	).Scan(&terminated); err != nil || !terminated {
		t.Fatalf("terminate runtime backend %d = %t, %v", pids[0], terminated, err)
	}
}

type httpCallResult struct {
	status int
	body   []byte
	err    error
}

func assertSafeHTTPTimeout(t *testing.T, result <-chan httpCallResult) {
	t.Helper()
	select {
	case actual := <-result:
		if actual.err != nil {
			t.Fatalf("HTTP timeout call: %v", actual.err)
		}
		if actual.status != http.StatusInternalServerError ||
			string(actual.body) != `{"message":"Internal Server Error"}` {
			t.Fatalf("HTTP timeout response = %d %q", actual.status, actual.body)
		}
	case <-time.After(cancellationWaitTimeout):
		t.Fatal("HTTP timeout call did not finish within the test bound")
	}
}

func assertAsyncGRPCStatus(t *testing.T, result <-chan error, want codes.Code) {
	t.Helper()
	select {
	case err := <-result:
		assertCancellationStatus(t, err, want)
	case <-time.After(cancellationWaitTimeout):
		t.Fatalf("gRPC call did not finish with %s within the test bound", want)
	}
}

func assertCancellationStatus(t *testing.T, err error, want codes.Code) {
	t.Helper()
	if status.Code(err) != want {
		t.Fatalf("gRPC status = %s, want %s: %v", status.Code(err), want, err)
	}
	message := status.Convert(err).Message()
	wantMessage := map[codes.Code]string{
		codes.Canceled:         context.Canceled.Error(),
		codes.DeadlineExceeded: context.DeadlineExceeded.Error(),
		codes.Unavailable:      "service unavailable",
	}[want]
	if message != wantMessage {
		t.Fatalf("gRPC message = %q, want %q", message, wantMessage)
	}
}

func assertGRPCRecovery(t *testing.T, runtime *productRuntime) {
	t.Helper()
	response, err := runtime.grpc.GetProduct(
		context.Background(),
		&productv1.GetProductRequest{Code: "CAR"},
	)
	if err != nil || response.GetProduct().GetCode() != "CAR" {
		t.Fatalf("gRPC recovery response = %v, error = %v", response, err)
	}
}

func assertHTTPRecovery(t *testing.T, runtime *productRuntime) {
	t.Helper()
	response, err := runtime.http.Get("http://" + runtime.addresses.http + "/products/CAR")
	if err != nil {
		t.Fatalf("HTTP recovery call: %v", err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("HTTP recovery status = %d, want %d", response.StatusCode, http.StatusOK)
	}
}
