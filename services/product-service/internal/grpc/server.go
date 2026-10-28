// Package productgrpc owns the Product catalog gRPC transport boundary.
package productgrpc

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"buf.build/go/protovalidate"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
	grpcgo "google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/stats"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	productv1 "github.com/igor-baiborodine/insurance-hub/services/product-service/gen/product/v1"
	"github.com/igor-baiborodine/insurance-hub/services/product-service/internal/application"
	"github.com/igor-baiborodine/insurance-hub/services/product-service/internal/domain"
	"github.com/igor-baiborodine/insurance-hub/services/product-service/internal/telemetry"
)

const (
	internalMessage    = "internal error"
	unavailableMessage = "service unavailable"
	invalidMessage     = "invalid request"
)

type (
	ListProducts func(context.Context) ([]domain.Product, error)
	GetProduct   func(context.Context, string) (domain.Product, error)
)

type Settings struct {
	RequestTimeout  time.Duration
	MaxReceiveBytes int
	MaxSendBytes    int
	Observer        *telemetry.RequestObserver
}

// NewServer constructs the instrumented Product server and registers both unary methods.
func NewServer(
	logger *slog.Logger,
	tracerProvider trace.TracerProvider,
	propagator propagation.TextMapPropagator,
	settings Settings,
	listProducts ListProducts,
	getProduct GetProduct,
) (*grpcgo.Server, error) {
	if logger == nil {
		return nil, errors.New("create Product gRPC server: logger is required")
	}
	if tracerProvider == nil {
		return nil, errors.New("create Product gRPC server: tracer provider is required")
	}
	if propagator == nil {
		return nil, errors.New("create Product gRPC server: propagator is required")
	}
	if settings.RequestTimeout <= 0 {
		return nil, errors.New(
			"create Product gRPC server: request timeout must be positive",
		)
	}
	if settings.MaxReceiveBytes <= 0 || settings.MaxSendBytes <= 0 {
		return nil, errors.New(
			"create Product gRPC server: message limits must be positive",
		)
	}
	if listProducts == nil || getProduct == nil {
		return nil, errors.New("create Product gRPC server: catalog handlers are required")
	}

	validator, err := protovalidate.New(
		protovalidate.WithMessages(
			&productv1.ListProductsRequest{},
			&productv1.GetProductRequest{},
		),
		protovalidate.WithDisableLazy(),
	)
	if err != nil {
		return nil, errors.New("create Product gRPC server: initialize request validation")
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
					otelgrpc.WithMeterProvider(metricnoop.NewMeterProvider()),
					otelgrpc.WithFilter(func(info *stats.RPCTagInfo) bool {
						return info.FullMethodName == productv1.ProductService_ListProducts_FullMethodName ||
							info.FullMethodName == productv1.ProductService_GetProduct_FullMethodName
					}),
				),
			},
		),
		grpcgo.ChainUnaryInterceptor(
			validationInterceptor(validator),
		),
	)
	productv1.RegisterProductServiceServer(server, &productServer{
		requestTimeout: settings.RequestTimeout,
		listProducts:   listProducts,
		getProduct:     getProduct,
	})
	return server, nil
}

type productServer struct {
	productv1.UnimplementedProductServiceServer
	requestTimeout time.Duration
	listProducts   ListProducts
	getProduct     GetProduct
}

func (server *productServer) ListProducts(
	ctx context.Context,
	_ *productv1.ListProductsRequest,
) (*productv1.ListProductsResponse, error) {
	requestCtx, cancel := context.WithTimeout(ctx, server.requestTimeout)
	defer cancel()
	products, err := server.listProducts(requestCtx)
	if err != nil {
		return nil, server.transportError(
			requestCtx,
			err,
		)
	}
	response := &productv1.ListProductsResponse{
		Products: make([]*productv1.Product, 0, len(products)),
	}
	for _, product := range products {
		if err := requestCtx.Err(); err != nil {
			return nil, server.transportError(
				requestCtx,
				err,
			)
		}
		mapped, mapErr := mapProduct(requestCtx, product)
		if mapErr != nil {
			return nil, server.transportError(
				requestCtx,
				mapErr,
			)
		}
		response.Products = append(response.Products, mapped)
	}
	if err := requestCtx.Err(); err != nil {
		return nil, server.transportError(
			requestCtx,
			err,
		)
	}
	return response, nil
}

