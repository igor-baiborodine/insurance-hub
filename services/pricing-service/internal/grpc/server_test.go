package grpc_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
	grpcgo "google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/descriptorpb"

	"github.com/igor-baiborodine/insurance-hub/libs/servicekit/logger"

	pricingv1 "github.com/igor-baiborodine/insurance-hub/services/pricing-service/gen/pricing/v1"
	"github.com/igor-baiborodine/insurance-hub/services/pricing-service/internal/application"
	"github.com/igor-baiborodine/insurance-hub/services/pricing-service/internal/domain"
	transport "github.com/igor-baiborodine/insurance-hub/services/pricing-service/internal/grpc"
)

func TestPricingService_GeneratedIdentity(t *testing.T) {
	// given
	const (
		wantFile       = "pricing/v1/pricing_service.proto"
		wantPackage    = "pricing.v1"
		wantModule     = "github.com/igor-baiborodine/insurance-hub/services/pricing-service"
		wantService    = "pricing.v1.PricingService"
		wantFullMethod = "/pricing.v1.PricingService/CalculatePrice"
	)

	// when
	descriptor := (&pricingv1.CalculatePriceRequest{}).ProtoReflect().Descriptor().ParentFile()

	// then
	if descriptor.Path() != wantFile || string(descriptor.Package()) != wantPackage {
		t.Errorf("descriptor identity = (%q, %q)", descriptor.Path(), descriptor.Package())
	}
	options, ok := descriptor.Options().(*descriptorpb.FileOptions)
	if !ok {
		t.Fatalf("descriptor options type = %T", descriptor.Options())
	}
	if options.GetGoPackage() != wantModule+"/gen/pricing/v1;pricingv1" {
		t.Errorf("go_package = %q", options.GetGoPackage())
	}
	service := descriptor.Services().ByName("PricingService")
	if service == nil || string(service.FullName()) != wantService {
		t.Errorf("service descriptor = %v", service)
	}
	if pricingv1.PricingService_CalculatePrice_FullMethodName != wantFullMethod {
		t.Errorf("full method = %q", pricingv1.PricingService_CalculatePrice_FullMethodName)
	}
}

func TestPricingService_CalculatePrice_MapsRequestAndExactQuote(t *testing.T) {
	// given
	var calls atomic.Int64
	calculate := func(
		ctx context.Context,
		request application.CalculatePriceRequest,
	) (domain.Quote, error) {
		calls.Add(1)
		if err := ctx.Err(); err != nil {
			return domain.Quote{}, err
		}
		if request.ProductCode != "TRI" ||
			request.PolicyFrom.Format(time.DateOnly) != "2017-04-16" ||
			request.PolicyTo.Format(time.DateOnly) != "2018-04-15" ||
			len(request.SelectedCovers) != 2 ||
			len(request.Answers) != 2 {
			t.Fatalf("mapped request = %+v", request)
		}
		if request.Answers[0].Kind != domain.NumericAnswer ||
			request.Answers[0].Number.String() != "1.00" ||
			request.Answers[1].Kind != domain.TextAnswer || request.Answers[1].Text != "1.00" {
			t.Fatalf("typed answers = %+v", request.Answers)
		}
		return quote(t, "78", map[string]string{"C1": "26", "C2": "52"}), nil
	}
	client := newPricingClient(
		t,
		calculate,
		noop.NewTracerProvider(),
		propagation.TraceContext{},
		new(bytes.Buffer),
	)

	// when
	response, err := client.CalculatePrice(context.Background(), validRequest())
	// then
	if err != nil {
		t.Fatalf("CalculatePrice: %v", err)
	}
	if calls.Load() != 1 || response.GetTotalPrice() != "78" ||
		response.GetCoversPrices()["C1"] != "26" || response.GetCoversPrices()["C2"] != "52" {
		t.Errorf("response = %v, calls = %d", response, calls.Load())
	}
}

