//go:build integration

package integrationtest

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	grpcgo "google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	productv1 "github.com/igor-baiborodine/insurance-hub/services/product-service/gen/product/v1"
	"github.com/igor-baiborodine/insurance-hub/services/product-service/internal/logger"
	"github.com/igor-baiborodine/insurance-hub/services/product-service/internal/service"
)

const (
	acceptedDataEdgeCaseCount = 16
	acceptedFailureCaseCount  = 13
	acceptedQASuccessCount    = 5
)

func TestProductParity(t *testing.T) {
	// given
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
	if err := ValidateBaselineCoverage(baselineRoot, ScenarioRegistry()); err != nil {
		t.Fatalf("validate accepted corpus before replay: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	harness, err := Start(ctx, Options{FixtureRoot: baselineRoot, Image: image})
	if err != nil {
		t.Fatalf("start harness: %v", err)
	}
	t.Cleanup(func() { _ = harness.Close() })
	serverVersion, err := harness.ServerVersion(ctx)
	if err != nil {
		t.Fatalf("read PostgreSQL server version: %v", err)
	}
	runtime := startParityRuntime(t, harness)

	t.Run("replays the captured QA catalog through both transports", func(t *testing.T) {
		replayQACatalog(t, ctx, harness, runtime, baselineRoot, image, serverVersion)
	})

	t.Run("replays every synthetic data edge", func(t *testing.T) {
		replayDataEdges(t, ctx, harness, runtime, baselineRoot)
	})

	t.Run("replays every direct Product failure", func(t *testing.T) {
		replayDirectFailures(t, ctx, harness, runtime, baselineRoot)
	})

	// when
	if err := ValidateBaselineCoverage(baselineRoot, ScenarioRegistry()); err != nil {
		t.Fatalf("validate accepted corpus after replay: %v", err)
	}
	runtime.stop(t)

	// then
	waitForReaderConnections(t, harness, 0)
	for _, marker := range []string{"password", "secret", "rawDefinitionJson"} {
		if strings.Contains(runtime.logs.String(), marker) {
			t.Fatalf("runtime logs contain unsafe marker %q", marker)
		}
	}
}

type parityRuntime struct {
	addresses   serviceAddresses
	http        *http.Client
	grpc        productv1.ProductServiceClient
	connection  *grpcgo.ClientConn
	stopService context.CancelFunc
	result      chan error
	logs        *bytes.Buffer
	stopped     bool
}

func startParityRuntime(t *testing.T, harness *Harness) *parityRuntime {
	t.Helper()
	addresses := serviceAddresses{
		http: freeAddress(t), grpc: freeAddress(t), health: freeAddress(t),
	}
	settings := loadServiceSettings(t, harness.RuntimeDatabaseURL(), addresses)
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
	connection, err := grpcgo.NewClient(
		addresses.grpc,
		grpcgo.WithTransportCredentials(insecure.NewCredentials()),
		grpcgo.WithDefaultCallOptions(
			grpcgo.MaxCallSendMsgSize(settings.GRPC.MaxReceiveBytes),
			grpcgo.MaxCallRecvMsgSize(settings.GRPC.MaxSendBytes),
		),
	)
	if err != nil {
		stopService()
		t.Fatalf("create parity gRPC client: %v", err)
	}
	runtime := &parityRuntime{
		addresses:   addresses,
		http:        &http.Client{Timeout: 5 * time.Second},
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

func (runtime *parityRuntime) stop(t *testing.T) {
	t.Helper()
	if runtime.stopped {
		return
	}
	runtime.stopped = true
	if err := runtime.connection.Close(); err != nil {
		t.Errorf("close parity gRPC client: %v", err)
	}
	runtime.stopService()
	select {
	case err := <-runtime.result:
		if err != nil {
			t.Errorf("run parity service: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Error("parity service did not stop within the test bound")
	}
}

func replayQACatalog(
	t *testing.T,
	ctx context.Context,
	harness *Harness,
	runtime *parityRuntime,
	baselineRoot string,
	image string,
	serverVersion string,
) {
	t.Helper()
	fixture, err := harness.LoadCatalog(ctx, FixtureQA)
	if err != nil {
		t.Fatalf("load QA catalog: %v", err)
	}
	snapshot, err := harness.Snapshot(ctx)
	if err != nil {
		t.Fatalf("read QA replay snapshot: %v", err)
	}
	if snapshot.RowCount != fixture.DataIdentity.RowCount ||
		snapshot.DataIdentity != fixture.DataIdentity.Value ||
		snapshot.SchemaIdentity != fixture.SchemaIdentity.Value ||
		!reflect.DeepEqual(snapshot.Codes, fixture.DataIdentity.Codes) {
		t.Fatalf("QA snapshot = %#v, fixture = %#v", snapshot, fixture)
	}
	httpExpectations := loadQAHTTPExpectations(t, baselineRoot)
	grpcExpectations := loadQAGRPCExpectations(t, baselineRoot)
	if len(httpExpectations) != acceptedQASuccessCount ||
		len(grpcExpectations) != acceptedQASuccessCount {
		t.Fatalf(
			"QA success coverage HTTP/gRPC = %d/%d, want %d/%d",
			len(httpExpectations),
			len(grpcExpectations),
			acceptedQASuccessCount,
			acceptedQASuccessCount,
		)
	}

	assertHTTPExpectation(
		t,
		runtime.http,
		runtime.addresses.http,
		"/products",
		httpExpectations["HTTP-DIRECT-LIST-001"],
	)
	listResponse, err := runtime.grpc.ListProducts(ctx, &productv1.ListProductsRequest{})
	if err != nil {
		t.Fatalf("list QA products over gRPC: %v", err)
	}
	assertGRPCProducts(t, grpcExpectations["HTTP-DIRECT-LIST-001"], listResponse.GetProducts())
	for _, code := range fixture.DataIdentity.Codes {
		scenarioID := "HTTP-DIRECT-GET-" + code + "-001"
		assertHTTPExpectation(
			t,
			runtime.http,
			runtime.addresses.http,
			"/products/"+code,
			httpExpectations[scenarioID],
		)
		response, getErr := runtime.grpc.GetProduct(
			ctx,
			&productv1.GetProductRequest{Code: code},
		)
		if getErr != nil {
			t.Fatalf("get QA product %s over gRPC: %v", code, getErr)
		}
		assertGRPCProducts(
			t,
			grpcExpectations[scenarioID],
			[]*productv1.Product{response.GetProduct()},
		)
	}
	t.Logf(
		"QA replay identity: scenario=%s provenance=%s source-revision=%s schema-md5=%s data-md5=%s codes=%v image=%s server=%s corpus-sha256=%s",
		fixture.ScenarioID,
		fixture.Provenance,
		fixture.SourceRevision,
		fixture.SchemaIdentity.Value,
		fixture.DataIdentity.Value,
		fixture.DataIdentity.Codes,
		image,
		serverVersion,
		acceptedCorpusSHA256["catalog/qa.json"],
	)
}

type dataEdgeFixture struct {
	FixtureID string         `json:"fixtureId"`
	Cases     []dataEdgeCase `json:"cases"`
}

type dataEdgeCase struct {
	ID          string        `json:"id"`
	ScenarioIDs []string      `json:"scenarioIds"`
	Kind        string        `json:"kind"`
	Repetitions int           `json:"repetitions"`
	RequestPath string        `json:"requestPath"`
	Rows        []dataEdgeRow `json:"rows"`
	Expected    struct {
		Outcome     string `json:"outcome"`
		Status      int    `json:"status"`
		ContentType string `json:"contentType"`
		RawBody     string `json:"rawBody"`
		SQLState    string `json:"sqlState"`
	} `json:"expected"`
}

type dataEdgeRow struct {
	Code              *string `json:"code"`
	RawDefinitionJSON *string `json:"rawDefinitionJson"`
}

func replayDataEdges(
	t *testing.T,
	ctx context.Context,
	harness *Harness,
	runtime *parityRuntime,
	baselineRoot string,
) {
	t.Helper()
	fixture := loadDataEdgeFixture(t, baselineRoot)
	executed := make(map[string]struct{}, len(fixture.Cases))
	for _, testCase := range fixture.Cases {
		t.Run(testCase.ID, func(t *testing.T) {
			executed[testCase.ID] = struct{}{}
			switch testCase.Kind {
			case "http":
				replayHTTPDataEdge(t, ctx, harness, runtime, testCase)
			case "database-constraint":
				replayConstraintDataEdge(t, ctx, harness, testCase)
			default:
				t.Fatalf("unknown data-edge kind %q", testCase.Kind)
			}
		})
	}
	if len(executed) != acceptedDataEdgeCaseCount {
		t.Fatalf(
			"executed data-edge cases = %d, want %d",
			len(executed),
			acceptedDataEdgeCaseCount,
		)
	}
}

func loadDataEdgeFixture(t *testing.T, baselineRoot string) dataEdgeFixture {
	t.Helper()
	content, err := os.ReadFile(filepath.Join(baselineRoot, "data-edges", "cases.json"))
	if err != nil {
		t.Fatalf("read data-edge fixture: %v", err)
	}
	var fixture dataEdgeFixture
	if err := json.Unmarshal(content, &fixture); err != nil {
		t.Fatalf("decode data-edge fixture: %v", err)
	}
	if fixture.FixtureID != "DATA-EDGES-001" ||
		len(fixture.Cases) != acceptedDataEdgeCaseCount {
		t.Fatalf(
			"data-edge fixture identity/count = %q/%d",
			fixture.FixtureID,
			len(fixture.Cases),
		)
	}
	return fixture
}

func replayHTTPDataEdge(
	t *testing.T,
	ctx context.Context,
	harness *Harness,
	runtime *parityRuntime,
	testCase dataEdgeCase,
) {
	t.Helper()
	rows := make([]FixtureRow, 0, len(testCase.Rows))
	for _, row := range testCase.Rows {
		if row.Code == nil || row.RawDefinitionJSON == nil {
			t.Fatalf("HTTP data-edge %s has a null SQL value", testCase.ID)
		}
		rows = append(rows, FixtureRow{
			Code: *row.Code, RawLosslessDefinitionJSON: *row.RawDefinitionJSON,
		})
	}
	if err := harness.ResetRows(ctx, rows); err != nil {
		t.Fatalf("load data edge: %v", err)
	}
	wantHTTP, err := GoHTTPExpectation(testCase.ID, HTTPExpectation{
		Status: testCase.Expected.Status, ContentType: testCase.Expected.ContentType,
		Body: []byte(testCase.Expected.RawBody),
	})
	if err != nil {
		t.Fatalf("resolve Go HTTP expectation: %v", err)
	}
	repetitions := max(testCase.Repetitions, 1)
	for range repetitions {
		actual := rawHTTPIntegrationGET(t, runtime.addresses.http, testCase.RequestPath)
		if err := CompareHTTP(wantHTTP, actual); err != nil {
			t.Errorf("HTTP data-edge parity: %v", err)
		}
	}

	if testCase.Expected.Outcome == "http-success" {
		expected, parseErr := ParseGRPCExpectations([]byte(testCase.Expected.RawBody))
		if parseErr != nil {
			t.Fatalf("parse gRPC data-edge expectation: %v", parseErr)
		}
		if testCase.RequestPath == "/products" {
			response, callErr := runtime.grpc.ListProducts(
				ctx,
				&productv1.ListProductsRequest{},
			)
			if callErr != nil {
				t.Fatalf("list data-edge products over gRPC: %v", callErr)
			}
			assertGRPCProducts(t, expected, response.GetProducts())
			return
		}
		response, callErr := runtime.grpc.GetProduct(
			ctx,
			&productv1.GetProductRequest{Code: *testCase.Rows[0].Code},
		)
		if callErr != nil {
			t.Fatalf("get data-edge product over gRPC: %v", callErr)
		}
		assertGRPCProducts(t, expected, []*productv1.Product{response.GetProduct()})
		return
	}

	_, getErr := runtime.grpc.GetProduct(
		ctx,
		&productv1.GetProductRequest{Code: *testCase.Rows[0].Code},
	)
	assertGRPCStatus(t, getErr, codes.Internal, "internal error")
	_, listErr := runtime.grpc.ListProducts(ctx, &productv1.ListProductsRequest{})
	assertGRPCStatus(t, listErr, codes.Internal, "internal error")
}

func replayConstraintDataEdge(
	t *testing.T,
	ctx context.Context,
	harness *Harness,
	testCase dataEdgeCase,
) {
	t.Helper()
	if len(testCase.Rows) != 1 {
		t.Fatalf("constraint case rows = %d, want 1", len(testCase.Rows))
	}
	row := testCase.Rows[0]
	var code any
	if row.Code != nil {
		code = *row.Code
	}
	var definition any
	if row.RawDefinitionJSON != nil {
		definition = *row.RawDefinitionJSON
	}
	_, err := harness.adminPool.Exec(
		ctx,
		"INSERT INTO public.product (code, definition) VALUES ($1, $2::jsonb)",
		code,
		definition,
	)
	var postgresErr *pgconn.PgError
	if !errors.As(err, &postgresErr) || postgresErr.SQLState() != testCase.Expected.SQLState {
		t.Fatalf("constraint error = %v, want SQLSTATE %s", err, testCase.Expected.SQLState)
	}
}

type failureFixture struct {
	FixtureID string        `json:"fixtureId"`
	Cases     []failureCase `json:"cases"`
}

type failureCase struct {
	ID                string   `json:"id"`
	ScenarioIDs       []string `json:"scenarioIds"`
	Target            string   `json:"target"`
	Setup             string   `json:"setup"`
	RawPath           string   `json:"rawPath"`
	Code              string   `json:"code"`
	RawDefinitionJSON string   `json:"rawDefinitionJson"`
	Expected          struct {
		Status      int    `json:"status"`
		ContentType string `json:"contentType"`
		RawBody     string `json:"rawBody"`
	} `json:"expected"`
}

func replayDirectFailures(
	t *testing.T,
	ctx context.Context,
	harness *Harness,
	runtime *parityRuntime,
	baselineRoot string,
) {
	t.Helper()
	fixture := loadFailureFixture(t, baselineRoot)
	executed := make(map[string]struct{}, len(fixture.Cases))
	for _, testCase := range fixture.Cases {
		t.Run(testCase.ID, func(t *testing.T) {
			executed[testCase.ID] = struct{}{}
			restore := prepareFailureCase(t, ctx, harness, testCase)
			if restore != nil {
				defer restore()
			}
			wantHTTP, err := GoHTTPExpectation(testCase.ID, HTTPExpectation{
				Status:      testCase.Expected.Status,
				ContentType: testCase.Expected.ContentType,
				Body:        []byte(testCase.Expected.RawBody),
			})
			if err != nil {
				t.Fatalf("resolve failure HTTP expectation: %v", err)
			}
			actual := rawHTTPIntegrationGET(t, runtime.addresses.http, testCase.RawPath)
			if err := CompareHTTP(wantHTTP, actual); err != nil {
				t.Errorf("direct HTTP failure parity: %v", err)
			}
			replayFailureGRPC(t, ctx, runtime.grpc, testCase)
		})
	}
	if len(executed) != acceptedFailureCaseCount {
		t.Fatalf(
			"executed direct failures = %d, want %d",
			len(executed),
			acceptedFailureCaseCount,
		)
	}
}

func loadFailureFixture(t *testing.T, baselineRoot string) failureFixture {
	t.Helper()
	content, err := os.ReadFile(filepath.Join(baselineRoot, "failures", "cases.json"))
	if err != nil {
		t.Fatalf("read failure fixture: %v", err)
	}
	var accepted failureFixture
	if err := json.Unmarshal(content, &accepted); err != nil {
		t.Fatalf("decode failure fixture: %v", err)
	}
	productCases := make([]failureCase, 0, acceptedFailureCaseCount)
	for _, testCase := range accepted.Cases {
		if testCase.Target == "product" {
			productCases = append(productCases, testCase)
		}
	}
	if accepted.FixtureID != "HTTP-FAILURES-001" ||
		len(productCases) != acceptedFailureCaseCount {
		t.Fatalf(
			"failure fixture identity/product-count = %q/%d",
			accepted.FixtureID,
			len(productCases),
		)
	}
	accepted.Cases = productCases
	return accepted
}

func prepareFailureCase(
	t *testing.T,
	ctx context.Context,
	harness *Harness,
	testCase failureCase,
) func() {
	t.Helper()
	switch testCase.Setup {
	case "empty":
		if err := harness.ResetRows(ctx, nil); err != nil {
			t.Fatalf("prepare empty failure case: %v", err)
		}
	case "single":
		if err := harness.ResetRows(ctx, []FixtureRow{{
			Code: "ONLY", RawLosslessDefinitionJSON: `{"name":"Only product"}`,
		}}); err != nil {
			t.Fatalf("prepare single failure case: %v", err)
		}
	case "raw-row":
		if err := harness.ResetRows(ctx, []FixtureRow{{
			Code: testCase.Code, RawLosslessDefinitionJSON: testCase.RawDefinitionJSON,
		}}); err != nil {
			t.Fatalf("prepare raw-row failure case: %v", err)
		}
	case "database-unavailable":
		if _, err := harness.LoadCatalog(ctx, FixtureQA); err != nil {
			t.Fatalf("prepare database failure catalog: %v", err)
		}
		execAdmin(t, harness, "REVOKE SELECT ON TABLE public.product FROM "+runtimeRole)
		return func() {
			execAdmin(
				t,
				harness,
				"GRANT SELECT ON TABLE public.product TO "+runtimeRole,
			)
		}
	default:
		t.Fatalf("unknown direct failure setup %q", testCase.Setup)
	}
	return nil
}

func replayFailureGRPC(
	t *testing.T,
	ctx context.Context,
	client productv1.ProductServiceClient,
	testCase failureCase,
) {
	t.Helper()
	if testCase.ID == "FAIL-PRODUCT-LOOKUP-BAD-ENCODING-001" {
		t.Log(
			"gRPC not applicable: malformed percent encoding is an HTTP request-target case",
		)
		return
	}
	if testCase.Setup == "raw-row" {
		_, err := client.GetProduct(ctx, &productv1.GetProductRequest{Code: testCase.Code})
		assertGRPCStatus(t, err, codes.Internal, "internal error")
		_, err = client.ListProducts(ctx, &productv1.ListProductsRequest{})
		assertGRPCStatus(t, err, codes.Internal, "internal error")
		return
	}
	if testCase.Setup == "database-unavailable" {
		if testCase.RawPath == "/products" {
			_, err := client.ListProducts(ctx, &productv1.ListProductsRequest{})
			assertGRPCStatus(t, err, codes.Unavailable, "service unavailable")
			return
		}
		_, err := client.GetProduct(ctx, &productv1.GetProductRequest{Code: "CAR"})
		assertGRPCStatus(t, err, codes.Unavailable, "service unavailable")
		return
	}

	expected, err := ParseGRPCExpectations([]byte(testCase.Expected.RawBody))
	if testCase.RawPath == "/products" || testCase.RawPath == "/products/" {
		response, callErr := client.ListProducts(ctx, &productv1.ListProductsRequest{})
		if callErr != nil {
			t.Fatalf("list direct failure setup over gRPC: %v", callErr)
		}
		if err != nil {
			t.Fatalf("parse direct list expectation: %v", err)
		}
		assertGRPCProducts(t, expected, response.GetProducts())
		return
	}
	code, decodeErr := url.PathUnescape(strings.TrimPrefix(testCase.RawPath, "/products/"))
	if decodeErr != nil {
		t.Fatalf("decode accepted lookup path: %v", decodeErr)
	}
	response, callErr := client.GetProduct(ctx, &productv1.GetProductRequest{Code: code})
	if testCase.Expected.Status == http.StatusNotFound {
		assertGRPCStatus(t, callErr, codes.NotFound, "product not found")
		return
	}
	if callErr != nil {
		t.Fatalf("get direct failure setup over gRPC: %v", callErr)
	}
	if err != nil {
		t.Fatalf("parse direct get expectation: %v", err)
	}
	assertGRPCProducts(t, expected, []*productv1.Product{response.GetProduct()})
}

func assertGRPCStatus(t *testing.T, err error, wantCode codes.Code, wantMessage string) {
	t.Helper()
	if status.Code(err) != wantCode || status.Convert(err).Message() != wantMessage {
		t.Fatalf(
			"gRPC status/message = %s/%q, want %s/%q: %v",
			status.Code(err),
			status.Convert(err).Message(),
			wantCode,
			wantMessage,
			err,
		)
	}
}
