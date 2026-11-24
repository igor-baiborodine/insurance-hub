package grpc

import (
	"context"
	"errors"
	"log/slog"
	"slices"
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

	policyv1 "github.com/igor-baiborodine/insurance-hub/services/policy-service/gen/policy/v1"
	"github.com/igor-baiborodine/insurance-hub/services/policy-service/internal/application"
	"github.com/igor-baiborodine/insurance-hub/services/policy-service/internal/domain"
	"github.com/igor-baiborodine/insurance-hub/services/policy-service/internal/telemetry"
)

const (
	internalMessage    = "internal error"
	invalidMessage     = "invalid request"
	notFoundMessage    = "not found"
	conflictMessage    = "conflict"
	unavailableMessage = "service unavailable"
)

// operations names the observed RPCs; other methods are not instrumented.
var operations = map[string]string{
	policyv1.PolicyService_CreateOffer_FullMethodName: "create_offer",
	policyv1.PolicyService_IssuePolicy_FullMethodName: "issue_policy",
	policyv1.PolicyService_GetPolicy_FullMethodName:   "get_policy",
}

// CreateOffer is the offer use case invoked by the PolicyService transport.
type CreateOffer func(context.Context, domain.OfferRequest) (domain.Offer, error)

// IssuePolicy is the issuance use case; it returns the new policy number.
type IssuePolicy func(context.Context, application.IssuePolicyCommand) (string, error)

// GetPolicy is the lookup use case for a policy's latest version.
type GetPolicy func(context.Context, string) (application.PolicyDetails, error)

// Handlers are the use cases served by PolicyService. CreateOffer is required; a missing
// lifecycle use case reports that policy storage is not configured.
type Handlers struct {
	CreateOffer CreateOffer
	IssuePolicy IssuePolicy
	GetPolicy   GetPolicy
}

type Settings struct {
	RequestTimeout  time.Duration
	MaxReceiveBytes int
	MaxSendBytes    int
	Observer        *telemetry.RequestObserver
	MeterProvider   metric.MeterProvider
}

// NewServer constructs the instrumented server and registers the generated PolicyService.
func NewServer(
	logger *slog.Logger,
	tracerProvider trace.TracerProvider,
	propagator propagation.TextMapPropagator,
	settings Settings,
	handlers Handlers,
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
	if handlers.CreateOffer == nil {
		return nil, errors.New("create gRPC server: offer handler is required")
	}

	if settings.RequestTimeout <= 0 || settings.MaxReceiveBytes <= 0 ||
		settings.MaxSendBytes <= 0 {
		return nil, errors.New(
			"create gRPC server: request timeout and message limits must be positive",
		)
	}

	validator, err := protovalidate.New(
		protovalidate.WithMessages(
			&policyv1.CreateOfferRequest{},
			&policyv1.IssuePolicyRequest{},
			&policyv1.GetPolicyRequest{},
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
						_, observed := operations[info.FullMethodName]
						return observed
					}),
				),
			},
		),
		grpcgo.UnaryInterceptor(validationInterceptor(logger, validator)),
	)
	if handlers.IssuePolicy == nil {
		handlers.IssuePolicy = application.UnavailableIssuance
	}
	if handlers.GetPolicy == nil {
		handlers.GetPolicy = application.UnavailablePolicyDetails
	}
	policyv1.RegisterPolicyServiceServer(server, &policyServer{
		logger:         logger,
		requestTimeout: settings.RequestTimeout,
		handlers:       handlers,
	})
	return server, nil
}

type policyServer struct {
	policyv1.UnimplementedPolicyServiceServer
	logger         *slog.Logger
	requestTimeout time.Duration
	handlers       Handlers
}

func (server *policyServer) CreateOffer(
	ctx context.Context,
	request *policyv1.CreateOfferRequest,
) (*policyv1.CreateOfferResponse, error) {
	requestCtx, cancel := context.WithTimeout(ctx, server.requestTimeout)
	defer cancel()
	mapped, err := offerRequest(request)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, invalidMessage)
	}
	offer, err := server.handlers.CreateOffer(requestCtx, mapped)
	if err != nil {
		return nil, server.offerError(ctx, err)
	}
	return &policyv1.CreateOfferResponse{
		OfferNumber:  offer.Number,
		TotalPrice:   offer.TotalPrice,
		CoversPrices: offer.CoverPrices,
	}, nil
}

func (server *policyServer) IssuePolicy(
	ctx context.Context,
	request *policyv1.IssuePolicyRequest,
) (*policyv1.IssuePolicyResponse, error) {
	requestCtx, cancel := context.WithTimeout(ctx, server.requestTimeout)
	defer cancel()
	holder := request.GetPolicyHolder()
	number, err := server.handlers.IssuePolicy(requestCtx, application.IssuePolicyCommand{
		OfferNumber: request.GetOfferNumber(),
		Holder: domain.PolicyHolder{
			FirstName: holder.GetFirstName(),
			LastName:  holder.GetLastName(),
			TaxID:     holder.GetTaxId(),
		},
		AgentLogin: request.GetAgentLogin(),
	})
	if err != nil {
		return nil, server.lifecycleError(
			ctx,
			policyv1.PolicyService_IssuePolicy_FullMethodName,
			err,
		)
	}
	return &policyv1.IssuePolicyResponse{PolicyNumber: number}, nil
}

