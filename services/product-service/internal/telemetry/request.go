package telemetry

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"

	"github.com/igor-baiborodine/insurance-hub/services/product-service/internal/application"
	productlogger "github.com/igor-baiborodine/insurance-hub/services/product-service/internal/logger"
)

const (
	OperationList = "list"
	OperationGet  = "get"
	TransportHTTP = "http"
	TransportGRPC = "grpc"
)

type RequestObserver struct {
	logger     *slog.Logger
	tracer     trace.Tracer
	requests   metric.Int64Counter
	duration   metric.Float64Histogram
	propagator propagation.TextMapPropagator
}

func NewRequestObserver(
	logger *slog.Logger,
	tracerProvider trace.TracerProvider,
	meterProvider metric.MeterProvider,
	propagator propagation.TextMapPropagator,
) (*RequestObserver, error) {
	if logger == nil || tracerProvider == nil || meterProvider == nil || propagator == nil {
		return nil, errors.New("create Product request observer: dependencies are required")
	}
	meter := meterProvider.Meter("insurance-hub/product")
	requests, err := meter.Int64Counter(
		"insurance_hub.product.requests",
		metric.WithUnit("{request}"),
	)
	if err != nil {
		return nil, errors.New("create Product request counter")
	}
	duration, err := meter.Float64Histogram(
		"insurance_hub.product.request.duration", metric.WithUnit("s"),
	)
	if err != nil {
		return nil, errors.New("create Product request duration")
	}
	return &RequestObserver{
		logger: logger, tracer: tracerProvider.Tracer("insurance-hub/product"),
		requests: requests, duration: duration, propagator: propagator,
	}, nil
}

type requestKey struct{}

type Request struct {
	observer  *RequestObserver
	operation string
	transport string
	started   time.Time
	outcome   string
	span      trace.Span
	ownedSpan bool
}

// StartHTTP extracts the caller context only after a Product route is selected.
func (observer *RequestObserver) StartHTTP(
	request *http.Request,
	operation, route string,
) (*http.Request, *Request) {
	ctx := observer.propagator.Extract(
		request.Context(),
		propagation.HeaderCarrier(request.Header),
	)
	method := request.Method
	if method != http.MethodGet {
		method = "_OTHER"
	}
	scheme := "http"
	if request.TLS != nil {
		scheme = "https"
	}
	protocol := "1.1"
	if request.ProtoMajor == 2 {
		protocol = "2"
	}
	ctx, span := observer.tracer.Start(
		ctx,
		"GET "+route,
		trace.WithSpanKind(trace.SpanKindServer),
		trace.WithAttributes(
			attribute.String("http.request.method", method),
			attribute.String("http.route", route),
			attribute.String("url.scheme", scheme),
			attribute.String("network.protocol.version", protocol),
		),
	)
	state := &Request{
		observer: observer, operation: operation, transport: TransportHTTP,
		started: time.Now(), span: span, ownedSpan: true,
	}
	return request.WithContext(context.WithValue(ctx, requestKey{}, state)), state
}

// StartGRPC uses the server span created by the pinned gRPC stats handler.
func (observer *RequestObserver) StartGRPC(
	ctx context.Context,
	operation string,
) (context.Context, *Request) {
	state := &Request{
		observer: observer, operation: operation, transport: TransportGRPC,
		started: time.Now(), span: trace.SpanFromContext(ctx),
	}
	return context.WithValue(ctx, requestKey{}, state), state
}

func (state *Request) SetError(err error) {
	state.outcome = Classify(err)
}

func MarkError(ctx context.Context, err error) {
	if state, ok := ctx.Value(requestKey{}).(*Request); ok {
		state.SetError(err)
	}
}

// Application creates a child span at the use-case boundary. The caller owns the business function.
func (observer *RequestObserver) Application(
	ctx context.Context, operation string, execute func(context.Context) error,
) error {
	transport := TransportHTTP
	if state, ok := ctx.Value(requestKey{}).(*Request); ok {
		transport = state.transport
	}
	ctx, span := observer.tracer.Start(ctx, "product."+operation, trace.WithAttributes(
		attribute.String("insurance_hub.product.operation", operation),
		attribute.String("insurance_hub.product.transport", transport),
	))
	defer span.End()
	err := execute(ctx)
	outcome := Classify(err)
	span.SetAttributes(attribute.String("insurance_hub.product.outcome", outcome))
	if errorType := ErrorType(outcome); errorType != "" {
		span.SetAttributes(attribute.String("error.type", errorType))
		span.SetStatus(codes.Error, "")
	}
	return err
}