func TestPricingService_CalculatePrice_RejectsInvalidRequestsBeforeCalculation(t *testing.T) {
	// given
	var calls atomic.Int64
	calculate := func(context.Context, application.CalculatePriceRequest) (domain.Quote, error) {
		calls.Add(1)
		return quote(t, "1", map[string]string{"C1": "1"}), nil
	}
	client := newPricingClient(
		t,
		calculate,
		noop.NewTracerProvider(),
		propagation.TraceContext{},
		new(bytes.Buffer),
	)
	tests := []struct {
		name    string
		request *pricingv1.CalculatePriceRequest
	}{
		{name: "missing fields", request: &pricingv1.CalculatePriceRequest{}},
		{name: "invalid calendar date", request: func() *pricingv1.CalculatePriceRequest {
			request := validRequest()
			request.PolicyFrom = "2025-02-30"
			return request
		}()},
		{name: "missing answer choice", request: func() *pricingv1.CalculatePriceRequest {
			request := validRequest()
			request.Answers[0].Value = nil
			return request
		}()},
		{name: "invalid decimal", request: func() *pricingv1.CalculatePriceRequest {
			request := validRequest()
			request.Answers[0].Value = &pricingv1.Answer_Number{Number: "NaN"}
			return request
		}()},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// when
			response, err := client.CalculatePrice(context.Background(), test.request)

			// then
			if status.Code(err) != codes.InvalidArgument ||
				status.Convert(err).Message() != "invalid request" {
				t.Fatalf("result = (%v, %v)", response, err)
			}
			if response != nil {
				t.Errorf("response = %v, want nil", response)
			}
		})
	}
	if calls.Load() != 0 {
		t.Errorf("calculation calls = %d, want 0", calls.Load())
	}
}

func TestPricingService_CalculatePrice_MapsSafeFailures(t *testing.T) {
	// given
	tests := []struct {
		name, message string
		failure       error
		code          codes.Code
	}{
		{
			name:    "canceled",
			failure: context.Canceled,
			code:    codes.Canceled,
			message: context.Canceled.Error(),
		},
		{
			name:    "deadline",
			failure: context.DeadlineExceeded,
			code:    codes.DeadlineExceeded,
			message: context.DeadlineExceeded.Error(),
		},
		{
			name:    "storage unavailable",
			failure: application.ErrReaderUnavailable,
			code:    codes.Unavailable,
			message: "service unavailable",
		},
		{
			name:    "calculation",
			failure: application.ErrCalculation,
			code:    codes.Internal,
			message: "internal error",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// given
			client := newPricingClient(
				t,
				func(context.Context, application.CalculatePriceRequest) (domain.Quote, error) {
					return domain.Quote{}, test.failure
				},
				noop.NewTracerProvider(),
				propagation.TraceContext{},
				new(bytes.Buffer),
			)

			// when
			response, err := client.CalculatePrice(context.Background(), validRequest())

			// then
			if response != nil || status.Code(err) != test.code ||
				status.Convert(err).Message() != test.message {
				t.Fatalf("result = (%v, %v)", response, err)
			}
		})
	}
}

func TestPricingService_CalculatePrice_SanitizesInternalFailureAndPropagatesTrace(t *testing.T) {
	// given
	const privateMarker = "private-adapter-marker"
	spanRecorder := tracetest.NewSpanRecorder()
	tracerProvider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(spanRecorder))
	t.Cleanup(func() { _ = tracerProvider.Shutdown(context.Background()) })
	handlerContext := make(chan trace.SpanContext, 1)
	calculate := func(ctx context.Context, _ application.CalculatePriceRequest) (domain.Quote, error) {
		handlerContext <- trace.SpanContextFromContext(ctx)
		return domain.Quote{}, errors.New(privateMarker)
	}
	var logs bytes.Buffer
	client := newPricingClient(t, calculate, tracerProvider, propagation.TraceContext{}, &logs)
	parentCtx, parentSpan := tracerProvider.Tracer("test").Start(context.Background(), "parent")
	parent := parentSpan.SpanContext()

	// when
	response, err := client.CalculatePrice(parentCtx, validRequest())
	parentSpan.End()

	// then
	if response != nil || status.Code(err) != codes.Internal ||
		status.Convert(err).Message() != "internal error" {
		t.Fatalf("result = (%v, %v)", response, err)
	}
	serverContext := <-handlerContext
	if serverContext.TraceID() != parent.TraceID() {
		t.Errorf("trace = %v, want %v", serverContext.TraceID(), parent.TraceID())
	}
	if strings.Contains(logs.String(), privateMarker) {
		t.Fatalf("logs expose private failure: %s", logs.String())
	}
}

