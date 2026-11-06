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

	dashboardv1 "github.com/igor-baiborodine/insurance-hub/services/dashboard-service/gen/dashboard/v1"
	"github.com/igor-baiborodine/insurance-hub/services/dashboard-service/internal/application"
	"github.com/igor-baiborodine/insurance-hub/services/dashboard-service/internal/domain"
	"github.com/igor-baiborodine/insurance-hub/services/dashboard-service/internal/telemetry"
)

const internalMessage = "internal error"

// TotalSalesExecutor is the application boundary used by the RPC transport.
type TotalSalesExecutor interface {
	Execute(context.Context, application.TotalSalesQuery) (application.TotalSalesResult, error)
}

// AgentSalesExecutor is the application boundary used by the agent-sales RPC transport.
type AgentSalesExecutor interface {
	Execute(context.Context, application.AgentSalesQuery) (application.AgentSalesResult, error)
}

type Settings struct {
	RequestTimeout  time.Duration
	MaxReceiveBytes int
	MaxSendBytes    int
	Observer        *telemetry.RequestObserver
	MeterProvider   metric.MeterProvider
}

// NewServer constructs the instrumented server and registers DashboardService.
func NewServer(
	logger *slog.Logger,
	tracerProvider trace.TracerProvider,
	propagator propagation.TextMapPropagator,
	settings Settings,
	totalSales TotalSalesExecutor,
	agentSales AgentSalesExecutor,
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
	if totalSales == nil {
		return nil, errors.New("create gRPC server: total-sales executor is required")
	}
	if agentSales == nil {
		return nil, errors.New("create gRPC server: agent-sales executor is required")
	}
	if settings.RequestTimeout <= 0 || settings.MaxReceiveBytes <= 0 ||
		settings.MaxSendBytes <= 0 {
		return nil, errors.New(
			"create gRPC server: request timeout and message limits must be positive",
		)
	}

	validator, err := protovalidate.New(
		protovalidate.WithMessages(
			&dashboardv1.GetTotalSalesRequest{},
			&dashboardv1.GetAgentSalesRequest{},
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
		grpcgo.StatsHandler(&requestStatsHandler{
			observer: settings.Observer,
			Handler: otelgrpc.NewServerHandler(
				otelgrpc.WithTracerProvider(tracerProvider),
				otelgrpc.WithPropagators(propagator),
				otelgrpc.WithMeterProvider(meterProvider),
				otelgrpc.WithFilter(func(info *stats.RPCTagInfo) bool {
					return recognizedRPC(info.FullMethodName)
				}),
			),
		}),
		grpcgo.UnaryInterceptor(validationInterceptor(logger, validator)),
	)
	dashboardv1.RegisterDashboardServiceServer(server, &dashboardServer{
		logger: logger, requestTimeout: settings.RequestTimeout,
		totalSales: totalSales, agentSales: agentSales,
	})
	return server, nil
}

type dashboardServer struct {
	dashboardv1.UnimplementedDashboardServiceServer
	logger         *slog.Logger
	requestTimeout time.Duration
	totalSales     TotalSalesExecutor
	agentSales     AgentSalesExecutor
}

func (server *dashboardServer) GetTotalSales(
	ctx context.Context,
	request *dashboardv1.GetTotalSalesRequest,
) (*dashboardv1.GetTotalSalesResponse, error) {
	dateFrom, err := domain.ParseDate(request.GetSaleDateFrom())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid request")
	}
	dateTo, err := domain.ParseDate(request.GetSaleDateTo())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid request")
	}
	product := domain.AllProducts()
	if request.ProductCode != nil {
		product = domain.ExactProduct(request.GetProductCode())
	}

	requestCtx, cancel := context.WithTimeout(ctx, server.requestTimeout)
	defer cancel()
	result, err := server.totalSales.Execute(requestCtx, application.TotalSalesQuery{
		Product: product, DateFrom: dateFrom, DateTo: dateTo,
	})
	if err == nil {
		err = requestCtx.Err()
	}
	if err != nil {
		return nil, server.mapError(
			ctx,
			dashboardv1.DashboardService_GetTotalSales_FullMethodName,
			err,
		)
	}
	response := &dashboardv1.GetTotalSalesResponse{
		Total: &dashboardv1.SalesValue{
			PoliciesCount:           result.Total.PoliciesCount(),
			PremiumAmountMinorUnits: result.Total.PremiumAmount().MinorUnits(),
		},
		PerProductTotal: make(
			map[string]*dashboardv1.SalesValue,
			len(result.PerProductTotal),
		),
	}
	for productCode, value := range result.PerProductTotal {
		response.PerProductTotal[productCode] = &dashboardv1.SalesValue{
			PoliciesCount:           value.PoliciesCount(),
			PremiumAmountMinorUnits: value.PremiumAmount().MinorUnits(),
		}
	}
	return response, nil
}

func (server *dashboardServer) GetAgentSales(
	ctx context.Context,
	request *dashboardv1.GetAgentSalesRequest,
) (*dashboardv1.GetAgentSalesResponse, error) {
	dateFrom, err := domain.ParseDate(request.GetSaleDateFrom())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid request")
	}
	dateTo, err := domain.ParseDate(request.GetSaleDateTo())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid request")
	}
	agent := domain.AllAgents()
	if request.AgentLogin != nil {
		agent = domain.ExactAgent(request.GetAgentLogin())
	}
	product := domain.AllProducts()
	if request.ProductCode != nil {
		product = domain.ExactProduct(request.GetProductCode())
	}

	requestCtx, cancel := context.WithTimeout(ctx, server.requestTimeout)
	defer cancel()
	result, err := server.agentSales.Execute(requestCtx, application.AgentSalesQuery{
		Agent: agent, Product: product, DateFrom: dateFrom, DateTo: dateTo,
	})
	if err == nil {
		err = requestCtx.Err()
	}
	if err != nil {
		return nil, server.mapError(
			ctx,
			dashboardv1.DashboardService_GetAgentSales_FullMethodName,
			err,
		)
	}
	response := &dashboardv1.GetAgentSalesResponse{
		PerAgentTotal: make(
			map[string]*dashboardv1.AgentSalesValue,
			len(result.PerAgentTotal),
		),
	}
	for agentLogin, value := range result.PerAgentTotal {
		response.PerAgentTotal[agentLogin] = &dashboardv1.AgentSalesValue{
			PoliciesCount: value.PoliciesCount(),
			PremiumAmount: value.PremiumAmount().String(),
		}
	}
	return response, nil
}

func (server *dashboardServer) mapError(ctx context.Context, method string, err error) error {
	switch {
	case errors.Is(err, context.Canceled):
		return status.Error(codes.Canceled, context.Canceled.Error())
	case errors.Is(err, context.DeadlineExceeded):
		return status.Error(codes.DeadlineExceeded, context.DeadlineExceeded.Error())
	case errors.Is(err, application.ErrTotalSalesUnavailable),
		errors.Is(err, application.ErrAgentSalesUnavailable):
		return status.Error(codes.Unavailable, "service unavailable")
	default:
		server.logger.ErrorContext(
			ctx,
			"dashboard request failed",
			slog.String("rpc", method),
		)
		return status.Error(codes.Internal, internalMessage)
	}
}

func recognizedRPC(method string) bool {
	return method == dashboardv1.DashboardService_GetTotalSales_FullMethodName ||
		method == dashboardv1.DashboardService_GetAgentSales_FullMethodName
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
