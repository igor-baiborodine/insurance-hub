package grpc

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

	scaffoldv1 "github.com/igor-baiborodine/insurance-hub/templates/go-service/gen/scaffold/v1"
	"github.com/igor-baiborodine/insurance-hub/templates/go-service/internal/telemetry"
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

type Settings struct {
	RequestTimeout  time.Duration
	MaxReceiveBytes int
	MaxSendBytes    int
	Observer        *telemetry.RequestObserver
}

// NewServer constructs the instrumented server and registers the generated ExampleService.
func NewServer(
	logger *slog.Logger,
	tracerProvider trace.TracerProvider,
	propagator propagation.TextMapPropagator,
	settings Settings,
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

	if settings.RequestTimeout <= 0 || settings.MaxReceiveBytes <= 0 ||
		settings.MaxSendBytes <= 0 {
		return nil, errors.New(
			"create gRPC server: request timeout and message limits must be positive",
		)
	}

	validator, err := protovalidate.New(
		protovalidate.WithMessages(&scaffoldv1.EchoRequest{}),
		protovalidate.WithDisableLazy(),
	)
	if err != nil {
		return nil, errors.New("create gRPC server: initialize request validation")
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
						return info.FullMethodName == scaffoldv1.ExampleService_Echo_FullMethodName
					}),
				),
			},
		),
		grpcgo.UnaryInterceptor(validationInterceptor(logger, validator)),
	)
	scaffoldv1.RegisterExampleServiceServer(server, &exampleServer{
		logger:         logger,
		requestTimeout: settings.RequestTimeout,
		echo:           echo,
	})
	return server, nil
}

type exampleServer struct {
	scaffoldv1.UnimplementedExampleServiceServer
	logger         *slog.Logger
	requestTimeout time.Duration
	echo           Echo
}

func (server *exampleServer) Echo(
	ctx context.Context,
	request *scaffoldv1.EchoRequest,
) (*scaffoldv1.EchoResponse, error) {
	requestCtx, cancel := context.WithTimeout(ctx, server.requestTimeout)
	defer cancel()
	message, err := server.echo(requestCtx, request.GetMessage())
	if err == nil {
		err = requestCtx.Err()
	}
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return nil, status.Error(codes.Canceled, context.Canceled.Error())
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, status.Error(
				codes.DeadlineExceeded,
				context.DeadlineExceeded.Error(),
			)
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
		info.FullMethodName == scaffoldv1.ExampleService_Echo_FullMethodName {
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
