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
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	productv1 "github.com/igor-baiborodine/insurance-hub/services/product-service/gen/product/v1"
	"github.com/igor-baiborodine/insurance-hub/services/product-service/internal/application"
	"github.com/igor-baiborodine/insurance-hub/services/product-service/internal/domain"
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
		grpcgo.StatsHandler(otelgrpc.NewServerHandler(
			otelgrpc.WithTracerProvider(tracerProvider),
			otelgrpc.WithPropagators(propagator),
			otelgrpc.WithMeterProvider(metricnoop.NewMeterProvider()),
		)),
		grpcgo.UnaryInterceptor(validationInterceptor(logger, validator)),
	)
	productv1.RegisterProductServiceServer(server, &productServer{
		logger:         logger,
		requestTimeout: settings.RequestTimeout,
		listProducts:   listProducts,
		getProduct:     getProduct,
	})
	return server, nil
}

type productServer struct {
	productv1.UnimplementedProductServiceServer
	logger         *slog.Logger
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
			productv1.ProductService_ListProducts_FullMethodName,
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
				productv1.ProductService_ListProducts_FullMethodName,
				err,
			)
		}
		mapped, mapErr := mapProduct(requestCtx, product)
		if mapErr != nil {
			return nil, server.transportError(
				requestCtx,
				productv1.ProductService_ListProducts_FullMethodName,
				mapErr,
			)
		}
		response.Products = append(response.Products, mapped)
	}
	if err := requestCtx.Err(); err != nil {
		return nil, server.transportError(
			requestCtx,
			productv1.ProductService_ListProducts_FullMethodName,
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
			productv1.ProductService_GetProduct_FullMethodName,
			err,
		)
	}
	mapped, err := mapProduct(requestCtx, product)
	if err != nil {
		return nil, server.transportError(
			requestCtx,
			productv1.ProductService_GetProduct_FullMethodName,
			err,
		)
	}
	if err := requestCtx.Err(); err != nil {
		return nil, server.transportError(
			requestCtx,
			productv1.ProductService_GetProduct_FullMethodName,
			err,
		)
	}
	return &productv1.GetProductResponse{Product: mapped}, nil
}

func (server *productServer) transportError(
	ctx context.Context,
	rpc string,
	err error,
) error {
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
		server.logger.ErrorContext(
			ctx,
			"Product gRPC request failed",
			slog.String("rpc", rpc),
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
			logger.ErrorContext(ctx, "Product gRPC request validation failed",
				slog.String("rpc", info.FullMethod),
			)
			return nil, status.Error(codes.Internal, internalMessage)
		}
		if err := validator.Validate(message); err != nil {
			if _, ok := errors.AsType[*protovalidate.ValidationError](err); ok {
				return nil, status.Error(codes.InvalidArgument, invalidMessage)
			}
			logger.ErrorContext(ctx, "Product gRPC request validation failed",
				slog.String("rpc", info.FullMethod),
			)
			return nil, status.Error(codes.Internal, internalMessage)
		}
		return handler(ctx, request)
	}
}
