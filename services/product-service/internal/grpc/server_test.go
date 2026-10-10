package productgrpc_test

import (
	"bytes"
	"context"
	"errors"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
	grpcgo "google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	productv1 "github.com/igor-baiborodine/insurance-hub/services/product-service/gen/product/v1"
	"github.com/igor-baiborodine/insurance-hub/services/product-service/internal/application"
	"github.com/igor-baiborodine/insurance-hub/services/product-service/internal/domain"
	transport "github.com/igor-baiborodine/insurance-hub/services/product-service/internal/grpc"
	"github.com/igor-baiborodine/insurance-hub/services/product-service/internal/logger"
)

func TestProductService_MapsCompleteCatalog(t *testing.T) {
	// given
	product := richProduct(t)
	client := newProductClient(
		t,
		defaultSettings(),
		func(context.Context) ([]domain.Product, error) {
			return []domain.Product{product}, nil
		},
		func(_ context.Context, code string) (domain.Product, error) {
			if code != product.Code {
				t.Errorf("get code = %q, want %q", code, product.Code)
			}
			return product, nil
		},
		new(bytes.Buffer),
	)

	// when
	listResponse, listErr := client.ListProducts(
		context.Background(),
		&productv1.ListProductsRequest{},
	)
	getResponse, getErr := client.GetProduct(
		context.Background(),
		&productv1.GetProductRequest{Code: product.Code},
	)

	// then
	if listErr != nil || getErr != nil || len(listResponse.GetProducts()) != 1 {
		t.Fatalf(
			"responses: list=%v get=%v errors=(%v, %v)",
			listResponse,
			getResponse,
			listErr,
			getErr,
		)
	}
	assertMappedProduct(t, listResponse.GetProducts()[0])
	assertMappedProduct(t, getResponse.GetProduct())
}

func TestProductService_ReturnsEmptyList(t *testing.T) {
	// given
	client := newProductClient(
		t,
		defaultSettings(),
		func(context.Context) ([]domain.Product, error) { return []domain.Product{}, nil },
		func(context.Context, string) (domain.Product, error) {
			return domain.Product{}, application.ErrProductNotFound
		},
		new(bytes.Buffer),
	)

	// when
	response, err := client.ListProducts(context.Background(), &productv1.ListProductsRequest{})

	// then
	if err != nil || response == nil || len(response.GetProducts()) != 0 {
		t.Fatalf("empty response = %v, error = %v", response, err)
	}
}

func TestProductService_ValidatesGetBeforeApplication(t *testing.T) {
	// given
	var calls atomic.Int64
	client := newProductClient(
		t,
		defaultSettings(),
		func(context.Context) ([]domain.Product, error) { return nil, nil },
		func(context.Context, string) (domain.Product, error) {
			calls.Add(1)
			return domain.Product{}, nil
		},
		new(bytes.Buffer),
	)

	// when
	response, err := client.GetProduct(
		context.Background(),
		&productv1.GetProductRequest{},
	)

	// then
	if response != nil || status.Code(err) != codes.InvalidArgument ||
		status.Convert(err).Message() != "invalid request" || calls.Load() != 0 {
		t.Fatalf("response=%v status=%v message=%q calls=%d",
			response, status.Code(err), status.Convert(err).Message(), calls.Load())
	}
}

