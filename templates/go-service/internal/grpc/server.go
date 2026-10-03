package grpc

import (
	"context"
	"errors"
	"log/slog"

	"buf.build/go/protovalidate"
	scaffoldv1 "github.com/igor-baiborodine/insurance-hub/templates/go-service/gen/scaffold/v1"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
	grpcgo "google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

const internalMessage = "internal error"

// Echo handles the example's single responsibility independently of transport details.
type Echo func(context.Context, string) (string, error)

// IdentityEcho returns the supplied message while preserving a canceled request context.
func IdentityEcho(ctx context.Context, message string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return message, nil
}

// NewServer constructs the instrumented server and registers the generated ExampleService.
func NewServer(
	logger *slog.Logger,
	tracerProvider trace.TracerProvider,
	propagator propagation.TextMapPropagator,
	echo Echo,
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
	if echo == nil {
		return nil, errors.New("create gRPC server: echo handler is required")
	}

	validator, err := protovalidate.New(
		protovalidate.WithMessages(&scaffoldv1.EchoRequest{}),
		protovalidate.WithDisableLazy(),
	)
	if err != nil {
		return nil, errors.New("create gRPC server: initialize request validation")
	}

	server := grpcgo.NewServer(
		grpcgo.StatsHandler(otelgrpc.NewServerHandler(
			otelgrpc.WithTracerProvider(tracerProvider),
			otelgrpc.WithPropagators(propagator),
			otelgrpc.WithMeterProvider(metricnoop.NewMeterProvider()),
		)),
		grpcgo.UnaryInterceptor(validationInterceptor(logger, validator)),
	)
	scaffoldv1.RegisterExampleServiceServer(server, &exampleServer{
		logger: logger,
		echo:   echo,
	})
	return server, nil
}

type exampleServer struct {
	scaffoldv1.UnimplementedExampleServiceServer
	logger *slog.Logger
	echo   Echo
}

func (server *exampleServer) Echo(
	ctx context.Context,
	request *scaffoldv1.EchoRequest,
) (*scaffoldv1.EchoResponse, error) {
	message, err := server.echo(ctx, request.GetMessage())
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return nil, status.Error(codes.Canceled, context.Canceled.Error())
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, status.Error(codes.DeadlineExceeded, context.DeadlineExceeded.Error())
		}
		server.logger.ErrorContext(ctx, "echo request failed",
			slog.String("rpc", scaffoldv1.ExampleService_Echo_FullMethodName),
		)
		return nil, status.Error(codes.Internal, internalMessage)
	}
	return &scaffoldv1.EchoResponse{Message: message}, nil
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
