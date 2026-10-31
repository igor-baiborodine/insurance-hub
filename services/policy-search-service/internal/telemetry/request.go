package telemetry

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	kitlogger "github.com/igor-baiborodine/insurance-hub/libs/servicekit/logger"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

const (
	OperationSearch = "search"
	TransportHTTP   = "http"
)

// RequestObserver records bounded policy-search request signals without query or policy values.
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
		return nil, errors.New(
			"create policy search request observer: dependencies are required",
		)
	}
	meter := meterProvider.Meter("insurance-hub/policy-search")
	requests, err := meter.Int64Counter(
		"insurance_hub.policy_search.requests",
		metric.WithUnit("{request}"),
	)
	if err != nil {
		return nil, errors.New("create policy search request counter")
	}
	duration, err := meter.Float64Histogram(
		"insurance_hub.policy_search.request.duration",
		metric.WithUnit("s"),
	)
	if err != nil {
		return nil, errors.New("create policy search request duration")
	}
	return &RequestObserver{
		logger:     logger,
		tracer:     tracerProvider.Tracer("insurance-hub/policy-search"),
		requests:   requests,
		duration:   duration,
		propagator: propagator,
	}, nil
}

type requestKey struct{}

// Request retains the bounded labels and span for one selected HTTP route.
type Request struct {
	observer *RequestObserver
	started  time.Time
	outcome  string
	span     trace.Span
}

// StartHTTP extracts W3C/B3 context only after the policy route is selected.
func (observer *RequestObserver) StartHTTP(request *http.Request) (*http.Request, *Request) {
	ctx := observer.propagator.Extract(
		request.Context(), propagation.HeaderCarrier(request.Header),
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
		"GET /policies",
		trace.WithSpanKind(trace.SpanKindServer),
		trace.WithAttributes(
			attribute.String("http.request.method", method),
			attribute.String("http.route", "/policies"),
			attribute.String("url.scheme", scheme),
			attribute.String("network.protocol.version", protocol),
		),
	)
	state := &Request{observer: observer, started: time.Now(), span: span}
	ctx = context.WithValue(ctx, requestKey{}, state)
	return request.WithContext(ctx), state
}

// Application creates a child span around the application use case.
func (observer *RequestObserver) Application(
	ctx context.Context,
	execute func(context.Context) error,
) error {
	ctx, span := observer.tracer.Start(ctx, "policy_search.search", trace.WithAttributes(
		attribute.String("insurance_hub.policy_search.operation", OperationSearch),
		attribute.String("insurance_hub.policy_search.transport", TransportHTTP),
	))
	defer span.End()
	err := execute(ctx)
	outcome := Classify(err)
	span.SetAttributes(attribute.String("insurance_hub.policy_search.outcome", outcome))
	if outcome != "success" {
		span.SetAttributes(attribute.String("error.type", outcome))
		span.SetStatus(codes.Error, "")
	}
	return err
}

// MarkError attaches an application or mapping failure to the selected request.
func MarkError(ctx context.Context, err error) {
	if state, ok := ctx.Value(requestKey{}).(*Request); ok {
		state.outcome = Classify(err)
	}
}

// Finish records the request once with bounded labels and no caller-controlled values.
func (state *Request) Finish(ctx context.Context, status int) {
	outcome := state.outcome
	if outcome == "" {
		outcome = outcomeFromStatus(status)
	}
	attrs := []attribute.KeyValue{
		attribute.String("insurance_hub.policy_search.operation", OperationSearch),
		attribute.String("insurance_hub.policy_search.transport", TransportHTTP),
		attribute.String("insurance_hub.policy_search.outcome", outcome),
	}
	if status != 0 {
		attrs = append(attrs, attribute.Int("http.response.status_code", status))
	}
	state.span.SetAttributes(attrs...)
	logAttrs := []any{
		slog.String("operation", OperationSearch),
		slog.String("transport", TransportHTTP),
		slog.String("outcome", outcome),
		slog.Float64(
			"duration_ms",
			float64(time.Since(state.started))/float64(time.Millisecond),
		),
	}
	if status != 0 {
		logAttrs = append(logAttrs, slog.Int("http_status_code", status))
	}
	level := slog.LevelError
	switch outcome {
	case "success", "invalid_request":
		level = slog.LevelInfo
	case "cancelled", "deadline_exceeded":
		level = slog.LevelWarn
	}
	if outcome != "success" && outcome != "invalid_request" {
		state.span.SetAttributes(attribute.String("error.type", outcome))
		state.span.SetStatus(codes.Error, "")
		logAttrs = append(logAttrs, slog.String("error_type", outcome))
	}
	measurement := metric.WithAttributes(attrs...)
	state.observer.requests.Add(ctx, 1, measurement)
	state.observer.duration.Record(ctx, time.Since(state.started).Seconds(), measurement)
	kitlogger.LogRequired(
		ctx,
		state.observer.logger,
		level,
		"Policy search request completed",
		logAttrs...)
	state.span.End()
}

func outcomeFromStatus(status int) string {
	switch status {
	case http.StatusOK:
		return "success"
	case http.StatusBadRequest, http.StatusMethodNotAllowed:
		return "invalid_request"
	default:
		return "internal_error"
	}
}

// Classify maps only stable error categories to telemetry labels.
func Classify(err error) string {
	switch {
	case err == nil:
		return "success"
	case errors.Is(err, context.Canceled):
		return "cancelled"
	case errors.Is(err, context.DeadlineExceeded):
		return "deadline_exceeded"
	default:
		return "internal_error"
	}
}
