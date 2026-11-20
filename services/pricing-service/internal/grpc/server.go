package grpc

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"buf.build/go/protovalidate"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"go.opentelemetry.io/otel/metric"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
	grpcgo "google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/stats"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	pricingv1 "github.com/igor-baiborodine/insurance-hub/services/pricing-service/gen/pricing/v1"
	"github.com/igor-baiborodine/insurance-hub/services/pricing-service/internal/application"
	"github.com/igor-baiborodine/insurance-hub/services/pricing-service/internal/domain"
	"github.com/igor-baiborodine/insurance-hub/services/pricing-service/internal/telemetry"
)

const (
	internalMessage    = "internal error"
	unavailableMessage = "service unavailable"
	invalidMessage     = "invalid request"
)

// CalculatePrice invokes the application calculation independently of transport details.
type CalculatePrice func(context.Context, application.CalculatePriceRequest) (domain.Quote, error)

type Settings struct {
	RequestTimeout  time.Duration
	MaxReceiveBytes int
	MaxSendBytes    int
	Observer        *telemetry.RequestObserver
	MeterProvider   metric.MeterProvider
}

// NewServer constructs the instrumented server and registers the generated PricingService.
func NewServer(
	logger *slog.Logger,
	tracerProvider trace.TracerProvider,
	propagator propagation.TextMapPropagator,
	settings Settings,
	calculatePrice CalculatePrice,
) (*grpcgo.Server, error) {
	if logger == nil {
		return nil, errors.New("create gRPC server: logger is required")
	}
	if tracerProvider == nil {
		return nil, errors.New("create gRPC server: tracer provider is required")
	}
	if propagator == nil {
		return nil, errors.New("create gRPC server: propagator is required")
	}
	if calculatePrice == nil {
		return nil, errors.New("create gRPC server: calculate-price handler is required")
	}

	if settings.RequestTimeout <= 0 || settings.MaxReceiveBytes <= 0 ||
		settings.MaxSendBytes <= 0 {
		return nil, errors.New(
			"create gRPC server: request timeout and message limits must be positive",
		)
	}

	validator, err := protovalidate.New(
		protovalidate.WithMessages(
			&pricingv1.CalculatePriceRequest{},
			&pricingv1.Answer{},
		),
		protovalidate.WithDisableLazy(),
	)
	if err != nil {
		return nil, errors.New("create gRPC server: initialize request validation")
	}
	meterProvider := settings.MeterProvider
	if meterProvider == nil {
		meterProvider = metricnoop.NewMeterProvider()
	}

	server := grpcgo.NewServer(
		grpcgo.MaxRecvMsgSize(settings.MaxReceiveBytes),
		grpcgo.MaxSendMsgSize(settings.MaxSendBytes),
		grpcgo.StatsHandler(
			&requestStatsHandler{
				observer: settings.Observer,
				Handler: otelgrpc.NewServerHandler(
					otelgrpc.WithTracerProvider(tracerProvider),
					otelgrpc.WithPropagators(propagator),
					otelgrpc.WithMeterProvider(meterProvider),
					otelgrpc.WithFilter(func(info *stats.RPCTagInfo) bool {
						return info.FullMethodName == pricingv1.PricingService_CalculatePrice_FullMethodName
					}),
				),
			},
		),
		grpcgo.UnaryInterceptor(validationInterceptor(logger, validator)),
	)
	pricingv1.RegisterPricingServiceServer(server, &pricingServer{
		logger:         logger,
		requestTimeout: settings.RequestTimeout,
		calculatePrice: calculatePrice,
	})
	return server, nil
}

type pricingServer struct {
	pricingv1.UnimplementedPricingServiceServer
	logger         *slog.Logger
	requestTimeout time.Duration
	calculatePrice CalculatePrice
}

func (server *pricingServer) CalculatePrice(
	ctx context.Context,
	request *pricingv1.CalculatePriceRequest,
) (*pricingv1.CalculatePriceResponse, error) {
	requestCtx, cancel := context.WithTimeout(ctx, server.requestTimeout)
	defer cancel()
	mapped, err := mapRequest(request)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, invalidMessage)
	}
	quote, err := server.calculatePrice(requestCtx, mapped)
	if err != nil {
		return nil, server.transportError(ctx, err)
	}
	if err := requestCtx.Err(); err != nil {
		return nil, server.transportError(ctx, err)
	}
	response := &pricingv1.CalculatePriceResponse{
		TotalPrice:   quote.Total.String(),
		CoversPrices: make(map[string]string, len(quote.Covers)),
	}
	for code, price := range quote.Covers {
		response.CoversPrices[code] = price.String()
	}
	return response, nil
}