func (server *policyServer) GetPolicy(
	ctx context.Context,
	request *policyv1.GetPolicyRequest,
) (*policyv1.GetPolicyResponse, error) {
	requestCtx, cancel := context.WithTimeout(ctx, server.requestTimeout)
	defer cancel()
	details, err := server.handlers.GetPolicy(requestCtx, request.GetPolicyNumber())
	if err != nil {
		return nil, server.lifecycleError(
			ctx,
			policyv1.PolicyService_GetPolicy_FullMethodName,
			err,
		)
	}
	return &policyv1.GetPolicyResponse{Policy: policyDetails(details)}, nil
}

// policyDetails maps the latest version; covers are "code - price" texts in sorted order.
func policyDetails(details application.PolicyDetails) *policyv1.PolicyDetails {
	version := details.Latest
	covers := make([]string, 0, len(version.Covers))
	for _, cover := range version.Covers {
		covers = append(covers, cover.Code+" - "+cover.Price)
	}
	slices.Sort(covers)
	return &policyv1.PolicyDetails{
		Number:        details.Number,
		DateFrom:      version.VersionFrom.Format(time.DateOnly),
		DateTo:        version.VersionTo.Format(time.DateOnly),
		PolicyHolder:  version.Holder.FullName(),
		TotalPremium:  version.TotalPremium,
		ProductCode:   version.ProductCode,
		AccountNumber: version.AccountNumber,
		Covers:        covers,
	}
}

// offerRequest maps the validated wire request; dates must be real calendar dates.
func offerRequest(request *policyv1.CreateOfferRequest) (domain.OfferRequest, error) {
	policyFrom, err := calendarDate(request.GetPolicyFrom())
	if err != nil {
		return domain.OfferRequest{}, err
	}
	policyTo, err := calendarDate(request.GetPolicyTo())
	if err != nil {
		return domain.OfferRequest{}, err
	}
	answers := make([]domain.Answer, 0, len(request.GetAnswers()))
	for _, answer := range request.GetAnswers() {
		mapped := domain.Answer{QuestionCode: answer.GetQuestionCode()}
		switch value := answer.GetValue().(type) {
		case *policyv1.OfferAnswer_Choice:
			mapped.Kind, mapped.Value = domain.ChoiceAnswer, value.Choice
		case *policyv1.OfferAnswer_Text:
			mapped.Kind, mapped.Value = domain.TextAnswer, value.Text
		case *policyv1.OfferAnswer_Number:
			mapped.Kind, mapped.Value = domain.NumericAnswer, value.Number
		default:
			return domain.OfferRequest{}, errors.New("answer has no value")
		}
		answers = append(answers, mapped)
	}
	return domain.OfferRequest{
		ProductCode:    request.GetProductCode(),
		PolicyFrom:     policyFrom,
		PolicyTo:       policyTo,
		SelectedCovers: append([]string(nil), request.GetSelectedCovers()...),
		Answers:        answers,
	}, nil
}

func calendarDate(value string) (time.Time, error) {
	date, err := time.Parse(time.DateOnly, value)
	if err != nil || date.Format(time.DateOnly) != value {
		return time.Time{}, errors.New("invalid calendar date")
	}
	return date, nil
}

// lifecycleError maps policy use-case failures to safe statuses; only unexpected defects are
// logged.
func (server *policyServer) lifecycleError(ctx context.Context, rpc string, err error) error {
	switch {
	case errors.Is(err, context.Canceled):
		return status.Error(codes.Canceled, context.Canceled.Error())
	case errors.Is(err, context.DeadlineExceeded):
		return status.Error(codes.DeadlineExceeded, context.DeadlineExceeded.Error())
	case errors.Is(err, application.ErrOfferNotFound),
		errors.Is(err, application.ErrPolicyNotFound):
		return status.Error(codes.NotFound, notFoundMessage)
	case errors.Is(err, domain.ErrOfferExpired):
		return status.Error(codes.FailedPrecondition, conflictMessage)
	case errors.Is(err, application.ErrStorageUnavailable),
		errors.Is(err, application.ErrPoliciesUnavailable):
		return status.Error(codes.Unavailable, unavailableMessage)
	default:
		server.logger.ErrorContext(ctx, "policy request failed", slog.String("rpc", rpc))
		return status.Error(codes.Internal, internalMessage)
	}
}

// offerError maps use-case failures to safe statuses; only unexpected defects are logged.
func (server *policyServer) offerError(ctx context.Context, err error) error {
	switch {
	case errors.Is(err, context.Canceled):
		return status.Error(codes.Canceled, context.Canceled.Error())
	case errors.Is(err, context.DeadlineExceeded):
		return status.Error(codes.DeadlineExceeded, context.DeadlineExceeded.Error())
	case errors.Is(err, domain.ErrInvalidOffer), errors.Is(err, application.ErrQuoteRejected):
		return status.Error(codes.InvalidArgument, invalidMessage)
	case errors.Is(err, application.ErrProductNotFound):
		return status.Error(codes.NotFound, notFoundMessage)
	case errors.Is(err, application.ErrProviderUnavailable),
		errors.Is(err, application.ErrStorageUnavailable),
		errors.Is(err, application.ErrOffersUnavailable):
		return status.Error(codes.Unavailable, unavailableMessage)
	default:
		server.logger.ErrorContext(ctx, "create-offer request failed",
			slog.String("rpc", policyv1.PolicyService_CreateOffer_FullMethodName),
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
				return nil, status.Error(codes.InvalidArgument, invalidMessage)
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
	if operation, observed := operations[info.FullMethodName]; handler.observer != nil &&
		observed {
		return context.WithValue(
			ctx,
			requestStateKey{},
			handler.observer.StartGRPC(ctx, operation),
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
