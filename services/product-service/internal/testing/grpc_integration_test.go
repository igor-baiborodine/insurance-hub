//go:build integration

package integrationtest

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace/noop"
	grpcgo "google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	productv1 "github.com/igor-baiborodine/insurance-hub/services/product-service/gen/product/v1"
	"github.com/igor-baiborodine/insurance-hub/services/product-service/internal/application"
	"github.com/igor-baiborodine/insurance-hub/services/product-service/internal/config"
	transport "github.com/igor-baiborodine/insurance-hub/services/product-service/internal/grpc"
	"github.com/igor-baiborodine/insurance-hub/services/product-service/internal/logger"
	"github.com/igor-baiborodine/insurance-hub/services/product-service/internal/postgres"
)

func TestProductGRPC(t *testing.T) {
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
	t.Setenv("PRODUCT_DATABASE_URL", harness.RuntimeDatabaseURL())
	settings, err := config.Load()
	if err != nil {
		t.Fatalf("load Product configuration: %v", err)
	}
	reader, err := postgres.Open(ctx, settings.Database)
	if err != nil {
		t.Fatalf("open reader: %v", err)
	}
	readerClosed := false
	t.Cleanup(func() {
		if !readerClosed {
			reader.Close()
		}
	})
	listProducts, err := application.NewListProducts(reader)
	if err != nil {
		t.Fatal(err)
	}
	getProduct, err := application.NewGetProduct(reader)
	if err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	client := startGRPCIntegrationServer(
		t,
		settings,
		transport.ListProducts(listProducts.Execute),
		transport.GetProduct(getProduct.Execute),
		&logs,
	)
	expectations := loadQAGRPCExpectations(t, baselineRoot)

	t.Run("lists and gets the accepted QA catalog anonymously", func(t *testing.T) {
		// when
		listResponse, listErr := client.ListProducts(ctx, &productv1.ListProductsRequest{})

		// then
		if listErr != nil {
			t.Fatalf("list products: %v", listErr)
		}
		assertGRPCProducts(
			t,
			expectations["HTTP-DIRECT-LIST-001"],
			listResponse.GetProducts(),
		)
		for _, code := range []string{"CAR", "FAI", "HSI", "TRI"} {
			// when
			getResponse, getErr := client.GetProduct(
				ctx,
				&productv1.GetProductRequest{Code: code},
			)

			// then
			if getErr != nil {
				t.Fatalf("get %s: %v", code, getErr)
			}
			assertGRPCProducts(
				t,
				expectations["HTTP-DIRECT-GET-"+code+"-001"],
				[]*productv1.Product{getResponse.GetProduct()},
			)
		}
	})

	t.Run("validates empty and exact missing codes", func(t *testing.T) {
		// when
		_, emptyErr := client.GetProduct(ctx, &productv1.GetProductRequest{})
		_, missingErr := client.GetProduct(
			ctx,
			&productv1.GetProductRequest{Code: "car"},
		)

		// then
		assertSafeStatus(t, emptyErr, codes.InvalidArgument, "invalid request", &logs)
		assertSafeStatus(t, missingErr, codes.NotFound, "product not found", &logs)
	})

	t.Run("returns an empty catalog", func(t *testing.T) {
		// given
		if resetErr := harness.ResetRows(ctx, nil); resetErr != nil {
			t.Fatalf("reset empty catalog: %v", resetErr)
		}

		// when
		response, listErr := client.ListProducts(ctx, &productv1.ListProductsRequest{})

		// then
		if listErr != nil || response == nil || len(response.GetProducts()) != 0 {
			t.Fatalf("empty list = %v, error = %v", response, listErr)
		}
	})

	t.Run("maps corrupt storage to safe internal statuses", func(t *testing.T) {
		// given
		if resetErr := harness.ResetRows(ctx, []FixtureRow{
			{Code: "GOOD", RawLosslessDefinitionJSON: `{}`},
			{
				Code:                      "CORRUPT",
				RawLosslessDefinitionJSON: `{"questions":[{"type":"numeric","unexpected":true}]}`,
			},
		}); resetErr != nil {
			t.Fatalf("reset corrupt catalog: %v", resetErr)
		}

		// when
		_, listErr := client.ListProducts(ctx, &productv1.ListProductsRequest{})
		_, getErr := client.GetProduct(
			ctx,
			&productv1.GetProductRequest{Code: "CORRUPT"},
		)

		// then
		assertSafeStatus(t, listErr, codes.Internal, "internal error", &logs)
		assertSafeStatus(t, getErr, codes.Internal, "internal error", &logs)
	})

	t.Run("maps caller cancellation", func(t *testing.T) {
		// given
		canceledCtx, cancelCall := context.WithCancel(context.Background())
		cancelCall()

		// when
		_, callErr := client.ListProducts(canceledCtx, &productv1.ListProductsRequest{})

		// then
		if status.Code(callErr) != codes.Canceled {
			t.Fatalf("status = %v, want Canceled: %v", status.Code(callErr), callErr)
		}
	})

	t.Run("maps unavailable database without retry or detail", func(t *testing.T) {
		// given
		reader.Close()
		readerClosed = true

		// when
		_, listErr := client.ListProducts(ctx, &productv1.ListProductsRequest{})
		_, getErr := client.GetProduct(ctx, &productv1.GetProductRequest{Code: "CAR"})

		// then
		assertSafeStatus(t, listErr, codes.Unavailable, "service unavailable", &logs)
		assertSafeStatus(t, getErr, codes.Unavailable, "service unavailable", &logs)
	})
}