func (server *productServer) GetProduct(
	ctx context.Context,
	request *productv1.GetProductRequest,
) (*productv1.GetProductResponse, error) {
	requestCtx, cancel := context.WithTimeout(ctx, server.requestTimeout)
	defer cancel()
	product, err := server.getProduct(requestCtx, request.GetCode())
	if err != nil {
		return nil, server.transportError(
			requestCtx,
			err,
		)
	}
	mapped, err := mapProduct(requestCtx, product)
	if err != nil {
		return nil, server.transportError(
			requestCtx,
			err,
		)
	}
	if err := requestCtx.Err(); err != nil {
		return nil, server.transportError(
			requestCtx,
			err,
		)
	}
	return &productv1.GetProductResponse{Product: mapped}, nil
}

func (server *productServer) transportError(
	ctx context.Context,
	err error,
) error {
	if errors.Is(err, errInvalidDomainProduct) {
		telemetry.MarkError(ctx, application.ErrInvalidDefinition)
	} else {
		telemetry.MarkError(ctx, err)
	}
	switch {
	case errors.Is(err, context.Canceled):
		return status.Error(codes.Canceled, context.Canceled.Error())
	case errors.Is(err, context.DeadlineExceeded):
		return status.Error(codes.DeadlineExceeded, context.DeadlineExceeded.Error())
	case errors.Is(err, application.ErrCodeRequired):
		return status.Error(codes.InvalidArgument, invalidMessage)
	case errors.Is(err, application.ErrProductNotFound):
		return status.Error(codes.NotFound, "product not found")
	case errors.Is(err, application.ErrUnavailable):
		return status.Error(codes.Unavailable, unavailableMessage)
	case errors.Is(err, application.ErrInvalidDefinition):
		return status.Error(codes.Internal, internalMessage)
	default:
		return status.Error(codes.Internal, internalMessage)
	}
}

func validationInterceptor(
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
			return nil, status.Error(codes.Internal, internalMessage)
		}
		if err := validator.Validate(message); err != nil {
			if _, ok := errors.AsType[*protovalidate.ValidationError](err); ok {
				return nil, status.Error(codes.InvalidArgument, invalidMessage)
			}
			return nil, status.Error(codes.Internal, internalMessage)
		}
		return handler(ctx, request)
	}
}

type requestStateKey struct{}

// The stats End event includes failures that occur after the unary handler returns,
// including response encoding and the configured send-size check. TagRPC starts
// observation before decoding so registered calls rejected before dispatch also finish.
type requestStatsHandler struct {
	stats.Handler
	observer *telemetry.RequestObserver
}

func (handler *requestStatsHandler) TagRPC(
	ctx context.Context,
	info *stats.RPCTagInfo,
) context.Context {
	ctx = handler.Handler.TagRPC(ctx, info)
	if handler.observer == nil {
		return ctx
	}
	var operation string
	switch info.FullMethodName {
	case productv1.ProductService_ListProducts_FullMethodName:
		operation = telemetry.OperationList
	case productv1.ProductService_GetProduct_FullMethodName:
		operation = telemetry.OperationGet
	default:
		return ctx
	}
	ctx, request := handler.observer.StartGRPC(ctx, operation)
	return context.WithValue(ctx, requestStateKey{}, request)
}

func (handler *requestStatsHandler) HandleRPC(ctx context.Context, event stats.RPCStats) {
	if end, ok := event.(*stats.End); ok {
		if request, ok := ctx.Value(requestStateKey{}).(*telemetry.Request); ok {
			request.Finish(
				ctx,
				"rpc.response.status_code",
				rpcCodeName(status.Code(end.Error)),
			)
		}
	}
	// Product attributes must be set before otelgrpc ends its server span.
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
