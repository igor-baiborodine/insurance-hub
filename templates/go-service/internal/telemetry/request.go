package telemetry

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"

	servicelogger "github.com/igor-baiborodine/insurance-hub/templates/go-service/internal/logger"
)

// RequestObserver records only the fixed Echo operation and bounded transport outcomes.
type RequestObserver struct {
	logger   *slog.Logger
	requests metric.Int64Counter
	duration metric.Float64Histogram
}

func NewRequestObserver(
	logger *slog.Logger,
	provider metric.MeterProvider,
) (*RequestObserver, error) {
	if logger == nil || provider == nil {
		return nil, errors.New(
			"create request observer: logger and meter provider are required",
		)
	}
	meter := provider.Meter("insurance-hub/service")
	requests, err := meter.Int64Counter(
		"insurance_hub.service.requests",
		metric.WithUnit("{request}"),
	)
	if err != nil {
		return nil, errors.New("create request counter")
	}
	duration, err := meter.Float64Histogram(
		"insurance_hub.service.request.duration",
		metric.WithUnit("s"),
	)
	if err != nil {
		return nil, errors.New("create request duration")
	}
	return &RequestObserver{logger: logger, requests: requests, duration: duration}, nil
}

type Request struct {
	observer *RequestObserver
	started  time.Time
	span     trace.Span
}

func (observer *RequestObserver) StartGRPC(ctx context.Context) *Request {
	return &Request{observer: observer, started: time.Now(), span: trace.SpanFromContext(ctx)}
}

// Finish runs at the transport End event, including decoding and response-size failures.
func (request *Request) Finish(ctx context.Context, status string) {
	outcome := "internal_error"
	level := slog.LevelError
	switch status {
	case "OK":
		outcome, level = "success", slog.LevelInfo
	case "INVALID_ARGUMENT", "RESOURCE_EXHAUSTED":
		outcome, level = "invalid_request", slog.LevelInfo
	case "CANCELLED":
		outcome, level = "cancelled", slog.LevelWarn
	case "DEADLINE_EXCEEDED":
		outcome, level = "deadline_exceeded", slog.LevelWarn
	default:
		status = "INTERNAL"
	}
	elapsed := time.Since(request.started)
	attrs := []attribute.KeyValue{
		attribute.String("insurance_hub.service.operation", "echo"),
		attribute.String("insurance_hub.service.transport", "grpc"),
		attribute.String("insurance_hub.service.outcome", outcome),
		attribute.String("rpc.response.status_code", status),
	}
	request.span.SetAttributes(attrs...)
	if outcome != "success" {
		request.span.SetAttributes(attribute.String("error.type", outcome))
		request.span.SetStatus(codes.Error, "")
	}
	request.observer.requests.Add(ctx, 1, metric.WithAttributes(attrs...))
	request.observer.duration.Record(ctx, elapsed.Seconds(), metric.WithAttributes(attrs...))
	servicelogger.LogCompletion(ctx, request.observer.logger, level,
		slog.String("operation", "echo"), slog.String("transport", "grpc"),
		slog.String("outcome", outcome), slog.String("rpc_status_code", status),
		slog.Float64("duration_ms", float64(elapsed)/float64(time.Millisecond)),
	)
}