func startGRPCIntegrationServer(
	t *testing.T,
	settings config.Config,
	list transport.ListProducts,
	get transport.GetProduct,
	logs *bytes.Buffer,
) productv1.ProductServiceClient {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	server, err := transport.NewServer(
		logger.New(logs, settings.ServiceName, settings.LogLevel),
		noop.NewTracerProvider(),
		propagation.TraceContext{},
		transport.Settings{
			RequestTimeout:  settings.RequestTimeout,
			MaxReceiveBytes: settings.GRPC.MaxReceiveBytes,
			MaxSendBytes:    settings.GRPC.MaxSendBytes,
		},
		list,
		get,
	)
	if err != nil {
		_ = listener.Close()
		t.Fatalf("new gRPC server: %v", err)
	}
	serveResult := make(chan error, 1)
	go func() { serveResult <- server.Serve(listener) }()
	connection, err := grpcgo.NewClient(
		listener.Addr().String(),
		grpcgo.WithTransportCredentials(insecure.NewCredentials()),
		grpcgo.WithDefaultCallOptions(
			grpcgo.MaxCallSendMsgSize(settings.GRPC.MaxReceiveBytes),
			grpcgo.MaxCallRecvMsgSize(settings.GRPC.MaxSendBytes),
		),
	)
	if err != nil {
		server.Stop()
		_ = listener.Close()
		t.Fatalf("new generated client: %v", err)
	}
	t.Cleanup(func() {
		if err := connection.Close(); err != nil {
			t.Errorf("close gRPC client: %v", err)
		}
		server.Stop()
		_ = listener.Close()
		select {
		case err := <-serveResult:
			if err != nil && !errors.Is(err, grpcgo.ErrServerStopped) {
				t.Errorf("serve gRPC: %v", err)
			}
		case <-time.After(time.Second):
			t.Error("gRPC server did not stop")
		}
	})
	return productv1.NewProductServiceClient(connection)
}