func (state *Request) Finish(ctx context.Context, statusKey, statusValue string) {
	outcome := state.outcome
	if outcome == "" {
		outcome = outcomeFromStatus(statusValue)
	}
	metricAttrs := []attribute.KeyValue{
		attribute.String("insurance_hub.product.operation", state.operation),
		attribute.String("insurance_hub.product.transport", state.transport),
		attribute.String("insurance_hub.product.outcome", outcome),
	}
	logAttrs := []any{
		slog.String("operation", state.operation),
		slog.String("transport", state.transport),
		slog.String("outcome", outcome),
		slog.Float64(
			"duration_ms",
			float64(time.Since(state.started))/float64(time.Millisecond),
		),
	}
	if statusValue != "" {
		if statusKey == "http.response.status_code" {
			code, _ := strconv.Atoi(statusValue)
			metricAttrs = append(metricAttrs, attribute.Int(statusKey, code))
			logAttrs = append(logAttrs, slog.Int(logStatusKey(statusKey), code))
		} else {
			metricAttrs = append(metricAttrs, attribute.String(statusKey, statusValue))
			logAttrs = append(
				logAttrs,
				slog.String(logStatusKey(statusKey), statusValue),
			)
		}
	}
	spanAttrs := append([]attribute.KeyValue(nil), metricAttrs...)
	if errorType := ErrorType(outcome); errorType != "" {
		spanAttrs = append(spanAttrs, attribute.String("error.type", errorType))
		logAttrs = append(logAttrs, slog.String("error_type", errorType))
		state.span.SetStatus(codes.Error, "")
	}
	state.span.SetAttributes(spanAttrs...)
	measurementOptions := metric.WithAttributes(metricAttrs...)
	state.observer.requests.Add(ctx, 1, measurementOptions)
	state.observer.duration.Record(ctx, time.Since(state.started).Seconds(), measurementOptions)
	level := slog.LevelError
	switch outcome {
	case "success", "not_found", "invalid_request":
		level = slog.LevelInfo
	case "cancelled", "deadline_exceeded":
		level = slog.LevelWarn
	}
	productlogger.LogProductCompletion(ctx, state.observer.logger, level, logAttrs...)
	if state.ownedSpan {
		state.span.End()
	}
}

func logStatusKey(key string) string {
	if key == "http.response.status_code" {
		return "http_status_code"
	}
	return "rpc_status_code"
}

func outcomeFromStatus(status string) string {
	switch status {
	case "200", "OK":
		return "success"
	case "404", "NOT_FOUND":
		return "not_found"
	case "400", "405", "INVALID_ARGUMENT", "RESOURCE_EXHAUSTED":
		return "invalid_request"
	case "CANCELLED":
		return "cancelled"
	case "DEADLINE_EXCEEDED":
		return "deadline_exceeded"
	case "UNAVAILABLE":
		return "storage_unavailable"
	default:
		return "internal_error"
	}
}

func Classify(err error) string {
	switch {
	case err == nil:
		return "success"
	case errors.Is(err, context.Canceled):
		return "cancelled"
	case errors.Is(err, context.DeadlineExceeded):
		return "deadline_exceeded"
	case errors.Is(err, application.ErrCodeRequired):
		return "invalid_request"
	case errors.Is(err, application.ErrProductNotFound):
		return "not_found"
	case errors.Is(err, application.ErrUnavailable):
		return "storage_unavailable"
	case errors.Is(err, application.ErrInvalidDefinition):
		return "invalid_data"
	default:
		return "internal_error"
	}
}

func ErrorType(outcome string) string {
	switch outcome {
	case "invalid_request",
		"cancelled",
		"deadline_exceeded",
		"storage_unavailable",
		"invalid_data",
		"internal_error":
		return outcome
	default:
		return ""
	}
}
