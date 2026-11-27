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

	paymentv1 "github.com/igor-baiborodine/insurance-hub/services/payment-service/gen/payment/v1"
	"github.com/igor-baiborodine/insurance-hub/services/payment-service/internal/application"
	"github.com/igor-baiborodine/insurance-hub/services/payment-service/internal/telemetry"
)

const internalMessage = "internal error"

// AccountReads is the application boundary exposed through the private Payment API.
type AccountReads interface {
	List(context.Context) ([]application.Account, error)
	Balance(context.Context, string) (application.AccountBalance, error)
}

type Settings struct {
	RequestTimeout  time.Duration
	MaxReceiveBytes int
	MaxSendBytes    int
	Observer        *telemetry.RequestObserver
	MeterProvider   metric.MeterProvider
}

// NewServer constructs the instrumented server and registers the generated PaymentService.
func NewServer(
	logger *slog.Logger,
	tracerProvider trace.TracerProvider,
	propagator propagation.TextMapPropagator,
	settings Settings,
	reads AccountReads,
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
	if reads == nil {
		return nil, errors.New("create gRPC server: account reads are required")
	}

	if settings.RequestTimeout <= 0 || settings.MaxReceiveBytes <= 0 ||
		settings.MaxSendBytes <= 0 {
		return nil, errors.New(
			"create gRPC server: request timeout and message limits must be positive",
		)
	}

	validator, err := protovalidate.New(
		protovalidate.WithMessages(
			&paymentv1.ListAccountsRequest{},
			&paymentv1.GetAccountBalanceRequest{},
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
						_, observed := rpcOperation(info.FullMethodName)
						return observed
					}),
				),
			},
		),
		grpcgo.UnaryInterceptor(validationInterceptor(logger, validator)),
	)
	paymentv1.RegisterPaymentServiceServer(server, &paymentServer{
		logger:         logger,
		requestTimeout: settings.RequestTimeout,
		reads:          reads,
	})
	return server, nil
}

type paymentServer struct {
	paymentv1.UnimplementedPaymentServiceServer
	logger         *slog.Logger
	requestTimeout time.Duration
	reads          AccountReads
}

func (server *paymentServer) ListAccounts(
	ctx context.Context,
	_ *paymentv1.ListAccountsRequest,
) (*paymentv1.ListAccountsResponse, error) {
	requestCtx, cancel := context.WithTimeout(ctx, server.requestTimeout)
	defer cancel()
	accounts, err := server.reads.List(requestCtx)
	if err == nil {
		err = requestCtx.Err()
	}
	if err != nil {
		return nil, server.accountError(
			ctx,
			paymentv1.PaymentService_ListAccounts_FullMethodName,
			err,
		)
	}
	result := make([]*paymentv1.Account, 0, len(accounts))
	for _, account := range accounts {
		result = append(result, accountMessage(account))
	}
	return &paymentv1.ListAccountsResponse{Accounts: result}, nil
}

func (server *paymentServer) GetAccountBalance(
	ctx context.Context,
	request *paymentv1.GetAccountBalanceRequest,
) (*paymentv1.GetAccountBalanceResponse, error) {
	requestCtx, cancel := context.WithTimeout(ctx, server.requestTimeout)
	defer cancel()
	account, err := server.reads.Balance(requestCtx, request.GetAccountNumber())
	if err == nil {
		err = requestCtx.Err()
	}
	if err != nil {
		return nil, server.accountError(
			ctx,
			paymentv1.PaymentService_GetAccountBalance_FullMethodName,
			err,
		)
	}
	return &paymentv1.GetAccountBalanceResponse{Account: &paymentv1.AccountBalance{
		PolicyAccountNumber: account.PolicyAccountNumber,
		PolicyNumber:        account.PolicyNumber,
		Balance:             account.Balance,
		Created:             account.Created.UnixMilli(),
		Updated:             account.Updated.UnixMilli(),
	}}, nil
}

func accountMessage(account application.Account) *paymentv1.Account {
	return &paymentv1.Account{
		PolicyAccountNumber: account.PolicyAccountNumber,
		PolicyNumber:        account.PolicyNumber,
		Created:             account.Created.UnixMilli(),
		Updated:             account.Updated.UnixMilli(),
	}
}

func (server *paymentServer) accountError(ctx context.Context, method string, err error) error {
	switch {
	case errors.Is(err, context.Canceled):
		return status.Error(codes.Canceled, context.Canceled.Error())
	case errors.Is(err, context.DeadlineExceeded):
		return status.Error(codes.DeadlineExceeded, context.DeadlineExceeded.Error())
	case errors.Is(err, application.ErrAccountNotFound):
		return status.Error(codes.NotFound, "account not found")
	case errors.Is(err, application.ErrStorageUnavailable):
		return status.Error(codes.Unavailable, "service unavailable")
	default:
		server.logger.ErrorContext(
			ctx,
			"account request failed",
			slog.String("rpc", method),
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
	if operation, observed := rpcOperation(
		info.FullMethodName,
	); handler.observer != nil &&
		observed {
		return context.WithValue(
			ctx,
			requestStateKey{},
			handler.observer.StartGRPCOperation(ctx, operation),
		)
	}
	return ctx
}

func rpcOperation(method string) (string, bool) {
	switch method {
	case paymentv1.PaymentService_ListAccounts_FullMethodName:
		return "list_accounts", true
	case paymentv1.PaymentService_GetAccountBalance_FullMethodName:
		return "get_account_balance", true
	default:
		return "", false
	}
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