func TestProductService_MapsSafeApplicationErrorsOnce(t *testing.T) {
	privateMarker := "postgres://reader:secret@private/catalog raw-definition"
	tests := []struct {
		name        string
		handlerErr  error
		wantCode    codes.Code
		wantMessage string
	}{
		{"missing", application.ErrProductNotFound, codes.NotFound, "product not found"},
		{
			"invalid definition",
			errors.Join(application.ErrInvalidDefinition, errors.New(privateMarker)),
			codes.Internal,
			"internal error",
		},
		{
			"unavailable",
			errors.Join(application.ErrUnavailable, errors.New(privateMarker)),
			codes.Unavailable,
			"service unavailable",
		},
		{"canceled", context.Canceled, codes.Canceled, context.Canceled.Error()},
		{
			"deadline",
			context.DeadlineExceeded,
			codes.DeadlineExceeded,
			context.DeadlineExceeded.Error(),
		},
		{"unknown", errors.New(privateMarker), codes.Internal, "internal error"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// given
			var calls atomic.Int64
			var logs bytes.Buffer
			client := newProductClient(
				t,
				defaultSettings(),
				func(context.Context) ([]domain.Product, error) { return nil, nil },
				func(context.Context, string) (domain.Product, error) {
					calls.Add(1)
					return domain.Product{}, test.handlerErr
				},
				&logs,
			)

			// when
			response, err := client.GetProduct(
				context.Background(),
				&productv1.GetProductRequest{Code: "MISSING"},
			)

			// then
			if response != nil || status.Code(err) != test.wantCode ||
				status.Convert(err).
					Message() !=
					test.wantMessage || calls.Load() != 1 {
				t.Fatalf(
					"response=%v status=%v message=%q calls=%d",
					response,
					status.Code(err),
					status.Convert(err).Message(),
					calls.Load(),
				)
			}
			if strings.Contains(status.Convert(err).Message(), privateMarker) ||
				strings.Contains(logs.String(), privateMarker) {
				t.Fatalf(
					"private failure detail leaked: status=%v logs=%s",
					err,
					logs.String(),
				)
			}
		})
	}
}

func TestProductService_HonorsCallerCancellationAndConfiguredDeadline(t *testing.T) {
	tests := []struct {
		name          string
		settings      transport.Settings
		cancel        bool
		wantCode      codes.Code
		clientTimeout time.Duration
	}{
		{"caller cancellation", defaultSettings(), true, codes.Canceled, time.Second},
		{
			"caller deadline",
			defaultSettings(),
			false,
			codes.DeadlineExceeded,
			25 * time.Millisecond,
		},
		{
			"configured deadline",
			transport.Settings{
				RequestTimeout:  25 * time.Millisecond,
				MaxReceiveBytes: 1 << 20,
				MaxSendBytes:    8 << 20,
			},
			false,
			codes.DeadlineExceeded,
			time.Second,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// given
			started := make(chan struct{})
			client := newProductClient(
				t,
				test.settings,
				func(ctx context.Context) ([]domain.Product, error) {
					close(started)
					<-ctx.Done()
					return nil, ctx.Err()
				},
				func(context.Context, string) (domain.Product, error) {
					return domain.Product{}, nil
				},
				new(bytes.Buffer),
			)
			ctx, cancel := context.WithTimeout(context.Background(), test.clientTimeout)
			defer cancel()
			result := make(chan error, 1)
			go func() {
				_, err := client.ListProducts(ctx, &productv1.ListProductsRequest{})
				result <- err
			}()
			select {
			case <-started:
			case <-time.After(time.Second):
				t.Fatal("application handler did not start")
			}

			// when
			if test.cancel {
				cancel()
			}

			// then
			select {
			case err := <-result:
				if status.Code(err) != test.wantCode {
					t.Fatalf(
						"status = %v, want %v: %v",
						status.Code(err),
						test.wantCode,
						err,
					)
				}
			case <-time.After(time.Second):
				t.Fatal("bounded RPC did not return")
			}
		})
	}
}

func TestProductService_DeadlineDuringMappingPreventsSuccess(t *testing.T) {
	// given
	product := productWithChoices(t, 100_000)
	var applicationReturnedWithBudget atomic.Bool
	settings := defaultSettings()
	settings.RequestTimeout = time.Millisecond
	client := newProductClient(
		t,
		settings,
		func(ctx context.Context) ([]domain.Product, error) {
			applicationReturnedWithBudget.Store(ctx.Err() == nil)
			return []domain.Product{product}, nil
		},
		func(context.Context, string) (domain.Product, error) {
			return domain.Product{}, nil
		},
		new(bytes.Buffer),
	)

	// when
	response, err := client.ListProducts(
		context.Background(),
		&productv1.ListProductsRequest{},
	)

	// then
	if !applicationReturnedWithBudget.Load() {
		t.Fatal("application did not return before the request deadline")
	}
	if response != nil || status.Code(err) != codes.DeadlineExceeded {
		t.Fatalf(
			"response=%v status=%v error=%v",
			response,
			status.Code(err),
			err,
		)
	}
}