func loadQAGRPCExpectations(
	t *testing.T,
	baselineRoot string,
) map[string][]GRPCProductExpectation {
	t.Helper()
	content, err := os.ReadFile(filepath.Join(baselineRoot, "http", "qa.json"))
	if err != nil {
		t.Fatalf("read QA HTTP expectations: %v", err)
	}
	var fixture struct {
		Observations []struct {
			ScenarioID string `json:"scenarioId"`
			Boundary   string `json:"boundary"`
			Response   struct {
				Status  int    `json:"status"`
				RawBody string `json:"rawBody"`
			} `json:"response"`
		} `json:"observations"`
	}
	if err := json.Unmarshal(content, &fixture); err != nil {
		t.Fatalf("decode QA HTTP expectations: %v", err)
	}
	result := make(map[string][]GRPCProductExpectation)
	for _, observation := range fixture.Observations {
		if observation.Boundary != "direct" || observation.Response.Status != 200 {
			continue
		}
		products, parseErr := ParseGRPCExpectations([]byte(observation.Response.RawBody))
		if parseErr != nil {
			t.Fatalf("parse %s: %v", observation.ScenarioID, parseErr)
		}
		result[observation.ScenarioID] = products
	}
	if len(result) != 5 {
		t.Fatalf("QA direct success expectations = %d, want 5", len(result))
	}
	return result
}

func assertGRPCProducts(
	t *testing.T,
	expected []GRPCProductExpectation,
	actual []*productv1.Product,
) {
	t.Helper()
	wantByCode := make(map[string]GRPCProductExpectation, len(expected))
	for _, product := range expected {
		wantByCode[product.Code] = product
	}
	gotByCode := make(map[string]GRPCProductExpectation, len(actual))
	for _, product := range actual {
		converted := grpcExpectation(product)
		if _, duplicate := gotByCode[converted.Code]; duplicate {
			t.Fatalf("duplicate gRPC product code %q", converted.Code)
		}
		gotByCode[converted.Code] = converted
	}
	if !reflect.DeepEqual(gotByCode, wantByCode) {
		t.Errorf("gRPC products = %#v, want %#v", gotByCode, wantByCode)
	}
}

func grpcExpectation(product *productv1.Product) GRPCProductExpectation {
	result := GRPCProductExpectation{
		Code:               product.GetCode(),
		Name:               product.GetName(),
		Image:              product.GetImage(),
		Description:        product.GetDescription(),
		MaxNumberOfInsured: product.GetMaxNumberOfInsured(),
		Icon:               product.GetIcon(),
	}
	for _, cover := range product.GetCovers() {
		converted := GRPCCoverExpectation{
			Code:        cover.GetCode(),
			Name:        cover.GetName(),
			Description: cover.GetDescription(),
			Optional:    cover.GetOptional(),
		}
		if cover.SumInsured != nil {
			value := cover.GetSumInsured()
			converted.SumInsured = &value
		}
		result.Covers = append(result.Covers, converted)
	}
	for _, question := range product.GetQuestions() {
		converted := GRPCQuestionExpectation{
			Code:  question.GetCode(),
			Text:  question.GetText(),
			Index: question.GetIndex(),
		}
		switch {
		case question.GetChoice() != nil:
			converted.Kind = "choice"
			for _, choice := range question.GetChoice().GetChoices() {
				converted.Choices = append(converted.Choices, GRPCChoiceExpectation{
					Code: choice.GetCode(), Label: choice.GetLabel(),
				})
			}
		case question.GetDate() != nil:
			converted.Kind = "date"
		case question.GetNumeric() != nil:
			converted.Kind = "numeric"
		}
		result.Questions = append(result.Questions, converted)
	}
	return result
}

func assertSafeStatus(
	t *testing.T,
	err error,
	wantCode codes.Code,
	wantMessage string,
	logs *bytes.Buffer,
) {
	t.Helper()
	if status.Code(err) != wantCode || status.Convert(err).Message() != wantMessage {
		t.Fatalf("status=%v message=%q, want %v/%q: %v",
			status.Code(err), status.Convert(err).Message(), wantCode, wantMessage, err)
	}
	unsafe := []string{"postgres", "reader", "password", "secret", "definition", "127.0.0.1"}
	for _, marker := range unsafe {
		if strings.Contains(status.Convert(err).Message(), marker) ||
			strings.Contains(logs.String(), marker) {
			t.Fatalf(
				"unsafe marker %q exposed: status=%v logs=%s",
				marker,
				err,
				logs.String(),
			)
		}
	}
}