func mapRequest(
	request *pricingv1.CalculatePriceRequest,
) (application.CalculatePriceRequest, error) {
	policyFrom, err := time.Parse(time.DateOnly, request.GetPolicyFrom())
	if err != nil || policyFrom.Format(time.DateOnly) != request.GetPolicyFrom() {
		return application.CalculatePriceRequest{}, application.ErrInvalidRequest
	}
	policyTo, err := time.Parse(time.DateOnly, request.GetPolicyTo())
	if err != nil || policyTo.Format(time.DateOnly) != request.GetPolicyTo() {
		return application.CalculatePriceRequest{}, application.ErrInvalidRequest
	}
	answers := make([]domain.Answer, 0, len(request.GetAnswers()))
	for _, answer := range request.GetAnswers() {
		if answer == nil {
			return application.CalculatePriceRequest{}, application.ErrInvalidRequest
		}
		mapped := domain.Answer{QuestionCode: answer.GetQuestionCode()}
		switch value := answer.GetValue().(type) {
		case *pricingv1.Answer_Text:
			mapped.Kind, mapped.Text = domain.TextAnswer, value.Text
		case *pricingv1.Answer_Number:
			number, parseErr := domain.NewDecimal(value.Number)
			if parseErr != nil {
				return application.CalculatePriceRequest{}, application.ErrInvalidRequest
			}
			mapped.Kind, mapped.Number = domain.NumericAnswer, number
		default:
			return application.CalculatePriceRequest{}, application.ErrInvalidRequest
		}
		answers = append(answers, mapped)
	}
	return application.CalculatePriceRequest{
		ProductCode:    request.GetProductCode(),
		PolicyFrom:     policyFrom,
		PolicyTo:       policyTo,
		SelectedCovers: append([]string(nil), request.GetSelectedCovers()...),
		Answers:        answers,
	}, nil
}

func (server *pricingServer) transportError(ctx context.Context, err error) error {
	switch {
	case errors.Is(err, context.Canceled):
		return status.Error(codes.Canceled, context.Canceled.Error())
	case errors.Is(err, context.DeadlineExceeded):
		return status.Error(codes.DeadlineExceeded, context.DeadlineExceeded.Error())
	case errors.Is(err, application.ErrReaderUnavailable):
		return status.Error(codes.Unavailable, unavailableMessage)
	default:
		server.logger.ErrorContext(ctx, "calculate-price request failed",
			slog.String("rpc", pricingv1.PricingService_CalculatePrice_FullMethodName),
		)
		return status.Error(codes.Internal, internalMessage)
	}
}

func validationInterceptor(
	logger *slog.Logger,
	validator protovalidate.Validator,
) grpcgo.UnaryServerInterceptor {
	return func(
		ctx context.Context,
		request any,
		info *grpcgo.UnaryServerInfo,
		handler grpcgo.UnaryHandler,
	) (any, error) {
		message, ok := request.(proto.Message)
		if !ok {
			logger.ErrorContext(ctx, "request validation failed",
				slog.String("rpc", info.FullMethod),
			)
			return nil, status.Error(codes.Internal, internalMessage)
		}
		if err := validator.Validate(message); err != nil {
			if _, ok := errors.AsType[*protovalidate.ValidationError](err); ok {
				return nil, status.Error(codes.InvalidArgument, "invalid request")
			}
			logger.ErrorContext(ctx, "request validation failed",
				slog.String("rpc", info.FullMethod),
			)
			return nil, status.Error(codes.Internal, internalMessage)
		}
		return handler(ctx, request)
	}
}

type requestStateKey struct{}

type requestStatsHandler struct {
	stats.Handler
	observer *telemetry.RequestObserver
}

func (handler *requestStatsHandler) TagRPC(
	ctx context.Context,
	info *stats.RPCTagInfo,
) context.Context {
	ctx = handler.Handler.TagRPC(ctx, info)
	if handler.observer != nil &&
		info.FullMethodName == pricingv1.PricingService_CalculatePrice_FullMethodName {
		return context.WithValue(ctx, requestStateKey{}, handler.observer.StartGRPC(ctx))
	}
	return ctx
}

func (handler *requestStatsHandler) HandleRPC(ctx context.Context, event stats.RPCStats) {
	if end, ok := event.(*stats.End); ok {
		if request, ok := ctx.Value(requestStateKey{}).(*telemetry.Request); ok {
			request.Finish(ctx, rpcCodeName(status.Code(end.Error)))
		}
	}
	handler.Handler.HandleRPC(ctx, event)
}

func rpcCodeName(code codes.Code) string {
	switch code {
	case codes.OK:
		return "OK"
	case codes.InvalidArgument:
		return "INVALID_ARGUMENT"
	case codes.Canceled:
		return "CANCELLED"
	case codes.DeadlineExceeded:
		return "DEADLINE_EXCEEDED"
	case codes.ResourceExhausted:
		return "RESOURCE_EXHAUSTED"
	case codes.Unknown:
		return "UNKNOWN"
	case codes.NotFound:
		return "NOT_FOUND"
	case codes.AlreadyExists:
		return "ALREADY_EXISTS"
	case codes.PermissionDenied:
		return "PERMISSION_DENIED"
	case codes.FailedPrecondition:
		return "FAILED_PRECONDITION"
	case codes.Aborted:
		return "ABORTED"
	case codes.OutOfRange:
		return "OUT_OF_RANGE"
	case codes.Unimplemented:
		return "UNIMPLEMENTED"
	case codes.Internal:
		return "INTERNAL"
	case codes.Unavailable:
		return "UNAVAILABLE"
	case codes.DataLoss:
		return "DATA_LOSS"
	case codes.Unauthenticated:
		return "UNAUTHENTICATED"
	default:
		return "UNKNOWN"
	}
}