func TestProductService_EnforcesSendLimitWithoutTruncation(t *testing.T) {
	// given
	settings := defaultSettings()
	settings.MaxSendBytes = 64
	client := newProductClient(
		t,
		settings,
		func(context.Context) ([]domain.Product, error) {
			return []domain.Product{
				{Code: "BIG", Description: strings.Repeat("x", 1024)},
			}, nil
		},
		func(context.Context, string) (domain.Product, error) {
			return domain.Product{}, nil
		},
		new(bytes.Buffer),
	)

	// when
	response, err := client.ListProducts(context.Background(), &productv1.ListProductsRequest{})

	// then
	if response != nil || status.Code(err) != codes.ResourceExhausted {
		t.Fatalf("response=%v status=%v error=%v", response, status.Code(err), err)
	}
}

func TestProductService_EnforcesReceiveLimitBeforeApplication(t *testing.T) {
	// given
	settings := defaultSettings()
	settings.MaxReceiveBytes = 64
	var calls atomic.Int64
	client := newProductClient(
		t,
		settings,
		func(context.Context) ([]domain.Product, error) { return nil, nil },
		func(context.Context, string) (domain.Product, error) {
			calls.Add(1)
			return domain.Product{}, nil
		},
		new(bytes.Buffer),
	)

	// when
	response, err := client.GetProduct(
		context.Background(),
		&productv1.GetProductRequest{Code: strings.Repeat("x", 1024)},
	)

	// then
	if response != nil || status.Code(err) != codes.ResourceExhausted || calls.Load() != 0 {
		t.Fatalf("response=%v status=%v calls=%d error=%v",
			response, status.Code(err), calls.Load(), err)
	}
}

func TestNewServer_RejectsMissingDependenciesAndLimits(t *testing.T) {
	// given
	log := logger.New(new(bytes.Buffer), "product-service", 0)
	tracer := noop.NewTracerProvider()
	propagator := propagation.TraceContext{}
	list := transport.ListProducts(
		func(context.Context) ([]domain.Product, error) { return nil, nil },
	)
	get := transport.GetProduct(func(context.Context, string) (domain.Product, error) {
		return domain.Product{}, nil
	})
	tests := []struct {
		name       string
		log        bool
		tracer     bool
		propagator bool
		settings   transport.Settings
		list       transport.ListProducts
		get        transport.GetProduct
	}{
		{"logger", false, true, true, defaultSettings(), list, get},
		{"tracer", true, false, true, defaultSettings(), list, get},
		{"propagator", true, true, false, defaultSettings(), list, get},
		{
			"timeout",
			true,
			true,
			true,
			transport.Settings{MaxReceiveBytes: 1, MaxSendBytes: 1},
			list,
			get,
		},
		{
			"message limit",
			true,
			true,
			true,
			transport.Settings{RequestTimeout: time.Second},
			list,
			get,
		},
		{"list", true, true, true, defaultSettings(), nil, get},
		{"get", true, true, true, defaultSettings(), list, nil},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// given
			selectedLogger := log
			if !test.log {
				selectedLogger = nil
			}
			selectedTracer := traceProvider(tracer, test.tracer)
			selectedPropagator := textMapPropagator(propagator, test.propagator)

			// when
			server, err := transport.NewServer(
				selectedLogger,
				selectedTracer,
				selectedPropagator,
				test.settings,
				test.list,
				test.get,
			)

			// then
			if err == nil || server != nil {
				t.Fatalf("NewServer() = (%v, %v), want (nil, error)", server, err)
			}
		})
	}
}