func TestNewServer_RejectsMissingDependenciesAndLimits(t *testing.T) {
	// given
	log := logger.New(new(bytes.Buffer), "pricing-service", 0)
	tracerProvider := noop.NewTracerProvider()
	propagator := propagation.TraceContext{}
	calculate := transport.CalculatePrice(
		func(context.Context, application.CalculatePriceRequest) (domain.Quote, error) {
			return quote(t, "1", map[string]string{"C1": "1"}), nil
		},
	)
	tests := []struct {
		name       string
		log        *slog.Logger
		tracer     trace.TracerProvider
		propagator propagation.TextMapPropagator
		settings   transport.Settings
		calculate  transport.CalculatePrice
	}{
		{
			name:       "logger",
			tracer:     tracerProvider,
			propagator: propagator,
			settings:   defaultSettings(),
			calculate:  calculate,
		},
		{
			name:       "tracer",
			log:        log,
			propagator: propagator,
			settings:   defaultSettings(),
			calculate:  calculate,
		},
		{
			name:      "propagator",
			log:       log,
			tracer:    tracerProvider,
			settings:  defaultSettings(),
			calculate: calculate,
		},
		{
			name:       "handler",
			log:        log,
			tracer:     tracerProvider,
			propagator: propagator,
			settings:   defaultSettings(),
		},
		{
			name:       "limits",
			log:        log,
			tracer:     tracerProvider,
			propagator: propagator,
			calculate:  calculate,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// when
			server, err := transport.NewServer(
				test.log,
				test.tracer,
				test.propagator,
				test.settings,
				test.calculate,
			)

			// then
			if err == nil || server != nil {
				t.Fatalf("NewServer() = (%v, %v)", server, err)
			}
		})
	}
}

func validRequest() *pricingv1.CalculatePriceRequest {
	return &pricingv1.CalculatePriceRequest{
		ProductCode: "TRI", PolicyFrom: "2017-04-16", PolicyTo: "2018-04-15",
		SelectedCovers: []string{"C1", "C2"},
		Answers: []*pricingv1.Answer{
			{
				QuestionCode: "NUM_OF_ADULTS",
				Value:        &pricingv1.Answer_Number{Number: "1.00"},
			},
			{QuestionCode: "DESTINATION", Value: &pricingv1.Answer_Text{Text: "1.00"}},
		},
	}
}

func quote(t *testing.T, total string, covers map[string]string) domain.Quote {
	t.Helper()
	mapped := make(map[string]domain.Decimal, len(covers))
	for code, value := range covers {
		decimal, err := domain.NewDecimal(value)
		if err != nil {
			t.Fatal(err)
		}
		mapped[code] = decimal
	}
	decimal, err := domain.NewDecimal(total)
	if err != nil {
		t.Fatal(err)
	}
	return domain.Quote{Total: decimal, Covers: mapped}
}

func newPricingClient(
	t *testing.T,
	calculate transport.CalculatePrice,
	tracerProvider trace.TracerProvider,
	propagator propagation.TextMapPropagator,
	logs *bytes.Buffer,
) pricingv1.PricingServiceClient {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server, err := transport.NewServer(
		logger.New(logs, "pricing-service", 0), tracerProvider, propagator,
		defaultSettings(), calculate,
	)
	if err != nil {
		_ = listener.Close()
		t.Fatal(err)
	}
	serveResult := make(chan error, 1)
	go func() { serveResult <- server.Serve(listener) }()
	connection, err := grpcgo.NewClient(
		listener.Addr().String(),
		grpcgo.WithTransportCredentials(insecure.NewCredentials()),
		grpcgo.WithStatsHandler(otelgrpc.NewClientHandler(
			otelgrpc.WithTracerProvider(tracerProvider),
			otelgrpc.WithPropagators(propagator),
			otelgrpc.WithMeterProvider(metricnoop.NewMeterProvider()),
		)),
	)
	if err != nil {
		server.Stop()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = connection.Close()
		server.Stop()
		_ = listener.Close()
		select {
		case err := <-serveResult:
			if err != nil && !errors.Is(err, grpcgo.ErrServerStopped) {
				t.Errorf("serve: %v", err)
			}
		case <-time.After(time.Second):
			t.Error("server did not stop")
		}
	})
	return pricingv1.NewPricingServiceClient(connection)
}

func defaultSettings() transport.Settings {
	return transport.Settings{
		RequestTimeout:  time.Second,
		MaxReceiveBytes: 1048576,
		MaxSendBytes:    8388608,
	}
}
