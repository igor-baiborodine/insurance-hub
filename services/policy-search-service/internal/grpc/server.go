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

	policysearchv1 "github.com/igor-baiborodine/insurance-hub/services/policy-search-service/gen/policysearch/v1"
	"github.com/igor-baiborodine/insurance-hub/services/policy-search-service/internal/domain"
	"github.com/igor-baiborodine/insurance-hub/services/policy-search-service/internal/telemetry"
)

const internalMessage = "internal error"

// SearchPolicies invokes the application use case without exposing transport types to it.
type SearchPolicies func(context.Context, string) ([]domain.Policy, error)

type Settings struct {
	RequestTimeout  time.Duration
	MaxReceiveBytes int
	MaxSendBytes    int
	Observer        *telemetry.RequestObserver
	MeterProvider   metric.MeterProvider
}

// NewServer constructs the instrumented server and registers PolicySearchService.
func NewServer(
	logger *slog.Logger,
	tracerProvider trace.TracerProvider,
	propagator propagation.TextMapPropagator,
	settings Settings,
	search SearchPolicies,
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
	if search == nil {
		return nil, errors.New("create gRPC server: search handler is required")
	}
	if settings.RequestTimeout <= 0 || settings.MaxReceiveBytes <= 0 ||
		settings.MaxSendBytes <= 0 {
		return nil, errors.New(
			"create gRPC server: request timeout and message limits must be positive",
		)
	}

	validator, err := protovalidate.New(
		protovalidate.WithMessages(&policysearchv1.SearchPoliciesRequest{}),
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
						return info.FullMethodName ==
							policysearchv1.PolicySearchService_SearchPolicies_FullMethodName
					}),
				),
			},
		),
		grpcgo.UnaryInterceptor(validationInterceptor(logger, validator)),
	)
	policysearchv1.RegisterPolicySearchServiceServer(server, &policySearchServer{
		logger:         logger,
		requestTimeout: settings.RequestTimeout,
		search:         search,
	})
	return server, nil
}

type policySearchServer struct {
	policysearchv1.UnimplementedPolicySearchServiceServer
	logger         *slog.Logger
	requestTimeout time.Duration
	search         SearchPolicies
}

func (server *policySearchServer) SearchPolicies(
	ctx context.Context,
	request *policysearchv1.SearchPoliciesRequest,
) (*policysearchv1.SearchPoliciesResponse, error) {
	if request == nil || request.Q == nil {
		return nil, status.Error(codes.InvalidArgument, "q is required")
	}
	requestCtx, cancel := context.WithTimeout(ctx, server.requestTimeout)
	defer cancel()
	policies, err := server.search(requestCtx, request.GetQ())
	if err == nil {
		err = requestCtx.Err()
	}
	if err != nil {
		switch {
		case errors.Is(err, context.Canceled):
			return nil, status.Error(codes.Canceled, context.Canceled.Error())
		case errors.Is(err, context.DeadlineExceeded):
			return nil, status.Error(
				codes.DeadlineExceeded,
				context.DeadlineExceeded.Error(),
			)
		default:
			server.logger.ErrorContext(ctx, "policy search failed",
				slog.String(
					"rpc",
					policysearchv1.PolicySearchService_SearchPolicies_FullMethodName,
				),
			)
			return nil, status.Error(codes.Internal, internalMessage)
		}
	}
	response := &policysearchv1.SearchPoliciesResponse{
		Policies: make([]*policysearchv1.Policy, len(policies)),
	}
	for index := range policies {
		response.Policies[index] = policyMessage(policies[index])
	}
	return response, nil
}

func policyMessage(policy domain.Policy) *policysearchv1.Policy {
	message := &policysearchv1.Policy{}
	if policy.Number != "" {
		message.Number = proto.String(policy.Number)
	}
	if policy.DateFrom != nil {
		message.DateFrom = proto.String(policy.DateFrom.String())
	}
	if policy.DateTo != nil {
		message.DateTo = proto.String(policy.DateTo.String())
	}
	if policy.PolicyHolder != "" {
		message.PolicyHolder = proto.String(policy.PolicyHolder)
	}
	return message
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
	if handler.observer != nil &&
		info.FullMethodName == policysearchv1.PolicySearchService_SearchPolicies_FullMethodName {
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