func richProduct(t *testing.T) domain.Product {
	t.Helper()
	amount, err := domain.NewDecimal("12345678901234567890.2300")
	if err != nil {
		t.Fatal(err)
	}
	choice, _ := domain.NewQuestion("CHOICE", 3, "Choice", domain.ChoiceQuestion{
		Choices: []domain.Choice{{Code: "B", Label: "Second"}, {Code: "A", Label: "First"}},
	})
	date, _ := domain.NewQuestion("DATE", 1, "Date", domain.DateQuestion{})
	numeric, _ := domain.NewQuestion("NUMBER", 2, "Number", domain.NumericQuestion{})
	return domain.Product{
		Code:        "EDGE",
		Name:        "Rich",
		Image:       "/edge.png",
		Description: "Synthetic",
		Covers: []domain.Cover{
			{
				Code:        "C",
				Name:        "Cover",
				Description: "Exact",
				Optional:    true,
				SumInsured:  &amount,
			},
			{Code: "N", Name: "Nil"},
		},
		Questions: []domain.Question{
			choice,
			date,
			numeric,
		},
		MaxNumberOfInsured: 7,
		Icon:               "edge",
	}
}

func productWithChoices(t *testing.T, count int) domain.Product {
	t.Helper()
	choices := make([]domain.Choice, count)
	for index := range choices {
		choices[index] = domain.Choice{Code: "C", Label: "Choice"}
	}
	question, err := domain.NewQuestion(
		"CHOICE",
		1,
		"Choice",
		domain.ChoiceQuestion{Choices: choices},
	)
	if err != nil {
		t.Fatal(err)
	}
	return domain.Product{Code: "LARGE", Questions: []domain.Question{question}}
}

func assertMappedProduct(t *testing.T, product *productv1.Product) {
	t.Helper()
	if product == nil || product.GetCode() != "EDGE" || product.GetName() != "Rich" ||
		product.GetImage() != "/edge.png" || product.GetDescription() != "Synthetic" ||
		product.GetMaxNumberOfInsured() != 7 || product.GetIcon() != "edge" ||
		len(product.GetCovers()) != 2 || len(product.GetQuestions()) != 3 {
		t.Fatalf("mapped product = %+v", product)
	}
	if !product.Covers[0].GetOptional() || product.Covers[0].SumInsured == nil ||
		product.Covers[0].GetSumInsured() != "12345678901234567890.2300" ||
		product.Covers[1].SumInsured != nil {
		t.Errorf("mapped covers = %+v", product.Covers)
	}
	if product.Questions[0].GetChoice() == nil ||
		product.Questions[0].GetChoice().GetChoices()[0].GetCode() != "B" ||
		product.Questions[0].GetChoice().GetChoices()[1].GetCode() != "A" ||
		product.Questions[1].GetDate() == nil || product.Questions[2].GetNumeric() == nil {
		t.Errorf("mapped questions = %+v", product.Questions)
	}
}

func defaultSettings() transport.Settings {
	return transport.Settings{
		RequestTimeout: time.Second, MaxReceiveBytes: 1 << 20, MaxSendBytes: 8 << 20,
	}
}

func newProductClient(
	t *testing.T,
	settings transport.Settings,
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
		logger.New(logs, "product-service", 0),
		noop.NewTracerProvider(),
		propagation.TraceContext{},
		settings,
		list,
		get,
	)
	if err != nil {
		_ = listener.Close()
		t.Fatalf("new server: %v", err)
	}
	serveResult := make(chan error, 1)
	go func() { serveResult <- server.Serve(listener) }()
	connection, err := grpcgo.NewClient(
		listener.Addr().String(),
		grpcgo.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		server.Stop()
		_ = listener.Close()
		t.Fatalf("new client: %v", err)
	}
	t.Cleanup(func() {
		if err := connection.Close(); err != nil {
			t.Errorf("close client: %v", err)
		}
		server.Stop()
		_ = listener.Close()
		select {
		case err := <-serveResult:
			if err != nil && !errors.Is(err, grpcgo.ErrServerStopped) {
				t.Errorf("serve: %v", err)
			}
		case <-time.After(time.Second):
			t.Error("gRPC server did not stop")
		}
	})
	return productv1.NewProductServiceClient(connection)
}

func traceProvider(provider trace.TracerProvider, enabled bool) trace.TracerProvider {
	if !enabled {
		return nil
	}
	return provider
}

func textMapPropagator(
	propagator propagation.TraceContext,
	enabled bool,
) propagation.TextMapPropagator {
	if !enabled {
		return nil
	}
	return propagator
}
