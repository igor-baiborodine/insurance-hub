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

	documentv1 "github.com/igor-baiborodine/insurance-hub/services/document-service/gen/document/v1"
	"github.com/igor-baiborodine/insurance-hub/services/document-service/internal/application"
	"github.com/igor-baiborodine/insurance-hub/services/document-service/internal/telemetry"
)

const internalMessage = "internal error"

// FindDocumentsExecutor is the application boundary used by the RPC transport.
type FindDocumentsExecutor interface {
	Execute(context.Context, string) (application.LookupResult, error)
}

type Settings struct {
	RequestTimeout       time.Duration
	MaxReceiveBytes      int
	MaxSendBytes         int
	MaximumResponseBytes int
	Observer             *telemetry.RequestObserver
	LookupObserver       *telemetry.LookupObserver
	MeterProvider        metric.MeterProvider
}

// NewServer constructs the instrumented server and registers the Document lookup RPC.
func NewServer(
	logger *slog.Logger,
	tracerProvider trace.TracerProvider,
	propagator propagation.TextMapPropagator,
	settings Settings,
	find FindDocumentsExecutor,
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
	if find == nil {
		return nil, errors.New("create gRPC server: find-documents executor is required")
	}
	if settings.RequestTimeout <= 0 || settings.MaxReceiveBytes <= 0 ||
		settings.MaxSendBytes <= 0 || settings.MaximumResponseBytes <= 0 {
		return nil, errors.New(
			"create gRPC server: request timeout and message limits must be positive",
		)
	}
	validator, err := protovalidate.New(
		protovalidate.WithMessages(&documentv1.FindDocumentsRequest{}),
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
		grpcgo.StatsHandler(&requestStatsHandler{
			observer: settings.Observer,
			Handler: otelgrpc.NewServerHandler(
				otelgrpc.WithTracerProvider(tracerProvider),
				otelgrpc.WithPropagators(propagator),
				otelgrpc.WithMeterProvider(meterProvider),
				otelgrpc.WithFilter(func(info *stats.RPCTagInfo) bool {
					return info.FullMethodName ==
						documentv1.DocumentService_FindDocuments_FullMethodName
				}),
			),
		}),
		grpcgo.UnaryInterceptor(validationInterceptor(logger, validator)),
	)
	documentv1.RegisterDocumentServiceServer(server, &documentServer{
		logger: logger, requestTimeout: settings.RequestTimeout,
		maximumResponseBytes: settings.MaximumResponseBytes,
		lookupObserver:       settings.LookupObserver, find: find,
	})
	return server, nil
}

type documentServer struct {
	documentv1.UnimplementedDocumentServiceServer
	logger               *slog.Logger
	requestTimeout       time.Duration
	maximumResponseBytes int
	lookupObserver       *telemetry.LookupObserver
	find                 FindDocumentsExecutor
}

func (server *documentServer) FindDocuments(
	ctx context.Context,
	request *documentv1.FindDocumentsRequest,
) (*documentv1.FindDocumentsResponse, error) {
	if request.GetPolicyNumber() == "" {
		return nil, status.Error(codes.InvalidArgument, "invalid request")
	}
	requestCtx, cancel := context.WithTimeout(ctx, server.requestTimeout)
	defer cancel()
	result, err := server.find.Execute(requestCtx, request.GetPolicyNumber())
	if err == nil {
		err = requestCtx.Err()
	}
	if server.lookupObserver != nil {
		server.lookupObserver.Record(requestCtx, len(result.Documents), result.Omitted, err)
	}
	if err != nil {
		return nil, server.mapError(
			ctx,
			documentv1.DocumentService_FindDocuments_FullMethodName,
			err,
		)
	}
	response := &documentv1.FindDocumentsResponse{
		Documents: make([]*documentv1.Document, 0, len(result.Documents)),
	}
	for _, document := range result.Documents {
		response.Documents = append(response.Documents, &documentv1.Document{
			PolicyNumber: document.PolicyNumber,
			Content:      document.Content,
		})
	}
	if proto.Size(response) > server.maximumResponseBytes {
		return nil, status.Error(codes.ResourceExhausted, "response limit exceeded")
	}
	return response, nil
}

func (server *documentServer) mapError(ctx context.Context, method string, err error) error {
	switch {
	case errors.Is(err, context.Canceled):
		return status.Error(codes.Canceled, context.Canceled.Error())
	case errors.Is(err, context.DeadlineExceeded):
		return status.Error(codes.DeadlineExceeded, context.DeadlineExceeded.Error())
	case errors.Is(err, application.ErrResponseLimit):
		return status.Error(codes.ResourceExhausted, "response limit exceeded")
	case errors.Is(err, application.ErrUnavailable):
		return status.Error(codes.Unavailable, "service unavailable")
	default:
		server.logger.ErrorContext(
			ctx,
			"document request failed",
			slog.String("rpc", method),
		)
		return status.Error(codes.Internal, internalMessage)
	}
}

func recognizedRPC(method string) bool {
	return rpcOperation(method) != ""
}

func rpcOperation(method string) string {
	if method == documentv1.DocumentService_FindDocuments_FullMethodName {
		return telemetry.OperationFindDocuments
	}
	return ""
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
			logger.ErrorContext(
				ctx,
				"request validation failed",
				slog.String("rpc", info.FullMethod),
			)
			return nil, status.Error(codes.Internal, internalMessage)
		}
		if err := validator.Validate(message); err != nil {
			if _, ok := errors.AsType[*protovalidate.ValidationError](err); ok {
				return nil, status.Error(codes.InvalidArgument, "invalid request")
			}
			logger.ErrorContext(
				ctx,
				"request validation failed",
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
	if handler.observer != nil && recognizedRPC(info.FullMethodName) {
		return context.WithValue(
			ctx,
			requestStateKey{},
			handler.observer.StartGRPC(ctx, rpcOperation(info.FullMethodName)),
		)
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
	case codes.NotFound:
		return "NOT_FOUND"
	case codes.AlreadyExists:
		return "ALREADY_EXISTS"
	case codes.PermissionDenied:
		return "PERMISSION_DENIED"
	case codes.ResourceExhausted:
		return "RESOURCE_EXHAUSTED"
	case codes.Unknown:
		return "UNKNOWN"
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
